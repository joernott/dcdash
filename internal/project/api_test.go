// SPDX-License-Identifier: GPL-3.0-or-later

package project_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
	"github.com/joernott/dcdash/internal/project"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

var testUsers = map[string][]string{
	"admin":  {"project:*:*:admin", "proxmox:*:*:read"},
	"member": {"project:webshop:objects:write", "proxmox:lab:qemu/101:read"},
	"reader": {"project:webshop:objects:read"},
}

type guests struct{}

func (guests) ProjectGuests(_ context.Context, tag string) ([]project.Guest, []error) {
	if tag != "project-webshop" {
		return nil, nil
	}
	return []project.Guest{
		{Cluster: "lab", Type: "qemu", VMID: 101, Name: "web", Node: "pve1", Status: "running"},
		{Cluster: "lab", Type: "qemu", VMID: 102, Name: "db", Node: "pve1", Status: "running"},
	}, []error{errors.New("prod: unreachable")}
}

type env struct {
	srv   *httptest.Server
	fake  *nbfake.Server
	audit *audit.Log
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "webshop.yaml", "title: Webshop\ncolor: 2196f3\n")
	write(t, dir, "intranet.yaml", "")
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	f, nb := nbfake.NewService(t, auditLog)
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "192.0.2.0/24"})
	s := project.New(ctx, config.Projects{Dir: dir}, nb, auditLog, zerolog.Nop())
	s.SetGuestSource(guests{})
	mux := http.NewServeMux()
	s.Register(mux)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), auditLog)
	inject := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if perms, ok := testUsers[r.Header.Get("X-Test-User")]; ok {
			user := r.Header.Get("X-Test-User")
			set, _ := rbac.ParseSet(perms)
			ctx := session.WithSession(r.Context(), &session.Session{ID: "sess-" + user, Username: user, Source: "ldap", Stage: session.StageAuthenticated})
			r = r.WithContext(auth.WithSubject(ctx, &rbac.Subject{Username: user, Permissions: set}))
		}
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(authz.Middleware(inject))
	t.Cleanup(srv.Close)
	return &env{srv: srv, fake: f, audit: auditLog}
}

func (e *env) do(t *testing.T, user, method, path string, body, out any) int {
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
	if res.StatusCode < 300 && out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %s", err, data)
		}
	}
	return res.StatusCode
}

func TestProjectAPI(t *testing.T) {
	e := newEnv(t)
	var list project.List
	if st := e.do(t, "member", "GET", "/api/v1/projects", nil, &list); st != 200 || len(list.Projects) != 1 ||
		list.Projects[0].Name != "webshop" || !list.Projects[0].CanWrite || list.State != nil {
		t.Fatalf("member list: %d %+v", st, list)
	}
	if e.do(t, "admin", "GET", "/api/v1/projects", nil, &list); len(list.Projects) != 2 || list.State == nil {
		t.Errorf("admin list: %+v", list)
	}

	var page project.Page
	if st := e.do(t, "member", "GET", "/api/v1/projects/webshop", nil, &page); st != 200 {
		t.Fatal(st)
	}
	if len(page.Guests) != 2 || !page.Guests[0].Visible || page.Guests[1].Visible || page.Guests[1].Node != "" ||
		len(page.Errors) != 1 || page.NetBox == nil || len(page.NetBox["prefix"]) != 0 {
		t.Errorf("page: %+v", page)
	}
	if st := e.do(t, "member", "GET", "/api/v1/projects/intranet", nil, nil); st != 403 {
		t.Errorf("other project: %d", st)
	}
	if st := e.do(t, "admin", "GET", "/api/v1/projects/unknown", nil, nil); st != 404 {
		t.Errorf("unknown: %d", st)
	}

	var found struct {
		Objects []struct {
			ID      int64  `json:"id"`
			Display string `json:"display"`
		} `json:"objects"`
	}
	if st := e.do(t, "member", "GET", "/api/v1/projects/webshop/search?type=prefix&q=192.0", nil, &found); st != 200 || len(found.Objects) != 1 {
		t.Fatalf("search: %d %+v", st, found)
	}
	if st := e.do(t, "reader", "GET", "/api/v1/projects/webshop/search?type=prefix&q=192.0", nil, nil); st != 403 {
		t.Errorf("reader search: %d", st)
	}
	if st := e.do(t, "member", "POST", "/api/v1/projects/webshop/objects", project.ObjectRequest{Type: "prefix", ID: found.Objects[0].ID}, nil); st != 200 {
		t.Fatalf("add: %d", st)
	}
	e.do(t, "member", "GET", "/api/v1/projects/webshop", nil, &page)
	if len(page.NetBox["prefix"]) != 1 {
		t.Errorf("after add: %+v", page.NetBox)
	}
	if st := e.do(t, "member", "DELETE", "/api/v1/projects/webshop/objects/prefix/"+itoa(found.Objects[0].ID), nil, nil); st != 200 {
		t.Errorf("remove: %d", st)
	}
	if st := e.do(t, "member", "POST", "/api/v1/projects/webshop/objects", project.ObjectRequest{Type: "device", ID: 1}, nil); st != 400 {
		t.Errorf("bad type: %d", st)
	}
	l, _ := e.audit.Query(context.Background(), audit.Filter{Action: project.AuditAssign})
	if len(l) != 1 || l[0].Target != "webshop" || l[0].Details["display"] != "192.0.2.0/24" {
		t.Errorf("audit: %+v", l)
	}
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }
