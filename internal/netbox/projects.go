// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"slices"
	"strings"

	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
)

// Project tags (task 12.8, docs/adr/0007-projects.md).

// ProjectTag is the tag of a project as it should be in NetBox.
type ProjectTag struct {
	Slug        string
	Name        string
	Color       string
	Description string
}

// TagSync is the result of a tag synchronization.
type TagSync struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	// Orphaned are tags with the prefix that have no project; they are not
	// deleted.
	Orphaned []string `json:"orphaned"`
}

// SyncProjectTags creates and updates the project tags in NetBox. Tags with
// prefix but without project are reported as orphaned. Without write-back,
// nothing is written; missing and changed tags are then reported as errors.
func (s *Service) SyncProjectTags(ctx context.Context, prefix string, want []ProjectTag) (TagSync, error) {
	out := TagSync{Created: []string{}, Updated: []string{}, Orphaned: []string{}}
	have, err := s.client.Tags(ctx)
	if err != nil {
		return out, err
	}
	bySlug := map[string]TagObject{}
	for _, t := range have {
		bySlug[t.Slug] = t
	}
	var missing []string
	for _, w := range want {
		cur, ok := bySlug[w.Slug]
		body := map[string]any{"name": w.Name, "color": w.Color, "description": w.Description}
		switch {
		case !ok && !s.cfg.WriteBack, ok && !s.cfg.WriteBack && (cur.Name != w.Name || cur.Color != w.Color || cur.Description != w.Description):
			missing = append(missing, w.Slug)
		case !ok:
			body["slug"] = w.Slug
			if err := s.client.Create(ctx, pathTags, body, nil); err != nil {
				return out, fmt.Errorf("create the tag %s: %w", w.Slug, err)
			}
			out.Created = append(out.Created, w.Slug)
		case cur.Name != w.Name || cur.Color != w.Color || cur.Description != w.Description:
			if err := s.client.Update(ctx, pathTags, cur.ID, body, nil); err != nil {
				return out, fmt.Errorf("update the tag %s: %w", w.Slug, err)
			}
			out.Updated = append(out.Updated, w.Slug)
		}
	}
	for _, t := range have {
		if strings.HasPrefix(t.Slug, prefix) && !slices.ContainsFunc(want, func(w ProjectTag) bool { return w.Slug == t.Slug }) {
			out.Orphaned = append(out.Orphaned, t.Slug)
		}
	}
	slices.Sort(out.Orphaned)
	if len(missing) > 0 {
		return out, fmt.Errorf("NetBox: write-back is off and these project tags are missing or differ: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// ProjectPrefix is the prefix of project tags (see internal/project).
const ProjectPrefix = "project-"

// tagIDs returns the IDs of tags by slug. Unknown slugs are left out.
func (s *Service) tagIDs(ctx context.Context) (map[string]int64, error) {
	tags, err := s.client.Tags(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(tags))
	for _, t := range tags {
		out[t.Slug] = t.ID
	}
	return out, nil
}

// projectTagBody returns the tags of an object with its project tags
// replaced by projects (slugs project-<name>), or nil if nothing changes.
// Other tags are kept. Projects whose tag doesn't exist in NetBox are left
// out and returned as missing.
func projectTagBody(current []Tag, projects []string, ids map[string]int64) (tags []any, missing []string) {
	want := map[string]bool{}
	for _, p := range projects {
		if _, ok := ids[p]; ok {
			want[p] = true
		} else {
			missing = append(missing, p)
		}
	}
	changed := false
	have := map[string]bool{}
	for _, t := range current {
		if strings.HasPrefix(t.Slug, ProjectPrefix) && !want[t.Slug] {
			changed = true
			continue
		}
		have[t.Slug] = true
		tags = append(tags, t.ID)
	}
	for _, p := range slices.Sorted(maps.Keys(want)) {
		if !have[p] {
			changed = true
			tags = append(tags, ids[p])
		}
	}
	if !changed {
		return nil, missing
	}
	if tags == nil {
		tags = []any{}
	}
	return tags, missing
}

// ProjectsOf returns the project tags (slugs) of a list of tags.
func ProjectsOf(tags []Tag) []string {
	var out []string
	for _, t := range tags {
		if strings.HasPrefix(t.Slug, ProjectPrefix) {
			out = append(out, t.Slug)
		}
	}
	slices.Sort(out)
	return out
}

// addressProjects returns the project names on the NetBox objects of
// addresses (without prefix length); addresses not in NetBox have none.
func (s *Service) addressProjects(ctx context.Context, addrs []string) ([]string, error) {
	var out []string
	for _, a := range addrs {
		addr, err := netip.ParseAddr(a)
		if err != nil {
			continue
		}
		ip, err := s.findAddress(ctx, addr)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, t := range ProjectsOf(ip.Tags) {
			out = append(out, strings.TrimPrefix(t, ProjectPrefix))
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// CheckAddressProjects checks a change of the projects of addresses (DHCP
// reservations, DNS records): the projects must exist as NetBox tags, and the
// user needs write on every added and removed project. It writes the error
// response. want nil means no change.
func (s *Service) CheckAddressProjects(w http.ResponseWriter, r *http.Request, addrs []string, want *[]string) bool {
	if want == nil {
		return true
	}
	if !s.writing() {
		if len(*want) > 0 {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "projects of addresses need NetBox with write_back")
			return false
		}
		return true
	}
	cur, err := s.addressProjects(r.Context(), addrs)
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	ids, err := s.tagIDs(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	for _, p := range *want {
		if _, ok := ids[ProjectPrefix+p]; !ok {
			httpx.WriteError(w, http.StatusBadRequest, "unknown_project", fmt.Sprintf("unknown project %q", p))
			return false
		}
	}
	for _, p := range slices.Concat(cur, *want) {
		if slices.Contains(cur, p) && slices.Contains(*want, p) {
			continue
		}
		if !auth.Check(w, r, rbac.P("project", p, "objects", rbac.ActionWrite)) {
			return false
		}
	}
	return true
}

// tagProjects sets the projects (names) of an address object.
func (s *Service) tagProjects(ctx context.Context, msg string, ip IPAddress, projects []string, changes *[]string) error {
	ids, err := s.tagIDs(ctx)
	if err != nil {
		return err
	}
	slugs := make([]string, 0, len(projects))
	for _, p := range projects {
		slugs = append(slugs, ProjectPrefix+p)
	}
	tags, _ := projectTagBody(ip.Tags, slugs, ids)
	if tags == nil {
		return nil
	}
	if err := s.client.Update(ctx, pathIPAddresses, ip.ID, map[string]any{"tags": tags, "changelog_message": msg}, nil); err != nil {
		return fmt.Errorf("update the projects of %s: %w", ip.Address, err)
	}
	*changes = append(*changes, "address "+ip.Address+" projects")
	return nil
}
