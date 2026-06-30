// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package sessions implements google.golang.org/adk/session.Service
// backed by a SQLite database.
package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/session"

	"github.com/achetronic/baifo/internal/storage"
)

// ErrSessionNotFound is returned by mutating operations (SetTitle,
// Delete) when the targeted session does not exist, so callers can
// distinguish "nothing matched" from a successful no-op instead of
// reporting a false success. See issue #4.
var ErrSessionNotFound = errors.New("session not found")

// Service is the SQLite-backed implementation of session.Service.
// Construct with New, hand the result to a runner.Config.
type Service struct {
	db *storage.DB

	// appendHook is an optional callback fired after every
	// successful AppendEvent.
	appendHook AppendHookFunc
}

// AppendHookFunc is the callback signature for SetAppendHook.
type AppendHookFunc func(entry IndexEntry)

// SetAppendHook installs (or clears, with nil) the AppendEvent callback.
func (s *Service) SetAppendHook(fn AppendHookFunc) {
	s.appendHook = fn
}

// New wraps the given storage.DB database.
func New(db *storage.DB) (*Service, error) {
	if db == nil {
		return nil, errors.New("nil storage db")
	}
	return &Service{db: db}, nil
}

// SetTitle updates the title of an existing session in the index.
// Returns ErrSessionNotFound when no row matches, so renaming a
// missing session surfaces an error instead of a silent success.
func (s *Service) SetTitle(ctx context.Context, appName, userID, sessionID, title string) error {
	res, err := s.db.SQL().ExecContext(ctx, `
		UPDATE sessions SET title = ? WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, title, appName, userID, sessionID)
	if err != nil {
		return fmt.Errorf("set title: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set title rows affected: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// GetIndexEntry returns the single IndexEntry for the named session.
func (s *Service) GetIndexEntry(ctx context.Context, appName, userID, sessionID string) (IndexEntry, bool, error) {
	var entry IndexEntry
	var createdAtStr, lastAtStr string
	err := s.db.SQL().QueryRowContext(ctx, `
		SELECT session_id, app_name, user_id, title, created_at, last_at, msg_count
		FROM sessions
		WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, appName, userID, sessionID).Scan(&entry.ID, &entry.AppName, &entry.UserID, &entry.Title, &createdAtStr, &lastAtStr, &entry.MsgCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IndexEntry{}, false, nil
		}
		return IndexEntry{}, false, fmt.Errorf("get index entry: %w", err)
	}

	entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
	entry.LastAt, _ = time.Parse(time.RFC3339Nano, lastAtStr)
	return entry, true, nil
}

// ListIndex returns the index entries for every session of (appName, userID), sorted by LastAt descending.
func (s *Service) ListIndex(ctx context.Context, appName, userID string) ([]IndexEntry, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT session_id, app_name, user_id, title, created_at, last_at, msg_count
		FROM sessions
		WHERE app_name = ? AND user_id = ?;
	`, appName, userID)
	if err != nil {
		return nil, fmt.Errorf("list index: %w", err)
	}
	defer rows.Close()

	var out []IndexEntry
	for rows.Next() {
		var entry IndexEntry
		var createdAtStr, lastAtStr string
		err := rows.Scan(&entry.ID, &entry.AppName, &entry.UserID, &entry.Title, &createdAtStr, &lastAtStr, &entry.MsgCount)
		if err != nil {
			return nil, fmt.Errorf("scan index entry: %w", err)
		}
		entry.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAtStr)
		entry.LastAt, _ = time.Parse(time.RFC3339Nano, lastAtStr)
		out = append(out, entry)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].LastAt.After(out[j].LastAt)
	})
	return out, nil
}

// MostRecent returns the index entry of the most recently used session.
func (s *Service) MostRecent(ctx context.Context, appName, userID string) (IndexEntry, bool, error) {
	entries, err := s.ListIndex(ctx, appName, userID)
	if err != nil {
		return IndexEntry{}, false, err
	}
	if len(entries) == 0 {
		return IndexEntry{}, false, nil
	}
	return entries[0], true, nil
}

// ─── session.Service implementation ──────────────────────────────────

var _ session.Service = (*Service)(nil)

// Create implements session.Service.
func (s *Service) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	id := req.SessionID
	if id == "" {
		id = uuid.NewString()
	}

	_, sessionDelta := splitState(req.State)
	stateBytes, err := json.Marshal(sessionDelta)
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	_, err = s.db.SQL().ExecContext(ctx, `
		INSERT INTO sessions (app_name, user_id, session_id, title, created_at, last_at, msg_count, state)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?);
	`, req.AppName, req.UserID, id, "", nowStr, nowStr, string(stateBytes))
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	sess := &sqliteSession{
		svc:            s,
		id:             id,
		appName:        req.AppName,
		userID:         req.UserID,
		state:          newState(sessionDelta),
		events:         newEvents(nil),
		lastUpdateTime: now,
	}
	return &session.CreateResponse{Session: sess}, nil
}

// Get implements session.Service.
func (s *Service) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	var stateStr, lastAtStr string
	err := s.db.SQL().QueryRowContext(ctx, `
		SELECT state, last_at FROM sessions
		WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, req.AppName, req.UserID, req.SessionID).Scan(&stateStr, &lastAtStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("session not found: %s", req.SessionID)
		}
		return nil, fmt.Errorf("query session: %w", err)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(stateStr), &state); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}

	lastAt, _ := time.Parse(time.RFC3339Nano, lastAtStr)

	// Query events
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT event_data FROM session_events
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		ORDER BY event_index ASC;
	`, req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []*session.Event
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		var ev session.Event
		if err := json.Unmarshal(data, &ev); err == nil {
			events = append(events, &ev)
		}
	}

	if req.NumRecentEvents > 0 && len(events) > req.NumRecentEvents {
		events = events[len(events)-req.NumRecentEvents:]
	}
	if !req.After.IsZero() {
		filtered := events[:0]
		for _, e := range events {
			if !e.Timestamp.Before(req.After) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}

	sess := &sqliteSession{
		svc:            s,
		id:             req.SessionID,
		appName:        req.AppName,
		userID:         req.UserID,
		state:          newState(state),
		events:         newEvents(events),
		lastUpdateTime: lastAt,
	}
	return &session.GetResponse{Session: sess}, nil
}

// List implements session.Service.
func (s *Service) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	rows, err := s.db.SQL().QueryContext(ctx, `
		SELECT session_id, state, last_at FROM sessions
		WHERE app_name = ? AND user_id = ?;
	`, req.AppName, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []session.Session
	for rows.Next() {
		var id, stateStr, lastAtStr string
		if err := rows.Scan(&id, &stateStr, &lastAtStr); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		var state map[string]any
		_ = json.Unmarshal([]byte(stateStr), &state)
		lastAt, _ := time.Parse(time.RFC3339Nano, lastAtStr)

		sessions = append(sessions, &sqliteSession{
			svc:            s,
			id:             id,
			appName:        req.AppName,
			userID:         req.UserID,
			state:          newState(state),
			events:         newEvents(nil),
			lastUpdateTime: lastAt,
		})
	}
	return &session.ListResponse{Sessions: sessions}, nil
}

// Delete implements session.Service.
func (s *Service) Delete(ctx context.Context, req *session.DeleteRequest) error {
	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE app_name = ? AND user_id = ? AND session_id = ?;", req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return fmt.Errorf("delete session row: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete session rows affected: %w", err)
	}
	if n == 0 {
		return ErrSessionNotFound
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM session_events WHERE app_name = ? AND user_id = ? AND session_id = ?;", req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return fmt.Errorf("delete session events: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete tx: %w", err)
	}
	return nil
}

// AppendEvent implements session.Service.
func (s *Service) AppendEvent(ctx context.Context, sess session.Session, evt *session.Event) error {
	if evt == nil {
		return errors.New("event is nil")
	}
	if evt.LLMResponse.Partial {
		return nil
	}
	trimTempState(evt)

	appName, userID, id := sess.AppName(), sess.UserID(), sess.ID()
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	tx, err := s.db.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin append tx: %w", err)
	}
	defer tx.Rollback()

	// Get next event_index
	var nextIdx int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(event_index), 0) + 1 FROM session_events
		WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, appName, userID, id).Scan(&nextIdx)
	if err != nil {
		return fmt.Errorf("get next event index: %w", err)
	}

	// Insert event
	_, err = tx.ExecContext(ctx, `
		INSERT INTO session_events (app_name, user_id, session_id, event_index, event_data)
		VALUES (?, ?, ?, ?, ?);
	`, appName, userID, id, nextIdx, data)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	// Get current index metadata
	var indexTitle, indexCreatedStr, indexStateStr string
	var indexMsgCount int
	err = tx.QueryRowContext(ctx, `
		SELECT title, created_at, msg_count, state FROM sessions
		WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, appName, userID, id).Scan(&indexTitle, &indexCreatedStr, &indexMsgCount, &indexStateStr)
	if err != nil {
		return fmt.Errorf("query session index: %w", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	var sessionState map[string]any
	_ = json.Unmarshal([]byte(indexStateStr), &sessionState)
	if len(evt.Actions.StateDelta) > 0 {
		if sessionState == nil {
			sessionState = make(map[string]any, len(evt.Actions.StateDelta))
		}
		for k, v := range evt.Actions.StateDelta {
			sessionState[k] = v
		}
	}
	newStateBytes, _ := json.Marshal(sessionState)

	// Update sessions table
	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET last_at = ?, msg_count = msg_count + 1, state = ?
		WHERE app_name = ? AND user_id = ? AND session_id = ?;
	`, nowStr, string(newStateBytes), appName, userID, id)
	if err != nil {
		return fmt.Errorf("update sessions row: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit append tx: %w", err)
	}

	// Update in-memory snap
	if b, ok := sess.(*sqliteSession); ok && b.events != nil {
		b.events.append(evt)
		b.lastUpdateTime = evt.Timestamp
	}

	if hook := s.appendHook; hook != nil {
		entry, found, err := s.GetIndexEntry(ctx, appName, userID, id)
		if err == nil && found {
			hook(entry)
		}
	}

	return nil
}
