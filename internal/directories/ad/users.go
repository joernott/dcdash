// SPDX-License-Identifier: GPL-3.0-or-later

package ad

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
	"golang.org/x/text/encoding/unicode"
)

// userAccountControl flags.
const (
	uacDisabled           = 0x2
	uacNormal             = 0x200
	uacPasswordNeverExpir = 0x10000
	// In msDS-User-Account-Control-Computed.
	uacLockout         = 0x10
	uacPasswordExpired = 0x800000
)

// User is a user account.
type User struct {
	DN                   string     `json:"dn"`
	Name                 string     `json:"name"` // sAMAccountName
	UPN                  string     `json:"upn,omitempty"`
	GivenName            string     `json:"givenName,omitempty"`
	Surname              string     `json:"surname,omitempty"`
	DisplayName          string     `json:"displayName,omitempty"`
	Mail                 string     `json:"mail,omitempty"`
	Description          string     `json:"description,omitempty"`
	Disabled             bool       `json:"disabled"`
	LockedOut            bool       `json:"lockedOut"`
	MustChangePassword   bool       `json:"mustChangePassword"`
	PasswordExpired      bool       `json:"passwordExpired"`
	PasswordNeverExpires bool       `json:"passwordNeverExpires"`
	PasswordLastSet      *time.Time `json:"passwordLastSet,omitempty"`
	LastLogon            *time.Time `json:"lastLogon,omitempty"`
	Created              *time.Time `json:"created,omitempty"`
	Changed              *time.Time `json:"changed,omitempty"`
	// Groups are the DNs of the groups the user is a direct member of (without the primary
	// group).
	Groups []string `json:"groups"`
	// Managed: the user lies in one of the containers dcdash may change.
	Managed bool `json:"managed"`
	// Protected: a built-in or privileged account, or one configured as protected; it can
	// only be changed with the admin action.
	Protected bool `json:"protected"`

	sid     string
	primary uint32
}

var userAttrs = []string{"sAMAccountName", "userPrincipalName", "givenName", "sn", "displayName", "mail", "description",
	"userAccountControl", "msDS-User-Account-Control-Computed", "pwdLastSet", "lastLogonTimestamp", "whenCreated",
	"whenChanged", "memberOf", "objectSid", "isCriticalSystemObject", "primaryGroupID"}

const userClass = "(objectCategory=person)(objectClass=user)"

func (c *Client) userFrom(e *ldap.Entry) User {
	uac, _ := strconv.ParseUint(e.GetAttributeValue("userAccountControl"), 10, 32)
	computed, _ := strconv.ParseUint(e.GetAttributeValue("msDS-User-Account-Control-Computed"), 10, 32)
	primary, _ := strconv.ParseUint(e.GetAttributeValue("primaryGroupID"), 10, 32)
	pls := e.GetAttributeValue("pwdLastSet")
	u := User{
		DN:                   e.DN,
		Name:                 e.GetAttributeValue("sAMAccountName"),
		UPN:                  e.GetAttributeValue("userPrincipalName"),
		GivenName:            e.GetAttributeValue("givenName"),
		Surname:              e.GetAttributeValue("sn"),
		DisplayName:          e.GetAttributeValue("displayName"),
		Mail:                 e.GetAttributeValue("mail"),
		Description:          e.GetAttributeValue("description"),
		Disabled:             uac&uacDisabled != 0,
		LockedOut:            computed&uacLockout != 0,
		PasswordExpired:      computed&uacPasswordExpired != 0 && pls != "0",
		MustChangePassword:   pls == "0",
		PasswordNeverExpires: uac&uacPasswordNeverExpir != 0,
		PasswordLastSet:      fileTime(pls),
		LastLogon:            fileTime(e.GetAttributeValue("lastLogonTimestamp")),
		Created:              generalizedTime(e.GetAttributeValue("whenCreated")),
		Changed:              generalizedTime(e.GetAttributeValue("whenChanged")),
		Groups:               e.GetAttributeValues("memberOf"),
		Managed:              c.cfg.Managed(e.DN),
		primary:              uint32(primary),
	}
	if u.Groups == nil {
		u.Groups = []string{}
	}
	u.sid, _ = parseSID(e.GetRawAttributeValue("objectSid"))
	u.Protected = e.GetAttributeValue("isCriticalSystemObject") == "TRUE" || (u.sid != "" && rid(u.sid) < 1000) ||
		slices.ContainsFunc(c.cfg.ProtectedUsers, func(n string) bool { return strings.EqualFold(n, u.Name) })
	return u
}

// privilegedFilter matches the transitive members of the privileged groups.
func (c *Client) privilegedFilter(ctx context.Context, conn *ldap.Conn) (string, error) {
	sid, err := c.domainSID(conn)
	if err != nil {
		return "", err
	}
	var f strings.Builder
	f.WriteString("(|")
	for _, s := range privilegedSIDs(sid) {
		f.WriteString("(objectSid=" + s + ")")
	}
	f.WriteString(")")
	groups, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree, "(&(objectClass=group)"+f.String()+")", []string{"cn"}, 0)
	if err != nil {
		return "", err
	}
	var m strings.Builder
	m.WriteString("(|")
	for _, g := range groups {
		m.WriteString("(memberOf:1.2.840.113556.1.4.1941:=" + ldap.EscapeFilter(g.DN) + ")")
	}
	for _, r := range privilegedDomainRIDs {
		m.WriteString("(primaryGroupID=" + strconv.FormatUint(uint64(r), 10) + ")")
	}
	m.WriteString(")")
	return m.String(), nil
}

// markPrivileged sets Protected on users that are (transitive) members of a privileged
// group.
func (c *Client) markPrivileged(ctx context.Context, conn *ldap.Conn, users []User) error {
	f, err := c.privilegedFilter(ctx, conn)
	if err != nil {
		return err
	}
	priv, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree, "(&"+userClass+f+")", []string{"cn"}, 0)
	if err != nil {
		return err
	}
	dns := map[string]bool{}
	for _, e := range priv {
		dns[strings.ToLower(e.DN)] = true
	}
	for i := range users {
		if dns[strings.ToLower(users[i].DN)] {
			users[i].Protected = true
		}
	}
	return nil
}

// Users returns the users whose account name, names or mail address contain query (all
// if it is empty), at most limit (0: all), sorted by name.
func (c *Client) Users(ctx context.Context, query string, limit int) (users []User, truncated bool, err error) {
	filter := "(&" + userClass + ")"
	if q := strings.TrimSpace(query); q != "" {
		q = ldap.EscapeFilter(q)
		filter = "(&" + userClass + "(|(sAMAccountName=*" + q + "*)(displayName=*" + q + "*)(givenName=*" + q + "*)(sn=*" + q + "*)(mail=*" + q + "*)(userPrincipalName=*" + q + "*)))"
	}
	err = c.with(ctx, func(conn *ldap.Conn) error {
		entries, t, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree, filter, userAttrs, limit)
		if err != nil {
			return err
		}
		truncated = t
		for _, e := range entries {
			users = append(users, c.userFrom(e))
		}
		return c.markPrivileged(ctx, conn, users)
	})
	if err != nil {
		return nil, false, wrap("list users", err)
	}
	slices.SortFunc(users, func(a, b User) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return users, truncated, nil
}

// findUser returns the user with the account name.
func (c *Client) findUser(ctx context.Context, conn *ldap.Conn, name string) (User, error) {
	entries, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree,
		"(&"+userClass+"(sAMAccountName="+ldap.EscapeFilter(name)+"))", userAttrs, 2)
	if err != nil {
		return User{}, err
	}
	if len(entries) != 1 {
		return User{}, &Error{Kind: ErrNotFound, Op: "user " + name}
	}
	u := []User{c.userFrom(entries[0])}
	if err := c.markPrivileged(ctx, conn, u); err != nil {
		return User{}, err
	}
	return u[0], nil
}

// User returns one user by account name.
func (c *Client) User(ctx context.Context, name string) (User, error) {
	var u User
	err := c.with(ctx, func(conn *ldap.Conn) (err error) {
		u, err = c.findUser(ctx, conn, name)
		return err
	})
	return u, wrap("read user", err)
}

// NewUser describes a user to create.
type NewUser struct {
	// OU is the DN of the container; it must be managed.
	OU          string `json:"ou"`
	Name        string `json:"name"`
	UPN         string `json:"upn,omitempty"`
	GivenName   string `json:"givenName,omitempty"`
	Surname     string `json:"surname,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Mail        string `json:"mail,omitempty"`
	Description string `json:"description,omitempty"`
	Password    string `json:"password"`
	// MustChangePassword at the next logon.
	MustChangePassword bool `json:"mustChangePassword"`
	Disabled           bool `json:"disabled"`
}

// UserChange changes the attributes of a user; nil fields stay, empty ones are removed.
type UserChange struct {
	GivenName   *string `json:"givenName,omitempty"`
	Surname     *string `json:"surname,omitempty"`
	DisplayName *string `json:"displayName,omitempty"`
	Mail        *string `json:"mail,omitempty"`
	Description *string `json:"description,omitempty"`
	UPN         *string `json:"upn,omitempty"`
}

// encodePassword encodes a password for unicodePwd: in double quotes, UTF-16LE.
func encodePassword(pw string) string {
	s, _ := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().String(`"` + pw + `"`)
	return s
}

// CreateUser creates a user with a password.
func (c *Client) CreateUser(ctx context.Context, n NewUser) (User, error) {
	if !c.cfg.Managed(n.OU) {
		return User{}, &Error{Kind: ErrForbidden, Op: "create user", Message: "the container is not managed by dcdash"}
	}
	upn := n.UPN
	if upn == "" {
		upn = n.Name + "@" + strings.ToLower(c.cfg.Domain)
	}
	uac := uacNormal
	if n.Disabled {
		uac |= uacDisabled
	}
	dn := "CN=" + ldap.EscapeDN(n.Name) + "," + n.OU
	a := ldap.NewAddRequest(dn, nil)
	a.Attribute("objectClass", []string{"top", "person", "organizationalPerson", "user"})
	a.Attribute("sAMAccountName", []string{n.Name})
	a.Attribute("userPrincipalName", []string{upn})
	for attr, v := range map[string]string{"givenName": n.GivenName, "sn": n.Surname, "displayName": n.DisplayName,
		"mail": n.Mail, "description": n.Description} {
		if v != "" {
			a.Attribute(attr, []string{v})
		}
	}
	a.Attribute("unicodePwd", []string{encodePassword(n.Password)})
	a.Attribute("userAccountControl", []string{strconv.Itoa(uac)})
	var u User
	err := c.with(ctx, func(conn *ldap.Conn) error {
		if _, err := c.findUser(ctx, conn, n.Name); err == nil {
			return &Error{Kind: ErrExists, Op: "create user", Message: n.Name}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := conn.Add(a); err != nil {
			return err
		}
		if n.MustChangePassword {
			m := ldap.NewModifyRequest(dn, nil)
			m.Replace("pwdLastSet", []string{"0"})
			if err := conn.Modify(m); err != nil {
				return err
			}
		}
		var err error
		u, err = c.findUser(ctx, conn, n.Name)
		return err
	})
	return u, wrap("create user", err)
}

// modifyUser runs a modify request on a user found by name.
func (c *Client) modifyUser(ctx context.Context, op, name string, build func(u User, m *ldap.ModifyRequest) error) error {
	err := c.with(ctx, func(conn *ldap.Conn) error {
		u, err := c.findUser(ctx, conn, name)
		if err != nil {
			return err
		}
		m := ldap.NewModifyRequest(u.DN, nil)
		if err := build(u, m); err != nil {
			return err
		}
		if len(m.Changes) == 0 {
			return nil
		}
		return conn.Modify(m)
	})
	return wrap(op, err)
}

// UpdateUser changes attributes of a user.
func (c *Client) UpdateUser(ctx context.Context, name string, ch UserChange) error {
	return c.modifyUser(ctx, "change user", name, func(_ User, m *ldap.ModifyRequest) error {
		for attr, v := range map[string]*string{"givenName": ch.GivenName, "sn": ch.Surname, "displayName": ch.DisplayName,
			"mail": ch.Mail, "description": ch.Description, "userPrincipalName": ch.UPN} {
			if v == nil {
				continue
			}
			if *v == "" {
				m.Replace(attr, nil)
			} else {
				m.Replace(attr, []string{*v})
			}
		}
		return nil
	})
}

// SetUserEnabled enables or disables a user.
func (c *Client) SetUserEnabled(ctx context.Context, name string, enabled bool) error {
	return wrap("enable user", c.with(ctx, func(conn *ldap.Conn) error {
		u, err := c.findUser(ctx, conn, name)
		if err != nil {
			return err
		}
		e, err := one(conn, u.DN, "userAccountControl")
		if err != nil {
			return err
		}
		uac, err := strconv.ParseUint(e.GetAttributeValue("userAccountControl"), 10, 32)
		if err != nil {
			return fmt.Errorf("userAccountControl of %s: %w", name, err)
		}
		if enabled {
			uac &^= uacDisabled
		} else {
			uac |= uacDisabled
		}
		m := ldap.NewModifyRequest(u.DN, nil)
		m.Replace("userAccountControl", []string{strconv.FormatUint(uac, 10)})
		return conn.Modify(m)
	}))
}

// UnlockUser clears the lockout of a user locked out by failed logins.
func (c *Client) UnlockUser(ctx context.Context, name string) error {
	return c.modifyUser(ctx, "unlock user", name, func(_ User, m *ldap.ModifyRequest) error {
		m.Replace("lockoutTime", []string{"0"})
		return nil
	})
}

// ResetPassword sets a new password (an administrative reset); mustChange makes the user
// change it at the next logon.
func (c *Client) ResetPassword(ctx context.Context, name, password string, mustChange bool) error {
	return wrap("reset password", c.with(ctx, func(conn *ldap.Conn) error {
		u, err := c.findUser(ctx, conn, name)
		if err != nil {
			return err
		}
		m := ldap.NewModifyRequest(u.DN, nil)
		m.Replace("unicodePwd", []string{encodePassword(password)})
		if err := conn.Modify(m); err != nil {
			return err
		}
		m = ldap.NewModifyRequest(u.DN, nil)
		if mustChange {
			m.Replace("pwdLastSet", []string{"0"})
		} else {
			m.Replace("pwdLastSet", []string{"-1"})
		}
		return conn.Modify(m)
	}))
}

// DeleteUser deletes a user.
func (c *Client) DeleteUser(ctx context.Context, name string) error {
	err := c.with(ctx, func(conn *ldap.Conn) error {
		u, err := c.findUser(ctx, conn, name)
		if err != nil {
			return err
		}
		return conn.Del(ldap.NewDelRequest(u.DN, nil))
	})
	return wrap("delete user", err)
}

// ChangePassword changes the password of a user who knows the old one (self-service). It
// is a password change, not a reset: the directory checks the old password, the policy and
// the history, and counts a wrong old password towards the lockout. It works for accounts
// that must change their password, which can't bind.
func (c *Client) ChangePassword(ctx context.Context, name, oldPassword, newPassword string) error {
	return wrap("change password", c.with(ctx, func(conn *ldap.Conn) error {
		entries, _, err := search(ctx, conn, c.cfg.BaseDN, ldap.ScopeWholeSubtree,
			"(&"+userClass+"(|(sAMAccountName="+ldap.EscapeFilter(name)+")(userPrincipalName="+ldap.EscapeFilter(name)+")))",
			[]string{"sAMAccountName"}, 2)
		if err != nil {
			return err
		}
		if len(entries) != 1 {
			return &Error{Kind: ErrNotFound, Op: "change password"}
		}
		m := ldap.NewModifyRequest(entries[0].DN, nil)
		m.Delete("unicodePwd", []string{encodePassword(oldPassword)})
		m.Add("unicodePwd", []string{encodePassword(newPassword)})
		return conn.Modify(m)
	}))
}
