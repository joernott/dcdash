// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/rbac"
)

// Module is the permission module of projects: "project:<name>:objects:read"
// shows a project, "project:<name>:objects:write" assigns objects to it.
const Module = "project"

// Audit actions of assignments.
const (
	AuditAssign   = "project.assign"
	AuditUnassign = "project.unassign"
)

// SetGuestSource connects the Proxmox module; nil if not configured.
func (s *Service) SetGuestSource(g GuestSource) { s.guests = g }

// Register adds the routes of the module.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects", s.handleList)
	mux.HandleFunc("GET /api/v1/projects/{name}", s.handleProject)
	mux.HandleFunc("GET /api/v1/projects/{name}/search", s.handleSearch)
	mux.HandleFunc("POST /api/v1/projects/{name}/objects", s.handleAdd)
	mux.HandleFunc("DELETE /api/v1/projects/{name}/objects/{type}/{id}", s.handleRemove)
}

func permission(name string, action rbac.Action) rbac.Permission {
	return rbac.P(Module, name, "objects", action)
}

// View is a project in the API.
type View struct {
	Project
	Tag string `json:"tag"`
	// CanWrite is true if the user may assign objects to the project.
	CanWrite bool `json:"canWrite"`
}

// List is the response of GET /api/v1/projects.
type List struct {
	Projects []View `json:"projects"`
	// State is only shown to users with access to all projects.
	State *State `json:"state,omitempty"`
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := List{Projects: []View{}}
	for _, p := range s.Projects() {
		if subj.Can(permission(p.Name, rbac.ActionRead)) {
			out.Projects = append(out.Projects, View{Project: p, Tag: p.Tag(), CanWrite: subj.Can(permission(p.Name, rbac.ActionWrite))})
		}
	}
	if subj.Can(permission("*", rbac.ActionRead)) {
		st := s.State()
		out.State = &st
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// project returns the project of the request after checking the permission.
func (s *Service) project(w http.ResponseWriter, r *http.Request, action rbac.Action) (Project, bool) {
	name := r.PathValue("name")
	if !auth.Check(w, r, permission(name, action)) {
		return Project{}, false
	}
	p, ok := s.Get(name)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "unknown project")
		return Project{}, false
	}
	return p, true
}

// GuestView is a guest of a project; Visible is false for guests the user
// may not read (shown without link).
type GuestView struct {
	Guest
	Visible bool `json:"visible"`
}

// ObjectView is a NetBox object of a project.
type ObjectView struct {
	netbox.Object
	// ProxmoxCluster is the Proxmox cluster of a NetBox VM, if mapped.
	ProxmoxCluster string `json:"proxmoxCluster,omitempty"`
}

// Page is the response of GET /api/v1/projects/{name}.
type Page struct {
	View
	Guests []GuestView `json:"guests"`
	// NetBox are the NetBox objects by type (vm, ip, iprange, prefix, vlan,
	// interface); missing without NetBox.
	NetBox map[string][]ObjectView `json:"netbox,omitempty"`
	// Errors are sources that couldn't be read.
	Errors []string `json:"errors"`
}

func (s *Service) handleProject(w http.ResponseWriter, r *http.Request) {
	p, ok := s.project(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	subj := auth.SubjectFromContext(r.Context())
	out := Page{View: View{Project: p, Tag: p.Tag(), CanWrite: subj.Can(permission(p.Name, rbac.ActionWrite))},
		Guests: []GuestView{}, Errors: []string{}}
	if s.guests != nil {
		gs, errs := s.guests.ProjectGuests(r.Context(), p.Tag())
		for _, g := range gs {
			visible := subj.Can(rbac.P("proxmox", g.Cluster, g.Type+"/"+strconv.Itoa(g.VMID), rbac.ActionRead))
			if !visible {
				g.Node, g.Status = "", ""
			}
			out.Guests = append(out.Guests, GuestView{Guest: g, Visible: visible})
		}
		for _, err := range errs {
			out.Errors = append(out.Errors, "Proxmox "+err.Error())
		}
	}
	if s.nb != nil {
		objs, err := s.nb.ProjectObjects(r.Context(), p.Tag())
		if err != nil {
			out.Errors = append(out.Errors, err.Error())
		} else {
			out.NetBox = map[string][]ObjectView{}
			for typ, l := range objs {
				views := make([]ObjectView, 0, len(l))
				for _, o := range l {
					views = append(views, ObjectView{Object: o, ProxmoxCluster: s.nb.ProxmoxCluster(o.Cluster)})
				}
				out.NetBox[typ] = views
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.project(w, r, rbac.ActionWrite); !ok {
		return
	}
	if s.nb == nil {
		httpx.WriteError(w, http.StatusConflict, "netbox_missing", "NetBox isn't configured")
		return
	}
	typ, q := r.URL.Query().Get("type"), r.URL.Query().Get("q")
	if _, ok := netbox.ObjectTypes[typ]; !ok || q == "" || len(q) > 100 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "type and q (at most 100 characters) are required")
		return
	}
	l, err := s.nb.SearchObjects(r.Context(), typ, q)
	if err != nil {
		writeNetBoxError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"objects": l})
}

// ObjectRequest is the body of POST /api/v1/projects/{name}/objects.
type ObjectRequest struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
}

func (s *Service) handleAdd(w http.ResponseWriter, r *http.Request) {
	var req ObjectRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.setObject(w, r, req.Type, req.ID, true)
}

func (s *Service) handleRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "numeric ID required")
		return
	}
	s.setObject(w, r, r.PathValue("type"), id, false)
}

// setObject adds a NetBox object to a project or removes it. VMs of guests
// are changed through the guest (Proxmox tags), not here.
func (s *Service) setObject(w http.ResponseWriter, r *http.Request, typ string, id int64, add bool) {
	p, ok := s.project(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	if s.nb == nil {
		httpx.WriteError(w, http.StatusConflict, "netbox_missing", "NetBox isn't configured")
		return
	}
	if _, ok := netbox.ObjectTypes[typ]; !ok || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "unknown object type or ID")
		return
	}
	action := AuditUnassign
	if add {
		action = AuditAssign
	}
	details := map[string]any{"object": typ, "id": id}
	o, err := s.nb.SetObjectProject(r.Context(), netbox.Base(r), typ, id, p.Tag(), add)
	if err != nil {
		details["error"] = err.Error()
		s.audit.RecordRequest(r, action, p.Name, audit.ResultFailure, details)
		writeNetBoxError(w, r, err)
		return
	}
	details["display"] = o.Display
	s.audit.RecordRequest(r, action, p.Name, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusOK, o)
}

func writeNetBoxError(w http.ResponseWriter, r *http.Request, err error) {
	hlog.FromRequest(r).Warn().Err(err).Msg("project: NetBox request failed")
	switch {
	case errors.Is(err, netbox.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, netbox.ErrNoTag):
		httpx.WriteError(w, http.StatusConflict, "tag_missing", err.Error())
	case errors.Is(err, netbox.ErrUnavailable):
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "NetBox not reachable")
	default:
		httpx.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
	}
}
