// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/joernott/dcdash/internal/ldapauth"
)

// Argon2id parameters (RFC 9106, second recommended option: 64 MiB, t=3, p=4).
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns an argon2id hash in PHC string format:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type argonHash struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

// parseHash parses a PHC argon2id string and rejects unreasonable parameters.
func parseHash(encoded string) (*argonHash, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, errors.New("not an argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, errors.New("unsupported argon2 version")
	}
	h := &argonHash{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.memory, &h.time, &h.threads); err != nil {
		return nil, errors.New("invalid argon2 parameters")
	}
	if h.memory < 8*1024 || h.memory > 1024*1024 || h.time < 1 || h.time > 20 || h.threads < 1 {
		return nil, errors.New("argon2 parameters out of range")
	}
	var err error
	if h.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(h.salt) < 8 {
		return nil, errors.New("invalid argon2 salt")
	}
	if h.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(h.key) < 16 {
		return nil, errors.New("invalid argon2 hash")
	}
	return h, nil
}

// ValidateHash checks that encoded is a usable argon2id hash.
func ValidateHash(encoded string) error {
	_, err := parseHash(encoded)
	return err
}

// VerifyPassword compares password with an argon2id hash in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	h, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), h.salt, h.time, h.memory, h.threads, uint32(len(h.key))) // #nosec G115 -- key length checked by parseHash
	return subtle.ConstantTimeCompare(key, h.key) == 1, nil
}

// LocalAccount is the break-glass account: a single local user that can log in
// when the directory is unavailable. It always has to complete the TOTP step
// and holds the root role.
type LocalAccount struct {
	username string
	hash     string
}

// NewLocalAccount creates the account; the hash is validated.
func NewLocalAccount(username, hash string) (*LocalAccount, error) {
	if strings.TrimSpace(username) == "" {
		return nil, errors.New("local account: username required")
	}
	if err := ValidateHash(hash); err != nil {
		return nil, fmt.Errorf("local account: password_hash: %w", err)
	}
	return &LocalAccount{username: strings.TrimSpace(username), hash: hash}, nil
}

// Matches reports whether a login name refers to the local account. The name
// is reserved: it is never looked up in the directory.
func (a *LocalAccount) Matches(username string) bool {
	return a != nil && strings.EqualFold(strings.TrimSpace(username), a.username)
}

// Authenticate verifies the password of the local account.
func (a *LocalAccount) Authenticate(_ context.Context, _ string, password string) (*ldapauth.User, error) {
	if password == "" {
		return nil, ldapauth.ErrInvalidCredentials
	}
	ok, err := VerifyPassword(a.hash, password)
	if err != nil {
		return nil, fmt.Errorf("local account: %w", err)
	}
	if !ok {
		return nil, ldapauth.ErrInvalidCredentials
	}
	return &ldapauth.User{Username: a.username, DisplayName: "Break-glass account"}, nil
}
