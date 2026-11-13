// SPDX-License-Identifier: GPL-3.0-or-later

// Package proxmox connects dcdash to Proxmox VE clusters: inventory, power
// actions, tasks and consoles. See docs/adr/0002-proxmox-client.md for why it
// uses its own small API client.
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// maxResponse limits the size of an API response.
const maxResponse = 32 << 20

// Error is an error response of the Proxmox API.
type Error struct {
	// Status is the HTTP status code; Proxmox puts the message into the
	// reason phrase (e.g. "500 VM 101 not running").
	Status  int
	Message string
	// Errors are parameter errors of a 400 response.
	Errors map[string]string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("proxmox: %d %s", e.Status, e.Message)
	for k, v := range e.Errors {
		msg += fmt.Sprintf("; %s: %s", k, strings.TrimSpace(v))
	}
	return msg
}

// IsStatus reports whether err is an API error with the given status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// ErrUnavailable means that no endpoint of the cluster could be reached.
var ErrUnavailable = errors.New("proxmox: no endpoint reachable")

// endpoint is one node's API.
type endpoint struct {
	base string // https://host:port
	tls  *tls.Config
	http *http.Client
}

// Cluster is the API client of one cluster. It uses the endpoints in order and
// switches to the next one when an endpoint cannot be reached.
type Cluster struct {
	name      string
	endpoints []*endpoint
	active    atomic.Int32
	token     string // "user@realm!name=secret"
	timeout   time.Duration
	log       zerolog.Logger

	// console is the optional ticket login for consoles.
	consoleUser     string
	consolePassword string
	ticketMu        sync.Mutex
	ticket          *ticket
	now             func() time.Time
}

// New creates the client of a cluster. It does not connect.
func New(cfg config.ProxmoxCluster, log zerolog.Logger) (*Cluster, error) {
	c := &Cluster{
		name:            cfg.Name,
		token:           cfg.TokenID + "=" + cfg.TokenSecret.Value(),
		timeout:         cfg.RequestTimeout(),
		log:             log.With().Str("component", "proxmox").Str("cluster", cfg.Name).Logger(),
		consoleUser:     cfg.Console.Username,
		consolePassword: cfg.Console.Password.Value(),
		now:             time.Now,
	}
	for _, e := range cfg.Endpoints {
		u, err := url.Parse(e.URL)
		if err != nil {
			return nil, fmt.Errorf("proxmox cluster %s: %w", cfg.Name, err)
		}
		tc, err := tlsutil.ClientConfig(cfg.TLSFor(e), u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("proxmox cluster %s, endpoint %s: %w", cfg.Name, e.URL, err)
		}
		tr := &http.Transport{
			TLSClientConfig:     tc,
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		}
		c.endpoints = append(c.endpoints, &endpoint{
			base: strings.TrimSuffix(u.Scheme+"://"+u.Host, "/"),
			tls:  tc,
			http: &http.Client{Transport: tr, Timeout: c.timeout},
		})
	}
	if len(c.endpoints) == 0 {
		return nil, fmt.Errorf("proxmox cluster %s: no endpoints", cfg.Name)
	}
	return c, nil
}

// Name returns the cluster name.
func (c *Cluster) Name() string { return c.name }

// HasConsoleUser reports whether a Proxmox user is configured for consoles.
func (c *Cluster) HasConsoleUser() bool { return c.consoleUser != "" }

// authFunc adds authentication to a request.
type authFunc func(h http.Header)

func (c *Cluster) tokenAuth(h http.Header) { h.Set("Authorization", "PVEAPIToken="+c.token) }

// do sends a request, trying the endpoints in order starting with the last
// working one. form is sent as application/x-www-form-urlencoded (POST, PUT)
// or as the query string (GET, DELETE). The data member of the response is
// decoded into out (nil ignores it).
func (c *Cluster) do(ctx context.Context, method, path string, form url.Values, out any) error {
	_, err := c.doAuth(ctx, c.tokenAuth, method, path, form, out)
	return err
}

// doAuth is do with a given authentication; it returns the endpoint used.
func (c *Cluster) doAuth(ctx context.Context, a authFunc, method, path string, form url.Values, out any) (*endpoint, error) {
	n := len(c.endpoints)
	start := int(c.active.Load())
	var errs []error
	for i := range n {
		idx := (start + i) % n
		ep := c.endpoints[idx]
		err := c.send(ctx, ep, a, method, path, form, out)
		if err == nil || !failover(err) || ctx.Err() != nil {
			if err == nil && idx != start {
				c.active.Store(int32(idx)) // #nosec G115 -- idx < len(endpoints)
				c.log.Info().Str("endpoint", ep.base).Msg("switched to endpoint")
			}
			return ep, err
		}
		c.log.Warn().Err(err).Str("endpoint", ep.base).Msg("endpoint unavailable")
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(errs...))
}

func (c *Cluster) send(ctx context.Context, ep *endpoint, a authFunc, method, path string, form url.Values, out any) error {
	u := ep.base + "/api2/json" + path
	var body io.Reader
	if len(form) > 0 {
		if method == http.MethodGet || method == http.MethodDelete {
			u += "?" + form.Encode()
		} else {
			body = strings.NewReader(form.Encode())
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body) // #nosec G704 -- endpoint URL from the configuration
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	a(req.Header)
	res, err := ep.http.Do(req) // #nosec G704 -- endpoint URL from the configuration
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return responseError(res, data)
	}
	if out == nil {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("proxmox: invalid response: %w", err)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return fmt.Errorf("proxmox: response without data")
	}
	return json.Unmarshal(env.Data, out)
}

func responseError(res *http.Response, body []byte) error {
	e := &Error{Status: res.StatusCode}
	// The reason phrase carries the message, e.g. "500 VM 101 not running".
	e.Message = strings.TrimSpace(strings.TrimPrefix(res.Status, fmt.Sprint(res.StatusCode)))
	var v struct {
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &v) == nil {
		if m := strings.TrimSpace(v.Message); m != "" {
			e.Message = m
		}
		e.Errors = v.Errors
	}
	return e
}

// failover reports whether another endpoint may succeed: connection and TLS
// errors, timeouts and gateway errors of a reverse proxy in front of a node.
// Errors of the Proxmox API itself (4xx, 500, 595, ...) are returned.
func failover(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Status == http.StatusBadGateway || e.Status == http.StatusServiceUnavailable || e.Status == http.StatusGatewayTimeout
	}
	var ue *url.Error
	return errors.As(err, &ue)
}

// Version is the response of GET /version.
type Version struct {
	Version string `json:"version"`
	Release string `json:"release"`
}

// Version returns the Proxmox VE version.
func (c *Cluster) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.do(ctx, http.MethodGet, "/version", nil, &v)
	return v, err
}

// Check is the status check of the cluster for the dashboard.
func (c *Cluster) Check(ctx context.Context) (string, error) {
	v, err := c.Version(ctx)
	if err != nil {
		return "", err
	}
	return "Proxmox VE " + v.Version + " via " + c.endpoints[c.active.Load()].base, nil
}

// ticket is a Proxmox login ticket of the console user.
type ticket struct {
	value   string
	csrf    string
	expires time.Time
}

// ticketLifetime is shorter than Proxmox's 2 hours.
const ticketLifetime = 90 * time.Minute

// ticketAuth returns authentication with a ticket of the console user, logging
// in when needed.
func (c *Cluster) ticketAuth(ctx context.Context) (authFunc, error) {
	c.ticketMu.Lock()
	defer c.ticketMu.Unlock()
	if c.ticket == nil || c.now().After(c.ticket.expires) {
		var v struct {
			Ticket string `json:"ticket"`
			CSRF   string `json:"CSRFPreventionToken"`
		}
		form := url.Values{"username": {c.consoleUser}, "password": {c.consolePassword}}
		if _, err := c.doAuth(ctx, func(http.Header) {}, http.MethodPost, "/access/ticket", form, &v); err != nil {
			return nil, fmt.Errorf("console user login: %w", err)
		}
		c.ticket = &ticket{value: v.Ticket, csrf: v.CSRF, expires: c.now().Add(ticketLifetime)}
	}
	t := *c.ticket
	return func(h http.Header) {
		h.Set("Cookie", "PVEAuthCookie="+t.value)
		h.Set("CSRFPreventionToken", t.csrf)
	}, nil
}
