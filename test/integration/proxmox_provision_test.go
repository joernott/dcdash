// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

// Creating and deleting guests on a real cluster (task 10.9) through dcdash's API. The test
// runs when DCDASH_IT_PVE_PROVISION_VMIDS is set, besides the API access variables:
//
//	DCDASH_IT_PVE_PROVISION_VMIDS        ID range for the test guests, e.g. "1001-1099"
//	DCDASH_IT_PVE_PROVISION_TEMPLATE     VM ID of a template, cloned as a linked clone
//	DCDASH_IT_PVE_PROVISION_CT_TEMPLATE  volume of a container template
//	DCDASH_IT_PVE_PROVISION_STORAGE      storage for disks (also used for images)
//	DCDASH_IT_PVE_PROVISION_BRIDGE       bridge, and optionally _VLAN
//	DCDASH_IT_PVE_PROVISION_ISO          optional: volume of an ISO image for a VM from an ISO
//
// The guests are not started; they are deleted at the end (also when the test fails).

// A public key whose private key was discarded; it contains "+" and "/" to test the encoding.
const itSSHKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIE+D5dw1ubUw6nIwzC2L9dJWz8XQ2mCX/U5mIR25tUav dcdash-it"

type provAPI struct {
	t   *testing.T
	srv *httptest.Server
}

func (a provAPI) call(method, path string, body, out any) int {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.srv.URL+"/api/v1/proxmox/clusters/it"+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		a.t.Logf("%s %s: %d %s", method, path, res.StatusCode, data)
	} else if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			a.t.Fatal(err)
		}
	}
	return res.StatusCode
}

// startProvAPI serves dcdash's Proxmox API for the test cluster with the
// provisioning settings prov, for a user with admin on everything.
func startProvAPI(t *testing.T, prov config.ProxmoxProvisioning) provAPI {
	t.Helper()
	cfg := config.ProxmoxCluster{
		Name: "it", Endpoints: []config.ProxmoxEndpoint{{URL: requireEnv(t, "DCDASH_IT_PVE_URL"),
			Fingerprint: requireEnv(t, "DCDASH_IT_PVE_FINGERPRINT")}},
		TokenID: requireEnv(t, "DCDASH_IT_PVE_TOKEN_ID"), TokenSecret: config.NewSecret(requireEnv(t, "DCDASH_IT_PVE_TOKEN_SECRET")),
		Timeout: config.Duration(30 * time.Second), Provisioning: prov,
	}
	pc, err := proxmox.New(cfg, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := proxmox.NewService([]*proxmox.Cluster{pc}, audit.New(st.DB(), zerolog.Nop()), nil, zerolog.Nop())
	mux := http.NewServeMux()
	svc.Register(mux)
	perms, _ := rbac.ParseSet([]string{"proxmox:it:*:admin"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := session.WithSession(r.Context(), &session.Session{ID: "it", Username: "it", Source: "ldap", Stage: session.StageAuthenticated})
		mux.ServeHTTP(w, r.WithContext(auth.WithSubject(ctx, &rbac.Subject{Username: "it", Permissions: perms})))
	}))
	t.Cleanup(srv.Close)
	return provAPI{t: t, srv: srv}
}

func TestProxmoxProvisioning(t *testing.T) {
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
	tpl, _ := strconv.Atoi(requireEnv(t, "DCDASH_IT_PVE_PROVISION_TEMPLATE"))
	ctTemplate := requireEnv(t, "DCDASH_IT_PVE_PROVISION_CT_TEMPLATE")
	storage := requireEnv(t, "DCDASH_IT_PVE_PROVISION_STORAGE")
	bridge := requireEnv(t, "DCDASH_IT_PVE_PROVISION_BRIDGE")
	vlan, _ := strconv.Atoi(os.Getenv("DCDASH_IT_PVE_PROVISION_VLAN"))

	c := pveCluster(t, false)
	prov := config.ProxmoxProvisioning{
		VMIDs: config.IDRanges{{Min: minID, Max: maxID}}, Storages: []string{storage}, Bridges: []string{bridge},
		Templates: config.IDRanges{{Min: tpl, Max: tpl}},
	}
	iso := os.Getenv("DCDASH_IT_PVE_PROVISION_ISO")
	if iso != "" {
		prov.ImageStorages = []string{storage, strings.SplitN(iso, ":", 2)[0]}
	}
	if vlan != 0 {
		prov.VLANs = config.IDRanges{{Min: vlan, Max: vlan}}
	}
	api := startProvAPI(t, prov)

	// Whatever happens, guests created in the range by this test are deleted.
	var created []proxmox.Job
	t.Cleanup(func() {
		for _, j := range created {
			res, err := c.FindGuest(context.Background(), j.Type, j.VMID)
			if err != nil {
				continue
			}
			upid, err := c.DeleteGuest(context.Background(), res.Node, j.Type, j.VMID, true)
			if err != nil {
				t.Errorf("cleanup %s/%d: %v", j.Type, j.VMID, err)
				continue
			}
			t.Logf("cleanup %s/%d: %s", j.Type, j.VMID, waitTask(t, c, upid))
		}
	})

	var opts proxmox.ProvisioningOptions
	if st := api.call(http.MethodGet, "/provisioning", nil, &opts); st != http.StatusOK {
		t.Fatalf("options: %d", st)
	}
	i := slices.IndexFunc(opts.Templates, func(x proxmox.TemplateOption) bool { return x.VMID == tpl })
	if i < 0 {
		t.Fatalf("template %d not offered: %+v", tpl, opts.Templates)
	}
	node := opts.Templates[i].Node
	if node != opts.Node {
		api.call(http.MethodGet, "/provisioning?node="+node, nil, &opts)
	}
	t.Logf("options on %s: storages %+v, bridges %+v, %d container templates", node, opts.Storages, opts.Bridges, len(opts.ContainerTemplates))
	if !slices.ContainsFunc(opts.ContainerTemplates, func(v proxmox.Volume) bool { return v.VolID == ctTemplate }) {
		t.Errorf("container template %s not offered", ctTemplate)
	}

	run := func(name string, req proxmox.CreateRequest) proxmox.Job {
		t.Helper()
		var j proxmox.Job
		if st := api.call(http.MethodPost, "/guests", req, &j); st != http.StatusAccepted {
			t.Fatalf("%s: create: %d", name, st)
		}
		created = append(created, j)
		deadline := time.Now().Add(10 * time.Minute)
		for j.State == proxmox.JobRunning && time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			api.call(http.MethodGet, "/jobs/"+j.ID, nil, &j)
		}
		for _, s := range j.Steps {
			t.Logf("%s: %s %s %s %s", name, s.Name, s.State, s.UPID, s.Error)
		}
		if j.State != proxmox.JobDone {
			t.Fatalf("%s: job %s: %s", name, j.State, j.Error)
		}
		return j
	}
	net := proxmox.CreateNetwork{Bridge: bridge, VLAN: vlan}

	// Two network devices (task 11.13): the first with a given MAC address and a static
	// address, the second with SLAAC.
	clone := run("clone", proxmox.CreateRequest{Type: proxmox.TypeQEMU, Source: proxmox.SourceTemplate, Template: tpl,
		Name: "dcdash-it-clone", Cores: 1, Memory: 1024, Disk: 65, Networks: []proxmox.CreateNetwork{
			{Bridge: bridge, VLAN: vlan, IPv4: "192.0.2.10/24", Gateway4: "192.0.2.1", Nameservers: []string{"192.0.2.53"}, MAC: "BC:24:11:1D:C0:01"},
			{Bridge: bridge, VLAN: vlan, IPv6: "auto"},
		},
		User: "it", SSHKeys: itSSHKey, Description: "dcdash integration test", Tags: []string{"dcdash-it"}})
	cfgVM, err := c.GuestConfig(t.Context(), clone.Node, proxmox.TypeQEMU, clone.VMID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("clone %d config: net0=%v ipconfig0=%v ciuser=%v cores=%v memory=%v", clone.VMID, cfgVM["net0"], cfgVM["ipconfig0"],
		cfgVM["ciuser"], cfgVM["cores"], cfgVM["memory"])
	if cfgVM["ipconfig0"] != "ip=192.0.2.10/24,gw=192.0.2.1" || cfgVM["ciuser"] != "it" || !strings.Contains(cfgVM["sshkeys"].(string), "dcdash-it") {
		t.Errorf("clone config: %v", cfgVM)
	}
	if d, _ := cfgVM["scsi0"].(string); !strings.Contains(d, "size=65G") {
		t.Errorf("clone disk: %v", cfgVM["scsi0"])
	}
	if vlan != 0 && !strings.Contains(cfgVM["net0"].(string), "tag="+strconv.Itoa(vlan)) {
		t.Errorf("clone net0: %v", cfgVM["net0"])
	}
	t.Logf("clone net1=%v ipconfig1=%v", cfgVM["net1"], cfgVM["ipconfig1"])
	if n0, _ := cfgVM["net0"].(string); !strings.Contains(n0, "=BC:24:11:1D:C0:01,") {
		t.Errorf("clone net0 without the given MAC: %v", n0)
	}
	if n1, _ := cfgVM["net1"].(string); !strings.Contains(n1, "=BC:24:11:") || cfgVM["ipconfig1"] != "ip6=auto" {
		t.Errorf("clone net1: %v, ipconfig1 %v", n1, cfgVM["ipconfig1"])
	}

	ct := run("container", proxmox.CreateRequest{Type: proxmox.TypeLXC, Node: node, Name: "dcdash-it-ct", OSTemplate: ctTemplate,
		Cores: 1, Memory: 512, Disk: 2, Storage: storage, Networks: []proxmox.CreateNetwork{net, {Bridge: bridge}}, SSHKeys: itSSHKey})
	cfgCT, err := c.GuestConfig(t.Context(), ct.Node, proxmox.TypeLXC, ct.VMID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("container %d config: net0=%v net1=%v rootfs=%v unprivileged=%v", ct.VMID, cfgCT["net0"], cfgCT["net1"], cfgCT["rootfs"], cfgCT["unprivileged"])
	if n1, _ := cfgCT["net1"].(string); !strings.Contains(n1, "name=eth1") || !strings.Contains(n1, "hwaddr=BC:24:11:") {
		t.Errorf("container net1: %v", n1)
	}

	jobs := []proxmox.Job{clone, ct}
	if iso != "" {
		jobs = append(jobs, run("iso", proxmox.CreateRequest{Type: proxmox.TypeQEMU, Source: proxmox.SourceISO, Node: node,
			Name: "dcdash-it-iso", ISO: iso, OSType: "linux", Cores: 1, Memory: 1024, Disk: 8, Storage: storage, Network: net}))
	}
	for _, j := range jobs {
		var ref proxmox.TaskRef
		path := "/guests/" + j.Type + "/" + strconv.Itoa(j.VMID)
		if st := api.call(http.MethodDelete, path, proxmox.DeleteRequest{Confirm: j.Name, Purge: true}, &ref); st != http.StatusAccepted {
			t.Fatalf("delete %s: %d", path, st)
		}
		if exit := waitTask(t, c, ref.UPID); exit != "OK" {
			t.Errorf("delete %s: %s", path, exit)
		}
		if _, err := c.FindGuest(t.Context(), j.Type, j.VMID); err == nil {
			t.Errorf("%s still exists", path)
		}
	}
}
