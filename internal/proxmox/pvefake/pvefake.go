// SPDX-License-Identifier: GPL-3.0-or-later

// Package pvefake is a small in-memory fake of the Proxmox VE API for tests:
// cluster resources, guest status and config, power actions with tasks, and
// consoles (vncproxy/termproxy with vncwebsocket). Terminals echo their input;
// VNC sends the RFB version and then echoes.
package pvefake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Guest is a virtual machine or container of the fake.
type Guest struct {
	Type   string
	VMID   int
	Name   string
	Node   string
	Status string // running, stopped, paused
	Tags   string
}

// Settings control the behavior of the fake. Change them with Server.Set.
type Settings struct {
	// Token is the accepted API token "user@realm!name=secret".
	Token string
	// User and Password are accepted by /access/ticket (empty: no login).
	User, Password string
	// TokenConsole controls whether consoles accept the API token.
	TokenConsole bool
	// Fail makes every API request fail with this status (e.g. 503).
	Fail int
	// Denied lists guests ("qemu/101") the token may not operate or open.
	Denied map[string]bool
}

// Server is the fake. Serve it with httptest.NewTLSServer.
type Server struct {
	mu       sync.Mutex
	set      Settings
	nodes    []string
	guests   []*Guest
	tasks    map[string]*task
	consoles map[string]console
	seq      int
	requests int
}

type task struct {
	upid, node, typ, id, user string
	start, end                time.Time
	exit                      string
	log                       []string
}

type console struct {
	port, ticket, user string
	term               bool
}

// New creates a fake with the given nodes and guests.
func New(token string, nodes []string, guests ...Guest) *Server {
	s := &Server{set: Settings{Token: token, Denied: map[string]bool{}}, nodes: nodes, tasks: map[string]*task{}, consoles: map[string]console{}}
	for i := range guests {
		g := guests[i]
		s.guests = append(s.guests, &g)
	}
	return s
}

// Set changes the settings.
func (s *Server) Set(f func(*Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.set)
}

// Requests returns the number of API requests received.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Guest returns a copy of a guest's state.
func (s *Server) Guest(typ string, vmid int) (Guest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.guests {
		if g.Type == typ && g.VMID == vmid {
			return *g, true
		}
	}
	return Guest{}, false
}

func writeData(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	// Proxmox puts the message into the reason phrase, which Go cannot set;
	// the body carries it too.
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "message": msg + "\n"})
}

type authKind int

const (
	authNone authKind = iota
	authToken
	authTicket
)

func authenticate(set Settings, r *http.Request) (authKind, string) {
	if h := r.Header.Get("Authorization"); set.Token != "" && h == "PVEAPIToken="+set.Token {
		return authToken, set.Token[:strings.Index(set.Token, "=")]
	}
	if c, err := r.Cookie("PVEAuthCookie"); err == nil && set.User != "" && c.Value == "PVE:"+set.User+":fake" {
		return authTicket, set.User
	}
	return authNone, ""
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	set := s.set
	s.mu.Unlock()
	if set.Fail != 0 {
		fail(w, set.Fail, "fake failure")
		return
	}
	p, ok := strings.CutPrefix(r.URL.Path, "/api2/json")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if p == "/access/ticket" && r.Method == http.MethodPost {
		login(set, w, r)
		return
	}
	kind, user := authenticate(set, r)
	if kind == authNone {
		fail(w, http.StatusUnauthorized, "authentication failure")
		return
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case p == "/version":
		writeData(w, map[string]string{"version": "9.0.11", "release": "9.0", "repoid": "fake"})
	case p == "/cluster/resources":
		s.resources(w)
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "termproxy":
		s.openConsole(w, set, kind, user, true)
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "vncwebsocket":
		s.websocket(w, r, set, kind)
	case len(parts) >= 4 && parts[0] == "nodes" && parts[2] == "tasks":
		s.task(w, parts)
	case len(parts) >= 5 && parts[0] == "nodes" && (parts[2] == "qemu" || parts[2] == "lxc"):
		s.guestRequest(w, r, set, parts, kind, user)
	default:
		fail(w, http.StatusNotImplemented, "Method '"+r.Method+" "+p+"' not implemented")
	}
}

func login(set Settings, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || set.User == "" || r.PostForm.Get("username") != set.User || r.PostForm.Get("password") != set.Password {
		fail(w, http.StatusUnauthorized, "authentication failure")
		return
	}
	writeData(w, map[string]string{"ticket": "PVE:" + set.User + ":fake", "CSRFPreventionToken": "csrf", "username": set.User})
}

func (s *Server) resources(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, n := range s.nodes {
		out = append(out, map[string]any{"id": "node/" + n, "type": "node", "node": n, "status": "online",
			"cpu": 0.12, "maxcpu": 16, "mem": 8 << 30, "maxmem": 64 << 30, "disk": 10 << 30, "maxdisk": 100 << 30, "uptime": 86400})
		out = append(out, map[string]any{"id": "storage/" + n + "/local", "type": "storage", "node": n, "storage": "local",
			"status": "available", "disk": 20 << 30, "maxdisk": 200 << 30})
	}
	for _, g := range s.guests {
		m := map[string]any{"id": g.Type + "/" + strconv.Itoa(g.VMID), "type": g.Type, "vmid": g.VMID, "name": g.Name,
			"node": g.Node, "status": g.Status, "tags": g.Tags, "template": 0, "maxcpu": 2, "maxmem": 2 << 30, "maxdisk": 32 << 30}
		if g.Status == "running" {
			m["cpu"], m["mem"], m["uptime"] = 0.05, 1<<30, 3600
		}
		out = append(out, m)
	}
	writeData(w, out)
}

func (s *Server) findGuest(node, typ, id string) *Guest {
	vmid, _ := strconv.Atoi(id)
	for _, g := range s.guests {
		if g.Node == node && g.Type == typ && g.VMID == vmid {
			return g
		}
	}
	return nil
}

// guestRequest handles /nodes/{node}/{type}/{vmid}/...
func (s *Server) guestRequest(w http.ResponseWriter, r *http.Request, set Settings, parts []string, kind authKind, user string) {
	s.mu.Lock()
	g := s.findGuest(parts[1], parts[2], parts[3])
	var gc Guest
	if g != nil {
		gc = *g
	}
	s.mu.Unlock()
	if g == nil {
		fail(w, http.StatusInternalServerError, "Configuration file 'nodes/"+parts[1]+"/"+parts[2]+"/"+parts[3]+".conf' does not exist")
		return
	}
	rest := strings.Join(parts[4:], "/")
	switch {
	case rest == "status/current" && r.Method == http.MethodGet:
		writeData(w, map[string]any{"status": gc.Status, "vmid": gc.VMID, "name": gc.Name, "qmpstatus": gc.Status, "ha": map[string]any{"managed": 0}})
	case rest == "config" && r.Method == http.MethodGet:
		writeData(w, map[string]any{"name": gc.Name, "cores": 2, "memory": "2048", "net0": "virtio=BC:24:11:00:00:01,bridge=vmbr0",
			"cipassword": "**********", "digest": "abc", "tags": gc.Tags})
	case strings.HasPrefix(rest, "status/") && r.Method == http.MethodPost:
		s.action(w, set, g, strings.TrimPrefix(rest, "status/"), user)
	case (rest == "vncproxy" || rest == "termproxy") && r.Method == http.MethodPost:
		if set.Denied[gc.Type+"/"+strconv.Itoa(gc.VMID)] {
			fail(w, http.StatusForbidden, "Permission check failed (/vms/"+parts[3]+", VM.Console)")
			return
		}
		s.openConsole(w, set, kind, user, rest == "termproxy")
	case rest == "vncwebsocket":
		s.websocket(w, r, set, kind)
	default:
		fail(w, http.StatusNotImplemented, "Method '"+r.Method+" "+rest+"' not implemented")
	}
}

var transitions = map[string]struct{ from, to string }{
	"start": {"stopped", "running"}, "stop": {"", "stopped"}, "shutdown": {"running", "stopped"},
	"reboot": {"running", "running"}, "suspend": {"running", "paused"}, "resume": {"paused", "running"},
}

func (s *Server) action(w http.ResponseWriter, set Settings, g *Guest, action, user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := transitions[action]
	if !ok {
		fail(w, http.StatusNotImplemented, "unknown action")
		return
	}
	if set.Denied[g.Type+"/"+strconv.Itoa(g.VMID)] {
		fail(w, http.StatusForbidden, "Permission check failed (/vms/"+strconv.Itoa(g.VMID)+", VM.PowerMgmt)")
		return
	}
	prefix := map[string]string{"qemu": "qm", "lxc": "vz"}[g.Type]
	s.seq++
	now := time.Now()
	upid := fmt.Sprintf("UPID:%s:%08X:%08X:%08X:%s%s:%d:%s:", g.Node, 1000+s.seq, 5000+s.seq, now.Unix(), prefix, action, g.VMID, user)
	tk := &task{upid: upid, node: g.Node, typ: prefix + action, id: strconv.Itoa(g.VMID), user: user, start: now, end: now}
	if t.from != "" && g.Status != t.from {
		tk.exit = "VM " + strconv.Itoa(g.VMID) + " not " + t.from
		tk.log = []string{"TASK ERROR: " + tk.exit}
	} else {
		g.Status = t.to
		tk.exit = "OK"
		tk.log = []string{action + " " + strconv.Itoa(g.VMID), "TASK OK"}
	}
	s.tasks[upid] = tk
	writeData(w, upid)
}

func (s *Server) task(w http.ResponseWriter, parts []string) {
	upid, err := url.PathUnescape(parts[3])
	if err != nil {
		upid = parts[3]
	}
	s.mu.Lock()
	tk, ok := s.tasks[upid]
	s.mu.Unlock()
	if !ok {
		fail(w, http.StatusInternalServerError, "no such task")
		return
	}
	switch strings.Join(parts[4:], "/") {
	case "status":
		writeData(w, map[string]any{"upid": tk.upid, "node": tk.node, "type": tk.typ, "id": tk.id, "user": tk.user,
			"status": "stopped", "exitstatus": tk.exit, "starttime": tk.start.Unix(), "endtime": tk.end.Unix()})
	case "log":
		lines := []map[string]any{}
		for i, l := range tk.log {
			lines = append(lines, map[string]any{"n": i + 1, "t": l})
		}
		writeData(w, lines)
	default:
		fail(w, http.StatusNotImplemented, "not implemented")
	}
}

func (s *Server) openConsole(w http.ResponseWriter, set Settings, kind authKind, user string, term bool) {
	if kind == authToken && !set.TokenConsole && term {
		// Proxmox issues the ticket, but the login on the websocket fails.
		user += "-token"
	}
	s.mu.Lock()
	s.seq++
	c := console{port: strconv.Itoa(5900 + s.seq), ticket: "PVEVNC:fake" + strconv.Itoa(s.seq), user: user, term: term}
	s.consoles[c.port] = c
	s.mu.Unlock()
	writeData(w, map[string]any{"port": c.port, "ticket": c.ticket, "user": strings.TrimSuffix(user, "-token"), "upid": "UPID:fake"})
}

var upgrader = websocket.Upgrader{Subprotocols: []string{"binary"}, CheckOrigin: func(*http.Request) bool { return true }}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request, set Settings, kind authKind) {
	q := r.URL.Query()
	s.mu.Lock()
	c, ok := s.consoles[q.Get("port")]
	delete(s.consoles, q.Get("port"))
	s.mu.Unlock()
	if !ok || q.Get("vncticket") != c.ticket {
		fail(w, http.StatusForbidden, "invalid ticket")
		return
	}
	if kind == authToken && !set.TokenConsole && !c.term {
		fail(w, http.StatusForbidden, "token not accepted")
		return
	}
	if !strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), "binary") {
		fail(w, http.StatusBadRequest, "binary subprotocol required")
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	if c.term {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if string(msg) != c.user+":"+c.ticket+"\n" {
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte("NO"))
			return
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("OK"))
	} else {
		_ = ws.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n"))
	}
	for {
		t, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if c.term {
			msg = termOutput(msg)
			if msg == nil {
				continue
			}
		}
		if err := ws.WriteMessage(t, msg); err != nil {
			return
		}
	}
}

// termOutput returns the echo of a termproxy input message "0:<len>:<data>";
// resize ("1:...") and ping ("2") produce no output.
func termOutput(msg []byte) []byte {
	parts := strings.SplitN(string(msg), ":", 3)
	if len(parts) != 3 || parts[0] != "0" {
		return nil
	}
	data := parts[2]
	if data == "\r" {
		data = "\r\n"
	}
	return []byte(data)
}
