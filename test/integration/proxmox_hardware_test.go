// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox"
)

// Changing a VM on a real cluster (task 11.12) through dcdash's API: options, disks and
// network devices of an empty VM that is never started. It runs when
// DCDASH_IT_PVE_PROVISION_VMIDS, _STORAGE and _BRIDGE are set (optionally _VLAN; see
// proxmox_provision_test.go). The VM is created with the Proxmox API and deleted at the end,
// also when the test fails.
func TestProxmoxHardware(t *testing.T) {
	vmids := os.Getenv("DCDASH_IT_PVE_PROVISION_VMIDS")
	if vmids == "" {
		t.Skip("DCDASH_IT_PVE_PROVISION_VMIDS not set")
	}
	lo, hi, _ := strings.Cut(vmids, "-")
	minID, _ := strconv.Atoi(lo)
	maxID, _ := strconv.Atoi(hi)
	if minID < 100 || maxID < minID {
		t.Fatalf("invalid DCDASH_IT_PVE_PROVISION_VMIDS %q", vmids)
	}
	storage := requireEnv(t, "DCDASH_IT_PVE_PROVISION_STORAGE")
	bridge := requireEnv(t, "DCDASH_IT_PVE_PROVISION_BRIDGE")
	vlan, _ := strconv.Atoi(os.Getenv("DCDASH_IT_PVE_PROVISION_VLAN"))
	ctx := context.Background()
	c := pveCluster(t, false)

	res, err := c.Resources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	node := ""
	for _, r := range res {
		if r.Type == "node" && r.Status == "online" {
			node = r.Node
			break
		}
	}
	vmid := 0
	for id := minID; id <= maxID && vmid == 0; id++ {
		if free, err := c.IDFree(ctx, id); err == nil && free {
			vmid = id
		}
	}
	if node == "" || vmid == 0 {
		t.Fatalf("no online node (%q) or no free ID in %s", node, vmids)
	}
	upid, err := c.CreateGuest(ctx, node, proxmox.TypeQEMU, url.Values{
		"vmid": {strconv.Itoa(vmid)}, "name": {"dcdash-it-hardware"}, "memory": {"512"}, "cores": {"1"},
		"scsihw": {"virtio-scsi-single"}, "scsi0": {storage + ":1"}, "net0": {"virtio,bridge=" + bridge},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		upid, err := c.DeleteGuest(context.Background(), node, proxmox.TypeQEMU, vmid, true)
		if err != nil {
			t.Errorf("cleanup qemu/%d: %v", vmid, err)
			return
		}
		t.Logf("cleanup qemu/%d: %s", vmid, waitTask(t, c, upid))
	})
	if st := waitTask(t, c, upid); st != "OK" {
		t.Fatalf("create qemu/%d: %s", vmid, st)
	}
	t.Logf("created qemu/%d on %s", vmid, node)

	prov := config.ProxmoxProvisioning{
		VMIDs: config.IDRanges{{Min: minID, Max: maxID}}, Storages: []string{storage}, Bridges: []string{bridge},
		Limits: config.ProvisioningLimits{Cores: 4, Memory: 4 * config.GiB, Disk: 8 * config.GiB},
	}
	if vlan != 0 {
		prov.VLANs = config.IDRanges{{Min: vlan, Max: vlan}}
	}
	api := startProvAPI(t, prov)
	base := "/guests/qemu/" + strconv.Itoa(vmid) + "/hardware"
	get := func() proxmox.Hardware {
		t.Helper()
		var hw proxmox.Hardware
		if st := api.call(http.MethodGet, base, nil, &hw); st != http.StatusOK {
			t.Fatalf("hardware: %d", st)
		}
		return hw
	}
	change := func(what, method, path string, body any) proxmox.ConfigResult {
		t.Helper()
		var r proxmox.ConfigResult
		if st := api.call(method, base+path, body, &r); st != http.StatusOK || !r.Done {
			t.Fatalf("%s: %d %+v", what, st, r)
		}
		return r
	}
	option := func(hw proxmox.Hardware, key string) proxmox.OptionSetting {
		for _, o := range hw.Options {
			if o.Key == key {
				return o
			}
		}
		return proxmox.OptionSetting{}
	}
	disk := func(hw proxmox.Hardware, key string) (proxmox.HardwareDisk, bool) {
		for _, d := range hw.Disks {
			if d.Key == key {
				return d, true
			}
		}
		return proxmox.HardwareDisk{}, false
	}
	nic := func(hw proxmox.Hardware, key string) (proxmox.NetworkDevice, bool) {
		for _, n := range hw.Networks {
			if n.Key == key {
				return n, true
			}
		}
		return proxmox.NetworkDevice{}, false
	}

	hw := get()
	if d, ok := disk(hw, "scsi0"); !ok || d.Size != "1G" || hw.Running || hw.Digest == "" {
		t.Fatalf("initial hardware: %+v", hw)
	}
	if n, ok := nic(hw, "net0"); !ok || !strings.HasPrefix(n.MAC, "BC:24:11:") {
		t.Errorf("net0: %+v", n)
	}

	// Options; the configuration of a stopped VM changes at once.
	change("options", http.MethodPost, "/options", proxmox.OptionsRequest{Digest: hw.Digest, Set: map[string]string{
		"cores": "2", "memory": "1024", "agent": "enabled=1", "boot": "order=scsi0;net0", "description": "dcdash integration test",
	}})
	old := hw.Digest
	hw = get()
	if option(hw, "cores").Value != "2" || option(hw, "memory").Value != "1024" || option(hw, "agent").Value != "enabled=1" {
		t.Errorf("options: %+v", hw.Options)
	}
	// The digest of the old configuration is refused.
	var r proxmox.ConfigResult
	if st := api.call(http.MethodPost, base+"/options", proxmox.OptionsRequest{Digest: old, Set: map[string]string{"onboot": "1"}}, &r); st != http.StatusConflict {
		t.Errorf("stale digest: %d, want 409", st)
	}
	change("remove option", http.MethodPost, "/options", proxmox.OptionsRequest{Digest: hw.Digest, Delete: []string{"description"}})

	// Disks: create, grow, detach, attach to another bus, delete.
	hw = get()
	if k := change("create disk", http.MethodPost, "/disks", proxmox.DiskRequest{Digest: hw.Digest, Bus: "scsi", Storage: storage, SizeGiB: 1, Discard: true}).Key; k != "scsi1" {
		t.Fatalf("new disk: %s", k)
	}
	hw = get()
	change("grow disk", http.MethodPut, "/disks/scsi1", proxmox.DiskUpdate{Digest: hw.Digest, SizeGiB: 2})
	hw = get()
	if d, _ := disk(hw, "scsi1"); d.Size != "2G" || !d.Discard {
		t.Errorf("scsi1: %+v", d)
	}
	change("detach", http.MethodPost, "/disks/scsi1/detach", proxmox.DiskAction{Digest: hw.Digest})
	hw = get()
	u, ok := disk(hw, "unused0")
	if !ok {
		t.Fatalf("no unused disk after detach: %+v", hw.Disks)
	}
	if k := change("attach", http.MethodPost, "/disks/unused0/attach", proxmox.DiskAction{Digest: hw.Digest, Bus: "virtio"}).Key; k != "virtio0" {
		t.Fatalf("attached as %s", k)
	}
	hw = get()
	if d, ok := disk(hw, "virtio0"); !ok || d.Volume != u.Volume {
		t.Errorf("virtio0: %+v", hw.Disks)
	}
	change("delete disk", http.MethodDelete, "/disks/virtio0", proxmox.DiskAction{Digest: hw.Digest, Confirm: "virtio0"})
	hw = get()
	if len(hw.Disks) != 1 {
		t.Errorf("disks after delete: %+v", hw.Disks)
	}

	// Network devices: add, change (the MAC stays), remove.
	if k := change("add nic", http.MethodPost, "/networks", proxmox.NetworkRequest{Digest: hw.Digest, Model: "virtio", Bridge: bridge, VLAN: vlan, Firewall: true}).Key; k != "net1" {
		t.Fatalf("new nic: %s", k)
	}
	hw = get()
	n1, _ := nic(hw, "net1")
	if !strings.HasPrefix(n1.MAC, "BC:24:11:") || n1.VLAN != vlan || !n1.Firewall {
		t.Errorf("net1: %+v", n1)
	}
	change("change nic", http.MethodPut, "/networks/net1", proxmox.NetworkRequest{Digest: hw.Digest, Model: "e1000", Bridge: bridge, LinkDown: true})
	hw = get()
	if n, _ := nic(hw, "net1"); n.MAC != n1.MAC || n.Model != "e1000" || !n.LinkDown || n.Firewall {
		t.Errorf("net1 after change: %+v (was %+v)", n, n1)
	}
	change("remove nic", http.MethodDelete, "/networks/net1", proxmox.NetworkAction{Digest: hw.Digest})
	if _, ok := nic(get(), "net1"); ok {
		t.Error("net1 still there")
	}
}
