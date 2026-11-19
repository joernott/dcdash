// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
)

// Write-back of switch ports and VLANs (task 13.7, docs/adr/0008-aruba-cx.md).
// A switch is a device in NetBox; dcdash updates the interfaces NetBox already
// has for its ports and never creates or deletes interfaces.

const (
	pathDevices          = "/dcim/devices/"
	pathDeviceInterfaces = "/dcim/interfaces/"
)

// DeviceInterface is an interface of a device.
type DeviceInterface struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Enabled     bool    `json:"enabled"`
	Mode        *Choice `json:"mode"`
	Tags        []Tag   `json:"tags"`
}

// SwitchPort is the state of a switch port as NetBox stores it.
type SwitchPort struct {
	Name        string
	Description string
	Enabled     bool
	// Mode is "access" or "trunk".
	Mode string
	// VLAN is the access VLAN or the native VLAN of a trunk.
	VLAN int
	// TrunkVLANs are the allowed VLANs of a trunk, AllVLANs if not restricted.
	TrunkVLANs []int
	AllVLANs   bool
}

// DeviceInterfaces returns the interfaces of a device by name. It returns
// ErrNotFound if NetBox has no such device.
func (s *Service) DeviceInterfaces(ctx context.Context, device string) (map[string]DeviceInterface, error) {
	if _, err := s.client.lookupID(ctx, pathDevices, url.Values{"name": {device}}); err != nil {
		return nil, err
	}
	list, err := list[DeviceInterface](ctx, s.client, pathDeviceInterfaces, url.Values{"device": {device}})
	if err != nil {
		return nil, err
	}
	out := make(map[string]DeviceInterface, len(list))
	for _, i := range list {
		out[i.Name] = i
	}
	return out, nil
}

// SyncSwitchPort writes a port to its NetBox interface: description,
// enabled, 802.1Q mode, untagged and tagged VLANs. It returns nil without
// write-back. VLANs NetBox doesn't know are left out and noted.
func (s *Service) SyncSwitchPort(ctx context.Context, base audit.Entry, device string, p SwitchPort) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := func() error {
		ifcs, err := s.DeviceInterfaces(ctx, device)
		if err != nil {
			return fmt.Errorf("device %s: %w", device, err)
		}
		ifc, ok := ifcs[p.Name]
		if !ok {
			return fmt.Errorf("the device %s has no interface %s in NetBox", device, p.Name)
		}
		body := map[string]any{"description": p.Description, "enabled": p.Enabled, "tagged_vlans": []any{},
			"changelog_message": message(base, "switch port "+device+" "+p.Name)}
		untagged, err := s.switchVLAN(ctx, p.VLAN, &changes)
		if err != nil {
			return err
		}
		body["untagged_vlan"] = nil
		if untagged != 0 {
			body["untagged_vlan"] = untagged
		}
		switch {
		case p.Mode != "trunk":
			body["mode"] = "access"
		case p.AllVLANs:
			body["mode"] = "tagged-all"
		default:
			body["mode"] = "tagged"
			tagged := []any{}
			for _, vid := range p.TrunkVLANs {
				if vid == p.VLAN {
					continue
				}
				id, err := s.switchVLAN(ctx, vid, &changes)
				if err != nil {
					return err
				}
				if id != 0 {
					tagged = append(tagged, id)
				}
			}
			body["tagged_vlans"] = tagged
		}
		if err := s.client.Update(ctx, pathDeviceInterfaces, ifc.ID, body, nil); err != nil {
			return fmt.Errorf("update the interface %s: %w", p.Name, err)
		}
		changes = append(changes, "interface "+p.Name+" updated")
		return nil
	}()
	return s.finish(ctx, base, "device/"+device+"/"+p.Name, map[string]any{"changes": changes}, err)
}

// switchVLAN returns the NetBox ID of a VLAN; a VLAN NetBox doesn't know is
// noted and returns 0.
func (s *Service) switchVLAN(ctx context.Context, vid int, changes *[]string) (int64, error) {
	if vid == 0 {
		return 0, nil
	}
	id, err := s.vlanID(ctx, vid)
	if err != nil {
		return 0, err
	}
	if id == 0 {
		*changes = append(*changes, "VLAN "+strconv.Itoa(vid)+" is not in NetBox")
	}
	return id, nil
}

// SyncSwitchVLAN writes a VLAN created, renamed or deleted on a switch to
// NetBox, in the configured VLAN group and site. A deleted VLAN is deleted in
// NetBox only if dcdash created it (it has the tag); other VLANs may be used
// elsewhere and are kept. It returns nil without write-back.
func (s *Service) SyncSwitchVLAN(ctx context.Context, base audit.Entry, vid int, name string, deleted bool) *Result {
	if !s.writing() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	var changes []string
	err := func() error {
		msg := message(base, "VLAN "+strconv.Itoa(vid))
		q, err := s.vlanQuery(ctx, vid)
		if err != nil {
			return err
		}
		vlans, err := list[taggedVLAN](ctx, s.client, pathVLANs, q)
		if err != nil {
			return err
		}
		switch {
		case deleted && len(vlans) == 0:
		case deleted && hasTag(vlans[0].Tags, s.cfg.Tag):
			if err := s.client.Delete(ctx, pathVLANs, vlans[0].ID, msg); err != nil {
				return fmt.Errorf("delete the VLAN %d: %w", vid, err)
			}
			changes = append(changes, "VLAN deleted")
		case deleted:
			changes = append(changes, "VLAN kept (not created by dcdash)")
		case len(vlans) == 0:
			tag, err := s.tagID(ctx)
			if err != nil {
				return err
			}
			body := map[string]any{"vid": vid, "name": name, "status": "active", "tags": []any{tag}, "changelog_message": msg}
			if g := q.Get("group_id"); g != "" {
				body["group"], _ = strconv.ParseInt(g, 10, 64)
			}
			if st := q.Get("site_id"); st != "" {
				body["site"], _ = strconv.ParseInt(st, 10, 64)
			}
			if err := s.client.Create(ctx, pathVLANs, body, nil); err != nil {
				return fmt.Errorf("create the VLAN %d: %w", vid, err)
			}
			changes = append(changes, "VLAN created")
		case vlans[0].Name != name:
			if err := s.client.Update(ctx, pathVLANs, vlans[0].ID, map[string]any{"name": name, "changelog_message": msg}, nil); err != nil {
				return fmt.Errorf("rename the VLAN %d: %w", vid, err)
			}
			changes = append(changes, "VLAN renamed")
		}
		return nil
	}()
	return s.finish(ctx, base, "vlan/"+strconv.Itoa(vid), map[string]any{"changes": changes, "name": name}, err)
}

type taggedVLAN struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Tags []Tag  `json:"tags"`
}

// SwitchPortProjects returns the project names of the interface (its tags with the
// project prefix, without it).
func SwitchPortProjects(i DeviceInterface) []string {
	out := []string{}
	for _, t := range ProjectsOf(i.Tags) {
		out = append(out, strings.TrimPrefix(t, ProjectPrefix))
	}
	return out
}
