// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/joernott/dcdash/internal/httpx"
)

// CSRFHeader carries the session's CSRF token on state-changing requests.
const CSRFHeader = "X-CSRF-Token"

// CSRF protects state-changing requests (anything but GET, HEAD and OPTIONS)
// against cross-site request forgery with three independent checks:
//
//  1. An Origin header (or, without it, a Referer) must name the requested host.
//  2. A request body must be JSON: browsers cannot send JSON cross-site without
//     a CORS preflight, which dcdash never allows.
//  3. With a session, the X-CSRF-Token header must equal the session's token.
//
// The session cookie is additionally SameSite=Strict. CSRF must run after
// Middleware, which puts the session into the request context.
func CSRF(next http.Handler) http.Handler { return CSRFExcept()(next) }

// CSRFExcept is CSRF with paths that skip the token check (3.); the Origin and
// JSON checks still apply. It is meant for the login endpoint: it replaces any
// existing session, and the login page does not know that session's token.
func CSRFExcept(paths ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return csrf(next, paths) }
}

func csrf(next http.Handler, exempt []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			httpx.WriteError(w, http.StatusForbidden, "csrf_failed", "cross-origin request rejected")
			return
		}
		if r.ContentLength != 0 && !httpx.IsJSON(r) {
			httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "request body must be application/json")
			return
		}
		if s := FromContext(r.Context()); s != nil && !slices.Contains(exempt, r.URL.Path) {
			got := r.Header.Get(CSRFHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.CSRFToken)) != 1 {
				httpx.WriteError(w, http.StatusForbidden, "csrf_failed", "missing or invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the request's Origin (or Referer) names the host
// the request was sent to. Requests without either header (non-browser
// clients) pass; they are covered by the CSRF token.
func sameOrigin(r *http.Request) bool {
	source := r.Header.Get("Origin")
	if source == "" {
		source = r.Header.Get("Referer")
	}
	if source == "" {
		return true
	}
	if source == "null" {
		return false
	}
	u, err := url.Parse(source)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
