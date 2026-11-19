// SPDX-License-Identifier: GPL-3.0-or-later

package dns_test

import (
	"testing"

	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func TestRecordsWrittenToNetBox(t *testing.T) {
	e := newAPIEnv(t)
	f, nb := nbfake.NewService(t, e.audit)
	e.svc.SetNetBox(nb)
	path := base + "/cf/zones/example.org/records"
	var rs dns.RecordSet
	e.do(t, "viewer", "GET", path, nil, &rs)
	var created dns.RecordResult
	req := dns.RecordRequest{Name: "api", Type: "A", TTL: 300, Content: "192.0.2.5", Version: rs.Version}
	if code, msg := e.do(t, "editor", "POST", path, req, &created); code != 201 || created.NetBox == nil || created.NetBox.Status != netbox.StatusOK {
		t.Fatalf("create: %d %s %+v", code, msg, created)
	}
	ips := f.Objects(nbfake.IPs)
	if len(ips) != 1 || ips[0]["address"] != "192.0.2.5/32" || ips[0]["dns_name"] != "api.example.org" {
		t.Fatalf("NetBox: %+v", ips)
	}
	var updated dns.RecordResult
	upd := dns.RecordRequest{Name: "api2", Type: "A", TTL: 300, Content: "192.0.2.5", Version: created.Version}
	if code, msg := e.do(t, "editor", "PUT", path+"/"+created.ID, upd, &updated); code != 200 || updated.NetBox.Status != netbox.StatusOK ||
		f.Objects(nbfake.IPs)[0]["dns_name"] != "api2.example.org" {
		t.Fatalf("update: %d %s %+v %+v", code, msg, updated, f.Objects(nbfake.IPs))
	}
	var deleted dns.DeleteResult
	if code, msg := e.do(t, "editor", "DELETE", path+"/"+updated.ID+"?version="+updated.Version, nil, &deleted); code != 200 ||
		deleted.NetBox.Status != netbox.StatusOK || f.Objects(nbfake.IPs)[0]["dns_name"] != "" {
		t.Fatalf("delete: %d %s %+v %+v", code, msg, deleted, f.Objects(nbfake.IPs))
	}
	// Other record types aren't written.
	var txt dns.RecordResult
	e.do(t, "viewer", "GET", path, nil, &rs)
	if code, msg := e.do(t, "editor", "POST", path, dns.RecordRequest{Name: "t", Type: "TXT", Content: `"x"`, TTL: 300, Version: rs.Version}, &txt); code != 201 || txt.NetBox != nil {
		t.Errorf("TXT: %d %s %+v", code, msg, txt)
	}
	if code, _ := e.do(t, "editor", "DELETE", path+"/"+txt.ID+"?version="+txt.Version, nil, nil); code != 204 {
		t.Errorf("delete TXT: %d", code)
	}
}

func TestRecordProjects(t *testing.T) {
	e := newAPIEnv(t)
	f, nb := nbfake.NewService(t, e.audit)
	f.Seed(nbfake.Tags, nbfake.Object{"name": "Webshop", "slug": "project-webshop"})
	e.svc.SetNetBox(nb)
	path := base + "/cf/zones/example.org/records"
	var rs dns.RecordSet
	e.do(t, "viewer", "GET", path, nil, &rs)
	ps := []string{"webshop"}
	req := dns.RecordRequest{Name: "shop", Type: "A", TTL: 300, Content: "192.0.2.9", Version: rs.Version, Projects: &ps}
	if code, c := e.do(t, "editor", "POST", path, req, nil); code != 403 {
		t.Errorf("without project permission: %d %s", code, c)
	}
}
