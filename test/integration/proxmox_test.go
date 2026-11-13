// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package integration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/proxmox"
)

// Tests against a real Proxmox VE cluster. They run when these variables are
// set (see scripts/integration-test.sh):
//
//	DCDASH_IT_PVE_URL, _FINGERPRINT, _TOKEN_ID, _TOKEN_SECRET  API access
//	DCDASH_IT_PVE_CONSOLE_USER, _CONSOLE_PASSWORD              optional console user
//	DCDASH_IT_PVE_CONSOLE_GUESTS  e.g. "qemu/201,lxc/202": consoles are opened
//	                              (handshake only, no input is sent)
//	DCDASH_IT_PVE_POWER_GUEST     e.g. "qemu/110": a guest that may be started,
//	                              suspended, resumed and stopped. A stopped
//	                              guest is started and stopped again at the end.

func pveCluster(t *testing.T, withConsoleUser bool) *proxmox.Cluster {
	t.Helper()
	cfg := config.ProxmoxCluster{
		Name: "it",
		Endpoints: []config.ProxmoxEndpoint{{
			URL:         requireEnv(t, "DCDASH_IT_PVE_URL"),
			Fingerprint: requireEnv(t, "DCDASH_IT_PVE_FINGERPRINT"),
		}},
		TokenID:     requireEnv(t, "DCDASH_IT_PVE_TOKEN_ID"),
		TokenSecret: config.NewSecret(requireEnv(t, "DCDASH_IT_PVE_TOKEN_SECRET")),
		Timeout:     config.Duration(15 * time.Second),
	}
	if withConsoleUser {
		cfg.Console = config.ProxmoxConsole{
			Username: requireEnv(t, "DCDASH_IT_PVE_CONSOLE_USER"),
			Password: config.NewSecret(requireEnv(t, "DCDASH_IT_PVE_CONSOLE_PASSWORD")),
		}
	}
	c, err := proxmox.New(cfg, zerolog.New(zerolog.NewTestWriter(t)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// parseGuests parses "qemu/201,lxc/202".
func parseGuests(t *testing.T, s string) [][2]string {
	t.Helper()
	var out [][2]string
	for _, g := range strings.Split(s, ",") {
		typ, id, ok := strings.Cut(strings.TrimSpace(g), "/")
		if !ok || !proxmox.IsGuestType(typ) {
			t.Fatalf("invalid guest %q (want qemu/<vmid> or lxc/<vmid>)", g)
		}
		out = append(out, [2]string{typ, id})
	}
	return out
}

func TestProxmoxRead(t *testing.T) {
	c := pveCluster(t, false)
	ctx := t.Context()
	detail, err := c.Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(detail)
	rs, err := c.Resources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	var first *proxmox.Resource
	for i, r := range rs {
		count[r.Type]++
		if first == nil && proxmox.IsGuestType(r.Type) {
			first = &rs[i]
		}
	}
	t.Logf("resources visible to the token: %v", count)
	if first == nil {
		t.Skip("the token sees no guests (missing VM.Audit?)")
	}
	cfg, err := c.GuestConfig(ctx, first.Node, first.Type, first.VMID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["cipassword"]; ok {
		t.Error("cipassword returned")
	}
	st, err := c.GuestStatus(ctx, first.Node, first.Type, first.VMID)
	if err != nil || st["status"] == nil {
		t.Fatalf("status %v, %v", st, err)
	}
}

// openConsole opens a console and checks the first message: the RFB version
// for VNC; terminals are already logged in by Dial.
func openConsole(t *testing.T, c *proxmox.Cluster, typ string, vmid int) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	res, err := c.FindGuest(ctx, typ, vmid)
	if err != nil {
		return err
	}
	con, err := c.OpenGuestConsole(ctx, res.Node, typ, vmid)
	if err != nil {
		return err
	}
	ws, err := con.Dial(ctx)
	if err != nil {
		return err
	}
	defer ws.Close()
	if con.Kind == proxmox.ConsoleVNC {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(msg), "RFB ") {
			t.Errorf("VNC greeting %q", msg)
		}
	}
	_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return nil
}

func TestProxmoxConsoles(t *testing.T) {
	guests := parseGuests(t, requireEnv(t, "DCDASH_IT_PVE_CONSOLE_GUESTS"))
	modes := []struct {
		name        string
		consoleUser bool
	}{{"token", false}, {"console user", true}}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			c := pveCluster(t, m.consoleUser)
			for _, g := range guests {
				vmid, _ := strconv.Atoi(g[1])
				err := openConsole(t, c, g[0], vmid)
				t.Logf("%s %s/%d: %v", m.name, g[0], vmid, errOK(err))
				if err != nil {
					t.Errorf("%s/%d: %v", g[0], vmid, err)
				}
			}
		})
	}
}

func errOK(err error) string {
	if err == nil {
		return "OK"
	}
	return err.Error()
}

// waitTask waits until a task has finished and returns its exit status.
func waitTask(t *testing.T, c *proxmox.Cluster, upid string) string {
	t.Helper()
	u, err := proxmox.ParseUPID(upid)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := c.Task(context.Background(), u)
		if err != nil {
			t.Fatal(err)
		}
		if st.Status != "running" {
			return st.ExitState
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("task %s did not finish", upid)
	return ""
}

func TestProxmoxPowerActions(t *testing.T) {
	g := os.Getenv("DCDASH_IT_PVE_POWER_GUEST")
	if g == "" {
		t.Skip("DCDASH_IT_PVE_POWER_GUEST not set")
	}
	guest := parseGuests(t, g)[0]
	vmid, _ := strconv.Atoi(guest[1])
	c := pveCluster(t, false)
	res, err := c.FindGuest(t.Context(), guest[0], vmid)
	if err != nil {
		t.Fatal(err)
	}
	run := func(action string) {
		t.Helper()
		upid, err := c.Action(context.Background(), res.Node, guest[0], vmid, action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if exit := waitTask(t, c, upid); exit != "OK" {
			t.Fatalf("%s: task exit %q", action, exit)
		}
		log, err := c.TaskLog(context.Background(), mustUPID(t, upid), 0, 50)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d log lines", action, len(log))
	}
	switch res.Status {
	case "running":
	case "stopped":
		// The guest is left as it was found: started here, stopped at the end.
		run("start")
		t.Cleanup(func() { run("stop") })
	default:
		t.Skipf("%s is %s", g, res.Status)
	}
	// The console of the power guest is tested here, while it runs.
	if guest[0] == proxmox.TypeQEMU {
		for _, m := range []struct {
			name string
			user bool
		}{{"token", false}, {"console user", true}} {
			if m.user && os.Getenv("DCDASH_IT_PVE_CONSOLE_USER") == "" {
				continue
			}
			err := openConsole(t, pveCluster(t, m.user), guest[0], vmid)
			t.Logf("console (%s): %s", m.name, errOK(err))
			if err != nil {
				t.Errorf("console (%s): %v", m.name, err)
			}
		}
	}
	run("suspend")
	run("resume")
}

func mustUPID(t *testing.T, s string) proxmox.UPID {
	t.Helper()
	u, err := proxmox.ParseUPID(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
