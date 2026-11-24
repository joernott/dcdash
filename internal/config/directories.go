// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Directory types. Both are Active Directory domains; the type selects defaults and the
// error texts dcdash expects (see docs/adr/0010-active-directory.md).
const (
	DirectoryTypeSamba   = "samba"
	DirectoryTypeWindows = "windows"
)

// Directories configures the Active Directory domains managed by dcdash. They are
// independent of the login directory (auth.ldap), even if that is one of them.
type Directories []Directory

// Directory is one Active Directory domain, reached over LDAPS.
type Directory struct {
	// Name identifies the directory in the UI, in permissions
	// (directory:<name>:<resource>:<action>) and in project files.
	Name string `yaml:"name"`
	// Type is "samba" or "windows".
	Type string `yaml:"type"`
	// Domain is the DNS name of the domain, e.g. ad.example.com.
	Domain string `yaml:"domain"`
	// Servers are the domain controllers, tried in order (ldaps:// URLs only: dcdash sets
	// passwords, which needs an encrypted connection).
	Servers []LDAPServer `yaml:"servers"`
	TLS     TLSClient    `yaml:"tls"`
	// BindDN and BindPassword of the service account dcdash manages the domain with
	// (DN or user principal name).
	BindDN       string `yaml:"bind_dn"`
	BindPassword Secret `yaml:"bind_password"`
	// BaseDN of the domain; default derived from Domain (DC=ad,DC=example,DC=com).
	BaseDN string `yaml:"base_dn"`
	// ManagedOUs are the containers dcdash may create, change and delete users and groups
	// in (DNs below BaseDN); default: the whole domain. Objects elsewhere are read-only.
	ManagedOUs []string `yaml:"managed_ous"`
	// ProtectedUsers and ProtectedGroups (sAMAccountName) can only be changed with the
	// admin action, in addition to the built-in accounts and groups.
	ProtectedUsers  []string `yaml:"protected_users"`
	ProtectedGroups []string `yaml:"protected_groups"`
	// Kerberos is used for signed DNS updates (GSS-TSIG).
	Kerberos Kerberos `yaml:"kerberos"`
	// SelfService offers the directory on the self-service password page (default true).
	SelfService *bool `yaml:"self_service"`
	// Timeout for connecting and for each operation.
	Timeout Duration `yaml:"timeout"`
	// Links: web interface, SSH and RDP of the system, shown on its overview tile.
	Links `yaml:",inline"`
}

// Kerberos settings of a directory.
type Kerberos struct {
	// Realm, default: the domain in upper case.
	Realm string `yaml:"realm"`
	// KDCs as host[:port]; default: the hosts of the servers, port 88.
	KDCs []string `yaml:"kdcs"`
}

// DefaultDirectoryTimeout is used when a directory sets no timeout.
const DefaultDirectoryTimeout = 10 * time.Second

// RequestTimeout returns the timeout with its default applied.
func (d Directory) RequestTimeout() time.Duration {
	if d.Timeout <= 0 {
		return DefaultDirectoryTimeout
	}
	return d.Timeout.D()
}

// SelfServiceEnabled reports whether users of the directory may change their password on
// the self-service page.
func (d Directory) SelfServiceEnabled() bool { return d.SelfService == nil || *d.SelfService }

// TLSFor returns the TLS settings for a server: tls, with the fingerprint of the server if
// it has one.
func (d Directory) TLSFor(s LDAPServer) TLSClient {
	t := d.TLS
	if s.Fingerprint != "" {
		t.Fingerprint = s.Fingerprint
	}
	return t
}

// Managed reports whether dn lies in one of the managed containers.
func (d Directory) Managed(dn string) bool {
	for _, ou := range d.ManagedOUs {
		if DNWithin(dn, ou) {
			return true
		}
	}
	return false
}

// DNWithin reports whether dn is container or lies below it. DNs are compared as DNs:
// case-insensitive, and spaces around "," and "=" don't matter. Strings that aren't DNs
// are compared as text.
func DNWithin(dn, container string) bool {
	d, err1 := ldap.ParseDN(dn)
	c, err2 := ldap.ParseDN(container)
	if err1 != nil || err2 != nil {
		dn, container = strings.ToLower(dn), strings.ToLower(container)
		return dn == container || strings.HasSuffix(dn, ","+container)
	}
	return c.EqualFold(d) || c.AncestorOfFold(d)
}

// domainPattern matches a DNS domain name with at least two labels.
var domainPattern = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// DomainBaseDN returns the base DN of a DNS domain: DC=ad,DC=example,DC=com.
func DomainBaseDN(domain string) string {
	labels := strings.Split(strings.ToLower(domain), ".")
	for i, l := range labels {
		labels[i] = "DC=" + l
	}
	return strings.Join(labels, ",")
}

func (d Directories) applyDefaults() {
	for i := range d {
		x := &d[i]
		if x.BaseDN == "" && x.Domain != "" {
			x.BaseDN = DomainBaseDN(x.Domain)
		}
		if len(x.ManagedOUs) == 0 && x.BaseDN != "" {
			x.ManagedOUs = []string{x.BaseDN}
		}
		if x.Kerberos.Realm == "" {
			x.Kerberos.Realm = strings.ToUpper(x.Domain)
		}
		if len(x.Kerberos.KDCs) == 0 {
			for _, s := range x.Servers {
				if u, err := url.Parse(s.URL); err == nil && u.Hostname() != "" {
					x.Kerberos.KDCs = append(x.Kerberos.KDCs, net.JoinHostPort(u.Hostname(), "88"))
				}
			}
		}
		for j, k := range x.Kerberos.KDCs {
			x.Kerberos.KDCs[j] = withPort(k, "88")
		}
	}
}

func (d Directories) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, x := range d {
		pre := "directories[" + strconv.Itoa(i) + "]"
		validateLinks(x.Links, pre+".", add)
		if !instanceNamePattern.MatchString(x.Name) {
			add("%s.name %q: lower-case letters, digits, '.', '-' and '_' (max. 64)", pre, x.Name)
		} else if seen[x.Name] {
			add("%s.name %q: duplicate", pre, x.Name)
		}
		seen[x.Name] = true
		if x.Type != DirectoryTypeSamba && x.Type != DirectoryTypeWindows {
			add("%s.type %q: must be %s or %s", pre, x.Type, DirectoryTypeSamba, DirectoryTypeWindows)
		}
		if !domainPattern.MatchString(x.Domain) {
			add("%s.domain %q: the DNS name of the domain, e.g. ad.example.com", pre, x.Domain)
		}
		if len(x.Servers) == 0 {
			add("%s.servers: required", pre)
		}
		for j, s := range x.Servers {
			key := pre + ".servers[" + strconv.Itoa(j) + "]"
			if u, err := url.Parse(s.URL); err != nil || u.Scheme != "ldaps" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
				add("%s.url %q: must be ldaps://host[:port]", key, s.URL)
			}
			if s.Fingerprint != "" {
				TLSClient{Fingerprint: s.Fingerprint}.validate(key+".", add)
			}
		}
		x.TLS.validate(pre+".tls.", add)
		if x.BindDN == "" || !x.BindPassword.IsSet() {
			add("%s.bind_dn and bind_password: required", pre)
		}
		base := strings.ToLower(x.BaseDN)
		if !strings.HasPrefix(base, "dc=") {
			add("%s.base_dn %q: must be the domain's DN (DC=...)", pre, x.BaseDN)
		}
		for j, ou := range x.ManagedOUs {
			if _, err := ldap.ParseDN(ou); err != nil {
				add("%s.managed_ous[%d] %q: not a DN: %v", pre, j, ou, err)
			} else if !DNWithin(ou, x.BaseDN) {
				add("%s.managed_ous[%d] %q: must be below base_dn %q", pre, j, ou, x.BaseDN)
			}
		}
		for j, u := range x.ProtectedUsers {
			if strings.TrimSpace(u) == "" {
				add("%s.protected_users[%d]: empty", pre, j)
			}
		}
		for j, g := range x.ProtectedGroups {
			if strings.TrimSpace(g) == "" {
				add("%s.protected_groups[%d]: empty", pre, j)
			}
		}
		if x.Kerberos.Realm == "" || strings.ContainsAny(x.Kerberos.Realm, " @/") {
			add("%s.kerberos.realm %q: invalid", pre, x.Kerberos.Realm)
		}
		for j, k := range x.Kerberos.KDCs {
			if h, p, err := net.SplitHostPort(k); err != nil || h == "" || p == "" {
				add("%s.kerberos.kdcs[%d] %q: must be host[:port]", pre, j, k)
			}
		}
		if x.Timeout < 0 {
			add("%s.timeout: must not be negative", pre)
		}
	}
}
