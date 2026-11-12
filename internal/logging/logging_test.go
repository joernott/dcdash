// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/joernott/dcdash/internal/config"
)

func TestJSONOutputAndLevel(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(config.Log{Level: "info", Format: "json"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug().Msg("hidden")
	log.Info().Str("k", "v").Msg("shown")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if entry["message"] != "shown" || entry["k"] != "v" || entry["level"] != "info" || entry["time"] == nil {
		t.Errorf("unexpected entry: %v", entry)
	}
}

func TestConsoleOutput(t *testing.T) {
	var buf bytes.Buffer
	log, err := New(config.Log{Level: "debug", Format: "console"}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	log.Debug().Msg("hello")
	if !strings.Contains(buf.String(), "hello") || strings.HasPrefix(buf.String(), "{") {
		t.Errorf("unexpected console output: %q", buf.String())
	}
}

func TestInvalidSettings(t *testing.T) {
	if _, err := New(config.Log{Level: "loud", Format: "json"}, &bytes.Buffer{}); err == nil {
		t.Error("expected level error")
	}
	if _, err := New(config.Log{Level: "info", Format: "xml"}, &bytes.Buffer{}); err == nil {
		t.Error("expected format error")
	}
}
