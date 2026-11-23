// SPDX-License-Identifier: GPL-3.0-or-later

package addns

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/dns"
)

// fakeDir is a directory with zones held in memory; signed updates of the provider are
// applied to it, plain queries answered from it.
type fakeDir struct {
	cfg     config.Directory
	zones   map[string][]mdns.RR
	version int
	err     error
}

func rr(s string) mdns.RR {
	r, err := mdns.NewRR(s)
	if err != nil {
		panic(err)
	}
	return r
}

func newFakeDir() *fakeDir {
	return &fakeDir{cfg: config.Directory{Name: "lab", Domain: "ad.test", Kerberos: config.Kerberos{Realm: "AD.TEST", KDCs: []string{"dc1.ad.test:88"}}},
		zones: map[string][]mdns.RR{
			"ad.test": {rr("ad.test. 3600 IN SOA dc1.ad.test. hostmaster.ad.test. 1 900 600 86400 3600"),
				rr("ad.test. 3600 IN NS dc1.ad.test."), rr("www.ad.test. 300 IN A 192.0.2.1"), rr("dc1.ad.test. 300 IN A 192.0.2.2")},
			"_msdcs.ad.test": {rr("_msdcs.ad.test. 3600 IN SOA dc1.ad.test. hostmaster.ad.test. 1 900 600 86400 3600")},
		}}
}

func (f *fakeDir) Config() config.Directory { return f.cfg }

func (f *fakeDir) DNSZones(context.Context) ([]ad.DNSZone, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []ad.DNSZone
	for n := range f.zones {
		out = append(out, ad.DNSZone{Name: n})
	}
	slices.SortFunc(out, func(a, b ad.DNSZone) int { return compare(a.Name, b.Name) })
	return out, nil
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (f *fakeDir) DNSRecords(_ context.Context, zone string) ([]mdns.RR, string, error) {
	rrs, ok := f.zones[zone]
	if !ok {
		return nil, "", &ad.Error{Kind: ad.ErrNotFound}
	}
	return slices.Clone(rrs), strconv.Itoa(f.version), nil
}

func (f *fakeDir) DNSVersion(_ context.Context, zone string) (string, error) {
	if _, ok := f.zones[zone]; !ok {
		return "", &ad.Error{Kind: ad.ErrNotFound}
	}
	return strconv.Itoa(f.version), nil
}

func (f *fakeDir) CreateDNSZone(_ context.Context, zone string, soa *mdns.SOA, ns []*mdns.NS) error {
	if _, ok := f.zones[zone]; ok {
		return &ad.Error{Kind: ad.ErrExists}
	}
	f.zones[zone] = []mdns.RR{soa, ns[0]}
	return nil
}

func (f *fakeDir) DeleteDNSZone(_ context.Context, zone string) error {
	delete(f.zones, zone)
	return nil
}

func (f *fakeDir) KerberosUser(context.Context) (string, error) { return "svc", nil }

// answer answers a plain query from the zones.
func (f *fakeDir) answer(_ context.Context, name string, qtype uint16) (*mdns.Msg, error) {
	m := new(mdns.Msg)
	for _, rrs := range f.zones {
		for _, r := range rrs {
			if r.Header().Name == mdns.Fqdn(name) && r.Header().Rrtype == qtype {
				m.Answer = append(m.Answer, r)
			}
		}
	}
	return m, nil
}

// apply applies an update; ignore drops it (like a server that ignores a CNAME).
func (f *fakeDir) apply(ignore bool) func(context.Context, *mdns.Msg) (*mdns.Msg, error) {
	return func(_ context.Context, m *mdns.Msg) (*mdns.Msg, error) {
		if len(m.Answer) > 0 {
			return &mdns.Msg{MsgHdr: mdns.MsgHdr{Rcode: mdns.RcodeNotImplemented}}, nil // prerequisites
		}
		zone := dns.NormalizeName(m.Question[0].Name)
		if !ignore {
			for _, r := range m.Ns {
				if r.Header().Class == mdns.ClassNONE {
					del := mdns.Copy(r)
					del.Header().Class = mdns.ClassINET
					f.zones[zone] = slices.DeleteFunc(f.zones[zone], func(x mdns.RR) bool { return mdns.IsDuplicate(x, del) })
				} else {
					f.zones[zone] = append(f.zones[zone], r)
				}
			}
			f.version++
		}
		return &mdns.Msg{MsgHdr: mdns.MsgHdr{Rcode: mdns.RcodeSuccess}}, nil
	}
}

func newProvider(f *fakeDir, manageZones bool) *Provider {
	p := New(config.DNSProvider{Name: "lab-dns", Type: config.DNSTypeAD,
		AD: config.ADDNS{Directory: "lab", Server: "dc1.ad.test", ManageZones: manageZones}}, f, zerolog.Nop())
	p.query = f.answer
	p.signed = f.apply(false)
	return p
}

func TestRecords(t *testing.T) {
	f := newFakeDir()
	p := newProvider(f, false)
	if p.Kind() != "ad" || p.Capabilities().ZoneCreate {
		t.Errorf("kind or capabilities")
	}
	if d, err := p.Check(context.Background()); err != nil || d.Params["count"] != "2" {
		t.Errorf("Check = %+v, %v", d, err)
	}
	ctx := context.Background()
	rs, err := p.Records(ctx, "ad.test")
	if err != nil || len(rs.Records) != 4 || rs.Records[0].Type != "SOA" || !rs.Records[0].ReadOnly || rs.Version != "0" {
		t.Fatalf("Records = %+v, %v", rs, err)
	}
	// Sorted by name, then type: ad.test NS, dc1, www.
	if rs.Records[1].Type != "NS" || rs.Records[2].Name != "dc1.ad.test" || rs.Records[3].Name != "www.ad.test" {
		t.Errorf("order: %+v", rs.Records)
	}

	r, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "new.ad.test", Type: "A", Content: "192.0.2.9"}, "0")
	if err != nil || r.TTL != defaultTTL || r.ID == "" {
		t.Fatalf("CreateRecord = %+v, %v", r, err)
	}
	if _, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "x.ad.test", Type: "A", Content: "192.0.2.9"}, "0"); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("stale version: %v", err)
	}
	if _, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "new.ad.test", Type: "A", Content: "192.0.2.9"}, "1"); !errors.Is(err, dns.ErrExists) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := p.CreateRecord(ctx, "other.test", dns.Record{Name: "a.other.test", Type: "A", Content: "192.0.2.9"}, "1"); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("zone of another server: %v", err)
	}
	if _, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "c.ad.test", Type: "A", Content: "192.0.2.9", Comment: "x"}, "1"); err == nil {
		t.Errorf("comment accepted")
	}
	u, err := p.UpdateRecord(ctx, "ad.test", r.ID, dns.Record{Name: "new.ad.test", Type: "A", TTL: 60, Content: "192.0.2.10"}, "1")
	if err != nil || u.Content != "192.0.2.10" {
		t.Fatalf("UpdateRecord = %+v, %v", u, err)
	}
	if _, err := p.UpdateRecord(ctx, "ad.test", rs.Records[0].ID, dns.Record{Name: "ad.test", Type: "A", Content: "192.0.2.1"}, "2"); err == nil {
		t.Errorf("SOA changed")
	}
	if err := p.DeleteRecord(ctx, "ad.test", u.ID, "1"); !errors.Is(err, dns.ErrConflict) {
		t.Errorf("delete with stale version: %v", err)
	}
	if err := p.DeleteRecord(ctx, "ad.test", "nope", "2"); !errors.Is(err, dns.ErrNotFound) {
		t.Errorf("delete unknown: %v", err)
	}
	if err := p.DeleteRecord(ctx, "ad.test", u.ID, "2"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	if rs, _ := p.Records(ctx, "ad.test"); len(rs.Records) != 4 {
		t.Errorf("after delete: %+v", rs.Records)
	}

	// A server that ignores the update.
	p.signed = f.apply(true)
	if _, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "ign.ad.test", Type: "A", Content: "192.0.2.9"}, "3"); !errors.Is(err, dns.ErrRejected) {
		t.Errorf("ignored update: %v", err)
	}
	// Refused updates.
	p.signed = func(context.Context, *mdns.Msg) (*mdns.Msg, error) {
		return &mdns.Msg{MsgHdr: mdns.MsgHdr{Rcode: mdns.RcodeRefused}}, nil
	}
	if _, err := p.CreateRecord(ctx, "ad.test", dns.Record{Name: "ref.ad.test", Type: "A", Content: "192.0.2.9"}, "3"); !errors.Is(err, dns.ErrForbidden) {
		t.Errorf("refused update: %v", err)
	}
	f.err = &ad.Error{Kind: ad.ErrUnavailable}
	if _, err := p.Zones(ctx); !errors.Is(err, dns.ErrUnavailable) {
		t.Errorf("directory down: %v", err)
	}
}

func TestZones(t *testing.T) {
	f := newFakeDir()
	ctx := context.Background()
	if _, err := newProvider(f, false).CreateZone(ctx, "new.test"); !errors.Is(err, dns.ErrUnsupported) {
		t.Errorf("zone creation without manage_zones: %v", err)
	}
	p := newProvider(f, true)
	if _, err := p.CreateZone(ctx, "bad zone"); err == nil {
		t.Error("invalid name accepted")
	}
	z, err := p.CreateZone(ctx, "New.Test.")
	if err != nil || z.Name != "new.test" {
		t.Fatalf("CreateZone = %+v, %v", z, err)
	}
	soa := f.zones["new.test"][0].(*mdns.SOA)
	if soa.Ns != "dc1.ad.test." || soa.Mbox != "hostmaster.new.test." || f.zones["new.test"][1].(*mdns.NS).Ns != "dc1.ad.test." {
		t.Errorf("new zone: %v", f.zones["new.test"])
	}
	if _, err := p.CreateZone(ctx, "new.test"); !errors.Is(err, dns.ErrExists) {
		t.Errorf("duplicate zone: %v", err)
	}
	for _, z := range []string{"ad.test", "_msdcs.ad.test"} {
		if err := p.DeleteZone(ctx, z); err == nil {
			t.Errorf("%s can be deleted", z)
		}
	}
	if err := p.DeleteZone(ctx, "new.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.zones["new.test"]; ok {
		t.Error("zone not deleted")
	}
	if c := p.krb5Conf(); c != "[libdefaults]\n default_realm = AD.TEST\n dns_lookup_kdc = false\n dns_lookup_realm = false\n udp_preference_limit = 1\n[realms]\n AD.TEST = {\n  kdc = dc1.ad.test:88\n }\n" {
		t.Errorf("krb5.conf:\n%s", c)
	}
}
