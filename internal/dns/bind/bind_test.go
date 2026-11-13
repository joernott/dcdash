// SPDX-License-Identifier: GPL-3.0-or-later

package bind

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
)

func testProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(config.DNSProvider{
		Name: "bind-test",
		Type: config.DNSTypeBind,
		Bind: config.Bind{
			RFC2136:     config.RFC2136{Nameserver: "127.0.0.1", TSIGKey: "dcdash", TSIGSecret: config.NewSecret("c2VjcmV0"), TSIGAlgorithm: "hmac-sha256"},
			CatalogZone: "catalog.example.org",
			RNDC:        config.RNDC{Binary: "/usr/sbin/rndc", Server: "ns1", Port: 953, KeyFile: "/etc/rndc.key"},
			SFTP:        config.SFTP{Address: "ns1", User: "dcdash", PrivateKeyFile: "/k", HostKey: "x", ZoneDir: "/zones"},
			NewZones: config.NewZones{
				FileDir: "/var/lib/bind/dyn",
				TTL:     config.Duration(time.Hour),
				SOA: config.SOA{
					MName: "ns1.example.org", RName: "hostmaster.example.org",
					Refresh: config.Duration(time.Hour), Retry: config.Duration(15 * time.Minute),
					Expire: config.Duration(14 * 24 * time.Hour), Minimum: config.Duration(5 * time.Minute),
				},
				NameServers: []string{"ns1.example.org", "ns2.example.org."},
			},
		},
	}, nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC) }
	return p
}

func TestRenderZoneFile(t *testing.T) {
	p := testProvider(t)
	got := p.RenderZoneFile("new.example.org")
	for _, want := range []string{
		"$ORIGIN new.example.org.\n$TTL 3600\n",
		"@ IN SOA ns1.example.org. hostmaster.example.org. 2026111301 3600 900 1209600 300\n",
		"@ IN NS ns1.example.org.\n@ IN NS ns2.example.org.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// The file parses as a zone.
	zp := mdns.NewZoneParser(strings.NewReader(got), "new.example.org.", "")
	n := 0
	for _, ok := zp.Next(); ok; _, ok = zp.Next() {
		n++
	}
	if zp.Err() != nil || n != 3 {
		t.Errorf("zone file: %d records, %v", n, zp.Err())
	}
}

func TestCapabilitiesWithoutFiles(t *testing.T) {
	p := testProvider(t)
	if p.Capabilities().ZoneCreate {
		t.Error("zone creation without ZoneFiles")
	}
	if _, err := p.CreateZone(context.Background(), "x.example.org"); !errors.Is(err, dns.ErrUnsupported) {
		t.Errorf("create: %v", err)
	}
	if err := p.DeleteZone(context.Background(), "x.example.org"); !errors.Is(err, dns.ErrUnsupported) {
		t.Errorf("delete: %v", err)
	}
}

func TestMemberLabel(t *testing.T) {
	a := memberLabel("example.org")
	if len(a) != 40 || memberLabel("example.org.") != a || memberLabel("example.com") == a {
		t.Errorf("label %q", a)
	}
}

func TestRNDC(t *testing.T) {
	r := NewRNDC(config.RNDC{Binary: "/usr/sbin/rndc", Server: "ns1", Port: 953, KeyFile: "/etc/rndc.key"}, time.Second)
	var got []string
	r.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		if args[len(args)-2] == "delzone" {
			return []byte("rndc: 'delzone' failed: not found\n"), &exec.ExitError{}
		}
		return []byte("ok\n"), nil
	}
	if err := r.AddZone(context.Background(), "new.example.org", "/var/lib/bind/dyn/new.example.org.db", "allow-transfer { none; };"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/sbin/rndc", "-s", "ns1", "-p", "953", "-k", "/etc/rndc.key", "addzone", "new.example.org",
		`{ type primary; file "/var/lib/bind/dyn/new.example.org.db"; allow-transfer { none; }; };`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("args\n got %q\nwant %q", got, want)
	}
	err := r.DelZone(context.Background(), "x.example.org")
	var re *RNDCError
	if !errors.As(err, &re) || re.Command != "delzone" || !strings.Contains(re.Output, "not found") {
		t.Errorf("delzone error %v", err)
	}
}

func TestSFTPFileNames(t *testing.T) {
	s := &SFTPFiles{dir: "/zones"}
	if p, err := s.file("example.org.db"); err != nil || p != "/zones/example.org.db" {
		t.Errorf("%q %v", p, err)
	}
	for _, bad := range []string{"", ".", "..", "../etc/passwd", "a/b", `a\b`} {
		if _, err := s.file(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNormalizeNoCloudflareFields(t *testing.T) {
	yes := true
	if _, err := normalize(dns.Record{Name: "a", Type: "A", Content: "192.0.2.1", Proxied: &yes}, "example.org"); err == nil {
		t.Error("proxied accepted")
	}
	r, err := normalize(dns.Record{Name: "a", Type: "A", Content: "192.0.2.1"}, "example.org")
	if err != nil || r.TTL != 3600 {
		t.Errorf("%+v %v", r, err)
	}
}
