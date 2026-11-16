// SPDX-License-Identifier: GPL-3.0-or-later

// Package keafake is a small in-memory fake of a Kea 3.x control socket for
// tests: the commands dcdash uses, with Kea's response format and result
// codes as observed against kea-dhcp4/kea-dhcp6 3.0.4 (see
// test/integration/dhcp_kea_test.go, which checks the same behavior against
// the real server).
package keafake

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// Subnet is a configured subnet.
type Subnet struct {
	ID     uint32
	Prefix string
	// Pools as "first-last"; Total is their size (set by the test).
	Pools   []string
	PDPools []map[string]any
	Total   float64
	TotalPD float64
}

// Lease is a stored lease.
type Lease struct {
	IP        string `json:"ip-address"`
	HWAddress string `json:"hw-address,omitempty"`
	ClientID  string `json:"client-id,omitempty"`
	DUID      string `json:"duid,omitempty"`
	IAID      uint32 `json:"iaid,omitempty"`
	Type      string `json:"type,omitempty"`
	PrefixLen int    `json:"prefix-len,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	SubnetID  uint32 `json:"subnet-id"`
	Valid     uint32 `json:"valid-lft"`
	CLTT      int64  `json:"cltt"`
	State     int    `json:"state"`
}

// Host is a reservation as Kea's JSON object.
type Host map[string]any

// Settings change the fake's behavior.
type Settings struct {
	// Hooks loaded: lease_cmds, host_cmds, subnet_cmds, stat_cmds, ha.
	Hooks []string
	// HostDatabase: reservations can be added (host_cmds needs it).
	HostDatabase bool
	Version      string
	// Unavailable answers every request with HTTP 503.
	Unavailable bool
}

// Server is the fake. Family is "4" or "6".
type Server struct {
	mu       sync.Mutex
	family   string
	user     string
	password string
	settings Settings
	subnets  []Subnet
	leases   []Lease
	config   []Host // reservations from the configuration file
	database []Host
	started  time.Time
	requests int
}

// New creates a fake for family "v4" or "v6" with all hooks and a host
// database.
func New(family, user, password string, subnets ...Subnet) *Server {
	return &Server{
		family:   strings.TrimPrefix(family, "v"),
		user:     user,
		password: password,
		subnets:  subnets,
		settings: Settings{Hooks: []string{"lease_cmds", "host_cmds", "subnet_cmds", "stat_cmds"}, HostDatabase: true, Version: "3.0.4"},
		started:  time.Now(),
	}
}

// Set changes the settings.
func (s *Server) Set(f func(*Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.settings)
}

// AddLease stores a lease (test setup).
func (s *Server) AddLease(l Lease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.CLTT == 0 {
		l.CLTT = time.Now().Unix()
	}
	if l.Valid == 0 {
		l.Valid = 3600
	}
	s.leases = append(s.leases, l)
	s.sortLeases()
}

// AddConfigReservation stores a reservation of the configuration file.
func (s *Server) AddConfigReservation(h Host) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Normalize numbers to float64, as in hosts decoded from requests.
	b, _ := json.Marshal(h)
	var n Host
	_ = json.Unmarshal(b, &n)
	s.config = append(s.config, n)
}

// Requests returns the number of commands served.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func (s *Server) sortLeases() {
	slices.SortFunc(s.leases, func(a, b Lease) int {
		return netip.MustParseAddr(a.IP).Compare(netip.MustParseAddr(b.IP))
	})
}

type answer struct {
	Result    int    `json:"result"`
	Text      string `json:"text,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if s.settings.Unavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != s.user || p != s.password {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{ "result": 401, "text": "Unauthorized" }`))
		return
	}
	var req struct {
		Command   string          `json:"command"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var args map[string]any
	_ = json.Unmarshal(req.Arguments, &args)
	a := s.command(req.Command, args)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]answer{a})
}

func (s *Server) hook(name string) bool { return slices.Contains(s.settings.Hooks, name) }

func (s *Server) commands() []string {
	v := s.family
	cmds := []string{"build-report", "config-get", "config-hash-get", "config-reload", "config-set", "config-test",
		"config-write", "list-commands", "shutdown", "statistic-get", "statistic-get-all", "status-get", "version-get"}
	if s.hook("lease_cmds") {
		cmds = append(cmds, "lease"+v+"-add", "lease"+v+"-del", "lease"+v+"-get", "lease"+v+"-get-all",
			"lease"+v+"-get-by-hostname", "lease"+v+"-get-page", "lease"+v+"-update", "lease"+v+"-wipe")
		if v == "4" {
			cmds = append(cmds, "lease4-get-by-client-id", "lease4-get-by-hw-address")
		} else {
			cmds = append(cmds, "lease6-get-by-duid")
		}
	}
	if s.hook("host_cmds") {
		cmds = append(cmds, "reservation-add", "reservation-del", "reservation-get", "reservation-get-all",
			"reservation-get-by-address", "reservation-get-by-hostname", "reservation-get-by-id",
			"reservation-get-page", "reservation-update")
	}
	if s.hook("subnet_cmds") {
		cmds = append(cmds, "subnet"+v+"-add", "subnet"+v+"-del", "subnet"+v+"-get", "subnet"+v+"-list", "subnet"+v+"-update")
	}
	if s.hook("stat_cmds") {
		cmds = append(cmds, "stat-lease"+v+"-get")
	}
	if s.hook("ha") {
		cmds = append(cmds, "ha-heartbeat", "ha-sync", "ha-maintenance-start")
	}
	slices.Sort(cmds)
	return cmds
}

func fail(text string) answer         { return answer{Result: 1, Text: text} }
func empty(text string, a any) answer { return answer{Result: 3, Text: text, Arguments: a} }

func str(args map[string]any, k string) string {
	v, _ := args[k].(string)
	return v
}

func num(args map[string]any, k string) (float64, bool) {
	v, ok := args[k].(float64)
	return v, ok
}

func (s *Server) command(cmd string, args map[string]any) answer {
	v := s.family
	if !slices.Contains(s.commands(), cmd) {
		return answer{Result: 2, Text: fmt.Sprintf("'%s' command not supported.", cmd)}
	}
	switch strings.Replace(cmd, v, "#", 1) {
	case "version-get":
		return answer{Text: s.settings.Version, Arguments: map[string]any{"extended": s.settings.Version + " (fake)"}}
	case "status-get":
		st := map[string]any{"pid": 1, "uptime": int64(time.Since(s.started).Seconds()), "reload": 0, "multi-threading-enabled": true}
		if s.hook("ha") {
			st["high-availability"] = []any{map[string]any{"ha-mode": "hot-standby", "ha-servers": map[string]any{
				"local":  map[string]any{"role": "primary", "state": "hot-standby", "server-name": "server1", "scopes": []string{"server1"}},
				"remote": map[string]any{"role": "standby", "last-state": "hot-standby", "server-name": "server2", "in-touch": true, "age": 3},
			}}}
		}
		return answer{Arguments: st}
	case "list-commands":
		return answer{Arguments: s.commands()}
	case "config-get":
		srv := map[string]any{"subnet" + v: s.subnetObjects()}
		if s.settings.HostDatabase {
			srv["hosts-database"] = map[string]any{"type": "postgresql", "password": "secret-in-config"}
		}
		return answer{Arguments: map[string]any{"Dhcp" + v: srv, "hash": "0123"}}
	case "lease#-get-page":
		return s.leasePage(args)
	case "lease#-get":
		for _, l := range s.leases {
			if l.IP == str(args, "ip-address") && (v == "4" || cmp.Or(str(args, "type"), "IA_NA") == cmp.Or(l.Type, "IA_NA")) {
				return answer{Text: "IPv" + v + " lease found.", Arguments: l}
			}
		}
		return empty("Lease not found.", map[string]any{})
	case "lease#-get-all":
		ids, _ := args["subnets"].([]any)
		var out []Lease
		for _, l := range s.leases {
			if len(ids) == 0 || slices.Contains(ids, any(float64(l.SubnetID))) {
				out = append(out, l)
			}
		}
		return leaseList(v, out)
	case "lease#-get-by-hw-address", "lease#-get-by-client-id", "lease#-get-by-hostname", "lease#-get-by-duid":
		key := strings.TrimPrefix(cmd, "lease"+v+"-get-by-")
		var out []Lease
		for _, l := range s.leases {
			if map[string]string{"hw-address": l.HWAddress, "client-id": l.ClientID, "hostname": l.Hostname, "duid": l.DUID}[key] == str(args, key) {
				out = append(out, l)
			}
		}
		return leaseList(v, out)
	case "lease#-del":
		for i, l := range s.leases {
			if l.IP == str(args, "ip-address") && (v == "4" || cmp.Or(str(args, "type"), "IA_NA") == cmp.Or(l.Type, "IA_NA")) {
				s.leases = slices.Delete(s.leases, i, i+1)
				return answer{Text: "IPv" + v + " lease deleted."}
			}
		}
		return empty("IPv"+v+" lease not found.", nil)
	case "subnet#-list":
		var l []map[string]any
		for _, sn := range s.subnets {
			l = append(l, map[string]any{"id": sn.ID, "subnet": sn.Prefix, "shared-network-name": nil})
		}
		return answer{Text: fmt.Sprintf("%d IPv%s subnets found", len(l), v), Arguments: map[string]any{"subnets": l}}
	case "subnet#-get":
		id, _ := num(args, "id")
		for _, o := range s.subnetObjects() {
			if o["id"] == uint32(id) {
				return answer{Arguments: map[string]any{"subnet" + v: []any{o}}}
			}
		}
		return empty("No subnet with id found", nil)
	case "stat-lease#-get":
		return s.leaseStats()
	case "statistic-get-all":
		all := map[string]any{}
		for _, st := range s.leaseStats().Arguments.(map[string]any)["result-set"].(map[string]any)["rows"].([][]float64) {
			all[fmt.Sprintf("subnet[%d].pool[0].total-addresses", uint32(st[0]))] = [][]any{{st[1], "2026-01-01 00:00:00.000000"}}
			all[fmt.Sprintf("subnet[%d].pool[0].assigned-addresses", uint32(st[0]))] = [][]any{{st[2], "2026-01-01 00:00:00.000000"}}
		}
		return answer{Arguments: all}
	case "reservation-get-page":
		return s.reservationPage(args)
	case "reservation-get":
		hosts := s.database
		if str(args, "operation-target") == "memory" {
			hosts = s.config
		}
		if i := findHost(hosts, args); i >= 0 {
			return answer{Text: "Host found.", Arguments: hosts[i]}
		}
		return empty("Host not found.", map[string]any{})
	case "reservation-add":
		if !s.settings.HostDatabase {
			return fail("Host database not available, cannot add host.")
		}
		h, _ := args["reservation"].(map[string]any)
		key := hostKey(h)
		if key == nil || findHost(s.database, key) >= 0 || findHost(s.config, key) >= 0 {
			return fail("failed to add new host: this host has already been added")
		}
		s.database = append(s.database, h)
		return answer{Text: "Host added."}
	case "reservation-update":
		h, _ := args["reservation"].(map[string]any)
		key := hostKey(h)
		if i := findHost(s.database, key); key != nil && i >= 0 {
			s.database[i] = h
			return answer{Text: "Host updated."}
		}
		return fail("Host not updated (not found).")
	case "reservation-del":
		if !s.settings.HostDatabase {
			return fail("Unable to delete a host because there is no hosts-database configured.")
		}
		if i := findHost(s.database, args); i >= 0 {
			s.database = slices.Delete(s.database, i, i+1)
			return answer{Text: "Host deleted."}
		}
		return empty("Host not deleted (not found).", nil)
	}
	return answer{Result: 2, Text: fmt.Sprintf("'%s' command not supported by the fake.", cmd)}
}

func leaseList(v string, l []Lease) answer {
	if l == nil {
		l = []Lease{}
	}
	text := fmt.Sprintf("%d IPv%s lease(s) found.", len(l), v)
	if len(l) == 0 {
		return empty(text, map[string]any{"leases": l})
	}
	return answer{Text: text, Arguments: map[string]any{"leases": l}}
}

func (s *Server) leasePage(args map[string]any) answer {
	from, limit := str(args, "from"), 0
	if n, ok := num(args, "limit"); ok {
		limit = int(n)
	}
	if limit <= 0 || from == "" {
		return fail("invalid page arguments")
	}
	var out []Lease
	for _, l := range s.leases {
		if from != "start" && netip.MustParseAddr(l.IP).Compare(netip.MustParseAddr(from)) <= 0 {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, l)
	}
	return leaseList(s.family, out)
}

func (s *Server) subnetObjects() []map[string]any {
	var out []map[string]any
	for _, sn := range s.subnets {
		var pools []map[string]any
		for _, p := range sn.Pools {
			pools = append(pools, map[string]any{"pool": p, "option-data": []any{}})
		}
		o := map[string]any{"id": sn.ID, "subnet": sn.Prefix, "pools": pools, "valid-lifetime": 3600, "option-data": []any{}}
		if s.family == "6" {
			pd := sn.PDPools
			if pd == nil {
				pd = []map[string]any{}
			}
			o["pd-pools"] = pd
		}
		out = append(out, o)
	}
	return out
}

func (s *Server) leaseStats() answer {
	cols := []string{"subnet-id", "total-addresses", "cumulative-assigned-addresses", "assigned-addresses", "declined-addresses"}
	if s.family == "6" {
		cols = []string{"subnet-id", "total-nas", "cumulative-assigned-nas", "assigned-nas", "declined-nas", "total-pds", "cumulative-assigned-pds", "assigned-pds"}
	}
	var rows [][]float64
	for _, sn := range s.subnets {
		var na, pd, declined float64
		for _, l := range s.leases {
			switch {
			case l.SubnetID != sn.ID:
			case l.State == 1:
				declined++
			case l.Type == "IA_PD":
				pd++
			default:
				na++
			}
		}
		if s.family == "6" {
			rows = append(rows, []float64{float64(sn.ID), sn.Total, na, na, declined, sn.TotalPD, pd, pd})
		} else {
			rows = append(rows, []float64{float64(sn.ID), sn.Total, na, na, declined})
		}
	}
	// statistic-get-all reads the rows as [id, total, assigned].
	return answer{Arguments: map[string]any{"result-set": map[string]any{"columns": cols, "rows": rows}}}
}

func (s *Server) reservationPage(args map[string]any) answer {
	limit := 100
	if n, ok := num(args, "limit"); ok {
		limit = int(n)
	}
	src, from := 0, 0
	if n, ok := num(args, "source-index"); ok {
		src = int(n)
	}
	if n, ok := num(args, "from"); ok {
		from = int(n)
	}
	sid, filter := num(args, "subnet-id")
	sources := [][]Host{s.config}
	if s.settings.HostDatabase {
		sources = append(sources, s.database)
	}
	for ; src < len(sources); src, from = src+1, 0 {
		var page []Host
		i := 0
		for ; i < len(sources[src]) && len(page) < limit; i++ {
			h := sources[src][i]
			if i < from || (filter && h["subnet-id"] != sid) {
				continue
			}
			page = append(page, h)
		}
		if len(page) > 0 {
			text := fmt.Sprintf("%d IPv%s host(s) found.", len(page), s.family)
			return answer{Text: text, Arguments: map[string]any{"count": len(page), "hosts": page, "next": map[string]any{"from": i, "source-index": src}}}
		}
	}
	return empty("0 IPv"+s.family+" host(s) found.", map[string]any{"count": 0, "hosts": []any{}})
}

var idTypes = []string{"hw-address", "duid", "client-id", "circuit-id", "flex-id"}

// hostKey returns subnet-id, identifier-type and identifier of a host.
func hostKey(h map[string]any) map[string]any {
	for _, t := range idTypes {
		if id, ok := h[t].(string); ok && id != "" {
			return map[string]any{"subnet-id": h["subnet-id"], "identifier-type": t, "identifier": id}
		}
	}
	return nil
}

func findHost(hosts []Host, key map[string]any) int {
	t := str(key, "identifier-type")
	return slices.IndexFunc(hosts, func(h Host) bool {
		return h["subnet-id"] == key["subnet-id"] && t != "" && h[t] == key["identifier"]
	})
}
