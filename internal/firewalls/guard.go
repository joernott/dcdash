// SPDX-License-Identifier: GPL-3.0-or-later

package firewalls

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/joernott/dcdash/internal/firewalls/opnsense"
)

// Without savepoints (OPNsense 26.7 and later), nothing undoes a rule that cuts
// dcdash off. The lockout guard therefore keeps dcdash away from the rules that
// could do that: the rules of the interface dcdash reaches the firewall
// through, floating rules (no interface) and rules on interface groups.

// guardTTL is how long the detected interfaces are reused.
const guardTTL = 5 * time.Minute

// errGuardUnknown: dcdash can't tell which interface it reaches the firewall
// through, so it doesn't change rules.
var errGuardUnknown = errors.New("dcdash can't tell which interface it reaches the firewall through")

// protectedError: the change touches a rule that could cut dcdash off.
type protectedError struct {
	// Interface is the protected interface, "" for a floating rule, or an
	// unknown name (an interface group).
	Interface string
	Reason    string
}

func (e *protectedError) Error() string { return e.Reason }

// guard holds the interfaces of a firewall and the protected ones.
type guard struct {
	// known are the interface keys; protected maps the protected keys to
	// their description.
	known     map[string]bool
	protected map[string]string
	at        time.Time
}

// newGuard finds the interfaces dcdash reaches the firewall through: the one
// with the route back to dcdash's address, and the one with the address dcdash
// connects to (they differ with asymmetric routing). extra are configured
// protected interfaces. Without a detected interface, and without configured ones, it
// returns errGuardUnknown.
func newGuard(ifaces []opnsense.Interface, routes []opnsense.Route, local, remote netip.Addr, extra []string) (guard, error) {
	g := guard{known: map[string]bool{}, protected: map[string]string{}}
	byDevice := map[string]opnsense.Interface{}
	for _, i := range ifaces {
		g.known[i.Key] = true
		byDevice[i.Device] = i
		if slices.ContainsFunc(i.Prefixes, func(p netip.Prefix) bool { return p.Addr() == remote }) {
			g.protected[i.Key] = i.Description
		}
	}
	best := -1
	var via string
	for _, r := range routes {
		if r.Destination.Contains(local) && r.Destination.Bits() > best {
			best, via = r.Destination.Bits(), r.Device
		}
	}
	routed := false
	if i, ok := byDevice[via]; ok {
		g.protected[i.Key] = i.Description
		routed = true
	}
	if !routed && len(extra) == 0 {
		return g, fmt.Errorf("%w: no route to %s through an assigned interface", errGuardUnknown, local)
	}
	for _, k := range extra {
		if _, ok := g.protected[k]; !ok {
			g.protected[k] = k
		}
	}
	return g, nil
}

// check returns a protectedError if the rule is enabled and could cut dcdash off.
func (g guard) check(r opnsense.Rule) error {
	if !r.Enabled {
		return nil
	}
	if r.Interface == "" {
		return &protectedError{Reason: "dcdash doesn't change floating rules on a firewall without savepoints"}
	}
	for _, i := range strings.Split(r.Interface, ",") {
		if desc, ok := g.protected[i]; ok {
			return &protectedError{Interface: i, Reason: fmt.Sprintf(
				"dcdash reaches the firewall through %s (%s) and doesn't change its rules on a firewall without savepoints", i, desc)}
		}
		if !g.known[i] {
			return &protectedError{Interface: i, Reason: fmt.Sprintf(
				"%s is not an assigned interface (an interface group?); dcdash doesn't change such rules on a firewall without savepoints", i)}
		}
	}
	return nil
}

// protectedKeys returns the protected interface keys, sorted.
func (g guard) protectedKeys() []string {
	keys := make([]string, 0, len(g.protected))
	for k := range g.protected {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lockoutGuard returns the guard of a firewall, detecting the interfaces at
// most every guardTTL.
func (s *Service) lockoutGuard(ctx context.Context, x *fw) (guard, error) {
	x.mu.Lock()
	g := x.guard
	x.mu.Unlock()
	if g.known != nil && s.now().Sub(g.at) < guardTTL {
		return g, nil
	}
	local, remote, err := x.backend.Path(ctx)
	if err != nil && len(x.protected) > 0 {
		// The configured interfaces are enough.
		s.log.Warn().Err(err).Str("firewall", x.backend.Name()).Msg("lockout guard: using only the configured protected interfaces")
		local, remote = netip.Addr{}, netip.Addr{}
	} else if err != nil {
		return guard{}, fmt.Errorf("%w: %w", errGuardUnknown, err)
	}
	ifaces, err := x.backend.Interfaces(ctx)
	if err != nil {
		return guard{}, fmt.Errorf("%w: interfaces: %w", errGuardUnknown, err)
	}
	routes, err := x.backend.Routes(ctx)
	if err != nil {
		return guard{}, fmt.Errorf("%w: routes: %w", errGuardUnknown, err)
	}
	g, err = newGuard(ifaces, routes, local, remote, x.protected)
	if err != nil {
		return guard{}, err
	}
	g.at = s.now()
	x.mu.Lock()
	x.guard = g
	x.mu.Unlock()
	s.log.Info().Str("firewall", x.backend.Name()).Str("local", local.String()).Str("remote", remote.String()).
		Strs("protected", g.protectedKeys()).Msg("lockout guard: protected interfaces")
	return g, nil
}
