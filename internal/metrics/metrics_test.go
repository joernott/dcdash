// SPDX-License-Identifier: GPL-3.0-or-later

package metrics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joernott/dcdash/internal/status"
)

func scrape(t *testing.T, h http.Handler, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestToken(t *testing.T) {
	h := New().Handler("metrics-test-token-only")
	for _, tok := range []string{"", "wrong", "metrics-test-token-onl"} {
		if code, _ := scrape(t, h, tok); code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", tok, code)
		}
	}
	code, body := scrape(t, h, "metrics-test-token-only")
	if code != http.StatusOK || !strings.Contains(body, "dcdash_build_info{") || !strings.Contains(body, "go_goroutines") {
		t.Fatalf("status %d, body:\n%s", code, body)
	}
}

type testKey struct{}

func TestRequestsByRoute(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// A middleware that replaces the request, as session and authz do.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Router(mux).ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), testKey{}, 1)))
	})
	h := m.Middleware(inner)
	for _, p := range []string{"/api/v1/items/1", "/api/v1/items/2", "/nowhere"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	_, body := scrape(t, m.Handler(""), "")
	for _, want := range []string{
		`dcdash_http_requests_total{code="404",method="GET",route="GET /api/v1/items/{id}"} 2`,
		`dcdash_http_requests_total{code="404",method="GET",route="unmatched"} 1`,
		`dcdash_http_request_duration_seconds_count{route="GET /api/v1/items/{id}"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in:\n%s", want, body)
		}
	}
}

func TestAuditCertificateAndBackends(t *testing.T) {
	m := New()
	m.AuditEvent("auth.login", "failure")
	m.AuditEvent("auth.login", "failure")
	expiry := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	var haveCert bool
	m.AddCertificate(func() (time.Time, bool) { return expiry, haveCert })
	m.AddBackends(status.New(
		status.Check{Name: "database", Kind: "system", Check: func(context.Context) (string, error) { return "", nil }},
		status.Check{Name: "kea4", Kind: "dhcp", Check: func(context.Context) (string, error) { return "", errors.New("down") }},
	).Run)

	_, body := scrape(t, m.Handler(""), "")
	if strings.Contains(body, "dcdash_certificate_expiry_timestamp_seconds ") {
		t.Error("certificate expiry reported without a certificate")
	}
	haveCert = true
	_, body = scrape(t, m.Handler(""), "")
	for _, want := range []string{
		`dcdash_audit_events_total{action="auth.login",result="failure"} 2`,
		`dcdash_certificate_expiry_timestamp_seconds 1.798859045e+09`,
		`dcdash_backend_up{kind="system",name="database"} 1`,
		`dcdash_backend_up{kind="dhcp",name="kea4"} 0`,
		`dcdash_backend_check_duration_seconds{kind="dhcp",name="kea4"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in:\n%s", want, body)
		}
	}
}
