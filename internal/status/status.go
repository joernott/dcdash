// SPDX-License-Identifier: GPL-3.0-or-later

// Package status reports the state of dcdash's components and backends
// (database, certificate, directory, and later Proxmox, DNS and DHCP) for the
// dashboard. Unlike /readyz, it includes components whose failure must not take
// dcdash out of a load balancer, such as the directory.
package status

import (
	"context"
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
	Check func(ctx context.Context) (string, error)
}

// Component is the state of one component.
type Component struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"` // "ok" or "error"
	Detail string `json:"detail"`
	// LatencyMS is how long the check took.
	LatencyMS int64 `json:"latencyMs"`
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
			detail, err := c.Check(cctx)
			comp := Component{Name: c.Name, Kind: c.Kind, Status: "ok", Detail: detail, LatencyMS: time.Since(start).Milliseconds()}
			if err != nil {
				comp.Status, comp.Detail = "error", err.Error()
			}
			res[i] = comp
		})
	}
	wg.Wait()
	return Report{Components: res, CheckedAt: time.Now().UTC().Truncate(time.Second)}
}

// Register adds GET /api/v1/status; guard must require a logged-in user.
func (s *Service) Register(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/status", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, s.Run(r.Context()))
	})))
}
