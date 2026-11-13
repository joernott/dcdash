// SPDX-License-Identifier: GPL-3.0-or-later

// Package cffake is a small in-memory fake of the Cloudflare API v4 for
// tests: zones (list, create, delete) and DNS records (list, get, create,
// update, delete) with Cloudflare's envelope, pagination and error codes.
// Serve it under /client/v4/ and use that URL as base_url.
package cffake

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefix is the path under which the fake serves the API.
const Prefix = "/client/v4/"

// Record is a stored DNS record in Cloudflare's JSON form.
type Record struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Content    string         `json:"content,omitempty"`
	TTL        int            `json:"ttl"`
	Proxied    bool           `json:"proxied"`
	Proxiable  bool           `json:"proxiable"`
	Priority   *float64       `json:"priority,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Comment    string         `json:"comment,omitempty"`
	CreatedOn  time.Time      `json:"created_on"`
	ModifiedOn time.Time      `json:"modified_on"`
}

type zone struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
	records     []*Record
}

// Server is the fake API.
type Server struct {
	mu       sync.Mutex
	token    string
	zones    []*zone
	requests int
	now      func() time.Time
}

// New creates a fake that accepts the token and serves the given zones.
func New(token string, zoneNames ...string) *Server {
	s := &Server{token: token, now: time.Now}
	for _, n := range zoneNames {
		s.zones = append(s.zones, &zone{ID: newID(), Name: n, Status: "active", NameServers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}})
	}
	return s
}

// Add stores a record in a zone (test setup).
func (s *Server) Add(zoneName string, r Record) Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	z := s.zoneByName(zoneName)
	if z == nil {
		panic("cffake: no zone " + zoneName)
	}
	r.ID = newID()
	r.CreatedOn = s.now().UTC()
	r.ModifiedOn = r.CreatedOn
	r.Proxiable = r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME"
	z.records = append(z.records, &r)
	return r
}

// Touch changes the modification time of a record, as a concurrent edit.
func (s *Server) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, z := range s.zones {
		for _, r := range z.records {
			if r.ID == id {
				r.ModifiedOn = r.ModifiedOn.Add(time.Second)
			}
		}
	}
}

// Requests returns the number of API requests served.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) zoneByName(n string) *zone {
	for _, z := range s.zones {
		if z.Name == n {
			return z
		}
	}
	return nil
}

func (s *Server) zoneByID(id string) *zone {
	for _, z := range s.zones {
		if z.ID == id {
			return z
		}
	}
	return nil
}

type apiErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func write(w http.ResponseWriter, status int, result any, info any, errs ...apiErr) {
	if errs == nil {
		errs = []apiErr{}
	}
	body := map[string]any{"success": status < 300, "errors": errs, "messages": []any{}, "result": result}
	if info != nil {
		body["result_info"] = info
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	write(w, status, nil, nil, apiErr{Code: code, Message: msg})
}

// page returns one page of items and Cloudflare's result_info.
func page[T any](r *http.Request, items []T) ([]T, map[string]int) {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 {
		per = 20
	}
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if p <= 0 {
		p = 1
	}
	total := len(items)
	start := min((p-1)*per, total)
	end := min(start+per, total)
	pages := (total + per - 1) / per
	return items[start:end], map[string]int{"page": p, "per_page": per, "count": end - start, "total_count": total, "total_pages": pages}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		fail(w, http.StatusForbidden, 9109, "Invalid access token")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	parts := strings.Split(path, "/")
	switch {
	case path == "zones" && r.Method == http.MethodGet:
		list := make([]zone, 0, len(s.zones))
		for _, z := range s.zones {
			list = append(list, *z)
		}
		items, info := page(r, list)
		write(w, http.StatusOK, items, info)
	case path == "zones" && r.Method == http.MethodPost:
		s.createZone(w, r)
	case len(parts) == 2 && parts[0] == "zones" && r.Method == http.MethodDelete:
		i := slices.IndexFunc(s.zones, func(z *zone) bool { return z.ID == parts[1] })
		if i < 0 {
			fail(w, http.StatusNotFound, 1001, "Invalid zone identifier")
			return
		}
		s.zones = slices.Delete(s.zones, i, i+1)
		write(w, http.StatusOK, map[string]string{"id": parts[1]}, nil)
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		z := s.zoneByID(parts[1])
		if z == nil {
			fail(w, http.StatusNotFound, 7003, "Could not route to /zones/"+parts[1])
			return
		}
		s.records(w, r, z, parts[3:])
	default:
		fail(w, http.StatusNotFound, 7000, "No route for that URI")
	}
}

func (s *Server) createZone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Account.ID == "" {
		fail(w, http.StatusBadRequest, 1001, "invalid request")
		return
	}
	if s.zoneByName(req.Name) != nil {
		fail(w, http.StatusBadRequest, 1061, req.Name+" already exists")
		return
	}
	z := &zone{ID: newID(), Name: req.Name, Status: "pending", NameServers: []string{"ada.ns.cloudflare.com", "bob.ns.cloudflare.com"}}
	s.zones = append(s.zones, z)
	write(w, http.StatusOK, z, nil)
}

func (s *Server) records(w http.ResponseWriter, r *http.Request, z *zone, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			list := make([]Record, 0, len(z.records))
			for _, rec := range z.records {
				list = append(list, *rec)
			}
			items, info := page(r, list)
			write(w, http.StatusOK, items, info)
		case http.MethodPost:
			rec, ok := decodeRecord(w, r)
			if !ok {
				return
			}
			for _, o := range z.records {
				if o.Name == rec.Name && o.Type == rec.Type && o.Content == rec.Content && sameData(o.Data, rec.Data) {
					fail(w, http.StatusBadRequest, 81058, "An identical record already exists.")
					return
				}
			}
			rec.ID = newID()
			rec.CreatedOn = s.now().UTC()
			rec.ModifiedOn = rec.CreatedOn
			z.records = append(z.records, &rec)
			write(w, http.StatusOK, rec, nil)
		default:
			fail(w, http.StatusMethodNotAllowed, 10000, "method not allowed")
		}
		return
	}
	i := slices.IndexFunc(z.records, func(rec *Record) bool { return rec.ID == rest[0] })
	if i < 0 {
		fail(w, http.StatusNotFound, 81044, "Record does not exist.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		write(w, http.StatusOK, z.records[i], nil)
	case http.MethodPut:
		rec, ok := decodeRecord(w, r)
		if !ok {
			return
		}
		rec.ID = z.records[i].ID
		rec.CreatedOn = z.records[i].CreatedOn
		rec.ModifiedOn = maxTime(s.now().UTC(), z.records[i].ModifiedOn.Add(time.Millisecond))
		z.records[i] = &rec
		write(w, http.StatusOK, rec, nil)
	case http.MethodDelete:
		z.records = slices.Delete(z.records, i, i+1)
		write(w, http.StatusOK, map[string]string{"id": rest[0]}, nil)
	default:
		fail(w, http.StatusMethodNotAllowed, 10000, "method not allowed")
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func sameData(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func decodeRecord(w http.ResponseWriter, r *http.Request) (Record, bool) {
	var rec Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil || rec.Name == "" || rec.Type == "" || rec.TTL == 0 {
		fail(w, http.StatusBadRequest, 9000, "DNS record is invalid")
		return Record{}, false
	}
	if rec.Content == "" && rec.Data == nil {
		fail(w, http.StatusBadRequest, 9005, "Content for "+rec.Type+" record is invalid")
		return Record{}, false
	}
	rec.Proxiable = rec.Type == "A" || rec.Type == "AAAA" || rec.Type == "CNAME"
	if rec.Proxied && !rec.Proxiable {
		fail(w, http.StatusBadRequest, 9004, "This record type cannot be proxied.")
		return Record{}, false
	}
	return rec, true
}
