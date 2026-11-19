// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
)

// The NetBox module against a real NetBox (task 12.6). It runs when DCDASH_IT_NETBOX_URL and
// DCDASH_IT_NETBOX_TOKEN are set (scripts/netbox-test.sh starts a NetBox 4.7 container and
// sets them; optionally DCDASH_IT_NETBOX_FINGERPRINT for https). The token needs write access
// to tags, clusters, VLANs, prefixes, VMs, interfaces, disks, MAC and IP addresses. The test
// creates its own tag, cluster, VLAN and prefix with a unique suffix and deletes everything it
// created at the end, also when it fails.

// itGuests is a netbox.GuestSource with fixed guests.
type itGuests struct{ guests []netbox.Guest }

func (s *itGuests) NetBoxGuests(context.Context, string) ([]netbox.Guest, error) {
	return s.guests, nil
}

func (s *itGuests) NetBoxGuest(_ context.Context, _ string, container bool, vmid int) (netbox.Guest, error) {
	for _, g := range s.guests {
		if g.VMID == vmid && g.Container == container {
			return g, nil
		}
	}
	return netbox.Guest{}, netbox.ErrNotFound
}

func TestNetBox(t *testing.T) {
	rawURL := os.Getenv("DCDASH_IT_NETBOX_URL")
	if rawURL == "" {
		t.Skip("DCDASH_IT_NETBOX_URL not set")
	}
	token := requireEnv(t, "DCDASH_IT_NETBOX_TOKEN")
	ctx := context.Background()
	suffix := strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	base := config.NetBox{Name: "netbox", URL: rawURL, Insecure: strings.HasPrefix(rawURL, "http:"),
		TLS: config.TLSClient{Fingerprint: os.Getenv("DCDASH_IT_NETBOX_FINGERPRINT")}, Token: config.NewSecret(token)}
	c, err := netbox.NewClient(base)
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NetBox %s", v)

	// Fixtures, deleted in reverse order.
	var cleanup []func()
	t.Cleanup(func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	})
	create := func(path string, body map[string]any) int64 {
		t.Helper()
		var o struct {
			ID int64 `json:"id"`
		}
		if err := c.Create(ctx, path, body, &o); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
		cleanup = append(cleanup, func() {
			if err := c.Delete(ctx, path, o.ID, ""); err != nil {
				t.Logf("cleanup %s%d: %v", path, o.ID, err)
			}
		})
		return o.ID
	}
	tag := "dcdash-it-" + suffix
	create("/extras/tags/", map[string]any{"name": tag, "slug": tag})
	ct := create("/virtualization/cluster-types/", map[string]any{"name": "dcdash-it-" + suffix, "slug": "dcdash-it-" + suffix})
	cluster := "dcdash-it-" + suffix
	create("/virtualization/clusters/", map[string]any{"name": cluster, "type": ct})
	group := create("/ipam/vlan-groups/", map[string]any{"name": "dcdash-it-" + suffix, "slug": "dcdash-it-" + suffix})
	vlan := create("/ipam/vlans/", map[string]any{"vid": 172, "name": "it-servers", "group": group})
	create("/ipam/prefixes/", map[string]any{"prefix": "198.18.0.0/24", "vlan": vlan, "tags": []any{map[string]any{"slug": tag}}})
	// Addresses the test creates are deleted by tag at the end (they come and go with
	// interfaces, so they are not tracked one by one).
	t.Cleanup(func() {
		ips, _ := c.IPAddresses(ctx, url.Values{"tag": {tag}})
		for _, ip := range ips {
			_ = c.Delete(ctx, "/ipam/ip-addresses/", ip.ID, "")
		}
		vms, _ := c.VMs(ctx, url.Values{"tag": {tag}})
		for _, vm := range vms {
			_ = c.Delete(ctx, "/virtualization/virtual-machines/", vm.ID, "")
		}
	})

	cfg := base
	cfg.WriteBack, cfg.Tag, cfg.OnDelete, cfg.VLANGroup = true, tag, config.NetBoxDeleteTagged, "dcdash-it-"+suffix
	cfg.Clusters = map[string]string{"lab": cluster}
	svc, err := netbox.NewService(cfg, (*audit.Log)(nil), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StatusChecks()[0].Detail(ctx); err != nil {
		t.Fatalf("status: %v", err)
	}
	vl, err := svc.VLAN(ctx, 172)
	if err != nil || vl.Name != "it-servers" || len(vl.Prefixes) != 1 || len(vl.Prefixes[0].Free) == 0 ||
		vl.Prefixes[0].Free[0] != "198.18.0.1/24" {
		t.Fatalf("VLAN: %+v %v", vl, err)
	}

	ab := audit.Entry{Actor: "dcdash-it"}
	g := netbox.Guest{Cluster: "lab", VMID: 1001, Name: "dcdash-it-" + suffix, VCPUs: 2, MemoryMiB: 2048,
		Disks: []netbox.GuestDisk{{Name: "scsi0", SizeMiB: 20480}},
		Networks: []netbox.GuestNetwork{
			{Name: "net0", MAC: "BC:24:11:AA:00:01", VLAN: 172, Addresses: []string{"198.18.0.10/24"}},
			{Name: "net1", MAC: "BC:24:11:AA:00:02"},
		}}
	if res := svc.SyncGuest(ctx, ab, g); res.Status != netbox.StatusOK {
		t.Fatalf("create: %+v", res)
	}
	vm, err := c.FindVM(ctx, url.Values{"name": {g.Name}})
	if err != nil || !vm.HasTag(tag) || vm.Disk == nil || *vm.Disk != 20480 || vm.PrimaryIP4 == nil ||
		vm.PrimaryIP4.Address != "198.18.0.10/24" {
		t.Fatalf("VM: %+v %v", vm, err)
	}
	ifcs, _ := c.Interfaces(ctx, url.Values{"virtual_machine_id": {strconv.FormatInt(vm.ID, 10)}})
	if len(ifcs) != 2 || ifcs[0].PrimaryMAC == nil || ifcs[0].UntaggedVLAN == nil || ifcs[0].UntaggedVLAN.VID != 172 {
		t.Fatalf("interfaces: %+v", ifcs)
	}

	// The guest is renamed, gets more memory and a second disk, loses net1 and gets a new
	// MAC address on net0.
	g2 := g
	g2.Name, g2.OldName, g2.MemoryMiB = g.Name+"-b", g.Name, 4096
	g2.Disks = []netbox.GuestDisk{{Name: "scsi0", SizeMiB: 30720}, {Name: "scsi1", SizeMiB: 8192}}
	g2.Networks = []netbox.GuestNetwork{{Name: "net0", MAC: "BC:24:11:AA:00:03", VLAN: 172, Addresses: []string{"198.18.0.10/24"}}}
	if res := svc.SyncGuest(ctx, ab, g2); res.Status != netbox.StatusOK {
		t.Fatalf("update: %+v", res)
	}
	vm, err = c.FindVM(ctx, url.Values{"name": {g2.Name}})
	if err != nil || *vm.Memory != 4096 || *vm.Disk != 30720+8192 {
		t.Fatalf("updated VM: %+v %v", vm, err)
	}
	macs, _ := c.MACAddresses(ctx, url.Values{"mac_address": {"BC:24:11:AA:00:01"}})
	if len(macs) != 0 {
		t.Errorf("old MAC address kept: %+v", macs)
	}

	// A DHCP reservation for the guest's MAC address and DNS names.
	r := netbox.Reservation{Server: "kea4", Addresses: []string{"198.18.0.20"}, MAC: "bc:24:11:aa:00:03"}
	if res := svc.SyncReservation(ctx, ab, "kea4/198.18.0.0/24", r, nil); res.Status != netbox.StatusOK {
		t.Fatalf("reservation: %+v", res)
	}
	ips, _ := c.IPAddresses(ctx, url.Values{"address": {"198.18.0.20"}})
	if len(ips) != 1 || ips[0].Address != "198.18.0.20/24" || ips[0].AssignedObjectID == nil {
		t.Fatalf("reserved address: %+v", ips)
	}
	rec := netbox.DNSRecord{Name: g2.Name + ".example.org.", Address: "198.18.0.10"}
	if res := svc.SyncDNSName(ctx, ab, "cf/example.org", rec, nil); res.Status != netbox.StatusOK {
		t.Fatalf("DNS: %+v", res)
	}
	ips, _ = c.IPAddresses(ctx, url.Values{"address": {"198.18.0.10"}})
	if len(ips) != 1 || ips[0].DNSName != g2.Name+".example.org" {
		t.Fatalf("DNS name: %+v", ips)
	}
	if res := svc.ClearDNSName(ctx, ab, "cf/example.org", rec); res.Status != netbox.StatusOK {
		t.Fatalf("clear DNS: %+v", res)
	}

	// The comparison sees the change of memory.
	src := &itGuests{guests: []netbox.Guest{g2}}
	src.guests[0].MemoryMiB = 8192
	svc.SetSources(src, nil, nil)
	cmp, err := svc.CompareGuests(ctx, "lab", func(bool, int) bool { return true })
	if err != nil || len(cmp.Guests) != 1 || cmp.Guests[0].State != netbox.StateDiffers || cmp.Guests[0].Differences[0].Field != "memory" {
		t.Fatalf("comparison: %+v %v", cmp, err)
	}

	// Deleting the guest deletes the VM (it has the tag) with its interfaces and addresses.
	if res := svc.DeleteGuest(ctx, ab, "lab", false, 1001, g2.Name); res.Status != netbox.StatusOK {
		t.Fatalf("delete: %+v", res)
	}
	if _, err := c.FindVM(ctx, url.Values{"name": {g2.Name}}); err == nil {
		t.Error("VM not deleted")
	}
	ips, _ = c.IPAddresses(ctx, url.Values{"address": {"198.18.0.10"}})
	if len(ips) != 0 {
		t.Errorf("address of the deleted interface kept: %+v", ips)
	}
	if res := svc.DeleteReservation(ctx, ab, "kea4/198.18.0.0/24", r); res.Status != netbox.StatusOK {
		t.Fatalf("delete reservation: %+v", res)
	}

	// Projects (task 12.8): the tag is created, a prefix found by search, tagged and listed.
	ptag := "project-it-" + suffix
	sync, err := svc.SyncProjectTags(ctx, "project-it-"+suffix, []netbox.ProjectTag{{Slug: ptag, Name: "IT " + suffix, Color: "2196f3"}})
	if err != nil || len(sync.Created) != 1 {
		t.Fatalf("project tags: %+v %v", sync, err)
	}
	t.Cleanup(func() {
		tags, _ := c.Tags(ctx)
		for _, tg := range tags {
			if tg.Slug == ptag {
				_ = c.Delete(ctx, "/extras/tags/", tg.ID, "")
			}
		}
	})
	found, err := svc.SearchObjects(ctx, "prefix", "198.18.0")
	if err != nil || len(found) == 0 || found[0].Display != "198.18.0.0/24" {
		t.Fatalf("search: %+v %v", found, err)
	}
	if _, err := svc.SetObjectProject(ctx, ab, "prefix", found[0].ID, ptag, true); err != nil {
		t.Fatalf("tag prefix: %v", err)
	}
	objs, err := svc.ProjectObjects(ctx, ptag)
	if err != nil || len(objs["prefix"]) != 1 || objs["prefix"][0].URL == "" {
		t.Fatalf("project objects: %+v %v", objs, err)
	}
	if _, err := svc.SetObjectProject(ctx, ab, "prefix", found[0].ID, ptag, false); err != nil {
		t.Fatalf("untag prefix: %v", err)
	}
}
