// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

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
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/totp"
	"github.com/joernott/dcdash/internal/vault"
)

// TestADRootLoginWithTOTP logs a member of dashroot in against the Samba AD:
// password, TOTP enrollment, root role, and a second login with the TOTP step.
// Only the correct password is sent, so the AD lockout policy is never hit.
func TestADRootLoginWithTOTP(t *testing.T) {
	cfg := adConfig(t)
	user := requireEnv(t, "DCDASH_IT_AD_ROOT_USER")
	password := requireEnv(t, "DCDASH_IT_AD_ROOT_PASSWORD")

	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	kr, err := vault.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	totpStore := totp.NewStore(st.DB(), kr)
	totpStore.SetClock(func() time.Time { return now })
	log := zerolog.New(zerolog.NewTestWriter(t))
	auditLog := audit.New(st.DB(), log)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), auditLog)
	sessions := session.NewManager(st.DB(), time.Hour, time.Hour, log)
	policy := lockout.Policy{MaxFailures: 5, Window: time.Minute, Duration: time.Minute}
	svc := auth.New(auth.Options{
		LDAP: adClient(t, cfg), Sessions: sessions, Authorizer: authz, Audit: auditLog, Log: log,
		UserLockout: policy, IPLockout: policy, TOTPLockout: policy,
		TOTP: totpStore, Keyring: kr, TOTPIssuer: "dcdash integration test",
	})
	mux := http.NewServeMux()
	svc.Register(mux)
	h := sessions.Middleware(authz.Middleware(session.CSRFExcept(auth.LoginPath)(mux)))

	var cookie, csrf string
	do := func(method, path string, body any, out any) int {
		t.Helper()
		var b string
		if body != nil {
			j, _ := json.Marshal(body)
			b = string(j)
		}
		r := httptest.NewRequest(method, "https://dcdash.test"+path, strings.NewReader(b))
		if b != "" {
			r.Header.Set("Content-Type", "application/json")
		} else {
			r.ContentLength = 0
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: session.CookieName, Value: cookie})
			r.Header.Set(session.CSRFHeader, csrf)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		for _, c := range rec.Result().Cookies() {
			if c.Name == session.CookieName {
				cookie = c.Value
			}
		}
		if out != nil {
			_ = json.Unmarshal(rec.Body.Bytes(), out)
		}
		if info, ok := out.(*auth.SessionInfo); ok && info.CSRFToken != "" {
			csrf = info.CSRFToken
		}
		return rec.Code
	}
	login := func() auth.SessionInfo {
		t.Helper()
		cookie = ""
		var info auth.SessionInfo
		if code := do(http.MethodPost, auth.LoginPath, map[string]string{"username": user, "password": password}, &info); code != http.StatusOK {
			t.Fatalf("login: %d", code)
		}
		return info
	}

	info := login()
	groups := make([]string, 0, len(info.User.Groups))
	for _, g := range info.User.Groups {
		groups = append(groups, g.Name)
	}
	t.Logf("user %q, groups %v", info.User.Username, groups)
	if info.Stage != session.StageTOTPEnroll || !info.TOTP.Required || !slices.ContainsFunc(groups, func(g string) bool { return strings.EqualFold(g, rbac.DefaultRootGroup) }) {
		t.Fatalf("after password: stage %q, totp %+v, groups %v", info.Stage, info.TOTP, groups)
	}

	var en auth.EnrollmentInfo
	if code := do(http.MethodPost, "/api/v1/auth/totp/enroll", nil, &en); code != http.StatusOK {
		t.Fatalf("enroll: %d", code)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.Secret)
	if err != nil {
		t.Fatal(err)
	}
	var res auth.EnrollmentResult
	if code := do(http.MethodPost, "/api/v1/auth/totp/enroll/confirm", map[string]string{"code": totp.Code(secret, totp.Step(now))}, &res); code != http.StatusOK {
		t.Fatalf("confirm: %d", code)
	}
	csrf = res.Session.CSRFToken
	if res.Session.Stage != session.StageAuthenticated || !slices.Equal(res.Session.Roles, []string{rbac.RootRole}) || len(res.RecoveryCodes) != totp.RecoveryCodeCount {
		t.Fatalf("after enrollment: %+v", res.Session)
	}
	if code := do(http.MethodPost, "/api/v1/auth/logout", nil, nil); code != http.StatusNoContent {
		t.Fatalf("logout: %d", code)
	}

	if info := login(); info.Stage != session.StageTOTP {
		t.Fatalf("second login: %+v", info)
	}
	now = now.Add(totp.Period)
	info = auth.SessionInfo{}
	if code := do(http.MethodPost, "/api/v1/auth/totp", map[string]string{"code": totp.Code(secret, totp.Step(now))}, &info); code != http.StatusOK {
		t.Fatalf("TOTP step: %d", code)
	}
	if info.Stage != session.StageAuthenticated || !slices.Contains(info.Permissions, "*:*:*:admin") {
		t.Errorf("after TOTP step: %+v", info)
	}
	entries, _ := auditLog.Query(context.Background(), audit.Filter{Actor: user})
	t.Logf("%d audit entries", len(entries))
	for _, e := range entries {
		if e.Result != audit.ResultSuccess {
			t.Errorf("unexpected audit entry: %s", fmt.Sprintf("%+v", e))
		}
	}
}
