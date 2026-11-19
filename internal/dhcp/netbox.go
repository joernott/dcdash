// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp

import (
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/netbox"
)

// Write-back of reservations to NetBox (task 12.3.3, docs/adr/0006-netbox.md).

// SetNetBox connects the NetBox module; nil if not configured.
func (s *Service) SetNetBox(nb *netbox.Service) { s.netbox = nb }

// nbReservation returns a reservation as the NetBox module sees it.
func nbReservation(server string, res kea.Reservation) netbox.Reservation {
	out := netbox.Reservation{Server: server, MAC: res.HWAddress}
	if res.IPAddress != "" {
		out.Addresses = append(out.Addresses, res.IPAddress)
	}
	out.Addresses = append(out.Addresses, res.IPAddresses...)
	return out
}

// DeleteResult is the response of DELETE .../reservations/{id} if the change
// was written to NetBox (otherwise the response is empty, 204).
type DeleteResult struct {
	NetBox *netbox.Result `json:"netbox"`
}
