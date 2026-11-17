// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox/pvefake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

func init() { taskPoll = 5 * time.Millisecond }

var provUsers = map[string][]string{
	"admin":   {"proxmox:*:*:admin"},
	"creator": {"proxmox:lab:qemu/5*:admin", "proxmox:lab:lxc/5*:admin", "proxmox:lab:qemu/9000:read", "proxmox:lab:node/*:read"},
	"viewer":  {"proxmox:lab:*:read"},
	"other":   {"proxmox:off:*:admin"},
}

const sshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEXAMPLEexampleEXAMPLEexample+/= user@host"

type provEnv struct {
	fake  *pvefake.Server
	srv   *httptest.Server
	audit *audit.Log
}

func newProvEnv(t *testing.T) *provEnv {
	t.Helper()
	guests := []pvefake.Guest{
		{Type: "qemu", VMID: 101, Name: "web", Node: "pve1", Status: "running"},
		{Type: "qemu", VMID: 5100, Name: "old", Node: "pve1", Status: "stopped"},
		{Type: "qemu", VMID: 9000, Name: "debian-ci", Node: "pve2", Status: "stopped", Template: true, Config: map[string]string{
			"scsi0": "local-lvm:base-9000-disk-0,iothread=1,size=10G", "ide2": "local-lvm:vm-9000-cloudinit,media=cdrom",
			"boot": "order=scsi0;net0", "net0": "virtio=BC:24:11:00:00:09,bridge=vmbr0"}},
		{Type: "qemu", VMID: 9001, Name: "plain", Node: "pve1", Status: "stopped", Template: true, Config: map[string]string{
			"virtio0": "ceph:base-9001-disk-0,size=8G", "ide2": "none,media=cdrom", "net0": "e1000=BC:24:11:00:00:0A,bridge=vmbr0"}},
	}
	fake := pvefake.New(testToken, []string{"pve1", "pve2"}, guests...)
	pve := httptest.NewTLSServer(fake)
	t.Cleanup(pve.Close)
	prov := config.ProxmoxProvisioning{
		VMIDs: config.IDRanges{{Min: 5000, Max: 5999}}, Storages: []string{"local-lvm", "ceph"}, ImageStorages: []string{"local"},
		Bridges: []string{"vmbr0"}, VLANs: config.IDRanges{{Min: 100, Max: 199}}, Templates: config.IDRanges{{Min: 9000, Max: 9099}},
		Pool: "dev", Limits: config.ProvisioningLimits{Cores: 8, Memory: 16 * config.GiB, Disk: 100 * config.GiB},
	}
	mk := func(name string, p config.ProxmoxProvisioning) *Cluster {
		c, err := New(config.ProxmoxCluster{Name: name, Endpoints: []config.ProxmoxEndpoint{endpointFor(pve)}, TokenID: "dcdash@pve!test",
			TokenSecret: config.NewSecret("secret"), Timeout: config.Duration(2 * time.Second), Provisioning: p}, zerolog.Nop())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	svc := NewService([]*Cluster{mk("lab", prov), mk("off", config.ProxmoxProvisioning{})}, auditLog, nil, zerolog.Nop())
	t.Cleanup(svc.jobs.wg.Wait)
	mux := http.NewServeMux()
	svc.Register(mux)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), auditLog)
	inject := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Test-User")
		if perms, ok := provUsers[user]; ok {
			set, _ := rbac.ParseSet(perms)
			ctx := session.WithSession(r.Context(), &session.Session{ID: "sess-" + user, Username: user, Source: "ldap", Stage: session.StageAuthenticated})
			r = r.WithContext(auth.WithSubject(ctx, &rbac.Subject{Username: user, Permissions: set}))
		}
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(authz.Middleware(inject))
	t.Cleanup(srv.Close)
	return &provEnv{fake: fake, srv: srv, audit: auditLog}
}

// do sends a request with an optional JSON body and decodes a 2xx response
// into out. It returns the status and the error code of other responses.
func (e *provEnv) do(t *testing.T, user, method, path string, body, out any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/api/v1/proxmox/clusters"+path, rd)
	req.Header.Set("X-Test-User", user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return res.StatusCode, e.Error.Code + ": " + e.Error.Message
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	return res.StatusCode, ""
}

// create submits a request and waits for its job.
func (e *provEnv) create(t *testing.T, user string, req CreateRequest) Job {
	t.Helper()
	var j Job
	if st, msg := e.do(t, user, http.MethodPost, "/lab/guests", req, &j); st != http.StatusAccepted {
		t.Fatalf("create: %d %s", st, msg)
	}
	for range 400 {
		if j.State != JobRunning {
			return j
		}
		time.Sleep(5 * time.Millisecond)
		e.do(t, user, http.MethodGet, "/lab/jobs/"+j.ID, nil, &j)
	}
	t.Fatalf("job still running: %+v", j)
	return j
}

func (e *provEnv) lastCall(t *testing.T, method, suffix string) url.Values {
	t.Helper()
	calls := e.fake.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Method == method && strings.HasSuffix(calls[i].Path, suffix) {
			return calls[i].Form
		}
	}
	t.Fatalf("no call %s %s in %+v", method, suffix, calls)
	return nil
}

func (e *provEnv) entries(t *testing.T, action string) []audit.Entry {
	t.Helper()
	l, err := e.audit.Query(context.Background(), audit.Filter{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func cloneRequest() CreateRequest {
	return CreateRequest{Type: TypeQEMU, Source: SourceTemplate, Template: 9000, Full: true, Storage: "local-lvm", Name: "app1",
		Cores: 2, Memory: 2048, Disk: 20, Network: CreateNetwork{Bridge: "vmbr0", VLAN: 100, IPv4: "10.0.0.5/24", Gateway4: "10.0.0.1",
			IPv6: "auto", Nameservers: []string{"10.0.0.53"}, SearchDomain: "example.internal"},
		User: "ops", Password: "s3cret-pw", SSHKeys: sshKey, Start: true, Tags: []string{"dev"}}
}

func TestProvisioningOptions(t *testing.T) {
	e := newProvEnv(t)
	var o ProvisioningOptions
	if st, msg := e.do(t, "creator", http.MethodGet, "/lab/provisioning?node=pve1", nil, &o); st != http.StatusOK {
		t.Fatalf("%d %s", st, msg)
	}
	names := func(n int, f func(int) string) string {
		var out []string
		for i := range n {
			out = append(out, f(i))
		}
		return strings.Join(out, ",")
	}
	if o.Node != "pve1" || strings.Join(o.Nodes, ",") != "pve1,pve2" ||
		names(len(o.Storages), func(i int) string { return o.Storages[i].Name }) != "local-lvm,ceph" ||
		names(len(o.ISOs), func(i int) string { return o.ISOs[i].VolID }) != "local:iso/debian-13.4.0-amd64-netinst.iso,local:iso/virtio-win.iso" ||
		len(o.ContainerTemplates) != 1 || names(len(o.Bridges), func(i int) string { return o.Bridges[i].Name }) != "vmbr0" ||
		!o.Bridges[0].VLANAware || len(o.Templates) != 1 || o.Templates[0].VMID != 9000 || o.Templates[0].Node != "pve2" ||
		o.Limits.Memory != 16384 || o.Defaults.Cores != 2 || o.Defaults.Storage != "local-lvm" || o.Pool != "dev" ||
		strings.Join(o.VMIDs, ",") != "5000-5999" || strings.Join(o.VLANs, ",") != "100-199" {
		t.Errorf("options: %+v", o)
	}
	if st, _ := e.do(t, "viewer", http.MethodGet, "/lab/provisioning", nil, nil); st != http.StatusForbidden {
		t.Errorf("viewer: %d", st)
	}
	if st, msg := e.do(t, "admin", http.MethodGet, "/off/provisioning", nil, nil); st != http.StatusConflict ||
		!strings.HasPrefix(msg, "provisioning_disabled") {
		t.Errorf("disabled: %d %s", st, msg)
	}
	if st, _ := e.do(t, "creator", http.MethodGet, "/lab/provisioning?node=pve9", nil, nil); st != http.StatusNotFound {
		t.Errorf("unknown node: %d", st)
	}
}

func TestCreateClone(t *testing.T) {
	e := newProvEnv(t)
	e.fake.Set(func(s *pvefake.Settings) { s.Taken = map[int]bool{5000: true} })
	j := e.create(t, "creator", cloneRequest())
	if j.State != JobDone || j.VMID != 5001 || j.Node != "pve2" || len(j.Steps) != 4 {
		t.Fatalf("job: %+v", j)
	}
	for i, n := range []string{"clone", "configure", "resize", "start"} {
		if j.Steps[i].Name != n || j.Steps[i].State != StepDone {
			t.Errorf("step %d: %+v", i, j.Steps[i])
		}
	}
	if j.Steps[0].UPID == "" || j.Steps[1].UPID != "" {
		t.Errorf("task IDs: %+v", j.Steps)
	}
	clone := e.lastCall(t, http.MethodPost, "/qemu/9000/clone")
	if clone.Get("newid") != "5001" || clone.Get("full") != "1" || clone.Get("storage") != "local-lvm" || clone.Get("pool") != "dev" ||
		clone.Get("name") != "app1" {
		t.Errorf("clone: %v", clone)
	}
	conf := e.lastCall(t, http.MethodPut, "/qemu/5001/config")
	keys, _ := url.QueryUnescape(conf.Get("sshkeys"))
	if conf.Get("net0") != "virtio,bridge=vmbr0,tag=100" || conf.Get("ciuser") != "ops" || conf.Get("cipassword") != "s3cret-pw" ||
		strings.TrimSpace(keys) != sshKey || strings.Contains(conf.Get("sshkeys"), "+") && !strings.Contains(conf.Get("sshkeys"), "%2B") ||
		conf.Get("ipconfig0") != "ip=10.0.0.5/24,gw=10.0.0.1,ip6=auto" || conf.Get("nameserver") != "10.0.0.53" ||
		conf.Get("searchdomain") != "example.internal" || conf.Get("cores") != "2" || conf.Get("memory") != "2048" ||
		conf.Get("tags") != "dev" || conf.Has("ide2") {
		t.Errorf("config: %v", conf)
	}
	if rs := e.lastCall(t, http.MethodPut, "/qemu/5001/resize"); rs.Get("disk") != "scsi0" || rs.Get("size") != "20G" {
		t.Errorf("resize: %v", rs)
	}
	if g, ok := e.fake.Guest("qemu", 5001); !ok || g.Status != "running" || g.Node != "pve2" {
		t.Errorf("guest: %+v %v", g, ok)
	}
	l := e.entries(t, "proxmox.guest.create")
	if len(l) != 1 || l[0].Result != audit.ResultSuccess || l[0].Target != "lab/qemu/5001" || l[0].Actor != "creator" ||
		l[0].Details["passwordSet"] != true || l[0].Details["sshKeys"] != float64(1) || l[0].Details["template"] != float64(9000) {
		t.Errorf("audit: %+v", l)
	}
	raw, _ := json.Marshal(l)
	if strings.Contains(string(raw), "s3cret") {
		t.Error("password in the audit log")
	}
	// The ID is free again in dcdash, but now used in Proxmox.
	j = e.create(t, "creator", CreateRequest{Type: TypeQEMU, Source: SourceTemplate, Template: 9000, Name: "app2", Cores: 1,
		Memory: 512, Network: CreateNetwork{Bridge: "vmbr0"}})
	if j.State != JobDone || j.VMID != 5002 || len(j.Steps) != 2 {
		t.Errorf("linked clone: %+v", j)
	}
	if c := e.lastCall(t, http.MethodPost, "/qemu/9000/clone"); c.Get("full") != "0" || c.Has("storage") {
		t.Errorf("linked clone: %v", c)
	}
	if c := e.lastCall(t, http.MethodPut, "/qemu/5002/config"); c.Has("ipconfig0") || c.Has("ciuser") {
		t.Errorf("without cloud-init: %v", c)
	}
}

func TestCreateCloneAddsCloudInitDrive(t *testing.T) {
	e := newProvEnv(t)
	req := cloneRequest()
	req.Template, req.Disk, req.Start, req.Storage, req.Full = 9001, 0, false, "", false
	req.Network.IPv6, req.Network.Gateway4, req.Network.IPv4 = "", "", "dhcp"
	j := e.create(t, "admin", req)
	if j.State != JobDone || len(j.Steps) != 2 {
		t.Fatalf("job: %+v", j)
	}
	conf := e.lastCall(t, http.MethodPut, "/config")
	if conf.Get("ide0") != "ceph:cloudinit" || conf.Get("net0") != "e1000,bridge=vmbr0,tag=100" || conf.Get("ipconfig0") != "ip=dhcp" {
		t.Errorf("config: %v", conf)
	}
}

func TestCreateISOAndContainer(t *testing.T) {
	e := newProvEnv(t)
	j := e.create(t, "creator", CreateRequest{Type: TypeQEMU, Source: SourceISO, Node: "pve1", VMID: 5500, Name: "win",
		ISO: "local:iso/virtio-win.iso", OSType: "windows", Cores: 4, Memory: 8192, Disk: 64, Storage: "ceph",
		Network: CreateNetwork{Bridge: "vmbr0"}, Description: "test"})
	if j.State != JobDone || j.VMID != 5500 || len(j.Steps) != 1 || j.Steps[0].Name != "create" {
		t.Fatalf("job: %+v", j)
	}
	f := e.lastCall(t, http.MethodPost, "/nodes/pve1/qemu")
	for k, v := range map[string]string{"vmid": "5500", "scsi0": "ceph:64,iothread=1", "ide2": "local:iso/virtio-win.iso,media=cdrom",
		"ostype": "win11", "bios": "ovmf", "efidisk0": "ceph:1,efitype=4m,pre-enrolled-keys=1", "tpmstate0": "ceph:1,version=v2.0",
		"net0": "virtio,bridge=vmbr0", "pool": "dev", "description": "test", "memory": "8192"} {
		if f.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.Get(k), v)
		}
	}
	j = e.create(t, "creator", CreateRequest{Type: TypeLXC, Node: "pve2", Name: "ct1", OSTemplate: "local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst",
		Cores: 1, Memory: 1024, Disk: 8, Storage: "local-lvm", Network: CreateNetwork{Bridge: "vmbr0", IPv4: "dhcp", IPv6: "2001:db8::5/64", Gateway6: "2001:db8::1"},
		Password: "rootpw1", SSHKeys: sshKey + "\n\n", Start: true})
	if j.State != JobDone || j.VMID != 5000 || j.Type != TypeLXC || len(j.Steps) != 2 {
		t.Fatalf("container job: %+v", j)
	}
	f = e.lastCall(t, http.MethodPost, "/nodes/pve2/lxc")
	for k, v := range map[string]string{"hostname": "ct1", "rootfs": "local-lvm:8", "unprivileged": "1", "password": "rootpw1",
		"ssh-public-keys": sshKey + "\n", "net0": "name=eth0,bridge=vmbr0,ip=dhcp,ip6=2001:db8::5/64,gw6=2001:db8::1"} {
		if f.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, f.Get(k), v)
		}
	}
	if g, _ := e.fake.Guest("lxc", 5000); g.Status != "running" {
		t.Errorf("container: %+v", g)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newProvEnv(t)
	base := cloneRequest
	for name, tc := range map[string]struct {
		user   string
		mod    func(*CreateRequest)
		status int
		code   string
	}{
		"cores":         {"creator", func(r *CreateRequest) { r.Cores = 9 }, 400, "invalid_request: cores"},
		"memory":        {"creator", func(r *CreateRequest) { r.Memory = 20000 }, 400, "invalid_request: memory"},
		"bridge":        {"creator", func(r *CreateRequest) { r.Network.Bridge = "vmbr1" }, 400, "invalid_request: network.bridge"},
		"vlan":          {"creator", func(r *CreateRequest) { r.Network.VLAN = 300 }, 400, "invalid_request: network.vlan"},
		"ipv4":          {"creator", func(r *CreateRequest) { r.Network.IPv4 = "10.0.0.5" }, 400, "invalid_request: network.ipv4"},
		"gateway":       {"creator", func(r *CreateRequest) { r.Network.IPv4 = "dhcp" }, 400, "invalid_request: network.gateway4"},
		"name":          {"creator", func(r *CreateRequest) { r.Name = "bad name;" }, 400, "invalid_request: name"},
		"tag":           {"creator", func(r *CreateRequest) { r.Tags = []string{"A B"} }, 400, "invalid_request: tags"},
		"ssh key":       {"creator", func(r *CreateRequest) { r.SSHKeys = "not a key" }, 400, "invalid_request: sshKeys"},
		"user":          {"creator", func(r *CreateRequest) { r.User = "Root!" }, 400, "invalid_request: user"},
		"storage":       {"creator", func(r *CreateRequest) { r.Storage = "local" }, 400, "invalid_request: storage"},
		"small disk":    {"creator", func(r *CreateRequest) { r.Disk = 5 }, 400, "invalid_request: disk"},
		"template":      {"creator", func(r *CreateRequest) { r.Template = 9001 }, 400, "invalid_request: template 9001"},
		"not template":  {"admin", func(r *CreateRequest) { r.Template = 101 }, 400, "invalid_request: template 101"},
		"vmid range":    {"creator", func(r *CreateRequest) { r.VMID = 4000 }, 400, "invalid_request: vmid"},
		"vmid used":     {"creator", func(r *CreateRequest) { r.VMID = 5100 }, 409, "vmid_unavailable"},
		"no permission": {"viewer", func(r *CreateRequest) {}, 403, ""},
		"type":          {"creator", func(r *CreateRequest) { r.Source = "" }, 400, "invalid_request: type"},
		"iso source":    {"creator", func(r *CreateRequest) { r.Source, r.Node, r.OSType = SourceISO, "pve1", "linux" }, 400, "invalid_request: template, user"},
		"iso missing": {"creator", func(r *CreateRequest) {
			*r = CreateRequest{Type: TypeQEMU, Source: SourceISO, Node: "pve1", Name: "x", ISO: "local:iso/none.iso", OSType: "linux",
				Cores: 1, Memory: 512, Disk: 8, Storage: "ceph", Network: CreateNetwork{Bridge: "vmbr0"}}
		}, 400, "invalid_request: iso"},
		"ct login": {"creator", func(r *CreateRequest) {
			*r = CreateRequest{Type: TypeLXC, Node: "pve1", Name: "x", OSTemplate: "local:vztmpl/debian-13-standard_13.1-2_amd64.tar.zst",
				Cores: 1, Memory: 512, Disk: 8, Storage: "ceph", Network: CreateNetwork{Bridge: "vmbr0"}}
		}, 400, "invalid_request: password or sshKeys"},
		"node": {"creator", func(r *CreateRequest) {
			*r = CreateRequest{Type: TypeLXC, Node: "pve9", Name: "x", Cores: 1, Memory: 512, Disk: 8, Storage: "ceph", Network: CreateNetwork{Bridge: "vmbr0"}}
		}, 400, "invalid_request: node"},
	} {
		t.Run(name, func(t *testing.T) {
			req := base()
			tc.mod(&req)
			st, msg := e.do(t, tc.user, http.MethodPost, "/lab/guests", req, nil)
			if st != tc.status || !strings.HasPrefix(msg, tc.code) {
				t.Errorf("%d %q, want %d %q", st, msg, tc.status, tc.code)
			}
		})
	}
	if len(e.fake.Calls()) != 0 {
		t.Errorf("calls: %+v", e.fake.Calls())
	}
	// Refused requests don't hold their ID.
	if j := e.create(t, "creator", base()); j.VMID != 5000 {
		t.Errorf("ID after refusals: %d", j.VMID)
	}
}

func TestCreateFailedTask(t *testing.T) {
	e := newProvEnv(t)
	e.fake.Set(func(s *pvefake.Settings) { s.FailTasks = map[string]string{"resize": "disk full"} })
	j := e.create(t, "creator", cloneRequest())
	if j.State != JobFailed || j.Error != "proxmox task failed: disk full" || j.Steps[2].State != StepFailed || j.Steps[3].State != StepSkipped {
		t.Fatalf("job: %+v", j)
	}
	l := e.entries(t, "proxmox.guest.create")
	if len(l) != 1 || l[0].Result != audit.ResultFailure || l[0].Details["error"] != "proxmox task failed: disk full" {
		t.Errorf("audit: %+v", l)
	}
	// Other users don't see the job; users who may read the guest do.
	if st, _ := e.do(t, "other", http.MethodGet, "/lab/jobs/"+j.ID, nil, nil); st != http.StatusForbidden {
		t.Errorf("other: %d", st)
	}
	if st, _ := e.do(t, "viewer", http.MethodGet, "/lab/jobs/"+j.ID, nil, nil); st != http.StatusOK {
		t.Errorf("viewer: %d", st)
	}
	if st, _ := e.do(t, "creator", http.MethodGet, "/lab/jobs/unknown", nil, nil); st != http.StatusNotFound {
		t.Errorf("unknown: %d", st)
	}
}

func TestDeleteGuest(t *testing.T) {
	e := newProvEnv(t)
	del := func(user, path string, body DeleteRequest) (int, string) {
		var ref TaskRef
		return e.do(t, user, http.MethodDelete, path, body, &ref)
	}
	if st, _ := del("creator", "/lab/guests/qemu/101", DeleteRequest{Confirm: "web"}); st != http.StatusForbidden {
		t.Errorf("no permission: %d", st)
	}
	if st, msg := del("admin", "/lab/guests/qemu/101", DeleteRequest{Confirm: "web"}); st != http.StatusConflict || !strings.HasPrefix(msg, "guest_running") {
		t.Errorf("running: %d %s", st, msg)
	}
	if st, msg := del("creator", "/lab/guests/qemu/5100", DeleteRequest{Confirm: "Old"}); st != http.StatusBadRequest ||
		!strings.HasPrefix(msg, "confirmation_mismatch") {
		t.Errorf("mismatch: %d %s", st, msg)
	}
	if st, msg := del("admin", "/off/guests/qemu/5100", DeleteRequest{Confirm: "old"}); st != http.StatusConflict {
		t.Errorf("disabled: %d %s", st, msg)
	}
	if st, msg := del("creator", "/lab/guests/qemu/5100", DeleteRequest{Confirm: "old", Purge: true}); st != http.StatusAccepted {
		t.Fatalf("delete: %d %s", st, msg)
	}
	if _, ok := e.fake.Guest("qemu", 5100); ok {
		t.Error("guest not deleted")
	}
	if f := e.lastCall(t, http.MethodDelete, "/qemu/5100"); f.Get("purge") != "1" || f.Get("destroy-unreferenced-disks") != "1" {
		t.Errorf("delete form: %v", f)
	}
	l := e.entries(t, "proxmox.guest.delete")
	if len(l) != 1 || l[0].Target != "lab/qemu/5100" || l[0].Details["purge"] != true || l[0].Details["upid"] == "" {
		t.Errorf("audit: %+v", l)
	}
}

func TestParseTemplate(t *testing.T) {
	ti := parseTemplate("pve1", map[string]any{"boot": "order=ide2;virtio1;net0", "ide2": "local:iso/x.iso,media=cdrom",
		"virtio1": "ceph:vm-1-disk-1,size=1T", "scsi0": "local:vm-1-disk-0,size=512M", "net0": "virtio=AA:BB,bridge=vmbr0",
		"ide0": "ceph:vm-1-cloudinit,media=cdrom"})
	if ti.bootDisk != "virtio1" || ti.diskGiB != 1024 || ti.diskStore != "ceph" || !ti.cloudInit || ti.freeSlot != "ide1" {
		t.Errorf("%+v", ti)
	}
	if ti := parseTemplate("pve1", map[string]any{}); ti.bootDisk != "" || ti.netModel != "virtio" || ti.freeSlot != "ide2" {
		t.Errorf("empty: %+v", ti)
	}
}

func TestJobLimits(t *testing.T) {
	s := newJobStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	for i := range maxJobsPerUser {
		s.jobs[string(rune('a'+i))] = &Job{User: "u", State: JobRunning}
	}
	s.jobs["old"] = &Job{User: "u", State: JobDone, Ended: now.Add(-2 * jobRetention)}
	if err := s.admit("u"); !errors.Is(err, errTooManyJobs) {
		t.Errorf("per user: %v", err)
	}
	if err := s.admit("v"); err != nil {
		t.Errorf("other user: %v", err)
	}
	if _, ok := s.jobs["old"]; ok {
		t.Error("old job not pruned")
	}
	if !s.reserve("lab", 1) || s.reserve("lab", 1) || !s.isReserved("lab", 1) {
		t.Error("reserve")
	}
	s.release("lab", 1)
	if s.isReserved("lab", 1) {
		t.Error("release")
	}
}
