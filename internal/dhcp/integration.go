// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/rbac"
)

// Reservations requested by other modules, e.g. by the creation of a guest in
// the Proxmox module (task 11.14). PlanReservation checks the permission and
// the request when the user submits it; Create adds the reservation later.

// PlannedReservation is a checked reservation that is not created yet.
type PlannedReservation struct {
	s   *Service
	q   request
	res kea.Reservation
	// Target is the audit target, e.g. "kea4/192.0.2.0/24".
	Target string
}

// PlanReservation checks that subj may add the reservation to the server's
// subnet and that it is valid. A missing permission is an *rbac.DeniedError.
func (s *Service) PlanReservation(ctx context.Context, subj *rbac.Subject, server string, req ReservationRequest) (*PlannedReservation, error) {
	srv, ok := s.servers[server]
	if !ok {
		return nil, invalid("unknown DHCP server %q", server)
	}
	info, subnets, err := s.state(ctx, srv, false)
	if err != nil {
		return nil, fmt.Errorf("DHCP server %s: %w", server, err)
	}
	q := request{srv: srv, name: server, subj: subj, info: info, subnets: subnets}
	res, known := resource(subnets, req.SubnetID)
	if !known {
		return nil, invalid("unknown subnet %d", req.SubnetID)
	}
	if perm := rbac.P(Module, server, res, rbac.ActionWrite); !subj.Can(perm) {
		return nil, &rbac.DeniedError{Permission: perm}
	}
	if !info.Capabilities.ReservationWrite && len(s.partners(server)) == 0 {
		return nil, errNoHostDatabase
	}
	r, err := q.reservation(req)
	if err != nil {
		return nil, err
	}
	return &PlannedReservation{s: s, q: q, res: r, Target: target(server, subnetName(subnets, r.SubnetID))}, nil
}

// Family returns "v4" or "v6" for a known server, "" otherwise.
func (s *Service) Family(server string) string {
	if srv, ok := s.servers[server]; ok {
		return srv.backend.Family()
	}
	return ""
}

// Describe names the reservation for the progress of a job.
func (p *PlannedReservation) Describe() string {
	addr := p.res.IPAddress
	if addr == "" && len(p.res.IPAddresses) > 0 {
		addr = p.res.IPAddresses[0]
	}
	if addr == "" {
		addr = p.res.HWAddress
	}
	return addr + " (" + p.q.name + ")"
}

// Create adds the reservation and records it in the audit log as
// dhcp.reservation.create, with the actor of entry.
func (p *PlannedReservation) Create(ctx context.Context, entry audit.Entry) error {
	details := maps.Clone(reservationDetails(p.res))
	b, err := p.s.writeTarget(ctx, p.q)
	if err == nil {
		details["server"] = b.Name()
		err = b.AddReservation(ctx, p.res)
	}
	entry.Action, entry.Target, entry.Details, entry.Result = "dhcp.reservation.create", p.Target, details, audit.ResultSuccess
	if err != nil {
		details["error"] = err.Error()
		entry.Result = audit.ResultFailure
	}
	_ = p.s.audit.Record(ctx, entry)
	if err == nil {
		base := entry
		base.Time, base.Action = time.Time{}, ""
		p.s.netbox.SyncReservation(ctx, base, p.Target, nbReservation(p.q.name, p.res), nil)
	}
	return err
}
