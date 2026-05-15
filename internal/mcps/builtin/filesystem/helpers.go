// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package filesystem

import (
	"fmt"
	"path/filepath"
	"strings"
)

// sanitizePath rejects shell brace-expansion patterns like {a,b} which
// agents sometimes try to use. The tool exec one shell command, so the
// brace would never be expanded the way the model expects.
//
// Lifted verbatim from filesystem-mcp/internal/tools/helpers.go.
func sanitizePath(path string) error {
	openIdx := strings.Index(path, "{")
	if openIdx == -1 {
		return nil
	}
	closeIdx := strings.Index(path[openIdx:], "}")
	if closeIdx == -1 {
		return fmt.Errorf("path contains an unclosed brace expansion pattern: %s", path)
	}
	inner := path[openIdx+1 : openIdx+closeIdx]
	if strings.Contains(inner, ",") {
		return fmt.Errorf("path contains a shell brace expansion pattern: %s — expand it into individual paths before calling this tool", path)
	}
	return nil
}

// resolvePath validates path with sanitizePath and returns its absolute
// form. Centralised here so every tool applies the same rules.
func resolvePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if err := sanitizePath(path); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	return abs, nil
}
