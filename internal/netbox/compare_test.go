// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func init() {
	testUsers["comparer"] = []string{"netbox:netbox:sync:write", "proxmox:lab:qemu/5000:read", "proxmox:lab:qemu/5001:read"}
	testUsers["looker"] = []string{"netbox:netbox:sync:read", "proxmox:lab:*:read"}
}

type sources struct {
	guests []netbox.Guest
	resv   []netbox.Reservation
	recs   []netbox.DNSRecord
}

func (s *sources) NetBoxGuests(context.Context, string) ([]netbox.Guest, error) { return s.guests, nil }

func (s *sources) NetBoxGuest(_ context.Context, _ string, container bool, vmid int) (netbox.Guest, error) {
	for _, g := range s.guests {
		if g.VMID == vmid && g.Container == container {
			return g, nil
		}
	}
	return netbox.Guest{}, netbox.ErrNotFound
}

func (s *sources) NetBoxReservations(context.Context) ([]netbox.Reservation, error) {
	return s.resv, nil
}

func (s *sources) NetBoxRecords(context.Context) ([]netbox.DNSRecord, error) { return s.recs, nil }

func TestCompareGuests(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	src := &sources{guests: []netbox.Guest{guest(), {Cluster: "lab", VMID: 5001, Name: "db1", MemoryMiB: 1024},
		{Cluster: "lab", VMID: 5002, Name: "hidden", MemoryMiB: 1024}}}
	e.svc.SetSources(src, src, src)
	if res := e.svc.SyncGuest(ctx, base, guest()); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	src.guests[0].MemoryMiB = 8192
	src.guests[0].Networks[1].VLAN = 0 // VLAN 999 isn't in NetBox: written untagged
	src.guests[0].Networks[0].Addresses = append(src.guests[0].Networks[0].Addresses, "192.0.2.51/24")
	tag := int64(1)
	extra := e.fake.Seed(nbfake.VMs, nbfake.Object{"name": "old", "cluster": int64(1), "tags": []int64{tag}})
	manual := e.fake.Seed(nbfake.VMs, nbfake.Object{"name": "manual", "cluster": int64(1)})

	var cmp netbox.GuestComparison
	if st, code := e.do(t, "comparer", "GET", "/api/v1/netbox/compare/guests/lab", nil, &cmp); st != 200 {
		t.Fatal(st, code)
	}
	if cmp.NetBoxCluster != "Lab" || len(cmp.Guests) != 4 {
		t.Fatalf("%+v", cmp)
	}
	g := cmp.Guests
	// Sorted by VMID; VMs that exist only in NetBox have no VMID.
	if g[0].Name != "manual" || g[0].State != netbox.StateExtra || g[0].Tagged ||
		g[1].Name != "old" || g[1].State != netbox.StateExtra || !g[1].Tagged {
		t.Errorf("extra: %+v %+v", g[0], g[1])
	}
	if g[2].VMID != 5000 || g[2].State != netbox.StateDiffers || len(g[2].Differences) != 2 ||
		g[2].Differences[0].Field != "memory" || g[2].Differences[0].Actual != "8192" || g[2].Differences[0].NetBox != "2048" ||
		g[2].Differences[1].Field != "addresses" {
		t.Errorf("differs: %+v", g[2])
	}
	if g[3].VMID != 5001 || g[3].State != netbox.StateMissing || g[3].Type != "qemu" {
		t.Errorf("missing: %+v", g[3])
	}

	// Update from the view.
	var wb netbox.WriteBack
	if st, code := e.do(t, "comparer", "POST", "/api/v1/netbox/compare/guests/lab/qemu/5001", nil, &wb); st != 200 || wb.NetBox.Status != netbox.StatusOK {
		t.Fatal(st, code, wb)
	}
	if st, _ := e.do(t, "comparer", "POST", "/api/v1/netbox/compare/guests/lab/qemu/5002", nil, nil); st != 404 {
		t.Errorf("hidden guest: %d", st)
	}
	if st, _ := e.do(t, "looker", "POST", "/api/v1/netbox/compare/guests/lab/qemu/5001", nil, nil); st != 403 {
		t.Errorf("read only: %d", st)
	}
	if l, _ := e.audit.Query(ctx, audit.Filter{Action: netbox.AuditApply}); len(l) != 1 || l[0].Target != "lab/qemu/5001" || l[0].Actor != "comparer" {
		t.Errorf("audit: %+v", l)
	}
	e.do(t, "looker", "GET", "/api/v1/netbox/compare/guests/lab", nil, &cmp)
	if len(cmp.Guests) != 5 || cmp.Guests[4].VMID != 5002 || cmp.Guests[3].State != netbox.StateOK {
		t.Errorf("after update: %+v", cmp.Guests)
	}

	// Deleting VMs that exist only in NetBox: only with the tag.
	if st, code := e.do(t, "comparer", "DELETE", "/api/v1/netbox/compare/guests/lab/vms/"+itoa(manual), nil, nil); st != 409 || code != "not_tagged" {
		t.Errorf("untagged: %d %s", st, code)
	}
	if st, code := e.do(t, "comparer", "DELETE", "/api/v1/netbox/compare/guests/lab/vms/"+itoa(cmp.Guests[2].NetBoxID), nil, nil); st != 409 || code != "guest_exists" {
		t.Errorf("guest exists: %d %s", st, code)
	}
	if st, code := e.do(t, "comparer", "DELETE", "/api/v1/netbox/compare/guests/lab/vms/"+itoa(extra), nil, &wb); st != 200 || wb.NetBox.Status != netbox.StatusOK {
		t.Errorf("delete: %d %s", st, code)
	}
	if st, _ := e.do(t, "comparer", "GET", "/api/v1/netbox/compare/guests/prod", nil, nil); st != 404 {
		t.Errorf("unmapped: %d", st)
	}
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/netbox/compare/guests/lab", nil, nil); st != 403 {
		t.Errorf("without sync:read: %d", st)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestCompareAddresses(t *testing.T) {
	e := newAPIEnv(t, nil)
	src := &sources{
		resv: []netbox.Reservation{
			{Server: "kea4-a", Addresses: []string{"192.0.2.10", "203.0.113.9"}},
			{Server: "kea4-b", Addresses: []string{"203.0.113.9"}},
		},
		recs: []netbox.DNSRecord{{Name: "printer.example.org", Address: "192.0.2.10"}, {Name: "new.example.org", Address: "2001:db8::9"}},
	}
	e.svc.SetSources(nil, src, src)
	var cmp netbox.AddressComparison
	if st, code := e.do(t, "looker", "GET", "/api/v1/netbox/compare/addresses", nil, &cmp); st != 200 {
		t.Fatal(st, code)
	}
	want := []netbox.AddressDiff{
		{Address: "192.0.2.10", Source: "dns", Name: "printer.example.org", State: netbox.StateDiffers},
		{Address: "203.0.113.9", Source: "dhcp:kea4-a", State: netbox.StateMissing},
		{Address: "2001:db8::9", Source: "dns", Name: "new.example.org", State: netbox.StateMissing},
	}
	if len(cmp.Addresses) != len(want) || len(cmp.Errors) != 0 {
		t.Fatalf("%+v", cmp)
	}
	for i, w := range want {
		if cmp.Addresses[i] != w {
			t.Errorf("%d: got %+v, want %+v", i, cmp.Addresses[i], w)
		}
	}
}
