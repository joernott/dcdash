// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "time"

// Status configures the checks of the overview page.
type Status struct {
	// RetryDelay is the wait before a check that failed with a 4xx response (for example
	// 408 from a Kea control agent) is repeated once; 0 repeats it at once.
	RetryDelay Duration `yaml:"retry_delay"`
}

// maxRetryDelay keeps the overview responsive.
const maxRetryDelay = 10 * time.Second

func defaultStatus() Status { return Status{RetryDelay: Duration(time.Second)} }

func (s Status) validate(add func(string, ...any)) {
	if s.RetryDelay < 0 || time.Duration(s.RetryDelay) > maxRetryDelay {
		add("status.retry_delay: must be between 0 and %s", maxRetryDelay)
	}
}
