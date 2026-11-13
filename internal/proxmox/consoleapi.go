// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
)

// consoleTicketTTL is how long a prepared console may wait for its websocket.
// Proxmox closes an unused vncproxy/termproxy after about 10 seconds.
const consoleTicketTTL = 15 * time.Second

// consoleAliveInterval is how often an open console checks that its dcdash
// session still exists (logout, termination by an administrator, expiry).
const consoleAliveInterval = 30 * time.Second

// maxConsoleMessage limits a message from the browser (keyboard, mouse, resize).
const maxConsoleMessage = 1 << 20

// pendingConsole is a prepared console waiting for its websocket.
type pendingConsole struct {
	console   *Console
	cluster   string
	target    string // e.g. "lab/qemu/101"
	perm      rbac.Permission
	sessionID string
	expires   time.Time
}

// consoleTickets holds one-time console tickets in memory.
type consoleTickets struct {
	mu      sync.Mutex
	pending map[string]*pendingConsole
	now     func() time.Time
}

func newConsoleTickets() *consoleTickets {
	return &consoleTickets{pending: map[string]*pendingConsole{}, now: time.Now}
}

func (t *consoleTickets) add(p *pendingConsole) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for k, v := range t.pending {
		if now.After(v.expires) {
			delete(t.pending, k)
		}
	}
	p.expires = now.Add(consoleTicketTTL)
	t.pending[id] = p
	return id, nil
}

// take removes and returns a ticket if it exists and has not expired.
func (t *consoleTickets) take(id string) *pendingConsole {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.pending[id]
	delete(t.pending, id)
	if !ok || t.now().After(p.expires) {
		return nil
	}
	return p
}

// ConsoleTicket is the response when a console is prepared. The browser opens
// the websocket GET /api/v1/proxmox/console/{id} within 15 seconds.
type ConsoleTicket struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // vnc or term
	// Password is the VNC password for noVNC (only for kind vnc).
	Password string `json:"password,omitempty"`
}

func (s *Service) handleGuestConsole(w http.ResponseWriter, r *http.Request) {
	c, res, ok := s.guest(w, r, rbac.ActionConsole)
	if !ok {
		return
	}
	target := c.Name() + "/" + guestResource(res.Type, res.VMID)
	con, err := c.OpenGuestConsole(r.Context(), res.Node, res.Type, res.VMID)
	s.prepared(w, r, c, con, err, target, rbac.P(Module, c.Name(), guestResource(res.Type, res.VMID), rbac.ActionConsole))
}

// A node shell is root access to the hypervisor, so it needs admin on the node.
func (s *Service) handleNodeShell(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.cluster(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	node := r.PathValue("node")
	if !nodeNamePattern.MatchString(node) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid node")
		return
	}
	perm := rbac.P(Module, c.Name(), nodeResource(node), rbac.ActionAdmin)
	if !auth.Check(w, r, perm) {
		return
	}
	con, err := c.OpenNodeShell(r.Context(), node)
	s.prepared(w, r, c, con, err, c.Name()+"/"+nodeResource(node), perm)
}

func (s *Service) prepared(w http.ResponseWriter, r *http.Request, c *Cluster, con *Console, err error, target string, perm rbac.Permission) {
	if err != nil {
		s.audit.RecordRequest(r, "proxmox.console.open", target, audit.ResultFailure, map[string]any{"error": err.Error()})
		upstreamError(w, r, err)
		return
	}
	sess := session.FromContext(r.Context())
	id, err := s.consoles.add(&pendingConsole{console: con, cluster: c.Name(), target: target, perm: perm, sessionID: sess.ID})
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("cannot create console ticket")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ConsoleTicket{ID: id, Kind: con.Kind, Password: con.Password})
}

// upgrader accepts only same-origin websockets (gorilla's default origin check
// compares Origin with Host), so other sites cannot use a user's session.
var upgrader = websocket.Upgrader{
	Subprotocols:    []string{"binary"},
	ReadBufferSize:  32 << 10,
	WriteBufferSize: 32 << 10,
}

func (s *Service) handleConsoleSocket(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	sess := session.FromContext(r.Context())
	if subj == nil || sess == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	p := s.consoles.take(r.PathValue("id"))
	if p == nil || p.sessionID != sess.ID {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "console ticket unknown or expired")
		return
	}
	// The roles may have changed since the console was prepared.
	if !auth.Check(w, r, p.perm) {
		return
	}
	if !websocket.IsWebSocketUpgrade(r) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "websocket required")
		return
	}
	dialCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	upstream, err := p.console.Dial(dialCtx)
	cancel()
	if err != nil {
		s.audit.RecordRequest(r, "proxmox.console.open", p.target, audit.ResultFailure, map[string]any{"error": err.Error()})
		upstreamError(w, r, err)
		return
	}
	defer upstream.Close()
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has written the error response.
		return
	}
	defer client.Close()
	client.SetReadLimit(maxConsoleMessage)

	start := time.Now()
	s.audit.RecordRequest(r, "proxmox.console.start", p.target, audit.ResultSuccess, map[string]any{"kind": p.console.Kind})
	log := hlog.FromRequest(r).With().Str("component", "proxmox").Str("target", p.target).Logger()
	log.Info().Str("kind", p.console.Kind).Msg("console started")

	token := sessionToken(r, s.sessions)
	reason, in, out := s.pipe(r.Context(), client, upstream, token)

	details := map[string]any{"kind": p.console.Kind, "durationSeconds": int(time.Since(start).Seconds()),
		"bytesIn": in, "bytesOut": out, "reason": reason}
	// The request context may be canceled already; the entry must be written.
	e := audit.FromRequest(r, "proxmox.console.end", p.target, audit.ResultSuccess)
	e.Details = details
	_ = s.audit.Record(context.WithoutCancel(r.Context()), e)
	log.Info().Str("reason", reason).Msg("console ended")
}

func sessionToken(r *http.Request, m *session.Manager) string {
	if m == nil {
		return ""
	}
	return m.TokenFromRequest(r)
}

// pipe copies messages in both directions until one side closes, the request
// ends or the dcdash session ends. It returns why it stopped and the bytes
// sent by the browser (in) and by Proxmox (out).
func (s *Service) pipe(ctx context.Context, client, upstream *websocket.Conn, token string) (string, int64, int64) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var in, out atomic.Int64
	reasons := make(chan string, 3)
	copyMsgs := func(dst, src *websocket.Conn, n *atomic.Int64, name string) {
		for {
			t, msg, err := src.ReadMessage()
			if err != nil {
				reasons <- name + " closed"
				return
			}
			n.Add(int64(len(msg)))
			if err := dst.WriteMessage(t, msg); err != nil {
				reasons <- name + " write failed"
				return
			}
		}
	}
	go copyMsgs(upstream, client, &in, "browser")
	go copyMsgs(client, upstream, &out, "proxmox")
	go func() {
		t := time.NewTicker(consoleAliveInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if s.sessions == nil {
					continue
				}
				sess, err := s.sessions.Get(ctx, token)
				if errors.Is(err, session.ErrNotFound) || (err == nil && !sess.Authenticated()) {
					reasons <- "session ended"
					return
				}
			}
		}
	}()
	var reason string
	select {
	case reason = <-reasons:
	case <-ctx.Done():
		reason = "server shutdown"
	}
	deadline := time.Now().Add(time.Second)
	_ = client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason), deadline)
	_ = upstream.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline)
	// Closing both connections ends the copy goroutines.
	_ = client.Close()
	_ = upstream.Close()
	return reason, in.Load(), out.Load()
}
