// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(good, []byte("server:\n  tls: {mode: none}\n"), 0o600); err != nil {
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
