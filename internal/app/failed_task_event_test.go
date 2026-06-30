// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
)

// TestEventFromA2A_FailedTaskSurfacesError is the regression guard for
// the silent-hang bug: the adka2a executor reports agent-run errors,
// processor errors and non-zero LLMResponse error codes as a
// TaskStateFailed status update (NOT as the iterator's err value).
// baifo used to drop every *a2a.TaskStatusUpdateEvent in eventFromA2A,
// so such a turn ended with no text and no error and the TUI's
// "thinking" indicator just vanished. This happened most often on the
// LLM round right after a large `exec` tool result. We now surface the
// failure as a visible event.
func TestEventFromA2A_FailedTaskSurfacesError(t *testing.T) {
	ev := &a2a.TaskStatusUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Final:     true,
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateFailed,
			Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: "llm error response: \"overloaded\""}),
		},
	}

	out := eventFromA2A(ev)
	if out == nil {
		t.Fatal("failed task event was dropped (nil); the turn would hang silently")
	}
	if out.Text == "" {
		t.Fatal("failed task event surfaced with empty Text")
	}
	if out.Role != "error" {
		t.Fatalf("failed task should be tagged Role=error for the TUI, got %q", out.Role)
	}
	if !strings.Contains(out.Text, "overloaded") {
		t.Fatalf("failure detail lost; got Text=%q", out.Text)
	}
}

// TestEventFromA2A_FailedTaskNoMessageStillVisible ensures we never end
// a turn silently even when the failed status carries no message.
func TestEventFromA2A_FailedTaskNoMessageStillVisible(t *testing.T) {
	ev := &a2a.TaskStatusUpdateEvent{
		ContextID: "s1",
		TaskID:    "t1",
		Final:     true,
		Status:    a2a.TaskStatus{State: a2a.TaskStateFailed},
	}

	out := eventFromA2A(ev)
	if out == nil || out.Text == "" {
		t.Fatal("failed task with no message must still produce a visible event")
	}
}

// TestEventFromA2A_NonFailedStatusDropped confirms we still skip the
// non-failure lifecycle states (working/completed/submitted) so the
// chat is not polluted with status noise.
func TestEventFromA2A_NonFailedStatusDropped(t *testing.T) {
	for _, st := range []a2a.TaskState{
		a2a.TaskStateWorking,
		a2a.TaskStateCompleted,
		a2a.TaskStateSubmitted,
	} {
		ev := &a2a.TaskStatusUpdateEvent{
			ContextID: "s1",
			TaskID:    "t1",
			Status:    a2a.TaskStatus{State: st},
		}
		if out := eventFromA2A(ev); out != nil {
			t.Fatalf("state %q should be dropped, got %+v", st, out)
		}
	}
}
