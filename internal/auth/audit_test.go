// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/joernott/dcdash/internal/audit"
)

func TestAuthEventsAreAudited(t *testing.T) {
	e := newEnv(t)
	e.do(login("bob", "wrong"))
	e.do(login("down", "x"))
	bob := e.do(login("bob", "secret"))
	token := cookieValue(t, bob)
	e.do(call{method: http.MethodGet, path: "/api/v1/test/audit", cookie: token})
	for range 3 {
		e.do(login("carol", "wrong"))
	}
	e.do(login("carol", "secret")) // locked
	csrf := csrfToken(t, bob)
	e.do(call{method: http.MethodPost, path: "/api/v1/auth/logout", cookie: token, csrf: csrf})

	entries, err := e.audit.Query(context.Background(), audit.Filter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ actor, action, result, reason string }
	got := map[key]int{}
	for _, en := range entries {
		reason, _ := en.Details["reason"].(string)
		got[key{en.Actor, en.Action, en.Result, reason}]++
		if en.ClientIP == "" {
			t.Errorf("entry without client IP: %+v", en)
		}
	}
	for k, n := range map[key]int{
		{"bob", "auth.login", audit.ResultFailure, "invalid_credentials"}:    1,
		{"down", "auth.login", audit.ResultFailure, "directory_unavailable"}: 1,
		{"bob", "auth.login", audit.ResultSuccess, ""}:                       1,
		{"bob", "authz.denied", audit.ResultDenied, ""}:                      1,
		{"carol", "auth.login", audit.ResultFailure, "invalid_credentials"}:  3,
		{"carol", "auth.login", audit.ResultDenied, "locked"}:                1,
		{"bob", "auth.logout", audit.ResultSuccess, ""}:                      1,
	} {
		if got[k] != n {
			t.Errorf("%+v: %d entries, want %d (all: %v)", k, got[k], n, got)
		}
	}
}
