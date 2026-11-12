// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/testutil"
)

func TestRedirectHandler(t *testing.T) {
	tests := []struct {
		port, host, target, want string
		status                   int
	}{
		{"443", "dcdash.example.com", "/a?b=c", "https://dcdash.example.com/a?b=c", http.StatusPermanentRedirect},
		{"443", "dcdash.example.com:80", "/", "https://dcdash.example.com/", http.StatusPermanentRedirect},
		{"8443", "dcdash.example.com:8080", "/x", "https://dcdash.example.com:8443/x", http.StatusPermanentRedirect},
		{"443", "[2001:db8::1]:80", "/", "https://[2001:db8::1]/", http.StatusPermanentRedirect},
		{"8443", "[2001:db8::1]", "/", "https://[2001:db8::1]:8443/", http.StatusPermanentRedirect},
		{"443", "evil.example/@x", "/", "", http.StatusBadRequest},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.target, nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		redirectHandler(tt.port).ServeHTTP(rec, req)
		if rec.Code != tt.status || rec.Header().Get("Location") != tt.want {
			t.Errorf("%s%s (port %s): got %d %q, want %d %q", tt.host, tt.target, tt.port,
				rec.Code, rec.Header().Get("Location"), tt.status, tt.want)
		}
	}
}

func TestHSTSOnlyOverTLS(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, zerolog.Nop(), Options{}).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if v := rec.Header().Get("Strict-Transport-Security"); v != "" {
		t.Errorf("HSTS over plain HTTP: %q", v)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.TLS = &tls.ConnectionState{}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if v := rec.Header().Get("Strict-Transport-Security"); v != "max-age=15552000" {
		t.Errorf("HSTS over TLS: %q", v)
	}

	cfg.Server.HSTSMaxAge = 0
	rec = httptest.NewRecorder()
	New(cfg, zerolog.Nop(), Options{}).Handler().ServeHTTP(rec, req)
	if v := rec.Header().Get("Strict-Transport-Security"); v != "" {
		t.Errorf("HSTS disabled but sent: %q", v)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestRunWithRedirectListener(t *testing.T) {
	certFile, keyFile := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{})
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.Listen = freeAddr(t)
	cfg.Server.RedirectListen = freeAddr(t)
	opts := Options{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(&cfg, zerolog.Nop(), opts).Run(ctx) }()

	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var resp *http.Response
	for i := 0; i < 50; i++ { // wait for the listeners
		if resp, err = client.Get("http://" + cfg.Server.RedirectListen + "/readyz"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET redirect listener: %v", err)
	}
	_ = resp.Body.Close()
	_, port, _ := net.SplitHostPort(cfg.Server.Listen)
	if want := "https://127.0.0.1:" + port + "/readyz"; resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != want {
		t.Errorf("got %d %q, want 308 %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestRunFailsOnBusyRedirectPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	cfg := config.Default()
	cfg.Server.Listen = freeAddr(t)
	cfg.Server.RedirectListen = busy.Addr().String()
	opts := Options{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if err := New(&cfg, zerolog.Nop(), opts).Run(context.Background()); err == nil {
		t.Error("expected error")
	}
}
