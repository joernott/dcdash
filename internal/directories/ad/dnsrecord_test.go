// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"encoding/binary"
	"testing"

	"github.com/go-ldap/ldap/v3"
	mdns "github.com/miekg/dns"
)

// raw builds a DNS_RECORD value with the data.
func raw(t uint16, ttl uint32, data []byte) []byte {
	b := make([]byte, recordHeader)
	binary.LittleEndian.PutUint16(b, uint16(len(data))) // #nosec G115 -- test data
	binary.LittleEndian.PutUint16(b[2:], t)
	b[4] = 5
	binary.BigEndian.PutUint32(b[12:], ttl)
	return append(b, data...)
}

func TestDecodeRecord(t *testing.T) {
	name := "www.ad.test."
	mx := append([]byte{0, 10}, encodeCountName("mail.ad.test")...)
	srv := append([]byte{0, 1, 0, 2, 0x01, 0xbb}, encodeCountName("dc1.ad.test.")...)
	for _, c := range []struct {
		data []byte
		want string
	}{
		{raw(mdns.TypeA, 300, []byte{192, 0, 2, 1}), "www.ad.test.\t300\tIN\tA\t192.0.2.1"},
		{raw(mdns.TypeAAAA, 300, []byte{0x20, 0x01, 0x0d, 0xb8, 15: 1}), "www.ad.test.\t300\tIN\tAAAA\t2001:db8::1"},
		{raw(mdns.TypeCNAME, 60, encodeCountName("web.ad.test")), "www.ad.test.\t60\tIN\tCNAME\tweb.ad.test."},
		{raw(mdns.TypeNS, 60, encodeCountName("dc1.ad.test")), "www.ad.test.\t60\tIN\tNS\tdc1.ad.test."},
		{raw(mdns.TypePTR, 60, encodeCountName("host.ad.test")), "www.ad.test.\t60\tIN\tPTR\thost.ad.test."},
		{raw(mdns.TypeMX, 60, mx), "www.ad.test.\t60\tIN\tMX\t10 mail.ad.test."},
		{raw(mdns.TypeSRV, 60, srv), "www.ad.test.\t60\tIN\tSRV\t1 2 443 dc1.ad.test."},
		{raw(mdns.TypeTXT, 60, []byte{2, 'h', 'i', 3, 'a', ' ', 'b'}), "www.ad.test.\t60\tIN\tTXT\t\"hi\" \"a b\""},
		{raw(mdns.TypeCAA, 60, []byte{0, 5, 'i', 's', 's', 'u', 'e', 'x'}), "www.ad.test.\t60\tIN\tCAA\t0 issue \"x\""},
		{raw(65280, 60, []byte{1, 2}), "www.ad.test.\t60\tCLASS1\tTYPE65280\t\\# 2 0102"},
	} {
		rr, err := decodeRecord(name, c.data)
		if err != nil {
			t.Errorf("%s: %v", c.want, err)
			continue
		}
		if rr.String() != c.want {
			t.Errorf("decode = %q, want %q", rr.String(), c.want)
		}
	}
	if rr, err := decodeRecord(name, raw(typeZero, 0, []byte{1, 2, 3, 4, 5, 6, 7, 8})); rr != nil || err != nil {
		t.Errorf("tombstone = %v, %v", rr, err)
	}
	for _, bad := range [][]byte{
		{1, 2, 3},
		raw(mdns.TypeA, 1, []byte{1, 2, 3}),
		raw(mdns.TypeCNAME, 1, []byte{9, 1, 3, 'a'}),
		raw(mdns.TypeTXT, 1, []byte{5, 'a'}),
		raw(mdns.TypeSOA, 1, []byte{1, 2}),
		append(raw(mdns.TypeA, 1, nil)[:recordHeader-1], 0),
	} {
		if _, err := decodeRecord(name, bad); err == nil {
			t.Errorf("decodeRecord(%v) accepted", bad)
		}
	}
}

func TestEncodeSOARoundTrip(t *testing.T) {
	soa := &mdns.SOA{Hdr: mdns.RR_Header{Name: "z.test.", Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: 3600},
		Ns: "dc1.ad.test.", Mbox: "hostmaster.z.test.", Serial: 7, Refresh: 900, Retry: 600, Expire: 86400, Minttl: 3600}
	ns := &mdns.NS{Hdr: mdns.RR_Header{Name: "z.test.", Rrtype: mdns.TypeNS, Class: mdns.ClassINET, Ttl: 3600}, Ns: "dc1.ad.test."}
	for _, rr := range []mdns.RR{soa, ns} {
		b, err := encodeRecord(rr, 7)
		if err != nil {
			t.Fatal(err)
		}
		if b[5] != rankZone || binary.LittleEndian.Uint32(b[8:]) != 7 {
			t.Errorf("header: %x", b[:recordHeader])
		}
		back, err := decodeRecord(nodeName("@", "z.test"), b)
		if err != nil || back.String() != rr.String() {
			t.Errorf("round trip = %v, %v; want %v", back, err, rr)
		}
	}
	if _, err := encodeRecord(&mdns.A{Hdr: mdns.RR_Header{Rrtype: mdns.TypeA}}, 1); err == nil {
		t.Error("A records are not encoded")
	}
	if nodeName("www", "z.test") != "www.z.test." {
		t.Error("nodeName")
	}
}

func TestNodesVersion(t *testing.T) {
	e := func(usn string) *ldap.Entry { return ldap.NewEntry("x", map[string][]string{"uSNChanged": {usn}}) }
	if v := nodesVersion(e("10"), []*ldap.Entry{e("12"), e("11")}); v != "12-2" {
		t.Errorf("version = %s", v)
	}
	if v := nodesVersion(e("10"), nil); v != "10-0" {
		t.Errorf("empty zone = %s", v)
	}
	if hiddenZone("rootdnsservers") != true || hiddenZone("..trustanchors") != true || hiddenZone("ad.test") {
		t.Error("hiddenZone")
	}
}
