// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package sessions

import (
	"iter"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/session"
)

// IndexEntry is the per-session metadata stored in the sessions table.
// Exposed so callers can list / search sessions without loading the full
// event history.
type IndexEntry struct {
	ID        string    `json:"id"`
	AppName   string    `json:"app_name"`
	UserID    string    `json:"user_id"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastAt    time.Time `json:"last_at"`
	MsgCount  int       `json:"msg_count"`
}

// storableSession is the view of a session minus its events.
type storableSession struct {
	ID             string         `json:"id"`
	AppName        string         `json:"app_name"`
	UserID         string         `json:"user_id"`
	State          map[string]any `json:"state"`
	LastUpdateTime time.Time      `json:"last_update_time"`
}

// sqliteSession satisfies session.Session.
type sqliteSession struct {
	svc *Service

	id      string
	appName string
	userID  string

	state          *sqliteState
	events         *sqliteEvents
	lastUpdateTime time.Time
}

func (s *sqliteSession) ID() string                { return s.id }
func (s *sqliteSession) AppName() string           { return s.appName }
func (s *sqliteSession) UserID() string            { return s.userID }
func (s *sqliteSession) State() session.State      { return s.state }
func (s *sqliteSession) Events() session.Events    { return s.events }
func (s *sqliteSession) LastUpdateTime() time.Time { return s.lastUpdateTime }

// sqliteState is a minimal in-memory State.
type sqliteState struct {
	mu   sync.RWMutex
	data map[string]any
}

func newState(initial map[string]any) *sqliteState {
	out := make(map[string]any, len(initial))
	for k, v := range initial {
		out[k] = v
	}
	return &sqliteState{data: out}
}

func (s *sqliteState) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s *sqliteState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *sqliteState) All() iter.Seq2[string, any] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := make(map[string]any, len(s.data))
	for k, v := range s.data {
		snapshot[k] = v
	}
	return func(yield func(string, any) bool) {
		for k, v := range snapshot {
			if !yield(k, v) {
				return
			}
		}
	}
}

// sqliteEvents satisfies session.Events.
type sqliteEvents struct {
	mu     sync.RWMutex
	events []*session.Event
}

func newEvents(initial []*session.Event) *sqliteEvents {
	cp := make([]*session.Event, len(initial))
	copy(cp, initial)
	return &sqliteEvents{events: cp}
}

func (e *sqliteEvents) All() iter.Seq[*session.Event] {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snapshot := make([]*session.Event, len(e.events))
	copy(snapshot, e.events)
	return func(yield func(*session.Event) bool) {
		for _, ev := range snapshot {
			if !yield(ev) {
				return
			}
		}
	}
}

func (e *sqliteEvents) Len() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.events)
}

func (e *sqliteEvents) append(evt *session.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, evt)
}

func (e *sqliteEvents) At(i int) *session.Event {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if i < 0 || i >= len(e.events) {
		return nil
	}
	return e.events[i]
}

// splitState separates app:, user:, temp:, and session-scoped keys.
func splitState(delta map[string]any) (userOrApp, sessionOnly map[string]any) {
	userOrApp = make(map[string]any)
	sessionOnly = make(map[string]any)
	for k, v := range delta {
		switch {
		case strings.HasPrefix(k, session.KeyPrefixApp),
			strings.HasPrefix(k, session.KeyPrefixUser):
			userOrApp[k] = v
		case strings.HasPrefix(k, session.KeyPrefixTemp):
			// drop temp keys
		default:
			sessionOnly[k] = v
		}
	}
	return userOrApp, sessionOnly
}

// trimTempState removes temp: keys from an event's StateDelta before
// the event is written to disk.
func trimTempState(evt *session.Event) {
	if len(evt.Actions.StateDelta) == 0 {
		return
	}
	filtered := make(map[string]any, len(evt.Actions.StateDelta))
	for k, v := range evt.Actions.StateDelta {
		if !strings.HasPrefix(k, session.KeyPrefixTemp) {
			filtered[k] = v
		}
	}
	evt.Actions.StateDelta = filtered
}
