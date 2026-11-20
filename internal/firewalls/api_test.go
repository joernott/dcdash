// SPDX-License-Identifier: GPL-3.0-or-later

package firewalls_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/firewalls"
	"github.com/joernott/dcdash/internal/firewalls/opnsense/opnfake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/store"
)

var testUsers = map[string][]string{
	"admin":  {"firewall:*:*:admin"},
	"viewer": {"firewall:fw1:*:read"},
	"vpn":    {"firewall:fw1:wireguard/laptop-*:operate", "firewall:fw1:ipsec/*:operate"},
	"netops": {"firewall:fw1:*:read", "firewall:fw1:alias/dns_*:write", "firewall:fw1:rule/lan:write", "firewall:fw1:system:operate"},
	"other":  {"dhcp:*:*:admin"},
}

type apiEnv struct {
	fw1, fw2 *opnfake.Server
	srv      *httptest.Server
	svc      *firewalls.Service
	audit    *audit.Log
}

// newAPIEnv creates fw1 and fw2 (wrong secret in dcdash's configuration). Rule changes are
// rolled back by fw1 after 300 ms unless canceled.
func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := &apiEnv{fw1: opnfake.New("key", "secret"), fw2: opnfake.New("key", "secret")}
	e.fw1.RollbackAfter = 300 * time.Millisecond
	e.fw1.CutsOff = func(r opnfake.Rule) bool { return r["action"] == "block" && r["destination_net"] == "(self)" }
	var cfgs config.Firewalls
	for _, f := range []struct {
		name, secret string
		fake         *opnfake.Server
	}{{"fw1", "secret", e.fw1}, {"fw2", "wrong", e.fw2}} {
		hs := httptest.NewServer(f.fake)
		t.Cleanup(hs.Close)
		cfgs = append(cfgs, config.Firewall{Name: f.name, Type: config.FirewallTypeOPNsense, URL: hs.URL, Insecure: true,
			APIKey: "key", APISecret: config.NewSecret(f.secret)})
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e.audit = audit.New(st.DB(), zerolog.Nop())
	if e.svc, err = firewalls.NewService(cfgs, e.audit, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	e.svc.SetSettle(0)
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

func (e *apiEnv) audited(t *testing.T, action string) []audit.Entry {
	t.Helper()
	l, err := e.audit.Query(context.Background(), audit.Filter{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestListAndHealth(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	var l firewalls.FirewallList
	if st, _ := e.do(t, "admin", "GET", "/api/v1/firewalls", nil, &l); st != 200 || len(l.Firewalls) != 2 {
		t.Fatalf("%d %+v", st, l)
	}
	a, b := l.Firewalls[0], l.Firewalls[1]
	if a.Status != "ok" || a.Version != "26.1.6" || a.Product != "OPNsense" || a.Host != "127.0.0.1" || a.MemoryTotal == 0 {
		t.Errorf("fw1: %+v", a)
	}
	if b.Status != "unavailable" || b.Error != "the firewall rejected dcdash's API key" {
		t.Errorf("fw2: %+v", b)
	}
	if st, _ := e.do(t, "other", "GET", "/api/v1/firewalls", nil, &l); st != 200 || len(l.Firewalls) != 0 {
		t.Errorf("other: %+v", l)
	}

	checks := e.svc.StatusChecks()
	if d, err := checks[0].Detail(ctx); err != nil || d.Code != "firewall.opnsense" || d.Params["version"] != "26.1.6" {
		t.Errorf("check: %+v %v", d, err)
	}
	e.fw1.Lock()
	e.fw1.Gateways[1].Status = "down"
	e.fw1.Unlock()
	var se *status.Error
	if _, err := checks[0].Detail(ctx); !errors.As(err, &se) || se.Code != "firewall.gatewayDown" || se.Params["gateways"] != "WAN2" {
		t.Errorf("gateway down: %v", err)
	}
	if _, err := checks[1].Detail(ctx); !errors.As(err, &se) || se.Code != "firewall.unauthorized" {
		t.Errorf("fw2: %v", err)
	}

	var h firewalls.Health
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/firewalls/fw1/health", nil, &h); st != 200 || len(h.Gateways) != 2 ||
		len(h.Services) != 3 || h.CanOperate {
		t.Errorf("health: %d %+v", st, h)
	}
	if st, _ := e.do(t, "vpn", "GET", "/api/v1/firewalls/fw1/health", nil, nil); st != 403 {
		t.Errorf("vpn health: %d", st)
	}
	if st, _ := e.do(t, "viewer", "POST", "/api/v1/firewalls/fw1/services/restart", firewalls.ServiceRequest{ID: "unbound"}, nil); st != 403 {
		t.Errorf("viewer restart: %d", st)
	}
	if st, _ := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/services/restart", firewalls.ServiceRequest{ID: "unbound"}, nil); st != 204 ||
		len(e.fw1.Restarted) != 1 {
		t.Errorf("restart: %d", st)
	}
	if st, _ := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/services/restart", firewalls.ServiceRequest{ID: "nope"}, nil); st != 404 {
		t.Errorf("unknown service: %d", st)
	}
	if l := e.audited(t, firewalls.AuditServiceRestart); len(l) != 1 || l[0].Target != "fw1/service/unbound" {
		t.Errorf("audit: %+v", l)
	}
}

func TestVPN(t *testing.T) {
	e := newAPIEnv(t)
	var v firewalls.VPN
	if st, _ := e.do(t, "vpn", "GET", "/api/v1/firewalls/fw1/vpn", nil, &v); st != 200 || len(v.Peers) != 1 ||
		v.Peers[0].Name != "laptop-alice" || !v.Peers[0].CanOperate || len(v.Tunnels) != 1 || !v.Tunnels[0].CanOperate {
		t.Fatalf("%d %+v", st, v)
	}
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/firewalls/fw1/vpn", nil, &v); st != 200 || len(v.Peers) != 2 || v.Peers[0].CanOperate {
		t.Errorf("viewer: %+v", v)
	}
	var p firewalls.PeerView
	if st, _ := e.do(t, "vpn", "PUT", "/api/v1/firewalls/fw1/wireguard/p-1", firewalls.PeerUpdate{Enabled: false}, &p); st != 200 ||
		p.Enabled || e.fw1.Applied["wireguard"] != 1 {
		t.Errorf("disable: %d %+v", st, p)
	}
	if st, _ := e.do(t, "vpn", "PUT", "/api/v1/firewalls/fw1/wireguard/p-2", firewalls.PeerUpdate{Enabled: true}, nil); st != 403 {
		t.Errorf("other peer: %d", st)
	}
	if st, _ := e.do(t, "vpn", "POST", "/api/v1/firewalls/fw1/ipsec/con1/restart", nil, nil); st != 204 || len(e.fw1.Restarted) != 2 {
		t.Errorf("restart: %d %v", st, e.fw1.Restarted)
	}
	if st, _ := e.do(t, "vpn", "POST", "/api/v1/firewalls/fw1/ipsec/nope/restart", nil, nil); st != 404 {
		t.Errorf("unknown tunnel: %d", st)
	}
	if l := e.audited(t, firewalls.AuditPeerUpdate); len(l) != 1 || l[0].Target != "fw1/wireguard/laptop-alice" || l[0].Details["changed"] != true {
		t.Errorf("audit: %+v", l)
	}
	if l := e.audited(t, firewalls.AuditTunnelRestart); len(l) != 1 {
		t.Errorf("audit: %+v", l)
	}
	// Without the WireGuard privilege, the tunnels are still shown.
	e.fw1.Deny = []string{"/wireguard/"}
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/firewalls/fw1/vpn", nil, &v); st != 200 || len(v.Peers) != 0 || len(v.Tunnels) != 1 ||
		v.Errors["wireguard"] != "the API user lacks the privilege for this" {
		t.Errorf("no privilege: %+v", v)
	}
}

func TestAliases(t *testing.T) {
	e := newAPIEnv(t)
	var l firewalls.AliasList
	if st, _ := e.do(t, "netops", "GET", "/api/v1/firewalls/fw1/aliases", nil, &l); st != 200 || len(l.Aliases) != 1 ||
		l.Aliases[0].CanWrite || !l.CanCreate {
		t.Fatalf("%d %+v", st, l)
	}
	var a firewalls.AliasView
	req := firewalls.AliasRequest{Name: "dns_servers", Type: "host", Enabled: true, Content: []string{" 192.0.2.53 ", ""}}
	if st, _ := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/aliases", req, &a); st != 201 || a.UUID == "" || len(a.Content) != 1 ||
		e.fw1.Applied["firewall"] != 1 {
		t.Fatalf("create: %d %+v", st, a)
	}
	for _, c := range []struct {
		user string
		body firewalls.AliasRequest
		st   int
		code string
	}{
		{"netops", firewalls.AliasRequest{Name: "web servers", Type: "host"}, 400, "invalid_alias"},
		{"netops", firewalls.AliasRequest{Name: "dns_x", Type: "geoip"}, 400, "invalid_alias"},
		{"netops", firewalls.AliasRequest{Name: "other", Type: "host"}, 403, "forbidden"},
		{"netops", firewalls.AliasRequest{Name: "dns_servers", Type: "host"}, 422, "rejected"},
	} {
		if st, code := e.do(t, c.user, "POST", "/api/v1/firewalls/fw1/aliases", c.body, nil); st != c.st || code != c.code {
			t.Errorf("%+v: %d %s", c.body, st, code)
		}
	}
	req.Content = []string{"192.0.2.53", "192.0.2.54"}
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/firewalls/fw1/aliases/"+a.UUID, req, &a); st != 200 || len(a.Content) != 2 {
		t.Errorf("update: %d %+v", st, a)
	}
	// A rename out of the permitted names is refused.
	req.Name = "resolvers"
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/firewalls/fw1/aliases/"+a.UUID, req, nil); st != 403 {
		t.Errorf("rename: %d", st)
	}
	if st, code := e.do(t, "admin", "DELETE", "/api/v1/firewalls/fw1/aliases/a-1", nil, nil); st != 422 || code != "rejected" {
		t.Errorf("in use: %d %s", st, code)
	}
	if st, _ := e.do(t, "netops", "DELETE", "/api/v1/firewalls/fw1/aliases/"+a.UUID, nil, nil); st != 204 {
		t.Errorf("delete: %d", st)
	}
	if st, _ := e.do(t, "netops", "DELETE", "/api/v1/firewalls/fw1/aliases/"+a.UUID, nil, nil); st != 404 {
		t.Errorf("deleted twice: %d", st)
	}
	for _, act := range []string{firewalls.AuditAliasCreate, firewalls.AuditAliasUpdate, firewalls.AuditAliasDelete} {
		if len(e.audited(t, act)) == 0 {
			t.Errorf("%s not audited", act)
		}
	}
	if l := e.audited(t, firewalls.AuditAliasDelete); len(l) != 2 || l[0].Result != audit.ResultSuccess || l[1].Result != audit.ResultFailure {
		t.Errorf("delete audit: %+v", l)
	}
}

func TestRules(t *testing.T) {
	e := newAPIEnv(t)
	var l firewalls.RuleList
	if st, _ := e.do(t, "netops", "GET", "/api/v1/firewalls/fw1/rules", nil, &l); st != 200 || len(l.Rules) != 2 ||
		l.Rules[0].CanWrite || !l.Rules[0].Legacy || !l.Rules[1].CanWrite || !l.CanCreate {
		t.Fatalf("%d %+v", st, l)
	}
	ssh := firewalls.RuleRequest{Enabled: true, Action: "pass", Quick: true, Interface: "lan", Direction: "in", IPProtocol: "inet",
		Protocol: "TCP", Source: "lan", DestinationPort: "22", Description: "ssh"}
	var res firewalls.RuleResult
	if st, code := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", ssh, &res); st != 200 || !res.Kept || res.Rule == nil {
		t.Fatalf("create: %d %s %+v", st, code, res)
	}
	if res.Rule.Sequence != 20 {
		t.Errorf("sequence after the last rule (10): %d", res.Rule.Sequence)
	}
	time.Sleep(400 * time.Millisecond)
	if len(e.fw1.AppliedRules()) != 2 {
		t.Errorf("kept: %+v", e.fw1.AppliedRules())
	}

	// A rule that locks dcdash out: the rollback runs.
	lockout := firewalls.RuleRequest{Enabled: true, Action: "block", Interface: "lan", Direction: "in", IPProtocol: "inet",
		Destination: "(self)", Description: "oops"}
	if st, _ := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", lockout, &res); st != 200 || res.Kept || !res.RolledBack {
		t.Fatalf("lockout: %d %+v", st, res)
	}
	time.Sleep(400 * time.Millisecond)
	if rules := e.fw1.AppliedRules(); len(rules) != 2 {
		t.Errorf("not rolled back: %+v", rules)
	}
	if l := e.audited(t, firewalls.AuditRuleCreate); len(l) != 2 || l[0].Result != audit.ResultFailure || l[0].Details["rolledBack"] != true {
		t.Errorf("audit: %+v", l)
	}

	for _, c := range []struct {
		body firewalls.RuleRequest
		st   int
		code string
	}{
		{firewalls.RuleRequest{Action: "allow", Interface: "lan", Direction: "in", IPProtocol: "inet"}, 400, "invalid_rule"},
		{firewalls.RuleRequest{Action: "pass", Interface: "lan", Direction: "in", IPProtocol: "inet", DestinationPort: "22"}, 400, "invalid_rule"},
		{firewalls.RuleRequest{Action: "pass", Interface: "wan", Direction: "in", IPProtocol: "inet"}, 403, "forbidden"},
	} {
		if st, code := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", c.body, nil); st != c.st || code != c.code {
			t.Errorf("%+v: %d %s", c.body, st, code)
		}
	}
	ssh.DestinationPort = "2222"
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/firewalls/fw1/rules/r-1", ssh, &res); st != 200 || !res.Kept {
		t.Errorf("update: %d %+v", st, res)
	}
	if st, _ := e.do(t, "netops", "DELETE", "/api/v1/firewalls/fw1/rules/r-1", nil, &res); st != 200 || !res.Kept {
		t.Errorf("delete: %d %+v", st, res)
	}
	if st, _ := e.do(t, "admin", "DELETE", "/api/v1/firewalls/fw1/rules/r-1", nil, nil); st != 404 {
		t.Errorf("deleted twice: %d", st)
	}
	// Without savepoints (OPNsense 26.7), the lockout guard protects the interface dcdash
	// reaches the firewall through (opt1 in the fake); the rules of lan can be changed.
	e.fw1.Lock()
	e.fw1.NoSavepoint = true
	e.fw1.Unlock()
	if st, _ := e.do(t, "netops", "GET", "/api/v1/firewalls/fw1/rules", nil, &l); st != 200 || !l.NoSavepoint || !l.CanCreate ||
		len(l.ProtectedInterfaces) != 1 || l.ProtectedInterfaces[0] != "opt1" || !l.Rules[1].CanWrite {
		t.Errorf("rules without savepoints: %d %+v", st, l)
	}
	ssh.Description = "ssh without savepoint"
	if st, code := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", ssh, &res); st != 200 || !res.Kept {
		t.Errorf("without savepoint: %d %s %+v", st, code, res)
	}
	mgmt := ssh
	mgmt.Interface = "opt1"
	if st, code := e.do(t, "admin", "POST", "/api/v1/firewalls/fw1/rules", mgmt, nil); st != 409 || code != "protected_rule" {
		t.Errorf("protected interface: %d %s", st, code)
	}
	// A disabled rule changes nothing.
	mgmt.Enabled = false
	if st, code := e.do(t, "admin", "POST", "/api/v1/firewalls/fw1/rules", mgmt, &res); st != 200 || !res.Kept {
		t.Errorf("disabled rule on the protected interface: %d %s", st, code)
	}
	if !slices.ContainsFunc(e.audited(t, firewalls.AuditRuleCreate), func(a audit.Entry) bool {
		return a.Result == audit.ResultFailure && strings.Contains(fmt.Sprint(a.Details["error"]), "reaches the firewall through opt1")
	}) {
		t.Error("refused change not audited")
	}
	// A rule that cuts dcdash off anyway (the fake's CutsOff) is reported, not undone.
	cut := firewalls.RuleRequest{Enabled: true, Action: "block", Quick: true, Interface: "lan", Direction: "in", IPProtocol: "inet",
		Destination: "(self)", Description: "cut"}
	if st, code := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", cut, &res); st != 200 || !res.LockedOut || res.Kept {
		t.Errorf("locked out: %d %s %+v", st, code, res)
	}
	if !slices.ContainsFunc(e.audited(t, firewalls.AuditRuleCreate), func(a audit.Entry) bool { return a.Details["lockedOut"] == true }) {
		t.Error("lockout not audited")
	}
}

func TestRulesGuardUnknown(t *testing.T) {
	e := newAPIEnv(t)
	e.fw1.Lock()
	e.fw1.NoSavepoint, e.fw1.Routes = true, nil
	e.fw1.Unlock()
	var l firewalls.RuleList
	if st, _ := e.do(t, "netops", "GET", "/api/v1/firewalls/fw1/rules", nil, &l); st != 200 || l.GuardError == "" || l.CanCreate ||
		slices.ContainsFunc(l.Rules, func(r firewalls.RuleView) bool { return r.CanWrite }) {
		t.Errorf("%d %+v", st, l)
	}
	ssh := firewalls.RuleRequest{Enabled: true, Action: "pass", Interface: "lan", Direction: "in", IPProtocol: "inet"}
	if st, code := e.do(t, "netops", "POST", "/api/v1/firewalls/fw1/rules", ssh, nil); st != 409 || code != "guard_unknown" {
		t.Errorf("guard unknown: %d %s", st, code)
	}
}
