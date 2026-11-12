// SPDX-License-Identifier: GPL-3.0-or-later

package certs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/testutil"
)

func leafNames(t *testing.T, m *FileManager) []string {
	t.Helper()
	c, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Leaf.DNSNames
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileManagerReload(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := testutil.WriteCert(t, dir, testutil.CertOptions{DNSNames: []string{"old.example"}})
	newCert, newKey := testutil.WriteCert(t, dir, testutil.CertOptions{DNSNames: []string{"new.example"}, Name: "new"})

	m, err := NewFileManager(certFile, keyFile, 0, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if got := leafNames(t, m); got[0] != "old.example" {
		t.Fatalf("initial: %v", got)
	}

	// Unchanged files: nothing happens.
	before, _ := m.GetCertificate(nil)
	m.reload()
	if after, _ := m.GetCertificate(nil); after != before {
		t.Error("certificate reloaded although files are unchanged")
	}

	// Only the certificate replaced: key does not match, old certificate stays.
	copyFile(t, newCert, certFile)
	m.reload()
	if got := leafNames(t, m); got[0] != "old.example" {
		t.Errorf("after inconsistent update: %v", got)
	}

	// Key replaced as well: new certificate is loaded.
	copyFile(t, newKey, keyFile)
	m.reload()
	if got := leafNames(t, m); got[0] != "new.example" {
		t.Errorf("after complete update: %v", got)
	}

	// Files removed: old certificate stays.
	_ = os.Remove(certFile)
	m.reload()
	if got := leafNames(t, m); got[0] != "new.example" {
		t.Errorf("after removal: %v", got)
	}
}

func TestFileManagerRunReloads(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := testutil.WriteCert(t, dir, testutil.CertOptions{DNSNames: []string{"old.example"}})
	newCert, newKey := testutil.WriteCert(t, dir, testutil.CertOptions{DNSNames: []string{"new.example"}, Name: "new"})

	m, err := NewFileManager(certFile, keyFile, 10*time.Millisecond, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	copyFile(t, newKey, keyFile)
	copyFile(t, newCert, certFile)
	deadline := time.Now().Add(5 * time.Second)
	for leafNames(t, m)[0] != "new.example" {
		if time.Now().After(deadline) {
			t.Fatal("certificate was not reloaded")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
}
