// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/status"
)

// Service holds the projects and synchronizes their tags to NetBox.
type Service struct {
	cfg   config.Projects
	nb    *netbox.Service
	audit *audit.Log
	log   zerolog.Logger

	mu       sync.RWMutex
	projects []Project
	errs     []string
	// synced is the state of the projects that was last synchronized to
	// NetBox successfully; empty if the next load must synchronize.
	synced   string
	tagErr   string
	orphaned []string

	// guests lists the guests of a project; nil without Proxmox.
	guests GuestSource
}

// AuditTags is the audit action of the tag synchronization.
const AuditTags = "project.tags"

// New loads the projects. nb may be nil (NetBox not configured).
func New(ctx context.Context, cfg config.Projects, nb *netbox.Service, auditLog *audit.Log, log zerolog.Logger) *Service {
	s := &Service{cfg: cfg, nb: nb, audit: auditLog, log: log.With().Str("component", "projects").Logger()}
	s.Reload(ctx)
	return s
}

// Reload reads the project files again and synchronizes the tags if the
// projects changed or the last synchronization failed.
func (s *Service) Reload(ctx context.Context) {
	ps, errs := Load(s.cfg.Dir)
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		msgs = append(msgs, err.Error())
		s.log.Warn().Err(err).Msg("invalid project file")
	}
	state := fingerprint(ps)
	s.mu.Lock()
	changed := !slices.Equal(names(s.projects), names(ps))
	s.projects, s.errs = ps, msgs
	needSync := s.synced != state
	s.mu.Unlock()
	if changed {
		s.log.Info().Int("projects", len(ps)).Msg("projects loaded")
	}
	if needSync && s.nb != nil {
		s.syncTags(ctx, ps, state)
	}
}

func names(ps []Project) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// fingerprint identifies the tag-relevant content of the projects.
func fingerprint(ps []Project) string {
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s\n", p.Name, p.Title, p.Color, p.Description)
	}
	return b.String()
}

func (s *Service) syncTags(ctx context.Context, ps []Project, state string) {
	want := make([]netbox.ProjectTag, 0, len(ps))
	for _, p := range ps {
		want = append(want, netbox.ProjectTag{Slug: p.Tag(), Name: p.Title, Color: p.Color, Description: p.Description})
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.nb.SyncProjectTags(ctx, TagPrefix, want)
	s.mu.Lock()
	s.orphaned = res.Orphaned
	if err != nil {
		s.tagErr = err.Error()
		s.synced = ""
	} else {
		s.tagErr, s.synced = "", state
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Warn().Err(err).Msg("project tags not synchronized to NetBox")
	}
	if len(res.Created)+len(res.Updated) == 0 && err == nil {
		return
	}
	e := audit.Entry{Actor: "dcdash", ActorSource: "system", Action: AuditTags, Target: "netbox", Result: audit.ResultSuccess,
		Details: map[string]any{"created": res.Created, "updated": res.Updated, "orphaned": res.Orphaned}}
	if err != nil {
		e.Result = audit.ResultFailure
		e.Details["error"] = err.Error()
	}
	if s.audit != nil {
		if aerr := s.audit.Record(context.WithoutCancel(ctx), e); aerr != nil {
			s.log.Error().Err(aerr).Msg("audit project.tags")
		}
	}
}

// Run reloads the projects every reload interval until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	if s.cfg.ReloadInterval <= 0 {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(s.cfg.ReloadInterval.D())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.Reload(ctx)
		}
	}
}

// Projects returns the loaded projects, sorted by name.
func (s *Service) Projects() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.projects)
}

// Get returns a project by name.
func (s *Service) Get(name string) (Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := slices.IndexFunc(s.projects, func(p Project) bool { return p.Name == name })
	if i < 0 {
		return Project{}, false
	}
	return s.projects[i], true
}

// Exists reports whether a project exists. A nil service has no projects.
func (s *Service) Exists(name string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Get(name)
	return ok
}

// State is the load and synchronization state, for the status check and the
// project list.
type State struct {
	Errors   []string `json:"errors"`
	TagError string   `json:"tagError,omitempty"`
	Orphaned []string `json:"orphaned"`
}

// State returns the load and synchronization state.
func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return State{Errors: append([]string{}, s.errs...), TagError: s.tagErr, Orphaned: append([]string{}, s.orphaned...)}
}

// StatusChecks returns the check for the overview page.
func (s *Service) StatusChecks() []status.Check {
	return []status.Check{{Name: "projects", Kind: "system", Detail: s.check}}
}

func (s *Service) check(context.Context) (status.Detail, error) {
	st := s.State()
	n := len(s.Projects())
	switch {
	case len(st.Errors) > 0:
		return status.Detail{}, &status.Error{Code: "projects.invalid",
			Params: map[string]string{"count": strconv.Itoa(len(st.Errors)), "first": st.Errors[0]},
			Err:    fmt.Errorf("%d invalid project files: %s", len(st.Errors), strings.Join(st.Errors, "; "))}
	case st.TagError != "":
		return status.Detail{}, errors.New("project tags: " + st.TagError)
	}
	return status.Detail{Text: fmt.Sprintf("%d projects", n), Code: "projects.loaded",
		Params: map[string]string{"count": strconv.Itoa(n)}}, nil
}
