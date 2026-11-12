// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/vault"
)

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Deps{Stdout: &out, Stderr: &errOut})
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestVersion(t *testing.T) {
	out, _, err := run(t, "version")
	if err != nil || !strings.HasPrefix(out, "dcdash ") {
		t.Fatalf("got %q, %v", out, err)
	}
	out, _, err = run(t, "version", "--json")
	var v map[string]string
	if err != nil || json.Unmarshal([]byte(out), &v) != nil || v["version"] == "" {
		t.Fatalf("json: got %q, %v", out, err)
	}
}

func TestConfigValidate(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	goodConfig := "server:\n  tls: {mode: none}\nsecurity:\n  encryption_key: \"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\"\n"
	if err := os.WriteFile(good, []byte(goodConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("log: {level: loud}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, "--config", good, "config", "validate")
	if err != nil || !strings.Contains(out, "configuration is valid") {
		t.Errorf("good config: %q, %v", out, err)
	}
	if _, _, err = run(t, "-c", bad, "config", "validate"); err == nil || !strings.Contains(err.Error(), "log.level") {
		t.Errorf("bad config: expected log.level error, got %v", err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DCDASH_CONFIG", "/from/env.yaml")
	if p := defaultConfigPath(); p != "/from/env.yaml" {
		t.Errorf("got %q", p)
	}
}

func TestServeFailsOnMissingConfig(t *testing.T) {
	if _, _, err := run(t, "-c", filepath.Join(t.TempDir(), "missing.yaml"), "serve"); err == nil {
		t.Fatal("expected error")
	}
}

func TestGenKey(t *testing.T) {
	out, _, err := run(t, "gen-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.ParseKey(out); err != nil {
		t.Errorf("generated key is invalid: %v", err)
	}
	out2, _, _ := run(t, "gen-key")
	if out == out2 {
		t.Error("gen-key returned the same key twice")
	}
}

func TestHashPassword(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Deps{Stdout: &out, Stderr: &errOut})
	cmd.SetIn(strings.NewReader("correct horse battery staple\n"))
	cmd.SetArgs([]string{"hash-password"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	hash := strings.TrimSpace(out.String())
	if ok, err := auth.VerifyPassword(hash, "correct horse battery staple"); err != nil || !ok {
		t.Errorf("hash does not verify: %q, %v", hash, err)
	}

	cmd = NewRootCommand(Deps{Stdout: &out, Stderr: &errOut})
	cmd.SetIn(strings.NewReader("short\n"))
	cmd.SetArgs([]string{"hash-password"})
	if err := cmd.Execute(); err == nil {
		t.Error("short password accepted")
	}
}
