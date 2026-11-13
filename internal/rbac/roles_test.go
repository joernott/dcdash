// SPDX-License-Identifier: GPL-3.0-or-later

package rbac

import (
	"slices"
	"testing"
)

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	dnsPerms, err := ParseSet([]string{"dns:cf:*:write"})
	if err != nil {
		t.Fatal(err)
	}
	vmPerms, err := ParseSet([]string{"proxmox:*:*:operate"})
	if err != nil {
		t.Fatal(err)
	}
	return NewResolver("", []Role{
		{Name: "dns-operator", Groups: []string{"dcdash-dns"}, RequireTOTP: true, Permissions: dnsPerms},
		{Name: "vm-operator", Groups: []string{"CN=dcdash-vm, OU=Groups, DC=example, DC=com"}, Permissions: vmPerms},
		{Name: RootRole, Groups: []string{"everyone"}, Permissions: Set{}}, // ignored
	})
}

const (
	dnsGroup  = "CN=dcdash-dns,OU=Groups,DC=example,DC=com"
	vmGroup   = "cn=DCDASH-VM,ou=groups,dc=example,dc=com"
	rootGroup = "CN=DashRoot,OU=Groups,DC=example,DC=com"
)

func TestResolveRoles(t *testing.T) {
	r := testResolver(t)
	cases := []struct {
		name     string
		groups   []string
		local    bool
		roles    []string
		needTOTP bool
	}{
		{"none", []string{"CN=other,DC=example,DC=com"}, false, []string{}, false},
		{"dns by CN", []string{dnsGroup}, false, []string{"dns-operator"}, true},
		{"vm by DN, case and spacing differ", []string{vmGroup}, false, []string{"vm-operator"}, false},
		{"both: strictest TOTP wins", []string{vmGroup, dnsGroup}, false, []string{"dns-operator", "vm-operator"}, true},
		{"root by default group", []string{rootGroup}, false, []string{RootRole}, true},
		{"break-glass is root", nil, true, []string{RootRole}, true},
		{"invalid DNs are ignored", []string{"not a dn", ""}, false, []string{}, false},
	}
	for _, c := range cases {
		s := r.Resolve("u", c.groups, c.local)
		if !slices.Equal(s.Roles, c.roles) || s.RequireTOTP != c.needTOTP {
			t.Errorf("%s: roles %v totp %v, want %v %v", c.name, s.Roles, s.RequireTOTP, c.roles, c.needTOTP)
		}
	}
}

func TestRootCannotBeReplaced(t *testing.T) {
	r := testResolver(t)
	if s := r.Resolve("u", []string{"CN=everyone,DC=example,DC=com"}, false); s.IsRoot() {
		t.Error("configured role named root replaced the built-in role")
	}
	roles := r.Roles()
	if roles[0].Name != RootRole || !roles[0].RequireTOTP || len(roles) != 3 {
		t.Errorf("roles: %+v", roles)
	}
}

func TestCustomRootGroup(t *testing.T) {
	r := NewResolver("CN=Domain Admins,CN=Users,DC=example,DC=com", nil)
	if !r.Resolve("u", []string{"CN=Domain Admins,CN=Users,DC=example,DC=com"}, false).IsRoot() {
		t.Error("custom root group by DN")
	}
	if r.Resolve("u", []string{rootGroup}, false).IsRoot() {
		t.Error("default root group still grants root")
	}
}

func TestSubjectCan(t *testing.T) {
	r := testResolver(t)
	root := r.Resolve("admin", []string{rootGroup}, false)
	if !root.Can(MustParse("dhcp:kea:10.0.0.0/24:admin")) {
		t.Error("root lacks a permission")
	}
	dns := r.Resolve("bob", []string{dnsGroup}, false)
	if !dns.Can(MustParse("dns:cf:example.com:write")) || dns.Can(MustParse("dns:bind:example.com:read")) || dns.IsRoot() {
		t.Error("dns operator permissions")
	}
	var none *Subject
	if none.Can(MustParse("a:b:c:read")) || none.IsRoot() {
		t.Error("nil subject")
	}
}
