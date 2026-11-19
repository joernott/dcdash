// SPDX-License-Identifier: GPL-3.0-or-later

package netbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/audit"
)

// Objects of projects in NetBox (task 12.8.3/12.8.4, docs/adr/0007-projects.md).

// ObjectTypes are the NetBox object types that can belong to a project, with
// their API paths.
var ObjectTypes = map[string]string{
	"vm":        pathVMs,
	"ip":        pathIPAddresses,
	"iprange":   "/ipam/ip-ranges/",
	"prefix":    pathPrefixes,
	"vlan":      pathVLANs,
	"interface": "/dcim/interfaces/",
}

// objectTypeOrder is the order of the types on the project page.
var objectTypeOrder = []string{"vm", "ip", "iprange", "prefix", "vlan", "interface"}

// Object is a NetBox object of a project.
type Object struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
	// Display is the name of the object, e.g. a prefix or "switch1 1/1/1".
	Display string `json:"display"`
	// Detail is additional information: DNS name, description, VLAN name.
	Detail string `json:"detail,omitempty"`
	// URL is the object's page in NetBox.
	URL string `json:"url,omitempty"`
	// VM fields: the NetBox cluster and the VMID custom field (0 if unknown).
	Cluster string `json:"cluster,omitempty"`
	VMID    int    `json:"vmid,omitempty"`
}

// object converts the API representation of an object.
func (s *Service) object(typ string, o map[string]any) Object {
	str := func(k string) string { v, _ := o[k].(string); return v }
	nested := func(k, f string) string {
		if m, ok := o[k].(map[string]any); ok {
			v, _ := m[f].(string)
			return v
		}
		return ""
	}
	id, _ := o["id"].(float64)
	out := Object{Type: typ, ID: int64(id), Display: str("display"), URL: str("display_url")}
	switch typ {
	case "vm":
		out.Display, out.Cluster = str("name"), nested("cluster", "name")
		if f := s.cfg.VMIDField; f != "" {
			if cf, ok := o["custom_fields"].(map[string]any); ok {
				if v, ok := cf[f].(float64); ok {
					out.VMID = int(v)
				}
			}
		}
	case "ip":
		out.Display, out.Detail = str("address"), strings.TrimSpace(str("dns_name")+" "+str("description"))
	case "iprange":
		out.Display, out.Detail = str("start_address")+" – "+str("end_address"), str("description")
	case "prefix":
		out.Display, out.Detail = str("prefix"), str("description")
	case "vlan":
		vid, _ := o["vid"].(float64)
		out.Display, out.Detail = strconv.Itoa(int(vid))+" "+str("name"), nested("group", "name")
	case "interface":
		out.Display, out.Detail = strings.TrimSpace(nested("device", "name")+" "+str("name")), str("description")
	}
	if out.Display == "" {
		out.Display = "#" + strconv.FormatInt(out.ID, 10)
	}
	return out
}

// ProjectObjects returns the NetBox objects with the tag of a project, by
// type.
func (s *Service) ProjectObjects(ctx context.Context, tag string) (map[string][]Object, error) {
	out := map[string][]Object{}
	for _, typ := range objectTypeOrder {
		l, err := list[map[string]any](ctx, s.client, ObjectTypes[typ], url.Values{"tag": {tag}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", typ, err)
		}
		objs := make([]Object, 0, len(l))
		for _, o := range l {
			objs = append(objs, s.object(typ, o))
		}
		out[typ] = objs
	}
	return out, nil
}

// maxSearch limits the results of a search.
const maxSearch = 25

// SearchObjects searches NetBox objects of a type by text (NetBox's q
// filter).
func (s *Service) SearchObjects(ctx context.Context, typ, q string) ([]Object, error) {
	path, ok := ObjectTypes[typ]
	if !ok {
		return nil, fmt.Errorf("%w: object type %q", ErrNotFound, typ)
	}
	var p page[map[string]any]
	if err := s.client.do(ctx, http.MethodGet, path, url.Values{"q": {q}, "limit": {strconv.Itoa(maxSearch)}}, nil, &p); err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(p.Results))
	for _, o := range p.Results {
		out = append(out, s.object(typ, o))
	}
	return out, nil
}

// ErrNoTag is returned if the tag of a project doesn't exist in NetBox.
var ErrNoTag = errors.New("the project's tag doesn't exist in NetBox")

// SetObjectProject adds the tag of a project to a NetBox object or removes
// it. It returns the object; the change is audited by the caller.
func (s *Service) SetObjectProject(ctx context.Context, base audit.Entry, typ string, id int64, tag string, add bool) (Object, error) {
	path, ok := ObjectTypes[typ]
	if !ok {
		return Object{}, fmt.Errorf("%w: object type %q", ErrNotFound, typ)
	}
	if !s.writing() {
		return Object{}, errors.New("NetBox: write_back is off")
	}
	var o struct {
		Tags []Tag `json:"tags"`
	}
	var raw map[string]any
	if err := s.client.do(ctx, http.MethodGet, path+strconv.FormatInt(id, 10)+"/", nil, nil, &raw); err != nil {
		return Object{}, err
	}
	if tl, ok := raw["tags"].([]any); ok {
		for _, t := range tl {
			if m, ok := t.(map[string]any); ok {
				tid, _ := m["id"].(float64)
				slug, _ := m["slug"].(string)
				o.Tags = append(o.Tags, Tag{ID: int64(tid), Slug: slug})
			}
		}
	}
	ids, err := s.tagIDs(ctx)
	if err != nil {
		return Object{}, err
	}
	if _, ok := ids[tag]; !ok {
		return Object{}, ErrNoTag
	}
	var want []string
	for _, p := range ProjectsOf(o.Tags) {
		if p != tag {
			want = append(want, p)
		}
	}
	if add {
		want = append(want, tag)
	}
	if tags, _ := projectTagBody(o.Tags, want, ids); tags != nil {
		verb := "remove from "
		if add {
			verb = "add to "
		}
		var updated map[string]any
		if err := s.client.Update(ctx, path, id, map[string]any{"tags": tags, "changelog_message": message(base, verb+tag)}, &updated); err != nil {
			return Object{}, err
		}
		raw = updated
	}
	return s.object(typ, raw), nil
}
