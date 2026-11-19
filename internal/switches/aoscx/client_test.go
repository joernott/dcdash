// SPDX-License-Identifier: GPL-3.0-or-later

package aoscx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/switches/aoscx"
	"github.com/joernott/dcdash/internal/switches/aoscx/cxfake"
	"github.com/joernott/dcdash/internal/tlsutil"
)

func newClient(t *testing.T, fake *cxfake.Server, change func(*config.Switch)) *aoscx.Client {
	t.Helper()
	hs := httptest.NewServer(fake)
	t.Cleanup(hs.Close)
	cfg := config.Switch{Name: "sw1", Type: config.SwitchTypeAOSCX, URL: hs.URL, Insecure: true,
		Username: "dcdash", Password: config.NewSecret("pw")}
	if change != nil {
		change(&cfg)
	}
	c, err := aoscx.New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestInventoryReusesTheSession(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	fake.Subsystems["chassis,2"] = cxfake.Product{ProductName: "6000 48G", SerialNumber: "CN2"}
	c := newClient(t, fake, nil)
	inv, err := c.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Hostname != "sw-test" || inv.Platform != "6000" || inv.Firmware != "PL.10.08.1010" ||
		!inv.Booted.Equal(time.Unix(1795091390, 0)) || len(inv.Members) != 2 || inv.Members[0].Name != "1" ||
		inv.Members[0].Serial != "CN00000001" || inv.Members[1].Product != "6000 48G" {
		t.Errorf("%+v", inv)
	}
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Logins() != 1 || c.APIVersion() != "v10.08" {
		t.Errorf("logins %d, version %q", fake.Logins(), c.APIVersion())
	}
	if err := c.Close(ctx); err != nil || fake.Sessions() != 0 {
		t.Errorf("close: %v, %d sessions", err, fake.Sessions())
	}
	if err := c.Close(ctx); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestExpiredSessionLogsInAgain(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	c := newClient(t, fake, nil)
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	fake.ExpireSessions()
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if fake.Logins() != 2 || fake.Sessions() != 1 {
		t.Errorf("logins %d, sessions %d", fake.Logins(), fake.Sessions())
	}
}

func TestConfiguredVersionAndCSRF(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	fake.CSRF = true
	c := newClient(t, fake, func(s *config.Switch) { s.APIVersion = cxfake.Version })
	// A changing request with the token gets past the CSRF check (404: the fake
	// has no such object).
	if err := c.Do(ctx, http.MethodPut, "/system/vlans/4000", map[string]any{"name": "x"}, nil); !errors.Is(err, aoscx.ErrNotFound) {
		t.Errorf("put: %v", err)
	}
	if slices.Contains(fake.Requests(), "GET /rest") {
		t.Errorf("version discovered although configured: %v", fake.Requests())
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	if _, err := newClient(t, fake, func(s *config.Switch) { s.Password = config.NewSecret("wrong") }).Inventory(ctx); !errors.Is(err, aoscx.ErrUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}

	fake.MaxSessions = 1
	c := newClient(t, fake, nil)
	if _, err := c.Inventory(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := newClient(t, fake, nil).Inventory(ctx); !errors.Is(err, aoscx.ErrSessionLimit) {
		t.Errorf("session limit: %v", err)
	}

	fake.FailNext(http.MethodGet, "/system", http.StatusInternalServerError)
	var e *aoscx.Error
	if _, err := c.Inventory(ctx); !errors.As(err, &e) || e.Status != 500 || e.Text != "injected failure" {
		t.Errorf("server error: %v", err)
	}
	// 10.08 has no PATCH and closes the connection.
	if err := c.Do(ctx, http.MethodPatch, "/system/vlans/4000", map[string]any{}, nil); !errors.Is(err, aoscx.ErrUnavailable) {
		t.Errorf("patch: %v", err)
	}
	unreachable, err := aoscx.New(config.Switch{Name: "x", URL: "http://127.0.0.1:1", Username: "u", Password: config.NewSecret("p")}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unreachable.Inventory(ctx); !errors.Is(err, aoscx.ErrUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestPinnedCertificate(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	hs := httptest.NewTLSServer(fake)
	t.Cleanup(hs.Close)
	pin := tlsutil.Fingerprint(hs.Certificate().Raw)
	for _, tc := range []struct {
		fingerprint string
		ok          bool
	}{{pin, true}, {"00:" + pin[3:], false}} {
		c, err := aoscx.New(config.Switch{Name: "sw1", URL: hs.URL, Username: "dcdash", Password: config.NewSecret("pw"),
			TLS: config.TLSClient{Fingerprint: tc.fingerprint}}, zerolog.Nop())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Inventory(ctx); (err == nil) != tc.ok {
			t.Errorf("fingerprint %s: %v", tc.fingerprint, err)
		}
	}
}

func TestPortsVLANsAndMACs(t *testing.T) {
	ctx := context.Background()
	fake := cxfake.New("dcdash", "pw")
	fake.Ports["1/1/2"].PoE = &cxfake.PoE{Status: "delivering", PowerW: 4.5}
	c := newClient(t, fake, nil)
	ports, err := c.Ports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 10 || ports[0].Name != "1/1/1" || ports[1].Name != "1/1/2" || ports[9].Name != "1/1/10" {
		t.Fatalf("order: %+v", ports)
	}
	up, srv, off := ports[0], ports[1], ports[3]
	if up.Mode != aoscx.ModeTrunk || !up.AllVLANs || up.VLAN != 1 || !up.Uplink() || len(up.Neighbors) != 1 ||
		up.Neighbors[0].SystemName != "core-sw" || !up.Neighbors[0].Bridge || up.Neighbors[0].MgmtAddress != "192.0.2.2" ||
		!up.LinkUp || up.SpeedMbps != 1000 {
		t.Errorf("uplink: %+v", up)
	}
	if srv.Mode != aoscx.ModeAccess || srv.VLAN != 10 || srv.Uplink() || srv.Description != "app1" || !srv.Enabled ||
		srv.PoE == nil || srv.PoE.Status != "delivering" || srv.PoE.PowerDrawnW != 4.5 {
		t.Errorf("server port: %+v %+v", srv, srv.PoE)
	}
	if off.Enabled || off.AdminUp {
		t.Errorf("shut down port: %+v", off)
	}

	// Port changes are read-modify-write with PUT.
	trunk, trunks, desc, disabled := aoscx.ModeTrunk, []int{10, 20}, "esx1", false
	if err := c.UpdatePort(ctx, "1/1/5", aoscx.PortChange{Mode: &trunk, TrunkVLANs: &trunks, Description: &desc, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	p, err := c.Port(ctx, "1/1/5")
	if err != nil || p.Mode != aoscx.ModeTrunk || p.VLAN != 10 || len(p.TrunkVLANs) != 2 || p.AllVLANs || p.Description != "esx1" || p.Enabled {
		t.Errorf("trunk: %+v %v", p, err)
	}
	access, vlan, empty := aoscx.ModeAccess, 20, ""
	if err := c.UpdatePort(ctx, "1/1/5", aoscx.PortChange{Mode: &access, VLAN: &vlan, Description: &empty}); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.Port(ctx, "1/1/5"); p.Mode != aoscx.ModeAccess || p.VLAN != 20 || len(p.TrunkVLANs) != 0 || p.Description != "" || p.Enabled {
		t.Errorf("access: %+v", p)
	}
	missing := 999
	if err := c.UpdatePort(ctx, "1/1/5", aoscx.PortChange{VLAN: &missing}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing VLAN: %v", err)
	}
	if _, err := c.Port(ctx, "9/9/9"); !errors.Is(err, aoscx.ErrNotFound) {
		t.Errorf("unknown port: %v", err)
	}

	if err := c.CreateVLAN(ctx, 4000, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameVLAN(ctx, 4000, "test2"); err != nil {
		t.Fatal(err)
	}
	vlans, err := c.VLANs(ctx)
	if err != nil || len(vlans) != 4 || vlans[0].ID != 1 || vlans[0].Type != "default" || vlans[3].Name != "test2" || !vlans[3].OperUp {
		t.Errorf("VLANs: %+v %v", vlans, err)
	}
	if err := c.DeleteVLAN(ctx, 4000); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteVLAN(ctx, 1); err == nil {
		t.Error("deleted the default VLAN")
	}
	if err := c.SaveConfig(ctx); err != nil || fake.Saves != 1 {
		t.Errorf("save: %v, %d", err, fake.Saves)
	}
	macs, err := c.MACs(ctx)
	if err != nil || len(macs) != 4 || macs[0].MAC != "00:11:22:33:44:55" || macs[0].Port != "1/1/3" || macs[0].VLAN != 20 {
		t.Errorf("MACs: %+v %v", macs, err)
	}
}
