// SPDX-License-Identifier: GPL-3.0-or-later

// Package netbox connects dcdash to NetBox: it reads VLANs, prefixes and free
// addresses, writes changes made in dcdash back to NetBox and compares NetBox
// with the connected systems (see docs/adr/0006-netbox.md).
package netbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// Errors of the client; APIError wraps them where they apply.
var (
	// ErrNotFound: the object doesn't exist (HTTP 404).
	ErrNotFound = errors.New("not found")
	// ErrForbidden: the token is invalid or lacks a permission (HTTP 401, 403).
	ErrForbidden = errors.New("forbidden")
	// ErrUnavailable: NetBox can't be reached or failed (network, HTTP 5xx).
	ErrUnavailable = errors.New("unavailable")
)

// APIError is an error response of NetBox. NetBox reports validation errors
// as an object of field names (or "__all__") and messages, other errors as
// {"detail": "..."}.
type APIError struct {
	Status int
	Detail string
	Fields map[string][]string
}

// HTTPStatus returns the HTTP status code (see status.HTTPStatusError).
func (e *APIError) HTTPStatus() int { return e.Status }

func (e *APIError) Error() string {
	msg := e.Detail
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for k := range e.Fields {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			m := strings.Join(e.Fields[k], " ")
			if k != "__all__" && k != "non_field_errors" {
				m = k + ": " + m
			}
			parts = append(parts, m)
		}
		if msg != "" {
			msg += ": "
		}
		msg += strings.Join(parts, "; ")
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return "NetBox: " + msg
}

// Unwrap maps the status code to the errors of the package.
func (e *APIError) Unwrap() error {
	switch {
	case e.Status == http.StatusNotFound:
		return ErrNotFound
	case e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden:
		return ErrForbidden
	case e.Status >= 500:
		return ErrUnavailable
	}
	return nil
}

// Client calls the NetBox REST API.
type Client struct {
	base string // https://netbox.example.com/api
	auth string // value of the Authorization header
	http *http.Client
}

// NewClient creates the client. It does not connect.
func NewClient(cfg config.NetBox) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("netbox: %w", err)
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	if u.Scheme == "https" {
		tc, err := tlsutil.ClientConfig(cfg.TLS, u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("netbox: %w", err)
		}
		tr.TLSClientConfig = tc
	}
	return &Client{
		base: strings.TrimSuffix(u.Scheme+"://"+u.Host+u.Path, "/") + "/api",
		auth: AuthHeader(cfg.Token.Value()),
		http: &http.Client{Transport: tr, Timeout: cfg.RequestTimeout()},
	}, nil
}

// AuthHeader returns the Authorization header for a token: "Bearer" for v2
// tokens (nbt_<key>.<token>), "Token" for v1 tokens.
func AuthHeader(token string) string {
	if strings.HasPrefix(token, "nbt_") {
		return "Bearer " + token
	}
	return "Token " + token
}

// do sends a request to path (below /api, with a trailing slash) and decodes
// the response into out (if not nil).
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("NetBox: %w: %w", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("NetBox: %w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode >= 300 {
		return parseError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("NetBox: %s %s: decode response: %w", method, path, err)
	}
	return nil
}

func parseError(status int, data []byte) error {
	e := &APIError{Status: status}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		if s := strings.TrimSpace(string(data)); s != "" && len(s) < 200 && !strings.HasPrefix(s, "<") {
			e.Detail = s
		}
		return e
	}
	for k, v := range obj {
		var s string
		if json.Unmarshal(v, &s) == nil {
			if k == "detail" {
				e.Detail = s
				continue
			}
			e.addField(k, s)
			continue
		}
		var list []string
		if json.Unmarshal(v, &list) == nil {
			for _, s := range list {
				e.addField(k, s)
			}
			continue
		}
		e.addField(k, string(v)) // nested errors, e.g. of bulk requests
	}
	return e
}

func (e *APIError) addField(k, msg string) {
	if e.Fields == nil {
		e.Fields = map[string][]string{}
	}
	e.Fields[k] = append(e.Fields[k], msg)
}

// pageSize is the page size of lists; NetBox limits it to MAX_PAGE_SIZE
// (1000 by default).
const pageSize = 500

// maxObjects stops paging through very large lists.
const maxObjects = 50000

type page[T any] struct {
	Count   int     `json:"count"`
	Next    *string `json:"next"`
	Results []T     `json:"results"`
}

// list returns all objects of a list endpoint that match q.
func list[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	qq := url.Values{}
	for k, v := range q {
		qq[k] = v
	}
	qq.Set("limit", strconv.Itoa(pageSize))
	var out []T
	for offset := 0; ; offset += pageSize {
		qq.Set("offset", strconv.Itoa(offset))
		var p page[T]
		if err := c.do(ctx, http.MethodGet, path, qq, nil, &p); err != nil {
			return nil, err
		}
		out = append(out, p.Results...)
		if p.Next == nil || len(p.Results) == 0 {
			return out, nil
		}
		if len(out) >= maxObjects {
			return nil, fmt.Errorf("NetBox: %s: more than %d objects", path, maxObjects)
		}
	}
}

// get returns the single object of a list endpoint that matches q, or
// ErrNotFound. More than one match is an error.
func get[T any](ctx context.Context, c *Client, path string, q url.Values) (T, error) {
	var zero T
	qq := url.Values{}
	for k, v := range q {
		qq[k] = v
	}
	qq.Set("limit", "2")
	var p page[T]
	if err := c.do(ctx, http.MethodGet, path, qq, nil, &p); err != nil {
		return zero, err
	}
	switch len(p.Results) {
	case 0:
		return zero, fmt.Errorf("NetBox: %s?%s: %w", path, q.Encode(), ErrNotFound)
	case 1:
		return p.Results[0], nil
	}
	return zero, fmt.Errorf("NetBox: %s?%s: %d objects match", path, q.Encode(), p.Count)
}

// Version returns the NetBox version (GET /api/status/). It needs a valid
// token.
func (c *Client) Version(ctx context.Context) (string, error) {
	var s struct {
		Version string `json:"netbox-version"`
	}
	if err := c.do(ctx, http.MethodGet, "/status/", nil, nil, &s); err != nil {
		return "", err
	}
	return s.Version, nil
}
