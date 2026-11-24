// SPDX-License-Identifier: GPL-3.0-or-later

// Package ad manages an Active Directory domain (Windows or Samba) over LDAPS: users,
// groups, passwords and the password policy. See docs/adr/0010-active-directory.md.
package ad

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/tlsutil"
)

// Errors of directory operations; *Error wraps one of them.
var (
	ErrUnavailable   = errors.New("directory unavailable")
	ErrNotFound      = errors.New("not found")
	ErrExists        = errors.New("already exists")
	ErrForbidden     = errors.New("insufficient rights")
	ErrPolicy        = errors.New("password policy")
	ErrWrongPassword = errors.New("wrong password")
	ErrInvalid       = errors.New("rejected by the directory")
)

// Error is an error answer of the directory. Message is the server's diagnostic message.
type Error struct {
	Kind    error
	Op      string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Op + ": " + e.Kind.Error()
	}
	return e.Op + ": " + e.Kind.Error() + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.Kind }

// wrap turns an LDAP error into an *Error (or ErrUnavailable for network errors).
func wrap(op string, err error) error {
	var ae *Error
	if err == nil || errors.As(err, &ae) || errors.Is(err, ErrUnavailable) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var le *ldap.Error
	if !errors.As(err, &le) {
		return fmt.Errorf("%s: %w: %w", op, ErrUnavailable, err)
	}
	msg := strings.TrimRight(le.Err.Error(), "\x00 ")
	if i := strings.Index(msg, "LDAP Result Code"); i >= 0 {
		if j := strings.Index(msg[i:], ": "); j >= 0 {
			msg = msg[i+j+2:]
		}
	}
	kind := ErrInvalid
	switch {
	case le.ResultCode == ldap.ErrorNetwork || le.ResultCode == ldap.LDAPResultServerDown || le.ResultCode == ldap.LDAPResultTimeout ||
		le.ResultCode == ldap.LDAPResultUnavailable || le.ResultCode == ldap.LDAPResultBusy:
		kind = ErrUnavailable
	case le.ResultCode == ldap.LDAPResultNoSuchObject:
		kind = ErrNotFound
	case le.ResultCode == ldap.LDAPResultEntryAlreadyExists:
		kind = ErrExists
	case le.ResultCode == ldap.LDAPResultInsufficientAccessRights:
		kind = ErrForbidden
	// The Windows error code at the start of the message: 00000056 is ERROR_INVALID_PASSWORD
	// (wrong old password), 0000052D ERROR_PASSWORD_RESTRICTION (policy, history, minimum age).
	case strings.HasPrefix(msg, "00000056"):
		kind = ErrWrongPassword
	case strings.HasPrefix(msg, "0000052D"):
		kind = ErrPolicy
	case le.ResultCode == ldap.LDAPResultInvalidCredentials:
		kind = ErrWrongPassword
	}
	return &Error{Kind: kind, Op: op, Message: msg}
}

// server is one domain controller with its TLS configuration.
type server struct {
	url  string
	host string
	tls  *tls.Config
}

// Client manages one domain. Every operation opens a connection to the domain controller
// that worked last, failing over to the others, and binds as the service account.
type Client struct {
	cfg       config.Directory
	servers   []server
	preferred atomic.Int32
	log       zerolog.Logger

	// sid is the domain SID, read once.
	sidMu sync.Mutex
	sid   string
}

// New creates a client. It does not connect yet.
func New(cfg config.Directory, log zerolog.Logger) (*Client, error) {
	if len(cfg.Servers) == 0 {
		return nil, errors.New("ad: no server configured")
	}
	c := &Client{cfg: cfg, log: log.With().Str("component", "directory").Str("directory", cfg.Name).Logger()}
	for _, s := range cfg.Servers {
		u, err := url.Parse(s.URL)
		if err != nil {
			return nil, fmt.Errorf("ad url: %w", err)
		}
		t, err := tlsutil.ClientConfig(cfg.TLSFor(s), u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("ad tls for %s: %w", s.URL, err)
		}
		c.servers = append(c.servers, server{url: s.URL, host: u.Host, tls: t})
	}
	return c, nil
}

// Name returns the name of the directory.
func (c *Client) Name() string { return c.cfg.Name }

// Config returns the configuration of the directory.
func (c *Client) Config() config.Directory { return c.cfg }

func (c *Client) dialServer(s server) (*ldap.Conn, error) {
	timeout := c.cfg.RequestTimeout()
	conn, err := ldap.DialURL(s.url, ldap.DialWithTLSConfig(s.tls), ldap.DialWithDialer(&net.Dialer{Timeout: timeout}))
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", s.url, err)
	}
	conn.SetTimeout(timeout)
	return conn, nil
}

// connect connects to the preferred server, failing over to the others, and binds as the
// service account.
func (c *Client) connect(ctx context.Context) (*ldap.Conn, error) {
	start := int(c.preferred.Load())
	msgs := make([]string, 0, len(c.servers))
	for n := range c.servers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		i := (start + n) % len(c.servers)
		conn, err := c.dialServer(c.servers[i])
		if err != nil {
			if len(c.servers) > 1 {
				c.log.Warn().Err(err).Str("url", c.servers[i].url).Msg("domain controller unavailable")
			}
			msgs = append(msgs, err.Error())
			continue
		}
		if i != start {
			c.log.Warn().Str("url", c.servers[i].url).Str("previous", c.servers[start].url).Msg("failed over to another domain controller")
			c.preferred.Store(int32(i)) // #nosec G115 -- i < len(servers), a short config list
		}
		if err := conn.Bind(c.cfg.BindDN, c.cfg.BindPassword.Value()); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: service account bind as %s: %w", ErrUnavailable, c.cfg.BindDN, err)
		}
		return conn, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(msgs, "; "))
}

// with runs f on a connection bound as the service account.
func (c *Client) with(ctx context.Context, f func(*ldap.Conn) error) error {
	conn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return f(conn)
}

// Check connects to every domain controller and binds as the service account. The
// directory is usable if one works; the others are named in the detail.
func (c *Client) Check(ctx context.Context) (status.Detail, error) {
	errs := make([]error, len(c.servers))
	var wg sync.WaitGroup
	for i, s := range c.servers {
		wg.Go(func() {
			conn, err := c.dialServer(s)
			if err == nil {
				err = conn.Bind(c.cfg.BindDN, c.cfg.BindPassword.Value())
				if err != nil {
					err = fmt.Errorf("service account bind at %s: %w", s.url, err)
				}
				_ = conn.Close()
			}
			errs[i] = err
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return status.Detail{}, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
	var up, down, msgs []string
	for i, err := range errs {
		if err != nil {
			down = append(down, c.servers[i].host)
			msgs = append(msgs, err.Error())
			continue
		}
		up = append(up, c.servers[i].host)
	}
	params := map[string]string{"domain": c.cfg.Domain, "count": strconv.Itoa(len(up))}
	switch {
	case len(up) == 0:
		return status.Detail{}, fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(msgs, "; "))
	case len(down) == 0:
		return status.Detail{Text: c.cfg.Domain + ": " + strings.Join(up, ", "), Code: "ad.connected", Params: params}, nil
	default:
		params["down"] = strings.Join(down, ", ")
		return status.Detail{Text: fmt.Sprintf("%s; not reachable: %s", c.cfg.Domain, strings.Join(msgs, "; ")),
			Code: "ad.degraded", Params: params}, nil
	}
}

// search runs a paged search and returns at most limit entries (0: all); truncated tells
// whether there were more.
func search(ctx context.Context, conn *ldap.Conn, base string, scope int, filter string, attrs []string, limit int) (out []*ldap.Entry, truncated bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req := ldap.NewSearchRequest(base, scope, ldap.NeverDerefAliases, 0, 0, false, filter, attrs,
		[]ldap.Control{ldap.NewControlPaging(500)})
	r := conn.SearchAsync(ctx, req, 64)
	for r.Next() {
		if r.Entry() == nil { // a referral, e.g. to the DNS partitions
			continue
		}
		if limit > 0 && len(out) >= limit {
			truncated = true
			cancel()
			break
		}
		out = append(out, r.Entry())
	}
	if err := r.Err(); err != nil && !truncated {
		return nil, false, err
	}
	return out, truncated, nil
}

// one returns the single entry of a base search, or ErrNotFound.
func one(conn *ldap.Conn, dn string, attrs ...string) (*ldap.Entry, error) {
	r, err := conn.Search(ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 0, false, "(objectClass=*)", attrs, nil))
	if err != nil {
		return nil, err
	}
	if len(r.Entries) == 0 {
		return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New(dn))
	}
	return r.Entries[0], nil
}

// domainSID returns the SID of the domain (read once).
func (c *Client) domainSID(conn *ldap.Conn) (string, error) {
	c.sidMu.Lock()
	defer c.sidMu.Unlock()
	if c.sid != "" {
		return c.sid, nil
	}
	e, err := one(conn, c.cfg.BaseDN, "objectSid")
	if err != nil {
		return "", err
	}
	sid, err := parseSID(e.GetRawAttributeValue("objectSid"))
	if err != nil {
		return "", err
	}
	c.sid = sid
	return sid, nil
}

// parentDN returns the DN without its first RDN; escaped commas belong to the RDN.
func parentDN(dn string) string {
	for i := 0; i < len(dn); i++ {
		switch dn[i] {
		case '\\':
			i++
		case ',':
			return dn[i+1:]
		}
	}
	return ""
}

// fileTime converts an AD FILETIME (100 ns since 1601) to a time; 0 and the maximum are
// "never".
func fileTime(s string) *time.Time {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v == 1<<63-1 {
		return nil
	}
	t := time.Unix(0, 0).UTC().Add(time.Duration(v-116444736000000000) * 100)
	return &t
}

// generalizedTime parses whenCreated and whenChanged.
func generalizedTime(s string) *time.Time {
	t, err := time.Parse("20060102150405.0Z", s)
	if err != nil {
		return nil
	}
	return &t
}

// interval converts a negative AD interval (100 ns units, maxPwdAge etc.) to a duration;
// the minimum value means "never".
func interval(s string) time.Duration {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v == -1<<63 || v >= 0 {
		return 0
	}
	return time.Duration(-v) * 100
}
