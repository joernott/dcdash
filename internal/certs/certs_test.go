// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"crypto/tls"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/testutil"
)

func TestHolderWithoutCertificate(t *testing.T) {
	var h holder
	if _, err := h.GetCertificate(nil); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("GetCertificate: %v", err)
	}
	if _, err := h.status(time.Now()); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("status: %v", err)
	}
}

func TestHolderStatus(t *testing.T) {
	now := time.Now()
	certFile, keyFile := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)})
	c, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	c.Leaf = nil // force parsing in set
	var h holder
	if err := h.set(&c); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.GetCertificate(nil); got != &c {
		t.Error("GetCertificate did not return the stored certificate")
	}
	if detail, err := h.status(now); err != nil || !strings.HasPrefix(detail, "valid until ") {
		t.Errorf("valid: %q, %v", detail, err)
	}
	if _, err := h.status(now.Add(2 * time.Hour)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired: %v", err)
	}
	if _, err := h.status(now.Add(-2 * time.Hour)); err == nil || !strings.Contains(err.Error(), "not valid before") {
		t.Errorf("not yet valid: %v", err)
	}
}

func TestHolderRejectsEmptyChain(t *testing.T) {
	var h holder
	if err := h.set(&tls.Certificate{}); err == nil {
		t.Error("expected error")
	}
}

func TestTLSConfig(t *testing.T) {
	certFile, keyFile := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{})
	m, err := NewFileManager(certFile, keyFile, 0, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	cfg := TLSConfig(m)
	if cfg.MinVersion != tls.VersionTLS12 || len(cfg.CipherSuites) == 0 {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if c, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err != nil || c == nil {
		t.Errorf("GetCertificate: %v", err)
	}
}

func TestNewFileManagerFailsOnMissingFiles(t *testing.T) {
	if _, err := NewFileManager("/nonexistent/cert.pem", "/nonexistent/key.pem", 0, zerolog.Nop()); err == nil {
		t.Error("expected error")
	}
}

func TestNew(t *testing.T) {
	cfg := config.Default()
	cfg.Server.TLS.Mode = config.TLSModeNone
	if m, err := New(&cfg, zerolog.Nop()); err != nil || m != nil {
		t.Errorf("mode none: %v, %v", m, err)
	}

	cfg.Server.TLS.Mode = config.TLSModeFiles
	cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile = testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{})
	m, err := New(&cfg, zerolog.Nop())
	if err != nil || m == nil {
		t.Fatalf("mode files: %v", err)
	}
	if _, err := m.Check(context.Background()); err != nil {
		t.Errorf("Check: %v", err)
	}
}

func TestStatusCheck(t *testing.T) {
	var empty FileManager
	empty.now = time.Now
	_, err := StatusCheck(&empty)(context.Background())
	var se *status.Error
	if !errors.As(err, &se) || se.Code != "certificate.missing" || !errors.Is(err, ErrNoCertificate) {
		t.Errorf("without certificate: %v", err)
	}

	now := time.Now()
	for _, c := range []struct {
		notAfter time.Time
		code     string
		param    string
	}{
		{now.Add(time.Hour), "certificate.valid", "until"},
		{now.Add(-time.Minute), "certificate.expired", "at"},
	} {
		cf, kf := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{NotBefore: now.Add(-2 * time.Hour), NotAfter: c.notAfter})
		m, err := NewFileManager(cf, kf, 0, zerolog.Nop())
		if err != nil {
			t.Fatal(err)
		}
		d, err := StatusCheck(m)(context.Background())
		want := c.notAfter.UTC().Format(time.RFC3339)
		switch {
		case c.code == "certificate.valid" && (err != nil || d.Code != c.code || d.Params[c.param] != want):
			t.Errorf("valid: %+v, %v", d, err)
		case c.code == "certificate.expired" && (!errors.As(err, &se) || se.Code != c.code || se.Params[c.param] != want):
			t.Errorf("expired: %v", err)
		}
	}
}
