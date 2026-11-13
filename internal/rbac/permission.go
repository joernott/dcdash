// SPDX-License-Identifier: GPL-3.0-or-later

// Package rbac implements roles and permissions.
//
// A permission has the form module:instance:resource:action, for example
// "dns:cloudflare-prod:example.com:write". Module, instance and resource may be
// "*" or contain "*" as a wildcard for any sequence of characters. The resource
// may contain colons (e.g. IPv6 subnets); module and instance may not.
//
// Actions form a hierarchy: read < operate < console < write < admin. A granted
// action includes all lower actions. Permissions only grant; there is no deny.
package rbac

import (
	"errors"
	"fmt"
	"strings"
)

// Action is a permission level.
type Action int

// Actions in ascending order of privilege.
const (
	ActionRead Action = iota + 1
	ActionOperate
	ActionConsole
	ActionWrite
	ActionAdmin
)

var actionNames = map[Action]string{
	ActionRead: "read", ActionOperate: "operate", ActionConsole: "console",
	ActionWrite: "write", ActionAdmin: "admin",
}

func (a Action) String() string {
	if n, ok := actionNames[a]; ok {
		return n
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// ParseAction parses an action name; "*" means admin.
func ParseAction(s string) (Action, error) {
	if s == "*" {
		return ActionAdmin, nil
	}
	for a, n := range actionNames {
		if n == s {
			return a, nil
		}
	}
	return 0, fmt.Errorf("unknown action %q (read, operate, console, write, admin or *)", s)
}

// Permission is a parsed permission string.
type Permission struct {
	Module   string
	Instance string
	Resource string
	Action   Action
}

// P builds a permission for a check, e.g. P("dns", "cf", "example.com", ActionWrite).
func P(module, instance, resource string, action Action) Permission {
	return Permission{Module: module, Instance: instance, Resource: resource, Action: action}
}

// String returns the permission in its textual form.
func (p Permission) String() string {
	return p.Module + ":" + p.Instance + ":" + p.Resource + ":" + p.Action.String()
}

// Parse parses a permission string.
func Parse(s string) (Permission, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 4 {
		return Permission{}, fmt.Errorf("permission %q: must be module:instance:resource:action", s)
	}
	p := Permission{
		Module:   parts[0],
		Instance: parts[1],
		Resource: strings.Join(parts[2:len(parts)-1], ":"),
	}
	if p.Module == "" || p.Instance == "" || p.Resource == "" {
		return Permission{}, fmt.Errorf("permission %q: empty segment", s)
	}
	a, err := ParseAction(parts[len(parts)-1])
	if err != nil {
		return Permission{}, fmt.Errorf("permission %q: %w", s, err)
	}
	p.Action = a
	return p, nil
}

// MustParse parses a permission and panics on error. Use for constants.
func MustParse(s string) Permission {
	p, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return p
}

// Grants reports whether the granted permission p covers the requested
// permission req. The segments of req are taken literally.
func (p Permission) Grants(req Permission) bool {
	return p.Action >= req.Action &&
		match(p.Module, req.Module) &&
		match(p.Instance, req.Instance) &&
		match(p.Resource, req.Resource)
}

// match matches value against a pattern in which "*" stands for any sequence
// of characters (including none). Matching is case-sensitive.
func match(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(value, part)
		if i < 0 {
			return false
		}
		value = value[i+len(part):]
	}
	return strings.HasSuffix(value, last)
}

// Set is a list of granted permissions.
type Set []Permission

// ParseSet parses a list of permission strings and returns all errors.
func ParseSet(ss []string) (Set, error) {
	set := make(Set, 0, len(ss))
	var errs []error
	for _, s := range ss {
		p, err := Parse(s)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		set = append(set, p)
	}
	return set, errors.Join(errs...)
}

// Allows reports whether any permission in the set grants req.
func (s Set) Allows(req Permission) bool {
	for _, g := range s {
		if g.Grants(req) {
			return true
		}
	}
	return false
}

// AllowsAny reports whether the set grants at least the given action on any
// resource of a module instance. The frontend uses this to decide whether to
// show a module at all.
func (s Set) AllowsAny(module, instance string, action Action) bool {
	for _, g := range s {
		if g.Action >= action && match(g.Module, module) && match(g.Instance, instance) {
			return true
		}
	}
	return false
}

// Strings returns the permissions in textual form.
func (s Set) Strings() []string {
	out := make([]string, len(s))
	for i, p := range s {
		out[i] = p.String()
	}
	return out
}
