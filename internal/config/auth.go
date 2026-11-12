// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

// LDAP directory flavors.
const (
	LDAPModeAD      = "ad"      // Active Directory / Samba AD
	LDAPModeGeneric = "generic" // e.g. OpenLDAP
)

// Auth configures authentication.
type Auth struct {
	LDAP    LDAP    `yaml:"ldap"`
	Session Session `yaml:"session"`
	Lockout Lockout `yaml:"lockout"`
}

// Lockout limits failed login attempts per user name and per client IP address.
type Lockout struct {
	User LockoutPolicy `yaml:"user"`
	IP   LockoutPolicy `yaml:"ip"`
}

// LockoutPolicy locks a key for Duration after MaxFailures failures within Window.
type LockoutPolicy struct {
	MaxFailures int      `yaml:"max_failures"`
	Window      Duration `yaml:"window"`
	Duration    Duration `yaml:"duration"`
}

func defaultLockout() Lockout {
	return Lockout{
		User: LockoutPolicy{MaxFailures: 5, Window: Duration(15 * time.Minute), Duration: Duration(15 * time.Minute)},
		IP:   LockoutPolicy{MaxFailures: 20, Window: Duration(15 * time.Minute), Duration: Duration(15 * time.Minute)},
	}
}

func (p LockoutPolicy) validate(prefix string, add func(string, ...any)) {
	if p.MaxFailures < 1 || p.Window <= 0 || p.Duration <= 0 {
		add("%s: max_failures, window and duration must be positive", prefix)
	}
}

// Session configures login sessions.
type Session struct {
	// Lifetime is the maximum duration of a session, regardless of activity.
	Lifetime Duration `yaml:"lifetime"`
	// IdleTimeout ends a session after this duration without requests.
	IdleTimeout Duration `yaml:"idle_timeout"`
}

func defaultSession() Session {
	return Session{Lifetime: Duration(8 * time.Hour), IdleTimeout: Duration(30 * time.Minute)}
}

func (s Session) validate(add func(string, ...any)) {
	if s.Lifetime <= 0 {
		add("auth.session.lifetime: must be positive")
	}
	if s.IdleTimeout <= 0 || s.IdleTimeout > s.Lifetime {
		add("auth.session.idle_timeout: must be positive and not longer than lifetime")
	}
}

// TLSClient configures how dcdash verifies a server it connects to.
type TLSClient struct {
	// CAFile contains additional trusted root certificates (PEM).
	CAFile string `yaml:"ca_file"`
	// ServerName overrides the host name expected in the server certificate.
	ServerName string `yaml:"server_name"`
	// Fingerprint pins the server certificate by its SHA-256 fingerprint (hex,
	// colons optional). The certificate is then accepted regardless of issuer
	// and host name, e.g. for self-signed certificates without SAN.
	Fingerprint string `yaml:"fingerprint"`
}

// NormalizedFingerprint returns the fingerprint as lower-case hex without colons.
func (t TLSClient) NormalizedFingerprint() string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(t.Fingerprint))
}

func (t TLSClient) validate(prefix string, add func(string, ...any)) {
	if t.Fingerprint == "" {
		return
	}
	fp := t.NormalizedFingerprint()
	if b, err := hex.DecodeString(fp); err != nil || len(b) != 32 {
		add("%sfingerprint: must be a SHA-256 fingerprint (64 hex digits, colons allowed)", prefix)
	}
}

// LDAP configures the directory used for authentication.
type LDAP struct {
	// URL of the directory: ldaps://host:636, or ldap://host:389 with start_tls.
	URL string `yaml:"url"`
	// StartTLS upgrades an ldap:// connection to TLS.
	StartTLS bool `yaml:"start_tls"`
	// Insecure allows ldap:// without StartTLS (passwords in clear text). Testing only.
	Insecure bool      `yaml:"insecure"`
	TLS      TLSClient `yaml:"tls"`
	// Mode is "ad" (Active Directory, Samba AD) or "generic".
	Mode string `yaml:"mode"`
	// BindDN and BindPassword of the service account used to search users and groups.
	BindDN       string `yaml:"bind_dn"`
	BindPassword Secret `yaml:"bind_password"`
	// BaseDN below which users are searched.
	BaseDN string `yaml:"base_dn"`
	// UserFilter finds the user; {username} is replaced by the escaped login name.
	// Default depends on mode.
	UserFilter string `yaml:"user_filter"`
	// GroupBaseDN below which groups are searched (default: base_dn).
	GroupBaseDN string `yaml:"group_base_dn"`
	// GroupFilter (generic mode) finds the groups of a user; {dn} is replaced
	// by the escaped user DN, {username} by the escaped login name.
	GroupFilter string `yaml:"group_filter"`
	// Attributes maps user attributes.
	Attributes LDAPAttributes `yaml:"attributes"`
	// Timeout for connecting and for each operation.
	Timeout Duration `yaml:"timeout"`
}

// LDAPAttributes names the user attributes dcdash reads.
type LDAPAttributes struct {
	Username    string `yaml:"username"`
	DisplayName string `yaml:"display_name"`
	Email       string `yaml:"email"`
}

// Defaults per directory mode.
var ldapDefaults = map[string]struct {
	userFilter  string
	groupFilter string
	attrs       LDAPAttributes
}{
	LDAPModeAD: {
		userFilter: "(&(objectCategory=person)(objectClass=user)(|(sAMAccountName={username})(userPrincipalName={username})))",
		attrs:      LDAPAttributes{Username: "sAMAccountName", DisplayName: "displayName", Email: "mail"},
	},
	LDAPModeGeneric: {
		userFilter:  "(&(objectClass=inetOrgPerson)(uid={username}))",
		groupFilter: "(|(&(objectClass=groupOfNames)(member={dn}))(&(objectClass=groupOfUniqueNames)(uniqueMember={dn}))(&(objectClass=posixGroup)(memberUid={username})))",
		attrs:       LDAPAttributes{Username: "uid", DisplayName: "cn", Email: "mail"},
	},
}

func defaultLDAP() LDAP {
	return LDAP{Mode: LDAPModeAD, Timeout: Duration(10 * time.Second)}
}

// ApplyDefaults fills mode-dependent defaults (filters, attributes, group base).
func (l *LDAP) ApplyDefaults() {
	d, ok := ldapDefaults[l.Mode]
	if !ok {
		return
	}
	if l.UserFilter == "" {
		l.UserFilter = d.userFilter
	}
	if l.GroupFilter == "" {
		l.GroupFilter = d.groupFilter
	}
	if l.Attributes.Username == "" {
		l.Attributes.Username = d.attrs.Username
	}
	if l.Attributes.DisplayName == "" {
		l.Attributes.DisplayName = d.attrs.DisplayName
	}
	if l.Attributes.Email == "" {
		l.Attributes.Email = d.attrs.Email
	}
	if l.GroupBaseDN == "" {
		l.GroupBaseDN = l.BaseDN
	}
}

func (l *LDAP) validate(add func(string, ...any)) {
	const p = "auth.ldap."
	u, err := url.Parse(l.URL)
	switch {
	case l.URL == "":
		add(p + "url: required")
	case err != nil || u.Host == "" || (u.Scheme != "ldap" && u.Scheme != "ldaps"):
		add(p+"url %q: must be ldaps://host[:port] or ldap://host[:port]", l.URL)
	case u.Scheme == "ldap" && !l.StartTLS && !l.Insecure:
		add(p + "url: ldap:// sends passwords in clear text; use ldaps://, start_tls: true, or insecure: true for testing")
	case u.Scheme == "ldaps" && l.StartTLS:
		add(p + "start_tls: not possible with ldaps://")
	}
	l.TLS.validate(p+"tls.", add)
	if _, ok := ldapDefaults[l.Mode]; !ok {
		add(p+"mode %q: must be %q or %q", l.Mode, LDAPModeAD, LDAPModeGeneric)
	}
	if l.BindDN == "" || !l.BindPassword.IsSet() {
		add(p + "bind_dn and bind_password: required")
	}
	if l.BaseDN == "" {
		add(p + "base_dn: required")
	}
	if !strings.Contains(l.UserFilter, "{username}") {
		add(p + "user_filter: must contain {username}")
	}
	if l.Mode == LDAPModeGeneric && !strings.Contains(l.GroupFilter, "{dn}") && !strings.Contains(l.GroupFilter, "{username}") {
		add(p + "group_filter: must contain {dn} or {username}")
	}
	if l.Timeout <= 0 {
		add(p + "timeout: must be positive")
	}
}
