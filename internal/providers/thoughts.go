// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package providers

import (
	"context"
	"iter"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// StripStaleThoughts returns a copy of req whose history carries no
// stale reasoning parts (genai.Part with Thought=true).
//
// Sessions in baifo are persisted provider-agnostically, so after a
// mid-session provider switch the history can hold reasoning parts
// whose opaque ThoughtSignature was minted by another provider. Gemini
// rejects those with a 400 "Corrupted thought signature", and even a
// provider's own reasoning from closed turns is dead weight that gets
// billed again as plain input tokens on every call.
//
// Reasoning from the turn still in flight must survive, because
// providers such as Anthropic and Gemini expect it echoed back while a
// tool-call loop is running. The ADK may persist one logical model turn
// as several consecutive model-role contents (text in one event, the
// function call in the next), so the active turn is defined as the
// contiguous trailing run of model-role and tool-response contents
// after the last real user message. Thoughts on model-role contents
// inside that run are kept; thoughts anywhere else, including ones
// carried under a user role (foreign agent context), are dropped.
//
// req itself is never mutated: contents are shallow-cloned with their
// parts filtered, so the ADK session history stays intact.
func StripStaleThoughts(req *model.LLMRequest) *model.LLMRequest {
	if req == nil || len(req.Contents) == 0 {
		return req
	}

	// activeStart is the index of the first content of the in-flight
	// turn: from here to the end everything is either model-role or a
	// tool response. When the history ends with a plain user message
	// the loop breaks on the first iteration and activeStart stays at
	// len(Contents), meaning no turn is in flight and every thought in
	// the history is stale.
	activeStart := len(req.Contents)
	for i := len(req.Contents) - 1; i >= 0; i-- {
		if !isModelContent(req.Contents[i]) && !isToolResponseContent(req.Contents[i]) {
			break
		}
		activeStart = i
	}

	clone := *req
	clone.Contents = make([]*genai.Content, len(req.Contents))
	for i, c := range req.Contents {
		cc := *c
		cc.Parts = make([]*genai.Part, 0, len(c.Parts))
		for _, p := range c.Parts {
			// A thought survives only on a model-role content inside
			// the active trailing run. Anything else (older model
			// turns, user-role thoughts from foreign agent context)
			// is dropped.
			if p.Thought && !(i >= activeStart && isModelContent(c)) {
				continue
			}
			cc.Parts = append(cc.Parts, p)
		}
		clone.Contents[i] = &cc
	}
	return &clone
}

// isModelContent reports whether the content was authored by the model.
// An empty role defaults to user, mirroring genai's own roleString.
func isModelContent(c *genai.Content) bool {
	return c.Role == genai.RoleModel
}

// isToolResponseContent reports whether the content carries at least one
// FunctionResponse part, i.e. it is the runner feeding tool results back
// to the model mid-loop rather than a human message.
func isToolResponseContent(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

// WrapStripThoughts decorates m so every GenerateContent call first runs
// StripStaleThoughts on the request. Every provider build function must
// wrap its model with this before returning it: the strip is a
// correctness fix (stale cross-provider thought signatures are rejected
// with a 400), so it lives on the inevitable path — the model itself —
// rather than in a runner plugin that not every consumer goes through.
// See .agents/TODO.md for the deferred plugin-migration discussion.
func WrapStripThoughts(m model.LLM) model.LLM {
	return stripThoughtsWrapper{m}
}

// stripThoughtsWrapper is the model.LLM decorator returned by
// WrapStripThoughts. It embeds the wrapped LLM so every other method
// passes through untouched.
type stripThoughtsWrapper struct {
	model.LLM
}

// GenerateContent filters stale thoughts from req and delegates to the
// wrapped model.
func (w stripThoughtsWrapper) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return w.LLM.GenerateContent(ctx, StripStaleThoughts(req), stream)
}
