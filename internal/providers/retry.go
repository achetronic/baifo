// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package providers

import (
	"context"
	"errors"
	"iter"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/adk/v2/model"
)

// Retry strategy identifiers. They mirror config.RetryStrategy* but are
// duplicated here so the providers package stays free of a config import.
const (
	StrategyBackoff    = "backoff"
	StrategyRetryAfter = "retry-after"
)

// RetryPolicy is the resolved, ready-to-use retry configuration the
// decorator consumes. The config package owns the user-facing YAML
// shape and its defaults; this struct is the already-validated form so
// the providers package has no dependency on config parsing.
type RetryPolicy struct {
	// MaxAttempts is the TOTAL number of tries including the first.
	// A value <= 1 disables retrying (the decorator is a no-op wrap).
	MaxAttempts int

	// InitialBackoff is the wait before the first retry. Each later
	// wait multiplies by Multiplier, capped at MaxBackoff.
	InitialBackoff time.Duration

	// MaxBackoff caps a single wait so exponential growth stays bounded.
	MaxBackoff time.Duration

	// Multiplier is the exponential growth factor (> 1 to grow).
	Multiplier float64

	// Jitter randomises each wait in [delay/2, delay] to avoid
	// synchronised retry storms across concurrent workers.
	Jitter bool

	// Strategy selects how each wait is computed. StrategyBackoff (the
	// default / empty value) always uses the exponential backoff above.
	// StrategyRetryAfter honours a provider-supplied Retry-After header
	// when present and falls back to the backoff otherwise.
	Strategy string
}

// enabled reports whether the policy actually retries.
func (p RetryPolicy) enabled() bool { return p.MaxAttempts > 1 }

// backoff returns the wait before the given retry. attempt is 1-based:
// the wait before the FIRST retry (i.e. after the 1st failed try) is
// attempt=1, before the second retry attempt=2, etc.
func (p RetryPolicy) backoff(attempt int) time.Duration {
	d := float64(p.InitialBackoff)
	for i := 1; i < attempt; i++ {
		d *= p.Multiplier
		if d >= float64(p.MaxBackoff) {
			d = float64(p.MaxBackoff)
			break
		}
	}
	wait := time.Duration(d)
	if wait > p.MaxBackoff {
		wait = p.MaxBackoff
	}
	if p.Jitter && wait > 0 {
		// Range [wait/2, wait].
		half := wait / 2
		wait = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	return wait
}

// retryAfterFn extracts a server-provided Retry-After delay from a
// provider failure. It returns false when the error carries no usable
// hint, in which case the caller falls back to the exponential backoff.
// Indirected so tests can inject deterministic values.
var retryAfterFn = defaultRetryAfter

// defaultRetryAfter understands the Anthropic SDK's typed API error,
// reading the Retry-After header off the underlying HTTP response. The
// header is either an integer number of seconds or an HTTP date.
func defaultRetryAfter(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	var apiErr *anthropicsdk.Error
	if !errors.As(err, &apiErr) || apiErr.Response == nil {
		return 0, false
	}
	return parseRetryAfter(apiErr.Response.Header.Get("Retry-After"))
}

// parseRetryAfter parses an HTTP Retry-After value, accepting both the
// delta-seconds and HTTP-date forms. Non-positive or unparseable values
// yield ok=false.
func parseRetryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d, true
		}
	}
	return 0, false
}

// retryHook is an optional observer fired before each backoff sleep.
// Used by tests to assert wait counts/durations without real sleeps;
// nil in production. The package-level var keeps the decorator's public
// surface (a model.LLM) unchanged.
var retryHook func(attempt int, wait time.Duration, err string)

// sleepFn is indirected so tests can run instantly. Defaults to a
// context-aware sleep.
var sleepFn = func(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// withRetry wraps an LLM so transient provider failures are retried
// with an incremental exponential backoff. When the policy is disabled
// the inner model is returned unwrapped so there is exactly zero
// overhead on the common path.
func withRetry(inner model.LLM, policy RetryPolicy) model.LLM {
	if inner == nil || !policy.enabled() {
		return inner
	}
	return &retryingLLM{inner: inner, policy: policy}
}

// retryingLLM is the decorator. It re-invokes GenerateContent on a
// failed attempt, but ONLY when the failure happened before any usable
// content was produced — retrying a stream that already emitted text
// would duplicate output, so a mid-stream failure is surfaced as-is.
type retryingLLM struct {
	inner  model.LLM
	policy RetryPolicy
}

var _ model.LLM = (*retryingLLM)(nil)

func (r *retryingLLM) Name() string { return r.inner.Name() }

// step is one buffered (response, error) pair drained from an attempt.
type step struct {
	resp *model.LLMResponse
	err  error
}

func (r *retryingLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		var lastErr string
		for attempt := 1; attempt <= r.policy.MaxAttempts; attempt++ {
			steps, producedContent, failed, failMsg, failErr := r.drain(ctx, req, stream)

			// Success, or a failure that came AFTER we already saw
			// real content (cannot safely retry without dup): forward
			// the attempt verbatim and stop.
			retryable := failed && !producedContent && attempt < r.policy.MaxAttempts && ctx.Err() == nil
			if !retryable {
				for _, s := range steps {
					if !yield(s.resp, s.err) {
						return
					}
				}
				return
			}

			// Retryable failure: wait, then try again. The wait is the
			// server's Retry-After when the strategy asks for it and the
			// failure carries one; otherwise the exponential backoff.
			lastErr = failMsg
			wait := r.waitFor(attempt, failErr)
			if retryHook != nil {
				retryHook(attempt, wait, lastErr)
			}
			sleepFn(ctx, wait)
			if ctx.Err() != nil {
				// Context died during the wait: surface whatever the
				// last attempt said rather than spinning.
				for _, s := range steps {
					if !yield(s.resp, s.err) {
						return
					}
				}
				return
			}
		}
	}
}

// waitFor resolves the delay before the next retry, applying the
// configured strategy. Under StrategyRetryAfter a server-supplied
// Retry-After wins; every other case falls back to the exponential
// backoff so a missing header never breaks the retry loop.
func (r *retryingLLM) waitFor(attempt int, failErr error) time.Duration {
	if r.policy.Strategy == StrategyRetryAfter {
		if d, ok := retryAfterFn(failErr); ok {
			return d
		}
	}
	return r.policy.backoff(attempt)
}

// drain runs ONE attempt to completion, buffering every yielded pair.
// It reports whether any usable (non-error, non-empty) content was
// produced, whether the attempt ended in a retryable failure, and the
// last failing error (if any) so the caller can inspect provider hints
// such as a Retry-After header.
func (r *retryingLLM) drain(ctx context.Context, req *model.LLMRequest, stream bool) (steps []step, producedContent, failed bool, failMsg string, failErr error) {
	for resp, err := range r.inner.GenerateContent(ctx, req, stream) {
		steps = append(steps, step{resp: resp, err: err})
		switch {
		case err != nil:
			failed = true
			failMsg = err.Error()
			failErr = err
		case resp != nil && (resp.ErrorCode != "" || resp.ErrorMessage != ""):
			failed = true
			failMsg = strings.TrimSpace(resp.ErrorCode + " " + resp.ErrorMessage)
		case resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0:
			producedContent = true
		}
	}
	return steps, producedContent, failed, failMsg, failErr
}
