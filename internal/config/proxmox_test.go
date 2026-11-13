// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestProxmoxConfig(t *testing.T) {
	t.Setenv("PVE_TOKEN", "secret-token")
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
proxmox:
  clusters:
    - name: lab
      endpoints:
        - url: https://pve1.example.com:8006
        - url: https://pve2.example.com:8006
          fingerprint: "B2:F0:61:C6:80:64:B8:C1:6C:70:5B:ED:29:67:81:BE:22:F2:70:00:FA:BF:E5:A5:67:04:E6:3A:52:4E:40:24"
      tls: {ca_file: /etc/dcdash/pve-root-ca.pem}
      token_id: "dcdash@pve!dcdash"
      token_secret: "env:PVE_TOKEN"
    - name: prod-1
      endpoints: [{url: "https://10.0.0.1:8006"}]
      token_id: "dcdash@pam!api"
      token_secret: "x"
      timeout: 30s
      console: {username: "dcdash-console@pve", password: "pw"}
`))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Proxmox.Clusters
	if len(c) != 2 || c[0].TokenSecret.Value() != "secret-token" || c[0].RequestTimeout() != DefaultProxmoxTimeout ||
		c[1].RequestTimeout() != 30*time.Second || c[1].Console.Password.Value() != "pw" {
		t.Errorf("clusters: %+v", c)
	}
	if got := c[0].TLSFor(c[0].Endpoints[0]); got.CAFile != "/etc/dcdash/pve-root-ca.pem" || got.Fingerprint != "" {
		t.Errorf("endpoint 0 TLS: %+v", got)
	}
	if got := c[0].TLSFor(c[0].Endpoints[1]); got.CAFile == "" || !strings.HasPrefix(got.Fingerprint, "B2:F0") {
		t.Errorf("endpoint 1 TLS: %+v", got)
	}
}

func TestProxmoxConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
proxmox:
  clusters:
    - name: "Lab:1"
      endpoints:
        - url: http://pve1.example.com:8006
        - url: https://pve2.example.com:8006/api2/json
          fingerprint: abc
      token_id: "dcdash@pve"
      timeout: -1s
      console: {username: dcdash}
    - name: dup
      endpoints: []
      token_id: "a@pve!b"
      token_secret: s
    - name: dup
      endpoints: [{url: "https://x"}]
      token_id: "a@pve!b"
      token_secret: s
`))
	for _, want := range []string{
		`proxmox.clusters[0].name "Lab:1"`,
		`proxmox.clusters[0].endpoints[0].url "http://pve1.example.com:8006"`,
		`proxmox.clusters[0].endpoints[1].url`,
		`proxmox.clusters[0].endpoints[1].fingerprint`,
		`proxmox.clusters[0].token_id "dcdash@pve"`,
		`proxmox.clusters[0].token_secret: required`,
		`proxmox.clusters[0].timeout`,
		`proxmox.clusters[0].console.username "dcdash"`,
		`proxmox.clusters[0].console.password`,
		`proxmox.clusters[1].endpoints: at least one`,
		`proxmox.clusters[2].name "dup": duplicate`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}
