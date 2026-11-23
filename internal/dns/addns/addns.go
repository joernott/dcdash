// SPDX-License-Identifier: GPL-3.0-or-later

// Package addns is the DNS provider for AD-integrated zones of a Windows or Samba domain
// (docs/adr/0010-active-directory.md): zones and records are read from the directory via
// LDAP, records are changed with RFC 2136 updates signed with GSS-TSIG (Kerberos, the
// service account of the directory), and zones are created and deleted via LDAP.
package addns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bodgit/tsig"
	"github.com/bodgit/tsig/gss"
	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/status"
)

// Directory is what the provider needs of the domain; *ad.Client implements it.
type Directory interface {
	Config() config.Directory
	DNSZones(ctx context.Context) ([]ad.DNSZone, error)
	DNSRecords(ctx context.Context, zone string) ([]mdns.RR, string, error)
	DNSVersion(ctx context.Context, zone string) (string, error)
	CreateDNSZone(ctx context.Context, zone string, soa *mdns.SOA, ns []*mdns.NS) error
	DeleteDNSZone(ctx context.Context, zone string) error
	KerberosUser(ctx context.Context) (string, error)
}

// Settings of new zones.
const (
	newZoneTTL     = 3600
	defaultTTL     = 3600
	newZoneRefresh = 900
	newZoneRetry   = 600
	newZoneExpire  = 86400
	newZoneMinimum = 3600
)

// Provider manages the AD-integrated zones of one domain.
type Provider struct {
	name        string
	dir         Directory
	server      string
	addr        string
	manageZones bool
	timeout     time.Duration
	// dialer resolves the server name with the configured resolver, if any.
	dialer *net.Dialer
	log    zerolog.Logger
	// query sends an unsigned query, signed a GSS-TSIG signed update; replaced in tests.
	query  func(ctx context.Context, name string, qtype uint16) (*mdns.Msg, error)
	signed func(ctx context.Context, m *mdns.Msg) (*mdns.Msg, error)
}

// New creates the provider for a DNS provider configuration of type ad.
func New(p config.DNSProvider, dir Directory, log zerolog.Logger) *Provider {
	x := &Provider{name: p.Name, dir: dir, server: p.AD.Server, addr: net.JoinHostPort(p.AD.Server, "53"),
		manageZones: p.AD.ManageZones, timeout: p.RequestTimeout(),
		log: log.With().Str("component", "dns").Str("provider", p.Name).Logger()}
	x.dialer = &net.Dialer{Timeout: x.timeout}
	if r := p.AD.Resolver; r != "" {
		if net.ParseIP(r) != nil {
			r = net.JoinHostPort(r, "53")
		}
		x.dialer.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: x.timeout}).DialContext(ctx, network, r)
		}}
	}
	x.query = x.plainQuery
	x.signed = x.signedExchange
	return x
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return p.name }

// Kind implements dns.Provider.
func (p *Provider) Kind() string { return config.DNSTypeAD }

// Capabilities implements dns.Provider.
func (p *Provider) Capabilities() dns.Capabilities {
	return dns.Capabilities{ZoneCreate: p.manageZones}
}

// Check reads the zones and queries the SOA of the domain's zone from the DNS server.
func (p *Provider) Check(ctx context.Context) (status.Detail, error) {
	zones, err := p.dir.DNSZones(ctx)
	if err != nil {
		return status.Detail{}, p.wrap(err)
	}
	domain := strings.ToLower(p.dir.Config().Domain)
	if _, err := p.soa(ctx, domain); err != nil {
		return status.Detail{}, err
	}
	return status.Detail{Text: fmt.Sprintf("%s: %d zones", p.server, len(zones)), Code: "dns.ad",
		Params: map[string]string{"server": p.server, "count": strconv.Itoa(len(zones))}}, nil
}

// wrap maps directory errors to the errors of package dns.
func (p *Provider) wrap(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ad.ErrNotFound):
		return fmt.Errorf("%w: %w", dns.ErrNotFound, err)
	case errors.Is(err, ad.ErrExists):
		return fmt.Errorf("%w: %w", dns.ErrExists, err)
	case errors.Is(err, ad.ErrForbidden):
		return fmt.Errorf("%w: %w", dns.ErrForbidden, err)
	case errors.Is(err, ad.ErrUnavailable):
		return fmt.Errorf("%w: %w", dns.ErrUnavailable, err)
	}
	return err
}

// Zones implements dns.Provider.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	zones, err := p.dir.DNSZones(ctx)
	if err != nil {
		return nil, p.wrap(err)
	}
	out := make([]dns.Zone, len(zones))
	for i, z := range zones {
		out[i] = dns.Zone{Name: z.Name}
	}
	return out, nil
}

// protectedZone reports whether a zone must not be deleted: the domain's own zone and the
// forest's _msdcs zone, which the domain controllers need.
func (p *Provider) protectedZone(zone string) bool {
	domain := strings.ToLower(p.dir.Config().Domain)
	return zone == domain || strings.HasPrefix(zone, "_msdcs.")
}

// CreateZone implements dns.Provider: the zone gets an SOA and an NS record naming the
// DNS server.
func (p *Provider) CreateZone(ctx context.Context, name string) (dns.Zone, error) {
	if !p.manageZones {
		return dns.Zone{}, dns.ErrUnsupported
	}
	zone := dns.NormalizeName(name)
	if !dns.ValidZoneName(zone) {
		return dns.Zone{}, &dns.ValidationError{Msg: "invalid zone name " + name}
	}
	server := mdns.Fqdn(strings.ToLower(p.server))
	soa := &mdns.SOA{Hdr: mdns.RR_Header{Name: zone + ".", Rrtype: mdns.TypeSOA, Class: mdns.ClassINET, Ttl: newZoneTTL},
		Ns: server, Mbox: "hostmaster." + zone + ".", Serial: 1, Refresh: newZoneRefresh, Retry: newZoneRetry,
		Expire: newZoneExpire, Minttl: newZoneMinimum}
	ns := &mdns.NS{Hdr: mdns.RR_Header{Name: zone + ".", Rrtype: mdns.TypeNS, Class: mdns.ClassINET, Ttl: newZoneTTL}, Ns: server}
	if err := p.dir.CreateDNSZone(ctx, zone, soa, []*mdns.NS{ns}); err != nil {
		return dns.Zone{}, p.wrap(err)
	}
	return dns.Zone{Name: zone}, nil
}

// DeleteZone implements dns.Provider.
func (p *Provider) DeleteZone(ctx context.Context, name string) error {
	if !p.manageZones {
		return dns.ErrUnsupported
	}
	zone := dns.NormalizeName(name)
	if p.protectedZone(zone) {
		return &dns.ValidationError{Msg: "the zone " + zone + " is needed by the domain and can't be deleted"}
	}
	return p.wrap(p.dir.DeleteDNSZone(ctx, zone))
}

// Records implements dns.Provider. The SOA is listed read-only. The version is the
// directory's (ad.Client.DNSVersion), because Samba doesn't increment the SOA serial.
func (p *Provider) Records(ctx context.Context, zone string) (dns.RecordSet, error) {
	soa, rrs, version, err := p.read(ctx, zone)
	if err != nil {
		return dns.RecordSet{}, err
	}
	out := []dns.Record{dns.FromRR(soa)}
	for _, rr := range rrs {
		out = append(out, dns.FromRR(rr))
	}
	for i := range out {
		out[i].Version = version
	}
	return dns.RecordSet{Records: out, Version: version}, nil
}

// read returns the SOA, the other records and the version of a zone from the directory.
func (p *Provider) read(ctx context.Context, zone string) (*mdns.SOA, []mdns.RR, string, error) {
	rrs, version, err := p.dir.DNSRecords(ctx, zone)
	if err != nil {
		return nil, nil, "", p.wrap(err)
	}
	var soa *mdns.SOA
	var other []mdns.RR
	for _, rr := range rrs {
		if s, ok := rr.(*mdns.SOA); ok && dns.NormalizeName(s.Hdr.Name) == zone {
			soa = s
			continue
		}
		other = append(other, rr)
	}
	if soa == nil {
		return nil, nil, "", fmt.Errorf("ad dns %s: %w: no SOA in %s", p.name, dns.ErrNotFound, zone)
	}
	slices.SortStableFunc(other, func(a, b mdns.RR) int {
		if c := strings.Compare(a.Header().Name, b.Header().Name); c != 0 {
			return c
		}
		return int(a.Header().Rrtype) - int(b.Header().Rrtype)
	})
	return soa, other, version, nil
}

// soa queries the current SOA of a zone from the DNS server.
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
	return nil, fmt.Errorf("ad dns %s: %w: the server has no SOA for %s", p.name, dns.ErrNotFound, zone)
}

func (p *Provider) plainQuery(ctx context.Context, name string, qtype uint16) (*mdns.Msg, error) {
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn(name), qtype)
	m.RecursionDesired = false
	c := &mdns.Client{Net: "tcp", Timeout: p.timeout, Dialer: p.dialer}
	in, _, err := c.ExchangeContext(ctx, m, p.addr)
	if err != nil {
		return nil, fmt.Errorf("ad dns %s: %w: %w", p.name, dns.ErrUnavailable, err)
	}
	return in, nil
}

// krb5Conf is the Kerberos configuration for the directory's realm.
func (p *Provider) krb5Conf() string {
	k := p.dir.Config().Kerberos
	var b strings.Builder
	b.WriteString("[libdefaults]\n default_realm = " + k.Realm + "\n dns_lookup_kdc = false\n dns_lookup_realm = false\n udp_preference_limit = 1\n")
	b.WriteString("[realms]\n " + k.Realm + " = {\n")
	for _, kdc := range k.KDCs {
		b.WriteString("  kdc = " + kdc + "\n")
	}
	b.WriteString(" }\n")
	return b.String()
}

// signedExchange negotiates a GSS-TSIG context with the DNS server as the directory's
// service account, sends the message signed with it and deletes the context.
func (p *Provider) signedExchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, error) {
	user, err := p.dir.KerberosUser(ctx)
	if err != nil {
		return nil, p.wrap(err)
	}
	cfg := p.dir.Config()
	c := &mdns.Client{Net: "tcp", Timeout: p.timeout, Dialer: p.dialer}
	g, err := gss.NewClient(c, gss.WithConfig(p.krb5Conf()))
	if err != nil {
		return nil, fmt.Errorf("ad dns %s: kerberos: %w", p.name, err)
	}
	defer g.Close()
	key, _, err := g.NegotiateContextWithCredentials(p.addr, cfg.Kerberos.Realm, user, cfg.BindPassword.Value())
	if err != nil {
		return nil, fmt.Errorf("ad dns %s: GSS-TSIG with %s as %s@%s: %w: %w", p.name, p.server, user, cfg.Kerberos.Realm, dns.ErrForbidden, err)
	}
	defer func() {
		if err := g.DeleteContext(key); err != nil {
			p.log.Debug().Err(err).Msg("deleting the GSS-TSIG context failed")
		}
	}()
	c.TsigProvider = g
	m.SetTsig(key, tsig.GSS, 300, time.Now().Unix())
	in, _, err := c.ExchangeContext(ctx, m, p.addr)
	if err != nil {
		return nil, fmt.Errorf("ad dns %s: %w: %w", p.name, dns.ErrUnavailable, err)
	}
	return in, nil
}

// checkVersion returns ErrConflict unless the zone is at version.
func (p *Provider) checkVersion(ctx context.Context, zone, version string) error {
	current, err := p.dir.DNSVersion(ctx, zone)
	if err != nil {
		return p.wrap(err)
	}
	if current != version {
		return dns.ErrConflict
	}
	return nil
}

// update sends an RFC 2136 update. It has no prerequisite: Samba answers value-dependent
// prerequisites with NOTIMP. The caller checks the version right before, which narrows,
// but doesn't close, the window for concurrent changes.
func (p *Provider) update(ctx context.Context, zone string, build func(m *mdns.Msg)) error {
	m := new(mdns.Msg)
	m.SetUpdate(mdns.Fqdn(zone))
	build(m)
	in, err := p.signed(ctx, m)
	if err != nil {
		return err
	}
	switch in.Rcode {
	case mdns.RcodeSuccess:
		return nil
	case mdns.RcodeNXRrset, mdns.RcodeYXRrset:
		return dns.ErrConflict
	case mdns.RcodeRefused, mdns.RcodeNotAuth:
		return fmt.Errorf("ad dns %s: update refused (rights of the service account on the zone?): %w", p.name, dns.ErrForbidden)
	default:
		return fmt.Errorf("ad dns %s: update failed: %s: %w", p.name, mdns.RcodeToString[in.Rcode], dns.ErrRejected)
	}
}

// exists reports whether the record is served.
func (p *Provider) exists(ctx context.Context, r dns.Record) (bool, error) {
	t, ok := mdns.StringToType[r.Type]
	if !ok {
		return false, &dns.ValidationError{Msg: "unknown type " + r.Type}
	}
	in, err := p.query(ctx, r.Name, t)
	if err != nil {
		return false, err
	}
	for _, rr := range in.Answer {
		if got := dns.FromRR(rr); got.Type == r.Type && got.Name == r.Name && got.Content == r.Content {
			return true, nil
		}
	}
	return false, nil
}

// normalize validates a record: no proxy flag or comment, TTL 3600 by default.
func normalize(r dns.Record, zone string) (dns.Record, error) {
	n, err := dns.Normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	if n.Proxied != nil && *n.Proxied || n.Comment != "" {
		return dns.Record{}, &dns.ValidationError{Msg: "AD DNS records have no proxy flag or comment"}
	}
	n.Proxied = nil
	if n.TTL == 0 {
		n.TTL = defaultTTL
	}
	return n, nil
}

// CreateRecord implements dns.Provider.
func (p *Provider) CreateRecord(ctx context.Context, zone string, r dns.Record, version string) (dns.Record, error) {
	r, err := normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	if _, err := p.findZone(ctx, zone); err != nil {
		return dns.Record{}, err
	}
	if err := p.checkVersion(ctx, zone, version); err != nil {
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
	if err := p.update(ctx, zone, func(m *mdns.Msg) { m.Insert([]mdns.RR{rr}) }); err != nil {
		return dns.Record{}, err
	}
	return p.verified(ctx, r)
}

// findZone returns ErrNotFound unless the zone is an AD-integrated zone of the domain, so
// that updates never go to other zones of the server.
func (p *Provider) findZone(ctx context.Context, zone string) (ad.DNSZone, error) {
	zones, err := p.dir.DNSZones(ctx)
	if err != nil {
		return ad.DNSZone{}, p.wrap(err)
	}
	for _, z := range zones {
		if z.Name == zone {
			return z, nil
		}
	}
	return ad.DNSZone{}, dns.ErrNotFound
}

// verified returns the stored record, or an error if the server didn't apply the change.
func (p *Provider) verified(ctx context.Context, r dns.Record) (dns.Record, error) {
	found, err := p.exists(ctx, r)
	if err != nil {
		return dns.Record{}, err
	}
	if !found {
		return dns.Record{}, fmt.Errorf("ad dns %s: the server didn't apply the change (e.g. a CNAME next to other records): %w", p.name, dns.ErrRejected)
	}
	r.ID = dns.RecordID(r)
	return r, nil
}

// find returns the record with the ID if the zone is at version.
func (p *Provider) find(ctx context.Context, zone, id, version string) (dns.Record, error) {
	_, rrs, current, err := p.read(ctx, zone)
	if err != nil {
		return dns.Record{}, err
	}
	if current != version {
		return dns.Record{}, dns.ErrConflict
	}
	for _, rr := range rrs {
		if r := dns.FromRR(rr); r.ID == id {
			if r.ReadOnly {
				return dns.Record{}, &dns.ValidationError{Msg: r.Type + " records can't be changed"}
			}
			return r, nil
		}
	}
	return dns.Record{}, dns.ErrNotFound
}

// UpdateRecord implements dns.Provider.
func (p *Provider) UpdateRecord(ctx context.Context, zone, id string, r dns.Record, version string) (dns.Record, error) {
	r, err := normalize(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	old, err := p.find(ctx, zone, id, version)
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
	if err := p.update(ctx, zone, func(m *mdns.Msg) {
		m.Remove([]mdns.RR{oldRR})
		m.Insert([]mdns.RR{newRR})
	}); err != nil {
		return dns.Record{}, err
	}
	return p.verified(ctx, r)
}

// DeleteRecord implements dns.Provider.
func (p *Provider) DeleteRecord(ctx context.Context, zone, id, version string) error {
	old, err := p.find(ctx, zone, id, version)
	if err != nil {
		return err
	}
	rr, err := dns.ToRR(old)
	if err != nil {
		return err
	}
	return p.update(ctx, zone, func(m *mdns.Msg) { m.Remove([]mdns.RR{rr}) })
}

var _ dns.Provider = (*Provider)(nil)
