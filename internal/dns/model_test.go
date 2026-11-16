// SPDX-License-Identifier: GPL-3.0-or-later

package dns

import (
	"errors"
	"strings"
	"testing"

	mdns "github.com/miekg/dns"
)

func TestNormalize(t *testing.T) {
	yes := true
	for _, c := range []struct {
		in       Record
		name     string
		content  string
		proxied  *bool
		errorMsg string
	}{
		{in: Record{Name: "www", Type: "a", TTL: 300, Content: " 192.0.2.1 "}, name: "www.example.org", content: "192.0.2.1"},
		{in: Record{Name: "@", Type: "AAAA", Content: "2001:DB8::1"}, name: "example.org", content: "2001:db8::1"},
		{in: Record{Name: "", Type: "MX", Content: "10 mail.example.org."}, name: "example.org", content: "10 mail.example.org."},
		{in: Record{Name: "WWW.Example.Org.", Type: "CNAME", Content: "web.example.net."}, name: "www.example.org", content: "web.example.net."},
		{in: Record{Name: "a.example.org", Type: "A", Content: "192.0.2.2"}, name: "a.example.org", content: "192.0.2.2"},
		{in: Record{Name: "_sip._tcp", Type: "SRV", Content: "10 5 5060 sip.example.org."}, name: "_sip._tcp.example.org", content: "10 5 5060 sip.example.org."},
		{in: Record{Name: "@", Type: "CAA", Content: `0 issue "letsencrypt.org"`}, name: "example.org", content: `0 issue "letsencrypt.org"`},
		{in: Record{Name: "@", Type: "TXT", Content: `v=spf1 -all; "x"`}, name: "example.org", content: `"v=spf1 -all; \"x\""`},
		{in: Record{Name: "@", Type: "TXT", Content: `"a" "b"`}, name: "example.org", content: `"a" "b"`},
		{in: Record{Name: "*", Type: "A", Content: "192.0.2.3"}, name: "*.example.org", content: "192.0.2.3"},
		{in: Record{Name: "web", Type: "A", Content: "192.0.2.4", Proxied: &yes}, name: "web.example.org", content: "192.0.2.4", proxied: &yes},

		{in: Record{Name: "x", Type: "SOA", Content: "a b 1 2 3 4 5"}, errorMsg: `type "SOA" can't be edited`},
		{in: Record{Name: "x", Type: "A", Content: "not-an-ip"}, errorMsg: "invalid A content"},
		{in: Record{Name: "x", Type: "A", Content: "192.0.2.1 ; comment"}, errorMsg: "invalid content"},
		{in: Record{Name: "x", Type: "A", Content: "192.0.2.1\n@ A 192.0.2.2"}, errorMsg: "invalid content"},
		{in: Record{Name: "x", Type: "A", Content: ""}, errorMsg: "invalid content"},
		{in: Record{Name: "www.example.com.", Type: "A", Content: "192.0.2.1"}, errorMsg: "not in zone"},
		{in: Record{Name: "a b", Type: "A", Content: "192.0.2.1"}, errorMsg: "invalid name"},
		{in: Record{Name: "a.*", Type: "A", Content: "192.0.2.1"}, errorMsg: "wildcard"},
		{in: Record{Name: "@", Type: "CNAME", Content: "x.example.net."}, errorMsg: "apex"},
		{in: Record{Name: "mx", Type: "MX", Content: "10 mail.example.org.", Proxied: &yes}, errorMsg: "proxied"},
		{in: Record{Name: "x", Type: "A", Content: "192.0.2.1", TTL: 1 << 31}, errorMsg: "TTL"},
	} {
		got, err := Normalize(c.in, "example.org")
		if c.errorMsg != "" {
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), c.errorMsg) {
				t.Errorf("%+v: error %v, want %q", c.in, err, c.errorMsg)
			}
			continue
		}
		if err != nil {
			t.Errorf("%+v: %v", c.in, err)
			continue
		}
		if got.Name != c.name || got.Content != c.content || (got.Proxied == nil) != (c.proxied == nil) {
			t.Errorf("%+v: got %+v", c.in, got)
		}
	}
}

func TestQuoteTXTLong(t *testing.T) {
	r, err := Normalize(Record{Name: "@", Type: "TXT", Content: strings.Repeat("a", 300)}, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	rr, err := ToRR(r)
	if err != nil {
		t.Fatal(err)
	}
	if txt := rr.(*mdns.TXT).Txt; len(txt) != 2 || len(txt[0]) != 255 || len(txt[1]) != 45 {
		t.Errorf("chunks %d", len(txt))
	}
}

func TestFromRRAndID(t *testing.T) {
	rr, _ := mdns.NewRR("WWW.example.org. 300 IN A 192.0.2.1")
	r := FromRR(rr)
	if r.Name != "www.example.org" || r.Type != "A" || r.TTL != 300 || r.Content != "192.0.2.1" || r.ReadOnly || r.ID == "" {
		t.Errorf("%+v", r)
	}
	rr2, _ := mdns.NewRR("www.example.org. 600 IN A 192.0.2.1")
	if FromRR(rr2).ID != r.ID {
		t.Error("ID depends on TTL")
	}
	soa, _ := mdns.NewRR("example.org. 3600 IN SOA ns1.example.org. hostmaster.example.org. 7 3600 900 1209600 300")
	if s := FromRR(soa); !s.ReadOnly || s.Content != "ns1.example.org. hostmaster.example.org. 7 3600 900 1209600 300" {
		t.Errorf("soa %+v", s)
	}
	back, err := ToRR(r)
	if err != nil || back.String() != "www.example.org.\t300\tIN\tA\t192.0.2.1" {
		t.Errorf("ToRR %v, %v", back, err)
	}
}

func TestZoneNames(t *testing.T) {
	for n, ok := range map[string]bool{
		"example.org": true, "sub.example.org": true, "10.in-addr.arpa": true, "org": true,
		"": false, "*.example.org": false, "example.org.": false, "a b.org": false, "a:b": false,
	} {
		if ValidZoneName(n) != ok {
			t.Errorf("ValidZoneName(%q) = %v", n, !ok)
		}
	}
	if NormalizeName(" Example.ORG. ") != "example.org" || !InZone("a.example.org", "example.org") || InZone("aexample.org", "example.org") {
		t.Error("NormalizeName/InZone")
	}
}
