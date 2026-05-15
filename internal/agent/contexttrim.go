// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

// This file implements the context-trim BeforeModel plugin, wired via
// guardrails.trim_oversized_user_text in baifo.yaml.
//
// Background: when a session is resumed under a different root agent name,
// the ADK's ConvertForeignEvent (internal/llminternal/contents_processor.go)
// rewrites the entire old transcript — including unbounded tool results — into
// user-role text parts prefixed with "For context: [agent] `exec` tool
// returned result: ...". Megabytes of binary data can enter the parent's
// context window and instantly trigger context-guard compaction, which
// distorts the token accounting the guard relies on.
//
// Fix: this plugin runs its BeforeModelCallback before every model call and
// truncates any user-role text part whose byte length exceeds maxChars. The
// LLMRequest is rebuilt from session events each turn, so the mutation is
// purely ephemeral — the stored session keeps full fidelity.
//
// Ordering matters: this plugin must be prepended to the runner's plugin
// list (before the contextguard plugin) so the guard's BeforeModel token
// counting sees the already-trimmed request.
package agent

import (
	"fmt"
	"unicode/utf8"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
)

// BuildContextTrimPlugin returns an ADK plugin whose BeforeModelCallback
// truncates every user-role text part of the outgoing LLMRequest that
// exceeds maxChars bytes. Returns nil when maxChars <= 0 (trimming
// disabled), so callers can test for nil to skip wiring.
func BuildContextTrimPlugin(maxChars int) *plugin.Plugin {
	if maxChars <= 0 {
		return nil
	}

	cb := llmagent.BeforeModelCallback(func(_ agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
		for _, c := range req.Contents {
			if c == nil || c.Role != "user" {
				continue
			}
			for _, p := range c.Parts {
				if p == nil || p.Text == "" || p.Thought {
					continue
				}
				if len(p.Text) > maxChars {
					p.Text = trimUserPart(p.Text, maxChars)
				}
			}
		}
		// Return (nil, nil) to let the model call proceed normally.
		// A non-nil *model.LLMResponse here would short-circuit the
		// entire model call — intentionally avoided.
		return nil, nil
	})

	p, _ := plugin.New(plugin.Config{
		Name:                "baifo-context-trim",
		BeforeModelCallback: cb,
	})
	return p
}

// WithContextTrim prepends the context-trim plugin to cfg.Plugins when
// trim is non-nil. Prepending matters: the contextguard's BeforeModel
// token counting must see the already-trimmed request.
func WithContextTrim(cfg runner.PluginConfig, trim *plugin.Plugin) runner.PluginConfig {
	if trim == nil {
		return cfg
	}
	plugins := make([]*plugin.Plugin, 0, 1+len(cfg.Plugins))
	plugins = append(plugins, trim)
	plugins = append(plugins, cfg.Plugins...)
	cfg.Plugins = plugins
	return cfg
}

// trimUserPart returns s truncated to at most maxChars bytes, cut back to
// a clean UTF-8 rune boundary, with a marker appended that records the
// original byte length. The marker format is:
//
//	"\n[context-trim: part truncated to N of M chars]"
//
// s must have len(s) > maxChars; callers are responsible for the guard.
func trimUserPart(s string, maxChars int) string {
	orig := len(s)
	cut := maxChars
	// Walk back from cut until we land on the start byte of a rune so
	// we never produce an incomplete multi-byte sequence.
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n[context-trim: part truncated to %d of %d chars]", cut, orig)
}
