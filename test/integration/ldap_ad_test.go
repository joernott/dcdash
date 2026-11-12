// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/ldapauth"
)

// adConfig returns the configuration for the Samba AD given by the
// DCDASH_IT_AD_* environment variables (kept outside the repository).
func adConfig(t *testing.T) config.LDAP {
	t.Helper()
	cfg := config.Default().Auth.LDAP
	cfg.Mode = config.LDAPModeAD
	cfg.URL = requireEnv(t, "DCDASH_IT_AD_URL")
	cfg.BindDN = requireEnv(t, "DCDASH_IT_AD_BIND_DN")
	cfg.BindPassword = config.NewSecret(requireEnv(t, "DCDASH_IT_AD_BIND_PASSWORD"))
	cfg.BaseDN = requireEnv(t, "DCDASH_IT_AD_BASE_DN")
	cfg.TLS.Fingerprint = requireEnv(t, "DCDASH_IT_AD_FINGERPRINT")
	cfg.Timeout = config.Duration(10 * time.Second)
	return cfg
}

func adClient(t *testing.T, cfg config.LDAP) *ldapauth.Client {
	t.Helper()
	c, err := ldapauth.New(cfg, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestADServiceBind(t *testing.T) {
	c := adClient(t, adConfig(t))
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestADAuthenticate(t *testing.T) {
	cfg := adConfig(t)
	c := adClient(t, cfg)

	// The service account is a regular user and can log in with its UPN.
	u, err := c.Authenticate(context.Background(), cfg.BindDN, cfg.BindPassword.Value())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	t.Logf("user %q (%s), display name %q, email %q", u.Username, u.DN, u.DisplayName, u.Email)
	for _, g := range u.Groups {
		t.Logf("  group %q (%s)", g.Name, g.DN)
	}
	if u.DN == "" || u.Username == "" {
		t.Errorf("incomplete user: %+v", u)
	}

	// Unknown users are rejected. Wrong passwords are deliberately not tested
	// against existing accounts, to not trigger the AD lockout policy.
	if _, err := c.Authenticate(context.Background(), "dcdash-no-such-user-4711", "x"); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestADWrongFingerprintIsRejected(t *testing.T) {
	cfg := adConfig(t)
	cfg.TLS.Fingerprint = "00000000000000000000000000000000000000000000000000000000000000AA"
	if _, err := adClient(t, cfg).Check(context.Background()); !errors.Is(err, ldapauth.ErrUnavailable) {
		t.Errorf("wrong fingerprint: %v", err)
	}
}

func TestADNestedGroups(t *testing.T) {
	c := adClient(t, adConfig(t))
	u, err := c.LookupUser(context.Background(), "Administrator")
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	// By AD default, Administrator is a direct member of "Domain Admins", which
	// in turn is a member of "Denied RODC Password Replication Group": that group
	// is only found through LDAP_MATCHING_RULE_IN_CHAIN.
	if !u.MemberOf("Domain Admins") {
		t.Errorf("direct group missing: %v", u.Groups)
	}
	if !u.MemberOf("Denied RODC Password Replication Group") {
		t.Errorf("nested group missing: %v", u.Groups)
	}
	if _, err := c.LookupUser(context.Background(), "dcdash-no-such-user-4711"); !errors.Is(err, ldapauth.ErrInvalidCredentials) {
		t.Errorf("unknown user: %v", err)
	}
}
