// SPDX-License-Identifier: GPL-3.0-or-later

// Package lockout limits repeated failed attempts (e.g. logins) per key, such
// as a user name or a client IP address. After MaxFailures failures within
// Window the key is locked for Duration. State is kept in memory; dcdash runs
// as a single instance.
//
// Attempts in progress count against the limit: Acquire reserves one, and
// Release ends it. Otherwise many parallel requests would all pass the check
// before the first failure is recorded.
package lockout

import (
	"net/netip"
	"sync"
	"time"
)

// Policy defines when a key is locked.
type Policy struct {
	MaxFailures int
	Window      time.Duration
	Duration    time.Duration
}

// maxEntries bounds memory use against floods of random keys; beyond it,
// entries without recent activity are purged, and if that isn't enough,
// unlocked entries are evicted.
const maxEntries = 100_000

// retryBusy is the wait reported when a key is not locked, but the attempts
// in progress already reach the limit.
const retryBusy = time.Second

type entry struct {
	failures    int
	inFlight    int
	windowStart time.Time
	lockedUntil time.Time
}

// Limiter tracks failures per key.
type Limiter struct {
	policy  Policy
	mu      sync.Mutex
	entries map[string]*entry
	now     func() time.Time
}

// New creates a limiter.
func New(p Policy) *Limiter {
	return &Limiter{policy: p, entries: map[string]*entry{}, now: time.Now}
}

// Acquire starts an attempt for key. If the key is locked, or the failures
// so far plus the attempts in progress reach the limit, it returns false and
// how long to wait. Otherwise the caller must call Release when the attempt
// is over (after Failure or Success).
func (l *Limiter) Acquire(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entry(key, now)
	if wait := e.lockedUntil.Sub(now); wait > 0 {
		return false, wait
	}
	if now.Sub(e.windowStart) > l.policy.Window {
		e.failures, e.windowStart = 0, now
	}
	if e.failures+e.inFlight >= l.policy.MaxFailures {
		return false, retryBusy
	}
	e.inFlight++
	return true, 0
}

// Release ends an attempt started with Acquire.
func (l *Limiter) Release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return
	}
	if e.inFlight > 0 {
		e.inFlight--
	}
	if e.inFlight == 0 && e.failures == 0 && e.lockedUntil.IsZero() {
		delete(l.entries, key)
	}
}

// Failure records a failed attempt and reports whether the key is now locked.
func (l *Limiter) Failure(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.entry(key, now)
	if now.Sub(e.windowStart) > l.policy.Window {
		e.failures, e.windowStart = 0, now
	}
	e.failures++
	if e.failures >= l.policy.MaxFailures {
		e.lockedUntil = now.Add(l.policy.Duration)
		e.failures, e.windowStart = 0, now
		return true
	}
	return false
}

// Success forgets earlier failures of key. Attempts in progress still count
// until they are released.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	switch {
	case !ok:
	case e.inFlight == 0:
		delete(l.entries, key)
	default:
		e.failures, e.lockedUntil = 0, time.Time{}
	}
}

// Unlock removes a lock (e.g. by an administrator).
func (l *Limiter) Unlock(key string) { l.Success(key) }

// entry returns the entry of key, creating it if needed. l.mu must be held.
func (l *Limiter) entry(key string, now time.Time) *entry {
	if e, ok := l.entries[key]; ok {
		return e
	}
	if len(l.entries) >= maxEntries {
		l.purge(now)
	}
	e := &entry{windowStart: now}
	l.entries[key] = e
	return e
}

// purge removes entries that are neither locked, nor in use, nor within their
// window. If that frees less than 1% of maxEntries, it also evicts unlocked
// entries without attempts in progress, so that a flood of keys can't grow
// the map or make every new key scan it.
func (l *Limiter) purge(now time.Time) {
	for k, e := range l.entries {
		if e.inFlight == 0 && now.After(e.lockedUntil) && now.Sub(e.windowStart) > l.policy.Window {
			delete(l.entries, k)
		}
	}
	for k, e := range l.entries {
		if len(l.entries) < maxEntries-maxEntries/100 {
			return
		}
		if e.inFlight == 0 && now.After(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
}

// Len returns the number of tracked keys.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// IPKey returns the key for a client address: the address itself for IPv4,
// the /64 network for IPv6, because a single client usually controls a whole
// /64 and could otherwise use a new address for every attempt.
func IPKey(ip netip.Addr) string {
	ip = ip.Unmap()
	if ip.Is4() || !ip.IsValid() {
		return ip.String()
	}
	p, _ := ip.Prefix(64)
	return p.String()
}
