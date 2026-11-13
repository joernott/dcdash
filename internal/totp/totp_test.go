// SPDX-License-Identifier: GPL-3.0-or-later

package totp

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/vault"
)

// RFC 6238 appendix B test vectors (SHA-1), last 6 of the 8 digits.
func TestCodeRFC6238(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want8 := range map[int64]string{
		59:          "94287082",
		1111111109:  "07081804",
		1111111111:  "14050471",
		1234567890:  "89005924",
		2000000000:  "69279037",
		20000000000: "65353130",
	} {
		if got := Code(secret, Step(time.Unix(unix, 0))); got != want8[2:] {
			t.Errorf("t=%d: %s, want %s", unix, got, want8[2:])
		}
	}
}

func TestMatchSkewAndReplay(t *testing.T) {
	secret := NewSecret()
	now := time.Unix(1_800_000_000, 0)
	cur := Step(now)
	for offset, ok := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		got := Match(secret, Code(secret, cur+offset), now, 0)
		if (got != 0) != ok || (ok && got != cur+offset) {
			t.Errorf("offset %d: step %d", offset, got)
		}
	}
	if Match(secret, Code(secret, cur), now, cur) != 0 {
		t.Error("code of an already used step accepted")
	}
	if Match(secret, Code(secret, cur+1), now, cur) != cur+1 {
		t.Error("code of a later step rejected")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if Match(secret, bad, now, 0) != 0 {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestURI(t *testing.T) {
	u, err := url.Parse(URI("dcdash test", "alice@example.com", []byte("12345678901234567890")))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/dcdash test:alice@example.com" ||
		q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "dcdash test" ||
		q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("URI: %s", u)
	}
}

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kr, err := vault.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(st.DB(), kr)
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestEnrollVerifyReset(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	secret := NewSecret()

	if _, err := s.Enroll(ctx, "Alice", secret, Code(secret, Step(*now)+10)); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("enroll with wrong code: %v", err)
	}
	codes, err := s.Enroll(ctx, "Alice", secret, Code(secret, Step(*now)))
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Errorf("%d recovery codes", len(codes))
	}
	if ok, _ := s.Enrolled(ctx, "alice"); !ok {
		t.Error("not enrolled (user names must ignore case)")
	}
	other := NewSecret()
	if _, err := s.Enroll(ctx, "alice", other, Code(other, Step(*now))); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Errorf("second enrollment with valid code: %v", err)
	}

	// The enrollment code's step counts as used.
	if err := s.Verify(ctx, "alice", Code(secret, Step(*now))); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("enrollment code reused: %v", err)
	}
	*now = now.Add(Period)
	if err := s.Verify(ctx, "ALICE", Code(secret, Step(*now))); err != nil {
		t.Errorf("valid code: %v", err)
	}
	if err := s.Verify(ctx, "alice", Code(secret, Step(*now))); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("replayed code: %v", err)
	}
	if err := s.Verify(ctx, "bob", "123456"); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("not enrolled: %v", err)
	}

	if ok, err := s.Reset(ctx, "alice"); !ok || err != nil {
		t.Errorf("reset: %v %v", ok, err)
	}
	if ok, _ := s.Enrolled(ctx, "alice"); ok {
		t.Error("still enrolled after reset")
	}
	if n, _ := s.RemainingRecoveryCodes(ctx, "alice"); n != 0 {
		t.Errorf("recovery codes survived the reset: %d", n)
	}
	if ok, _ := s.Reset(ctx, "alice"); ok {
		t.Error("second reset reported enrolled")
	}
}

func TestRecoveryCodes(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	secret := NewSecret()
	codes, err := s.Enroll(ctx, "alice", secret, Code(secret, Step(*now)))
	if err != nil {
		t.Fatal(err)
	}
	format := regexp.MustCompile(`^[a-z2-9]{4}(-[a-z2-9]{4}){3}$`)
	seen := map[string]bool{}
	for _, c := range codes {
		if !format.MatchString(c) || strings.ContainsAny(c, "01lio") || seen[c] {
			t.Errorf("bad or duplicate code %q", c)
		}
		seen[c] = true
	}
	// Case, spaces and dashes do not matter; each code works once.
	if err := s.UseRecoveryCode(ctx, "alice", " "+strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))); err != nil {
		t.Errorf("recovery code: %v", err)
	}
	if err := s.UseRecoveryCode(ctx, "alice", codes[0]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("reused recovery code: %v", err)
	}
	if err := s.UseRecoveryCode(ctx, "bob", codes[1]); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("code of another user: %v", err)
	}
	if err := s.UseRecoveryCode(ctx, "alice", "short"); !errors.Is(err, ErrInvalidCode) {
		t.Errorf("malformed code: %v", err)
	}
	if n, _ := s.RemainingRecoveryCodes(ctx, "alice"); n != RecoveryCodeCount-1 {
		t.Errorf("remaining: %d", n)
	}

	fresh, err := s.RegenerateRecoveryCodes(ctx, "alice")
	if err != nil || len(fresh) != RecoveryCodeCount {
		t.Fatalf("regenerate: %v", err)
	}
	if err := s.UseRecoveryCode(ctx, "alice", codes[1]); !errors.Is(err, ErrInvalidCode) {
		t.Error("old code still valid after regeneration")
	}
	if n, _ := s.RemainingRecoveryCodes(ctx, "alice"); n != RecoveryCodeCount {
		t.Errorf("remaining after regeneration: %d", n)
	}
	if _, err := s.RegenerateRecoveryCodes(ctx, "bob"); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("regenerate for unenrolled user: %v", err)
	}
}

func TestSecretIsEncryptedAndRotated(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	secret := NewSecret()
	if _, err := s.Enroll(ctx, "alice", secret, Code(secret, Step(*now))); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := s.db.QueryRowContext(ctx, `SELECT secret FROM totp`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), string(secret)) {
		t.Fatal("secret stored in plain text")
	}

	// A secret moved to another user cannot be decrypted (associated data).
	if _, err := s.db.ExecContext(ctx, `INSERT INTO totp (username, secret, last_step, created_at) VALUES ('mallory', ?, 0, 0)`, stored); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(ctx, "mallory", Code(secret, Step(*now)+1)); err == nil || errors.Is(err, ErrInvalidCode) {
		t.Errorf("moved secret: %v", err)
	}
	_, _ = s.Reset(ctx, "mallory")

	// Key rotation: the old key becomes a previous key.
	newKey := []byte("fedcba9876543210fedcba9876543210")
	kr, err := vault.New(newKey, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	s.kr = kr
	if n, err := s.ReencryptAll(ctx); err != nil || n != 1 {
		t.Fatalf("reencrypt: %d %v", n, err)
	}
	if n, _ := s.ReencryptAll(ctx); n != 0 {
		t.Error("second run re-encrypted again")
	}
	onlyNew, _ := vault.New(newKey)
	s.kr = onlyNew
	*now = now.Add(Period)
	if err := s.Verify(ctx, "alice", Code(secret, Step(*now))); err != nil {
		t.Errorf("after rotation without old key: %v", err)
	}
}
