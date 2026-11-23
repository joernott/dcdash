// SPDX-License-Identifier: GPL-3.0-or-later

// Package opnsense is a client for the REST API of OPNsense firewalls, see
// docs/adr/0009-opnsense.md. It authenticates with an API key and secret
// (HTTP basic authentication) on every request; there are no sessions.
package opnsense

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
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// maxResponse limits the size of a response.
const maxResponse = 32 << 20

var (
	// ErrUnavailable means that the firewall could not be reached or did not
	// answer like OPNsense.
	ErrUnavailable = errors.New("opnsense: firewall unavailable")
	// ErrUnauthorized means that OPNsense rejected the API key and secret.
	ErrUnauthorized = errors.New("opnsense: unauthorized")
	// ErrForbidden means that the API user lacks the privilege for the endpoint.
	ErrForbidden = errors.New("opnsense: forbidden")
	// ErrNotFound means that the endpoint or object does not exist (for
	// example a plugin that is not installed).
	ErrNotFound = errors.New("opnsense: not found")
)

// Error is a request that OPNsense answered with an error status.
type Error struct {
	Method string
	Path   string
	Status int
	Text   string
}

// HTTPStatus returns the HTTP status code (see status.HTTPStatusError).
func (e *Error) HTTPStatus() int { return e.Status }

func (e *Error) Error() string {
	msg := fmt.Sprintf("opnsense: %s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Text != "" {
		msg += ": " + e.Text
	}
	return msg
}

// Is maps HTTP statuses to the package errors.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	}
	return false
}

// ValidationError is a change that OPNsense rejected ("result": "failed")
// with messages per field.
type ValidationError struct {
	Path        string
	Validations map[string]string
}

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Validations))
	for k := range e.Validations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		// Keys are "<model>.<field>", e.g. "alias.name".
		field := k
		if i := strings.LastIndex(k, "."); i >= 0 {
			field = k[i+1:]
		}
		parts = append(parts, field+": "+e.Validations[k])
	}
	if len(parts) == 0 {
		return "opnsense: " + e.Path + ": the change was rejected"
	}
	return "opnsense: " + strings.Join(parts, "; ")
}

// Client talks to one firewall.
type Client struct {
	name   string
	base   string // scheme://host
	key    string
	secret string
	http   *http.Client
	// fresh opens a new connection for every request (see Reachable).
	fresh *http.Client
	log   zerolog.Logger
}

// New creates the client of a firewall. It does not connect.
func New(cfg config.Firewall, log zerolog.Logger) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("firewall %s: %w", cfg.Name, err)
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
			return nil, fmt.Errorf("firewall %s: %w", cfg.Name, err)
		}
		tr.TLSClientConfig = tc
	}
	freshTr := tr.Clone()
	freshTr.DisableKeepAlives = true
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		fresh:  &http.Client{Transport: freshTr, Timeout: 10 * time.Second, CheckRedirect: noRedirect},
		name:   cfg.Name,
		base:   u.Scheme + "://" + u.Host,
		key:    cfg.APIKey,
		secret: cfg.APISecret.Value(),
		http: &http.Client{Transport: tr, Timeout: cfg.RequestTimeout(),
			// The API does not redirect; a redirect would be the login page.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		log: log.With().Str("component", "opnsense").Str("firewall", cfg.Name).Logger(),
	}, nil
}

// Name returns the firewall name.
func (c *Client) Name() string { return c.name }

// Do sends a request to path below /api (for example "/core/firmware/status").
// body is sent as JSON if not nil; a JSON answer is decoded into out.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, c.http, method, path, body, out)
}

// Reachable checks over a new connection that the API still answers. After a
// rule change it tells whether the firewall still lets dcdash in: pf keeps
// existing connections, so a reused connection would pass anyway.
func (c *Client) Reachable(ctx context.Context) error {
	return c.do(ctx, c.fresh, http.MethodGet, "/core/firmware/status", nil, nil)
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("opnsense: %s %s: %w", method, path, err)
		}
		r = bytes.NewReader(data)
	} else if method == http.MethodPost {
		// OPNsense expects a JSON body on POST, even an empty one.
		r = strings.NewReader("{}")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api"+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.key, c.secret)
	req.Header.Set("Accept", "application/json")
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
	}
	c.log.Trace().Str("method", method).Str("path", path).Int("status", resp.StatusCode).
		Dur("took", time.Since(start)).Msg("opnsense request")
	switch {
	case len(data) > maxResponse:
		return fmt.Errorf("opnsense: %s %s: response larger than %d bytes", method, path, maxResponse)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &Error{Method: method, Path: path, Status: resp.StatusCode, Text: errorText(data)}
	case !strings.Contains(resp.Header.Get("Content-Type"), "json") && len(bytes.TrimSpace(data)) > 0 &&
		!json.Valid(data):
		// An HTML page (for example the login page) is not an API answer.
		return fmt.Errorf("%w: %s %s: no JSON answer", ErrUnavailable, method, path)
	}
	if err := checkResult(path, data); err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("opnsense: %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

// Get reads path into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

// Post sends body (or {}) to path and decodes the answer into out.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}

// checkResult turns {"result": "failed", "validations": {...}} into a
// ValidationError. Other answers pass.
func checkResult(path string, data []byte) error {
	var r struct {
		Result      string            `json:"result"`
		Validations map[string]string `json:"validations"`
	}
	if json.Unmarshal(data, &r) != nil || r.Result != "failed" {
		return nil
	}
	return &ValidationError{Path: path, Validations: r.Validations}
}

// errorText returns OPNsense's error message: {"message": "..."} or the start of the body.
func errorText(data []byte) string {
	var m struct {
		Message      string `json:"message"`
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal(data, &m) == nil {
		if m.Message != "" {
			return m.Message
		}
		if m.ErrorMessage != "" {
			return m.ErrorMessage
		}
	}
	s := strings.Join(strings.Fields(string(data)), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
