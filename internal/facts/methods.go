// SPDX-License-Identifier: Apache-2.0

package facts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/achetronic/adk-utils-go/memory/memorytypes"
)

// ErrNotFound is returned when an entry id is unknown.
var ErrNotFound = errors.New("memory entry not found")

// AddSessionToMemory persists every textual event of the given session
// as a FactEntry.
func (s *Store) AddSessionToMemory(ctx context.Context, sess session.Session) error {
	if sess == nil {
		return errors.New("session is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for ev := range sess.Events().All() {
		if ev == nil || ev.Content == nil {
			continue
		}
		text := contentText(ev.Content)
		if text == "" {
			continue
		}
		ts := ev.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		tsStr := ts.Format(time.RFC3339Nano)

		var embBytes []byte
		if s.eng != nil {
			if v, err := s.eng.EmbedNormalized("search_document: " + text); err == nil {
				embBytes = encodeEmbedding(v)
			}
		}

		_, err := s.db.SQL().ExecContext(ctx, `
			INSERT INTO facts (app_name, user_id, content, category, author, timestamp, embedding)
			VALUES (?, ?, ?, ?, ?, ?, ?);
		`, sess.AppName(), sess.UserID(), text, "", ev.Author, tsStr, embBytes)
		if err != nil {
			return fmt.Errorf("write entry: %w", err)
		}
	}
	return nil
}

// SearchMemory ranks entries by semantic similarity to the query when an
// embeddings engine is configured, falling back to a case-insensitive
// substring match across content, category and author otherwise.
func (s *Store) SearchMemory(ctx context.Context, req *memory.SearchRequest) (*memory.SearchResponse, error) {
	if req == nil || req.Query == "" {
		return nil, errors.New("query is required")
	}
	entries, err := s.searchEntries(ctx, req.AppName, req.UserID, req.Query)
	if err != nil {
		return nil, err
	}
	out := &memory.SearchResponse{}
	for _, e := range entries {
		out.Memories = append(out.Memories, memory.Entry{
			ID:        fmt.Sprintf("%d", e.ID),
			Author:    e.Author,
			Timestamp: e.Timestamp,
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: e.Content}},
			},
		})
	}
	return out, nil
}

// SearchWithID is the ExtendedMemoryService variant.
func (s *Store) SearchWithID(ctx context.Context, req *memory.SearchRequest) ([]memorytypes.EntryWithID, error) {
	if req == nil || req.Query == "" {
		return nil, errors.New("query is required")
	}
	entries, err := s.searchEntries(ctx, req.AppName, req.UserID, req.Query)
	if err != nil {
		return nil, err
	}
	out := make([]memorytypes.EntryWithID, 0, len(entries))
	for _, e := range entries {
		out = append(out, memorytypes.EntryWithID{
			ID:        int(e.ID),
			Author:    e.Author,
			Timestamp: e.Timestamp,
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: e.Content}},
			},
		})
	}
	return out, nil
}

// AddManualEntry inserts a brand-new fact without going through a session.
func (s *Store) AddManualEntry(ctx context.Context, appName, userID, content, author, category string) (uint64, error) {
	if content == "" {
		return 0, errors.New("content cannot be empty")
	}
	if appName == "" || userID == "" {
		return 0, errors.New("appName and userID are required")
	}
	if author == "" {
		author = "user"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tsStr := time.Now().UTC().Format(time.RFC3339Nano)

	var embBytes []byte
	if s.eng != nil {
		if v, err := s.eng.EmbedNormalized("search_document: " + content); err == nil {
			embBytes = encodeEmbedding(v)
		}
	}

	res, err := s.db.SQL().ExecContext(ctx, `
		INSERT INTO facts (app_name, user_id, content, category, author, timestamp, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, appName, userID, content, category, author, tsStr, embBytes)
	if err != nil {
		return 0, fmt.Errorf("write entry: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get last insert id: %w", err)
	}
	return uint64(id), nil
}

// UpdateMemory replaces the content of an existing entry.
func (s *Store) UpdateMemory(ctx context.Context, appName, userID string, entryID int, newContent string) error {
	if newContent == "" {
		return errors.New("content cannot be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tsStr := time.Now().UTC().Format(time.RFC3339Nano)

	var embBytes []byte
	if s.eng != nil {
		if v, err := s.eng.EmbedNormalized("search_document: " + newContent); err == nil {
			embBytes = encodeEmbedding(v)
		}
	}

	res, err := s.db.SQL().ExecContext(ctx, `
		UPDATE facts SET content = ?, timestamp = ?, embedding = ?
		WHERE app_name = ? AND user_id = ? AND id = ?;
	`, newContent, tsStr, embBytes, appName, userID, entryID)
	if err != nil {
		return fmt.Errorf("update entry: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMemory removes an entry by id.
func (s *Store) DeleteMemory(ctx context.Context, appName, userID string, entryID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.SQL().ExecContext(ctx, `
		DELETE FROM facts
		WHERE app_name = ? AND user_id = ? AND id = ?;
	`, appName, userID, entryID)
	if err != nil {
		return fmt.Errorf("delete entry: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns every entry of (app, user), newest first.
func (s *Store) List(appName, userID string) ([]FactEntry, error) {
	return s.searchEntries(context.Background(), appName, userID, "")
}
