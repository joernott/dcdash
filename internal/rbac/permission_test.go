// SPDX-License-Identifier: GPL-3.0-or-later

package rbac

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Permission{
		"dns:cf-prod:example.com:write":    {"dns", "cf-prod", "example.com", ActionWrite},
		"proxmox:*:*:operate":              {"proxmox", "*", "*", ActionOperate},
		"*:*:*:*":                          {"*", "*", "*", ActionAdmin},
		"dhcp:kea:2001:db8::/64:read":      {"dhcp", "kea", "2001:db8::/64", ActionRead},
		" system:core:audit:read ":         {"system", "core", "audit", ActionRead},
		"proxmox:cluster-a:vm/100:console": {"proxmox", "cluster-a", "vm/100", ActionConsole},
		"dns:bind:*.example.com:admin":     {"dns", "bind", "*.example.com", ActionAdmin},
	} {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %+v, want %+v", in, got, want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "dns", "dns:cf:write", "dns::x:read", ":a:b:read", "dns:a::read", "dns:a:b:delete", "dns:a:b:"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	if _, err := ParseSet([]string{"dns:a:b:read", "bad", "x:y:z:nope"}); err == nil ||
		!strings.Contains(err.Error(), `"bad"`) || !strings.Contains(err.Error(), "nope") {
		t.Errorf("ParseSet must report all errors: %v", err)
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, s := range []string{"dns:cf:example.com:write", "dhcp:kea:2001:db8::/64:read", "*:*:*:admin"} {
		if got := MustParse(s).String(); got != s {
			t.Errorf("%s -> %s", s, got)
		}
	}
	if MustParse("a:b:c:*").String() != "a:b:c:admin" {
		t.Error("* action is admin")
	}
}

func TestActionHierarchy(t *testing.T) {
	order := []Action{ActionRead, ActionOperate, ActionConsole, ActionWrite, ActionAdmin}
	for gi, granted := range order {
		g := P("proxmox", "c", "vm/1", granted)
		for ri, requested := range order {
			if got := g.Grants(P("proxmox", "c", "vm/1", requested)); got != (gi >= ri) {
				t.Errorf("%s grants %s = %v", granted, requested, got)
			}
		}
	}
}

func TestGrantsWildcards(t *testing.T) {
	cases := []struct {
		grant, req string
		want       bool
	}{
		{"dns:*:*:write", "dns:cf:example.com:read", true},
		{"dns:*:*:read", "dns:cf:example.com:write", false},
		{"dns:cf:*:write", "dns:bind:example.com:write", false},
		{"dns:cf:*:write", "dhcp:cf:example.com:write", false},
		{"*:*:*:admin", "proxmox:a:vm/1:console", true},
		{"dns:*:*.example.com:write", "dns:cf:www.example.com:write", true},
		{"dns:*:*.example.com:write", "dns:cf:example.com:write", false},
		{"dns:*:*.example.com:write", "dns:cf:evil-example.com:write", false},
		{"proxmox:prod-*:*:operate", "proxmox:prod-a:vm/1:operate", true},
		{"proxmox:prod-*:*:operate", "proxmox:test-a:vm/1:operate", false},
		{"proxmox:*:vm/1*:read", "proxmox:c:vm/100:read", true},
		{"proxmox:*:vm/*0:read", "proxmox:c:vm/100:read", true},
		{"proxmox:*:vm/*0:read", "proxmox:c:vm/101:read", false},
		{"x:*:a*b*c:read", "x:i:abc:read", true},
		{"x:*:a*b*c:read", "x:i:a-b-c:read", true},
		{"x:*:a*b*c:read", "x:i:ac:read", false},
		{"x:*:ab*ba:read", "x:i:aba:read", false},
		{"dns:CF:*:read", "dns:cf:x:read", false},
		{"dhcp:kea:2001:db8::/64:write", "dhcp:kea:2001:db8::/64:operate", true},
	}
	for _, c := range cases {
		if got := MustParse(c.grant).Grants(MustParse(c.req)); got != c.want {
			t.Errorf("%s grants %s = %v, want %v", c.grant, c.req, got, c.want)
		}
	}
}

func TestSet(t *testing.T) {
	s, err := ParseSet([]string{"dns:cf:*:write", "proxmox:*:*:read"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Allows(MustParse("proxmox:a:vm/1:read")) || s.Allows(MustParse("proxmox:a:vm/1:operate")) {
		t.Error("Allows")
	}
	if !s.AllowsAny("dns", "cf", ActionWrite) || s.AllowsAny("dns", "bind", ActionRead) || s.AllowsAny("dhcp", "x", ActionRead) {
		t.Error("AllowsAny")
	}
	var empty Set
	if empty.Allows(MustParse("a:b:c:read")) {
		t.Error("empty set allows")
	}
	if got := s.Strings(); len(got) != 2 || got[0] != "dns:cf:*:write" {
		t.Errorf("Strings: %v", got)
	}
}
