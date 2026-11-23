// SPDX-License-Identifier: GPL-3.0-or-later

package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		if !reflect.DeepEqual(got, w) {
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

func TestCodedDetails(t *testing.T) {
	s := New(
		Check{Name: "certificate", Kind: "system", Detail: func(context.Context) (Detail, error) {
			return Detail{Text: "valid until 2027-01-01T00:00:00Z", Code: "certificate.valid",
				Params: map[string]string{"until": "2027-01-01T00:00:00Z"}}, nil
		}},
		Check{Name: "kea", Kind: "dhcp", Detail: func(context.Context) (Detail, error) {
			return Detail{Text: "Kea 3.0.4"}, fmt.Errorf("check: %w", &Error{Code: "dhcp.partnerOutOfTouch",
				Params: map[string]string{"partner": "kea-b"}, Err: errors.New("HA partner kea-b not in touch")})
		}},
		Check{Name: "plain", Kind: "dns", Detail: func(context.Context) (Detail, error) {
			return Detail{Text: "x", Code: "dns.cloudflare"}, errors.New("connection refused")
		}},
	)
	r := s.Run(context.Background())
	want := []Component{
		{Name: "certificate", Kind: "system", Status: "ok", Detail: "valid until 2027-01-01T00:00:00Z",
			DetailCode: "certificate.valid", DetailParams: map[string]string{"until": "2027-01-01T00:00:00Z"}},
		{Name: "kea", Kind: "dhcp", Status: "error", Detail: "check: HA partner kea-b not in touch",
			DetailCode: "dhcp.partnerOutOfTouch", DetailParams: map[string]string{"partner": "kea-b"}},
		// An uncoded error drops the code of the success detail.
		{Name: "plain", Kind: "dns", Status: "error", Detail: "connection refused"},
	}
	for i, w := range want {
		got := r.Components[i]
		got.LatencyMS = 0
		if !reflect.DeepEqual(got, w) {
			t.Errorf("component %d: %+v, want %+v", i, got, w)
		}
	}
}

type httpErr int

func (e httpErr) Error() string   { return fmt.Sprintf("HTTP %d", int(e)) }
func (e httpErr) HTTPStatus() int { return int(e) }

// A check that fails with a 4xx response is repeated once after the retry delay; other
// errors are reported at once.
func TestRetryOnClientError(t *testing.T) {
	var flaky, broken, down int
	s := New(
		Check{Name: "flaky", Kind: "dhcp", Check: func(context.Context) (string, error) {
			if flaky++; flaky == 1 {
				return "", fmt.Errorf("kea: %w", httpErr(408))
			}
			return "Kea 3.0", nil
		}},
		Check{Name: "broken", Kind: "dhcp", Check: func(context.Context) (string, error) {
			broken++
			return "", &Error{Code: "x", Err: httpErr(404)}
		}},
		Check{Name: "down", Kind: "dhcp", Check: func(context.Context) (string, error) {
			down++
			return "", httpErr(503)
		}},
	)
	s.SetRetryDelay(50 * time.Millisecond)
	start := time.Now()
	rep := s.Run(context.Background())
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Errorf("no wait before the retry: %s", took)
	}
	if c := rep.Components[0]; c.Status != "ok" || c.Detail != "Kea 3.0" || flaky != 2 {
		t.Errorf("flaky: %+v after %d attempts", c, flaky)
	}
	if c := rep.Components[1]; c.Status != "error" || c.DetailCode != "x" || broken != 2 {
		t.Errorf("broken: %+v after %d attempts", c, broken)
	}
	if c := rep.Components[2]; c.Status != "error" || down != 1 {
		t.Errorf("down: %+v after %d attempts (5xx is not repeated)", c, down)
	}
}
