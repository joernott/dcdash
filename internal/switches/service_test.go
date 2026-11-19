// SPDX-License-Identifier: GPL-3.0-or-later

package switches_test

import (
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
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/switches"
	"github.com/joernott/dcdash/internal/switches/aoscx/cxfake"
)

// Test users and their permissions.
var testUsers = map[string][]string{
	"admin":  {"switch:*:*:admin"},
	"viewer": {"switch:sw1:*:read"},
	"other":  {"dhcp:*:*:admin"},
}

type apiEnv struct {
	sw1, sw2 *cxfake.Server
	srv      *httptest.Server
	svc      *switches.Service
}

// newAPIEnv creates sw1 and sw2 (wrong password in dcdash's configuration).
func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := &apiEnv{sw1: cxfake.New("dcdash", "pw"), sw2: cxfake.New("dcdash", "pw")}
	e.sw2.Hostname = "sw-two"
	var cfgs config.Switches
	for _, s := range []struct {
		name, password string
		fake           *cxfake.Server
	}{{"sw1", "pw", e.sw1}, {"sw2", "wrong", e.sw2}} {
		hs := httptest.NewServer(s.fake)
		t.Cleanup(hs.Close)
		cfgs = append(cfgs, config.Switch{Name: s.name, Type: config.SwitchTypeAOSCX, URL: hs.URL, Insecure: true,
			Username: "dcdash", Password: config.NewSecret(s.password)})
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	if e.svc, err = switches.NewService(cfgs, auditLog, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	e.svc.Register(mux)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), auditLog)
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

func (e *apiEnv) get(t *testing.T, user, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	req.Header.Set("X-Test-User", user)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusOK && out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %v: %s", path, err, data)
		}
	}
	return res.StatusCode
}

func TestSwitchList(t *testing.T) {
	e := newAPIEnv(t)
	var l switches.SwitchList
	if st := e.get(t, "admin", "/api/v1/switches", &l); st != 200 || len(l.Switches) != 2 {
		t.Fatalf("%d %+v", st, l)
	}
	a, b := l.Switches[0], l.Switches[1]
	if a.Name != "sw1" || a.Status != "ok" || a.Hostname != "sw-test" || a.Model != "6000 24G 4SFP Swch" ||
		a.Firmware != "PL.10.08.1010" || a.APIVersion != "v10.08" || a.Booted == nil || len(a.Members) != 1 || a.Type != "aoscx" {
		t.Errorf("sw1: %+v", a)
	}
	if b.Name != "sw2" || b.Status != "unavailable" || b.Error != "the switch rejected dcdash's credentials" || b.Members == nil {
		t.Errorf("sw2: %+v", b)
	}
	// The inventory is cached: a second list does not log in or read again.
	requests := len(e.sw1.Requests())
	if st := e.get(t, "viewer", "/api/v1/switches", &l); st != 200 || len(l.Switches) != 1 || l.Switches[0].Name != "sw1" {
		t.Errorf("viewer: %d %+v", st, l)
	}
	if len(e.sw1.Requests()) != requests || e.sw1.Logins() != 1 {
		t.Errorf("not cached: %v", e.sw1.Requests())
	}
	if st := e.get(t, "other", "/api/v1/switches", &l); st != 200 || len(l.Switches) != 0 {
		t.Errorf("other: %d %+v", st, l)
	}
	if st := e.get(t, "nobody", "/api/v1/switches", nil); st != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", st)
	}
}

func TestStatusChecksAndClose(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	checks := e.svc.StatusChecks()
	if len(checks) != 2 || checks[0].Kind != switches.Module || checks[0].Name != "sw1" {
		t.Fatalf("%+v", checks)
	}
	d, err := checks[0].Detail(ctx)
	if err != nil || d.Code != "switch.aoscx" || d.Text != "6000 24G 4SFP Swch sw-test, AOS-CX PL.10.08.1010" ||
		d.Params["hostname"] != "sw-test" {
		t.Errorf("sw1: %+v %v", d, err)
	}
	var se *status.Error
	if _, err := checks[1].Detail(ctx); !errors.As(err, &se) || se.Code != "switch.unauthorized" {
		t.Errorf("sw2: %v", err)
	}
	if err := e.svc.Close(); err != nil || e.sw1.Sessions() != 0 {
		t.Errorf("close: %v, %d sessions", err, e.sw1.Sessions())
	}
}
