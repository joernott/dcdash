// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/switches/aoscx"
)

// TestArubaCX reads from a real Aruba CX switch (DCDASH_IT_ARUBA_URL, _FINGERPRINT,
// _USER, _PASSWORD). It changes nothing.
func TestArubaCX(t *testing.T) {
	rawURL := os.Getenv("DCDASH_IT_ARUBA_URL")
	if rawURL == "" {
		t.Skip("DCDASH_IT_ARUBA_URL not set")
	}
	ctx := context.Background()
	c, err := aoscx.New(config.Switch{Name: "it", Type: config.SwitchTypeAOSCX, URL: rawURL,
		TLS:      config.TLSClient{Fingerprint: os.Getenv("DCDASH_IT_ARUBA_FINGERPRINT")},
		Username: requireEnv(t, "DCDASH_IT_ARUBA_USER"), Password: config.NewSecret(requireEnv(t, "DCDASH_IT_ARUBA_PASSWORD"))},
		zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(ctx); err != nil {
			t.Errorf("logout: %v", err)
		}
	})
	// Parallel requests share one session.
	var wg sync.WaitGroup
	invs := make([]aoscx.Inventory, 4)
	errs := make([]error, 4)
	for i := range invs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			invs[i], errs[i] = c.Inventory(ctx)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	inv := invs[0]
	t.Logf("%s: platform %s, firmware %s, API %s, booted %s, members %+v", inv.Hostname, inv.Platform, inv.Firmware,
		c.APIVersion(), inv.Booted, inv.Members)
	if inv.Hostname == "" || inv.Firmware == "" || !strings.HasPrefix(c.APIVersion(), "v10.") || len(inv.Members) == 0 ||
		inv.Members[0].Serial == "" || inv.Booted.IsZero() {
		t.Errorf("%+v", inv)
	}

	ports, err := c.Ports(ctx)
	if err != nil || len(ports) == 0 {
		t.Fatalf("ports: %v", err)
	}
	for _, p := range ports {
		if p.LinkUp || len(p.Neighbors) > 0 {
			t.Logf("port %s: enabled %v, link %v, %d Mbit/s, %s VLAN %d, trunk %v (all %v), uplink %v, neighbors %+v",
				p.Name, p.Enabled, p.LinkUp, p.SpeedMbps, p.Mode, p.VLAN, p.TrunkVLANs, p.AllVLANs, p.Uplink(), p.Neighbors)
		}
	}
	t.Logf("%d ports, first %s, last %s", len(ports), ports[0].Name, ports[len(ports)-1].Name)
	vlans, err := c.VLANs(ctx)
	if err != nil || len(vlans) == 0 || vlans[0].ID != 1 {
		t.Fatalf("VLANs: %+v %v", vlans, err)
	}
	macs, err := c.MACs(ctx)
	if err != nil || len(macs) == 0 {
		t.Fatalf("MACs: %v", err)
	}
	t.Logf("%d VLANs, %d MAC addresses, first %+v", len(vlans), len(macs), macs[0])

	if port := os.Getenv("DCDASH_IT_ARUBA_WRITE_PORT"); port != "" {
		writeTest(ctx, t, c, port, ports)
	}
}

// writeTest changes an unused port and a test VLAN and restores them
// (DCDASH_IT_ARUBA_WRITE_PORT, DCDASH_IT_ARUBA_WRITE_VLAN, default 4000). The test switch allows
// all ports except the uplink 1/1/1 and VLANs from 4000.
func writeTest(ctx context.Context, t *testing.T, c *aoscx.Client, port string, ports []aoscx.Port) {
	vlan := 4000
	if v := os.Getenv("DCDASH_IT_ARUBA_WRITE_VLAN"); v != "" {
		vlan, _ = strconv.Atoi(v)
	}
	if port == "1/1/1" || vlan < 4000 {
		t.Fatalf("refusing to change port %s or VLAN %d", port, vlan)
	}
	var before aoscx.Port
	for _, p := range ports {
		if p.Name == port {
			before = p
		}
	}
	if before.Name == "" || before.LinkUp || before.Uplink() {
		t.Fatalf("port %s must exist, without link, and not be an uplink: %+v", port, before)
	}
	if err := c.CreateVLAN(ctx, vlan, "dcdash-it"); err != nil {
		t.Fatalf("create VLAN: %v", err)
	}
	t.Cleanup(func() {
		desc, mode, enabled := before.Description, before.Mode, before.Enabled
		trunks := before.TrunkVLANs
		if err := c.UpdatePort(ctx, port, aoscx.PortChange{Enabled: &enabled, Description: &desc, Mode: &mode,
			VLAN: &before.VLAN, TrunkVLANs: &trunks}); err != nil {
			t.Errorf("restore port: %v", err)
		}
		if err := c.DeleteVLAN(ctx, vlan); err != nil {
			t.Errorf("delete VLAN: %v", err)
		}
		if err := c.SaveConfig(ctx); err != nil {
			t.Errorf("save: %v", err)
		}
		after, err := c.Port(ctx, port)
		if err != nil || after.Description != before.Description || after.VLAN != before.VLAN || after.Mode != before.Mode ||
			after.Enabled != before.Enabled {
			t.Errorf("not restored: %+v %v", after, err)
		}
	})
	if err := c.RenameVLAN(ctx, vlan, "dcdash-it-2"); err != nil {
		t.Errorf("rename VLAN: %v", err)
	}
	desc, access := "dcdash-it", aoscx.ModeAccess
	if err := c.UpdatePort(ctx, port, aoscx.PortChange{Description: &desc, Mode: &access, VLAN: &vlan}); err != nil {
		t.Fatalf("access port: %v", err)
	}
	if p, err := c.Port(ctx, port); err != nil || p.Description != desc || p.Mode != aoscx.ModeAccess || p.VLAN != vlan {
		t.Errorf("access port: %+v %v", p, err)
	}
	trunk, trunks, off := aoscx.ModeTrunk, []int{vlan}, false
	if err := c.UpdatePort(ctx, port, aoscx.PortChange{Mode: &trunk, TrunkVLANs: &trunks, Enabled: &off}); err != nil {
		t.Fatalf("trunk port: %v", err)
	}
	if p, err := c.Port(ctx, port); err != nil || p.Mode != aoscx.ModeTrunk || len(p.TrunkVLANs) != 1 || p.TrunkVLANs[0] != vlan ||
		p.Enabled {
		t.Errorf("trunk port: %+v %v", p, err)
	}
	vlans, _ := c.VLANs(ctx)
	if !slices.ContainsFunc(vlans, func(v aoscx.VLAN) bool { return v.ID == vlan && v.Name == "dcdash-it-2" }) {
		t.Errorf("VLAN not renamed: %+v", vlans)
	}
	if err := c.SaveConfig(ctx); err != nil {
		t.Errorf("save: %v", err)
	}
}
