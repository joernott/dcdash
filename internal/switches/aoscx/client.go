// SPDX-License-Identifier: GPL-3.0-or-later

// Package aoscx is a client for the REST API of Aruba CX switches (AOS-CX 10.x),
// see docs/adr/0008-aruba-cx.md.
//
// A switch allows only a few sessions per user, so the client logs in once and
// reuses the session. A request that fails because the session expired logs in
// again and is repeated once. Close logs out.
package aoscx

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
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// maxResponse limits the size of a response (the MAC address table of a large switch).
const maxResponse = 32 << 20

var (
	// ErrUnavailable means that the switch could not be reached or did not
	// answer like an AOS-CX switch.
	ErrUnavailable = errors.New("aoscx: switch unavailable")
	// ErrUnauthorized means that the switch rejected dcdash's credentials.
	ErrUnauthorized = errors.New("aoscx: unauthorized")
	// ErrForbidden means that the account may not do this (role "operators").
	ErrForbidden = errors.New("aoscx: forbidden")
	// ErrNotFound means that the object does not exist.
	ErrNotFound = errors.New("aoscx: not found")
	// ErrSessionLimit means that the account has too many sessions open on the
	// switch (for example in other tools).
	ErrSessionLimit = errors.New("aoscx: too many sessions")
)

// Error is a request the switch answered with an error status.
type Error struct {
	Method string
	Path   string
	Status int
	// Text is the switch's error message (plain text), shortened.
	Text string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("aoscx: %s %s: HTTP %d", e.Method, e.Path, e.Status)
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
	case ErrSessionLimit:
		return e.Status == http.StatusTooManyRequests || e.Status == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(e.Text), "session")
	}
	return false
}

// Client talks to one switch.
type Client struct {
	name     string
	base     string // scheme://host
	username string
	password string
	version  string // configured API version, "" for the newest
	http     *http.Client
	log      zerolog.Logger

	mu sync.Mutex
	// prefix is "/rest/v10.xx" once known.
	prefix string
	// session is the number of the current session (0: none). A request
	// that got 401 logs in again only if no one else did meanwhile.
	session int
	csrf    string
	cookies []*http.Cookie
}

// New creates the client of a switch. It does not connect.
func New(cfg config.Switch, log zerolog.Logger) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("switch %s: %w", cfg.Name, err)
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	}
	if u.Scheme == "https" {
		tc, err := tlsutil.ClientConfig(cfg.TLS, u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("switch %s: %w", cfg.Name, err)
		}
		tr.TLSClientConfig = tc
	}
	return &Client{
		name:     cfg.Name,
		base:     u.Scheme + "://" + u.Host,
		username: cfg.Username,
		password: cfg.Password.Value(),
		version:  cfg.APIVersion,
		http: &http.Client{Transport: tr, Timeout: cfg.RequestTimeout(),
			// The API does not redirect; a redirect would be a login page.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		log: log.With().Str("component", "aoscx").Str("switch", cfg.Name).Logger(),
	}, nil
}

// Name returns the switch name.
func (c *Client) Name() string { return c.name }

// APIVersion returns the API version in use ("v10.08"), "" before the first request.
func (c *Client) APIVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimPrefix(c.prefix, "/rest/")
}

// versions is the answer of GET /rest.
type versions map[string]struct {
	Version string `json:"version"`
	Prefix  string `json:"prefix"`
}

// discover finds the API prefix: the configured version or the newest one.
// Called with c.mu held.
func (c *Client) discover(ctx context.Context) error {
	if c.prefix != "" {
		return nil
	}
	if c.version != "" {
		c.prefix = "/rest/" + c.version
		return nil
	}
	rep, err := c.send(ctx, http.MethodGet, "/rest", nil, "", session{})
	if err != nil {
		return err
	}
	if rep.status != http.StatusOK {
		return fmt.Errorf("%w: GET /rest: HTTP %d", ErrUnavailable, rep.status)
	}
	var v versions
	if err := json.Unmarshal(rep.data, &v); err != nil || v["latest"].Prefix == "" {
		return fmt.Errorf("%w: GET /rest: no API versions", ErrUnavailable)
	}
	c.prefix = v["latest"].Prefix
	return nil
}

// login opens a session. Called with c.mu held.
func (c *Client) login(ctx context.Context) error {
	form := url.Values{"username": {c.username}, "password": {c.password}}.Encode()
	path := c.prefix + "/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, strings.NewReader(form))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Newer firmware returns a CSRF token on request; 10.08 returns none.
	req.Header.Set("x-use-csrf-token", "true")
	rep, err := c.do(req)
	if err != nil {
		return err
	}
	if rep.status != http.StatusOK {
		return &Error{Method: http.MethodPost, Path: path, Status: rep.status, Text: shorten(rep.data)}
	}
	c.csrf = rep.header.Get("x-csrf-token")
	c.cookies = rep.cookies
	c.session++
	c.log.Debug().Msg("logged in")
	return nil
}

// session is what a request needs of the current session.
type session struct {
	id      int
	csrf    string
	cookies []*http.Cookie
}

// ensure returns the API prefix and the current session, logging in if there is none.
func (c *Client) ensure(ctx context.Context) (string, session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.discover(ctx); err != nil {
		return "", session{}, err
	}
	if c.session == 0 {
		if err := c.login(ctx); err != nil {
			return "", session{}, err
		}
	}
	return c.prefix, c.current(), nil
}

// current returns the session. Called with c.mu held.
func (c *Client) current() session {
	return session{id: c.session, csrf: c.csrf, cookies: c.cookies}
}

// expired drops a session that the switch no longer accepts, unless another
// request has already replaced it.
func (c *Client) expired(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == id {
		c.drop()
	}
}

// drop forgets the session. Called with c.mu held.
func (c *Client) drop() {
	c.session, c.csrf, c.cookies = 0, "", nil
}

// Do sends a request to path below the API prefix (for example
// "/system/vlans"). body is sent as JSON if not nil; a JSON answer is decoded
// into out if out is not nil.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("aoscx: %s %s: %w", method, path, err)
		}
	}
	for attempt := 0; ; attempt++ {
		prefix, sess, err := c.ensure(ctx)
		if err != nil {
			return err
		}
		rep, err := c.send(ctx, method, prefix+path, payload, "application/json", sess)
		if err != nil {
			return err
		}
		if rep.status == http.StatusUnauthorized && attempt == 0 {
			// The session timed out (idle 20 minutes, at most 8 hours).
			c.log.Debug().Msg("session expired")
			c.expired(sess.id)
			continue
		}
		if rep.status < 200 || rep.status > 299 {
			return &Error{Method: method, Path: path, Status: rep.status, Text: shorten(rep.data)}
		}
		if out != nil && len(bytes.TrimSpace(rep.data)) > 0 {
			if err := json.Unmarshal(rep.data, out); err != nil {
				return fmt.Errorf("aoscx: %s %s: decode: %w", method, path, err)
			}
		}
		return nil
	}
}

// Get reads path into out.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) send(ctx context.Context, method, path string, payload []byte, contentType string, sess session) (reply, error) {
	var r io.Reader
	if payload != nil {
		r = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return reply{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if sess.csrf != "" {
		req.Header.Set("x-csrf-token", sess.csrf)
	}
	for _, ck := range sess.cookies {
		req.AddCookie(ck)
	}
	return c.do(req)
}

// reply is a response, read completely.
type reply struct {
	status  int
	header  http.Header
	cookies []*http.Cookie
	data    []byte
}

func (c *Client) do(req *http.Request) (reply, error) {
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return reply{}, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return reply{}, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, req.Method, req.URL.Path, err)
	}
	if len(data) > maxResponse {
		return reply{}, fmt.Errorf("aoscx: %s %s: response larger than %d bytes", req.Method, req.URL.Path, maxResponse)
	}
	c.log.Trace().Str("method", req.Method).Str("path", req.URL.Path).Int("status", resp.StatusCode).
		Dur("took", time.Since(start)).Msg("aoscx request")
	return reply{status: resp.StatusCode, header: resp.Header, cookies: resp.Cookies(), data: data}, nil
}

// Close logs out if there is a session.
func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == 0 {
		return nil
	}
	rep, err := c.send(ctx, http.MethodPost, c.prefix+"/logout", nil, "", c.current())
	c.drop()
	if err != nil {
		return err
	}
	if rep.status != http.StatusOK && rep.status != http.StatusUnauthorized {
		return &Error{Method: http.MethodPost, Path: "/logout", Status: rep.status, Text: shorten(rep.data)}
	}
	c.log.Debug().Msg("logged out")
	return nil
}

// shorten returns the start of an error body as one line.
func shorten(data []byte) string {
	s := strings.Join(strings.Fields(string(data)), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// EscapePath escapes an interface name for a URL path ("1/1/1" -> "1%2F1%2F1").
func EscapePath(name string) string { return url.PathEscape(name) }
