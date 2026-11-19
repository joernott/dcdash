// SPDX-License-Identifier: GPL-3.0-or-later

package proxmox

import (
	"context"
	"net/http"
	"slices"
	"testing"
)

func init() {
	provUsers["projector"] = []string{"proxmox:lab:qemu/5*:write", "proxmox:lab:lxc/5*:admin", "project:webshop:objects:write"}
	provUsers["outsider"] = []string{"proxmox:lab:qemu/5*:admin"}
}

type knownProjects []string

func (k knownProjects) Exists(name string) bool { return slices.Contains(k, name) }

func TestGuestProjects(t *testing.T) {
	e := newProvEnv(t)
	e.svc.SetProjects(knownProjects{"webshop", "intranet"})
	path := "/lab/guests/qemu/5100/projects"
	var res ProjectsResult
	if st, msg := e.do(t, "projector", http.MethodPut, path, ProjectsRequest{Projects: []string{"webshop"}}, &res); st != http.StatusOK {
		t.Fatal(st, msg)
	}
	g, _ := e.fake.Guest("qemu", 5100)
	if g.Tags != "project-webshop" || len(res.Projects) != 1 {
		t.Errorf("tags %q, %+v", g.Tags, res)
	}
	l := e.entries(t, "project.assign")
	if len(l) != 1 || l[0].Target != "webshop" || l[0].Details["guest"] != "lab/qemu/5100" {
		t.Errorf("audit: %+v", l)
	}
	for _, c := range []struct {
		user     string
		projects []string
		status   int
	}{
		{"projector", []string{"unknown"}, http.StatusBadRequest},
		{"projector", []string{"intranet"}, http.StatusForbidden}, // no permission on intranet
		{"outsider", nil, http.StatusForbidden},                   // may not remove webshop
		{"viewer", nil, http.StatusForbidden},                     // no write on the guest
	} {
		if st, _ := e.do(t, c.user, http.MethodPut, path, ProjectsRequest{Projects: c.projects}, nil); st != c.status {
			t.Errorf("%s %v: %d, want %d", c.user, c.projects, st, c.status)
		}
	}
	if st, _ := e.do(t, "projector", http.MethodPut, path, ProjectsRequest{}, &res); st != http.StatusOK {
		t.Fatal(st)
	}
	if g, _ := e.fake.Guest("qemu", 5100); g.Tags != "" {
		t.Errorf("tags after removing: %q", g.Tags)
	}

	// The tags option needs the project permission for project tags.
	hw := e.hardware(t)
	opt := OptionsRequest{Digest: hw.Digest, Set: map[string]string{"tags": "dev;project-intranet"}}
	if st, _ := e.do(t, "projector", http.MethodPost, hw5100+"/options", opt, nil); st != http.StatusForbidden {
		t.Errorf("tags option: %d", st)
	}
	opt.Set["tags"] = "dev;project-webshop"
	if st, msg := e.do(t, "projector", http.MethodPost, hw5100+"/options", opt, nil); st != http.StatusOK {
		t.Errorf("tags option: %d %s", st, msg)
	}

	gs, errs := e.svc.ProjectGuests(context.Background(), "project-webshop")
	// Both test clusters use the same fake.
	if len(errs) != 0 || len(gs) != 2 || gs[0].VMID != 5100 || gs[0].Cluster != "lab" || gs[1].Cluster != "off" {
		t.Errorf("project guests: %+v %v", gs, errs)
	}
}

func TestCreateWithProjects(t *testing.T) {
	e := newProvEnv(t)
	e.svc.SetProjects(knownProjects{"webshop"})
	provUsers["projectcreator"] = append(slices.Clone(provUsers["creator"]), "project:webshop:objects:write")
	req := cloneRequest()
	req.Projects = []string{"webshop"}
	j := e.create(t, "projectcreator", req)
	if j.State != JobDone {
		t.Fatalf("job: %+v", j)
	}
	if conf := e.lastCall(t, http.MethodPut, "/qemu/5000/config"); conf.Get("tags") != "dev;project-webshop" {
		t.Errorf("tags: %q", conf.Get("tags"))
	}
	// Without the project permission, also as a plain tag.
	if st, _ := e.do(t, "creator", http.MethodPost, "/lab/guests", req, nil); st != http.StatusForbidden {
		t.Errorf("without permission: %d", st)
	}
	req.Projects, req.Tags = nil, []string{"project-webshop"}
	if st, _ := e.do(t, "creator", http.MethodPost, "/lab/guests", req, nil); st != http.StatusForbidden {
		t.Errorf("project tag without permission: %d", st)
	}
}
