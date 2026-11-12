// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

// Package integration contains tests against real services started by
// test/integration/compose.yaml. Run them with scripts/integration-test.sh.
package integration

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/certs"
	"github.com/joernott/dcdash/internal/config"
)

// tsigSecret matches the "dcdash-acme" key in bind/named.conf (test only).
const tsigSecret = "ewXfZjSD1V7tg9Zfb9LhRq8EZrSbZ6LGrTinSG/wS8o="

func requireEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s not set; run scripts/integration-test.sh", name)
	}
	return v
}

func acmeConfig(t *testing.T, directory, caFile string, domains ...string) config.ACME {
	dns := requireEnv(t, "DCDASH_IT_DNS")
	cfg := config.Default().Server.TLS.ACME
	cfg.DirectoryURL = directory
	cfg.CAFile = caFile
	cfg.AcceptTOS = true
	cfg.Domains = domains
	cfg.DNS.Provider = config.DNSProviderRFC2136
	cfg.DNS.RFC2136 = config.RFC2136{
		Nameserver:    dns,
		TSIGKey:       "dcdash-acme",
		TSIGSecret:    config.NewSecret(tsigSecret),
		TSIGAlgorithm: "hmac-sha256",
	}
	cfg.DNS.Resolvers = []string{dns}
	cfg.DNS.PropagationTimeout = config.Duration(time.Minute)
	// The NS name ns.dcdash.test only exists in the test zone, not in the
	// system resolver, so lego cannot query the authoritative server by name.
	cfg.DNS.DisableAuthoritativeCheck = true

	full := config.Default()
	full.Server.TLS.Mode = config.TLSModeACME
	full.Server.TLS.ACME = cfg
	if err := full.Validate(); err != nil {
		t.Fatalf("invalid test configuration: %v", err)
	}
	return cfg
}

// obtainAndVerify runs the ACME manager until a certificate is available, then
// checks the certificate, its reuse after a restart and that it is served via TLS.
func obtainAndVerify(t *testing.T, cfg config.ACME) {
	log := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.InfoLevel)
	dataDir := t.TempDir()

	m, err := certs.NewACMEManager(cfg, dataDir, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	for {
		if _, err := m.Check(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no certificate obtained within timeout")
		case <-time.After(500 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}

	c, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.Domains {
		if !slices.Contains(c.Leaf.DNSNames, d) {
			t.Errorf("certificate names %v lack %s", c.Leaf.DNSNames, d)
		}
	}
	t.Logf("obtained certificate for %v from %s, valid until %s", c.Leaf.DNSNames, c.Leaf.Issuer, c.Leaf.NotAfter)

	// After a restart the stored certificate is used without contacting the CA.
	m2, err := certs.NewACMEManager(cfg, dataDir, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Check(context.Background()); err != nil {
		t.Errorf("stored certificate not reused: %v", err)
	}

	// The certificate is served over TLS.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", certs.TLSConfig(m2))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", ln.Addr().String(), &tls.Config{
		ServerName:         cfg.Domains[0],
		InsecureSkipVerify: true, // #nosec G402 -- only the served leaf is compared, not the chain
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer conn.Close()
	if served := conn.ConnectionState().PeerCertificates[0]; !served.Equal(c.Leaf) {
		t.Error("served certificate differs from obtained certificate")
	}
}

func TestACMEWithPebble(t *testing.T) {
	cfg := acmeConfig(t,
		requireEnv(t, "DCDASH_IT_PEBBLE_DIRECTORY"),
		requireEnv(t, "DCDASH_IT_PEBBLE_CA"),
		"test1.dcdash.test", "*.test1.dcdash.test",
	)
	obtainAndVerify(t, cfg)
}

func TestACMEWithStepCA(t *testing.T) {
	cfg := acmeConfig(t,
		requireEnv(t, "DCDASH_IT_STEPCA_DIRECTORY"),
		requireEnv(t, "DCDASH_IT_STEPCA_CA"),
		"test2.dcdash.test",
	)
	obtainAndVerify(t, cfg)
}
