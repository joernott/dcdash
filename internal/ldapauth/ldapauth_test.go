// SPDX-License-Identifier: GPL-3.0-or-later

package ldapauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
)

func testClient(t *testing.T, mode string) *Client {
	t.Helper()
	cfg := config.Default().Auth.LDAP
	cfg.URL = "ldaps://dc.example.com"
	cfg.Mode = mode
	cfg.BaseDN = "DC=example,DC=com"
	cfg.BindDN = "svc@example.com"
	cfg.BindPassword = config.NewSecret("pw")
	c, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUserFilterEscapesInput(t *testing.T) {
	c := testClient(t, config.LDAPModeAD)
	f := c.userFilter("a*)(cn=*")
	if strings.Contains(f, "a*)(cn=*") || !strings.Contains(f, `a\2a\29\28cn=\2a`) {
		t.Errorf("filter not escaped: %s", f)
	}
}

func TestDefaultsPerMode(t *testing.T) {
	ad := testClient(t, config.LDAPModeAD)
	if !strings.Contains(ad.cfg.UserFilter, "sAMAccountName={username}") || ad.cfg.Attributes.Username != "sAMAccountName" {
		t.Errorf("AD defaults: %+v", ad.cfg)
	}
	if ad.cfg.GroupBaseDN != ad.cfg.BaseDN {
		t.Error("group_base_dn should default to base_dn")
	}
	gen := testClient(t, config.LDAPModeGeneric)
	if !strings.Contains(gen.cfg.UserFilter, "uid={username}") || !strings.Contains(gen.cfg.GroupFilter, "{dn}") {
		t.Errorf("generic defaults: %+v", gen.cfg)
	}
	if contains(gen.userAttributes(), "userAccountControl") || !contains(ad.userAttributes(), "userAccountControl") {
		t.Error("userAccountControl must only be requested in AD mode")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestAuthenticateRejectsWithoutContactingDirectory(t *testing.T) {
	c := testClient(t, config.LDAPModeAD)
	c.dial = func() (*ldap.Conn, error) {
		t.Fatal("directory must not be contacted")
		return nil, nil
	}
	for _, tc := range []struct{ user, pass string }{
		{"alice", ""},                        // empty password = unauthenticated bind
		{"", "secret"},                       // no user
		{"   ", "secret"},                    // blank user
		{strings.Repeat("a", 300), "secret"}, // absurd length
	} {
		if _, err := c.Authenticate(context.Background(), tc.user, tc.pass); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%q/%q: %v", tc.user, tc.pass, err)
		}
	}
}

func TestUnreachableDirectory(t *testing.T) {
	cfg := testClient(t, config.LDAPModeAD).cfg
	cfg.URL = "ldaps://127.0.0.1:1"
	cfg.Timeout = config.Duration(500_000_000)
	c, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Authenticate(context.Background(), "alice", "secret"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Authenticate: %v", err)
	}
	if _, err := c.Check(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Check: %v", err)
	}
}

func TestAccountDisabled(t *testing.T) {
	e := ldap.NewEntry("CN=x", map[string][]string{"userAccountControl": {"514"}})
	if !accountDisabled(e) {
		t.Error("514 (normal account + disabled) not detected")
	}
	e = ldap.NewEntry("CN=x", map[string][]string{"userAccountControl": {"512"}})
	if accountDisabled(e) {
		t.Error("512 detected as disabled")
	}
}
