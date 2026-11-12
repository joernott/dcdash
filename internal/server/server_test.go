// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/testutil"
)

func testConfig() *config.Config {
	cfg := config.Default()
	cfg.Server.TLS.Mode = config.TLSModeNone
	return &cfg
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	h := New(testConfig(), zerolog.Nop(), Options{}).Handler()
	rec := do(t, h, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id")
	}
	for _, hdr := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(hdr) == "" {
			t.Errorf("missing security header %s", hdr)
		}
	}
}

func TestReadyz(t *testing.T) {
	ok := Check{Name: "db", Check: func(context.Context) (string, error) { return "", nil }}
	detail := Check{Name: "certificate", Check: func(context.Context) (string, error) { return "valid until X", nil }}
	bad := Check{Name: "ldap", Check: func(context.Context) (string, error) { return "", errors.New("unreachable") }}

	rec := do(t, New(testConfig(), zerolog.Nop(), Options{Checks: []Check{ok, detail}}).Handler(), http.MethodGet, "/readyz")
	var body HealthStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusOK || body.Checks["db"] != "ok" || body.Checks["certificate"] != "valid until X" {
		t.Errorf("all ok: got %d %+v", rec.Code, body)
	}

	rec = do(t, New(testConfig(), zerolog.Nop(), Options{Checks: []Check{ok, bad}}).Handler(), http.MethodGet, "/readyz")
	body = HealthStatus{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body.Checks["ldap"] != "error: unreachable" || body.Checks["db"] != "ok" {
		t.Errorf("failing check: got %d %+v", rec.Code, body)
	}
}

func TestVersion(t *testing.T) {
	rec := do(t, New(testConfig(), zerolog.Nop(), Options{}).Handler(), http.MethodGet, "/api/v1/version")
	var v map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if v["version"] == "" || v["goVersion"] == "" {
		t.Errorf("incomplete version info: %v", v)
	}
}

func TestUnknownAPIIsJSON404(t *testing.T) {
	rec := do(t, New(testConfig(), zerolog.Nop(), Options{}).Handler(), http.MethodGet, "/api/v1/nope")
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusNotFound || body.Error.Code != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestUIHandlerServesNonAPI(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ui")) })
	h := New(testConfig(), zerolog.Nop(), Options{UI: ui}).Handler()
	if rec := do(t, h, http.MethodGet, "/some/page"); rec.Body.String() != "ui" {
		t.Errorf("UI not served: %q", rec.Body)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("API path served by UI: %d", rec.Code)
	}
}

func TestRecovererAndAccessLog(t *testing.T) {
	var buf bytes.Buffer
	log := zerolog.New(&buf)
	h := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), log, "")
	rec := do(t, h, http.MethodGet, "/x")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "panic in handler") || !strings.Contains(out, `"status":500`) || !strings.Contains(out, `"req_id"`) {
		t.Errorf("unexpected log output: %s", out)
	}
}

func TestServeTLSAndShutdown(t *testing.T) {
	certFile, keyFile := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{})
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	opts := Options{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(&cfg, zerolog.Nop(), opts).Serve(ctx, ln) }()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- self-signed test certificate
	}}
	resp, err := client.Get("https://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Errorf("unexpected response: %d %+v", resp.StatusCode, resp.TLS)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}
