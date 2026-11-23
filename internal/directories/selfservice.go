// SPDX-License-Identifier: GPL-3.0-or-later

package directories

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/lockout"
)

// The self-service password change works without a dcdash login, for every user of a
// directory with self_service (like LTB Self Service Password). It is a password change
// with the old password, done over the service account's connection (see
// ad.Client.ChangePassword), so it works for users who must change their password.
const (
	// ChangePasswordPath is exempt from the CSRF token check: the page is used without a
	// session. The origin and JSON checks still apply.
	ChangePasswordPath = "/api/v1/password/change"
	// AuditPasswordChange is the audit action of a self-service change.
	AuditPasswordChange = "directory.password.change"
	// SourceSelfService is the actor source of self-service audit entries.
	SourceSelfService = "self-service"
	// defaultFailureDelay is the minimum time a failed change takes, so that an unknown
	// user and a wrong password can't be told apart by the response time.
	defaultFailureDelay = time.Second
)

// EnableSelfService turns on the self-service password change with rate limits per user
// (directory and name) and per client IP address.
func (s *Service) EnableSelfService(user, ip lockout.Policy) {
	s.ssUsers, s.ssIPs = lockout.New(user), lockout.New(ip)
	s.ssDelay = defaultFailureDelay
}

// SetFailureDelay changes the minimum time of a failed self-service change (tests).
func (s *Service) SetFailureDelay(d time.Duration) { s.ssDelay = d }

// registerSelfService adds the routes of the self-service page; they answer 404 unless
// EnableSelfService was called.
func (s *Service) registerSelfService(mux *http.ServeMux) {
	enabled := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.ssUsers == nil {
				httpx.WriteError(w, http.StatusNotFound, "not_found", "the self-service password change is off")
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/password/directories", enabled(s.handleSelfServiceDirectories))
	mux.HandleFunc("POST "+ChangePasswordPath, enabled(s.handleChangePassword))
}

// SelfServiceDirectory is a directory on the self-service page.
type SelfServiceDirectory struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	// Policy is missing if it couldn't be read.
	Policy *ad.PasswordPolicy `json:"policy,omitempty"`
}

func (s *Service) handleSelfServiceDirectories(w http.ResponseWriter, r *http.Request) {
	out := []SelfServiceDirectory{}
	for _, n := range s.order {
		b := s.dirs[n]
		cfg := b.Config()
		if !cfg.SelfServiceEnabled() {
			continue
		}
		d := SelfServiceDirectory{Name: n, Domain: cfg.Domain}
		ctx, cancel := context.WithTimeout(r.Context(), cfg.RequestTimeout())
		if p, err := b.Policy(ctx); err == nil {
			d.Policy = &p
		} else {
			hlog.FromRequest(r).Warn().Err(err).Str("directory", n).Msg("password policy not available")
		}
		cancel()
		out = append(out, d)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"directories": out})
}

// PasswordChange is the body of POST /api/v1/password/change.
type PasswordChange struct {
	Directory string `json:"directory"`
	// Username is the account name or the user principal name.
	Username        string `json:"username"`
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// validLogin checks an account name or user principal name.
func validLogin(name string) bool {
	return name != "" && len(name) <= 256 && strings.TrimSpace(name) == name &&
		!strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || strings.ContainsRune(`"/\[]:;|=,+*?<>`, r) })
}

// policyReason classifies a password policy error by the domain controller's message, for
// a translated explanation. Windows only sends the code, so its reason is "policy".
func policyReason(err error) string {
	var e *ad.Error
	if !errors.As(err, &e) {
		return "policy"
	}
	m := strings.ToLower(e.Message)
	switch {
	case strings.Contains(m, "too short"):
		return "tooShort"
	case strings.Contains(m, "history") || strings.Contains(m, "already used"):
		return "history"
	case strings.Contains(m, "too young"):
		return "tooYoung"
	case strings.Contains(m, "complexity"):
		return "complexity"
	}
	return "policy"
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req PasswordChange
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	b := s.dirs[req.Directory]
	// Checked before the name is logged, audited or used as a lockout key.
	switch {
	case b == nil || !b.Config().SelfServiceEnabled():
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "unknown directory")
		return
	case !validLogin(req.Username):
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid user name")
		return
	case validPassword(req.CurrentPassword) != "" || validPassword(req.NewPassword) != "":
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "the passwords must have 1 to 256 characters")
		return
	case req.CurrentPassword == req.NewPassword:
		httpx.WriteError(w, http.StatusBadRequest, "same_password", "the new password must differ from the current one")
		return
	}
	ip := httpx.RequestIP(r)
	ipKey := "ip:" + lockout.IPKey(ip)
	userKey := "user:" + req.Directory + ":" + strings.ToLower(req.Username)
	log := hlog.FromRequest(r).With().Str("component", "directories").Str("directory", req.Directory).
		Str("username", req.Username).Str("client_ip", ip.String()).Logger()
	tgt := req.Directory + "/" + UserResource(req.Username)
	record := func(result string, details map[string]any) {
		e := audit.FromRequest(r, AuditPasswordChange, tgt, result)
		e.Actor, e.ActorSource = req.Username, SourceSelfService
		for k, v := range details {
			e.Details[k] = v
		}
		if err := s.audit.Record(r.Context(), e); err != nil {
			log.Error().Err(err).Msg("audit failed")
		}
	}
	for _, check := range []struct {
		l   *lockout.Limiter
		key string
	}{{s.ssIPs, ipKey}, {s.ssUsers, userKey}} {
		ok, wait := check.l.Acquire(check.key)
		if !ok {
			log.Warn().Str("locked", check.key).Dur("retry_after", wait).Msg("password change rejected: too many attempts")
			record(audit.ResultDenied, map[string]any{"reason": "locked", "lock": strings.SplitN(check.key, ":", 2)[0]})
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts, try again later")
			return
		}
		defer check.l.Release(check.key)
	}

	err := b.ChangePassword(r.Context(), req.Username, req.CurrentPassword, req.NewPassword)
	slow := func() {
		if d := s.ssDelay - time.Since(start); d > 0 {
			time.Sleep(d)
		}
	}
	switch {
	case err == nil:
		s.ssUsers.Success(userKey)
		log.Info().Msg("password changed (self-service)")
		record(audit.ResultSuccess, nil)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ad.ErrNotFound), errors.Is(err, ad.ErrWrongPassword):
		// The same answer for unknown users and wrong passwords.
		lockedUser, lockedIP := s.ssUsers.Failure(userKey), s.ssIPs.Failure(ipKey)
		log.Warn().Bool("user_locked", lockedUser).Bool("ip_locked", lockedIP).Msg("password change failed: invalid credentials")
		record(audit.ResultFailure, map[string]any{"reason": "invalid_credentials"})
		slow()
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_credentials", "the user name or the current password is wrong")
	case errors.Is(err, ad.ErrPolicy):
		reason := policyReason(err)
		log.Info().Str("reason", reason).Msg("password change rejected by the password policy")
		record(audit.ResultFailure, map[string]any{"reason": "password_policy", "policy": reason})
		httpx.WriteError(w, http.StatusUnprocessableEntity, "password_policy_"+reason, errorMessage(err))
	case errors.Is(err, ad.ErrUnavailable):
		log.Error().Err(err).Msg("password change failed: directory unavailable")
		record(audit.ResultFailure, map[string]any{"reason": "directory_unavailable"})
		httpx.WriteError(w, http.StatusServiceUnavailable, "directory_unavailable", "the directory is not available")
	default:
		log.Warn().Err(err).Msg("password change rejected")
		record(audit.ResultFailure, map[string]any{"reason": "rejected", "error": err.Error()})
		slow()
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", "the directory rejected the change")
	}
}

// errorMessage is the domain controller's message of an error, or the error.
func errorMessage(err error) string {
	var e *ad.Error
	if errors.As(err, &e) && e.Message != "" {
		return e.Message
	}
	return err.Error()
}
