// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/totp"
	"github.com/joernott/dcdash/internal/vault"
)

// fakeDirectory accepts alice (dashroot), bob (dcdash-dns), dave (dcdash-ops) and
// carol (no groups),
// all with password "secret"; "down" simulates an unavailable directory.
type fakeDirectory struct{ calls int }

func (f *fakeDirectory) Authenticate(_ context.Context, username, password string) (*ldapauth.User, error) {
	f.calls++
	switch {
	case username == "down":
		return nil, fmt.Errorf("%w: connection refused", ldapauth.ErrUnavailable)
	case username == "bob" && password == "secret":
		return &ldapauth.User{
			DN: "CN=Bob,OU=Users,DC=example,DC=com", Username: "bob", DisplayName: "Bob",
			Groups: []ldapauth.Group{{DN: "CN=dcdash-dns,OU=Groups,DC=example,DC=com", Name: "dcdash-dns"}},
		}, nil
	case username == "dave" && password == "secret":
		return &ldapauth.User{
			DN: "CN=Dave,OU=Users,DC=example,DC=com", Username: "dave",
			Groups: []ldapauth.Group{{DN: "CN=dcdash-ops,OU=Groups,DC=example,DC=com", Name: "dcdash-ops"}},
		}, nil
	case username == "carol" && password == "secret":
		return &ldapauth.User{DN: "CN=Carol,OU=Users,DC=example,DC=com", Username: "carol"}, nil
	case username == "alice" && password == "secret":
		return &ldapauth.User{
			DN: "CN=Alice,OU=Users,DC=example,DC=com", Username: "alice", DisplayName: "Alice",
			Groups: []ldapauth.Group{{DN: "CN=dashroot,OU=Groups,DC=example,DC=com", Name: "dashroot"}},
		}, nil
	default:
		return nil, ldapauth.ErrInvalidCredentials
	}
}

type env struct {
	h     http.Handler
	dir   *fakeDirectory
	sm    *session.Manager
	audit *audit.Log
	totp  *totp.Store
	// now is the clock of the TOTP store; advance it to get codes of later time steps.
	now time.Time
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith creates a test server; local is the break-glass account or nil.
func newEnvWith(t *testing.T, local *LocalAccount) *env {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kr, err := vault.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{dir: &fakeDirectory{}, now: time.Now()}
	e.sm = session.NewManager(st.DB(), 8*time.Hour, 30*time.Minute, zerolog.Nop())
	e.audit = audit.New(st.DB(), zerolog.Nop())
	e.totp = totp.NewStore(st.DB(), kr)
	e.totp.SetClock(func() time.Time { return e.now })
	authz := testAuthorizer(t, e.audit)
	svc := New(Options{
		LDAP: e.dir, Local: local, Sessions: e.sm, Authorizer: authz, Audit: e.audit, Log: zerolog.Nop(),
		UserLockout: lockout.Policy{MaxFailures: 3, Window: time.Minute, Duration: time.Minute},
		IPLockout:   lockout.Policy{MaxFailures: 5, Window: time.Minute, Duration: time.Minute},
		TOTP:        e.totp, Keyring: kr, TOTPIssuer: "dcdash test",
		TOTPLockout: lockout.Policy{MaxFailures: 3, Window: time.Minute, Duration: time.Minute},
	})
	mux := http.NewServeMux()
	svc.Register(mux)
	registerTestRoutes(mux)
	e.h = e.sm.Middleware(authz.Middleware(session.CSRFExcept(LoginPath)(mux)))
	return e
}

type call struct {
	method, path, body, cookie, csrf, ip string
}

func (e *env) do(c call) *httptest.ResponseRecorder {
	r := httptest.NewRequest(c.method, "https://dcdash.example.com"+c.path, strings.NewReader(c.body))
	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	} else {
		r.ContentLength = 0
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: session.CookieName, Value: c.cookie})
	}
	if c.csrf != "" {
		r.Header.Set(session.CSRFHeader, c.csrf)
	}
	if c.ip != "" {
		r.RemoteAddr = c.ip + ":1234"
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func login(user, pass string) call {
	return call{method: http.MethodPost, path: "/api/v1/auth/login", body: fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass)}
}

func cookieValue(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			return c.Value
		}
	}
	t.Fatal("no session cookie set")
	return ""
}

func TestLoginMeLogout(t *testing.T) {
	e := newEnv(t)
	rec := e.do(login("bob", "secret")) // bob needs no TOTP
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	var info SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.User.Username != "bob" || info.Stage != session.StageAuthenticated || info.CSRFToken == "" ||
		len(info.User.Groups) != 1 || info.User.Groups[0].Name != "dcdash-dns" {
		t.Errorf("login response: %+v", info)
	}
	token := cookieValue(t, rec)

	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/auth/me", cookie: token}); rec.Code != http.StatusOK {
		t.Errorf("me: %d", rec.Code)
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/protected", cookie: token}); rec.Code != http.StatusNoContent {
		t.Errorf("protected: %d", rec.Code)
	}

	// Logout needs the CSRF token.
	if rec := e.do(call{method: http.MethodPost, path: "/api/v1/auth/logout", cookie: token}); rec.Code != http.StatusForbidden {
		t.Errorf("logout without CSRF token: %d", rec.Code)
	}
	if rec := e.do(call{method: http.MethodPost, path: "/api/v1/auth/logout", cookie: token, csrf: info.CSRFToken}); rec.Code != http.StatusNoContent {
		t.Errorf("logout: %d", rec.Code)
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/auth/me", cookie: token}); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout: %d", rec.Code)
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/protected"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("protected without session: %d", rec.Code)
	}
}

func TestLoginFailuresAndUserLockout(t *testing.T) {
	e := newEnv(t)
	for i := range 3 {
		if rec := e.do(login("alice", "wrong")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
	}
	calls := e.dir.calls
	rec := e.do(login("Alice", "secret")) // locked; user key is case-insensitive
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("locked user: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if e.dir.calls != calls {
		t.Error("directory contacted for a locked user")
	}
	if rec := e.do(login("bob", "x")); rec.Code != http.StatusUnauthorized {
		t.Errorf("other user affected by user lockout: %d", rec.Code)
	}
}

func TestIPLockout(t *testing.T) {
	e := newEnv(t)
	for i := range 5 {
		c := login(fmt.Sprintf("user%d", i), "x")
		c.ip = "203.0.113.9"
		e.do(c)
	}
	c := login("alice", "secret")
	c.ip = "203.0.113.9"
	if rec := e.do(c); rec.Code != http.StatusTooManyRequests {
		t.Errorf("locked IP: %d", rec.Code)
	}
	c.ip = "203.0.113.10"
	if rec := e.do(c); rec.Code != http.StatusOK {
		t.Errorf("other IP: %d", rec.Code)
	}
}

func TestDirectoryUnavailable(t *testing.T) {
	e := newEnv(t)
	for range 5 {
		if rec := e.do(login("down", "x")); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("unavailable: %d", rec.Code)
		}
	}
	// Outages do not count as failed attempts.
	if rec := e.do(login("alice", "secret")); rec.Code != http.StatusOK {
		t.Errorf("login after outage: %d", rec.Code)
	}
}

func TestLoginReplacesExistingSession(t *testing.T) {
	e := newEnv(t)
	first := cookieValue(t, e.do(login("alice", "secret")))
	c := login("alice", "secret")
	c.cookie = first
	second := cookieValue(t, e.do(c))
	if first == second {
		t.Fatal("session token reused")
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/auth/me", cookie: first}); rec.Code != http.StatusUnauthorized {
		t.Errorf("old session still valid: %d", rec.Code)
	}
}

func TestBadRequests(t *testing.T) {
	e := newEnv(t)
	for _, body := range []string{`{"username":"alice"`, `{"user":"alice","password":"secret"}`} {
		c := login("", "")
		c.body = body
		if rec := e.do(c); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	// Cross-origin login attempts are rejected by the CSRF middleware.
	r := httptest.NewRequest(http.MethodPost, "https://dcdash.example.com/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"secret"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin login: %d", rec.Code)
	}
}

func TestGroupName(t *testing.T) {
	for dn, want := range map[string]string{
		"CN=dashroot,OU=Groups,DC=example,DC=com": "dashroot",
		`CN=Doe\, John,DC=example,DC=com`:         "Doe, John",
		"not a dn":                                "not a dn",
	} {
		if got := groupName(dn); got != want {
			t.Errorf("groupName(%q) = %q, want %q", dn, got, want)
		}
	}
}

// csrfToken returns the CSRF token from a login or me response.
func csrfToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var info SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || info.CSRFToken == "" {
		t.Fatalf("no CSRF token in %s", rec.Body)
	}
	return info.CSRFToken
}
