// SPDX-License-Identifier: GPL-3.0-or-later

package tlsutil

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/testutil"
)

// serveTLS starts a TLS server with a self-signed certificate for "localhost"
// and returns its address and the certificate's DER bytes and PEM file.
func serveTLS(t *testing.T) (addr string, der []byte, certFile string) {
	t.Helper()
	certFile, keyFile := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{DNSNames: []string{"localhost"}})
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	return ln.Addr().String(), cert.Certificate[0], certFile
}

func dial(addr string, cfg *tls.Config) error {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		return err
	}
	return conn.Close()
}

func TestSystemRootsRejectSelfSigned(t *testing.T) {
	addr, _, _ := serveTLS(t)
	cfg, err := ClientConfig(config.TLSClient{}, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := dial(addr, cfg); err == nil {
		t.Error("self-signed certificate accepted without ca_file or fingerprint")
	}
}

func TestCAFile(t *testing.T) {
	addr, _, certFile := serveTLS(t)
	cfg, err := ClientConfig(config.TLSClient{CAFile: certFile}, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := dial(addr, cfg); err != nil {
		t.Errorf("trusted via ca_file: %v", err)
	}

	// Host name is still verified.
	cfg, _ = ClientConfig(config.TLSClient{CAFile: certFile}, "other.example")
	if err := dial(addr, cfg); err == nil {
		t.Error("wrong host name accepted")
	}
	// server_name override.
	cfg, _ = ClientConfig(config.TLSClient{CAFile: certFile, ServerName: "localhost"}, "other.example")
	if err := dial(addr, cfg); err != nil {
		t.Errorf("server_name override: %v", err)
	}
}

func TestFingerprintPinning(t *testing.T) {
	addr, der, _ := serveTLS(t)
	fp := Fingerprint(der)

	for _, variant := range []string{fp, strings.ToLower(strings.ReplaceAll(fp, ":", ""))} {
		cfg, err := ClientConfig(config.TLSClient{Fingerprint: variant}, "any-name.example")
		if err != nil {
			t.Fatal(err)
		}
		if err := dial(addr, cfg); err != nil {
			t.Errorf("pinned %s: %v", variant, err)
		}
	}

	wrong := "00" + strings.ReplaceAll(fp, ":", "")[2:]
	cfg, err := ClientConfig(config.TLSClient{Fingerprint: wrong}, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := dial(addr, cfg); err == nil || !strings.Contains(err.Error(), "does not match the pinned fingerprint") {
		t.Errorf("wrong fingerprint: %v", err)
	}
}

func TestClientConfigErrors(t *testing.T) {
	if _, err := ClientConfig(config.TLSClient{CAFile: filepath.Join(t.TempDir(), "missing.pem")}, "x"); err == nil {
		t.Error("missing ca_file accepted")
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	_ = os.WriteFile(empty, []byte("no certificates here"), 0o600)
	if _, err := ClientConfig(config.TLSClient{CAFile: empty}, "x"); err == nil {
		t.Error("ca_file without certificates accepted")
	}
	if _, err := ClientConfig(config.TLSClient{Fingerprint: "abc"}, "x"); err == nil {
		t.Error("short fingerprint accepted")
	}
}

func TestFingerprintFormat(t *testing.T) {
	fp := Fingerprint([]byte("x"))
	if len(fp) != 95 || strings.ToUpper(fp) != fp || strings.Count(fp, ":") != 31 {
		t.Errorf("unexpected format %q", fp)
	}
}
