// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNoopRedactor(t *testing.T) {
	redactor := &NoopRedactor{}

	attr := slog.String("secret", "my-super-secret-value")
	redacted := redactor.Redact(attr)

	if redacted.Value.String() != "my-super-secret-value" {
		t.Errorf("Expected NoopRedactor to leave value unchanged, got: %s", redacted.Value.String())
	}
}

func TestRedactedHandler(t *testing.T) {
	var buf bytes.Buffer
	baseHandler := slog.NewTextHandler(&buf, nil)

	handler := &redactedHandler{
		Handler:  baseHandler,
		redactor: &NoopRedactor{},
	}

	logger := slog.New(handler)
	logger.Info("test message", "key", "value")

	out := buf.String()
	if !strings.Contains(out, "test message") || !strings.Contains(out, "key=value") {
		t.Errorf("Expected log to contain message and attributes, got: %s", out)
	}
}
