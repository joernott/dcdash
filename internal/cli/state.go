// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/user"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/joernott/dcdash/internal/audit"
	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/store"
	"github.com/joernott/dcdash/internal/totp"
	"github.com/joernott/dcdash/internal/vault"
)

// state gives administrative commands access to the database. SQLite allows
// using it while the server is running.
type state struct {
	cfg   *config.Config
	st    *store.Store
	kr    *vault.Keyring
	audit *audit.Log
	totp  *totp.Store
}

func openState(ctx context.Context, configPath string) (*state, error) {
	cfg, _, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	kr, err := newKeyring(ctx, cfg, st)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	var sinks []audit.Sink
	if cfg.Audit.File != "" {
		sinks = append(sinks, audit.NewFileSink(cfg.Audit.File))
	}
	return &state{
		cfg: cfg, st: st, kr: kr,
		audit: audit.New(st.DB(), zerolog.Nop(), sinks...),
		totp:  totp.NewStore(st.DB(), kr),
	}, nil
}

func (s *state) Close() error { return s.st.Close() }

// record writes an audit entry for a CLI action. The actor is the operating
// system user running the command.
func (s *state) record(ctx context.Context, action, target, result string, details map[string]any) {
	actor := "unknown"
	if u, err := user.Current(); err == nil {
		actor = u.Username
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor: actor, ActorSource: audit.SourceCLI, Action: action, Target: target, Result: result, Details: details,
	})
}

func newTOTPCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "totp",
		Short: "Manage TOTP (second login factor)",
		Long: "Manage TOTP enrollments. Run these commands as the user dcdash runs as\n" +
			"(e.g. sudo -u dcdash dcdash totp ...), so that the database stays accessible.",
	}
	cmd.AddCommand(newTOTPEnrollCommand(configPath), newTOTPResetCommand(configPath))
	return cmd
}

func newTOTPEnrollCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "enroll <username>",
		Short: "Enroll TOTP for the break-glass account",
		Long: "Enrolls TOTP for the break-glass account (auth.local). It cannot enroll in the\n" +
			"web interface, so that its password alone never grants access. Directory users\n" +
			"enroll at their first login.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openState(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer s.Close()
			local := s.cfg.Auth.Local
			if !local.Enabled || !strings.EqualFold(strings.TrimSpace(local.Username), strings.TrimSpace(args[0])) {
				return fmt.Errorf("%q is not the break-glass account (auth.local.username); directory users enroll at their first login", args[0])
			}
			name := strings.TrimSpace(local.Username)
			if ok, err := s.totp.Enrolled(cmd.Context(), name); err != nil || ok {
				if err == nil {
					err = errors.New("TOTP is already enrolled; reset it first with `dcdash totp reset`")
				}
				return err
			}
			secret := totp.NewSecret()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Add this account to your authenticator app:\n\n  %s\n\n", totp.URI(s.cfg.Auth.TOTP.Issuer, name, secret))
			fmt.Fprintf(out, "or enter the secret manually: %s\n", totp.EncodeSecret(secret))
			fmt.Fprintln(out, "To show the URI as QR code in the terminal, you can use: qrencode -t ansiutf8 '<uri>'")
			fmt.Fprint(cmd.ErrOrStderr(), "\nCode from the app: ")
			code, err := readLine(cmd.InOrStdin())
			if err != nil {
				return err
			}
			codes, err := s.totp.Enroll(cmd.Context(), name, secret, code)
			if err != nil {
				s.record(cmd.Context(), "auth.totp.enroll", name, audit.ResultFailure, map[string]any{"reason": err.Error()})
				return err
			}
			s.record(cmd.Context(), "auth.totp.enroll", name, audit.ResultSuccess, nil)
			fmt.Fprintln(out, "\nTOTP enrolled. Store these recovery codes in a safe place; each works once:")
			for _, c := range codes {
				fmt.Fprintln(out, "  "+c)
			}
			return nil
		},
	}
}

func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newReencryptCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reencrypt",
		Short: "Re-encrypt stored secrets with the current encryption key",
		Long: "After a key rotation, re-encrypts all secrets that are still encrypted with a\n" +
			"key from security.previous_encryption_keys. Afterwards the previous keys can be\n" +
			"removed from the configuration.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openState(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer s.Close()
			n, err := s.totp.ReencryptAll(cmd.Context())
			result := audit.ResultSuccess
			if err != nil {
				result = audit.ResultFailure
			}
			s.record(cmd.Context(), "security.reencrypt", "", result, map[string]any{"totp_secrets": n})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%d TOTP secret(s) re-encrypted; all secrets now use the current key\n", n)
			return err
		},
	}
}

func newTOTPResetCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset <username>",
		Short: "Reset the TOTP enrollment of a user",
		Long: "Removes the TOTP secret and recovery codes of a user, e.g. after the device was\n" +
			"lost. Directory users enroll again at their next login; the break-glass account\n" +
			"with `dcdash totp enroll`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openState(cmd.Context(), *configPath)
			if err != nil {
				return err
			}
			defer s.Close()
			name := strings.TrimSpace(args[0])
			ok, err := s.totp.Reset(cmd.Context(), name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("TOTP is not enrolled for %q", name)
			}
			s.record(cmd.Context(), "admin.totp.reset", name, audit.ResultSuccess, nil)
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "TOTP of %s reset\n", name)
			return err
		},
	}
}
