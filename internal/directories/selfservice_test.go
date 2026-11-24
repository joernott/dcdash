// SPDX-License-Identifier: GPL-3.0-or-later

package directories_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/directories"
	"github.com/joernott/dcdash/internal/lockout"
)

func newSelfServiceEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := newAPIEnv(t)
	e.svc.EnableSelfService(lockout.Policy{MaxFailures: 3, Window: time.Minute, Duration: time.Minute},
		lockout.Policy{MaxFailures: 10, Window: time.Minute, Duration: time.Minute})
	e.svc.SetFailureDelay(0)
	return e
}

func change(dir, user, current, next string) map[string]any {
	return map[string]any{"directory": dir, "username": user, "currentPassword": current, "newPassword": next}
}

func TestSelfServiceDirectories(t *testing.T) {
	e := newSelfServiceEnv(t)
	var out struct {
		Directories []directories.SelfServiceDirectory `json:"directories"`
	}
	// No login needed.
	if code, _ := e.do(t, "", "GET", "/api/v1/password/directories", nil, &out); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(out.Directories) != 1 || out.Directories[0].Name != "lab" || out.Directories[0].Policy == nil || out.Directories[0].Policy.MinLength != 7 {
		t.Errorf("directories: %+v", out)
	}
	e.lab.Down = true
	out.Directories = nil
	e.do(t, "", "GET", "/api/v1/password/directories", nil, &out)
	if out.Directories[0].Policy != nil {
		t.Errorf("policy of an unavailable directory: %+v", out.Directories[0])
	}
}

func TestSelfServiceChange(t *testing.T) {
	e := newSelfServiceEnv(t)
	const path = "/api/v1/password/change"
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{change("nope", "lab-alice", "Alice-1234", "Alice-5678"), http.StatusBadRequest, "invalid_request"},
		{change("lab", "lab alice\n", "Alice-1234", "Alice-5678"), http.StatusBadRequest, "invalid_request"},
		{change("lab", "lab-alice", "", "Alice-5678"), http.StatusBadRequest, "invalid_request"},
		{change("lab", "lab-alice", "Alice-1234", "Alice-1234"), http.StatusBadRequest, "same_password"},
		{change("lab", "lab-alice", "Alice-1234", "short"), http.StatusUnprocessableEntity, "password_policy_tooShort"},
	} {
		if code, ec := e.do(t, "", "POST", path, c.body, nil); code != c.status || ec != c.code {
			t.Errorf("%v = %d %s, want %d %s", c.body, code, ec, c.status, c.code)
		}
	}
	// Unknown users and wrong passwords get the same answer.
	for _, body := range []map[string]any{change("lab", "nobody", "x", "Nobody-5678"), change("lab", "lab-root", "wrong", "Root-56789")} {
		if code, ec := e.do(t, "", "POST", path, body, nil); code != http.StatusUnauthorized || ec != "invalid_credentials" {
			t.Errorf("%v = %d %s", body, code, ec)
		}
	}
	// A user who must change the password, by user principal name.
	e.lab.UserMap["lab-alice"].MustChangePassword = true
	if code, ec := e.do(t, "", "POST", path, change("lab", "lab-alice@ad.test", "Alice-1234", "Alice-5678"), nil); code != http.StatusNoContent {
		t.Fatalf("change = %d %s", code, ec)
	}
	if u := e.lab.UserMap["lab-alice"]; u.Password != "Alice-5678" || u.MustChangePassword {
		t.Errorf("after change: %+v", u)
	}
	a := e.lastAudit(t)
	if a.Action != directories.AuditPasswordChange || a.Actor != "lab-alice@ad.test" || a.ActorSource != directories.SourceSelfService ||
		a.Result != audit.ResultSuccess || a.Target != "lab/user/lab-alice@ad.test" {
		t.Errorf("audit: %+v", a)
	}
	if s := fmt.Sprint(a.Details); s != "map[]" {
		t.Errorf("audit details: %s", s)
	}

	// Lockout after three failures for the user, also with the right password then.
	for range 3 {
		e.do(t, "", "POST", path, change("lab", "lab-alice", "wrong", "Alice-9999"), nil)
	}
	if code, ec := e.do(t, "", "POST", path, change("lab", "lab-alice", "Alice-5678", "Alice-9999"), nil); code != http.StatusTooManyRequests || ec != "too_many_attempts" {
		t.Errorf("after 3 failures = %d %s", code, ec)
	}
	if a := e.lastAudit(t); a.Result != audit.ResultDenied {
		t.Errorf("audit of the lockout: %+v", a)
	}
	// Other users are not affected.
	if code, _ := e.do(t, "", "POST", path, change("lab", "lab-root", "Root-12345", "Root-56789"), nil); code != http.StatusNoContent {
		t.Errorf("other user = %d", code)
	}

	e.lab.Down = true
	if code, ec := e.do(t, "", "POST", path, change("lab", "lab-root", "Root-56789", "Root-99999"), nil); code != http.StatusServiceUnavailable || ec != "directory_unavailable" {
		t.Errorf("down = %d %s", code, ec)
	}
}

func TestSelfServiceDisabled(t *testing.T) {
	e := newAPIEnv(t)
	// Without EnableSelfService the routes don't exist.
	if code, _ := e.do(t, "", "GET", "/api/v1/password/directories", nil, nil); code != http.StatusNotFound {
		t.Errorf("status %d", code)
	}
}
