// SPDX-License-Identifier: GPL-3.0-or-later

// Package bind is the bind9 DNS provider: zones and records are read with
// AXFR and changed with RFC 2136 dynamic updates, both signed with TSIG.
// Server operations use rndc (rndc.go), zone files are provisioned over SFTP
// (sftp.go).
package bind

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
)

// Provider manages the zones of one bind9 primary.
type Provider struct {
	name    string
	cfg     config.Bind
	server  string
	key     string // TSIG key name, fully qualified
	alg     string
	secret  map[string]string
	timeout time.Duration
	rndc    *RNDC
	files   ZoneFiles
	log     zerolog.Logger
	now     func() time.Time
}

var tsigAlgorithms = map[string]string{
	"hmac-sha1": mdns.HmacSHA1, "hmac-sha224": mdns.HmacSHA224, "hmac-sha256": mdns.HmacSHA256,
	"hmac-sha384": mdns.HmacSHA384, "hmac-sha512": mdns.HmacSHA512,
}

// New creates the provider. files may be nil (no zone creation).
func New(p config.DNSProvider, files ZoneFiles, log zerolog.Logger) (*Provider, error) {
	b := p.Bind
	alg, ok := tsigAlgorithms[b.TSIGAlgorithm]
	if !ok {
		return nil, fmt.Errorf("bind %s: unsupported TSIG algorithm %q", p.Name, b.TSIGAlgorithm)
	}
	key := mdns.Fqdn(strings.ToLower(b.TSIGKey))
	pr := &Provider{
		name:    p.Name,
		cfg:     b,
		server:  config.WithDefaultPort(b.Nameserver),
		key:     key,
		alg:     alg,
		secret:  map[string]string{key: b.TSIGSecret.Value()},
		timeout: p.RequestTimeout(),
		files:   files,
		log:     log.With().Str("dns", p.Name).Logger(),
		now:     time.Now,
	}
	if b.RNDC.Enabled() {
		pr.rndc = NewRNDC(b.RNDC, p.RequestTimeout())
	}
	return pr, nil
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return p.name }

// Kind implements dns.Provider.
func (p *Provider) Kind() string { return config.DNSTypeBind }

// Capabilities implements dns.Provider.
func (p *Provider) Capabilities() dns.Capabilities {
	return dns.Capabilities{ZoneCreate: p.cfg.ZoneCreation() && p.files != nil}
}

// RNDC returns the rndc client, or nil.
func (p *Provider) RNDC() *RNDC { return p.rndc }

// Check queries the SOA of a managed zone (and the rndc status if
// configured).
func (p *Provider) Check(ctx context.Context) (string, error) {
	zones, err := p.Zones(ctx)
	if err != nil {
		return "", err
	}
	detail := fmt.Sprintf("bind9 at %s, %d zones", p.server, len(zones))
	if len(zones) > 0 {
		if _, err := p.soa(ctx, zones[0].Name); err != nil {
			return "", err
		}
	}
	if p.rndc != nil {
		if _, err := p.rndc.Status(ctx); err != nil {
			return "", fmt.Errorf("rndc: %w", err)
		}
		detail += ", rndc ok"
	}
	if p.files != nil {
		if err := p.files.Check(ctx); err != nil {
			return "", err
		}
		detail += ", sftp ok"
	}
	return detail, nil
}

// Zones returns the configured zones and the members of the catalog zone.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	names := map[string]bool{}
	for _, z := range p.cfg.Zones {
		names[dns.NormalizeName(z)] = true
	}
	if p.cfg.CatalogZone != "" {
		members, err := p.catalogMembers(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			names[m.zone] = true
		}
	}
	out := make([]dns.Zone, 0, len(names))
	for n := range names {
		out = append(out, dns.Zone{Name: n})
	}
	slices.SortFunc(out, func(a, b dns.Zone) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// managed returns ErrNotFound unless the zone is managed by this provider,
// so the TSIG key is never used on other zones.
func (p *Provider) managed(ctx context.Context, zone string) error {
	zones, err := p.Zones(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(zones, func(z dns.Zone) bool { return z.Name == zone }) {
		return dns.ErrNotFound
	}
	return nil
}

// transfer reads a zone with AXFR. The SOA is returned separately.
func (p *Provider) transfer(ctx context.Context, zone string) (*mdns.SOA, []mdns.RR, error) {
	m := new(mdns.Msg)
	m.SetAxfr(mdns.Fqdn(zone))
	m.SetTsig(p.key, p.alg, 300, p.now().Unix())
	t := &mdns.Transfer{
		TsigSecret:   p.secret,
		DialTimeout:  p.timeout,
		ReadTimeout:  p.timeout,
		WriteTimeout: p.timeout,
	}
	ch, err := t.In(m, p.server)
	if err != nil {
		return nil, nil, p.netError(err)
	}
	var soa *mdns.SOA
	var rrs []mdns.RR
	for env := range ch {
		if env.Error != nil {
			return nil, nil, p.transferError(env.Error)
		}
		for _, rr := range env.RR {
			if s, ok := rr.(*mdns.SOA); ok {
				if soa == nil {
					soa = s
				}
				continue // first and last record of the transfer
			}
			rrs = append(rrs, rr)
		}
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
	}
	if soa == nil {
		return nil, nil, fmt.Errorf("bind %s: AXFR of %s returned no SOA", p.name, zone)
	}
	return soa, rrs, nil
}

func (p *Provider) transferError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "NOTAUTH"), strings.Contains(msg, "REFUSED"):
		return fmt.Errorf("bind %s: zone transfer refused (allow-transfer for the TSIG key?): %w", p.name, err)
	case strings.Contains(msg, "NOTZONE"), strings.Contains(msg, "SERVFAIL"):
		return fmt.Errorf("bind %s: %w: %w", p.name, dns.ErrNotFound, err)
	}
	return fmt.Errorf("bind %s: zone transfer: %w", p.name, err)
}

func (p *Provider) netError(err error) error {
	return fmt.Errorf("bind %s: %w: %w", p.name, dns.ErrUnavailable, err)
}

// generated record types that are not shown (DNSSEC signatures and chains).
var hiddenTypes = []uint16{mdns.TypeRRSIG, mdns.TypeNSEC, mdns.TypeNSEC3, mdns.TypeNSEC3PARAM, 65534 /* bind signing state */}

// Records implements dns.Provider. The SOA is listed read-only.
func (p *Provider) Records(ctx context.Context, zone string) (dns.RecordSet, error) {
	if err := p.managed(ctx, zone); err != nil {
		return dns.RecordSet{}, err
	}
	soa, rrs, err := p.transfer(ctx, zone)
	if err != nil {
		return dns.RecordSet{}, err
	}
	version := strconv.FormatUint(uint64(soa.Serial), 10)
	out := []dns.Record{dns.FromRR(soa)}
	for _, rr := range rrs {
		if slices.Contains(hiddenTypes, rr.Header().Rrtype) {
			continue
		}
		out = append(out, dns.FromRR(rr))
	}
	for i := range out {
		out[i].Version = version
	}
	return dns.RecordSet{Records: out, Version: version}, nil
}

// soa queries the current SOA of a zone from the primary.
func (p *Provider) soa(ctx context.Context, zone string) (*mdns.SOA, error) {
	in, err := p.query(ctx, zone, mdns.TypeSOA)
	if err != nil {
		return nil, err
	}
	for _, rr := range in.Answer {
		if s, ok := rr.(*mdns.SOA); ok && dns.NormalizeName(s.Hdr.Name) == zone {
			return s, nil
		}
	}
	return nil, fmt.Errorf("bind %s: %w: no SOA for %s", p.name, dns.ErrNotFound, zone)
}

// query sends a TSIG-signed query over TCP to the primary.
func (p *Provider) query(ctx context.Context, name string, qtype uint16) (*mdns.Msg, error) {
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn(name), qtype)
	m.RecursionDesired = false
	return p.exchange(ctx, m)
}

func (p *Provider) exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, error) {
	m.SetTsig(p.key, p.alg, 300, p.now().Unix())
	c := &mdns.Client{Net: "tcp", Timeout: p.timeout, TsigSecret: p.secret}
	in, _, err := c.ExchangeContext(ctx, m, p.server)
	if err != nil {
		if strings.Contains(err.Error(), "tsig") || strings.Contains(err.Error(), "TSIG") {
			return nil, fmt.Errorf("bind %s: TSIG check failed (key name, secret or algorithm?): %w", p.name, err)
		}
		return nil, p.netError(err)
	}
	return in, nil
}

// checkVersion returns the current SOA if its serial equals version.
func (p *Provider) checkVersion(ctx context.Context, zone, version string) (*mdns.SOA, error) {
	soa, err := p.soa(ctx, zone)
	if err != nil {
		return nil, err
	}
	if strconv.FormatUint(uint64(soa.Serial), 10) != version {
		return nil, dns.ErrConflict
	}
	return soa, nil
}

// update sends an RFC 2136 update. The current SOA is a prerequisite, so the
// update fails if the zone changed since it was read.
func (p *Provider) update(ctx context.Context, zone string, soa *mdns.SOA, build func(m *mdns.Msg)) error {
	m := new(mdns.Msg)
	m.SetUpdate(mdns.Fqdn(zone))
	m.Used([]mdns.RR{soa})
	build(m)
	in, err := p.exchange(ctx, m)
	if err != nil {
		return err
	}
	switch in.Rcode {
	case mdns.RcodeSuccess:
		return nil
	case mdns.RcodeNXRrset, mdns.RcodeYXRrset:
		return dns.ErrConflict
	case mdns.RcodeRefused, mdns.RcodeNotAuth:
		return fmt.Errorf("bind %s: update refused (update-policy for the TSIG key?): %w", p.name, dns.ErrForbidden)
	default:
		return fmt.Errorf("bind %s: update failed: %s", p.name, mdns.RcodeToString[in.Rcode])
	}
}

// exists reports whether the record with this name, type and content is in
// the zone. bind ignores some updates silently (e.g. a CNAME next to other
// data, RFC 2136 3.4.2.2), so changes are verified.
func (p *Provider) exists(ctx context.Context, r dns.Record) (bool, error) {
	t, ok := mdns.StringToType[r.Type]
	if !ok {
		return false, fmt.Errorf("bind %s: unknown type %s", p.name, r.Type)
	}
	in, err := p.query(ctx, r.Name, t)
	if err != nil {
		return false, err
	}
	for _, rr := range in.Answer {
		got := dns.FromRR(rr)
		if got.Type == r.Type && got.Name == r.Name && got.Content == r.Content {
			return true, nil
		}
	}
	return false, nil
}

// CreateRecord implements dns.Provider.
func (p *Provider) CreateRecord(ctx context.Context, zone string, r dns.Record, version string) (dns.Record, error) {
	if err := p.managed(ctx, zone); err != nil {
		return dns.Record{}, err
	}
	r, err := normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	soa, err := p.checkVersion(ctx, zone, version)
	if err != nil {
		return dns.Record{}, err
	}
	if found, err := p.exists(ctx, r); err != nil {
		return dns.Record{}, err
	} else if found {
		return dns.Record{}, dns.ErrExists
	}
	rr, err := dns.ToRR(r)
	if err != nil {
		return dns.Record{}, err
	}
	if err := p.update(ctx, zone, soa, func(m *mdns.Msg) { m.Insert([]mdns.RR{rr}) }); err != nil {
		return dns.Record{}, err
	}
	return p.verified(ctx, r)
}

// verified returns the stored record, or an error if bind ignored the update.
func (p *Provider) verified(ctx context.Context, r dns.Record) (dns.Record, error) {
	found, err := p.exists(ctx, r)
	if err != nil {
		return dns.Record{}, err
	}
	if !found {
		return dns.Record{}, fmt.Errorf("bind %s: the server ignored the change (e.g. a CNAME next to other records): %w", p.name, dns.ErrRejected)
	}
	r.ID = dns.RecordID(r)
	return r, nil
}

// find returns the record with the ID and the SOA of the transfer, after
// checking the version.
func (p *Provider) find(ctx context.Context, zone, id, version string) (dns.Record, *mdns.SOA, error) {
	if err := p.managed(ctx, zone); err != nil {
		return dns.Record{}, nil, err
	}
	soa, rrs, err := p.transfer(ctx, zone)
	if err != nil {
		return dns.Record{}, nil, err
	}
	if strconv.FormatUint(uint64(soa.Serial), 10) != version {
		return dns.Record{}, nil, dns.ErrConflict
	}
	for _, rr := range rrs {
		if r := dns.FromRR(rr); r.ID == id {
			if r.ReadOnly {
				return dns.Record{}, nil, &dns.ValidationError{Msg: r.Type + " records can't be changed"}
			}
			return r, soa, nil
		}
	}
	return dns.Record{}, nil, dns.ErrNotFound
}

// UpdateRecord implements dns.Provider.
func (p *Provider) UpdateRecord(ctx context.Context, zone, id string, r dns.Record, version string) (dns.Record, error) {
	r, err := normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	old, soa, err := p.find(ctx, zone, id, version)
	if err != nil {
		return dns.Record{}, err
	}
	oldRR, err := dns.ToRR(old)
	if err != nil {
		return dns.Record{}, err
	}
	newRR, err := dns.ToRR(r)
	if err != nil {
		return dns.Record{}, err
	}
	if err := p.update(ctx, zone, soa, func(m *mdns.Msg) {
		m.Remove([]mdns.RR{oldRR})
		m.Insert([]mdns.RR{newRR})
	}); err != nil {
		return dns.Record{}, err
	}
	return p.verified(ctx, r)
}

// DeleteRecord implements dns.Provider.
func (p *Provider) DeleteRecord(ctx context.Context, zone, id, version string) error {
	old, soa, err := p.find(ctx, zone, id, version)
	if err != nil {
		return err
	}
	rr, err := dns.ToRR(old)
	if err != nil {
		return err
	}
	return p.update(ctx, zone, soa, func(m *mdns.Msg) { m.Remove([]mdns.RR{rr}) })
}

// normalize validates a record; bind9 has no proxy flag or comments.
func normalize(r dns.Record, zone string) (dns.Record, error) {
	n, err := dns.Normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	if n.Proxied != nil && *n.Proxied || n.Comment != "" {
		return dns.Record{}, &dns.ValidationError{Msg: "bind9 records have no proxy flag or comment"}
	}
	n.Proxied = nil
	if n.TTL == 0 {
		n.TTL = 3600
	}
	return n, nil
}

var _ dns.Provider = (*Provider)(nil)

// errNoZoneFiles is returned when zone creation is requested without SFTP.
var errNoZoneFiles = errors.New("bind: zone creation needs rndc, sftp and new_zones in the configuration")
