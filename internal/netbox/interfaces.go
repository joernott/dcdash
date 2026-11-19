// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import "strings"

// ifaceMatch maps the network devices of a guest to the existing NetBox interfaces of its
// VM. NetBox data maintained by hand often names interfaces like the guest OS does (ens18,
// eth0, Ethernet), so an interface is found by name first and then by its primary MAC
// address. Interfaces that match no network device are only deleted when dcdash created
// them (they have the dcdash tag); all others are kept with their addresses.
type ifaceMatch struct {
	// byNet is the interface of a network device (by device name), if NetBox has one.
	byNet map[string]Interface
	// stale are unmatched interfaces with the dcdash tag: dcdash deletes them.
	stale []Interface
	// foreign are unmatched interfaces without the tag: dcdash leaves them alone.
	foreign []Interface
}

func matchInterfaces(nets []GuestNetwork, have []Interface, tag string) ifaceMatch {
	m := ifaceMatch{byNet: map[string]Interface{}}
	used := map[int64]bool{}
	for _, n := range nets {
		for _, i := range have {
			if !used[i.ID] && i.Name == n.Name {
				m.byNet[n.Name], used[i.ID] = i, true
				break
			}
		}
	}
	for _, n := range nets {
		if _, ok := m.byNet[n.Name]; ok || n.MAC == "" {
			continue
		}
		for _, i := range have {
			if !used[i.ID] && strings.EqualFold(i.MAC(), n.MAC) {
				m.byNet[n.Name], used[i.ID] = i, true
				break
			}
		}
	}
	for _, i := range have {
		switch {
		case used[i.ID]:
		case i.HasTag(tag):
			m.stale = append(m.stale, i)
		default:
			m.foreign = append(m.foreign, i)
		}
	}
	return m
}

// name returns the NetBox name of a network device's interface: the existing interface's
// name if there is one, otherwise the device name.
func (m ifaceMatch) name(n GuestNetwork) string {
	if i, ok := m.byNet[n.Name]; ok {
		return i.Name
	}
	return n.Name
}
