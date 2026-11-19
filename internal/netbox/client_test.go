// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

const testToken = "nbt_test.secret-test-only"

func newClient(t *testing.T, f *nbfake.Server, token string) *netbox.Client {
	t.Helper()
	hs := httptest.NewServer(f)
	t.Cleanup(hs.Close)
	c, err := netbox.NewClient(config.NetBox{URL: hs.URL + "/", Insecure: true, Token: config.NewSecret(token)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthHeader(t *testing.T) {
	if h := netbox.AuthHeader("nbt_abc.def"); h != "Bearer nbt_abc.def" {
		t.Error(h)
	}
	if h := netbox.AuthHeader("0123456789abcdef0123456789abcdef01234567"); h != "Token 0123456789abcdef0123456789abcdef01234567" {
		t.Error(h)
	}
}

func TestClientVersionAndErrors(t *testing.T) {
	ctx := context.Background()
	f := nbfake.New(testToken)
	v, err := newClient(t, f, testToken).Version(ctx)
	if err != nil || v != nbfake.Version {
		t.Fatalf("version %q, %v", v, err)
	}
	// v1 tokens use the Token scheme.
	if _, err := newClient(t, nbfake.New("0123456789abcdef"), "0123456789abcdef").Version(ctx); err != nil {
		t.Errorf("v1 token: %v", err)
	}
	_, err = newClient(t, f, "nbt_wrong.token").Version(ctx)
	var ae *netbox.APIError
	if !errors.Is(err, netbox.ErrForbidden) || !errors.As(err, &ae) || ae.Status != http.StatusForbidden ||
		err.Error() != "NetBox: Invalid v2 token" {
		t.Errorf("wrong token: %v", err)
	}

	c := newClient(t, f, testToken)
	f.FailNext(http.MethodGet, nbfake.VLANs, http.StatusInternalServerError)
	if _, err := c.VLANs(ctx, nil); !errors.Is(err, netbox.ErrUnavailable) {
		t.Errorf("500: %v", err)
	}
	if err := c.Delete(ctx, "/ipam/vlans/", 99, ""); !errors.Is(err, netbox.ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	err = c.Create(ctx, "/virtualization/virtual-disks/", map[string]any{"name": "scsi0"}, nil)
	if !errors.As(err, &ae) || ae.Status != http.StatusBadRequest || len(ae.Fields["size"]) != 1 ||
		!strings.Contains(err.Error(), "size: This field is required.") ||
		!strings.Contains(err.Error(), "virtual_machine: This field is required.") {
		t.Errorf("validation: %v", err)
	}

	hs := httptest.NewServer(f)
	hs.Close()
	down, _ := netbox.NewClient(config.NetBox{URL: hs.URL, Insecure: true, Token: config.NewSecret(testToken)})
	if _, err := down.Version(ctx); !errors.Is(err, netbox.ErrUnavailable) {
		t.Errorf("unreachable: %v", err)
	}
	if _, err := netbox.NewClient(config.NetBox{URL: "https://nb.example.com", TLS: config.TLSClient{CAFile: "/nonexistent"}}); err == nil {
		t.Error("missing CA file accepted")
	}
}

func TestClientPagination(t *testing.T) {
	f := nbfake.New(testToken)
	for i := 1; i <= 1100; i++ {
		f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(i%4000 + 1), "name": "v" + strconv.Itoa(i)})
	}
	c := newClient(t, f, testToken)
	vlans, err := c.VLANs(context.Background(), nil)
	if err != nil || len(vlans) != 1100 || vlans[1099].Name != "v1100" {
		t.Fatalf("%d vlans, %v", len(vlans), err)
	}
	vlans, err = c.VLANs(context.Background(), url.Values{"vid": {"8"}})
	if err != nil || len(vlans) != 1 || vlans[0].Name != "v7" {
		t.Errorf("filter: %+v, %v", vlans, err)
	}
}

// TestFakeBehavior checks the NetBox behavior that dcdash depends on (ADR
// 0006), as observed on NetBox 4.7.
func TestFakeBehavior(t *testing.T) {
	ctx := context.Background()
	f := nbfake.New(testToken)
	tag := f.Seed(nbfake.Tags, nbfake.Object{"name": "dcdash", "slug": "dcdash"})
	cl := f.Seed(nbfake.Clusters, nbfake.Object{"name": "Lab"})
	vl := f.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(172), "name": "kunden"})
	pf := f.Seed(nbfake.Prefixes, nbfake.Object{"prefix": "192.0.2.0/30", "vlan": vl})
	c := newClient(t, f, testToken)

	var vm netbox.VirtualMachine
	if err := c.Create(ctx, "/virtualization/virtual-machines/", map[string]any{
		"name": "app1", "cluster": cl, "vcpus": 2, "memory": 2048, "tags": []any{map[string]any{"slug": "dcdash"}},
		"custom_fields": map[string]any{"proxmox_vmid": 1001}, "changelog_message": "dcdash: alice",
	}, &vm); err != nil {
		t.Fatal(err)
	}
	if vm.Status.Value != "active" || !vm.HasTag("dcdash") || vm.Cluster.Name != "Lab" || *vm.Memory != 2048 || vm.Disk != nil {
		t.Errorf("vm: %+v", vm)
	}
	if err := c.Create(ctx, "/virtualization/virtual-machines/", map[string]any{"name": "app1", "cluster": cl}, nil); err == nil ||
		!strings.Contains(err.Error(), "unique per cluster") {
		t.Errorf("duplicate VM: %v", err)
	}
	found, err := c.FindVM(ctx, url.Values{"cluster_id": {strconv.FormatInt(cl, 10)}, "cf_proxmox_vmid": {"1001"}})
	if err != nil || found.ID != vm.ID {
		t.Errorf("find by VMID: %+v %v", found, err)
	}
	if err := c.Create(ctx, "/virtualization/virtual-disks/", map[string]any{"virtual_machine": vm.ID, "name": "scsi0", "size": 20480}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, "/virtualization/virtual-machines/", vm.ID, map[string]any{"disk": 1}, nil); err == nil {
		t.Error("disk field changed although the VM has virtual disks")
	}
	if err := c.Update(ctx, "/virtualization/virtual-machines/", vm.ID, map[string]any{"vcpus": 4}, &vm); err != nil || *vm.Disk != 20480 {
		t.Errorf("disk sum: %+v %v", vm.Disk, err)
	}

	var ifc netbox.Interface
	if err := c.Create(ctx, "/virtualization/interfaces/", map[string]any{"virtual_machine": vm.ID, "name": "net0", "mode": "access", "untagged_vlan": vl}, &ifc); err != nil {
		t.Fatal(err)
	}
	var mac netbox.MACAddress
	if err := c.Create(ctx, "/dcim/mac-addresses/", map[string]any{"mac_address": "bc:24:11:00:00:01",
		"assigned_object_type": netbox.TypeVMInterface, "assigned_object_id": ifc.ID}, &mac); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, "/virtualization/interfaces/", ifc.ID, map[string]any{"primary_mac_address": mac.ID}, &ifc); err != nil ||
		ifc.PrimaryMAC.MACAddress != "BC:24:11:00:00:01" || ifc.UntaggedVLAN.VID != 172 || ifc.Mode.Value != "access" {
		t.Errorf("interface: %+v %v", ifc, err)
	}
	ifcs, err := c.Interfaces(ctx, url.Values{"mac_address": {"bc:24:11:00:00:01"}})
	if err != nil || len(ifcs) != 1 || ifcs[0].VirtualMachine.Name != "app1" {
		t.Errorf("by MAC: %+v %v", ifcs, err)
	}
	var ip netbox.IPAddress
	if err := c.Create(ctx, "/ipam/ip-addresses/", map[string]any{"address": "192.0.2.1/30", "dns_name": "app1.example.org",
		"assigned_object_type": netbox.TypeVMInterface, "assigned_object_id": ifc.ID, "tags": []any{tag}}, &ip); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, "/ipam/ip-addresses/", map[string]any{"address": "192.0.2.1/24"}, nil); err == nil ||
		!strings.Contains(err.Error(), "Duplicate IP address found in global table") {
		t.Errorf("duplicate IP: %v", err)
	}
	if err := c.Update(ctx, "/virtualization/virtual-machines/", vm.ID, map[string]any{"primary_ip4": ip.ID}, &vm); err != nil ||
		vm.PrimaryIP4.Address != "192.0.2.1/30" {
		t.Errorf("primary IP: %+v %v", vm.PrimaryIP4, err)
	}
	free, err := c.AvailableIPs(ctx, pf, 5)
	if err != nil || len(free) != 1 || free[0].Address != "192.0.2.2/30" {
		t.Errorf("available: %+v %v", free, err)
	}

	// Deleting the interface deletes its address and MAC address.
	if err := c.Delete(ctx, "/virtualization/interfaces/", ifc.ID, ""); err != nil {
		t.Fatal(err)
	}
	if len(f.Objects(nbfake.IPs)) != 0 || len(f.Objects(nbfake.MACs)) != 0 {
		t.Errorf("cascade: %v %v", f.Objects(nbfake.IPs), f.Objects(nbfake.MACs))
	}
	if err := c.Delete(ctx, "/virtualization/virtual-machines/", vm.ID, "dcdash: delete"); err != nil {
		t.Fatal(err)
	}
	if len(f.Objects(nbfake.Disks)) != 0 {
		t.Error("disks not deleted with the VM")
	}
	ch := f.Changes()
	if ch[0].Message != "dcdash: alice" || ch[len(ch)-1].Action != "delete" || ch[len(ch)-1].Message != "dcdash: delete" {
		t.Errorf("change log: %+v", ch)
	}
}
