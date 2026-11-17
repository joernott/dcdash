// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestMetricsConfig(t *testing.T) {
	for _, tc := range []struct {
		yaml, err string
	}{
		{"metrics: {enabled: false}", ""},
		{"metrics: {enabled: true, listen: '127.0.0.1:9100'}", ""},
		{"metrics: {enabled: true, token: 'a-long-enough-token'}", ""},
		{"metrics: {enabled: true}", "metrics.token: required"},
		{"metrics: {enabled: true, listen: '127.0.0.1:9100', token: short}", "at least 16"},
		{"metrics: {enabled: true, listen: nowhere}", "metrics.listen"},
	} {
		_, err := Parse(strings.NewReader(testKeyYAML + "server: {tls: {mode: none}}\n" + tc.yaml + "\n"))
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v", tc.yaml, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: error %v, want %q", tc.yaml, err, tc.err)
		}
	}
}
