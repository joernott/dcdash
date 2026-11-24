// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// PasswordPolicy is the password policy of the domain (fine-grained policies are not
// evaluated).
type PasswordPolicy struct {
	MinLength  int  `json:"minLength"`
	Complexity bool `json:"complexity"`
	// History is the number of previous passwords that can't be reused.
	History int `json:"history"`
	// MinAge and MaxAge in seconds; 0 means none.
	MinAge int64 `json:"minAge"`
	MaxAge int64 `json:"maxAge"`
	// LockoutThreshold is the number of failed logins that lock an account (0: none);
	// LockoutDuration in seconds.
	LockoutThreshold int   `json:"lockoutThreshold"`
	LockoutDuration  int64 `json:"lockoutDuration"`
}

const pwdComplex = 0x1

// Policy reads the password policy from the domain object.
func (c *Client) Policy(ctx context.Context) (PasswordPolicy, error) {
	var p PasswordPolicy
	err := c.with(ctx, func(conn *ldap.Conn) error {
		e, err := one(conn, c.cfg.BaseDN, "minPwdLength", "pwdProperties", "pwdHistoryLength", "minPwdAge", "maxPwdAge",
			"lockoutThreshold", "lockoutDuration")
		if err != nil {
			return err
		}
		num := func(a string) int { v, _ := strconv.Atoi(e.GetAttributeValue(a)); return v }
		p = PasswordPolicy{
			MinLength:        num("minPwdLength"),
			Complexity:       num("pwdProperties")&pwdComplex != 0,
			History:          num("pwdHistoryLength"),
			MinAge:           int64(interval(e.GetAttributeValue("minPwdAge")) / time.Second),
			MaxAge:           int64(interval(e.GetAttributeValue("maxPwdAge")) / time.Second),
			LockoutThreshold: num("lockoutThreshold"),
			LockoutDuration:  int64(interval(e.GetAttributeValue("lockoutDuration")) / time.Second),
		}
		return nil
	})
	return p, wrap("read password policy", err)
}

// Container is an organizational unit or container users and groups can be created in.
type Container struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
}

// Containers returns the managed organizational units and containers (CN=Users, ...),
// sorted by DN from the domain down.
func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	var out []Container
	err := c.with(ctx, func(conn *ldap.Conn) error {
		for _, ou := range c.cfg.ManagedOUs {
			entries, _, err := search(ctx, conn, ou, ldap.ScopeWholeSubtree,
				"(|(objectClass=organizationalUnit)(&(objectClass=container)(!(isCriticalSystemObject=TRUE))(|(cn=Users)(cn=Computers)))(objectClass=domainDNS))",
				[]string{"ou", "cn", "dc"}, 0)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.DN), "dc=domaindnszones") || strings.Contains(strings.ToLower(e.DN), "dc=forestdnszones") {
					continue
				}
				name := e.GetAttributeValue("ou")
				if name == "" {
					name = e.GetAttributeValue("cn")
				}
				if name == "" {
					name = e.GetAttributeValue("dc")
				}
				out = append(out, Container{DN: e.DN, Name: name})
			}
		}
		return nil
	})
	if err != nil {
		return nil, wrap("list containers", err)
	}
	slices.SortFunc(out, func(a, b Container) int { return strings.Compare(reverseDN(a.DN), reverseDN(b.DN)) })
	return slices.CompactFunc(out, func(a, b Container) bool { return strings.EqualFold(a.DN, b.DN) }), nil
}

// reverseDN orders DNs from the root down, so that children follow their parents.
func reverseDN(dn string) string {
	parts := strings.Split(strings.ToLower(dn), ",")
	slices.Reverse(parts)
	return strings.Join(parts, ",")
}
