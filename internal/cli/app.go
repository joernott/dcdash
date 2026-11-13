// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/vault"
)

// app holds the services of a running server and what they add to it.
type app struct {
	routes     []func(*http.ServeMux)
	middleware []func(http.Handler) http.Handler
	// runners are background tasks that run until the context is canceled.
	runners []func(context.Context) error
	closers []func() error
}

// Close releases resources (e.g. the syslog connection).
func (a *app) Close() {
	for _, c := range a.closers {
		_ = c()
	}
}

// newApp creates the services that need the database: audit log, sessions,
// authorization and the login API.
func newApp(ctx context.Context, cfg *config.Config, st *store.Store, log zerolog.Logger) (*app, error) {
	a := &app{}
	if _, err := newKeyring(ctx, cfg, st); err != nil {
		return nil, err
	}
	proxies, err := cfg.Server.TrustedProxyPrefixes()
	if err != nil {
		return nil, err
	}

	auditLog, err := newAuditLog(cfg.Audit, st, log, a)
	if err != nil {
		return nil, err
	}
	a.runners = append(a.runners, func(ctx context.Context) error { return auditLog.Run(ctx, cfg.Audit.Retention.D()) })

	authz := auth.NewAuthorizer(rbac.NewResolver(cfg.Auth.RootGroup, cfg.RBACRoles()), auditLog)
	sessions := session.NewManager(st.DB(), cfg.Auth.Session.Lifetime.D(), cfg.Auth.Session.IdleTimeout.D(), log)
	a.runners = append(a.runners, sessions.Run)

	authSvc, err := newAuthService(cfg.Auth, sessions, authz, auditLog, log)
	if err != nil {
		return nil, err
	}
	a.routes = append(a.routes,
		authSvc.Register,
		func(mux *http.ServeMux) {
			auditLog.Register(mux, func(h http.Handler) http.Handler { return auth.Require(rbac.PermAuditRead, h) })
		},
	)
	a.middleware = append(a.middleware,
		httpx.ClientIPMiddleware(proxies), sessions.Middleware, authz.Middleware, session.CSRFExcept(auth.LoginPath))
	return a, nil
}

// newAuditLog creates the audit log with the configured additional outputs.
func newAuditLog(cfg config.Audit, st *store.Store, log zerolog.Logger, a *app) (*audit.Log, error) {
	var sinks []audit.Sink
	if cfg.File != "" {
		sinks = append(sinks, audit.NewFileSink(cfg.File))
		log.Info().Str("file", cfg.File).Msg("audit log: writing to file")
	}
	if cfg.Syslog.Enabled {
		s, err := audit.NewSyslogSink(cfg.Syslog.Network, cfg.Syslog.Address, cfg.Syslog.Tag)
		if err != nil {
			return nil, fmt.Errorf("audit.syslog: %w", err)
		}
		a.closers = append(a.closers, s.Close)
		sinks = append(sinks, s)
		log.Info().Str("address", cfg.Syslog.Address).Msg("audit log: writing to syslog")
	}
	return audit.New(st.DB(), log, sinks...), nil
}

// newKeyring builds the key ring from the configuration and verifies that it
// matches the data in the store.
func newKeyring(ctx context.Context, cfg *config.Config, st *store.Store) (*vault.Keyring, error) {
	current, err := vault.ParseKey(cfg.Security.EncryptionKey.Value())
	if err != nil {
		return nil, fmt.Errorf("security.encryption_key: %w", err)
	}
	var previous [][]byte
	for _, s := range cfg.Security.PreviousEncryptionKeys {
		k, err := vault.ParseKey(s.Value())
		if err != nil {
			return nil, fmt.Errorf("security.previous_encryption_keys: %w", err)
		}
		previous = append(previous, k)
	}
	kr, err := vault.New(current, previous...)
	if err != nil {
		return nil, err
	}
	if err := kr.Verify(ctx, st, store.ErrNotFound); err != nil {
		return nil, err
	}
	return kr, nil
}

// newAuthService creates the authentication service.
func newAuthService(a config.Auth, sessions *session.Manager, authz *auth.Authorizer, auditLog *audit.Log, log zerolog.Logger) (*auth.Service, error) {
	opts := auth.Options{
		Sessions:    sessions,
		Authorizer:  authz,
		Audit:       auditLog,
		UserLockout: lockoutPolicy(a.Lockout.User),
		IPLockout:   lockoutPolicy(a.Lockout.IP),
		Log:         log,
	}
	if a.Local.Enabled {
		local, err := auth.NewLocalAccount(a.Local.Username, a.Local.PasswordHash.Value())
		if err != nil {
			return nil, err
		}
		opts.Local = local
		log.Warn().Str("username", a.Local.Username).Msg("break-glass account enabled (always requires TOTP)")
	}
	if a.LDAP.URL != "" {
		client, err := ldapauth.New(a.LDAP, log)
		if err != nil {
			return nil, err
		}
		opts.LDAP = client
		log.Info().Str("url", a.LDAP.URL).Str("mode", a.LDAP.Mode).Msg("LDAP authentication enabled")
	} else {
		log.Warn().Msg("auth.ldap.url is not set: LDAP login is disabled")
	}
	return auth.New(opts), nil
}

func lockoutPolicy(p config.LockoutPolicy) lockout.Policy {
	return lockout.Policy{MaxFailures: p.MaxFailures, Window: p.Window.D(), Duration: p.Duration.D()}
}
