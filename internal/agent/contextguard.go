// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"sync"

	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"

	"github.com/achetronic/adk-utils-go/plugin/contextguard"

	"github.com/achetronic/baifo/internal/config"
)

// Session-state key prefixes mirror the (unexported) constants the
// contextguard plugin writes per agent. baifo reads them back to drive
// the TUI footer gauge and the "context compacted" notice. They are
// part of the plugin's observable contract; keep them in sync with
// adk-utils-go/plugin/contextguard. The agent name is appended to each
// prefix to form the full key.
const (
	// GuardStateRealTokensPrefix holds the last real prompt-token count
	// reported by the provider for this agent. Reset to 0 right after a
	// threshold compaction, so it doubles as the "used" gauge value.
	GuardStateRealTokensPrefix = "__context_guard_real_tokens_"

	// GuardStateSummaryPrefix holds the running conversation summary.
	// Non-empty once at least one compaction has happened.
	GuardStateSummaryPrefix = "__context_guard_summary_"

	// GuardStateSummarizedAtPrefix holds the token count recorded at the
	// last compaction. Used together with the summary length to build a
	// fingerprint the TUI diffs across turns to detect a fresh compaction.
	GuardStateSummarizedAtPrefix = "__context_guard_summarized_at_"

	// GuardStateContentsAtPrefix holds the Content count recorded at the
	// last sliding-window compaction. Used to compute turns since then.
	GuardStateContentsAtPrefix = "__context_guard_contents_at_compaction_"
)

// guardRegistry caches a single CrushRegistry so GuardContextWindow does
// not rebuild the (1000+ entry) catwalk map on every call. The plugin
// keeps its own private registry; this one is read-only and only feeds
// the TUI gauge, so sharing across calls is safe.
var (
	guardRegistry     *contextguard.CrushRegistry
	guardRegistryOnce sync.Once
)

func guardReg() *contextguard.CrushRegistry {
	guardRegistryOnce.Do(func() {
		guardRegistry = contextguard.NewCrushRegistry()
	})
	return guardRegistry
}

// GuardContextWindow returns the context window (in tokens) catwalk
// records for modelID, via the same registry the plugin consults.
// Unknown models fall back to the registry's conservative default.
func GuardContextWindow(modelID string) int {
	return guardReg().ContextWindow(modelID)
}

// GuardDefaultMaxTurns is the sliding-window turn limit baifo applies
// when a context_guard block selects sliding_window without max_turns.
// Mirrored by both agentOptionsFor (what the plugin actually uses) and
// the TUI gauge so the displayed denominator matches reality.
const GuardDefaultMaxTurns = 30

// GuardThreshold mirrors the plugin's computeBuffer logic: large windows
// (>= 200k) reserve a fixed 20k-token buffer, smaller ones reserve 20%.
// The threshold is the token count at which a threshold-strategy
// compaction fires, so it is the natural denominator for the gauge.
func GuardThreshold(window int) int {
	const largeWindow = 200_000
	buffer := 20_000
	if window < largeWindow {
		buffer = int(float64(window) * 0.20)
	}
	t := window - buffer
	if t < 1 {
		// Degenerate window (buffer ate everything, or a zero/garbage
		// value): fall back to the window itself, then to 1, so the
		// gauge never divides by a non-positive denominator.
		if window > 0 {
			t = window
		} else {
			t = 1
		}
	}
	return t
}

// AgentGuardSpec is one row in the BuildContextGuardConfig input.
// Each row registers the named agent with the contextguard plugin
// so it can summarise that agent's conversation when the configured
// threshold is hit. Used by both the root spec and every static
// template that opts into the plugin.
type AgentGuardSpec struct {
	// Name is the agent identifier the contextguard plugin keys
	// summaries by. For the root, this is cfg.Root.Name. For
	// static templates, this is the template's Name field.
	Name string

	// Config is the user-facing ContextGuardConfig from baifo.yaml
	// for this agent. An Enabled=false config disables the row
	// silently; the builder skips it.
	Config config.ContextGuardConfig
}

// BuildContextGuardConfig returns a runner.PluginConfig that, when
// attached to a runner, summarises any registered agent's
// conversation once it approaches its context-window threshold.
// Returns the zero value (no plugins) when no row asks for
// guarding, which lets runner.New treat it as a no-op.
//
// summariserLLM is the model the plugin invokes to produce the
// summary. We use the ROOT LLM in baifo even for static-template
// agents because instantiating every worker at boot just to grab
// its LLM pointer would be heavy, and a single capable summariser
// is fine for what's essentially a "compress this transcript" job.
//
// We use catwalk's embedded model database (via CrushRegistry) so
// each model's real context window is known without hitting the
// network at boot. Unknown models fall back to a conservative
// default (128k tokens / 4k output) inside the registry.
func BuildContextGuardConfig(summariserLLM model.LLM, rows []AgentGuardSpec) runner.PluginConfig {
	if summariserLLM == nil {
		return runner.PluginConfig{}
	}
	if !anyEnabled(rows) {
		return runner.PluginConfig{}
	}

	registry := contextguard.NewCrushRegistry()
	guard := contextguard.New(registry)
	for _, r := range rows {
		if !shouldGuard(r.Config) {
			continue
		}
		guard.Add(r.Name, summariserLLM, agentOptionsFor(r.Config)...)
	}
	return guard.PluginConfig()
}

// shouldGuard reports whether the given config slot opts the agent
// into the plugin. Default is OFF: a config with no context_guard
// block stays unguarded.
func shouldGuard(c config.ContextGuardConfig) bool {
	return c.Enabled
}

// anyEnabled is the fast-path short-circuit for BuildContextGuardConfig.
// Returns true as soon as the first enabled row is found so we don't
// even allocate the contextguard registry when nobody opts in.
func anyEnabled(rows []AgentGuardSpec) bool {
	for _, r := range rows {
		if shouldGuard(r.Config) {
			return true
		}
	}
	return false
}

// agentOptionsFor translates the user-facing ContextGuardConfig into
// the AgentOption variadic the plugin's Add method consumes. We map:
//
//   - strategy=sliding_window + max_turns => WithSlidingWindow
//   - max_tokens > 0                      => WithMaxTokens (overrides model window)
//   - strategy=threshold (default)        => no extra options needed
//
// Empty strings or zeros mean "use the plugin defaults", which are
// the threshold strategy backed by CrushRegistry's per-model window.
func agentOptionsFor(c config.ContextGuardConfig) []contextguard.AgentOption {
	var opts []contextguard.AgentOption
	switch c.Strategy {
	case "sliding_window":
		turns := c.MaxTurns
		if turns <= 0 {
			turns = GuardDefaultMaxTurns // the plugin's own default is 20 but our schema implies a turn-based override worth more headroom
		}
		opts = append(opts, contextguard.WithSlidingWindow(turns))
	}
	if c.MaxTokens > 0 {
		opts = append(opts, contextguard.WithMaxTokens(c.MaxTokens))
	}
	return opts
}
