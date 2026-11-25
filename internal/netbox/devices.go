// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
)

// The comparison of physical machines (Proxmox nodes) and switches with their NetBox devices
// (task 18.5). dcdash compares a few fields and updates those it can set safely: the cluster
// and primary IP of a node, the serial number of a switch. It doesn't create devices, as it
// doesn't know their type, role and site, and doesn't change the device type.

// Node is a Proxmox node, from the Proxmox module.
type Node struct {
	Name string
	// IP is the address the cluster knows the node by, or "".
	IP string
}

// NodeSource provides the nodes of the Proxmox clusters (the Proxmox module).
type NodeSource interface {
	NetBoxNodes(ctx context.Context, cluster string) ([]Node, error)
}

// Switch is a configured switch, from the switch module.
type Switch struct {
	Name string
	// Device is the name of its NetBox device: netbox_device, or the switch name.
	Device     string
	Model      string
	PartNumber string
	Serial     string
	// Error says why the switch couldn't be read; the fields above it are empty then.
	Error string
}

// SwitchSource provides the switches (the switch module).
type SwitchSource interface {
	NetBoxSwitches(ctx context.Context) []Switch
}

// SetDeviceSources connects the modules whose devices are compared; nil for modules that
// aren't configured.
func (s *Service) SetDeviceSources(n NodeSource, sw SwitchSource) {
	s.nodes, s.switches = n, sw
}

// Device is a NetBox device, with the fields dcdash compares.
type Device struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Serial     string `json:"serial"`
	DeviceType *struct {
		ID         int64  `json:"id"`
		Model      string `json:"model"`
		PartNumber string `json:"part_number"`
	} `json:"device_type"`
	Cluster    *Ref   `json:"cluster"`
	PrimaryIP4 *IPRef `json:"primary_ip4"`
	PrimaryIP6 *IPRef `json:"primary_ip6"`
}

// Fields of a device comparison.
const (
	FieldCluster   = "cluster"
	FieldPrimaryIP = "primaryIp"
	FieldModel     = "model"
	FieldSerial    = "serial"
)

// Kinds of compared devices.
const (
	KindNode   = "node"
	KindSwitch = "switch"
)

// StateUnavailable means that the switch couldn't be read.
const StateUnavailable = "unavailable"

// ErrNoDevice means that NetBox has no device for the node or switch.
var ErrNoDevice = errors.New("NetBox has no device of that name")

// DeviceDiff compares a node or switch with its NetBox device.
type DeviceDiff struct {
	// Kind is node or switch.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Device is the name of the NetBox device.
	Device string `json:"device"`
	// State is ok, missing (no device), differs or unavailable (the switch couldn't be read).
	State       string       `json:"state"`
	Differences []Difference `json:"differences"`
	// NetBoxID is the ID of the NetBox device, 0 if there is none.
	NetBoxID int64 `json:"netboxId,omitempty"`
	// Updatable is true if an update changes at least one of the differences.
	Updatable bool   `json:"updatable"`
	Error     string `json:"error,omitempty"`
}

// DeviceComparison is the response of GET /api/v1/netbox/compare/nodes/{cluster} and
// /compare/switches.
type DeviceComparison struct {
	Devices []DeviceDiff `json:"devices"`
}

func (s *Service) device(ctx context.Context, name string) (Device, error) {
	return get[Device](ctx, s.client, pathDevices, url.Values{"name": {name}})
}

// deviceIP returns the NetBox IP address ip (without prefix length) if it is assigned to an
// interface of the device, or ErrNotFound.
func (s *Service) deviceIP(ctx context.Context, deviceID int64, ip string) (IPAddress, error) {
	return get[IPAddress](ctx, s.client, pathIPAddresses,
		url.Values{"address": {ip}, "device_id": {strconv.FormatInt(deviceID, 10)}})
}

// primaryAddresses returns the primary addresses of a device without prefix length.
func primaryAddresses(d Device) []string {
	var out []string
	for _, ip := range []*IPRef{d.PrimaryIP4, d.PrimaryIP6} {
		if ip != nil {
			out = append(out, strings.SplitN(ip.Address, "/", 2)[0])
		}
	}
	return out
}

// CompareNodes compares the nodes of a Proxmox cluster with their NetBox devices (by name).
// visible filters the nodes the user may see.
func (s *Service) CompareNodes(ctx context.Context, cluster string, visible func(node string) bool) (DeviceComparison, error) {
	out := DeviceComparison{Devices: []DeviceDiff{}}
	if _, ok := s.cfg.Clusters[cluster]; !ok {
		return out, fmt.Errorf("NetBox: %w: the cluster %s isn't mapped", ErrNotFound, cluster)
	}
	if s.nodes == nil {
		return out, errors.New("the Proxmox module isn't configured")
	}
	nodes, err := s.nodes.NetBoxNodes(ctx, cluster)
	if err != nil {
		return out, err
	}
	for _, n := range nodes {
		if !visible(n.Name) {
			continue
		}
		d, _, err := s.compareNode(ctx, cluster, n)
		if err != nil {
			return out, err
		}
		out.Devices = append(out.Devices, d)
	}
	return out, nil
}

func (s *Service) compareNode(ctx context.Context, cluster string, n Node) (DeviceDiff, Device, error) {
	d := DeviceDiff{Kind: KindNode, Name: n.Name, Device: n.Name, Differences: []Difference{}}
	dev, err := s.device(ctx, n.Name)
	if errors.Is(err, ErrNotFound) {
		d.State = StateMissing
		return d, dev, nil
	}
	if err != nil {
		return d, dev, err
	}
	d.NetBoxID = dev.ID
	want, have := s.cfg.Clusters[cluster], ""
	if dev.Cluster != nil {
		have = dev.Cluster.Name
	}
	if have != want {
		d.Differences = append(d.Differences, Difference{Field: FieldCluster, Actual: want, NetBox: have})
		d.Updatable = true
	}
	if ips := primaryAddresses(dev); n.IP != "" && !slices.Contains(ips, n.IP) {
		d.Differences = append(d.Differences, Difference{Field: FieldPrimaryIP, Actual: n.IP, NetBox: strings.Join(ips, ", ")})
		if _, err := s.deviceIP(ctx, dev.ID, n.IP); err == nil {
			d.Updatable = true
		} else if !errors.Is(err, ErrNotFound) {
			return d, dev, err
		}
	}
	d.State = StateOK
	if len(d.Differences) > 0 {
		d.State = StateDiffers
	}
	return d, dev, nil
}

// ApplyNode sets the cluster of the node's NetBox device and, if its address is assigned to
// an interface of the device, the primary IP.
func (s *Service) ApplyNode(ctx context.Context, base audit.Entry, cluster, node string) (*Result, error) {
	if s.nodes == nil {
		return nil, errors.New("the Proxmox module isn't configured")
	}
	if _, ok := s.cfg.Clusters[cluster]; !ok {
		return nil, fmt.Errorf("NetBox: %w: the cluster %s isn't mapped", ErrNotFound, cluster)
	}
	nodes, err := s.nodes.NetBoxNodes(ctx, cluster)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(nodes, func(n Node) bool { return n.Name == node })
	if i < 0 {
		return nil, fmt.Errorf("NetBox: %w: unknown node %s", ErrNotFound, node)
	}
	d, dev, err := s.compareNode(ctx, cluster, nodes[i])
	if err != nil {
		return nil, err
	}
	if d.State == StateMissing {
		return nil, ErrNoDevice
	}
	body := map[string]any{}
	var changes []string
	for _, diff := range d.Differences {
		switch diff.Field {
		case FieldCluster:
			id, _, err := s.clusterID(ctx, cluster)
			if err != nil {
				return nil, err
			}
			body["cluster"] = id
			changes = append(changes, "cluster "+diff.Actual)
		case FieldPrimaryIP:
			ip, err := s.deviceIP(ctx, dev.ID, diff.Actual)
			if errors.Is(err, ErrNotFound) {
				changes = append(changes, "primary IP "+diff.Actual+" not set: not assigned to the device")
				continue
			}
			if err != nil {
				return nil, err
			}
			field := "primary_ip4"
			if a, err := netip.ParseAddr(diff.Actual); err == nil && a.Is6() {
				field = "primary_ip6"
			}
			body[field] = ip.ID
			changes = append(changes, "primary IP "+diff.Actual)
		}
	}
	err = s.updateDevice(ctx, base, dev, body)
	base.Action = AuditApply
	return s.finish(ctx, base, "node:"+cluster+"/"+node, map[string]any{"name": node, "changes": nonNil(changes)}, err), nil
}

// CompareSwitches compares the switches with their NetBox devices. visible filters the
// switches the user may see.
func (s *Service) CompareSwitches(ctx context.Context, visible func(name string) bool) (DeviceComparison, error) {
	out := DeviceComparison{Devices: []DeviceDiff{}}
	if s.switches == nil {
		return out, nil
	}
	for _, sw := range s.switches.NetBoxSwitches(ctx) {
		if !visible(sw.Name) {
			continue
		}
		d, _, err := s.compareSwitch(ctx, sw)
		if err != nil {
			return out, err
		}
		out.Devices = append(out.Devices, d)
	}
	return out, nil
}

func (s *Service) compareSwitch(ctx context.Context, sw Switch) (DeviceDiff, Device, error) {
	d := DeviceDiff{Kind: KindSwitch, Name: sw.Name, Device: sw.Device, Differences: []Difference{}}
	dev, err := s.device(ctx, sw.Device)
	if errors.Is(err, ErrNotFound) {
		d.State = StateMissing
		return d, dev, nil
	}
	if err != nil {
		return d, dev, err
	}
	d.NetBoxID = dev.ID
	if sw.Error != "" {
		d.State, d.Error = StateUnavailable, sw.Error
		return d, dev, nil
	}
	if !sameModel(sw, dev) {
		nb := ""
		if dev.DeviceType != nil {
			nb = joinModel(dev.DeviceType.Model, dev.DeviceType.PartNumber)
		}
		d.Differences = append(d.Differences, Difference{Field: FieldModel, Actual: joinModel(sw.Model, sw.PartNumber), NetBox: nb})
	}
	if sw.Serial != "" && !strings.EqualFold(sw.Serial, dev.Serial) {
		d.Differences = append(d.Differences, Difference{Field: FieldSerial, Actual: sw.Serial, NetBox: dev.Serial})
		d.Updatable = true
	}
	d.State = StateOK
	if len(d.Differences) > 0 {
		d.State = StateDiffers
	}
	return d, dev, nil
}

// sameModel compares the part numbers if both are known, otherwise the model names: the
// switch reports its product name, which NetBox device types often spell differently.
func sameModel(sw Switch, dev Device) bool {
	if dev.DeviceType == nil {
		return sw.Model == "" && sw.PartNumber == ""
	}
	if sw.PartNumber != "" && dev.DeviceType.PartNumber != "" {
		return strings.EqualFold(sw.PartNumber, dev.DeviceType.PartNumber)
	}
	return strings.EqualFold(sw.Model, dev.DeviceType.Model)
}

func joinModel(model, part string) string {
	switch {
	case part == "":
		return model
	case model == "":
		return part
	}
	return model + " (" + part + ")"
}

// ApplySwitch sets the serial number of the switch's NetBox device.
func (s *Service) ApplySwitch(ctx context.Context, base audit.Entry, name string) (*Result, error) {
	if s.switches == nil {
		return nil, fmt.Errorf("NetBox: %w: unknown switch %s", ErrNotFound, name)
	}
	all := s.switches.NetBoxSwitches(ctx)
	i := slices.IndexFunc(all, func(sw Switch) bool { return sw.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("NetBox: %w: unknown switch %s", ErrNotFound, name)
	}
	d, dev, err := s.compareSwitch(ctx, all[i])
	if err != nil {
		return nil, err
	}
	if d.State == StateMissing {
		return nil, ErrNoDevice
	}
	body := map[string]any{}
	var changes []string
	for _, diff := range d.Differences {
		if diff.Field == FieldSerial {
			body["serial"] = diff.Actual
			changes = append(changes, "serial "+diff.Actual)
		}
	}
	err = s.updateDevice(ctx, base, dev, body)
	base.Action = AuditApply
	return s.finish(ctx, base, "switch:"+name, map[string]any{"name": name, "device": d.Device, "changes": nonNil(changes)}, err), nil
}

// updateDevice writes the fields of body to the device, with a change log message.
func (s *Service) updateDevice(ctx context.Context, base audit.Entry, dev Device, body map[string]any) error {
	if len(body) == 0 {
		return nil
	}
	body["changelog_message"] = message(base, "update "+dev.Name)
	if err := s.client.Update(ctx, pathDevices, dev.ID, body, nil); err != nil {
		return fmt.Errorf("update device %s: %w", dev.Name, err)
	}
	return nil
}
