// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestDHCPConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
dhcp:
  servers:
    - name: kea4-a
      family: v4
      url: https://kea1.example.com:8000
      tls: {ca_file: /etc/dcdash/kea-ca.pem, cert_file: /etc/dcdash/kea-client.pem, key_file: /etc/dcdash/kea-client.key}
      username: dcdash
      password: "pw"
      ha_group: site1
    - name: kea4-b
      family: v4
      url: https://kea2.example.com:8000
      username: dcdash
      password: "pw"
      ha_group: site1
    - name: kea6-lab
      family: v6
      url: http://127.0.0.1:8001
      insecure: true
      username: dcdash
      password: "pw6"
      timeout: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.DHCP.Servers
	if len(s) != 3 || s[0].TLS.CertFile == "" || s[0].RequestTimeout() != DefaultDHCPTimeout ||
		s[2].RequestTimeout() != 5*time.Second || s[2].Password.Value() != "pw6" || s[1].HAGroup != "site1" {
		t.Errorf("servers: %+v", s)
	}
}

func TestDHCPConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
dhcp:
  servers:
    - name: "Kea:1"
      family: v5
      url: http://kea1.example.com:8000
      tls: {cert_file: /etc/dcdash/kea-client.pem}
      timeout: -1s
      ha_group: site1
    - name: kea6
      family: v6
      url: https://kea2.example.com:8000/api
      username: dcdash
      password: "pw"
      ha_group: site1
    - name: kea6
      family: v6
      url: https://kea3.example.com:8000
      username: dcdash
      password: "pw"
      ha_group: "Site 2"
`))
	if err == nil {
		t.Fatal("invalid DHCP configuration accepted")
	}
	for _, want := range []string{
		`dhcp.servers[0].name "Kea:1"`,
		`dhcp.servers[0].family "v5"`,
		`dhcp.servers[0].url "http://kea1.example.com:8000": http sends the password in clear text`,
		"dhcp.servers[0].tls.cert_file and key_file: set both or neither",
		"dhcp.servers[0].username: required",
		"dhcp.servers[0].password: required",
		"dhcp.servers[0].timeout: must not be negative",
		`dhcp.servers[1].url "https://kea2.example.com:8000/api": must be https://host[:port] without a path`,
		`dhcp.servers[1].ha_group "site1": all servers of a group must have the same family`,
		`dhcp.servers[2].name "kea6": duplicate`,
		`dhcp.servers[2].ha_group "Site 2"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
}
