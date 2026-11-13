// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/status"
)

// Module is the permission module of Proxmox.
const Module = "proxmox"

// Permission resources: "qemu/<vmid>", "lxc/<vmid>", "node/<name>" and
// "storage/<node>/<storage>".
func guestResource(typ string, vmid int) string { return typ + "/" + strconv.Itoa(vmid) }
func nodeResource(node string) string           { return "node/" + node }

// Service is the Proxmox API of dcdash.
type Service struct {
	clusters map[string]*Cluster
	order    []string
	audit    *audit.Log
	sessions *session.Manager
	consoles *consoleTickets
	log      zerolog.Logger
}

// NewService creates the API for the given clusters. sessions is used to end
// console connections whose session ended.
func NewService(clusters []*Cluster, auditLog *audit.Log, sessions *session.Manager, log zerolog.Logger) *Service {
	s := &Service{
		clusters: map[string]*Cluster{}, audit: auditLog, sessions: sessions,
		consoles: newConsoleTickets(), log: log.With().Str("component", "proxmox").Logger(),
	}
	for _, c := range clusters {
		s.clusters[c.Name()] = c
		s.order = append(s.order, c.Name())
	}
	return s
}

// StatusChecks returns a dashboard check per cluster.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, status.Check{Name: n, Kind: Module, Check: s.clusters[n].Check})
	}
	return out
}

// Register adds the Proxmox routes.
func (s *Service) Register(mux *http.ServeMux) {
	const base = "/api/v1/proxmox/clusters"
	mux.HandleFunc("GET "+base, s.handleClusters)
	mux.HandleFunc("GET "+base+"/{cluster}/resources", s.handleResources)
	mux.HandleFunc("GET "+base+"/{cluster}/guests/{type}/{vmid}", s.handleGuest)
	mux.HandleFunc("POST "+base+"/{cluster}/guests/{type}/{vmid}/actions/{action}", s.handleAction)
	mux.HandleFunc("GET "+base+"/{cluster}/tasks/{upid}", s.handleTask)
	mux.HandleFunc("POST "+base+"/{cluster}/guests/{type}/{vmid}/console", s.handleGuestConsole)
	mux.HandleFunc("POST "+base+"/{cluster}/nodes/{node}/shell", s.handleNodeShell)
	mux.HandleFunc("GET /api/v1/proxmox/console/{id}", s.handleConsoleSocket)
}

// ClusterInfo describes a cluster in GET /api/v1/proxmox/clusters.
type ClusterInfo struct {
	Name string `json:"name"`
	// ConsoleUser is true if consoles use a dedicated Proxmox user.
	ConsoleUser bool `json:"consoleUser"`
}

// ClusterList is the response of GET /api/v1/proxmox/clusters.
type ClusterList struct {
	Clusters []ClusterInfo `json:"clusters"`
}

func (s *Service) handleClusters(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := ClusterList{Clusters: []ClusterInfo{}}
	for _, n := range s.order {
		if subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			out.Clusters = append(out.Clusters, ClusterInfo{Name: n, ConsoleUser: s.clusters[n].HasConsoleUser()})
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// cluster returns the cluster of the request if the subject has at least
// action on any of its resources; otherwise it writes an error.
func (s *Service) cluster(w http.ResponseWriter, r *http.Request, action rbac.Action) (*Cluster, *rbac.Subject, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil, nil, false
	}
	name := r.PathValue("cluster")
	c, ok := s.clusters[name]
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown cluster")
		return nil, nil, false
	}
	if !subj.Permissions.AllowsAny(Module, name, action) {
		auth.Check(w, r, rbac.P(Module, name, "*", action)) // writes 403 and the audit entry
		return nil, nil, false
	}
	return c, subj, true
}

// upstreamError writes the response for an error of the Proxmox API.
func upstreamError(w http.ResponseWriter, r *http.Request, err error) {
	var pe *Error
	switch {
	case errors.Is(err, ErrNotFound) || IsStatus(err, http.StatusNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not found in Proxmox")
	case errors.Is(err, context.Canceled):
		// The client went away; nothing to answer.
	case errors.Is(err, ErrUnavailable):
		hlog.FromRequest(r).Warn().Err(err).Msg("proxmox unavailable")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "Proxmox cluster not reachable")
	case errors.As(err, &pe) && (pe.Status == http.StatusUnauthorized || pe.Status == http.StatusForbidden):
		hlog.FromRequest(r).Warn().Err(err).Msg("proxmox denied the request")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_forbidden", pe.Message)
	case errors.As(err, &pe):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", pe.Message)
	default:
		hlog.FromRequest(r).Error().Err(err).Msg("proxmox request failed")
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", "Proxmox request failed")
	}
}

// Node is a node in the inventory.
type Node struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	CPU     float64 `json:"cpu"`
	MaxCPU  float64 `json:"maxCpu"`
	Mem     int64   `json:"mem"`
	MaxMem  int64   `json:"maxMem"`
	Disk    int64   `json:"disk"`
	MaxDisk int64   `json:"maxDisk"`
	Uptime  int64   `json:"uptime"`
}

// Guest is a virtual machine or container in the inventory.
type Guest struct {
	Type     string   `json:"type"`
	VMID     int      `json:"vmid"`
	Name     string   `json:"name"`
	Node     string   `json:"node"`
	Status   string   `json:"status"`
	Template bool     `json:"template"`
	Tags     []string `json:"tags"`
	Pool     string   `json:"pool"`
	Lock     string   `json:"lock"`
	HAState  string   `json:"haState"`
	CPU      float64  `json:"cpu"`
	MaxCPU   float64  `json:"maxCpu"`
	Mem      int64    `json:"mem"`
	MaxMem   int64    `json:"maxMem"`
	Disk     int64    `json:"disk"`
	MaxDisk  int64    `json:"maxDisk"`
	Uptime   int64    `json:"uptime"`
}

// Storage is a storage of a node in the inventory.
type Storage struct {
	Name    string `json:"name"`
	Node    string `json:"node"`
	Status  string `json:"status"`
	Disk    int64  `json:"disk"`
	MaxDisk int64  `json:"maxDisk"`
}

// Inventory is the response of GET .../resources.
type Inventory struct {
	Nodes   []Node    `json:"nodes"`
	Guests  []Guest   `json:"guests"`
	Storage []Storage `json:"storage"`
}

func splitTags(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ';' || s[i] == ',' || s[i] == ' ' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func toGuest(r Resource) Guest {
	return Guest{
		Type: r.Type, VMID: r.VMID, Name: r.Name, Node: r.Node, Status: r.Status, Template: r.Template != 0,
		Tags: splitTags(r.Tags), Pool: r.Pool, Lock: r.Lock, HAState: r.HAState, CPU: r.CPU, MaxCPU: r.MaxCPU,
		Mem: r.Mem, MaxMem: r.MaxMem, Disk: r.Disk, MaxDisk: r.MaxDisk, Uptime: r.Uptime,
	}
}

func (s *Service) handleResources(w http.ResponseWriter, r *http.Request) {
	c, subj, ok := s.cluster(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	rs, err := c.Resources(r.Context())
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	inv := Inventory{Nodes: []Node{}, Guests: []Guest{}, Storage: []Storage{}}
	can := func(res string) bool { return subj.Can(rbac.P(Module, c.Name(), res, rbac.ActionRead)) }
	for _, x := range rs {
		switch {
		case x.Type == "node" && can(nodeResource(x.Node)):
			inv.Nodes = append(inv.Nodes, Node{Name: x.Node, Status: x.Status, CPU: x.CPU, MaxCPU: x.MaxCPU,
				Mem: x.Mem, MaxMem: x.MaxMem, Disk: x.Disk, MaxDisk: x.MaxDisk, Uptime: x.Uptime})
		case IsGuestType(x.Type) && can(guestResource(x.Type, x.VMID)):
			inv.Guests = append(inv.Guests, toGuest(x))
		case x.Type == "storage" && can("storage/"+x.Node+"/"+x.Storage):
			inv.Storage = append(inv.Storage, Storage{Name: x.Storage, Node: x.Node, Status: x.Status, Disk: x.Disk, MaxDisk: x.MaxDisk})
		}
	}
	slices.SortFunc(inv.Nodes, func(a, b Node) int { return compareStrings(a.Name, b.Name) })
	slices.SortFunc(inv.Guests, func(a, b Guest) int { return a.VMID - b.VMID })
	slices.SortFunc(inv.Storage, func(a, b Storage) int {
		if a.Node != b.Node {
			return compareStrings(a.Node, b.Node)
		}
		return compareStrings(a.Name, b.Name)
	})
	httpx.WriteJSON(w, http.StatusOK, inv)
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// guest parses type and vmid of the path, checks action on the guest and
// returns its current resource entry.
func (s *Service) guest(w http.ResponseWriter, r *http.Request, action rbac.Action) (*Cluster, Resource, bool) {
	c, _, ok := s.cluster(w, r, rbac.ActionRead)
	if !ok {
		return nil, Resource{}, false
	}
	typ := r.PathValue("type")
	vmid, err := strconv.Atoi(r.PathValue("vmid"))
	if !IsGuestType(typ) || err != nil || vmid <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid guest")
		return nil, Resource{}, false
	}
	if !auth.Check(w, r, rbac.P(Module, c.Name(), guestResource(typ, vmid), action)) {
		return nil, Resource{}, false
	}
	res, err := c.FindGuest(r.Context(), typ, vmid)
	if err != nil {
		upstreamError(w, r, err)
		return nil, Resource{}, false
	}
	return c, res, true
}

// GuestDetail is the response of GET .../guests/{type}/{vmid}.
type GuestDetail struct {
	Guest  Guest          `json:"guest"`
	Status map[string]any `json:"status"`
	Config map[string]any `json:"config"`
}

func (s *Service) handleGuest(w http.ResponseWriter, r *http.Request) {
	c, res, ok := s.guest(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	st, err := c.GuestStatus(r.Context(), res.Node, res.Type, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	cfg, err := c.GuestConfig(r.Context(), res.Node, res.Type, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, GuestDetail{Guest: toGuest(res), Status: st, Config: cfg})
}

// TaskRef is the response of a power action.
type TaskRef struct {
	UPID string `json:"upid"`
	Node string `json:"node"`
}

func (s *Service) handleAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if !IsAction(action) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "unknown action")
		return
	}
	c, res, ok := s.guest(w, r, rbac.ActionOperate)
	if !ok {
		return
	}
	target := c.Name() + "/" + guestResource(res.Type, res.VMID)
	details := map[string]any{"node": res.Node, "name": res.Name}
	upid, err := c.Action(r.Context(), res.Node, res.Type, res.VMID, action)
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, "proxmox.guest."+action, target, audit.ResultFailure, details)
		upstreamError(w, r, err)
		return
	}
	details["upid"] = upid
	s.audit.RecordRequest(r, "proxmox.guest."+action, target, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusAccepted, TaskRef{UPID: upid, Node: res.Node})
}

// Task is the response of GET .../tasks/{upid}.
type Task struct {
	UPID      string    `json:"upid"`
	Node      string    `json:"node"`
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	User      string    `json:"user"`
	Running   bool      `json:"running"`
	ExitState string    `json:"exitStatus"`
	OK        bool      `json:"ok"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitzero"`
	Log       []string  `json:"log"`
}

// maxTaskLog limits the log lines returned with a task.
const maxTaskLog = 500

func (s *Service) handleTask(w http.ResponseWriter, r *http.Request) {
	c, subj, ok := s.cluster(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	u, err := ParseUPID(r.PathValue("upid"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid task ID")
		return
	}
	// A guest task needs read on the guest (VM or container: Proxmox IDs
	// are unique across both), other tasks read on the node.
	readable := subj.Can(rbac.P(Module, c.Name(), nodeResource(u.Node), rbac.ActionRead))
	if id := u.VMID(); id > 0 {
		readable = subj.Can(rbac.P(Module, c.Name(), guestResource(TypeQEMU, id), rbac.ActionRead)) ||
			subj.Can(rbac.P(Module, c.Name(), guestResource(TypeLXC, id), rbac.ActionRead))
	}
	if !readable {
		auth.Check(w, r, rbac.P(Module, c.Name(), "task/"+u.Node+"/"+u.ID, rbac.ActionRead))
		return
	}
	st, err := c.Task(r.Context(), u)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	out := Task{
		UPID: u.Raw, Node: u.Node, Type: st.Type, ID: st.ID, User: st.User, Running: st.Status == "running",
		ExitState: st.ExitState, OK: st.Status != "running" && st.ExitState == "OK",
		StartTime: time.Unix(st.StartTime, 0).UTC(), Log: []string{},
	}
	if st.EndTime > 0 {
		out.EndTime = time.Unix(st.EndTime, 0).UTC()
	}
	if r.URL.Query().Get("log") != "" {
		lines, err := c.TaskLog(r.Context(), u, 0, maxTaskLog)
		if err != nil {
			upstreamError(w, r, err)
			return
		}
		for _, l := range lines {
			out.Log = append(out.Log, l.T)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
