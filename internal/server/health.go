// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"time"

	"github.com/joernott/dcdash/internal/httpx"
)

// Check is a named readiness check, e.g. "database" or "certificate".
// On success it may return a detail such as "valid until ...", which is
// reported instead of "ok".
type Check struct {
	Name  string
	Check func(ctx context.Context) (string, error)
}

// HealthStatus is the body of /healthz and /readyz.
type HealthStatus struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

const readyTimeout = 5 * time.Second

// handleHealth reports liveness: the process is up and serving HTTP.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, HealthStatus{Status: "ok"})
}

// handleReady reports readiness: all registered checks pass.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()

	res := HealthStatus{Status: "ok", Checks: map[string]string{}}
	status := http.StatusOK
	for _, c := range s.opts.Checks {
		detail, err := c.Check(ctx)
		switch {
		case err != nil:
			res.Checks[c.Name] = "error: " + err.Error()
			res.Status = "unavailable"
			status = http.StatusServiceUnavailable
		case detail != "":
			res.Checks[c.Name] = detail
		default:
			res.Checks[c.Name] = "ok"
		}
	}
	httpx.WriteJSON(w, status, res)
}
