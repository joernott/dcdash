// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestLinksValidate(t *testing.T) {
	for _, c := range []struct {
		links Links
		bad   string // part of the expected error, "" for valid
	}{
		{Links{}, ""},
		{Links{UI: "https://pve.example.com:8006/", SSH: "root@pve1.example.com", RDP: "dc1.example.com"}, ""},
		{Links{SSH: "pve1:2222", RDP: "10.0.0.5:3389"}, ""},
		{Links{SSH: "admin@[2001:db8::1]:22", RDP: "2001:db8::5"}, ""},
		{Links{UI: "ftp://x.example.com"}, "ui"},
		{Links{UI: "https://user:pw@x.example.com"}, "ui"},
		{Links{UI: "pve.example.com"}, "ui"},
		{Links{SSH: "root@"}, "ssh"},
		{Links{SSH: "bad user@host"}, "ssh"},
		{Links{SSH: "host:99999"}, "ssh"},
		{Links{RDP: "host name"}, "rdp"},
		{Links{RDP: "dc1:"}, "rdp"},
	} {
		var errs []string
		validateLinks(c.links, "x.", func(f string, a ...any) { errs = append(errs, f) })
		switch {
		case c.bad == "" && len(errs) > 0:
			t.Errorf("%+v: unexpected %v", c.links, errs)
		case c.bad != "" && (len(errs) != 1 || !strings.Contains(errs[0], c.bad)):
			t.Errorf("%+v: want an error for %s, got %v", c.links, c.bad, errs)
		}
	}
	if got := (Links{SSH: "root@pve1"}).SSHURL(); got != "ssh://root@pve1" {
		t.Errorf("SSHURL: %s", got)
	}
}

func TestLinksInConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
switches:
  - name: sw1
    url: https://sw1.example.com
    username: u
    password: p
    ui: https://sw1.example.com
    ssh: admin@sw1.example.com
`))
	if err != nil {
		t.Fatal(err)
	}
	if l := cfg.Switches[0].Links; l.UI != "https://sw1.example.com" || l.SSH != "admin@sw1.example.com" {
		t.Errorf("%+v", l)
	}
	_, err = Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
switches:
  - name: sw1
    url: https://sw1.example.com
    username: u
    password: p
    rdp: "not valid"
`))
	if err == nil || !strings.Contains(err.Error(), `switches[0].rdp "not valid"`) {
		t.Errorf("want rdp error: %v", err)
	}
}
