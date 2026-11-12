// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"time"
)

const (
	minRetry = time.Minute
	maxRetry = time.Hour
	// maxSleep bounds the wait between checks, so that clock changes or a
	// suspended VM do not delay a renewal for too long.
	maxSleep = 12 * time.Hour
)

// renewAt returns when the current certificate should be renewed: renew_before
// ahead of expiry, but at most a third of the certificate lifetime ahead, so
// that short-lived certificates (e.g. step-ca's 24h default) are not renewed
// continuously. Without a certificate it returns the zero time (renew now).
func (m *ACMEManager) renewAt() time.Time {
	c := m.current()
	if c == nil {
		return time.Time{}
	}
	before := m.cfg.RenewBefore.D()
	if third := c.Leaf.NotAfter.Sub(c.Leaf.NotBefore) / 3; third < before {
		before = third
	}
	return c.Leaf.NotAfter.Add(-before)
}

// nextRetry doubles the retry delay from minRetry up to maxRetry.
func nextRetry(prev time.Duration) time.Duration {
	if prev < minRetry {
		return minRetry
	}
	return min(prev*2, maxRetry)
}

// Run obtains a certificate if none is available and renews it when due until
// ctx is canceled. Failed attempts are retried with exponential backoff; the
// current certificate stays in use meanwhile.
func (m *ACMEManager) Run(ctx context.Context) error {
	var retry time.Duration
	for {
		var wait time.Duration
		if at := m.renewAt(); !m.now().Before(at) {
			if err := m.obtainFn(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				retry = nextRetry(retry)
				wait = retry
				ev := m.log.Error().Err(err).Dur("retry_in", retry)
				if c := m.current(); c != nil {
					ev = ev.Time("current_not_after", c.Leaf.NotAfter)
				}
				ev.Msg("certificate request failed")
			} else {
				retry = 0
				continue // compute the next renewal time
			}
		} else {
			wait = min(at.Sub(m.now()), maxSleep)
			m.log.Debug().Time("renew_at", at).Dur("next_check_in", wait).Msg("certificate is current")
		}
		if !m.sleep(ctx, wait) {
			return nil
		}
	}
}

// sleepCtx waits for d or until ctx is canceled; it reports whether d elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
