// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/totp"
)

// enroll enrolls TOTP for a user directly in the store and returns the secret.
func (e *env) enroll(t *testing.T, user string) []byte {
	t.Helper()
	secret := totp.NewSecret()
	if _, err := e.totp.Enroll(context.Background(), user, secret, totp.Code(secret, totp.Step(e.now))); err != nil {
		t.Fatal(err)
	}
	return secret
}

// code advances the clock by one time step and returns the current code.
func (e *env) code(secret []byte) string {
	e.now = e.now.Add(totp.Period)
	return totp.Code(secret, totp.Step(e.now))
}

// sess is a browser session in the tests.
type sess struct {
	e            *env
	cookie, csrf string
	info         SessionInfo
}

func (e *env) login(t *testing.T, user, pass string) *sess {
	t.Helper()
	rec := e.do(login(user, pass))
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", user, rec.Code, rec.Body)
	}
	s := &sess{e: e, cookie: cookieValue(t, rec)}
	s.update(t, rec)
	return s
}

func (s *sess) update(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), &s.info); err != nil {
		t.Fatalf("session info: %v: %s", err, rec.Body)
	}
	s.csrf = s.info.CSRFToken
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			s.cookie = c.Value
		}
	}
}

func (s *sess) post(path, body string) *httptest.ResponseRecorder {
	return s.e.do(call{method: http.MethodPost, path: path, body: body, cookie: s.cookie, csrf: s.csrf})
}

func (s *sess) get(path string) *httptest.ResponseRecorder {
	return s.e.do(call{method: http.MethodGet, path: path, cookie: s.cookie})
}

func codeBody(code string) string { return fmt.Sprintf(`{"code":%q}`, code) }

func TestEnrollmentRequiredByRole(t *testing.T) {
	e := newEnv(t)
	s := e.login(t, "alice", "secret") // root: TOTP required
	if s.info.Stage != session.StageTOTPEnroll || !s.info.TOTP.Required || s.info.TOTP.Enrolled || len(s.info.Roles) != 0 {
		t.Fatalf("after password: %+v", s.info)
	}
	if rec := s.get("/api/v1/protected"); rec.Code != http.StatusUnauthorized {
		t.Errorf("enrollment session granted access: %d", rec.Code)
	}
	if rec := s.post("/api/v1/auth/totp", codeBody("123456")); rec.Code != http.StatusConflict {
		t.Errorf("TOTP step before enrollment: %d", rec.Code)
	}
	if rec := s.post("/api/v1/auth/totp/enroll/confirm", codeBody("123456")); rec.Code != http.StatusConflict {
		t.Errorf("confirm without enrollment: %d", rec.Code)
	}

	rec := s.post("/api/v1/auth/totp/enroll", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("enroll: %d %s", rec.Code, rec.Body)
	}
	var en EnrollmentInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &en)
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.Secret)
	if err != nil || len(secret) != totp.SecretSize || !strings.HasPrefix(en.URI, "otpauth://totp/dcdash%20test:alice?") {
		t.Fatalf("enrollment info: %+v", en)
	}

	if rec := s.post("/api/v1/auth/totp/enroll/confirm", codeBody(totp.Code(secret, totp.Step(e.now)+5))); rec.Code != http.StatusUnauthorized {
		t.Errorf("confirm with wrong code: %d", rec.Code)
	}
	oldCookie := s.cookie
	rec = s.post("/api/v1/auth/totp/enroll/confirm", codeBody(totp.Code(secret, totp.Step(e.now))))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	var res EnrollmentResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.RecoveryCodes) != totp.RecoveryCodeCount || res.Session.Stage != session.StageAuthenticated ||
		!slices.Equal(res.Session.Roles, []string{"root"}) || !res.Session.TOTP.Enrolled {
		t.Fatalf("enrollment result: %+v", res)
	}
	s.update(t, rec) // takes the new cookie
	if s.cookie == oldCookie {
		t.Error("token not rotated after enrollment")
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/protected", cookie: oldCookie}); rec.Code != http.StatusUnauthorized {
		t.Errorf("old token still valid: %d", rec.Code)
	}
	if rec := s.get("/api/v1/test/root"); rec.Code != http.StatusNoContent {
		t.Errorf("root access after enrollment: %d", rec.Code)
	}

	// The next login asks for a code; the enrollment code cannot be replayed.
	s = e.login(t, "alice", "secret")
	if s.info.Stage != session.StageTOTP {
		t.Fatalf("second login: %+v", s.info)
	}
	if rec := s.post("/api/v1/auth/totp", codeBody(totp.Code(secret, totp.Step(e.now)))); rec.Code != http.StatusUnauthorized {
		t.Errorf("replayed enrollment code: %d", rec.Code)
	}
	rec = s.post("/api/v1/auth/totp", codeBody(e.code(secret)))
	if rec.Code != http.StatusOK {
		t.Fatalf("TOTP step: %d %s", rec.Code, rec.Body)
	}
	s.update(t, rec)
	if s.info.Stage != session.StageAuthenticated || s.get("/api/v1/test/root").Code != http.StatusNoContent {
		t.Errorf("after TOTP step: %+v", s.info)
	}
}

func TestRecoveryCodeLogin(t *testing.T) {
	e := newEnv(t)
	secret := totp.NewSecret()
	codes, err := e.totp.Enroll(context.Background(), "alice", secret, totp.Code(secret, totp.Step(e.now)))
	if err != nil {
		t.Fatal(err)
	}
	s := e.login(t, "alice", "secret")
	if rec := s.post("/api/v1/auth/totp", `{"code":"123456","recoveryCode":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("both fields: %d", rec.Code)
	}
	rec := s.post("/api/v1/auth/totp", fmt.Sprintf(`{"recoveryCode":%q}`, codes[0]))
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery code: %d %s", rec.Code, rec.Body)
	}
	s.update(t, rec)
	if s.info.TOTP.RecoveryCodesRemaining != totp.RecoveryCodeCount-1 {
		t.Errorf("remaining: %+v", s.info.TOTP)
	}
	s = e.login(t, "alice", "secret")
	if rec := s.post("/api/v1/auth/totp", fmt.Sprintf(`{"recoveryCode":%q}`, codes[0])); rec.Code != http.StatusUnauthorized {
		t.Errorf("reused recovery code: %d", rec.Code)
	}
}

func TestTOTPLockout(t *testing.T) {
	e := newEnv(t)
	secret := e.enroll(t, "alice")
	s := e.login(t, "alice", "secret")
	wrong := totp.Code(secret, totp.Step(e.now)+5)
	for range 3 {
		if rec := s.post("/api/v1/auth/totp", codeBody(wrong)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code: %d", rec.Code)
		}
	}
	rec := s.post("/api/v1/auth/totp", codeBody(e.code(secret)))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("locked: %d", rec.Code)
	}
	entries, _ := e.audit.Query(context.Background(), audit.Filter{Action: "auth.totp.verify"})
	results := map[string]int{}
	for _, en := range entries {
		results[en.Result]++
	}
	if results[audit.ResultFailure] != 3 || results[audit.ResultDenied] != 1 {
		t.Errorf("audit: %v", results)
	}
}

func TestVoluntaryEnrollment(t *testing.T) {
	e := newEnv(t)
	s := e.login(t, "bob", "secret") // TOTP not required
	if s.info.Stage != session.StageAuthenticated || s.info.TOTP.Required || s.info.TOTP.Enrolled {
		t.Fatalf("bob: %+v", s.info)
	}
	if rec := s.post("/api/v1/auth/totp", codeBody("123456")); rec.Code != http.StatusConflict {
		t.Errorf("TOTP step when authenticated: %d", rec.Code)
	}
	rec := s.post("/api/v1/auth/totp/enroll", "")
	var en EnrollmentInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &en)
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.Secret)
	rec = s.post("/api/v1/auth/totp/enroll/confirm", codeBody(totp.Code(secret, totp.Step(e.now))))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	var res EnrollmentResult
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Session.Stage != session.StageAuthenticated || !res.Session.TOTP.Enrolled {
		t.Errorf("voluntary enrollment: %+v", res.Session)
	}
	if rec := s.post("/api/v1/auth/totp/enroll", ""); rec.Code != http.StatusConflict {
		t.Errorf("second enrollment: %d", rec.Code)
	}
	// Once enrolled, TOTP is enforced.
	if s := e.login(t, "bob", "secret"); s.info.Stage != session.StageTOTP {
		t.Errorf("login after voluntary enrollment: %+v", s.info)
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	e := newEnv(t)
	secret := e.enroll(t, "bob")
	s := e.login(t, "bob", "secret")
	s.update(t, s.post("/api/v1/auth/totp", codeBody(e.code(secret))))
	if s.info.Stage != session.StageAuthenticated {
		t.Fatalf("login: %+v", s.info)
	}
	if rec := s.post("/api/v1/auth/totp/recovery-codes", `{"recoveryCode":"abcd-abcd-abcd-abcd"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("with recovery code: %d", rec.Code)
	}
	if rec := s.post("/api/v1/auth/totp/recovery-codes", codeBody(totp.Code(secret, totp.Step(e.now)+5))); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong code: %d", rec.Code)
	}
	rec := s.post("/api/v1/auth/totp/recovery-codes", codeBody(e.code(secret)))
	var codes RecoveryCodes
	_ = json.Unmarshal(rec.Body.Bytes(), &codes)
	if rec.Code != http.StatusOK || len(codes.RecoveryCodes) != totp.RecoveryCodeCount {
		t.Errorf("regenerate: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(call{method: http.MethodPost, path: "/api/v1/auth/totp/recovery-codes", body: codeBody("123456")}); rec.Code != http.StatusUnauthorized {
		t.Errorf("without session: %d", rec.Code)
	}
}

func TestBreakGlassNeedsCLIEnrollment(t *testing.T) {
	e, _ := newLocalEnv(t)
	rec := e.do(login("breakglass", "break-glass-password"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "totp_not_enrolled") {
		t.Fatalf("not enrolled: %d %s", rec.Code, rec.Body)
	}
	secret := e.enroll(t, "breakglass")
	s := e.login(t, "breakglass", "break-glass-password")
	if rec := s.post("/api/v1/auth/totp/enroll", ""); rec.Code != http.StatusConflict {
		t.Errorf("web enrollment in TOTP stage: %d", rec.Code)
	}
	s.update(t, s.post("/api/v1/auth/totp", codeBody(e.code(secret))))
	if s.info.Stage != session.StageAuthenticated || !slices.Equal(s.info.Roles, []string{"root"}) {
		t.Errorf("break-glass after TOTP: %+v", s.info)
	}
	if rec := s.post("/api/v1/auth/totp/enroll", ""); rec.Code != http.StatusForbidden {
		t.Errorf("break-glass web enrollment: %d", rec.Code)
	}
}
