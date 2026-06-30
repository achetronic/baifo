// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"

	"google.golang.org/adk/session"

	baifoagent "github.com/achetronic/baifo/internal/agent"
	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/facade"
)

// ContextGuardStatus implements facade.Facade. It reflects the root
// agent's context_guard configuration against the live session state
// the contextguard plugin maintains, producing the gauge the TUI footer
// renders and the fingerprint it diffs to detect a fresh compaction.
//
// The whole thing is best-effort: any missing piece (no guard block, no
// active session, unreadable state) collapses to a quiet zero value so
// the footer simply hides the chip rather than surfacing an error.
func (a *App) ContextGuardStatus(ctx context.Context) facade.ContextGuardStatus {
	a.mu.RLock()
	var (
		guard    config.ContextGuardConfig
		modelID  string
		rootName string
	)
	if root := a.rootTemplate(); root != nil {
		guard = root.ContextGuard
		modelID = root.LLM.Model
		rootName = root.Name
	}
	sessions := a.sessions
	userID := a.userID
	sid := a.sessionID
	a.mu.RUnlock()

	if !guard.Enabled || rootName == "" {
		return facade.ContextGuardStatus{}
	}

	strategy := guard.Strategy
	if strategy == "" {
		strategy = "threshold"
	}
	out := facade.ContextGuardStatus{Enabled: true, Strategy: strategy}

	if sessions == nil || sid == "" {
		return out
	}
	resp, err := sessions.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: userID, SessionID: sid,
	})
	if err != nil || resp == nil || resp.Session == nil {
		return out
	}
	st := resp.Session.State()

	// Fingerprint = compaction watermark + summary length. Either field
	// changing means a new compaction was recorded; both together make
	// accidental collisions across distinct compactions negligible.
	summary := guardStateString(st, baifoagent.GuardStateSummaryPrefix+rootName)
	if summary != "" {
		watermark := guardStateInt(st, baifoagent.GuardStateSummarizedAtPrefix+rootName)
		out.Fingerprint = fmt.Sprintf("%d:%d", watermark, len(summary))
		out.Summary = summary
	}

	switch strategy {
	case "sliding_window":
		maxTurns := guard.MaxTurns
		if maxTurns <= 0 {
			maxTurns = baifoagent.GuardDefaultMaxTurns
		}
		total := resp.Session.Events().Len()
		atCompaction := guardStateInt(st, baifoagent.GuardStateContentsAtPrefix+rootName)
		used := total - atCompaction
		if used < 0 {
			used = 0
		}
		out.Used = used
		out.Limit = maxTurns
		out.Percent = guardPercent(used, maxTurns)
	default: // threshold
		window := guard.MaxTokens
		if window <= 0 {
			window = baifoagent.GuardContextWindow(modelID)
		}
		threshold := baifoagent.GuardThreshold(window)
		used := guardStateInt(st, baifoagent.GuardStateRealTokensPrefix+rootName)
		out.Used = used
		out.Limit = threshold
		out.Percent = guardPercent(used, threshold)
	}
	return out
}

// guardPercent returns used/limit as an integer percentage clamped to
// the 0..100 range. A non-positive limit yields 0 (nothing to gauge).
func guardPercent(used, limit int) int {
	if limit <= 0 {
		return 0
	}
	p := used * 100 / limit
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// guardStateInt reads an integer-valued session-state key, coping with
// the several numeric shapes a value can take after a round-trip
// through JSON persistence (int, int64, float64).
func guardStateInt(st session.State, key string) int {
	v, err := st.Get(key)
	if err != nil || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// guardStateString reads a string-valued session-state key, returning
// the empty string when the key is absent or not a string.
func guardStateString(st session.State, key string) string {
	v, err := st.Get(key)
	if err != nil || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
