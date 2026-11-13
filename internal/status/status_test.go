// SPDX-License-Identifier: GPL-3.0-or-later

package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRun(t *testing.T) {
	slow := func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return "slow but ok", nil
		}
	}
	s := New(
		Check{Name: "database", Kind: "system", Check: func(context.Context) (string, error) { return "", nil }},
		Check{Name: "directory", Kind: "system", Check: func(context.Context) (string, error) { return "", errors.New("connection refused") }},
		Check{Name: "slow", Kind: "dns", Check: slow},
	)
	r := s.Run(context.Background())
	if len(r.Components) != 3 || r.CheckedAt.IsZero() {
		t.Fatalf("report: %+v", r)
	}
	want := []Component{
		{Name: "database", Kind: "system", Status: "ok"},
		{Name: "directory", Kind: "system", Status: "error", Detail: "connection refused"},
		{Name: "slow", Kind: "dns", Status: "ok", Detail: "slow but ok"},
	}
	for i, w := range want {
		got := r.Components[i]
		got.LatencyMS = 0
		if got != w {
			t.Errorf("component %d: %+v, want %+v", i, got, w)
		}
	}
	if r.Components[2].LatencyMS < 40 {
		t.Errorf("latency: %d", r.Components[2].LatencyMS)
	}
}

func TestRegister(t *testing.T) {
	mux := http.NewServeMux()
	guarded := false
	New(Check{Name: "database", Kind: "system", Check: func(context.Context) (string, error) { return "", nil }}).
		Register(mux, func(h http.Handler) http.Handler { guarded = true; return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	var r Report
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || !guarded || len(r.Components) != 1 || r.Components[0].Status != "ok" {
		t.Errorf("status: %d %s (guarded %v)", rec.Code, rec.Body, guarded)
	}
}
