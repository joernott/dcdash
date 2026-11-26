// SPDX-License-Identifier: GPL-3.0-or-later

// Package directories is dcdash's Active Directory module: users, groups and passwords of
// Windows and Samba domains (package ad), with permission checks and audit, and the
// self-service password change. See docs/adr/0010-active-directory.md.
package directories

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/status"
)

// Module is the permission module of directories. The instance is the directory name, the
// resource "user/<sAMAccountName>" or "group/<sAMAccountName>":
// "directory:<name>:<resource>:<action>". read shows, operate disables, enables, unlocks
// and resets passwords, write creates, changes and deletes; protected users and groups
// need admin.
const Module = "directory"

// UserResource is the permission resource of a user.
func UserResource(name string) string { return "user/" + strings.ToLower(name) }

// GroupResource is the permission resource of a group.
func GroupResource(name string) string { return "group/" + strings.ToLower(name) }

// Backend is what the service needs of a directory; *ad.Client implements it.
type Backend interface {
	Name() string
	Config() config.Directory
	Check(ctx context.Context) (status.Detail, error)
	Users(ctx context.Context, query string, limit int) ([]ad.User, bool, error)
	User(ctx context.Context, name string) (ad.User, error)
	CreateUser(ctx context.Context, n ad.NewUser) (ad.User, error)
	UpdateUser(ctx context.Context, name string, ch ad.UserChange) error
	SetUserEnabled(ctx context.Context, name string, enabled bool) error
	UnlockUser(ctx context.Context, name string) error
	ResetPassword(ctx context.Context, name, password string, mustChange bool) error
	DeleteUser(ctx context.Context, name string) error
	Groups(ctx context.Context, query string, limit int) ([]ad.Group, bool, error)
	Group(ctx context.Context, name string) (ad.Group, error)
	CreateGroup(ctx context.Context, n ad.NewGroup) (ad.Group, error)
	UpdateGroup(ctx context.Context, name string, ch ad.GroupChange) error
	DeleteGroup(ctx context.Context, name string) error
	SetMembers(ctx context.Context, group string, add, remove []string) error
	ChangePassword(ctx context.Context, name, oldPassword, newPassword string) error
	Policy(ctx context.Context) (ad.PasswordPolicy, error)
	Containers(ctx context.Context) ([]ad.Container, error)
}

// Service is the directory API of dcdash.
type Service struct {
	dirs  map[string]Backend
	order []string
	audit *audit.Log
	log   zerolog.Logger
	// ssUsers and ssIPs limit self-service password changes; nil: self-service is off.
	ssUsers, ssIPs *lockout.Limiter
	ssDelay        time.Duration
}

// NewService creates the service for the configured directories.
func NewService(cfgs config.Directories, auditLog *audit.Log, log zerolog.Logger) (*Service, error) {
	backends := make([]Backend, 0, len(cfgs))
	for _, c := range cfgs {
		b, err := ad.New(c, log)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return NewServiceWith(backends, auditLog, log), nil
}

// NewServiceWith creates the service for the given backends (tests).
func NewServiceWith(backends []Backend, auditLog *audit.Log, log zerolog.Logger) *Service {
	s := &Service{dirs: map[string]Backend{}, audit: auditLog, log: log.With().Str("component", "directories").Logger()}
	for _, b := range backends {
		s.dirs[b.Name()] = b
		s.order = append(s.order, b.Name())
	}
	return s
}

// Backend returns the directory with the name, or nil.
func (s *Service) Backend(name string) Backend { return s.dirs[name] }

// StatusChecks returns a dashboard check per directory.
func (s *Service) StatusChecks() []status.Check {
	out := make([]status.Check, 0, len(s.order))
	for _, n := range s.order {
		b := s.dirs[n]
		out = append(out, status.Check{Name: n, Kind: Module, Detail: func(ctx context.Context) (status.Detail, error) {
			d, err := b.Check(ctx)
			if err != nil {
				return d, &status.Error{Code: "ad.unavailable", Err: err}
			}
			return d, nil
		}})
	}
	return out
}

// Register adds the routes of the directory API.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/directories", s.handleDirectories)
	const base = "/api/v1/directories/{directory}"
	mux.HandleFunc("GET "+base+"/containers", s.handleContainers)
	mux.HandleFunc("GET "+base+"/policy", s.handlePolicy)
	mux.HandleFunc("GET "+base+"/users", s.handleUsers)
	mux.HandleFunc("POST "+base+"/users", s.handleCreateUser)
	mux.HandleFunc("GET "+base+"/users/{name}", s.handleUser)
	mux.HandleFunc("PATCH "+base+"/users/{name}", s.handleUpdateUser)
	mux.HandleFunc("DELETE "+base+"/users/{name}", s.handleDeleteUser)
	mux.HandleFunc("POST "+base+"/users/{name}/enable", s.handleEnableUser)
	mux.HandleFunc("POST "+base+"/users/{name}/disable", s.handleDisableUser)
	mux.HandleFunc("POST "+base+"/users/{name}/unlock", s.handleUnlockUser)
	mux.HandleFunc("POST "+base+"/users/{name}/password", s.handleResetPassword)
	mux.HandleFunc("GET "+base+"/groups", s.handleGroups)
	mux.HandleFunc("POST "+base+"/groups", s.handleCreateGroup)
	mux.HandleFunc("GET "+base+"/groups/{name}", s.handleGroup)
	mux.HandleFunc("PATCH "+base+"/groups/{name}", s.handleUpdateGroup)
	mux.HandleFunc("DELETE "+base+"/groups/{name}", s.handleDeleteGroup)
	mux.HandleFunc("POST "+base+"/groups/{name}/members", s.handleMembers)
	s.registerSelfService(mux)
}

// DirectoryView describes a directory in GET /api/v1/directories.
type DirectoryView struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Domain string `json:"domain"`
	// ManagedOUs are the containers dcdash may change objects in.
	ManagedOUs []string `json:"managedOUs"`
	// CanCreateUsers, CanCreateGroups: the user may create some users or groups.
	CanCreateUsers  bool `json:"canCreateUsers"`
	CanCreateGroups bool `json:"canCreateGroups"`
	// Links are the web interface, SSH and RDP addresses of the domain, if configured.
	Links *status.Links `json:"links,omitempty"`
}

func (s *Service) handleDirectories(w http.ResponseWriter, r *http.Request) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return
	}
	out := []DirectoryView{}
	for _, n := range s.order {
		if !subj.Permissions.AllowsAny(Module, n, rbac.ActionRead) {
			continue
		}
		cfg := s.dirs[n].Config()
		out = append(out, DirectoryView{Name: n, Type: cfg.Type, Domain: cfg.Domain, ManagedOUs: cfg.ManagedOUs,
			CanCreateUsers:  subj.Permissions.AllowsPrefix(Module, n, "user/", rbac.ActionWrite),
			CanCreateGroups: subj.Permissions.AllowsPrefix(Module, n, "group/", rbac.ActionWrite),
			Links:           status.NewLinks(cfg.UI, cfg.SSHURL(), cfg.RDP)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"directories": out})
}

// lookup returns the directory of the request, or writes 404 if it doesn't exist or the
// user may not see it.
func (s *Service) lookup(w http.ResponseWriter, r *http.Request) (Backend, *rbac.Subject, bool) {
	subj := auth.SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return nil, nil, false
	}
	name := r.PathValue("directory")
	b := s.dirs[name]
	if b == nil || !subj.Permissions.AllowsAny(Module, name, rbac.ActionRead) {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "directory not found")
		return nil, nil, false
	}
	return b, subj, true
}

func perm(b Backend, resource string, action rbac.Action) rbac.Permission {
	return rbac.P(Module, b.Name(), resource, action)
}

// may reports whether subj may perform action on an object: it must be managed, and a
// protected one needs admin.
func may(subj *rbac.Subject, b Backend, resource string, managed, protected bool, action rbac.Action) bool {
	if !managed || !subj.Can(perm(b, resource, action)) {
		return false
	}
	return !protected || subj.Can(perm(b, resource, rbac.ActionAdmin))
}

// writeError maps an error of a directory to a response.
func (s *Service) writeError(w http.ResponseWriter, r *http.Request, err error) {
	hlog.FromRequest(r).Warn().Err(err).Msg("directory request failed")
	var e *ad.Error
	msg := err.Error()
	if errors.As(err, &e) && e.Message != "" {
		msg = e.Message
	}
	switch {
	case errors.Is(err, ad.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "not_found", "not found in the directory")
	case errors.Is(err, ad.ErrExists):
		httpx.WriteError(w, http.StatusConflict, "exists", "already exists: "+msg)
	case errors.Is(err, ad.ErrPolicy):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "password_policy", msg)
	case errors.Is(err, ad.ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, "directory_forbidden", msg)
	case errors.Is(err, ad.ErrInvalid), errors.Is(err, ad.ErrWrongPassword):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "rejected", msg)
	default:
		httpx.WriteError(w, http.StatusBadGateway, "upstream_unavailable", "the directory is not available")
	}
}

// accountName matches valid sAMAccountNames: no control characters and none of
// " / \ [ ] : ; | = , + * ? < > @, not ending with a dot.
var accountName = regexp.MustCompile(`^[^"/\\\[\]:;|=,+*?<>@\x00-\x1f]*[^"/\\\[\]:;|=,+*?<>@\x00-\x1f. ]$`)

// validName checks an account name; users have at most 20 characters, groups 64.
func validName(name string, max int) string {
	switch {
	case name == "" || len([]rune(name)) > max:
		return "the name must have 1 to " + strconv.Itoa(max) + " characters"
	case strings.TrimSpace(name) != name || !accountName.MatchString(name):
		return `the name must not contain " / \ [ ] : ; | = , + * ? < > @, start with a space or end with a space or dot`
	}
	return ""
}

// maxPassword limits passwords (AD allows 256 characters).
const maxPassword = 256

// maxText limits names, descriptions and mail addresses.
const maxText = 1024

func (s *Service) handleContainers(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	cs, err := b.Containers(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if cs == nil {
		cs = []ad.Container{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"containers": cs})
}

func (s *Service) handlePolicy(w http.ResponseWriter, r *http.Request) {
	b, _, ok := s.lookup(w, r)
	if !ok {
		return
	}
	p, err := b.Policy(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}
