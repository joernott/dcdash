// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// openLDAPConfig returns a generic-mode configuration for the OpenLDAP test
// container (test/integration/openldap).
func openLDAPConfig(t *testing.T, url string) config.LDAP {
	t.Helper()
	cfg := config.Default().Auth.LDAP
	cfg.Mode = config.LDAPModeGeneric
	cfg.URL = url
	cfg.BindDN = "cn=dcdash-svc,ou=services,dc=dcdash,dc=test"
	cfg.BindPassword = config.NewSecret("svc-test-only")
	cfg.BaseDN = "ou=people,dc=dcdash,dc=test"
	cfg.GroupBaseDN = "ou=groups,dc=dcdash,dc=test"
	cfg.Timeout = config.Duration(5 * time.Second)
	return cfg
}

func newLDAPClient(t *testing.T, cfg config.LDAP) *ldapauth.Client {
	t.Helper()
	full := config.Default()
	full.Security.EncryptionKey = config.NewSecret("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	full.Server.TLS.Mode = config.TLSModeNone
	full.Auth.LDAP = cfg
	full.Auth.LDAP.ApplyDefaults()
	if err := full.Validate(); err != nil {
		t.Fatalf("invalid test configuration: %v", err)
	}
	c, err := ldapauth.New(cfg, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func groupNames(u *ldapauth.User) []string {
	names := make([]string, 0, len(u.Groups))
	for _, g := range u.Groups {
		names = append(names, g.Name)
	}
	return names
}

func TestOpenLDAPStartTLS(t *testing.T) {
	cfg := openLDAPConfig(t, requireEnv(t, "DCDASH_IT_OPENLDAP_URL"))
	cfg.StartTLS = true
	cfg.TLS.CAFile = requireEnv(t, "DCDASH_IT_OPENLDAP_CERT")
	c := newLDAPClient(t, cfg)
	ctx := context.Background()

	if _, err := c.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}

	alice, err := c.Authenticate(ctx, "alice", "alice-test-only")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	if alice.DisplayName != "Alice Admin" || alice.Email != "alice@dcdash.test" {
		t.Errorf("alice attributes: %+v", alice)
	}
	// groupOfNames, groupOfUniqueNames and posixGroup memberships are found.
	for _, g := range []string{"dashroot", "dcdash-vm", "ops", "cn=dashroot,ou=groups,dc=dcdash,dc=test"} {
		if !alice.MemberOf(g) {
			t.Errorf("alice not in %s (groups %v)", g, groupNames(alice))
		}
	}

	bob, err := c.Authenticate(ctx, "bob", "bob-test-only")
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if bob.MemberOf("dashroot") || !bob.MemberOf("dcdash-vm") {
		t.Errorf("bob groups: %v", groupNames(bob))
	}

	for _, tc := range []struct{ user, pass string }{
		{"alice", "wrong"},
		{"alice", ""},
		{"nobody", "x"},
		{"*", "alice-test-only"},
		{"alice)(uid=*", "alice-test-only"},
	} {
		if _, err := c.Authenticate(ctx, tc.user, tc.pass); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
			t.Errorf("%q/%q: %v", tc.user, tc.pass, err)
		}
	}
}

func TestOpenLDAPLDAPSWithFingerprint(t *testing.T) {
	certFile := requireEnv(t, "DCDASH_IT_OPENLDAP_CERT")
	cfg := openLDAPConfig(t, requireEnv(t, "DCDASH_IT_OPENLDAPS_URL"))
	cfg.TLS.Fingerprint = fingerprintOf(t, certFile)
	if _, err := newLDAPClient(t, cfg).Authenticate(context.Background(), "alice", "alice-test-only"); err != nil {
		t.Errorf("LDAPS with pinned fingerprint: %v", err)
	}

	// Without CA or pin the self-signed certificate is rejected.
	cfg.TLS.Fingerprint = ""
	if _, err := newLDAPClient(t, cfg).Check(context.Background()); !errors.Is(err, ldapauth.ErrUnavailable) {
		t.Errorf("untrusted certificate accepted: %v", err)
	}
}

func TestOpenLDAPServiceAccountErrors(t *testing.T) {
	cfg := openLDAPConfig(t, requireEnv(t, "DCDASH_IT_OPENLDAP_URL"))
	cfg.StartTLS = true
	cfg.TLS.CAFile = requireEnv(t, "DCDASH_IT_OPENLDAP_CERT")
	cfg.BindPassword = config.NewSecret("wrong")
	c := newLDAPClient(t, cfg)
	// A broken service account is an outage, not a credential problem of the user.
	if _, err := c.Authenticate(context.Background(), "alice", "alice-test-only"); !errors.Is(err, ldapauth.ErrUnavailable) {
		t.Errorf("wrong service password: %v", err)
	}
}

func fingerprintOf(t *testing.T, certFile string) string {
	t.Helper()
	data, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM certificate in " + certFile)
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		t.Fatal(err)
	}
	return tlsutil.Fingerprint(block.Bytes)
}
