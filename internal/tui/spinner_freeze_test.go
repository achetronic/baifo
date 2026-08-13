// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// tickMsg returns a spinner.TickMsg seeded from the model's current
// spinner state. We call m.streamSpinner.Tick() to get the real msg
// type the handler expects, so there is no dependency on the internal
// tag field or unexported types.
func seedTickMsg(m Model) tea.Msg {
	cmd := m.streamSpinner.Tick
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestSpinnerTickChainDiesWhenStreamEnds checks fix (A):
// feeding a TickMsg while streamCancel == nil must return a nil cmd
// (chain stops), and feeding one while a stream is live must return a
// non-nil cmd (chain continues).
func TestSpinnerTickChainDiesWhenStreamEnds(t *testing.T) {
	f := &fakeFacade{reply: "ok"}
	model := NewModel(f, "v0")
	model.splash = false

	// Submit a message to start a stream and arm the spinner.
	model.composer.ta.SetValue("hello")
	mRaw, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected streaming cmd after submit")
	}
	m := mRaw.(Model)

	// streamCancel is now set. Construct a TickMsg and feed it in.
	// The handler must re-arm (return non-nil cmd) while the stream is live.
	tickMsg := seedTickMsg(m)
	if tickMsg == nil {
		t.Fatal("spinner.Tick returned nil msg, cannot drive test")
	}
	afterTick, tickCmd := m.Update(tickMsg)
	if tickCmd == nil {
		t.Error("TickMsg while stream is live: handler returned nil cmd (chain dead, spinner will freeze)")
	}
	_ = afterTick

	// Now drain the stream so streamCancel is cleared.
	mDrained := drainStream(t, mRaw, cmd)
	final := mDrained.(Model)
	if final.streamCancel != nil {
		t.Fatal("streamCancel should be nil after stream done")
	}

	// Feed a TickMsg into the post-stream model. The handler must NOT
	// re-arm: returning a non-nil cmd here is the zombie-chain bug.
	tickMsg2 := seedTickMsg(final)
	if tickMsg2 == nil {
		// spinner.Tick can be nil if the spinner has never been started;
		// build a bare TickMsg manually so we can still exercise the branch.
		tickMsg2 = spinner.TickMsg{}
	}
	_, deadCmd := final.Update(tickMsg2)
	if deadCmd != nil {
		t.Error("TickMsg after stream done: handler re-armed the tick chain (zombie chain, fix A regression)")
	}
}

// TestDoubleSubmitDoesNotDoubleSeedSpinner checks fix (B):
// when a second submit arrives while a stream is (hypothetically) still
// live, submitComposer must NOT include a second m.streamSpinner.Tick in
// the returned batch. We force this by setting streamCancel to a non-nil
// dummy before submitting.
func TestDoubleSubmitDoesNotDoubleSeedSpinner(t *testing.T) {
	f := &fakeFacade{reply: "ok"}
	model := NewModel(f, "v0")
	model.splash = false

	// Simulate "a stream is already alive" by installing a non-nil cancel.
	fakeCtx, fakeCancel := func() (interface{ Done() <-chan struct{} }, func()) {
		// Use context directly -- import is already in model.go; here we
		// just need any non-nil func value for streamCancel.
		import_context_cancel_pair := make(chan struct{})
		close(import_context_cancel_pair)
		return nil, func() {}
	}()
	_ = fakeCtx
	model.streamCancel = fakeCancel

	// Submit while "streaming".
	model.composer.ta.SetValue("second")
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a cmd from submit even when already streaming")
	}

	// Execute the top-level cmd. When alreadyStreaming==true the fix
	// returns a plain Cmd (not a Batch), so the result must NOT be a
	// tea.BatchMsg containing a TickMsg.
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		// A BatchMsg means Tick was included. Walk it and fail if any
		// element looks like a spinner tick by executing it and checking
		// the type of the returned message.
		for _, subCmd := range batch {
			if subCmd == nil {
				continue
			}
			subMsg := subCmd()
			if _, isTickMsg := subMsg.(spinner.TickMsg); isTickMsg {
				t.Error("second submit while streaming seeded a second spinner.Tick (fix B regression)")
			}
		}
	}
	// If msg is NOT a BatchMsg, the fix worked: only the stream cmd
	// was returned, no duplicate tick seed.
}

// TestSpinnerTickContinuesAcrossConsecutiveStreams checks the combined
// fix end-to-end: two messages back-to-back, one tick chain, no freeze.
// After the second stream drains, a TickMsg must return nil cmd (stopped).
func TestSpinnerTickContinuesAcrossConsecutiveStreams(t *testing.T) {
	f := &fakeFacade{reply: "first"}
	model := NewModel(f, "v0")
	model.splash = false

	// First message, drain stream.
	model.composer.ta.SetValue("one")
	m1Raw, cmd1 := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd1 == nil {
		t.Fatal("first submit: expected streaming cmd")
	}
	m1 := drainStream(t, m1Raw, cmd1)
	if m1.(Model).streamCancel != nil {
		t.Fatal("after first stream: streamCancel not cleared")
	}

	// Second message, drain stream.
	f.reply = "second"
	m1m := m1.(Model)
	m1m.composer.ta.SetValue("two")
	m2Raw, cmd2 := m1m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd2 == nil {
		t.Fatal("second submit: expected streaming cmd")
	}

	// While the second stream is in-flight (before draining), a TickMsg
	// must keep the chain alive.
	m2mid := m2Raw.(Model)
	if m2mid.streamCancel == nil {
		t.Fatal("second submit: streamCancel is nil immediately after submit")
	}
	tickMsg := seedTickMsg(m2mid)
	if tickMsg == nil {
		tickMsg = spinner.TickMsg{}
	}
	_, midCmd := m2mid.Update(tickMsg)
	if midCmd == nil {
		t.Error("TickMsg during second stream: handler returned nil (spinner frozen mid-stream)")
	}

	// Drain second stream. streamCancel must be nil after.
	m2 := drainStream(t, m2Raw, cmd2)
	final := m2.(Model)
	if final.streamCancel != nil {
		t.Fatal("after second stream: streamCancel not cleared")
	}

	// Now a TickMsg must NOT re-arm.
	tickMsg2 := seedTickMsg(final)
	if tickMsg2 == nil {
		tickMsg2 = spinner.TickMsg{}
	}
	_, doneCmd := final.Update(tickMsg2)
	if doneCmd != nil {
		t.Error("TickMsg after second stream: chain still alive (zombie chain)")
	}

	// Final message count: user1, root1, user2, root2.
	if n := len(final.messages); n != 4 {
		t.Errorf("message count = %d, want 4", n)
	}
}
