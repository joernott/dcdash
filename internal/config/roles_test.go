// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

func TestRoles(t *testing.T) {
	cfg, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
auth:
  root_group: dcdash-admins
roles:
  - name: dns-operator
    groups: [dcdash-dns]
    permissions: ["dns:cf:*:write"]
  - name: viewer
    groups: ["CN=dcdash-view,OU=Groups,DC=example,DC=com"]
    require_totp: false
    permissions: ["*:*:*:read"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.RootGroup != "dcdash-admins" {
		t.Errorf("root_group = %q", cfg.Auth.RootGroup)
	}
	roles := cfg.RBACRoles()
	if len(roles) != 2 || !roles[0].RequireTOTP || roles[1].RequireTOTP || len(roles[0].Permissions) != 1 {
		t.Errorf("roles: %+v", roles)
	}
}

func TestRoleValidation(t *testing.T) {
	_, err := Parse(strings.NewReader(testKeyYAML + `
server:
  tls: {mode: none}
roles:
  - name: root
    groups: [x]
  - name: Bad Name
    groups: [x]
  - name: ops
    groups: []
    permissions: ["dns:cf:write", "dns:cf:*:delete"]
  - name: ops
    groups: [" "]
`))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`roles[0].name: "root" is built in`,
		`roles[1].name "Bad Name"`,
		"roles[2].groups: at least one group",
		`roles[2].permissions: permission "dns:cf:write"`,
		`unknown action "delete"`,
		`roles[3].name "ops": duplicate`,
		"roles[3].groups[0]: empty",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in: %v", want, err)
		}
	}
}
