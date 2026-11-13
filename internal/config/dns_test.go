// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
	"time"
)

func TestDNSConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
dns:
  providers:
    - name: cloudflare-prod
      type: cloudflare
      cloudflare: {api_token: "cf-token", account_id: "abc"}
    - name: bind-internal
      type: bind
      timeout: 5s
      bind:
        nameserver: ns1.example.com
        tsig_key: dcdash
        tsig_secret: "c2VjcmV0"
        catalog_zone: catalog.example.com
        rndc: {binary: /usr/sbin/rndc, server: ns1.example.com, key_file: /etc/dcdash/rndc.key}
        sftp:
          address: ns1.example.com
          user: dcdash
          private_key_file: /etc/dcdash/bind.key
          host_key: "ssh-ed25519 AAAA"
          zone_dir: /zones
        new_zones:
          file_dir: /var/lib/bind/dyn
          soa: {mname: ns1.example.com, rname: hostmaster.example.com}
          nameservers: [ns1.example.com, ns2.example.com]
    - name: bind-ro
      type: bind
      bind: {nameserver: "10.0.0.1:5353", tsig_key: k, tsig_secret: s, zones: [example.org]}
`))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.DNS.Providers
	if len(p) != 3 || p[0].Cloudflare.APIToken.Value() != "cf-token" || p[0].RequestTimeout() != DefaultDNSTimeout {
		t.Fatalf("providers: %+v", p)
	}
	b := p[1].Bind
	if p[1].RequestTimeout() != 5*time.Second || b.TSIGAlgorithm != "hmac-sha256" || b.RNDC.Port != 953 ||
		!b.ZoneCreation() || b.NewZones.TTL.D() != time.Hour || b.NewZones.SOA.Minimum.D() != 5*time.Minute {
		t.Errorf("bind: %+v", b)
	}
	if got := b.ZoneOptions(); got != "update-policy { grant dcdash. zonesub ANY; }; allow-transfer { key dcdash.; };" {
		t.Errorf("zone options %q", got)
	}
	if p[2].Bind.ZoneCreation() {
		t.Error("zone creation without rndc and sftp")
	}
}

func TestDNSConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
dns:
  providers:
    - name: "CF:1"
      type: cloudflare
      cloudflare: {base_url: "ftp://x"}
    - name: b
      type: bind
      bind:
        tsig_algorithm: md5
        zones: ["bad zone", "*.example.org"]
        rndc: {binary: rndc, server: ns1, key_file: /k, port: 70000}
        sftp: {address: ns1, user: u, private_key_file: /k, zone_dir: zones}
        new_zones: {file_dir: dyn, soa: {mname: "", rname: x}, options: "a\nb"}
    - name: b
      type: powerdns
`))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		`dns.providers[0].name "CF:1"`, "dns.providers[0].cloudflare.api_token: required",
		"base_url", "dns.providers[1].bind.nameserver: required", "tsig_key and tsig_secret: required",
		`tsig_algorithm "md5"`, `invalid zone "bad zone"`, `invalid zone "*.example.org"`,
		`rndc.binary "rndc"`, "rndc.port 70000", "exactly one of host_key and known_hosts_file",
		`sftp.zone_dir "zones"`, `new_zones.file_dir "dyn"`, "soa: mname and rname",
		"nameservers: at least one", "options: must be a single line",
		`dns.providers[2].name "b": duplicate`, `type "powerdns"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}
