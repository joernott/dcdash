// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DNS provider types.
const (
	DNSTypeCloudflare = "cloudflare"
	DNSTypeBind       = "bind"
)

// DNS configures the DNS providers managed by dcdash.
type DNS struct {
	Providers []DNSProvider `yaml:"providers"`
}

// DNSProvider is one DNS backend: a Cloudflare account or a bind9 primary.
type DNSProvider struct {
	// Name identifies the provider in the UI and in permissions
	// (dns:<name>:<zone>:<action>).
	Name string `yaml:"name"`
	// Type is "cloudflare" or "bind".
	Type string `yaml:"type"`
	// Timeout of a single request to the backend.
	Timeout    Duration      `yaml:"timeout"`
	Cloudflare DNSCloudflare `yaml:"cloudflare"`
	Bind       Bind          `yaml:"bind"`
}

// DefaultDNSTimeout is used when a provider sets no timeout.
const DefaultDNSTimeout = 15 * time.Second

// RequestTimeout returns the timeout with its default applied.
func (p DNSProvider) RequestTimeout() time.Duration {
	if p.Timeout <= 0 {
		return DefaultDNSTimeout
	}
	return p.Timeout.D()
}

// DNSCloudflare configures a Cloudflare account for DNS management.
type DNSCloudflare struct {
	// APIToken needs Zone:Read and DNS:Edit; Zone:Edit to create and delete
	// zones. Use a different token than for ACME.
	APIToken Secret `yaml:"api_token"`
	// AccountID is needed to create zones. Empty: zones can't be created.
	AccountID string `yaml:"account_id"`
	// BaseURL overrides the API URL (tests only).
	BaseURL string `yaml:"base_url"`
}

// Bind configures a bind9 primary. Records are read with AXFR and changed
// with RFC 2136 updates, both signed with the TSIG key.
type Bind struct {
	RFC2136 `yaml:",inline"`
	// Zones lists zones to manage in addition to those in the catalog zone.
	Zones []string `yaml:"zones"`
	// CatalogZone (RFC 9432) lists the zones of the primary. dcdash reads it
	// with AXFR and adds and removes zones in it when it creates or deletes
	// them, so secondaries follow.
	CatalogZone string `yaml:"catalog_zone"`
	// RNDC runs server operations (status, reload, addzone, delzone).
	RNDC RNDC `yaml:"rndc"`
	// SFTP uploads and removes zone files on the primary.
	SFTP SFTP `yaml:"sftp"`
	// NewZones is the template for zones created by dcdash.
	NewZones NewZones `yaml:"new_zones"`
}

// RNDC configures the rndc binary used for server operations.
type RNDC struct {
	// Binary is the path of rndc; empty: rndc is not used.
	Binary string `yaml:"binary"`
	// Server and Port of the rndc control channel (default port 953).
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`
	// KeyFile contains the rndc key (rndc -k).
	KeyFile string `yaml:"key_file"`
}

// Enabled reports whether rndc is configured.
func (r RNDC) Enabled() bool { return r.Binary != "" }

// SFTP configures the SFTP access to the zone directory of a primary.
type SFTP struct {
	// Address is host:port of the SSH server (default port 22). Empty: SFTP
	// is not used.
	Address string `yaml:"address"`
	User    string `yaml:"user"`
	// PrivateKeyFile is the dedicated key of dcdash for this primary.
	PrivateKeyFile string `yaml:"private_key_file"`
	// HostKey pins the server's host key ("ssh-ed25519 AAAA..."), or
	// KnownHostsFile names a known_hosts file. One of them is required:
	// unknown host keys are rejected.
	HostKey        string `yaml:"host_key"`
	KnownHostsFile string `yaml:"known_hosts_file"`
	// ZoneDir is the zone directory as seen over SFTP (e.g. in a chroot).
	ZoneDir string `yaml:"zone_dir"`
}

// Enabled reports whether SFTP is configured.
func (s SFTP) Enabled() bool { return s.Address != "" }

// NewZones is the template for zones created by dcdash.
type NewZones struct {
	// FileDir is the zone directory as seen by named (for rndc addzone).
	// Empty: dcdash can't create zones.
	FileDir string `yaml:"file_dir"`
	// Options are the zone options for rndc addzone, without "type" and
	// "file". Default: updates and transfers for the TSIG key.
	Options string `yaml:"options"`
	// TTL of the SOA and NS records.
	TTL Duration `yaml:"ttl"`
	SOA SOA      `yaml:"soa"`
	// NameServers are the NS records of a new zone.
	NameServers []string `yaml:"nameservers"`
}

// SOA is the SOA template of new zones.
type SOA struct {
	// MName is the primary name server, RName the contact mailbox in
	// domain form (hostmaster.example.com).
	MName   string   `yaml:"mname"`
	RName   string   `yaml:"rname"`
	Refresh Duration `yaml:"refresh"`
	Retry   Duration `yaml:"retry"`
	Expire  Duration `yaml:"expire"`
	Minimum Duration `yaml:"minimum"`
}

// ZoneCreation reports whether the provider can create and delete zones.
func (b Bind) ZoneCreation() bool {
	return b.RNDC.Enabled() && b.SFTP.Enabled() && b.NewZones.FileDir != ""
}

// ZoneOptions returns the options for rndc addzone with the default applied.
func (b Bind) ZoneOptions() string {
	if b.NewZones.Options != "" {
		return b.NewZones.Options
	}
	k := strings.TrimSuffix(b.TSIGKey, ".") + "."
	return "update-policy { grant " + k + " zonesub ANY; }; allow-transfer { key " + k + "; };"
}

func defaultNewZones() NewZones {
	return NewZones{
		TTL: Duration(time.Hour),
		SOA: SOA{
			Refresh: Duration(time.Hour), Retry: Duration(15 * time.Minute),
			Expire: Duration(14 * 24 * time.Hour), Minimum: Duration(5 * time.Minute),
		},
	}
}

// applyDefaults fills in defaults that YAML can't express for list items.
func (d *DNS) applyDefaults() {
	def := defaultNewZones()
	for i := range d.Providers {
		b := &d.Providers[i].Bind
		if b.TSIGAlgorithm == "" {
			b.TSIGAlgorithm = "hmac-sha256"
		}
		if b.RNDC.Port == 0 {
			b.RNDC.Port = 953
		}
		n := &b.NewZones
		if n.TTL == 0 {
			n.TTL = def.TTL
		}
		for _, f := range []struct{ v, d *Duration }{
			{&n.SOA.Refresh, &def.SOA.Refresh}, {&n.SOA.Retry, &def.SOA.Retry},
			{&n.SOA.Expire, &def.SOA.Expire}, {&n.SOA.Minimum, &def.SOA.Minimum},
		} {
			if *f.v == 0 {
				*f.v = *f.d
			}
		}
	}
}

func (d DNS) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, p := range d.Providers {
		pre := "dns.providers[" + strconv.Itoa(i) + "]"
		if !instanceNamePattern.MatchString(p.Name) {
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, p.Name)
		} else if seen[p.Name] {
			add("%s.name %q: duplicate", pre, p.Name)
		}
		seen[p.Name] = true
		if p.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
		switch p.Type {
		case DNSTypeCloudflare:
			p.Cloudflare.validate(pre+".cloudflare", add)
		case DNSTypeBind:
			p.Bind.validate(pre+".bind", add)
		default:
			add("%s.type %q: must be cloudflare or bind", pre, p.Type)
		}
	}
}

func (c DNSCloudflare) validate(pre string, add func(string, ...any)) {
	if !c.APIToken.IsSet() {
		add("%s.api_token: required", pre)
	}
	if c.BaseURL != "" {
		if u, err := url.Parse(c.BaseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			add("%s.base_url %q: must be an http(s) URL", pre, c.BaseURL)
		}
	}
}

func (b Bind) validate(pre string, add func(string, ...any)) {
	if b.Nameserver == "" {
		add("%s.nameserver: required", pre)
	}
	if b.TSIGKey == "" || !b.TSIGSecret.IsSet() {
		add("%s.tsig_key and tsig_secret: required", pre)
	}
	if !slices.Contains(tsigAlgorithms, b.TSIGAlgorithm) {
		add("%s.tsig_algorithm %q: must be one of %s", pre, b.TSIGAlgorithm, strings.Join(tsigAlgorithms, ", "))
	}
	for _, z := range b.Zones {
		if !validDomain(z) || strings.HasPrefix(z, "*.") {
			add("%s.zones: invalid zone %q", pre, z)
		}
	}
	if b.CatalogZone != "" && (!validDomain(b.CatalogZone) || strings.HasPrefix(b.CatalogZone, "*.")) {
		add("%s.catalog_zone %q: invalid zone name", pre, b.CatalogZone)
	}
	if len(b.Zones) == 0 && b.CatalogZone == "" {
		add("%s: zones or catalog_zone required", pre)
	}
	if r := b.RNDC; r.Enabled() {
		if !path.IsAbs(r.Binary) {
			add("%s.rndc.binary %q: must be an absolute path", pre, r.Binary)
		}
		if r.Server == "" || r.KeyFile == "" {
			add("%s.rndc: server and key_file required", pre)
		}
		if r.Port < 1 || r.Port > 65535 {
			add("%s.rndc.port %d: invalid", pre, r.Port)
		}
	}
	if s := b.SFTP; s.Enabled() {
		if _, _, err := net.SplitHostPort(withPort(s.Address, "22")); err != nil {
			add("%s.sftp.address %q: must be host[:port]", pre, s.Address)
		}
		if s.User == "" || s.PrivateKeyFile == "" {
			add("%s.sftp: user and private_key_file required", pre)
		}
		if (s.HostKey == "") == (s.KnownHostsFile == "") {
			add("%s.sftp: exactly one of host_key and known_hosts_file required", pre)
		}
		if !path.IsAbs(s.ZoneDir) {
			add("%s.sftp.zone_dir %q: must be an absolute path", pre, s.ZoneDir)
		}
	}
	n := b.NewZones
	if n.FileDir == "" {
		return
	}
	if !b.RNDC.Enabled() || !b.SFTP.Enabled() {
		add("%s.new_zones: needs rndc and sftp", pre)
	}
	if !path.IsAbs(n.FileDir) {
		add("%s.new_zones.file_dir %q: must be an absolute path", pre, n.FileDir)
	}
	if !validDomain(n.SOA.MName) || !validDomain(n.SOA.RName) {
		add("%s.new_zones.soa: mname and rname must be domain names", pre)
	}
	if len(n.NameServers) == 0 {
		add("%s.new_zones.nameservers: at least one required", pre)
	}
	for _, ns := range n.NameServers {
		if !validDomain(ns) {
			add("%s.new_zones.nameservers: invalid name %q", pre, ns)
		}
	}
	if strings.ContainsAny(n.Options, "\n\r") {
		add("%s.new_zones.options: must be a single line", pre)
	}
}

// withPort appends a default port to an address without one.
func withPort(addr, port string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), port)
}
