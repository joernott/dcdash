// SPDX-License-Identifier: GPL-3.0-or-later

package lockout

import (
	"fmt"
	"net/netip"
	"testing"
	"time"
)

func newTestLimiter() (*Limiter, *time.Time) {
	now := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	l := New(Policy{MaxFailures: 3, Window: 10 * time.Minute, Duration: 15 * time.Minute})
	l.now = func() time.Time { return now }
	return l, &now
}

func TestLockAfterMaxFailures(t *testing.T) {
	l, now := newTestLimiter()
	for i := 1; i <= 2; i++ {
		if l.Failure("alice") {
			t.Fatalf("locked after %d failures", i)
		}
		if ok, _ := l.Acquire("alice"); !ok {
			t.Fatalf("not allowed after %d failures", i)
		}
		l.Release("alice")
	}
	if !l.Failure("alice") {
		t.Fatal("not locked after 3 failures")
	}
	ok, wait := l.Acquire("alice")
	if ok || wait != 15*time.Minute {
		t.Errorf("Allowed = %v, %v", ok, wait)
	}
	if ok, _ := l.Acquire("bob"); !ok {
		t.Error("other key affected")
	}
	l.Release("bob")

	*now = now.Add(15*time.Minute + time.Second)
	if ok, _ := l.Acquire("alice"); !ok {
		t.Error("still locked after lockout duration")
	}
}

func TestWindowResets(t *testing.T) {
	l, now := newTestLimiter()
	l.Failure("alice")
	l.Failure("alice")
	*now = now.Add(11 * time.Minute) // outside the window
	if l.Failure("alice") {
		t.Error("failures outside the window counted")
	}
}

func TestSuccessResets(t *testing.T) {
	l, _ := newTestLimiter()
	l.Failure("alice")
	l.Failure("alice")
	l.Success("alice")
	for i := range 2 {
		if l.Failure("alice") {
			t.Errorf("locked at failure %d: failures before success counted", i+1)
		}
	}
	if l.Len() != 1 {
		t.Errorf("Len = %d", l.Len())
	}
}

func TestPurge(t *testing.T) {
	l, now := newTestLimiter()
	for i := range 10 {
		l.Failure(fmt.Sprint("user", i))
	}
	l.Failure("locked")
	l.Failure("locked")
	l.Failure("locked")
	*now = now.Add(11 * time.Minute)
	l.purge(*now)
	if l.Len() != 1 {
		t.Errorf("after purge: %d entries, want only the locked one", l.Len())
	}
}

// Parallel attempts count before their result is known.
func TestAttemptsInProgress(t *testing.T) {
	l, _ := newTestLimiter()
	for i := range 3 {
		if ok, _ := l.Acquire("alice"); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if ok, wait := l.Acquire("alice"); ok || wait != retryBusy {
		t.Fatalf("4th parallel attempt: %v, %v", ok, wait)
	}
	l.Release("alice") // e.g. the directory was unavailable: no failure
	if ok, _ := l.Acquire("alice"); !ok {
		t.Fatal("released attempt still counted")
	}
	l.Failure("alice")
	l.Release("alice")
	l.Failure("alice")
	l.Release("alice")
	if !l.Failure("alice") {
		t.Error("not locked after 3 failures")
	}
	l.Release("alice")
	if ok, _ := l.Acquire("alice"); ok {
		t.Error("locked key acquired")
	}

	l.Acquire("bob")
	l.Success("bob")
	l.Release("bob")
	if l.Len() != 1 {
		t.Errorf("Len = %d, want only alice", l.Len())
	}
}

func TestEvictWhenFull(t *testing.T) {
	l, _ := newTestLimiter()
	l.Failure("locked")
	l.Failure("locked")
	l.Failure("locked")
	for i := range maxEntries + 10 {
		l.Failure(fmt.Sprint("user", i))
	}
	if n := l.Len(); n > maxEntries {
		t.Errorf("Len = %d, more than %d", n, maxEntries)
	}
	if ok, _ := l.Acquire("locked"); ok {
		t.Error("the locked entry was evicted")
	}
}

func TestIPKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1":                "192.0.2.1",
		"::ffff:192.0.2.1":         "192.0.2.1",
		"2001:db8:1:2:aaaa::1":     "2001:db8:1:2::/64",
		"2001:db8:1:2:bbbb:cccc::": "2001:db8:1:2::/64",
	} {
		if got := IPKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("IPKey(%s) = %s, want %s", in, got, want)
		}
	}
}
