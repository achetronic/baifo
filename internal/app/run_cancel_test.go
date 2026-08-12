// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// fakeRunner blocks inside Run until its context is cancelled, then
// yields the cancellation error — mimicking an in-flight LLM call.
type fakeRunner struct {
	started chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, _, _ string, _ *genai.Content, _ agent.RunConfig) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		close(f.started)
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

// TestCancellableRunnerPropagatesCallerCancel verifies the core of the
// Esc-aborts-the-run fix: the A2A execution manager detaches the run
// context (context.WithoutCancel), so cancelling the caller's context
// must still reach the runner via the value smuggled in by
// withCallerCancel.
func TestCancellableRunnerPropagatesCallerCancel(t *testing.T) {
	inner := &fakeRunner{started: make(chan struct{})}
	cr := &cancellableRunner{inner: inner}

	callerCtx, cancel := context.WithCancel(context.Background())
	// Simulate what SendMessage + taskexec do: stash the caller ctx as
	// a value, then detach cancellation.
	runCtx := context.WithoutCancel(withCallerCancel(callerCtx))

	done := make(chan error, 1)
	go func() {
		for _, err := range cr.Run(runCtx, "u", "s", nil, agent.RunConfig{}) {
			done <- err
			return
		}
		done <- nil
	}()

	<-inner.started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller cancellation never reached the runner (run kept going)")
	}
}

// TestCancellableRunnerWithoutCallerCtxIsTransparent verifies remote
// A2A executions (no caller context stored) keep the detached-run
// semantics: cancelling an unrelated caller context does nothing and
// the runner only stops with its own context.
func TestCancellableRunnerWithoutCallerCtxIsTransparent(t *testing.T) {
	inner := &fakeRunner{started: make(chan struct{})}
	cr := &cancellableRunner{inner: inner}

	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		for _, err := range cr.Run(runCtx, "u", "s", nil, agent.RunConfig{}) {
			done <- err
			return
		}
		done <- nil
	}()

	<-inner.started
	select {
	case err := <-done:
		t.Fatalf("runner stopped before its own context was cancelled: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancelRun()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not observe its own context cancellation")
	}
}
