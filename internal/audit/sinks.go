// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"encoding/json"
	"os"
	"sync"
)

// FileSink appends each entry as a JSON line to a file. The file is opened for
// every entry, so it can be rotated (e.g. by logrotate) without a restart.
type FileSink struct {
	path string
	mu   sync.Mutex
}

// NewFileSink creates a file sink. The file is created with mode 0600.
func NewFileSink(path string) *FileSink { return &FileSink{path: path} }

// Write implements Sink.
func (s *FileSink) Write(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- path is given by the administrator
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
