// SPDX-License-Identifier: GPL-3.0-or-later

package firewalls

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/firewalls/opnsense"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Audit actions.
const (
	AuditServiceRestart = "firewall.service.restart"
	AuditPeerUpdate     = "firewall.wireguard.update"
	AuditTunnelRestart  = "firewall.ipsec.restart"
	AuditAliasCreate    = "firewall.alias.create"
	AuditAliasUpdate    = "firewall.alias.update"
	AuditAliasDelete    = "firewall.alias.delete"
	AuditRuleCreate     = "firewall.rule.create"
	AuditRuleUpdate     = "firewall.rule.update"
	AuditRuleDelete     = "firewall.rule.delete"
)

// Limits.
const (
	maxAliasEntries = 4096
	maxDescription  = 255
)

var aliasNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// PeerResource is the permission resource of a WireGuard peer.
func PeerResource(name string) string { return "wireguard/" + name }

// TunnelResource is the permission resource of an IPsec connection.
func TunnelResource(name string) string { return "ipsec/" + name }

// AliasResource is the permission resource of an alias.
func AliasResource(name string) string { return "alias/" + name }

// RuleResource is the permission resource of the rules of an interface.
func RuleResource(iface string) string { return "rule/" + iface }

func (s *Service) registerDetails(mux *http.ServeMux) {
	const base = "/api/v1/firewalls/{firewall}"
	mux.HandleFunc("GET "+base+"/health", s.handleHealth)
	mux.HandleFunc("POST "+base+"/services/restart", s.handleRestartService)
	mux.HandleFunc("GET "+base+"/vpn", s.handleVPN)
	mux.HandleFunc("PUT "+base+"/wireguard/{uuid}", s.handleSetPeer)
	mux.HandleFunc("POST "+base+"/ipsec/{name}/restart", s.handleRestartTunnel)
	mux.HandleFunc("GET "+base+"/aliases", s.handleAliases)
	mux.HandleFunc("POST "+base+"/aliases", s.handleCreateAlias)
	mux.HandleFunc("PUT "+base+"/aliases/{uuid}", s.handleUpdateAlias)
	mux.HandleFunc("DELETE "+base+"/aliases/{uuid}", s.handleDeleteAlias)
	mux.HandleFunc("GET "+base+"/rules", s.handleRules)
	mux.HandleFunc("POST "+base+"/rules", s.handleCreateRule)
	mux.HandleFunc("PUT "+base+"/rules/{uuid}", s.handleUpdateRule)
	mux.HandleFunc("DELETE "+base+"/rules/{uuid}", s.handleDeleteRule)
}

// lookup returns the firewall of the request, or writes 404 if it doesn't exist or the
// user may not see it.
func (s *Service) lookup(w http.ResponseWriter, r *http.Request) (*fw, *rbac.Subject, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil, nil, false
	}
	name := r.PathValue("firewall")
	x := s.firewalls[name]
	if x == nil || !subj.Permissions.AllowsAny(Module, name, rbac.ActionRead) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "firewall not found")
		return nil, nil, false
	}
	return x, subj, true
}

func perm(x *fw, resource string, action rbac.Action) rbac.Permission {
	return rbac.P(Module, x.backend.Name(), resource, action)
}

// writeError maps an error of a firewall to a response.
func (s *Service) writeError(w http.ResponseWriter, r *http.Request, err error) {
	hlog.FromRequest(r).Warn().Err(err).Msg("firewall request failed")
	var ve *opnsense.ValidationError
	var e *opnsense.Error
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", errorText(err))
	case errors.Is(err, opnsense.ErrUnavailable), errors.Is(err, opnsense.ErrUnauthorized):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", errorText(err))
	case errors.Is(err, opnsense.ErrForbidden):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", errorText(err))
	case errors.Is(err, opnsense.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not found on the firewall")
	case errors.As(err, &e) && e.Status == http.StatusInternalServerError:
		// OPNsense reports refused changes (an alias in use) as 500 with a message.
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", errorText(err))
	default:
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", errorText(err))
	}
}

// Health is the response of GET .../health.
type Health struct {
	Gateways   []opnsense.Gateway `json:"gateways"`
	Services   []ServiceView      `json:"services"`
	CanOperate bool               `json:"canOperate"`
}

// ServiceView is a service in the health response.
type ServiceView struct {
	opnsense.Service
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok || !auth.Check(w, r, perm(x, ResourceSystem, rbac.ActionRead)) {
		return
	}
	gws, err := x.backend.Gateways(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	svcs, err := x.backend.Services(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := Health{Gateways: gws, Services: make([]ServiceView, 0, len(svcs)),
		CanOperate: subj.Can(perm(x, ResourceSystem, rbac.ActionOperate))}
	for _, sv := range svcs {
		out.Services = append(out.Services, ServiceView{Service: sv})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ServiceRequest is the body of POST .../services/restart.
type ServiceRequest struct {
	ID string `json:"id"`
}

func (s *Service) handleRestartService(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok || !auth.Check(w, r, perm(x, ResourceSystem, rbac.ActionOperate)) {
		return
	}
	var req ServiceRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id := req.ID
	svcs, err := x.backend.Services(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !slices.ContainsFunc(svcs, func(sv opnsense.Service) bool { return sv.ID == id }) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "service not found")
		return
	}
	tgt := x.backend.Name() + "/service/" + id
	if err := x.backend.RestartService(r.Context(), id); err != nil {
		s.audit.RecordRequest(r, AuditServiceRestart, tgt, audit.ResultFailure, map[string]any{"error": err.Error()})
		s.writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, AuditServiceRestart, tgt, audit.ResultSuccess, nil)
	w.WriteHeader(http.StatusNoContent)
}

// PeerView is a WireGuard peer in the VPN response.
type PeerView struct {
	opnsense.Peer
	CanOperate bool `json:"canOperate"`
}

// TunnelView is an IPsec connection in the VPN response.
type TunnelView struct {
	opnsense.Tunnel
	CanOperate bool `json:"canOperate"`
}

// VPN is the response of GET .../vpn. A part that the firewall doesn't offer
// (no WireGuard or IPsec, or no privilege) is reported in Errors.
type VPN struct {
	Peers   []PeerView        `json:"peers"`
	Tunnels []TunnelView      `json:"tunnels"`
	Errors  map[string]string `json:"errors"`
}

func (s *Service) handleVPN(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	out := VPN{Peers: []PeerView{}, Tunnels: []TunnelView{}, Errors: map[string]string{}}
	peers, err := x.backend.Peers(r.Context())
	if err != nil {
		out.Errors["wireguard"] = errorText(err)
	}
	for _, p := range peers {
		res := PeerResource(p.Name)
		if subj.Can(perm(x, res, rbac.ActionRead)) {
			out.Peers = append(out.Peers, PeerView{Peer: p, CanOperate: subj.Can(perm(x, res, rbac.ActionOperate))})
		}
	}
	tunnels, err := x.backend.Tunnels(r.Context())
	if err != nil {
		out.Errors["ipsec"] = errorText(err)
	}
	for _, t := range tunnels {
		res := TunnelResource(t.Name)
		if subj.Can(perm(x, res, rbac.ActionRead)) {
			out.Tunnels = append(out.Tunnels, TunnelView{Tunnel: t, CanOperate: subj.Can(perm(x, res, rbac.ActionOperate))})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// PeerUpdate is the body of PUT .../wireguard/{uuid}.
type PeerUpdate struct {
	Enabled bool `json:"enabled"`
}

func (s *Service) handleSetPeer(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var u PeerUpdate
	if err := httpx.DecodeJSON(w, r, &u); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	uuid := r.PathValue("uuid")
	peers, err := x.backend.Peers(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	i := slices.IndexFunc(peers, func(p opnsense.Peer) bool { return p.UUID == uuid })
	if i < 0 {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "peer not found")
		return
	}
	p := peers[i]
	if !auth.Check(w, r, perm(x, PeerResource(p.Name), rbac.ActionOperate)) {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	tgt := x.backend.Name() + "/" + PeerResource(p.Name)
	details := map[string]any{"enabled": u.Enabled, "previous": p.Enabled}
	changed, err := x.backend.SetPeerEnabled(r.Context(), uuid, u.Enabled)
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditPeerUpdate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	details["changed"] = changed
	s.audit.RecordRequest(r, AuditPeerUpdate, tgt, audit.ResultSuccess, details)
	p.Enabled = u.Enabled
	httpx.WriteJSON(w, http.StatusOK, PeerView{Peer: p, CanOperate: true})
}

func (s *Service) handleRestartTunnel(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !auth.Check(w, r, perm(x, TunnelResource(name), rbac.ActionOperate)) {
		return
	}
	tunnels, err := x.backend.Tunnels(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !slices.ContainsFunc(tunnels, func(t opnsense.Tunnel) bool { return t.Name == name }) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "IPsec connection not found")
		return
	}
	tgt := x.backend.Name() + "/" + TunnelResource(name)
	if err := x.backend.RestartTunnel(r.Context(), name); err != nil {
		s.audit.RecordRequest(r, AuditTunnelRestart, tgt, audit.ResultFailure, map[string]any{"error": err.Error()})
		s.writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, AuditTunnelRestart, tgt, audit.ResultSuccess, nil)
	w.WriteHeader(http.StatusNoContent)
}

// AliasView is an alias in the responses.
type AliasView struct {
	opnsense.Alias
	CanWrite bool `json:"canWrite"`
	// Editable: dcdash edits aliases of the types in opnsense.AliasTypes.
	Editable bool `json:"editable"`
}

// AliasList is the response of GET .../aliases.
type AliasList struct {
	Aliases []AliasView `json:"aliases"`
	// CanCreate: the user may create aliases (write on some alias/<name>).
	CanCreate bool `json:"canCreate"`
}

func (s *Service) handleAliases(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	aliases, err := x.backend.Aliases(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := AliasList{Aliases: []AliasView{}, CanCreate: subj.Permissions.AllowsPrefix(Module, x.backend.Name(), "alias/", rbac.ActionWrite)}
	for _, a := range aliases {
		res := AliasResource(a.Name)
		if subj.Can(perm(x, res, rbac.ActionRead)) {
			out.Aliases = append(out.Aliases, AliasView{Alias: a, CanWrite: subj.Can(perm(x, res, rbac.ActionWrite)),
				Editable: slices.Contains(opnsense.AliasTypes, a.Type)})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// AliasRequest is the body of POST and PUT .../aliases.
type AliasRequest struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Enabled     bool     `json:"enabled"`
	Content     []string `json:"content"`
	Description string   `json:"description"`
}

func (a *AliasRequest) normalize() string {
	a.Name, a.Description = strings.TrimSpace(a.Name), strings.TrimSpace(a.Description)
	content := make([]string, 0, len(a.Content))
	for _, c := range a.Content {
		if c = strings.TrimSpace(c); c != "" {
			content = append(content, c)
		}
	}
	a.Content = content
	switch {
	case !aliasNamePattern.MatchString(a.Name):
		return "the name must start with a letter or underscore and have up to 32 letters, digits and underscores"
	case !slices.Contains(opnsense.AliasTypes, a.Type):
		return "the type must be one of " + strings.Join(opnsense.AliasTypes, ", ")
	case len(a.Content) > maxAliasEntries:
		return fmt.Sprintf("at most %d entries", maxAliasEntries)
	}
	return checkText(a.Description)
}

// checkText checks a description: printable, without line breaks, limited length.
func checkText(s string) string {
	if len(s) > maxDescription {
		return fmt.Sprintf("the description is longer than %d characters", maxDescription)
	}
	for _, c := range s {
		if !unicode.IsPrint(c) {
			return "the description may not contain control characters"
		}
	}
	return ""
}

func (a AliasRequest) alias(uuid string) opnsense.Alias {
	return opnsense.Alias{UUID: uuid, Name: a.Name, Type: a.Type, Enabled: a.Enabled, Content: a.Content, Description: a.Description}
}

func (s *Service) handleCreateAlias(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req AliasRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if msg := req.normalize(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_alias", msg)
		return
	}
	if !auth.Check(w, r, perm(x, AliasResource(req.Name), rbac.ActionWrite)) {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	tgt := x.backend.Name() + "/" + AliasResource(req.Name)
	details := map[string]any{"type": req.Type, "content": req.Content, "enabled": req.Enabled}
	uuid, err := x.backend.AddAlias(r.Context(), req.alias(""))
	if err == nil {
		err = x.backend.ApplyAliases(r.Context())
	}
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditAliasCreate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, AuditAliasCreate, tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusCreated, AliasView{Alias: req.alias(uuid), CanWrite: true, Editable: true})
}

// findAlias returns an alias by UUID, or writes 404.
func (s *Service) findAlias(w http.ResponseWriter, r *http.Request, x *fw, subj *rbac.Subject) (opnsense.Alias, bool) {
	aliases, err := x.backend.Aliases(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return opnsense.Alias{}, false
	}
	uuid := r.PathValue("uuid")
	for _, a := range aliases {
		if a.UUID == uuid && subj.Can(perm(x, AliasResource(a.Name), rbac.ActionRead)) {
			return a, true
		}
	}
	httpx.WriteError(w, http.StatusNotFound, "not_found", "alias not found")
	return opnsense.Alias{}, false
}

func (s *Service) handleUpdateAlias(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req AliasRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if msg := req.normalize(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_alias", msg)
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	old, ok := s.findAlias(w, r, x, subj)
	if !ok {
		return
	}
	// A rename needs write on the old and the new name.
	if !auth.Check(w, r, perm(x, AliasResource(old.Name), rbac.ActionWrite)) ||
		!auth.Check(w, r, perm(x, AliasResource(req.Name), rbac.ActionWrite)) {
		return
	}
	if !slices.Contains(opnsense.AliasTypes, old.Type) {
		httpx.WriteError(w, http.StatusConflict, "alias_read_only", "aliases of type "+old.Type+" are changed in OPNsense")
		return
	}
	tgt := x.backend.Name() + "/" + AliasResource(old.Name)
	details := map[string]any{"name": req.Name, "type": req.Type, "content": req.Content, "enabled": req.Enabled,
		"previous": map[string]any{"name": old.Name, "type": old.Type, "content": old.Content, "enabled": old.Enabled}}
	err := x.backend.SetAlias(r.Context(), req.alias(old.UUID))
	if err == nil {
		err = x.backend.ApplyAliases(r.Context())
	}
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditAliasUpdate, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, AuditAliasUpdate, tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusOK, AliasView{Alias: req.alias(old.UUID), CanWrite: true, Editable: true})
}

func (s *Service) handleDeleteAlias(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	a, ok := s.findAlias(w, r, x, subj)
	if !ok || !auth.Check(w, r, perm(x, AliasResource(a.Name), rbac.ActionWrite)) {
		return
	}
	tgt := x.backend.Name() + "/" + AliasResource(a.Name)
	details := map[string]any{"type": a.Type, "content": a.Content}
	err := x.backend.DeleteAlias(r.Context(), a.UUID)
	if err == nil {
		err = x.backend.ApplyAliases(r.Context())
	}
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, AuditAliasDelete, tgt, audit.ResultFailure, details)
		s.writeError(w, r, err)
		return
	}
	s.audit.RecordRequest(r, AuditAliasDelete, tgt, audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

// RuleView is a rule in the responses.
type RuleView struct {
	opnsense.Rule
	CanWrite bool `json:"canWrite"`
}

// RuleList is the response of GET .../rules.
type RuleList struct {
	Rules []RuleView `json:"rules"`
	// CanCreate: the user may create rules (write on some rule/<interface>).
	CanCreate bool `json:"canCreate"`
	// NoSavepoint: the firewall has no savepoints (OPNsense 26.7 and later), so dcdash
	// doesn't change its rules.
	NoSavepoint bool `json:"noSavepoint"`
}

func (s *Service) handleRules(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	rules, err := x.backend.Rules(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := RuleList{Rules: []RuleView{}, CanCreate: subj.Permissions.AllowsPrefix(Module, x.backend.Name(), "rule/", rbac.ActionWrite)}
	out.NoSavepoint = !s.hasSavepoints(r.Context(), x)
	out.CanCreate = out.CanCreate && !out.NoSavepoint
	for _, rule := range rules {
		res := RuleResource(rule.Interface)
		if subj.Can(perm(x, res, rbac.ActionRead)) {
			out.Rules = append(out.Rules, RuleView{Rule: rule, CanWrite: !rule.Legacy && !out.NoSavepoint && subj.Can(perm(x, res, rbac.ActionWrite))})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// RuleRequest is the body of POST and PUT .../rules.
type RuleRequest struct {
	Enabled         bool   `json:"enabled"`
	Sequence        int    `json:"sequence"`
	Action          string `json:"action"`
	Quick           bool   `json:"quick"`
	Interface       string `json:"interface"`
	Direction       string `json:"direction"`
	IPProtocol      string `json:"ipProtocol"`
	Protocol        string `json:"protocol"`
	Source          string `json:"source"`
	SourceNot       bool   `json:"sourceNot"`
	SourcePort      string `json:"sourcePort"`
	Destination     string `json:"destination"`
	DestinationNot  bool   `json:"destinationNot"`
	DestinationPort string `json:"destinationPort"`
	Log             bool   `json:"log"`
	Description     string `json:"description"`
}

var (
	ruleActions    = []string{"pass", "block", "reject"}
	ruleDirections = []string{"in", "out"}
	ruleIPs        = []string{"inet", "inet6", "inet46"}
	portProtocols  = []string{"TCP", "UDP", "TCP/UDP"}
	ifacePattern   = regexp.MustCompile(`^[A-Za-z0-9_.]{1,32}$`)
	addrPattern    = regexp.MustCompile(`^[A-Za-z0-9_.:/()\-]{1,128}$`)
	portPattern    = regexp.MustCompile(`^[A-Za-z0-9_:\-]{1,64}$`)
)

func (q *RuleRequest) normalize() string {
	q.Description = strings.TrimSpace(q.Description)
	if q.Source == "" {
		q.Source = "any"
	}
	if q.Destination == "" {
		q.Destination = "any"
	}
	if q.Protocol == "" {
		q.Protocol = "any"
	}
	switch {
	case !slices.Contains(ruleActions, q.Action):
		return "the action must be pass, block or reject"
	case !slices.Contains(ruleDirections, q.Direction):
		return "the direction must be in or out"
	case !slices.Contains(ruleIPs, q.IPProtocol):
		return "the IP version must be inet, inet6 or inet46"
	case !ifacePattern.MatchString(q.Interface):
		return "an interface is required"
	case !addrPattern.MatchString(q.Source) || !addrPattern.MatchString(q.Destination):
		return "source and destination must be any, an address, a network, an alias or an interface network"
	case (q.SourcePort != "" || q.DestinationPort != "") && !slices.Contains(portProtocols, q.Protocol):
		return "ports need the protocol TCP, UDP or TCP/UDP"
	case q.SourcePort != "" && !portPattern.MatchString(q.SourcePort), q.DestinationPort != "" && !portPattern.MatchString(q.DestinationPort):
		return "a port is a number, a range (1000-2000) or a port alias"
	case q.Sequence < 0 || q.Sequence > 999999:
		return "the sequence must be between 0 and 999999"
	}
	return checkText(q.Description)
}

func (q RuleRequest) rule(uuid string) opnsense.Rule {
	return opnsense.Rule{UUID: uuid, Enabled: q.Enabled, Sequence: q.Sequence, Action: q.Action, Quick: q.Quick,
		Interface: q.Interface, Direction: q.Direction, IPProtocol: q.IPProtocol, Protocol: q.Protocol, Source: q.Source,
		SourceNot: q.SourceNot, SourcePort: q.SourcePort, Destination: q.Destination, DestinationNot: q.DestinationNot,
		DestinationPort: q.DestinationPort, Log: q.Log, Description: q.Description}
}

// RuleResult is the response of a rule change.
type RuleResult struct {
	// Kept: the change is applied and the rollback was canceled.
	Kept bool `json:"kept"`
	// RolledBack: dcdash could not reach the firewall after applying the change, so it left
	// the rollback running: OPNsense restores the previous rules within a minute.
	RolledBack bool      `json:"rolledBack"`
	Warning    string    `json:"warning,omitempty"`
	Rule       *RuleView `json:"rule,omitempty"`
}

// applyRules makes a rule change with a savepoint: savepoint, change, apply with the
// revision, check over a new connection that the firewall still answers, then cancel the
// rollback. If the check fails, the rollback runs and OPNsense restores the rules.
func (s *Service) applyRules(ctx context.Context, x *fw, change func() error) (RuleResult, error) {
	rev, err := x.backend.Savepoint(ctx)
	if err != nil {
		if errors.Is(err, opnsense.ErrNoSavepoint) {
			x.setSavepoints(false)
		}
		return RuleResult{}, err
	}
	x.setSavepoints(true)
	if err := change(); err != nil {
		return RuleResult{}, err
	}
	if err := x.backend.ApplyRules(ctx, rev); err != nil {
		return RuleResult{}, fmt.Errorf("apply: %w", err)
	}
	time.Sleep(s.settle)
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := x.backend.Reachable(check); err != nil {
		s.log.Warn().Err(err).Str("firewall", x.backend.Name()).Str("revision", rev).
			Msg("firewall not reachable after a rule change; OPNsense rolls it back")
		return RuleResult{RolledBack: true}, nil
	}
	if err := x.backend.CancelRollback(ctx, rev); err != nil {
		return RuleResult{Warning: "the rollback could not be canceled, the change may be undone: " + errorText(err)}, nil
	}
	return RuleResult{Kept: true}, nil
}

// ruleError writes the error of a rule change.
func (s *Service) ruleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, opnsense.ErrNoSavepoint) {
		httpx.WriteError(w, http.StatusConflict, "no_savepoint",
			"this OPNsense version has no savepoints; dcdash changes rules only with an automatic rollback")
		return
	}
	s.writeError(w, r, err)
}

func (s *Service) recordRule(r *http.Request, action, tgt string, res RuleResult, err error, details map[string]any) {
	result := audit.ResultSuccess
	switch {
	case err != nil:
		result, details["error"] = audit.ResultFailure, err.Error()
	case res.RolledBack:
		result, details["rolledBack"] = audit.ResultFailure, true
	case res.Warning != "":
		details["warning"] = res.Warning
	}
	s.audit.RecordRequest(r, action, tgt, result, details)
}

func (s *Service) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	x, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req RuleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if msg := req.normalize(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_rule", msg)
		return
	}
	if !auth.Check(w, r, perm(x, RuleResource(req.Interface), rbac.ActionWrite)) {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	var uuid string
	res, err := s.applyRules(r.Context(), x, func() (err error) {
		uuid, err = x.backend.AddRule(r.Context(), req.rule(""))
		return err
	})
	s.recordRule(r, AuditRuleCreate, x.backend.Name()+"/"+RuleResource(req.Interface), res, err,
		map[string]any{"rule": req, "uuid": uuid})
	if err != nil {
		s.ruleError(w, r, err)
		return
	}
	if res.Kept {
		res.Rule = &RuleView{Rule: req.rule(uuid), CanWrite: true}
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// findRule returns an editable rule by UUID, or writes an error.
func (s *Service) findRule(w http.ResponseWriter, r *http.Request, x *fw, subj *rbac.Subject) (opnsense.Rule, bool) {
	rules, err := x.backend.Rules(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return opnsense.Rule{}, false
	}
	uuid := r.PathValue("uuid")
	for _, rule := range rules {
		if rule.UUID == uuid && !rule.Legacy && subj.Can(perm(x, RuleResource(rule.Interface), rbac.ActionRead)) {
			return rule, true
		}
	}
	httpx.WriteError(w, http.StatusNotFound, "not_found", "rule not found")
	return opnsense.Rule{}, false
}

func (s *Service) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req RuleRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if msg := req.normalize(); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_rule", msg)
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	old, ok := s.findRule(w, r, x, subj)
	if !ok {
		return
	}
	// Moving a rule to another interface needs write on both.
	if !auth.Check(w, r, perm(x, RuleResource(old.Interface), rbac.ActionWrite)) ||
		!auth.Check(w, r, perm(x, RuleResource(req.Interface), rbac.ActionWrite)) {
		return
	}
	res, err := s.applyRules(r.Context(), x, func() error { return x.backend.SetRule(r.Context(), req.rule(old.UUID)) })
	s.recordRule(r, AuditRuleUpdate, x.backend.Name()+"/"+RuleResource(old.Interface), res, err,
		map[string]any{"uuid": old.UUID, "rule": req, "previous": old})
	if err != nil {
		s.ruleError(w, r, err)
		return
	}
	if res.Kept {
		res.Rule = &RuleView{Rule: req.rule(old.UUID), CanWrite: true}
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (s *Service) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	x, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	x.writeMu.Lock()
	defer x.writeMu.Unlock()
	old, ok := s.findRule(w, r, x, subj)
	if !ok || !auth.Check(w, r, perm(x, RuleResource(old.Interface), rbac.ActionWrite)) {
		return
	}
	res, err := s.applyRules(r.Context(), x, func() error { return x.backend.DeleteRule(r.Context(), old.UUID) })
	s.recordRule(r, AuditRuleDelete, x.backend.Name()+"/"+RuleResource(old.Interface), res, err,
		map[string]any{"uuid": old.UUID, "previous": old})
	if err != nil {
		s.ruleError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
