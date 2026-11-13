// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads, defaults and validates the dcdash YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/joernott/dcdash/internal/vault"
)

// DefaultPath is used when neither --config nor DCDASH_CONFIG is given.
const DefaultPath = "/etc/dcdash/config.yaml"

// TLS modes.
const (
	TLSModeFiles = "files" // certificate and key from PEM files
	TLSModeNone  = "none"  // plain HTTP, only for development or behind a TLS-terminating proxy
)

// Config is the root of the configuration file.
type Config struct {
	Server   Server   `yaml:"server"`
	Auth     Auth     `yaml:"auth"`
	Roles    []Role   `yaml:"roles"`
	Audit    Audit    `yaml:"audit"`
	Security Security `yaml:"security"`
	Log      Log      `yaml:"log"`
	DataDir  string   `yaml:"data_dir"`
}

// Security configures the protection of data at rest.
type Security struct {
	// EncryptionKey (base64, 32 bytes) encrypts secrets stored in the database,
	// e.g. TOTP seeds. Generate one with `dcdash gen-key`.
	EncryptionKey Secret `yaml:"encryption_key"`
	// PreviousEncryptionKeys are still accepted for decryption after a key rotation.
	PreviousEncryptionKeys []Secret `yaml:"previous_encryption_keys"`
}

// Server configures the HTTP(S) listener.
type Server struct {
	Listen string `yaml:"listen"`
	// RedirectListen is an optional plain HTTP listener (e.g. ":80") that
	// redirects every request to HTTPS. Empty disables it.
	RedirectListen string `yaml:"redirect_listen"`
	// TrustedProxies are reverse proxies (IP addresses or CIDR ranges) whose
	// X-Forwarded-For header is used to determine the client IP address.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// HSTSMaxAge is the max-age of the Strict-Transport-Security header sent
	// over HTTPS (0 disables the header).
	HSTSMaxAge        Duration `yaml:"hsts_max_age"`
	TLS               TLS      `yaml:"tls"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	ShutdownTimeout   Duration `yaml:"shutdown_timeout"`
}

// TLS configures how the server obtains its certificate.
type TLS struct {
	Mode     string `yaml:"mode"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// ReloadInterval is how often cert_file and key_file are checked for changes (0 disables).
	ReloadInterval Duration `yaml:"reload_interval"`
	// ACME is used in mode "acme".
	ACME ACME `yaml:"acme"`
}

// Log configures logging.
type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Default returns a configuration with all defaults applied.
func Default() Config {
	return Config{
		Server: Server{
			Listen:            ":8443",
			HSTSMaxAge:        Duration(180 * 24 * time.Hour),
			TLS:               TLS{Mode: TLSModeFiles, ReloadInterval: Duration(time.Minute), ACME: defaultACME()},
			ReadHeaderTimeout: Duration(10 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
		},
		Auth:    Auth{LDAP: defaultLDAP(), Session: defaultSession(), Lockout: defaultLockout(), TOTP: TOTP{Issuer: "dcdash"}},
		Audit:   defaultAudit(),
		Log:     Log{Level: "info", Format: "json"},
		DataDir: "/var/lib/dcdash",
	}
}

// Load reads and validates the configuration file at path.
// Warnings (e.g. insecure file permissions) are returned separately and do not fail loading.
func Load(path string) (*Config, []string, error) {
	f, err := os.Open(path) // #nosec G304 -- path is given by the administrator
	if err != nil {
		return nil, nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var warnings []string
	if w := checkPermissions(f); w != "" {
		warnings = append(warnings, w)
	}

	cfg, err := Parse(f)
	if err != nil {
		return nil, warnings, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.Auth.LDAP.URL == "" && !cfg.Auth.Local.Enabled {
		warnings = append(warnings, "no login method configured (auth.ldap.url or auth.local): nobody can log in")
	}
	if cfg.Server.TLS.Mode == TLSModeNone {
		warnings = append(warnings, "server.tls.mode is \"none\": serving plain HTTP, use only for development or behind a TLS-terminating proxy")
	}
	return cfg, warnings, nil
}

// Parse decodes a configuration from r, applies defaults and validates it.
// Unknown keys are rejected to catch typos early.
func Parse(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Auth.LDAP.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks the configuration and returns all problems found, joined.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		add("server.listen %q: must be host:port", c.Server.Listen)
	}
	if c.Server.RedirectListen != "" {
		if _, _, err := net.SplitHostPort(c.Server.RedirectListen); err != nil {
			add("server.redirect_listen %q: must be host:port", c.Server.RedirectListen)
		}
		if c.Server.TLS.Mode == TLSModeNone {
			add("server.redirect_listen: requires TLS (server.tls.mode is %q)", TLSModeNone)
		}
	}
	if _, err := c.Server.TrustedProxyPrefixes(); err != nil {
		add("server.trusted_proxies: %v", err)
	}
	if c.Server.HSTSMaxAge < 0 {
		add("server.hsts_max_age: must not be negative")
	}
	switch c.Server.TLS.Mode {
	case TLSModeFiles:
		if c.Server.TLS.CertFile == "" {
			add("server.tls.cert_file: required for mode %q", TLSModeFiles)
		}
		if c.Server.TLS.KeyFile == "" {
			add("server.tls.key_file: required for mode %q", TLSModeFiles)
		}
		if c.Server.TLS.ReloadInterval < 0 {
			add("server.tls.reload_interval: must not be negative")
		}
	case TLSModeACME:
		c.Server.TLS.ACME.validate(add)
	case TLSModeNone:
	default:
		add("server.tls.mode %q: must be one of %q, %q, %q", c.Server.TLS.Mode, TLSModeFiles, TLSModeACME, TLSModeNone)
	}
	if c.Server.ReadHeaderTimeout <= 0 {
		add("server.read_header_timeout: must be positive")
	}
	if c.Server.ShutdownTimeout <= 0 {
		add("server.shutdown_timeout: must be positive")
	}
	if !slices.Contains([]string{"trace", "debug", "info", "warn", "error"}, c.Log.Level) {
		add("log.level %q: must be one of trace, debug, info, warn, error", c.Log.Level)
	}
	if !slices.Contains([]string{"json", "console"}, c.Log.Format) {
		add("log.format %q: must be json or console", c.Log.Format)
	}
	if c.DataDir == "" {
		add("data_dir: required")
	}
	if c.Auth.LDAP.URL != "" {
		c.Auth.LDAP.validate(add)
	}
	c.Auth.Session.validate(add)
	c.Auth.Lockout.User.validate("auth.lockout.user", add)
	c.Auth.Lockout.IP.validate("auth.lockout.ip", add)
	c.Auth.Lockout.TOTP.validate("auth.lockout.totp", add)
	if strings.TrimSpace(c.Auth.TOTP.Issuer) == "" || strings.Contains(c.Auth.TOTP.Issuer, ":") {
		add("auth.totp.issuer: required, without \":\"")
	}
	c.Auth.Local.validate(add)
	validateRoles(c.Roles, add)
	c.Audit.validate(add)
	if !c.Security.EncryptionKey.IsSet() {
		add("security.encryption_key: required (generate one with `dcdash gen-key`)")
	} else if _, err := vault.ParseKey(c.Security.EncryptionKey.Value()); err != nil {
		add("security.encryption_key: %v", err)
	}
	for i, k := range c.Security.PreviousEncryptionKeys {
		if _, err := vault.ParseKey(k.Value()); err != nil {
			add("security.previous_encryption_keys[%d]: %v", i, err)
		}
	}
	return errors.Join(errs...)
}

// checkPermissions warns when the config file is readable by other users,
// because it may contain secrets.
func checkPermissions(f *os.File) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	if fi.Mode().Perm()&0o007 != 0 {
		return fmt.Sprintf("config file %s is accessible by other users (mode %04o); consider chmod 0640", f.Name(), fi.Mode().Perm())
	}
	return ""
}

// TrustedProxyPrefixes parses server.trusted_proxies; single addresses become
// host prefixes (/32 or /128).
func (s Server) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(s.TrustedProxies))
	for _, p := range s.TrustedProxies {
		if pfx, err := netip.ParsePrefix(p); err == nil {
			out = append(out, pfx.Masked())
			continue
		}
		a, err := netip.ParseAddr(p)
		if err != nil {
			return nil, fmt.Errorf("%q is neither an IP address nor a CIDR range", p)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}
