// SPDX-License-Identifier: GPL-3.0-or-later

package ldapauth

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/joernott/dcdash/internal/config"
)

// matchingRuleInChain is LDAP_MATCHING_RULE_IN_CHAIN: in AD it makes a
// member filter match transitively, i.e. it also finds nested groups.
const matchingRuleInChain = "1.2.840.113556.1.4.1941"

const groupPageSize = 500

// groupFilter returns the filter that finds all groups of the user.
func (c *Client) groupFilter(userDN, username string) string {
	if c.cfg.Mode == config.LDAPModeAD {
		return fmt.Sprintf("(&(objectClass=group)(member:%s:=%s))", matchingRuleInChain, ldap.EscapeFilter(userDN))
	}
	return strings.NewReplacer(
		"{dn}", ldap.EscapeFilter(userDN),
		"{username}", ldap.EscapeFilter(username),
	).Replace(c.cfg.GroupFilter)
}

// groups returns all groups the user is a member of, in AD mode including
// nested groups.
func (c *Client) groups(conn *ldap.Conn, userDN, username string) ([]Group, error) {
	req := ldap.NewSearchRequest(c.cfg.GroupBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		0, int(c.cfg.Timeout.D()/time.Second), false, c.groupFilter(userDN, username), []string{"cn"}, nil)
	res, err := conn.SearchWithPaging(req, groupPageSize)
	if err != nil {
		return nil, fmt.Errorf("%w: search groups: %w", ErrUnavailable, err)
	}
	groups := make([]Group, 0, len(res.Entries))
	for _, e := range res.Entries {
		groups = append(groups, Group{DN: e.DN, Name: e.GetAttributeValue("cn")})
	}
	slices.SortFunc(groups, func(a, b Group) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return groups, nil
}

// MemberOf reports whether the user is a member of the group given by its CN
// (e.g. "dashroot") or its full DN (anything containing "="). Both comparisons
// are case-insensitive; DNs are compared semantically (spacing, escaping).
func (u *User) MemberOf(group string) bool {
	group = strings.TrimSpace(group)
	if !strings.Contains(group, "=") {
		for _, g := range u.Groups {
			if strings.EqualFold(g.Name, group) {
				return true
			}
		}
		return false
	}
	want, err := ldap.ParseDN(group)
	if err != nil {
		return false
	}
	for _, g := range u.Groups {
		if dn, err := ldap.ParseDN(g.DN); err == nil && want.EqualFold(dn) {
			return true
		}
	}
	return false
}
