// SPDX-License-Identifier: GPL-3.0-or-later

// Package ldapauth authenticates users against an LDAP directory (Active
// Directory, Samba AD or a generic LDAP server) and resolves their groups.
package ldapauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/tlsutil"
)

var (
	// ErrInvalidCredentials is returned for unknown users, wrong passwords and
	// disabled accounts alike, so that callers cannot tell them apart.
	ErrInvalidCredentials = errors.New("invalid username or password")
	// ErrUnavailable is returned when the directory cannot be used (network,
	// TLS or service account problems).
	ErrUnavailable = errors.New("directory unavailable")
)

// maxUsernameLength guards against abusive input; real login names are short.
const maxUsernameLength = 256

// AD userAccountControl flag for disabled accounts.
const uacAccountDisable = 0x2

// User is an authenticated directory user.
type User struct {
	DN          string
	Username    string
	DisplayName string
	Email       string
	Groups      []Group
}

// Group is a directory group the user is a (possibly nested) member of.
type Group struct {
	DN   string
	Name string // the group's CN
}

// Client talks to the directory. It opens a connection per operation, which
// keeps it simple and robust against server restarts; logins are infrequent.
type Client struct {
	cfg  config.LDAP
	tls  *tls.Config
	log  zerolog.Logger
	dial func() (*ldap.Conn, error)
}

// New creates a client. It does not connect yet.
func New(cfg config.LDAP, log zerolog.Logger) (*Client, error) {
	cfg.ApplyDefaults()
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("ldap url: %w", err)
	}
	tlsCfg, err := tlsutil.ClientConfig(cfg.TLS, u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("ldap tls: %w", err)
	}
	c := &Client{cfg: cfg, tls: tlsCfg, log: log.With().Str("component", "ldap").Logger()}
	c.dial = c.connect
	return c, nil
}

func (c *Client) connect() (*ldap.Conn, error) {
	timeout := c.cfg.Timeout.D()
	conn, err := ldap.DialURL(c.cfg.URL,
		ldap.DialWithTLSConfig(c.tls),
		ldap.DialWithDialer(&net.Dialer{Timeout: timeout}),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: connect to %s: %w", ErrUnavailable, c.cfg.URL, err)
	}
	conn.SetTimeout(timeout)
	if c.cfg.StartTLS {
		if err := conn.StartTLS(c.tls); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: StartTLS: %w", ErrUnavailable, err)
		}
	}
	return conn, nil
}

// serviceConn connects and binds as the service account.
func (c *Client) serviceConn() (*ldap.Conn, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	if err := conn.Bind(c.cfg.BindDN, c.cfg.BindPassword.Value()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: service account bind as %s: %w", ErrUnavailable, c.cfg.BindDN, err)
	}
	return conn, nil
}

// Check verifies that the directory is reachable and the service account can
// bind; it is used as a readiness check.
func (c *Client) Check(context.Context) (string, error) {
	conn, err := c.serviceConn()
	if err != nil {
		return "", err
	}
	_ = conn.Close()
	return "", nil
}

// userFilter builds the search filter for a login name; the name is escaped.
func (c *Client) userFilter(username string) string {
	return strings.ReplaceAll(c.cfg.UserFilter, "{username}", ldap.EscapeFilter(username))
}

func (c *Client) userAttributes() []string {
	a := c.cfg.Attributes
	attrs := []string{a.Username, a.DisplayName, a.Email}
	if c.cfg.Mode == config.LDAPModeAD {
		attrs = append(attrs, "userAccountControl")
	}
	return attrs
}

// findUser searches the user and returns the entry if it is unique.
func (c *Client) findUser(conn *ldap.Conn, username string) (*ldap.Entry, error) {
	req := ldap.NewSearchRequest(c.cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		2, int(c.cfg.Timeout.D()/time.Second), false, c.userFilter(username), c.userAttributes(), nil)
	res, err := conn.Search(req)
	switch {
	case ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded):
		c.log.Warn().Str("username", username).Msg("login name matches several directory entries")
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, fmt.Errorf("%w: search user: %w", ErrUnavailable, err)
	case len(res.Entries) == 0:
		c.log.Debug().Str("username", username).Msg("user not found")
		return nil, ErrInvalidCredentials
	case len(res.Entries) > 1:
		c.log.Warn().Str("username", username).Msg("login name matches several directory entries")
		return nil, ErrInvalidCredentials
	}
	return res.Entries[0], nil
}

func accountDisabled(e *ldap.Entry) bool {
	uac, err := strconv.ParseInt(e.GetAttributeValue("userAccountControl"), 10, 64)
	return err == nil && uac&uacAccountDisable != 0
}

// Authenticate verifies username and password and returns the user with its
// groups. It returns ErrInvalidCredentials for any credential problem and an
// error wrapping ErrUnavailable if the directory cannot be used.
func (c *Client) Authenticate(ctx context.Context, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	// An empty password would result in an unauthenticated bind, which
	// succeeds on many servers: never let it reach the directory.
	if username == "" || password == "" || len(username) > maxUsernameLength {
		return nil, ErrInvalidCredentials
	}

	conn, err := c.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	entry, err := c.findUser(conn, username)
	if err != nil {
		return nil, err
	}
	if c.cfg.Mode == config.LDAPModeAD && accountDisabled(entry) {
		c.log.Info().Str("dn", entry.DN).Msg("login attempt for disabled account")
		return nil, ErrInvalidCredentials
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := c.verifyPassword(entry.DN, password); err != nil {
		return nil, err
	}

	return c.userWithGroups(conn, entry, username)
}

// verifyPassword binds as the user on a separate connection.
func (c *Client) verifyPassword(dn, password string) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Bind(dn, password)
	switch {
	case err == nil:
		return nil
	case ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials):
		return ErrInvalidCredentials
	default:
		return fmt.Errorf("%w: user bind: %w", ErrUnavailable, err)
	}
}

// LookupUser returns a user with its groups without verifying a password, e.g.
// to refresh the roles of a logged-in user. Unknown or disabled users result in
// ErrInvalidCredentials.
func (c *Client) LookupUser(ctx context.Context, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(username) > maxUsernameLength {
		return nil, ErrInvalidCredentials
	}
	conn, err := c.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	entry, err := c.findUser(conn, username)
	if err != nil {
		return nil, err
	}
	if c.cfg.Mode == config.LDAPModeAD && accountDisabled(entry) {
		return nil, ErrInvalidCredentials
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.userWithGroups(conn, entry, username)
}

func (c *Client) userWithGroups(conn *ldap.Conn, entry *ldap.Entry, username string) (*User, error) {
	a := c.cfg.Attributes
	user := &User{
		DN:          entry.DN,
		Username:    entry.GetAttributeValue(a.Username),
		DisplayName: entry.GetAttributeValue(a.DisplayName),
		Email:       entry.GetAttributeValue(a.Email),
	}
	if user.Username == "" {
		user.Username = username
	}
	groups, err := c.groups(conn, entry.DN, user.Username)
	if err != nil {
		return nil, err
	}
	user.Groups = groups
	return user, nil
}
