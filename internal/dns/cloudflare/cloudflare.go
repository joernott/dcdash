// SPDX-License-Identifier: GPL-3.0-or-later

// Package cloudflare is the Cloudflare DNS provider, using the official SDK
// (cloudflare-go). Records are converted between the zone-file presentation
// format of the dns package and Cloudflare's content, priority and data
// fields.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cf "github.com/cloudflare/cloudflare-go/v6"
	cfdns "github.com/cloudflare/cloudflare-go/v6/dns"
	"github.com/cloudflare/cloudflare-go/v6/option"
	"github.com/cloudflare/cloudflare-go/v6/zones"
	mdns "github.com/miekg/dns"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dns"
)

// Provider manages the zones of one Cloudflare account.
type Provider struct {
	name      string
	accountID string
	client    *cf.Client
	log       zerolog.Logger

	mu      sync.Mutex
	zoneIDs map[string]string
}

// New creates the provider.
func New(p config.DNSProvider, log zerolog.Logger) *Provider {
	opts := []option.RequestOption{
		option.WithAPIToken(p.Cloudflare.APIToken.Value()),
		option.WithHTTPClient(&http.Client{Timeout: p.RequestTimeout()}),
		option.WithMaxRetries(1),
	}
	if p.Cloudflare.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(p.Cloudflare.BaseURL))
	}
	return &Provider{
		name:      p.Name,
		accountID: p.Cloudflare.AccountID,
		client:    cf.NewClient(opts...),
		log:       log.With().Str("dns", p.Name).Logger(),
		zoneIDs:   map[string]string{},
	}
}

// Name implements dns.Provider.
func (p *Provider) Name() string { return p.name }

// Kind implements dns.Provider.
func (p *Provider) Kind() string { return config.DNSTypeCloudflare }

// Capabilities implements dns.Provider.
func (p *Provider) Capabilities() dns.Capabilities {
	return dns.Capabilities{Proxied: true, Comments: true, AutoTTL: true, ZoneCreate: p.accountID != ""}
}

// Check lists the zones the token can see.
func (p *Provider) Check(ctx context.Context) (string, error) {
	zs, err := p.Zones(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Cloudflare, %d zones", len(zs)), nil
}

// apiError maps an SDK error to the errors of the dns package.
func (p *Provider) apiError(err error) error {
	if err == nil {
		return nil
	}
	var ae *cf.Error
	if errors.As(err, &ae) {
		msgs := make([]string, 0, len(ae.Errors))
		codes := make([]int64, 0, len(ae.Errors))
		for _, e := range ae.Errors {
			msgs = append(msgs, e.Message)
			codes = append(codes, e.Code)
		}
		msg := strings.Join(msgs, "; ")
		switch {
		case ae.StatusCode == http.StatusNotFound:
			return fmt.Errorf("cloudflare %s: %w: %s", p.name, dns.ErrNotFound, msg)
		case ae.StatusCode == http.StatusUnauthorized || ae.StatusCode == http.StatusForbidden:
			return fmt.Errorf("cloudflare %s: %w: %s", p.name, dns.ErrForbidden, msg)
		// 81053/81057/81058: a record with the same name/content exists;
		// 1061: the zone exists.
		case slices.ContainsFunc(codes, func(c int64) bool { return c == 81053 || c == 81057 || c == 81058 || c == 1061 }):
			return fmt.Errorf("cloudflare %s: %w: %s", p.name, dns.ErrExists, msg)
		case ae.StatusCode == http.StatusBadRequest:
			return &dns.ValidationError{Msg: "Cloudflare: " + msg}
		case ae.StatusCode >= 500:
			return fmt.Errorf("cloudflare %s: %w: %s", p.name, dns.ErrUnavailable, msg)
		}
		return fmt.Errorf("cloudflare %s: HTTP %d: %s", p.name, ae.StatusCode, msg)
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("cloudflare %s: %w: %w", p.name, dns.ErrUnavailable, err)
	}
	return fmt.Errorf("cloudflare %s: %w", p.name, err)
}

// Zones implements dns.Provider.
func (p *Provider) Zones(ctx context.Context) ([]dns.Zone, error) {
	iter := p.client.Zones.ListAutoPaging(ctx, zones.ZoneListParams{PerPage: cf.F(50.0)})
	var out []dns.Zone
	ids := map[string]string{}
	for iter.Next() {
		z := iter.Current()
		name := dns.NormalizeName(z.Name)
		ids[name] = z.ID
		out = append(out, dns.Zone{Name: name, Status: string(z.Status), ID: z.ID, NameServers: z.NameServers})
	}
	if err := iter.Err(); err != nil {
		return nil, p.apiError(err)
	}
	p.mu.Lock()
	p.zoneIDs = ids
	p.mu.Unlock()
	slices.SortFunc(out, func(a, b dns.Zone) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// zoneID returns the ID of a zone, reloading the zone list on a miss.
func (p *Provider) zoneID(ctx context.Context, zone string) (string, error) {
	p.mu.Lock()
	id, ok := p.zoneIDs[zone]
	p.mu.Unlock()
	if ok {
		return id, nil
	}
	if _, err := p.Zones(ctx); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.zoneIDs[zone]; ok {
		return id, nil
	}
	return "", dns.ErrNotFound
}

// CreateZone implements dns.Provider. The new zone is "pending" until the
// registrar delegates it to the returned name servers.
func (p *Provider) CreateZone(ctx context.Context, name string) (dns.Zone, error) {
	if p.accountID == "" {
		return dns.Zone{}, fmt.Errorf("%w: zone creation needs cloudflare.account_id", dns.ErrUnsupported)
	}
	name = dns.NormalizeName(name)
	if !dns.ValidZoneName(name) {
		return dns.Zone{}, &dns.ValidationError{Msg: fmt.Sprintf("invalid zone name %q", name)}
	}
	z, err := p.client.Zones.New(ctx, zones.ZoneNewParams{
		Account: cf.F(zones.ZoneNewParamsAccount{ID: cf.F(p.accountID)}),
		Name:    cf.F(name),
		Type:    cf.F(zones.TypeFull),
	})
	if err != nil {
		return dns.Zone{}, p.apiError(err)
	}
	p.mu.Lock()
	p.zoneIDs[name] = z.ID
	p.mu.Unlock()
	return dns.Zone{Name: name, Status: string(z.Status), ID: z.ID, NameServers: z.NameServers}, nil
}

// DeleteZone implements dns.Provider.
func (p *Provider) DeleteZone(ctx context.Context, name string) error {
	if p.accountID == "" {
		return fmt.Errorf("%w: zone deletion needs cloudflare.account_id", dns.ErrUnsupported)
	}
	id, err := p.zoneID(ctx, name)
	if err != nil {
		return err
	}
	if _, err := p.client.Zones.Delete(ctx, zones.ZoneDeleteParams{ZoneID: cf.F(id)}); err != nil {
		return p.apiError(err)
	}
	p.mu.Lock()
	delete(p.zoneIDs, name)
	p.mu.Unlock()
	return nil
}

// Records implements dns.Provider.
func (p *Provider) Records(ctx context.Context, zone string) (dns.RecordSet, error) {
	id, err := p.zoneID(ctx, zone)
	if err != nil {
		return dns.RecordSet{}, err
	}
	iter := p.client.DNS.Records.ListAutoPaging(ctx, cfdns.RecordListParams{ZoneID: cf.F(id), PerPage: cf.F(500.0)})
	out := []dns.Record{}
	for iter.Next() {
		out = append(out, fromCloudflare(iter.Current()))
	}
	if err := iter.Err(); err != nil {
		return dns.RecordSet{}, p.apiError(err)
	}
	slices.SortFunc(out, func(a, b dns.Record) int {
		return cmpStrings(a.Name, b.Name, a.Type, b.Type, a.Content, b.Content)
	})
	return dns.RecordSet{Records: out}, nil
}

func cmpStrings(pairs ...string) int {
	for i := 0; i+1 < len(pairs); i += 2 {
		if c := strings.Compare(pairs[i], pairs[i+1]); c != 0 {
			return c
		}
	}
	return 0
}

// CreateRecord implements dns.Provider; Cloudflare has no zone version.
func (p *Provider) CreateRecord(ctx context.Context, zone string, r dns.Record, _ string) (dns.Record, error) {
	id, err := p.zoneID(ctx, zone)
	if err != nil {
		return dns.Record{}, err
	}
	body, err := toCloudflare(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	res, err := p.client.DNS.Records.New(ctx, cfdns.RecordNewParams{ZoneID: cf.F(id), Body: body.newBody()})
	if err != nil {
		return dns.Record{}, p.apiError(err)
	}
	return fromCloudflare(*res), nil
}

// current returns a record after checking its version (modified_on).
func (p *Provider) current(ctx context.Context, zoneID, id, version string) (cfdns.RecordResponse, error) {
	res, err := p.client.DNS.Records.Get(ctx, id, cfdns.RecordGetParams{ZoneID: cf.F(zoneID)})
	if err != nil {
		return cfdns.RecordResponse{}, p.apiError(err)
	}
	if versionOf(*res) != version {
		return cfdns.RecordResponse{}, dns.ErrConflict
	}
	return *res, nil
}

// UpdateRecord implements dns.Provider. Cloudflare has no conditional
// update; dcdash compares modified_on right before the update.
func (p *Provider) UpdateRecord(ctx context.Context, zone, id string, r dns.Record, version string) (dns.Record, error) {
	zoneID, err := p.zoneID(ctx, zone)
	if err != nil {
		return dns.Record{}, err
	}
	cur, err := p.current(ctx, zoneID, id, version)
	if err != nil {
		return dns.Record{}, err
	}
	if fromCloudflare(cur).ReadOnly {
		return dns.Record{}, &dns.ValidationError{Msg: string(cur.Type) + " records can't be changed"}
	}
	body, err := toCloudflare(r, zone)
	if err != nil {
		return dns.Record{}, err
	}
	res, err := p.client.DNS.Records.Update(ctx, id, cfdns.RecordUpdateParams{ZoneID: cf.F(zoneID), Body: body.updateBody()})
	if err != nil {
		return dns.Record{}, p.apiError(err)
	}
	return fromCloudflare(*res), nil
}

// DeleteRecord implements dns.Provider.
func (p *Provider) DeleteRecord(ctx context.Context, zone, id, version string) error {
	zoneID, err := p.zoneID(ctx, zone)
	if err != nil {
		return err
	}
	if _, err := p.current(ctx, zoneID, id, version); err != nil {
		return err
	}
	_, err = p.client.DNS.Records.Delete(ctx, id, cfdns.RecordDeleteParams{ZoneID: cf.F(zoneID)})
	return p.apiError(err)
}

func versionOf(r cfdns.RecordResponse) string { return r.ModifiedOn.UTC().Format(time.RFC3339Nano) }

// data holds the fields of Cloudflare's "data" object that dcdash uses.
type data struct {
	Priority int    `json:"priority,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	Port     int    `json:"port,omitempty"`
	Target   string `json:"target,omitempty"`
	Flags    int    `json:"flags,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Value    string `json:"value,omitempty"`
}

// fromCloudflare converts a Cloudflare record into the model.
func fromCloudflare(r cfdns.RecordResponse) dns.Record {
	typ := string(r.Type)
	out := dns.Record{
		ID:      r.ID,
		Name:    dns.NormalizeName(r.Name),
		Type:    typ,
		TTL:     uint32(max(float64(r.TTL), 0)),
		Comment: r.Comment,
		Version: versionOf(r),
	}
	if typ == "A" || typ == "AAAA" || typ == "CNAME" {
		proxied := r.Proxied
		out.Proxied = &proxied
	}
	content := r.Content
	switch typ {
	case "CNAME", "NS", "PTR":
		content = mdns.Fqdn(content)
	case "MX":
		content = fmt.Sprintf("%d %s", int(r.Priority), mdns.Fqdn(content))
	case "TXT":
		if !strings.HasPrefix(content, `"`) {
			content = quoteTXT(content)
		}
	case "SRV", "CAA":
		var d data
		if b, err := json.Marshal(r.Data); err == nil {
			_ = json.Unmarshal(b, &d)
		}
		if typ == "SRV" {
			content = fmt.Sprintf("%d %d %d %s", d.Priority, d.Weight, d.Port, mdns.Fqdn(d.Target))
		} else {
			content = fmt.Sprintf("%d %s %q", d.Flags, d.Tag, d.Value)
		}
	}
	out.Content = content
	// Re-render through miekg/dns for the canonical form; types it can't
	// parse stay as they are and read-only.
	if rr, err := mdns.NewRR(mdns.Fqdn(out.Name) + " 300 IN " + typ + " " + content); err == nil && rr != nil {
		out.Content = dns.RDataString(rr)
	}
	out.ReadOnly = !slices.Contains(dns.EditableTypes, typ)
	return out
}

// quoteTXT quotes plain TXT content returned by Cloudflare.
func quoteTXT(s string) string {
	r, err := dns.Normalize(dns.Record{Name: "@", Type: "TXT", Content: s}, "example.org")
	if err != nil {
		return strconv.Quote(s)
	}
	return r.Content
}

// fields are the request fields of a record, shared by create and update.
type fields struct {
	name     string
	ttl      uint32
	typ      string
	comment  string
	proxied  *bool
	content  string
	priority *float64
	data     any
}

func (f fields) newBody() cfdns.RecordNewParamsBody {
	b := cfdns.RecordNewParamsBody{Name: cf.F(f.name), TTL: cf.F(cfdns.TTL(f.ttl)), Type: cf.F(cfdns.RecordNewParamsBodyType(f.typ))}
	if f.comment != "" {
		b.Comment = cf.F(f.comment)
	}
	if f.proxied != nil {
		b.Proxied = cf.F(*f.proxied)
	}
	if f.content != "" {
		b.Content = cf.F(f.content)
	}
	if f.priority != nil {
		b.Priority = cf.F(*f.priority)
	}
	if f.data != nil {
		b.Data = cf.F(f.data)
	}
	return b
}

func (f fields) updateBody() cfdns.RecordUpdateParamsBody {
	b := cfdns.RecordUpdateParamsBody{Name: cf.F(f.name), TTL: cf.F(cfdns.TTL(f.ttl)), Type: cf.F(cfdns.RecordUpdateParamsBodyType(f.typ))}
	// An update replaces the record: an empty comment removes it.
	b.Comment = cf.F(f.comment)
	if f.proxied != nil {
		b.Proxied = cf.F(*f.proxied)
	}
	if f.content != "" {
		b.Content = cf.F(f.content)
	}
	if f.priority != nil {
		b.Priority = cf.F(*f.priority)
	}
	if f.data != nil {
		b.Data = cf.F(f.data)
	}
	return b
}

// toCloudflare validates a record and converts it into Cloudflare's request
// fields.
func toCloudflare(r dns.Record, zone string) (fields, error) {
	n, err := dns.Normalize(r, zone)
	if err != nil {
		return fields{}, err
	}
	rr, err := dns.ToRR(n)
	if err != nil {
		return fields{}, err
	}
	f := fields{name: n.Name, ttl: n.TTL, typ: n.Type, comment: n.Comment, proxied: n.Proxied}
	if f.ttl == 0 {
		f.ttl = 1 // automatic
	}
	switch v := rr.(type) {
	case *mdns.A:
		f.content = v.A.String()
	case *mdns.AAAA:
		f.content = v.AAAA.String()
	case *mdns.CNAME:
		f.content = dns.NormalizeName(v.Target)
	case *mdns.NS:
		f.content = dns.NormalizeName(v.Ns)
	case *mdns.PTR:
		f.content = dns.NormalizeName(v.Ptr)
	case *mdns.MX:
		f.content = dns.NormalizeName(v.Mx)
		prio := float64(v.Preference)
		f.priority = &prio
	case *mdns.TXT:
		f.content = n.Content
	case *mdns.SRV:
		f.data = data{Priority: int(v.Priority), Weight: int(v.Weight), Port: int(v.Port), Target: dns.NormalizeName(v.Target)}
	case *mdns.CAA:
		f.data = data{Flags: int(v.Flag), Tag: v.Tag, Value: v.Value}
	default:
		return fields{}, &dns.ValidationError{Msg: "unsupported type " + n.Type}
	}
	return f, nil
}

var _ dns.Provider = (*Provider)(nil)
