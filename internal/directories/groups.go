// SPDX-License-Identifier: GPL-3.0-or-later

package directories

import (
	"net/http"
	"slices"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Audit actions for groups.
const (
	AuditGroupCreate  = "directory.group.create"
	AuditGroupUpdate  = "directory.group.update"
	AuditGroupDelete  = "directory.group.delete"
	AuditGroupMembers = "directory.group.members"
)

// maxMembersChange limits the members added and removed in one request.
const maxMembersChange = 500

// GroupView is a group with what the requesting user may do with it.
type GroupView struct {
	ad.Group
	// CanWrite: change, delete and change the members.
	CanWrite bool `json:"canWrite"`
}

func groupView(subj *rbac.Subject, b Backend, g ad.Group) GroupView {
	return GroupView{Group: g, CanWrite: may(subj, b, GroupResource(g.Name), g.Managed, g.Protected, rbac.ActionWrite)}
}

var scopes = []string{ad.ScopeGlobal, ad.ScopeDomainLocal, ad.ScopeUniversal}

func (s *Service) handleGroups(w http.ResponseWriter, r *http.Request) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	groups, truncated, err := b.Groups(r.Context(), r.URL.Query().Get("q"), listLimit(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]GroupView, 0, len(groups))
	for _, g := range groups {
		if subj.Can(perm(b, GroupResource(g.Name), rbac.ActionRead)) {
			out = append(out, groupView(subj, b, g))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"groups": out, "truncated": truncated})
}

// findGroup reads the group of the request (with its members) and checks the action like
// findUser.
func (s *Service) findGroup(w http.ResponseWriter, r *http.Request, action rbac.Action) (Backend, *rbac.Subject, ad.Group, bool) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return nil, nil, ad.Group{}, false
	}
	name := r.PathValue("name")
	if !subj.Can(perm(b, GroupResource(name), rbac.ActionRead)) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "group not found")
		return nil, nil, ad.Group{}, false
	}
	g, err := b.Group(r.Context(), name)
	if err != nil {
		s.writeError(w, r, err)
		return nil, nil, ad.Group{}, false
	}
	if action > rbac.ActionRead && !may(subj, b, GroupResource(g.Name), g.Managed, g.Protected, action) {
		msg := "permission denied"
		switch {
		case !g.Managed:
			msg = "the group is outside the containers dcdash manages"
		case g.Protected:
			msg = "the group is protected; changing it needs the admin permission"
		}
		httpx.WriteError(w, http.StatusForbidden, "forbidden", msg)
		return nil, nil, ad.Group{}, false
	}
	return b, subj, g, true
}

func (s *Service) handleGroup(w http.ResponseWriter, r *http.Request) {
	b, subj, g, ok := s.findGroup(w, r, rbac.ActionRead)
	if !ok {
		return
	}
	if g.Members == nil {
		g.Members = []ad.Member{}
	}
	httpx.WriteJSON(w, http.StatusOK, groupView(subj, b, g))
}

func (s *Service) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	b, subj, ok := s.lookup(w, r)
	if !ok {
		return
	}
	var req ad.NewGroup
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Scope == "" {
		req.Scope = ad.ScopeGlobal
	}
	msg := validName(req.Name, 64)
	if msg == "" {
		msg = validText(map[string]string{"description": req.Description, "mail": req.Mail})
	}
	if msg == "" && !slices.Contains(scopes, req.Scope) {
		msg = "scope: global, domainLocal or universal"
	}
	if msg == "" && !b.Config().Managed(req.OU) {
		msg = "ou: not a container dcdash manages"
	}
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_group", msg)
		return
	}
	if !auth.Check(w, r, perm(b, GroupResource(req.Name), rbac.ActionWrite)) {
		return
	}
	tgt := b.Name() + "/" + GroupResource(req.Name)
	details := map[string]any{"ou": req.OU, "scope": req.Scope, "security": req.Security, "description": req.Description}
	g, err := b.CreateGroup(r.Context(), req)
	if err != nil {
		s.fail(w, r, AuditGroupCreate, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditGroupCreate, tgt, audit.ResultSuccess, details)
	httpx.WriteJSON(w, http.StatusCreated, groupView(subj, b, g))
}

func (s *Service) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var req ad.GroupChange
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	fields := map[string]string{}
	details := map[string]any{}
	for k, v := range map[string]*string{"description": req.Description, "mail": req.Mail} {
		if v != nil {
			fields[k] = *v
			details[k] = *v
		}
	}
	msg := validText(fields)
	if msg == "" && req.Name != nil {
		msg = validName(*req.Name, 64)
		details["name"] = *req.Name
	}
	if msg == "" && req.Scope != nil {
		if !slices.Contains(scopes, *req.Scope) {
			msg = "scope: global, domainLocal or universal"
		}
		details["scope"] = *req.Scope
	}
	if req.Security != nil {
		details["security"] = *req.Security
	}
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_group", msg)
		return
	}
	b, subj, g, ok := s.findGroup(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	name := g.Name
	// A rename also needs write on the new name.
	if req.Name != nil && !auth.Check(w, r, perm(b, GroupResource(*req.Name), rbac.ActionWrite)) {
		return
	}
	tgt := b.Name() + "/" + GroupResource(g.Name)
	if err := b.UpdateGroup(r.Context(), g.Name, req); err != nil {
		s.fail(w, r, AuditGroupUpdate, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditGroupUpdate, tgt, audit.ResultSuccess, details)
	if req.Name != nil {
		name = *req.Name
	}
	s.respondGroup(w, r, b, subj, name)
}

func (s *Service) respondGroup(w http.ResponseWriter, r *http.Request, b Backend, subj *rbac.Subject, name string) {
	g, err := b.Group(r.Context(), name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if g.Members == nil {
		g.Members = []ad.Member{}
	}
	httpx.WriteJSON(w, http.StatusOK, groupView(subj, b, g))
}

func (s *Service) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	b, _, g, ok := s.findGroup(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + GroupResource(g.Name)
	details := map[string]any{"dn": g.DN, "members": len(g.Members)}
	if err := b.DeleteGroup(r.Context(), g.Name); err != nil {
		s.fail(w, r, AuditGroupDelete, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditGroupDelete, tgt, audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

// MembersChange is the body of POST .../groups/{name}/members: account names of users,
// groups or computers to add and remove.
type MembersChange struct {
	Add    []string `json:"add"`
	Remove []string `json:"remove"`
}

func (s *Service) handleMembers(w http.ResponseWriter, r *http.Request) {
	var req MembersChange
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(req.Add)+len(req.Remove) == 0 || len(req.Add)+len(req.Remove) > maxMembersChange {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_members", "add or remove 1 to 500 members")
		return
	}
	for _, n := range append(slices.Clone(req.Add), req.Remove...) {
		if validName(n, 64) != "" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_members", "invalid account name: "+n)
			return
		}
	}
	b, subj, g, ok := s.findGroup(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	tgt := b.Name() + "/" + GroupResource(g.Name)
	details := map[string]any{"add": req.Add, "remove": req.Remove}
	if err := b.SetMembers(r.Context(), g.Name, req.Add, req.Remove); err != nil {
		s.fail(w, r, AuditGroupMembers, tgt, details, err)
		return
	}
	s.audit.RecordRequest(r, AuditGroupMembers, tgt, audit.ResultSuccess, details)
	s.respondGroup(w, r, b, subj, g.Name)
}
