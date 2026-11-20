// SPDX-License-Identifier: GPL-3.0-or-later

package firewalls

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/joernott/dcdash/internal/firewalls/opnsense"
)

// The interfaces and routes of the test firewall (OPNsense 26.7, 2026-11-20): dcdash
// (10.15.44.48) reaches it through INFRA0 (opt3) via the gateway 10.15.34.1.
var (
	testIfaces = []opnsense.Interface{
		{Key: "wan", Description: "WAN0", Device: "igb0", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.100.1/24")}},
		{Key: "opt1", Description: "test_wan_ipv6_test", Device: "igb1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.15.192.1/24")}},
		{Key: "opt2", Description: "WAN1", Device: "igb2"},
		{Key: "opt3", Description: "INFRA0", Device: "igb3", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.15.34.34/24")}},
	}
	testRoutes = []opnsense.Route{
		{Destination: netip.MustParsePrefix("0.0.0.0/0"), Device: "igb3"},
		{Destination: netip.MustParsePrefix("10.15.0.0/16"), Device: "igb3"},
		{Destination: netip.MustParsePrefix("10.15.34.0/24"), Device: "igb3"},
		{Destination: netip.MustParsePrefix("10.15.192.0/24"), Device: "igb1"},
		{Destination: netip.MustParsePrefix("10.15.34.34/32"), Device: "lo0"},
	}
)

func TestGuard(t *testing.T) {
	local, remote := netip.MustParseAddr("10.15.44.48"), netip.MustParseAddr("10.15.34.34")
	g, err := newGuard(testIfaces, testRoutes, local, remote, nil)
	if err != nil || len(g.protectedKeys()) != 1 || g.protectedKeys()[0] != "opt3" {
		t.Fatalf("%v %v", g.protectedKeys(), err)
	}
	var pe *protectedError
	for _, c := range []struct {
		iface   string
		enabled bool
		ok      bool
	}{
		{"opt1", true, true}, {"opt2", true, true}, {"opt3", true, false}, {"opt3", false, true},
		{"", true, false}, {"opt1,opt3", true, false}, {"opt1,opt2", true, true}, {"servers", true, false},
	} {
		err := g.check(opnsense.Rule{Interface: c.iface, Enabled: c.enabled})
		if (err == nil) != c.ok || (err != nil && !errors.As(err, &pe)) {
			t.Errorf("%q enabled %v: %v", c.iface, c.enabled, err)
		}
	}

	// dcdash connects to the address of another interface than the one the route goes
	// through: both are protected.
	g, _ = newGuard(testIfaces, testRoutes, local, netip.MustParseAddr("10.15.192.1"), nil)
	if k := g.protectedKeys(); len(k) != 2 || k[0] != "opt1" || k[1] != "opt3" {
		t.Errorf("asymmetric: %v", k)
	}
	// No route through an assigned interface: unknown, unless interfaces are configured.
	if _, err := newGuard(testIfaces, nil, local, remote, nil); !errors.Is(err, errGuardUnknown) {
		t.Errorf("no routes: %v", err)
	}
	g, err = newGuard(testIfaces, nil, netip.Addr{}, netip.Addr{}, []string{"opt3"})
	if err != nil || g.check(opnsense.Rule{Interface: "opt3", Enabled: true}) == nil ||
		g.check(opnsense.Rule{Interface: "opt1", Enabled: true}) != nil {
		t.Errorf("configured: %v %v", g.protectedKeys(), err)
	}
}
