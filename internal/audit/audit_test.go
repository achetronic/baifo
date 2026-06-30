// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"testing"
	"time"

	"github.com/achetronic/baifo/internal/storage"
)

func newRecorder(t *testing.T) (*Recorder, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRecorder(db), db
}

func TestRecordPersistsJSON(t *testing.T) {
	r, db := newRecorder(t)

	e := Entry{
		AgentID:    "root",
		ToolName:   "ls",
		Args:       map[string]any{"path": "/etc"},
		Result:     "ok",
		DurationMs: 12,
	}
	if err := r.Record(e); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var storedAgentID, storedToolName, storedArgsStr, storedResultStr, tsStr string
	var storedDurationMs int64
	err := db.SQL().QueryRow(`
		SELECT timestamp, agent_id, tool_name, args, result, duration_ms
		FROM audit LIMIT 1;
	`).Scan(&tsStr, &storedAgentID, &storedToolName, &storedArgsStr, &storedResultStr, &storedDurationMs)
	if err != nil {
		t.Fatalf("query row: %v", err)
	}

	if storedToolName != "ls" {
		t.Errorf("ToolName: got %q, want %q", storedToolName, "ls")
	}
	if tsStr == "" {
		t.Error("Timestamp should be auto-populated")
	}
}

func TestRecordKeysPreserveInsertionOrder(t *testing.T) {
	r, db := newRecorder(t)

	// Insert three entries with the SAME timestamp on purpose, so we
	// can confirm SQLite autoincrement ID breaks the tie.
	ts := time.Now().UTC()
	for _, name := range []string{"first", "second", "third"} {
		if err := r.Record(Entry{Timestamp: ts, ToolName: name}); err != nil {
			t.Fatalf("Record %s: %v", name, err)
		}
	}

	rows, err := db.SQL().Query("SELECT tool_name FROM audit ORDER BY id ASC;")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, name)
	}

	want := []string{"first", "second", "third"}
	if len(names) != len(want) {
		t.Fatalf("len: got %d, want %d", len(names), len(want))
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, names[i], want[i])
		}
	}
}

func TestNilRecorderIsNoOp(t *testing.T) {
	var r *Recorder
	if err := r.Record(Entry{ToolName: "x"}); err != nil {
		t.Errorf("nil Recorder should silently succeed, got %v", err)
	}
}
