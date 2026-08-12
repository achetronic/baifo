// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package facts

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/storage"
)

// newTestStore opens a fresh SQLite DB in a temp dir and returns a
// Store backed by it. The DB is closed via t.Cleanup.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, nil)
}

// fakeEvents is a minimal session.Events implementation used to fake
// a single-event session from the memory toolset.
type fakeEvents struct {
	events []*session.Event
}

func (f *fakeEvents) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, e := range f.events {
			if !yield(e) {
				return
			}
		}
	}
}
func (f *fakeEvents) Len() int                { return len(f.events) }
func (f *fakeEvents) At(i int) *session.Event { return f.events[i] }

// fakeSession is a minimal session.Session with a single user-authored
// text event, mirroring what the memory toolset hands to AddSession.
type fakeSession struct {
	id, app, user string
	events        *fakeEvents
}

func (s *fakeSession) ID() string                { return s.id }
func (s *fakeSession) AppName() string           { return s.app }
func (s *fakeSession) UserID() string            { return s.user }
func (s *fakeSession) State() session.State      { return nil }
func (s *fakeSession) Events() session.Events    { return s.events }
func (s *fakeSession) LastUpdateTime() time.Time { return time.Now() }

func mkSession(id, content string) *fakeSession {
	ev := &session.Event{
		Author:    "user",
		Timestamp: time.Now().UTC(),
	}
	ev.Content = &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: content}},
	}
	return &fakeSession{
		id:     id,
		app:    "baifo",
		user:   "alby",
		events: &fakeEvents{events: []*session.Event{ev}},
	}
}

func TestAddAndSearchRoundTrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddSessionToMemory(context.Background(), mkSession("m1", "alby loves Go")); err != nil {
		t.Fatalf("AddSessionToMemory: %v", err)
	}
	resp, err := s.SearchMemory(context.Background(), &memory.SearchRequest{
		AppName: "baifo", UserID: "alby", Query: "alby",
	})
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 match, got %d", len(resp.Memories))
	}
	got := resp.Memories[0].Content.Parts[0].Text
	if got != "alby loves Go" {
		t.Errorf("content: got %q", got)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m", "Baifo is a personal harness"))

	resp, err := s.SearchMemory(context.Background(), &memory.SearchRequest{
		AppName: "baifo", UserID: "alby", Query: "baifo",
	})
	if err != nil {
		t.Fatalf("SearchMemory: %v", err)
	}
	if len(resp.Memories) != 1 {
		t.Errorf("expected case-insensitive match, got %d hits", len(resp.Memories))
	}
}

func TestSearchEmptyQueryRejected(t *testing.T) {
	s := newTestStore(t)
	_, err := s.SearchMemory(context.Background(), &memory.SearchRequest{
		AppName: "baifo", UserID: "alby",
	})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestListReturnsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m1", "old"))
	time.Sleep(2 * time.Millisecond)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m2", "new"))

	out, err := s.List("baifo", "alby")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	if out[0].Content != "new" || out[1].Content != "old" {
		t.Errorf("ordering: got %+v", out)
	}
}

func TestUpdateMemoryReplacesContent(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m", "original"))

	entries, _ := s.List("baifo", "alby")
	id := int(entries[0].ID)

	if err := s.UpdateMemory(context.Background(), "baifo", "alby", id, "updated"); err != nil {
		t.Fatalf("UpdateMemory: %v", err)
	}

	entries, _ = s.List("baifo", "alby")
	if entries[0].Content != "updated" {
		t.Errorf("update did not persist: %q", entries[0].Content)
	}
}

func TestUpdateMemoryRejectsUnknownID(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateMemory(context.Background(), "baifo", "alby", 9999, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestDeleteMemoryRemovesEntry(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m", "to be removed"))

	entries, _ := s.List("baifo", "alby")
	id := int(entries[0].ID)

	if err := s.DeleteMemory(context.Background(), "baifo", "alby", id); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	entries, _ = s.List("baifo", "alby")
	if len(entries) != 0 {
		t.Errorf("entry not deleted: %d remaining", len(entries))
	}
}

func TestSearchWithIDExposesNumericID(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddSessionToMemory(context.Background(), mkSession("m", "marker"))

	res, err := s.SearchWithID(context.Background(), &memory.SearchRequest{
		AppName: "baifo", UserID: "alby", Query: "marker",
	})
	if err != nil {
		t.Fatalf("SearchWithID: %v", err)
	}
	if len(res) != 1 || res[0].ID <= 0 {
		t.Errorf("expected one entry with positive id, got %+v", res)
	}
}
