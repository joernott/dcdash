// SPDX-License-Identifier: GPL-3.0-or-later

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestClientCertificate(t *testing.T) {
	dir := t.TempDir()
	clientCert, clientKey := testutil.WriteCert(t, dir, testutil.CertOptions{Name: "client"})
	pemBytes, err := os.ReadFile(clientCert)
	if err != nil {
		t.Fatal(err)
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AppendCertsFromPEM(pemBytes)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs}
	srv.StartTLS()
	defer srv.Close()
	fp := Fingerprint(srv.Certificate().Raw)

	get := func(c config.TLSClient) error {
		cfg, err := ClientConfig(c, "localhost")
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(srv.URL)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	if err := get(config.TLSClient{Fingerprint: fp}); err == nil {
		t.Error("request without client certificate accepted")
	}
	if err := get(config.TLSClient{Fingerprint: fp, CertFile: clientCert, KeyFile: clientKey}); err != nil {
		t.Errorf("request with client certificate: %v", err)
	}
	if _, err := ClientConfig(config.TLSClient{CertFile: clientCert, KeyFile: filepath.Join(dir, "missing.pem")}, "x"); err == nil {
		t.Error("missing key_file accepted")
	}
}
