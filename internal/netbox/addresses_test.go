// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"testing"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

// address returns the fake's object of an address (with prefix length).
func address(e *apiEnv, a string) nbfake.Object {
	for _, o := range objects(e, nbfake.IPs) {
		if o["address"] == a {
			return o
		}
	}
	return nil
}

func TestReservations(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	// The guest exists in NetBox with its interface and MAC address.
	if res := e.svc.SyncGuest(ctx, base, guest()); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	r := netbox.Reservation{Server: "kea4", Addresses: []string{"192.0.2.60"}, MAC: "bc:24:11:00:00:01"}
	if res := e.svc.SyncReservation(ctx, base, "kea4/192.0.2.0/24", r, nil); res == nil || res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	ip := address(e, "192.0.2.60/24") // prefix length of the NetBox prefix
	if ip == nil || ip["description"] != "DHCP reservation (kea4)" || ip["assigned_object_id"] == nil {
		t.Fatalf("addresses: %+v", objects(e, nbfake.IPs))
	}
	// Without a matching interface and outside the prefixes: /32, unassigned.
	r2 := netbox.Reservation{Server: "kea4", Addresses: []string{"203.0.113.5"}, MAC: "aa:bb:cc:00:00:01"}
	if res := e.svc.SyncReservation(ctx, base, "kea4/global", r2, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if ip := address(e, "203.0.113.5/32"); ip == nil || ip["assigned_object_id"] != nil {
		t.Fatalf("addresses: %+v", objects(e, nbfake.IPs))
	}
	// An existing address keeps its description and gets assigned.
	n := len(objects(e, nbfake.IPs))
	if res := e.svc.SyncReservation(ctx, base, "kea4/192.0.2.0/24",
		netbox.Reservation{Server: "kea4", Addresses: []string{"192.0.2.10"}, MAC: "BC:24:11:00:00:02"}, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if ip := address(e, "192.0.2.10/24"); len(objects(e, nbfake.IPs)) != n || ip["description"] != "DHCP reservation (kea4)" ||
		ip["assigned_object_id"] == nil {
		t.Errorf("existing: %+v", ip)
	}

	// Changing the address of the unassigned reservation deletes the old
	// one (created by dcdash, no DNS name).
	r3 := r2
	r3.Addresses = []string{"203.0.113.6"}
	if res := e.svc.SyncReservation(ctx, base, "kea4/global", r3, &r2); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if address(e, "203.0.113.5/32") != nil || address(e, "203.0.113.6/32") == nil {
		t.Errorf("changed: %+v", objects(e, nbfake.IPs))
	}
	// Deleting a reservation whose address is assigned only clears the
	// description.
	if res := e.svc.DeleteReservation(ctx, base, "kea4/192.0.2.0/24", r); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if ip := address(e, "192.0.2.60/24"); ip == nil || ip["description"] != "" {
		t.Errorf("deleted, assigned: %+v", ip)
	}
	if res := e.svc.DeleteReservation(ctx, base, "kea4/global", r3); res.Status != netbox.StatusOK || address(e, "203.0.113.6/32") != nil {
		t.Errorf("deleted: %+v %+v", res, objects(e, nbfake.IPs))
	}
	if l := e.svc.DeleteReservation(ctx, base, "kea4/global", r3); l.Status != netbox.StatusOK {
		t.Errorf("missing address: %+v", l)
	}

	// Read-only NetBox and nil service.
	ro := newAPIEnv(t, func(c *config.NetBox) { c.WriteBack = false })
	var none *netbox.Service
	if ro.svc.SyncReservation(ctx, base, "x", r, nil) != nil || none.DeleteReservation(ctx, base, "x", r) != nil || ro.svc.Writes() {
		t.Error("write-back disabled")
	}
}

func TestDNSNames(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	rec := netbox.DNSRecord{Name: "App1.example.org.", Address: "192.0.2.10"}
	if res := e.svc.SyncDNSName(ctx, base, "cf/example.org", rec, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if ip := address(e, "192.0.2.10/24"); ip["dns_name"] != "app1.example.org" {
		t.Errorf("existing address: %+v", ip)
	}
	v6 := netbox.DNSRecord{Name: "app1.example.org", Address: "2001:db8:1::10"}
	if res := e.svc.SyncDNSName(ctx, base, "cf/example.org", v6, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if ip := address(e, "2001:db8:1::10/64"); ip == nil || ip["dns_name"] != "app1.example.org" || len(ip["tags"].([]int64)) != 1 {
		t.Errorf("new address: %+v", objects(e, nbfake.IPs))
	}
	// Renamed: the new name replaces the old one; moved to another address:
	// the old address loses the name.
	moved := netbox.DNSRecord{Name: "app2.example.org", Address: "192.0.2.2"}
	if res := e.svc.SyncDNSName(ctx, base, "cf/example.org", moved, &rec); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if address(e, "192.0.2.10/24")["dns_name"] != "" || address(e, "192.0.2.2/24")["dns_name"] != "app2.example.org" {
		t.Errorf("moved: %+v", objects(e, nbfake.IPs))
	}
	// Deleting clears the name only if it is the record's.
	if res := e.svc.ClearDNSName(ctx, base, "cf/example.org", netbox.DNSRecord{Name: "other.example.org", Address: "192.0.2.2"}); res.Status != netbox.StatusOK ||
		address(e, "192.0.2.2/24")["dns_name"] != "app2.example.org" {
		t.Errorf("other name: %+v", address(e, "192.0.2.2/24"))
	}
	if res := e.svc.ClearDNSName(ctx, base, "cf/example.org", moved); res.Status != netbox.StatusOK ||
		address(e, "192.0.2.2/24")["dns_name"] != "" {
		t.Errorf("cleared: %+v", address(e, "192.0.2.2/24"))
	}
	e.fake.FailNext("GET", nbfake.IPs, 500)
	if res := e.svc.SyncDNSName(ctx, base, "cf/example.org", rec, nil); res.Status != netbox.StatusFailed {
		t.Errorf("NetBox down: %+v", res)
	}
}
