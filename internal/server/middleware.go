// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
)

// chain wraps h with the standard middleware stack (outermost first):
// logger in context, request ID, remote address, access log, panic recovery, security headers.
func chain(h http.Handler, log zerolog.Logger) http.Handler {
	h = securityHeaders(h)
	h = recoverer(h)
	h = hlog.AccessHandler(accessLog)(h)
	h = hlog.RemoteAddrHandler("ip")(h)
	h = hlog.RequestIDHandler("req_id", "X-Request-Id")(h)
	h = hlog.NewHandler(log)(h)
	return h
}

func accessLog(r *http.Request, status, size int, d time.Duration) {
	level := zerolog.InfoLevel
	switch {
	case status >= 500:
		level = zerolog.ErrorLevel
	case r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || strings.HasPrefix(r.URL.Path, "/assets/"):
		level = zerolog.DebugLevel
	}
	hlog.FromRequest(r).WithLevel(level).
		Str("method", r.Method).
		Str("path", r.URL.Path).
		Int("status", status).
		Int("size", size).
		Dur("duration", d).
		Msg("request")
}

// recoverer turns panics into a 500 response and logs them with a stack trace.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			hlog.FromRequest(r).Error().
				Interface("panic", rec).
				Bytes("stack", debug.Stack()).
				Msg("panic in handler")
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy allows only same-origin resources. Inline styles are
// permitted because Vue style bindings render style attributes.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

// stdLogger adapts zerolog for http.Server.ErrorLog (e.g. TLS handshake errors).
func stdLogger(l zerolog.Logger) *log.Logger {
	return log.New(l.With().Str("component", "http").Logger().Level(zerolog.WarnLevel), "", 0)
}
