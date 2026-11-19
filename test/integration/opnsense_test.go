// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/firewalls/opnsense"
)

// TestOPNsense reads from a real OPNsense firewall (DCDASH_IT_OPN_URL, _FINGERPRINT, _KEY,
// _SECRET). With DCDASH_IT_OPN_WRITE=1 it also creates and deletes a test alias and a
// disabled test rule on the interface DCDASH_IT_OPN_RULE_IF (default lan), each rule change
// with a savepoint. With DCDASH_IT_OPN_ROLLBACK=1 it checks that OPNsense rolls back a change
// whose rollback isn't canceled (about 70 seconds). Disabled rules don't change the traffic.
func TestOPNsense(t *testing.T) {
	rawURL := os.Getenv("DCDASH_IT_OPN_URL")
	if rawURL == "" {
		t.Skip("DCDASH_IT_OPN_URL not set")
	}
	ctx := context.Background()
	c, err := opnsense.New(config.Firewall{Name: "it", Type: config.FirewallTypeOPNsense, URL: rawURL,
		TLS:    config.TLSClient{Fingerprint: os.Getenv("DCDASH_IT_OPN_FINGERPRINT")},
		APIKey: requireEnv(t, "DCDASH_IT_OPN_KEY"), APISecret: config.NewSecret(requireEnv(t, "DCDASH_IT_OPN_SECRET"))},
		zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	sys, err := c.System(ctx)
	if err != nil || sys.Version == "" {
		t.Fatalf("system: %+v %v", sys, err)
	}
	t.Logf("%s %s, uptime %q, memory %d/%d", sys.Product, sys.Version, sys.Uptime, sys.MemoryUsed, sys.MemoryTotal)
	gws, err := c.Gateways(ctx)
	t.Logf("gateways: %+v %v", gws, err)
	svcs, err := c.Services(ctx)
	t.Logf("%d services, error %v", len(svcs), err)
	peers, err := c.Peers(ctx)
	t.Logf("WireGuard: %d peers, error %v", len(peers), err)
	for _, p := range peers {
		t.Logf("  peer %s enabled %v status %q handshake %s rx %d tx %d", p.Name, p.Enabled, p.Status, p.LatestHandshake, p.RxBytes, p.TxBytes)
	}
	tunnels, err := c.Tunnels(ctx)
	t.Logf("IPsec: %d tunnels, error %v", len(tunnels), err)
	for _, tn := range tunnels {
		t.Logf("  tunnel %s %q connected %v children %+v", tn.Name, tn.Description, tn.Connected, tn.Children)
	}
	aliases, err := c.Aliases(ctx)
	if err != nil {
		t.Fatalf("aliases: %v", err)
	}
	rules, err := c.Rules(ctx)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	legacy := 0
	for _, r := range rules {
		if r.Legacy {
			legacy++
		}
	}
	t.Logf("%d aliases, %d rules (%d read-only)", len(aliases), len(rules), legacy)

	if os.Getenv("DCDASH_IT_OPN_WRITE") != "1" {
		return
	}
	suffix := strconv.FormatInt(time.Now().Unix()%100000, 10)
	name := "dcdash_it_" + suffix
	uuid, err := c.AddAlias(ctx, opnsense.Alias{Name: name, Type: "host", Enabled: true, Content: []string{"192.0.2.1"},
		Description: "dcdash integration test"})
	if err != nil {
		t.Fatalf("add alias: %v", err)
	}
	t.Cleanup(func() {
		if err := c.DeleteAlias(ctx, uuid); err != nil {
			t.Errorf("delete alias: %v", err)
		}
		_ = c.ApplyAliases(ctx)
	})
	if err := c.SetAlias(ctx, opnsense.Alias{UUID: uuid, Name: name, Type: "host", Enabled: true,
		Content: []string{"192.0.2.1", "192.0.2.2"}}); err != nil {
		t.Errorf("set alias: %v", err)
	}
	if err := c.ApplyAliases(ctx); err != nil {
		t.Errorf("apply aliases: %v", err)
	}
	aliases, _ = c.Aliases(ctx)
	if !slices.ContainsFunc(aliases, func(a opnsense.Alias) bool { return a.Name == name && len(a.Content) == 2 }) {
		t.Errorf("alias not changed: %+v", aliases)
	}

	iface := os.Getenv("DCDASH_IT_OPN_RULE_IF")
	if iface == "" {
		iface = "lan"
	}
	test := opnsense.Rule{Enabled: false, Sequence: 999999, Action: "pass", Quick: true, Interface: iface, Direction: "in",
		IPProtocol: "inet", Protocol: "TCP", Source: "192.0.2.1", Destination: "192.0.2.2", DestinationPort: "9",
		Description: "dcdash integration test " + suffix}
	rev, err := c.Savepoint(ctx)
	if err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	ruleUUID, err := c.AddRule(ctx, test)
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}
	if err := c.ApplyRules(ctx, rev); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := c.Reachable(ctx); err != nil {
		t.Fatalf("not reachable: %v", err)
	}
	if err := c.CancelRollback(ctx, rev); err != nil {
		t.Fatalf("cancel rollback: %v", err)
	}
	if os.Getenv("DCDASH_IT_OPN_ROLLBACK") == "1" {
		// Delete the rule, but let OPNsense roll the deletion back.
		rev, err := c.Savepoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteRule(ctx, ruleUUID); err != nil {
			t.Fatal(err)
		}
		if err := c.ApplyRules(ctx, rev); err != nil {
			t.Fatal(err)
		}
		time.Sleep(70 * time.Second)
		rules, _ := c.Rules(ctx)
		if !slices.ContainsFunc(rules, func(r opnsense.Rule) bool { return r.UUID == ruleUUID }) {
			t.Errorf("the deletion was not rolled back")
		}
	}
	rev, err = c.Savepoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRule(ctx, ruleUUID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if err := c.ApplyRules(ctx, rev); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelRollback(ctx, rev); err != nil {
		t.Errorf("cancel rollback: %v", err)
	}
}
