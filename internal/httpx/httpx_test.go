// SPDX-License-Identifier: GPL-3.0-or-later

package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusTeapot, "teapot", "short and stout")
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusTeapot || body.Error.Code != "teapot" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("got %d %+v %v", rec.Code, body, rec.Header())
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	decode := func(ct, body string) error {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", ct)
		var v in
		return DecodeJSON(httptest.NewRecorder(), r, &v)
	}
	if err := decode("application/json; charset=utf-8", `{"name":"x"}`); err != nil {
		t.Errorf("valid: %v", err)
	}
	for name, tc := range map[string][2]string{
		"wrong type":    {"text/plain", `{"name":"x"}`},
		"unknown field": {"application/json", `{"name":"x","admin":true}`},
		"trailing data": {"application/json", `{"name":"x"} {}`},
		"broken":        {"application/json", `{"name":`},
		"too large":     {"application/json", `{"name":"` + strings.Repeat("a", MaxBodySize) + `"}`},
	} {
		if err := decode(tc[0], tc[1]); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	for _, tc := range []struct {
		remote, xff, want string
	}{
		{"192.0.2.10:5555", "", "192.0.2.10"},
		{"192.0.2.10:5555", "203.0.113.7", "192.0.2.10"},              // untrusted peer: header ignored
		{"10.0.0.5:5555", "203.0.113.7", "203.0.113.7"},               // trusted proxy
		{"10.0.0.5:5555", "198.51.100.1, 203.0.113.7", "203.0.113.7"}, // rightmost untrusted wins
		{"10.0.0.5:5555", "203.0.113.7, 10.0.0.9", "203.0.113.7"},     // chain of proxies
		{"10.0.0.5:5555", "garbage, 10.0.0.9", "10.0.0.9"},            // unparsable: stop
		{"10.0.0.5:5555", "", "10.0.0.5"},                             // proxy without header
		{"[::1]:443", "2001:db8::7", "2001:db8::7"},                   // IPv6
		{"[::ffff:192.0.2.10]:1", "", "192.0.2.10"},                   // mapped IPv4
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		if tc.xff != "" {
			r.Header.Set("X-Forwarded-For", tc.xff)
		}
		if got := ClientIP(r, trusted); got.String() != tc.want {
			t.Errorf("%s / %q: got %s, want %s", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestClientIPMiddleware(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := RequestIP(r); got.String() != "10.0.0.5" {
		t.Errorf("without middleware: %s", got)
	}
	var got netip.Addr
	h := ClientIPMiddleware([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestIP(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got.String() != "203.0.113.7" {
		t.Errorf("with middleware: %s", got)
	}
}
