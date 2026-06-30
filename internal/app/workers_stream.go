// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/workers"
)

// SubscribeWorker implements facade.Facade.
func (a *App) SubscribeWorker(id string) ([]facade.WorkerStreamEvent, <-chan facade.WorkerStreamEvent, func(), error) {
	if a.workers == nil {
		return nil, nil, nil, errors.New("workers manager not initialised")
	}
	rawHistory, rawStream, cancel, err := a.workers.SubscribeWorker(id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("subscribe worker %q: %w", id, err)
	}

	// Translate the history snapshot up-front so the consumer does
	// not have to know about workers.WorkerEvent.
	history := make([]facade.WorkerStreamEvent, 0, len(rawHistory))
	for _, evt := range rawHistory {
		if translated, ok := translateWorkerEvent(evt); ok {
			history = append(history, translated)
		}
	}

	// Pipe the live stream through a translator goroutine. We use a
	// modestly buffered channel so a slow consumer cannot block the
	// worker bus. The goroutine exits when the upstream channel is
	// closed (the underlying unsubscribe runs at cancel).
	out := make(chan facade.WorkerStreamEvent, 64)
	go func() {
		defer close(out)
		for evt := range rawStream {
			translated, ok := translateWorkerEvent(evt)
			if !ok {
				continue
			}
			select {
			case out <- translated:
			default:
				// Slow consumer: drop the event rather than block
				// the upstream worker bus. The dropped count is
				// already tracked by workers.EventBus.Drops.
			}
		}
	}()

	return history, out, cancel, nil
}

// SendToWorker implements facade.Facade.
func (a *App) SendToWorker(ctx context.Context, id string, message string) error {
	if a.workers == nil {
		return errors.New("workers manager not initialised")
	}
	if message == "" {
		return errors.New("message is empty")
	}
	return a.workers.Query(ctx, id, message)
}

// translateWorkerEvent unpacks a workers.WorkerEvent into the TUI
// shape. Returns (event, false) when the input is something the chat
// view has no use for (e.g. a thought event whose payload is empty),
// so callers can simply skip it.
func translateWorkerEvent(evt workers.WorkerEvent) (facade.WorkerStreamEvent, bool) {
	out := facade.WorkerStreamEvent{
		WorkerID:  evt.WorkerID,
		Index:     evt.Index,
		Timestamp: evt.Timestamp,
	}
	switch evt.Kind {
	case workers.EventAssistantMessage, workers.EventThought:
		text, ok := evt.Payload.(string)
		if !ok || text == "" {
			return out, false
		}
		out.Kind = facade.WorkerStreamText
		out.Text = text
		return out, true

	case workers.EventToolCall:
		// Workers publish the full call envelope (Name, ID, Args)
		// as a ToolCallPayload so the TUI can render a card
		// identical to the root's. We also accept a bare-string
		// payload for backwards compatibility with any caller that
		// hasn't migrated yet — that branch produces a degraded
		// row with only the tool name visible.
		switch pl := evt.Payload.(type) {
		case workers.ToolCallPayload:
			out.Kind = facade.WorkerStreamToolCall
			out.ToolCalls = []facade.ToolCallInfo{{
				CallID: pl.ID,
				Name:   pl.Name,
				Args:   pl.Args,
			}}
			return out, true
		case string:
			out.Kind = facade.WorkerStreamToolCall
			out.ToolCalls = []facade.ToolCallInfo{{Name: pl}}
			return out, true
		default:
			return out, false
		}

	case workers.EventToolResult:
		// Same shape duality as EventToolCall: the new
		// ToolResultPayload carries Name + ID + Result, and a bare
		// string keeps older publishers compatible.
		switch pl := evt.Payload.(type) {
		case workers.ToolResultPayload:
			out.Kind = facade.WorkerStreamToolResult
			out.ToolResults = []facade.ToolResultInfo{{
				CallID: pl.ID,
				Name:   pl.Name,
				Result: pl.Result,
			}}
			return out, true
		case string:
			out.Kind = facade.WorkerStreamToolResult
			out.ToolResults = []facade.ToolResultInfo{{Name: pl}}
			return out, true
		default:
			return out, false
		}

	case workers.EventStatusChange:
		info, ok := evt.Payload.(workers.WorkerInfo)
		if !ok {
			return out, false
		}
		out.Kind = facade.WorkerStreamStatus
		out.StatusChange = info.Status.String()
		return out, true
	}
	return out, false
}

// SubscribeWorkerLifecycle implements facade.Facade. Subscribes
// the caller to the workers manager's global event bus, filters
// the firehose down to "interesting" lifecycle transitions
// (spawn + done/failed/killed) and translates each into the
// public facade.WorkerLifecycleEvent shape.
//
// The returned channel is buffered at 32 entries. Lifecycle
// events are sparse — at most a handful per minute under realistic
// workloads — so the buffer is generous enough that even a TUI
// stuck on a slow render won't lose entries.
//
// Implementation note: we track which workers we've already seen
// in a small map keyed by id. The first status-change event for a
// given id is treated as a "spawned" notice (it's the cheapest way
// to surface births without piggy-backing on the manager's internal
// goroutine; the manager publishes status-change unconditionally on
// every transition including the initial running→running one).
func (a *App) SubscribeWorkerLifecycle() (<-chan facade.WorkerLifecycleEvent, func()) {
	if a.workers == nil {
		ch := make(chan facade.WorkerLifecycleEvent)
		close(ch)
		return ch, func() {}
	}
	raw, unsubRaw := a.workers.GlobalBus().Subscribe()
	out := make(chan facade.WorkerLifecycleEvent, 32)

	go func() {
		defer close(out)
		seen := make(map[string]struct{}, 8)
		for evt := range raw {
			if evt.Kind != workers.EventStatusChange {
				continue
			}
			info, ok := evt.Payload.(workers.WorkerInfo)
			if !ok {
				continue
			}
			kind, emit := lifecycleKindFor(info.Status)
			if !emit {
				// Not a "spawned" or terminal transition. Use
				// the first sighting of this id as our spawn
				// marker — every worker emits a status-change
				// the moment Manager.Spawn registers it, so this
				// branch fires exactly once per worker.
				if _, known := seen[info.ID]; !known {
					seen[info.ID] = struct{}{}
					select {
					case out <- buildLifecycleEvent(facade.WorkerLifecycleSpawned, info, evt.Timestamp):
					default:
					}
				}
				continue
			}
			// Mark seen so a separate Spawned notice doesn't
			// also fire for the same worker.
			seen[info.ID] = struct{}{}
			select {
			case out <- buildLifecycleEvent(kind, info, evt.Timestamp):
			default:
				// Drop on full buffer. The TUI's consumer keeps
				// up under realistic loads; sustained drops
				// would mean the operator's chat is paused, in
				// which case the events would be redundant
				// anyway (collect_agent surfaces the same info).
			}
		}
	}()

	cancel := func() {
		unsubRaw() // closes raw, the goroutine drains and closes out
	}
	return out, cancel
}

// lifecycleKindFor maps a worker status to the WorkerLifecycle*
// kind the TUI consumes. Returns ok=false for transient states
// (Running, Idle) — those are not worth surfacing on the root
// chat; they are best seen via the Workers tab.
func lifecycleKindFor(s workers.Status) (facade.WorkerLifecycleEventKind, bool) {
	switch s {
	case workers.StatusDone:
		return facade.WorkerLifecycleDone, true
	case workers.StatusFailed:
		return facade.WorkerLifecycleFailed, true
	case workers.StatusKilled:
		return facade.WorkerLifecycleKilled, true
	}
	return 0, false
}

// buildLifecycleEvent packages a WorkerInfo + status transition
// into the public facade event. WorkerInfo.Kind is an enum we
// translate to a stable lowercase string so consumers don't need
// to depend on internal/workers.
func buildLifecycleEvent(kind facade.WorkerLifecycleEventKind, info workers.WorkerInfo, ts time.Time) facade.WorkerLifecycleEvent {
	workerKind := "dynamic"
	if info.Kind == workers.KindStatic {
		workerKind = "static"
	}
	return facade.WorkerLifecycleEvent{
		Kind:       kind,
		WorkerID:   info.ID,
		Name:       info.Name,
		WorkerKind: workerKind,
		Status:     info.Status.String(),
		LastEvent:  info.LastEvent,
		Timestamp:  ts,
	}
}
