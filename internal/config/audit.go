// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"path/filepath"
	"slices"
)

// Audit configures the audit log. Entries are always stored in the database;
// file and syslog are optional additional outputs.
type Audit struct {
	// Retention deletes entries older than this from the database (0 keeps them forever).
	Retention Duration `yaml:"retention"`
	// File appends every entry as a JSON line to this file (empty disables it).
	File   string      `yaml:"file"`
	Syslog AuditSyslog `yaml:"syslog"`
}

// AuditSyslog sends audit entries to syslog (facility authpriv).
type AuditSyslog struct {
	Enabled bool `yaml:"enabled"`
	// Network is "" (local syslog daemon), "udp", "tcp" or "unix".
	Network string `yaml:"network"`
	// Address of the syslog server (host:port, or a socket path for "unix").
	Address string `yaml:"address"`
	Tag     string `yaml:"tag"`
}

func defaultAudit() Audit {
	return Audit{Syslog: AuditSyslog{Tag: "dcdash"}}
}

func (a Audit) validate(add func(string, ...any)) {
	if a.Retention < 0 {
		add("audit.retention: must not be negative")
	}
	if a.File != "" && !filepath.IsAbs(a.File) {
		add("audit.file: must be an absolute path")
	}
	if !a.Syslog.Enabled {
		return
	}
	if !slices.Contains([]string{"", "udp", "tcp", "unix"}, a.Syslog.Network) {
		add("audit.syslog.network %q: must be empty (local), udp, tcp or unix", a.Syslog.Network)
	}
	if (a.Syslog.Network == "") != (a.Syslog.Address == "") {
		add("audit.syslog: network and address must both be set, or both be empty for the local syslog daemon")
	}
}
