// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRF(t *testing.T) {
	s := &Session{Username: "alice", Stage: StageAuthenticated, CSRFToken: "csrf-token"}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := CSRF(ok)

	type req struct {
		name, method, origin, referer, contentType, token, body string
		session                                                 bool
		want                                                    int
	}
	json := "application/json"
	tests := []req{
		{name: "GET passes", method: http.MethodGet, origin: "https://evil.example", want: 204},
		{name: "login without session", method: http.MethodPost, origin: "https://dcdash.example.com", contentType: json, body: "{}", want: 204},
		{name: "non-browser client without origin", method: http.MethodPost, contentType: json, body: "{}", want: 204},
		{name: "cross origin", method: http.MethodPost, origin: "https://evil.example", contentType: json, body: "{}", want: 403},
		{name: "null origin", method: http.MethodPost, origin: "null", contentType: json, body: "{}", want: 403},
		{name: "cross-site referer", method: http.MethodPost, referer: "https://evil.example/page", contentType: json, body: "{}", want: 403},
		{name: "same-site referer", method: http.MethodPost, referer: "https://dcdash.example.com/login", contentType: json, body: "{}", want: 204},
		{name: "form body", method: http.MethodPost, origin: "https://dcdash.example.com", contentType: "application/x-www-form-urlencoded", body: "a=b", want: 415},
		{name: "text/plain body", method: http.MethodPost, contentType: "text/plain", body: "{}", want: 415},
		{name: "session without token", method: http.MethodPost, contentType: json, body: "{}", session: true, want: 403},
		{name: "session wrong token", method: http.MethodDelete, token: "nope", session: true, want: 403},
		{name: "session right token", method: http.MethodPost, contentType: json, body: "{}", token: "csrf-token", session: true, want: 204},
		{name: "logout without body", method: http.MethodPost, token: "csrf-token", session: true, want: 204},
	}
	for _, tc := range tests {
		r := httptest.NewRequest(tc.method, "https://dcdash.example.com/api/v1/x", strings.NewReader(tc.body))
		if tc.body == "" {
			r.ContentLength = 0
		}
		for k, v := range map[string]string{"Origin": tc.origin, "Referer": tc.referer, "Content-Type": tc.contentType, CSRFHeader: tc.token} {
			if v != "" {
				r.Header.Set(k, v)
			}
		}
		if tc.session {
			r = r.WithContext(WithSession(r.Context(), s))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}

func TestCSRFExcept(t *testing.T) {
	s := &Session{Username: "alice", Stage: StageAuthenticated, CSRFToken: "csrf-token"}
	h := CSRFExcept("/api/v1/auth/login")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	send := func(path, origin string) int {
		r := httptest.NewRequest(http.MethodPost, "https://dcdash.example.com"+path, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r = r.WithContext(WithSession(r.Context(), s))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if got := send("/api/v1/auth/login", ""); got != http.StatusNoContent {
		t.Errorf("exempt path without token: %d", got)
	}
	if got := send("/api/v1/auth/login", "https://evil.example"); got != http.StatusForbidden {
		t.Errorf("exempt path still needs same origin: %d", got)
	}
	if got := send("/api/v1/other", ""); got != http.StatusForbidden {
		t.Errorf("other path without token: %d", got)
	}
}
