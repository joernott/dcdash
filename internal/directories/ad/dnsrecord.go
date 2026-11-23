// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"strings"

	mdns "github.com/miekg/dns"
)

// DNS records are stored in the dnsRecord attribute of dnsNode objects in the DNS_RECORD
// format of MS-DNSP 2.3.2.2: data length, type (little-endian), version 5, rank, flags,
// serial, TTL (big-endian), reserved, timestamp, then the data. Multi-byte values in the
// data are big-endian, names are DNS_COUNT_NAMEs.

const (
	recordHeader = 24
	rankZone     = 0xf0
	typeZero     = 0 // a tombstone
)

var errRecord = errors.New("invalid DNS record")

// decodeRecord converts a dnsRecord value of the node name (absolute, with dot) to an RR.
// Types dcdash doesn't decode come back as RFC 3597 unknown records.
func decodeRecord(name string, b []byte) (mdns.RR, error) {
	if len(b) < recordHeader {
		return nil, errRecord
	}
	n := int(binary.LittleEndian.Uint16(b[0:]))
	t := binary.LittleEndian.Uint16(b[2:])
	if len(b) < recordHeader+n {
		return nil, errRecord
	}
	if t == typeZero {
		return nil, nil
	}
	hdr := mdns.RR_Header{Name: name, Rrtype: t, Class: mdns.ClassINET, Ttl: binary.BigEndian.Uint32(b[12:])}
	d := b[recordHeader : recordHeader+n]
	var err error
	switch t {
	case mdns.TypeA:
		if len(d) != 4 {
			return nil, errRecord
		}
		return &mdns.A{Hdr: hdr, A: net.IP(append([]byte{}, d...))}, nil
	case mdns.TypeAAAA:
		if len(d) != 16 {
			return nil, errRecord
		}
		return &mdns.AAAA{Hdr: hdr, AAAA: net.IP(append([]byte{}, d...))}, nil
	case mdns.TypeNS, mdns.TypeCNAME, mdns.TypePTR:
		var target string
		if target, _, err = countName(d); err != nil {
			return nil, err
		}
		switch t {
		case mdns.TypeNS:
			return &mdns.NS{Hdr: hdr, Ns: target}, nil
		case mdns.TypeCNAME:
			return &mdns.CNAME{Hdr: hdr, Target: target}, nil
		default:
			return &mdns.PTR{Hdr: hdr, Ptr: target}, nil
		}
	case mdns.TypeMX:
		if len(d) < 2 {
			return nil, errRecord
		}
		var target string
		if target, _, err = countName(d[2:]); err != nil {
			return nil, err
		}
		return &mdns.MX{Hdr: hdr, Preference: binary.BigEndian.Uint16(d), Mx: target}, nil
	case mdns.TypeSRV:
		if len(d) < 6 {
			return nil, errRecord
		}
		var target string
		if target, _, err = countName(d[6:]); err != nil {
			return nil, err
		}
		return &mdns.SRV{Hdr: hdr, Priority: binary.BigEndian.Uint16(d), Weight: binary.BigEndian.Uint16(d[2:]),
			Port: binary.BigEndian.Uint16(d[4:]), Target: target}, nil
	case mdns.TypeTXT:
		var txt []string
		for len(d) > 0 {
			l := int(d[0])
			if len(d) < 1+l {
				return nil, errRecord
			}
			txt = append(txt, string(d[1:1+l]))
			d = d[1+l:]
		}
		return &mdns.TXT{Hdr: hdr, Txt: txt}, nil
	case mdns.TypeSOA:
		if len(d) < 20 {
			return nil, errRecord
		}
		soa := &mdns.SOA{Hdr: hdr, Serial: binary.BigEndian.Uint32(d), Refresh: binary.BigEndian.Uint32(d[4:]),
			Retry: binary.BigEndian.Uint32(d[8:]), Expire: binary.BigEndian.Uint32(d[12:]), Minttl: binary.BigEndian.Uint32(d[16:])}
		rest := d[20:]
		var used int
		if soa.Ns, used, err = countName(rest); err != nil {
			return nil, err
		}
		if soa.Mbox, _, err = countName(rest[used:]); err != nil {
			return nil, err
		}
		return soa, nil
	}
	// Other types are stored as wire-format RDATA.
	hdr.Rdlength = uint16(len(d)) // #nosec G115 -- the length came from a uint16
	if known, _, err := mdns.UnpackRRWithHeader(hdr, d, 0); err == nil && known != nil {
		return known, nil
	}
	return &mdns.RFC3597{Hdr: hdr, Rdata: hex.EncodeToString(d)}, nil
}

// countName decodes a DNS_COUNT_NAME (total length, label count, length-prefixed labels,
// a terminating zero) to an absolute name, and returns the bytes it used.
func countName(b []byte) (string, int, error) {
	if len(b) < 2 || len(b) < 2+int(b[0]) {
		return "", 0, errRecord
	}
	body := b[2 : 2+int(b[0])]
	labels := make([]string, 0, int(b[1]))
	for len(body) > 0 && body[0] != 0 {
		l := int(body[0])
		if len(body) < 1+l {
			return "", 0, errRecord
		}
		labels = append(labels, string(body[1:1+l]))
		body = body[1+l:]
	}
	return strings.Join(labels, ".") + ".", 2 + int(b[0]), nil
}

// encodeCountName encodes an absolute or relative name as DNS_COUNT_NAME.
func encodeCountName(name string) []byte {
	labels := strings.Split(strings.TrimSuffix(name, "."), ".")
	body := make([]byte, 0, len(name)+2)
	for _, l := range labels {
		body = append(body, byte(len(l))) // #nosec G115 -- labels of validated names have at most 63 bytes
		body = append(body, l...)
	}
	body = append(body, 0)
	return append([]byte{byte(len(body)), byte(len(labels))}, body...) // #nosec G115 -- validated names have at most 255 bytes
}

// encodeRecord encodes the SOA and NS records of a new zone in the DNS_RECORD format.
func encodeRecord(rr mdns.RR, serial uint32) ([]byte, error) {
	var d []byte
	switch r := rr.(type) {
	case *mdns.SOA:
		d = make([]byte, 20)
		for i, v := range []uint32{r.Serial, r.Refresh, r.Retry, r.Expire, r.Minttl} {
			binary.BigEndian.PutUint32(d[4*i:], v)
		}
		d = append(append(d, encodeCountName(r.Ns)...), encodeCountName(r.Mbox)...)
	case *mdns.NS:
		d = encodeCountName(r.Ns)
	default:
		return nil, errors.New("encoding " + mdns.TypeToString[rr.Header().Rrtype] + " records is not supported")
	}
	b := make([]byte, recordHeader, recordHeader+len(d))
	binary.LittleEndian.PutUint16(b[0:], uint16(len(d))) // #nosec G115 -- names are at most 255 bytes each
	binary.LittleEndian.PutUint16(b[2:], rr.Header().Rrtype)
	b[4] = 5
	b[5] = rankZone
	binary.LittleEndian.PutUint32(b[8:], serial)
	binary.BigEndian.PutUint32(b[12:], rr.Header().Ttl)
	return append(b, d...), nil
}

// nodeName returns the absolute owner name of a node in a zone: "@" is the apex.
func nodeName(node, zone string) string {
	if node == "@" {
		return zone + "."
	}
	return node + "." + zone + "."
}
