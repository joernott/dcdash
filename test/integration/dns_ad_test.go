// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/dns/addns"
)

// adDNS returns an AD DNS provider for a target, its zone for record tests and the name
// of a zone it may create.
func adDNS(t *testing.T, target adTarget, server, resolver, zone string) (*addns.Provider, string, string) {
	t.Helper()
	p := config.DNSProvider{Name: target.cfg.Name + "-dns", Type: config.DNSTypeAD,
		AD: config.ADDNS{Directory: target.cfg.Name, Server: server, Resolver: resolver, ManageZones: true}}
	cfg := target.cfg
	cfg.Kerberos.Realm = strings.ToUpper(cfg.Domain)
	if len(cfg.Kerberos.KDCs) == 0 {
		u, err := url.Parse(cfg.Servers[0].URL)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Kerberos.KDCs = []string{net.JoinHostPort(u.Hostname(), "88")}
	}
	target.cfg = cfg
	return addns.New(p, target.client(t), zerolog.New(zerolog.NewTestWriter(t))), zone, target.prefix + "zone.test"
}

func TestADDNSSamba(t *testing.T) {
	target := sambaTarget(t)
	p, zone, newZone := adDNS(t, target, "dc1."+target.cfg.Domain, strings.TrimPrefix(target.cfg.Servers[0].URL, "ldaps://"), "lab.dcdash.test")
	testADDNS(t, p, target, zone, newZone)
}

func TestADDNSLab(t *testing.T) {
	target := labTarget(t)
	server := requireEnv(t, "DCDASH_IT_AD_DNS_SERVER")
	p, zone, newZone := adDNS(t, target, server, "", target.cfg.Domain)
	testADDNS(t, p, target, zone, newZone)
}

func testADDNS(t *testing.T, p *addns.Provider, target adTarget, zone, newZone string) {
	ctx := context.Background()
	if d, err := p.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	} else {
		t.Logf("check: %s", d.Text)
	}
	zones, err := p.Zones(ctx)
	if err != nil || !slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == zone }) {
		t.Fatalf("Zones = %v, %v", zones, err)
	}
	name := target.prefix + "a1." + zone
	cleanup := func() {
		rs, err := p.Records(ctx, zone)
		if err != nil {
			return
		}
		for _, r := range rs.Records {
			if r.Name == name {
				_ = p.DeleteRecord(ctx, zone, r.ID, rs.Version)
				rs, _ = p.Records(ctx, zone)
			}
		}
		_ = p.DeleteZone(ctx, newZone)
	}
	cleanup()
	t.Cleanup(cleanup)

	rs, err := p.Records(ctx, zone)
	if err != nil || len(rs.Records) < 2 || rs.Records[0].Type != "SOA" || !rs.Records[0].ReadOnly {
		t.Fatalf("Records = %+v, %v", rs, err)
	}
	if !slices.ContainsFunc(rs.Records, func(r dns.Record) bool { return r.Type == "NS" }) {
		t.Errorf("no NS record in %+v", rs.Records)
	}

	r, err := p.CreateRecord(ctx, zone, dns.Record{Name: name, Type: "A", TTL: 300, Content: "192.0.2.10"}, rs.Version)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if _, err := p.CreateRecord(ctx, zone, dns.Record{Name: name, Type: "A", Content: "192.0.2.10"}, rs.Version); !errors.Is(err, dns.ErrConflict) && !errors.Is(err, dns.ErrExists) {
		t.Errorf("stale version or duplicate: %v", err)
	}
	rs2, err := p.Records(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("version %s -> %s", rs.Version, rs2.Version)
	if rs2.Version == rs.Version {
		t.Errorf("the version didn't change with the update")
	}
	if !slices.ContainsFunc(rs2.Records, func(x dns.Record) bool { return x.ID == r.ID && x.TTL == 300 }) {
		t.Errorf("created record not listed: %+v", r)
	}
	txt, err := p.CreateRecord(ctx, zone, dns.Record{Name: name, Type: "TXT", Content: `"dcdash test" "second string"`}, rs2.Version)
	if err != nil {
		t.Fatalf("CreateRecord TXT: %v", err)
	}
	rs3, _ := p.Records(ctx, zone)
	u, err := p.UpdateRecord(ctx, zone, r.ID, dns.Record{Name: name, Type: "A", TTL: 600, Content: "192.0.2.11"}, rs3.Version)
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	rs4, _ := p.Records(ctx, zone)
	if !slices.ContainsFunc(rs4.Records, func(x dns.Record) bool { return x.ID == txt.ID && x.Content == txt.Content }) {
		t.Errorf("TXT not listed as created: %+v", txt)
	}
	for _, id := range []string{u.ID, txt.ID} {
		if err := p.DeleteRecord(ctx, zone, id, rs4.Version); err != nil {
			t.Fatalf("DeleteRecord: %v", err)
		}
		rs4, _ = p.Records(ctx, zone)
	}
	if slices.ContainsFunc(rs4.Records, func(x dns.Record) bool { return x.Name == name }) {
		t.Errorf("records left: %+v", rs4.Records)
	}

	// Zones.
	if err := p.DeleteZone(ctx, target.cfg.Domain); err == nil {
		t.Errorf("the domain's zone can be deleted")
	}
	if _, err := p.CreateZone(ctx, newZone); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	zs, err := p.Records(ctx, newZone)
	if err != nil || len(zs.Records) != 2 {
		t.Fatalf("records of the new zone = %+v, %v", zs, err)
	}
	if _, err := p.CreateRecord(ctx, newZone, dns.Record{Name: "www." + newZone, Type: "A", Content: "192.0.2.12"}, zs.Version); err != nil {
		t.Errorf("record in the new zone: %v", err)
	}
	if err := p.DeleteZone(ctx, newZone); err != nil {
		t.Fatalf("DeleteZone: %v", err)
	}
	if zones, _ := p.Zones(ctx); slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == newZone }) {
		t.Errorf("zone not deleted")
	}
}
