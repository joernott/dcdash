// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows && !plan9

package audit

import (
	"encoding/json"
	"log/syslog"
)

// SyslogSink sends each entry as JSON to syslog (facility authpriv, severity
// info; denied and failed actions with severity warning).
type SyslogSink struct {
	w *syslog.Writer
}

// NewSyslogSink connects to syslog. An empty network and address use the
// local syslog daemon; otherwise network is "udp", "tcp" or "unix".
func NewSyslogSink(network, address, tag string) (*SyslogSink, error) {
	w, err := syslog.Dial(network, address, syslog.LOG_AUTHPRIV|syslog.LOG_INFO, tag)
	if err != nil {
		return nil, err
	}
	return &SyslogSink{w: w}, nil
}

// Write implements Sink.
func (s *SyslogSink) Write(e Entry) error {
	msg, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if e.Result == ResultSuccess {
		return s.w.Info(string(msg))
	}
	return s.w.Warning(string(msg))
}

// Close closes the connection.
func (s *SyslogSink) Close() error { return s.w.Close() }
