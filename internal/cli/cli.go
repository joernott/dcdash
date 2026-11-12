// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli implements the dcdash command line interface.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/joernott/dcdash/internal/certs"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/logging"
	"github.com/joernott/dcdash/internal/server"
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
