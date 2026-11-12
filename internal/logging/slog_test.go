// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestSlogHandler(t *testing.T) {
	var buf bytes.Buffer
	zl := zerolog.New(&buf).Level(zerolog.InfoLevel)
	l := slog.New(NewSlogHandler(zl)).With("component", "acme")

	l.Debug("hidden")
	l.WithGroup("req").Info("obtaining",
		"domains", []string{"a.example"},
		"n", 3,
		"ok", true,
		"wait", 2*time.Second,
		"err", errors.New("boom"),
		slog.Group("tls", "version", "1.3"),
	)
	l.Error("failed")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %q", buf.String())
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"level": "info", "message": "obtaining", "component": "acme",
		"req.n": float64(3), "req.ok": true, "req.err": "boom", "req.tls.version": "1.3",
	}
	for k, v := range want {
		if e[k] != v {
			t.Errorf("%s = %v, want %v (entry %v)", k, e[k], v, e)
		}
	}
	if !strings.Contains(lines[1], `"level":"error"`) {
		t.Errorf("unexpected second line: %s", lines[1])
	}
}

func TestSlogLevels(t *testing.T) {
	for in, want := range map[slog.Level]zerolog.Level{
		slog.LevelDebug: zerolog.DebugLevel,
		slog.LevelInfo:  zerolog.InfoLevel,
		slog.LevelWarn:  zerolog.WarnLevel,
		slog.LevelError: zerolog.ErrorLevel,
	} {
		if got := zerologLevel(in); got != want {
			t.Errorf("%v -> %v, want %v", in, got, want)
		}
	}
}
