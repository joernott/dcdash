// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"net/http"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
)

// Authorizer derives the subject (roles and permissions) of a request from its
// session and checks permissions. The backend checks every request; the
// frontend only hides what the user cannot use.
type Authorizer struct {
	roles *rbac.Resolver
}

// NewAuthorizer creates an authorizer.
func NewAuthorizer(roles *rbac.Resolver) *Authorizer {
	return &Authorizer{roles: roles}
}

// Subject returns the subject for a session (also for sessions that have not
// completed all login steps; their permissions must not be used).
func (a *Authorizer) Subject(s *session.Session) *rbac.Subject {
	return a.roles.Resolve(s.Username, s.Groups, s.Source == session.SourceLocal)
}

type subjectKey struct{}

// SubjectFromContext returns the subject of a fully authenticated request, or nil.
func SubjectFromContext(ctx context.Context) *rbac.Subject {
	s, _ := ctx.Value(subjectKey{}).(*rbac.Subject)
	return s
}

// WithSubject returns a context carrying the subject.
func WithSubject(ctx context.Context, s *rbac.Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, s)
}

// Middleware puts the subject of fully authenticated sessions into the request
// context. It must run after the session middleware. Roles are resolved on
// every request from the groups stored at login and the current role
// configuration.
func (a *Authorizer) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := session.FromContext(r.Context()); s != nil && s.Authenticated() {
			r = r.WithContext(WithSubject(r.Context(), a.Subject(s)))
		}
		next.ServeHTTP(w, r)
	})
}

// Allowed reports whether the request's subject holds a permission granting req.
func Allowed(r *http.Request, req rbac.Permission) bool {
	return SubjectFromContext(r.Context()).Can(req)
}

// Check writes 401 or 403 and returns false unless the request's subject holds
// a permission granting req. Use it in handlers whose permission depends on the
// request (e.g. the zone in the path).
func Check(w http.ResponseWriter, r *http.Request, req rbac.Permission) bool {
	subj := SubjectFromContext(r.Context())
	if subj == nil {
		httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		return false
	}
	if !subj.Can(req) {
		deny(w, r, subj, req.String())
		return false
	}
	return true
}

// Require wraps a handler that needs a fixed permission.
func Require(req rbac.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Check(w, r, req) {
			next.ServeHTTP(w, r)
		}
	})
}

// RequireRoot wraps a handler that only the root role may use.
func RequireRoot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subj := SubjectFromContext(r.Context())
		switch {
		case subj == nil:
			httpx.WriteError(w, http.StatusUnauthorized, "not_authenticated", "not logged in")
		case !subj.IsRoot():
			deny(w, r, subj, "role:"+rbac.RootRole)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

func deny(w http.ResponseWriter, r *http.Request, subj *rbac.Subject, required string) {
	hlog.FromRequest(r).Warn().Str("component", "authz").Str("username", subj.Username).
		Str("required", required).Str("method", r.Method).Str("path", r.URL.Path).Msg("permission denied")
	httpx.WriteError(w, http.StatusForbidden, "forbidden", "permission denied")
}
