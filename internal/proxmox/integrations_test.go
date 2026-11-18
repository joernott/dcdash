// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dhcp"
	"github.com/joernott/dcdash/internal/dhcp/kea/keafake"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/status"
)

func init() {
	base := []string{"proxmox:lab:qemu/5*:admin", "proxmox:lab:lxc/5*:admin", "proxmox:lab:qemu/9000:read", "proxmox:lab:node/*:read"}
	provUsers["integrator"] = append(base, "dhcp:kea4:192.0.2.0/24:write", "dhcp:kea6:*:write",
		"dns:mem:example.org:write", "dns:mem:2.0.192.in-addr.arpa:write")
	provUsers["noDNS"] = append(base, "dhcp:kea4:*:write")
}

// memDNS is a DNS provider in memory, like bind9 with a version per zone.
type memDNS struct {
	mu      sync.Mutex
	records map[string][]dns.Record
}

func (m *memDNS) Name() string                                 { return "mem" }
func (m *memDNS) Kind() string                                 { return "bind" }
func (m *memDNS) Capabilities() dns.Capabilities               { return dns.Capabilities{} }
func (m *memDNS) Check(context.Context) (status.Detail, error) { return status.Detail{}, nil }
func (m *memDNS) CreateZone(context.Context, string) (dns.Zone, error) {
	return dns.Zone{}, dns.ErrUnsupported
}
func (m *memDNS) DeleteZone(context.Context, string) error { return dns.ErrUnsupported }
func (m *memDNS) UpdateRecord(context.Context, string, string, dns.Record, string) (dns.Record, error) {
	return dns.Record{}, dns.ErrUnsupported
}
func (m *memDNS) DeleteRecord(context.Context, string, string, string) error {
	return dns.ErrUnsupported
}

func (m *memDNS) Zones(context.Context) ([]dns.Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []dns.Zone
	for z := range m.records {
		out = append(out, dns.Zone{Name: z})
	}
	return out, nil
}

func (m *memDNS) Records(_ context.Context, zone string) (dns.RecordSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs, ok := m.records[zone]
	if !ok {
		return dns.RecordSet{}, dns.ErrNotFound
	}
	return dns.RecordSet{Records: rs, Version: "v" + string(rune('0'+len(rs)))}, nil
}

func (m *memDNS) CreateRecord(_ context.Context, zone string, r dns.Record, version string) (dns.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version != "v"+string(rune('0'+len(m.records[zone]))) {
		return dns.Record{}, dns.ErrConflict
	}
	r.ID = r.Name + "/" + r.Type
	m.records[zone] = append(m.records[zone], r)
	return r, nil
}

// withIntegrations connects a DHCPv4 and a DHCPv6 fake and a DNS provider.
func withIntegrations(t *testing.T, e *provEnv) *memDNS {
	t.Helper()
	var cfgs []config.DHCPServer
	for _, s := range []struct {
		name, family string
		fake         *keafake.Server
	}{
		{"kea4", "v4", keafake.New("v4", "dcdash", "pw", keafake.Subnet{ID: 1, Prefix: "192.0.2.0/24", Pools: []string{"192.0.2.100-192.0.2.199"}, Total: 100})},
		{"kea6", "v6", keafake.New("v6", "dcdash", "pw", keafake.Subnet{ID: 1, Prefix: "2001:db8:1::/64", Pools: []string{"2001:db8:1::1000-2001:db8:1::1fff"}, Total: 4096})},
	} {
		hs := httptest.NewServer(s.fake)
		t.Cleanup(hs.Close)
		cfgs = append(cfgs, config.DHCPServer{Name: s.name, Family: s.family, URL: hs.URL, Insecure: true,
			Username: "dcdash", Password: config.NewSecret("pw")})
	}
	d, err := dhcp.NewService(cfgs, e.audit, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	mem := &memDNS{records: map[string][]dns.Record{"example.org": nil, "2.0.192.in-addr.arpa": nil}}
	e.svc.SetIntegrations(d, dns.NewService([]dns.Provider{mem}, e.audit, zerolog.Nop()))
	return mem
}

func integrationRequest() CreateRequest {
	req := cloneRequest()
	req.Network = CreateNetwork{}
	req.Networks = []CreateNetwork{{Bridge: "vmbr0", IPv4: "dhcp", MAC: "BC:24:11:00:00:50"}}
	req.Reservations = []CreateReservation{
		{Network: 0, Server: "kea4", SubnetID: 1, IPAddress: "192.0.2.50", Hostname: "app1"},
		{Network: 0, Server: "kea6", SubnetID: 1, IPAddress: "2001:db8:1::50"},
	}
	req.Records = []CreateRecord{
		{Provider: "mem", Zone: "example.org", Name: "app1", Type: "A", Content: "192.0.2.50"},
		{Provider: "mem", Zone: "2.0.192.in-addr.arpa", Name: "50", Type: "PTR", Content: "app1.example.org."},
	}
	return req
}

func TestCreateWithReservationsAndRecords(t *testing.T) {
	e := newProvEnv(t)
	mem := withIntegrations(t, e)
	j := e.create(t, "integrator", integrationRequest())
	if j.State != JobDone {
		t.Fatalf("job: %+v", j)
	}
	var names []string
	for _, s := range j.Steps {
		names = append(names, s.Name)
		if s.State != StepDone {
			t.Errorf("step %+v", s)
		}
	}
	// Reservations and records come before the start, so the guest gets its address at once.
	if strings.Join(names, ",") != "clone,configure,resize,dhcp,dhcp,dns,dns,start" {
		t.Errorf("steps: %v", names)
	}
	if j.Steps[3].Detail != "192.0.2.50 (kea4)" || j.Steps[5].Detail != "app1.example.org A 192.0.2.50" {
		t.Errorf("details: %+v", j.Steps)
	}

	res := e.entries(t, "dhcp.reservation.create")
	if len(res) != 2 || res[0].Actor != "integrator" || res[0].Result != "success" {
		t.Fatalf("dhcp audit: %+v", res)
	}
	for _, r := range res {
		if r.Details["identifier"] != "bc:24:11:00:00:50" || r.Details["identifierType"] != "hw-address" {
			t.Errorf("reservation is not for the MAC of net0: %+v", r.Details)
		}
	}
	if recs := e.entries(t, "dns.record.create"); len(recs) != 2 || recs[0].Result != "success" {
		t.Errorf("dns audit: %+v", recs)
	}
	if rs := mem.records["2.0.192.in-addr.arpa"]; len(rs) != 1 || rs[0].Name != "50.2.0.192.in-addr.arpa" {
		t.Errorf("PTR: %+v", rs)
	}
	if l := e.entries(t, "proxmox.guest.create"); len(l) != 1 || l[0].Details["reservations"] != float64(2) {
		t.Errorf("create audit: %+v", l)
	}
}

func TestCreateIntegrationsAreCheckedFirst(t *testing.T) {
	e := newProvEnv(t)
	withIntegrations(t, e)
	for name, tc := range map[string]struct {
		user string
		edit func(*CreateRequest)
		want int
		msg  string
	}{
		"dns not allowed":            {"noDNS", func(*CreateRequest) {}, http.StatusForbidden, "forbidden"},
		"address outside the subnet": {"integrator", func(r *CreateRequest) { r.Reservations[0].IPAddress = "198.51.100.5" }, http.StatusBadRequest, "reservations[0]"},
		"unknown device":             {"integrator", func(r *CreateRequest) { r.Reservations[1].Network = 3 }, http.StatusBadRequest, "reservations[1]: network"},
		"unknown server":             {"integrator", func(r *CreateRequest) { r.Reservations[0].Server = "kea9" }, http.StatusBadRequest, "unknown DHCP server"},
		"record type":                {"integrator", func(r *CreateRequest) { r.Records[0].Type = "MX" }, http.StatusBadRequest, "records[0]: type"},
		"record outside the zone":    {"integrator", func(r *CreateRequest) { r.Records[0].Name = "app1.example.com." }, http.StatusBadRequest, "records[0]"},
		"bad address":                {"integrator", func(r *CreateRequest) { r.Records[0].Content = "300.1.1.1" }, http.StatusBadRequest, "records[0]"},
	} {
		req := integrationRequest()
		tc.edit(&req)
		st, msg := e.do(t, tc.user, http.MethodPost, "/lab/guests", req, nil)
		if st != tc.want || !strings.Contains(msg, tc.msg) {
			t.Errorf("%s: %d %q, want %d %q", name, st, msg, tc.want, tc.msg)
		}
	}
	// Nothing was created, and the IDs are free again.
	if g, ok := e.fake.Guest("qemu", 5000); ok {
		t.Errorf("guest created: %+v", g)
	}
	if l := e.entries(t, "dhcp.reservation.create"); len(l) != 0 {
		t.Errorf("reservations: %+v", l)
	}

	// Without the modules, reservations are refused.
	e.svc.SetIntegrations(nil, nil)
	if st, msg := e.do(t, "integrator", http.MethodPost, "/lab/guests", integrationRequest(), nil); st != http.StatusBadRequest || !strings.Contains(msg, "no DHCP servers") {
		t.Errorf("without DHCP: %d %q", st, msg)
	}
}
