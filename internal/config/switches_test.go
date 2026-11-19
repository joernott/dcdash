// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestSwitchesConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
switches:
  - name: sw1
    url: https://10.0.0.1
    tls: {fingerprint: "11:37:4E:47:15:7E:67:C3:3E:5D:B8:74:70:10:01:5C:EF:CB:89:88:16:45:85:86:BB:FB:7B:43:74:3F:DA:F4"}
    username: dcdash
    password: "pw"
  - name: lab
    type: aoscx
    url: http://127.0.0.1:8443
    insecure: true
    username: dcdash
    password: "pw2"
    api_version: v10.08
    timeout: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Switches
	if len(s) != 2 || s[0].Type != SwitchTypeAOSCX || s[0].RequestTimeout() != DefaultSwitchTimeout ||
		s[1].RequestTimeout() != 5*time.Second || s[1].Password.Value() != "pw2" || s[1].APIVersion != "v10.08" {
		t.Errorf("switches: %+v", s)
	}
}

func TestSwitchesConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
switches:
  - name: "SW:1"
    type: procurve
    url: http://sw1.example.com/rest
    tls: {fingerprint: "zz"}
    api_version: "10.08"
    timeout: -1s
  - name: sw2
    url: http://sw2.example.com
    username: dcdash
    password: pw
  - name: sw2
    url: https://sw3.example.com
    username: dcdash
    password: pw
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`switches[0].name "SW:1"`, `switches[0].type "procurve"`, `switches[0].url`, `switches[0].tls.fingerprint`,
		`switches[0].username: required`, `switches[0].password: required`, `switches[0].api_version "10.08"`,
		`switches[0].timeout`, `switches[1].url "http://sw2.example.com": http sends`, `switches[2].name "sw2": duplicate`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}
}
