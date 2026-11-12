// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// FileManager serves a certificate and key loaded from PEM files.
type FileManager struct {
	holder
	certFile, keyFile string
	interval          time.Duration
	log               zerolog.Logger
	now               func() time.Time
}

// NewFileManager loads certFile and keyFile. It fails if they cannot be loaded,
// so that a misconfiguration is detected at startup.
func NewFileManager(certFile, keyFile string, interval time.Duration, log zerolog.Logger) (*FileManager, error) {
	m := &FileManager{
		certFile: certFile,
		keyFile:  keyFile,
		interval: interval,
		log:      log.With().Str("component", "certs").Logger(),
		now:      time.Now,
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *FileManager) load() error {
	c, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	if err != nil {
		return fmt.Errorf("load certificate %s / key %s: %w", m.certFile, m.keyFile, err)
	}
	return m.set(&c)
}

// Run implements Manager. File reloading is added in task 2.2.
func (m *FileManager) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Check implements Manager.
func (m *FileManager) Check(context.Context) (string, error) { return m.status(m.now()) }
