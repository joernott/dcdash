// SPDX-License-Identifier: GPL-3.0-or-later

package opnsense_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/firewalls/opnsense"
	"github.com/joernott/dcdash/internal/firewalls/opnsense/opnfake"
	"github.com/joernott/dcdash/internal/tlsutil"
)

func newClient(t *testing.T, fake *opnfake.Server, secret string) *opnsense.Client {
	t.Helper()
	hs := httptest.NewServer(fake)
	t.Cleanup(hs.Close)
	c, err := opnsense.New(config.Firewall{Name: "fw1", Type: config.FirewallTypeOPNsense, URL: hs.URL, Insecure: true,
		APIKey: "key", APISecret: config.NewSecret(secret)}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSystemGatewaysServices(t *testing.T) {
	ctx := context.Background()
	fake := opnfake.New("key", "secret")
	fake.Gateways[1].Status = "down"
	c := newClient(t, fake, "secret")
	s, err := c.System(ctx)
	if err != nil || s.Version != "26.1.6" || s.Product != "OPNsense" || s.Uptime == "" || s.MemoryTotal != 8589934592 {
		t.Errorf("%+v %v", s, err)
	}
	gw, err := c.Gateways(ctx)
	if err != nil || len(gw) != 2 || gw[0].Name != "WAN2" || gw[0].Status != opnsense.GatewayDown || gw[0].Delay != "" ||
		gw[1].Status != opnsense.GatewayOnline || gw[1].Delay != "1.2 ms" {
		t.Errorf("%+v %v", gw, err)
	}
	sv, err := c.Services(ctx)
	if err != nil || len(sv) != 3 || sv[0].ID != "configd" || !sv[0].Locked || !sv[1].Running {
		t.Errorf("%+v %v", sv, err)
	}
	if err := c.RestartService(ctx, "unbound"); err != nil || len(fake.Restarted) != 1 {
		t.Errorf("restart: %v %v", err, fake.Restarted)
	}
	// Without the dashboard privilege, uptime and memory stay empty.
	fake.Deny = []string{"/diagnostics/"}
	if s, err := c.System(ctx); err != nil || s.Uptime != "" || s.Version == "" {
		t.Errorf("%+v %v", s, err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	fake := opnfake.New("key", "secret")
	if _, err := newClient(t, fake, "wrong").System(ctx); !errors.Is(err, opnsense.ErrUnauthorized) {
		t.Errorf("wrong secret: %v", err)
	}
	c := newClient(t, fake, "secret")
	fake.Deny = []string{"/routes/"}
	if _, err := c.Gateways(ctx); !errors.Is(err, opnsense.ErrForbidden) {
		t.Errorf("forbidden: %v", err)
	}
	var e *opnsense.Error
	fake.FailNext("GET", "/core/firmware/status", 500)
	if _, err := c.System(ctx); !errors.As(err, &e) || e.Text != "injected failure" {
		t.Errorf("server error: %v", err)
	}
	if err := c.Get(ctx, "/nope/nope/nope", nil); !errors.Is(err, opnsense.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestVPN(t *testing.T) {
	ctx := context.Background()
	fake := opnfake.New("key", "secret")
	c := newClient(t, fake, "secret")
	peers, err := c.Peers(ctx)
	if err != nil || len(peers) != 2 || peers[0].Name != "branch-office" || peers[0].Enabled || peers[0].Status != "" ||
		peers[1].Status != "online" || peers[1].RxBytes != 1200 || peers[1].LatestHandshake.IsZero() || peers[1].Interface != "wg0" {
		t.Fatalf("%+v %v", peers, err)
	}
	if changed, err := c.SetPeerEnabled(ctx, "p-2", true); err != nil || !changed || fake.Applied["wireguard"] != 1 {
		t.Errorf("enable: %v %v", changed, err)
	}
	if changed, err := c.SetPeerEnabled(ctx, "p-2", true); err != nil || changed || fake.Applied["wireguard"] != 1 {
		t.Errorf("enable again: %v %v", changed, err)
	}
	tunnels, err := c.Tunnels(ctx)
	if err != nil || len(tunnels) != 1 || !tunnels[0].Connected || len(tunnels[0].Children) != 1 ||
		tunnels[0].Children[0].State != "INSTALLED" || tunnels[0].BytesOut != 200 {
		t.Fatalf("%+v %v", tunnels, err)
	}
	if err := c.RestartTunnel(ctx, "con1"); err != nil || strings.Join(fake.Restarted, ",") != "disconnect con1,connect con1" {
		t.Errorf("restart: %v %v", err, fake.Restarted)
	}
}

func TestAliases(t *testing.T) {
	ctx := context.Background()
	fake := opnfake.New("key", "secret")
	c := newClient(t, fake, "secret")
	aliases, err := c.Aliases(ctx)
	if err != nil || len(aliases) != 1 || aliases[0].Name != "webservers" || len(aliases[0].Content) != 2 {
		t.Fatalf("%+v %v", aliases, err)
	}
	uuid, err := c.AddAlias(ctx, opnsense.Alias{Name: "dns_servers", Type: "host", Enabled: true, Content: []string{"192.0.2.53"}})
	if err != nil || uuid == "" {
		t.Fatal(uuid, err)
	}
	var ve *opnsense.ValidationError
	if _, err := c.AddAlias(ctx, opnsense.Alias{Name: "dns_servers", Type: "host"}); !errors.As(err, &ve) ||
		!strings.Contains(err.Error(), "name: An alias with this name already exists.") {
		t.Errorf("duplicate: %v", err)
	}
	if err := c.SetAlias(ctx, opnsense.Alias{UUID: uuid, Name: "dns_servers", Type: "host", Content: []string{"192.0.2.53", "192.0.2.54"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyAliases(ctx); err != nil || fake.Applied["firewall"] != 1 {
		t.Errorf("apply: %v", err)
	}
	if err := c.DeleteAlias(ctx, "a-1"); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("in use: %v", err)
	}
	if err := c.DeleteAlias(ctx, uuid); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteAlias(ctx, uuid); !errors.Is(err, opnsense.ErrNotFound) {
		t.Errorf("deleted twice: %v", err)
	}
}

func TestRulesWithSavepoint(t *testing.T) {
	ctx := context.Background()
	fake := opnfake.New("key", "secret")
	fake.RollbackAfter = 200 * time.Millisecond
	fake.CutsOff = func(r opnfake.Rule) bool { return r["action"] == "block" && r["destination_net"] == "(self)" }
	c := newClient(t, fake, "secret")
	rules, err := c.Rules(ctx)
	if err != nil || len(rules) != 2 || !rules[0].Legacy || rules[1].Legacy || rules[1].DestinationPort != "443" || !rules[1].Quick {
		t.Fatalf("%+v %v", rules, err)
	}

	// A kept change: savepoint, apply, cancel the rollback.
	rev, err := c.Savepoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssh := opnsense.Rule{Enabled: true, Action: "pass", Quick: true, Interface: "lan", Direction: "in", IPProtocol: "inet",
		Protocol: "TCP", Source: "lan", Destination: "any", DestinationPort: "22", Description: "ssh"}
	if _, err := c.AddRule(ctx, ssh); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyRules(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelRollback(ctx, rev); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(fake.AppliedRules()) != 2 {
		t.Errorf("kept: %+v", fake.AppliedRules())
	}

	// A rule that cuts off the API is rolled back by the firewall.
	rev, _ = c.Savepoint(ctx)
	if _, err := c.AddRule(ctx, opnsense.Rule{Enabled: true, Action: "block", Interface: "lan", Direction: "in",
		IPProtocol: "inet", Protocol: "any", Source: "any", Destination: "(self)"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyRules(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if _, err := c.System(ctx); !errors.Is(err, opnsense.ErrUnavailable) {
		t.Errorf("not cut off: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := c.System(ctx); err != nil || len(fake.AppliedRules()) != 2 {
		t.Errorf("not rolled back: %v %+v", err, fake.AppliedRules())
	}

	var ve *opnsense.ValidationError
	if _, err := c.AddRule(ctx, opnsense.Rule{Action: "allow", Interface: "lan"}); !errors.As(err, &ve) {
		t.Errorf("invalid: %v", err)
	}
	fake.NoSavepoint = true
	if _, err := c.Savepoint(ctx); !errors.Is(err, opnsense.ErrNoSavepoint) {
		t.Errorf("26.7: %v", err)
	}
}

func TestPinnedCertificate(t *testing.T) {
	fake := opnfake.New("key", "secret")
	hs := httptest.NewTLSServer(fake)
	t.Cleanup(hs.Close)
	c, err := opnsense.New(config.Firewall{Name: "fw1", URL: hs.URL, APIKey: "key", APISecret: config.NewSecret("secret"),
		TLS: config.TLSClient{Fingerprint: tlsutil.Fingerprint(hs.Certificate().Raw)}}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.System(context.Background()); err != nil {
		t.Error(err)
	}
}
