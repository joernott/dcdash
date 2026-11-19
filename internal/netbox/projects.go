// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
