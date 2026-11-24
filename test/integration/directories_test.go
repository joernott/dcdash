// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories/ad"
)

// adTarget is a domain the directory tests may write to: the Samba AD DC of the test
// environment, or a lab domain controller given by DCDASH_IT_AD_ADMIN* (objects there get
// the prefix dcdashdev and live in OU=dcdashdev-test).
type adTarget struct {
	cfg    config.Directory
	ou     string
	prefix string
}

func sambaTarget(t *testing.T) adTarget {
	t.Helper()
	domain := requireEnv(t, "DCDASH_IT_SAMBA_DOMAIN")
	cfg := config.Directory{
		Name:         "it",
		Type:         config.DirectoryTypeSamba,
		Domain:       domain,
		Servers:      []config.LDAPServer{{URL: requireEnv(t, "DCDASH_IT_SAMBA_URL"), Fingerprint: requireEnv(t, "DCDASH_IT_SAMBA_FINGERPRINT")}},
		BindDN:       requireEnv(t, "DCDASH_IT_SAMBA_BIND_DN"),
		BindPassword: config.NewSecret(requireEnv(t, "DCDASH_IT_SAMBA_BIND_PASSWORD")),
		BaseDN:       config.DomainBaseDN(domain),
		Timeout:      config.Duration(10 * time.Second),
	}
	ou := requireEnv(t, "DCDASH_IT_SAMBA_OU")
	cfg.ManagedOUs = []string{ou}
	return adTarget{cfg: cfg, ou: ou, prefix: "it-"}
}

func labTarget(t *testing.T) adTarget {
	t.Helper()
	base := requireEnv(t, "DCDASH_IT_AD_BASE_DN")
	ou := "OU=dcdashdev-test," + base
	cfg := config.Directory{
		Name:         "lab",
		Type:         config.DirectoryTypeSamba,
		Domain:       strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(base, "DC=", ""), ",", ".")),
		Servers:      []config.LDAPServer{{URL: requireEnv(t, "DCDASH_IT_AD_URL"), Fingerprint: requireEnv(t, "DCDASH_IT_AD_FINGERPRINT")}},
		BindDN:       requireEnv(t, "DCDASH_IT_AD_ADMIN"),
		BindPassword: config.NewSecret(requireEnv(t, "DCDASH_IT_AD_ADMIN_PASSWORD")),
		BaseDN:       base,
		ManagedOUs:   []string{ou},
		Timeout:      config.Duration(10 * time.Second),
	}
	return adTarget{cfg: cfg, ou: ou, prefix: "dcdashdev-"}
}

func (a adTarget) client(t *testing.T) *ad.Client {
	t.Helper()
	c, err := ad.New(a.cfg, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testPassword returns a random password that meets the default complexity rules.
func testPassword(t *testing.T) string {
	t.Helper()
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 14)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			t.Fatal(err)
		}
		b[i] = chars[n.Int64()]
	}
	return string(b) + "-7a"
}

func TestDirectorySamba(t *testing.T) { testDirectory(t, sambaTarget(t)) }

func TestDirectoryLab(t *testing.T) { testDirectory(t, labTarget(t)) }

func testDirectory(t *testing.T, target adTarget) {
	ctx := context.Background()
	c := target.client(t)
	if _, err := c.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	user, group, nested := target.prefix+"u1", target.prefix+"g1", target.prefix+"g2"
	cleanup := func() {
		_ = c.DeleteUser(ctx, user)
		_ = c.DeleteGroup(ctx, group)
		_ = c.DeleteGroup(ctx, target.prefix+"g1b")
		_ = c.DeleteGroup(ctx, nested)
	}
	cleanup()
	t.Cleanup(cleanup)

	p, err := c.Policy(ctx)
	if err != nil || p.MinLength == 0 {
		t.Fatalf("Policy = %+v, %v", p, err)
	}
	cs, err := c.Containers(ctx)
	if err != nil || !slices.ContainsFunc(cs, func(x ad.Container) bool { return strings.EqualFold(x.DN, target.ou) }) {
		t.Fatalf("Containers = %v, %v", cs, err)
	}

	// The built-in Administrator is protected and not managed.
	admins, _, err := c.Users(ctx, "Administrator", 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(admins, func(u ad.User) bool { return strings.EqualFold(u.Name, "Administrator") }); i < 0 || !admins[i].Protected || admins[i].Managed {
		t.Errorf("Administrator: %+v", admins)
	}
	if da, err := c.Group(ctx, "Domain Admins"); err != nil || !da.Protected || len(da.Members) == 0 {
		t.Errorf("Domain Admins = %+v, %v", da, err)
	}

	// Users.
	pw := testPassword(t)
	u, err := c.CreateUser(ctx, ad.NewUser{OU: target.ou, Name: user, GivenName: "Dev", Surname: "Probe",
		DisplayName: "dcdash test user", Password: pw})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if !u.Managed || u.Protected || u.Disabled || u.MustChangePassword || !strings.HasPrefix(u.UPN, user+"@") {
		t.Errorf("created user: %+v", u)
	}
	if _, err := c.CreateUser(ctx, ad.NewUser{OU: target.ou, Name: user, Password: pw}); !errors.Is(err, ad.ErrExists) {
		t.Errorf("duplicate user: %v", err)
	}
	if _, err := c.CreateUser(ctx, ad.NewUser{OU: target.cfg.BaseDN, Name: target.prefix + "x", Password: pw}); !errors.Is(err, ad.ErrForbidden) {
		t.Errorf("user outside the managed OUs: %v", err)
	}
	if _, err := c.CreateUser(ctx, ad.NewUser{OU: target.ou, Name: target.prefix + "short", Password: "a"}); !errors.Is(err, ad.ErrPolicy) {
		t.Errorf("short password: %v", err)
	}
	mail := user + "@example.com"
	if err := c.UpdateUser(ctx, user, ad.UserChange{Mail: &mail, Description: ptr("changed")}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if u, err = c.User(ctx, user); err != nil || u.Mail != mail || u.Description != "changed" {
		t.Errorf("after update: %+v, %v", u, err)
	}
	if list, _, err := c.Users(ctx, target.prefix+"u", 0); err != nil || len(list) != 1 || list[0].Name != user {
		t.Errorf("Users(%q) = %+v, %v", target.prefix+"u", list, err)
	}

	// Self-service change, admin reset with "must change", change of a "must change" user.
	pw2 := testPassword(t)
	if err := c.ChangePassword(ctx, user, pw, pw2); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if err := c.ChangePassword(ctx, user, "wrong-"+pw, testPassword(t)); !errors.Is(err, ad.ErrWrongPassword) {
		t.Errorf("wrong old password: %v", err)
	}
	pw3 := testPassword(t)
	if err := c.ResetPassword(ctx, user, pw3, true); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if u, _ = c.User(ctx, user); !u.MustChangePassword {
		t.Errorf("must change not set: %+v", u)
	}
	pw4 := testPassword(t)
	if err := c.ChangePassword(ctx, u.UPN, pw3, pw4); err != nil {
		t.Fatalf("ChangePassword of a must-change user by UPN: %v", err)
	}
	if u, _ = c.User(ctx, user); u.MustChangePassword {
		t.Errorf("must change still set after the change: %+v", u)
	}
	if err := c.ChangePassword(ctx, user, pw4, "a"); !errors.Is(err, ad.ErrPolicy) {
		t.Errorf("too short: %v", err)
	}
	if err := c.ChangePassword(ctx, target.prefix+"nobody", pw4, pw); !errors.Is(err, ad.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}

	// Disable, enable, unlock.
	if err := c.SetUserEnabled(ctx, user, false); err != nil {
		t.Fatal(err)
	}
	if u, _ = c.User(ctx, user); !u.Disabled {
		t.Errorf("not disabled: %+v", u)
	}
	if err := c.SetUserEnabled(ctx, user, true); err != nil {
		t.Fatal(err)
	}
	if p.LockoutThreshold > 0 {
		for range p.LockoutThreshold {
			_ = c.ChangePassword(ctx, user, "wrong-"+pw4, testPassword(t))
		}
		if u, _ = c.User(ctx, user); !u.LockedOut {
			t.Errorf("not locked out after %d wrong passwords: %+v", p.LockoutThreshold, u)
		}
	}
	if err := c.UnlockUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if u, _ = c.User(ctx, user); u.LockedOut || u.Disabled {
		t.Errorf("after unlock: %+v", u)
	}

	// Groups.
	g, err := c.CreateGroup(ctx, ad.NewGroup{OU: target.ou, Name: group, Description: "dcdash test", Scope: ad.ScopeGlobal, Security: true})
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if g.Scope != ad.ScopeGlobal || !g.Security || !g.Managed || g.Protected {
		t.Errorf("created group: %+v", g)
	}
	if _, err := c.CreateGroup(ctx, ad.NewGroup{OU: target.ou, Name: nested, Scope: ad.ScopeGlobal, Security: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMembers(ctx, group, []string{user, nested, user}, nil); err != nil {
		t.Fatalf("add members: %v", err)
	}
	if err := c.SetMembers(ctx, group, []string{user}, []string{target.prefix + "nobody"}); err != nil {
		t.Errorf("idempotent add, remove of an unknown member: %v", err)
	}
	g, err = c.Group(ctx, group)
	if err != nil || len(g.Members) != 2 || g.Members[0].Kind == g.Members[1].Kind {
		t.Errorf("members: %+v, %v", g.Members, err)
	}
	if u, _ = c.User(ctx, user); !slices.ContainsFunc(u.Groups, func(dn string) bool { return strings.EqualFold(dn, g.DN) }) {
		t.Errorf("user groups: %v", u.Groups)
	}
	if err := c.UpdateGroup(ctx, group, ad.GroupChange{Scope: ptr(ad.ScopeUniversal), Description: ptr("")}); err != nil {
		t.Fatalf("UpdateGroup: %v", err)
	}
	if err := c.UpdateGroup(ctx, group, ad.GroupChange{Name: ptr(target.prefix + "g1b")}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	g, err = c.Group(ctx, target.prefix+"g1b")
	if err != nil || g.Scope != ad.ScopeUniversal || g.Description != "" || !strings.HasPrefix(g.DN, "CN="+target.prefix+"g1b,") || len(g.Members) != 2 {
		t.Errorf("after update and rename: %+v, %v", g, err)
	}
	if err := c.SetMembers(ctx, target.prefix+"g1b", nil, []string{user}); err != nil {
		t.Fatal(err)
	}
	if list, _, err := c.Groups(ctx, target.prefix+"g", 0); err != nil || len(list) != 2 {
		t.Errorf("Groups = %+v, %v", list, err)
	}
	for _, n := range []string{target.prefix + "g1b", nested} {
		if err := c.DeleteGroup(ctx, n); err != nil {
			t.Errorf("DeleteGroup(%s): %v", n, err)
		}
	}
	if err := c.DeleteUser(ctx, user); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := c.User(ctx, user); !errors.Is(err, ad.ErrNotFound) {
		t.Errorf("deleted user: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
