// SPDX-License-Identifier: GPL-3.0-or-later

// Package cxfake is an in-memory AOS-CX REST API for tests. It answers like
// the firmware 10.08 test switch (see docs/adr/0008-aruba-cx.md): form login
// with a session cookie, an optional CSRF token (newer firmware), a limit of
// sessions per user, and no PATCH.
package cxfake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Version is the API version of the fake.
const Version = "v10.08"

// Prefix is the API path prefix.
const Prefix = "/rest/" + Version

// cookieName is the session cookie of AOS-CX.
const cookieName = "id"

// Server is the fake switch. Change its exported fields before the first
// request or under Lock.
type Server struct {
	sync.Mutex
	Username, Password string
	// CSRF makes login return a token that changing requests must send.
	CSRF bool
	// MaxSessions limits open sessions (AOS-CX: a few per user); 0 is 3.
	MaxSessions int

	Hostname, Platform, Firmware string
	BootTime                     int64
	// Subsystems maps "<type>,<name>" to product info.
	Subsystems map[string]Product

	// Ports by name, VLANs by ID, the MAC address table and LLDP neighbors by
	// port (see network.go).
	Ports     map[string]*Port
	VLANs     map[int]*VLAN
	MACs      []MAC
	Neighbors map[string][]Neighbor
	// Saves counts copies of the running to the startup configuration.
	Saves int

	sessions map[string]string // session ID -> CSRF token
	logins   int
	requests []string
	fail     map[string]int // "METHOD path" -> status
}

// Product is the product_info of a subsystem.
type Product struct {
	ProductName  string `json:"product_name"`
	SerialNumber string `json:"serial_number"`
	PartNumber   string `json:"part_number"`
}

// New returns a fake 6000 switch with one chassis.
func New(username, password string) *Server {
	p := Product{ProductName: "6000 24G 4SFP Swch", SerialNumber: "CN00000001", PartNumber: "R8N88A"}
	s := &Server{
		Username: username, Password: password,
		Hostname: "sw-test", Platform: "6000", Firmware: "PL.10.08.1010", BootTime: 1795091390,
		Subsystems: map[string]Product{"chassis,1": p, "line_card,1/1": p, "management_module,1/1": p},
		sessions:   map[string]string{},
		fail:       map[string]int{},
	}
	s.seed()
	return s
}

// Logins returns the number of successful logins.
func (s *Server) Logins() int {
	s.Lock()
	defer s.Unlock()
	return s.logins
}

// Sessions returns the number of open sessions.
func (s *Server) Sessions() int {
	s.Lock()
	defer s.Unlock()
	return len(s.sessions)
}

// ExpireSessions ends all sessions, as the idle timeout does.
func (s *Server) ExpireSessions() {
	s.Lock()
	defer s.Unlock()
	s.sessions = map[string]string{}
}

// Requests returns "METHOD path" of all requests so far.
func (s *Server) Requests() []string {
	s.Lock()
	defer s.Unlock()
	return append([]string(nil), s.requests...)
}

// FailNext makes the next request of method on path (below the prefix, e.g.
// "/system") answer with status.
func (s *Server) FailNext(method, path string, status int) {
	s.Lock()
	defer s.Unlock()
	s.fail[method+" "+path] = status
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)
	if r.URL.Path == "/rest" && r.Method == http.MethodGet {
		writeJSON(w, map[string]any{
			"latest": map[string]string{"version": Version, "prefix": Prefix, "doc": "/api/" + Version},
			"v10.04": map[string]string{"version": "v10.04", "prefix": "/rest/v10.04", "doc": "/api/v10.04"},
			Version:  map[string]string{"version": Version, "prefix": Prefix, "doc": "/api/" + Version},
			"v1":     map[string]string{"version": "v1", "prefix": "/rest/v1", "doc": "/api/v1"},
		})
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, Prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		// 10.08 closes the connection on PATCH.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if st, ok := s.fail[r.Method+" "+path]; ok {
		delete(s.fail, r.Method+" "+path)
		http.Error(w, "injected failure", st)
		return
	}
	switch {
	case path == "/login" && r.Method == http.MethodPost:
		s.login(w, r)
		return
	case path == "/logout" && r.Method == http.MethodPost:
		if id := s.session(r); id != "" {
			delete(s.sessions, id)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	id := s.session(r)
	if id == "" {
		http.Error(w, "Login failed: session not found", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && s.CSRF && r.Header.Get("x-csrf-token") != s.sessions[id] {
		http.Error(w, "CSRF token missing or invalid", http.StatusUnauthorized)
		return
	}
	switch {
	case path == "/system" && r.Method == http.MethodGet:
		writeJSON(w, filter(map[string]any{
			"hostname": s.Hostname, "platform_name": s.Platform, "software_version": s.Firmware, "boot_time": s.BootTime,
		}, r))
	case path == "/system/subsystems" && r.Method == http.MethodGet:
		out := map[string]any{}
		for k, p := range s.Subsystems {
			out[k] = map[string]any{"product_info": p}
		}
		writeJSON(w, out)
	default:
		s.network(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("username") != s.Username || r.PostForm.Get("password") != s.Password {
		http.Error(w, "Login failed.", http.StatusUnauthorized)
		return
	}
	limit := s.MaxSessions
	if limit == 0 {
		limit = 3
	}
	if len(s.sessions) >= limit {
		http.Error(w, "session limit reached", http.StatusTooManyRequests)
		return
	}
	id, token := random(), ""
	if s.CSRF && r.Header.Get("x-use-csrf-token") == "true" {
		token = random()
		w.Header().Set("x-csrf-token", token)
	}
	s.sessions[id] = token
	s.logins++
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusOK)
}

func (s *Server) session(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	if _, ok := s.sessions[c.Value]; !ok {
		return ""
	}
	return c.Value
}

// filter keeps the ?attributes= of an object.
func filter(o map[string]any, r *http.Request) map[string]any {
	attrs := r.URL.Query().Get("attributes")
	if attrs == "" {
		return o
	}
	out := map[string]any{}
	for _, a := range strings.Split(attrs, ",") {
		if v, ok := o[a]; ok {
			out[a] = v
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func random() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
