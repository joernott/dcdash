// SPDX-License-Identifier: GPL-3.0-or-later

package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/status"
)

// Module is the permission module of DNS. The permission resource is the
// zone name: "dns:<provider>:<zone>:<action>". read lists zones and records,
// write changes records, admin creates and deletes zones.
const Module = "dns"

// Service is the DNS API of dcdash.
type Service struct {
	providers map[string]Provider
	order     []string
	audit     *audit.Log
	log       zerolog.Logger
	// netbox receives changed A and AAAA records; nil if not configured.
	netbox *netbox.Service
	// links are the configured UI, SSH and RDP links per provider.
	links map[string]*status.Links
}

// NewService creates the API for the given providers.
func NewService(providers []Provider, auditLog *audit.Log, log zerolog.Logger) *Service {
	s := &Service{providers: map[string]Provider{}, audit: auditLog, log: log.With().Str("component", "dns").Logger()}
	for _, p := range providers {
		s.providers[p.Name()] = p
		s.order = append(s.order, p.Name())
	}
	return s
}

// SetLinks sets the UI, SSH and RDP links per provider name, shown on the DNS page.
func (s *Service) SetLinks(links map[string]*status.Links) {
	s.links = links
}

// StatusChecks returns a dashboard check per provider.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, status.Check{Name: n, Kind: Module, Detail: s.providers[n].Check})
	}
	return out
}

// Register adds the DNS routes.
func (s *Service) Register(mux *http.ServeMux) {
	const base = "/api/v1/dns/providers"
	mux.HandleFunc("GET "+base, s.handleProviders)
	mux.HandleFunc("GET "+base+"/{provider}/zones", s.handleZones)
	mux.HandleFunc("POST "+base+"/{provider}/zones", s.handleCreateZone)
	mux.HandleFunc("DELETE "+base+"/{provider}/zones/{zone}", s.handleDeleteZone)
	mux.HandleFunc("GET "+base+"/{provider}/zones/{zone}/records", s.handleRecords)
	mux.HandleFunc("POST "+base+"/{provider}/zones/{zone}/records", s.handleCreateRecord)
	mux.HandleFunc("PUT "+base+"/{provider}/zones/{zone}/records/{id}", s.handleUpdateRecord)
	mux.HandleFunc("DELETE "+base+"/{provider}/zones/{zone}/records/{id}", s.handleDeleteRecord)
}

// ProviderInfo describes a provider in GET /api/v1/dns/providers.
type ProviderInfo struct {
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	Capabilities Capabilities `json:"capabilities"`
	// Links are the web interface, SSH and RDP addresses of the server, if configured.
	Links *status.Links `json:"links,omitempty"`
}

// ProviderList is the response of GET /api/v1/dns/providers.
type ProviderList struct {
	Providers []ProviderInfo `json:"providers"`
}

func (s *Service) handleProviders(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := ProviderList{Providers: []ProviderInfo{}}
	for _, n := range s.order {
		if subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			p := s.providers[n]
			out.Providers = append(out.Providers, ProviderInfo{Name: n, Kind: p.Kind(), Capabilities: p.Capabilities(), Links: s.links[n]})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// provider returns the provider of the request if the user has the action on
// at least one zone of it; it writes the error response otherwise.
func (s *Service) provider(w http.ResponseWriter, r *http.Request, action rbac.Action) (Provider, *rbac.Subject, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil, nil, false
	}
	name := r.PathValue("provider")
	p, ok := s.providers[name]
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown DNS provider")
		return nil, nil, false
	}
	if !subj.Permissions.AllowsAny(Module, name, action) {
		auth.Check(w, r, rbac.P(Module, name, "*", action)) // writes 403 and the audit entry
		return nil, nil, false
	}
	return p, subj, true
}

// zone returns the provider and the zone of the request, checked for the
// action on that zone.
func (s *Service) zone(w http.ResponseWriter, r *http.Request, action rbac.Action) (Provider, string, bool) {
	p, _, ok := s.provider(w, r, action)
	if !ok {
		return nil, "", false
	}
	zone := NormalizeName(r.PathValue("zone"))
	if !ValidZoneName(zone) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid zone name")
		return nil, "", false
	}
	if !auth.Check(w, r, rbac.P(Module, p.Name(), zone, action)) {
		return nil, "", false
	}
	return p, zone, true
}

// writeError writes the response for an error of a provider.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_record", ve.Msg)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "zone or record not found")
	case errors.Is(err, ErrConflict):
		httpx.WriteError(w, http.StatusConflict, "conflict", "changed in the meantime, reload and try again")
	case errors.Is(err, ErrExists):
		httpx.WriteError(w, http.StatusConflict, "exists", "already exists")
	case errors.Is(err, ErrUnsupported):
		httpx.WriteError(w, http.StatusConflict, "unsupported", err.Error())
	case errors.Is(err, ErrRejected):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", err.Error())
	case errors.Is(err, context.Canceled):
		// The client went away.
	case errors.Is(err, ErrUnavailable):
		hlog.FromRequest(r).Warn().Err(err).Msg("dns backend unavailable")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "DNS backend not reachable")
	case errors.Is(err, ErrForbidden):
		hlog.FromRequest(r).Warn().Err(err).Msg("dns backend refused the request")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", "the DNS backend refused the request")
	default:
		hlog.FromRequest(r).Error().Err(err).Msg("dns request failed")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", "DNS request failed")
	}
}

// ZoneList is the response of GET .../zones.
type ZoneList struct {
	Zones []Zone `json:"zones"`
}

func (s *Service) handleZones(w http.ResponseWriter, r *http.Request) {
	p, subj, ok := s.provider(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	zones, err := p.Zones(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := ZoneList{Zones: []Zone{}}
	for _, z := range zones {
		if subj.Can(rbac.P(Module, p.Name(), z.Name, rbac.ActionRead)) {
			out.Zones = append(out.Zones, z)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ZoneRequest is the body of POST .../zones.
type ZoneRequest struct {
	Name string `json:"name"`
}

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

func (s *Service) handleCreateZone(w http.ResponseWriter, r *http.Request) {
	p, _, ok := s.provider(w, r, rbac.ActionAdmin)
	if !ok {
		return
	}
	var req ZoneRequest
	if !decode(w, r, &req) {
		return
	}
	zone := NormalizeName(req.Name)
	if !ValidZoneName(zone) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_record", "invalid zone name")
		return
	}
	if !auth.Check(w, r, rbac.P(Module, p.Name(), zone, rbac.ActionAdmin)) {
		return
	}
	target := p.Name() + "/" + zone
	z, err := p.CreateZone(r.Context(), zone)
	if err != nil {
		s.audit.RecordRequest(r, "dns.zone.create", target, audit.ResultFailure, map[string]any{"error": err.Error()})
		writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dns.zone.create", target, audit.ResultSuccess, nil)
	httpx.WriteJSON(w, http.StatusCreated, z)
}

func (s *Service) handleDeleteZone(w http.ResponseWriter, r *http.Request) {
	p, zone, ok := s.zone(w, r, rbac.ActionAdmin)
	if !ok {
		return
	}
	// Safety confirmation: the client repeats the zone name.
	if NormalizeName(r.URL.Query().Get("confirm")) != zone {
		httpx.WriteError(w, http.StatusBadRequest, "confirmation_required", "repeat the zone name in the confirm parameter")
		return
	}
	target := p.Name() + "/" + zone
	if err := p.DeleteZone(r.Context(), zone); err != nil {
		s.audit.RecordRequest(r, "dns.zone.delete", target, audit.ResultFailure, map[string]any{"error": err.Error()})
		writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dns.zone.delete", target, audit.ResultSuccess, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleRecords(w http.ResponseWriter, r *http.Request) {
	p, zone, ok := s.zone(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	rs, err := p.Records(r.Context(), zone)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if rs.Records == nil {
		rs.Records = []Record{}
	}
	httpx.WriteJSON(w, http.StatusOK, rs)
}

// RecordRequest is the body of POST and PUT .../records. Version is the
// RecordSet version (create) or the record's version (update).
type RecordRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	TTL     uint32 `json:"ttl"`
	Content string `json:"content"`
	Proxied *bool  `json:"proxied,omitempty"`
	Comment string `json:"comment,omitempty"`
	Version string `json:"version"`
	// Projects of the address of an A or AAAA record in NetBox (task
	// 12.8.3); missing keeps them.
	Projects *[]string `json:"projects,omitempty"`
}

// projectAddrs returns the address of an A or AAAA request for the project
// check.
func (q RecordRequest) projectAddrs() []string {
	if q.Type == "A" || q.Type == "AAAA" {
		return []string{q.Content}
	}
	return nil
}

func (q RecordRequest) record() Record {
	return Record{Name: q.Name, Type: q.Type, TTL: q.TTL, Content: q.Content, Proxied: q.Proxied, Comment: q.Comment}
}

// recordDetails are the audit details of a record.
func recordDetails(r Record, id string) map[string]any {
	d := map[string]any{"name": r.Name, "type": r.Type, "ttl": r.TTL, "content": r.Content}
	if id != "" {
		d["id"] = id
	}
	if r.Proxied != nil {
		d["proxied"] = *r.Proxied
	}
	return d
}

// recordTarget is the audit target of a record change.
func recordTarget(p Provider, zone string) string { return p.Name() + "/" + zone }

func (s *Service) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	p, zone, ok := s.zone(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req RecordRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.netbox.CheckAddressProjects(w, r, req.projectAddrs(), req.Projects) {
		return
	}
	rec, err := p.CreateRecord(r.Context(), zone, req.record(), req.Version)
	if err != nil {
		d := recordDetails(req.record(), "")
		d["error"] = err.Error()
		s.audit.RecordRequest(r, "dns.record.create", recordTarget(p, zone), audit.ResultFailure, d)
		writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dns.record.create", recordTarget(p, zone), audit.ResultSuccess, recordDetails(rec, rec.ID))
	httpx.WriteJSON(w, http.StatusCreated, RecordResult{Record: rec,
		NetBox: s.recordChanged(r.Context(), netbox.Base(r), recordTarget(p, zone), rec, nil, req.Projects)})
}

func (s *Service) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	p, zone, ok := s.zone(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	var req RecordRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.netbox.CheckAddressProjects(w, r, req.projectAddrs(), req.Projects) {
		return
	}
	id := r.PathValue("id")
	// Looked up first so the audit entry shows the previous values.
	prev, hasPrev := s.findRecord(r, p, zone, id)
	rec, err := p.UpdateRecord(r.Context(), zone, id, req.record(), req.Version)
	if err != nil {
		d := recordDetails(req.record(), id)
		d["error"] = err.Error()
		s.audit.RecordRequest(r, "dns.record.update", recordTarget(p, zone), audit.ResultFailure, d)
		writeError(w, r, err)
		return
	}
	d := recordDetails(rec, rec.ID)
	d["previousId"] = id
	if hasPrev {
		d["previous"] = recordDetails(prev, id)
	}
	s.audit.RecordRequest(r, "dns.record.update", recordTarget(p, zone), audit.ResultSuccess, d)
	var before *Record
	if hasPrev {
		before = &prev
	}
	httpx.WriteJSON(w, http.StatusOK, RecordResult{Record: rec,
		NetBox: s.recordChanged(r.Context(), netbox.Base(r), recordTarget(p, zone), rec, before, req.Projects)})
}

func (s *Service) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	p, zone, ok := s.zone(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	id := r.PathValue("id")
	version := r.URL.Query().Get("version")
	// The record is looked up first so the audit entry shows what was
	// deleted.
	details := map[string]any{"id": id}
	old, found := s.findRecord(r, p, zone, id)
	if found {
		details = recordDetails(old, id)
	}
	if err := p.DeleteRecord(r.Context(), zone, id, version); err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "dns.record.delete", recordTarget(p, zone), audit.ResultFailure, details)
		writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, "dns.record.delete", recordTarget(p, zone), audit.ResultSuccess, details)
	if found {
		if nb := s.recordDeleted(r.Context(), netbox.Base(r), recordTarget(p, zone), old); nb != nil {
			httpx.WriteJSON(w, http.StatusOK, DeleteResult{NetBox: nb})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// findRecord looks up a record by ID for the audit log; false if it can't be
// found (the change itself then reports the error).
func (s *Service) findRecord(r *http.Request, p Provider, zone, id string) (Record, bool) {
	rs, err := p.Records(r.Context(), zone)
	if err != nil {
		return Record{}, false
	}
	i := slices.IndexFunc(rs.Records, func(x Record) bool { return x.ID == id })
	if i < 0 {
		return Record{}, false
	}
	return rs.Records[i], true
}
