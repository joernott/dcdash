// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/testutil"
)

func certValid(t *testing.T, notBefore, notAfter time.Time) *tls.Certificate {
	t.Helper()
	cf, kf := testutil.WriteCert(t, t.TempDir(), testutil.CertOptions{NotBefore: notBefore, NotAfter: notAfter})
	c, err := tls.LoadX509KeyPair(cf, kf)
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestRenewAt(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	m := &ACMEManager{cfg: testACMEConfig("a.example")} // renew_before 30 days

	if !m.renewAt().IsZero() {
		t.Error("without certificate: renewal should be due immediately")
	}

	// 90-day certificate: renewed 30 days before expiry.
	_ = m.set(certValid(t, now, now.Add(90*24*time.Hour)))
	if got, want := m.renewAt(), now.Add(60*24*time.Hour); !got.Equal(want) {
		t.Errorf("90d: renewAt = %v, want %v", got, want)
	}

	// 24-hour certificate: renewed after two thirds of its lifetime.
	_ = m.set(certValid(t, now, now.Add(24*time.Hour)))
	if got, want := m.renewAt(), now.Add(16*time.Hour); !got.Equal(want) {
		t.Errorf("24h: renewAt = %v, want %v", got, want)
	}
}

func TestNextRetry(t *testing.T) {
	var d time.Duration
	var got []time.Duration
	for range 9 {
		d = nextRetry(d)
		got = append(got, d)
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("retry sequence %v, want %v", got, want)
		}
	}
}

// fakeLoop records the waits of the renewal loop and stops it after n waits.
type fakeLoop struct {
	waits []time.Duration
	stop  int
}

func (f *fakeLoop) sleep(_ context.Context, d time.Duration) bool {
	f.waits = append(f.waits, d)
	return len(f.waits) < f.stop
}

func TestRunRetriesWithBackoffThenSchedulesRenewal(t *testing.T) {
	now := time.Now()
	m, err := NewACMEManager(testACMEConfig("a.example"), t.TempDir(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }
	loop := &fakeLoop{stop: 3}
	m.sleep = loop.sleep

	attempts := 0
	m.obtainFn = func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("CA unavailable")
		}
		return m.set(certValid(t, now.Add(-time.Minute), now.Add(90*24*time.Hour)))
	}

	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	// Two failures (1m, 2m backoff), then a success; the next wait is capped at maxSleep.
	want := []time.Duration{time.Minute, 2 * time.Minute, maxSleep}
	if len(loop.waits) != len(want) {
		t.Fatalf("waits = %v, want %v", loop.waits, want)
	}
	for i := range want {
		if loop.waits[i] != want[i] {
			t.Errorf("waits = %v, want %v", loop.waits, want)
		}
	}
}

func TestRunRenewsWhenDue(t *testing.T) {
	now := time.Now()
	m, err := NewACMEManager(testACMEConfig("a.example"), t.TempDir(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	// Current certificate expires in 10 days: inside the 30-day renewal window.
	_ = m.set(certValid(t, now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour)))
	m.now = func() time.Time { return now }
	loop := &fakeLoop{stop: 1}
	m.sleep = loop.sleep

	renewed := false
	m.obtainFn = func(context.Context) error {
		renewed = true
		return m.set(certValid(t, now, now.Add(90*24*time.Hour)))
	}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !renewed {
		t.Error("certificate was not renewed")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	m, err := NewACMEManager(testACMEConfig("a.example"), t.TempDir(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.obtainFn = func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestSleepCtx(t *testing.T) {
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("expected elapsed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(ctx, time.Hour) {
		t.Error("expected canceled")
	}
}
