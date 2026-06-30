// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package editor

import "testing"

func TestFind_QueryFindsMatches(t *testing.T) {
	m := newTestModel(t, "hello world\nhello again\nbye now")
	m, _ = m.Update(press("ctrl+f", ""))
	if m.searchSt == nil {
		t.Fatalf("ctrl+f should open the search bar")
	}
	// Type "hello" letter by letter; each keystroke recomputes.
	for _, r := range "hello" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if len(m.searchSt.matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(m.searchSt.matches))
	}
	// First match should jump the cursor to (0, 0).
	if m.cursor.line != 0 || m.cursor.col != 0 {
		t.Errorf("cursor should be on first match: got %v", m.cursor)
	}
}

func TestFind_NextWrapsAround(t *testing.T) {
	m := newTestModel(t, "foo\nfoo\nfoo")
	m, _ = m.Update(press("ctrl+f", ""))
	for _, r := range "foo" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// Three matches. Enter advances; cycle through all three back to first.
	m, _ = m.Update(press("enter", ""))
	if m.cursor.line != 1 {
		t.Errorf("enter should jump to line 1, got %v", m.cursor)
	}
	m, _ = m.Update(press("enter", ""))
	if m.cursor.line != 2 {
		t.Errorf("enter should jump to line 2, got %v", m.cursor)
	}
	m, _ = m.Update(press("enter", ""))
	if m.cursor.line != 0 {
		t.Errorf("enter should wrap back to line 0, got %v", m.cursor)
	}
}

func TestFind_PrevWithCapitalN(t *testing.T) {
	m := newTestModel(t, "alpha\nbeta\ngamma\nalpha")
	m, _ = m.Update(press("ctrl+f", ""))
	for _, r := range "alpha" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	// We start at (0,0). Capital N should go to the previous match,
	// which wraps to (3, 0).
	m, _ = m.Update(press("N", "N"))
	if m.cursor.line != 3 {
		t.Errorf("N should wrap to last match (line 3), got %v", m.cursor)
	}
}

func TestFind_EscClosesBar(t *testing.T) {
	m := newTestModel(t, "hello")
	m, _ = m.Update(press("ctrl+f", ""))
	if m.searchSt == nil {
		t.Fatalf("setup")
	}
	m, _ = m.Update(press("esc", ""))
	if m.searchSt != nil {
		t.Errorf("esc should close the search bar")
	}
}

func TestFind_NoMatchesIsNotAnError(t *testing.T) {
	m := newTestModel(t, "hello world")
	m, _ = m.Update(press("ctrl+f", ""))
	for _, r := range "xyz" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if m.searchSt == nil {
		t.Fatalf("search bar should still be open")
	}
	if len(m.searchSt.matches) != 0 {
		t.Errorf("expected no matches, got %d", len(m.searchSt.matches))
	}
	if m.searchSt.current != -1 {
		t.Errorf("current should be -1 when no matches, got %d", m.searchSt.current)
	}
	// Enter / n on empty matches must not panic.
	m, _ = m.Update(press("enter", ""))
}

func TestFind_BackspaceShrinksQuery(t *testing.T) {
	m := newTestModel(t, "hello world")
	m, _ = m.Update(press("ctrl+f", ""))
	for _, r := range "world" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if len(m.searchSt.matches) != 1 {
		t.Fatalf("setup: expected 1 match")
	}
	m, _ = m.Update(press("backspace", ""))
	if m.searchSt.query != "worl" {
		t.Errorf("backspace should shrink query, got %q", m.searchSt.query)
	}
}

func TestFind_CaseInsensitive(t *testing.T) {
	m := newTestModel(t, "Hello\nHELLO\nhello")
	m, _ = m.Update(press("ctrl+f", ""))
	for _, r := range "hello" {
		m, _ = m.Update(press(string(r), string(r)))
	}
	if len(m.searchSt.matches) != 3 {
		t.Errorf("expected 3 case-insensitive matches, got %d", len(m.searchSt.matches))
	}
}
