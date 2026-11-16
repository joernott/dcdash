// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// MaxSubnetLeases limits the leases returned for one subnet.
const MaxSubnetLeases = 5000

// Register adds the DHCP routes.
func (s *Service) Register(mux *http.ServeMux) {
	const base = "/api/v1/dhcp/servers"
	mux.HandleFunc("GET "+base, s.handleServers)
	mux.HandleFunc("GET "+base+"/{server}/subnets", s.handleSubnets)
	mux.HandleFunc("GET "+base+"/{server}/leases", s.handleLeases)
	mux.HandleFunc("DELETE "+base+"/{server}/leases/{ip}", s.handleDeleteLease)
	mux.HandleFunc("GET "+base+"/{server}/reservations", s.handleReservations)
	mux.HandleFunc("POST "+base+"/{server}/reservations", s.handleCreateReservation)
	mux.HandleFunc("PUT "+base+"/{server}/reservations/{id}", s.handleUpdateReservation)
	mux.HandleFunc("DELETE "+base+"/{server}/reservations/{id}", s.handleDeleteReservation)
}

// HAView is the HA state of a server.
type HAView struct {
	Mode         string `json:"mode"`
	Role         string `json:"role"`
	State        string `json:"state"`
	Partner      string `json:"partner"`
	PartnerState string `json:"partnerState"`
	InTouch      bool   `json:"inTouch"`
}

// ServerView describes a server in GET /api/v1/dhcp/servers.
type ServerView struct {
	Name     string   `json:"name"`
	Family   string   `json:"family"`
	HAGroup  string   `json:"haGroup,omitempty"`
	Partners []string `json:"partners,omitempty"`
	// Status is "ok" or "unavailable" (Error says why).
	Status       string           `json:"status"`
	Error        string           `json:"error,omitempty"`
	Version      string           `json:"version,omitempty"`
	Uptime       int64            `json:"uptime,omitempty"`
	Capabilities kea.Capabilities `json:"capabilities"`
	HostDatabase bool             `json:"hostDatabase"`
	HA           []HAView         `json:"ha"`
	SearchKeys   []string         `json:"searchKeys"`
}

// ServerList is the response of GET /api/v1/dhcp/servers.
type ServerList struct {
	Servers []ServerView `json:"servers"`
}

func (s *Service) view(ctx context.Context, name string) ServerView {
	srv := s.servers[name]
	v := ServerView{Name: name, Family: srv.backend.Family(), HAGroup: srv.group, HA: []HAView{}, SearchKeys: srv.backend.SearchKeys()}
	for _, p := range s.partners(name) {
		v.Partners = append(v.Partners, p.backend.Name())
	}
	info, _, err := s.state(ctx, srv, false)
	if err != nil {
		v.Status, v.Error = "unavailable", errorText(err)
		return v
	}
	v.Status, v.Version, v.Uptime = "ok", info.Version, info.Status.Uptime
	v.Capabilities, v.HostDatabase = info.Capabilities, info.HostDatabase
	for _, ha := range info.Status.HA {
		l, r := ha.Servers.Local, ha.Servers.Remote
		v.HA = append(v.HA, HAView{Mode: ha.Mode, Role: l.Role, State: l.State, Partner: r.Name, PartnerState: r.LastState, InTouch: r.InTouch})
	}
	return v
}

// errorText is the error shown to users: no internals beyond Kea's own text.
func errorText(err error) string {
	var ke *kea.Error
	switch {
	case errors.Is(err, kea.ErrUnauthorized):
		return "Kea rejected dcdash's credentials"
	case errors.Is(err, kea.ErrUnavailable):
		return "server not reachable"
	case errors.As(err, &ke):
		return ke.Text
	}
	return "request failed"
}

func (s *Service) handleServers(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := ServerList{Servers: []ServerView{}}
	for _, n := range s.order {
		if subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			out.Servers = append(out.Servers, s.view(r.Context(), n))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// request is a checked request for one server.
type request struct {
	srv     *server
	name    string
	subj    *rbac.Subject
	info    kea.Info
	subnets []kea.SubnetRef
}

// can reports whether the user has the action on the subnet.
func (q request) can(subnetID uint32, action rbac.Action) bool {
	res, ok := resource(q.subnets, subnetID)
	return ok && q.subj.Can(rbac.P(Module, q.name, res, action))
}

// check writes 403 (with audit entry) unless the user has the action on the
// subnet; unknown subnets are 404.
func (q request) check(w http.ResponseWriter, r *http.Request, subnetID uint32, action rbac.Action) bool {
	res, ok := resource(q.subnets, subnetID)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown subnet")
		return false
	}
	return auth.Check(w, r, rbac.P(Module, q.name, res, action))
}

// server returns the server of the request if the user has the action on at
// least one subnet of it and the server answers; it writes the error
// response otherwise.
func (s *Service) server(w http.ResponseWriter, r *http.Request, action rbac.Action) (request, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return request{}, false
	}
	name := r.PathValue("server")
	srv, ok := s.servers[name]
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown DHCP server")
		return request{}, false
	}
	if !subj.Permissions.AllowsAny(Module, name, action) {
		auth.Check(w, r, rbac.P(Module, name, "*", action)) // writes 403 and the audit entry
		return request{}, false
	}
	info, subnets, err := s.state(r.Context(), srv, false)
	if err != nil {
		writeError(w, r, err)
		return request{}, false
	}
	return request{srv: srv, name: name, subj: subj, info: info, subnets: subnets}, true
}

// unsupported writes 409 for a feature the server does not offer.
func unsupported(w http.ResponseWriter, what string) {
	httpx.WriteError(w, http.StatusConflict, "unsupported", what)
}

// writeError writes the response for an error of a Kea server.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ke *kea.Error
	switch {
	case errors.Is(err, context.Canceled):
		// The client went away.
	case errors.Is(err, kea.ErrUnsupported):
		httpx.WriteError(w, http.StatusConflict, "unsupported", "the DHCP server does not support this (hook library missing?)")
	case errors.Is(err, kea.ErrConflict):
		httpx.WriteError(w, http.StatusConflict, "conflict", "the DHCP server reports a conflict: "+errorText(err))
	case errors.As(err, &ke):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", ke.Text)
	case errors.Is(err, kea.ErrUnauthorized):
		hlog.FromRequest(r).Warn().Err(err).Msg("kea rejected dcdash's credentials")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", "the DHCP server rejected dcdash's credentials")
	case errors.Is(err, kea.ErrUnavailable):
		hlog.FromRequest(r).Warn().Err(err).Msg("dhcp server unavailable")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "DHCP server not reachable")
	default:
		hlog.FromRequest(r).Error().Err(err).Msg("dhcp request failed")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", "DHCP request failed")
	}
}

// SubnetView is a subnet with its pools and utilization.
type SubnetView struct {
	ID            uint32   `json:"id"`
	Prefix        string   `json:"prefix"`
	SharedNetwork string   `json:"sharedNetwork,omitempty"`
	Pools         []string `json:"pools"`
	PDPools       []string `json:"pdPools,omitempty"`
	// Statistics (stat_cmds); absent without the hook.
	Stats *kea.SubnetStats `json:"stats,omitempty"`
}

// SubnetList is the response of GET .../subnets.
type SubnetList struct {
	Subnets []SubnetView `json:"subnets"`
}

func (s *Service) handleSubnets(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	ctx := r.Context()
	stats := map[uint32]kea.SubnetStats{}
	if q.info.Capabilities.Statistics {
		list, err := q.srv.backend.LeaseStats(ctx)
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, st := range list {
			stats[st.SubnetID] = st
		}
	}
	out := SubnetList{Subnets: []SubnetView{}}
	for _, ref := range q.subnets {
		if !q.can(ref.ID, rbac.ActionRead) {
			continue
		}
		v := SubnetView{ID: ref.ID, Prefix: ref.Prefix, Pools: []string{}}
		if ref.SharedNetwork != nil {
			v.SharedNetwork = *ref.SharedNetwork
		}
		if q.info.Capabilities.Subnets {
			sn, err := q.srv.backend.Subnet(ctx, ref.ID)
			if err != nil {
				writeError(w, r, err)
				return
			}
			for _, p := range sn.Pools {
				v.Pools = append(v.Pools, p.Pool)
			}
			for _, p := range sn.PDPools {
				v.PDPools = append(v.PDPools, p.Prefix+"/"+strconv.Itoa(p.PrefixLen)+" → /"+strconv.Itoa(p.DelegatedLen))
			}
		}
		if st, ok := stats[ref.ID]; ok {
			v.Stats = &st
		}
		out.Subnets = append(out.Subnets, v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// LeaseView is a lease in API responses.
type LeaseView struct {
	IPAddress         string    `json:"ipAddress"`
	Type              string    `json:"type,omitempty"`
	PrefixLen         int       `json:"prefixLen,omitempty"`
	HWAddress         string    `json:"hwAddress,omitempty"`
	ClientID          string    `json:"clientId,omitempty"`
	DUID              string    `json:"duid,omitempty"`
	IAID              uint32    `json:"iaid,omitempty"`
	Hostname          string    `json:"hostname,omitempty"`
	SubnetID          uint32    `json:"subnetId"`
	Subnet            string    `json:"subnet"`
	State             string    `json:"state"`
	ValidLifetime     uint32    `json:"validLifetime"`
	PreferredLifetime uint32    `json:"preferredLifetime,omitempty"`
	Expires           time.Time `json:"expires"`
	CanDelete         bool      `json:"canDelete"`
}

// LeaseList is the response of GET .../leases.
type LeaseList struct {
	Leases []LeaseView `json:"leases"`
	// Truncated is set when a subnet has more than MaxSubnetLeases leases.
	Truncated bool `json:"truncated"`
}

var leaseStates = map[int]string{kea.LeaseDefault: "assigned", kea.LeaseDeclined: "declined", kea.LeaseExpired: "expired", kea.LeaseReleased: "released"}

func (q request) leaseView(l kea.Lease) LeaseView {
	return LeaseView{
		IPAddress: l.IPAddress, Type: l.Type, PrefixLen: l.PrefixLen, HWAddress: l.HWAddress, ClientID: l.ClientID,
		DUID: l.DUID, IAID: l.IAID, Hostname: l.Hostname, SubnetID: l.SubnetID, Subnet: subnetName(q.subnets, l.SubnetID),
		State: leaseStates[l.State], ValidLifetime: l.ValidLifetime, PreferredLifetime: l.PreferredLifetime,
		Expires:   time.Unix(l.CLTT+int64(l.ValidLifetime), 0).UTC(),
		CanDelete: q.info.Capabilities.LeaseDelete && q.can(l.SubnetID, rbac.ActionOperate),
	}
}

// handleLeases lists the leases of a subnet (?subnet=<id>) or searches
// leases (?by=<key>&q=<value>), returning only leases of readable subnets.
func (s *Service) handleLeases(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	if !q.info.Capabilities.Leases {
		unsupported(w, "the DHCP server does not offer leases (lease_cmds hook)")
		return
	}
	ctx := r.Context()
	query := r.URL.Query()
	var (
		leases []kea.Lease
		err    error
	)
	switch {
	case query.Has("subnet"):
		id, perr := strconv.ParseUint(query.Get("subnet"), 10, 32)
		if perr != nil || !q.check(w, r, uint32(id), rbac.ActionRead) {
			if perr != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid subnet ID")
			}
			return
		}
		leases, err = q.srv.backend.SubnetLeases(ctx, uint32(id))
	case query.Get("by") != "" && query.Get("q") != "":
		by := query.Get("by")
		if !slices.Contains(q.srv.backend.SearchKeys(), by) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid search key")
			return
		}
		leases, err = q.srv.backend.LeasesBy(ctx, by, query.Get("q"))
		var ke *kea.Error
		if errors.As(err, &ke) && ke.Result == kea.ResultError {
			// Kea rejects malformed values (e.g. "not an IP address").
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", ke.Text)
			return
		}
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "give subnet, or by and q")
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := LeaseList{Leases: []LeaseView{}}
	for _, l := range leases {
		if !q.can(l.SubnetID, rbac.ActionRead) {
			continue
		}
		if len(out.Leases) == MaxSubnetLeases {
			out.Truncated = true
			break
		}
		out.Leases = append(out.Leases, q.leaseView(l))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// PartnerResult is the outcome of a change on an HA partner.
type PartnerResult struct {
	Server string `json:"server"`
	// Done: the change was made; false with no error: nothing to change.
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// LeaseDeleteResult is the response of DELETE .../leases/{ip}.
type LeaseDeleteResult struct {
	Partners []PartnerResult `json:"partners"`
}

// handleDeleteLease deletes a lease (operate on its subnet). Kea's HA hook
// does not pass lease_cmds changes to the partner (see ADR 0003), so the
// lease is deleted on the other servers of the HA group as well.
func (s *Service) handleDeleteLease(w http.ResponseWriter, r *http.Request) {
	q, ok := s.server(w, r, rbac.ActionOperate)
	if !ok {
		return
	}
	if !q.info.Capabilities.LeaseDelete {
		unsupported(w, "the DHCP server cannot delete leases (lease_cmds hook)")
		return
	}
	ctx := r.Context()
	ip, typ := r.PathValue("ip"), r.URL.Query().Get("type")
	if typ != "" && typ != "IA_NA" && typ != "IA_PD" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "type must be IA_NA or IA_PD")
		return
	}
	l, found, err := q.srv.backend.Lease(ctx, ip, typ)
	var ke *kea.Error
	switch {
	case errors.As(err, &ke) && ke.Result == kea.ResultError:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", ke.Text)
		return
	case err != nil:
		writeError(w, r, err)
		return
	case !found:
		httpx.WriteError(w, http.StatusNotFound, "not_found", "lease not found")
		return
	}
	if !q.check(w, r, l.SubnetID, rbac.ActionOperate) {
		return
	}
	tgt := target(q.name, subnetName(q.subnets, l.SubnetID))
	details := map[string]any{"ip": ip, "hwAddress": l.HWAddress, "duid": l.DUID, "hostname": l.Hostname}
	if typ != "" {
		details["type"] = typ
	}
	if _, err := q.srv.backend.DeleteLease(ctx, ip, typ); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "dhcp.lease.delete", tgt, audit.ResultFailure, details)
		writeError(w, r, err)
		return
	}
	out := LeaseDeleteResult{Partners: []PartnerResult{}}
	for _, p := range s.partners(q.name) {
		done, err := p.backend.DeleteLease(ctx, ip, typ)
		pr := PartnerResult{Server: p.backend.Name(), Done: done}
		if err != nil {
			pr.Error = errorText(err)
		}
		out.Partners = append(out.Partners, pr)
	}
	if len(out.Partners) > 0 {
		details["partners"] = out.Partners
	}
	s.audit.RecordRequest(r, "dhcp.lease.delete", tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// decode reads a JSON request body.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
		return false
	}
	return true
}
