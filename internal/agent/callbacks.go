// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"

	"github.com/achetronic/baifo/internal/audit"
	"github.com/achetronic/baifo/internal/secrets"
)

// expandedPairs is a process-wide map keyed by tool.Context.FunctionCallID
// that holds the (secret name → raw value) pairs the expander
// substituted for one specific tool call. The before-tool callback
// populates it; the after-tool redactor consumes it and removes the
// entry. We use a sync.Map keyed by the ADK-assigned, globally unique
// FunctionCallID so concurrent tool calls do not collide.
//
// This is the simplest correct way to share state between before- and
// after- callbacks without mutating tool.Context (which ADK does not
// allow) or holding global locks.
var expandedPairs sync.Map // map[string]map[string]string

// makeBeforeExpand returns a BeforeToolCallback that:
//   - rewrites ${secret:NAME} placeholders in the args (mutating the
//     map in place so ADK uses the expanded values);
//   - stashes the (name → raw value) pairs in expandedPairs under the
//     FunctionCallID so the after-tool redactor can find them;
//   - returns (nil, nil) so ADK proceeds with the tool call.
//
// When the secrets store is unavailable, the expander leaves
// placeholders in place and the tool surfaces a clear error.
//
// The opaqueTools set lists tool names whose arguments must NOT be
// expanded (typically the spawn tools like `spawn_static_agent`,
// `spawn_dynamic_agent`, `spawn_dynamic_agents`). Spawn tools take
// agent specs as input, including the child's system prompt and
// initial user message. Without this guard, a ${secret:NAME}
// placeholder embedded in such a field would be eagerly substituted
// with the raw value, baking the plaintext into the child's prompt
// and bypassing the child agent's own allowlist (since the root
// holds AllowAll). Skipping expansion for spawn tools keeps the
// placeholder literal all the way to the child agent's builder; the
// child's own BeforeToolCallback then decides at *its* tool boundary
// whether to expand, using *its* (typically narrower) allower. See
// `internal/tools/spawn.OpaqueToolNames` and SECRETS.md for the full
// rationale.
//
// The set is keyed by exact tool name. nil/empty disables the guard.
func makeBeforeExpand(store *secrets.Store, allower secrets.Allower, opaqueTools map[string]struct{}) llmagent.BeforeToolCallback {
	return func(ctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		if store == nil {
			return nil, nil
		}
		if t != nil {
			if _, opaque := opaqueTools[t.Name()]; opaque {
				// This tool's args are forwarded verbatim so the
				// expander stays out of the way. The placeholder
				// will be expanded (or not) at the child agent's
				// own tool boundary.
				return nil, nil
			}
		}

		// Wrap args in a fresh outer map so secrets.Expand sees a
		// map[string]any to walk.
		outer := map[string]any(args)

		expanded, derivedCtx, err := secrets.Expand(ctx, store, allower, outer)
		if err != nil {
			return nil, err
		}
		writeMapInPlace(args, expanded.(map[string]any))

		// Persist the pairs across the callback boundary so the
		// redactor can scrub them out of the result.
		if pairs := secrets.ExpandedPairs(derivedCtx); len(pairs) > 0 {
			expandedPairs.Store(ctx.FunctionCallID(), pairs)
		}
		return nil, nil
	}
}

// makeAfterRedact returns an AfterToolCallback that scrubs raw secret
// values out of the tool result. It runs two passes:
//
//  1. **Targeted pass**: replace every raw value the BeforeToolCallback
//     just substituted (read from `expandedPairs` keyed by the current
//     FunctionCallID, then deleted). This is the "tools that reflect
//     their input in their output" defence: cheap and always on.
//
//  2. **Comprehensive pass**: when `scrubAll` is true and `store` is
//     non-nil, snapshot every secret in the store once per call and
//     replace any value that appears in the result, even ones the
//     model never asked for. This closes the "tools that emit
//     secrets they were not given" gap (e.g. an MCP echoing a debug
//     header that contains a token from another secret in the store).
//
//     `minLen` is the floor below which a stored value is excluded
//     from the comprehensive pass. Short values (e.g. `password:
//     "1234"`) would otherwise cause catastrophic false positives,
//     redacting unrelated occurrences of those substrings inside
//     legitimate tool output. The targeted pass above still scrubs
//     short values when the LLM explicitly referenced them via a
//     ${secret:NAME} placeholder, so the trade-off is safe: short
//     secrets stay protected on the path that matters most.
//
// The result map is mutated in place so that the audit callback
// running after this one logs the redacted view, never the raw one.
func makeAfterRedact(store *secrets.Store, scrubAll bool, minLen int) llmagent.AfterToolCallback {
	return func(ctx tool.Context, _ tool.Tool, _, result map[string]any, _ error) (map[string]any, error) {
		if result == nil {
			// Still drain the expandedPairs entry so it does not
			// leak across the sync.Map between unrelated calls.
			expandedPairs.LoadAndDelete(ctx.FunctionCallID())
			return nil, nil
		}

		// Pass 1: targeted.
		if raw, ok := expandedPairs.LoadAndDelete(ctx.FunctionCallID()); ok {
			redactInPlace(result, raw.(map[string]string))
		}

		// Pass 2: comprehensive. Off when scrubAll is false or the
		// store is unavailable; both are legitimate (tests can build
		// a Builder without a store).
		if scrubAll && store != nil {
			snap, err := store.Snapshot()
			if err == nil && len(snap) > 0 {
				filtered := filterByMinLen(snap, minLen)
				if len(filtered) > 0 {
					redactInPlace(result, filtered)
				}
			}
		}
		return nil, nil
	}
}

// filterByMinLen drops entries whose value is shorter than minLen.
// We use length on the raw bytes, not the rune count, because the
// matcher works on UTF-8 byte substrings; misjudging multi-byte
// characters as "shorter than they look" is the conservative
// direction (we err toward NOT redacting a borderline case rather
// than scrubbing a 2-character substring all over the result).
func filterByMinLen(pairs map[string]string, minLen int) map[string]string {
	if minLen <= 0 {
		// No floor → everything goes through. Documented as
		// dangerous in CONFIG.md but supported for power users.
		return pairs
	}
	out := make(map[string]string, len(pairs))
	for name, raw := range pairs {
		if len(raw) < minLen {
			continue
		}
		out[name] = raw
	}
	return out
}

// makeAfterAudit returns an AfterToolCallback that appends one Entry
// per tool call to the audit log. A nil Recorder is a no-op so test
// scenarios can build agents without auditing.
func makeAfterAudit(agentID string, rec *audit.Recorder) llmagent.AfterToolCallback {
	return func(_ tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		entry := audit.Entry{
			Timestamp: time.Now().UTC(),
			AgentID:   agentID,
			ToolName:  t.Name(),
			Args:      args,
			Result:    result,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		_ = rec.Record(entry)
		return nil, nil
	}
}

// writeMapInPlace makes dst equal to src by clearing dst and copying
// every key from src. We mutate in place because ADK gives us the args
// map by reference and re-assigning the local would not propagate.
func writeMapInPlace(dst, src map[string]any) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}

// redactInPlace walks every string in result (including inside nested
// maps and slices) and replaces any occurrence of a raw value from
// pairs with the corresponding ${secret:NAME} placeholder.
func redactInPlace(result map[string]any, pairs map[string]string) {
	for k, v := range result {
		result[k] = redactValue(v, pairs)
	}
}

func redactValue(v any, pairs map[string]string) any {
	switch val := v.(type) {
	case string:
		return scrub(val, pairs)
	case map[string]any:
		redactInPlace(val, pairs)
		return val
	case []any:
		for i, item := range val {
			val[i] = redactValue(item, pairs)
		}
		return val
	default:
		return val
	}
}

func scrub(s string, pairs map[string]string) string {
	for name, raw := range pairs {
		if raw == "" {
			continue
		}
		s = strings.ReplaceAll(s, raw, "${secret:"+name+"}")
	}
	return s
}
