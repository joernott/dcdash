// SPDX-License-Identifier: GPL-3.0-or-later

package bind

import (
	"context"
	"crypto/sha1" //nolint:gosec // RFC 9432 member labels, not a security function
	"encoding/hex"
	"strings"

	mdns "github.com/miekg/dns"

	"github.com/joernott/dcdash/internal/dns"
)

// A catalog zone (RFC 9432) lists member zones as PTR records below
// "zones.<catalog>". Secondaries that consume the catalog add and remove the
// member zones automatically.

type catalogMember struct {
	zone string
	rr   mdns.RR
}

// catalogMembers reads the member zones of the catalog with AXFR.
func (p *Provider) catalogMembers(ctx context.Context) ([]catalogMember, error) {
	cat := dns.NormalizeName(p.cfg.CatalogZone)
	_, rrs, err := p.transfer(ctx, cat)
	if err != nil {
		return nil, err
	}
	suffix := ".zones." + cat
	var out []catalogMember
	for _, rr := range rrs {
		ptr, ok := rr.(*mdns.PTR)
		if !ok || !strings.HasSuffix(dns.NormalizeName(ptr.Hdr.Name), suffix) {
			continue
		}
		// Only the member label itself, not properties below it
		// (e.g. "group.<id>.zones.<catalog>").
		label := strings.TrimSuffix(dns.NormalizeName(ptr.Hdr.Name), suffix)
		if strings.Contains(label, ".") {
			continue
		}
		out = append(out, catalogMember{zone: dns.NormalizeName(ptr.Ptr), rr: rr})
	}
	return out, nil
}

// memberLabel is the unique member label of a zone: the SHA-1 hash of the
// zone name in wire format, as bind9 and other tools use it.
func memberLabel(zone string) string {
	buf := make([]byte, 256)
	n, err := mdns.PackDomainName(mdns.Fqdn(zone), buf, 0, nil, false)
	if err != nil {
		n = 0
	}
	sum := sha1.Sum(buf[:n]) //nolint:gosec // see import
	return hex.EncodeToString(sum[:])
}

// addToCatalog adds the zone to the catalog.
func (p *Provider) addToCatalog(ctx context.Context, zone string) error {
	cat := dns.NormalizeName(p.cfg.CatalogZone)
	soa, err := p.soa(ctx, cat)
	if err != nil {
		return err
	}
	rr := &mdns.PTR{
		Hdr: mdns.RR_Header{Name: mdns.Fqdn(memberLabel(zone) + ".zones." + cat), Rrtype: mdns.TypePTR, Class: mdns.ClassINET, Ttl: 0},
		Ptr: mdns.Fqdn(zone),
	}
	return p.update(ctx, cat, soa, func(m *mdns.Msg) { m.Insert([]mdns.RR{rr}) })
}

// removeFromCatalog removes all member records of the zone from the catalog.
func (p *Provider) removeFromCatalog(ctx context.Context, zone string) error {
	cat := dns.NormalizeName(p.cfg.CatalogZone)
	members, err := p.catalogMembers(ctx)
	if err != nil {
		return err
	}
	var rrs []mdns.RR
	for _, m := range members {
		if m.zone == zone {
			rrs = append(rrs, m.rr)
		}
	}
	if len(rrs) == 0 {
		return nil
	}
	soa, err := p.soa(ctx, cat)
	if err != nil {
		return err
	}
	return p.update(ctx, cat, soa, func(m *mdns.Msg) { m.Remove(rrs) })
}

// inCatalog reports whether the zone is a member of the catalog.
func (p *Provider) inCatalog(ctx context.Context, zone string) (bool, error) {
	if p.cfg.CatalogZone == "" {
		return false, nil
	}
	members, err := p.catalogMembers(ctx)
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if m.zone == zone {
			return true, nil
		}
	}
	return false, nil
}
