// SPDX-License-Identifier: GPL-3.0-or-later

// Package vault encrypts secrets stored in the database (e.g. TOTP seeds) with
// AES-256-GCM. A key ring holds the current key and previous keys, so that the
// key can be rotated: data encrypted with a previous key stays readable and is
// re-encrypted with the current key when it is written again.
package vault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the size of an encryption key in bytes (AES-256).
const KeySize = 32

const (
	formatV1  = 0x01
	keyIDSize = 4
	nonceSize = 12
	headerLen = 1 + keyIDSize + nonceSize
)

// ErrDecrypt is returned when a ciphertext cannot be decrypted: unknown key,
// wrong associated data or tampered data.
var ErrDecrypt = errors.New("cannot decrypt: unknown key or corrupted data")

type keyID [keyIDSize]byte

type key struct {
	id   keyID
	aead cipher.AEAD
}

// Keyring encrypts with the current key and decrypts with any known key.
type Keyring struct {
	current key
	byID    map[keyID]key
}

// ParseKey decodes a base64-encoded 32-byte key.
func ParseKey(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("encryption key is not valid base64: %w", err)
	}
	if len(b) != KeySize {
		return nil, fmt.Errorf("encryption key must be %d bytes, got %d", KeySize, len(b))
	}
	return b, nil
}

// GenerateKey returns a new random key, base64-encoded.
func GenerateKey() string {
	b := make([]byte, KeySize)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.StdEncoding.EncodeToString(b)
}

func newKey(raw []byte) (key, error) {
	if len(raw) != KeySize {
		return key{}, fmt.Errorf("encryption key must be %d bytes, got %d", KeySize, len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return key{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return key{}, err
	}
	sum := sha256.Sum256(append([]byte("dcdash key id\x00"), raw...))
	var id keyID
	copy(id[:], sum[:keyIDSize])
	return key{id: id, aead: aead}, nil
}

// New creates a key ring with the current key and optional previous keys.
func New(current []byte, previous ...[]byte) (*Keyring, error) {
	cur, err := newKey(current)
	if err != nil {
		return nil, err
	}
	k := &Keyring{current: cur, byID: map[keyID]key{cur.id: cur}}
	for _, p := range previous {
		pk, err := newKey(p)
		if err != nil {
			return nil, fmt.Errorf("previous key: %w", err)
		}
		if _, dup := k.byID[pk.id]; !dup {
			k.byID[pk.id] = pk
		}
	}
	return k, nil
}

// Encrypt encrypts plaintext with the current key. aad (associated data) binds
// the ciphertext to its context, e.g. "totp:<user>"; the same aad must be
// given to Decrypt.
func (k *Keyring) Encrypt(plaintext, aad []byte) ([]byte, error) {
	out := make([]byte, headerLen, headerLen+len(plaintext)+k.current.aead.Overhead())
	out[0] = formatV1
	copy(out[1:], k.current.id[:])
	nonce := out[1+keyIDSize : headerLen]
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.current.aead.Seal(out, nonce, plaintext, aad), nil
}

// Decrypt decrypts a ciphertext created by Encrypt with any key of the ring.
func (k *Keyring) Decrypt(ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < headerLen || ciphertext[0] != formatV1 {
		return nil, ErrDecrypt
	}
	var id keyID
	copy(id[:], ciphertext[1:1+keyIDSize])
	kk, ok := k.byID[id]
	if !ok {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[1+keyIDSize : headerLen]
	pt, err := kk.aead.Open(nil, nonce, ciphertext[headerLen:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// IsCurrent reports whether ciphertext was encrypted with the current key. Data
// encrypted with a previous key should be re-encrypted when it is written.
func (k *Keyring) IsCurrent(ciphertext []byte) bool {
	return len(ciphertext) >= headerLen && bytes.Equal(ciphertext[1:1+keyIDSize], k.current.id[:])
}

// MetaStore is the part of the store used to keep the key check value.
type MetaStore interface {
	GetMeta(ctx context.Context, key string) ([]byte, error)
	SetMeta(ctx context.Context, key string, value []byte) error
}

const (
	keyCheckName  = "vault.key_check"
	keyCheckValue = "dcdash key check"
)

// Verify makes sure the key ring can read the data in the store. On first use
// it stores a check value encrypted with the current key; later it fails if
// that value cannot be decrypted, i.e. the key was changed without keeping the
// old key in previous_encryption_keys. A check value encrypted with a previous
// key is re-encrypted with the current key.
func (k *Keyring) Verify(ctx context.Context, st MetaStore, notFound error) error {
	aad := []byte(keyCheckName)
	ct, err := st.GetMeta(ctx, keyCheckName)
	switch {
	case errors.Is(err, notFound):
		return k.writeKeyCheck(ctx, st)
	case err != nil:
		return fmt.Errorf("read key check: %w", err)
	}
	pt, err := k.Decrypt(ct, aad)
	if err != nil || string(pt) != keyCheckValue {
		return errors.New("security.encryption_key does not match the database; " +
			"if the key was changed, list the old key in security.previous_encryption_keys")
	}
	if !k.IsCurrent(ct) {
		return k.writeKeyCheck(ctx, st)
	}
	return nil
}

func (k *Keyring) writeKeyCheck(ctx context.Context, st MetaStore) error {
	ct, err := k.Encrypt([]byte(keyCheckValue), []byte(keyCheckName))
	if err != nil {
		return err
	}
	if err := st.SetMeta(ctx, keyCheckName, ct); err != nil {
		return fmt.Errorf("store key check: %w", err)
	}
	return nil
}
