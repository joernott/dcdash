// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenMigratesAndReopens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s := open(t, dir)
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SchemaVersion(context.Background())
	if err != nil || v != ms[len(ms)-1].version {
		t.Fatalf("schema version %d, %v", v, err)
	}
	if _, err := s.Check(context.Background()); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := s.SetMeta(context.Background(), "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	// Reopening applies nothing new and keeps the data.
	s2 := open(t, dir)
	if got, err := s2.GetMeta(context.Background(), "k"); err != nil || string(got) != "v1" {
		t.Errorf("GetMeta after reopen: %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(s2.Path())
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("database file mode %o, want 600", perm)
		}
	}
}

func TestMeta(t *testing.T) {
	s := open(t, t.TempDir())
	ctx := context.Background()
	if _, err := s.GetMeta(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
	for _, v := range []string{"one", "two"} {
		if err := s.SetMeta(ctx, "k", []byte(v)); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.GetMeta(ctx, "k"); string(got) != "two" {
		t.Errorf("got %q", got)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if _, err := s.DB().Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future', 'now')`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(context.Background(), dir); err == nil {
		t.Fatal("expected error for newer schema")
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	s := open(t, t.TempDir())
	var on int
	if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil || on != 1 {
		t.Errorf("foreign_keys = %d, %v", on, err)
	}
	var mode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v", mode, err)
	}
}
