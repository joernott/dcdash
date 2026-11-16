// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/dns/bind"
)

// Tests against the bind9 containers dns-primary and dns-secondary (see
// test/integration/compose.yaml and scripts/integration-test.sh).

const (
	staticZone  = "static.dcdash.test"
	catalogZone = "catalog.dcdash.test"
)

func bindConfig(t *testing.T) config.DNSProvider {
	t.Helper()
	primary := requireEnv(t, "DCDASH_IT_BIND_PRIMARY")
	cfg := config.DNSProvider{
		Name: "bind-it",
		Type: config.DNSTypeBind,
		Bind: config.Bind{
			RFC2136: config.RFC2136{
				Nameserver:    primary,
				TSIGKey:       "dcdash.",
				TSIGSecret:    config.NewSecret(requireEnv(t, "DCDASH_IT_BIND_TSIG_SECRET")),
				TSIGAlgorithm: "hmac-sha256",
			},
			Zones:       []string{staticZone},
			CatalogZone: catalogZone,
			RNDC: config.RNDC{
				Binary:  requireEnv(t, "DCDASH_IT_RNDC"),
				Server:  primary,
				Port:    953,
				KeyFile: requireEnv(t, "DCDASH_IT_BIND_RNDC_KEY"),
			},
			SFTP: config.SFTP{
				Address:        primary + ":22",
				User:           "dcdash",
				PrivateKeyFile: requireEnv(t, "DCDASH_IT_BIND_SFTP_KEY"),
				HostKey:        requireEnv(t, "DCDASH_IT_BIND_SFTP_HOSTKEY"),
				ZoneDir:        "/zones",
			},
			NewZones: config.NewZones{
				FileDir: "/srv/sftp/zones",
				Options: "update-policy { grant dcdash. zonesub ANY; }; allow-transfer { key dcdash.; 10.77.0.55; }; also-notify { 10.77.0.55; };",
				TTL:     config.Duration(time.Hour),
				SOA: config.SOA{
					MName: "ns1.dcdash.test", RName: "hostmaster.dcdash.test",
					Refresh: config.Duration(time.Hour), Retry: config.Duration(10 * time.Minute),
					Expire: config.Duration(24 * time.Hour), Minimum: config.Duration(time.Minute),
				},
				NameServers: []string{"ns1.dcdash.test"},
			},
		},
		Timeout: config.Duration(10 * time.Second),
	}
	return cfg
}

func bindProvider(t *testing.T) *bind.Provider {
	t.Helper()
	cfg := bindConfig(t)
	files, err := bind.NewSFTP(cfg.Bind.SFTP, cfg.RequestTimeout())
	if err != nil {
		t.Fatal(err)
	}
	p, err := bind.New(cfg, files, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func findRecord(rs dns.RecordSet, name, typ string) (dns.Record, bool) {
	for _, r := range rs.Records {
		if r.Name == name && r.Type == typ {
			return r, true
		}
	}
	return dns.Record{}, false
}

func TestBindRecords(t *testing.T) {
	p := bindProvider(t)
	ctx := t.Context()
	detail, err := p.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(detail)
	if !p.Capabilities().ZoneCreate {
		t.Error("zone creation not enabled")
	}

	rs, err := p.Records(ctx, staticZone)
	if err != nil {
		t.Fatal(err)
	}
	if soa, ok := findRecord(rs, staticZone, "SOA"); !ok || !soa.ReadOnly || rs.Version == "" {
		t.Fatalf("records %+v", rs)
	}
	if _, ok := findRecord(rs, "www."+staticZone, "A"); !ok {
		t.Fatalf("www missing: %+v", rs)
	}

	// Create, with optimistic locking and duplicate detection.
	name := "it-" + randomLabel()
	created, err := p.CreateRecord(ctx, staticZone, dns.Record{Name: name, Type: "TXT", TTL: 120, Content: "hello dcdash"}, rs.Version)
	if err != nil {
		t.Fatal(err)
	}
	if created.Content != `"hello dcdash"` || created.ID == "" {
		t.Errorf("created %+v", created)
	}
	if _, err := p.CreateRecord(ctx, staticZone, dns.Record{Name: name, Type: "A", Content: "192.0.2.1"}, rs.Version); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("stale version: %v", err)
	}
	rs, _ = p.Records(ctx, staticZone)
	if _, err := p.CreateRecord(ctx, staticZone, dns.Record{Name: name, Type: "TXT", Content: "hello dcdash"}, rs.Version); !errors.Is(err, dns.ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	// bind ignores a CNAME next to other data; dcdash notices.
	if _, err := p.CreateRecord(ctx, staticZone, dns.Record{Name: name, Type: "CNAME", Content: "www." + staticZone + "."}, rs.Version); !errors.Is(err, dns.ErrRejected) {
		t.Errorf("CNAME conflict: %v", err)
	}

	// Update and delete.
	rs, _ = p.Records(ctx, staticZone)
	cur, ok := findRecord(rs, name+"."+staticZone, "TXT")
	if !ok || cur.ID != created.ID || cur.TTL != 120 {
		t.Fatalf("created record not listed: %+v", cur)
	}
	updated, err := p.UpdateRecord(ctx, staticZone, cur.ID, dns.Record{Name: name, Type: "TXT", TTL: 300, Content: "changed"}, cur.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteRecord(ctx, staticZone, cur.ID, cur.Version); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("delete with stale version: %v", err)
	}
	rs, _ = p.Records(ctx, staticZone)
	if err := p.DeleteRecord(ctx, staticZone, cur.ID, rs.Version); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("delete old ID: %v", err)
	}
	if err := p.DeleteRecord(ctx, staticZone, updated.ID, rs.Version); err != nil {
		t.Fatal(err)
	}
	rs, _ = p.Records(ctx, staticZone)
	if _, ok := findRecord(rs, name+"."+staticZone, "TXT"); ok {
		t.Error("record not deleted")
	}

	// The TSIG key is never used on zones the provider doesn't manage.
	if _, err := p.Records(ctx, "other.dcdash.test"); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("unmanaged zone: %v", err)
	}
	if _, err := p.Records(ctx, catalogZone); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("catalog zone listed as zone: %v", err)
	}
}

func TestBindZoneLifecycle(t *testing.T) {
	p := bindProvider(t)
	ctx := t.Context()
	zone := "it-" + randomLabel() + ".dcdash.test"

	if _, err := p.CreateZone(ctx, staticZone); !errors.Is(err, dns.ErrExists) {
		t.Errorf("create existing zone: %v", err)
	}
	if err := p.DeleteZone(ctx, staticZone); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("delete zone from the configuration: %v", err)
	}

	if _, err := p.CreateZone(ctx, zone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.DeleteZone(context.WithoutCancel(ctx), zone) })
	zones, err := p.Zones(ctx)
	if err != nil || !slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == zone }) {
		t.Fatalf("zones %v, %v", zones, err)
	}
	rs, err := p.Records(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	if ns, ok := findRecord(rs, zone, "NS"); !ok || ns.Content != "ns1.dcdash.test." {
		t.Errorf("records of the new zone: %+v", rs)
	}
	if _, err := p.CreateRecord(ctx, zone, dns.Record{Name: "www", Type: "A", TTL: 60, Content: "192.0.2.20"}, rs.Version); err != nil {
		t.Fatal(err)
	}

	// The secondary picks up the zone from the catalog.
	secondary := requireEnv(t, "DCDASH_IT_BIND_SECONDARY")
	waitDNS(t, secondary, "www."+zone, mdns.TypeA, true)

	if err := p.DeleteZone(ctx, zone); err != nil {
		t.Fatal(err)
	}
	zones, _ = p.Zones(ctx)
	if slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == zone }) {
		t.Error("zone still listed")
	}
	waitDNS(t, secondary, zone, mdns.TypeSOA, false)
	// The zone file is gone: creating the zone again works.
	if _, err := p.CreateZone(ctx, zone); err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if err := p.DeleteZone(ctx, zone); err != nil {
		t.Fatal(err)
	}
}

// waitDNS waits until the server answers (or no longer answers) the query
// authoritatively.
func waitDNS(t *testing.T, server, name string, qtype uint16, want bool) {
	t.Helper()
	c := &mdns.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		m := new(mdns.Msg)
		m.SetQuestion(mdns.Fqdn(name), qtype)
		in, _, err := c.Exchange(m, server+":53")
		got := err == nil && in.Rcode == mdns.RcodeSuccess && len(in.Answer) > 0
		if got == want {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s %s on %s: want answer=%v", name, mdns.TypeToString[qtype], server, want)
}

func randomLabel() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return strings.ToLower(hex.EncodeToString(b))
}
