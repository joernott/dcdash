// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/session"
)

// rootSession logs alice (root) in, including the TOTP step.
func (e *env) rootSession(t *testing.T) *sess {
	t.Helper()
	secret := e.enroll(t, "alice")
	s := e.login(t, "alice", "secret")
	s.update(t, s.post("/api/v1/auth/totp", codeBody(e.code(secret))))
	if s.info.Stage != session.StageAuthenticated {
		t.Fatalf("root login: %+v", s.info)
	}
	return s
}

func (s *sess) delete(path string) *httptest.ResponseRecorder {
	return s.e.do(call{method: http.MethodDelete, path: path, cookie: s.cookie, csrf: s.csrf})
}

func listSessions(t *testing.T, s *sess, query string) SessionList {
	t.Helper()
	rec := s.get("/api/v1/admin/sessions" + query)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "csrf") || strings.Contains(rec.Body.String(), s.cookie) {
		t.Errorf("session list leaks tokens: %s", rec.Body)
	}
	var l SessionList
	_ = json.Unmarshal(rec.Body.Bytes(), &l)
	return l
}

func TestAdminSessions(t *testing.T) {
	e := newEnv(t)
	root := e.rootSession(t)
	bob := e.login(t, "bob", "secret")
	dave := e.login(t, "dave", "secret")

	all := listSessions(t, root, "")
	if len(all.Sessions) != 3 {
		t.Fatalf("sessions: %+v", all)
	}
	var bobID string
	for _, s := range all.Sessions {
		if s.Current != (s.Username == "alice") || s.ID == "" {
			t.Errorf("session: %+v", s)
		}
		if s.Username == "bob" {
			bobID = s.ID
		}
	}
	if l := listSessions(t, root, "?username=BOB"); len(l.Sessions) != 1 || l.Sessions[0].ID != bobID {
		t.Errorf("filtered: %+v", l)
	}

	// Permissions: bob has none, dave may only list.
	if rec := bob.get("/api/v1/admin/sessions"); rec.Code != http.StatusForbidden {
		t.Errorf("bob lists sessions: %d", rec.Code)
	}
	if l := listSessions(t, dave, ""); len(l.Sessions) != 3 {
		t.Errorf("dave lists sessions: %+v", l)
	}
	if rec := dave.delete("/api/v1/admin/sessions/" + bobID); rec.Code != http.StatusForbidden {
		t.Errorf("dave terminates a session: %d", rec.Code)
	}

	if rec := root.delete("/api/v1/admin/sessions/" + bobID); rec.Code != http.StatusNoContent {
		t.Fatalf("terminate: %d %s", rec.Code, rec.Body)
	}
	if rec := bob.get("/api/v1/auth/me"); rec.Code != http.StatusUnauthorized {
		t.Errorf("terminated session still valid: %d", rec.Code)
	}
	if rec := root.delete("/api/v1/admin/sessions/" + bobID); rec.Code != http.StatusNotFound {
		t.Errorf("terminate twice: %d", rec.Code)
	}

	e.login(t, "bob", "secret")
	e.login(t, "bob", "secret")
	rec := root.delete("/api/v1/admin/users/Bob/sessions")
	var n TerminatedSessions
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	if rec.Code != http.StatusOK || n.Terminated != 2 {
		t.Errorf("terminate user sessions: %d %s", rec.Code, rec.Body)
	}

	entries, _ := e.audit.Query(context.Background(), audit.Filter{Action: "admin.session.terminate"})
	if len(entries) != 2 || entries[0].Actor != "alice" || entries[0].Target != "Bob" || entries[1].Target != "bob" {
		t.Errorf("audit: %+v", entries)
	}
}

func TestAdminResetTOTP(t *testing.T) {
	e := newEnv(t)
	root := e.rootSession(t)
	e.enroll(t, "bob")
	dave := e.login(t, "dave", "secret")
	if rec := dave.delete("/api/v1/admin/users/bob/totp"); rec.Code != http.StatusForbidden {
		t.Errorf("non-root reset: %d", rec.Code)
	}
	if rec := root.delete("/api/v1/admin/users/bob/totp"); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := root.delete("/api/v1/admin/users/bob/totp"); rec.Code != http.StatusNotFound {
		t.Errorf("reset twice: %d", rec.Code)
	}
	// bob's roles do not require TOTP, so he logs in without it now.
	if s := e.login(t, "bob", "secret"); s.info.Stage != session.StageAuthenticated {
		t.Errorf("after reset: %+v", s.info)
	}
	entries, _ := e.audit.Query(context.Background(), audit.Filter{Action: "admin.totp.reset"})
	if len(entries) != 1 || entries[0].Actor != "alice" || entries[0].Target != "bob" {
		t.Errorf("audit: %+v", entries)
	}
	denied, _ := e.audit.Query(context.Background(), audit.Filter{Action: "authz.denied", Actor: "dave"})
	if len(denied) != 1 || denied[0].Target != "role:root" {
		t.Errorf("denied: %+v", denied)
	}
}
