// SPDX-License-Identifier: GPL-3.0-or-later

package vault

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := ParseKey(GenerateKey())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKey(t *testing.T) {
	if _, err := ParseKey("not base64!"); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, err := ParseKey("c2hvcnQ="); err == nil {
		t.Error("expected error for short key")
	}
	if k, err := ParseKey(" " + GenerateKey() + "\n"); err != nil || len(k) != KeySize {
		t.Errorf("valid key: %v", err)
	}
}

func TestEncryptDecrypt(t *testing.T) {
	kr, err := New(mustKey(t))
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("JBSWY3DPEHPK3PXP")
	ct, err := kr.Encrypt(pt, []byte("totp:alice"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, pt) {
		t.Error("ciphertext contains plaintext")
	}
	ct2, _ := kr.Encrypt(pt, []byte("totp:alice"))
	if bytes.Equal(ct, ct2) {
		t.Error("nonce reused: identical ciphertexts")
	}
	got, err := kr.Decrypt(ct, []byte("totp:alice"))
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("Decrypt: %q, %v", got, err)
	}

	// Wrong associated data, tampering and garbage are rejected.
	if _, err := kr.Decrypt(ct, []byte("totp:bob")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong aad: %v", err)
	}
	bad := bytes.Clone(ct)
	bad[len(bad)-1] ^= 1
	if _, err := kr.Decrypt(bad, []byte("totp:alice")); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered: %v", err)
	}
	for _, junk := range [][]byte{nil, {0x01}, append([]byte{0x02}, ct[1:]...)} {
		if _, err := kr.Decrypt(junk, nil); !errors.Is(err, ErrDecrypt) {
			t.Errorf("junk %x: %v", junk, err)
		}
	}
}

func TestRotation(t *testing.T) {
	oldKey, newKey := mustKey(t), mustKey(t)
	oldRing, _ := New(oldKey)
	ct, _ := oldRing.Encrypt([]byte("secret"), nil)

	// New key without the old one: old data is unreadable.
	onlyNew, _ := New(newKey)
	if _, err := onlyNew.Decrypt(ct, nil); !errors.Is(err, ErrDecrypt) {
		t.Errorf("decrypt without old key: %v", err)
	}

	// New key with the old one as previous key: readable, but not current.
	rotated, err := New(newKey, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.Decrypt(ct, nil); err != nil || string(got) != "secret" {
		t.Errorf("decrypt with previous key: %q, %v", got, err)
	}
	if rotated.IsCurrent(ct) {
		t.Error("old ciphertext reported as current")
	}
	ct2, _ := rotated.Encrypt([]byte("secret"), nil)
	if !rotated.IsCurrent(ct2) {
		t.Error("new ciphertext not reported as current")
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Error("expected error for short current key")
	}
	if _, err := New(mustKey(t), []byte("short")); err == nil {
		t.Error("expected error for short previous key")
	}
}

var errNotFound = errors.New("not found")

type memMeta map[string][]byte

func (m memMeta) GetMeta(_ context.Context, k string) ([]byte, error) {
	v, ok := m[k]
	if !ok {
		return nil, errNotFound
	}
	return v, nil
}

func (m memMeta) SetMeta(_ context.Context, k string, v []byte) error {
	m[k] = v
	return nil
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	st := memMeta{}
	oldKey, newKey := mustKey(t), mustKey(t)

	oldRing, _ := New(oldKey)
	if err := oldRing.Verify(ctx, st, errNotFound); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := oldRing.Verify(ctx, st, errNotFound); err != nil {
		t.Fatalf("same key: %v", err)
	}

	// Changed key without the old one: refused.
	onlyNew, _ := New(newKey)
	if err := onlyNew.Verify(ctx, st, errNotFound); err == nil {
		t.Fatal("expected error for changed key")
	}

	// Rotation: accepted and the check value is re-encrypted with the new key ...
	rotated, _ := New(newKey, oldKey)
	if err := rotated.Verify(ctx, st, errNotFound); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	if !rotated.IsCurrent(st[keyCheckName]) {
		t.Error("key check not re-encrypted with current key")
	}
	// ... so afterwards the old key can be removed for the check value.
	if err := onlyNew.Verify(ctx, st, errNotFound); err != nil {
		t.Errorf("after rotation: %v", err)
	}
}
