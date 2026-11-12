// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var testFS = fstest.MapFS{
	"index.html":       {Data: []byte("<html>index</html>")},
	"favicon.ico":      {Data: []byte("icon")},
	"assets/app-1.js":  {Data: []byte("console.log(1)")},
	"assets/app-1.css": {Data: []byte("body{}")},
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestHandler(t *testing.T) {
	h := Handler(testFS)
	tests := []struct {
		target, wantBody, wantCache string
		wantStatus                  int
	}{
		{"/", "index", "no-cache", http.StatusOK},
		{"/proxmox/clusters/a", "index", "no-cache", http.StatusOK}, // client-side route
		{"/assets/app-1.js", "console.log", "immutable", http.StatusOK},
		{"/favicon.ico", "icon", "no-cache", http.StatusOK},
		{"/assets/missing.js", "", "", http.StatusNotFound},
		{"/../../etc/passwd", "index", "no-cache", http.StatusOK}, // cleaned, no traversal
	}
	for _, tt := range tests {
		rec := get(t, h, http.MethodGet, tt.target)
		if rec.Code != tt.wantStatus {
			t.Errorf("%s: status %d, want %d", tt.target, rec.Code, tt.wantStatus)
			continue
		}
		if !strings.Contains(rec.Body.String(), tt.wantBody) {
			t.Errorf("%s: body %q, want %q", tt.target, rec.Body, tt.wantBody)
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), tt.wantCache) {
			t.Errorf("%s: Cache-Control %q, want %q", tt.target, rec.Header().Get("Cache-Control"), tt.wantCache)
		}
	}
}

func TestHandlerMethods(t *testing.T) {
	h := Handler(testFS)
	if rec := get(t, h, http.MethodPost, "/"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
	if rec := get(t, h, http.MethodHead, "/some/route"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d, body %q", rec.Code, rec.Body)
	}
}

func TestHandlerWithoutBuild(t *testing.T) {
	rec := get(t, Handler(fstest.MapFS{}), http.MethodGet, "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not built") {
		t.Errorf("got %d %q", rec.Code, rec.Body)
	}
}

func TestDistIsEmbedded(t *testing.T) {
	if _, err := fs.Stat(Dist(), ".gitkeep"); err != nil {
		t.Errorf("embedded dist missing .gitkeep: %v", err)
	}
}
