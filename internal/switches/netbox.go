// SPDX-License-Identifier: GPL-3.0-or-later

package switches

import (
	"context"
	"net/http"

	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/switches/aoscx"
)

// NetBox write-back of ports and VLANs (task 13.7). Only switches with
// switches[].netbox_device are written; their ports are the interfaces of
// that device.

// NetBoxResult is the outcome of a write-back.
type NetBoxResult = netbox.Result

// SetNetBox connects the NetBox module; nil if not configured.
func (s *Service) SetNetBox(nb *netbox.Service) { s.nb = nb }

func (s *Service) netboxEnabled(x *sw) bool { return s.nb != nil && x.device != "" }

// netboxPorts returns the NetBox interface and projects of each port.
func (s *Service) netboxPorts(ctx context.Context, x *sw) (map[string]*PortNetBox, error) {
	if !s.netboxEnabled(x) {
		return nil, nil
	}
	ifcs, err := s.nb.DeviceInterfaces(ctx, x.device)
	if err != nil {
		s.log.Warn().Err(err).Str("switch", x.backend.Name()).Str("device", x.device).Msg("NetBox interfaces not available")
		return nil, err
	}
	out := make(map[string]*PortNetBox, len(ifcs))
	for name, i := range ifcs {
		out[name] = &PortNetBox{ID: i.ID, Projects: netbox.SwitchPortProjects(i)}
	}
	return out, nil
}

// netboxPort writes a changed port to NetBox.
func (s *Service) netboxPort(ctx context.Context, r *http.Request, x *sw, p aoscx.Port) *NetBoxResult {
	if !s.netboxEnabled(x) {
		return nil
	}
	return s.nb.SyncSwitchPort(ctx, netbox.Base(r), x.device, netbox.SwitchPort{Name: p.Name, Description: p.Description,
		Enabled: p.Enabled, Mode: p.Mode, VLAN: p.VLAN, TrunkVLANs: p.TrunkVLANs, AllVLANs: p.AllVLANs})
}

// netboxVLAN writes a created, renamed or deleted VLAN to NetBox.
func (s *Service) netboxVLAN(ctx context.Context, r *http.Request, x *sw, id int, name string, deleted bool) *NetBoxResult {
	if !s.netboxEnabled(x) {
		return nil
	}
	return s.nb.SyncSwitchVLAN(ctx, netbox.Base(r), id, name, deleted)
}
