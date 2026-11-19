// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp

import (
	"context"
	"errors"
	"fmt"

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

// NetBoxReservations implements netbox.ReservationSource: the reservations of
// all servers that offer them. Servers of an HA pair report the same
// reservations.
func (s *Service) NetBoxReservations(ctx context.Context) ([]netbox.Reservation, error) {
	var (
		out  []netbox.Reservation
		errs []error
	)
	for _, name := range s.order {
		srv := s.servers[name]
		info, _, err := s.state(ctx, srv, false)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if !info.Capabilities.Reservations {
			continue
		}
		list, err := srv.backend.Reservations(ctx, -1)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		for _, r := range list {
			out = append(out, nbReservation(name, r))
		}
	}
	return out, errors.Join(errs...)
}
