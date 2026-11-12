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
