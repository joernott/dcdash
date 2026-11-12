// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// TLSModeACME obtains and renews the certificate via ACME (Let's Encrypt, step-ca, ...).
const TLSModeACME = "acme"

// DNS-01 challenge providers.
const (
	DNSProviderCloudflare = "cloudflare"
	DNSProviderRFC2136    = "rfc2136"
)

// LetsEncryptDirectory is the default ACME directory.
const LetsEncryptDirectory = "https://acme-v02.api.letsencrypt.org/directory"

var (
	acmeKeyTypes   = []string{"EC256", "EC384", "RSA2048", "RSA3072", "RSA4096"}
	tsigAlgorithms = []string{"hmac-sha1", "hmac-sha224", "hmac-sha256", "hmac-sha384", "hmac-sha512"}
)

// ACME configures certificate management via an ACME CA using the DNS-01 challenge.
type ACME struct {
	// DirectoryURL of the ACME server, e.g. Let's Encrypt or
	// https://ca.example.com/acme/<provisioner>/directory for step-ca.
	DirectoryURL string `yaml:"directory_url"`
	// Email is the optional account contact.
	Email string `yaml:"email"`
	// AcceptTOS must be true: it confirms acceptance of the CA's terms of service.
	AcceptTOS bool `yaml:"accept_tos"`
	// Domains to include in the certificate; the first one is the common name.
	Domains []string `yaml:"domains"`
	// KeyType of the certificate key: EC256, EC384, RSA2048, RSA3072, RSA4096.
	KeyType string `yaml:"key_type"`
	// CAFile contains additional root certificates trusted for the connection to
	// the ACME server, e.g. the step-ca root.
	CAFile string `yaml:"ca_file"`
	// RenewBefore is how long before expiry the certificate is renewed. For
	// short-lived certificates at most a third of the lifetime is used.
	RenewBefore Duration `yaml:"renew_before"`
	// EAB is the optional external account binding required by some CAs.
	EAB EAB `yaml:"eab"`
	// DNS configures the DNS-01 challenge.
	DNS ACMEDNS `yaml:"dns"`
}

// EAB holds external account binding credentials.
type EAB struct {
	KeyID   string `yaml:"key_id"`
	HMACKey Secret `yaml:"hmac_key"`
}

// ACMEDNS configures how DNS-01 challenge records are published and checked.
type ACMEDNS struct {
	Provider   string     `yaml:"provider"`
	Cloudflare Cloudflare `yaml:"cloudflare"`
	RFC2136    RFC2136    `yaml:"rfc2136"`
	// Resolvers used to check that the challenge record is visible (host:port).
	// Default: the system resolvers.
	Resolvers []string `yaml:"resolvers"`
	// PropagationTimeout is how long to wait for the record to become visible
	// (0: provider default).
	PropagationTimeout Duration `yaml:"propagation_timeout"`
	// DisableAuthoritativeCheck skips querying the zone's authoritative name
	// servers directly, e.g. when they are not reachable from dcdash.
	DisableAuthoritativeCheck bool `yaml:"disable_authoritative_check"`
}

// Cloudflare holds Cloudflare API credentials for the DNS-01 challenge. The
// token needs Zone:Read and DNS:Edit permission for the zones of all domains.
type Cloudflare struct {
	APIToken Secret `yaml:"api_token"`
	// ZoneToken is an optional separate token with Zone:Read permission.
	ZoneToken Secret `yaml:"zone_token"`
}

// RFC2136 configures dynamic DNS updates (e.g. bind9) for the DNS-01 challenge.
type RFC2136 struct {
	// Nameserver is the primary server accepting updates (host or host:port).
	Nameserver    string `yaml:"nameserver"`
	TSIGKey       string `yaml:"tsig_key"`
	TSIGSecret    Secret `yaml:"tsig_secret"`
	TSIGAlgorithm string `yaml:"tsig_algorithm"`
}

func defaultACME() ACME {
	return ACME{
		DirectoryURL: LetsEncryptDirectory,
		KeyType:      "EC256",
		RenewBefore:  Duration(30 * 24 * time.Hour),
		DNS: ACMEDNS{
			RFC2136: RFC2136{TSIGAlgorithm: "hmac-sha256"},
		},
	}
}

// WithDefaultPort appends ":53" to a DNS server address without port.
func WithDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(strings.Trim(addr, "[]"), "53")
}

func validDomain(d string) bool {
	d = strings.TrimPrefix(d, "*.")
	if d == "" || len(d) > 253 || strings.ContainsAny(d, " /:*") {
		return false
	}
	return !strings.HasPrefix(d, ".") && !strings.HasSuffix(d, ".") && !strings.Contains(d, "..")
}

func (a *ACME) validate(add func(string, ...any)) {
	const p = "server.tls.acme."
	if u, err := url.Parse(a.DirectoryURL); err != nil || u.Scheme != "https" || u.Host == "" {
		add(p+"directory_url %q: must be an https URL", a.DirectoryURL)
	}
	if !a.AcceptTOS {
		add(p + "accept_tos: must be true to accept the terms of service of the CA")
	}
	if len(a.Domains) == 0 {
		add(p + "domains: at least one domain is required")
	}
	for _, d := range a.Domains {
		if !validDomain(d) {
			add(p+"domains: invalid domain %q", d)
		}
	}
	if !slices.Contains(acmeKeyTypes, a.KeyType) {
		add(p+"key_type %q: must be one of %s", a.KeyType, strings.Join(acmeKeyTypes, ", "))
	}
	if a.RenewBefore <= 0 {
		add(p + "renew_before: must be positive")
	}
	if (a.EAB.KeyID == "") != !a.EAB.HMACKey.IsSet() {
		add(p + "eab: key_id and hmac_key must be given together")
	}

	dns := a.DNS
	switch dns.Provider {
	case DNSProviderCloudflare:
		if !dns.Cloudflare.APIToken.IsSet() {
			add(p + "dns.cloudflare.api_token: required")
		}
	case DNSProviderRFC2136:
		r := dns.RFC2136
		if r.Nameserver == "" {
			add(p + "dns.rfc2136.nameserver: required")
		}
		if r.TSIGKey == "" || !r.TSIGSecret.IsSet() {
			add(p + "dns.rfc2136: tsig_key and tsig_secret are required")
		}
		if !slices.Contains(tsigAlgorithms, r.TSIGAlgorithm) {
			add(p+"dns.rfc2136.tsig_algorithm %q: must be one of %s", r.TSIGAlgorithm, strings.Join(tsigAlgorithms, ", "))
		}
	default:
		add(p+"dns.provider %q: must be %q or %q", dns.Provider, DNSProviderCloudflare, DNSProviderRFC2136)
	}
	for _, r := range dns.Resolvers {
		if _, _, err := net.SplitHostPort(WithDefaultPort(r)); err != nil || r == "" {
			add(p+"dns.resolvers: invalid address %q", r)
		}
	}
	if dns.PropagationTimeout < 0 {
		add(p + "dns.propagation_timeout: must not be negative")
	}
}
