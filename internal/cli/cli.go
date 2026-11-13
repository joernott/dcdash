// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli implements the dcdash command line interface.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/joernott/dcdash/internal/auth"
	"github.com/joernott/dcdash/internal/certs"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/ldapauth"
	"github.com/joernott/dcdash/internal/lockout"
	"github.com/joernott/dcdash/internal/logging"
	"github.com/joernott/dcdash/internal/rbac"
	"github.com/joernott/dcdash/internal/server"
	"github.com/joernott/dcdash/internal/session"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/vault"
	"github.com/joernott/dcdash/internal/version"
)

// Deps are the external collaborators of the CLI, replaceable in tests.
type Deps struct {
	Stdout io.Writer
	Stderr io.Writer
	// Server holds the server options, e.g. the embedded frontend handler.
	Server server.Options
}

// NewRootCommand builds the dcdash command tree.
func NewRootCommand(deps Deps) *cobra.Command {
	var configPath string

	root := &cobra.Command{
		Use:           "dcdash",
		Short:         "Dashboard for managing central data center services",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(deps.Stdout)
	root.SetErr(deps.Stderr)
	root.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath(),
		"configuration file (env DCDASH_CONFIG)")

	root.AddCommand(
		newServeCommand(&configPath, deps),
		newVersionCommand(),
		newGenKeyCommand(),
		newHashPasswordCommand(),
		newConfigCommand(&configPath),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(deps Deps) int {
	if err := NewRootCommand(deps).Execute(); err != nil {
		fmt.Fprintln(deps.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func defaultConfigPath() string {
	if p := os.Getenv("DCDASH_CONFIG"); p != "" {
		return p
	}
	return config.DefaultPath
}

func newServeCommand(configPath *string, deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the dcdash server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, warnings, err := config.Load(*configPath)
			if err != nil {
				return err
			}
			log, err := logging.New(cfg.Log, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			for _, w := range warnings {
				log.Warn().Msg(w)
			}
			v := version.Get()
			log.Info().Str("version", v.Version).Str("commit", v.Commit).Str("config", *configPath).Msg("starting dcdash")

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			st, err := store.Open(ctx, cfg.DataDir)
			if err != nil {
				return err
			}
			defer st.Close()
			log.Info().Str("path", st.Path()).Msg("database opened")
			if _, err := newKeyring(ctx, cfg, st); err != nil {
				return err
			}

			opts := deps.Server
			opts.Checks = append(slices.Clone(opts.Checks), server.Check{Name: "database", Check: st.Check})
			mgr, err := certs.New(cfg, log)
			if err != nil {
				return err
			}
			g, gctx := errgroup.WithContext(ctx)
			if mgr != nil {
				opts.TLSConfig = certs.TLSConfig(mgr)
				opts.Checks = append(opts.Checks, server.Check{Name: "certificate", Check: mgr.Check})
				g.Go(func() error { return mgr.Run(gctx) })
			}

			authz := auth.NewAuthorizer(rbac.NewResolver(cfg.Auth.RootGroup, cfg.RBACRoles()))
			authSvc, sessions, err := newAuthService(cfg, st, authz, log)
			if err != nil {
				return err
			}
			opts.Routes = append(slices.Clone(opts.Routes), authSvc.Register)
			opts.Middleware = append(slices.Clone(opts.Middleware),
				sessions.Middleware, authz.Middleware, session.CSRFExcept(auth.LoginPath))
			g.Go(func() error { return sessions.Run(gctx) })

			g.Go(func() error { return server.New(cfg, log, opts).Run(gctx) })
			return g.Wait()
		},
	}
}

func newVersionCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := version.Get()
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "dcdash %s (commit %s, built %s, %s)\n",
				v.Version, v.Commit, v.Date, v.GoVersion)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func newConfigCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration utilities",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate the configuration file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, warnings, err := config.Load(*configPath)
			for _, w := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "Warning:", w)
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: configuration is valid\n", *configPath)
			return err
		},
	})
	return cmd
}

func newGenKeyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "gen-key",
		Short: "Generate a random encryption key for security.encryption_key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), vault.GenerateKey())
			return err
		},
	}
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

// newAuthService creates the session manager and the authentication service.
func newAuthService(cfg *config.Config, st *store.Store, authz *auth.Authorizer, log zerolog.Logger) (*auth.Service, *session.Manager, error) {
	a := cfg.Auth
	sessions := session.NewManager(st.DB(), a.Session.Lifetime.D(), a.Session.IdleTimeout.D(), log)
	proxies, err := cfg.Server.TrustedProxyPrefixes()
	if err != nil {
		return nil, nil, err
	}
	opts := auth.Options{
		Sessions:       sessions,
		Authorizer:     authz,
		UserLockout:    lockoutPolicy(a.Lockout.User),
		IPLockout:      lockoutPolicy(a.Lockout.IP),
		TrustedProxies: proxies,
		Log:            log,
	}
	if a.Local.Enabled {
		local, err := auth.NewLocalAccount(a.Local.Username, a.Local.PasswordHash.Value())
		if err != nil {
			return nil, nil, err
		}
		opts.Local = local
		log.Warn().Str("username", a.Local.Username).Msg("break-glass account enabled (always requires TOTP)")
	}
	if a.LDAP.URL != "" {
		client, err := ldapauth.New(a.LDAP, log)
		if err != nil {
			return nil, nil, err
		}
		opts.LDAP = client
		log.Info().Str("url", a.LDAP.URL).Str("mode", a.LDAP.Mode).Msg("LDAP authentication enabled")
	} else {
		log.Warn().Msg("auth.ldap.url is not set: LDAP login is disabled")
	}
	return auth.New(opts), sessions, nil
}

func lockoutPolicy(p config.LockoutPolicy) lockout.Policy {
	return lockout.Policy{MaxFailures: p.MaxFailures, Window: p.Window.D(), Duration: p.Duration.D()}
}

func newHashPasswordCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "hash-password",
		Short: "Create an argon2id hash for auth.local.password_hash",
		Long: "Reads a password (from the terminal without echo, or the first line of stdin)\n" +
			"and prints its argon2id hash for the break-glass account.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			password, err := readPassword(cmd)
			if err != nil {
				return err
			}
			hash, err := auth.HashPassword(password)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), hash)
			return err
		},
	}
}

// minPasswordLength for the break-glass account, which bypasses the directory.
const minPasswordLength = 16

func readPassword(cmd *cobra.Command) (string, error) {
	var password string
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) { // #nosec G115 -- file descriptors fit into int
		fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
		b, err := term.ReadPassword(int(f.Fd())) // #nosec G115 -- file descriptors fit into int
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		fmt.Fprint(cmd.ErrOrStderr(), "Repeat password: ")
		again, err := term.ReadPassword(int(f.Fd())) // #nosec G115 -- file descriptors fit into int
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		if string(b) != string(again) {
			return "", errors.New("passwords do not match")
		}
		password = string(b)
	} else {
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		password = strings.TrimRight(line, "\r\n")
	}
	if len(password) < minPasswordLength {
		return "", fmt.Errorf("the password must have at least %d characters", minPasswordLength)
	}
	return password, nil
}
