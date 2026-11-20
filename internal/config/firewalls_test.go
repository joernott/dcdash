// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestFirewallsConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
firewalls:
  - name: fw1
    url: https://fw1.example.com
    tls: {fingerprint: "11:37:4E:47:15:7E:67:C3:3E:5D:B8:74:70:10:01:5C:EF:CB:89:88:16:45:85:86:BB:FB:7B:43:74:3F:DA:F4"}
    api_key: key1
    api_secret: "secret1"
  - name: lab
    type: opnsense
    url: http://127.0.0.1:8443
    insecure: true
    api_key: key2
    api_secret: "secret2"
    timeout: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	f := cfg.Firewalls
	if len(f) != 2 || f[0].Type != FirewallTypeOPNsense || f[0].RequestTimeout() != DefaultFirewallTimeout ||
		f[1].RequestTimeout() != 5*time.Second || f[1].APISecret.Value() != "secret2" {
		t.Errorf("firewalls: %+v", f)
	}
}

func TestFirewallsConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
firewalls:
  - name: "FW:1"
    type: pfsense
    url: https://fw1.example.com/api
    timeout: -1s
  - name: fw2
    url: http://fw2.example.com
    api_key: k
    api_secret: s
  - name: fw2
    url: https://fw3.example.com
    api_key: k
    api_secret: s
    protected_interfaces: [opt1, "bad key"]
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`firewalls[0].name "FW:1"`, `firewalls[0].type "pfsense"`, `firewalls[0].url`, `firewalls[0].api_key: required`,
		`firewalls[0].api_secret: required`, `firewalls[0].timeout`, `firewalls[1].url "http://fw2.example.com": http sends`,
		`firewalls[2].name "fw2": duplicate`, `firewalls[2].protected_interfaces[1] "bad key"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}
}
