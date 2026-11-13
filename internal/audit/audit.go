// SPDX-License-Identifier: GPL-3.0-or-later

// Package audit records logins and every state-changing action: who did what,
// when, to which object, and with which result. Entries are stored in the
// database and can additionally be written to a file or syslog.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/session"
)

// Results.
const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultDenied  = "denied"
)

// Actor sources besides the session sources (session.SourceLDAP, session.SourceLocal).
const (
	SourceCLI       = "cli"
	SourceSystem    = "system"
	SourceAnonymous = "anonymous"
)

// Entry is an audit record.
type Entry struct {
	ID          int64          `json:"id"`
	Time        time.Time      `json:"time"`
	Actor       string         `json:"actor"`
	ActorSource string         `json:"actorSource"`
	ClientIP    string         `json:"clientIp"`
	Action      string         `json:"action"`
	Target      string         `json:"target"`
	Result      string         `json:"result"`
	Details     map[string]any `json:"details"`
	RequestID   string         `json:"requestId"`
}

// Sink receives every entry after it has been stored, e.g. a file or syslog.
type Sink interface {
	Write(e Entry) error
}

// Log writes and queries audit entries.
type Log struct {
	db    *sql.DB
	sinks []Sink
	log   zerolog.Logger
	now   func() time.Time
}

// New creates an audit log.
func New(db *sql.DB, log zerolog.Logger, sinks ...Sink) *Log {
	return &Log{db: db, sinks: sinks, log: log.With().Str("component", "audit").Logger(), now: time.Now}
}

// maxField limits the length of free-text fields that may come from users.
const maxField = 512

func clip(s string) string {
	if len(s) <= maxField {
		return s
	}
	s = s[:maxField]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Record stores an entry. Errors are logged and returned; callers usually
// ignore them, because the action itself already happened. A nil Log discards
// entries.
func (l *Log) Record(ctx context.Context, e Entry) error {
	if l == nil {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = l.now()
	}
	e.Time = e.Time.UTC().Truncate(time.Millisecond)
	e.Actor, e.Target = clip(e.Actor), clip(e.Target)
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	details, err := json.Marshal(e.Details)
	if err != nil {
		details = []byte(`{"error":"details not serializable"}`)
	}
	res, err := l.db.ExecContext(ctx, `INSERT INTO audit_log
		(time, actor, actor_source, client_ip, action, target, result, details, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Time.UnixMilli(), e.Actor, e.ActorSource, e.ClientIP, e.Action, e.Target, e.Result, string(details), e.RequestID)
	if err != nil {
		// The entry must not get lost: at least put it into the application log.
		l.log.Error().Err(err).Str("actor", e.Actor).Str("action", e.Action).Str("target", e.Target).
			Str("result", e.Result).RawJSON("details", details).Msg("cannot store audit entry")
		return fmt.Errorf("store audit entry: %w", err)
	}
	e.ID, _ = res.LastInsertId()
	for _, s := range l.sinks {
		if err := s.Write(e); err != nil {
			l.log.Error().Err(err).Int64("id", e.ID).Msg("cannot write audit entry to sink")
		}
	}
	return nil
}

// FromRequest builds an entry for an action performed in an HTTP request. The
// actor is taken from the request's session; without a session it is
// "anonymous" (use Entry.Actor for failed logins).
func FromRequest(r *http.Request, action, target, result string) Entry {
	e := Entry{
		Actor: "", ActorSource: SourceAnonymous, ClientIP: httpx.RequestIP(r).String(),
		Action: action, Target: target, Result: result, Details: map[string]any{},
	}
	if id, ok := hlog.IDFromRequest(r); ok {
		e.RequestID = id.String()
	}
	if s := session.FromContext(r.Context()); s != nil {
		e.Actor, e.ActorSource = s.Username, s.Source
	}
	return e
}

// RecordRequest records an action performed in an HTTP request (see FromRequest).
func (l *Log) RecordRequest(r *http.Request, action, target, result string, details map[string]any) {
	e := FromRequest(r, action, target, result)
	if details != nil {
		e.Details = details
	}
	_ = l.Record(r.Context(), e)
}

// Filter selects entries. Empty fields do not filter.
type Filter struct {
	// Actor matches the actor exactly, ignoring case.
	Actor string
	// Action matches exactly, or as a prefix if it ends with "*" (e.g. "auth.*").
	Action string
	Target string
	Result string
	Since  time.Time
	Until  time.Time
	// Before returns entries with an ID lower than this (for paging).
	Before int64
	// Limit is the maximum number of entries (default 100, at most 1000).
	Limit int
}

// Query returns matching entries, newest first.
func (l *Log) Query(ctx context.Context, f Filter) ([]Entry, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, arg any) { where = append(where, cond); args = append(args, arg) }
	if f.Actor != "" {
		add("actor = ? COLLATE NOCASE", f.Actor)
	}
	if prefix, ok := strings.CutSuffix(f.Action, "*"); ok {
		add(`action LIKE ? ESCAPE '\'`, escapeLike(prefix)+"%")
	} else if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.Target != "" {
		add("target = ?", f.Target)
	}
	if f.Result != "" {
		add("result = ?", f.Result)
	}
	if !f.Since.IsZero() {
		add("time >= ?", f.Since.UnixMilli())
	}
	if !f.Until.IsZero() {
		add("time < ?", f.Until.UnixMilli())
	}
	if f.Before > 0 {
		add("id < ?", f.Before)
	}
	switch {
	case f.Limit <= 0:
		f.Limit = 100
	case f.Limit > 1000:
		f.Limit = 1000
	}
	q := `SELECT id, time, actor, actor_source, client_ip, action, target, result, details, request_id FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ") // #nosec G202 -- fixed conditions only; values are parameters
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit)

	rows, err := l.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var (
			e       Entry
			ms      int64
			details string
		)
		if err := rows.Scan(&e.ID, &ms, &e.Actor, &e.ActorSource, &e.ClientIP, &e.Action, &e.Target, &e.Result, &details, &e.RequestID); err != nil {
			return nil, err
		}
		e.Time = time.UnixMilli(ms).UTC()
		if err := json.Unmarshal([]byte(details), &e.Details); err != nil || e.Details == nil {
			e.Details = map[string]any{}
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Cleanup deletes entries older than retention and returns how many were deleted.
func (l *Log) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM audit_log WHERE time < ?`, l.now().Add(-retention).UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Run deletes entries older than retention once a day until ctx is canceled.
// A retention of 0 keeps entries forever.
func (l *Log) Run(ctx context.Context, retention time.Duration) error {
	if retention <= 0 {
		return nil
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := l.Cleanup(ctx, retention); err != nil && ctx.Err() == nil {
			l.log.Warn().Err(err).Msg("audit cleanup failed")
		} else if n > 0 {
			l.log.Info().Int64("deleted", n).Dur("retention", retention).Msg("old audit entries removed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
