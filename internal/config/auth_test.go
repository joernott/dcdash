// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

const ldapAD = `
server:
  tls: {mode: none}
auth:
  ldap:
    url: ldaps://dc1.example.com
    bind_dn: svc@example.com
    bind_password: secret
    base_dn: DC=example,DC=com
    tls:
      fingerprint: "BF:21:52:3E:B1:73:9E:70:02:3E:E6:62:4B:1A:F8:AF:A5:BE:96:93:B4:0E:90:30:5E:3B:DE:B2:06:CC:AB:24"
`

func TestLDAPDefaults(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + ldapAD))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l := cfg.Auth.LDAP
	if l.Mode != LDAPModeAD || l.Timeout.D().Seconds() != 10 {
		t.Errorf("defaults: %+v", l)
	}
	if !strings.Contains(l.UserFilter, "sAMAccountName") || l.Attributes.Username != "sAMAccountName" || l.GroupBaseDN != l.BaseDN {
		t.Errorf("AD defaults not applied: %+v", l)
	}
	if fp := l.TLS.NormalizedFingerprint(); len(fp) != 64 || strings.Contains(fp, ":") {
		t.Errorf("normalized fingerprint %q", fp)
	}
}

func TestLDAPDisabledWithoutURL(t *testing.T) {
	if _, err := Parse(strings.NewReader(testKeyYAML + "server:\n  tls: {mode: none}\n")); err != nil {
		t.Errorf("config without LDAP: %v", err)
	}
}

func TestLDAPValidation(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"plain ldap":       {"url: ldap://dc1\n    bind_dn: a\n    bind_password: b\n    base_dn: c", "clear text"},
		"ldaps + starttls": {"url: ldaps://dc1\n    start_tls: true\n    bind_dn: a\n    bind_password: b\n    base_dn: c", "start_tls"},
		"bad scheme":       {"url: http://dc1\n    bind_dn: a\n    bind_password: b\n    base_dn: c", "must be ldaps://"},
		"missing bind":     {"url: ldaps://dc1\n    base_dn: c", "bind_dn and bind_password"},
		"missing base":     {"url: ldaps://dc1\n    bind_dn: a\n    bind_password: b", "base_dn: required"},
		"bad mode":         {"url: ldaps://dc1\n    mode: novell\n    bind_dn: a\n    bind_password: b\n    base_dn: c", "mode"},
		"bad fingerprint":  {"url: ldaps://dc1\n    bind_dn: a\n    bind_password: b\n    base_dn: c\n    tls: {fingerprint: abc}", "fingerprint"},
		"filter":           {"url: ldaps://dc1\n    bind_dn: a\n    bind_password: b\n    base_dn: c\n    user_filter: (uid=x)", "{username}"},
	}
	for name, tc := range tests {
		_, err := Parse(strings.NewReader(testKeyYAML + "server:\n  tls: {mode: none}\nauth:\n  ldap:\n    " + tc.yaml + "\n"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: expected error containing %q, got %v", name, tc.want, err)
		}
	}

	// ldap:// is accepted with StartTLS or the explicit insecure flag.
	for _, extra := range []string{"start_tls: true", "insecure: true"} {
		_, err := Parse(strings.NewReader(testKeyYAML + "server:\n  tls: {mode: none}\nauth:\n  ldap:\n    url: ldap://dc1\n    " +
			extra + "\n    bind_dn: a\n    bind_password: b\n    base_dn: c\n"))
		if err != nil {
			t.Errorf("%s: %v", extra, err)
		}
	}
}
