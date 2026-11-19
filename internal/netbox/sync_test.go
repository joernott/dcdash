// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

var base = audit.Entry{Actor: "alice", ActorSource: "ldap"}

func guest() netbox.Guest {
	return netbox.Guest{
		Cluster: "lab", VMID: 5000, Name: "app1", VCPUs: 4, MemoryMiB: 2048,
		Disks: []netbox.GuestDisk{{Name: "scsi0", SizeMiB: 20480}},
		Networks: []netbox.GuestNetwork{
			{Name: "net0", MAC: "BC:24:11:00:00:01", VLAN: 100, Addresses: []string{"192.0.2.50/24", "2001:db8:1::50/64"}},
			{Name: "net1", MAC: "BC:24:11:00:00:02", VLAN: 999},
		},
	}
}

func objects(e *apiEnv, typ string) []nbfake.Object { return e.fake.Objects(typ) }

func TestSyncGuest(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, func(c *config.NetBox) { c.VMIDField = "proxmox_vmid" })
	res := e.svc.SyncGuest(ctx, base, guest())
	if res == nil || res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	vms := objects(e, nbfake.VMs)
	if len(vms) != 1 || vms[0]["name"] != "app1" || vms[0]["memory"] != float64(2048) || vms[0]["vcpus"] != float64(4) ||
		vms[0]["custom_fields"].(map[string]any)["proxmox_vmid"] != float64(5000) || vms[0]["primary_ip4"] == nil ||
		vms[0]["primary_ip6"] == nil {
		t.Fatalf("vm: %+v", vms)
	}
	ifcs := objects(e, nbfake.Interfaces)
	if len(ifcs) != 2 || ifcs[0]["mode"] != "access" || ifcs[0]["untagged_vlan"] == nil ||
		ifcs[1]["untagged_vlan"] != nil || ifcs[1]["primary_mac_address"] == nil {
		t.Errorf("interfaces: %+v", ifcs)
	}
	if ips := objects(e, nbfake.IPs); len(ips) != 5 || ips[3]["address"] != "192.0.2.50/24" || ips[3]["assigned_object_id"] == nil {
		t.Errorf("addresses: %+v", ips)
	}
	if d := objects(e, nbfake.Disks); len(d) != 1 || d[0]["size"] != float64(20480) {
		t.Errorf("disks: %+v", d)
	}
	if ch := e.fake.Changes(); ch[0].Message != "dcdash (alice): update app1" {
		t.Errorf("change log: %+v", ch[0])
	}
	l, _ := e.audit.Query(ctx, audit.Filter{Action: netbox.AuditSync})
	if len(l) != 1 || l[0].Target != "lab/qemu/5000" || l[0].Result != audit.ResultSuccess || l[0].Actor != "alice" ||
		!strings.Contains(strings.Join(anyStrings(l[0].Details["changes"]), ","), "vm created") {
		t.Errorf("audit: %+v", l)
	}

	// Unchanged: nothing is written.
	n := len(e.fake.Changes())
	if res := e.svc.SyncGuest(ctx, base, guest()); res.Status != netbox.StatusOK || len(e.fake.Changes()) != n {
		t.Errorf("unchanged: %+v, %d changes", res, len(e.fake.Changes())-n)
	}

	// Renamed, more memory, a grown and a new disk, net1 removed, a new MAC
	// address on net0.
	g := guest()
	g.Name, g.MemoryMiB = "app2", 4096
	g.Disks = []netbox.GuestDisk{{Name: "scsi0", SizeMiB: 30720}, {Name: "scsi1", SizeMiB: 8192}}
	g.Networks = g.Networks[:1]
	g.Networks[0].MAC = "BC:24:11:00:00:03"
	if res := e.svc.SyncGuest(ctx, base, g); res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	vms = objects(e, nbfake.VMs)
	if len(vms) != 1 || vms[0]["name"] != "app2" || vms[0]["memory"] != float64(4096) {
		t.Errorf("vm: %+v", vms)
	}
	if d := objects(e, nbfake.Disks); len(d) != 2 || d[0]["size"] != float64(30720) {
		t.Errorf("disks: %+v", d)
	}
	macs := objects(e, nbfake.MACs)
	if len(objects(e, nbfake.Interfaces)) != 1 || len(macs) != 1 || macs[0]["mac_address"] != "BC:24:11:00:00:03" {
		t.Errorf("interfaces: %+v, MACs %+v", objects(e, nbfake.Interfaces), macs)
	}

	// Deleting a VM with the tag deletes it.
	if res := e.svc.DeleteGuest(ctx, base, "lab", false, 5000, "app2"); res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	if len(objects(e, nbfake.VMs)) != 0 || len(objects(e, nbfake.IPs)) != 3 {
		t.Errorf("after delete: %+v %+v", objects(e, nbfake.VMs), objects(e, nbfake.IPs))
	}
	// A VM that isn't in NetBox is no error.
	if res := e.svc.DeleteGuest(ctx, base, "lab", false, 5000, "app2"); res.Status != netbox.StatusOK {
		t.Errorf("%+v", res)
	}
}

func anyStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestSyncGuestFindsVMByName(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	// A VM documented by hand, without the tag: it is updated, and deleting
	// the guest only decommissions it.
	e.fake.Seed(nbfake.VMs, nbfake.Object{"name": "app1", "cluster": int64(1), "status": "active"})
	if res := e.svc.SyncGuest(ctx, base, guest()); res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	g := guest()
	g.Name, g.OldName = "app2", "app1"
	if res := e.svc.SyncGuest(ctx, base, g); res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	if vms := objects(e, nbfake.VMs); len(vms) != 1 || vms[0]["name"] != "app2" {
		t.Fatalf("%+v", vms)
	}
	if res := e.svc.DeleteGuest(ctx, base, "lab", false, 5000, "app2"); res.Status != netbox.StatusOK {
		t.Fatalf("%+v", res)
	}
	if vms := objects(e, nbfake.VMs); len(vms) != 1 || vms[0]["status"] != "decommissioning" {
		t.Errorf("%+v", vms)
	}
}

func TestSyncGuestErrorsAndSkips(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	// An address of another interface.
	e.fake.Seed(nbfake.Interfaces, nbfake.Object{"name": "eth0", "virtual_machine": e.fake.Seed(nbfake.VMs, nbfake.Object{"name": "other"})})
	e.fake.Seed(nbfake.IPs, nbfake.Object{"address": "192.0.2.50/24", "assigned_object_type": netbox.TypeVMInterface, "assigned_object_id": int64(1)})
	res := e.svc.SyncGuest(ctx, base, guest())
	if res == nil || res.Status != netbox.StatusFailed || !strings.Contains(res.Error, "192.0.2.50/24 is assigned to another interface") {
		t.Errorf("%+v", res)
	}
	l, _ := e.audit.Query(ctx, audit.Filter{Action: netbox.AuditSync})
	if len(l) != 1 || l[0].Result != audit.ResultFailure {
		t.Errorf("audit: %+v", l)
	}
	e.fake.FailNext("POST", nbfake.Disks, 500)
	g := guest()
	g.Networks = nil
	g.Disks = append(g.Disks, netbox.GuestDisk{Name: "scsi1", SizeMiB: 1})
	if res := e.svc.SyncGuest(ctx, base, g); res.Status != netbox.StatusFailed || !strings.Contains(res.Error, "create the disk scsi1") {
		t.Errorf("%+v", res)
	}

	// Clusters that aren't mapped and disabled write-back are skipped.
	g.Cluster = "prod"
	if res := e.svc.SyncGuest(ctx, base, g); res != nil {
		t.Errorf("unmapped: %+v", res)
	}
	e = newAPIEnv(t, func(c *config.NetBox) { c.WriteBack = false })
	if res := e.svc.SyncGuest(ctx, base, guest()); res != nil || e.svc.Applies("lab") {
		t.Errorf("read-only: %+v", res)
	}
	var none *netbox.Service
	if none.Applies("lab") || none.SyncGuest(ctx, base, guest()) != nil || none.DeleteGuest(ctx, base, "lab", false, 1, "x") != nil {
		t.Error("nil service")
	}
	// on_delete: delete removes VMs without the tag.
	e = newAPIEnv(t, func(c *config.NetBox) { c.OnDelete = config.NetBoxDeleteAlways })
	e.fake.Seed(nbfake.VMs, nbfake.Object{"name": "app1", "cluster": int64(1)})
	if res := e.svc.DeleteGuest(ctx, base, "lab", true, 5000, "app1"); res.Status != netbox.StatusOK || len(objects(e, nbfake.VMs)) != 0 {
		t.Errorf("%+v %+v", res, objects(e, nbfake.VMs))
	}
}
