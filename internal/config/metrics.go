// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "net"

// Metrics configures the optional Prometheus endpoint /metrics.
type Metrics struct {
	Enabled bool `yaml:"enabled"`
	// Listen is a separate plain HTTP listener for /metrics (e.g.
	// "127.0.0.1:9100"). If empty, /metrics is served on the main listener and
	// requires Token.
	Listen string `yaml:"listen"`
	// Token is the bearer token Prometheus must send
	// (Authorization: Bearer <token>). Optional on a separate listener.
	Token Secret `yaml:"token"`
}

// minMetricsToken is the minimum token length; shorter tokens can be guessed.
const minMetricsToken = 16

func (m Metrics) validate(add func(string, ...any)) {
	if !m.Enabled {
		return
	}
	if m.Listen != "" {
		if _, _, err := net.SplitHostPort(m.Listen); err != nil {
			add("metrics.listen %q: must be host:port", m.Listen)
		}
	} else if !m.Token.IsSet() {
		add("metrics.token: required when /metrics is served on the main listener (metrics.listen is empty)")
	}
	if m.Token.IsSet() && len(m.Token.Value()) < minMetricsToken {
		add("metrics.token: must be at least %d characters", minMetricsToken)
	}
}
