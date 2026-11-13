// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows || plan9

package audit

import "errors"

// SyslogSink is not available on this platform.
type SyslogSink struct{}

// NewSyslogSink always fails on this platform.
func NewSyslogSink(_, _, _ string) (*SyslogSink, error) {
	return nil, errors.New("syslog is not supported on this platform")
}

// Write implements Sink.
func (*SyslogSink) Write(Entry) error { return nil }

// Close does nothing.
func (*SyslogSink) Close() error { return nil }
