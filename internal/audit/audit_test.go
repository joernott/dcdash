// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
)

type memSink struct{ entries []Entry }

func (m *memSink) Write(e Entry) error { m.entries = append(m.entries, e); return nil }

type failingSink struct{}

func (failingSink) Write(Entry) error { return errors.New("sink down") }

func newLog(t *testing.T, sinks ...Sink) *Log {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st.DB(), zerolog.Nop(), sinks...)
}

func TestRecordAndQuery(t *testing.T) {
	sink := &memSink{}
	l := newLog(t, sink, failingSink{})
	ctx := context.Background()
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	for i, e := range []Entry{
		{Actor: "alice", ActorSource: "ldap", Action: "auth.login", Result: ResultSuccess},
		{Actor: "bob", ActorSource: SourceAnonymous, Action: "auth.login", Result: ResultFailure, Details: map[string]any{"reason": "invalid_credentials"}},
		{Actor: "Alice", ActorSource: "ldap", Action: "dns.record.update", Target: "example.com/www A", Result: ResultSuccess},
		{Actor: "alice", ActorSource: "ldap", Action: "authz.denied", Target: "system:core:audit:read", Result: ResultDenied},
		{Actor: "admin", ActorSource: SourceCLI, Action: "auth_totp.reset", Result: ResultSuccess},
	} {
		e.Time = base.Add(time.Duration(i) * time.Minute)
		if err := l.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if len(sink.entries) != 5 || sink.entries[0].ID == 0 || sink.entries[1].Details["reason"] != "invalid_credentials" {
		t.Errorf("sink: %+v", sink.entries)
	}

	count := func(f Filter) int {
		t.Helper()
		got, err := l.Query(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	for name, c := range map[string]struct {
		f    Filter
		want int
	}{
		"all":                  {Filter{}, 5},
		"actor ignores case":   {Filter{Actor: "ALICE"}, 3},
		"action exact":         {Filter{Action: "auth.login"}, 2},
		"action prefix":        {Filter{Action: "auth*"}, 4},
		"action dot prefix":    {Filter{Action: "auth.*"}, 2},
		"prefix escapes _":     {Filter{Action: "auth_*"}, 1},
		"result":               {Filter{Result: ResultDenied}, 1},
		"target":               {Filter{Target: "example.com/www A"}, 1},
		"since":                {Filter{Since: base.Add(2 * time.Minute)}, 3},
		"until (exclusive)":    {Filter{Until: base.Add(2 * time.Minute)}, 2},
		"combined":             {Filter{Actor: "alice", Action: "auth.*", Result: ResultSuccess}, 1},
		"limit":                {Filter{Limit: 2}, 2},
		"no match":             {Filter{Actor: "mallory"}, 0},
		"SQL in filter values": {Filter{Actor: "' OR 1=1 --"}, 0},
	} {
		if got := count(c.f); got != c.want {
			t.Errorf("%s: %d entries, want %d", name, got, c.want)
		}
	}

	first, _ := l.Query(ctx, Filter{Limit: 2})
	if first[0].Action != "auth_totp.reset" || !first[0].Time.Equal(base.Add(4*time.Minute)) {
		t.Errorf("newest first: %+v", first[0])
	}
	rest, _ := l.Query(ctx, Filter{Before: first[1].ID})
	if len(rest) != 3 || rest[0].ID >= first[1].ID {
		t.Errorf("paging: %+v", rest)
	}
	if rest[2].Details == nil {
		t.Error("details must not be nil")
	}
}

func TestRecordClipsLongFields(t *testing.T) {
	l := newLog(t)
	long := strings.Repeat("ä", 400) // 800 bytes
	if err := l.Record(context.Background(), Entry{Actor: long, Action: "auth.login", Result: ResultFailure}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Query(context.Background(), Filter{})
	if len(got[0].Actor) > maxField || !strings.HasPrefix(long, got[0].Actor) {
		t.Errorf("actor not clipped to valid UTF-8: %d bytes", len(got[0].Actor))
	}
}

func TestNilLog(t *testing.T) {
	var l *Log
	if err := l.Record(context.Background(), Entry{}); err != nil {
		t.Error(err)
	}
	l.RecordRequest(httptest.NewRequest(http.MethodGet, "/", nil), "x", "", ResultSuccess, nil)
}

func TestFromRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	e := FromRequest(r, "a", "t", ResultSuccess)
	if e.Actor != "" || e.ActorSource != SourceAnonymous || e.ClientIP != "192.0.2.1" {
		t.Errorf("anonymous: %+v", e)
	}
	r = r.WithContext(session.WithSession(r.Context(), &session.Session{Username: "alice", Source: session.SourceLDAP}))
	if e := FromRequest(r, "a", "t", ResultSuccess); e.Actor != "alice" || e.ActorSource != session.SourceLDAP {
		t.Errorf("with session: %+v", e)
	}
}

func TestCleanup(t *testing.T) {
	l := newLog(t)
	ctx := context.Background()
	now := time.Now()
	_ = l.Record(ctx, Entry{Time: now.Add(-48 * time.Hour), Actor: "old", Action: "x", Result: ResultSuccess})
	_ = l.Record(ctx, Entry{Time: now, Actor: "new", Action: "x", Result: ResultSuccess})
	n, err := l.Cleanup(ctx, 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("cleanup: %d, %v", n, err)
	}
	if got, _ := l.Query(ctx, Filter{}); len(got) != 1 || got[0].Actor != "new" {
		t.Errorf("remaining: %+v", got)
	}
}

func TestFileSink(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.log")
	l := newLog(t, NewFileSink(p))
	for _, actor := range []string{"alice", "bob"} {
		_ = l.Record(context.Background(), Entry{Actor: actor, Action: "auth.login", Result: ResultSuccess})
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var actors []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		actors = append(actors, e.Actor)
	}
	if strings.Join(actors, ",") != "alice,bob" {
		t.Errorf("file entries: %v", actors)
	}
	if fi, _ := os.Stat(p); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
}

func TestQueryAPI(t *testing.T) {
	l := newLog(t)
	for range 3 {
		_ = l.Record(context.Background(), Entry{Actor: "alice", Action: "auth.login", Result: ResultSuccess})
	}
	mux := http.NewServeMux()
	guarded := false
	l.Register(mux, func(h http.Handler) http.Handler { guarded = true; return h })
	if !guarded {
		t.Fatal("guard not applied")
	}
	get := func(query string) (*httptest.ResponseRecorder, Page) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/audit?"+query, nil))
		var p Page
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		return rec, p
	}
	rec, p := get("limit=2&actor=alice")
	if rec.Code != http.StatusOK || len(p.Entries) != 2 || p.NextBefore == nil {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body)
	}
	_, p = get("limit=2&before=" + jsonInt(*p.NextBefore))
	if len(p.Entries) != 1 || p.NextBefore != nil {
		t.Errorf("last page: %+v", p)
	}
	for _, q := range []string{"limit=0", "limit=1001", "limit=x", "before=-1", "since=yesterday", "result=maybe"} {
		if rec, _ := get(q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
	if rec, _ := get("since=2026-01-01T00:00:00Z&until=2100-01-01T00:00:00Z&action=auth.*"); rec.Code != http.StatusOK {
		t.Errorf("valid filters: %d", rec.Code)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }
