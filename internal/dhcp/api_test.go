// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dhcp"
	"github.com/joernott/dcdash/internal/dhcp/kea/keafake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

// Test users and their permissions.
var testUsers = map[string][]string{
	"admin":  {"dhcp:*:*:admin"},
	"viewer": {"dhcp:kea4-a:*:read"},
	"netops": {"dhcp:kea4-a:192.0.2.0/24:write"},
	"other":  {"proxmox:*:*:admin"},
}

type apiEnv struct {
	a, b, v6 *keafake.Server
	srv      *httptest.Server
	audit    *audit.Log
}

func subnets4() []keafake.Subnet {
	return []keafake.Subnet{
		{ID: 1, Prefix: "192.0.2.0/24", Pools: []string{"192.0.2.100-192.0.2.199"}, Total: 100},
		{ID: 2, Prefix: "198.51.100.0/24", Pools: []string{"198.51.100.10-198.51.100.19"}, Total: 10},
	}
}

// newAPIEnv creates kea4-a and kea4-b (an HA pair) and kea6. setup changes
// the fakes before the service probes them.
func newAPIEnv(t *testing.T, setup func(e *apiEnv)) *apiEnv {
	t.Helper()
	e := &apiEnv{
		a:  keafake.New("v4", "dcdash", "pw", subnets4()...),
		b:  keafake.New("v4", "dcdash", "pw", subnets4()...),
		v6: keafake.New("v6", "dcdash", "pw", keafake.Subnet{ID: 1, Prefix: "2001:db8:1::/64", Pools: []string{"2001:db8:1::1000-2001:db8:1::1fff"}, Total: 4096}),
	}
	for _, f := range []*keafake.Server{e.a, e.b} {
		f.AddLease(keafake.Lease{IP: "192.0.2.100", HWAddress: "11:22:33:44:55:01", Hostname: "laptop", SubnetID: 1})
		f.AddLease(keafake.Lease{IP: "198.51.100.10", HWAddress: "11:22:33:44:55:01", Hostname: "laptop", SubnetID: 2})
		f.AddConfigReservation(keafake.Host{"subnet-id": 1, "hw-address": "aa:bb:cc:00:00:01", "ip-address": "192.0.2.10", "hostname": "printer"})
	}
	e.b.Set(func(s *keafake.Settings) { s.Hooks = append(s.Hooks, "ha") })
	if setup != nil {
		setup(e)
	}
	var cfgs []config.DHCPServer
	for _, s := range []struct {
		name, family, group string
		fake                *keafake.Server
	}{{"kea4-a", "v4", "site1", e.a}, {"kea4-b", "v4", "site1", e.b}, {"kea6", "v6", "", e.v6}} {
		hs := httptest.NewServer(s.fake)
		t.Cleanup(hs.Close)
		cfgs = append(cfgs, config.DHCPServer{Name: s.name, Family: s.family, URL: hs.URL, Insecure: true,
			Username: "dcdash", Password: config.NewSecret("pw"), HAGroup: s.group})
	}

	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.audit = audit.New(st.DB(), zerolog.Nop())
	svc, err := dhcp.NewService(cfgs, e.audit, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.Register(mux)
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

func (e *apiEnv) entries(t *testing.T, action string) []audit.Entry {
	t.Helper()
	l, err := e.audit.Query(context.Background(), audit.Filter{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const base = "/api/v1/dhcp/servers"

func TestServersAndSubnets(t *testing.T) {
	e := newAPIEnv(t, nil)
	var sl dhcp.ServerList
	if code, _ := e.do(t, "admin", "GET", base, nil, &sl); code != 200 || len(sl.Servers) != 3 {
		t.Fatalf("servers: %d %+v", code, sl)
	}
	a, b := sl.Servers[0], sl.Servers[1]
	if a.Status != "ok" || a.Version != "3.0.4" || a.HAGroup != "site1" || len(a.Partners) != 1 || a.Partners[0] != "kea4-b" ||
		!a.Capabilities.ReservationWrite || len(a.SearchKeys) != 4 || len(a.HA) != 0 {
		t.Errorf("kea4-a: %+v", a)
	}
	if len(b.HA) != 1 || b.HA[0].State != "hot-standby" || !b.HA[0].InTouch || sl.Servers[2].Family != "v6" {
		t.Errorf("kea4-b / kea6: %+v %+v", b, sl.Servers[2])
	}
	if code, _ := e.do(t, "viewer", "GET", base, nil, &sl); code != 200 || len(sl.Servers) != 1 || sl.Servers[0].Name != "kea4-a" {
		t.Errorf("viewer servers: %d %+v", code, sl)
	}
	if code, _ := e.do(t, "other", "GET", base, nil, &sl); code != 200 || len(sl.Servers) != 0 {
		t.Errorf("other servers: %d %+v", code, sl)
	}

	var subs dhcp.SubnetList
	if code, _ := e.do(t, "viewer", "GET", base+"/kea4-a/subnets", nil, &subs); code != 200 || len(subs.Subnets) != 2 ||
		subs.Subnets[0].Stats == nil || subs.Subnets[0].Stats.Assigned != 1 || subs.Subnets[0].Pools[0] != "192.0.2.100-192.0.2.199" {
		t.Errorf("viewer subnets: %d %+v", code, subs)
	}
	if code, _ := e.do(t, "netops", "GET", base+"/kea4-a/subnets", nil, &subs); code != 200 || len(subs.Subnets) != 1 {
		t.Errorf("netops subnets: %d %+v", code, subs)
	}
	if code, _ := e.do(t, "viewer", "GET", base+"/kea4-b/subnets", nil, nil); code != 403 {
		t.Errorf("viewer kea4-b: %d", code)
	}
	if code, _ := e.do(t, "admin", "GET", base+"/nope/subnets", nil, nil); code != 404 {
		t.Errorf("unknown server: %d", code)
	}
}

func TestUnavailableServer(t *testing.T) {
	e := newAPIEnv(t, func(e *apiEnv) { e.v6.Set(func(s *keafake.Settings) { s.Unavailable = true }) })
	var sl dhcp.ServerList
	if code, _ := e.do(t, "admin", "GET", base, nil, &sl); code != 200 || sl.Servers[2].Status != "unavailable" ||
		sl.Servers[2].Error != "server not reachable" || sl.Servers[0].Status != "ok" {
		t.Errorf("servers: %d %+v", code, sl)
	}
	if code, c := e.do(t, "admin", "GET", base+"/kea6/subnets", nil, nil); code != 502 || c != "upstream_unavailable" {
		t.Errorf("subnets: %d %s", code, c)
	}

	// Without hooks, the features are unsupported.
	e = newAPIEnv(t, func(e *apiEnv) { e.v6.Set(func(s *keafake.Settings) { s.Hooks = nil }) })
	if code, c := e.do(t, "admin", "GET", base+"/kea6/leases?by=ip&q=2001:db8:1::5", nil, nil); code != 409 || c != "unsupported" {
		t.Errorf("leases without lease_cmds: %d %s", code, c)
	}
	if code, c := e.do(t, "admin", "GET", base+"/kea6/reservations", nil, nil); code != 409 || c != "unsupported" {
		t.Errorf("reservations without host_cmds: %d %s", code, c)
	}
	// Subnets then come from config-get, without pools or statistics.
	var subs dhcp.SubnetList
	if code, _ := e.do(t, "admin", "GET", base+"/kea6/subnets", nil, &subs); code != 200 || len(subs.Subnets) != 1 ||
		subs.Subnets[0].Prefix != "2001:db8:1::/64" || subs.Subnets[0].Stats != nil || len(subs.Subnets[0].Pools) != 0 {
		t.Errorf("subnets without hooks: %d %+v", code, subs)
	}
}

func TestLeases(t *testing.T) {
	e := newAPIEnv(t, nil)
	var ll dhcp.LeaseList
	if code, _ := e.do(t, "viewer", "GET", base+"/kea4-a/leases?subnet=1", nil, &ll); code != 200 || len(ll.Leases) != 1 ||
		ll.Leases[0].Subnet != "192.0.2.0/24" || ll.Leases[0].State != "assigned" || ll.Leases[0].CanDelete {
		t.Errorf("subnet leases: %d %+v", code, ll)
	}
	if code, _ := e.do(t, "viewer", "GET", base+"/kea4-a/leases?by=hw-address&q=11:22:33:44:55:01", nil, &ll); code != 200 || len(ll.Leases) != 2 {
		t.Errorf("viewer search: %d %+v", code, ll)
	}
	// Search results are filtered by subnet permission.
	if code, _ := e.do(t, "netops", "GET", base+"/kea4-a/leases?by=hostname&q=laptop", nil, &ll); code != 200 || len(ll.Leases) != 1 || !ll.Leases[0].CanDelete {
		t.Errorf("netops search: %d %+v", code, ll)
	}
	if code, _ := e.do(t, "netops", "GET", base+"/kea4-a/leases?subnet=2", nil, nil); code != 403 {
		t.Errorf("netops subnet 2: %d", code)
	}
	if code, c := e.do(t, "viewer", "GET", base+"/kea4-a/leases?by=duid&q=00:01", nil, nil); code != 400 || c != "invalid_request" {
		t.Errorf("v4 by DUID: %d %s", code, c)
	}
	if code, _ := e.do(t, "viewer", "GET", base+"/kea4-a/leases", nil, nil); code != 400 {
		t.Errorf("no query: %d", code)
	}

	// Delete: operate on the lease's subnet; the HA partner loses the lease too.
	if code, _ := e.do(t, "viewer", "DELETE", base+"/kea4-a/leases/192.0.2.100", nil, nil); code != 403 {
		t.Errorf("viewer delete: %d", code)
	}
	if code, _ := e.do(t, "netops", "DELETE", base+"/kea4-a/leases/198.51.100.10", nil, nil); code != 403 {
		t.Errorf("netops delete in subnet 2: %d", code)
	}
	var dr dhcp.LeaseDeleteResult
	if code, _ := e.do(t, "netops", "DELETE", base+"/kea4-a/leases/192.0.2.100", nil, &dr); code != 200 ||
		len(dr.Partners) != 1 || dr.Partners[0].Server != "kea4-b" || !dr.Partners[0].Done {
		t.Errorf("delete: %d %+v", code, dr)
	}
	if code, c := e.do(t, "netops", "DELETE", base+"/kea4-a/leases/192.0.2.100", nil, nil); code != 404 || c != "not_found" {
		t.Errorf("delete twice: %d %s", code, c)
	}
	var lb dhcp.LeaseList
	if e.do(t, "admin", "GET", base+"/kea4-b/leases?subnet=1", nil, &lb); len(lb.Leases) != 0 {
		t.Errorf("partner still has the lease: %+v", lb)
	}
	l := e.entries(t, "dhcp.lease.delete")
	if len(l) != 1 || l[0].Target != "kea4-a/192.0.2.0/24" || l[0].Details["ip"] != "192.0.2.100" || l[0].Details["partners"] == nil {
		t.Errorf("audit: %+v", l)
	}
}

func TestReservations(t *testing.T) {
	e := newAPIEnv(t, nil)
	path := base + "/kea4-a/reservations"
	var rl dhcp.ReservationList
	if code, _ := e.do(t, "viewer", "GET", path, nil, &rl); code != 200 || len(rl.Reservations) != 1 ||
		!rl.Reservations[0].ReadOnly || rl.Reservations[0].Source != "config" || rl.Reservations[0].IPAddresses[0] != "192.0.2.10" {
		t.Fatalf("list: %d %+v", code, rl)
	}
	req := dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "hw-address", Identifier: "AA-BB-CC-00-00-02", IPAddresses: []string{"192.0.2.11"}, Hostname: "Scanner"}
	if code, _ := e.do(t, "viewer", "POST", path, req, nil); code != 403 {
		t.Errorf("viewer create: %d", code)
	}
	var created dhcp.ReservationView
	if code, c := e.do(t, "netops", "POST", path, req, &created); code != 201 || created.Identifier != "aa:bb:cc:00:00:02" ||
		created.Hostname != "scanner" || created.ReadOnly || created.Source != "database" {
		t.Fatalf("create: %d %s %+v", code, c, created)
	}
	if code, c := e.do(t, "netops", "POST", path, req, nil); code != 422 || c != "rejected" {
		t.Errorf("duplicate: %d %s", code, c)
	}
	for _, bad := range []dhcp.ReservationRequest{
		{SubnetID: 1, IdentifierType: "hw-address", Identifier: "zz", IPAddresses: []string{"192.0.2.12"}},
		{SubnetID: 1, IdentifierType: "duid", Identifier: "00:01", IPAddresses: []string{"192.0.2.12"}},
		{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:03", IPAddresses: []string{"198.51.100.5"}},
		{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:03", IPAddresses: []string{"192.0.2.12", "192.0.2.13"}},
		{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:03", Hostname: "bad_name"},
		{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:03"},
	} {
		if code, c := e.do(t, "netops", "POST", path, bad, nil); code != 400 || c != "invalid_reservation" {
			t.Errorf("invalid %+v: %d %s", bad, code, c)
		}
	}
	if code, _ := e.do(t, "netops", "POST", path, dhcp.ReservationRequest{SubnetID: 2, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:03", IPAddresses: []string{"198.51.100.5"}}, nil); code != 403 {
		t.Errorf("netops subnet 2: %d", code)
	}

	upd := req
	upd.Hostname, upd.Version = "scanner-2", created.Version
	var updated dhcp.ReservationView
	if code, c := e.do(t, "netops", "PUT", path+"/"+created.ID, upd, &updated); code != 200 || updated.Hostname != "scanner-2" || updated.Version == created.Version {
		t.Fatalf("update: %d %s %+v", code, c, updated)
	}
	if code, c := e.do(t, "netops", "PUT", path+"/"+created.ID, upd, nil); code != 409 || c != "conflict" {
		t.Errorf("stale update: %d %s", code, c)
	}
	moved := upd
	moved.Version, moved.Identifier = updated.Version, "aa:bb:cc:00:00:09"
	if code, c := e.do(t, "netops", "PUT", path+"/"+created.ID, moved, nil); code != 400 || c != "invalid_reservation" {
		t.Errorf("identifier change: %d %s", code, c)
	}
	cfgID := rl.Reservations[0].ID
	if code, c := e.do(t, "admin", "PUT", path+"/"+cfgID, dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:01", Hostname: "x", Version: rl.Reservations[0].Version}, nil); code != 409 || c != "read_only" {
		t.Errorf("update config reservation: %d %s", code, c)
	}
	if code, c := e.do(t, "netops", "DELETE", path+"/"+created.ID+"?version="+created.Version, nil, nil); code != 409 || c != "conflict" {
		t.Errorf("stale delete: %d %s", code, c)
	}
	if code, _ := e.do(t, "netops", "DELETE", path+"/"+created.ID+"?version="+updated.Version, nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code, c := e.do(t, "netops", "DELETE", path+"/"+created.ID+"?version="+updated.Version, nil, nil); code != 404 || c != "not_found" {
		t.Errorf("delete twice: %d %s", code, c)
	}
	if code, c := e.do(t, "netops", "DELETE", path+"/not-an-id?version=x", nil, nil); code != 400 || c != "invalid_request" {
		t.Errorf("bad ID: %d %s", code, c)
	}

	if l := e.entries(t, "dhcp.reservation.create"); len(l) != 2 || l[0].Target != "kea4-a/192.0.2.0/24" {
		t.Errorf("create audit: %+v", l)
	}
	if l := e.entries(t, "dhcp.reservation.update"); len(l) != 1 || l[0].Details["previous"] == nil {
		t.Errorf("update audit: %+v", l)
	}
	if l := e.entries(t, "dhcp.reservation.delete"); len(l) != 1 || l[0].Details["hostname"] != "scanner-2" {
		t.Errorf("delete audit: %+v", l)
	}

	// v6 reservations with prefixes.
	p6 := base + "/kea6/reservations"
	r6 := dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "duid", Identifier: "0003000100112233", IPAddresses: []string{"2001:db8:1::11"}, Prefixes: []string{"2001:db8:8000:200::/56"}}
	var c6 dhcp.ReservationView
	if code, c := e.do(t, "admin", "POST", p6, r6, &c6); code != 201 || c6.Identifier != "00:03:00:01:00:11:22:33" || len(c6.Prefixes) != 1 {
		t.Errorf("v6 create: %d %s %+v", code, c, c6)
	}
}

// Without a host database on the requested server, reservation changes go
// to its HA partner, which shares the host database.
func TestReservationWriteTarget(t *testing.T) {
	e := newAPIEnv(t, func(e *apiEnv) { e.a.Set(func(s *keafake.Settings) { s.HostDatabase = false }) })
	req := dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:05", IPAddresses: []string{"192.0.2.15"}}
	if code, c := e.do(t, "netops", "POST", base+"/kea4-a/reservations", req, nil); code != 201 {
		t.Fatalf("create via partner: %d %s", code, c)
	}
	var rl dhcp.ReservationList
	if e.do(t, "admin", "GET", base+"/kea4-b/reservations", nil, &rl); len(rl.Reservations) != 2 {
		t.Errorf("partner reservations: %+v", rl)
	}
	if l := e.entries(t, "dhcp.reservation.create"); len(l) != 1 || l[0].Details["server"] != "kea4-b" {
		t.Errorf("audit: %+v", l)
	}
	// kea6 has no partner.
	e2 := newAPIEnv(t, func(e *apiEnv) { e.v6.Set(func(s *keafake.Settings) { s.HostDatabase = false }) })
	r6 := dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "duid", Identifier: "00:01:02", IPAddresses: []string{"2001:db8:1::11"}}
	if code, c := e2.do(t, "admin", "POST", base+"/kea6/reservations", r6, nil); code != 409 || c != "unsupported" {
		t.Errorf("no host database: %d %s", code, c)
	}
}
