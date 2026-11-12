// SPDX-License-Identifier: GPL-3.0-or-later

// Package logging configures the zerolog logger used throughout dcdash.
package logging

import (
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog"

	"github.com/joernott/dcdash/internal/config"
)

// New creates a logger writing to w according to cfg.
func New(cfg config.Log, w io.Writer) (zerolog.Logger, error) {
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		return zerolog.Nop(), fmt.Errorf("log level: %w", err)
	}
	switch cfg.Format {
	case "json":
	case "console":
		w = zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}
	default:
		return zerolog.Nop(), fmt.Errorf("log format %q: must be json or console", cfg.Format)
	}
	zerolog.TimeFieldFormat = time.RFC3339Nano
	return zerolog.New(w).Level(level).With().Timestamp().Logger(), nil
}
