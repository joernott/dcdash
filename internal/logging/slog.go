// SPDX-License-Identifier: GPL-3.0-or-later

package logging

import (
	"context"
	"log/slog"
	"strings"

	"github.com/rs/zerolog"
)

// NewSlogHandler returns a slog.Handler that writes to a zerolog logger, so
// that libraries logging via log/slog (e.g. lego) use the dcdash log format.
func NewSlogHandler(l zerolog.Logger) slog.Handler {
	return &slogHandler{log: l}
}

type slogHandler struct {
	log    zerolog.Logger
	attrs  []slog.Attr
	groups []string
}

func zerologLevel(l slog.Level) zerolog.Level {
	switch {
	case l < slog.LevelInfo:
		return zerolog.DebugLevel
	case l < slog.LevelWarn:
		return zerolog.InfoLevel
	case l < slog.LevelError:
		return zerolog.WarnLevel
	default:
		return zerolog.ErrorLevel
	}
}

func (h *slogHandler) Enabled(_ context.Context, l slog.Level) bool {
	return zerologLevel(l) >= h.log.GetLevel()
}

func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	ev := h.log.WithLevel(zerologLevel(r.Level))
	if ev == nil {
		return nil
	}
	for _, a := range h.attrs {
		addAttr(ev, "", a) // keys already carry the group prefix from WithAttrs
	}
	prefix := strings.Join(h.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		addAttr(ev, prefix, a)
		return true
	})
	ev.Msg(r.Message)
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Attributes added here belong to the current group.
	prefixed := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	prefixed = append(prefixed, h.attrs...)
	for _, a := range attrs {
		if len(h.groups) > 0 {
			a.Key = strings.Join(h.groups, ".") + "." + a.Key
		}
		prefixed = append(prefixed, a)
	}
	return &slogHandler{log: h.log, attrs: prefixed, groups: h.groups}
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := append(append([]string{}, h.groups...), name)
	// Already prefixed attributes keep their keys; only later ones get the group.
	return &slogHandler{log: h.log, attrs: h.attrs, groups: groups}
}

func addAttr(ev *zerolog.Event, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" && !strings.HasPrefix(key, prefix+".") {
		key = prefix + "." + key
	}
	v := a.Value
	switch v.Kind() {
	case slog.KindGroup:
		for _, g := range v.Group() {
			addAttr(ev, key, g)
		}
	case slog.KindString:
		ev.Str(key, v.String())
	case slog.KindInt64:
		ev.Int64(key, v.Int64())
	case slog.KindUint64:
		ev.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		ev.Float64(key, v.Float64())
	case slog.KindBool:
		ev.Bool(key, v.Bool())
	case slog.KindDuration:
		ev.Dur(key, v.Duration())
	case slog.KindTime:
		ev.Time(key, v.Time())
	default:
		if err, ok := v.Any().(error); ok {
			ev.AnErr(key, err)
		} else {
			ev.Interface(key, v.Any())
		}
	}
}
