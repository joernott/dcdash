// SPDX-License-Identifier: GPL-3.0-or-later

package directories_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories"
	"github.com/joernott/dcdash/internal/directories/adfake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

const (
	labOU   = "OU=Lab,DC=ad,DC=test"
	usersCN = "CN=Users,DC=ad,DC=test"
)

var testUsers = map[string][]string{
	"admin":    {"directory:*:*:admin"},
	"viewer":   {"directory:lab:*:read"},
	"helpdesk": {"directory:lab:*:read", "directory:lab:user/*:operate"},
	"labops":   {"directory:lab:*:read", "directory:lab:user/lab-*:write", "directory:lab:group/lab-*:write"},
	"other":    {"dhcp:*:*:admin"},
}

type apiEnv struct {
	lab   *adfake.Directory
	corp  *adfake.Directory
	srv   *httptest.Server
	svc   *directories.Service
	audit *audit.Log
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := &apiEnv{
		lab: adfake.New(config.Directory{Name: "lab", Type: config.DirectoryTypeSamba, Domain: "ad.test", BaseDN: "DC=ad,DC=test",
			ManagedOUs: []string{labOU}}),
		corp: adfake.New(config.Directory{Name: "corp", Type: config.DirectoryTypeWindows, Domain: "corp.test", BaseDN: "DC=corp,DC=test",
			ManagedOUs: []string{"DC=corp,DC=test"}}),
	}
	e.lab.AddUser(usersCN, "Administrator", "Admin-1234", true)
	e.lab.AddUser(usersCN, "outside", "Outside-1234", false)
	e.lab.AddUser(labOU, "lab-alice", "Alice-1234", false)
	e.lab.AddUser(labOU, "lab-root", "Root-12345", true)
	e.lab.AddGroup(usersCN, "Domain Admins", true)
	e.lab.AddGroup(labOU, "lab-admins", false)
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.audit = audit.New(st.DB(), zerolog.Nop())
	e.svc = directories.NewServiceWith([]directories.Backend{e.lab, e.corp}, e.audit, zerolog.Nop())
	mux := http.NewServeMux()
	e.svc.Register(mux)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), e.audit)
	inject := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if perms, ok := testUsers[r.Header.Get("X-Test-User")]; ok {
			user := r.Header.Get("X-Test-User")
			set, _ := rbac.ParseSet(perms)
			ctx := session.WithSession(r.Context(), &session.Session{ID: "sess-" + user, Username: user, Source: "ldap", Stage: session.StageAuthenticated})
			ctx = auth.WithSubject(ctx, &rbac.Subject{Username: user, Permissions: set})
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
	e.srv = httptest.NewServer(authz.Middleware(inject))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *apiEnv) do(t *testing.T, user, method, path string, body, out any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("X-Test-User", user)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var eb struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &eb)
		return res.StatusCode, eb.Error.Code
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	return res.StatusCode, ""
}

// lastAudit returns the newest audit entry.
func (e *apiEnv) lastAudit(t *testing.T) audit.Entry {
	t.Helper()
	entries, err := e.audit.Query(context.Background(), audit.Filter{Limit: 1})
	if err != nil || len(entries) == 0 {
		t.Fatalf("audit: %v %v", entries, err)
	}
	return entries[0]
}

func TestDirectoriesList(t *testing.T) {
	e := newAPIEnv(t)
	var out struct {
		Directories []directories.DirectoryView `json:"directories"`
	}
	e.do(t, "admin", "GET", "/api/v1/directories", nil, &out)
	if len(out.Directories) != 2 || out.Directories[0].Name != "lab" || !out.Directories[0].CanCreateUsers {
		t.Errorf("admin: %+v", out)
	}
	e.do(t, "labops", "GET", "/api/v1/directories", nil, &out)
	if len(out.Directories) != 1 || !out.Directories[0].CanCreateUsers || !out.Directories[0].CanCreateGroups {
		t.Errorf("labops: %+v", out)
	}
	e.do(t, "helpdesk", "GET", "/api/v1/directories", nil, &out)
	if len(out.Directories) != 1 || out.Directories[0].CanCreateUsers {
		t.Errorf("helpdesk: %+v", out)
	}
	e.do(t, "other", "GET", "/api/v1/directories", nil, &out)
	if len(out.Directories) != 0 {
		t.Errorf("other: %+v", out)
	}
	if code, _ := e.do(t, "other", "GET", "/api/v1/directories/lab/users", nil, nil); code != http.StatusNotFound {
		t.Errorf("other sees lab users: %d", code)
	}
	if code, _ := e.do(t, "admin", "GET", "/api/v1/directories/nope/users", nil, nil); code != http.StatusNotFound {
		t.Errorf("unknown directory: %d", code)
	}
}

func TestUsers(t *testing.T) {
	e := newAPIEnv(t)
	var list struct {
		Users     []directories.UserView `json:"users"`
		Truncated bool                   `json:"truncated"`
	}
	e.do(t, "labops", "GET", "/api/v1/directories/lab/users", nil, &list)
	can := map[string]bool{}
	for _, u := range list.Users {
		can[u.Name] = u.CanWrite
	}
	// lab-root is protected, outside and Administrator aren't managed.
	if len(list.Users) != 4 || !can["lab-alice"] || can["lab-root"] || can["outside"] || can["Administrator"] {
		t.Errorf("labops list: %+v", list)
	}
	e.do(t, "admin", "GET", "/api/v1/directories/lab/users?q=lab-&limit=1", nil, &list)
	if len(list.Users) != 1 || !list.Truncated {
		t.Errorf("query and limit: %+v", list)
	}

	// Create: validation, permission, managed container, policy, conflict.
	create := map[string]any{"ou": labOU, "name": "lab-bob", "givenName": "Bob", "password": "Bob-12345", "mustChangePassword": true}
	for _, c := range []struct {
		user   string
		change map[string]any
		status int
		code   string
	}{
		{"labops", map[string]any{"name": "lab bob."}, http.StatusBadRequest, "invalid_user"},
		{"labops", map[string]any{"name": "lab-bob-with-a-very-long-name"}, http.StatusBadRequest, "invalid_user"},
		{"labops", map[string]any{"mail": "not a mail"}, http.StatusBadRequest, "invalid_user"},
		{"labops", map[string]any{"password": ""}, http.StatusBadRequest, "invalid_user"},
		{"labops", map[string]any{"ou": usersCN}, http.StatusBadRequest, "invalid_user"},
		{"labops", map[string]any{"name": "bob"}, http.StatusForbidden, "forbidden"},
		{"helpdesk", nil, http.StatusForbidden, "forbidden"},
		{"labops", map[string]any{"password": "short"}, http.StatusUnprocessableEntity, "password_policy"},
		{"labops", map[string]any{"name": "lab-alice"}, http.StatusConflict, "exists"},
	} {
		body := map[string]any{}
		for k, v := range create {
			body[k] = v
		}
		for k, v := range c.change {
			body[k] = v
		}
		if code, ec := e.do(t, c.user, "POST", "/api/v1/directories/lab/users", body, nil); code != c.status || ec != c.code {
			t.Errorf("create %v as %s = %d %s, want %d %s", c.change, c.user, code, ec, c.status, c.code)
		}
	}
	var u directories.UserView
	if code, ec := e.do(t, "labops", "POST", "/api/v1/directories/lab/users", create, &u); code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, ec)
	}
	if u.Name != "lab-bob" || !u.MustChangePassword || !u.CanWrite || e.lab.UserMap["lab-bob"].Password != "Bob-12345" {
		t.Errorf("created: %+v", u)
	}
	if a := e.lastAudit(t); a.Action != directories.AuditUserCreate || a.Target != "lab/user/lab-bob" || strings.Contains(fmt.Sprint(a.Details), "Bob-12345") {
		t.Errorf("audit: %+v", a)
	}

	// Change.
	if code, _ := e.do(t, "labops", "PATCH", "/api/v1/directories/lab/users/lab-bob", map[string]any{"mail": "bob@ad.test", "surname": "Builder"}, &u); code != http.StatusOK || u.Mail != "bob@ad.test" || u.Surname != "Builder" {
		t.Errorf("update = %d %+v", code, u)
	}
	if code, _ := e.do(t, "labops", "PATCH", "/api/v1/directories/lab/users/lab-bob", map[string]any{"upn": ""}, nil); code != http.StatusBadRequest {
		t.Errorf("empty upn: %d", code)
	}
	for _, c := range []struct {
		user, name string
		status     int
	}{
		{"labops", "lab-root", http.StatusForbidden}, // protected
		{"labops", "outside", http.StatusForbidden},  // not managed, and no permission
		{"admin", "outside", http.StatusForbidden},   // not managed
		{"viewer", "lab-bob", http.StatusForbidden},
		{"labops", "lab-nobody", http.StatusNotFound},
	} {
		if code, _ := e.do(t, c.user, "PATCH", "/api/v1/directories/lab/users/"+c.name, map[string]any{"description": "x"}, nil); code != c.status {
			t.Errorf("update %s as %s = %d, want %d", c.name, c.user, code, c.status)
		}
	}
	if code, _ := e.do(t, "admin", "PATCH", "/api/v1/directories/lab/users/lab-root", map[string]any{"description": "x"}, nil); code != http.StatusOK {
		t.Errorf("admin changes a protected user: %d", code)
	}

	// Operate: helpdesk may disable, unlock and reset, but not change.
	e.lab.UserMap["lab-alice"].LockedOut = true
	for _, op := range []string{"disable", "unlock"} {
		if code, ec := e.do(t, "helpdesk", "POST", "/api/v1/directories/lab/users/lab-alice/"+op, nil, &u); code != http.StatusOK {
			t.Errorf("%s = %d %s", op, code, ec)
		}
	}
	if !u.Disabled || u.LockedOut {
		t.Errorf("after disable and unlock: %+v", u)
	}
	e.do(t, "helpdesk", "POST", "/api/v1/directories/lab/users/lab-alice/enable", nil, &u)
	if u.Disabled {
		t.Errorf("not enabled")
	}
	if code, _ := e.do(t, "helpdesk", "POST", "/api/v1/directories/lab/users/lab-alice/password", map[string]any{"password": "New-12345", "mustChangePassword": true}, &u); code != http.StatusOK || !u.MustChangePassword || e.lab.UserMap["lab-alice"].Password != "New-12345" {
		t.Errorf("reset = %d %+v", code, u)
	}
	if a := e.lastAudit(t); a.Action != directories.AuditUserPassword || strings.Contains(fmt.Sprint(a.Details), "New-12345") {
		t.Errorf("audit of reset: %+v", a)
	}
	if code, _ := e.do(t, "helpdesk", "POST", "/api/v1/directories/lab/users/lab-root/password", map[string]any{"password": "New-12345"}, nil); code != http.StatusForbidden {
		t.Errorf("helpdesk resets a protected user: %d", code)
	}
	if code, _ := e.do(t, "helpdesk", "DELETE", "/api/v1/directories/lab/users/lab-alice", nil, nil); code != http.StatusForbidden {
		t.Errorf("helpdesk deletes: %d", code)
	}
	if code, ec := e.do(t, "helpdesk", "POST", "/api/v1/directories/lab/users/lab-alice/password", map[string]any{"password": "x"}, nil); code != http.StatusUnprocessableEntity || ec != "password_policy" {
		t.Errorf("reset against the policy = %d %s", code, ec)
	}

	// Delete.
	if code, _ := e.do(t, "labops", "DELETE", "/api/v1/directories/lab/users/lab-bob", nil, nil); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
	if _, ok := e.lab.UserMap["lab-bob"]; ok {
		t.Error("not deleted")
	}

	// The directory is down.
	e.lab.Down = true
	if code, ec := e.do(t, "admin", "GET", "/api/v1/directories/lab/users", nil, nil); code != http.StatusBadGateway || ec != "upstream_unavailable" {
		t.Errorf("down = %d %s", code, ec)
	}
}

func TestGroups(t *testing.T) {
	e := newAPIEnv(t)
	var g directories.GroupView
	if code, ec := e.do(t, "labops", "POST", "/api/v1/directories/lab/groups", map[string]any{"ou": labOU, "name": "lab-devs", "scope": "global", "security": true, "description": "Developers"}, &g); code != http.StatusCreated || !g.CanWrite || g.Scope != "global" {
		t.Fatalf("create = %d %s %+v", code, ec, g)
	}
	for _, c := range []struct {
		body   map[string]any
		status int
	}{
		{map[string]any{"ou": labOU, "name": "lab-x", "scope": "forest"}, http.StatusBadRequest},
		{map[string]any{"ou": labOU, "name": "devs"}, http.StatusForbidden},
		{map[string]any{"ou": labOU, "name": "lab-devs"}, http.StatusConflict},
	} {
		if code, _ := e.do(t, "labops", "POST", "/api/v1/directories/lab/groups", c.body, nil); code != c.status {
			t.Errorf("create %v = %d, want %d", c.body, code, c.status)
		}
	}
	if code, _ := e.do(t, "labops", "POST", "/api/v1/directories/lab/groups/lab-devs/members", map[string]any{"add": []string{"lab-alice", "lab-admins"}}, &g); code != http.StatusOK || len(g.Members) != 2 {
		t.Errorf("add members = %d %+v", code, g.Members)
	}
	if code, ec := e.do(t, "labops", "POST", "/api/v1/directories/lab/groups/lab-devs/members", map[string]any{"add": []string{"nobody"}}, nil); code != http.StatusNotFound {
		t.Errorf("unknown member = %d %s", code, ec)
	}
	if code, _ := e.do(t, "labops", "POST", "/api/v1/directories/lab/groups/lab-devs/members", map[string]any{}, nil); code != http.StatusBadRequest {
		t.Errorf("empty change = %d", code)
	}
	var u directories.UserView
	e.do(t, "viewer", "GET", "/api/v1/directories/lab/users/lab-alice", nil, &u)
	if len(u.Groups) != 1 || !strings.HasPrefix(u.Groups[0], "CN=lab-devs,") || u.CanOperate {
		t.Errorf("user groups: %+v", u)
	}
	// Domain Admins is protected: labops can't, admin can't either (not managed).
	for _, user := range []string{"labops", "admin"} {
		if code, _ := e.do(t, user, "POST", "/api/v1/directories/lab/groups/Domain%20Admins/members", map[string]any{"add": []string{"lab-alice"}}, nil); code != http.StatusForbidden {
			t.Errorf("%s changes Domain Admins: %d", user, code)
		}
	}
	// Change and rename; a rename needs write on the new name.
	if code, _ := e.do(t, "labops", "PATCH", "/api/v1/directories/lab/groups/lab-devs", map[string]any{"name": "devs"}, nil); code != http.StatusForbidden {
		t.Errorf("rename outside the permission: %d", code)
	}
	if code, ec := e.do(t, "labops", "PATCH", "/api/v1/directories/lab/groups/lab-devs", map[string]any{"scope": "domainLocal"}, nil); code != http.StatusUnprocessableEntity || ec != "rejected" {
		t.Errorf("global to domain local = %d %s", code, ec)
	}
	g = directories.GroupView{}
	if code, _ := e.do(t, "labops", "PATCH", "/api/v1/directories/lab/groups/lab-devs", map[string]any{"name": "lab-developers", "scope": "universal", "description": ""}, &g); code != http.StatusOK || g.Name != "lab-developers" || g.Scope != "universal" || g.Description != "" || len(g.Members) != 2 {
		t.Errorf("update = %d %+v", code, g)
	}
	if a := e.lastAudit(t); a.Action != directories.AuditGroupUpdate || a.Target != "lab/group/lab-devs" {
		t.Errorf("audit: %+v", a)
	}
	var list struct {
		Groups []directories.GroupView `json:"groups"`
	}
	e.do(t, "viewer", "GET", "/api/v1/directories/lab/groups?q=lab", nil, &list)
	if len(list.Groups) != 2 || list.Groups[0].CanWrite || list.Groups[0].Members != nil {
		t.Errorf("list: %+v", list)
	}
	if code, _ := e.do(t, "labops", "DELETE", "/api/v1/directories/lab/groups/lab-developers", nil, nil); code != http.StatusNoContent {
		t.Errorf("delete = %d", code)
	}
}

func TestContainersAndPolicy(t *testing.T) {
	e := newAPIEnv(t)
	var cs struct {
		Containers []struct {
			DN, Name string
		} `json:"containers"`
	}
	if e.do(t, "viewer", "GET", "/api/v1/directories/lab/containers", nil, &cs); len(cs.Containers) != 1 || cs.Containers[0].DN != labOU {
		t.Errorf("containers: %+v", cs)
	}
	var p struct {
		MinLength int `json:"minLength"`
	}
	if e.do(t, "viewer", "GET", "/api/v1/directories/lab/policy", nil, &p); p.MinLength != 7 {
		t.Errorf("policy: %+v", p)
	}
}

func TestStatusChecks(t *testing.T) {
	e := newAPIEnv(t)
	checks := e.svc.StatusChecks()
	if len(checks) != 2 || checks[0].Kind != directories.Module || checks[0].Name != "lab" {
		t.Fatalf("checks: %+v", checks)
	}
	if d, err := checks[0].Detail(context.Background()); err != nil || d.Code != "ad.connected" {
		t.Errorf("lab: %+v %v", d, err)
	}
	e.lab.Down = true
	if _, err := checks[0].Detail(context.Background()); err == nil {
		t.Error("down directory is ok")
	}
}
