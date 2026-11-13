// SPDX-License-Identifier: GPL-3.0-or-later

package bind

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	mdns "github.com/miekg/dns"

	"github.com/joernott/dcdash/internal/dns"
)

// zoneFile is the name of a zone's file in the zone directory.
func zoneFile(zone string) string { return zone + ".db" }

// RenderZoneFile renders the initial zone file of a new zone from the
// new_zones template: SOA and NS records. The serial is the date with a
// two-digit counter (YYYYMMDD01).
func (p *Provider) RenderZoneFile(zone string) string {
	n := p.cfg.NewZones
	secs := func(d time.Duration) int64 { return int64(d / time.Second) }
	serial := p.now().UTC().Format("20060102") + "01"
	var b strings.Builder
	fmt.Fprintf(&b, "; Zone %s, created by dcdash. Changes are made with dynamic updates.\n", zone)
	fmt.Fprintf(&b, "$ORIGIN %s\n$TTL %d\n", mdns.Fqdn(zone), secs(n.TTL.D()))
	fmt.Fprintf(&b, "@ IN SOA %s %s %s %d %d %d %d\n", mdns.Fqdn(n.SOA.MName), mdns.Fqdn(n.SOA.RName), serial,
		secs(n.SOA.Refresh.D()), secs(n.SOA.Retry.D()), secs(n.SOA.Expire.D()), secs(n.SOA.Minimum.D()))
	for _, ns := range n.NameServers {
		fmt.Fprintf(&b, "@ IN NS %s\n", mdns.Fqdn(ns))
	}
	return b.String()
}

// CreateZone uploads the zone file, adds the zone with rndc addzone and
// lists it in the catalog zone. A failed step undoes the earlier ones.
func (p *Provider) CreateZone(ctx context.Context, zone string) (dns.Zone, error) {
	if !p.Capabilities().ZoneCreate {
		return dns.Zone{}, fmt.Errorf("%w: %w", dns.ErrUnsupported, errNoZoneFiles)
	}
	zone = dns.NormalizeName(zone)
	if !dns.ValidZoneName(zone) {
		return dns.Zone{}, &dns.ValidationError{Msg: fmt.Sprintf("invalid zone name %q", zone)}
	}
	if zone == dns.NormalizeName(p.cfg.CatalogZone) {
		return dns.Zone{}, dns.ErrExists
	}
	zones, err := p.Zones(ctx)
	if err != nil {
		return dns.Zone{}, err
	}
	for _, z := range zones {
		if z.Name == zone {
			return dns.Zone{}, dns.ErrExists
		}
	}
	// The zone may exist on the server without being listed (e.g. in
	// named.conf); don't overwrite its file.
	if _, err := p.soa(ctx, zone); err == nil {
		return dns.Zone{}, fmt.Errorf("%w: the server already serves %s", dns.ErrExists, zone)
	}
	file := zoneFile(zone)
	if err := p.files.Upload(ctx, file, []byte(p.RenderZoneFile(zone))); err != nil {
		return dns.Zone{}, err
	}
	if err := p.rndc.AddZone(ctx, zone, path.Join(p.cfg.NewZones.FileDir, file), p.cfg.ZoneOptions()); err != nil {
		return dns.Zone{}, errors.Join(err, p.files.Remove(context.WithoutCancel(ctx), file))
	}
	if err := p.addToCatalog(ctx, zone); err != nil {
		undo := context.WithoutCancel(ctx)
		return dns.Zone{}, errors.Join(err, p.rndc.DelZone(undo, zone), p.files.Remove(undo, file))
	}
	p.log.Info().Str("zone", zone).Msg("zone created")
	return dns.Zone{Name: zone}, nil
}

// DeleteZone removes the zone from the catalog (so secondaries drop it),
// deletes it with rndc delzone and removes its files. Only zones in the
// catalog can be deleted: zones from the configuration are in named.conf.
func (p *Provider) DeleteZone(ctx context.Context, zone string) error {
	if !p.Capabilities().ZoneCreate {
		return fmt.Errorf("%w: %w", dns.ErrUnsupported, errNoZoneFiles)
	}
	zone = dns.NormalizeName(zone)
	member, err := p.inCatalog(ctx, zone)
	if err != nil {
		return err
	}
	if !member {
		return dns.ErrNotFound
	}
	if err := p.removeFromCatalog(ctx, zone); err != nil {
		return err
	}
	if err := p.rndc.DelZone(ctx, zone); err != nil {
		return err
	}
	file := zoneFile(zone)
	if err := p.files.Remove(ctx, file, file+".jnl"); err != nil {
		return fmt.Errorf("zone %s deleted, but its files remain: %w", zone, err)
	}
	p.log.Info().Str("zone", zone).Msg("zone deleted")
	return nil
}
