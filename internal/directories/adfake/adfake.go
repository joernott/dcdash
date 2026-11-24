// SPDX-License-Identifier: GPL-3.0-or-later

// Package adfake is an in-memory Active Directory for the tests of the directories module.
// It implements directories.Backend with the rules of package ad that the module relies on:
// managed containers, protected objects, password changes that check the old password and
// a minimum length, and errors of the same kinds.
package adfake

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joernott/dcdash/internal/config"
	"github.com/joernott/dcdash/internal/directories/ad"
	"github.com/joernott/dcdash/internal/status"
)

// Directory is an in-memory domain. UserMap and GroupMap (by lower-case name) may be
// changed directly by tests.
type Directory struct {
	mu        sync.Mutex
	cfg       config.Directory
	UserMap   map[string]*User
	GroupMap  map[string]*ad.Group
	PolicyVal ad.PasswordPolicy
	// Down makes every call fail with ad.ErrUnavailable.
	Down bool
	// Changes counts password changes by user (self-service).
	Changes map[string]int
}

// User is a user with its password.
type User struct {
	ad.User
	Password string
}

// New creates a domain with the configuration (name, domain, base DN, managed OUs).
func New(cfg config.Directory) *Directory {
	return &Directory{cfg: cfg, UserMap: map[string]*User{}, GroupMap: map[string]*ad.Group{},
		PolicyVal: ad.PasswordPolicy{MinLength: 7, Complexity: true, History: 24, LockoutThreshold: 5, LockoutDuration: 1800},
		Changes:   map[string]int{}}
}

// AddUser adds a user in the container ou.
func (d *Directory) AddUser(ou, name, password string, protected bool) *User {
	d.mu.Lock()
	defer d.mu.Unlock()
	u := &User{User: ad.User{DN: "CN=" + name + "," + ou, Name: name, UPN: name + "@" + d.cfg.Domain, Groups: []string{},
		Managed: d.cfg.Managed(ou), Protected: protected}, Password: password}
	d.UserMap[strings.ToLower(name)] = u
	return u
}

// AddGroup adds a group in the container ou.
func (d *Directory) AddGroup(ou, name string, protected bool) *ad.Group {
	d.mu.Lock()
	defer d.mu.Unlock()
	g := &ad.Group{DN: "CN=" + name + "," + ou, Name: name, Scope: ad.ScopeGlobal, Security: true,
		Managed: d.cfg.Managed(ou), Protected: protected}
	d.GroupMap[strings.ToLower(name)] = g
	return g
}

func (d *Directory) err(op string) error {
	if d.Down {
		return &ad.Error{Kind: ad.ErrUnavailable, Op: op}
	}
	return nil
}

func notFound(op string) error { return &ad.Error{Kind: ad.ErrNotFound, Op: op} }

// Name implements directories.Backend.
func (d *Directory) Name() string { return d.cfg.Name }

// Config implements directories.Backend.
func (d *Directory) Config() config.Directory { return d.cfg }

// Check implements directories.Backend.
func (d *Directory) Check(context.Context) (status.Detail, error) {
	if err := d.err("check"); err != nil {
		return status.Detail{}, err
	}
	return status.Detail{Text: d.cfg.Domain, Code: "ad.connected", Params: map[string]string{"domain": d.cfg.Domain, "count": "1"}}, nil
}

func (d *Directory) user(name string) (*User, bool) {
	u, ok := d.UserMap[strings.ToLower(name)]
	return u, ok
}

func (d *Directory) userCopy(u *User) ad.User {
	c := u.User
	c.Groups = []string{}
	for _, g := range d.GroupMap {
		if slices.ContainsFunc(g.Members, func(m ad.Member) bool { return strings.EqualFold(m.DN, u.DN) }) {
			c.Groups = append(c.Groups, g.DN)
		}
	}
	slices.Sort(c.Groups)
	return c
}

// Users implements directories.Backend.
func (d *Directory) Users(_ context.Context, query string, limit int) ([]ad.User, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("list users"); err != nil {
		return nil, false, err
	}
	q := strings.ToLower(query)
	var out []ad.User
	for _, u := range d.UserMap {
		if q == "" || strings.Contains(strings.ToLower(u.Name+" "+u.DisplayName+" "+u.Mail), q) {
			out = append(out, d.userCopy(u))
		}
	}
	slices.SortFunc(out, func(a, b ad.User) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// User implements directories.Backend.
func (d *Directory) User(_ context.Context, name string) (ad.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("read user"); err != nil {
		return ad.User{}, err
	}
	u, ok := d.user(name)
	if !ok {
		return ad.User{}, notFound("read user")
	}
	return d.userCopy(u), nil
}

func (d *Directory) checkPassword(op, pw string) error {
	if len([]rune(pw)) < d.PolicyVal.MinLength {
		return &ad.Error{Kind: ad.ErrPolicy, Op: op, Message: "0000052D: the password is too short"}
	}
	return nil
}

// CreateUser implements directories.Backend.
func (d *Directory) CreateUser(_ context.Context, n ad.NewUser) (ad.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("create user"); err != nil {
		return ad.User{}, err
	}
	if !d.cfg.Managed(n.OU) {
		return ad.User{}, &ad.Error{Kind: ad.ErrForbidden, Op: "create user"}
	}
	if _, ok := d.user(n.Name); ok {
		return ad.User{}, &ad.Error{Kind: ad.ErrExists, Op: "create user", Message: n.Name}
	}
	if err := d.checkPassword("create user", n.Password); err != nil {
		return ad.User{}, err
	}
	upn := n.UPN
	if upn == "" {
		upn = n.Name + "@" + d.cfg.Domain
	}
	now := time.Now().UTC()
	u := &User{User: ad.User{DN: "CN=" + n.Name + "," + n.OU, Name: n.Name, UPN: upn, GivenName: n.GivenName, Surname: n.Surname,
		DisplayName: n.DisplayName, Mail: n.Mail, Description: n.Description, Disabled: n.Disabled,
		MustChangePassword: n.MustChangePassword, Created: &now, Groups: []string{}, Managed: true}, Password: n.Password}
	d.UserMap[strings.ToLower(n.Name)] = u
	return d.userCopy(u), nil
}

// UpdateUser implements directories.Backend.
func (d *Directory) UpdateUser(_ context.Context, name string, ch ad.UserChange) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("change user"); err != nil {
		return err
	}
	u, ok := d.user(name)
	if !ok {
		return notFound("change user")
	}
	for _, f := range []struct {
		v   *string
		dst *string
	}{{ch.GivenName, &u.GivenName}, {ch.Surname, &u.Surname}, {ch.DisplayName, &u.DisplayName}, {ch.Mail, &u.Mail},
		{ch.Description, &u.Description}, {ch.UPN, &u.UPN}} {
		if f.v != nil {
			*f.dst = *f.v
		}
	}
	return nil
}

func (d *Directory) withUser(op, name string, f func(*User) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err(op); err != nil {
		return err
	}
	u, ok := d.user(name)
	if !ok {
		return notFound(op)
	}
	return f(u)
}

// SetUserEnabled implements directories.Backend.
func (d *Directory) SetUserEnabled(_ context.Context, name string, enabled bool) error {
	return d.withUser("enable user", name, func(u *User) error { u.Disabled = !enabled; return nil })
}

// UnlockUser implements directories.Backend.
func (d *Directory) UnlockUser(_ context.Context, name string) error {
	return d.withUser("unlock user", name, func(u *User) error { u.LockedOut = false; return nil })
}

// ResetPassword implements directories.Backend.
func (d *Directory) ResetPassword(_ context.Context, name, password string, mustChange bool) error {
	return d.withUser("reset password", name, func(u *User) error {
		if err := d.checkPassword("reset password", password); err != nil {
			return err
		}
		u.Password, u.MustChangePassword = password, mustChange
		return nil
	})
}

// DeleteUser implements directories.Backend.
func (d *Directory) DeleteUser(_ context.Context, name string) error {
	return d.withUser("delete user", name, func(u *User) error {
		delete(d.UserMap, strings.ToLower(u.Name))
		for _, g := range d.GroupMap {
			g.Members = slices.DeleteFunc(g.Members, func(m ad.Member) bool { return strings.EqualFold(m.DN, u.DN) })
		}
		return nil
	})
}

// ChangePassword implements directories.Backend: a wrong old password counts towards the
// lockout.
func (d *Directory) ChangePassword(_ context.Context, name, oldPassword, newPassword string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("change password"); err != nil {
		return err
	}
	var u *User
	for _, x := range d.UserMap {
		if strings.EqualFold(x.Name, name) || strings.EqualFold(x.UPN, name) {
			u = x
		}
	}
	if u == nil {
		return notFound("change password")
	}
	if u.LockedOut || u.Disabled || u.Password != oldPassword {
		return &ad.Error{Kind: ad.ErrWrongPassword, Op: "change password", Message: "00000056: the old password doesn't match"}
	}
	if err := d.checkPassword("change password", newPassword); err != nil {
		return err
	}
	if newPassword == oldPassword {
		return &ad.Error{Kind: ad.ErrPolicy, Op: "change password", Message: "0000052D: the password was used before"}
	}
	u.Password, u.MustChangePassword = newPassword, false
	d.Changes[strings.ToLower(u.Name)]++
	return nil
}

// Policy implements directories.Backend.
func (d *Directory) Policy(context.Context) (ad.PasswordPolicy, error) {
	if err := d.err("read password policy"); err != nil {
		return ad.PasswordPolicy{}, err
	}
	return d.PolicyVal, nil
}

// Containers implements directories.Backend.
func (d *Directory) Containers(context.Context) ([]ad.Container, error) {
	if err := d.err("list containers"); err != nil {
		return nil, err
	}
	out := []ad.Container{}
	for _, ou := range d.cfg.ManagedOUs {
		name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(ou, "OU="), "CN="), ",")
		out = append(out, ad.Container{DN: ou, Name: name})
	}
	return out, nil
}

func (d *Directory) group(name string) (*ad.Group, bool) {
	g, ok := d.GroupMap[strings.ToLower(name)]
	return g, ok
}

// Groups implements directories.Backend.
func (d *Directory) Groups(_ context.Context, query string, limit int) ([]ad.Group, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("list groups"); err != nil {
		return nil, false, err
	}
	q := strings.ToLower(query)
	var out []ad.Group
	for _, g := range d.GroupMap {
		if q == "" || strings.Contains(strings.ToLower(g.Name+" "+g.Description), q) {
			c := *g
			c.Members = nil
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b ad.Group) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// Group implements directories.Backend.
func (d *Directory) Group(_ context.Context, name string) (ad.Group, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("read group"); err != nil {
		return ad.Group{}, err
	}
	g, ok := d.group(name)
	if !ok {
		return ad.Group{}, notFound("read group")
	}
	c := *g
	c.Members = slices.Clone(g.Members)
	if c.Members == nil {
		c.Members = []ad.Member{}
	}
	return c, nil
}

// CreateGroup implements directories.Backend.
func (d *Directory) CreateGroup(_ context.Context, n ad.NewGroup) (ad.Group, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("create group"); err != nil {
		return ad.Group{}, err
	}
	if !d.cfg.Managed(n.OU) {
		return ad.Group{}, &ad.Error{Kind: ad.ErrForbidden, Op: "create group"}
	}
	if _, ok := d.group(n.Name); ok {
		return ad.Group{}, &ad.Error{Kind: ad.ErrExists, Op: "create group", Message: n.Name}
	}
	g := &ad.Group{DN: "CN=" + n.Name + "," + n.OU, Name: n.Name, Description: n.Description, Mail: n.Mail, Scope: n.Scope,
		Security: n.Security, Managed: true, Members: []ad.Member{}}
	d.GroupMap[strings.ToLower(n.Name)] = g
	return *g, nil
}

// UpdateGroup implements directories.Backend. Like AD, it refuses to turn a global group
// into a domain local one directly and the other way round.
func (d *Directory) UpdateGroup(_ context.Context, name string, ch ad.GroupChange) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("change group"); err != nil {
		return err
	}
	g, ok := d.group(name)
	if !ok {
		return notFound("change group")
	}
	if ch.Scope != nil && *ch.Scope != g.Scope && *ch.Scope != ad.ScopeUniversal && g.Scope != ad.ScopeUniversal {
		return &ad.Error{Kind: ad.ErrInvalid, Op: "change group", Message: "the group type cannot be changed directly"}
	}
	if ch.Name != nil && !strings.EqualFold(*ch.Name, g.Name) {
		if _, exists := d.group(*ch.Name); exists {
			return &ad.Error{Kind: ad.ErrExists, Op: "rename group", Message: *ch.Name}
		}
		delete(d.GroupMap, strings.ToLower(g.Name))
		g.Name = *ch.Name
		g.DN = "CN=" + g.Name + g.DN[strings.Index(g.DN, ","):]
		d.GroupMap[strings.ToLower(g.Name)] = g
	}
	if ch.Description != nil {
		g.Description = *ch.Description
	}
	if ch.Mail != nil {
		g.Mail = *ch.Mail
	}
	if ch.Scope != nil {
		g.Scope = *ch.Scope
	}
	if ch.Security != nil {
		g.Security = *ch.Security
	}
	return nil
}

// DeleteGroup implements directories.Backend.
func (d *Directory) DeleteGroup(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("delete group"); err != nil {
		return err
	}
	g, ok := d.group(name)
	if !ok {
		return notFound("delete group")
	}
	delete(d.GroupMap, strings.ToLower(g.Name))
	return nil
}

// SetMembers implements directories.Backend.
func (d *Directory) SetMembers(_ context.Context, group string, add, remove []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.err("change members"); err != nil {
		return err
	}
	g, ok := d.group(group)
	if !ok {
		return notFound("change members")
	}
	member := func(name string) (ad.Member, bool) {
		if u, ok := d.user(name); ok {
			return ad.Member{DN: u.DN, Name: u.Name, DisplayName: u.DisplayName, Kind: "user", Disabled: u.Disabled}, true
		}
		if x, ok := d.group(name); ok {
			return ad.Member{DN: x.DN, Name: x.Name, Kind: "group"}, true
		}
		return ad.Member{}, false
	}
	for _, n := range add {
		m, ok := member(n)
		if !ok {
			return &ad.Error{Kind: ad.ErrNotFound, Op: "member " + n}
		}
		if !slices.ContainsFunc(g.Members, func(x ad.Member) bool { return x.DN == m.DN }) {
			g.Members = append(g.Members, m)
		}
	}
	for _, n := range remove {
		g.Members = slices.DeleteFunc(g.Members, func(x ad.Member) bool { return strings.EqualFold(x.Name, n) })
	}
	slices.SortFunc(g.Members, func(a, b ad.Member) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return nil
}
