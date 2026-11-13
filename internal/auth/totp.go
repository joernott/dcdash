// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/totp"
)

// errBreakGlassNotEnrolled: the break-glass account can only enroll TOTP with
// `dcdash totp enroll`, so that its password alone never grants access.
var errBreakGlassNotEnrolled = errors.New("break-glass account has no TOTP enrolled")

// TOTPInfo describes the TOTP state of the logged-in user.
type TOTPInfo struct {
	Enrolled bool `json:"enrolled"`
	// Required is true if a role of the user (or the break-glass account) requires TOTP.
	Required bool `json:"required"`
	// RecoveryCodesRemaining is the number of unused recovery codes.
	RecoveryCodesRemaining int `json:"recoveryCodesRemaining"`
}

// totpRequired reports whether the user must use TOTP because of a role.
// Users who enrolled voluntarily must use it as well (see initialStage).
func (s *Service) totpRequired(sess *session.Session) bool {
	if sess.Source == session.SourceLocal {
		return true
	}
	return s.authz != nil && s.authz.Subject(sess).RequireTOTP
}

// initialStage returns the session stage after the password step.
func (s *Service) initialStage(ctx context.Context, n session.New) (string, error) {
	enrolled, err := s.totp.Enrolled(ctx, n.Username)
	if err != nil {
		return "", err
	}
	required := s.totpRequired(&session.Session{Username: n.Username, Source: n.Source, Groups: n.Groups})
	switch {
	case enrolled:
		return session.StageTOTP, nil
	case required && n.Source == session.SourceLocal:
		return "", errBreakGlassNotEnrolled
	case required:
		return session.StageTOTPEnroll, nil
	default:
		return session.StageAuthenticated, nil
	}
}

func (s *Service) totpInfo(ctx context.Context, sess *session.Session) (TOTPInfo, error) {
	info := TOTPInfo{Required: s.totpRequired(sess)}
	var err error
	if info.Enrolled, err = s.totp.Enrolled(ctx, sess.Username); err != nil || !info.Enrolled {
		return info, err
	}
	info.RecoveryCodesRemaining, err = s.totp.RemainingRecoveryCodes(ctx, sess.Username)
	return info, err
}

// TOTPRequest is the body of POST /api/v1/auth/totp and of the endpoints that
// need a current code. Exactly one of the fields is set.
type TOTPRequest struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
}

// EnrollmentInfo is returned when an enrollment starts.
type EnrollmentInfo struct {
	// Secret is the base32 secret for manual entry.
	Secret string `json:"secret"`
	// URI is the otpauth:// URI, usually shown as QR code.
	URI string `json:"uri"`
}

// EnrollmentResult is returned when an enrollment is confirmed.
type EnrollmentResult struct {
	RecoveryCodes []string    `json:"recoveryCodes"`
	Session       SessionInfo `json:"session"`
}

// RecoveryCodes is returned when recovery codes are regenerated.
type RecoveryCodes struct {
	RecoveryCodes []string `json:"recoveryCodes"`
}

func (s *Service) registerTOTP(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/totp", s.handleTOTP)
	mux.HandleFunc("POST /api/v1/auth/totp/enroll", s.handleEnrollStart)
	mux.HandleFunc("POST /api/v1/auth/totp/enroll/confirm", s.handleEnrollConfirm)
	mux.Handle("POST /api/v1/auth/totp/recovery-codes", RequireAuthenticated(http.HandlerFunc(s.handleRegenerateCodes)))
}

// requireStage returns the session if it is in one of the stages; otherwise it
// writes an error.
func requireStage(w http.ResponseWriter, r *http.Request, stages ...string) *session.Session {
	sess := session.FromContext(r.Context())
	if sess == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil
	}
	for _, st := range stages {
		if sess.Stage == st {
			return sess
		}
	}
	httpx.WriteError(w, http.StatusConflict, "wrong_stage", "not possible in the current login step")
	return nil
}

// checkCode verifies a TOTP or recovery code with lockout. It writes the
// error response and returns false on failure.
func (s *Service) checkCode(w http.ResponseWriter, r *http.Request, sess *session.Session, req TOTPRequest, action string) bool {
	lockKey := "totp:" + strings.ToLower(sess.Username)
	if ok, wait := s.totpFailures.Allowed(lockKey); !ok {
		s.auditSession(r, sess, action, "", audit.ResultDenied, map[string]any{"reason": "locked"})
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts, try again later")
		return false
	}
	method := "totp"
	var err error
	switch {
	case req.Code != "" && req.RecoveryCode == "":
		err = s.totp.Verify(r.Context(), sess.Username, req.Code)
	case req.RecoveryCode != "" && req.Code == "":
		method = "recovery_code"
		err = s.totp.UseRecoveryCode(r.Context(), sess.Username, req.RecoveryCode)
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "either code or recoveryCode is required")
		return false
	}
	switch {
	case errors.Is(err, totp.ErrInvalidCode), errors.Is(err, totp.ErrNotEnrolled):
		locked := s.totpFailures.Failure(lockKey)
		s.auditSession(r, sess, action, "", audit.ResultFailure, map[string]any{"method": method, "locked": locked})
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid code")
		return false
	case err != nil:
		hlog.FromRequest(r).Error().Err(err).Str("username", sess.Username).Msg("TOTP verification failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return false
	}
	s.totpFailures.Success(lockKey)
	details := map[string]any{"method": method}
	if method == "recovery_code" {
		n, _ := s.totp.RemainingRecoveryCodes(r.Context(), sess.Username)
		details["remaining"] = n
	}
	s.auditSession(r, sess, action, "", audit.ResultSuccess, details)
	return true
}

// complete moves a pending session to the authenticated stage with a new token.
func (s *Service) complete(w http.ResponseWriter, r *http.Request, sess *session.Session) bool {
	token, err := s.sessions.SetStage(r.Context(), sess, session.StageAuthenticated)
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot update session")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return false
	}
	s.sessions.SetCookie(w, token, sess.ExpiresAt)
	if sess.Source == session.SourceLocal {
		hlog.FromRequest(r).Warn().Str("component", "auth").Str("username", sess.Username).Msg("break-glass account logged in")
	}
	return true
}

func (s *Service) handleTOTP(w http.ResponseWriter, r *http.Request) {
	var req TOTPRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sess := requireStage(w, r, session.StageTOTP)
	if sess == nil || !s.checkCode(w, r, sess, req, "auth.totp.verify") || !s.complete(w, r, sess) {
		return
	}
	s.writeSession(w, r, sess)
}

func (s *Service) handleEnrollStart(w http.ResponseWriter, r *http.Request) {
	sess := requireStage(w, r, session.StageTOTPEnroll, session.StageAuthenticated)
	if sess == nil {
		return
	}
	if sess.Source == session.SourceLocal {
		httpx.WriteError(w, http.StatusForbidden, "forbidden", "the break-glass account enrolls TOTP with `dcdash totp enroll`")
		return
	}
	if enrolled, err := s.totp.Enrolled(r.Context(), sess.Username); err != nil || enrolled {
		if err == nil {
			httpx.WriteError(w, http.StatusConflict, "already_enrolled", "TOTP is already enrolled")
		} else {
			httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
		return
	}
	secret := totp.NewSecret()
	enc, err := s.keyring.Encrypt(secret, totp.PendingAAD(sess.Username))
	if err == nil {
		err = s.sessions.SetPendingTOTP(r.Context(), sess, enc)
	}
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot start TOTP enrollment")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, EnrollmentInfo{
		Secret: totp.EncodeSecret(secret),
		URI:    totp.URI(s.issuer, sess.Username, secret),
	})
}

func (s *Service) handleEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	var req TOTPRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sess := requireStage(w, r, session.StageTOTPEnroll, session.StageAuthenticated)
	if sess == nil {
		return
	}
	if sess.PendingTOTP == nil {
		httpx.WriteError(w, http.StatusConflict, "no_enrollment", "no TOTP enrollment in progress")
		return
	}
	lockKey := "totp:" + strings.ToLower(sess.Username)
	if ok, wait := s.totpFailures.Allowed(lockKey); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		httpx.WriteError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts, try again later")
		return
	}
	secret, err := s.keyring.Decrypt(sess.PendingTOTP, totp.PendingAAD(sess.Username))
	var codes []string
	if err == nil {
		codes, err = s.totp.Enroll(r.Context(), sess.Username, secret, req.Code)
	}
	switch {
	case errors.Is(err, totp.ErrInvalidCode):
		s.totpFailures.Failure(lockKey)
		s.auditSession(r, sess, "auth.totp.enroll", "", audit.ResultFailure, map[string]any{"reason": "invalid_code"})
		httpx.WriteError(w, http.StatusUnauthorized, "invalid_code", "invalid code")
		return
	case errors.Is(err, totp.ErrAlreadyEnrolled):
		httpx.WriteError(w, http.StatusConflict, "already_enrolled", "TOTP is already enrolled")
		return
	case err != nil:
		hlog.FromRequest(r).Error().Err(err).Msg("cannot complete TOTP enrollment")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.totpFailures.Success(lockKey)
	_ = s.sessions.SetPendingTOTP(r.Context(), sess, nil)
	s.auditSession(r, sess, "auth.totp.enroll", "", audit.ResultSuccess, map[string]any{"voluntary": sess.Authenticated()})
	if !sess.Authenticated() && !s.complete(w, r, sess) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, EnrollmentResult{RecoveryCodes: codes, Session: s.sessionInfo(r.Context(), sess)})
}

func (s *Service) handleRegenerateCodes(w http.ResponseWriter, r *http.Request) {
	var req TOTPRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sess := session.FromContext(r.Context())
	if req.RecoveryCode != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "a current TOTP code is required")
		return
	}
	if !s.checkCode(w, r, sess, req, "auth.totp.verify") {
		return
	}
	codes, err := s.totp.RegenerateRecoveryCodes(r.Context(), sess.Username)
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot regenerate recovery codes")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	s.auditSession(r, sess, "auth.recovery_codes.regenerate", "", audit.ResultSuccess, nil)
	httpx.WriteJSON(w, http.StatusOK, RecoveryCodes{RecoveryCodes: codes})
}
