// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/docs"
	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/dhcp"
	"github.com/joernott/dcdash/internal/dns"
	"github.com/joernott/dcdash/internal/dns/bind"
	"github.com/joernott/dcdash/internal/dns/cloudflare"
	"github.com/joernott/dcdash/internal/help"
	"github.com/joernott/dcdash/internal/httpx"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/netbox"
	"github.com/joernott/dcdash/internal/prefs"
	"github.com/joernott/dcdash/internal/project"
	"github.com/joernott/dcdash/internal/proxmox"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/status"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/switches"
	"github.com/joernott/dcdash/internal/totp"
	"github.com/joernott/dcdash/internal/vault"
	"github.com/joernott/dcdash/internal/version"
)

// app holds the services of a running server and what they add to it.
type app struct {
	routes     []func(*http.ServeMux)
	middleware []func(http.Handler) http.Handler
	// runners are background tasks that run until the context is canceled.
	runners []func(context.Context) error
	closers []func() error
	// statusChecks are shown on the dashboard (GET /api/v1/status).
	statusChecks []status.Check
	// audit is the audit log, e.g. for metrics.
	audit *audit.Log
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
	kr, err := newKeyring(ctx, cfg, st)
	if err != nil {
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
	a.audit = auditLog
	a.runners = append(a.runners, func(ctx context.Context) error { return auditLog.Run(ctx, cfg.Audit.Retention.D()) })

	authz := auth.NewAuthorizer(rbac.NewResolver(cfg.Auth.RootGroup, cfg.RBACRoles()), auditLog)
	sessions := session.NewManager(st.DB(), cfg.Auth.Session.Lifetime.D(), cfg.Auth.Session.IdleTimeout.D(), log)
	a.runners = append(a.runners, sessions.Run)

	authSvc, directory, err := newAuthService(cfg.Auth, sessions, authz, auditLog, totp.NewStore(st.DB(), kr), kr, log)
	if err != nil {
		return nil, err
	}
	a.statusChecks = append(a.statusChecks, status.Check{Name: "database", Kind: "system", Check: st.Check})
	if directory != nil {
		a.statusChecks = append(a.statusChecks, status.Check{Name: "directory", Kind: "system", Detail: directory.Status})
	}
	a.routes = append(a.routes,
		authSvc.Register,
		func(mux *http.ServeMux) {
			auditLog.Register(mux, func(h http.Handler) http.Handler { return auth.Require(rbac.PermAuditRead, h) })
		},
		func(mux *http.ServeMux) { prefs.NewStore(st.DB()).Register(mux, auditLog, auth.RequireAuthenticated) },
	)
	docsSvc, err := help.New(docs.FS, version.Version)
	if err != nil {
		return nil, err
	}
	a.routes = append(a.routes, func(mux *http.ServeMux) { docsSvc.Register(mux, auth.RequireAuthenticated) })
	pve, err := newProxmox(cfg.Proxmox, auditLog, sessions, log)
	if err != nil {
		return nil, err
	}
	if pve != nil {
		a.statusChecks = append(a.statusChecks, pve.StatusChecks()...)
		a.routes = append(a.routes, pve.Register)
	}
	dnsSvc, err := newDNS(cfg.DNS, auditLog, log)
	if err != nil {
		return nil, err
	}
	if dnsSvc != nil {
		a.statusChecks = append(a.statusChecks, dnsSvc.StatusChecks()...)
		a.routes = append(a.routes, dnsSvc.Register)
	}
	var dhcpSvc *dhcp.Service
	if len(cfg.DHCP.Servers) > 0 {
		if dhcpSvc, err = dhcp.NewService(cfg.DHCP.Servers, auditLog, log); err != nil {
			return nil, err
		}
		a.statusChecks = append(a.statusChecks, dhcpSvc.StatusChecks()...)
		a.routes = append(a.routes, dhcpSvc.Register)
	}
	if len(cfg.Switches) > 0 {
		swSvc, err := switches.NewService(cfg.Switches, auditLog, log)
		if err != nil {
			return nil, err
		}
		a.statusChecks = append(a.statusChecks, swSvc.StatusChecks()...)
		a.routes = append(a.routes, swSvc.Register)
		a.closers = append(a.closers, swSvc.Close)
	}
	if pve != nil {
		// The creation wizard adds DHCP reservations and DNS records (task 11.14).
		pve.SetIntegrations(dhcpSvc, dnsSvc)
	}
	var nb *netbox.Service
	if cfg.NetBox.Enabled() {
		if nb, err = netbox.NewService(cfg.NetBox, auditLog, log); err != nil {
			return nil, err
		}
		a.statusChecks = append(a.statusChecks, nb.StatusChecks()...)
		a.routes = append(a.routes, nb.Register)
		// Changes of guests, DHCP reservations and DNS records are written to
		// NetBox (task 12.3).
		if pve != nil {
			pve.SetNetBox(nb)
		}
		if dhcpSvc != nil {
			dhcpSvc.SetNetBox(nb)
		}
		if dnsSvc != nil {
			dnsSvc.SetNetBox(nb)
		}
		// The comparison view (task 12.4) reads the state of the modules.
		var (
			gs netbox.GuestSource
			rs netbox.ReservationSource
			ds netbox.RecordSource
		)
		if pve != nil {
			gs = pve
		}
		if dhcpSvc != nil {
			rs = dhcpSvc
		}
		if dnsSvc != nil {
			ds = dnsSvc
		}
		nb.SetSources(gs, rs, ds)
	}
	// Projects (task 12.8): groups of objects with the tag project-<name>.
	projects := project.New(ctx, cfg.Projects, nb, auditLog, log)
	a.runners = append(a.runners, projects.Run)
	a.statusChecks = append(a.statusChecks, projects.StatusChecks()...)
	a.routes = append(a.routes, projects.Register)
	if pve != nil {
		pve.SetProjects(projects)
		projects.SetGuestSource(pve)
	}
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
func newAuthService(a config.Auth, sessions *session.Manager, authz *auth.Authorizer, auditLog *audit.Log,
	totpStore *totp.Store, kr *vault.Keyring, log zerolog.Logger,
) (*auth.Service, *ldapauth.Client, error) {
	opts := auth.Options{
		TOTP:        totpStore,
		Keyring:     kr,
		TOTPIssuer:  a.TOTP.Issuer,
		TOTPLockout: lockoutPolicy(a.Lockout.TOTP),
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
			return nil, nil, err
		}
		opts.Local = local
		log.Warn().Str("username", a.Local.Username).Msg("break-glass account enabled (always requires TOTP)")
	}
	if a.LDAP.Enabled() {
		client, err := ldapauth.New(a.LDAP, log)
		if err != nil {
			return nil, nil, err
		}
		opts.LDAP = client
		urls := make([]string, 0, len(a.LDAP.Endpoints()))
		for _, s := range a.LDAP.Endpoints() {
			urls = append(urls, s.URL)
		}
		log.Info().Strs("urls", urls).Str("mode", a.LDAP.Mode).Msg("LDAP authentication enabled")
	} else {
		log.Warn().Msg("auth.ldap.url and auth.ldap.servers are not set: LDAP login is disabled")
	}
	client, _ := opts.LDAP.(*ldapauth.Client)
	return auth.New(opts), client, nil
}

func lockoutPolicy(p config.LockoutPolicy) lockout.Policy {
	return lockout.Policy{MaxFailures: p.MaxFailures, Window: p.Window.D(), Duration: p.Duration.D()}
}

// newProxmox creates the Proxmox module, or nil if no cluster is configured.
func newProxmox(cfg config.Proxmox, auditLog *audit.Log, sessions *session.Manager, log zerolog.Logger) (*proxmox.Service, error) {
	if len(cfg.Clusters) == 0 {
		return nil, nil
	}
	clusters := make([]*proxmox.Cluster, 0, len(cfg.Clusters))
	for _, c := range cfg.Clusters {
		cl, err := proxmox.New(c, log)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, cl)
		log.Info().Str("cluster", c.Name).Int("endpoints", len(c.Endpoints)).Msg("proxmox: cluster configured")
	}
	return proxmox.NewService(clusters, auditLog, sessions, log), nil
}

// newDNS creates the DNS module, or nil if no provider is configured.
func newDNS(cfg config.DNS, auditLog *audit.Log, log zerolog.Logger) (*dns.Service, error) {
	if len(cfg.Providers) == 0 {
		return nil, nil
	}
	providers := make([]dns.Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		switch p.Type {
		case config.DNSTypeCloudflare:
			providers = append(providers, cloudflare.New(p, log))
		case config.DNSTypeBind:
			var files bind.ZoneFiles
			if p.Bind.SFTP.Enabled() {
				sf, err := bind.NewSFTP(p.Bind.SFTP, p.RequestTimeout())
				if err != nil {
					return nil, fmt.Errorf("dns provider %s: %w", p.Name, err)
				}
				files = sf
			}
			bp, err := bind.New(p, files, log)
			if err != nil {
				return nil, err
			}
			providers = append(providers, bp)
		}
		log.Info().Str("provider", p.Name).Str("type", p.Type).Msg("dns: provider configured")
	}
	return dns.NewService(providers, auditLog, log), nil
}
