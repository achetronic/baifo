// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package storage wraps modernc.org/sqlite with baifo-specific tables and
// schema versioning. One file lives under .baifo/data/baifo.db.
package storage

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	// SchemaVersion is the current on-disk schema version, written to the
	// meta table on every Open() call.
	SchemaVersion = "1"

	// schemaVersionKey is the key under which the schema version lives in
	// the meta table.
	schemaVersionKey = "schema_version"
)

// DB wraps a sql.DB instance with baifo-specific accessors.
type DB struct {
	db *sql.DB
}

// Open opens or creates baifo.db inside <dir>/data/. The data subdirectory
// is created if missing. All tables are ensured to exist.
func Open(dir string) (*DB, error) {
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, "baifo.db")

	// PRAGMAs go in the DSN so modernc.org/sqlite applies them to
	// EVERY connection the database/sql pool opens, not just one.
	// (A bare `PRAGMA` via db.Exec only touches whichever pooled
	// connection happens to run it, leaving the rest at defaults —
	// which is how busy_timeout used to silently fail under load.)
	//
	//   busy_timeout(5000)  wait up to 5s on a locked db instead of
	//                        failing immediately with SQLITE_BUSY.
	//   journal_mode(WAL)   readers don't block the writer; persisted
	//                        in the file header but harmless to repeat.
	//   synchronous(NORMAL) the recommended durability level under WAL:
	//                        far fewer fsyncs (less stalling) while still
	//                        crash-safe, since the WAL checkpoint is the
	//                        real durability boundary.
	//   foreign_keys(ON)    enforce FK constraints (off by default in
	//                        SQLite, and it's a per-connection setting).
	dsn := "file:" + dbPath +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db := &DB{db: sqlDB}
	if err := db.init(); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			slog.Warn("failed to close database after init error", "error", closeErr)
		}
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return db, nil
}

// Close closes the underlying database.
func (db *DB) Close() error {
	return db.db.Close()
}

// SQL exposes the underlying *sql.DB.
func (db *DB) SQL() *sql.DB {
	return db.db
}

// init creates every missing table, sets PRAGMAs, and stamps the current schema version.
func (db *DB) init() error {
	return migrate(db.db)
}
