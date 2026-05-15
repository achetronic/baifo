// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package providers

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"testing"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// scriptedLLM yields a pre-programmed sequence of attempts. Each call
// to GenerateContent consumes the next entry in attempts.
type scriptedLLM struct {
	attempts [][]step
	calls    int
}

func (s *scriptedLLM) Name() string { return "scripted" }

func (s *scriptedLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	idx := s.calls
	s.calls++
	return func(yield func(*model.LLMResponse, error) bool) {
		if idx >= len(s.attempts) {
			return
		}
		for _, st := range s.attempts[idx] {
			if !yield(st.resp, st.err) {
				return
			}
		}
	}
}

func okStep() step {
	return step{resp: &model.LLMResponse{Content: &genai.Content{Parts: []*genai.Part{{Text: "hi"}}}}}
}
func errStep() step { return step{err: errors.New("boom")} }
func apiErrStep() step {
	return step{resp: &model.LLMResponse{ErrorCode: "529", ErrorMessage: "overloaded"}}
}

// collect drains a decorated model and returns how many usable content
// responses and how many error pairs it yielded.
func collect(m model.LLM) (content, errs int) {
	for resp, err := range m.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			errs++
			continue
		}
		if resp != nil && (resp.ErrorCode != "" || resp.ErrorMessage != "") {
			errs++
			continue
		}
		if resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
			content++
		}
	}
	return content, errs
}

// withFastSleep swaps sleepFn/retryHook for the duration of a test so
// retries run instantly and we can count them.
func withFastSleep(t *testing.T) *int {
	t.Helper()
	origSleep := sleepFn
	origHook := retryHook
	var waits int
	sleepFn = func(context.Context, time.Duration) {}
	retryHook = func(int, time.Duration, string) { waits++ }
	t.Cleanup(func() { sleepFn = origSleep; retryHook = origHook })
	return &waits
}

func TestRetry_DisabledIsNoOpWrap(t *testing.T) {
	inner := &scriptedLLM{attempts: [][]step{{okStep()}}}
	got := withRetry(inner, RetryPolicy{MaxAttempts: 1})
	if got != model.LLM(inner) {
		t.Fatal("disabled policy should return the inner model unwrapped")
	}
}

func TestRetry_RetriesTransientErrorThenSucceeds(t *testing.T) {
	waits := withFastSleep(t)
	inner := &scriptedLLM{attempts: [][]step{
		{errStep()},    // attempt 1 fails
		{apiErrStep()}, // attempt 2 fails (API error response)
		{okStep()},     // attempt 3 succeeds
	}}
	m := withRetry(inner, RetryPolicy{MaxAttempts: 4, InitialBackoff: time.Second, MaxBackoff: 10 * time.Second, Multiplier: 2})

	content, errs := collect(m)
	if content != 1 || errs != 0 {
		t.Fatalf("expected 1 content / 0 errors after recovery, got content=%d errs=%d", content, errs)
	}
	if inner.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", inner.calls)
	}
	if *waits != 2 {
		t.Fatalf("expected 2 backoff waits, got %d", *waits)
	}
}

func TestRetry_ExhaustsAndSurfacesLastError(t *testing.T) {
	withFastSleep(t)
	inner := &scriptedLLM{attempts: [][]step{
		{errStep()}, {errStep()}, {errStep()},
	}}
	m := withRetry(inner, RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, Multiplier: 2})

	content, errs := collect(m)
	if content != 0 || errs != 1 {
		t.Fatalf("expected the final error surfaced once, got content=%d errs=%d", content, errs)
	}
	if inner.calls != 3 {
		t.Fatalf("expected 3 attempts (max), got %d", inner.calls)
	}
}

func TestRetry_DoesNotRetryAfterContentProduced(t *testing.T) {
	waits := withFastSleep(t)
	// A stream that emits content and THEN errors must NOT be retried
	// (retrying would duplicate the already-forwarded content).
	inner := &scriptedLLM{attempts: [][]step{
		{okStep(), errStep()},
		{okStep()}, // would be used if it wrongly retried
	}}
	m := withRetry(inner, RetryPolicy{MaxAttempts: 4, InitialBackoff: time.Millisecond, MaxBackoff: time.Second, Multiplier: 2})

	content, errs := collect(m)
	if inner.calls != 1 {
		t.Fatalf("must not retry a mid-stream failure, got %d attempts", inner.calls)
	}
	if content != 1 || errs != 1 {
		t.Fatalf("expected the partial content + error forwarded, got content=%d errs=%d", content, errs)
	}
	if *waits != 0 {
		t.Fatalf("expected no backoff waits, got %d", *waits)
	}
}

func TestRetry_BackoffGrowsAndCaps(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 10, InitialBackoff: time.Second, MaxBackoff: 5 * time.Second, Multiplier: 2, Jitter: false}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i, w := range want {
		if got := p.backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d, ok := parseRetryAfter("3"); !ok || d != 3*time.Second {
		t.Errorf("seconds form: got %v ok=%v, want 3s true", d, ok)
	}
	if _, ok := parseRetryAfter(""); ok {
		t.Error("empty should not be ok")
	}
	if _, ok := parseRetryAfter("0"); ok {
		t.Error("zero seconds should not be ok")
	}
	if _, ok := parseRetryAfter("nonsense"); ok {
		t.Error("garbage should not be ok")
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := parseRetryAfter(future); !ok || d <= 0 {
		t.Errorf("http-date form: got %v ok=%v, want positive true", d, ok)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if _, ok := parseRetryAfter(past); ok {
		t.Error("past http-date should not be ok")
	}
}

func TestRetry_RetryAfterStrategyHonoursHeader(t *testing.T) {
	// Capture the waits the decorator chooses.
	origSleep := sleepFn
	origHook := retryHook
	origRA := retryAfterFn
	var waits []time.Duration
	sleepFn = func(context.Context, time.Duration) {}
	retryHook = func(_ int, w time.Duration, _ string) { waits = append(waits, w) }
	retryAfterFn = func(error) (time.Duration, bool) { return 7 * time.Second, true }
	t.Cleanup(func() { sleepFn = origSleep; retryHook = origHook; retryAfterFn = origRA })

	inner := &scriptedLLM{attempts: [][]step{{errStep()}, {okStep()}}}
	m := withRetry(inner, RetryPolicy{MaxAttempts: 4, InitialBackoff: time.Second, MaxBackoff: 10 * time.Second, Multiplier: 2, Strategy: StrategyRetryAfter})

	if content, errs := collect(m); content != 1 || errs != 0 {
		t.Fatalf("expected recovery, got content=%d errs=%d", content, errs)
	}
	if len(waits) != 1 || waits[0] != 7*time.Second {
		t.Fatalf("expected one 7s Retry-After wait, got %v", waits)
	}
}

func TestRetry_RetryAfterStrategyFallsBackToBackoff(t *testing.T) {
	origSleep := sleepFn
	origHook := retryHook
	origRA := retryAfterFn
	var waits []time.Duration
	sleepFn = func(context.Context, time.Duration) {}
	retryHook = func(_ int, w time.Duration, _ string) { waits = append(waits, w) }
	retryAfterFn = func(error) (time.Duration, bool) { return 0, false } // no header
	t.Cleanup(func() { sleepFn = origSleep; retryHook = origHook; retryAfterFn = origRA })

	inner := &scriptedLLM{attempts: [][]step{{errStep()}, {okStep()}}}
	m := withRetry(inner, RetryPolicy{MaxAttempts: 4, InitialBackoff: time.Second, MaxBackoff: 10 * time.Second, Multiplier: 2, Jitter: false, Strategy: StrategyRetryAfter})

	if content, errs := collect(m); content != 1 || errs != 0 {
		t.Fatalf("expected recovery, got content=%d errs=%d", content, errs)
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Fatalf("expected fallback to 1s backoff, got %v", waits)
	}
}
