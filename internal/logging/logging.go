// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package logging

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Redactor defines the interface for sanitizing log attributes
// before they are written to the sink.
type Redactor interface {
	Redact(attr slog.Attr) slog.Attr
}

// NoopRedactor is a stub that returns the attribute unchanged.
// It will be replaced by the actual secrets redactor in Phase 2.
type NoopRedactor struct{}

func (r *NoopRedactor) Redact(attr slog.Attr) slog.Attr {
	return attr
}

// redactedHandler wraps an slog.Handler to apply redaction rules.
type redactedHandler struct {
	slog.Handler
	redactor Redactor
}

func (h *redactedHandler) Handle(ctx context.Context, r slog.Record) error {
	newRecord := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		newRecord.AddAttrs(h.redactor.Redact(a))
		return true
	})
	return h.Handler.Handle(ctx, newRecord)
}

func (h *redactedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redactedAttrs := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redactedAttrs[i] = h.redactor.Redact(a)
	}
	return &redactedHandler{
		Handler:  h.Handler.WithAttrs(redactedAttrs),
		redactor: h.redactor,
	}
}

func (h *redactedHandler) WithGroup(name string) slog.Handler {
	return &redactedHandler{
		Handler:  h.Handler.WithGroup(name),
		redactor: h.redactor,
	}
}

// Init configures the global slog logger and standard log redirection.
// It returns the log *os.File pointer (or nil if discarding), and any error during file opening.
func Init(logFilePath, levelStr, formatStr string, redactor Redactor) (*os.File, error) {
	var w io.Writer = io.Discard
	var f *os.File
	var err error

	if logFilePath != "" {
		dir := filepath.Dir(logFilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		f, err = os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, err
		}
		w = f
	}

	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var baseHandler slog.Handler
	if strings.ToLower(formatStr) == "json" {
		baseHandler = slog.NewJSONHandler(w, opts)
	} else {
		baseHandler = slog.NewTextHandler(w, opts)
	}

	handler := &redactedHandler{
		Handler:  baseHandler,
		redactor: redactor,
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	log.SetOutput(w)

	return f, nil
}
