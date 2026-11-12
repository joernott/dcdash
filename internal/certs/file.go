// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// FileManager serves a certificate and key loaded from PEM files and reloads
// them when their content changes (e.g. after renewal by an external tool).
type FileManager struct {
	holder
	certFile, keyFile string
	interval          time.Duration
	log               zerolog.Logger
	now               func() time.Time
	fingerprint       [sha256.Size]byte
}

// NewFileManager loads certFile and keyFile. It fails if they cannot be loaded,
// so that a misconfiguration is detected at startup. The files are checked for
// changes every interval; an interval <= 0 disables reloading.
func NewFileManager(certFile, keyFile string, interval time.Duration, log zerolog.Logger) (*FileManager, error) {
	m := &FileManager{
		certFile: certFile,
		keyFile:  keyFile,
		interval: interval,
		log:      log.With().Str("component", "certs").Logger(),
		now:      time.Now,
	}
	fp, err := m.readFingerprint()
	if err != nil {
		return nil, err
	}
	if err := m.load(fp); err != nil {
		return nil, err
	}
	return m, nil
}

// readFingerprint hashes both files, so that any change to either is detected,
// including atomic replacements and symlink swaps.
func (m *FileManager) readFingerprint() ([sha256.Size]byte, error) {
	h := sha256.New()
	for _, f := range []string{m.certFile, m.keyFile} {
		b, err := os.ReadFile(f) // #nosec G304 -- paths are given by the administrator
		if err != nil {
			return [sha256.Size]byte{}, fmt.Errorf("read %s: %w", f, err)
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	var fp [sha256.Size]byte
	copy(fp[:], h.Sum(nil))
	return fp, nil
}

func (m *FileManager) load(fp [sha256.Size]byte) error {
	c, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	if err != nil {
		return fmt.Errorf("load certificate %s / key %s: %w", m.certFile, m.keyFile, err)
	}
	if err := m.set(&c); err != nil {
		return err
	}
	m.fingerprint = fp
	m.log.Info().
		Str("cert_file", m.certFile).
		Strs("dns_names", c.Leaf.DNSNames).
		Time("not_after", c.Leaf.NotAfter).
		Msg("certificate loaded")
	return nil
}

// reload loads the files again if their content changed. On failure the
// previous certificate stays in use.
func (m *FileManager) reload() {
	fp, err := m.readFingerprint()
	if err != nil {
		m.log.Warn().Err(err).Msg("cannot read certificate files, keeping current certificate")
		return
	}
	if fp == m.fingerprint {
		return
	}
	if err := m.load(fp); err != nil {
		// Typical while a tool writes certificate and key one after the other;
		// the next check picks up the consistent state.
		m.log.Warn().Err(err).Msg("cannot load changed certificate files, keeping current certificate")
	}
}

// Run checks the files for changes until ctx is canceled.
func (m *FileManager) Run(ctx context.Context) error {
	if m.interval <= 0 {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			m.reload()
		}
	}
}

// Check implements Manager.
func (m *FileManager) Check(context.Context) (string, error) { return m.status(m.now()) }
