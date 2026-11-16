// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net/url"
	"strconv"
	"time"
)

// DHCP address families.
const (
	DHCPv4 = "v4"
	DHCPv6 = "v6"
)

// DHCP configures the Kea DHCP servers managed by dcdash (see
// docs/adr/0003-kea-api.md).
type DHCP struct {
	Servers []DHCPServer `yaml:"servers"`
}

// DHCPServer is one kea-dhcp4 or kea-dhcp6 daemon, reached over its own
// HTTP(S) control socket.
type DHCPServer struct {
	// Name identifies the server in the UI and in permissions
	// (dhcp:<name>:<resource>:<action>).
	Name string `yaml:"name"`
	// Family is "v4" (kea-dhcp4) or "v6" (kea-dhcp6).
	Family string `yaml:"family"`
	// URL of the control socket, e.g. https://kea1.example.com:8000.
	URL string `yaml:"url"`
	// Insecure allows an http:// URL (password in clear text). Testing only.
	Insecure bool `yaml:"insecure"`
	// TLS: ca_file or fingerprint for the server certificate, cert_file and
	// key_file for a client certificate (Kea's cert-required).
	TLS TLSClient `yaml:"tls"`
	// Username and Password for Kea's basic authentication.
	Username string `yaml:"username"`
	Password Secret `yaml:"password"`
	// Timeout of a single command.
	Timeout Duration `yaml:"timeout"`
	// HAGroup groups the servers of a Kea HA pair (same family). Empty: the
	// server is standalone.
	HAGroup string `yaml:"ha_group"`
}

// DefaultDHCPTimeout is used when a server sets no timeout.
const DefaultDHCPTimeout = 15 * time.Second

// RequestTimeout returns timeout with its default applied.
func (s DHCPServer) RequestTimeout() time.Duration {
	if s.Timeout <= 0 {
		return DefaultDHCPTimeout
	}
	return s.Timeout.D()
}

func (d DHCP) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	groups := map[string]string{} // HA group -> family
	for i, s := range d.Servers {
		pre := "dhcp.servers[" + strconv.Itoa(i) + "]"
		if !instanceNamePattern.MatchString(s.Name) {
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, s.Name)
		} else if seen[s.Name] {
			add("%s.name %q: duplicate", pre, s.Name)
		}
		seen[s.Name] = true
		if s.Family != DHCPv4 && s.Family != DHCPv6 {
			add("%s.family %q: must be v4 or v6", pre, s.Family)
		}
		u, err := url.Parse(s.URL)
		switch {
		case err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http"):
			add("%s.url %q: must be https://host[:port] without a path", pre, s.URL)
		case u.Scheme == "http" && !s.Insecure:
			add("%s.url %q: http sends the password in clear text; use https or set insecure: true", pre, s.URL)
		}
		s.TLS.validate(pre+".tls.", add)
		if s.Username == "" {
			add("%s.username: required", pre)
		}
		if !s.Password.IsSet() {
			add("%s.password: required", pre)
		}
		if s.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
		if s.HAGroup != "" {
			if !instanceNamePattern.MatchString(s.HAGroup) {
				add("%s.ha_group %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, s.HAGroup)
			} else if f, ok := groups[s.HAGroup]; ok && f != s.Family {
				add("%s.ha_group %q: all servers of a group must have the same family", pre, s.HAGroup)
			}
			groups[s.HAGroup] = s.Family
		}
	}
}
