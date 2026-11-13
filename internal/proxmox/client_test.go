// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox/pvefake"
	"github.com/joernott/dcdash/internal/tlsutil"
)

const testToken = "dcdash@pve!test=secret"

func testGuests() []pvefake.Guest {
	return []pvefake.Guest{
		{Type: TypeQEMU, VMID: 101, Name: "web", Node: "pve1", Status: "running", Tags: "prod;web"},
		{Type: TypeQEMU, VMID: 102, Name: "db", Node: "pve2", Status: "stopped"},
		{Type: TypeLXC, VMID: 200, Name: "proxy", Node: "pve1", Status: "running"},
	}
}

// fakeServer starts a fake Proxmox API over TLS.
func fakeServer(t *testing.T) (*pvefake.Server, *httptest.Server) {
	t.Helper()
	f := pvefake.New(testToken, []string{"pve1", "pve2"}, testGuests()...)
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func endpointFor(srv *httptest.Server) config.ProxmoxEndpoint {
	return config.ProxmoxEndpoint{URL: srv.URL, Fingerprint: tlsutil.Fingerprint(srv.Certificate().Raw)}
}

func newCluster(t *testing.T, endpoints ...config.ProxmoxEndpoint) *Cluster {
	t.Helper()
	c, err := New(config.ProxmoxCluster{
		Name: "lab", Endpoints: endpoints, TokenID: "dcdash@pve!test", TokenSecret: config.NewSecret("secret"),
		Timeout: config.Duration(2 * time.Second),
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVersionAndCheck(t *testing.T) {
	_, srv := fakeServer(t)
	c := newCluster(t, endpointFor(srv))
	v, err := c.Version(t.Context())
	if err != nil || v.Version != "9.0.11" {
		t.Fatalf("version %+v, %v", v, err)
	}
	detail, err := c.Check(t.Context())
	if err != nil || !strings.Contains(detail, "9.0.11") || !strings.Contains(detail, srv.URL) {
		t.Errorf("check %q, %v", detail, err)
	}
}

func TestWrongTokenIsNotFailover(t *testing.T) {
	f1, srv1 := fakeServer(t)
	f2, srv2 := fakeServer(t)
	f1.Set(func(s *pvefake.Settings) { s.Token = "other@pve!x=y" })
	f2.Set(func(s *pvefake.Settings) { s.Token = "other@pve!x=y" })
	c := newCluster(t, endpointFor(srv1), endpointFor(srv2))
	_, err := c.Version(t.Context())
	if !IsStatus(err, http.StatusUnauthorized) || f2.Requests() != 0 {
		t.Errorf("err %v, requests to second endpoint %d", err, f2.Requests())
	}
}

func TestFailover(t *testing.T) {
	_, dead := fakeServer(t)
	deadEP := endpointFor(dead)
	dead.Close()
	f2, srv2 := fakeServer(t)
	c := newCluster(t, deadEP, endpointFor(srv2))
	if _, err := c.Version(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.active.Load() != 1 {
		t.Errorf("active endpoint %d", c.active.Load())
	}
	// The working endpoint is used first from now on.
	if _, err := c.Resources(t.Context()); err != nil || f2.Requests() != 2 {
		t.Errorf("resources: %v, requests %d", err, f2.Requests())
	}
}

func TestFailoverOnGatewayError(t *testing.T) {
	f1, srv1 := fakeServer(t)
	f1.Set(func(s *pvefake.Settings) { s.Fail = http.StatusServiceUnavailable })
	_, srv2 := fakeServer(t)
	c := newCluster(t, endpointFor(srv1), endpointFor(srv2))
	if _, err := c.Version(t.Context()); err != nil {
		t.Fatal(err)
	}
	// An API error is not retried on another endpoint.
	c.active.Store(0)
	f1.Set(func(s *pvefake.Settings) { s.Fail = http.StatusInternalServerError })
	if _, err := c.Version(t.Context()); !IsStatus(err, http.StatusInternalServerError) {
		t.Errorf("500: %v", err)
	}
}

func TestAllEndpointsDown(t *testing.T) {
	_, srv := fakeServer(t)
	ep := endpointFor(srv)
	ep.Fingerprint = strings.Repeat("00:", 31) + "00" // pin mismatch
	c := newCluster(t, ep)
	_, err := c.Version(t.Context())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("err %v", err)
	}
}

func TestInventoryActionsAndTasks(t *testing.T) {
	f, srv := fakeServer(t)
	c := newCluster(t, endpointFor(srv))
	ctx := t.Context()
	g, err := c.FindGuest(ctx, TypeQEMU, 102)
	if err != nil || g.Node != "pve2" || g.Name != "db" {
		t.Fatalf("find: %+v, %v", g, err)
	}
	if _, err := c.FindGuest(ctx, TypeLXC, 101); !errors.Is(err, ErrNotFound) {
		t.Errorf("lxc 101: %v", err)
	}
	cfg, err := c.GuestConfig(ctx, "pve2", TypeQEMU, 102)
	if err != nil || cfg["name"] != "db" || cfg["cipassword"] != nil || cfg["digest"] != nil {
		t.Errorf("config %v, %v", cfg, err)
	}
	if st, err := c.GuestStatus(ctx, "pve2", TypeQEMU, 102); err != nil || st["status"] != "stopped" {
		t.Errorf("status %v, %v", st, err)
	}

	upid, err := c.Action(ctx, "pve2", TypeQEMU, 102, "start")
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := f.Guest(TypeQEMU, 102); g.Status != "running" {
		t.Errorf("status after start: %s", g.Status)
	}
	u, err := ParseUPID(upid)
	if err != nil || u.Node != "pve2" || u.VMID() != 102 || u.Type != "qmstart" || u.User != "dcdash@pve!test" {
		t.Fatalf("upid %+v, %v", u, err)
	}
	st, err := c.Task(ctx, u)
	if err != nil || st.Status != "stopped" || st.ExitState != "OK" {
		t.Errorf("task %+v, %v", st, err)
	}
	log, err := c.TaskLog(ctx, u, 0, 50)
	if err != nil || len(log) != 2 || log[1].T != "TASK OK" {
		t.Errorf("log %+v, %v", log, err)
	}

	// Resume of a running VM fails in the task, not in the request.
	upid, err = c.Action(ctx, "pve2", TypeQEMU, 102, "resume")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = ParseUPID(upid)
	if st, _ := c.Task(ctx, u); st.ExitState != "VM 102 not paused" {
		t.Errorf("failed task: %+v", st)
	}

	if _, err := c.Action(ctx, "pve2", TypeQEMU, 102, "delete"); err == nil {
		t.Error("unknown action accepted")
	}
	if _, err := c.Action(ctx, "../x", TypeQEMU, 102, "start"); err == nil {
		t.Error("invalid node accepted")
	}
}

func TestParseUPID(t *testing.T) {
	for _, s := range []string{"", "UPID:pve1:1", "TASK:pve1:0:0:0:qmstart:101:root@pam:", "UPID:../x:0:0:0:qmstart:101:root@pam:"} {
		if _, err := ParseUPID(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	u, err := ParseUPID("UPID:pve1:0000A1B2:00C0FFEE:6AF6FD47:vncshell::root@pam:")
	if err != nil || u.VMID() != 0 || u.Type != "vncshell" {
		t.Errorf("node task %+v, %v", u, err)
	}
}

func readMsg(t *testing.T, ws *websocket.Conn) string {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	return string(msg)
}

func TestConsoles(t *testing.T) {
	_, srv := fakeServer(t)
	c := newCluster(t, endpointFor(srv)) // API token only
	ctx := t.Context()
	if c.Terminals() {
		t.Error("terminals without console user")
	}

	vnc, err := c.OpenGuestConsole(ctx, "pve1", TypeQEMU, 101)
	if err != nil || vnc.Kind != ConsoleVNC || vnc.Password == "" {
		t.Fatalf("vnc %+v, %v", vnc, err)
	}
	ws, err := vnc.Dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := readMsg(t, ws); got != "RFB 003.008\n" {
		t.Errorf("vnc greeting %q", got)
	}
	// The fake continues with RFB: one security type (None).
	_ = ws.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n"))
	if got := readMsg(t, ws); got != "\x01\x01" {
		t.Errorf("security types %q", got)
	}
	_ = ws.Close()
	if _, err := vnc.Dial(ctx); err == nil {
		t.Error("console ticket used twice")
	}

	// Terminals need the console user.
	if _, err := c.OpenGuestConsole(ctx, "pve1", TypeLXC, 200); !errors.Is(err, ErrTerminalUnavailable) {
		t.Errorf("lxc terminal with token: %v", err)
	}
	if _, err := c.OpenNodeShell(ctx, "pve1"); !errors.Is(err, ErrTerminalUnavailable) {
		t.Errorf("node shell with token: %v", err)
	}
	if _, err := c.OpenNodeShell(ctx, "pve1/../x"); err == nil || errors.Is(err, ErrTerminalUnavailable) {
		t.Errorf("invalid node: %v", err)
	}
}

// Proxmox issues termproxy tickets to API tokens, but rejects the login with
// them on the websocket. That is why terminals need the console user.
func TestTerminalLoginWithTokenRejected(t *testing.T) {
	_, srv := fakeServer(t)
	c := newCluster(t, endpointFor(srv))
	term, err := c.openConsoleAuth(t.Context(), "/nodes/pve1/lxc/200", ConsoleTerm, nil, c.tokenAuth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := term.Dial(t.Context()); !errors.Is(err, ErrConsoleRejected) {
		t.Errorf("term with token: %v", err)
	}
}

func TestConsoleUser(t *testing.T) {
	f, srv := fakeServer(t)
	f.Set(func(s *pvefake.Settings) { s.User, s.Password = "console@pve", "pw" })
	cfg := config.ProxmoxCluster{
		Name: "lab", Endpoints: []config.ProxmoxEndpoint{endpointFor(srv)}, TokenID: "dcdash@pve!test",
		TokenSecret: config.NewSecret("secret"), Console: config.ProxmoxConsole{Username: "console@pve", Password: config.NewSecret("pw")},
	}
	c, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c.now = func() time.Time { return now }
	if !c.Terminals() {
		t.Error("no terminals with console user")
	}
	for _, typ := range []string{TypeLXC, TypeQEMU} {
		vmid := map[string]int{TypeLXC: 200, TypeQEMU: 101}[typ]
		con, err := c.OpenGuestConsole(t.Context(), "pve1", typ, vmid)
		if err != nil {
			t.Fatal(err)
		}
		ws, err := con.Dial(t.Context())
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if typ == TypeLXC {
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte("0:2:ls"))
			if got := readMsg(t, ws); got != "ls" {
				t.Errorf("echo %q", got)
			}
		}
		_ = ws.Close()
	}
	shell, err := c.OpenNodeShell(t.Context(), "pve1")
	if err != nil || shell.Kind != ConsoleTerm {
		t.Fatalf("shell %+v, %v", shell, err)
	}
	// The ticket is reused until it expires, then renewed.
	logins := f.Requests()
	if _, err := c.OpenGuestConsole(t.Context(), "pve1", TypeLXC, 200); err != nil || f.Requests() != logins+1 {
		t.Errorf("reuse: %v, requests %d -> %d", err, logins, f.Requests())
	}
	now = now.Add(ticketLifetime + time.Second)
	if _, err := c.OpenGuestConsole(t.Context(), "pve1", TypeLXC, 200); err != nil || f.Requests() != logins+3 {
		t.Errorf("renew: %v, requests %d -> %d", err, logins, f.Requests())
	}

	// A wrong password is not retried on other endpoints.
	f.Set(func(s *pvefake.Settings) { s.Password = "changed" })
	c.resetTicket()
	if _, err := c.OpenGuestConsole(context.Background(), "pve1", TypeLXC, 200); !IsStatus(err, http.StatusUnauthorized) {
		t.Errorf("wrong password: %v", err)
	}
}
