// SPDX-License-Identifier: GPL-3.0-or-later

package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
