// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
	mdns "github.com/miekg/dns"
)

// DNSZone is an AD-integrated DNS zone.
type DNSZone struct {
	// Name in lower case, without trailing dot.
	Name string
	DN   string
}

// zoneContainers are the containers of AD-integrated zones: the domain and forest DNS
// partitions and the legacy one in the domain partition.
func (c *Client) zoneContainers() []string {
	return []string{
		"CN=MicrosoftDNS,DC=DomainDnsZones," + c.cfg.BaseDN,
		"CN=MicrosoftDNS,DC=ForestDnsZones," + c.cfg.BaseDN,
		"CN=MicrosoftDNS,CN=System," + c.cfg.BaseDN,
	}
}

// hiddenZone reports whether a zone is internal: the root hints and trust anchors.
func hiddenZone(name string) bool {
	return name == "rootdnsservers" || strings.HasPrefix(name, "..")
}

// DNSZones returns the AD-integrated zones, sorted by name.
func (c *Client) DNSZones(ctx context.Context) ([]DNSZone, error) {
	var out []DNSZone
	err := c.with(ctx, func(conn *ldap.Conn) error {
		for _, base := range c.zoneContainers() {
			entries, _, err := search(ctx, conn, base, ldap.ScopeSingleLevel, "(objectClass=dnsZone)", []string{"dc"}, 0)
			if err != nil && !isNotFound(err) {
				return err
			}
			for _, e := range entries {
				n := strings.ToLower(e.GetAttributeValue("dc"))
				if !hiddenZone(n) && !slices.ContainsFunc(out, func(z DNSZone) bool { return z.Name == n }) {
					out = append(out, DNSZone{Name: n, DN: e.DN})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrap("list DNS zones", err)
	}
	slices.SortFunc(out, func(a, b DNSZone) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func isNotFound(err error) bool { return ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) }

// findZone returns the zone object of a zone.
func (c *Client) findZone(ctx context.Context, conn *ldap.Conn, zone string) (*ldap.Entry, error) {
	for _, base := range c.zoneContainers() {
		entries, _, err := search(ctx, conn, base, ldap.ScopeSingleLevel,
			"(&(objectClass=dnsZone)(dc="+ldap.EscapeFilter(zone)+"))", []string{"dc", "dNSProperty", "uSNChanged"}, 1)
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		if len(entries) == 1 {
			return entries[0], nil
		}
	}
	return nil, &Error{Kind: ErrNotFound, Op: "DNS zone " + zone}
}

// DNSRecords returns the records of a zone, read from its dnsNode objects, and the zone's
// version (see DNSVersion). Tombstoned nodes and records are left out.
func (c *Client) DNSRecords(ctx context.Context, zone string) ([]mdns.RR, string, error) {
	var out []mdns.RR
	var version string
	err := c.with(ctx, func(conn *ldap.Conn) error {
		z, err := c.findZone(ctx, conn, zone)
		if err != nil {
			return err
		}
		nodes, _, err := search(ctx, conn, z.DN, ldap.ScopeSingleLevel, "(objectClass=dnsNode)",
			[]string{"dc", "dnsRecord", "dNSTombstoned", "uSNChanged"}, 0)
		if err != nil {
			return err
		}
		version = nodesVersion(z, nodes)
		for _, n := range nodes {
			if n.GetAttributeValue("dNSTombstoned") == "TRUE" {
				continue
			}
			name := nodeName(n.GetAttributeValue("dc"), zone)
			for _, v := range n.GetRawAttributeValues("dnsRecord") {
				rr, err := decodeRecord(name, v)
				if err != nil {
					c.log.Warn().Err(err).Str("node", n.DN).Msg("skipping an undecodable DNS record")
					continue
				}
				if rr != nil {
					out = append(out, rr)
				}
			}
		}
		return nil
	})
	return out, version, wrap("read DNS records", err)
}

// nodesVersion is the version of a zone: the highest uSNChanged of the zone and its nodes
// (tombstoned ones included) and their number. It changes with every change of a record.
// Samba doesn't increment the SOA serial on dynamic updates, so the serial can't be used.
// The value is per domain controller.
func nodesVersion(zone *ldap.Entry, nodes []*ldap.Entry) string {
	var highest uint64
	for _, e := range append([]*ldap.Entry{zone}, nodes...) {
		if u, err := strconv.ParseUint(e.GetAttributeValue("uSNChanged"), 10, 64); err == nil && u > highest {
			highest = u
		}
	}
	return strconv.FormatUint(highest, 10) + "-" + strconv.Itoa(len(nodes))
}

// DNSVersion returns the current version of a zone.
func (c *Client) DNSVersion(ctx context.Context, zone string) (string, error) {
	var version string
	err := c.with(ctx, func(conn *ldap.Conn) error {
		z, err := c.findZone(ctx, conn, zone)
		if err != nil {
			return err
		}
		nodes, _, err := search(ctx, conn, z.DN, ldap.ScopeSingleLevel, "(objectClass=dnsNode)", []string{"uSNChanged"}, 0)
		if err != nil {
			return err
		}
		version = nodesVersion(z, nodes)
		return nil
	})
	return version, wrap("read the DNS zone version", err)
}

// CreateDNSZone creates a zone in the domain's DNS partition with its SOA and NS records.
// The zone properties (dNSProperty) are copied from the domain's own zone, so that the
// new zone behaves like it (e.g. secure dynamic updates).
func (c *Client) CreateDNSZone(ctx context.Context, zone string, soa *mdns.SOA, ns []*mdns.NS) error {
	return wrap("create DNS zone", c.with(ctx, func(conn *ldap.Conn) error {
		if _, err := c.findZone(ctx, conn, zone); err == nil {
			return &Error{Kind: ErrExists, Op: "create DNS zone", Message: zone}
		} else if !isNotFoundErr(err) {
			return err
		}
		var props []string
		if own, err := c.findZone(ctx, conn, strings.ToLower(c.cfg.Domain)); err == nil {
			for _, p := range own.GetRawAttributeValues("dNSProperty") {
				props = append(props, string(p))
			}
		}
		dn := "DC=" + ldap.EscapeDN(zone) + "," + c.zoneContainers()[0]
		a := ldap.NewAddRequest(dn, nil)
		a.Attribute("objectClass", []string{"top", "dnsZone"})
		if len(props) > 0 {
			a.Attribute("dNSProperty", props)
		}
		if err := conn.Add(a); err != nil {
			return err
		}
		records := make([]string, 0, 1+len(ns))
		for _, rr := range append([]mdns.RR{soa}, nsRRs(ns)...) {
			b, err := encodeRecord(rr, soa.Serial)
			if err != nil {
				return err
			}
			records = append(records, string(b))
		}
		at := ldap.NewAddRequest("DC=@,"+dn, nil)
		at.Attribute("objectClass", []string{"top", "dnsNode"})
		at.Attribute("dnsRecord", records)
		if err := conn.Add(at); err != nil {
			_ = conn.Del(ldap.NewDelRequest(dn, nil))
			return err
		}
		return nil
	}))
}

func nsRRs(ns []*mdns.NS) []mdns.RR {
	out := make([]mdns.RR, len(ns))
	for i, n := range ns {
		out[i] = n
	}
	return out
}

func isNotFoundErr(err error) bool { return isNotFound(err) || errors.Is(err, ErrNotFound) }

// DeleteDNSZone deletes a zone with all its nodes.
func (c *Client) DeleteDNSZone(ctx context.Context, zone string) error {
	return wrap("delete DNS zone", c.with(ctx, func(conn *ldap.Conn) error {
		z, err := c.findZone(ctx, conn, zone)
		if err != nil {
			return err
		}
		nodes, _, err := search(ctx, conn, z.DN, ldap.ScopeSingleLevel, "(objectClass=*)", []string{"dc"}, 0)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if err := conn.Del(ldap.NewDelRequest(n.DN, nil)); err != nil && !isNotFound(err) {
				return err
			}
		}
		return conn.Del(ldap.NewDelRequest(z.DN, nil))
	}))
}

// KerberosUser returns the account name of the service account, for Kerberos: the part
// before @ of a user principal name, or the sAMAccountName of a bind DN.
func (c *Client) KerberosUser(ctx context.Context) (string, error) {
	if user, _, ok := strings.Cut(c.cfg.BindDN, "@"); ok && !strings.Contains(user, "=") {
		return user, nil
	}
	var name string
	err := c.with(ctx, func(conn *ldap.Conn) error {
		e, err := one(conn, c.cfg.BindDN, "sAMAccountName")
		if err != nil {
			return err
		}
		name = e.GetAttributeValue("sAMAccountName")
		return nil
	})
	return name, wrap("read the service account", err)
}
