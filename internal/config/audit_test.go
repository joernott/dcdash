// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestAuditConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
audit:
  retention: 365d
  file: /var/log/dcdash/audit.log
  syslog: {enabled: true, network: udp, address: "log.example.com:514"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Retention.D() != 365*24*time.Hour || cfg.Audit.Syslog.Tag != "dcdash" {
		t.Errorf("audit: %+v", cfg.Audit)
	}
	_, err = Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
audit:
  retention: -1h
  file: audit.log
  syslog: {enabled: true, network: http, address: ""}
`))
	for _, want := range []string{"audit.retention", "audit.file: must be an absolute path", `audit.syslog.network "http"`, "network and address"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestDurationDays(t *testing.T) {
	for in, want := range map[string]time.Duration{"0": 0, "7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "1h30m": 90 * time.Minute} {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("%s: %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"d", "-1d", "1.5d", "999999d"} {
		if _, err := parseDuration(in); err == nil {
			t.Errorf("%s accepted", in)
		}
	}
}
