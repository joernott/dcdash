// SPDX-License-Identifier: GPL-3.0-or-later

package kea_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dhcp/kea"
	"github.com/joernott/dcdash/internal/dhcp/kea/keafake"
)

func client(t *testing.T, family string, h http.Handler, password string) *kea.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := kea.New(config.DHCPServer{
		Name: "kea", Family: family, URL: srv.URL, Insecure: true,
		Username: "dcdash", Password: config.NewSecret(password),
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fake4() *keafake.Server {
	f := keafake.New("v4", "dcdash", "pw",
		keafake.Subnet{ID: 1, Prefix: "192.0.2.0/24", Pools: []string{"192.0.2.100-192.0.2.199"}, Total: 100},
		keafake.Subnet{ID: 2, Prefix: "198.51.100.0/24", Pools: []string{"198.51.100.10-198.51.100.19"}, Total: 10},
	)
	f.AddLease(keafake.Lease{IP: "192.0.2.101", HWAddress: "11:22:33:44:55:01", Hostname: "a", SubnetID: 1})
	f.AddLease(keafake.Lease{IP: "192.0.2.100", HWAddress: "11:22:33:44:55:02", Hostname: "b", SubnetID: 1})
	f.AddLease(keafake.Lease{IP: "198.51.100.10", HWAddress: "11:22:33:44:55:01", Hostname: "a", SubnetID: 2, State: kea.LeaseDeclined})
	f.AddConfigReservation(keafake.Host{"subnet-id": 1, "hw-address": "aa:bb:cc:00:00:01", "ip-address": "192.0.2.10", "hostname": "printer"})
	return f
}

func TestProbe(t *testing.T) {
	ctx := context.Background()
	f := fake4()
	c := client(t, "v4", f, "pw")
	info, err := c.Probe(ctx)
	want := kea.Capabilities{Leases: true, LeaseDelete: true, Reservations: true, ReservationWrite: true, Subnets: true, Statistics: true}
	if err != nil || info.Version != "3.0.4" || !info.HostDatabase || info.Capabilities != want {
		t.Fatalf("probe: %+v, %v", info, err)
	}

	f.Set(func(s *keafake.Settings) { s.HostDatabase = false; s.Hooks = []string{"host_cmds", "ha"} })
	info, err = c.Probe(ctx)
	want = kea.Capabilities{Reservations: true, HA: true}
	if err != nil || info.HostDatabase || info.Capabilities != want || len(info.Status.HA) != 1 ||
		info.Status.HA[0].Servers.Remote.Name != "server2" {
		t.Errorf("probe without host database: %+v, %v", info, err)
	}

	if _, err := client(t, "v4", f, "wrong").Version(ctx); !errors.Is(err, kea.ErrUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := client(t, "v6", f, "pw").HostDatabase(ctx); err == nil {
		t.Error("v6 client accepted a DHCPv4 configuration")
	}
	if _, err := c.Do(ctx, "foo", nil, nil); !errors.Is(err, kea.ErrUnsupported) {
		t.Errorf("unknown command: %v", err)
	}
}

func TestResponses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{"single object", 200, `{"result": 0, "text": "3.0.4"}`, func(err error) bool { return err == nil }},
		{"conflict", 200, `[{"result": 4, "text": "busy"}]`, func(err error) bool { return errors.Is(err, kea.ErrConflict) }},
		{"error", 200, `[{"result": 1, "text": "bad"}]`, func(err error) bool {
			var e *kea.Error
			return errors.As(err, &e) && e.Text == "bad" && e.Command == "version-get"
		}},
		{"two services", 200, `[{"result": 0}, {"result": 0}]`, func(err error) bool { return errors.Is(err, kea.ErrUnavailable) }},
		{"not json", 200, `<html>`, func(err error) bool { return errors.Is(err, kea.ErrUnavailable) }},
		{"http error", 503, ``, func(err error) bool { return errors.Is(err, kea.ErrUnavailable) }},
	} {
		c := client(t, "v4", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}), "pw")
		if _, err := c.Version(ctx); !tc.check(err) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	c, err := kea.New(config.DHCPServer{Name: "x", Family: "v4", URL: "http://127.0.0.1:1", Username: "u", Password: config.NewSecret("p")}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Version(ctx); !errors.Is(err, kea.ErrUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestLeases(t *testing.T) {
	ctx := context.Background()
	c := client(t, "v4", fake4(), "pw")
	page, err := c.LeasePage(ctx, "", 2)
	if err != nil || len(page) != 2 || page[0].IPAddress != "192.0.2.100" {
		t.Errorf("first page: %+v, %v", page, err)
	}
	page, err = c.LeasePage(ctx, "192.0.2.101", 2)
	if err != nil || len(page) != 1 || page[0].IPAddress != "198.51.100.10" {
		t.Errorf("second page: %+v, %v", page, err)
	}
	if got, err := c.LeasesBy(ctx, kea.ByHWAddress, "11:22:33:44:55:01"); err != nil || len(got) != 2 {
		t.Errorf("by MAC: %+v, %v", got, err)
	}
	if got, err := c.LeasesBy(ctx, kea.ByIP, "192.0.2.100"); err != nil || len(got) != 1 || got[0].Hostname != "b" {
		t.Errorf("by IP: %+v, %v", got, err)
	}
	if got, err := c.LeasesBy(ctx, kea.ByHostname, "nobody"); err != nil || len(got) != 0 {
		t.Errorf("by hostname: %+v, %v", got, err)
	}
	if _, err := c.LeasesBy(ctx, kea.ByDUID, "00:01"); err == nil {
		t.Error("v4 searched by DUID")
	}
	if ok, err := c.DeleteLease(ctx, "192.0.2.100", ""); !ok || err != nil {
		t.Errorf("delete: %v, %v", ok, err)
	}
	if ok, err := c.DeleteLease(ctx, "192.0.2.100", ""); ok || err != nil {
		t.Errorf("delete again: %v, %v", ok, err)
	}
}

func TestSubnetsAndStats(t *testing.T) {
	ctx := context.Background()
	c := client(t, "v4", fake4(), "pw")
	subnets, err := c.Subnets(ctx)
	if err != nil || len(subnets) != 2 || subnets[1].Prefix != "198.51.100.0/24" {
		t.Errorf("subnets: %+v, %v", subnets, err)
	}
	s, err := c.Subnet(ctx, 2)
	if err != nil || len(s.Pools) != 1 {
		t.Errorf("subnet: %+v, %v", s, err)
	}
	if _, err := c.Subnet(ctx, 9); err == nil {
		t.Error("missing subnet found")
	}
	stats, err := c.LeaseStats(ctx)
	if err != nil || len(stats) != 2 || stats[0].Assigned != 2 || stats[1].Declined != 1 || stats[1].Total != 10 {
		t.Errorf("stats: %+v, %v", stats, err)
	}
	pools, err := c.PoolStats(ctx)
	if err != nil || len(pools) != 2 || pools[0].SubnetID != 1 || pools[0].Total != 100 {
		t.Errorf("pool stats: %+v, %v", pools, err)
	}

	f6 := keafake.New("v6", "dcdash", "pw", keafake.Subnet{ID: 1, Prefix: "2001:db8:1::/64", Total: 1.8446744073709552e19, TotalPD: 256})
	f6.AddLease(keafake.Lease{IP: "2001:db8:8000:100::", Type: "IA_PD", PrefixLen: 56, DUID: "00:01", SubnetID: 1})
	stats, err = client(t, "v6", f6, "pw").LeaseStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].Total < 1e19 || stats[0].AssignedPD != 1 || stats[0].TotalPD != 256 {
		t.Errorf("v6 stats: %+v, %v", stats, err)
	}
}

func TestReservations(t *testing.T) {
	ctx := context.Background()
	f := fake4()
	c := client(t, "v4", f, "pw")
	for _, h := range []string{"aa:bb:cc:00:00:02", "aa:bb:cc:00:00:03", "aa:bb:cc:00:00:04"} {
		if err := c.AddReservation(ctx, kea.Reservation{SubnetID: 1, HWAddress: h, Hostname: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddReservation(ctx, kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:01"}); err == nil {
		t.Error("duplicate of a configuration reservation added")
	}
	all, err := c.Reservations(ctx, -1)
	if err != nil || len(all) != 4 || all[0].Source != kea.SourceConfig || all[3].Source != kea.SourceDatabase {
		t.Fatalf("all: %+v, %v", all, err)
	}
	if typ, id := all[0].Identifier(); typ != "hw-address" || id != "aa:bb:cc:00:00:01" {
		t.Errorf("identifier: %s %s", typ, id)
	}
	if got, err := c.Reservations(ctx, 2); err != nil || len(got) != 0 {
		t.Errorf("subnet 2: %+v, %v", got, err)
	}

	res := kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:03", Hostname: "renamed"}
	if err := c.UpdateReservation(ctx, res); err != nil {
		t.Error(err)
	}
	got, found, err := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:03", kea.SourceDatabase)
	if err != nil || !found || got.Hostname != "renamed" || got.Source != kea.SourceDatabase {
		t.Errorf("get: %+v, %v, %v", got, found, err)
	}
	if _, found, _ := c.Reservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:01", kea.SourceDatabase); found {
		t.Error("configuration reservation found in the database")
	}
	if err := c.UpdateReservation(ctx, kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:01"}); err == nil {
		t.Error("configuration reservation updated")
	}
	if ok, err := c.DeleteReservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:03"); !ok || err != nil {
		t.Errorf("delete: %v, %v", ok, err)
	}
	if ok, err := c.DeleteReservation(ctx, 1, "hw-address", "aa:bb:cc:00:00:03"); ok || err != nil {
		t.Errorf("delete again: %v, %v", ok, err)
	}

	f.Set(func(s *keafake.Settings) { s.HostDatabase = false })
	if err := c.AddReservation(ctx, kea.Reservation{SubnetID: 1, HWAddress: "aa:bb:cc:00:00:09"}); err == nil {
		t.Error("reservation added without a host database")
	}
}

func TestHTTPError(t *testing.T) {
	var err error = &kea.HTTPError{Command: "status-get", Status: 408}
	var h interface{ HTTPStatus() int }
	if !errors.Is(err, kea.ErrUnavailable) || !errors.As(err, &h) || h.HTTPStatus() != 408 ||
		err.Error() != kea.ErrUnavailable.Error()+": status-get: HTTP 408" {
		t.Errorf("%v", err)
	}
}
