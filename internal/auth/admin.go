// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
)

// SessionSummary describes a session for administrators. It contains no tokens.
type SessionSummary struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Source      string    `json:"source"`
	Stage       string    `json:"stage"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	RemoteAddr  string    `json:"remoteAddr"`
	UserAgent   string    `json:"userAgent"`
	// Current marks the session of the request.
	Current bool `json:"current"`
}

// SessionList is the response of GET /api/v1/admin/sessions.
type SessionList struct {
	Sessions []SessionSummary `json:"sessions"`
}

// TerminatedSessions is the response when the sessions of a user are ended.
type TerminatedSessions struct {
	Terminated int64 `json:"terminated"`
}

func (s *Service) registerAdmin(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/admin/sessions", Require(rbac.PermSessionsRead, http.HandlerFunc(s.handleListSessions)))
	mux.Handle("DELETE /api/v1/admin/sessions/{id}", Require(rbac.PermSessionsWrite, http.HandlerFunc(s.handleTerminateSession)))
	mux.Handle("DELETE /api/v1/admin/users/{username}/sessions", Require(rbac.PermSessionsWrite, http.HandlerFunc(s.handleTerminateUserSessions)))
	mux.Handle("DELETE /api/v1/admin/users/{username}/totp", RequireRoot(http.HandlerFunc(s.handleResetTOTP)))
}

func internalError(w http.ResponseWriter, r *http.Request, err error, msg string) {
	hlog.FromRequest(r).Error().Err(err).Msg(msg)
	httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func (s *Service) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.sessions.List(r.Context(), strings.TrimSpace(r.URL.Query().Get("username")))
	if err != nil {
		internalError(w, r, err, "cannot list sessions")
		return
	}
	current := session.FromContext(r.Context())
	out := SessionList{Sessions: make([]SessionSummary, 0, len(list))}
	for _, l := range list {
		out.Sessions = append(out.Sessions, SessionSummary{
			ID: l.ID, Username: l.Username, DisplayName: l.DisplayName, Source: l.Source, Stage: l.Stage,
			CreatedAt: l.CreatedAt, LastSeenAt: l.LastSeenAt, ExpiresAt: l.ExpiresAt,
			RemoteAddr: l.RemoteAddr, UserAgent: l.UserAgent, Current: current != nil && current.ID == l.ID,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleTerminateSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	username, err := s.sessions.DeleteByID(r.Context(), id)
	switch {
	case errors.Is(err, session.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "session not found")
		return
	case err != nil:
		internalError(w, r, err, "cannot terminate session")
		return
	}
	s.audit.RecordRequest(r, "admin.session.terminate", username, audit.ResultSuccess, map[string]any{"session": id})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleTerminateUserSessions(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	n, err := s.sessions.DeleteUser(r.Context(), username)
	if err != nil {
		internalError(w, r, err, "cannot terminate sessions")
		return
	}
	s.audit.RecordRequest(r, "admin.session.terminate", username, audit.ResultSuccess, map[string]any{"count": n})
	httpx.WriteJSON(w, http.StatusOK, TerminatedSessions{Terminated: n})
}

func (s *Service) handleResetTOTP(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	ok, err := s.totp.Reset(r.Context(), username)
	if err != nil {
		internalError(w, r, err, "cannot reset TOTP")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "TOTP is not enrolled for this user")
		return
	}
	s.audit.RecordRequest(r, "admin.totp.reset", username, audit.ResultSuccess, nil)
	hlog.FromRequest(r).Warn().Str("component", "auth").Str("username", username).Msg("TOTP reset")
	w.WriteHeader(http.StatusNoContent)
}
