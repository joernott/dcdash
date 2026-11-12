// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"crypto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-acme/lego/v5/certificate"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/testutil"
)

func testACMEConfig(domains ...string) config.ACME {
	cfg := config.Default().Server.TLS.ACME
	cfg.DirectoryURL = "https://ca.example.com:9000/acme/acme/directory"
	cfg.AcceptTOS = true
	cfg.Domains = domains
	cfg.DNS.Provider = config.DNSProviderCloudflare
	cfg.DNS.Cloudflare.APIToken = config.NewSecret("token")
	return cfg
}

func TestStorageDir(t *testing.T) {
	got, err := storageDir("/var/lib/dcdash", "https://ca.example.com:9000/acme/acme/directory")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/var/lib/dcdash", "acme", "ca.example.com_9000_acme_acme_directory"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if _, err := storageDir("/x", "not a url"); err == nil {
		t.Error("expected error")
	}
}

// resourceFrom builds a certificate.Resource from a test certificate on disk.
func resourceFrom(t *testing.T, dir string, names ...string) *certificate.Resource {
	t.Helper()
	certPath, keyPath := testutil.WriteCert(t, dir, testutil.CertOptions{DNSNames: names, Name: names[0]})
	c, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	k, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return &certificate.Resource{Domains: names, Certificate: c, PrivateKey: k}
}

func TestStoreAndLoadStored(t *testing.T) {
	dataDir := t.TempDir()
	cfg := testACMEConfig("a.example", "b.example")
	m, err := NewACMEManager(cfg, dataDir, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if m.current() != nil {
		t.Fatal("unexpected certificate in empty storage")
	}
	if _, err := m.store(resourceFrom(t, t.TempDir(), "a.example", "b.example")); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{certFile, certKeyFile, certMetaFile} {
		fi, err := os.Stat(filepath.Join(m.dir, f))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != storageFilePerm && os.PathSeparator == '/' {
			t.Errorf("%s has mode %o", f, perm)
		}
	}

	// Same domains in different order: stored certificate is used.
	cfg.Domains = []string{"b.example", "a.example"}
	m2, err := NewACMEManager(cfg, dataDir, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if m2.current() == nil {
		t.Error("stored certificate not loaded")
	}
	if detail, err := m2.Check(t.Context()); err != nil || !strings.HasPrefix(detail, "valid until") {
		t.Errorf("Check: %q, %v", detail, err)
	}

	// Changed domains: stored certificate is ignored.
	cfg.Domains = []string{"a.example", "c.example"}
	m3, err := NewACMEManager(cfg, dataDir, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if m3.current() != nil {
		t.Error("stored certificate used despite changed domains")
	}

	// Changed key type: ignored as well.
	cfg.Domains = []string{"a.example", "b.example"}
	cfg.KeyType = "RSA2048"
	m4, err := NewACMEManager(cfg, dataDir, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if m4.current() != nil {
		t.Error("stored certificate used despite changed key type")
	}
}

func TestStoreRejectsInvalidResource(t *testing.T) {
	m, err := NewACMEManager(testACMEConfig("a.example"), t.TempDir(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store(&certificate.Resource{Certificate: []byte("x"), PrivateKey: []byte("y")}); err == nil {
		t.Error("expected error")
	}
}

func TestAccountKeyIsPersisted(t *testing.T) {
	m, err := NewACMEManager(testACMEConfig("a.example"), t.TempDir(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	u1, err := m.loadAccount()
	if err != nil {
		t.Fatal(err)
	}
	u2, err := m.loadAccount()
	if err != nil {
		t.Fatal(err)
	}
	if u1.reg != nil {
		t.Error("unexpected registration")
	}
	pub1, _ := u1.GetPrivateKey().Public().(interface{ Equal(crypto.PublicKey) bool })
	if pub1 == nil || !pub1.Equal(u2.GetPrivateKey().Public()) {
		t.Error("account key not reused")
	}

	// A stored registration is loaded.
	if err := os.WriteFile(filepath.Join(m.dir, accountFile), []byte(`{"status":"valid","accountURL":"https://ca/acct/1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	u3, err := m.loadAccount()
	if err != nil {
		t.Fatal(err)
	}
	if u3.reg == nil || u3.reg.Location != "https://ca/acct/1" {
		t.Errorf("registration not loaded: %+v", u3.reg)
	}
}

func TestHTTPClientWithCAFile(t *testing.T) {
	cfg := testACMEConfig("a.example")
	m := &ACMEManager{cfg: cfg}
	if hc, err := m.httpClient(); err != nil || hc != nil {
		t.Errorf("without ca_file: %v, %v", hc, err)
	}

	m.cfg.CAFile, _ = testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{})
	if hc, err := m.httpClient(); err != nil || hc == nil {
		t.Errorf("with ca_file: %v", err)
	}

	m.cfg.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := m.httpClient(); err == nil {
		t.Error("expected error for missing ca_file")
	}
}

func TestWriteFileAtomicReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	for _, content := range []string{"one", "two"} {
		if err := writeFileAtomic(p, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Errorf("got %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}
