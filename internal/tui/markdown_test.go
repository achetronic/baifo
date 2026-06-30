// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"
)

// TestMarkdownRendererBasic exercises the cache end-to-end with a
// trivial bold input: the rendered string must contain the original
// word (Glamour wraps it in ANSI but never strips text) and must
// not equal the input (some styling must have been applied).
func TestMarkdownRendererBasic(t *testing.T) {
	c := newMarkdownCache()
	out := c.render("k", "**hello**", 60, true)
	if !strings.Contains(out, "hello") {
		t.Fatalf("output must contain the original word: %q", out)
	}
	if out == "**hello**" {
		t.Fatalf("output equals input — nothing was styled: %q", out)
	}
}

// TestMarkdownRendererThrottle confirms the cache returns the
// previous output when called within the throttle window with the
// same key, and re-renders when force=true even within the window.
func TestMarkdownRendererThrottle(t *testing.T) {
	c := newMarkdownCache()
	first := c.render("k", "hello", 60, false)
	// Same input, force=false, well within markdownThrottle:
	// should be the cached output (string identity check).
	second := c.render("k", "hello", 60, false)
	if first != second {
		t.Errorf("cached render diverged from first call:\n  first=%q\n  second=%q", first, second)
	}

	// Different text + force=true → cache rewrites.
	third := c.render("k", "**hello**", 60, true)
	if third == first {
		t.Errorf("force=true didn't trigger a fresh render")
	}
}

// TestPrepareForGlamourClosesOpenFence checks the half-formed
// Markdown guard: when a code fence is open at the end of the
// input, prepareForGlamour appends a closing fence so Glamour
// doesn't swallow everything below.
func TestPrepareForGlamourClosesOpenFence(t *testing.T) {
	in := "Here:\n\n```go\nfunc foo() {\n"
	got := prepareForGlamour(in)
	// We expect at least one closing fence somewhere AFTER the
	// dangling code line.
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "```") {
		t.Errorf("expected trailing ``` close, got:\n%s", got)
	}
}

// TestPrepareForGlamourClosedInputUnchanged verifies a fully
// closed body comes out untouched (no spurious fence appended).
func TestPrepareForGlamourClosedInputUnchanged(t *testing.T) {
	in := "Plain paragraph.\n\n```go\nfunc foo() {}\n```\nDone."
	got := prepareForGlamour(in)
	if got != in {
		t.Errorf("closed body was rewritten:\n  in=%q\n  out=%q", in, got)
	}
}

// TestMarkdownRendererIdenticalInputNeverReRenders is the regression
// guard for the per-chunk full-transcript repaint: historical (no
// longer streaming) messages come through renderMessages with
// force=true on EVERY SetMessages pass, so an unchanged input must hit
// the cache instead of re-running Glamour — otherwise each streamed
// chunk re-renders every past message and the TUI freezes on long
// conversations.
func TestMarkdownRendererIdenticalInputNeverReRenders(t *testing.T) {
	c := newMarkdownCache()
	first := c.render("k", "# title\n\nsome **bold** prose", 60, true)
	renders := c.renders
	if renders == 0 {
		t.Fatal("first render must invoke Glamour")
	}
	for i := 0; i < 5; i++ {
		out := c.render("k", "# title\n\nsome **bold** prose", 60, true)
		if out != first {
			t.Fatalf("cached output diverged on pass %d", i)
		}
	}
	if c.renders != renders {
		t.Fatalf("identical input re-invoked Glamour: %d extra renders", c.renders-renders)
	}

	// Changed input must still re-render (force bypasses the throttle).
	c.render("k", "# title\n\nsome **bold** prose, extended", 60, true)
	if c.renders != renders+1 {
		t.Fatalf("changed input with force=true must re-render exactly once, got %d", c.renders-renders)
	}
}
