// SPDX-License-Identifier: GPL-3.0-or-later

// Package prefs stores the preferences of web interface users, such as
// language and theme, so that they follow the user across browsers.
package prefs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/session"
)

// Supported values. An empty language means: use the browser language.
var (
	Languages = []string{"", "en", "de"}
	Themes    = []string{"system", "light", "dark"}
)

// Preferences of a user.
type Preferences struct {
	Language string `json:"language"`
	Theme    string `json:"theme"`
}

// Default returns the preferences of a user who has not saved any.
func Default() Preferences { return Preferences{Language: "", Theme: "system"} }

// Validate checks the values.
func (p Preferences) Validate() error {
	var errs []error
	if !slices.Contains(Languages, p.Language) {
		errs = append(errs, fmt.Errorf("language %q: must be empty (browser language), en or de", p.Language))
	}
	if !slices.Contains(Themes, p.Theme) {
		errs = append(errs, fmt.Errorf("theme %q: must be system, light or dark", p.Theme))
	}
	return errors.Join(errs...)
}

// Store reads and writes preferences.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore creates a store.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

func key(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

// Get returns the user's preferences, or the defaults.
func (s *Store) Get(ctx context.Context, username string) (Preferences, error) {
	p := Default()
	err := s.db.QueryRowContext(ctx, `SELECT language, theme FROM user_preferences WHERE username = ?`, key(username)).
		Scan(&p.Language, &p.Theme)
	if errors.Is(err, sql.ErrNoRows) {
		return Default(), nil
	}
	return p, err
}

// Set validates and stores the user's preferences.
func (s *Store) Set(ctx context.Context, username string, p Preferences) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_preferences (username, language, theme, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (username) DO UPDATE SET language = excluded.language, theme = excluded.theme,
			updated_at = excluded.updated_at`,
		key(username), p.Language, p.Theme, s.now().Unix())
	return err
}

// Register adds GET and PUT /api/v1/me/preferences. guard must reject
// requests without a fully authenticated session.
func (s *Store) Register(mux *http.ServeMux, auditLog *audit.Log, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/me/preferences", guard(http.HandlerFunc(s.handleGet)))
	mux.Handle("PUT /api/v1/me/preferences", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handlePut(w, r, auditLog)
	})))
}

func (s *Store) handleGet(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	p, err := s.Get(r.Context(), sess.Username)
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot load preferences")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Store) handlePut(w http.ResponseWriter, r *http.Request, auditLog *audit.Log) {
	var p Preferences
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := p.Validate(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sess := session.FromContext(r.Context())
	if err := s.Set(r.Context(), sess.Username, p); err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot save preferences")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	auditLog.RecordRequest(r, "user.preferences.update", sess.Username, audit.ResultSuccess,
		map[string]any{"language": p.Language, "theme": p.Theme})
	httpx.WriteJSON(w, http.StatusOK, p)
}
