// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// placeholderRE matches ${secret:NAME} where NAME is [A-Za-z0-9_-]+.
// Anchoring inside a string is done by the walker; this regex finds
// every occurrence in a single string.
var placeholderRE = regexp.MustCompile(`\$\{secret:([A-Za-z0-9_\-]+)\}`)

// expandedKey is the unexported context-key type used to attach the
// (name → raw value) map produced by Expand. Using a typed key avoids
// collisions with other packages that stash values in the context.
type expandedKey struct{}

// expanded is the value stored under expandedKey: a copy of every
// (name, raw value) pair the expander substituted in a given tool call.
// The redactor reads this back to know what literal strings to scrub.
type expanded struct {
	pairs map[string]string
}

// Allower decides whether a given agent is allowed to see the named
// secret. See AllowerFor for how an allowlist slice maps to the
// concrete implementations: AllowAll, AllowNone, AllowList.
type Allower interface {
	Allowed(name string) bool
}

// AllowAll permits every secret. Reserved for the root agent, which
// is the user-facing coordinator and the frontier of trust: it must
// be able to dereference any secret the operator stored. Sub-agents
// (static or dynamic) never receive this Allower — they go through
// AllowerFor with their explicit allowlist.
type AllowAll struct{}

// Allowed always returns true.
func (AllowAll) Allowed(string) bool { return true }

// AllowNone forbids every secret. This is the least-privilege default
// for any agent built from an explicit (possibly empty) allowlist:
// static templates with no allowed_secrets, dynamic workers whose
// spawn call omitted the field, etc. The model still sees the
// placeholder name in tool args; the expander leaves it untouched.
type AllowNone struct{}

// Allowed always returns false.
func (AllowNone) Allowed(string) bool { return false }

// AllowList implements Allower with a whitelist. Names not in the list
// are rejected. Used when an agent's allowed_secrets is non-empty.
type AllowList struct {
	names map[string]struct{}
}

// NewAllowList returns an Allower that permits exactly the given names.
func NewAllowList(names []string) AllowList {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return AllowList{names: m}
}

// Allowed reports whether name is in the whitelist.
func (a AllowList) Allowed(name string) bool {
	_, ok := a.names[name]
	return ok
}

// AllowerFor returns an Allower for an explicit allowlist.
//
//   - len(allowed) == 0  → AllowNone (least-privilege default for any
//     agent built from an explicit list, regardless of whether the
//     slice is nil or just empty).
//   - non-empty list     → AllowList of those names.
//
// The root agent does NOT go through this function — it is wired
// directly to AllowAll in the builder. See agent.Spec.UnrestrictedSecrets
// for the toggle.
func AllowerFor(allowed []string) Allower {
	if len(allowed) == 0 {
		return AllowNone{}
	}
	return NewAllowList(allowed)
}

// Expand walks an arbitrary tool-call argument value and substitutes
// every ${secret:NAME} placeholder with the real secret value, provided
// (a) the agent's allowlist permits NAME and (b) the secret exists.
//
// Placeholders for unknown or disallowed names are left in place so the
// downstream tool surfaces a clear "Authorization header malformed:
// literal ${secret:foo}" type of error, which the LLM can recover from.
//
// The function also returns a context derived from ctx that stores the
// (name → raw value) pairs actually substituted. Pass that derived
// context to Redact after the tool returns.
func Expand(ctx context.Context, store *Store, allower Allower, args any) (any, context.Context, error) {
	if store == nil {
		return args, ctx, fmt.Errorf("nil secrets store")
	}
	if allower == nil {
		// Defensive default. Reaching this branch means a caller
		// forgot to set the allower, which is a wiring bug. Falling
		// back to AllowNone errs on the side of least-privilege: the
		// agent's tool calls will keep placeholders unexpanded and
		// the tool will fail with a clear "literal ${secret:...}"
		// error rather than leaking a secret silently.
		allower = AllowNone{}
	}

	pairs := map[string]string{}
	out, err := walkStrings(args, func(s string) (string, error) {
		return placeholderRE.ReplaceAllStringFunc(s, func(match string) string {
			name := extractName(match)
			if !allower.Allowed(name) {
				return match
			}
			val, err := store.Get(name)
			if err != nil {
				// Unknown / undecryptable: leave the placeholder in place.
				return match
			}
			pairs[name] = val
			return val
		}), nil
	})
	if err != nil {
		return args, ctx, err
	}

	newCtx := context.WithValue(ctx, expandedKey{}, expanded{pairs: pairs})
	return out, newCtx, nil
}

// Redact walks an arbitrary tool-call result and replaces every raw
// secret value that was substituted in this call (looked up via ctx)
// with its ${secret:NAME} placeholder. Secrets that were not actually
// expanded in this call are NOT scanned: that would be expensive and
// false-positive prone.
func Redact(ctx context.Context, result any) (any, error) {
	pairs := ExpandedPairs(ctx)
	if len(pairs) == 0 {
		return result, nil
	}
	return walkStrings(result, func(s string) (string, error) {
		for name, raw := range pairs {
			if raw == "" {
				continue
			}
			s = strings.ReplaceAll(s, raw, "${secret:"+name+"}")
		}
		return s, nil
	})
}

// ExpandedPairs returns the (name → raw value) pairs that Expand
// substituted during this call. Callers that need to scrub a result
// outside the standard Redact pipeline can use this to read the
// pairs directly. Returns an empty map when nothing was expanded.
func ExpandedPairs(ctx context.Context) map[string]string {
	exp, ok := ctx.Value(expandedKey{}).(expanded)
	if !ok {
		return nil
	}
	return exp.pairs
}

// extractName pulls NAME out of a string like "${secret:NAME}". Returns
// an empty string when the input does not match — caller leaves the
// placeholder untouched in that case.
func extractName(placeholder string) string {
	m := placeholderRE.FindStringSubmatch(placeholder)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// walkStrings traverses every string inside an arbitrary value
// (map[string]any, []any, plain string) and applies fn to it. Other
// types pass through unchanged. Errors bubble up immediately.
func walkStrings(v any, fn func(string) (string, error)) (any, error) {
	switch val := v.(type) {
	case string:
		return fn(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			converted, err := walkStrings(item, fn)
			if err != nil {
				return nil, err
			}
			out[k] = converted
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			converted, err := walkStrings(item, fn)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	default:
		return val, nil
	}
}
