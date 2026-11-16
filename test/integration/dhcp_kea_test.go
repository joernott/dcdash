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
	"github.com/joernott/dcdash/internal/dhcp/kea"
)

// Tests against the Kea containers kea-dhcp4 and kea-dhcp6 with their
// PostgreSQL host database (see test/integration/compose.yaml).

func keaClient(t *testing.T, family, urlEnv string, password string) *kea.Client {
	t.Helper()
	c, err := kea.New(config.DHCPServer{
		Name:     "kea" + family,
		Family:   family,
		URL:      requireEnv(t, urlEnv),
		Insecure: true,
		Username: "dcdash",
		Password: config.NewSecret(password),
		Timeout:  config.Duration(10 * time.Second),
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func keaCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestKeaProbe(t *testing.T) {
	ctx := keaCtx(t)
	for _, tc := range []struct{ family, env string }{{"v4", "DCDASH_IT_KEA4_URL"}, {"v6", "DCDASH_IT_KEA6_URL"}} {
		c := keaClient(t, tc.family, tc.env, requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
		info, err := c.Probe(ctx)
		if err != nil {
			t.Fatalf("%s: %v", tc.family, err)
		}
		want := kea.Capabilities{Leases: true, LeaseDelete: true, Reservations: true, ReservationWrite: true, Subnets: true, Statistics: true}
		if info.Version[:2] != "3." || !info.HostDatabase || info.Capabilities != want || info.Status.Uptime < 0 {
			t.Errorf("%s: %+v", tc.family, info)
		}
	}

	bad := keaClient(t, "v4", "DCDASH_IT_KEA4_URL", "wrong-password")
	if _, err := bad.Version(ctx); !errors.Is(err, kea.ErrUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}
	c := keaClient(t, "v4", "DCDASH_IT_KEA4_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	if _, err := c.Do(ctx, "no-such-command", nil, nil); !errors.Is(err, kea.ErrUnsupported) {
		t.Errorf("unknown command: %v", err)
	}
}

func TestKeaSubnetsAndStatistics(t *testing.T) {
	ctx := keaCtx(t)
	c := keaClient(t, "v4", "DCDASH_IT_KEA4_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	subnets, err := c.Subnets(ctx)
	if err != nil || len(subnets) != 2 || subnets[0].Prefix != "192.0.2.0/24" {
		t.Fatalf("subnets: %+v, %v", subnets, err)
	}
	s, err := c.Subnet(ctx, 1)
	if err != nil || len(s.Pools) != 2 || s.Pools[0].Pool != "192.0.2.100-192.0.2.199" || len(s.Options) != 1 {
		t.Errorf("subnet 1: %+v, %v", s, err)
	}
	if _, err := c.Subnet(ctx, 99); err == nil {
		t.Error("subnet 99 found")
	}

	if _, err := c.Do(ctx, "lease4-add", map[string]any{"ip-address": "192.0.2.150", "hw-address": "11:22:33:44:55:01", "hostname": "stats"}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.DeleteLease(context.Background(), "192.0.2.150", "") })
	stats, err := c.LeaseStats(ctx)
	if err != nil || len(stats) != 2 || stats[0].SubnetID != 1 || stats[0].Total != 110 || stats[0].Assigned < 1 {
		t.Errorf("lease stats: %+v, %v", stats, err)
	}
	pools, err := c.PoolStats(ctx)
	if err != nil || len(pools) != 3 || pools[0].Total != 100 || pools[1].PoolID != 1 || pools[1].Total != 10 || pools[0].Assigned < 1 {
		t.Errorf("pool stats: %+v, %v", pools, err)
	}

	c6 := keaClient(t, "v6", "DCDASH_IT_KEA6_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	s6, err := c6.Subnet(ctx, 1)
	if err != nil || len(s6.PDPools) != 1 || s6.PDPools[0].DelegatedLen != 56 {
		t.Errorf("subnet6 1: %+v, %v", s6, err)
	}
	stats6, err := c6.LeaseStats(ctx)
	if err != nil || len(stats6) != 1 || stats6[0].Total != 4096 || stats6[0].TotalPD != 256 {
		t.Errorf("lease6 stats: %+v, %v", stats6, err)
	}
}

func TestKeaLeases(t *testing.T) {
	ctx := keaCtx(t)
	c := keaClient(t, "v4", "DCDASH_IT_KEA4_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	for i, ip := range []string{"192.0.2.160", "192.0.2.161", "192.0.2.162"} {
		if _, err := c.Do(ctx, "lease4-add", map[string]any{"ip-address": ip, "hw-address": "11:22:33:44:56:0" + string(rune('1'+i)), "hostname": "lease-it"}, nil); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = c.DeleteLease(context.Background(), ip, "") })
	}
	page, err := c.LeasePage(ctx, "192.0.2.160", 2)
	if err != nil || len(page) != 2 || page[0].IPAddress != "192.0.2.161" {
		t.Errorf("page: %+v, %v", page, err)
	}
	for _, q := range []struct{ key, value string }{
		{kea.ByIP, "192.0.2.160"}, {kea.ByHWAddress, "11:22:33:44:56:02"}, {kea.ByHostname, "lease-it"},
	} {
		got, err := c.LeasesBy(ctx, q.key, q.value)
		if err != nil || len(got) == 0 || got[0].Hostname != "lease-it" {
			t.Errorf("by %s: %+v, %v", q.key, got, err)
		}
	}
	if got, err := c.LeasesBy(ctx, kea.ByIP, "192.0.2.199"); err != nil || len(got) != 0 {
		t.Errorf("missing lease: %+v, %v", got, err)
	}
	if _, err := c.LeasesBy(ctx, kea.ByDUID, "00:01"); err == nil {
		t.Error("v4 searched by DUID")
	}
	if ok, err := c.DeleteLease(ctx, "192.0.2.162", ""); !ok || err != nil {
		t.Errorf("delete: %v, %v", ok, err)
	}
	if ok, err := c.DeleteLease(ctx, "192.0.2.162", ""); ok || err != nil {
		t.Errorf("delete again: %v, %v", ok, err)
	}

	c6 := keaClient(t, "v6", "DCDASH_IT_KEA6_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	duid := "00:03:00:01:11:22:33:44:55:66"
	t.Cleanup(func() {
		_, _ = c6.DeleteLease(context.Background(), "2001:db8:1::1001", "")
		_, _ = c6.DeleteLease(context.Background(), "2001:db8:8000:100::", "IA_PD")
	})
	if _, err := c6.Do(ctx, "lease6-add", map[string]any{"ip-address": "2001:db8:1::1001", "duid": duid, "iaid": 1, "hostname": "lease6-it"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c6.Do(ctx, "lease6-add", map[string]any{"ip-address": "2001:db8:8000:100::", "prefix-len": 56, "type": "IA_PD", "duid": duid, "iaid": 2, "subnet-id": 1}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := c6.LeasesBy(ctx, kea.ByDUID, duid)
	if err != nil || len(got) != 2 {
		t.Errorf("v6 by DUID: %+v, %v", got, err)
	}
	if ok, err := c6.DeleteLease(ctx, "2001:db8:8000:100::", "IA_PD"); !ok || err != nil {
		t.Errorf("delete PD: %v, %v", ok, err)
	}
	if ok, err := c6.DeleteLease(ctx, "2001:db8:1::1001", ""); !ok || err != nil {
		t.Errorf("delete NA: %v, %v", ok, err)
	}
}

func TestKeaReservations(t *testing.T) {
	ctx := keaCtx(t)
	c := keaClient(t, "v4", "DCDASH_IT_KEA4_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	res := kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:02", IPAddress: "192.0.2.11", Hostname: "scanner"}
	if err := c.AddReservation(ctx, res); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.DeleteReservation(context.Background(), 1, "hw-address", "aa:bb:cc:00:00:02") })
	if err := c.AddReservation(ctx, res); err == nil {
		t.Error("duplicate reservation added")
	}

	all, err := c.Reservations(ctx, -1)
	if err != nil || len(all) != 2 || all[0].Source != kea.SourceConfig || all[1].Source != kea.SourceDatabase || all[1].Hostname != "scanner" {
		t.Fatalf("reservations: %+v, %v", all, err)
	}
	if got, err := c.Reservations(ctx, 2); err != nil || len(got) != 0 {
		t.Errorf("subnet 2: %+v, %v", got, err)
	}

	got, found, err := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:02", kea.SourceDatabase)
	if err != nil || !found || got.IPAddress != "192.0.2.11" {
		t.Errorf("get: %+v, %v, %v", got, found, err)
	}
	if _, found, err := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:01", kea.SourceDatabase); found || err != nil {
		t.Errorf("config reservation found in the database: %v, %v", found, err)
	}
	if _, found, err := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:01", kea.SourceConfig); !found || err != nil {
		t.Errorf("config reservation: %v, %v", found, err)
	}

	res.IPAddress, res.Hostname = "192.0.2.12", "scanner-2"
	if err := c.UpdateReservation(ctx, res); err != nil {
		t.Errorf("update: %v", err)
	}
	if got, _, _ := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:02", kea.SourceDatabase); got.Hostname != "scanner-2" {
		t.Errorf("after update: %+v", got)
	}
	cfgRes := kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:01", IPAddress: "192.0.2.13"}
	if err := c.UpdateReservation(ctx, cfgRes); err == nil {
		t.Error("configuration file reservation updated")
	}

	if ok, err := c.DeleteReservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:02"); !ok || err != nil {
		t.Errorf("delete: %v, %v", ok, err)
	}
	if ok, err := c.DeleteReservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:02"); ok || err != nil {
		t.Errorf("delete again: %v, %v", ok, err)
	}

	c6 := keaClient(t, "v6", "DCDASH_IT_KEA6_URL", requireEnv(t, "DCDASH_IT_KEA_PASSWORD"))
	r6 := kea.Reservation{SubnetID: 1, DUID: "00:03:00:01:aa:bb:cc:00:00:02", IPAddresses: []string{"2001:db8:1::11"}, Prefixes: []string{"2001:db8:8000:200::/56"}, Hostname: "nas6"}
	if err := c6.AddReservation(ctx, r6); err != nil {
		t.Fatal(err)
	}
	all6, err := c6.Reservations(ctx, 1)
	if err != nil || len(all6) != 2 || len(all6[1].Prefixes) != 1 {
		t.Errorf("v6 reservations: %+v, %v", all6, err)
	}
	if ok, err := c6.DeleteReservation(ctx, 1, "duid", r6.DUID); !ok || err != nil {
		t.Errorf("v6 delete: %v, %v", ok, err)
	}
}
