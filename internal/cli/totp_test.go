// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/base32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/totp"
)

const (
	keyA = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
	keyB = "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA="
)

// writeConfig writes a configuration with a break-glass account.
func writeConfig(t *testing.T, dir, key string, previous ...string) string {
	t.Helper()
	hash, err := auth.HashPassword("break-glass-password")
	if err != nil {
		t.Fatal(err)
	}
	cfg := "server:\n  tls: {mode: none}\ndata_dir: " + filepath.Join(dir, "data") + "\n" +
		"auth:\n  local:\n    enabled: true\n    username: breakglass\n    password_hash: '" + hash + "'\n" +
		"security:\n  encryption_key: \"" + key + "\"\n"
	if len(previous) > 0 {
		cfg += "  previous_encryption_keys: [\"" + strings.Join(previous, "\", \"") + "\"]\n"
	}
	p := filepath.Join(dir, "config-"+key[:4]+".yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// codeReader answers the code prompt by reading the secret printed to out.
type codeReader struct {
	out  *bytes.Buffer
	done bool
}

var secretLine = regexp.MustCompile(`enter the secret manually: ([A-Z2-7]+)`)

func (c *codeReader) Read(p []byte) (int, error) {
	if c.done {
		return 0, nil
	}
	c.done = true
	m := secretLine.FindStringSubmatch(c.out.String())
	if m == nil {
		return copy(p, "000000\n"), nil
	}
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(m[1])
	return copy(p, totp.Code(secret, totp.Step(time.Now()))+"\n"), nil
}

func runWithInput(t *testing.T, in func(out *bytes.Buffer) io.Reader, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := NewRootCommand(Deps{Stdout: &out, Stderr: &errOut})
	if in != nil {
		cmd.SetIn(in(&out))
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestTOTPEnrollAndReencrypt(t *testing.T) {
	dir := t.TempDir()
	cfgA := writeConfig(t, dir, keyA)
	answer := func(out *bytes.Buffer) io.Reader { return &codeReader{out: out} }

	if _, err := runWithInput(t, answer, "-c", cfgA, "totp", "enroll", "alice"); err == nil || !strings.Contains(err.Error(), "not the break-glass account") {
		t.Errorf("directory user: %v", err)
	}
	out, err := runWithInput(t, answer, "-c", cfgA, "totp", "enroll", "BreakGlass")
	if err != nil {
		t.Fatalf("enroll: %v\n%s", err, out)
	}
	if !strings.Contains(out, "otpauth://totp/dcdash:breakglass?") || strings.Count(out, "\n  ") < totp.RecoveryCodeCount+1 {
		t.Errorf("output:\n%s", out)
	}
	if _, err := runWithInput(t, answer, "-c", cfgA, "totp", "enroll", "breakglass"); err == nil || !strings.Contains(err.Error(), "already enrolled") {
		t.Errorf("second enrollment: %v", err)
	}

	// Key rotation: B is the new key, A a previous one.
	cfgB := writeConfig(t, dir, keyB, keyA)
	out, err = runWithInput(t, nil, "-c", cfgB, "reencrypt")
	if err != nil || !strings.Contains(out, "1 TOTP secret(s) re-encrypted") {
		t.Fatalf("reencrypt: %q, %v", out, err)
	}
	if out, _ := runWithInput(t, nil, "-c", cfgB, "reencrypt"); !strings.Contains(out, "0 TOTP secret(s)") {
		t.Errorf("second reencrypt: %q", out)
	}

	// Without the old key, the secret is still readable.
	s, err := openState(context.Background(), writeConfig(t, dir, keyB))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, err := s.totp.ReencryptAll(context.Background()); err != nil || n != 0 {
		t.Errorf("secrets left with the old key: %d, %v", n, err)
	}
	entries, _ := s.audit.Query(context.Background(), audit.Filter{})
	actions := map[string]int{}
	for _, e := range entries {
		actions[e.Action+"/"+e.Result]++
		if e.ActorSource != audit.SourceCLI || e.Actor == "" {
			t.Errorf("CLI audit entry: %+v", e)
		}
	}
	if actions["auth.totp.enroll/success"] != 1 || actions["security.reencrypt/success"] != 2 {
		t.Errorf("audit: %v", actions)
	}

	if out, err := runWithInput(t, nil, "-c", cfgB, "totp", "reset", "breakglass"); err != nil || !strings.Contains(out, "reset") {
		t.Errorf("reset: %q, %v", out, err)
	}
	if _, err := runWithInput(t, nil, "-c", cfgB, "totp", "reset", "breakglass"); err == nil || !strings.Contains(err.Error(), "not enrolled") {
		t.Errorf("second reset: %v", err)
	}
}
