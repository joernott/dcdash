// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const minimal = `
server:
  tls:
    cert_file: /etc/dcdash/cert.pem
    key_file: /etc/dcdash/key.pem
`

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + minimal))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Server.Listen != ":8443" {
		t.Errorf("listen = %q, want :8443", cfg.Server.Listen)
	}
	if cfg.Server.ShutdownTimeout.D() != 15*time.Second {
		t.Errorf("shutdown_timeout = %v", cfg.Server.ShutdownTimeout.D())
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "json" {
		t.Errorf("log = %+v", cfg.Log)
	}
}

func TestParseOverrides(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  listen: "127.0.0.1:9000"
  tls: {mode: none}
  shutdown_timeout: 2m
log: {level: debug, format: console}
data_dir: /tmp/dcdash
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9000" || cfg.Server.TLS.Mode != TLSModeNone ||
		cfg.Server.ShutdownTimeout.D() != 2*time.Minute || cfg.Log.Level != "debug" || cfg.DataDir != "/tmp/dcdash" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + "server:\n  listn: \":1\"\n"))
	if err == nil || !strings.Contains(err.Error(), "listn") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestValidateCollectsAllErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  listen: "nonsense"
  tls: {mode: files}
log: {level: loud, format: xml}
data_dir: ""
`))
	if err == nil {
		t.Fatal("expected validation errors")
	}
	for _, want := range []string{"server.listen", "cert_file", "key_file", "log.level", "log.format", "data_dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestInvalidDuration(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + minimal + "  shutdown_timeout: soon\n"))
	if err == nil || !strings.Contains(err.Error(), "invalid duration") {
		t.Fatalf("expected duration error, got %v", err)
	}
}

func TestEmptyFileFailsValidation(t *testing.T) {
	if _, err := Parse(strings.NewReader(testKeyYAML + "")); err == nil {
		t.Fatal("expected error: tls files missing")
	}
}

func TestLoadWarnsOnWorldReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check is not applicable on Windows")
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(testKeyYAML+minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	hasPermissionWarning := func(warnings []string) bool {
		return slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "other users") })
	}
	_, warnings, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !hasPermissionWarning(warnings) {
		t.Errorf("warnings = %v", warnings)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, warnings, _ = Load(p); hasPermissionWarning(warnings) {
		t.Errorf("unexpected permission warning for 0640: %v", warnings)
	}
}

func TestLoadWarnsWithoutLoginMethod(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(testKeyYAML+minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	_, warnings, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "no login method") }) {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error")
	}
}

type secretHolder struct {
	S Secret `yaml:"s"`
}

func decodeSecret(t *testing.T, raw string) (Secret, error) {
	t.Helper()
	var h secretHolder
	err := yaml.Unmarshal([]byte(fmt.Sprintf("s: %q\n", raw)), &h)
	return h.S, err
}

func TestSecretSources(t *testing.T) {
	t.Setenv("DCDASH_TEST_SECRET", "from-env")
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]string{
		"literal":                "literal",
		"env:DCDASH_TEST_SECRET": "from-env",
		"file:" + p:              "from-file",
	} {
		s, err := decodeSecret(t, raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if s.Value() != want {
			t.Errorf("%s: got %q, want %q", raw, s.Value(), want)
		}
	}
}

func TestSecretErrors(t *testing.T) {
	if _, err := decodeSecret(t, "env:DCDASH_TEST_DOES_NOT_EXIST"); err == nil {
		t.Error("expected error for missing env var")
	}
	if _, err := decodeSecret(t, "file:/does/not/exist"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestSecretIsMasked(t *testing.T) {
	s := NewSecret("hunter2")
	if fmt.Sprint(s) == "hunter2" || fmt.Sprintf("%v", s) == "hunter2" {
		t.Error("secret leaked via fmt")
	}
	j, _ := json.Marshal(struct{ S Secret }{s})
	y, _ := yaml.Marshal(secretHolder{s})
	if strings.Contains(string(j), "hunter2") || strings.Contains(string(y), "hunter2") {
		t.Errorf("secret leaked via marshal: %s / %s", j, y)
	}
}

func TestRedirectAndHSTSValidation(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  redirect_listen: "nonsense"
  hsts_max_age: -1h
  tls: {mode: none}
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"redirect_listen \"nonsense\"", "requires TLS", "hsts_max_age"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

// testKeyYAML provides the required encryption key for test configurations.
const testKeyYAML = "security:\n  encryption_key: \"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\"\n"

func TestEncryptionKeyValidation(t *testing.T) {
	_, err := Parse(strings.NewReader("server:\n  tls: {mode: none}\n"))
	if err == nil || !strings.Contains(err.Error(), "security.encryption_key: required") {
		t.Errorf("missing key: %v", err)
	}
	_, err = Parse(strings.NewReader(`
server:
  tls: {mode: none}
security:
  encryption_key: c2hvcnQ=
  previous_encryption_keys: ["not base64!"]
`))
	if err == nil || !strings.Contains(err.Error(), "must be 32 bytes") || !strings.Contains(err.Error(), "previous_encryption_keys[0]") {
		t.Errorf("invalid keys: %v", err)
	}
}

func TestTrustedProxies(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + "server:\n  tls: {mode: none}\n  trusted_proxies: [10.0.0.0/8, 192.0.2.1, \"::1\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := cfg.Server.TrustedProxyPrefixes()
	if len(p) != 3 || p[1].String() != "192.0.2.1/32" || p[2].String() != "::1/128" {
		t.Errorf("prefixes: %v", p)
	}
	_, err = Parse(strings.NewReader(testKeyYAML + "server:\n  tls: {mode: none}\n  trusted_proxies: [proxy.example]\n"))
	if err == nil || !strings.Contains(err.Error(), "trusted_proxies") {
		t.Errorf("invalid proxy: %v", err)
	}
}
