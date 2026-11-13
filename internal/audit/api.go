// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rs/zerolog/hlog"

	"github.com/joernott/dcdash/internal/httpx"
)

// Page is the response of GET /api/v1/audit.
type Page struct {
	Entries []Entry `json:"entries"`
	// NextBefore is the "before" value for the next (older) page; nil on the last page.
	NextBefore *int64 `json:"nextBefore"`
}

// Register adds the query endpoint. guard checks the caller's permission
// (system:core:audit:read).
func (l *Log) Register(mux *http.ServeMux, guard func(http.Handler) http.Handler) {
	mux.Handle("GET /api/v1/audit", guard(http.HandlerFunc(l.handleQuery)))
}

func (l *Log) handleQuery(w http.ResponseWriter, r *http.Request) {
	f, err := parseFilter(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	entries, err := l.Query(r.Context(), f)
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("audit query failed")
		httpx.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	page := Page{Entries: entries}
	if len(entries) == f.Limit {
		next := entries[len(entries)-1].ID
		page.NextBefore = &next
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func parseFilter(q url.Values) (Filter, error) {
	f := Filter{Actor: q.Get("actor"), Action: q.Get("action"), Target: q.Get("target"), Result: q.Get("result")}
	var errs []error
	parseTime := func(name string, dst *time.Time) {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				errs = append(errs, errors.New(name+": must be an RFC 3339 time"))
			}
			*dst = t
		}
	}
	parseTime("since", &f.Since)
	parseTime("until", &f.Until)
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			errs = append(errs, errors.New("before: must be a positive integer"))
		}
		f.Before = n
	}
	f.Limit = 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			errs = append(errs, errors.New("limit: must be between 1 and 1000"))
		}
		f.Limit = n
	}
	switch f.Result {
	case "", ResultSuccess, ResultFailure, ResultDenied:
	default:
		errs = append(errs, errors.New("result: must be success, failure or denied"))
	}
	return f, errors.Join(errs...)
}
