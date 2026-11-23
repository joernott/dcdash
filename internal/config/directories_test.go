// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDirectoriesConfig(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
directories:
  - name: lab1
    type: samba
    domain: Ad.Example.com
    servers:
      - url: ldaps://dc1.ad.example.com
        fingerprint: "11:37:4E:47:15:7E:67:C3:3E:5D:B8:74:70:10:01:5C:EF:CB:89:88:16:45:85:86:BB:FB:7B:43:74:3F:DA:F4"
      - url: ldaps://dc2.ad.example.com:3269
    bind_dn: svc-dcdash@ad.example.com
    bind_password: "secret"
  - name: corp
    type: windows
    domain: corp.example.net
    servers: [{url: "ldaps://10.0.0.1"}]
    tls: {ca_file: /etc/dcdash/corp-ca.pem}
    bind_dn: CN=dcdash,OU=Service,DC=corp,DC=example,DC=net
    bind_password: "secret2"
    managed_ous: ["OU=Lab,DC=corp,DC=example,DC=net"]
    protected_users: [backup]
    kerberos: {realm: CORP.EXAMPLE.NET, kdcs: [dc1.corp.example.net, "10.0.0.2:8888"]}
    self_service: false
    timeout: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Directories
	if len(d) != 2 {
		t.Fatalf("directories: %+v", d)
	}
	lab, corp := d[0], d[1]
	if lab.BaseDN != "DC=ad,DC=example,DC=com" || !slices.Equal(lab.ManagedOUs, []string{lab.BaseDN}) ||
		lab.Kerberos.Realm != "AD.EXAMPLE.COM" ||
		!slices.Equal(lab.Kerberos.KDCs, []string{"dc1.ad.example.com:88", "dc2.ad.example.com:88"}) ||
		!lab.SelfServiceEnabled() || lab.RequestTimeout() != DefaultDirectoryTimeout {
		t.Errorf("lab defaults: %+v", lab)
	}
	if lab.TLSFor(lab.Servers[0]).Fingerprint == "" || lab.TLSFor(lab.Servers[1]).Fingerprint != "" {
		t.Errorf("per-server fingerprint not applied")
	}
	if corp.BaseDN != "DC=corp,DC=example,DC=net" || corp.SelfServiceEnabled() || corp.RequestTimeout() != 5*time.Second ||
		!slices.Equal(corp.Kerberos.KDCs, []string{"dc1.corp.example.net:88", "10.0.0.2:8888"}) {
		t.Errorf("corp: %+v", corp)
	}
	for dn, want := range map[string]bool{
		"CN=x,OU=Lab,DC=corp,DC=example,DC=net":      true,
		"ou=lab,dc=corp,dc=example,dc=net":           true,
		"CN=x,OU=Other,DC=corp,DC=example,DC=net":    false,
		"CN=x,OU=NotLab,DC=corp,DC=example,DC=net":   false,
		"CN=x,OU=Lab,DC=corp,DC=example,DC=net,DC=x": false,
	} {
		if corp.Managed(dn) != want {
			t.Errorf("Managed(%q) = %v", dn, !want)
		}
	}
}

func TestDirectoriesConfigErrors(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
directories:
  - name: "Lab 1"
    type: openldap
    domain: localhost
    base_dn: ou=x
    timeout: -1s
  - name: lab2
    type: samba
    domain: ad.example.com
    servers: [{url: "ldap://dc1.ad.example.com"}, {url: "ldaps://dc2", fingerprint: "zz"}]
    bind_dn: svc
    bind_password: s
    managed_ous: ["OU=x,DC=other,DC=com"]
    protected_groups: [""]
    kerberos: {kdcs: ["dc1:"]}
  - name: lab2
    type: windows
    domain: ad2.example.com
    servers: [{url: "ldaps://dc1"}]
    bind_dn: svc
    bind_password: s
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`directories[0].name "Lab 1"`, `directories[0].type "openldap"`, `directories[0].domain "localhost"`,
		`directories[0].servers: required`, `directories[0].bind_dn and bind_password: required`,
		`directories[0].base_dn "ou=x"`, `directories[0].timeout`,
		`directories[1].servers[0].url "ldap://dc1.ad.example.com": must be ldaps`,
		`directories[1].servers[1].fingerprint`, `directories[1].managed_ous[0]`,
		`directories[1].protected_groups[0]: empty`, `directories[1].kerberos.kdcs[0] "dc1:"`,
		`directories[2].name "lab2": duplicate`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in\n%v", want, err)
		}
	}
}
