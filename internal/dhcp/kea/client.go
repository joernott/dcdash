// SPDX-License-Identifier: GPL-3.0-or-later

// Package kea is a client for the control socket of a Kea 3.x DHCP daemon
// (kea-dhcp4 or kea-dhcp6) over HTTP(S), see docs/adr/0003-kea-api.md.
package kea

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
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// maxResponse limits the size of a command response (lease pages, statistics).
const maxResponse = 32 << 20

// Kea result codes.
const (
	ResultSuccess     = 0
	ResultError       = 1
	ResultUnsupported = 2
	ResultEmpty       = 3
	ResultConflict    = 4
)

var (
	// ErrUnavailable means that the server could not be reached or did not
	// answer with a Kea response.
	ErrUnavailable = errors.New("kea: server unavailable")
	// ErrUnauthorized means that Kea rejected dcdash's credentials.
	ErrUnauthorized = errors.New("kea: unauthorized")
	// ErrUnsupported means that the command is not available (result 2),
	// usually because the hook library is not loaded.
	ErrUnsupported = errors.New("kea: command not supported")
	// ErrConflict means that the change conflicts with the server state
	// (result 4).
	ErrConflict = errors.New("kea: conflict")
)

// Error is a command that Kea answered with an error result.
type Error struct {
	Command string
	Result  int
	Text    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("kea: %s: %s (result %d)", e.Command, e.Text, e.Result)
}

// Is maps result codes to ErrUnsupported and ErrConflict.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnsupported:
		return e.Result == ResultUnsupported
	case ErrConflict:
		return e.Result == ResultConflict
	}
	return false
}

// Response is the answer to a command.
type Response struct {
	Result    int             `json:"result"`
	Text      string          `json:"text"`
	Arguments json.RawMessage `json:"arguments"`
}

// Empty reports whether the command succeeded without data (result 3).
func (r Response) Empty() bool { return r.Result == ResultEmpty }

// Client sends commands to one Kea daemon.
type Client struct {
	name     string
	family   string
	url      string
	username string
	password string
	http     *http.Client
	log      zerolog.Logger
}

// New creates the client of a server. It does not connect.
func New(cfg config.DHCPServer, log zerolog.Logger) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("dhcp server %s: %w", cfg.Name, err)
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
			return nil, fmt.Errorf("dhcp server %s: %w", cfg.Name, err)
		}
		tr.TLSClientConfig = tc
	}
	return &Client{
		name:     cfg.Name,
		family:   cfg.Family,
		url:      u.Scheme + "://" + u.Host + "/",
		username: cfg.Username,
		password: cfg.Password.Value(),
		http:     &http.Client{Transport: tr, Timeout: cfg.RequestTimeout()},
		log:      log.With().Str("component", "kea").Str("server", cfg.Name).Logger(),
	}, nil
}

// Name returns the server name.
func (c *Client) Name() string { return c.name }

// Family returns "v4" or "v6".
func (c *Client) Family() string { return c.family }

// v returns "4" or "6" for command names such as lease4-get-page.
func (c *Client) v() string { return strings.TrimPrefix(c.family, "v") }

type request struct {
	Command   string `json:"command"`
	Arguments any    `json:"arguments,omitempty"`
}

// Do sends a command. A success (0) or empty (3) result returns no error,
// and the arguments are decoded into out (if out is not nil and arguments
// were returned). Other results return an *Error.
func (c *Client) Do(ctx context.Context, command string, args, out any) (Response, error) {
	body, err := json.Marshal(request{Command: command, Arguments: args})
	if err != nil {
		return Response{}, fmt.Errorf("kea: %s: %w", command, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("kea: %s: %w", command, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.username, c.password)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, command, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return Response{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, command, err)
	}
	c.log.Trace().Str("command", command).Int("status", resp.StatusCode).Dur("took", time.Since(start)).Msg("kea command")
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return Response{}, fmt.Errorf("%w: %s", ErrUnauthorized, command)
	case resp.StatusCode != http.StatusOK:
		return Response{}, fmt.Errorf("%w: %s: HTTP %d", ErrUnavailable, command, resp.StatusCode)
	case len(data) > maxResponse:
		return Response{}, fmt.Errorf("kea: %s: response larger than %d bytes", command, maxResponse)
	}
	r, err := decode(data)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, command, err)
	}
	if r.Result != ResultSuccess && r.Result != ResultEmpty {
		return r, &Error{Command: command, Result: r.Result, Text: r.Text}
	}
	if out != nil && len(r.Arguments) > 0 && string(r.Arguments) != "null" {
		if err := json.Unmarshal(r.Arguments, out); err != nil {
			return r, fmt.Errorf("kea: %s: decode arguments: %w", command, err)
		}
	}
	return r, nil
}

// decode reads a response: a list with one element per service (Kea always
// sends a list on HTTP), or a single object.
func decode(data []byte) (Response, error) {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var list []Response
		if err := json.Unmarshal(data, &list); err != nil {
			return Response{}, err
		}
		if len(list) != 1 {
			return Response{}, fmt.Errorf("expected one response, got %d", len(list))
		}
		return list[0], nil
	}
	var r Response
	err := json.Unmarshal(data, &r)
	return r, err
}
