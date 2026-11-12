// SPDX-License-Identifier: GPL-3.0-or-later

// Package httpx contains HTTP helpers shared by the API handlers.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"strings"
)

// ErrorBody is the JSON body of every API error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes an API error. Code is stable and machine-readable
// (the frontend translates it); Message is an English fallback.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WriteJSON writes v as JSON with the given status. API responses are never cached.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an ErrorBody.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

// MaxBodySize limits JSON request bodies.
const MaxBodySize = 1 << 20

// DecodeJSON decodes a JSON request body into v. Unknown fields, trailing data
// and bodies larger than MaxBodySize are rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if !IsJSON(r) {
		return errors.New("content type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON body: unexpected data after the JSON value")
	}
	return nil
}

// IsJSON reports whether the request declares a JSON body.
func IsJSON(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "application/json"
}

// ClientIP returns the IP address of the client. If the direct peer is a
// trusted proxy, the X-Forwarded-For chain is walked from the right and the
// first address that is not a trusted proxy is returned; forwarded addresses
// from untrusted peers are ignored, so clients cannot spoof their address.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !isTrusted(peer, trusted) {
		return peer
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a := parseAddr(strings.TrimSpace(hops[i]))
		if !a.IsValid() {
			break
		}
		if !isTrusted(a, trusted) {
			return a
		}
		peer = a
	}
	return peer
}

func parseAddr(s string) netip.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	a, _ := netip.ParseAddr(strings.Trim(s, "[]"))
	return a.Unmap()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if a.IsValid() && p.Contains(a) {
			return true
		}
	}
	return false
}
