// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

// tagSlugs returns the tag slugs of a fake object.
func tagSlugs(e *apiEnv, o nbfake.Object) []string {
	var out []string
	ids, _ := o["tags"].([]int64)
	for _, tag := range objects(e, nbfake.Tags) {
		if slices.Contains(ids, tag["id"].(int64)) {
			out = append(out, tag["slug"].(string))
		}
	}
	slices.Sort(out)
	return out
}

func TestProjectTagsOfGuests(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	e.fake.Seed(nbfake.Tags, nbfake.Object{"name": "Webshop", "slug": "project-webshop"})
	e.fake.Seed(nbfake.Tags, nbfake.Object{"name": "Intranet", "slug": "project-intranet"})
	g := guest()
	g.Projects = []string{"project-webshop", "project-missing"}
	if res := e.svc.SyncGuest(ctx, base, g); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	vm := objects(e, nbfake.VMs)[0]
	if got := tagSlugs(e, vm); !slices.Equal(got, []string{"dcdash", "project-webshop"}) {
		t.Errorf("VM tags: %v", got)
	}
	if got := tagSlugs(e, address(e, "192.0.2.50/24")); !slices.Equal(got, []string{"dcdash", "project-webshop"}) {
		t.Errorf("address tags: %v", got)
	}
	// Changing the projects keeps other tags.
	g.Projects = []string{"project-intranet"}
	if res := e.svc.SyncGuest(ctx, base, g); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if got := tagSlugs(e, objects(e, nbfake.VMs)[0]); !slices.Equal(got, []string{"dcdash", "project-intranet"}) {
		t.Errorf("VM tags after change: %v", got)
	}

	// Addresses of reservations and records.
	ps := []string{"webshop"}
	r := netbox.Reservation{Server: "kea4", Addresses: []string{"192.0.2.70"}, Projects: &ps}
	if res := e.svc.SyncReservation(ctx, base, "kea4/192.0.2.0/24", r, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if got := tagSlugs(e, address(e, "192.0.2.70/24")); !slices.Equal(got, []string{"dcdash", "project-webshop"}) {
		t.Errorf("reservation tags: %v", got)
	}
	none := []string{}
	rec := netbox.DNSRecord{Name: "x.example.org", Address: "192.0.2.70", Projects: &none}
	if res := e.svc.SyncDNSName(ctx, base, "cf/example.org", rec, nil); res.Status != netbox.StatusOK {
		t.Fatal(res)
	}
	if got := tagSlugs(e, address(e, "192.0.2.70/24")); !slices.Equal(got, []string{"dcdash"}) {
		t.Errorf("record tags: %v", got)
	}
}

func TestProjectObjects(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	e.fake.Seed(nbfake.Tags, nbfake.Object{"name": "Webshop", "slug": "project-webshop"})
	dev := e.fake.Seed(nbfake.Devices, nbfake.Object{"name": "switch1"})
	port := e.fake.Seed(nbfake.DevIfaces, nbfake.Object{"device": dev, "name": "1/1/1"})
	rng := e.fake.Seed(nbfake.IPRanges, nbfake.Object{"start_address": "192.0.2.100/24", "end_address": "192.0.2.199/24"})

	found, err := e.svc.SearchObjects(ctx, "prefix", "192.0.2")
	if err != nil || len(found) != 1 || found[0].Display != "192.0.2.0/24" {
		t.Fatalf("search: %+v %v", found, err)
	}
	for _, o := range []struct {
		typ string
		id  int64
	}{{"prefix", found[0].ID}, {"interface", port}, {"iprange", rng}, {"vlan", 1}} {
		if _, err := e.svc.SetObjectProject(ctx, base, o.typ, o.id, "project-webshop", true); err != nil {
			t.Fatalf("%s: %v", o.typ, err)
		}
	}
	objs, err := e.svc.ProjectObjects(ctx, "project-webshop")
	if err != nil || len(objs["prefix"]) != 1 || len(objs["interface"]) != 1 || objs["interface"][0].Display != "switch1 1/1/1" ||
		objs["iprange"][0].Display != "192.0.2.100/24 – 192.0.2.199/24" || objs["vlan"][0].Display != "100 servers" || len(objs["vm"]) != 0 {
		t.Fatalf("objects: %+v %v", objs, err)
	}
	if _, err := e.svc.SetObjectProject(ctx, base, "vlan", 1, "project-webshop", false); err != nil {
		t.Fatal(err)
	}
	if objs, _ := e.svc.ProjectObjects(ctx, "project-webshop"); len(objs["vlan"]) != 0 {
		t.Errorf("VLAN not removed: %+v", objs["vlan"])
	}
	if _, err := e.svc.SetObjectProject(ctx, base, "vlan", 1, "project-none", true); !errors.Is(err, netbox.ErrNoTag) {
		t.Errorf("missing tag: %v", err)
	}
	if _, err := e.svc.SearchObjects(ctx, "device", "x"); err == nil {
		t.Error("unknown type accepted")
	}
}
