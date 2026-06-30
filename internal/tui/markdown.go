// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
)

// markdown.go renders LLM responses through Glamour with three
// pragmatic adaptations for the baifo chat:
//
//   1. Width-aware. The renderer is bound to the chat's content
//      width minus the selection-marker gutter so wraps match
//      rowSpans (the click→row map).
//
//   2. Throttled during streaming. We avoid re-rendering on every
//      chunk; instead we keep the last good render cached per
//      message and recompute at most once per markdownThrottle
//      window. The final chunk forces a re-render.
//
//   3. Tolerant of half-formed Markdown. When the streamed text
//      ends mid-code-fence, the open block is kept as plain text
//      until the fence closes; closed text above is rendered
//      normally. This avoids the "screen dance" Glamour otherwise
//      produces while a code block is being typed out.
//
// Failure-safe: if Glamour errors out for any reason, we fall back
// to the raw text so the chat never goes blank.

// markdownThrottle is the minimum interval between re-renders of
// the same in-flight message. Tuned high enough that even chatty
// LLMs hit it (most chunks arrive < 100ms apart); tuned low enough
// that the human eye doesn't perceive lag.
const markdownThrottle = 200 * time.Millisecond

// markdownWrapMargin is subtracted from the chat content width
// before being handed to Glamour. The selection-marker column
// (chatSelectionMarker, 2 cols) lives outside the rendered text;
// Glamour wraps at this narrower budget so its output fits next
// to the marker without horizontal overflow.
//
// Computed as a function (not a const) because chatSelectionMarker
// could in principle be widened; tests rely on it.
func markdownWrapMargin() int {
	const safety = 1 // 1 extra col so trailing spans never touch the edge
	return runeLen(chatSelectionMarker) + safety
}

func runeLen(s string) int { return len([]rune(s)) }

// markdownCache is the per-Model cache of in-flight Glamour renders.
// Keyed by message identity (we use the *Message.ToolCallID for
// tool rows or a synthetic key for plain messages). Lives on the
// Model so it survives across Update ticks without forcing the
// renderer to be a pointer receiver.
type markdownCache struct {
	mu       sync.Mutex
	entries  map[string]*markdownEntry
	renderer *glamour.TermRenderer
	width    int // last width the renderer was configured for

	// renders counts actual Glamour invocations. Tests use it to pin
	// the "identical input never re-renders" guarantee that keeps the
	// per-chunk repaint of the whole transcript cheap.
	renders int
}

// markdownEntry tracks one message's render state. lastInput is
// the raw text that produced lastOutput; we use it to short-circuit
// when the next chunk hasn't changed anything material (e.g.
// trailing whitespace stripped).
type markdownEntry struct {
	lastRenderedAt time.Time
	lastInput      string
	lastOutput     string
}

// newMarkdownCache builds an empty cache.
func newMarkdownCache() *markdownCache {
	return &markdownCache{entries: map[string]*markdownEntry{}}
}

// render returns the Glamour-rendered version of text for the
// given message key, throttled to one render per markdownThrottle
// window. When force is true (e.g. the streaming done message
// arrived), the throttle is bypassed and we always re-render.
//
// width is the chat's outer content width (contentWidth() minus
// the marker gutter); the cache invalidates and rebuilds the
// renderer whenever the width changes — this is rare (only on
// terminal resize).
//
// Returns the styled output; on Glamour error returns text
// unchanged so the chat never blanks out.
func (c *markdownCache) render(key, text string, width int, force bool) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	// Defensive: width can be 0 during early init / weird resize
	// transitions. Fall back to plain text in that case.
	if width < 8 {
		return text
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.renderer == nil || c.width != width {
		r, err := buildMarkdownRenderer(width)
		if err != nil {
			// Couldn't build the renderer (very unlikely): give
			// up gracefully on markdown forever for this session.
			c.renderer = nil
			return text
		}
		c.renderer = r
		c.width = width
		// Width changed: invalidate every cached render so
		// they re-flow at the new size on next access.
		c.entries = map[string]*markdownEntry{}
	}

	e, ok := c.entries[key]
	now := time.Now()
	if ok {
		// Same input as last time → return cache (cheap path, no
		// Glamour invocation needed). This MUST apply even when
		// force=true: every SetMessages pass renders the whole
		// transcript and historical messages come through with
		// force=true, so without this short-circuit each streamed
		// chunk re-ran Glamour over EVERY past message — an
		// O(history × chunks) repaint that visibly froze the TUI
		// on long conversations. The output is deterministic for
		// a given (text, width), so reusing it is always correct.
		if e.lastInput == text {
			return e.lastOutput
		}
		// Throttle window not yet elapsed → return the previous
		// render so the eye sees a stable frame. force bypasses
		// only this throttle, never the identity check above.
		if !force && now.Sub(e.lastRenderedAt) < markdownThrottle {
			return e.lastOutput
		}
	}

	prepared := prepareForGlamour(text)
	c.renders++
	out, err := c.renderer.Render(prepared)
	if err != nil {
		// On error, return the raw text. The user sees their
		// message un-styled but nothing breaks.
		return text
	}
	// Glamour likes to wrap output in a top/bottom blank line.
	// The chat already provides spacing between messages via
	// renderMessages, so we trim to keep the layout tight.
	out = strings.Trim(out, "\n")

	if !ok {
		e = &markdownEntry{}
		c.entries[key] = e
	}
	e.lastInput = text
	e.lastOutput = out
	e.lastRenderedAt = now
	return out
}

// forget drops the cache entry for the given key. Called when the
// message reaches its terminal state and we no longer need to
// throttle anything for it. Optional — entries left in the cache
// just consume a bit of memory.
func (c *markdownCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// prepareForGlamour applies the "half-formed Markdown" guard. When
// the input ends inside a code fence (an odd count of ``` lines),
// we strip everything from the unmatched opening fence onward and
// leave it as raw plain text appended at the end. Glamour
// processes the closed portion and we concatenate the dangling
// raw segment back so the user sees what the LLM is typing.
//
// The check is intentionally simple: count fences at line starts.
// Backticks inline (`like this`) don't count, so they don't
// interfere with the heuristic. Indented code fences inside
// blockquotes are the edge case we don't handle perfectly — but
// they're rare in LLM output and worst case we render the half
// of the block that's complete and the rest as plain.
func prepareForGlamour(text string) string {
	lines := strings.Split(text, "\n")
	openFenceLine := -1
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " \t")
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		// Either opens or closes.
		if openFenceLine == -1 {
			openFenceLine = i
		} else {
			openFenceLine = -1 // closed
		}
	}
	if openFenceLine == -1 {
		return text
	}
	// Everything up to (but not including) the open fence line is
	// "closed Markdown" — let Glamour render it. Everything from
	// the fence onward we'll show as-is below.
	closed := strings.Join(lines[:openFenceLine], "\n")
	dangling := strings.Join(lines[openFenceLine:], "\n")
	if closed == "" {
		// Nothing closed yet — fall back to plain text entirely
		// so we don't ask Glamour to render an empty body.
		return text
	}
	// Reattach the dangling chunk preceded by a blank line so the
	// renderer ends its closed section cleanly. The dangling text
	// is rendered as a fenced block by Glamour anyway — except
	// it's unclosed, so we explicitly close it before handing it
	// over. This produces a stable visual: closed body styled,
	// dangling body as a code block in progress.
	return closed + "\n\n" + dangling + "\n```"
}

// buildMarkdownRenderer constructs a Glamour TermRenderer themed in
// the Canarias palette. We start from the built-in dark style (so we
// inherit sane defaults and a full Chroma syntax theme for code
// blocks) and repaint the colours that carry the chat's personality:
// sun-orange headings, golden-clay links, lime-wash body text, and
// picón-toned code surfaces. The Document margin is zeroed so the
// agent's text shares the user's left column instead of looking like
// a quote.
//
// width is the wrap budget Glamour should respect. Passing 0 disables
// wrap; we always pass a real width because the chat viewport has its
// own bounds.
func buildMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	style := canariasMarkdownStyle()
	return glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
}

// Canarias palette hex values used by the Markdown theme. Kept as
// local string constants because Glamour's StyleConfig wants *string
// hex, not lipgloss colours.
const (
	mdSun       = "#F2922B" // headings, bullets, link text
	mdSunLight  = "#F6AC56" // h2/h3 step-down
	mdClayGold  = "#C98A4B" // links
	mdLimeWash  = "#E8DDCB" // body text, strong
	mdLimeFaint = "#A89B86" // block quotes, image captions
	mdClay      = "#8A4B2E" // quote bar, rules
	mdPiconUp   = "#473C34" // inline-code background
	mdPiconAlt  = "#2B2520" // code-block background
	mdLavaRed   = "#C2412B" // inline-code text accent
)

// canariasMarkdownStyle returns the dark base StyleConfig with the
// Canarias colours layered on top. We mutate a copy of
// glamour.DarkStyleConfig rather than building one from scratch so
// any field we don't explicitly theme keeps a reasonable default.
func canariasMarkdownStyle() ansi.StyleConfig {
	s := glamour.DarkStyleConfig

	sptr := func(v string) *string { return &v }
	bptr := func(v bool) *bool { return &v }
	zero := uint(0)

	// Document: flush left, lime-wash body.
	s.Document.Margin = &zero
	s.Document.Color = sptr(mdLimeWash)

	// Headings: sun orange, bold, no garish background box on H1.
	// A small "▰ " prefix on H1 gives it identity without shouting.
	s.Heading.Color = sptr(mdSun)
	s.Heading.Bold = bptr(true)
	s.H1.Prefix = "▰ "
	s.H1.Suffix = ""
	s.H1.Color = sptr(mdSun)
	s.H1.BackgroundColor = nil // drop the violet block from the dark theme
	s.H1.Bold = bptr(true)
	s.H2.Prefix = ">> "
	s.H2.Color = sptr(mdSun)
	s.H3.Prefix = "> "
	s.H3.Color = sptr(mdSunLight)
	s.H4.Color = sptr(mdSunLight)
	s.H5.Color = sptr(mdSunLight)
	s.H6.Color = sptr(mdLimeFaint)

	// Emphasis: lime-wash. Strong reads a touch brighter via bold.
	s.Emph.Color = sptr(mdLimeWash)
	s.Emph.Italic = bptr(true)
	s.Strong.Color = sptr(mdLimeWash)
	s.Strong.Bold = bptr(true)

	// Links: golden clay, underlined; the visible text in sun.
	s.Link.Color = sptr(mdClayGold)
	s.Link.Underline = bptr(true)
	s.LinkText.Color = sptr(mdSun)
	s.LinkText.Bold = bptr(true)

	// Lists: sun bullets / enumerators.
	s.Item.BlockPrefix = "• "
	s.Item.Color = sptr(mdSun)
	s.Enumeration.Color = sptr(mdSun)

	// Block quotes: a clay side-bar + faded, italic lime-wash text.
	s.BlockQuote.Color = sptr(mdLimeFaint)
	s.BlockQuote.Italic = bptr(true)
	s.BlockQuote.IndentToken = sptr("│ ")

	// Horizontal rule: a fine clay line instead of the grey dashes.
	s.HorizontalRule.Color = sptr(mdClay)
	s.HorizontalRule.Format = "\n────────\n"

	// Inline code: lime-wash on lightened picón, with a faint lava
	// tint so it stands out against body text without a loud box.
	s.Code.Color = sptr(mdLavaRed)
	s.Code.BackgroundColor = sptr(mdPiconUp)

	// Code blocks: keep the inherited Chroma syntax theme (it's tuned
	// and readable) but sit it on a picón panel so it belongs to the
	// Canarias world rather than floating on neutral grey.
	s.CodeBlock.Margin = &zero
	if s.CodeBlock.Chroma != nil {
		s.CodeBlock.Chroma.Background.BackgroundColor = sptr(mdPiconAlt)
		s.CodeBlock.Chroma.Text.Color = sptr(mdLimeWash)
	}

	return s
}
