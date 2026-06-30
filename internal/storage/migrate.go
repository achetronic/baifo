// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"fmt"
	"strconv"
)

// migrate applies pending database schema migrations. Connection-level
// PRAGMAs (journal_mode, busy_timeout, synchronous, foreign_keys) are
// set via the DSN in Open so they apply to every pooled connection;
// they are deliberately NOT set here, where a bare Exec would only
// touch a single connection.
func migrate(db *sql.DB) error {
	// Ensure meta table exists so we can read/write the schema version.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`); err != nil {
		return fmt.Errorf("create meta table: %w", err)
	}

	var versionStr string
	err := db.QueryRow("SELECT value FROM meta WHERE key = ?", schemaVersionKey).Scan(&versionStr)
	version := 0
	if err == nil {
		version, _ = strconv.Atoi(versionStr)
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("read schema version: %w", err)
	}

	// V1: Initial schema.
	if version < 1 {
		if err := migrateV1(db); err != nil {
			return err
		}
		version = 1
		if err := setVersion(db, version); err != nil {
			return err
		}
	}

	// We can add V2, V3 etc. here in the future.
	// Note: We leave the ad-hoc embedding column check in V1 because some existing
	// v1 databases might not have it and some might. In the future, a true V2 would
	// just bump the version and assume V1 has the baseline.

	// Because existing installations have version=1 but MIGHT lack the embedding column
	// (since it was added as an ad-hoc fix later without bumping the version), we must
	// still check for it independently of versioning to avoid breaking users.
	if err := ensureEmbeddingColumn(db); err != nil {
		return err
	}

	return nil
}

func setVersion(db *sql.DB, version int) error {
	_, err := db.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value;
	`, schemaVersionKey, strconv.Itoa(version))
	return err
}

func migrateV1(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			app_name TEXT,
			user_id TEXT,
			session_id TEXT,
			title TEXT,
			created_at TIMESTAMP,
			last_at TIMESTAMP,
			msg_count INTEGER,
			state TEXT,
			PRIMARY KEY (app_name, user_id, session_id)
		);`,
		`CREATE TABLE IF NOT EXISTS session_events (
			app_name TEXT,
			user_id TEXT,
			session_id TEXT,
			event_index INTEGER,
			event_data BLOB,
			PRIMARY KEY (app_name, user_id, session_id, event_index)
		);`,
		`CREATE TABLE IF NOT EXISTS facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			app_name TEXT,
			user_id TEXT,
			content TEXT,
			category TEXT,
			author TEXT,
			timestamp TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TIMESTAMP,
			agent_id TEXT,
			tool_name TEXT,
			args TEXT,
			result TEXT,
			error TEXT,
			duration_ms INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_tokens (
			mcp_name TEXT PRIMARY KEY,
			token_data TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			mcp_name TEXT PRIMARY KEY,
			client_data TEXT
		);`,
	}

	for i, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("v1 migration query %d: %w", i, err)
		}
	}
	return nil
}

func ensureEmbeddingColumn(db *sql.DB) error {
	var hasEmbedding bool
	rows, err := db.Query("PRAGMA table_info(facts);")
	if err != nil {
		return nil // table might not exist in some weird state, ignore
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull, pk int
		var dfltValue interface{}
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &dfltValue, &pk); err == nil {
			if name == "embedding" {
				hasEmbedding = true
			}
		}
	}

	if !hasEmbedding {
		if _, err := db.Exec("ALTER TABLE facts ADD COLUMN embedding BLOB;"); err != nil {
			return fmt.Errorf("add embedding column: %w", err)
		}
	}
	return nil
}
