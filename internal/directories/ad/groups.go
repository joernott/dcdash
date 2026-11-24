// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// Group scopes.
const (
	ScopeGlobal      = "global"
	ScopeDomainLocal = "domainLocal"
	ScopeUniversal   = "universal"
)

// groupType flags.
const (
	gtBuiltin     = 0x1
	gtGlobal      = 0x2
	gtDomainLocal = 0x4
	gtUniversal   = 0x8
	gtSecurity    = 0x80000000
)

// Group is a group.
type Group struct {
	DN          string     `json:"dn"`
	Name        string     `json:"name"` // sAMAccountName
	Description string     `json:"description,omitempty"`
	Mail        string     `json:"mail,omitempty"`
	Scope       string     `json:"scope"`
	Security    bool       `json:"security"`
	Created     *time.Time `json:"created,omitempty"`
	Changed     *time.Time `json:"changed,omitempty"`
	Managed     bool       `json:"managed"`
	// Protected: a built-in or privileged group, or one configured as protected.
	Protected bool `json:"protected"`
	// Members are set by Group only.
	Members []Member `json:"members,omitempty"`
}

// Member is a direct member of a group.
type Member struct {
	DN          string `json:"dn"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	// Kind is "user", "group", "computer" or "other" (e.g. foreign security principals).
	Kind     string `json:"kind"`
	Disabled bool   `json:"disabled,omitempty"`
}

var groupAttrs = []string{"sAMAccountName", "description", "mail", "groupType", "whenCreated", "whenChanged",
	"objectSid", "isCriticalSystemObject"}

func (c *Client) groupFrom(e *ldap.Entry, privileged []string) Group {
	gt64, _ := strconv.ParseInt(e.GetAttributeValue("groupType"), 10, 64)
	gt := uint32(gt64) // #nosec G115 -- groupType is a signed 32-bit flag set
	g := Group{
		DN:          e.DN,
		Name:        e.GetAttributeValue("sAMAccountName"),
		Description: e.GetAttributeValue("description"),
		Mail:        e.GetAttributeValue("mail"),
		Security:    gt&gtSecurity != 0,
		Created:     generalizedTime(e.GetAttributeValue("whenCreated")),
		Changed:     generalizedTime(e.GetAttributeValue("whenChanged")),
		Managed:     c.cfg.Managed(e.DN),
	}
	switch {
	case gt&gtUniversal != 0:
		g.Scope = ScopeUniversal
	case gt&(gtDomainLocal|gtBuiltin) != 0:
		g.Scope = ScopeDomainLocal
	default:
		g.Scope = ScopeGlobal
	}
	sid, _ := parseSID(e.GetRawAttributeValue("objectSid"))
	g.Protected = e.GetAttributeValue("isCriticalSystemObject") == "TRUE" || gt&gtBuiltin != 0 ||
		strings.HasPrefix(sid, "S-1-5-32-") || (sid != "" && rid(sid) < 1000) || slices.Contains(privileged, sid) ||
		slices.ContainsFunc(c.cfg.ProtectedGroups, func(n string) bool { return strings.EqualFold(n, g.Name) })
	return g
}

// groupType returns the groupType value for a scope.
func groupType(scope string, security bool) (string, error) {
	var gt uint32
	switch scope {
	case ScopeGlobal, "":
		gt = gtGlobal
	case ScopeDomainLocal:
		gt = gtDomainLocal
	case ScopeUniversal:
		gt = gtUniversal
	default:
		return "", &Error{Kind: ErrInvalid, Op: "group", Message: "unknown scope " + scope}
	}
	if security {
		gt |= gtSecurity
	}
	return strconv.FormatInt(int64(int32(gt)), 10), nil // #nosec G115 -- AD stores groupType as a signed 32-bit integer
}

func (c *Client) privileged(conn *ldap.Conn) ([]string, error) {
	sid, err := c.domainSID(conn)
	if err != nil {
		return nil, err
	}
	return privilegedSIDs(sid), nil
}

// Groups returns the groups whose name or description contain query (all if empty), at
// most limit (0: all), sorted by name.
func (c *Client) Groups(ctx context.Context, query string, limit int) (groups []Group, truncated bool, err error) {
	filter := "(objectClass=group)"
	if q := strings.TrimSpace(query); q != "" {
		q = ldap.EscapeFilter(q)
		filter = "(&(objectClass=group)(|(sAMAccountName=*" + q + "*)(description=*" + q + "*)))"
	}
	err = c.with(ctx, func(conn *ldap.Conn) error {
		priv, err := c.privileged(conn)
		if err != nil {
			return err
		}
		entries, t, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree, filter, groupAttrs, limit)
		if err != nil {
			return err
		}
		truncated = t
		for _, e := range entries {
			groups = append(groups, c.groupFrom(e, priv))
		}
		return nil
	})
	if err != nil {
		return nil, false, wrap("list groups", err)
	}
	slices.SortFunc(groups, func(a, b Group) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return groups, truncated, nil
}

func (c *Client) findGroup(ctx context.Context, conn *ldap.Conn, name string) (Group, error) {
	priv, err := c.privileged(conn)
	if err != nil {
		return Group{}, err
	}
	entries, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree,
		"(&(objectClass=group)(sAMAccountName="+ldap.EscapeFilter(name)+"))", groupAttrs, 2)
	if err != nil {
		return Group{}, err
	}
	if len(entries) != 1 {
		return Group{}, &Error{Kind: ErrNotFound, Op: "group " + name}
	}
	return c.groupFrom(entries[0], priv), nil
}

// members returns the direct members of a group (by their memberOf back link, so that
// large groups need no range retrieval).
func (c *Client) members(ctx context.Context, conn *ldap.Conn, dn string) ([]Member, error) {
	entries, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree, "(memberOf="+ldap.EscapeFilter(dn)+")",
		[]string{"sAMAccountName", "displayName", "objectClass", "userAccountControl", "cn"}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(entries))
	for _, e := range entries {
		m := Member{DN: e.DN, Name: e.GetAttributeValue("sAMAccountName"), DisplayName: e.GetAttributeValue("displayName"), Kind: "other"}
		classes := e.GetAttributeValues("objectClass")
		switch {
		case slices.Contains(classes, "computer"):
			m.Kind = "computer"
		case slices.Contains(classes, "group"):
			m.Kind = "group"
		case slices.Contains(classes, "user"):
			m.Kind = "user"
		}
		if m.Name == "" {
			m.Name = e.GetAttributeValue("cn")
		}
		uac, _ := strconv.ParseUint(e.GetAttributeValue("userAccountControl"), 10, 32)
		m.Disabled = uac&uacDisabled != 0
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Member) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// Group returns one group with its direct members.
func (c *Client) Group(ctx context.Context, name string) (Group, error) {
	var g Group
	err := c.with(ctx, func(conn *ldap.Conn) (err error) {
		if g, err = c.findGroup(ctx, conn, name); err != nil {
			return err
		}
		g.Members, err = c.members(ctx, conn, g.DN)
		return err
	})
	return g, wrap("read group", err)
}

// NewGroup describes a group to create.
type NewGroup struct {
	OU          string `json:"ou"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Mail        string `json:"mail,omitempty"`
	Scope       string `json:"scope"`
	Security    bool   `json:"security"`
}

// GroupChange changes a group; nil fields stay, empty strings are removed.
type GroupChange struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Mail        *string `json:"mail,omitempty"`
	Scope       *string `json:"scope,omitempty"`
	Security    *bool   `json:"security,omitempty"`
}

// CreateGroup creates a group.
func (c *Client) CreateGroup(ctx context.Context, n NewGroup) (Group, error) {
	if !c.cfg.Managed(n.OU) {
		return Group{}, &Error{Kind: ErrForbidden, Op: "create group", Message: "the container is not managed by dcdash"}
	}
	gt, err := groupType(n.Scope, n.Security)
	if err != nil {
		return Group{}, err
	}
	a := ldap.NewAddRequest("CN="+ldap.EscapeDN(n.Name)+","+n.OU, nil)
	a.Attribute("objectClass", []string{"top", "group"})
	a.Attribute("sAMAccountName", []string{n.Name})
	a.Attribute("groupType", []string{gt})
	if n.Description != "" {
		a.Attribute("description", []string{n.Description})
	}
	if n.Mail != "" {
		a.Attribute("mail", []string{n.Mail})
	}
	var g Group
	err = c.with(ctx, func(conn *ldap.Conn) error {
		if _, err := c.findGroup(ctx, conn, n.Name); err == nil {
			return &Error{Kind: ErrExists, Op: "create group", Message: n.Name}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := conn.Add(a); err != nil {
			return err
		}
		var err error
		g, err = c.findGroup(ctx, conn, n.Name)
		g.Members = []Member{}
		return err
	})
	return g, wrap("create group", err)
}

// UpdateGroup changes a group. A new name renames the object (CN) and sAMAccountName.
// Scope changes are subject to AD's rules: global and domain local groups can only become
// universal and back.
func (c *Client) UpdateGroup(ctx context.Context, name string, ch GroupChange) error {
	return wrap("change group", c.with(ctx, func(conn *ldap.Conn) error {
		g, err := c.findGroup(ctx, conn, name)
		if err != nil {
			return err
		}
		m := ldap.NewModifyRequest(g.DN, nil)
		for attr, v := range map[string]*string{"description": ch.Description, "mail": ch.Mail} {
			if v == nil {
				continue
			}
			if *v == "" {
				m.Replace(attr, nil)
			} else {
				m.Replace(attr, []string{*v})
			}
		}
		if ch.Scope != nil || ch.Security != nil {
			scope, sec := g.Scope, g.Security
			if ch.Scope != nil {
				scope = *ch.Scope
			}
			if ch.Security != nil {
				sec = *ch.Security
			}
			gt, err := groupType(scope, sec)
			if err != nil {
				return err
			}
			m.Replace("groupType", []string{gt})
		}
		if ch.Name != nil && *ch.Name != g.Name {
			if _, err := c.findGroup(ctx, conn, *ch.Name); err == nil {
				return &Error{Kind: ErrExists, Op: "rename group", Message: *ch.Name}
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			rdn := "CN=" + ldap.EscapeDN(*ch.Name)
			if err := conn.ModifyDN(ldap.NewModifyDNRequest(g.DN, rdn, true, "")); err != nil {
				return err
			}
			m.DN = rdn + "," + parentDN(g.DN)
			m.Replace("sAMAccountName", []string{*ch.Name})
		}
		if len(m.Changes) == 0 {
			return nil
		}
		return conn.Modify(m)
	}))
}

// DeleteGroup deletes a group.
func (c *Client) DeleteGroup(ctx context.Context, name string) error {
	return wrap("delete group", c.with(ctx, func(conn *ldap.Conn) error {
		g, err := c.findGroup(ctx, conn, name)
		if err != nil {
			return err
		}
		return conn.Del(ldap.NewDelRequest(g.DN, nil))
	}))
}

// memberDN finds a user, group or computer by account name.
func (c *Client) memberDN(ctx context.Context, conn *ldap.Conn, name string) (string, error) {
	n := ldap.EscapeFilter(name)
	entries, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree,
		"(&(|(objectClass=user)(objectClass=group))(|(sAMAccountName="+n+")(sAMAccountName="+n+"$)))", []string{"cn"}, 2)
	if err != nil {
		return "", err
	}
	if len(entries) != 1 {
		return "", &Error{Kind: ErrNotFound, Op: "member " + name}
	}
	return entries[0].DN, nil
}

// SetMembers adds and removes direct members of a group by account name. Adding a member
// that is already one, or removing one that isn't, is not an error.
func (c *Client) SetMembers(ctx context.Context, group string, add, remove []string) error {
	return wrap("change members", c.with(ctx, func(conn *ldap.Conn) error {
		g, err := c.findGroup(ctx, conn, group)
		if err != nil {
			return err
		}
		current, err := c.members(ctx, conn, g.DN)
		if err != nil {
			return err
		}
		is := map[string]bool{}
		for _, m := range current {
			is[strings.ToLower(m.DN)] = true
		}
		m := ldap.NewModifyRequest(g.DN, nil)
		for _, n := range add {
			dn, err := c.memberDN(ctx, conn, n)
			if err != nil {
				return err
			}
			if !is[strings.ToLower(dn)] {
				m.Add("member", []string{dn})
				is[strings.ToLower(dn)] = true
			}
		}
		for _, n := range remove {
			dn, err := c.memberDN(ctx, conn, n)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if is[strings.ToLower(dn)] {
				m.Delete("member", []string{dn})
				delete(is, strings.ToLower(dn))
			}
		}
		if len(m.Changes) == 0 {
			return nil
		}
		return conn.Modify(m)
	}))
}
