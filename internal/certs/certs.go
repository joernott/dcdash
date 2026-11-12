// SPDX-License-Identifier: GPL-3.0-or-later

// Package certs provides the server certificate from PEM files or via ACME and
// keeps it current without restarting the server (hot reload through
// tls.Config.GetCertificate).
package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
)

// Manager provides the server certificate and keeps it up to date.
type Manager interface {
	// GetCertificate returns the current certificate; it is used as tls.Config.GetCertificate.
	GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// Run keeps the certificate current (file reload, ACME renewal) until ctx is canceled.
	Run(ctx context.Context) error
	// Check reports whether a valid certificate is available, for /readyz.
	Check(ctx context.Context) (string, error)
}

// ErrNoCertificate is returned while no certificate has been loaded or obtained yet.
var ErrNoCertificate = errors.New("no certificate available")

// New creates the manager for the configured TLS mode. It returns nil for mode
// "none" (plain HTTP).
func New(cfg *config.Config, log zerolog.Logger) (Manager, error) {
	t := cfg.Server.TLS
	switch t.Mode {
	case config.TLSModeNone:
		return nil, nil
	case config.TLSModeFiles:
		return NewFileManager(t.CertFile, t.KeyFile, 0, log)
	default:
		return nil, fmt.Errorf("unsupported TLS mode %q", t.Mode)
	}
}

// TLSConfig returns the server TLS configuration: TLS 1.2 or newer, only AEAD
// cipher suites with forward secrecy for TLS 1.2, certificates from m.
func TLSConfig(m Manager) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{ // TLS 1.2 only; TLS 1.3 suites are not configurable
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		GetCertificate: m.GetCertificate,
	}
}

// holder stores the current certificate for lock-free concurrent access.
type holder struct {
	cert atomic.Pointer[tls.Certificate]
}

// set replaces the current certificate. The leaf is parsed if necessary.
func (h *holder) set(c *tls.Certificate) error {
	if c.Leaf == nil {
		if len(c.Certificate) == 0 {
			return errors.New("certificate chain is empty")
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse certificate: %w", err)
		}
		c.Leaf = leaf
	}
	h.cert.Store(c)
	return nil
}

func (h *holder) current() *tls.Certificate { return h.cert.Load() }

// GetCertificate implements Manager.GetCertificate.
func (h *holder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := h.current(); c != nil {
		return c, nil
	}
	return nil, ErrNoCertificate
}

// status reports the validity of the current certificate at time now.
func (h *holder) status(now time.Time) (string, error) {
	c := h.current()
	if c == nil {
		return "", ErrNoCertificate
	}
	if now.After(c.Leaf.NotAfter) {
		return "", fmt.Errorf("certificate expired at %s", c.Leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	if now.Before(c.Leaf.NotBefore) {
		return "", fmt.Errorf("certificate not valid before %s", c.Leaf.NotBefore.UTC().Format(time.RFC3339))
	}
	return "valid until " + c.Leaf.NotAfter.UTC().Format(time.RFC3339), nil
}
