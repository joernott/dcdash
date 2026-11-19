// SPDX-License-Identifier: GPL-3.0-or-later

package project_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox/nbfake"
	"github.com/joernott/dcdash/internal/project"
	"github.com/joernott/dcdash/internal/store"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "webshop.yaml", "title: Webshop relaunch\ncolor: \"#2196F3\"\ndescription: New shop\n")
	write(t, dir, "intranet.yaml", "")
	write(t, dir, "Bad_Name.yaml", "title: x\n")
	write(t, dir, "color.yaml", "color: blue\n")
	write(t, dir, "typo.yaml", "titel: x\n")
	write(t, dir, "zcopy.yaml", "title: Webshop relaunch\n")
	write(t, dir, "notes.txt", "ignored")
	ps, errs := project.Load(dir)
	if len(ps) != 2 || ps[0].Name != "intranet" || ps[0].Title != "project-intranet" || ps[0].Color != project.DefaultColor ||
		ps[1].Name != "webshop" || ps[1].Title != "Webshop relaunch" || ps[1].Color != "2196f3" || ps[1].Tag() != "project-webshop" {
		t.Errorf("projects: %+v", ps)
	}
	all := ""
	for _, e := range errs {
		all += e.Error() + "\n"
	}
	for _, want := range []string{"Bad_Name.yaml: the file name", `color.yaml: color "blue"`, "typo.yaml:", `zcopy.yaml: the title "Webshop relaunch" is used by webshop.yaml`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if ps, errs := project.Load(filepath.Join(dir, "missing")); ps != nil || errs != nil {
		t.Errorf("missing dir: %v %v", ps, errs)
	}
	if n, ok := project.NameOfTag("project-webshop"); !ok || n != "webshop" {
		t.Error("NameOfTag")
	}
	if _, ok := project.NameOfTag("prod"); ok {
		t.Error("NameOfTag of other tag")
	}
}

func TestTagSync(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	write(t, dir, "webshop.yaml", "title: Webshop\ncolor: 2196f3\n")
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auditLog := audit.New(st.DB(), zerolog.Nop())
	f, nb := nbfake.NewService(t, auditLog)
	f.Seed(nbfake.Tags, nbfake.Object{"name": "Old project", "slug": "project-old"})
	s := project.New(ctx, config.Projects{Dir: dir}, nb, auditLog, zerolog.Nop())
	tag := func(slug string) nbfake.Object {
		for _, o := range f.Objects(nbfake.Tags) {
			if o["slug"] == slug {
				return o
			}
		}
		return nil
	}
	if o := tag("project-webshop"); o == nil || o["name"] != "Webshop" || o["color"] != "2196f3" {
		t.Fatalf("tag: %+v", f.Objects(nbfake.Tags))
	}
	if st := s.State(); len(st.Orphaned) != 1 || st.Orphaned[0] != "project-old" || st.TagError != "" {
		t.Errorf("state: %+v", st)
	}
	d, err := s.StatusChecks()[0].Detail(ctx)
	if err != nil || d.Code != "projects.loaded" || d.Params["count"] != "1" {
		t.Errorf("status: %+v %v", d, err)
	}

	// Changed color and a new project; unchanged projects aren't written again.
	write(t, dir, "webshop.yaml", "title: Webshop\ncolor: f44336\n")
	write(t, dir, "intranet.yaml", "")
	n := len(f.Changes())
	s.Reload(ctx)
	if tag("project-webshop")["color"] != "f44336" || tag("project-intranet") == nil || len(f.Changes()) != n+2 {
		t.Errorf("after reload: %+v", f.Objects(nbfake.Tags))
	}
	s.Reload(ctx)
	if len(f.Changes()) != n+2 {
		t.Error("unchanged projects synchronized again")
	}
	l, _ := auditLog.Query(ctx, audit.Filter{Action: project.AuditTags})
	if len(l) != 2 || l[0].Actor != "dcdash" {
		t.Errorf("audit: %+v", l)
	}

	// NetBox down: the error is reported and the next reload retries.
	write(t, dir, "intranet.yaml", "color: 000000\n")
	f.FailNext("GET", nbfake.Tags, 500)
	s.Reload(ctx)
	if _, err := s.StatusChecks()[0].Detail(ctx); err == nil || !strings.Contains(err.Error(), "project tags") {
		t.Errorf("status: %v", err)
	}
	s.Reload(ctx)
	if tag("project-intranet")["color"] != "000000" || s.State().TagError != "" {
		t.Errorf("retry: %+v", s.State())
	}

	// An invalid file is reported on the overview page.
	write(t, dir, "x.yaml", "color: nope\n")
	s.Reload(ctx)
	if _, err := s.StatusChecks()[0].Detail(ctx); err == nil || !strings.Contains(err.Error(), "x.yaml") {
		t.Errorf("invalid file: %v", err)
	}
	if !s.Exists("webshop") || s.Exists("x") || (*project.Service)(nil).Exists("webshop") {
		t.Error("Exists")
	}
}
