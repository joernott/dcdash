// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newManager(t *testing.T) (*Manager, *clock) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	m := NewManager(st.DB(), 8*time.Hour, 30*time.Minute, zerolog.Nop())
	c := &clock{t: time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)}
	m.now = c.now
	return m, c
}

func create(t *testing.T, m *Manager) (string, *Session) {
	t.Helper()
	token, s, err := m.Create(context.Background(), New{
		Username: "alice", DisplayName: "Alice", Source: SourceLDAP,
		Groups: []string{"CN=dashroot,DC=example,DC=com"}, Stage: StageAuthenticated,
		RemoteAddr: "192.0.2.1", UserAgent: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return token, s
}

func TestCreateAndGet(t *testing.T) {
	m, _ := newManager(t)
	token, created := create(t, m)
	if len(token) < 40 || created.CSRFToken == "" || created.CSRFToken == token {
		t.Fatalf("weak or reused tokens: %q / %q", token, created.CSRFToken)
	}

	s, err := m.Get(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if s.Username != "alice" || !s.Authenticated() || len(s.Groups) != 1 || s.CSRFToken != created.CSRFToken {
		t.Errorf("unexpected session: %+v", s)
	}
	if _, err := m.Get(context.Background(), token+"x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong token: %v", err)
	}
	if _, err := m.Get(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty token: %v", err)
	}
}

func TestTokenIsStoredHashed(t *testing.T) {
	m, _ := newManager(t)
	token, _ := create(t, m)
	var stored []byte
	if err := m.db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(token)) || len(stored) != 32 {
		t.Error("token not stored as hash")
	}
}

func TestIdleTimeoutAndActivity(t *testing.T) {
	m, c := newManager(t)
	token, _ := create(t, m)

	// Activity every 20 minutes keeps the session alive beyond the idle timeout.
	for range 5 {
		c.advance(20 * time.Minute)
		if _, err := m.Get(context.Background(), token); err != nil {
			t.Fatalf("active session expired: %v", err)
		}
	}
	// 31 minutes without activity end it.
	c.advance(31 * time.Minute)
	if _, err := m.Get(context.Background(), token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("idle session still valid: %v", err)
	}
	var n int
	_ = m.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Error("idle session not deleted")
	}
}

func TestAbsoluteLifetime(t *testing.T) {
	m, c := newManager(t)
	token, _ := create(t, m)
	for range 16 { // 8 hours of activity every 29 minutes
		c.advance(29 * time.Minute)
		if _, err := m.Get(context.Background(), token); err != nil {
			t.Fatalf("session ended early at %v: %v", c.t, err)
		}
	}
	c.advance(29 * time.Minute) // now beyond 8h
	if _, err := m.Get(context.Background(), token); !errors.Is(err, ErrNotFound) {
		t.Errorf("session outlived its lifetime: %v", err)
	}
}

func TestSetStageRotatesToken(t *testing.T) {
	m, _ := newManager(t)
	token, s := create(t, m)
	newToken, err := m.SetStage(context.Background(), s, StageTOTP)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(context.Background(), token); !errors.Is(err, ErrNotFound) {
		t.Error("old token still valid after rotation")
	}
	got, err := m.Get(context.Background(), newToken)
	if err != nil || got.Stage != StageTOTP || got.Authenticated() {
		t.Errorf("rotated session: %+v, %v", got, err)
	}
}

func TestDeleteAndDeleteUserAndCleanup(t *testing.T) {
	m, c := newManager(t)
	t1, _ := create(t, m)
	t2, _ := create(t, m)
	create(t, m)

	if err := m.Delete(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(context.Background(), t1); !errors.Is(err, ErrNotFound) {
		t.Error("deleted session still valid")
	}
	if _, err := m.Get(context.Background(), t2); err != nil {
		t.Errorf("other session affected: %v", err)
	}
	if n, err := m.DeleteUser(context.Background(), "alice"); err != nil || n != 2 {
		t.Errorf("DeleteUser: %d, %v", n, err)
	}

	create(t, m)
	c.advance(time.Hour)
	if n, err := m.Cleanup(context.Background()); err != nil || n != 1 {
		t.Errorf("Cleanup: %d, %v", n, err)
	}
}

func TestCookies(t *testing.T) {
	m, _ := newManager(t)
	rec := httptest.NewRecorder()
	m.SetCookie(rec, "tok", time.Now().Add(time.Hour))
	h := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-dcdash_session=tok", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(h, want) {
			t.Errorf("cookie %q lacks %q", h, want)
		}
	}
	if strings.Contains(h, "Domain=") {
		t.Errorf("cookie must not have a Domain: %q", h)
	}

	rec = httptest.NewRecorder()
	m.ClearCookie(rec)
	if h := rec.Header().Get("Set-Cookie"); !strings.Contains(h, "Max-Age=0") {
		t.Errorf("clear cookie: %q", h)
	}
}

func TestMiddleware(t *testing.T) {
	m, _ := newManager(t)
	token, _ := create(t, m)
	var got *Session
	h := m.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()) }))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got == nil || got.Username != "alice" {
		t.Fatalf("session not in context: %+v", got)
	}

	// Unknown token: no session, cookie cleared.
	got = nil
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "bogus"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got != nil || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Errorf("bogus token: session %+v, cookie %q", got, rec.Header().Get("Set-Cookie"))
	}
}
