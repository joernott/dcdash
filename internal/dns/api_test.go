// SPDX-License-Identifier: GPL-3.0-or-later

package dns_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/dns/cloudflare"
	"github.com/joernott/dcdash/internal/dns/cloudflare/cffake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

// Test users and their permissions.
var testUsers = map[string][]string{
	"admin":  {"dns:*:*:admin"},
	"viewer": {"dns:cf:*:read"},
	"editor": {"dns:cf:example.org:write"},
	"other":  {"proxmox:*:*:admin"},
}

type apiEnv struct {
	fake  *cffake.Server
	srv   *httptest.Server
	audit *audit.Log
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	fake := cffake.New("token", "example.org", "example.net")
	fake.Add("example.org", cffake.Record{Name: "www.example.org", Type: "A", Content: "192.0.2.1", TTL: 300})
	cfSrv := httptest.NewServer(fake)
	t.Cleanup(cfSrv.Close)
	p := cloudflare.New(config.DNSProvider{
		Name: "cf", Type: config.DNSTypeCloudflare, Timeout: config.Duration(5 * time.Second),
		Cloudflare: config.DNSCloudflare{APIToken: config.NewSecret("token"), AccountID: "acc", BaseURL: cfSrv.URL + cffake.Prefix},
	}, zerolog.Nop())

	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	svc := dns.NewService([]dns.Provider{p}, auditLog, zerolog.Nop())
	mux := http.NewServeMux()
	svc.Register(mux)
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
	srv := httptest.NewServer(authz.Middleware(inject))
	t.Cleanup(srv.Close)
	return &apiEnv{fake: fake, srv: srv, audit: auditLog}
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

func (e *apiEnv) entries(t *testing.T, action string) []audit.Entry {
	t.Helper()
	l, err := e.audit.Query(context.Background(), audit.Filter{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const base = "/api/v1/dns/providers"

func TestProvidersAndZones(t *testing.T) {
	e := newAPIEnv(t)
	var pl dns.ProviderList
	if code, _ := e.do(t, "viewer", "GET", base, nil, &pl); code != 200 || len(pl.Providers) != 1 ||
		pl.Providers[0].Kind != "cloudflare" || !pl.Providers[0].Capabilities.Proxied || !pl.Providers[0].Capabilities.ZoneCreate {
		t.Errorf("providers: %d %+v", code, pl)
	}
	if code, _ := e.do(t, "other", "GET", base, nil, &pl); code != 200 || len(pl.Providers) != 0 {
		t.Errorf("other: %d %+v", code, pl)
	}
	var zl dns.ZoneList
	if code, _ := e.do(t, "viewer", "GET", base+"/cf/zones", nil, &zl); code != 200 || len(zl.Zones) != 2 {
		t.Errorf("viewer zones: %d %+v", code, zl)
	}
	// Zones are filtered by permission.
	if code, _ := e.do(t, "editor", "GET", base+"/cf/zones", nil, &zl); code != 200 || len(zl.Zones) != 1 || zl.Zones[0].Name != "example.org" {
		t.Errorf("editor zones: %d %+v", code, zl)
	}
	if code, _ := e.do(t, "other", "GET", base+"/cf/zones", nil, nil); code != 403 {
		t.Errorf("other zones: %d", code)
	}
	if code, _ := e.do(t, "editor", "GET", base+"/cf/zones/example.net/records", nil, nil); code != 403 {
		t.Errorf("editor other zone: %d", code)
	}
	if code, _ := e.do(t, "admin", "GET", base+"/nope/zones", nil, nil); code != 404 {
		t.Errorf("unknown provider: %d", code)
	}
}

func TestRecordAPI(t *testing.T) {
	e := newAPIEnv(t)
	path := base + "/cf/zones/example.org/records"
	var rs dns.RecordSet
	if code, _ := e.do(t, "viewer", "GET", path, nil, &rs); code != 200 || len(rs.Records) != 1 {
		t.Fatalf("records: %d %+v", code, rs)
	}
	req := dns.RecordRequest{Name: "api", Type: "A", TTL: 300, Content: "192.0.2.5", Version: rs.Version}
	if code, _ := e.do(t, "viewer", "POST", path, req, nil); code != 403 {
		t.Errorf("viewer create: %d", code)
	}
	var created dns.Record
	if code, msg := e.do(t, "editor", "POST", path, req, &created); code != 201 || created.Name != "api.example.org" {
		t.Fatalf("create: %d %s %+v", code, msg, created)
	}
	if code, c := e.do(t, "editor", "POST", path, req, nil); code != 409 || c != "exists" {
		t.Errorf("duplicate: %d %s", code, c)
	}
	bad := dns.RecordRequest{Name: "x", Type: "A", Content: "not-an-ip"}
	if code, c := e.do(t, "editor", "POST", path, bad, nil); code != 400 || c != "invalid_record" {
		t.Errorf("invalid: %d %s", code, c)
	}
	if code, c := e.do(t, "editor", "POST", path, map[string]any{"name": "x", "unknown": 1}, nil); code != 400 || c != "invalid_request" {
		t.Errorf("unknown field: %d %s", code, c)
	}

	upd := dns.RecordRequest{Name: "api", Type: "A", TTL: 600, Content: "192.0.2.6", Version: created.Version}
	var updated dns.Record
	if code, msg := e.do(t, "editor", "PUT", path+"/"+created.ID, upd, &updated); code != 200 || updated.Content != "192.0.2.6" {
		t.Fatalf("update: %d %s %+v", code, msg, updated)
	}
	if code, c := e.do(t, "editor", "PUT", path+"/"+created.ID, upd, nil); code != 409 || c != "conflict" {
		t.Errorf("stale update: %d %s", code, c)
	}
	if code, _ := e.do(t, "editor", "DELETE", path+"/"+created.ID+"?version="+updated.Version, nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, c := e.do(t, "editor", "DELETE", path+"/"+created.ID+"?version="+updated.Version, nil, nil); code != 404 || c != "not_found" {
		t.Errorf("delete twice: %d %s", code, c)
	}

	if l := e.entries(t, "dns.record.create"); len(l) != 3 || l[0].Target != "cf/example.org" {
		t.Errorf("create audit: %+v", l)
	}
	del := e.entries(t, "dns.record.delete")
	if len(del) != 2 || del[1].Details["content"] != "192.0.2.6" || del[1].Result != audit.ResultSuccess {
		t.Errorf("delete audit: %+v", del)
	}
	updAudit := e.entries(t, "dns.record.update")
	if len(updAudit) == 0 || updAudit[len(updAudit)-1].Result != audit.ResultSuccess {
		t.Fatalf("update audit: %+v", updAudit)
	}
	if prev, _ := updAudit[len(updAudit)-1].Details["previous"].(map[string]any); prev["content"] != "192.0.2.5" {
		t.Errorf("update audit without previous values: %+v", updAudit[len(updAudit)-1].Details)
	}
}

func TestZoneAPI(t *testing.T) {
	e := newAPIEnv(t)
	path := base + "/cf/zones"
	if code, _ := e.do(t, "editor", "POST", path, dns.ZoneRequest{Name: "example.org"}, nil); code != 403 {
		t.Errorf("editor create zone: %d", code)
	}
	var z dns.Zone
	if code, msg := e.do(t, "admin", "POST", path, dns.ZoneRequest{Name: "New.Example"}, &z); code != 201 || z.Name != "new.example" || len(z.NameServers) != 2 {
		t.Fatalf("create zone: %d %s %+v", code, msg, z)
	}
	if code, c := e.do(t, "admin", "POST", path, dns.ZoneRequest{Name: "new.example"}, nil); code != 409 || c != "exists" {
		t.Errorf("create twice: %d %s", code, c)
	}
	if code, c := e.do(t, "admin", "POST", path, dns.ZoneRequest{Name: "*.bad"}, nil); code != 400 || c != "invalid_record" {
		t.Errorf("invalid zone: %d %s", code, c)
	}
	if code, c := e.do(t, "admin", "DELETE", path+"/new.example", nil, nil); code != 400 || c != "confirmation_required" {
		t.Errorf("delete without confirmation: %d %s", code, c)
	}
	if code, _ := e.do(t, "admin", "DELETE", path+"/new.example?confirm=new.example", nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if l := e.entries(t, "dns.zone.create"); len(l) != 2 || l[1].Result != audit.ResultSuccess || l[0].Result != audit.ResultFailure {
		t.Errorf("zone audit: %+v", l)
	}
	if l := e.entries(t, "dns.zone.delete"); len(l) != 1 || l[0].Target != "cf/new.example" {
		t.Errorf("delete audit: %+v", l)
	}
}
