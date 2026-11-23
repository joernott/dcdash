// SPDX-License-Identifier: GPL-3.0-or-later

// Package status reports the state of dcdash's components and backends
// (database, certificate, directory, and later Proxmox, DNS and DHCP) for the
// dashboard. Unlike /readyz, it includes components whose failure must not take
// dcdash out of a load balancer, such as the directory.
package status

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/joernott/dcdash/internal/httpx"
)

// Check reports the state of one component.
type Check struct {
	// Name identifies the component, e.g. "database" or "directory".
	Name string
	// Kind groups components in the UI: "system" for dcdash itself, otherwise
	// the module ("proxmox", "dns", "dhcp").
	Kind string
	// Check returns a detail on success (e.g. "valid until ...") or an error.
	// Used if Detail is nil.
	Check func(ctx context.Context) (string, error)
	// Detail is like Check, but returns a detail the web interface can
	// translate. Its errors may be *Error for the same purpose.
	Detail func(ctx context.Context) (Detail, error)
	// Links are the system's own interfaces, shown as buttons on its tile.
	Links *Links
}

// Detail is the result of a successful check: English text for logs and API
// clients, and optionally a code with parameters that the web interface
// translates (e.g. "certificate.valid" with "until").
type Detail struct {
	Text   string
	Code   string
	Params map[string]string
}

// Error is a failed check with a code for the web interface, like Detail.
type Error struct {
	Code   string
	Params map[string]string
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

// Component is the state of one component.
type Component struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"` // "ok" or "error"
	Detail string `json:"detail"`
	// DetailCode and DetailParams describe Detail for translation; empty if
	// there is no translation (e.g. an error message from a backend).
	DetailCode   string            `json:"detailCode,omitempty"`
	DetailParams map[string]string `json:"detailParams,omitempty"`
	// LatencyMS is how long the check took.
	LatencyMS int64 `json:"latencyMs"`
	// Links are the system's web interface, SSH and RDP addresses, if configured.
	Links *Links `json:"links,omitempty"`
}

// Links are the addresses of a system's own interfaces.
type Links struct {
	// UI is the URL of the web interface.
	UI string `json:"ui,omitempty"`
	// SSH is an ssh:// URL.
	SSH string `json:"ssh,omitempty"`
	// RDP is host[:port] for a Remote Desktop connection.
	RDP string `json:"rdp,omitempty"`
}

// Report is the response of GET /api/v1/status.
type Report struct {
	Components []Component `json:"components"`
	CheckedAt  time.Time   `json:"checkedAt"`
}

// Timeout limits each check.
const Timeout = 5 * time.Second

// Service runs the checks.
type Service struct {
	checks []Check
}

// New creates the service.
func New(checks ...Check) *Service { return &Service{checks: checks} }

// Run runs all checks concurrently and returns their results in order.
func (s *Service) Run(ctx context.Context) Report {
	res := make([]Component, len(s.checks))
	var wg sync.WaitGroup
	for i, c := range s.checks {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, Timeout)
			defer cancel()
			start := time.Now()
			d, err := c.run(cctx)
			comp := Component{Name: c.Name, Kind: c.Kind, Status: "ok", Detail: d.Text, DetailCode: d.Code,
				DetailParams: d.Params, LatencyMS: time.Since(start).Milliseconds(), Links: c.Links}
			if err != nil {
				comp.Status, comp.Detail, comp.DetailCode, comp.DetailParams = "error", err.Error(), "", nil
				var ce *Error
				if errors.As(err, &ce) {
					comp.DetailCode, comp.DetailParams = ce.Code, ce.Params
				}
			}
			res[i] = comp
		})
	}
	wg.Wait()
	return Report{Components: res, CheckedAt: time.Now().UTC().Truncate(time.Second)}
}

func (c Check) run(ctx context.Context) (Detail, error) {
	if c.Detail != nil {
		return c.Detail(ctx)
	}
	text, err := c.Check(ctx)
	return Detail{Text: text}, err
}

// Register adds GET /api/v1/status; guard must require a logged-in user.
func (s *Service) Register(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/status", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, s.Run(r.Context()))
	})))
}
