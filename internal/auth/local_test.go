// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("unexpected format: %s", h)
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Error("same hash twice: salt not random")
	}
	if ok, err := VerifyPassword(h, "correct horse battery staple"); err != nil || !ok {
		t.Errorf("correct password: %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "wrong"); ok {
		t.Error("wrong password accepted")
	}
	if _, err := HashPassword(""); err == nil {
		t.Error("empty password hashed")
	}
}

func TestParseHashRejectsBadInput(t *testing.T) {
	good, _ := HashPassword("x")
	parts := strings.Split(good, "$")
	for name, h := range map[string]string{
		"bcrypt":       "$2y$10$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz01234",
		"argon2i":      strings.Replace(good, "argon2id", "argon2i", 1),
		"old version":  strings.Replace(good, "v=19", "v=16", 1),
		"huge memory":  strings.Replace(good, "m=65536", "m=99999999", 1),
		"zero time":    strings.Replace(good, "t=3", "t=0", 1),
		"short salt":   strings.Join([]string{"", parts[1], parts[2], parts[3], "YWI", parts[5]}, "$"),
		"bad hash b64": strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "!!!"}, "$"),
		"empty":        "",
	} {
		if err := ValidateHash(h); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func newLocalEnv(t *testing.T) (*env, *LocalAccount) {
	t.Helper()
	hash, err := HashPassword("break-glass-password")
	if err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalAccount("breakglass", hash)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sm := session.NewManager(st.DB(), time.Hour, time.Hour, zerolog.Nop())
	dir := &fakeDirectory{}
	authz := testAuthorizer(t)
	svc := New(Options{
		LDAP: dir, Local: local, Sessions: sm, Authorizer: authz, Log: zerolog.Nop(),
		UserLockout: lockout.Policy{MaxFailures: 3, Window: time.Minute, Duration: time.Minute},
		IPLockout:   lockout.Policy{MaxFailures: 50, Window: time.Minute, Duration: time.Minute},
	})
	mux := http.NewServeMux()
	svc.Register(mux)
	registerTestRoutes(mux)
	return &env{h: sm.Middleware(authz.Middleware(session.CSRFExcept(LoginPath)(mux))), dir: dir, sm: sm}, local
}

func TestLocalLoginRequiresTOTP(t *testing.T) {
	e, _ := newLocalEnv(t)
	rec := e.do(login("BreakGlass", "break-glass-password"))
	if rec.Code != http.StatusOK {
		t.Fatalf("local login: %d %s", rec.Code, rec.Body)
	}
	var info SessionInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info.Stage != session.StageTOTP || info.User.Source != session.SourceLocal {
		t.Errorf("local session: %+v", info)
	}
	if e.dir.calls != 0 {
		t.Error("reserved local name was sent to the directory")
	}
	// The pending session does not grant access.
	token := cookieValue(t, rec)
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/protected", cookie: token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("pending TOTP session granted access: %d", rec.Code)
	}
}

func TestLocalLoginFailuresAndLockout(t *testing.T) {
	e, _ := newLocalEnv(t)
	for range 3 {
		if rec := e.do(login("breakglass", "wrong")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong local password: %d", rec.Code)
		}
	}
	if rec := e.do(login("breakglass", "break-glass-password")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("locked local account: %d", rec.Code)
	}
	// LDAP users are unaffected.
	if rec := e.do(login("alice", "secret")); rec.Code != http.StatusOK {
		t.Errorf("LDAP login: %d", rec.Code)
	}
}

func TestLocalAccountAuthenticate(t *testing.T) {
	_, local := newLocalEnv(t)
	if _, err := local.Authenticate(context.Background(), "breakglass", ""); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
		t.Errorf("empty password: %v", err)
	}
	if !local.Matches(" BREAKGLASS ") || local.Matches("alice") {
		t.Error("Matches")
	}
	var none *LocalAccount
	if none.Matches("breakglass") {
		t.Error("nil account matched")
	}
	if _, err := NewLocalAccount("x", "not a hash"); err == nil {
		t.Error("invalid hash accepted")
	}
	if _, err := NewLocalAccount(" ", "$argon2id$..."); err == nil {
		t.Error("empty username accepted")
	}
}
