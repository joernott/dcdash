// SPDX-License-Identifier: GPL-3.0-or-later

package prefs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

func newStore(t *testing.T) (*Store, *audit.Log) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewStore(st.DB()), audit.New(st.DB(), zerolog.Nop())
}

func TestStore(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if p, err := s.Get(ctx, "alice"); err != nil || p != Default() {
		t.Errorf("default: %+v, %v", p, err)
	}
	if err := s.Set(ctx, "Alice", Preferences{Language: "de", Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "alice", Preferences{Language: "de", Theme: "light"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, "ALICE"); p != (Preferences{Language: "de", Theme: "light"}) {
		t.Errorf("stored: %+v", p)
	}
	if err := s.Set(ctx, "alice", Preferences{Language: "fr", Theme: "pink"}); err == nil ||
		!strings.Contains(err.Error(), "language") || !strings.Contains(err.Error(), "theme") {
		t.Errorf("invalid values: %v", err)
	}
}

func TestAPI(t *testing.T) {
	s, auditLog := newStore(t)
	mux := http.NewServeMux()
	s.Register(mux, auditLog, func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if session.FromContext(r.Context()) == nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, r)
		})
	})
	do := func(method, body string, withSession bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/me/preferences", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if withSession {
			r = r.WithContext(session.WithSession(r.Context(), &session.Session{Username: "alice", Source: session.SourceLDAP}))
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}
	if rec := do(http.MethodGet, "", false); rec.Code != http.StatusUnauthorized {
		t.Errorf("without session: %d", rec.Code)
	}
	rec := do(http.MethodGet, "", true)
	var p Preferences
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != http.StatusOK || p != Default() {
		t.Errorf("get default: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPut, `{"language":"de","theme":"dark"}`, true); rec.Code != http.StatusOK {
		t.Errorf("put: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(do(http.MethodGet, "", true).Body.Bytes(), &p)
	if p != (Preferences{Language: "de", Theme: "dark"}) {
		t.Errorf("after put: %+v", p)
	}
	for _, body := range []string{`{"language":"xx","theme":"dark"}`, `{"language":"de"}`, `{"language":"de","theme":"dark","extra":1}`} {
		if rec := do(http.MethodPut, body, true); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	entries, _ := auditLog.Query(context.Background(), audit.Filter{Action: "user.preferences.update"})
	if len(entries) != 1 || entries[0].Actor != "alice" || entries[0].Details["theme"] != "dark" {
		t.Errorf("audit: %+v", entries)
	}
}
