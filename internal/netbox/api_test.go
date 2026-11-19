// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

// Test users and their permissions.
var testUsers = map[string][]string{
	"admin":  {"netbox:*:*:admin"},
	"viewer": {"netbox:netbox:ipam:read"},
	"syncer": {"netbox:netbox:sync:write"},
	"other":  {"proxmox:*:*:admin"},
}

type apiEnv struct {
	fake  *nbfake.Server
	srv   *httptest.Server
	audit *audit.Log
	svc   *netbox.Service
	cfg   config.NetBox
}

// seed adds a tag, the cluster "Lab", VLANs 100 and 150 with prefixes, and
// used addresses.
func seed(f *nbfake.Server) {
	f.Seed(nbfake.Tags, nbfake.Object{"name": "dcdash", "slug": "dcdash"})
	f.Seed(nbfake.Clusters, nbfake.Object{"name": "Lab"})
	servers := f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(100), "name": "servers", "description": "Server network"})
	f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(150), "name": "storage"})
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "2001:db8:1::/64", "vlan": servers})
	f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "192.0.2.0/24", "vlan": servers})
	for _, a := range []string{"192.0.2.1/24", "192.0.2.2/24", "192.0.2.10/24"} {
		f.Seed(nbfake.IPs, nbfake.Object{"address": a, "status": "active"})
	}
}

// newAPIEnv creates the service against a seeded fake. change adjusts the
// configuration.
func newAPIEnv(t *testing.T, change func(*config.NetBox)) *apiEnv {
	t.Helper()
	e := &apiEnv{fake: nbfake.New(testToken)}
	seed(e.fake)
	hs := httptest.NewServer(e.fake)
	t.Cleanup(hs.Close)
	e.cfg = config.NetBox{Name: "netbox", URL: hs.URL, Insecure: true, Token: config.NewSecret(testToken),
		WriteBack: true, Tag: "dcdash", OnDelete: config.NetBoxDeleteTagged, Clusters: map[string]string{"lab": "Lab"}}
	if change != nil {
		change(&e.cfg)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.audit = audit.New(st.DB(), zerolog.Nop())
	e.svc, err = netbox.NewService(e.cfg, e.audit, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
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

// do sends a request and decodes a successful response into out. It returns
// the status and the error code of a failed request.
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
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	return res.StatusCode, ""
}

func TestInfo(t *testing.T) {
	e := newAPIEnv(t, nil)
	var info netbox.Info
	if st, _ := e.do(t, "other", "GET", "/api/v1/netbox", nil, &info); st != 200 || info.Name != "netbox" || !info.WriteBack ||
		len(info.Clusters) != 1 || info.Clusters[0] != "lab" {
		t.Errorf("%d %+v", st, info)
	}
	if st, _ := e.do(t, "nobody", "GET", "/api/v1/netbox", nil, nil); st != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", st)
	}
}

func TestVLAN(t *testing.T) {
	e := newAPIEnv(t, nil)
	var v netbox.VLANView
	if st, code := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/100", nil, &v); st != 200 {
		t.Fatal(st, code)
	}
	if v.Name != "servers" || v.Description != "Server network" || len(v.Prefixes) != 2 ||
		v.Prefixes[0].Prefix != "192.0.2.0/24" || len(v.Prefixes[0].Free) != 8 || v.Prefixes[0].Free[0] != "192.0.2.3/24" || v.Prefixes[0].Free[6] != "192.0.2.9/24" || v.Prefixes[0].Free[7] != "192.0.2.11/24" || v.Prefixes[0].Family != 4 ||
		v.Prefixes[1].Free[0] != "2001:db8:1::1/64" || v.Prefixes[1].Family != 6 {
		t.Errorf("%+v", v)
	}
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/150", nil, &v); st != 200 || v.Name != "storage" || len(v.Prefixes) != 0 {
		t.Errorf("150: %d %+v", st, v)
	}
	if st, code := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/999", nil, nil); st != 404 || code != "not_found" {
		t.Errorf("unknown VLAN: %d %s", st, code)
	}
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/5000", nil, nil); st != 400 {
		t.Errorf("invalid VLAN: %d", st)
	}
	if st, _ := e.do(t, "syncer", "GET", "/api/v1/netbox/vlans/100", nil, nil); st != 403 {
		t.Errorf("without ipam:read: %d", st)
	}
	l, _ := e.audit.Query(context.Background(), audit.Filter{Action: "authz.denied"})
	if len(l) != 1 || !strings.Contains(l[0].Target, "netbox:netbox:ipam:read") {
		t.Errorf("audit: %+v", l)
	}
	e.fake.FailNext("GET", nbfake.VLANs, 500)
	if st, code := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/100", nil, nil); st != 502 || code != "upstream_unavailable" {
		t.Errorf("NetBox down: %d %s", st, code)
	}

	// The VLAN group must exist.
	e = newAPIEnv(t, func(c *config.NetBox) { c.VLANGroup = "dc1" })
	if st, code := e.do(t, "viewer", "GET", "/api/v1/netbox/vlans/100", nil, nil); st != 502 || code != "upstream_error" {
		t.Errorf("missing group: %d %s", st, code)
	}
}

func TestStatusCheck(t *testing.T) {
	e := newAPIEnv(t, nil)
	checks := e.svc.StatusChecks()
	d, err := checks[0].Detail(context.Background())
	if err != nil || d.Code != "netbox.connected" || d.Params["version"] != nbfake.Version || checks[0].Kind != "netbox" {
		t.Errorf("%+v %v", d, err)
	}
	for _, c := range []struct {
		change func(*config.NetBox)
		want   string
	}{
		{func(c *config.NetBox) { c.Tag = "other" }, `the tag "other" doesn't exist`},
		{func(c *config.NetBox) { c.VRF = "internal" }, `the VRF "internal" doesn't exist`},
		{func(c *config.NetBox) { c.Clusters = map[string]string{"lab": "Prod"} }, `the cluster "Prod" doesn't exist`},
		{func(c *config.NetBox) { c.Token = config.NewSecret("nbt_x.y") }, "Invalid v2 token"},
	} {
		e := newAPIEnv(t, c.change)
		if _, err := e.svc.StatusChecks()[0].Detail(context.Background()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("want %q, got %v", c.want, err)
		}
	}
	// Without write-back the tag isn't needed.
	e = newAPIEnv(t, func(c *config.NetBox) { c.Tag = "other"; c.WriteBack = false })
	if _, err := e.svc.StatusChecks()[0].Detail(context.Background()); err != nil {
		t.Error(err)
	}
}
