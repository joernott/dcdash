// SPDX-License-Identifier: GPL-3.0-or-later

// Package switches is dcdash's switch module: Aruba CX switches (package
// aoscx), their inventory and health, with permission checks and audit.
package switches

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/switches/aoscx"
)

// Module is the permission module of switches. The permission instance is the
// switch name, the resource "port/<interface>" or "vlan/<id>":
// "switch:<name>:<resource>:<action>". read shows the switch, its ports,
// VLANs and MAC addresses; write changes ports and VLANs.
const Module = "switch"

// Cache lifetimes of an inventory.
const (
	probeTTL      = 60 * time.Second
	probeErrorTTL = 10 * time.Second
)

// Backend is what the service needs of a switch; *aoscx.Client implements it.
type Backend interface {
	Name() string
	APIVersion() string
	Inventory(ctx context.Context) (aoscx.Inventory, error)
	Close(ctx context.Context) error
}

type sw struct {
	backend Backend
	typ     string

	mu       sync.Mutex
	inv      aoscx.Inventory
	err      error
	probedAt time.Time
}

// Service is the switch API of dcdash.
type Service struct {
	switches map[string]*sw
	order    []string
	audit    *audit.Log
	log      zerolog.Logger
	now      func() time.Time
}

// NewService creates the service for the configured switches.
func NewService(cfgs config.Switches, auditLog *audit.Log, log zerolog.Logger) (*Service, error) {
	s := newService(auditLog, log)
	for _, c := range cfgs {
		b, err := aoscx.New(c, log)
		if err != nil {
			return nil, err
		}
		s.add(b, c.Type)
	}
	return s, nil
}

func newService(auditLog *audit.Log, log zerolog.Logger) *Service {
	return &Service{switches: map[string]*sw{}, audit: auditLog,
		log: log.With().Str("component", "switches").Logger(), now: time.Now}
}

func (s *Service) add(b Backend, typ string) {
	s.switches[b.Name()] = &sw{backend: b, typ: typ}
	s.order = append(s.order, b.Name())
}

// inventory returns the cached inventory of a switch, refreshing it when it
// is older than its lifetime (or always with fresh).
func (s *Service) inventory(ctx context.Context, x *sw, fresh bool) (aoscx.Inventory, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ttl := probeTTL
	if x.err != nil {
		ttl = probeErrorTTL
	}
	if !fresh && !x.probedAt.IsZero() && s.now().Sub(x.probedAt) < ttl {
		return x.inv, x.err
	}
	inv, err := x.backend.Inventory(ctx)
	if errors.Is(err, context.Canceled) {
		return inv, err // do not cache a canceled request
	}
	if err != nil {
		s.log.Warn().Err(err).Str("switch", x.backend.Name()).Msg("switch not available")
	}
	x.inv, x.err, x.probedAt = inv, err, s.now()
	return inv, err
}

// StatusChecks returns a dashboard check per switch.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		x := s.switches[n]
		out = append(out, status.Check{Name: n, Kind: Module, Detail: func(ctx context.Context) (status.Detail, error) {
			inv, err := s.inventory(ctx, x, true)
			if err != nil {
				return status.Detail{}, &status.Error{Code: errorCode(err), Err: err}
			}
			model := model(inv)
			return status.Detail{Text: model + " " + inv.Hostname + ", AOS-CX " + inv.Firmware, Code: "switch.aoscx",
				Params: map[string]string{"model": model, "hostname": inv.Hostname, "firmware": inv.Firmware}}, nil
		}})
	}
	return out
}

// Close logs out of all switches.
func (s *Service) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []error
	for _, n := range s.order {
		errs = append(errs, s.switches[n].backend.Close(ctx))
	}
	return errors.Join(errs...)
}

// model is the product name of the first chassis, or the platform.
func model(inv aoscx.Inventory) string {
	if len(inv.Members) > 0 && inv.Members[0].Product != "" {
		return inv.Members[0].Product
	}
	return inv.Platform
}

// errorCode is the status code of a switch error for the web interface.
func errorCode(err error) string {
	switch {
	case errors.Is(err, aoscx.ErrUnauthorized):
		return "switch.unauthorized"
	case errors.Is(err, aoscx.ErrSessionLimit):
		return "switch.sessionLimit"
	case errors.Is(err, aoscx.ErrUnavailable):
		return "switch.unavailable"
	}
	return ""
}

// errorText is the error shown to users: no internals beyond the switch's own text.
func errorText(err error) string {
	var e *aoscx.Error
	switch {
	case errors.Is(err, aoscx.ErrUnauthorized):
		return "the switch rejected dcdash's credentials"
	case errors.Is(err, aoscx.ErrSessionLimit):
		return "too many sessions on the switch"
	case errors.Is(err, aoscx.ErrUnavailable):
		return "switch not reachable"
	case errors.As(err, &e) && e.Text != "":
		return e.Text
	}
	return "request failed"
}

// Register adds the switch routes.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/switches", s.handleSwitches)
}

// SwitchView describes a switch in GET /api/v1/switches.
type SwitchView struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Status is "ok" or "unavailable" (Error says why).
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	Hostname   string         `json:"hostname,omitempty"`
	Model      string         `json:"model,omitempty"`
	Platform   string         `json:"platform,omitempty"`
	Firmware   string         `json:"firmware,omitempty"`
	APIVersion string         `json:"apiVersion,omitempty"`
	Booted     *time.Time     `json:"booted,omitempty"`
	Members    []aoscx.Member `json:"members"`
}

// SwitchList is the response of GET /api/v1/switches.
type SwitchList struct {
	Switches []SwitchView `json:"switches"`
}

func (s *Service) view(ctx context.Context, name string) SwitchView {
	x := s.switches[name]
	v := SwitchView{Name: name, Type: x.typ, Members: []aoscx.Member{}}
	inv, err := s.inventory(ctx, x, false)
	if err != nil {
		v.Status, v.Error = "unavailable", errorText(err)
		return v
	}
	v.Status, v.Hostname, v.Model, v.Platform, v.Firmware = "ok", inv.Hostname, model(inv), inv.Platform, inv.Firmware
	v.APIVersion = x.backend.APIVersion()
	if !inv.Booted.IsZero() {
		v.Booted = &inv.Booted
	}
	if inv.Members != nil {
		v.Members = inv.Members
	}
	return v
}

func (s *Service) handleSwitches(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := SwitchList{Switches: []SwitchView{}}
	// The inventories are read in parallel: a switch that does not answer
	// should not delay the others by its timeout.
	views := make([]*SwitchView, len(s.order))
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
	for _, v := range views {
		if v != nil {
			out.Switches = append(out.Switches, *v)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
