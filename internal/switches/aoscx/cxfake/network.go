// SPDX-License-Identifier: GPL-3.0-or-later

package cxfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Port is a port of the fake. VLAN 0 means VLAN 1 (vlan_tag null).
type Port struct {
	Type        string // "system" or "lag"
	Description string
	// Admin is user_config.admin: "", "up" or "down".
	Admin string
	// Mode is the vlan_mode: "" (access), "native-untagged", "native-tagged".
	Mode   string
	VLAN   int
	Trunks []int
	Link   bool
	// SpeedMbps while the link is up.
	SpeedMbps int64
	Connector string
	Members   []string // of a LAG
	// PoE, if set, is returned as poe_interface (status, power drawn in W).
	PoE *PoE
}

// PoE is the PoE state of a fake port.
type PoE struct {
	Status string
	PowerW float64
}

// VLAN is a VLAN of the fake.
type VLAN struct {
	Name string
	Type string // "default" or "static"
}

// MAC is an entry of the MAC address table.
type MAC struct {
	VLAN int
	MAC  string
	Port string
	From string
}

// Neighbor is an LLDP neighbor.
type Neighbor struct {
	ChassisID, PortID, Name, PortDescription, MgmtIP, Capabilities string
}

// seed creates 8 RJ45 ports and 2 SFP+ ports: 1/1/1 is the uplink (trunk,
// all VLANs, LLDP neighbor core-sw), 1/1/2 a server in VLAN 10, 1/1/3 a
// client in VLAN 20, 1/1/4 is shut down.
func (s *Server) seed() {
	s.VLANs = map[int]*VLAN{1: {Name: "DEFAULT_VLAN_1", Type: "default"}, 10: {Name: "servers", Type: "static"},
		20: {Name: "clients", Type: "static"}}
	s.Ports = map[string]*Port{}
	for i := 1; i <= 10; i++ {
		p := &Port{Type: "system", Connector: "RJ45", VLAN: 10}
		if i > 8 {
			p.Connector = "SFP_PLUS"
		}
		s.Ports["1/1/"+strconv.Itoa(i)] = p
	}
	up := s.Ports["1/1/1"]
	up.Mode, up.VLAN, up.Link, up.SpeedMbps, up.Description = "native-untagged", 0, true, 1000, "uplink core-sw"
	s.Ports["1/1/2"].Link, s.Ports["1/1/2"].SpeedMbps, s.Ports["1/1/2"].Description = true, 1000, "app1"
	s.Ports["1/1/3"].Link, s.Ports["1/1/3"].SpeedMbps, s.Ports["1/1/3"].VLAN = true, 100, 20
	s.Ports["1/1/4"].Admin = "down"
	s.Neighbors = map[string][]Neighbor{"1/1/1": {{ChassisID: "d4:e0:53:00:00:01", PortID: "1/1/49", Name: "core-sw",
		PortDescription: "to sw-test", MgmtIP: "192.0.2.2", Capabilities: "Bridge, Router"}}}
	s.MACs = []MAC{
		{VLAN: 10, MAC: "bc:24:11:00:00:01", Port: "1/1/2", From: "dynamic"},
		{VLAN: 10, MAC: "bc:24:11:00:00:99", Port: "1/1/1", From: "dynamic"},
		{VLAN: 20, MAC: "00:11:22:33:44:55", Port: "1/1/3", From: "dynamic"},
		{VLAN: 1, MAC: "d4:e0:53:00:00:01", Port: "1/1/1", From: "dynamic"},
	}
}

func (s *Server) vlanURI(id int) string { return Prefix + "/system/vlans/" + strconv.Itoa(id) }

func (s *Server) refs(ids ...int) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		out[strconv.Itoa(id)] = s.vlanURI(id)
	}
	return out
}

// writable returns the writable attributes of a port, as the switch does
// (plus one attribute dcdash doesn't know, which must survive a PUT).
func (s *Server) writable(p *Port) map[string]any {
	w := map[string]any{"description": nil, "vlan_mode": nil, "vlan_tag": nil, "vlan_trunks": s.refs(p.Trunks...),
		"user_config": map[string]any{"eee": false}, "other_config": map[string]any{"lldp_trap_enable": true}}
	if p.Description != "" {
		w["description"] = p.Description
	}
	if p.Mode != "" {
		w["vlan_mode"] = p.Mode
	}
	if p.VLAN > 1 {
		w["vlan_tag"] = s.refs(p.VLAN)
	}
	if p.Admin != "" {
		w["user_config"].(map[string]any)["admin"] = p.Admin
	}
	return w
}

func (s *Server) portJSON(name string, p *Port) map[string]any {
	o := s.writable(p)
	o["name"], o["type"] = name, p.Type
	o["admin_state"], o["link_state"], o["link_speed"] = "down", "down", 0
	if p.Admin != "down" && p.Link {
		o["admin_state"], o["link_state"], o["link_speed"] = "up", "up", p.SpeedMbps*1_000_000
	}
	o["hw_intf_info"] = map[string]any{"connector": p.Connector, "max_speed": "1000"}
	ifs := map[string]string{name: Prefix + "/system/interfaces/" + url.PathEscape(name)}
	if p.Type == "lag" {
		ifs = map[string]string{}
		for _, m := range p.Members {
			ifs[m] = Prefix + "/system/interfaces/" + url.PathEscape(m)
		}
	}
	o["interfaces"] = ifs
	return o
}

// network serves interfaces, VLANs, MAC addresses and the configuration.
func (s *Server) network(w http.ResponseWriter, r *http.Request) {
	// Interface names are URL-encoded (1%2F1%2F1): split the escaped path.
	raw := strings.TrimPrefix(r.URL.EscapedPath(), Prefix+"/")
	var seg []string
	for _, p := range strings.Split(raw, "/") {
		u, err := url.PathUnescape(p)
		if err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		seg = append(seg, u)
	}
	key := r.Method + " " + strings.Join(seg, "|")
	switch {
	case key == "GET system|interfaces":
		out := map[string]any{}
		for n, p := range s.Ports {
			out[n] = filter(s.portJSON(n, p), r)
		}
		out["vlan1"] = map[string]any{"name": "vlan1", "type": "vlan"}
		writeJSON(w, out)
	case key == "GET system|interfaces|*|lldp_neighbors":
		out := map[string]any{}
		for port, ns := range s.Neighbors {
			m := map[string]any{}
			for _, n := range ns {
				m[n.ChassisID+","+n.PortID] = map[string]any{"chassis_id": n.ChassisID, "port_id": n.PortID,
					"neighbor_info": map[string]any{"chassis_name": n.Name, "port_description": n.PortDescription,
						"mgmt_ip_list": n.MgmtIP, "chassis_capability_enabled": n.Capabilities}}
			}
			out[url.PathEscape(port)] = m
		}
		writeJSON(w, out)
	case key == "GET system|interfaces|*|poe_interface":
		out := map[string]any{}
		for n, p := range s.Ports {
			if p.PoE != nil {
				out[url.PathEscape(n)] = map[string]any{"status": map[string]any{"detection_status": p.PoE.Status,
					"power_drawn": p.PoE.PowerW}}
			}
		}
		if len(out) == 0 {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, out)
	case len(seg) == 3 && seg[0] == "system" && seg[1] == "interfaces":
		s.port(w, r, seg[2])
	case key == "GET system|vlans":
		out := map[string]any{}
		for id, v := range s.VLANs {
			out[strconv.Itoa(id)] = s.vlanJSON(id, v)
		}
		writeJSON(w, out)
	case key == "POST system|vlans":
		var body struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID < 1 || body.ID > 4094 {
			http.Error(w, "invalid VLAN", http.StatusBadRequest)
			return
		}
		if s.VLANs[body.ID] != nil {
			http.Error(w, "VLAN already exists", http.StatusBadRequest)
			return
		}
		s.VLANs[body.ID] = &VLAN{Name: body.Name, Type: "static"}
		w.WriteHeader(http.StatusCreated)
	case len(seg) == 3 && seg[0] == "system" && seg[1] == "vlans" && seg[2] != "*":
		s.vlan(w, r, seg[2])
	case key == "GET system|vlans|*|macs":
		out := map[string]map[string]any{}
		for _, m := range s.MACs {
			v := strconv.Itoa(m.VLAN)
			if out[v] == nil {
				out[v] = map[string]any{}
			}
			out[v][m.From+","+m.MAC] = map[string]any{"mac_addr": m.MAC, "from": m.From,
				"port": map[string]string{m.Port: Prefix + "/system/interfaces/" + url.PathEscape(m.Port)}}
		}
		writeJSON(w, out)
	case key == "PUT fullconfigs|startup-config" && r.URL.Query().Get("from") == Prefix+"/fullconfigs/running-config":
		s.Saves++
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) vlanJSON(id int, v *VLAN) map[string]any {
	return map[string]any{"id": id, "name": v.Name, "type": v.Type, "admin": "up", "oper_state": "up", "description": nil}
}

func (s *Server) port(w http.ResponseWriter, r *http.Request, name string) {
	p := s.Ports[name]
	if p == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("selector") == "writable" {
			writeJSON(w, s.writable(p))
			return
		}
		writeJSON(w, filter(s.portJSON(name, p), r))
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := s.applyPort(p, body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// applyPort replaces the writable attributes, like a PUT on the switch: a
// missing attribute is reset, an unknown one rejected.
func (s *Server) applyPort(p *Port, body map[string]any) error {
	known := s.writable(p)
	for k := range body {
		if _, ok := known[k]; !ok {
			return fmt.Errorf("attribute %s is not writable", k)
		}
	}
	n := *p
	n.Description, _ = body["description"].(string)
	n.Mode, _ = body["vlan_mode"].(string)
	if n.Mode != "" && n.Mode != "native-untagged" && n.Mode != "native-tagged" && n.Mode != "access" {
		return fmt.Errorf("invalid vlan_mode %s", n.Mode)
	}
	if n.Mode == "access" {
		n.Mode = ""
	}
	n.Admin = ""
	if uc, ok := body["user_config"].(map[string]any); ok {
		n.Admin, _ = uc["admin"].(string)
	}
	var err error
	if n.VLAN, err = s.oneVLAN(body["vlan_tag"]); err != nil {
		return err
	}
	if n.Trunks, err = s.vlanList(body["vlan_trunks"]); err != nil {
		return err
	}
	*p = n
	return nil
}

// oneVLAN reads a vlan_tag: null, a URI or a reference map.
func (s *Server) oneVLAN(v any) (int, error) {
	ids, err := s.vlanList(v)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	return ids[0], nil
}

// vlanList reads VLAN references: null, a URI, a list of URIs or a map.
func (s *Server) vlanList(v any) ([]int, error) {
	var uris []string
	switch x := v.(type) {
	case nil:
	case string:
		uris = []string{x}
	case []any:
		for _, u := range x {
			str, _ := u.(string)
			uris = append(uris, str)
		}
	case map[string]any:
		for _, u := range x {
			str, _ := u.(string)
			uris = append(uris, str)
		}
	default:
		return nil, fmt.Errorf("invalid VLAN reference %v", v)
	}
	var ids []int
	for _, u := range uris {
		id, err := strconv.Atoi(strings.TrimPrefix(u, Prefix+"/system/vlans/"))
		if err != nil || s.VLANs[id] == nil {
			return nil, fmt.Errorf("VLAN %s does not exist", u)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Server) vlan(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := strconv.Atoi(idText)
	v := s.VLANs[id]
	if err != nil || v == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Query().Get("selector") == "writable" {
			writeJSON(w, map[string]any{"name": v.Name, "admin": "up", "description": nil, "type": v.Type})
			return
		}
		writeJSON(w, s.vlanJSON(id, v))
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		name, _ := body["name"].(string)
		v.Name = name
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if v.Type == "default" {
			http.Error(w, "The default VLAN cannot be deleted", http.StatusBadRequest)
			return
		}
		delete(s.VLANs, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
