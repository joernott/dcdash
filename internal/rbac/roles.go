// SPDX-License-Identifier: GPL-3.0-or-later

package rbac

import (
	"slices"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// RootRole is the built-in role with all permissions. It always requires TOTP.
const RootRole = "root"

// DefaultRootGroup is the directory group that grants the root role.
const DefaultRootGroup = "dashroot"

// rootPermission is held implicitly by the root role.
var rootPermission = Permission{Module: "*", Instance: "*", Resource: "*", Action: ActionAdmin}

// Role maps directory groups to permissions.
type Role struct {
	Name string
	// Groups are given by CN (e.g. "dcdash-dns") or by DN (anything containing "=").
	Groups      []string
	RequireTOTP bool
	Permissions Set
}

// Subject is what a logged-in user may do, derived from their groups.
type Subject struct {
	Username    string
	Roles       []string
	Permissions Set
	// RequireTOTP is true if any role of the user requires TOTP.
	RequireTOTP bool
}

// IsRoot reports whether the subject holds the root role.
func (s *Subject) IsRoot() bool { return s != nil && slices.Contains(s.Roles, RootRole) }

// Can reports whether the subject holds a permission granting req.
func (s *Subject) Can(req Permission) bool { return s != nil && s.Permissions.Allows(req) }

// Resolver derives subjects from group memberships.
type Resolver struct {
	roles []Role // root first
}

// NewResolver creates a resolver. The root role is added for rootGroup
// (DefaultRootGroup if empty); it cannot be removed or weakened.
func NewResolver(rootGroup string, roles []Role) *Resolver {
	if strings.TrimSpace(rootGroup) == "" {
		rootGroup = DefaultRootGroup
	}
	all := []Role{{Name: RootRole, Groups: []string{rootGroup}, RequireTOTP: true, Permissions: Set{rootPermission}}}
	for _, r := range roles {
		if r.Name != RootRole {
			all = append(all, r)
		}
	}
	return &Resolver{roles: all}
}

// Resolve returns the subject for a user with the given group DNs. Users of
// the break-glass account (local) always get the root role.
func (r *Resolver) Resolve(username string, groups []string, local bool) *Subject {
	s := &Subject{Username: username, Roles: []string{}, Permissions: Set{}}
	parsed := parseGroups(groups)
	for _, role := range r.roles {
		if (role.Name != RootRole || !local) && !memberOfAny(parsed, role.Groups) {
			continue
		}
		s.Roles = append(s.Roles, role.Name)
		s.Permissions = append(s.Permissions, role.Permissions...)
		s.RequireTOTP = s.RequireTOTP || role.RequireTOTP
	}
	return s
}

// Roles returns the configured roles, root first.
func (r *Resolver) Roles() []Role { return slices.Clone(r.roles) }

type parsedGroup struct {
	dn   *ldap.DN
	name string // value of the first RDN
}

func parseGroups(dns []string) []parsedGroup {
	out := make([]parsedGroup, 0, len(dns))
	for _, s := range dns {
		dn, err := ldap.ParseDN(s)
		if err != nil || len(dn.RDNs) == 0 || len(dn.RDNs[0].Attributes) == 0 {
			continue
		}
		out = append(out, parsedGroup{dn: dn, name: dn.RDNs[0].Attributes[0].Value})
	}
	return out
}

// memberOfAny reports whether one of the user's groups matches one of the
// wanted groups, given by CN (case-insensitive) or by DN (semantic comparison).
func memberOfAny(groups []parsedGroup, wanted []string) bool {
	for _, w := range wanted {
		w = strings.TrimSpace(w)
		if !strings.Contains(w, "=") {
			for _, g := range groups {
				if strings.EqualFold(g.name, w) {
					return true
				}
			}
			continue
		}
		want, err := ldap.ParseDN(w)
		if err != nil {
			continue
		}
		for _, g := range groups {
			if want.EqualFold(g.dn) {
				return true
			}
		}
	}
	return false
}
