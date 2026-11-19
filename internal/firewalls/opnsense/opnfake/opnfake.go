// SPDX-License-Identifier: GPL-3.0-or-later

// Package opnfake is an in-memory OPNsense REST API for tests. It answers like
// OPNsense 26.1 (see docs/adr/0009-opnsense.md): basic authentication with key
// and secret, search answers with rows, {"result": "failed", "validations"}
// on invalid changes, and filter savepoints that roll back after a timeout
// unless they are cancelled.
package opnfake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server is the fake firewall. Change its exported fields before the first
// request or under Lock.
type Server struct {
	sync.Mutex
	Key, Secret string
	// Deny lists path prefixes (below /api) the API user may not use (403).
	Deny []string
	// NoSavepoint makes the savepoint calls unknown (404), as in OPNsense 26.7.
	NoSavepoint bool
	// RollbackAfter is the rollback timeout of a savepoint (OPNsense: 60 s).
	RollbackAfter time.Duration
	// CutsOff reports whether an enabled rule cuts the API off: while such a
	// rule is applied, the fake doesn't answer.
	CutsOff func(Rule) bool

	Version, Product, Uptime string
	Gateways                 []Gateway
	Services                 []Service
	Peers                    []Peer
	Tunnels                  []Tunnel
	Aliases                  []Alias
	Rules                    []Rule
	// LegacyRules are listed read-only.
	LegacyRules []Rule

	// Applied counts reconfigure calls per area ("wireguard", "alias", "filter").
	Applied map[string]int
	// Restarted lists restarted services and tunnels.
	Restarted []string

	// applied are the rules in effect; savepoints the saved rules per revision.
	applied    []Rule
	savepoints map[string][]Rule
	timers     map[string]*time.Timer
	requests   []string
	fail       map[string]int
}

// Gateway is a gateway of the fake.
type Gateway struct{ Name, Address, Status, Delay, Loss string }

// Service is a service of the fake.
type Service struct {
	ID, Name, Description string
	Running, Locked       bool
}

// Peer is a WireGuard peer of the fake; Handshake is a Unix time (0: never).
type Peer struct {
	UUID, Name, PublicKey, Endpoint, TunnelAddress string
	Enabled                                        bool
	Handshake, Rx, Tx                              int64
}

// Tunnel is an IPsec connection of the fake.
type Tunnel struct {
	Name, Description, Remote string
	Connected                 bool
	Children                  []string
}

// Alias is an alias of the fake.
type Alias struct {
	UUID, Name, Type, Description string
	Enabled                       bool
	Content                       []string
	// InUse aliases can't be deleted.
	InUse bool
}

// Rule is a filter rule of the fake, with the API's field names.
type Rule map[string]string

// New returns a fake firewall with two gateways, services, a WireGuard peer, an IPsec
// tunnel, an alias and a rule.
func New(key, secret string) *Server {
	s := &Server{Key: key, Secret: secret, RollbackAfter: 60 * time.Second,
		Version: "26.1.6", Product: "OPNsense", Uptime: "3 days, 04:05:06",
		Gateways: []Gateway{{Name: "WAN_DHCP", Address: "198.51.100.1", Status: "none", Delay: "1.2 ms", Loss: "0.0 %"},
			{Name: "WAN2", Address: "203.0.113.1", Status: "none", Delay: "~", Loss: "~"}},
		Services: []Service{{ID: "configd", Name: "configd", Description: "System Configuration Daemon", Running: true, Locked: true},
			{ID: "unbound", Name: "unbound", Description: "Unbound DNS", Running: true},
			{ID: "wireguard", Name: "wireguard", Description: "WireGuard", Running: true}},
		Peers: []Peer{{UUID: "p-1", Name: "laptop-alice", PublicKey: "pubA=", TunnelAddress: "10.10.0.2/32", Enabled: true,
			Handshake: time.Now().Add(-time.Minute).Unix(), Rx: 1200, Tx: 3400},
			{UUID: "p-2", Name: "branch-office", PublicKey: "pubB=", Endpoint: "192.0.2.50:51820", TunnelAddress: "10.20.0.0/24", Enabled: false}},
		Tunnels: []Tunnel{{Name: "con1", Description: "Site B", Remote: "192.0.2.80", Connected: true, Children: []string{"con1-net"}}},
		Aliases: []Alias{{UUID: "a-1", Name: "webservers", Type: "host", Enabled: true, Content: []string{"192.0.2.10", "192.0.2.11"},
			Description: "Web servers", InUse: true}},
		Rules: []Rule{{"uuid": "r-1", "enabled": "1", "sequence": "10", "action": "pass", "quick": "1", "interface": "lan",
			"direction": "in", "ipprotocol": "inet", "protocol": "TCP", "source_net": "any", "destination_net": "webservers",
			"destination_port": "443", "description": "web"}},
		LegacyRules: []Rule{{"enabled": "1", "sequence": "1", "action": "pass", "interface": "lan", "direction": "in",
			"ipprotocol": "inet", "protocol": "any", "source_net": "lan", "destination_net": "any",
			"description": "Default allow LAN to any rule", "legacy": "1"}},
		Applied: map[string]int{}, savepoints: map[string][]Rule{}, timers: map[string]*time.Timer{}, fail: map[string]int{},
	}
	s.applied = cloneRules(s.Rules)
	return s
}

func cloneRules(rs []Rule) []Rule {
	out := make([]Rule, len(rs))
	for i, r := range rs {
		c := Rule{}
		for k, v := range r {
			c[k] = v
		}
		out[i] = c
	}
	return out
}

// Requests returns "METHOD path" of all requests so far.
func (s *Server) Requests() []string {
	s.Lock()
	defer s.Unlock()
	return append([]string(nil), s.requests...)
}

// FailNext makes the next request of method on path (below /api) answer with status.
func (s *Server) FailNext(method, path string, status int) {
	s.Lock()
	defer s.Unlock()
	s.fail[method+" "+path] = status
}

// AppliedRules returns the rules in effect.
func (s *Server) AppliedRules() []Rule {
	s.Lock()
	defer s.Unlock()
	return cloneRules(s.applied)
}

func (s *Server) cutOff() bool {
	if s.CutsOff == nil {
		return false
	}
	return slices.ContainsFunc(s.applied, func(r Rule) bool { return r["enabled"] == "1" && s.CutsOff(r) })
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Lock()
	defer s.Unlock()
	path, ok := strings.CutPrefix(r.URL.Path, "/api")
	if !ok {
		http.Error(w, "<html>login</html>", http.StatusOK)
		return
	}
	s.requests = append(s.requests, r.Method+" "+path)
	if s.cutOff() {
		// The applied rules block the API: no answer.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	key, secret, ok := r.BasicAuth()
	if !ok || key != s.Key || secret != s.Secret {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"status": 401, "message": "Authentication Failed"})
		return
	}
	for _, d := range s.Deny {
		if strings.HasPrefix(path, d) {
			writeJSON(w, http.StatusForbidden, map[string]any{"status": 403, "message": "Forbidden"})
			return
		}
	}
	if st, ok := s.fail[r.Method+" "+path]; ok {
		delete(s.fail, r.Method+" "+path)
		writeJSON(w, st, map[string]any{"status": st, "message": "injected failure"})
		return
	}
	var body map[string]any
	if r.Method == http.MethodPost {
		if strings.Contains(r.Header.Get("Content-Type"), "json") {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"status": 400, "message": "Invalid JSON syntax"})
				return
			}
		}
	}
	seg := strings.Split(strings.Trim(path, "/"), "/")
	if !s.route(w, r, seg, body) {
		writeJSON(w, http.StatusNotFound, map[string]any{"errorMessage": "Endpoint not found"})
	}
}

// route answers a request; false if the endpoint doesn't exist.
func (s *Server) route(w http.ResponseWriter, r *http.Request, seg []string, body map[string]any) bool {
	if len(seg) < 3 {
		return false
	}
	cmd := strings.Join(seg[:3], "/")
	arg := ""
	if len(seg) > 3 {
		arg = strings.Join(seg[3:], "/")
	}
	post := r.Method == http.MethodPost
	ok := func(v any) bool { writeJSON(w, http.StatusOK, v); return true }
	switch cmd {
	case "core/firmware/status":
		return ok(map[string]any{"status": "none", "product": map[string]any{"product_version": s.Version, "product_name": s.Product}})
	case "diagnostics/system/system_time":
		return ok(map[string]any{"uptime": s.Uptime})
	case "diagnostics/system/system_resources":
		return ok(map[string]any{"memory": map[string]any{"total": "8589934592", "used": "2147483648"}})
	case "routes/gateway/status":
		items := []map[string]any{}
		for _, g := range s.Gateways {
			items = append(items, map[string]any{"name": g.Name, "address": g.Address, "status": g.Status, "delay": g.Delay,
				"stddev": g.Delay, "loss": g.Loss})
		}
		return ok(map[string]any{"status": "ok", "items": items})
	case "core/service/search":
		rows := []map[string]any{}
		for _, sv := range s.Services {
			rows = append(rows, map[string]any{"id": sv.ID, "name": sv.Name, "description": sv.Description,
				"running": b01(sv.Running), "locked": b01(sv.Locked)})
		}
		return ok(search(rows))
	case "core/service/restart":
		for i := range s.Services {
			if s.Services[i].ID == arg && post {
				s.Services[i].Running = true
				s.Restarted = append(s.Restarted, "service "+arg)
				return ok(map[string]any{"result": "ok"})
			}
		}
		return ok(map[string]any{"result": "failed"})
	case "wireguard/client/search_client":
		rows := []map[string]any{}
		for _, p := range s.Peers {
			rows = append(rows, map[string]any{"uuid": p.UUID, "name": p.Name, "enabled": strconv.Itoa(b01(p.Enabled)),
				"pubkey": p.PublicKey, "endpoint": p.Endpoint, "tunneladdress": p.TunnelAddress})
		}
		return ok(search(rows))
	case "wireguard/client/get_client":
		for _, p := range s.Peers {
			if p.UUID == arg {
				return ok(map[string]any{"client": map[string]any{"enabled": strconv.Itoa(b01(p.Enabled)), "name": p.Name}})
			}
		}
		return ok(map[string]any{})
	case "wireguard/client/toggle_client":
		for i := range s.Peers {
			if s.Peers[i].UUID == arg && post {
				s.Peers[i].Enabled = !s.Peers[i].Enabled
				res := "Disabled"
				if s.Peers[i].Enabled {
					res = "Enabled"
				}
				return ok(map[string]any{"result": res, "changed": true})
			}
		}
		return ok(map[string]any{"result": "failed"})
	case "wireguard/service/reconfigure", "firewall/alias/reconfigure":
		if !post {
			return ok(map[string]any{"result": "failed"})
		}
		s.Applied[seg[0]]++
		return ok(map[string]any{"result": "ok", "status": "ok"})
	case "wireguard/service/show":
		rows := []map[string]any{{"type": "interface", "if": "wg0", "status": "up"}}
		for _, p := range s.Peers {
			if !p.Enabled {
				continue
			}
			st := "offline"
			if p.Handshake > 0 && time.Since(time.Unix(p.Handshake, 0)) <= 5*time.Minute {
				st = "online"
			} else if p.Handshake > 0 {
				st = "stale"
			}
			rows = append(rows, map[string]any{"type": "peer", "if": "wg0", "ifname": "wg0", "name": p.Name,
				"public-key": p.PublicKey, "endpoint": "(none)", "latest-handshake": p.Handshake, "transfer-rx": p.Rx,
				"transfer-tx": p.Tx, "peer-status": st})
		}
		return ok(search(rows))
	case "ipsec/sessions/search_phase1":
		rows := []map[string]any{}
		for _, t := range s.Tunnels {
			rows = append(rows, map[string]any{"name": t.Name, "phase1desc": t.Description, "connected": t.Connected,
				"remote-addrs": t.Remote, "bytes-in": 100, "bytes-out": 200})
		}
		return ok(search(rows))
	case "ipsec/sessions/search_phase2":
		rows := []map[string]any{}
		for _, t := range s.Tunnels {
			if body != nil && body["id"] == t.Name {
				for _, c := range t.Children {
					state := "INSTALLED"
					if !t.Connected {
						state = ""
					}
					rows = append(rows, map[string]any{"name": c, "phase2desc": c, "state": state, "local-ts": "10.0.0.0/24",
						"remote-ts": "10.1.0.0/24", "bytes-in": 10, "bytes-out": 20})
				}
			}
		}
		return ok(search(rows))
	case "ipsec/sessions/disconnect", "ipsec/sessions/connect":
		for i := range s.Tunnels {
			if s.Tunnels[i].Name == arg && post {
				s.Tunnels[i].Connected = seg[2] == "connect"
				s.Restarted = append(s.Restarted, seg[2]+" "+arg)
				return ok(map[string]any{"result": "ok"})
			}
		}
		return ok(map[string]any{"result": "failed"})
	}
	return s.firewall(w, cmd, arg, post, body)
}

func (s *Server) firewall(w http.ResponseWriter, cmd, arg string, post bool, body map[string]any) bool {
	ok := func(v any) bool { writeJSON(w, http.StatusOK, v); return true }
	switch cmd {
	case "firewall/alias/search_item":
		rows := []map[string]any{{"uuid": "i-1", "name": "bogons", "type": "External (advanced)", "enabled": "1", "content": ""}}
		rows[0]["type"] = "external"
		for _, a := range s.Aliases {
			rows = append(rows, map[string]any{"uuid": a.UUID, "name": a.Name, "type": a.Type, "enabled": strconv.Itoa(b01(a.Enabled)),
				"content": strings.Join(a.Content, ","), "description": a.Description})
		}
		return ok(search(rows))
	case "firewall/alias/add_item", "firewall/alias/set_item":
		if !post {
			return ok(map[string]any{"result": "failed"})
		}
		a, v := s.parseAlias(body, arg)
		if len(v) > 0 {
			return ok(map[string]any{"result": "failed", "validations": v})
		}
		if cmd == "firewall/alias/add_item" {
			a.UUID = random()
			s.Aliases = append(s.Aliases, a)
			return ok(map[string]any{"result": "saved", "uuid": a.UUID})
		}
		for i := range s.Aliases {
			if s.Aliases[i].UUID == arg {
				a.InUse = s.Aliases[i].InUse
				s.Aliases[i] = a
				return ok(map[string]any{"result": "saved"})
			}
		}
		return ok(map[string]any{"result": "failed"})
	case "firewall/alias/del_item":
		for i, a := range s.Aliases {
			if a.UUID == arg && post {
				if a.InUse {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"errorMessage": "Alias " + a.Name + " is in use"})
					return true
				}
				s.Aliases = slices.Delete(s.Aliases, i, i+1)
				return ok(map[string]any{"result": "deleted"})
			}
		}
		return ok(map[string]any{"result": "not found"})
	case "firewall/filter/search_rule":
		rows := []map[string]any{}
		for _, r := range append(cloneRules(s.LegacyRules), cloneRules(s.Rules)...) {
			m := map[string]any{}
			for k, v := range r {
				m[k] = v
			}
			rows = append(rows, m)
		}
		return ok(search(rows))
	case "firewall/filter/add_rule", "firewall/filter/set_rule":
		if !post {
			return ok(map[string]any{"result": "failed"})
		}
		rule, v := parseRule(body)
		if len(v) > 0 {
			return ok(map[string]any{"result": "failed", "validations": v})
		}
		if cmd == "firewall/filter/add_rule" {
			rule["uuid"] = random()
			s.Rules = append(s.Rules, rule)
			return ok(map[string]any{"result": "saved", "uuid": rule["uuid"]})
		}
		for i := range s.Rules {
			if s.Rules[i]["uuid"] == arg {
				rule["uuid"] = arg
				s.Rules[i] = rule
				return ok(map[string]any{"result": "saved"})
			}
		}
		return ok(map[string]any{"result": "failed"})
	case "firewall/filter/del_rule":
		for i, r := range s.Rules {
			if r["uuid"] == arg && post {
				s.Rules = slices.Delete(s.Rules, i, i+1)
				return ok(map[string]any{"result": "deleted"})
			}
		}
		return ok(map[string]any{"result": "not found"})
	case "firewall/filter/savepoint", "firewall/filter/apply", "firewall/filter/cancel_rollback", "firewall/filter/revert":
		return s.savepoint(w, cmd, arg, post)
	}
	return false
}

func (s *Server) savepoint(w http.ResponseWriter, cmd, arg string, post bool) bool {
	ok := func(v any) bool { writeJSON(w, http.StatusOK, v); return true }
	if s.NoSavepoint && cmd != "firewall/filter/apply" {
		return false
	}
	if !post {
		return ok(map[string]any{"status": "failed"})
	}
	switch cmd {
	case "firewall/filter/savepoint":
		rev := strconv.FormatInt(time.Now().UnixNano(), 10)
		s.savepoints[rev] = cloneRules(s.applied)
		return ok(map[string]any{"status": "ok", "retention": "60", "revision": rev})
	case "firewall/filter/apply":
		s.Applied["filter"]++
		s.applied = cloneRules(s.Rules)
		if saved, found := s.savepoints[arg]; found {
			rev := arg
			s.timers[rev] = time.AfterFunc(s.RollbackAfter, func() {
				s.Lock()
				defer s.Unlock()
				s.applied, s.Rules = cloneRules(saved), cloneRules(saved)
				delete(s.timers, rev)
			})
		}
		return ok(map[string]any{"status": "OK\n\n"})
	case "firewall/filter/cancel_rollback":
		if t, found := s.timers[arg]; found {
			t.Stop()
			delete(s.timers, arg)
			delete(s.savepoints, arg)
			return ok(map[string]any{"status": "ok"})
		}
		return ok(map[string]any{"status": "failed"})
	case "firewall/filter/revert":
		saved, found := s.savepoints[arg]
		if !found {
			return ok(map[string]any{"status": "failed"})
		}
		if t, running := s.timers[arg]; running {
			t.Stop()
			delete(s.timers, arg)
		}
		s.applied, s.Rules = cloneRules(saved), cloneRules(saved)
		return ok(map[string]any{"status": "ok"})
	}
	return false
}

func (s *Server) parseAlias(body map[string]any, uuid string) (Alias, map[string]string) {
	m, _ := body["alias"].(map[string]any)
	str := func(k string) string { v, _ := m[k].(string); return v }
	a := Alias{UUID: uuid, Name: str("name"), Type: str("type"), Description: str("description"), Enabled: str("enabled") == "1"}
	for _, c := range strings.Split(str("content"), "\n") {
		if c = strings.TrimSpace(c); c != "" {
			a.Content = append(a.Content, c)
		}
	}
	v := map[string]string{}
	if a.Name == "" || strings.ContainsAny(a.Name, " -.") {
		v["alias.name"] = "The name must start with a letter or single underscore, be less than 32 characters and only consist of alphanumeric characters or underscores."
	}
	for _, o := range s.Aliases {
		if o.Name == a.Name && o.UUID != uuid {
			v["alias.name"] = "An alias with this name already exists."
		}
	}
	if !slices.Contains([]string{"host", "network", "port", "url", "urltable", "mac", "networkgroup"}, a.Type) {
		v["alias.type"] = "Option not in list."
	}
	return a, v
}

func parseRule(body map[string]any) (Rule, map[string]string) {
	m, _ := body["rule"].(map[string]any)
	r := Rule{}
	for k, val := range m {
		if s, ok := val.(string); ok {
			r[k] = s
		}
	}
	v := map[string]string{}
	if !slices.Contains([]string{"pass", "block", "reject"}, r["action"]) {
		v["rule.action"] = "Option not in list."
	}
	if r["interface"] == "" {
		v["rule.interface"] = "Option not in list."
	}
	if r["destination_port"] != "" && r["protocol"] != "TCP" && r["protocol"] != "UDP" && r["protocol"] != "TCP/UDP" {
		v["rule.destination_port"] = "Ports are only valid for TCP and UDP."
	}
	return r, v
}

func search(rows []map[string]any) map[string]any {
	return map[string]any{"total": len(rows), "rowCount": len(rows), "current": 1, "rows": rows}
}

func b01(b bool) int {
	if b {
		return 1
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func random() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
