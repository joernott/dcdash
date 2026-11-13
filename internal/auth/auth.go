// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth implements the login API: password verification against LDAP
// (or the local break-glass account), the TOTP step, lockout, sessions,
// authorization and logout.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/totp"
	"github.com/joernott/dcdash/internal/vault"
)

// Authenticator verifies a user name and password.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*ldapauth.User, error)
}

// Service provides the authentication endpoints.
type Service struct {
	ldap     Authenticator // nil if LDAP is not configured
	local    *LocalAccount // nil if the break-glass account is disabled
	sessions *session.Manager
	authz    *Authorizer
	users    *lockout.Limiter
	ips      *lockout.Limiter
	audit    *audit.Log
	log      zerolog.Logger

	totp         *totp.Store
	keyring      *vault.Keyring // encrypts secrets of enrollments in progress
	issuer       string
	totpFailures *lockout.Limiter
}

// Options configures the service.
type Options struct {
	LDAP     Authenticator
	Local    *LocalAccount
	Sessions *session.Manager
	// Authorizer resolves roles; nil means no roles (only for tests).
	Authorizer  *Authorizer
	UserLockout lockout.Policy
	IPLockout   lockout.Policy
	// Audit records logins; nil disables auditing (only for tests).
	Audit *audit.Log
	Log   zerolog.Logger

	TOTP        *totp.Store
	Keyring     *vault.Keyring
	TOTPIssuer  string
	TOTPLockout lockout.Policy
}

// New creates the service.
func New(o Options) *Service {
	return &Service{
		ldap:     o.LDAP,
		local:    o.Local,
		sessions: o.Sessions,
		authz:    o.Authorizer,
		users:    lockout.New(o.UserLockout),
		ips:      lockout.New(o.IPLockout),
		audit:    o.Audit,
		log:      o.Log.With().Str("component", "auth").Logger(),

		totp:         o.TOTP,
		keyring:      o.Keyring,
		issuer:       o.TOTPIssuer,
		totpFailures: lockout.New(o.TOTPLockout),
	}
}

// LoginPath is the login endpoint. It is exempt from the CSRF token check (see
// session.CSRFExcept), but not from the Origin and JSON checks.
const LoginPath = "/api/v1/auth/login"

// Register adds the endpoints to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+LoginPath, s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	s.registerTOTP(mux)
	s.registerAdmin(mux)
}

// LoginRequest is the body of POST /api/v1/auth/login.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// GroupInfo is a group of the logged-in user.
type GroupInfo struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
}

// UserInfo describes the logged-in user.
type UserInfo struct {
	Username    string      `json:"username"`
	DisplayName string      `json:"displayName"`
	Source      string      `json:"source"`
	Groups      []GroupInfo `json:"groups"`
}

// SessionInfo is returned by login and me.
type SessionInfo struct {
	User      UserInfo  `json:"user"`
	Stage     string    `json:"stage"`
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Roles and Permissions are only set once all login steps are completed.
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	TOTP        TOTPInfo `json:"totp"`
}

func (s *Service) sessionInfo(ctx context.Context, sess *session.Session) SessionInfo {
	groups := make([]GroupInfo, 0, len(sess.Groups))
	for _, dn := range sess.Groups {
		groups = append(groups, GroupInfo{DN: dn, Name: groupName(dn)})
	}
	info := SessionInfo{
		User:      UserInfo{Username: sess.Username, DisplayName: sess.DisplayName, Source: sess.Source, Groups: groups},
		Stage:     sess.Stage,
		CSRFToken: sess.CSRFToken,
		ExpiresAt: sess.ExpiresAt,
	}
	info.Roles, info.Permissions = []string{}, []string{}
	if sess.Authenticated() && s.authz != nil {
		subj := s.authz.Subject(sess)
		info.Roles, info.Permissions = subj.Roles, subj.Permissions.Strings()
	}
	info.TOTP, _ = s.totpInfo(ctx, sess)
	return info
}

// writeSession writes the session info as response.
func (s *Service) writeSession(w http.ResponseWriter, r *http.Request, sess *session.Session) {
	httpx.WriteJSON(w, http.StatusOK, s.sessionInfo(r.Context(), sess))
}

// groupName returns the value of the first RDN (usually the CN) of a DN.
func groupName(dn string) string {
	parsed, err := ldap.ParseDN(dn)
	if err != nil || len(parsed.RDNs) == 0 || len(parsed.RDNs[0].Attributes) == 0 {
		return dn
	}
	return parsed.RDNs[0].Attributes[0].Value
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ip := httpx.RequestIP(r)
	ipKey := "ip:" + ip.String()
	userKey := "user:" + strings.ToLower(strings.TrimSpace(req.Username))
	log := hlog.FromRequest(r).With().Str("component", "auth").Str("username", req.Username).Str("client_ip", ip.String()).Logger()

	for _, check := range []struct {
		l   *lockout.Limiter
		key string
	}{{s.ips, ipKey}, {s.users, userKey}} {
		if ok, wait := check.l.Allowed(check.key); !ok {
			log.Warn().Str("locked", check.key).Dur("retry_after", wait).Msg("login rejected: locked out")
			s.auditLogin(r, req.Username, audit.ResultDenied, map[string]any{"reason": "locked", "lock": strings.SplitN(check.key, ":", 2)[0]})
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed login attempts, try again later")
			return
		}
	}

	user, source, err := s.authenticate(r.Context(), req.Username, req.Password)
	switch {
	case errors.Is(err, ldapauth.ErrInvalidCredentials):
		lockedUser := s.users.Failure(userKey)
		lockedIP := s.ips.Failure(ipKey)
		log.Warn().Bool("user_locked", lockedUser).Bool("ip_locked", lockedIP).Msg("login failed: invalid credentials")
		s.auditLogin(r, req.Username, audit.ResultFailure, map[string]any{"reason": "invalid_credentials"})
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	case err != nil:
		log.Error().Err(err).Msg("login failed: directory unavailable")
		s.auditLogin(r, req.Username, audit.ResultFailure, map[string]any{"reason": "directory_unavailable"})
		httpx.WriteError(w, http.StatusServiceUnavailable, "directory_unavailable", "the user directory is not available")
		return
	}
	s.users.Success(userKey)

	// A new session for every login prevents session fixation.
	if old := s.sessions.TokenFromRequest(r); old != "" {
		_ = s.sessions.Delete(r.Context(), old)
	}
	groups := make([]string, 0, len(user.Groups))
	for _, g := range user.Groups {
		groups = append(groups, g.DN)
	}
	n := session.New{
		Username: user.Username, DisplayName: user.DisplayName, Source: source, Groups: groups,
		RemoteAddr: ip.String(), UserAgent: r.UserAgent(),
	}
	n.Stage, err = s.initialStage(r.Context(), n)
	if errors.Is(err, errBreakGlassNotEnrolled) {
		log.Warn().Msg("break-glass login rejected: no TOTP enrolled (use `dcdash totp enroll`)")
		s.auditLogin(r, req.Username, audit.ResultDenied, map[string]any{"reason": "totp_not_enrolled"})
		httpx.WriteError(w, http.StatusForbidden, "totp_not_enrolled", "TOTP is not enrolled for this account")
		return
	}
	if err != nil {
		log.Error().Err(err).Msg("cannot determine login stage")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	token, sess, err := s.sessions.Create(r.Context(), n)
	if err != nil {
		log.Error().Err(err).Msg("cannot create session")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.sessions.SetCookie(w, token, sess.ExpiresAt)
	s.auditSession(r, sess, "auth.login", "", audit.ResultSuccess, map[string]any{"stage": sess.Stage})
	if source == session.SourceLocal {
		log.Warn().Str("stage", sess.Stage).Msg("break-glass account used: password verified")
	} else {
		log.Info().Str("dn", user.DN).Int("groups", len(groups)).Msg("login successful")
	}
	s.writeSession(w, r, sess)
}

// authenticate verifies the credentials against the break-glass account (if
// the name is reserved for it) or the directory, and returns the session source.
func (s *Service) authenticate(ctx context.Context, username, password string) (*ldapauth.User, string, error) {
	if s.local.Matches(username) {
		u, err := s.local.Authenticate(ctx, username, password)
		return u, session.SourceLocal, err
	}
	if s.ldap == nil {
		return nil, "", ldapauth.ErrInvalidCredentials
	}
	u, err := s.ldap.Authenticate(ctx, username, password)
	return u, session.SourceLDAP, err
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess := session.FromContext(r.Context()); sess != nil {
		if err := s.sessions.Delete(r.Context(), s.sessions.TokenFromRequest(r)); err != nil {
			hlog.FromRequest(r).Error().Err(err).Msg("cannot delete session")
		}
		hlog.FromRequest(r).Info().Str("component", "auth").Str("username", sess.Username).Msg("logout")
		s.auditSession(r, sess, "auth.logout", "", audit.ResultSuccess, nil)
	}
	s.sessions.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	s.writeSession(w, r, sess)
}

// RequireAuthenticated rejects requests without a fully authenticated session.
func RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := session.FromContext(r.Context()); s == nil || !s.Authenticated() {
			httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// auditLogin records a failed or rejected login. The actor is the name given.
func (s *Service) auditLogin(r *http.Request, username, result string, details map[string]any) {
	e := audit.FromRequest(r, "auth.login", "", result)
	e.Actor, e.ActorSource, e.Details = strings.TrimSpace(username), audit.SourceAnonymous, details
	_ = s.audit.Record(r.Context(), e)
}

// auditSession records an action of a session's user. The session does not
// have to be in the request context (e.g. right after login).
func (s *Service) auditSession(r *http.Request, sess *session.Session, action, target, result string, details map[string]any) {
	e := audit.FromRequest(r, action, target, result)
	e.Actor, e.ActorSource = sess.Username, sess.Source
	if details != nil {
		e.Details = details
	}
	_ = s.audit.Record(r.Context(), e)
}
