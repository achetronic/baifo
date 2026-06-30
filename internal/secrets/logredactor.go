// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"log/slog"
	"strings"
	"sync"
)

// LogRedactor replaces every stored secret value that appears in a log
// attribute with its ${secret:NAME} placeholder. It implements the
// logging.Redactor interface (structural, no import of logging to avoid
// an import cycle) and is safe for concurrent use.
//
// The secret snapshot is cached between log lines and only refreshed
// when the Store's generation counter changes, so the expensive
// decrypt-all path (PBKDF2 + AES-GCM per entry) is amortised across
// as many log lines as happen between mutations. A nil Store or
// enabled=false makes every Redact call a cheap no-op.
type LogRedactor struct {
	mu      sync.Mutex
	store   *Store
	enabled bool
	minLen  int

	// cache state
	cachedGen  uint64
	cacheReady bool // false until first successful Snapshot
	snapshot   map[string]string
}

// NewLogRedactor creates a LogRedactor for the given Store. Pass a nil
// store when the store is not yet available at logging init time; call
// SetStore once the store is ready. enabled gates all redaction;
// minLen is the minimum byte length a secret value must have to be
// considered (mirrors SecretsConfig.EffectiveMinScrubLength).
func NewLogRedactor(store *Store, enabled bool, minLen int) *LogRedactor {
	return &LogRedactor{
		store:   store,
		enabled: enabled,
		minLen:  minLen,
	}
}

// SetStore replaces the underlying secrets Store and invalidates the
// cached snapshot. Safe to call concurrently; designed for use by
// ReloadFromDisk to point the live redactor at a freshly opened store
// without reconstructing the handler chain.
func (r *LogRedactor) SetStore(store *Store) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = store
	r.cacheReady = false // force refresh on next Redact
}

// SetConfig updates the enabled flag and minLen floor in place and
// invalidates the cached snapshot. Both changes take effect on the
// next Redact call.
func (r *LogRedactor) SetConfig(enabled bool, minLen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = enabled
	r.minLen = minLen
	r.cacheReady = false
}

// Redact implements logging.Redactor. It walks the attribute value,
// replacing any occurrence of a cached secret value with its
// ${secret:NAME} placeholder. Group attributes are recursed into so
// structured log lines are covered as well. Non-string typed values
// are rendered to a string only when a secret is actually found in
// them, preserving the original type otherwise.
func (r *LogRedactor) Redact(attr slog.Attr) slog.Attr {
	r.mu.Lock()
	enabled := r.enabled
	if !enabled || r.store == nil {
		r.mu.Unlock()
		return attr
	}

	// Refresh snapshot when the store mutated since we last looked.
	if !r.cacheReady || r.store.Generation() != r.cachedGen {
		snap, err := r.store.Snapshot()
		if err == nil {
			r.snapshot = filterRedactorByMinLen(snap, r.minLen)
			r.cachedGen = r.store.Generation()
			r.cacheReady = true
		}
		// On error keep the previous snapshot (or empty map on first
		// call). Degraded redaction is better than a logging panic.
	}
	snapshot := r.snapshot
	r.mu.Unlock()

	if len(snapshot) == 0 {
		return attr
	}

	return redactAttr(attr, snapshot)
}

// redactAttr applies snapshot redaction to one slog.Attr recursively.
func redactAttr(attr slog.Attr, snapshot map[string]string) slog.Attr {
	switch attr.Value.Kind() {
	case slog.KindGroup:
		// Recurse into every member of the group.
		src := attr.Value.Group()
		dst := make([]any, 0, len(src))
		for _, a := range src {
			dst = append(dst, redactAttr(a, snapshot))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(toAttrs(dst)...)}

	case slog.KindString:
		original := attr.Value.String()
		scrubbed := scrubString(original, snapshot)
		if scrubbed == original {
			return attr
		}
		return slog.String(attr.Key, scrubbed)

	default:
		// For non-string kinds: render to string, check for a hit,
		// and only downgrade to a string attr when a secret was found.
		// This keeps int/bool/duration values typed in the common case.
		rendered := attr.Value.String()
		scrubbed := scrubString(rendered, snapshot)
		if scrubbed == rendered {
			return attr
		}
		return slog.String(attr.Key, scrubbed)
	}
}

// toAttrs re-asserts the []any slice (which contains slog.Attr values)
// back to []slog.Attr for slog.GroupValue. The slog package only accepts
// the variadic any form from slog.Group helpers, so we do a typed loop.
func toAttrs(items []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(items))
	for _, item := range items {
		if a, ok := item.(slog.Attr); ok {
			out = append(out, a)
		}
	}
	return out
}

// scrubString replaces every occurrence of each secret raw value in s
// with the corresponding ${secret:NAME} placeholder.
func scrubString(s string, snapshot map[string]string) string {
	for name, raw := range snapshot {
		if raw == "" {
			continue
		}
		s = strings.ReplaceAll(s, raw, "${secret:"+name+"}")
	}
	return s
}

// filterRedactorByMinLen drops secret values whose byte length is below
// minLen. When minLen <= 0 all entries are kept (no floor).
func filterRedactorByMinLen(pairs map[string]string, minLen int) map[string]string {
	if minLen <= 0 {
		out := make(map[string]string, len(pairs))
		for k, v := range pairs {
			out[k] = v
		}
		return out
	}
	out := make(map[string]string, len(pairs))
	for name, raw := range pairs {
		if len(raw) >= minLen {
			out[name] = raw
		}
	}
	return out
}
