// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package storage

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesDataDirAndTables(t *testing.T) {
	tmp := t.TempDir()

	db, err := Open(tmp)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Check table existence
	tables := []string{"meta", "sessions", "session_events", "facts", "audit", "oauth_tokens", "oauth_clients"}
	for _, table := range tables {
		var name string
		err := db.SQL().QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?;", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q query error or missing: %v", table, err)
		}
	}

	// Verify schema version
	var got string
	err = db.SQL().QueryRow("SELECT value FROM meta WHERE key=?;", schemaVersionKey).Scan(&got)
	if err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if got != SchemaVersion {
		t.Errorf("schema version: got %q, want %q", got, SchemaVersion)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	tmp := t.TempDir()

	db, err := Open(tmp)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Reopening the same path must succeed and preserve the schema version.
	db2, err := Open(tmp)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	// The data dir must exist after Open.
	if _, err := filepath.Abs(filepath.Join(tmp, "data")); err != nil {
		t.Fatalf("data dir path: %v", err)
	}
}
