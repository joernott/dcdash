// SPDX-License-Identifier: GPL-3.0-or-later

package dhcp_test

import (
	"testing"

	"github.com/joernott/dcdash/internal/dhcp"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
)

func TestReservationsWrittenToNetBox(t *testing.T) {
	e := newAPIEnv(t, nil)
	f, nb := nbfake.NewService(t, e.audit)
	e.svc.SetNetBox(nb)
	path := base + "/kea4-a/reservations"
	req := dhcp.ReservationRequest{SubnetID: 1, IdentifierType: "hw-address", Identifier: "aa:bb:cc:00:00:02", IPAddresses: []string{"192.0.2.11"}}
	var created dhcp.ReservationView
	if code, c := e.do(t, "netops", "POST", path, req, &created); code != 201 || created.NetBox == nil || created.NetBox.Status != netbox.StatusOK {
		t.Fatalf("create: %d %s %+v", code, c, created)
	}
	ips := f.Objects(nbfake.IPs)
	if len(ips) != 1 || ips[0]["address"] != "192.0.2.11/32" || ips[0]["description"] != "DHCP reservation (kea4-a)" {
		t.Fatalf("NetBox: %+v", ips)
	}
	var deleted dhcp.DeleteResult
	if code, c := e.do(t, "netops", "DELETE", path+"/"+created.ID+"?version="+created.Version, nil, &deleted); code != 200 ||
		deleted.NetBox == nil || deleted.NetBox.Status != netbox.StatusOK || len(f.Objects(nbfake.IPs)) != 0 {
		t.Fatalf("delete: %d %s %+v %+v", code, c, deleted, f.Objects(nbfake.IPs))
	}
	if l := e.entries(t, netbox.AuditSync); len(l) != 2 || l[0].Actor != "netops" {
		t.Errorf("audit: %+v", l)
	}
}
