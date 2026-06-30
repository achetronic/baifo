// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/achetronic/baifo/internal/facade"
)

// FactDetails implements facade.Facade. Walks the facts store for the
// current user and surfaces every entry with its full metadata so
// the Settings overlay can render rich rows (timestamp + author +
// truncated content).
func (a *App) FactDetails() []facade.FactDetail {
	if a.facts == nil {
		return nil
	}
	entries, err := a.facts.List(appName, a.userID)
	if err != nil {
		return nil
	}
	out := make([]facade.FactDetail, 0, len(entries))
	for _, e := range entries {
		out = append(out, facade.FactDetail{
			ID:        e.ID,
			Content:   e.Content,
			Category:  e.Category,
			Author:    e.Author,
			Timestamp: e.Timestamp,
		})
	}
	return out
}

// AddFact implements facade.Facade. Forwards to the store's
// AddManualEntry, marking the author as "user" so the agent can
// tell self-added facts apart from agent-saved ones when searching.
//
// We do NOT emit a facade.ReloadEvent: the facts list reads on demand from
// SQLite, so the next overlay refresh picks the new row up by itself.
func (a *App) AddFact(ctx context.Context, content, category string) (uint64, error) {
	if a.facts == nil {
		return 0, fmt.Errorf("facts store not initialised")
	}
	id, err := a.facts.AddManualEntry(ctx, appName, a.userID, content, "user", category)
	if err != nil {
		return 0, err
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return id, nil
}

// FactContent returns the current content of the named entry plus
// its category, so the TUI can pre-fill the editor for /fact edit.
// Surfaces a fmt-wrapped error when the ID doesn't exist; the
// underlying error is sufficient for the caller to render a
// 'not found' toast.
func (a *App) FactContent(entryID uint64) (content, category string, err error) {
	if a.facts == nil {
		return "", "", fmt.Errorf("facts store not initialised")
	}
	entries, err := a.facts.List(appName, a.userID)
	if err != nil {
		return "", "", fmt.Errorf("list facts: %w", err)
	}
	for _, e := range entries {
		if e.ID == entryID {
			return e.Content, e.Category, nil
		}
	}
	return "", "", fmt.Errorf("fact #%d not found", entryID)
}

// UpdateFact implements facade.Facade. Bumps the entry's timestamp to now
// so the corrected fact appears at the top of subsequent searches —
// users edit when the agent recorded something slightly wrong, so
// the new content is the one that should win in future recall.
func (a *App) UpdateFact(ctx context.Context, entryID uint64, content string) error {
	if a.facts == nil {
		return fmt.Errorf("facts store not initialised")
	}
	if err := a.facts.UpdateMemory(ctx, appName, a.userID, int(entryID), content); err != nil {
		return err
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return nil
}

// DeleteFact implements facade.Facade.
func (a *App) DeleteFact(ctx context.Context, entryID uint64) error {
	if a.facts == nil {
		return fmt.Errorf("facts store not initialised")
	}
	if err := a.facts.DeleteMemory(ctx, appName, a.userID, int(entryID)); err != nil {
		return err
	}
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}
	return nil
}
