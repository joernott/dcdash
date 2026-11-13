// SPDX-License-Identifier: GPL-3.0-or-later

package cloudflare

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/dns/cloudflare/cffake"
)

func setup(t *testing.T, token, account string) (*Provider, *cffake.Server) {
	t.Helper()
	fake := cffake.New("good-token", "example.org", "example.net")
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	p := New(config.DNSProvider{
		Name:       "cf-test",
		Type:       config.DNSTypeCloudflare,
		Timeout:    config.Duration(5 * time.Second),
		Cloudflare: config.DNSCloudflare{APIToken: config.NewSecret(token), AccountID: account, BaseURL: srv.URL + cffake.Prefix},
	}, zerolog.Nop())
	return p, fake
}

func TestZonesAndCapabilities(t *testing.T) {
	p, _ := setup(t, "good-token", "")
	zs, err := p.Zones(t.Context())
	if err != nil || len(zs) != 2 || zs[0].Name != "example.net" || zs[1].Status != "active" || len(zs[0].NameServers) != 2 {
		t.Fatalf("zones %+v, %v", zs, err)
	}
	if c := p.Capabilities(); !c.Proxied || !c.Comments || !c.AutoTTL || c.ZoneCreate {
		t.Errorf("capabilities %+v", c)
	}
	if _, err := p.CreateZone(t.Context(), "new.org"); !errors.Is(err, dns.ErrUnsupported) {
		t.Errorf("create without account: %v", err)
	}
	if _, err := p.Records(t.Context(), "unknown.org"); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("unknown zone: %v", err)
	}
	if d, err := p.Check(t.Context()); err != nil || d != "Cloudflare, 2 zones" {
		t.Errorf("check %q %v", d, err)
	}
}

func TestBadToken(t *testing.T) {
	p, _ := setup(t, "wrong", "")
	if _, err := p.Zones(t.Context()); !errors.Is(err, dns.ErrForbidden) {
		t.Errorf("bad token: %v", err)
	}
}

func TestRecordConversion(t *testing.T) {
	p, fake := setup(t, "good-token", "")
	prio := 10.0
	fake.Add("example.org", cffake.Record{Name: "example.org", Type: "MX", Content: "mail.example.org", Priority: &prio, TTL: 300})
	fake.Add("example.org", cffake.Record{Name: "_sip._tcp.example.org", Type: "SRV", TTL: 1,
		Data: map[string]any{"priority": 10, "weight": 5, "port": 5060, "target": "sip.example.org"}})
	fake.Add("example.org", cffake.Record{Name: "example.org", Type: "CAA", TTL: 1, Data: map[string]any{"flags": 0, "tag": "issue", "value": "letsencrypt.org"}})
	fake.Add("example.org", cffake.Record{Name: "example.org", Type: "TXT", Content: "v=spf1 -all", TTL: 1})
	fake.Add("example.org", cffake.Record{Name: "www.example.org", Type: "CNAME", Content: "example.org", Proxied: true, TTL: 1})
	fake.Add("example.org", cffake.Record{Name: "example.org", Type: "HTTPS", Content: "1 . alpn=h2", TTL: 1})

	rs, err := p.Records(t.Context(), "example.org")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"MX":    "10 mail.example.org.",
		"SRV":   "10 5 5060 sip.example.org.",
		"CAA":   `0 issue "letsencrypt.org"`,
		"TXT":   `"v=spf1 -all"`,
		"CNAME": "example.org.",
	}
	for _, r := range rs.Records {
		if w, ok := want[r.Type]; ok && r.Content != w {
			t.Errorf("%s content %q, want %q", r.Type, r.Content, w)
		}
		if r.Type == "CNAME" && (r.Proxied == nil || !*r.Proxied) {
			t.Error("CNAME not proxied")
		}
		if r.Type == "MX" && r.Proxied != nil {
			t.Error("MX has a proxy flag")
		}
		if r.Type == "HTTPS" && !r.ReadOnly {
			t.Error("HTTPS editable")
		}
		if r.Version == "" || r.ID == "" {
			t.Errorf("no ID or version: %+v", r)
		}
	}
}

func TestRecordLifecycle(t *testing.T) {
	p, fake := setup(t, "good-token", "acc")
	ctx := t.Context()
	yes := true
	r, err := p.CreateRecord(ctx, "example.org", dns.Record{Name: "web", Type: "A", Content: "192.0.2.1", Proxied: &yes, Comment: "web server"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "web.example.org" || r.TTL != 1 || r.Comment != "web server" || r.Proxied == nil || !*r.Proxied {
		t.Errorf("created %+v", r)
	}
	if _, err := p.CreateRecord(ctx, "example.org", dns.Record{Name: "web", Type: "A", Content: "192.0.2.1"}, ""); !errors.Is(err, dns.ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	srv, err := p.CreateRecord(ctx, "example.org", dns.Record{Name: "_sip._tcp", Type: "SRV", TTL: 300, Content: "10 5 5060 sip.example.org."}, "")
	if err != nil || srv.Content != "10 5 5060 sip.example.org." {
		t.Fatalf("srv %+v %v", srv, err)
	}
	if _, err := p.CreateRecord(ctx, "example.org", dns.Record{Name: "x", Type: "A", Content: "nope"}, ""); err == nil {
		t.Error("invalid record accepted")
	}

	up, err := p.UpdateRecord(ctx, "example.org", r.ID, dns.Record{Name: "web", Type: "A", TTL: 300, Content: "192.0.2.2"}, r.Version)
	if err != nil {
		t.Fatal(err)
	}
	if up.Content != "192.0.2.2" || up.Comment != "" || up.Version == r.Version {
		t.Errorf("updated %+v", up)
	}
	// The old version is stale now.
	if _, err := p.UpdateRecord(ctx, "example.org", r.ID, dns.Record{Name: "web", Type: "A", Content: "192.0.2.3"}, r.Version); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("stale update: %v", err)
	}
	fake.Touch(r.ID)
	if err := p.DeleteRecord(ctx, "example.org", r.ID, up.Version); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("concurrent change: %v", err)
	}
	rs, _ := p.Records(ctx, "example.org")
	var cur dns.Record
	for _, x := range rs.Records {
		if x.ID == r.ID {
			cur = x
		}
	}
	if err := p.DeleteRecord(ctx, "example.org", r.ID, cur.Version); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteRecord(ctx, "example.org", r.ID, cur.Version); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestZoneLifecycle(t *testing.T) {
	p, _ := setup(t, "good-token", "acc")
	ctx := t.Context()
	z, err := p.CreateZone(ctx, "New.ORG")
	if err != nil || z.Name != "new.org" || z.Status != "pending" || len(z.NameServers) != 2 {
		t.Fatalf("create %+v %v", z, err)
	}
	if _, err := p.CreateZone(ctx, "new.org"); !errors.Is(err, dns.ErrExists) {
		t.Errorf("create twice: %v", err)
	}
	if _, err := p.CreateZone(ctx, "*.bad"); err == nil {
		t.Error("invalid zone accepted")
	}
	if _, err := p.Records(ctx, "new.org"); err != nil {
		t.Errorf("records of the new zone: %v", err)
	}
	if err := p.DeleteZone(ctx, "new.org"); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteZone(ctx, "new.org"); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}
