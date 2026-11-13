// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/joernott/dcdash/internal/rbac"
)

// Role maps directory groups to permissions.
type Role struct {
	Name string `yaml:"name"`
	// Groups are directory groups by CN ("dcdash-dns") or DN ("CN=dcdash-dns,OU=...").
	Groups []string `yaml:"groups"`
	// RequireTOTP (default true): members must use TOTP. If any role of a user
	// requires TOTP, the user must use it.
	RequireTOTP *bool `yaml:"require_totp"`
	// Permissions in the form module:instance:resource:action.
	Permissions []string `yaml:"permissions"`
}

// TOTPRequired returns require_totp with its default applied.
func (r Role) TOTPRequired() bool { return r.RequireTOTP == nil || *r.RequireTOTP }

var roleNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func validateRoles(roles []Role, add func(string, ...any)) {
	seen := map[string]bool{}
	for i, r := range roles {
		p := "roles[" + strconv.Itoa(i) + "]"
		switch {
		case !roleNamePattern.MatchString(r.Name):
			add("%s.name %q: lower-case letters, digits, '-' and '_' (max. 64)", p, r.Name)
		case r.Name == rbac.RootRole:
			add("%s.name: %q is built in; set auth.root_group to change its group", p, r.Name)
		case seen[r.Name]:
			add("%s.name %q: duplicate", p, r.Name)
		}
		seen[r.Name] = true
		if len(r.Groups) == 0 {
			add("%s.groups: at least one group required", p)
		}
		for j, g := range r.Groups {
			if strings.TrimSpace(g) == "" {
				add("%s.groups[%d]: empty", p, j)
			}
		}
		if _, err := rbac.ParseSet(r.Permissions); err != nil {
			add("%s.permissions: %v", p, err)
		}
	}
}

// RBACRoles converts the configured roles. The configuration must be valid.
func (c *Config) RBACRoles() []rbac.Role {
	out := make([]rbac.Role, 0, len(c.Roles))
	for _, r := range c.Roles {
		perms, _ := rbac.ParseSet(r.Permissions)
		out = append(out, rbac.Role{Name: r.Name, Groups: r.Groups, RequireTOTP: r.TOTPRequired(), Permissions: perms})
	}
	return out
}
