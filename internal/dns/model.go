// SPDX-License-Identifier: GPL-3.0-or-later

// Package dns manages DNS zones and records in Cloudflare and bind9 through a
// common model. Record content uses the presentation format of zone files
// (RFC 1035), e.g. "10 mail.example.com." for MX, so one validation covers
// all types.
package dns

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	mdns "github.com/miekg/dns"
)

// EditableTypes are the record types dcdash can create and change. Other
// types are listed read-only.
var EditableTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "PTR", "NS", "CAA"}

// Errors of providers. The API maps them to HTTP status codes.
var (
	// ErrNotFound: the zone or record doesn't exist.
	ErrNotFound = errors.New("dns: not found")
	// ErrConflict: the zone or record changed since it was read (version
	// mismatch); reload and try again.
	ErrConflict = errors.New("dns: changed concurrently")
	// ErrExists: the zone or record exists already.
	ErrExists = errors.New("dns: already exists")
	// ErrUnsupported: the provider can't do this (e.g. create zones).
	ErrUnsupported = errors.New("dns: not supported by this provider")
)

// ValidationError describes an invalid record or zone.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "dns: " + e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// Zone is a DNS zone.
type Zone struct {
	// Name without trailing dot, lower case.
	Name string `json:"name"`
	// Status is provider specific, e.g. "active" or "pending" (Cloudflare).
	Status string `json:"status,omitempty"`
	// ID is the provider's zone ID (Cloudflare), empty for bind9.
	ID string `json:"-"`
}

// Record is one resource record.
type Record struct {
	// ID identifies the record within the zone: the Cloudflare record ID, or
	// for bind9 a hash of name, type and content.
	ID string `json:"id"`
	// Name is the fully qualified owner name without trailing dot.
	Name string `json:"name"`
	Type string `json:"type"`
	// TTL in seconds; 1 means "automatic" for Cloudflare.
	TTL uint32 `json:"ttl"`
	// Content is the RDATA in presentation format.
	Content string `json:"content"`
	// Proxied is Cloudflare's proxy flag (A, AAAA, CNAME only).
	Proxied *bool `json:"proxied,omitempty"`
	// Comment is a Cloudflare record comment.
	Comment string `json:"comment,omitempty"`
	// ReadOnly records (SOA, DNSSEC, unknown types) can't be changed.
	ReadOnly bool `json:"readOnly,omitempty"`
	// Version must be sent back with an update or delete (optimistic
	// locking): the zone's SOA serial for bind9, the modification time for
	// Cloudflare.
	Version string `json:"version"`
}

// RecordSet is the content of a zone.
type RecordSet struct {
	Records []Record `json:"records"`
	// Version must be sent with a new record (bind9: SOA serial; empty for
	// Cloudflare).
	Version string `json:"version"`
}

// Capabilities describe provider-specific features for the UI.
type Capabilities struct {
	// Proxied and Comments are Cloudflare record fields.
	Proxied  bool `json:"proxied"`
	Comments bool `json:"comments"`
	// AutoTTL: TTL 1 means automatic.
	AutoTTL bool `json:"autoTtl"`
	// ZoneCreate: zones can be created and deleted.
	ZoneCreate bool `json:"zoneCreate"`
}

// Provider is a DNS backend. Zone names are passed without trailing dot.
type Provider interface {
	Name() string
	// Kind is "cloudflare" or "bind".
	Kind() string
	Capabilities() Capabilities
	// Check tests the connection and returns a short description.
	Check(ctx context.Context) (string, error)
	Zones(ctx context.Context) ([]Zone, error)
	CreateZone(ctx context.Context, name string) (Zone, error)
	DeleteZone(ctx context.Context, name string) error
	Records(ctx context.Context, zone string) (RecordSet, error)
	// CreateRecord adds a record; version is RecordSet.Version.
	CreateRecord(ctx context.Context, zone string, r Record, version string) (Record, error)
	// UpdateRecord replaces the record with the given ID; version is the
	// record's Version.
	UpdateRecord(ctx context.Context, zone, id string, r Record, version string) (Record, error)
	DeleteRecord(ctx context.Context, zone, id, version string) error
}

// NormalizeName lower-cases a domain name and removes the trailing dot.
func NormalizeName(n string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
}

// ValidZoneName reports whether n is a valid zone name (no wildcard).
func ValidZoneName(n string) bool {
	if n == "" || strings.HasPrefix(n, "*") || strings.HasSuffix(n, ".") {
		return false
	}
	_, ok := mdns.IsDomainName(n)
	return ok && !strings.ContainsAny(n, " /\\:@\"'")
}

// InZone reports whether name is the zone apex or below it.
func InZone(name, zone string) bool {
	return name == zone || strings.HasSuffix(name, "."+zone)
}

// Normalize validates a record for the zone and returns it in canonical form:
// the name is fully qualified (a relative name or "@" is completed with the
// zone), lower-case, and the content is re-rendered by miekg/dns. Fields set by
// the provider (ID, ReadOnly, Version) are cleared.
func Normalize(r Record, zone string) (Record, error) {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	if !slices.Contains(EditableTypes, r.Type) {
		return Record{}, invalid("type %q can't be edited (supported: %s)", r.Type, strings.Join(EditableTypes, ", "))
	}
	name := strings.TrimSpace(r.Name)
	switch {
	case name == "" || name == "@":
		name = zone
	case !strings.HasSuffix(name, "."):
		// Relative to the zone unless it already ends with the zone name.
		if n := NormalizeName(name); !InZone(n, zone) {
			name = name + "." + zone
		}
	}
	name = NormalizeName(name)
	if _, ok := mdns.IsDomainName(name); !ok || strings.ContainsAny(name, " /\\:@\"'") {
		return Record{}, invalid("invalid name %q", r.Name)
	}
	if !InZone(name, zone) {
		return Record{}, invalid("name %q is not in zone %s", name, zone)
	}
	if strings.Contains(name, "*") && !strings.HasPrefix(name, "*.") || strings.Count(name, "*") > 1 {
		return Record{}, invalid("a wildcard must be the leftmost label: %q", name)
	}
	if r.TTL > 1<<31-1 {
		return Record{}, invalid("TTL %d too large", r.TTL)
	}
	content := strings.TrimSpace(r.Content)
	if r.Type == "TXT" && content != "" && !strings.HasPrefix(content, `"`) {
		content = quoteTXT(content)
	}
	if content == "" || strings.ContainsAny(content, "\n\r;") && r.Type != "TXT" || strings.ContainsAny(content, "\n\r") {
		return Record{}, invalid("invalid content for %s", r.Type)
	}
	rr, err := mdns.NewRR(mdns.Fqdn(name) + " 3600 IN " + r.Type + " " + content)
	if err != nil || rr == nil {
		return Record{}, invalid("invalid %s content %q", r.Type, r.Content)
	}
	if r.Type == "CNAME" && name == zone {
		return Record{}, invalid("a CNAME can't be at the zone apex")
	}
	out := Record{Name: name, Type: r.Type, TTL: r.TTL, Content: RDataString(rr), Comment: strings.TrimSpace(r.Comment)}
	if r.Proxied != nil && *r.Proxied {
		if r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
			return Record{}, invalid("only A, AAAA and CNAME records can be proxied")
		}
		t := true
		out.Proxied = &t
	} else if r.Proxied != nil {
		f := false
		out.Proxied = &f
	}
	return out, nil
}

// quoteTXT turns plain text into TXT presentation format: quoted strings of
// at most 255 bytes, with '"' and '\' escaped.
func quoteTXT(s string) string {
	var parts []string
	for len(s) > 0 {
		n := min(len(s), 255)
		chunk := s[:n]
		s = s[n:]
		chunk = strings.ReplaceAll(strings.ReplaceAll(chunk, `\`, `\\`), `"`, `\"`)
		parts = append(parts, `"`+chunk+`"`)
	}
	return strings.Join(parts, " ")
}

// RDataString returns the RDATA of rr in presentation format.
func RDataString(rr mdns.RR) string {
	s := rr.String()
	// The header is "name\tttl\tclass\ttype\t"; everything after it is RDATA.
	hdr := rr.Header().String()
	return strings.TrimSpace(strings.TrimPrefix(s, hdr))
}

// FromRR converts a resource record into the model. Types outside
// EditableTypes are read-only.
func FromRR(rr mdns.RR) Record {
	h := rr.Header()
	typ := mdns.TypeToString[h.Rrtype]
	if typ == "" {
		typ = "TYPE" + strconv.Itoa(int(h.Rrtype))
	}
	r := Record{
		Name:    NormalizeName(h.Name),
		Type:    typ,
		TTL:     h.Ttl,
		Content: RDataString(rr),
	}
	r.ReadOnly = !slices.Contains(EditableTypes, typ)
	r.ID = RecordID(r)
	return r
}

// RecordID derives a stable ID from name, type and content, for providers
// without record IDs (bind9). The TTL is not part of it, so a TTL change
// keeps the ID.
func RecordID(r Record) string {
	sum := sha256.Sum256([]byte(r.Name + "\x00" + r.Type + "\x00" + r.Content))
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// ToRR converts a normalized record into a resource record.
func ToRR(r Record) (mdns.RR, error) {
	rr, err := mdns.NewRR(mdns.Fqdn(r.Name) + " " + strconv.FormatUint(uint64(r.TTL), 10) + " IN " + r.Type + " " + r.Content)
	if err != nil || rr == nil {
		return nil, invalid("invalid record %s %s %q", r.Name, r.Type, r.Content)
	}
	return rr, nil
}
