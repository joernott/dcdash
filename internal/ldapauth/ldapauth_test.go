// SPDX-License-Identifier: GPL-3.0-or-later

package ldapauth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Status: %v", err)
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

func TestGroupFilter(t *testing.T) {
	ad := testClient(t, config.LDAPModeAD)
	f := ad.groupFilter(`CN=Doe\, John (x),OU=Users,DC=example,DC=com`, "jdoe")
	if !strings.Contains(f, "member:1.2.840.113556.1.4.1941:=") || !strings.HasPrefix(f, "(&(objectClass=group)") {
		t.Errorf("AD filter: %s", f)
	}
	if strings.Contains(f, "(x)") {
		t.Errorf("AD filter not escaped: %s", f)
	}

	gen := testClient(t, config.LDAPModeGeneric)
	f = gen.groupFilter("uid=j*,ou=people,dc=example,dc=com", "j*")
	if !strings.Contains(f, `member=uid=j\2a,ou=people`) || !strings.Contains(f, `memberUid=j\2a`) {
		t.Errorf("generic filter: %s", f)
	}
}

func TestMemberOf(t *testing.T) {
	u := &User{Groups: []Group{
		{DN: "CN=dashroot,OU=Groups,DC=example,DC=com", Name: "dashroot"},
		{DN: "CN=DNS Admins,CN=Users,DC=example,DC=com", Name: "DNS Admins"},
	}}
	for group, want := range map[string]bool{
		"dashroot":     true,
		"DASHROOT":     true,
		" dns admins ": true,
		"dcdash-vm":    false,
		"cn=dashroot,ou=groups,dc=example,dc=com":    true,
		"CN=dashroot, OU=Groups, DC=example, DC=com": true,
		"CN=dashroot,OU=Other,DC=example,DC=com":     false,
		"CN=broken,=":                                false,
	} {
		if got := u.MemberOf(group); got != want {
			t.Errorf("MemberOf(%q) = %v, want %v", group, got, want)
		}
	}
}

func failoverClient(t *testing.T, urls ...string) *Client {
	t.Helper()
	cfg := testClient(t, config.LDAPModeAD).cfg
	cfg.URL = ""
	for _, u := range urls {
		cfg.Servers = append(cfg.Servers, config.LDAPServer{URL: u})
	}
	c, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Unreachable servers are skipped in order, and the server that worked is
// tried first next time.
func TestFailover(t *testing.T) {
	c := failoverClient(t, "ldaps://dc1.example.com", "ldaps://dc2.example.com", "ldaps://dc3.example.com")
	down := map[string]bool{"dc1.example.com": true}
	var tried []string
	c.dialServer = func(s server) (*ldap.Conn, error) {
		tried = append(tried, s.host)
		if down[s.host] {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}

	if _, err := c.connect(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(tried, ",") != "dc1.example.com,dc2.example.com" {
		t.Errorf("first connect tried %v", tried)
	}

	// dc2 is now preferred, even after dc1 is back.
	down = map[string]bool{}
	tried = nil
	if _, err := c.connect(); err != nil || strings.Join(tried, ",") != "dc2.example.com" {
		t.Errorf("second connect tried %v: %v", tried, err)
	}

	// dc2 fails: dc3 is next, then dc1 (wrapping around).
	down = map[string]bool{"dc2.example.com": true, "dc3.example.com": true}
	tried = nil
	if _, err := c.connect(); err != nil || strings.Join(tried, ",") != "dc2.example.com,dc3.example.com,dc1.example.com" {
		t.Errorf("third connect tried %v: %v", tried, err)
	}

	// All down: one error naming every server.
	down = map[string]bool{"dc1.example.com": true, "dc2.example.com": true, "dc3.example.com": true}
	_, err := c.connect()
	if !errors.Is(err, ErrUnavailable) || strings.Count(err.Error(), "connection refused") != 3 {
		t.Errorf("all down: %v", err)
	}
}

func TestStatusNamesUnreachableServers(t *testing.T) {
	c := failoverClient(t, "ldaps://dc1.example.com", "ldaps://dc2.example.com:636")
	var mu sync.Mutex
	down := map[string]bool{}
	c.probe = func(s server) error {
		mu.Lock()
		defer mu.Unlock()
		if down[s.host] {
			return errors.New("connection refused")
		}
		return nil
	}
	setDown := func(host string) {
		mu.Lock()
		defer mu.Unlock()
		down[host] = true
	}

	d, err := c.Status(context.Background())
	if err != nil || d.Code != "directory.connected" || d.Params["server"] != "dc1.example.com" || d.Params["count"] != "2" {
		t.Errorf("all up: %+v, %v", d, err)
	}

	setDown("dc2.example.com:636")
	d, err = c.Status(context.Background())
	if err != nil || d.Code != "directory.degraded" || d.Params["down"] != "dc2.example.com:636" ||
		!strings.Contains(d.Text, "connection refused") {
		t.Errorf("one down: %+v, %v", d, err)
	}

	setDown("dc1.example.com")
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("all down: %v", err)
	}
}

// A server that doesn't answer before the status check times out counts as
// unavailable instead of blocking the check.
func TestStatusTimeout(t *testing.T) {
	c := failoverClient(t, "ldaps://dc1.example.com", "ldaps://dc2.example.com")
	block := make(chan struct{})
	defer close(block)
	c.probe = func(s server) error {
		if s.host == "dc2.example.com" {
			<-block
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	d, err := c.Status(ctx)
	if err != nil || d.Code != "directory.degraded" || d.Params["down"] != "dc2.example.com" {
		t.Errorf("%+v, %v", d, err)
	}
}

// A server fingerprint overrides tls.fingerprint for that server only.
func TestServerFingerprint(t *testing.T) {
	cfg := testClient(t, config.LDAPModeAD).cfg
	cfg.URL = ""
	cfg.Servers = []config.LDAPServer{
		{URL: "ldaps://dc1.example.com", Fingerprint: strings.Repeat("ab", 32)},
		{URL: "ldaps://dc2.example.com"},
	}
	c, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if !c.servers[0].tls.InsecureSkipVerify || c.servers[0].tls.VerifyConnection == nil {
		t.Error("dc1: fingerprint not pinned")
	}
	if c.servers[1].tls.InsecureSkipVerify || c.servers[1].tls.ServerName != "dc2.example.com" {
		t.Errorf("dc2: %+v", c.servers[1].tls)
	}
}
