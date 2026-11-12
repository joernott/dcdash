// SPDX-License-Identifier: GPL-3.0-or-later

// Package config loads, defaults and validates the dcdash YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
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
	Server  Server `yaml:"server"`
	Log     Log    `yaml:"log"`
	DataDir string `yaml:"data_dir"`
}

// Server configures the HTTP(S) listener.
type Server struct {
	Listen            string   `yaml:"listen"`
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
			TLS:               TLS{Mode: TLSModeFiles, ReloadInterval: Duration(time.Minute), ACME: defaultACME()},
			ReadHeaderTimeout: Duration(10 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
		},
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
