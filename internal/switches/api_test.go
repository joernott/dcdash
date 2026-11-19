// SPDX-License-Identifier: GPL-3.0-or-later

package switches_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
	"github.com/joernott/dcdash/internal/switches"
)

func ptr[T any](v T) *T { return &v }

func TestPorts(t *testing.T) {
	e := newAPIEnv(t)
	var l switches.PortList
	if st, code := e.do(t, "viewer", "GET", "/api/v1/switches/sw1/ports", nil, &l); st != 200 {
		t.Fatal(st, code)
	}
	if len(l.Ports) != 10 || l.Ports[0].Name != "1/1/1" || !l.Ports[0].Uplink || l.Ports[1].Uplink || l.Ports[0].CanOperate ||
		len(l.VLANs) != 3 || l.VLANs[1].Name != "servers" || l.VLANs[1].CanWrite || l.Ports[0].NetBox != nil {
		t.Errorf("%+v", l)
	}
	if st, _ := e.do(t, "netops", "GET", "/api/v1/switches/sw1/ports", nil, &l); st != 200 ||
		l.Ports[4].Name != "1/1/5" || !l.Ports[4].CanWrite || l.Ports[3].CanWrite || !l.VLANs[0].CanWrite == (l.VLANs[0].ID == 4000) {
		t.Errorf("netops: %+v", l.Ports[3:5])
	}
	// Only the ports with read permission are listed.
	if st, _ := e.do(t, "operator", "GET", "/api/v1/switches/sw1/ports", nil, &l); st != 200 || len(l.Ports) != 10 || !l.Ports[0].CanOperate {
		t.Errorf("operator: %d", st)
	}
	for _, c := range []struct{ user, path string }{{"other", "/api/v1/switches/sw1/ports"}, {"viewer", "/api/v1/switches/sw2/ports"},
		{"admin", "/api/v1/switches/nope/ports"}} {
		if st, _ := e.do(t, c.user, "GET", c.path, nil, nil); st != http.StatusNotFound {
			t.Errorf("%s %s: %d", c.user, c.path, st)
		}
	}
	if st, code := e.do(t, "admin", "GET", "/api/v1/switches/sw2/ports", nil, nil); st != http.StatusBadGateway || code != "upstream_unavailable" {
		t.Errorf("sw2: %d %s", st, code)
	}
}

func TestUpdatePort(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	var res switches.PortResult
	body := switches.PortUpdate{Description: ptr("esx1"), Mode: ptr("trunk"), VLAN: ptr(10), TrunkVLANs: &[]int{10, 20}}
	if st, code := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/5", body, &res); st != 200 {
		t.Fatal(st, code)
	}
	p := res.Port
	if !res.Saved || p.Mode != "trunk" || p.VLAN != 10 || len(p.TrunkVLANs) != 2 || p.Description != "esx1" || !p.Uplink || e.sw1.Saves != 1 {
		t.Errorf("%+v", res)
	}
	l, _ := e.audit.Query(ctx, audit.Filter{Action: switches.AuditPortUpdate})
	if len(l) != 1 || l[0].Target != "sw1/1/1/5" || l[0].Result != audit.ResultSuccess || l[0].Details["saved"] != true ||
		l[0].Details["before"].(map[string]any)["vlan"] != float64(10) || l[0].Details["after"].(map[string]any)["mode"] != "trunk" {
		t.Errorf("audit: %+v", l)
	}

	// Enabling and disabling needs operate; other changes write.
	if st, _ := e.do(t, "operator", "PUT", "/api/v1/switches/sw1/ports/1/1/3", switches.PortUpdate{Enabled: ptr(false)}, &res); st != 200 ||
		res.Port.Enabled || res.Port.CanWrite {
		t.Errorf("operator disable: %d %+v", st, res.Port)
	}
	if st, _ := e.do(t, "operator", "PUT", "/api/v1/switches/sw1/ports/1/1/3", switches.PortUpdate{Description: ptr("x")}, nil); st != http.StatusForbidden {
		t.Errorf("operator description: %d", st)
	}
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/6", switches.PortUpdate{Description: ptr("x")}, nil); st != http.StatusForbidden {
		t.Errorf("netops other port: %d", st)
	}
	for _, c := range []struct {
		body switches.PortUpdate
		want string
	}{
		{switches.PortUpdate{VLAN: ptr(99)}, "invalid_port"},
		{switches.PortUpdate{Mode: ptr("hybrid")}, "invalid_port"},
		{switches.PortUpdate{Mode: ptr("access"), TrunkVLANs: &[]int{10}}, "invalid_port"},
		{switches.PortUpdate{Description: ptr("ä")}, "invalid_port"},
		{switches.PortUpdate{Description: ptr(fmt.Sprintf("%065d", 0))}, "invalid_port"},
	} {
		if st, code := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/5", c.body, nil); st != 400 || code != c.want {
			t.Errorf("%+v: %d %s", c.body, st, code)
		}
	}
	if st, _ := e.do(t, "admin", "PUT", "/api/v1/switches/sw1/ports/9/9/9", switches.PortUpdate{Enabled: ptr(true)}, nil); st != http.StatusNotFound {
		t.Errorf("unknown port: %d", st)
	}

	// A failed save is reported, the change stays.
	e.sw1.FailNext("PUT", "/fullconfigs/startup-config", 500)
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/5", switches.PortUpdate{Description: ptr("esx2")}, &res); st != 200 ||
		res.Saved || res.SaveError == "" || res.Port.Description != "esx2" {
		t.Errorf("save failure: %d %+v", st, res)
	}
	// A rejected change is audited as a failure.
	e.sw1.FailNext("PUT", "/system/interfaces/1/1/5", 400)
	if st, code := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/5", switches.PortUpdate{Description: ptr("esx3")}, nil); st != 422 || code != "rejected" {
		t.Errorf("rejected: %d %s", st, code)
	}
	l, _ = e.audit.Query(ctx, audit.Filter{Action: switches.AuditPortUpdate})
	if len(l) != 4 || l[0].Result != audit.ResultFailure {
		t.Errorf("audit: %+v", l)
	}
}

func TestVLANs(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t)
	var res switches.VLANResult
	if st, code := e.do(t, "netops", "POST", "/api/v1/switches/sw1/vlans", switches.VLANRequest{ID: 4001, Name: "lab net"}, &res); st != 201 ||
		!res.Saved || res.VLAN.Name != "lab net" {
		t.Fatal(st, code, res)
	}
	for _, c := range []struct {
		user string
		body switches.VLANRequest
		st   int
		code string
	}{
		{"netops", switches.VLANRequest{ID: 30, Name: "x"}, 403, "forbidden"},
		{"netops", switches.VLANRequest{ID: 4001, Name: "again"}, 409, "exists"},
		{"netops", switches.VLANRequest{ID: 4002, Name: "a;b"}, 400, "invalid_vlan"},
		{"admin", switches.VLANRequest{ID: 1, Name: "x"}, 400, "invalid_vlan"},
		{"viewer", switches.VLANRequest{ID: 4003, Name: "x"}, 403, "forbidden"},
	} {
		if st, code := e.do(t, c.user, "POST", "/api/v1/switches/sw1/vlans", c.body, nil); st != c.st || code != c.code {
			t.Errorf("%s %+v: %d %s", c.user, c.body, st, code)
		}
	}
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/vlans/4001", switches.VLANRequest{Name: "lab"}, &res); st != 200 || res.VLAN.Name != "lab" {
		t.Errorf("rename: %d %+v", st, res)
	}
	if st, _ := e.do(t, "admin", "PUT", "/api/v1/switches/sw1/vlans/4094", switches.VLANRequest{Name: "lab"}, nil); st != 404 {
		t.Errorf("rename unknown: %d", st)
	}
	// VLANs in use and VLAN 1 are not deleted.
	if st, code := e.do(t, "admin", "DELETE", "/api/v1/switches/sw1/vlans/20", nil, nil); st != 409 || code != "vlan_in_use" {
		t.Errorf("in use: %d %s", st, code)
	}
	if st, code := e.do(t, "admin", "DELETE", "/api/v1/switches/sw1/vlans/1", nil, nil); st != 409 || code != "default_vlan" {
		t.Errorf("default: %d %s", st, code)
	}
	var list switches.VLANList
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/switches/sw1/vlans", nil, &list); st != 200 || len(list.VLANs) != 4 ||
		len(list.Usage["20"]) != 1 || list.Usage["20"][0] != "1/1/3" || list.Usage["4001"] != nil || list.CanCreate {
		t.Errorf("list: %+v", list)
	}
	if st, _ := e.do(t, "netops", "DELETE", "/api/v1/switches/sw1/vlans/4001", nil, &res); st != 200 || !res.Saved {
		t.Errorf("delete: %d", st)
	}
	for _, a := range []string{switches.AuditVLANCreate, switches.AuditVLANUpdate, switches.AuditVLANDelete} {
		if l, _ := e.audit.Query(ctx, audit.Filter{Action: a}); len(l) != 1 || l[0].Target != "sw1/vlan/4001" || l[0].Result != audit.ResultSuccess {
			t.Errorf("%s: %+v", a, l)
		}
	}
	if e.sw1.Saves != 3 {
		t.Errorf("saves: %d", e.sw1.Saves)
	}
}

func TestMACSearch(t *testing.T) {
	e := newAPIEnv(t)
	var s switches.MACSearch
	if st, code := e.do(t, "admin", "GET", "/api/v1/switches/macs?q=BC-24-11", nil, &s); st != 200 {
		t.Fatal(st, code)
	}
	// sw2 can't log in; access ports come first.
	if len(s.Results) != 2 || s.Results[0].Port != "1/1/2" || s.Results[0].Uplink || s.Results[0].PortDescription != "app1" ||
		s.Results[1].Port != "1/1/1" || !s.Results[1].Uplink || len(s.Errors) != 1 || s.Errors[0].Switch != "sw2" {
		t.Errorf("%+v", s)
	}
	// Only ports the user may read.
	if st, _ := e.do(t, "netops", "GET", "/api/v1/switches/macs?q=bc2411", nil, &s); st != 200 || len(s.Results) != 2 || len(s.Errors) != 0 {
		t.Errorf("netops: %+v", s)
	}
	if st, code := e.do(t, "admin", "GET", "/api/v1/switches/macs?q=bc2", nil, nil); st != 400 || code != "invalid_query" {
		t.Errorf("short: %d %s", st, code)
	}
	if st, _ := e.do(t, "admin", "GET", "/api/v1/switches/macs?q=xyz1", nil, nil); st != 400 {
		t.Errorf("invalid: %d", st)
	}
	if st, _ := e.do(t, "other", "GET", "/api/v1/switches/macs?q=bc2411", nil, &s); st != 200 || len(s.Results) != 0 {
		t.Errorf("other: %+v", s)
	}
}

func TestNetBoxWriteBack(t *testing.T) {
	ctx := context.Background()
	e := newAPIEnv(t, true)
	dev := e.nb.Seed(nbfake.Devices, nbfake.Object{"name": "sw1-nb"})
	project := e.nb.Seed(nbfake.Tags, nbfake.Object{"name": "Webshop", "slug": "project-webshop"})
	e.nb.Seed(nbfake.DevIfaces, nbfake.Object{"name": "1/1/2", "device": dev, "tags": []int64{project}})
	e.nb.Seed(nbfake.DevIfaces, nbfake.Object{"name": "1/1/5", "device": dev})
	e.nb.Seed(nbfake.VLANs, nbfake.Object{"vid": int64(10), "name": "servers"})

	var l switches.PortList
	if st, _ := e.do(t, "viewer", "GET", "/api/v1/switches/sw1/ports", nil, &l); st != 200 || l.Ports[1].NetBox == nil ||
		len(l.Ports[1].NetBox.Projects) != 1 || l.Ports[1].NetBox.Projects[0] != "webshop" || l.Ports[0].NetBox != nil || l.NetBoxError != "" {
		t.Fatalf("%+v", l.Ports[:2])
	}

	var res switches.PortResult
	body := switches.PortUpdate{Description: ptr("esx1"), Mode: ptr("trunk"), VLAN: ptr(10), TrunkVLANs: &[]int{10, 20}}
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/ports/1/1/5", body, &res); st != 200 || res.NetBox == nil || res.NetBox.Status != "ok" {
		t.Fatalf("%d %+v", st, res.NetBox)
	}
	var ifc nbfake.Object
	for _, o := range e.nb.Objects(nbfake.DevIfaces) {
		if o["name"] == "1/1/5" {
			ifc = o
		}
	}
	// VLAN 20 isn't in NetBox: tagged mode without it.
	if ifc["description"] != "esx1" || ifc["mode"] != "tagged" || ifc["untagged_vlan"] == nil || ifc["enabled"] != true {
		t.Errorf("interface: %+v", ifc)
	}
	// A port without NetBox interface: the change is made, the write-back fails.
	if st, _ := e.do(t, "admin", "PUT", "/api/v1/switches/sw1/ports/1/1/6", switches.PortUpdate{Description: ptr("x")}, &res); st != 200 ||
		res.NetBox == nil || res.NetBox.Status != "failed" {
		t.Errorf("no interface: %+v", res.NetBox)
	}

	var vr switches.VLANResult
	if st, _ := e.do(t, "netops", "POST", "/api/v1/switches/sw1/vlans", switches.VLANRequest{ID: 4001, Name: "lab"}, &vr); st != 201 ||
		vr.NetBox == nil || vr.NetBox.Status != "ok" {
		t.Fatalf("create: %+v", vr.NetBox)
	}
	if st, _ := e.do(t, "netops", "PUT", "/api/v1/switches/sw1/vlans/4001", switches.VLANRequest{Name: "lab2"}, &vr); st != 200 || vr.NetBox.Status != "ok" {
		t.Errorf("rename: %+v", vr.NetBox)
	}
	found := func() string {
		for _, v := range e.nb.Objects(nbfake.VLANs) {
			if v["vid"] == float64(4001) || v["vid"] == int64(4001) {
				return fmt.Sprint(v["name"])
			}
		}
		return ""
	}
	if found() != "lab2" {
		t.Errorf("NetBox VLAN: %+v", e.nb.Objects(nbfake.VLANs))
	}
	if st, _ := e.do(t, "netops", "DELETE", "/api/v1/switches/sw1/vlans/4001", nil, &vr); st != 200 || vr.NetBox.Status != "ok" || found() != "" {
		t.Errorf("delete: %+v %s", vr.NetBox, found())
	}
	if l, _ := e.audit.Query(ctx, audit.Filter{Action: "netbox.sync"}); len(l) != 5 {
		t.Errorf("netbox audit: %d", len(l))
	}
}
