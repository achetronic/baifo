// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/achetronic/baifo/internal/storage"
)

func newSvc(t *testing.T) (*Service, *storage.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc, db
}

func TestCreateAndGetRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{
		AppName: "baifo", UserID: "u", SessionID: "s1",
		State: map[string]any{"name": "alice"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Session.ID() != "s1" {
		t.Errorf("ID: got %q, want s1", resp.Session.ID())
	}

	got, err := svc.Get(ctx, &session.GetRequest{
		AppName: "baifo", UserID: "u", SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Session.ID() != "s1" {
		t.Errorf("Get ID: got %q", got.Session.ID())
	}
	if v, err := got.Session.State().Get("name"); err != nil || v != "alice" {
		t.Errorf("state[name]: got (%v, %v), want (alice, nil)", v, err)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "dup"}); err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "dup"}); err == nil {
		t.Error("expected duplicate error")
	}
}

func TestAppendEventPersistsAndBumpsIndex(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	evt := session.NewEvent(t.Context(), "inv-1")
	evt.Author = "user"
	evt.Content = &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}
	if err := svc.AppendEvent(ctx, resp.Session, evt); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	got, err := svc.Get(ctx, &session.GetRequest{AppName: "a", UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Session.Events().Len() != 1 {
		t.Fatalf("events: got %d, want 1", got.Session.Events().Len())
	}
	first := got.Session.Events().At(0)
	if first.Author != "user" {
		t.Errorf("Author: got %q", first.Author)
	}

	entries, err := svc.ListIndex(ctx, "a", "u")
	if err != nil {
		t.Fatalf("ListIndex: %v", err)
	}
	if len(entries) != 1 || entries[0].MsgCount != 1 {
		t.Errorf("index entries: got %+v", entries)
	}
}

func TestListIndexOrderedByRecency(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	for _, id := range []string{"old", "mid", "new"} {
		_, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: id})
		if err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	entries, err := svc.ListIndex(ctx, "a", "u")
	if err != nil {
		t.Fatalf("ListIndex: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len: got %d, want 3", len(entries))
	}
	if entries[0].ID != "new" || entries[1].ID != "mid" || entries[2].ID != "old" {
		t.Errorf("ordering: got [%s, %s, %s]", entries[0].ID, entries[1].ID, entries[2].ID)
	}
}

func TestListEmpty(t *testing.T) {
	svc, _ := newSvc(t)
	resp, err := svc.List(context.Background(), &session.ListRequest{AppName: "a", UserID: "u"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected empty, got %d", len(resp.Sessions))
	}
}

func TestSessionsSurvivesReopen(t *testing.T) {
	tmp := t.TempDir()

	db, err := storage.Open(tmp)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	svc, _ := New(db)
	ctx := context.Background()
	resp, _ := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "alive"})
	evt := session.NewEvent(t.Context(), "inv")
	evt.Author = "model"
	_ = svc.AppendEvent(ctx, resp.Session, evt)
	_ = db.Close()

	db2, err := storage.Open(tmp)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer db2.Close()
	svc2, _ := New(db2)
	got, err := svc2.Get(ctx, &session.GetRequest{AppName: "a", UserID: "u", SessionID: "alive"})
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Session.Events().Len() != 1 {
		t.Errorf("events lost across reopen: %d", got.Session.Events().Len())
	}
}

func TestStateDeltaSurvivesReopen(t *testing.T) {
	tmp := t.TempDir()

	db, err := storage.Open(tmp)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	svc, _ := New(db)
	ctx := context.Background()
	resp, _ := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"})
	evt := session.NewEvent(t.Context(), "inv")
	evt.Author = "model"
	evt.Actions.StateDelta["todos"] = []any{
		map[string]any{"content": "do x", "status": "pending"},
	}
	evt.Actions.StateDelta["temp:scratch"] = "should not persist"
	if err := svc.AppendEvent(ctx, resp.Session, evt); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	_ = db.Close()

	db2, err := storage.Open(tmp)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer db2.Close()
	svc2, _ := New(db2)
	got, err := svc2.Get(ctx, &session.GetRequest{AppName: "a", UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}

	v, err := got.Session.State().Get("todos")
	if err != nil {
		t.Fatalf("todos key missing after reopen: %v", err)
	}
	list, ok := v.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("unexpected todos shape: %T %v", v, v)
	}

	if _, err := got.Session.State().Get("temp:scratch"); err == nil {
		t.Error("temp: key must not be persisted, but it was")
	}
}

// TestSetTitleMissingSession guards issue #4: renaming a session that
// does not exist must report ErrSessionNotFound rather than silently
// succeeding.
func TestSetTitleMissingSession(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	err := svc.SetTitle(ctx, "a", "u", "ghost", "new title")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SetTitle on missing session: got %v, want ErrSessionNotFound", err)
	}
}

// TestSetTitleExistingSession confirms the happy path still returns nil
// and actually updates the title.
func TestSetTitleExistingSession(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.SetTitle(ctx, "a", "u", "s", "renamed"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	entry, ok, err := svc.GetIndexEntry(ctx, "a", "u", "s")
	if err != nil || !ok {
		t.Fatalf("GetIndexEntry: ok=%v err=%v", ok, err)
	}
	if entry.Title != "renamed" {
		t.Errorf("title: got %q, want renamed", entry.Title)
	}
}

// TestDeleteMissingSession guards issue #4: deleting a session that does
// not exist must report ErrSessionNotFound rather than a false success.
func TestDeleteMissingSession(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	err := svc.Delete(ctx, &session.DeleteRequest{AppName: "a", UserID: "u", SessionID: "ghost"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Delete on missing session: got %v, want ErrSessionNotFound", err)
	}
}

// TestDeleteExistingSession confirms the happy path still removes the
// session and returns nil.
func TestDeleteExistingSession(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, &session.CreateRequest{AppName: "a", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, &session.DeleteRequest{AppName: "a", UserID: "u", SessionID: "s"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := svc.GetIndexEntry(ctx, "a", "u", "s"); err != nil || ok {
		t.Fatalf("after delete: ok=%v err=%v, want ok=false", ok, err)
	}
}
