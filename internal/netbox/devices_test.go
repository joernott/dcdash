// SPDX-License-Identifier: GPL-3.0-or-later

package netbox_test

import (
	"context"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func init() {
	testUsers["devicer"] = []string{"netbox:netbox:sync:write", "proxmox:lab:*:read", "switch:*:*:read"}
	testUsers["nodeonly"] = []string{"netbox:netbox:sync:write", "proxmox:lab:node/pve1:read"}
}

type deviceSources struct {
	nodes    []netbox.Node
	switches []netbox.Switch
}

func (d *deviceSources) NetBoxNodes(context.Context, string) ([]netbox.Node, error) {
	return d.nodes, nil
}

func (d *deviceSources) NetBoxSwitches(context.Context) []netbox.Switch { return d.switches }

func TestCompareDevices(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, nil)
	src := &deviceSources{
		nodes: []netbox.Node{{Name: "pve1", IP: "192.0.2.11"}, {Name: "pve2", IP: "192.0.2.12"}},
		switches: []netbox.Switch{
			{Name: "sw1", Device: "sw1", Model: "6000 24G 4SFP Swch", PartNumber: "R8N88A", Serial: "CN1"},
			{Name: "sw2", Device: "sw2", Error: "switch not reachable"},
		},
	}
	e.svc.SetDeviceSources(src, src)

	// pve1 is in another cluster and has its address on an interface, without primary IP.
	other := e.fake.Seed(nbfake.Clusters, nbfake.Object{"name": "Other"})
	pve1 := e.fake.Seed(nbfake.Devices, nbfake.Object{"name": "pve1", "cluster": other})
	ifc := e.fake.Seed(nbfake.DevIfaces, nbfake.Object{"name": "vmbr0", "device": pve1})
	ip := e.fake.Seed(nbfake.IPs, nbfake.Object{"address": "192.0.2.11/24", "assigned_object_type": "dcim.interface", "assigned_object_id": ifc})
	e.fake.Seed(nbfake.Devices, nbfake.Object{"name": "sw1",
		"device_type": map[string]any{"id": 1, "model": "Aruba 6000 24G", "part_number": "R8N88A"}})

	var cmp netbox.DeviceComparison
	if st, code := e.do(t, "nodeonly", "GET", "/api/v1/netbox/compare/nodes/lab", nil, &cmp); st != 200 {
		t.Fatal(st, code)
	}
	// Only the nodes the user may read.
	if len(cmp.Devices) != 1 {
		t.Fatalf("%+v", cmp)
	}
	d := cmp.Devices[0]
	if d.Kind != netbox.KindNode || d.Name != "pve1" || d.State != netbox.StateDiffers || !d.Updatable || len(d.Differences) != 2 ||
		d.Differences[0] != (netbox.Difference{Field: "cluster", Actual: "Lab", NetBox: "Other"}) ||
		d.Differences[1] != (netbox.Difference{Field: "primaryIp", Actual: "192.0.2.11", NetBox: ""}) {
		t.Errorf("pve1: %+v", d)
	}

	var wb netbox.WriteBack
	if st, code := e.do(t, "nodeonly", "POST", "/api/v1/netbox/compare/nodes/lab/pve1", nil, &wb); st != 200 || wb.NetBox.Status != netbox.StatusOK {
		t.Fatal(st, code, wb)
	}
	for _, o := range e.fake.Objects(nbfake.Devices) {
		if o["name"] == "pve1" && (o["cluster"] != int64(1) || o["primary_ip4"] != ip) {
			t.Errorf("pve1 in NetBox: %+v", o)
		}
	}
	e.do(t, "devicer", "GET", "/api/v1/netbox/compare/nodes/lab", nil, &cmp)
	if len(cmp.Devices) != 2 || cmp.Devices[0].State != netbox.StateOK || cmp.Devices[1].State != netbox.StateMissing {
		t.Errorf("after update: %+v", cmp.Devices)
	}
	// Devices are not created, hidden nodes are unknown.
	if st, code := e.do(t, "devicer", "POST", "/api/v1/netbox/compare/nodes/lab/pve2", nil, nil); st != 409 || code != "device_missing" {
		t.Errorf("missing device: %d %s", st, code)
	}
	if st, _ := e.do(t, "nodeonly", "POST", "/api/v1/netbox/compare/nodes/lab/pve2", nil, nil); st != 404 {
		t.Errorf("hidden node: %d", st)
	}
	if st, _ := e.do(t, "looker", "POST", "/api/v1/netbox/compare/nodes/lab/pve1", nil, nil); st != 403 {
		t.Errorf("read only: %d", st)
	}
	if st, _ := e.do(t, "devicer", "GET", "/api/v1/netbox/compare/nodes/prod", nil, nil); st != 404 {
		t.Errorf("unmapped: %d", st)
	}

	// Switches: the model matches by part number, the serial number differs.
	if st, code := e.do(t, "devicer", "GET", "/api/v1/netbox/compare/switches", nil, &cmp); st != 200 || len(cmp.Devices) != 2 {
		t.Fatal(st, code, cmp)
	}
	sw1, sw2 := cmp.Devices[0], cmp.Devices[1]
	if sw1.State != netbox.StateDiffers || !sw1.Updatable || len(sw1.Differences) != 1 ||
		sw1.Differences[0] != (netbox.Difference{Field: "serial", Actual: "CN1", NetBox: ""}) {
		t.Errorf("sw1: %+v", sw1)
	}
	if sw2.State != netbox.StateMissing {
		t.Errorf("sw2: %+v", sw2)
	}
	if st, code := e.do(t, "devicer", "POST", "/api/v1/netbox/compare/switches/sw1", nil, &wb); st != 200 || wb.NetBox.Status != netbox.StatusOK {
		t.Fatal(st, code, wb)
	}
	e.do(t, "devicer", "GET", "/api/v1/netbox/compare/switches", nil, &cmp)
	if cmp.Devices[0].State != netbox.StateOK {
		t.Errorf("sw1 after update: %+v", cmp.Devices[0])
	}
	// Without a switch permission, no switches.
	if e.do(t, "looker", "GET", "/api/v1/netbox/compare/switches", nil, &cmp); len(cmp.Devices) != 0 {
		t.Errorf("looker: %+v", cmp.Devices)
	}
	if st, _ := e.do(t, "looker", "POST", "/api/v1/netbox/compare/switches/sw1", nil, nil); st != 403 {
		t.Errorf("read only switch: %d", st)
	}

	l, _ := e.audit.Query(ctx, audit.Filter{Action: netbox.AuditApply})
	targets := map[string]bool{}
	for _, x := range l {
		targets[x.Target] = true
	}
	if len(l) != 2 || !targets["node:lab/pve1"] || !targets["switch:sw1"] {
		t.Errorf("audit: %+v", l)
	}
}

func TestCompareSwitchUnavailable(t *testing.T) {
	e := newAPIEnv(t, nil)
	src := &deviceSources{switches: []netbox.Switch{{Name: "sw1", Device: "core", Error: "switch not reachable"}}}
	e.svc.SetDeviceSources(nil, src)
	e.fake.Seed(nbfake.Devices, nbfake.Object{"name": "core", "serial": "X"})
	var cmp netbox.DeviceComparison
	e.do(t, "devicer", "GET", "/api/v1/netbox/compare/switches", nil, &cmp)
	if len(cmp.Devices) != 1 || cmp.Devices[0].State != netbox.StateUnavailable || cmp.Devices[0].Device != "core" ||
		cmp.Devices[0].Error != "switch not reachable" || cmp.Devices[0].Updatable {
		t.Errorf("%+v", cmp.Devices)
	}
	// Without the Proxmox module, the nodes can't be compared.
	if st, _ := e.do(t, "devicer", "GET", "/api/v1/netbox/compare/nodes/lab", nil, nil); st != 502 {
		t.Errorf("nodes without Proxmox: %d", st)
	}
}
