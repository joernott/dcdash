// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func TestNetBoxGuest(t *testing.T) {
	vm := netboxGuest("lab", TypeQEMU, 5000, map[string]any{
		"name": "app1", "sockets": float64(2), "cores": float64(4), "memory": "4096",
		"scsi0": "local-lvm:vm-5000-disk-0,size=20G", "virtio1": "ceph:vm-5000-disk-1,size=512M",
		"ide2": "local-lvm:vm-5000-cloudinit,media=cdrom",
		"net1": "virtio=bc:24:11:00:00:02,bridge=vmbr0", "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0,tag=100",
		"net10":     "e1000=BC:24:11:00:00:0A,bridge=vmbr1",
		"ipconfig0": "ip=192.0.2.50/24,gw=192.0.2.1,ip6=2001:db8::50/64", "ipconfig1": "ip=dhcp,ip6=auto",
	})
	want := netbox.Guest{Cluster: "lab", VMID: 5000, Name: "app1", VCPUs: 8, MemoryMiB: 4096,
		Disks: []netbox.GuestDisk{{Name: "scsi0", SizeMiB: 20480}, {Name: "virtio1", SizeMiB: 512}},
		Networks: []netbox.GuestNetwork{
			{Name: "net0", MAC: "BC:24:11:00:00:01", VLAN: 100, Addresses: []string{"192.0.2.50/24", "2001:db8::50/64"}},
			{Name: "net1", MAC: "BC:24:11:00:00:02"},
			{Name: "net10", MAC: "BC:24:11:00:00:0A"},
		}}
	if !reflect.DeepEqual(vm, want) {
		t.Errorf("VM:\n got %+v\nwant %+v", vm, want)
	}

	ct := netboxGuest("lab", TypeLXC, 5001, map[string]any{
		"hostname": "ct1", "cores": float64(2), "rootfs": "local-lvm:vm-5001-disk-0,size=8G",
		"mp0":  "local-lvm:vm-5001-disk-1,mp=/data,size=16G",
		"net0": "name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:03,ip=192.0.2.51/24,gw=192.0.2.1,tag=150,type=veth",
	})
	want = netbox.Guest{Cluster: "lab", VMID: 5001, Name: "ct1", Container: true, VCPUs: 2, MemoryMiB: 512,
		Disks:    []netbox.GuestDisk{{Name: "mp0", SizeMiB: 16384}, {Name: "rootfs", SizeMiB: 8192}},
		Networks: []netbox.GuestNetwork{{Name: "eth0", MAC: "BC:24:11:00:00:03", VLAN: 150, Addresses: []string{"192.0.2.51/24"}}}}
	if !reflect.DeepEqual(ct, want) {
		t.Errorf("container:\n got %+v\nwant %+v", ct, want)
	}
}

// withNetBox connects a NetBox fake with the cluster "Lab" (mapped to lab),
// the dcdash tag and VLAN 100.
func withNetBox(t *testing.T, e *provEnv) *nbfake.Server {
	t.Helper()
	f := nbfake.New("nbt_t.test-only")
	f.Seed(nbfake.Tags, nbfake.Object{"name": "dcdash", "slug": "dcdash"})
	f.Seed(nbfake.Clusters, nbfake.Object{"name": "Lab"})
	f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(100), "name": "servers"})
	hs := httptest.NewServer(f)
	t.Cleanup(hs.Close)
	nb, err := netbox.NewService(config.NetBox{Name: "netbox", URL: hs.URL, Insecure: true, Token: config.NewSecret("nbt_t.test-only"),
		WriteBack: true, Tag: "dcdash", OnDelete: config.NetBoxDeleteTagged, Clusters: map[string]string{"lab": "Lab"}}, e.audit, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.SetNetBox(nb)
	return f
}

func TestNetBoxWriteBack(t *testing.T) {
	e := newProvEnv(t)
	f := withNetBox(t, e)
	req := cloneRequest()
	req.Start = false
	j := e.create(t, "creator", req)
	if j.State != JobDone || j.Steps[len(j.Steps)-1].Name != "netbox" || j.Steps[len(j.Steps)-1].State != StepDone {
		t.Fatalf("job: %+v", j)
	}
	vms := f.Objects(nbfake.VMs)
	if len(vms) != 1 || vms[0]["name"] != "app1" || vms[0]["memory"] != float64(2048) {
		t.Fatalf("NetBox: %+v", vms)
	}
	ips := f.Objects(nbfake.IPs)
	if len(ips) != 1 || ips[0]["address"] != "10.0.0.5/24" {
		t.Errorf("addresses: %+v", ips)
	}
	if ifc := f.Objects(nbfake.Interfaces); len(ifc) != 1 || ifc[0]["untagged_vlan"] != float64(1) && ifc[0]["untagged_vlan"] != int64(1) {
		t.Errorf("interfaces: %+v", ifc)
	}
	l := e.entries(t, netbox.AuditSync)
	if len(l) != 1 || l[0].Actor != "creator" || l[0].Target != "lab/qemu/5000" {
		t.Errorf("audit: %+v", l)
	}

	// A new disk is written back with the response.
	path := "/lab/guests/qemu/5000/hardware"
	var hw Hardware
	if st, msg := e.do(t, "admin", http.MethodGet, path, nil, &hw); st != http.StatusOK {
		t.Fatal(st, msg)
	}
	var res ConfigResult
	if st, msg := e.do(t, "admin", http.MethodPost, path+"/disks",
		DiskRequest{Digest: hw.Digest, Bus: "scsi", Storage: "local-lvm", SizeGiB: 8}, &res); st != http.StatusOK {
		t.Fatal(st, msg)
	}
	if res.NetBox == nil || res.NetBox.Status != netbox.StatusOK || len(f.Objects(nbfake.Disks)) != 2 {
		t.Errorf("disk: %+v, %+v", res.NetBox, f.Objects(nbfake.Disks))
	}

	// Deleting the guest deletes the VM (it has the dcdash tag).
	var del DeleteResult
	if st, msg := e.do(t, "admin", http.MethodDelete, "/lab/guests/qemu/5000", DeleteRequest{Confirm: "app1"}, &del); st != http.StatusAccepted {
		t.Fatal(st, msg)
	}
	if del.NetBox == nil || del.NetBox.Status != netbox.StatusOK || len(f.Objects(nbfake.VMs)) != 0 {
		t.Errorf("delete: %+v, %+v", del.NetBox, f.Objects(nbfake.VMs))
	}
}

func TestNetBoxFailureDoesNotFailJob(t *testing.T) {
	e := newProvEnv(t)
	f := withNetBox(t, e)
	f.FailNext(http.MethodPost, nbfake.VMs, http.StatusInternalServerError)
	req := cloneRequest()
	req.Start = false
	j := e.create(t, "creator", req)
	last := j.Steps[len(j.Steps)-1]
	if j.State != JobDone || last.Name != "netbox" || last.State != StepFailed || last.Error == "" {
		t.Fatalf("job: %+v", j)
	}
	if _, ok := e.fake.Guest("qemu", 5000); !ok {
		t.Error("guest missing")
	}
	l := e.entries(t, netbox.AuditSync)
	if len(l) != 1 || l[0].Result != "failure" {
		t.Errorf("audit: %+v", l)
	}
	// Without NetBox there is no step.
	e = newProvEnv(t)
	j = e.create(t, "creator", req)
	if j.Steps[len(j.Steps)-1].Name == "netbox" {
		t.Errorf("steps: %+v", j.Steps)
	}
}

func TestNetBoxNodes(t *testing.T) {
	_, pve := fakeServer(t)
	svc := NewService([]*Cluster{newCluster(t, endpointFor(pve))}, nil, nil, zerolog.Nop())
	nodes, err := svc.NetBoxNodes(t.Context(), "lab")
	want := []netbox.Node{{Name: "pve1", IP: "192.0.2.11"}, {Name: "pve2", IP: "192.0.2.12"}}
	if err != nil || !reflect.DeepEqual(nodes, want) {
		t.Errorf("got %+v, %v", nodes, err)
	}
	if _, err := svc.NetBoxNodes(t.Context(), "prod"); err == nil {
		t.Error("unknown cluster accepted")
	}
}
