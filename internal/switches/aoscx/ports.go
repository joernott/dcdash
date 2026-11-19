// SPDX-License-Identifier: GPL-3.0-or-later

package aoscx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Port modes.
const (
	ModeAccess = "access"
	ModeTrunk  = "trunk"
)

// Port is a physical port or a LAG.
type Port struct {
	Name string `json:"name"`
	// Type is "system" (physical port) or "lag".
	Type        string `json:"type"`
	Description string `json:"description"`
	// Enabled is the configured state ("shutdown" is false).
	Enabled bool `json:"enabled"`
	// AdminUp and LinkUp are the operational states. An enabled port without
	// link is not AdminUp on 10.08.
	AdminUp   bool  `json:"adminUp"`
	LinkUp    bool  `json:"linkUp"`
	SpeedMbps int64 `json:"speedMbps"`
	// Connector is "RJ45", "SFP_PLUS", ...; MaxSpeedMbps the fastest speed.
	Connector    string `json:"connector,omitempty"`
	MaxSpeedMbps int64  `json:"maxSpeedMbps,omitempty"`
	// Mode is "access" or "trunk".
	Mode string `json:"mode"`
	// VLAN is the access VLAN, or the native VLAN of a trunk.
	VLAN int `json:"vlan"`
	// NativeTagged: the native VLAN of a trunk is sent tagged.
	NativeTagged bool `json:"nativeTagged,omitempty"`
	// TrunkVLANs are the allowed VLANs of a trunk; AllVLANs if not restricted.
	TrunkVLANs []int `json:"trunkVlans"`
	AllVLANs   bool  `json:"allVlans"`
	// LAG is the LAG the port belongs to; Members the ports of a LAG.
	LAG       string     `json:"lag,omitempty"`
	Members   []string   `json:"members,omitempty"`
	Neighbors []Neighbor `json:"neighbors"`
	PoE       *PoE       `json:"poe,omitempty"`
}

// Uplink reports whether the port leads to other switches: a trunk or a port
// with a bridge as LLDP neighbor.
func (p Port) Uplink() bool {
	if p.Mode == ModeTrunk {
		return true
	}
	return slices.ContainsFunc(p.Neighbors, func(n Neighbor) bool { return n.Bridge })
}

// Neighbor is an LLDP neighbor.
type Neighbor struct {
	ChassisID       string   `json:"chassisId"`
	PortID          string   `json:"portId"`
	SystemName      string   `json:"systemName,omitempty"`
	SystemDesc      string   `json:"systemDescription,omitempty"`
	PortDescription string   `json:"portDescription,omitempty"`
	MgmtAddress     string   `json:"mgmtAddress,omitempty"`
	Capabilities    []string `json:"capabilities"`
	// Bridge: the neighbor has the enabled capability "Bridge" (a switch).
	Bridge bool `json:"bridge"`
}

// PoE is the power over Ethernet state of a port. The attributes are read
// leniently: they were not verified on a PoE switch.
type PoE struct {
	Status      string  `json:"status,omitempty"`
	PowerDrawnW float64 `json:"powerDrawnW,omitempty"`
}

type rawPort struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description *string           `json:"description"`
	AdminState  string            `json:"admin_state"`
	LinkState   string            `json:"link_state"`
	LinkSpeed   int64             `json:"link_speed"`
	VLANMode    *string           `json:"vlan_mode"`
	VLANTag     map[string]string `json:"vlan_tag"`
	VLANTrunks  map[string]string `json:"vlan_trunks"`
	UserConfig  map[string]any    `json:"user_config"`
	HWIntfInfo  struct {
		Connector string `json:"connector"`
		MaxSpeed  string `json:"max_speed"`
	} `json:"hw_intf_info"`
	Interfaces map[string]string `json:"interfaces"`
}

const portAttributes = "name,type,description,admin_state,link_state,link_speed,vlan_mode,vlan_tag,vlan_trunks," +
	"user_config,hw_intf_info,interfaces"

// Ports reads the physical ports and LAGs with their LLDP neighbors and PoE
// state, sorted by name (1/1/2 before 1/1/10).
func (c *Client) Ports(ctx context.Context) ([]Port, error) {
	var raw map[string]rawPort
	if err := c.Get(ctx, "/system/interfaces?depth=2&attributes="+portAttributes, &raw); err != nil {
		return nil, err
	}
	neighbors, err := c.neighbors(ctx)
	if err != nil {
		return nil, err
	}
	poe := c.poe(ctx)
	var out []Port
	lagOf := map[string]string{}
	for _, r := range raw {
		if r.Type != "system" && r.Type != "lag" {
			continue
		}
		p := convertPort(r)
		p.Neighbors = neighbors[p.Name]
		if p.Neighbors == nil {
			p.Neighbors = []Neighbor{}
		}
		p.PoE = poe[p.Name]
		for _, m := range p.Members {
			lagOf[m] = p.Name
		}
		out = append(out, p)
	}
	for i := range out {
		out[i].LAG = lagOf[out[i].Name]
	}
	SortPorts(out)
	return out, nil
}

// Port reads one port.
func (c *Client) Port(ctx context.Context, name string) (Port, error) {
	ports, err := c.Ports(ctx)
	if err != nil {
		return Port{}, err
	}
	for _, p := range ports {
		if p.Name == name {
			return p, nil
		}
	}
	return Port{}, fmt.Errorf("%w: port %s", ErrNotFound, name)
}

func convertPort(r rawPort) Port {
	p := Port{Name: r.Name, Type: r.Type, AdminUp: r.AdminState == "up", LinkUp: r.LinkState == "up",
		SpeedMbps: r.LinkSpeed / 1_000_000, Connector: r.HWIntfInfo.Connector, TrunkVLANs: vlanIDs(r.VLANTrunks)}
	if r.Description != nil {
		p.Description = *r.Description
	}
	// user_config.admin is "down" when shut down and missing or "up" when enabled.
	p.Enabled = r.UserConfig["admin"] != "down"
	p.MaxSpeedMbps, _ = strconv.ParseInt(r.HWIntfInfo.MaxSpeed, 10, 64)
	p.VLAN = 1
	if ids := vlanIDs(r.VLANTag); len(ids) > 0 {
		p.VLAN = ids[0]
	}
	p.Mode = ModeAccess
	if r.VLANMode != nil && strings.HasPrefix(*r.VLANMode, "native-") {
		p.Mode, p.NativeTagged = ModeTrunk, *r.VLANMode == "native-tagged"
		p.AllVLANs = len(p.TrunkVLANs) == 0
	}
	if r.Type == "lag" {
		for m := range r.Interfaces {
			p.Members = append(p.Members, m)
		}
		sort.Slice(p.Members, func(i, j int) bool { return lessPort(p.Members[i], p.Members[j]) })
	}
	return p
}

// vlanIDs returns the sorted VLAN IDs of a reference map {"1404": "/rest/..."}.
func vlanIDs(m map[string]string) []int {
	ids := []int{}
	for k := range m {
		if id, err := strconv.Atoi(k); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// neighbors reads the LLDP neighbors of all ports at once.
func (c *Client) neighbors(ctx context.Context) (map[string][]Neighbor, error) {
	// {"1%2F1%2F1": {"<chassis id>,<port id>": {...}}}
	var raw map[string]map[string]struct {
		ChassisID    string `json:"chassis_id"`
		PortID       string `json:"port_id"`
		NeighborInfo struct {
			ChassisName        string `json:"chassis_name"`
			ChassisDescription string `json:"chassis_description"`
			PortDescription    string `json:"port_description"`
			MgmtIPList         string `json:"mgmt_ip_list"`
			CapabilityEnabled  string `json:"chassis_capability_enabled"`
		} `json:"neighbor_info"`
	}
	err := c.Get(ctx, "/system/interfaces/*/lldp_neighbors?depth=2&attributes=chassis_id,port_id,neighbor_info", &raw)
	if errors.Is(err, ErrNotFound) {
		return map[string][]Neighbor{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]Neighbor{}
	for port, ns := range raw {
		name, err := url.PathUnescape(port)
		if err != nil {
			name = port
		}
		for _, n := range ns {
			info := n.NeighborInfo
			nb := Neighbor{ChassisID: n.ChassisID, PortID: n.PortID, SystemName: info.ChassisName, SystemDesc: info.ChassisDescription,
				PortDescription: info.PortDescription, Capabilities: []string{}}
			if ips := strings.Split(info.MgmtIPList, ","); ips[0] != "" {
				nb.MgmtAddress = strings.TrimSpace(ips[0])
			}
			for _, capability := range strings.Split(info.CapabilityEnabled, ",") {
				if capability = strings.TrimSpace(capability); capability != "" {
					nb.Capabilities = append(nb.Capabilities, capability)
					nb.Bridge = nb.Bridge || strings.EqualFold(capability, "Bridge")
				}
			}
			out[name] = append(out[name], nb)
		}
	}
	for _, ns := range out {
		sort.Slice(ns, func(i, j int) bool { return ns[i].ChassisID+ns[i].PortID < ns[j].ChassisID+ns[j].PortID })
	}
	return out, nil
}

// poe reads the PoE state of all ports; switches without PoE answer 404, and
// errors only mean that no PoE state is shown.
func (c *Client) poe(ctx context.Context) map[string]*PoE {
	var raw map[string]map[string]json.RawMessage
	if err := c.Get(ctx, "/system/interfaces/*/poe_interface?depth=2", &raw); err != nil {
		if !errors.Is(err, ErrNotFound) {
			c.log.Debug().Err(err).Msg("PoE state not available")
		}
		return nil
	}
	out := map[string]*PoE{}
	for port, attrs := range raw {
		name, err := url.PathUnescape(port)
		if err != nil {
			name = port
		}
		p := &PoE{}
		// Look one level deep for the status and the power drawn.
		for _, v := range attrs {
			var m map[string]any
			if json.Unmarshal(v, &m) != nil {
				continue
			}
			if s, ok := m["detection_status"].(string); ok {
				p.Status = s
			}
			if w, ok := m["power_drawn"].(float64); ok {
				p.PowerDrawnW = w
			}
		}
		out[name] = p
	}
	return out
}

// SortPorts sorts ports by their numbers (1/1/2 before 1/1/10), LAGs last.
func SortPorts(ports []Port) {
	sort.SliceStable(ports, func(i, j int) bool { return lessPort(ports[i].Name, ports[j].Name) })
}

// lessPort compares port names number by number ("1/1/2" < "1/1/10" < "lag1").
func lessPort(a, b string) bool {
	pa, pb := portKey(a), portKey(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	if len(pa) != len(pb) {
		return len(pa) < len(pb)
	}
	return a < b
}

// portKey splits a name into numbers; names without numbers sort after all
// numbered ports.
func portKey(name string) []int {
	var out []int
	digits := strings.FieldsFunc(name, func(r rune) bool { return r < '0' || r > '9' })
	if !strings.ContainsAny(name[:1], "0123456789") {
		out = append(out, 1<<30) // lag1, ... after 1/1/x
	}
	for _, d := range digits {
		n, _ := strconv.Atoi(d)
		out = append(out, n)
	}
	return out
}

// PortChange is a change of a port; nil fields stay unchanged.
type PortChange struct {
	Enabled     *bool
	Description *string
	// Mode is "access" or "trunk".
	Mode *string
	// VLAN is the access VLAN or the native VLAN of a trunk.
	VLAN *int
	// TrunkVLANs are the allowed VLANs of a trunk; an empty list allows all.
	TrunkVLANs *[]int
}

// UpdatePort changes a port: it reads the port's writable attributes, changes
// them and writes them back with PUT (AOS-CX 10.08 has no PATCH, and PUT
// replaces all writable attributes).
func (c *Client) UpdatePort(ctx context.Context, name string, ch PortChange) error {
	path := "/system/interfaces/" + EscapePath(name)
	var w map[string]any
	if err := c.Get(ctx, path+"?selector=writable", &w); err != nil {
		return err
	}
	if ch.Description != nil {
		if *ch.Description == "" {
			w["description"] = nil
		} else {
			w["description"] = *ch.Description
		}
	}
	if ch.Enabled != nil {
		uc, _ := w["user_config"].(map[string]any)
		if uc == nil {
			uc = map[string]any{}
		}
		if *ch.Enabled {
			uc["admin"] = "up"
		} else {
			uc["admin"] = "down"
		}
		w["user_config"] = uc
	}
	if ch.Mode != nil {
		switch *ch.Mode {
		case ModeAccess:
			w["vlan_mode"] = nil // access is the default
			w["vlan_trunks"] = []string{}
		case ModeTrunk:
			if m, _ := w["vlan_mode"].(string); !strings.HasPrefix(m, "native-") {
				w["vlan_mode"] = "native-untagged"
			}
		default:
			return fmt.Errorf("aoscx: unknown port mode %q", *ch.Mode)
		}
	}
	if ch.VLAN != nil {
		w["vlan_tag"] = c.vlanURI(*ch.VLAN)
	}
	if ch.TrunkVLANs != nil {
		uris := make([]string, 0, len(*ch.TrunkVLANs))
		for _, id := range *ch.TrunkVLANs {
			uris = append(uris, c.vlanURI(id))
		}
		w["vlan_trunks"] = uris
	}
	return c.Do(ctx, http.MethodPut, path, w, nil)
}

func (c *Client) vlanURI(id int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prefix + "/system/vlans/" + strconv.Itoa(id)
}
