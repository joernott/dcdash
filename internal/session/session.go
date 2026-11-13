// SPDX-License-Identifier: GPL-3.0-or-later

// Package session manages server-side login sessions. The browser only holds a
// random token in a cookie; the database stores the token's SHA-256 hash, so a
// leaked database does not allow taking over sessions.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// Session stages.
const (
	// StageAuthenticated: the user is fully logged in.
	StageAuthenticated = "authenticated"
	// StageTOTP: the password was verified, the TOTP code is still pending.
	StageTOTP = "totp"
	// StageTOTPEnroll: the password was verified; the user must enroll TOTP first.
	StageTOTPEnroll = "totp_enroll"
)

// Session sources.
const (
	SourceLDAP  = "ldap"
	SourceLocal = "local"
)

// ErrNotFound is returned for unknown, expired or idle sessions.
var ErrNotFound = errors.New("session not found or expired")

// touchInterval limits how often last_seen_at is written.
const touchInterval = time.Minute

// PendingTimeout limits how long a session may wait for the TOTP step (or the
// enrollment) after the password was verified.
const PendingTimeout = 10 * time.Minute

// Session is a login session.
type Session struct {
	// ID identifies the session for administrators; unlike the token, it is not secret.
	ID          string
	Username    string
	DisplayName string
	Source      string
	Groups      []string
	Stage       string
	CSRFToken   string
	CreatedAt   time.Time
	LastSeenAt  time.Time
	ExpiresAt   time.Time
	RemoteAddr  string
	UserAgent   string
	// PendingTOTP is the encrypted TOTP secret during enrollment, or nil.
	PendingTOTP []byte

	tokenHash []byte
}

// Authenticated reports whether the session is fully logged in.
func (s *Session) Authenticated() bool { return s.Stage == StageAuthenticated }

// New describes a session to create.
type New struct {
	Username    string
	DisplayName string
	Source      string
	Groups      []string
	Stage       string
	RemoteAddr  string
	UserAgent   string
}

// Manager creates, loads and expires sessions.
type Manager struct {
	db       *sql.DB
	lifetime time.Duration
	idle     time.Duration
	log      zerolog.Logger
	now      func() time.Time
}

// NewManager creates a session manager.
func NewManager(db *sql.DB, lifetime, idle time.Duration, log zerolog.Logger) *Manager {
	return &Manager{
		db: db, lifetime: lifetime, idle: idle,
		log: log.With().Str("component", "session").Logger(),
		now: time.Now,
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Create stores a new session and returns its token for the cookie.
func (m *Manager) Create(ctx context.Context, n New) (string, *Session, error) {
	token, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	now := m.now().UTC().Truncate(time.Second)
	if n.Groups == nil {
		n.Groups = []string{}
	}
	groups, err := json.Marshal(n.Groups)
	if err != nil {
		return "", nil, err
	}
	id, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	s := &Session{
		ID: id[:22], Username: n.Username, DisplayName: n.DisplayName, Source: n.Source, Groups: n.Groups,
		Stage: n.Stage, CSRFToken: csrf, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(m.lifetime),
		RemoteAddr: n.RemoteAddr, UserAgent: truncate(n.UserAgent, 512), tokenHash: hashToken(token),
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO sessions
		(id, token_hash, username, display_name, source, groups, stage, csrf_token,
		 created_at, last_seen_at, expires_at, remote_addr, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.tokenHash, s.Username, s.DisplayName, s.Source, string(groups), s.Stage, s.CSRFToken,
		now.Unix(), now.Unix(), s.ExpiresAt.Unix(), s.RemoteAddr, s.UserAgent)
	if err != nil {
		return "", nil, fmt.Errorf("create session: %w", err)
	}
	return token, s, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Get returns the session for a token. Expired and idle sessions are deleted
// and reported as ErrNotFound. Activity extends the idle timeout.
func (m *Manager) Get(ctx context.Context, token string) (*Session, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	hash := hashToken(token)
	var (
		s                         Session
		groups                    string
		created, seen, expiresAtS int64
	)
	err := m.db.QueryRowContext(ctx, `SELECT id, username, display_name, source, groups, stage, csrf_token,
		created_at, last_seen_at, expires_at, remote_addr, user_agent, pending_totp
		FROM sessions WHERE token_hash = ?`, hash).Scan(
		&s.ID, &s.Username, &s.DisplayName, &s.Source, &groups, &s.Stage, &s.CSRFToken,
		&created, &seen, &expiresAtS, &s.RemoteAddr, &s.UserAgent, &s.PendingTOTP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	s.tokenHash = hash
	s.CreatedAt, s.LastSeenAt, s.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(seen, 0).UTC(), time.Unix(expiresAtS, 0).UTC()
	if err := json.Unmarshal([]byte(groups), &s.Groups); err != nil {
		return nil, fmt.Errorf("load session groups: %w", err)
	}

	now := m.now().UTC()
	if !now.Before(s.ExpiresAt) || !now.Before(s.LastSeenAt.Add(m.idle)) ||
		(!s.Authenticated() && !now.Before(s.CreatedAt.Add(PendingTimeout))) {
		_ = m.deleteHash(ctx, hash)
		return nil, ErrNotFound
	}
	if now.Sub(s.LastSeenAt) >= touchInterval {
		if _, err := m.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, now.Unix(), hash); err != nil {
			return nil, fmt.Errorf("update session: %w", err)
		}
		s.LastSeenAt = now.Truncate(time.Second)
	}
	return &s, nil
}

// SetStage changes the stage of a session (e.g. after the TOTP step) and
// rotates its token, so that a token observed before the step is useless
// afterwards. It returns the new token.
func (m *Manager) SetStage(ctx context.Context, s *Session, stage string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	newHash := hashToken(token)
	res, err := m.db.ExecContext(ctx, `UPDATE sessions SET token_hash = ?, stage = ? WHERE token_hash = ?`, newHash, stage, s.tokenHash)
	if err != nil {
		return "", fmt.Errorf("update session: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", ErrNotFound
	}
	s.tokenHash, s.Stage = newHash, stage
	return token, nil
}

// Delete ends the session with the given token (logout).
func (m *Manager) Delete(ctx context.Context, token string) error {
	return m.deleteHash(ctx, hashToken(token))
}

func (m *Manager) deleteHash(ctx context.Context, hash []byte) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	return err
}

// DeleteUser ends all sessions of a user (case does not matter) and returns how
// many were ended.
func (m *Manager) DeleteUser(ctx context.Context, username string) (int64, error) {
	res, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE username = ? COLLATE NOCASE`, username)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Cleanup deletes expired, idle and abandoned pending sessions and returns how
// many were deleted.
func (m *Manager) Cleanup(ctx context.Context) (int64, error) {
	now := m.now().UTC().Unix()
	res, err := m.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?
		OR (stage != ? AND created_at <= ?)`,
		now, now-int64(m.idle/time.Second), StageAuthenticated, now-int64(PendingTimeout/time.Second))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Run deletes expired sessions periodically until ctx is canceled.
func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		if n, err := m.Cleanup(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn().Err(err).Msg("session cleanup failed")
		} else if n > 0 {
			m.log.Debug().Int64("deleted", n).Msg("expired sessions removed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// --- cookies -----------------------------------------------------------------

// CookieName is the session cookie name. The __Host- prefix makes browsers
// enforce Secure, Path=/ and no Domain attribute. The cookie is always Secure:
// browsers reach dcdash via HTTPS (directly or through a TLS-terminating proxy)
// or via http://localhost, which browsers treat as a secure context.
const CookieName = "__Host-dcdash_session"

// SetCookie sends the session cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// ClearCookie deletes the session cookie in the browser.
func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

// TokenFromRequest returns the session token from the request cookie.
func (m *Manager) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// --- request context ---------------------------------------------------------

type ctxKey struct{}

// WithSession returns a context carrying the session.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// FromContext returns the session of the request, or nil.
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// Middleware loads the session from the cookie into the request context. It
// never rejects requests; use RequireAuthenticated for protected handlers.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := m.TokenFromRequest(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		s, err := m.Get(r.Context(), token)
		switch {
		case errors.Is(err, ErrNotFound):
			m.ClearCookie(w)
		case err != nil:
			m.log.Error().Err(err).Msg("cannot load session")
		default:
			r = r.WithContext(WithSession(r.Context(), s))
		}
		next.ServeHTTP(w, r)
	})
}

// SetPendingTOTP stores (or with nil, clears) the encrypted TOTP secret of an
// enrollment in progress.
func (m *Manager) SetPendingTOTP(ctx context.Context, s *Session, enc []byte) error {
	res, err := m.db.ExecContext(ctx, `UPDATE sessions SET pending_totp = ? WHERE token_hash = ?`, enc, s.tokenHash)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	s.PendingTOTP = enc
	return nil
}

// List returns the active sessions, optionally only those of one user (case
// does not matter), newest first. Tokens and CSRF tokens are not included.
func (m *Manager) List(ctx context.Context, username string) ([]Session, error) {
	now := m.now().UTC().Unix()
	q := `SELECT id, username, display_name, source, groups, stage, created_at, last_seen_at, expires_at,
		remote_addr, user_agent FROM sessions
		WHERE expires_at > ? AND last_seen_at > ? AND (stage = ? OR created_at > ?)`
	args := []any{now, now - int64(m.idle/time.Second), StageAuthenticated, now - int64(PendingTimeout/time.Second)}
	if username != "" {
		q += ` AND username = ? COLLATE NOCASE`
		args = append(args, username)
	}
	rows, err := m.db.QueryContext(ctx, q+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var (
			s                    Session
			groups               string
			created, seen, until int64
		)
		if err := rows.Scan(&s.ID, &s.Username, &s.DisplayName, &s.Source, &groups, &s.Stage,
			&created, &seen, &until, &s.RemoteAddr, &s.UserAgent); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(groups), &s.Groups)
		s.CreatedAt, s.LastSeenAt, s.ExpiresAt = time.Unix(created, 0).UTC(), time.Unix(seen, 0).UTC(), time.Unix(until, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteByID ends the session with the given ID and returns its user name.
func (m *Manager) DeleteByID(ctx context.Context, id string) (string, error) {
	var username string
	err := m.db.QueryRowContext(ctx, `DELETE FROM sessions WHERE id = ? RETURNING username`, id).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return username, err
}
