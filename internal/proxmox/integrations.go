// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/dhcp"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// DHCP reservations and DNS records that the creation job adds for a new guest
// (task 11.14). They are checked when the request arrives, so that a missing
// permission or a typo is reported before anything is created, and added by
// the job after the guest exists and before it is started.

// CreateReservation is a DHCP reservation for a network device of a new guest.
type CreateReservation struct {
	// Network is the index of the network device; its MAC address identifies
	// the guest. A device can have a reservation on a DHCPv4 and on a DHCPv6
	// server.
	Network   int    `json:"network"`
	Server    string `json:"server"`
	SubnetID  uint32 `json:"subnetId"`
	IPAddress string `json:"ipAddress,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
}

// CreateRecord is a DNS record for a new guest.
type CreateRecord struct {
	Provider string `json:"provider"`
	Zone     string `json:"zone"`
	// Name is the owner name, relative to the zone or fully qualified.
	Name string `json:"name"`
	// Type is A, AAAA or PTR.
	Type    string `json:"type"`
	Content string `json:"content"`
	// TTL in seconds; 0 uses 3600.
	TTL uint32 `json:"ttl,omitempty"`
}

// maxCreateEntries limits the reservations and records of a request.
const maxCreateEntries = 16

// SetIntegrations connects the DHCP and DNS modules; nil if not configured.
func (s *Service) SetIntegrations(d *dhcp.Service, n *dns.Service) {
	s.dhcp, s.dns = d, n
}

// planIntegrations checks the reservations and records of a create request
// and returns the job steps that add them. It writes the error response.
func (s *Service) planIntegrations(w http.ResponseWriter, r *http.Request, subj *rbac.Subject, req *CreateRequest) ([]step, bool) {
	if len(req.Reservations)+len(req.Records) > maxCreateEntries {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "reservations and records: at most "+strconv.Itoa(maxCreateEntries))
		return nil, false
	}
	base := audit.FromRequest(r, "", "", "")
	base.Time = time.Time{}
	fail := func(what string, i int, err error) ([]step, bool) {
		var de *rbac.DeniedError
		if errors.As(err, &de) {
			auth.Check(w, r, de.Permission) // writes 403 and the audit entry
			return nil, false
		}
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s[%d]: %v", what, i, err))
		return nil, false
	}
	var steps []step
	for i, rv := range req.Reservations {
		switch {
		case s.dhcp == nil:
			return fail("reservations", i, errors.New("no DHCP servers are configured"))
		case rv.Network < 0 || rv.Network >= len(req.Networks):
			return fail("reservations", i, errors.New("network: not a network device of the guest"))
		}
		var ips []string
		if rv.IPAddress != "" {
			ips = []string{rv.IPAddress}
		}
		pl, err := s.dhcp.PlanReservation(r.Context(), subj, rv.Server, dhcp.ReservationRequest{
			SubnetID: rv.SubnetID, IdentifierType: "hw-address", Identifier: req.Networks[rv.Network].MAC,
			IPAddresses: ips, Hostname: rv.Hostname,
		})
		if err != nil {
			return fail("reservations", i, err)
		}
		steps = append(steps, step{name: "dhcp", detail: pl.Describe(), run: func(ctx context.Context, _ func(string)) error {
			return pl.Create(ctx, base)
		}})
	}
	for i, rc := range req.Records {
		switch {
		case s.dns == nil:
			return fail("records", i, errors.New("no DNS providers are configured"))
		case rc.Type != "A" && rc.Type != "AAAA" && rc.Type != "PTR":
			return fail("records", i, errors.New("type: A, AAAA or PTR"))
		}
		ttl := rc.TTL
		if ttl == 0 {
			ttl = 3600
		}
		pl, err := s.dns.PlanRecord(subj, rc.Provider, rc.Zone, dns.RecordRequest{Name: rc.Name, Type: rc.Type, TTL: ttl, Content: rc.Content})
		if err != nil {
			return fail("records", i, err)
		}
		steps = append(steps, step{name: "dns", detail: pl.Describe(), run: func(ctx context.Context, _ func(string)) error {
			return pl.Create(ctx, base)
		}})
	}
	return steps, true
}
