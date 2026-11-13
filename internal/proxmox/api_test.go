// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox/pvefake"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

// Test users and their permissions.
var testUsers = map[string][]string{
	"admin":    {"proxmox:*:*:admin"},
	"viewer":   {"proxmox:lab:*:read"},
	"operator": {"proxmox:lab:qemu/10*:operate", "proxmox:lab:node/pve1:read"},
	"console":  {"proxmox:lab:lxc/200:console"},
	"other":    {"proxmox:other:*:admin", "dns:*:*:admin"},
}

type apiEnv struct {
	fake  *pvefake.Server
	srv   *httptest.Server
	audit *audit.Log
}

// newAPIEnv serves the API; the header X-Test-User selects the user. The
// cluster has a console user, so terminals are available.
func newAPIEnv(t *testing.T) *apiEnv { return newAPIEnvWith(t, true) }

func newAPIEnvWith(t *testing.T, consoleUser bool) *apiEnv {
	t.Helper()
	fake, pve := fakeServer(t)
	cluster := newCluster(t, endpointFor(pve))
	if consoleUser {
		fake.Set(func(s *pvefake.Settings) { s.User, s.Password = "console@pve", "pw" })
		var err error
		cluster, err = New(config.ProxmoxCluster{
			Name: "lab", Endpoints: []config.ProxmoxEndpoint{endpointFor(pve)}, TokenID: "dcdash@pve!test",
			TokenSecret: config.NewSecret("secret"), Timeout: config.Duration(2 * time.Second),
			Console: config.ProxmoxConsole{Username: "console@pve", Password: config.NewSecret("pw")},
		}, zerolog.Nop())
		if err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	svc := NewService([]*Cluster{cluster}, auditLog, nil, zerolog.Nop())
	mux := http.NewServeMux()
	svc.Register(mux)
	authz := auth.NewAuthorizer(rbac.NewResolver("", nil), auditLog)
	// The authorizer runs first (it records denials); the test users are
	// injected after it, because it would resolve them from groups.
	inject := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("X-Test-User")
		if user == "" {
			user = r.URL.Query().Get("user") // websockets
		}
		if perms, ok := testUsers[user]; ok {
			set, _ := rbac.ParseSet(perms)
			ctx := session.WithSession(r.Context(), &session.Session{ID: "sess-" + user, Username: user, Source: "ldap", Stage: session.StageAuthenticated})
			ctx = auth.WithSubject(ctx, &rbac.Subject{Username: user, Permissions: set})
			r = r.WithContext(ctx)
		}
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(authz.Middleware(inject))
	t.Cleanup(srv.Close)
	return &apiEnv{fake: fake, srv: srv, audit: auditLog}
}

func (e *apiEnv) do(t *testing.T, user, method, path string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if out != nil && res.StatusCode < 300 {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, body)
		}
	}
	return res.StatusCode
}

func (e *apiEnv) entries(t *testing.T, action string) []audit.Entry {
	t.Helper()
	l, err := e.audit.Query(context.Background(), audit.Filter{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const base = "/api/v1/proxmox/clusters"

func TestClusterListAndInventory(t *testing.T) {
	e := newAPIEnv(t)
	var cl ClusterList
	if code := e.do(t, "viewer", "GET", base, &cl); code != 200 || len(cl.Clusters) != 1 || cl.Clusters[0].Name != "lab" || !cl.Clusters[0].Terminals {
		t.Errorf("viewer clusters: %d %+v", code, cl)
	}
	if code := e.do(t, "other", "GET", base, &cl); code != 200 || len(cl.Clusters) != 0 {
		t.Errorf("other clusters: %d %+v", code, cl)
	}
	if code := e.do(t, "", "GET", base, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}

	var inv Inventory
	if code := e.do(t, "viewer", "GET", base+"/lab/resources", &inv); code != 200 ||
		len(inv.Nodes) != 2 || len(inv.Guests) != 3 || len(inv.Storage) != 2 {
		t.Fatalf("viewer inventory: %d %+v", code, inv)
	}
	if g := inv.Guests[0]; g.VMID != 101 || g.Node != "pve1" || len(g.Tags) != 2 || g.Tags[1] != "web" || g.Status != "running" {
		t.Errorf("guest: %+v", g)
	}
	// The operator sees only what its permissions cover.
	if code := e.do(t, "operator", "GET", base+"/lab/resources", &inv); code != 200 ||
		len(inv.Nodes) != 1 || len(inv.Guests) != 2 || len(inv.Storage) != 0 {
		t.Errorf("operator inventory: %d %+v", code, inv)
	}
	if code := e.do(t, "other", "GET", base+"/lab/resources", nil); code != 403 {
		t.Errorf("other: %d", code)
	}
	if code := e.do(t, "admin", "GET", base+"/nope/resources", nil); code != 404 {
		t.Errorf("unknown cluster: %d", code)
	}
	if len(e.entries(t, "authz.denied")) != 1 {
		t.Error("denial not audited")
	}
}

func TestGuestDetail(t *testing.T) {
	e := newAPIEnv(t)
	var d GuestDetail
	if code := e.do(t, "viewer", "GET", base+"/lab/guests/qemu/102", &d); code != 200 ||
		d.Guest.Node != "pve2" || d.Config["name"] != "db" || d.Status["status"] != "stopped" || d.Config["cipassword"] != nil {
		t.Errorf("detail: %d %+v", code, d)
	}
	for path, want := range map[string]int{
		"/lab/guests/qemu/999":  404,
		"/lab/guests/vm/101":    400,
		"/lab/guests/qemu/abc":  400,
		"/lab/guests/qemu/-1":   400,
		"/other/guests/qemu/99": 404,
	} {
		if code := e.do(t, "admin", "GET", base+path, nil); code != want {
			t.Errorf("%s: %d, want %d", path, code, want)
		}
	}
	if code := e.do(t, "operator", "GET", base+"/lab/guests/lxc/200", nil); code != 403 {
		t.Errorf("operator lxc: %d", code)
	}
}

func TestActionsAndTasks(t *testing.T) {
	e := newAPIEnv(t)
	var ref TaskRef
	if code := e.do(t, "operator", "POST", base+"/lab/guests/qemu/102/actions/start", &ref); code != 202 || ref.Node != "pve2" {
		t.Fatalf("start: %d %+v", code, ref)
	}
	if g, _ := e.fake.Guest(TypeQEMU, 102); g.Status != "running" {
		t.Errorf("status %s", g.Status)
	}
	l := e.entries(t, "proxmox.guest.start")
	if len(l) != 1 || l[0].Actor != "operator" || l[0].Target != "lab/qemu/102" || l[0].Result != audit.ResultSuccess || l[0].Details["upid"] != ref.UPID {
		t.Errorf("audit: %+v", l)
	}

	var task Task
	if code := e.do(t, "operator", "GET", base+"/lab/tasks/"+ref.UPID+"?log=1", &task); code != 200 ||
		task.Running || !task.OK || task.Type != "qmstart" || len(task.Log) != 2 {
		t.Errorf("task: %d %+v", code, task)
	}
	// The console user may not read tasks of VM 102.
	if code := e.do(t, "console", "GET", base+"/lab/tasks/"+ref.UPID, nil); code != 403 {
		t.Errorf("task without read: %d", code)
	}
	if code := e.do(t, "viewer", "GET", base+"/lab/tasks/UPID:bad", nil); code != 400 {
		t.Errorf("bad upid: %d", code)
	}

	// read is not enough for actions; unknown actions are rejected.
	if code := e.do(t, "viewer", "POST", base+"/lab/guests/qemu/101/actions/stop", nil); code != 403 {
		t.Errorf("viewer stop: %d", code)
	}
	if code := e.do(t, "operator", "POST", base+"/lab/guests/lxc/200/actions/stop", nil); code != 403 {
		t.Errorf("operator lxc stop: %d", code)
	}
	if code := e.do(t, "admin", "POST", base+"/lab/guests/qemu/101/actions/destroy", nil); code != 400 {
		t.Errorf("unknown action: %d", code)
	}

	// Proxmox refuses: the failure is audited and reported as upstream error.
	e.fake.Set(func(s *pvefake.Settings) { s.Denied["qemu/101"] = true })
	if code := e.do(t, "admin", "POST", base+"/lab/guests/qemu/101/actions/stop", nil); code != 502 {
		t.Errorf("denied by proxmox: %d", code)
	}
	if l := e.entries(t, "proxmox.guest.stop"); len(l) != 1 || l[0].Result != audit.ResultFailure {
		t.Errorf("failure audit: %+v", l)
	}
}

func (e *apiEnv) dial(t *testing.T, user, id string) (*websocket.Conn, int) {
	t.Helper()
	u := strings.Replace(e.srv.URL, "http://", "ws://", 1) + "/api/v1/proxmox/console/" + id + "?user=" + user
	h := http.Header{"Origin": {e.srv.URL}}
	ws, res, err := websocket.DefaultDialer.Dial(u, h)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		if res == nil {
			t.Fatal(err)
		}
		return nil, res.StatusCode
	}
	return ws, http.StatusSwitchingProtocols
}

func TestConsoleProxy(t *testing.T) {
	e := newAPIEnv(t)

	if code := e.do(t, "viewer", "POST", base+"/lab/guests/lxc/200/console", nil); code != 403 {
		t.Errorf("viewer console: %d", code)
	}
	var ct ConsoleTicket
	if code := e.do(t, "console", "POST", base+"/lab/guests/lxc/200/console", &ct); code != 200 || ct.Kind != ConsoleTerm || ct.Password != "" {
		t.Fatalf("console: %d %+v", code, ct)
	}
	// The ticket is bound to the session.
	if _, code := e.dial(t, "admin", ct.ID); code != 404 {
		t.Errorf("other session: %d", code)
	}
	if code := e.do(t, "console", "POST", base+"/lab/guests/lxc/200/console", &ct); code != 200 {
		t.Fatal(code)
	}
	ws, code := e.dial(t, "console", ct.ID)
	if code != 101 {
		t.Fatalf("dial: %d", code)
	}
	_ = ws.WriteMessage(websocket.BinaryMessage, []byte("0:5:hello"))
	if got := readMsg(t, ws); got != "hello" {
		t.Errorf("echo %q", got)
	}
	_ = ws.Close()
	// One-time ticket.
	if _, code := e.dial(t, "console", ct.ID); code != 404 {
		t.Errorf("reuse: %d", code)
	}
	waitFor(t, func() bool { return len(e.entries(t, "proxmox.console.end")) == 1 })
	end := e.entries(t, "proxmox.console.end")[0]
	if end.Target != "lab/lxc/200" || end.Details["bytesIn"] != float64(9) || end.Details["bytesOut"] != float64(5) {
		t.Errorf("end entry: %+v", end)
	}
	if l := e.entries(t, "proxmox.console.start"); len(l) != 1 || l[0].Actor != "console" {
		t.Errorf("start entries: %+v", l)
	}

	// VNC returns the VNC password for noVNC.
	if code := e.do(t, "admin", "POST", base+"/lab/guests/qemu/101/console", &ct); code != 200 || ct.Kind != ConsoleVNC || ct.Password == "" {
		t.Errorf("vnc: %d %+v", code, ct)
	}
	ws, code = e.dial(t, "admin", ct.ID)
	if code != 101 {
		t.Fatalf("vnc dial: %d", code)
	}
	if got := readMsg(t, ws); got != "RFB 003.008\n" {
		t.Errorf("vnc greeting %q", got)
	}
	_ = ws.Close()

	// Node shells need admin on the node.
	if code := e.do(t, "operator", "POST", base+"/lab/nodes/pve1/shell", nil); code != 403 {
		t.Errorf("operator shell: %d", code)
	}
	if code := e.do(t, "admin", "POST", base+"/lab/nodes/pve1/shell", &ct); code != 200 || ct.Kind != ConsoleTerm {
		t.Errorf("admin shell: %d %+v", code, ct)
	}
}

func TestConsoleRejectedByProxmox(t *testing.T) {
	e := newAPIEnv(t)
	e.fake.Set(func(s *pvefake.Settings) { s.RejectTerminals = true })
	var ct ConsoleTicket
	if code := e.do(t, "console", "POST", base+"/lab/guests/lxc/200/console", &ct); code != 200 {
		t.Fatal(code)
	}
	if _, code := e.dial(t, "console", ct.ID); code != 502 {
		t.Errorf("rejected login: %d", code)
	}
	if l := e.entries(t, "proxmox.console.open"); len(l) != 1 || l[0].Result != audit.ResultFailure {
		t.Errorf("audit: %+v", l)
	}
}

func TestTerminalsWithoutConsoleUser(t *testing.T) {
	e := newAPIEnvWith(t, false)
	var cl ClusterList
	if code := e.do(t, "viewer", "GET", base, &cl); code != 200 || len(cl.Clusters) != 1 || cl.Clusters[0].Terminals {
		t.Errorf("clusters: %d %+v", code, cl)
	}
	if code := e.do(t, "console", "POST", base+"/lab/guests/lxc/200/console", nil); code != 409 {
		t.Errorf("lxc terminal: %d", code)
	}
	if code := e.do(t, "admin", "POST", base+"/lab/nodes/pve1/shell", nil); code != 409 {
		t.Errorf("node shell: %d", code)
	}
	// VNC works with the API token.
	var ct ConsoleTicket
	if code := e.do(t, "admin", "POST", base+"/lab/guests/qemu/101/console", &ct); code != 200 || ct.Kind != ConsoleVNC {
		t.Fatalf("vnc: %d %+v", code, ct)
	}
	ws, code := e.dial(t, "admin", ct.ID)
	if code != 101 {
		t.Fatalf("vnc dial: %d", code)
	}
	_ = ws.Close()
}

func TestConsoleTicketExpiry(t *testing.T) {
	ct := newConsoleTickets()
	now := time.Now()
	ct.now = func() time.Time { return now }
	id, err := ct.add(&pendingConsole{sessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(consoleTicketTTL + time.Second)
	if ct.take(id) != nil {
		t.Error("expired ticket accepted")
	}
	if _, err := ct.add(&pendingConsole{}); err != nil || len(ct.pending) != 1 {
		t.Errorf("expired tickets not removed: %d", len(ct.pending))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 100 {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func TestLiveStatus(t *testing.T) {
	for _, c := range []struct {
		st   map[string]any
		want string
	}{
		{map[string]any{"status": "running", "qmpstatus": "paused"}, "paused"},
		{map[string]any{"status": "running"}, "running"},
		{map[string]any{"status": "stopped", "qmpstatus": ""}, "stopped"},
		{map[string]any{}, ""},
	} {
		if got := liveStatus(c.st); got != c.want {
			t.Errorf("liveStatus(%v) = %q, want %q", c.st, got, c.want)
		}
	}
}
