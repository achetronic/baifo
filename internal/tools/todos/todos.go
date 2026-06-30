// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package todos exposes per-(agent, session) TODO management as ADK
// tools. The list is stored under the session-state key "todos",
// which is the same key the contextguard plugin from adk-utils-go
// inspects on every summarisation pass: it forwards the items to
// the summariser prompt and asks the resuming model to restore
// them. The net effect is that a task list authored by the model
// survives context compaction without baifo doing anything beyond
// writing to the well-known key.
//
// Scope follows session scope:
//   - root agent → SQLite-backed session → todos survive restart
//     (thanks to AppendEvent now merging StateDelta into the
//     persisted session state; see internal/sessions/sqlite.go).
//   - workers   → InMemoryService session → todos die with the
//     worker, which is what we want for a one-shot worker.
//
// We intentionally don't share state across agents. Each agent's
// session has its own "todos" key and the contextguard plugin
// keys its summary cache by agent name as well.
package todos

import (
	"errors"
	"fmt"
	"sync"

	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// stateKey is the session-state slot contextguard already reads.
// Keep this string in sync with adk-utils-go/plugin/contextguard
// (compaction_utils.go: "todos") — if they diverge, summarisations
// stop preserving the list.
const stateKey = "todos"

// TodoItem mirrors the shape contextguard expects so we serialise
// straight into the session state without an intermediate type.
//
// Fields:
//   - Content: the task in imperative form ("Run the tests").
//   - Status: one of "pending", "in_progress", "completed". The
//     summariser's prompt assumes these three; arbitrary strings
//     work but disable the "you have N pending tasks" framing.
//   - ActiveForm: present continuous form shown while the task is
//     in_progress ("Running the tests"). Optional, but encouraged
//     because it makes the resumed prompt readable.
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

// Tools is the per-instance dependency bag. Today it carries just
// a mutex to serialise read-modify-write across concurrent tool
// calls (ADK can batch function_calls within one model turn). The
// type stays a struct so future deps (audit, rate-limits) slot in
// without churn at the call sites.
type Tools struct {
	mu sync.Mutex
}

// New returns a Tools ready for ADKTools(). Kept as a constructor
// for symmetry with the rest of internal/tools/*.
func New() *Tools { return &Tools{} }

// ADKTools returns the full toolset to be appended to an agent's
// tool list. Order follows usefulness: list first (the cheap
// read), then write (the heavy hitter), then update (the patch),
// then clear (the eraser).
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	builders := []func() (tool.Tool, error){
		t.buildList,
		t.buildWrite,
		t.buildUpdate,
		t.buildClear,
	}
	out := make([]tool.Tool, 0, len(builders))
	for _, b := range builders {
		tl, err := b()
		if err != nil {
			return nil, err
		}
		out = append(out, tl)
	}
	return out, nil
}

// todos_list

// ListResult is the response of todos_list. We wrap the slice in
// a struct so functiontool can describe the JSON shape; a bare
// slice would also work but loses the field name in the schema.
type ListResult struct {
	Todos []TodoItem `json:"todos"`
}

func (t *Tools) buildList() (tool.Tool, error) {
	const desc = "Read the current TODO list for this agent's session. " +
		"The list survives context-window compaction (the contextguard plugin " +
		"forwards it through summaries), so use it to remember multi-step work " +
		"that may outlive the model's immediate context."
	return functiontool.New(
		functiontool.Config{Name: "todos_list", Description: desc},
		func(ctx tool.Context, _ struct{}) (ListResult, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			items, err := readTodos(ctx)
			if err != nil {
				return ListResult{}, err
			}
			return ListResult{Todos: items}, nil
		},
	)
}

// todos_write

// WriteArgs is the input of todos_write. The whole list is
// replaced — there's no append semantics — because Claude-style
// "rewrite the plan" gives the model the simplest mental model
// and avoids ordering bugs across parallel tool calls.
type WriteArgs struct {
	Todos []TodoItem `json:"todos"`
}

// WriteResult reports how many items were stored, mainly for the
// LLM's own sanity check after a write.
type WriteResult struct {
	Count int `json:"count"`
}

func (t *Tools) buildWrite() (tool.Tool, error) {
	const desc = "Replace the TODO list for this agent's session. " +
		"Each item has {content, status, active_form}; status is one of " +
		"\"pending\", \"in_progress\", \"completed\". Keep exactly one item " +
		"as in_progress at a time. This list survives context-window " +
		"compaction — write it whenever you plan multi-step work."
	return functiontool.New(
		functiontool.Config{Name: "todos_write", Description: desc},
		func(ctx tool.Context, a WriteArgs) (WriteResult, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if err := writeTodos(ctx, a.Todos); err != nil {
				return WriteResult{}, err
			}
			return WriteResult{Count: len(a.Todos)}, nil
		},
	)
}

// todos_update

// UpdateArgs patches a single item by index. Empty string fields
// mean "leave the existing value alone"; this matches the way the
// model naturally thinks about an update ("mark item 2 completed")
// without forcing it to re-emit the whole list.
type UpdateArgs struct {
	Index      int    `json:"index"`
	Status     string `json:"status,omitempty"`
	Content    string `json:"content,omitempty"`
	ActiveForm string `json:"active_form,omitempty"`
}

// UpdateResult acknowledges the patch.
type UpdateResult struct {
	OK bool `json:"ok"`
}

func (t *Tools) buildUpdate() (tool.Tool, error) {
	const desc = "Patch one TODO by index (0-based). Pass only the fields " +
		"you want to change; empty strings keep the previous value. " +
		"Convenience wrapper around todos_write for marking an item as " +
		"in_progress or completed without re-emitting the full list."
	return functiontool.New(
		functiontool.Config{Name: "todos_update", Description: desc},
		func(ctx tool.Context, a UpdateArgs) (UpdateResult, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			items, err := readTodos(ctx)
			if err != nil {
				return UpdateResult{}, err
			}
			if a.Index < 0 || a.Index >= len(items) {
				return UpdateResult{}, fmt.Errorf("index %d out of range [0,%d)", a.Index, len(items))
			}
			if a.Status != "" {
				items[a.Index].Status = a.Status
			}
			if a.Content != "" {
				items[a.Index].Content = a.Content
			}
			if a.ActiveForm != "" {
				items[a.Index].ActiveForm = a.ActiveForm
			}
			if err := writeTodos(ctx, items); err != nil {
				return UpdateResult{}, err
			}
			return UpdateResult{OK: true}, nil
		},
	)
}

// todos_clear

// ClearResult acknowledges the wipe.
type ClearResult struct {
	OK bool `json:"ok"`
}

func (t *Tools) buildClear() (tool.Tool, error) {
	const desc = "Erase the TODO list. Use only when the multi-step task " +
		"the list described is fully done; otherwise summarise progress " +
		"with todos_write so the resumed conversation still has context."
	return functiontool.New(
		functiontool.Config{Name: "todos_clear", Description: desc},
		func(ctx tool.Context, _ struct{}) (ClearResult, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if err := writeTodos(ctx, nil); err != nil {
				return ClearResult{}, err
			}
			return ClearResult{OK: true}, nil
		},
	)
}

// helpers

// readTodos pulls the current list from session state, tolerating
// both shapes the value can take:
//
//   - []TodoItem when set earlier in the same process (we round-trip
//     the same Go value).
//   - []any of map[string]any after the session was reloaded from
//     SQLite (JSON unmarshal collapses concrete types to interfaces).
//
// Matches what contextguard's loadTodos does internally so both
// readers agree on the universe of valid shapes.
func readTodos(ctx tool.Context) ([]TodoItem, error) {
	raw, err := ctx.State().Get(stateKey)
	if err != nil {
		if errors.Is(err, session.ErrStateKeyNotExist) {
			return nil, nil
		}
		return nil, err
	}
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []TodoItem:
		return v, nil
	case []any:
		out := make([]TodoItem, 0, len(v))
		for _, it := range v {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, TodoItem{
				Content:    asString(m["content"]),
				Status:     asString(m["status"]),
				ActiveForm: asString(m["active_form"]),
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected todos shape in session state: %T", raw)
	}
}

// writeTodos commits the slice. Set both updates the in-memory
// state and records a StateDelta entry that AppendEvent persists.
func writeTodos(ctx tool.Context, items []TodoItem) error {
	if items == nil {
		items = []TodoItem{}
	}
	return ctx.State().Set(stateKey, items)
}

// asString coerces an interface{} (post-JSON) to a string,
// returning "" for nil or non-string values. Defensive against
// malformed entries that snuck in through earlier versions or
// hand-crafted session.State writes.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
