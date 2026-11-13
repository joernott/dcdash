// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth implements the login API: password verification against LDAP
// (or the local break-glass account), lockout, session creation and logout.
package auth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/session"
)

// Authenticator verifies a user name and password.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*ldapauth.User, error)
}

// Service provides the authentication endpoints.
type Service struct {
	ldap           Authenticator // nil if LDAP is not configured
	local          *LocalAccount // nil if the break-glass account is disabled
	sessions       *session.Manager
	authz          *Authorizer
	users          *lockout.Limiter
	ips            *lockout.Limiter
	trustedProxies []netip.Prefix
	log            zerolog.Logger
}

// Options configures the service.
type Options struct {
	LDAP     Authenticator
	Local    *LocalAccount
	Sessions *session.Manager
	// Authorizer resolves roles; nil means no roles (only for tests).
	Authorizer     *Authorizer
	UserLockout    lockout.Policy
	IPLockout      lockout.Policy
	TrustedProxies []netip.Prefix
	Log            zerolog.Logger
}

// New creates the service.
func New(o Options) *Service {
	return &Service{
		ldap:           o.LDAP,
		local:          o.Local,
		sessions:       o.Sessions,
		authz:          o.Authorizer,
		users:          lockout.New(o.UserLockout),
		ips:            lockout.New(o.IPLockout),
		trustedProxies: o.TrustedProxies,
		log:            o.Log.With().Str("component", "auth").Logger(),
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
}

func (s *Service) sessionInfo(sess *session.Session) SessionInfo {
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
	return info
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
	ip := httpx.ClientIP(r, s.trustedProxies)
	ipKey := "ip:" + ip.String()
	userKey := "user:" + strings.ToLower(strings.TrimSpace(req.Username))
	log := hlog.FromRequest(r).With().Str("component", "auth").Str("username", req.Username).Str("client_ip", ip.String()).Logger()

	for _, check := range []struct {
		l   *lockout.Limiter
		key string
	}{{s.ips, ipKey}, {s.users, userKey}} {
		if ok, wait := check.l.Allowed(check.key); !ok {
			log.Warn().Str("locked", check.key).Dur("retry_after", wait).Msg("login rejected: locked out")
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
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	case err != nil:
		log.Error().Err(err).Msg("login failed: directory unavailable")
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
	token, sess, err := s.sessions.Create(r.Context(), session.New{
		Username: user.Username, DisplayName: user.DisplayName, Source: source,
		Groups: groups, Stage: initialStage(source),
		RemoteAddr: ip.String(), UserAgent: r.UserAgent(),
	})
	if err != nil {
		log.Error().Err(err).Msg("cannot create session")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.sessions.SetCookie(w, token, sess.ExpiresAt)
	if source == session.SourceLocal {
		log.Warn().Str("stage", sess.Stage).Msg("break-glass account used: password verified")
	} else {
		log.Info().Str("dn", user.DN).Int("groups", len(groups)).Msg("login successful")
	}
	httpx.WriteJSON(w, http.StatusOK, s.sessionInfo(sess))
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

// initialStage returns the session stage after the password step. The
// break-glass account must always complete the TOTP step (Sprint 4); until
// that step exists, its sessions cannot be completed.
func initialStage(source string) string {
	if source == session.SourceLocal {
		return session.StageTOTP
	}
	return session.StageAuthenticated
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess := session.FromContext(r.Context()); sess != nil {
		if err := s.sessions.Delete(r.Context(), s.sessions.TokenFromRequest(r)); err != nil {
			hlog.FromRequest(r).Error().Err(err).Msg("cannot delete session")
		}
		hlog.FromRequest(r).Info().Str("component", "auth").Str("username", sess.Username).Msg("logout")
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
	httpx.WriteJSON(w, http.StatusOK, s.sessionInfo(sess))
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
