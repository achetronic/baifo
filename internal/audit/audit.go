// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package audit records tool calls into the SQLite audit table.
package audit

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/achetronic/baifo/internal/storage"
)

// Entry is one row of the audit log.
type Entry struct {
	Timestamp  time.Time `json:"timestamp"`
	AgentID    string    `json:"agent_id"`
	ToolName   string    `json:"tool_name"`
	Args       any       `json:"args"`             // already redacted
	Result     any       `json:"result,omitempty"` // already redacted, omitempty for failures
	Error      string    `json:"error,omitempty"`  // non-empty when the tool failed
	DurationMs int64     `json:"duration_ms"`
}

// Recorder appends entries to the SQLite audit table.
type Recorder struct {
	db *storage.DB
}

// NewRecorder returns a Recorder bound to the storage layer's audit table.
func NewRecorder(db *storage.DB) *Recorder {
	return &Recorder{db: db}
}

// Record persists one entry. Returns nil immediately on a nil Recorder.
func (r *Recorder) Record(e Entry) error {
	if r == nil {
		return nil
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}

	argsBytes, err := json.Marshal(e.Args)
	if err != nil {
		return fmt.Errorf("encode args: %w", err)
	}

	resultBytes, err := json.Marshal(e.Result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}

	tsStr := e.Timestamp.Format(time.RFC3339Nano)

	_, err = r.db.SQL().Exec(`
		INSERT INTO audit (timestamp, agent_id, tool_name, args, result, error, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, tsStr, e.AgentID, e.ToolName, string(argsBytes), string(resultBytes), e.Error, e.DurationMs)
	if err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}

	return nil
}
