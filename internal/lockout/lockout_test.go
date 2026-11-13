// SPDX-License-Identifier: GPL-3.0-or-later

package lockout

import (
	"fmt"
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
		if ok, _ := l.Allowed("alice"); !ok {
			t.Fatalf("not allowed after %d failures", i)
		}
	}
	if !l.Failure("alice") {
		t.Fatal("not locked after 3 failures")
	}
	ok, wait := l.Allowed("alice")
	if ok || wait != 15*time.Minute {
		t.Errorf("Allowed = %v, %v", ok, wait)
	}
	if ok, _ := l.Allowed("bob"); !ok {
		t.Error("other key affected")
	}

	*now = now.Add(15*time.Minute + time.Second)
	if ok, _ := l.Allowed("alice"); !ok {
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
