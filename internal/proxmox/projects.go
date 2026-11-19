// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/project"
	"github.com/joernott/dcdash/internal/rbac"
)

// Projects of guests (task 12.8.3, docs/adr/0007-projects.md): a guest's
// projects are its Proxmox tags project-<name>.

// ProjectPrefix is the prefix of project tags.
const ProjectPrefix = "project-"

// Projects knows the configured projects (the project module).
type Projects interface {
	Exists(name string) bool
}

// SetProjects connects the project module; nil if there is none.
func (s *Service) SetProjects(p Projects) { s.projects = p }

// projectsOfTags returns the project names of tags.
func projectsOfTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if n, ok := strings.CutPrefix(t, ProjectPrefix); ok && n != "" {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// projectPermission is the permission to assign objects to a project.
func projectPermission(name string) rbac.Permission {
	return rbac.P("project", name, "objects", rbac.ActionWrite)
}

// checkProjects checks that the projects exist and that the user may assign
// objects to them. It writes the error response.
func (s *Service) checkProjects(w http.ResponseWriter, r *http.Request, names []string) bool {
	for _, n := range names {
		if s.projects == nil || !s.projects.Exists(n) {
			httpx.WriteError(w, http.StatusBadRequest, "unknown_project", fmt.Sprintf("unknown project %q", n))
			return false
		}
		if !auth.Check(w, r, projectPermission(n)) {
			return false
		}
	}
	return true
}

// ProjectsRequest is the body of PUT .../guests/{type}/{vmid}/projects.
type ProjectsRequest struct {
	Projects []string `json:"projects"`
}

// ProjectsResult is the response of PUT .../guests/{type}/{vmid}/projects.
type ProjectsResult struct {
	Projects []string `json:"projects"`
	// NetBox is the result of the write-back to NetBox.
	NetBox *netbox.Result `json:"netbox,omitempty"`
}

// handleProjects replaces the projects of a guest: its project tags.
// Assigning and removing a project needs write on the guest and on the
// project.
func (s *Service) handleProjects(w http.ResponseWriter, r *http.Request) {
	var req ProjectsRequest
	if !decode(w, r, &req) {
		return
	}
	c, res, ok := s.guest(w, r, rbac.ActionWrite)
	if !ok {
		return
	}
	cfg, err := c.GuestConfig(r.Context(), res.Node, res.Type, res.VMID)
	if err != nil {
		upstreamError(w, r, err)
		return
	}
	tags := splitTags(fmt.Sprint(cfg["tags"]))
	if cfg["tags"] == nil {
		tags = nil
	}
	cur := projectsOfTags(tags)
	want := slices.Compact(slices.Sorted(slices.Values(req.Projects)))
	added := slices.DeleteFunc(slices.Clone(want), func(p string) bool { return slices.Contains(cur, p) })
	removed := slices.DeleteFunc(slices.Clone(cur), func(p string) bool { return slices.Contains(want, p) })
	if !s.checkProjects(w, r, added) {
		return
	}
	for _, p := range removed {
		if !auth.Check(w, r, projectPermission(p)) {
			return
		}
	}
	out := ProjectsResult{Projects: want}
	if len(added)+len(removed) == 0 {
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	next := slices.DeleteFunc(tags, func(t string) bool { return strings.HasPrefix(t, ProjectPrefix) })
	for _, p := range want {
		next = append(next, ProjectPrefix+p)
	}
	form := url.Values{"tags": {strings.Join(next, ";")}}
	if d, ok := cfg["digest"].(string); ok && d != "" {
		form.Set("digest", d)
	}
	target := c.Name() + "/" + guestResource(res.Type, res.VMID)
	err = c.setGuestConfig(r.Context(), res.Node, res.Type, res.VMID, form)
	for _, list := range []struct {
		action string
		names  []string
	}{{"project.assign", added}, {"project.unassign", removed}} {
		for _, p := range list.names {
			details := map[string]any{"object": "guest", "guest": target, "name": configName(cfg, res.Name)}
			result := audit.ResultSuccess
			if err != nil {
				result, details["error"] = audit.ResultFailure, err.Error()
			}
			s.audit.RecordRequest(r, list.action, p, result, details)
		}
	}
	if err != nil {
		writeConfigError(w, r, err)
		return
	}
	out.NetBox = s.syncNetBox(r.Context(), netbox.Base(r), c, res.Node, res.Type, res.VMID, res.Name)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setGuestConfig changes the configuration of a VM (and waits for the task)
// or a container.
func (c *Cluster) setGuestConfig(ctx context.Context, node, typ string, vmid int, form url.Values) error {
	if typ == TypeQEMU {
		upid, err := c.ConfigureVM(ctx, node, vmid, form)
		if err != nil || upid == "" {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, configTimeout)
		defer cancel()
		return c.WaitTask(ctx, upid)
	}
	p, err := guestPath(node, TypeLXC, vmid)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, p+"/config", form, nil)
}

// ProjectGuests implements project.GuestSource: the guests with the tag in
// all clusters (templates included).
func (s *Service) ProjectGuests(ctx context.Context, tag string) ([]project.Guest, []error) {
	var (
		out  []project.Guest
		errs []error
	)
	for _, name := range s.order {
		rs, err := s.clusters[name].Resources(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		for _, r := range rs {
			if (r.Type == TypeQEMU || r.Type == TypeLXC) && slices.Contains(splitTags(r.Tags), tag) {
				out = append(out, project.Guest{Cluster: name, Type: r.Type, VMID: r.VMID, Name: r.Name, Node: r.Node, Status: r.Status})
			}
		}
	}
	return out, errs
}
