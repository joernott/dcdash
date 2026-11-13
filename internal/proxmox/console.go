// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Console kinds.
const (
	ConsoleVNC  = "vnc"  // noVNC (RFB) for virtual machines
	ConsoleTerm = "term" // xterm.js (termproxy) for containers and node shells
)

// Console is a console session prepared by vncproxy or termproxy. It must be
// connected within about 10 seconds, otherwise Proxmox closes it.
type Console struct {
	Kind string
	// Password is the VNC password (the vncticket) for noVNC. It is only
	// usable through the authenticated vncwebsocket, so it may be passed to
	// the browser. Empty for terminals.
	Password string

	path   string // .../vncwebsocket
	port   string
	ticket string
	user   string
	ep     *endpoint
	auth   authFunc
}

type proxyResponse struct {
	Port   json.Number `json:"port"`
	Ticket string      `json:"ticket"`
	User   string      `json:"user"`
	UPID   string      `json:"upid"`
}

// ErrTerminalUnavailable means that a terminal (container console or node
// shell) was requested, but no console user is configured. Proxmox does not
// accept API tokens for the termproxy login (see ADR 0001).
var ErrTerminalUnavailable = errors.New("proxmox: terminals need a console user")

// Terminals reports whether container terminals and node shells are
// available: they need the console user.
func (c *Cluster) Terminals() bool { return c.HasConsoleUser() }

// consoleAuth returns the authentication for a console. VNC uses the API
// token. Terminals use the ticket of the console user, because termproxy
// rejects logins with tickets issued to an API token.
func (c *Cluster) consoleAuth(ctx context.Context, kind string) (authFunc, error) {
	if kind == ConsoleVNC {
		return c.tokenAuth, nil
	}
	if !c.HasConsoleUser() {
		return nil, ErrTerminalUnavailable
	}
	return c.ticketAuth(ctx)
}

// OpenGuestConsole prepares the console of a guest: VNC for virtual machines,
// a terminal for containers.
func (c *Cluster) OpenGuestConsole(ctx context.Context, node, typ string, vmid int) (*Console, error) {
	p, err := guestPath(node, typ, vmid)
	if err != nil {
		return nil, err
	}
	if typ == TypeQEMU {
		return c.openConsole(ctx, p, ConsoleVNC, url.Values{"websocket": {"1"}})
	}
	return c.openConsole(ctx, p, ConsoleTerm, nil)
}

// OpenNodeShell prepares a shell on a node (needs Sys.Console in Proxmox).
func (c *Cluster) OpenNodeShell(ctx context.Context, node string) (*Console, error) {
	if !nodeNamePattern.MatchString(node) {
		return nil, fmt.Errorf("proxmox: invalid node %q", node)
	}
	return c.openConsole(ctx, "/nodes/"+node, ConsoleTerm, nil)
}

func (c *Cluster) openConsole(ctx context.Context, base, kind string, form url.Values) (*Console, error) {
	a, err := c.consoleAuth(ctx, kind)
	if err != nil {
		return nil, err
	}
	return c.openConsoleAuth(ctx, base, kind, form, a)
}

func (c *Cluster) openConsoleAuth(ctx context.Context, base, kind string, form url.Values, a authFunc) (*Console, error) {
	endpointName := "/vncproxy"
	if kind == ConsoleTerm {
		endpointName = "/termproxy"
	}
	var pr proxyResponse
	ep, err := c.doAuth(ctx, a, http.MethodPost, base+endpointName, form, &pr)
	if err != nil {
		if IsStatus(err, http.StatusUnauthorized) {
			c.resetTicket()
		}
		return nil, err
	}
	con := &Console{Kind: kind, path: base + "/vncwebsocket", port: pr.Port.String(), ticket: pr.Ticket, user: pr.User, ep: ep, auth: a}
	if kind == ConsoleVNC {
		con.Password = pr.Ticket
	}
	return con, nil
}

func (c *Cluster) resetTicket() {
	c.ticketMu.Lock()
	c.ticket = nil
	c.ticketMu.Unlock()
}

// Dial connects the console websocket on the endpoint that prepared it. For
// terminals it also performs the termproxy login, so the connection carries
// only terminal data afterwards.
func (con *Console) Dial(ctx context.Context) (*websocket.Conn, error) {
	u := strings.Replace(con.ep.base, "https://", "wss://", 1) + "/api2/json" + con.path +
		"?" + url.Values{"port": {con.port}, "vncticket": {con.ticket}}.Encode()
	h := http.Header{}
	con.auth(h)
	d := websocket.Dialer{
		TLSClientConfig:  con.ep.tls,
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
		// Proxmox closes the connection without the "binary" subprotocol.
		Subprotocols: []string{"binary"},
	}
	ws, res, err := d.DialContext(ctx, u, h)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		if res != nil {
			return nil, &Error{Status: res.StatusCode, Message: strings.TrimSpace(strings.TrimPrefix(res.Status, fmt.Sprint(res.StatusCode)))}
		}
		return nil, err
	}
	if con.Kind == ConsoleTerm {
		if err := termLogin(ctx, ws, con.user, con.ticket); err != nil {
			_ = ws.Close()
			return nil, err
		}
	}
	return ws, nil
}

// ErrConsoleRejected means that termproxy did not accept the login.
var ErrConsoleRejected = errors.New("proxmox: console login rejected")

// termLogin sends "user:ticket\n" and expects "OK".
func termLogin(ctx context.Context, ws *websocket.Conn, user, ticket string) error {
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = ws.SetWriteDeadline(deadline)
	_ = ws.SetReadDeadline(deadline)
	if err := ws.WriteMessage(websocket.BinaryMessage, []byte(user+":"+ticket+"\n")); err != nil {
		return err
	}
	_, msg, err := ws.ReadMessage()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConsoleRejected, err)
	}
	if !bytes.HasPrefix(msg, []byte("OK")) {
		return ErrConsoleRejected
	}
	_ = ws.SetWriteDeadline(time.Time{})
	_ = ws.SetReadDeadline(time.Time{})
	return nil
}
