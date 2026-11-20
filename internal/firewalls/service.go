// SPDX-License-Identifier: GPL-3.0-or-later

// Package firewalls is dcdash's firewall module: OPNsense firewalls (package
// opnsense) with health, VPN, aliases and filter rules, permission checks and
// audit.
package firewalls

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/firewalls/opnsense"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/status"
)

// Module is the permission module of firewalls. The instance is the firewall
// name, the resource "system", "wireguard/<peer>", "ipsec/<connection>",
// "alias/<name>" or "rule/<interface>": "firewall:<name>:<resource>:<action>".
const Module = "firewall"

// ResourceSystem is the permission resource of health and services.
const ResourceSystem = "system"

// Cache lifetimes of the system state.
const (
	probeTTL      = 60 * time.Second
	probeErrorTTL = 10 * time.Second
)

// Backend is what the service needs of a firewall; *opnsense.Client implements it.
type Backend interface {
	Name() string
	System(ctx context.Context) (opnsense.System, error)
	Gateways(ctx context.Context) ([]opnsense.Gateway, error)
	Services(ctx context.Context) ([]opnsense.Service, error)
	RestartService(ctx context.Context, id string) error
	Peers(ctx context.Context) ([]opnsense.Peer, error)
	SetPeerEnabled(ctx context.Context, uuid string, enabled bool) (bool, error)
	Tunnels(ctx context.Context) ([]opnsense.Tunnel, error)
	RestartTunnel(ctx context.Context, name string) error
	Aliases(ctx context.Context) ([]opnsense.Alias, error)
	AddAlias(ctx context.Context, a opnsense.Alias) (string, error)
	SetAlias(ctx context.Context, a opnsense.Alias) error
	DeleteAlias(ctx context.Context, uuid string) error
	ApplyAliases(ctx context.Context) error
	Rules(ctx context.Context) ([]opnsense.Rule, error)
	AddRule(ctx context.Context, r opnsense.Rule) (string, error)
	SetRule(ctx context.Context, r opnsense.Rule) error
	DeleteRule(ctx context.Context, uuid string) error
	HasSavepoints(ctx context.Context) (bool, error)
	Savepoint(ctx context.Context) (string, error)
	ApplyRules(ctx context.Context, revision string) error
	CancelRollback(ctx context.Context, revision string) error
	Reachable(ctx context.Context) error
	Path(ctx context.Context) (local, remote netip.Addr, err error)
	Interfaces(ctx context.Context) ([]opnsense.Interface, error)
	Routes(ctx context.Context) ([]opnsense.Route, error)
}

type fw struct {
	backend Backend
	typ     string
	url     string
	// writeMu serializes changes: rule changes span savepoint, apply and check.
	writeMu sync.Mutex

	mu       sync.Mutex
	sys      opnsense.System
	err      error
	probedAt time.Time
	// spKnown: savepoints tells whether the firewall has savepoints.
	spKnown, savepoints bool
	// protected are the configured protected interfaces; guard the detected ones.
	protected []string
	guard     guard
}

// Service is the firewall API of dcdash.
type Service struct {
	firewalls map[string]*fw
	order     []string
	audit     *audit.Log
	log       zerolog.Logger
	now       func() time.Time
	// settle is the wait between applying rules and checking that dcdash
	// still reaches the firewall.
	settle time.Duration
}

// NewService creates the service for the configured firewalls.
func NewService(cfgs config.Firewalls, auditLog *audit.Log, log zerolog.Logger) (*Service, error) {
	s := &Service{firewalls: map[string]*fw{}, audit: auditLog, log: log.With().Str("component", "firewalls").Logger(),
		now: time.Now, settle: 2 * time.Second}
	for _, c := range cfgs {
		b, err := opnsense.New(c, log)
		if err != nil {
			return nil, err
		}
		s.firewalls[c.Name] = &fw{backend: b, typ: c.Type, url: c.URL, protected: c.ProtectedInterfaces}
		s.order = append(s.order, c.Name)
	}
	return s, nil
}

// SetSettle changes the wait after applying rules (tests).
func (s *Service) SetSettle(d time.Duration) { s.settle = d }

// system returns the cached system state, refreshing it when it is older
// than its lifetime (or always with fresh).
func (s *Service) system(ctx context.Context, x *fw, fresh bool) (opnsense.System, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ttl := probeTTL
	if x.err != nil {
		ttl = probeErrorTTL
	}
	if !fresh && !x.probedAt.IsZero() && s.now().Sub(x.probedAt) < ttl {
		return x.sys, x.err
	}
	sys, err := x.backend.System(ctx)
	if errors.Is(err, context.Canceled) {
		return sys, err
	}
	if err != nil {
		s.log.Warn().Err(err).Str("firewall", x.backend.Name()).Msg("firewall not available")
	}
	x.sys, x.err, x.probedAt = sys, err, s.now()
	return sys, err
}

// StatusChecks returns a dashboard check per firewall: the version, and an
// error if a gateway is down.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		x := s.firewalls[n]
		out = append(out, status.Check{Name: n, Kind: Module, Detail: func(ctx context.Context) (status.Detail, error) {
			sys, err := s.system(ctx, x, true)
			if err != nil {
				return status.Detail{}, &status.Error{Code: errorCode(err), Err: err}
			}
			params := map[string]string{"product": sys.Product, "version": sys.Version}
			gws, err := x.backend.Gateways(ctx)
			if err == nil {
				var down []string
				for _, g := range gws {
					if g.Status == opnsense.GatewayDown {
						down = append(down, g.Name)
					}
				}
				if len(down) > 0 {
					return status.Detail{}, &status.Error{Code: "firewall.gatewayDown",
						Params: map[string]string{"gateways": strings.Join(down, ", "), "count": strconv.Itoa(len(down))},
						Err:    errors.New("gateway down: " + strings.Join(down, ", "))}
				}
				params["gateways"] = strconv.Itoa(len(gws))
			}
			return status.Detail{Text: sys.Product + " " + sys.Version, Code: "firewall.opnsense", Params: params}, nil
		}})
	}
	return out
}

// errorCode is the status code of a firewall error for the web interface.
func errorCode(err error) string {
	switch {
	case errors.Is(err, opnsense.ErrUnauthorized):
		return "firewall.unauthorized"
	case errors.Is(err, opnsense.ErrForbidden):
		return "firewall.forbidden"
	case errors.Is(err, opnsense.ErrUnavailable):
		return "firewall.unavailable"
	}
	return ""
}

// errorText is the error shown to users.
func errorText(err error) string {
	var e *opnsense.Error
	var ve *opnsense.ValidationError
	switch {
	case errors.As(err, &ve):
		return strings.TrimPrefix(ve.Error(), "opnsense: ")
	case errors.Is(err, opnsense.ErrUnauthorized):
		return "the firewall rejected dcdash's API key"
	case errors.Is(err, opnsense.ErrForbidden):
		return "the API user lacks the privilege for this"
	case errors.Is(err, opnsense.ErrUnavailable):
		return "firewall not reachable"
	case errors.As(err, &e) && e.Text != "":
		return e.Text
	}
	return "request failed"
}

// Register adds the firewall routes.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/firewalls", s.handleFirewalls)
	s.registerDetails(mux)
}

// FirewallView describes a firewall in GET /api/v1/firewalls.
type FirewallView struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// URL of the web interface, Host for SSH.
	URL  string `json:"url"`
	Host string `json:"host"`
	// Status is "ok" or "unavailable" (Error says why).
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Product     string `json:"product,omitempty"`
	Version     string `json:"version,omitempty"`
	Uptime      string `json:"uptime,omitempty"`
	MemoryUsed  int64  `json:"memoryUsed,omitempty"`
	MemoryTotal int64  `json:"memoryTotal,omitempty"`
}

// FirewallList is the response of GET /api/v1/firewalls.
type FirewallList struct {
	Firewalls []FirewallView `json:"firewalls"`
}

func (s *Service) view(ctx context.Context, name string) FirewallView {
	x := s.firewalls[name]
	v := FirewallView{Name: name, Type: x.typ, URL: x.url}
	if u, err := url.Parse(x.url); err == nil {
		v.Host = u.Hostname()
	}
	sys, err := s.system(ctx, x, false)
	if err != nil {
		v.Status, v.Error = "unavailable", errorText(err)
		return v
	}
	v.Status, v.Product, v.Version, v.Uptime = "ok", sys.Product, sys.Version, sys.Uptime
	v.MemoryUsed, v.MemoryTotal = sys.MemoryUsed, sys.MemoryTotal
	return v
}

func (s *Service) handleFirewalls(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	views := make([]*FirewallView, len(s.order))
	var wg sync.WaitGroup
	for i, n := range s.order {
		if !subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := s.view(r.Context(), n)
			views[i] = &v
		}()
	}
	wg.Wait()
	out := FirewallList{Firewalls: []FirewallView{}}
	for _, v := range views {
		if v != nil {
			out.Firewalls = append(out.Firewalls, *v)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// hasSavepoints reports whether the firewall has savepoints, probed once (or
// with fresh) and corrected by every savepoint call. If the probe fails, it returns true:
// the change then reports the actual error.
func (s *Service) hasSavepoints(ctx context.Context, x *fw, fresh bool) bool {
	x.mu.Lock()
	known, has := x.spKnown, x.savepoints
	x.mu.Unlock()
	if known && !fresh {
		return has
	}
	has, err := x.backend.HasSavepoints(ctx)
	if err != nil {
		s.log.Debug().Err(err).Str("firewall", x.backend.Name()).Msg("savepoint probe failed")
		if known {
			return has
		}
		return true
	}
	x.setSavepoints(has)
	return has
}

func (x *fw) setSavepoints(has bool) {
	x.mu.Lock()
	x.spKnown, x.savepoints = true, has
	x.mu.Unlock()
}
