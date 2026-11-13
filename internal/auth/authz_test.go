// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/rbac"
)

func testAuthorizer(t *testing.T, auditLog *audit.Log) *Authorizer {
	t.Helper()
	return NewAuthorizer(rbac.NewResolver("", []rbac.Role{
		{Name: "dns-operator", Groups: []string{"dcdash-dns"}, RequireTOTP: false, Permissions: rbac.Set{rbac.MustParse("dns:cf:*:write")}},
		{Name: "session-viewer", Groups: []string{"dcdash-ops"}, RequireTOTP: false, Permissions: rbac.Set{rbac.PermSessionsRead}},
	}), auditLog)
}

func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// registerTestRoutes adds protected endpoints used by the tests.
func registerTestRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/protected", RequireAuthenticated(http.HandlerFunc(noContent)))
	mux.HandleFunc("GET /api/v1/test/dns/{instance}/{zone}", func(w http.ResponseWriter, r *http.Request) {
		if Check(w, r, rbac.P("dns", r.PathValue("instance"), r.PathValue("zone"), rbac.ActionRead)) {
			noContent(w, r)
		}
	})
	mux.Handle("GET /api/v1/test/audit", Require(rbac.MustParse("system:core:audit:read"), http.HandlerFunc(noContent)))
	mux.Handle("GET /api/v1/test/root", RequireRoot(http.HandlerFunc(noContent)))
}

func TestAuthorization(t *testing.T) {
	e := newEnv(t)
	bob := cookieValue(t, e.do(login("bob", "secret")))
	carol := cookieValue(t, e.do(login("carol", "secret")))
	cases := []struct {
		cookie, path string
		want         int
	}{
		{"", "/api/v1/test/dns/cf/example.com", http.StatusUnauthorized},
		{bob, "/api/v1/test/dns/cf/example.com", http.StatusNoContent},
		{bob, "/api/v1/test/dns/bind/example.com", http.StatusForbidden},
		{bob, "/api/v1/test/audit", http.StatusForbidden},
		{bob, "/api/v1/test/root", http.StatusForbidden},
		{"", "/api/v1/test/root", http.StatusUnauthorized},
		{carol, "/api/v1/protected", http.StatusNoContent},
		{carol, "/api/v1/test/dns/cf/example.com", http.StatusForbidden},
	}
	for _, c := range cases {
		if rec := e.do(call{method: http.MethodGet, path: c.path, cookie: c.cookie}); rec.Code != c.want {
			t.Errorf("%s with %q: %d, want %d", c.path, c.cookie, rec.Code, c.want)
		}
	}
}

func TestMeExposesRolesAndPermissions(t *testing.T) {
	e := newEnv(t)
	bob := cookieValue(t, e.do(login("bob", "secret")))
	rec := e.do(call{method: http.MethodGet, path: "/api/v1/auth/me", cookie: bob})
	var info SessionInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.Roles, []string{"dns-operator"}) || !slices.Equal(info.Permissions, []string{"dns:cf:*:write"}) {
		t.Errorf("me: roles %v, permissions %v", info.Roles, info.Permissions)
	}
	carol := cookieValue(t, e.do(login("carol", "secret")))
	rec = e.do(call{method: http.MethodGet, path: "/api/v1/auth/me", cookie: carol})
	info = SessionInfo{}
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info.Roles == nil || len(info.Roles) != 0 || info.Permissions == nil {
		t.Errorf("user without roles: %+v (must be empty arrays, not null)", info)
	}
}

func TestPendingSessionHasNoPermissions(t *testing.T) {
	e, _ := newLocalEnv(t)
	e.enroll(t, "breakglass")
	rec := e.do(login("breakglass", "break-glass-password"))
	var info SessionInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if len(info.Roles) != 0 || len(info.Permissions) != 0 {
		t.Errorf("pending session exposes roles: %+v", info)
	}
	if rec := e.do(call{method: http.MethodGet, path: "/api/v1/test/root", cookie: cookieValue(t, rec)}); rec.Code != http.StatusUnauthorized {
		t.Errorf("pending session used root: %d", rec.Code)
	}
}
