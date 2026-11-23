// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// FirewallTypeOPNsense is an OPNsense firewall (REST API with key and secret).
const FirewallTypeOPNsense = "opnsense"

// Firewalls configures the firewalls managed by dcdash (see docs/adr/0009-opnsense.md).
type Firewalls []Firewall

// Firewall is one firewall, reached over its REST API.
type Firewall struct {
	// Name identifies the firewall in the UI and in permissions
	// (firewall:<name>:<resource>:<action>).
	Name string `yaml:"name"`
	// Type of the firewall API; "opnsense" (default) is the only one so far.
	Type string `yaml:"type"`
	// URL of the web interface without path, e.g. https://fw1.example.com.
	URL string `yaml:"url"`
	// Insecure allows an http:// URL (credentials in clear text). Testing only.
	Insecure bool `yaml:"insecure"`
	// TLS: the default web interface certificate is self-signed; pin it with
	// fingerprint, or use ca_file for a certificate of your CA.
	TLS TLSClient `yaml:"tls"`
	// APIKey and APISecret of an OPNsense user (System > Access > Users > API keys).
	APIKey    string `yaml:"api_key"`
	APISecret Secret `yaml:"api_secret"`
	// Timeout of a single request.
	Timeout Duration `yaml:"timeout"`
	// ProtectedInterfaces are interface keys (opt1, lan, ...) whose rules dcdash
	// doesn't change on a firewall without savepoints, in addition to the
	// interface it reaches the firewall through, which it detects itself.
	ProtectedInterfaces []string `yaml:"protected_interfaces"`
	// Links: web interface, SSH and RDP of the system, shown on its overview tile.
	Links `yaml:",inline"`
}

// DefaultFirewallTimeout is used when a firewall sets no timeout.
const DefaultFirewallTimeout = 30 * time.Second

// RequestTimeout returns timeout with its default applied.
func (f Firewall) RequestTimeout() time.Duration {
	if f.Timeout <= 0 {
		return DefaultFirewallTimeout
	}
	return f.Timeout.D()
}

func (f Firewalls) applyDefaults() {
	for i := range f {
		if f[i].Type == "" {
			f[i].Type = FirewallTypeOPNsense
		}
	}
}

// interfaceKeyPattern matches OPNsense interface keys (lan, wan, opt1, ...).
var interfaceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.]{1,32}$`)

func (f Firewalls) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, fw := range f {
		pre := "firewalls[" + strconv.Itoa(i) + "]"
		validateLinks(fw.Links, pre+".", add)
		if !instanceNamePattern.MatchString(fw.Name) {
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, fw.Name)
		} else if seen[fw.Name] {
			add("%s.name %q: duplicate", pre, fw.Name)
		}
		seen[fw.Name] = true
		if fw.Type != FirewallTypeOPNsense {
			add("%s.type %q: must be opnsense", pre, fw.Type)
		}
		u, err := url.Parse(fw.URL)
		switch {
		case err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http"):
			add("%s.url %q: must be https://host[:port] without a path", pre, fw.URL)
		case u.Scheme == "http" && !fw.Insecure:
			add("%s.url %q: http sends the API secret in clear text; use https or set insecure: true", pre, fw.URL)
		}
		fw.TLS.validate(pre+".tls.", add)
		if fw.APIKey == "" {
			add("%s.api_key: required", pre)
		}
		if !fw.APISecret.IsSet() {
			add("%s.api_secret: required", pre)
		}
		if fw.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
		for j, k := range fw.ProtectedInterfaces {
			if !interfaceKeyPattern.MatchString(k) {
				add("%s.protected_interfaces[%d] %q: an interface key such as lan or opt1", pre, j, k)
			}
		}
	}
}
