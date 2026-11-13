// SPDX-License-Identifier: GPL-3.0-or-later

package totp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/vault"
)

// Errors.
var (
	ErrNotEnrolled     = errors.New("TOTP is not enrolled")
	ErrAlreadyEnrolled = errors.New("TOTP is already enrolled")
	// ErrInvalidCode covers wrong, expired and already used codes.
	ErrInvalidCode = errors.New("invalid code")
)

// RecoveryCodeCount is the number of recovery codes issued at once.
const RecoveryCodeCount = 10

// Store keeps enrolled secrets (encrypted) and recovery codes (hashed).
type Store struct {
	db  *sql.DB
	kr  *vault.Keyring
	now func() time.Time
}

// NewStore creates a store.
func NewStore(db *sql.DB, kr *vault.Keyring) *Store {
	return &Store{db: db, kr: kr, now: time.Now}
}

// key normalizes a login name; TOTP state is per user, regardless of case.
func key(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

func secretAAD(user string) []byte { return []byte("totp:" + user) }

// PendingAAD binds an enrollment secret stored in a session to its user.
func PendingAAD(username string) []byte { return []byte("totp-pending:" + key(username)) }

// Enrolled reports whether the user has enrolled TOTP.
func (s *Store) Enrolled(ctx context.Context, username string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM totp WHERE username = ?`, key(username)).Scan(&n)
	return n > 0, err
}

// Enroll stores the secret after checking a code generated from it, and
// returns new recovery codes. The code's time step counts as used.
func (s *Store) Enroll(ctx context.Context, username string, secret []byte, code string) ([]string, error) {
	user := key(username)
	step := Match(secret, code, s.now(), 0)
	if step == 0 {
		return nil, ErrInvalidCode
	}
	enc, err := s.kr.Encrypt(secret, secretAAD(user))
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO totp (username, secret, last_step, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (username) DO NOTHING`, user, enc, step, s.now().Unix())
	if err != nil {
		return nil, fmt.Errorf("store TOTP secret: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrAlreadyEnrolled
	}
	codes, err := insertRecoveryCodes(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

// Verify checks a TOTP code. A code is accepted only once.
func (s *Store) Verify(ctx context.Context, username, code string) error {
	user := key(username)
	var (
		enc  []byte
		last int64
	)
	err := s.db.QueryRowContext(ctx, `SELECT secret, last_step FROM totp WHERE username = ?`, user).Scan(&enc, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotEnrolled
	}
	if err != nil {
		return err
	}
	secret, err := s.kr.Decrypt(enc, secretAAD(user))
	if err != nil {
		return fmt.Errorf("decrypt TOTP secret of %s: %w", user, err)
	}
	step := Match(secret, code, s.now(), last)
	if step == 0 {
		return ErrInvalidCode
	}
	// The condition makes concurrent use of the same code fail for all but one request.
	res, err := s.db.ExecContext(ctx, `UPDATE totp SET last_step = ? WHERE username = ? AND last_step < ?`, step, user, step)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInvalidCode
	}
	if !s.kr.IsCurrent(enc) {
		// Key rotation: re-encrypt with the current key on use.
		_ = s.reencrypt(ctx, user, secret)
	}
	return nil
}

func (s *Store) reencrypt(ctx context.Context, user string, secret []byte) error {
	enc, err := s.kr.Encrypt(secret, secretAAD(user))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE totp SET secret = ? WHERE username = ?`, enc, user)
	return err
}

// ReencryptAll re-encrypts all secrets that are not encrypted with the
// current key and returns how many were re-encrypted (key rotation).
func (s *Store) ReencryptAll(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT username, secret FROM totp`)
	if err != nil {
		return 0, err
	}
	type rec struct {
		user string
		enc  []byte
	}
	var todo []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.user, &r.enc); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if !s.kr.IsCurrent(r.enc) {
			todo = append(todo, r)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for i, r := range todo {
		secret, err := s.kr.Decrypt(r.enc, secretAAD(r.user))
		if err != nil {
			return i, fmt.Errorf("decrypt TOTP secret of %s: %w", r.user, err)
		}
		if err := s.reencrypt(ctx, r.user, secret); err != nil {
			return i, err
		}
	}
	return len(todo), nil
}

// Reset removes the user's TOTP secret and recovery codes. It reports
// whether the user was enrolled.
func (s *Store) Reset(ctx context.Context, username string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM totp WHERE username = ?`, key(username))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- recovery codes ------------------------------------------------------------

// recoveryAlphabet avoids characters that are easily confused (0/O, 1/l/I).
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newRecoveryCode returns a code like "k7m2-x9pq-4hvt-c3wn" (16 characters
// from 31 symbols, about 79 bits).
func newRecoveryCode() string {
	var sb strings.Builder
	buf := make([]byte, 1)
	for n := 0; n < 16; {
		_, _ = rand.Read(buf)
		if int(buf[0]) >= 256-256%len(recoveryAlphabet) { // avoid modulo bias
			continue
		}
		if n > 0 && n%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(recoveryAlphabet[int(buf[0])%len(recoveryAlphabet)])
		n++
	}
	return sb.String()
}

// normalizeRecoveryCode ignores case, spaces and dashes.
func normalizeRecoveryCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(code))
}

// hashRecoveryCode binds the hash to the user, so equal codes of different
// users have different hashes.
func hashRecoveryCode(user, code string) []byte {
	h := sha256.Sum256([]byte("dcdash recovery code\x00" + user + "\x00" + normalizeRecoveryCode(code)))
	return h[:]
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertRecoveryCodes(ctx context.Context, db execer, user string) ([]string, error) {
	if _, err := db.ExecContext(ctx, `DELETE FROM recovery_codes WHERE username = ?`, user); err != nil {
		return nil, err
	}
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		codes[i] = newRecoveryCode()
		if _, err := db.ExecContext(ctx, `INSERT INTO recovery_codes (username, code_hash) VALUES (?, ?)`,
			user, hashRecoveryCode(user, codes[i])); err != nil {
			return nil, fmt.Errorf("store recovery code: %w", err)
		}
	}
	return codes, nil
}

// RegenerateRecoveryCodes replaces all recovery codes of an enrolled user.
func (s *Store) RegenerateRecoveryCodes(ctx context.Context, username string) ([]string, error) {
	user := key(username)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM totp WHERE username = ?`, user).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotEnrolled
	}
	codes, err := insertRecoveryCodes(ctx, tx, user)
	if err != nil {
		return nil, err
	}
	return codes, tx.Commit()
}

// UseRecoveryCode accepts an unused recovery code once.
func (s *Store) UseRecoveryCode(ctx context.Context, username, code string) error {
	user := key(username)
	if len(normalizeRecoveryCode(code)) != 16 {
		return ErrInvalidCode
	}
	res, err := s.db.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ?
		WHERE username = ? AND code_hash = ? AND used_at IS NULL`, s.now().Unix(), user, hashRecoveryCode(user, code))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInvalidCode
	}
	return nil
}

// RemainingRecoveryCodes returns the number of unused recovery codes.
func (s *Store) RemainingRecoveryCodes(ctx context.Context, username string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE username = ? AND used_at IS NULL`, key(username)).Scan(&n)
	return n, err
}

// SetClock replaces the clock (for tests).
func (s *Store) SetClock(now func() time.Time) { s.now = now }
