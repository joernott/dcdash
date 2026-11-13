// SPDX-License-Identifier: GPL-3.0-or-later

// Package lockout limits repeated failed attempts (e.g. logins) per key, such
// as a user name or a client IP address. After MaxFailures failures within
// Window the key is locked for Duration. State is kept in memory; dcdash runs
// as a single instance.
package lockout

import (
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
// entries without recent activity are purged.
const maxEntries = 100_000

type entry struct {
	failures    int
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

// Allowed reports whether an attempt for key may be made; if not, it also
// returns how long the key stays locked.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if wait := e.lockedUntil.Sub(l.now()); wait > 0 {
		return false, wait
	}
	return true, 0
}

// Failure records a failed attempt and reports whether the key is now locked.
func (l *Limiter) Failure(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= maxEntries {
			l.purge(now)
		}
		e = &entry{windowStart: now}
		l.entries[key] = e
	}
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

// Success forgets earlier failures of key.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// Unlock removes a lock (e.g. by an administrator).
func (l *Limiter) Unlock(key string) { l.Success(key) }

// purge removes entries that are neither locked nor within their window.
func (l *Limiter) purge(now time.Time) {
	for k, e := range l.entries {
		if now.After(e.lockedUntil) && now.Sub(e.windowStart) > l.policy.Window {
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
