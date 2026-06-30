// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// MessageKind classifies a chat row so the renderer knows which colour
// to use for the author label.
type MessageKind int

const (
	// MessageUser is what the user typed in the composer.
	MessageUser MessageKind = iota

	// MessageRoot is the root agent's reply text.
	MessageRoot

	// MessageSystem is an italic, dim notice (e.g. "session resumed").
	MessageSystem

	// MessageError surfaces a failure from the runner or a tool.
	MessageError

	// MessageNotice is a prominent, highlighted notice rendered with a
	// filled colour header band (e.g. "context guard"). Unlike
	// MessageSystem (a quiet dim line) it is meant to catch the eye in
	// the flow of the transcript. Its body (Text) is treated as an
	// expandable detail block — collapsed to just the band by default,
	// unfolded under it when Expanded is true.
	MessageNotice

	// MessageAgentError surfaces a failed agent turn (the executor
	// reported a failure as a task-failed event rather than an
	// iterator error). Rendered with the same special-row treatment as
	// MessageNotice but in the error colour, so the two read as a
	// consistent family of "special" rows while staying visually
	// distinct by colour.
	MessageAgentError

	// MessageToolCall is the agent invoking a tool. Rendered as a
	// single dim line summarising the call (name + truncated args).
	MessageToolCall

	// MessageToolResult is the result of a previous MessageToolCall.
	// Rendered as a single dim line summarising the response.
	MessageToolResult
)

// Message is one item in the chat history. The TUI keeps an in-memory
// slice and renders the full transcript into a viewport on every
// change. Persistence lives in Phase 4 (sessions); for the in-memory
// history we re-render the whole slice every Update, which is fine
// because the slice stays small for typical conversations and the
// viewport handles wrapping + scrolling.
type Message struct {
	Kind MessageKind
	Time time.Time
	Text string

	// ToolName is set on MessageToolCall and MessageToolResult rows.
	// Carries the namespaced tool name (e.g. "filesystem.read_file").
	ToolName string

	// ToolCallID pairs a MessageToolResult with its preceding
	// MessageToolCall. Empty for other kinds.
	ToolCallID string

	// ToolArgs is the argument map of a MessageToolCall. Nil for
	// other kinds. Stored verbatim so a future expand-on-click view
	// can pretty-print it without re-parsing.
	ToolArgs map[string]any

	// ToolResult is the response map of a MessageToolResult. Nil for
	// other kinds.
	ToolResult map[string]any

	// Expanded controls whether a tool row renders only its single
	// dimmed header line (false, default) or also unfolds the
	// args+result block beneath it (true). Plain user / root / system
	// rows ignore this flag.
	//
	// Today nothing flips it — the wiring is in place so a future
	// "Enter on the focused row" interaction can toggle without
	// touching the renderer.
	Expanded bool

	// StreamID ties this row to a single in-flight streaming turn.
	// Only set on the MessageRoot bubble that an active stream is
	// writing into; empty on every other row. Coalescing finds the
	// bubble by this ID instead of by array position, so anything
	// inserted after it (tool rows, lifecycle notices, errors) can
	// never displace it and split the reply.
	StreamID string
}

// coalesceStream appends a text delta to the streaming bubble
// identified by streamID, or starts a new bubble carrying that ID
// when none exists yet. It searches by ID (back to front, since the
// active bubble is usually near the tail) so any rows inserted after
// the bubble — tool calls, lifecycle notices, errors — cannot
// displace it. replace swaps the bubble's whole text (used by the
// final aggregated artifact) instead of appending.
//
// Returns the updated slice. A zero streamID is treated as "no active
// stream": the delta always starts a fresh, ID-less bubble (the
// caller is expected to pass a real ID while streaming).
func coalesceStream(msgs []Message, streamID, delta string, replace bool) []Message {
	if delta == "" {
		return msgs
	}
	if streamID != "" {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Kind == MessageRoot && msgs[i].StreamID == streamID {
				if replace {
					msgs[i].Text = delta
				} else {
					msgs[i].Text += delta
				}
				return msgs
			}
		}
	}
	return append(msgs, Message{
		Kind:     MessageRoot,
		Time:     time.Now(),
		Text:     delta,
		StreamID: streamID,
	})
}

func (m Message) CopyableText() string {
	if m.Kind == MessageToolCall {
		var b strings.Builder
		b.WriteString("Tool Call: " + m.ToolName + "\n")
		if len(m.ToolArgs) > 0 {
			b.WriteString("Arguments:\n")
			keys := make([]string, 0, len(m.ToolArgs))
			for k := range m.ToolArgs {
				keys = append(keys, k)
			}
			sortStrings(keys)
			for _, k := range keys {
				b.WriteString(fmt.Sprintf("  %s: %v\n", k, m.ToolArgs[k]))
			}
		}
		return strings.TrimSpace(b.String())
	}
	if m.Kind == MessageToolResult {
		var b strings.Builder
		b.WriteString("Tool Result: " + m.ToolName + "\n")
		if len(m.ToolResult) > 0 {
			b.WriteString("Result:\n")
			keys := make([]string, 0, len(m.ToolResult))
			for k := range m.ToolResult {
				keys = append(keys, k)
			}
			sortStrings(keys)
			for _, k := range keys {
				b.WriteString(fmt.Sprintf("  %s: %v\n", k, m.ToolResult[k]))
			}
		}
		return strings.TrimSpace(b.String())
	}
	return m.Text
}

// chatView is the scrolling chat component. It wraps bubbles/viewport
// and tracks a "stuck at bottom" flag so new events auto-scroll only
// when the user has not scrolled up manually.
//
// Behaviour mirrors Slack / Discord:
//
//   - On construction the viewport is at the bottom and stuckAtBottom
//     is true.
//   - When new content arrives, we re-render and, if stuck, GotoBottom.
//   - When the user scrolls and the viewport leaves the bottom, stuck
//     becomes false; we stop following new content.
//   - When the user scrolls back to the bottom (PgDn, End, or just by
//     reaching it with the wheel), stuck becomes true again.
//   - The config knob runtime.chat_auto_scroll only seeds the initial
//     stuck flag; the user always retains manual control.
type chatView struct {
	theme      Theme
	vp         viewport.Model
	stuck      bool
	autoScroll bool // mirror of runtime.chat_auto_scroll for seeding
	width      int
	height     int

	// selectedIndex is the index inside the messages slice that
	// should be marked as "focused" when the chat is in navigation
	// mode. -1 means "no selection" and the chat renders normally,
	// with no left-column marker on any row. The Model owns the
	// state machine that flips this; the chat view just paints.
	selectedIndex int

	// rowSpans is the list of (start_row, end_row) ranges, one
	// per message index, populated on every renderMessages pass.
	// Used by rowForMessage to map a click row to its owning
	// message. start_row is inclusive, end_row is exclusive; both
	// are in "content coordinates" (i.e. relative to the start of
	// the rendered string, not the viewport offset). Indices that
	// were consumed as the "result half" of a paired tool row
	// appear with start_row == end_row so they don't match clicks.
	rowSpans [][2]int

	// markdown is the per-chatView cache of rendered Markdown
	// outputs, keyed by a stable message identity (today: the
	// message's index inside m.messages). Throttled to avoid
	// re-running Glamour on every streamed chunk.
	markdown *markdownCache

	// streamingID is the StreamID of the message currently being
	// streamed, so the renderer can bypass the markdown throttle on
	// that row's final chunk. Empty means "no stream active".
	streamingID string

	// forceStreamRender, when true, makes the next renderMessages
	// pass bypass the markdown throttle for the streaming row. Set
	// by the trailing-edge flush so a paused stream still shows its
	// full text; cleared after the pass consumes it.
	forceStreamRender bool
}

// newChatView constructs a chatView. autoScroll seeds the initial
// stuckAtBottom flag (true by default; users who explicitly set
// runtime.chat_auto_scroll: false get a non-following chat from the
// start).
func newChatView(theme Theme, autoScroll bool) chatView {
	vp := viewport.New()
	return chatView{
		theme:         theme,
		vp:            vp,
		stuck:         autoScroll,
		autoScroll:    autoScroll,
		selectedIndex: -1,
		markdown:      newMarkdownCache(),
	}
}

// SetSize updates the viewport dimensions. Called whenever the model
// receives a tea.WindowSizeMsg or the layout zones change (composer
// shown / hidden, settings overlay open, ...).
//
// The geometry is:
//
//	c.width  = outer width of the chat panel (border + padding + content)
//	border   = 1 col left + 1 col right
//	padding  = 1 col left + 1 col right (lipgloss PanelBorder)
//	content  = c.width - 4
//
// The viewport receives the content width, so every line we hand
// it via SetContent is already sized to fit without the viewport
// having to wrap. contentWidth() is the canonical source for any
// caller that needs to lay out a row.
func (c *chatView) SetSize(width, height int) {
	c.width = width
	c.height = height
	inner := c.contentWidth()
	if inner < 1 {
		inner = 1
	}
	innerH := height - 2
	if innerH < 1 {
		innerH = 1
	}
	c.vp.SetWidth(inner)
	c.vp.SetHeight(innerH)
}

// contentWidth is the number of columns available for actual chat
// content, AFTER subtracting the panel border (2) and the panel
// padding (2). Every renderer in this file uses it so the math
// stays in one place. Returns 0 when the chat has not been sized
// yet (newChatView default), so callers can early-out.
func (c *chatView) contentWidth() int {
	w := c.width - 4
	if w < 0 {
		return 0
	}
	return w
}

// SetSelected updates which message index the chat paints as the
// focused row. Pass -1 to clear the marker; pass any index in
// range to highlight that row with the accent marker. Callers must
// follow up with SetMessages to actually repaint.
func (c *chatView) SetSelected(index int) {
	c.selectedIndex = index
}

// SetMessages re-renders the message slice into the viewport. When
// stuck at the bottom, the viewport snaps to the latest content;
// otherwise we preserve the user's scroll position by relying on
// SetContent's default behaviour (it keeps offsets when possible).
func (c *chatView) SetMessages(messages []Message) {
	body := c.renderMessages(messages)
	wasStuck := c.stuck
	c.vp.SetContent(body)
	if wasStuck {
		c.vp.GotoBottom()
	}
}

// GotoTop scrolls the viewport to its first line. Thin wrapper
// kept here so chat_focus.go can drive the chat without poking at
// the unexported viewport field directly.
func (c *chatView) GotoTop() {
	c.vp.GotoTop()
	c.stuck = c.vp.AtBottom()
}

// GotoBottom scrolls the viewport to its last line and flips
// stuck-mode on so subsequent content additions auto-follow.
func (c *chatView) GotoBottom() {
	c.vp.GotoBottom()
	c.stuck = true
}

// EnsureSelectedVisible scrolls the viewport so the currently
// selected message is fully on screen. No-op when the selection
// is invalid, when the message has no rendered span (folded tool
// result), or when the span already fits inside the visible
// window. Called from the keyboard navigation handler — mouse
// wheel doesn't need it because it manipulates the viewport
// directly.
//
// The logic mirrors what viewport.EnsureVisible does for a single
// line, extended to a row range: if the span's top is above the
// window, align the top of the span to the window's top; if the
// span's bottom is below, align the bottom of the span to the
// window's bottom. When the span is taller than the window we
// prefer showing its top (so the user sees the message header
// and can read down with the mouse / further keypresses).
func (c *chatView) EnsureSelectedVisible() {
	if c.selectedIndex < 0 || c.selectedIndex >= len(c.rowSpans) {
		return
	}
	span := c.rowSpans[c.selectedIndex]
	if span[0] == 0 && span[1] == 0 {
		// Hidden (folded into a paired tool call).
		return
	}
	top, bottom := span[0], span[1]
	height := c.vp.Height()
	if height <= 0 {
		return
	}
	yoff := c.vp.YOffset()
	winBottom := yoff + height - 1

	switch {
	case top < yoff:
		// Selection extends above the visible window — pull the
		// window up so the top of the message sits at the top.
		c.vp.SetYOffset(top)
	case bottom > winBottom:
		// Selection extends below — push the window down so the
		// bottom of the message sits at the bottom (or, when
		// the span is taller than the window, the span's top
		// sits at the top so we don't end up showing only the
		// last line).
		spanHeight := bottom - top + 1
		if spanHeight >= height {
			c.vp.SetYOffset(top)
		} else {
			c.vp.SetYOffset(bottom - height + 1)
		}
	}
	// Any explicit scroll movement unsticks the auto-bottom mode.
	// Re-derive from the final state so end-of-list selections
	// stay stuck and earlier ones don't.
	c.stuck = c.vp.AtBottom()
}

// Update forwards a tea.Msg to the viewport so PgUp/PgDn/mouse work,
// then recomputes the stuck flag from the new scroll position. We
// expose this as a method so the top-level Model can route key
// messages to the chat when the Chat tab is active.
func (c *chatView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.vp, cmd = c.vp.Update(msg)
	// AtBottom reflects the current viewport state; re-derive stuck
	// from it so manual scroll-up unsticks and manual scroll-down
	// re-sticks.
	c.stuck = c.vp.AtBottom()
	return cmd
}

// View renders the chat panel with its border. The border colour
// follows the theme's focused vs unfocused styles; the chat is the
// only focusable area on the Chat tab today, so we always paint it
// focused.
func (c *chatView) View() string {
	border := c.theme.PanelBorder()
	// lipgloss v2 Style.Width(N) makes the whole block N columns wide
	// (border + padding + content). c.width is the panel's intended
	// outer footprint (screen width minus one margin per side, set in
	// resizeChat), so we hand it straight to Width(). The viewport
	// inside was sized to contentWidth() == c.width-4 to fit within the
	// 2 border + 2 padding columns. MarginLeft matches the composer and
	// status bar so all three left/right edges line up.
	panel := border.Width(c.width).Height(c.height).Render(c.vp.View())
	return lipgloss.NewStyle().MarginLeft(panelSideMargin).Render(panel)
}

// AtBottom reports whether the viewport currently shows the latest
// content. Exposed for the status bar's future "new messages below"
// hint.
func (c *chatView) AtBottom() bool {
	return c.vp.AtBottom()
}

// renderMessages serialises the message slice into a single styled
// string suitable for SetContent.
//
// Tool calls and their matching results are PAIRED by ToolCallID
// and rendered as a single bordered card per pair. The pairing is
// done in this rendering step (not in the message slice itself) so
// the underlying Message log stays a flat append-only history —
// which is what the audit + session flow expects.
//
// A MessageToolCall row is rendered as:
//   - if a matching MessageToolResult appears later in the slice,
//     the result is folded into the same card and we skip the
//     result's own row when we get to it.
//   - otherwise the card is rendered as "in-flight" (info border,
//     no footer) and an entry stays in the pending map so future
//     SetMessages calls can paint the same card without flicker.
//
// An orphan MessageToolResult (no preceding call seen) is rendered
// as a small card on its own, mostly for robustness — in practice
// the agent always emits the call event first.
func (c *chatView) renderMessages(messages []Message) string {
	// Reset row-span bookkeeping. Every entry starts as a no-match
	// (start == end == 0) and gets filled in when we actually
	// render its piece below. Consumed result halves stay as
	// no-match by design.
	c.rowSpans = make([][2]int, len(messages))

	if len(messages) == 0 {
		return c.theme.FaintText().Render(
			"(no messages yet — type below to start)",
		)
	}

	// Pre-index results by CallID for one pass through the slice.
	// Empty CallIDs are skipped: orphan results without an ID
	// cannot be paired anyway and fall through to the orphan
	// branch below.
	resultByID := make(map[string]int, len(messages))
	for i, m := range messages {
		if m.Kind != MessageToolResult || m.ToolCallID == "" {
			continue
		}
		resultByID[m.ToolCallID] = i
	}

	// Skip set: indexes already consumed as the "result half" of a
	// pair. Avoids re-rendering them as standalone rows.
	consumed := make(map[int]struct{}, len(resultByID))

	var b strings.Builder
	wroteOne := false
	currentRow := 0
	for i, m := range messages {
		if _, skip := consumed[i]; skip {
			continue
		}
		if wroteOne {
			b.WriteString("\n\n")
			// One blank row between messages. The first '\n'
			// closes the previous message's last line; the
			// second '\n' is the blank row itself. So the
			// next message starts one row lower, not two.
			currentRow++
		}
		wroteOne = true

		startRow := currentRow

		var piece string
		if m.Kind == MessageToolCall {
			var resultPtr *Message
			if m.ToolCallID != "" {
				if ri, ok := resultByID[m.ToolCallID]; ok {
					r := messages[ri]
					resultPtr = &r
					consumed[ri] = struct{}{}
				}
			}
			piece = c.renderToolRow(m, resultPtr, i == c.selectedIndex)
		} else {
			// streaming is true only for the active in-flight
			// root message; when set, the markdown cache treats
			// it as a force re-render at the final tick. The
			// trailing-edge flush clears the throttle for one
			// pass via forceStreamRender so a paused stream still
			// shows its full text.
			streaming := false
			if c.streamingID != "" && m.StreamID == c.streamingID {
				streaming = !c.forceStreamRender
			}
			piece = c.renderMessage(m, i, i == c.selectedIndex, streaming)
		}
		b.WriteString(piece)
		currentRow += strings.Count(piece, "\n") + 1

		c.rowSpans[i] = [2]int{startRow, currentRow}
	}
	// forceStreamRender is a one-shot: consume it so subsequent
	// passes go back to the normal throttled streaming render.
	c.forceStreamRender = false
	return b.String()
}

// rowForMessage maps a row offset inside the rendered chat content
// back to the index of the message that owns that row. Returns
// (-1, false) when the row falls into the blank space between
// messages or outside the rendered range. Cheap O(n) scan over
// rowSpans; we don't render hundreds of messages, so a binary
// search isn't worth the cognitive overhead.
func (c *chatView) rowForMessage(messages []Message, row int) (int, bool) {
	_ = messages // kept for symmetry with the caller; the spans live on c
	if row < 0 {
		return -1, false
	}
	for i, span := range c.rowSpans {
		if span[0] == span[1] {
			continue // consumed / not rendered as its own block
		}
		if row >= span[0] && row < span[1] {
			return i, true
		}
	}
	return -1, false
}

func (c *chatView) renderMessage(m Message, idx int, selected, streaming bool) string {
	ts := m.Time.Format("15:04")
	var labelStyle lipgloss.Style
	var label string
	useMarkdown := false
	switch m.Kind {
	case MessageUser:
		label = "you · " + ts
		labelStyle = c.theme.DimText()
	case MessageRoot:
		label = "root · " + ts
		labelStyle = c.theme.EntityText("root")
		useMarkdown = true
	case MessageSystem:
		label = "· " + ts
		labelStyle = c.theme.FaintText().Italic(true)
	case MessageError:
		label = "error · " + ts
		labelStyle = c.theme.StatusError().Bold(true)
	case MessageNotice:
		return c.renderSpecialRow(m, idx, selected)
	case MessageAgentError:
		return c.renderSpecialRow(m, idx, selected)
	case MessageToolCall:
		return c.renderToolRow(m, nil, selected)
	case MessageToolResult:
		shell := Message{
			Kind:       MessageToolCall,
			Time:       m.Time,
			ToolName:   m.ToolName,
			ToolCallID: m.ToolCallID,
		}
		return c.renderToolRow(shell, &m, selected)
	default:
		label = ts
		labelStyle = c.theme.DimText()
	}
	header := labelStyle.Render(label)
	var body string
	if useMarkdown {
		body = c.renderMarkdownBody(m.Text, idx, !streaming)
	} else {
		body = c.renderBody(m.Text)
	}
	raw := header + "\n" + body
	return c.applyGutter(raw, selected)
}

// renderSpecialRow paints a "special" chat row (MessageNotice or
// MessageAgentError). Both kinds use the EXACT same structure so they
// read as one consistent family of prominent in-flow events, differing
// only by colour and labels:
//
//	GLYPH  label · HH:MM            ← filled colour header band
//	one-line preview of the body…   ← body, collapsed to a single line
//	> Enter to expand · summary     ← explicit expand affordance (footer)
//
// When the row is Expanded the body shows in full and the footer flips
// to "Enter to collapse". A row with no body shows just the band. The
// colours and labels:
//
//   - MessageNotice    → accent band, "context guard", body is the
//     compaction summary the model produced.
//   - MessageAgentError → error band, "agent error", body is the
//     failure message.
//
// The whole thing is width-bounded to the chat content area minus the
// selection gutter so it lines up with ordinary message rows.
func (c *chatView) renderSpecialRow(m Message, idx int, selected bool) string {
	ts := m.Time.Format("15:04")
	width := c.contentWidth() - lipgloss.Width(chatSelectionMarker)
	if width < 8 {
		width = 8
	}

	var (
		glyph      string
		label      string
		band       color.Color
		expandHint string
	)
	switch m.Kind {
	case MessageAgentError:
		glyph = c.theme.Glyph("warn")
		label = "agent error"
		band = colorError
		expandHint = "full error"
	default: // MessageNotice (context guard)
		glyph = c.theme.Glyph("compact")
		label = "context guard"
		band = c.theme.Accent.Primary
		expandHint = "summary"
	}

	// Header band: glyph + label + time on a filled colour bar.
	header := c.theme.SpecialHeaderBand(band).Render(" " + glyph + "  " + label + " · " + ts + " ")
	lines := []string{header}

	if m.Text != "" {
		if m.Expanded {
			// Expanded: render the body inside the same rounded box the
			// tool-execution rows use, so the two read as one family.
			// The body goes through the Markdown renderer; plain text
			// passes through unchanged, so this is safe whether the
			// summary is Markdown or plain prose.
			lines = append(lines, c.specialExpandedBox(m.Text, idx))
			lines = append(lines, c.theme.FaintText().Render(c.theme.Glyph("expanded")+" Enter to collapse"))
		} else {
			// Collapsed: no preview — just the band and an explicit
			// affordance telling the user there is more behind Enter.
			lines = append(lines, c.theme.FaintText().Render(c.theme.Glyph("chevron")+" Enter to expand · "+expandHint))
		}
	}

	return c.applyGutter(strings.Join(lines, "\n"), selected)
}

// specialExpandedBox wraps the given body in the same rounded,
// focus-background box the expanded tool rows use. The body is
// rendered as plain wrapped text with the focus background applied to
// every line (padded to the inner width) so the brown box colour is
// continuous — we deliberately do NOT route it through the Markdown
// renderer, whose own ANSI resets would punch black holes in the
// background. Plain prose and Markdown source both read fine this way;
// the priority here is a clean, fully-filled box. idx is unused now
// but kept for symmetry with the other body renderers.
func (c *chatView) specialExpandedBox(body string, idx int) string {
	_ = idx

	// Inner text width: total content width, minus the selection
	// gutter, the box's left margin (1), borders (2) and horizontal
	// padding (2*2). Floor it so very narrow terminals still render.
	inner := c.contentWidth() - lipgloss.Width(chatSelectionMarker) - 1 - 2 - 4
	if inner < 8 {
		inner = 8
	}

	// Wrap the plain text to the inner width, then paint each line
	// with the focus background padded to that width so the fill is
	// edge-to-edge with no black gaps.
	wrapped := lipgloss.NewStyle().Width(inner).Render(collapseWhitespace(body))
	bgLine := c.theme.PrimaryText().Background(colorBGFocus)
	var painted []string
	for _, ln := range strings.Split(wrapped, "\n") {
		pad := inner - lipgloss.Width(ln)
		if pad < 0 {
			pad = 0
		}
		painted = append(painted, bgLine.Render(ln+strings.Repeat(" ", pad)))
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBGFocus).
		BorderBackground(colorBGFocus).
		Background(colorBGFocus).
		Padding(1, 2).
		MarginLeft(1).
		MarginTop(1).
		MarginBottom(1)
	return boxStyle.Render(strings.Join(painted, "\n"))
}

// collapseWhitespace trims trailing space on each line and drops a
// trailing blank run so the box has no dead rows at the bottom.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// renderMarkdownBody runs the body through the Markdown cache,
// keyed by the message index. force=true (the message is no longer
// streaming) bypasses the throttle so the final, polished render
// is what the user sees once the stream ends.
//
// We compute the wrap width as contentWidth - markdownWrapMargin
// so Glamour's word wrap matches the chat's gutter and the marker
// column has a stable home. Falls back to the plain renderer when
// the cache decides not to render (width too small, etc.).
func (c *chatView) renderMarkdownBody(text string, idx int, force bool) string {
	if c.markdown == nil {
		return c.renderBody(text)
	}
	width := c.contentWidth() - markdownWrapMargin()
	if width < 8 {
		return c.renderBody(text)
	}
	key := strconv.Itoa(idx)
	return c.markdown.render(key, text, width, force)
}

// chatSelectionMarker is the glyph painted in the left column of
// every line of the focused message. When the message is not
// selected, the gutter collapses to spaces of the same width so
// neighbouring rows stay aligned in the same column.
const chatSelectionMarker = "▍ "

// applyGutter prepends a left-column gutter to every line of the
// given block. When selected is true, every line gets the accent
// marker so the eye reads "the entire message is highlighted, not
// just its first row"; when selected is false, blank spaces of the
// same width keep the alignment uniform.
//
// The function operates on the already-styled string, so callers
// can compose any header/body/expanded-block sequence and let the
// gutter wrap the whole thing. Empty inputs return the gutter for
// a single line so a fully-empty selected message still shows.
func (c *chatView) applyGutter(s string, selected bool) string {
	gutter := strings.Repeat(" ", lipgloss.Width(chatSelectionMarker))
	if selected {
		gutter = c.theme.AccentText().Render(chatSelectionMarker)
	}
	if s == "" {
		return gutter
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = gutter + l
	}
	return strings.Join(lines, "\n")
}

// renderBody styles the plain-text body of a non-tool message and
// hard-wraps every line to the chat's content width MINUS the
// selection-marker gutter. Wrapping here (instead of letting the
// viewport reflow) keeps logical lines and visual lines in 1:1
// correspondence, which is what rowSpans and the click-to-row
// math rely on. Multi-line bodies wrap word-by-word; lines without
// spaces wrap mid-character.
func (c *chatView) renderBody(text string) string {
	if text == "" {
		return ""
	}
	width := c.contentWidth() - lipgloss.Width(chatSelectionMarker)
	if width < 1 {
		width = 1
	}
	style := c.theme.PrimaryText().Width(width)
	// lipgloss.NewStyle().Width(W).Render(s) word-wraps and pads
	// every line to W. The padding is what gives the gutter a
	// stable column to live in even on selected rows.
	return style.Render(text)
}

// truncateInlineValue renders any value as a short single-line
// string suitable for the tool-card body. Strings are quoted only
// when they contain whitespace so URLs and identifiers render
// naturally. The result is purely textual — styling is left to the
// caller (the body renders it raw; nil shows as the literal "null").
func truncateInlineValue(v any, maxLen int) string {
	if v == nil {
		return "null"
	}
	switch val := v.(type) {
	case string:
		return truncateInline(val, maxLen)
	case bool:
		if val {
			return "true"
		}
		return "false"
	}
	return truncateInline(stringifyAny(v), maxLen)
}

// truncateInline lives in model.go (shared with the older summary
// renderer). We rely on the same helper here so the two summary
// paths stay consistent.

// stringifyAny renders an arbitrary value compactly. We avoid
// fmt.Sprintf("%v") for maps because the iteration order varies
// between runs and we want stable test output.
func stringifyAny(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sortStrings(keys)
		var b strings.Builder
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(stringifyAny(val[k]))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(stringifyAny(item))
		}
		b.WriteByte(']')
		return b.String()
	}
	// Fallback for numbers, time, etc.
	return sprintAny(v)
}

// sortStrings is a tiny wrapper so callers don't import "sort"
// inline. Equivalent to sort.Strings; the slice is mutated in
// place. Used by the tool-row body renderer for stable key order
// and by stringifyAny for stable map serialisation.
func sortStrings(s []string) {
	sortStringsInPlace(s)
}

// sortStringsInPlace is a manual insertion sort so the file does
// not depend on the "sort" package. For the tiny slices we sort
// (tool arg keys, result map keys — almost always <10) insertion
// sort is faster than the comparator dispatch of sort.Slice.
func sortStringsInPlace(s []string) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1] > s[j] {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

// sprintAny is the fallback for stringifyAny — numbers, time,
// structs and anything else that doesn't have a specific branch.
// We delegate to fmt.Sprint to stay close to Go's default Stringer
// behaviour without reimplementing the formatter for every type.
func sprintAny(v any) string {
	return fmt.Sprint(v)
}

// ─── Tool row renderer ───────────────────────────────────────────────
//
// Tools are rendered as a single dimmed line by default. The user's
// own messages and the root's replies are the headline of the chat —
// every chunk produced by tool plumbing must stay below them in the
// visual hierarchy.
//
// Collapsed (default), one line:
//
//   ▸ filesystem.read_file                                    running
//   ✓ filesystem.read_file                                      12:34
//   ✗ exec                                              exit 1 · 12:34
//
// Expanded (Message.Expanded == true), the header plus an indented
// args/result block sealed off by a faint vertical rail:
//
//   ✓ filesystem.read_file                                     12:34
//     ╎ args
//     ╎   path  /home/albyhernandez/.../internal/app/app.go
//     ╎
//     ╎ result
//     ╎   content  // Copyright 2026 The baifo Authors.
//     ╎           package app
//     ╎           …
//     ╎           + 86 more lines
//
// Every glyph and every text fragment goes through theme styles so
// nothing competes with the primary text colour reserved for the
// user/root rows.

// toolCardStateKind classifies the visual state of a tool row so
// the glyph, colour and right-hand summary stay coordinated. Kept
// as a tiny enum local to chat.go because the values only drive
// styling — there is no API surface to expose. The "card" name is
// historical; the renderer no longer draws cards.
type toolCardStateKind int

const (
	toolCardRunning toolCardStateKind = iota
	toolCardDone
	toolCardFailed
)

// We no longer use a vertical rail. Instead, the expanded body is enclosed in a colored box.

// toolRowMaxFieldKeyWidth caps how wide a key column gets in the
// expanded body. Keys longer than this stay full-length but stop
// participating in the alignment so a single pathological key name
// doesn't push every value off the screen.
const toolRowMaxFieldKeyWidth = 14

// toolRowExpandedValueMaxLen is the per-value truncation budget in
// the expanded view. Long strings are split into multiple lines
// indented under the key; the *total* number of lines per value is
// further capped by toolRowExpandedMaxValueLines below.
const toolRowExpandedValueMaxLen = 240

// toolRowExpandedMaxValueLines is the maximum number of body lines
// we emit per single value (file contents, exec stdout, ...). Beyond
// this we print a faint "+ N more lines" tail so the chat doesn't
// drown on a huge tool result. The expanded view is opened
// deliberately, so we are more generous here than in the collapsed
// summary: a pretty-printed JSON object needs room to breathe.
const toolRowExpandedMaxValueLines = 16

// renderToolRow renders one tool call (and its optional paired
// result) as a dimmed header line, plus the args+result block when
// Message.Expanded is true. The call message is the source of
// truth for the row identity; result, when non-nil, drives the
// state glyph and the right-hand summary.
//
// selected paints the accent selection marker as a continuous bar
// down the left side of every line of the row, including the
// expanded body. Non-selected rows render the same shape but with
// a blank gutter so the alignment stays uniform across the chat.
func (c *chatView) renderToolRow(call Message, result *Message, selected bool) string {
	state := c.toolRowState(result)
	header := c.toolRowHeader(call, result, state)
	raw := header
	if call.Expanded {
		// toolRowExpandedBody never returns empty: it always
		// emits at least a "(no args, no result)" line so the
		// toggle is visually obvious even on tools that carry
		// no payload.
		raw = header + "\n" + c.toolRowExpandedBody(call, result)
	}
	return c.applyGutter(raw, selected)
}

// toolRowState is the same classifier the old card renderer used
// (running / done / failed). Kept on chatView so we can swap glyphs
// and colours from the theme without threading state through.
func (c *chatView) toolRowState(result *Message) toolCardStateKind {
	if result == nil {
		return toolCardRunning
	}
	if result.ToolResult != nil {
		if v, ok := result.ToolResult["error"]; ok {
			if s, _ := v.(string); strings.TrimSpace(s) != "" {
				return toolCardFailed
			}
		}
	}
	return toolCardDone
}

// toolRowHeader builds the single-line header of a tool row. It is
// composed of three zones:
//
//	left  : glyph + tool name           (dim)
//	right : status / timestamp / error  (faint)
//	gap   : computed spaces to push right against the chat width
//
// Width comes from the chat viewport so the right zone lines up
// with the chat's right edge regardless of message text length.
// When the line would overflow (very long tool name, very narrow
// chat) we drop the right zone silently rather than wrap.
//
// The header does NOT add the selection-marker gutter: that's the
// caller's job via applyGutter, so every line of the tool row
// (header + expanded body) shares the same continuous left column.
// toolRowHeader builds the single-line header of a tool row.
//
// The header has two visual zones — the left (glyph + tool name)
// and the right (status / timestamp / error) — separated by a gap
// of spaces that pushes the right zone to the chat's right edge.
// The line ALWAYS fits in contentWidth() - selection-marker width
// columns: when it wouldn't, we shed information by priority:
//
//  1. name           — never dropped; truncated with … if needed.
//  2. right zone     — dropped first (timestamp/status/error).
//  3. timestamp half — dropped before the status/error half.
//
// This is what guarantees that every tool row occupies exactly one
// terminal row, which is what rowSpans (and click-row mapping)
// assume. Pre-wrap by the viewport would otherwise spill the
// header to a second row and break the click math.
//
// The header does NOT add the selection-marker gutter: applyGutter
// prepends it after we return so every line of the tool row
// (header + expanded body) shares the same continuous left column.
func (c *chatView) toolRowHeader(call Message, result *Message, state toolCardStateKind) string {
	glyph := c.toolRowStateGlyph(state)
	glyphStyled := c.toolRowGlyphStyle(state).Render(glyph)

	name := call.ToolName
	if name == "" {
		name = "(unnamed tool)"
	}

	// Budget for the line, AFTER subtracting the gutter the
	// caller will add. Anything below 1 means we have literally
	// no room and we render whatever we can fit.
	budget := c.contentWidth() - lipgloss.Width(chatSelectionMarker)
	if budget < 1 {
		budget = 1
	}

	// Left zone is "glyph + ' ' + name". Glyph + space takes
	// 2 cols; name takes the rest of its width.
	leftFixed := 2 // glyph + space
	rightFull, rightPrimary := c.toolRowRightParts(call, result, state)

	// Try the full layout first.
	if w := leftFixed + lipgloss.Width(name) + 1 + lipgloss.Width(rightFull); w <= budget && rightFull != "" {
		gap := budget - leftFixed - lipgloss.Width(name) - lipgloss.Width(rightFull)
		return glyphStyled + " " + c.theme.DimText().Render(name) +
			strings.Repeat(" ", gap) + rightFull
	}
	// Drop the secondary half of the right zone (the timestamp
	// in failed state). rightPrimary is the meaningful half:
	// "running" / "exit 1" / "12:34" for done.
	if w := leftFixed + lipgloss.Width(name) + 1 + lipgloss.Width(rightPrimary); w <= budget && rightPrimary != "" {
		gap := budget - leftFixed - lipgloss.Width(name) - lipgloss.Width(rightPrimary)
		return glyphStyled + " " + c.theme.DimText().Render(name) +
			strings.Repeat(" ", gap) + rightPrimary
	}
	// Drop the right zone entirely; pad the line to the full
	// width so applyGutter's gutter aligns with the next row.
	if w := leftFixed + lipgloss.Width(name); w <= budget {
		gap := budget - leftFixed - lipgloss.Width(name)
		return glyphStyled + " " + c.theme.DimText().Render(name) +
			strings.Repeat(" ", gap)
	}
	// Truncate the name itself.
	maxName := budget - leftFixed
	if maxName < 1 {
		maxName = 1
	}
	truncated := truncateInline(name, maxName)
	pad := budget - leftFixed - lipgloss.Width(truncated)
	if pad < 0 {
		pad = 0
	}
	return glyphStyled + " " + c.theme.DimText().Render(truncated) +
		strings.Repeat(" ", pad)
}

// toolRowRightParts returns two flavours of the right zone:
//
//	full     : the full string we'd LIKE to render
//	primary  : the minimum useful right zone (drops secondary
//	           info like the timestamp suffix on failures)
//
// The header uses them to gracefully degrade when the line is too
// narrow for the full layout.
func (c *chatView) toolRowRightParts(call Message, result *Message, state toolCardStateKind) (full, primary string) {
	switch state {
	case toolCardRunning:
		p := c.theme.FaintText().Render("running")
		return p, p
	case toolCardFailed:
		ts := call.Time.Format("15:04")
		reason := ""
		if result != nil {
			if s, ok := result.ToolResult["error"].(string); ok {
				reason = truncateInline(strings.TrimSpace(s), 32)
			}
		}
		if reason == "" {
			p := c.theme.FaintText().Render(ts)
			return p, p
		}
		full = c.theme.StatusError().Render(reason) +
			c.theme.FaintText().Render(" · "+ts)
		primary = c.theme.StatusError().Render(reason)
		return full, primary
	default:
		ts := c.theme.FaintText().Render(call.Time.Format("15:04"))
		return ts, ts
	}
}

// toolRowStateGlyph returns the single character that opens a tool
// row. ASCII fallbacks are intentional — these glyphs need to read
// at a glance, not be cute.
func (c *chatView) toolRowStateGlyph(state toolCardStateKind) string {
	switch state {
	case toolCardRunning:
		return "▸"
	case toolCardDone:
		return "✓"
	case toolCardFailed:
		return "✗"
	}
	return "·"
}

// toolRowGlyphStyle returns the lipgloss style for the leading
// glyph. Severity-coloured but NOT bold so the glyph guides the
// eye without competing with the user's text.
func (c *chatView) toolRowGlyphStyle(state toolCardStateKind) lipgloss.Style {
	switch state {
	case toolCardRunning:
		return c.theme.StatusInfo()
	case toolCardDone:
		return c.theme.StatusOK()
	case toolCardFailed:
		return c.theme.StatusError()
	}
	return c.theme.DimText()
}

// toolRowExpandedBody builds the indented args + result block
// rendered under the header when Message.Expanded is true. Returns
// the empty string when there's literally nothing to show (tool
// without args and without result).
//
// Layout:
//
//	╎ args
//	╎   key1  value1
//	╎   key2  value2
//	╎
//	╎ result
//	╎   key1  value1
//
// Strings that overflow the value column are split onto continuation
// lines that align under the value column (not under the key), with
// truncation governed by toolRowExpandedMaxValueLines.
func (c *chatView) toolRowExpandedBody(call Message, result *Message) string {
	hasArgs := len(call.ToolArgs) > 0
	hasResult := result != nil && len(result.ToolResult) > 0

	if !hasArgs && !hasResult {
		emptyText := c.withToolBG(c.theme.FaintText()).Render("(no args, no result)")
		boxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBGFocus).
			BorderBackground(colorBGFocus).
			Background(colorBGFocus).
			Padding(1, 2).
			MarginLeft(1).
			MarginTop(1).
			MarginBottom(1)
		return boxStyle.Render(emptyText)
	}

	state := c.toolRowState(result)

	var blocks []string
	if hasArgs {
		blocks = append(blocks, c.toolRowExpandedSection("call", call.ToolArgs, state))
	}
	if hasResult {
		blocks = append(blocks, c.toolRowExpandedSection("result", unwrapResultEnvelope(result.ToolResult), state))
	}
	content := strings.Join(blocks, "\n\n")

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBGFocus).
		BorderBackground(colorBGFocus).
		Background(colorBGFocus).
		Padding(1, 2).
		MarginLeft(1).
		MarginTop(1).
		MarginBottom(1)

	return boxStyle.Render(content)
}

// toolRowExpandedSection emits one labelled block ("call" /
// "result") plus its key=value entries. Keys are alphabetically
// sorted for stable output between identical events.
func (c *chatView) toolRowExpandedSection(label string, m map[string]any, state toolCardStateKind) string {
	if len(m) == 0 {
		return ""
	}

	var headerStyle lipgloss.Style
	var prefix string = "◆ "
	if label == "call" {
		headerStyle = c.theme.AccentText().Bold(true)
	} else if label == "result" {
		switch state {
		case toolCardFailed:
			headerStyle = c.theme.StatusError().Bold(true)
		case toolCardDone:
			headerStyle = c.theme.StatusOK().Bold(true)
		case toolCardRunning:
			headerStyle = c.theme.StatusWarning().Bold(true)
		default:
			headerStyle = c.theme.DimText().Bold(true)
		}
	} else {
		headerStyle = c.theme.DimText().Bold(true)
	}

	keys := make([]string, 0, len(m))
	maxKey := 0
	for k := range m {
		keys = append(keys, k)
		if w := lipgloss.Width(k); w > maxKey && w <= toolRowMaxFieldKeyWidth {
			maxKey = w
		}
	}
	sortStrings(keys)

	var fieldBlocks []string
	for _, k := range keys {
		fieldLines := c.toolRowExpandedField(k, m[k], maxKey)
		fieldBlocks = append(fieldBlocks, strings.Join(fieldLines, "\n"))
	}

	header := c.withToolBG(headerStyle).Render(prefix + strings.ToUpper(label))
	return header + "\n" + strings.Join(fieldBlocks, "\n\n")
}

// toolRowExpandedField emits the lines for a single key/value pair
// inside an expanded section. Scalar values render inline; strings
// that exceed the wrap width wrap onto continuation lines; maps/slices
// are stringified via stringifyAny and then wrapped the same way.
//
// keyCol is the padded width of the key column for visual
// alignment; keys that exceed toolRowMaxFieldKeyWidth break the
// alignment by design (we don't want one long key to push every
// other value off-screen).
func (c *chatView) toolRowExpandedField(key string, value any, keyCol int) []string {
	bgStyle := lipgloss.NewStyle().Background(colorBGFocus)
	dimText := c.theme.DimText().Background(colorBGFocus)
	faintText := c.theme.FaintText().Background(colorBGFocus)
	primaryText := c.theme.PrimaryText().Background(colorBGFocus)

	keyStyled := dimText.Bold(true).Render(key)

	// Available width inside the chat panel.
	avail := c.contentWidth() - lipgloss.Width(chatSelectionMarker)
	innerWidth := avail - 7 // 1 (margin) + 2 (border) + 4 (padding)
	if innerWidth < 10 {
		innerWidth = 10
	}
	wrapWidth := innerWidth - 4 // indent inside section
	if wrapWidth < 8 {
		wrapWidth = 8
	}

	valStr := stringifyExpanded(value, wrapWidth)
	originalValStr := valStr
	truncated := false
	if len(valStr) > 10000 {
		valStr = valStr[:10000]
		truncated = true
	}
	valLines := splitValueLines(valStr, wrapWidth)

	// Cap and add the "+ N more lines" tail when needed.
	more := 0
	if len(valLines) > toolRowExpandedMaxValueLines {
		more = len(valLines) - toolRowExpandedMaxValueLines
		valLines = valLines[:toolRowExpandedMaxValueLines]
	}
	if truncated {
		more += estimateWrappedLines(originalValStr[10000:], wrapWidth)
	}

	// Smart block layout: if value has multiple lines or is long, break lines beautifully
	isBlock := len(valLines) > 1 || truncated || lipgloss.Width(valStr) > 45

	out := make([]string, 0, len(valLines)+2)

	space2 := bgStyle.Render("  ")
	space4 := bgStyle.Render("    ")

	if isBlock {
		// Key on its own line
		out = append(out, space2+keyStyled+faintText.Render(":"))
		// Values beautifully indented below the key
		for _, l := range valLines {
			out = append(out, space4+primaryText.Render(l))
		}
		if more > 0 {
			out = append(out, space4+
				faintText.Render(fmt.Sprintf("+ %d more lines", more)))
		}
	} else {
		// Compact inline layout for short values
		pad := keyCol - lipgloss.Width(key)
		if pad < 0 {
			pad = 0
		}
		padding := bgStyle.Render(strings.Repeat(" ", pad))

		valStyled := primaryText.Render(valLines[0])
		out = append(out, space2+keyStyled+padding+faintText.Render(": ")+valStyled)
	}

	return out
}

// stringifyExpanded renders an arbitrary value for the expanded
// block. The goal is readability for *any* tool, with no per-tool
// knowledge:
//
//   - Scalars (string, bool, nil) render verbatim — most natural for
//     paths, URLs and prose.
//   - A string that is itself a JSON document is re-indented so tools
//     that hand back serialised JSON read as a tree, not one dense
//     line.
//   - Maps and slices render as indented JSON (two-space) so nesting
//     is visible instead of being collapsed onto a single line by
//     stringifyAny.
//
// maxLen is consumed by splitValueLines downstream as the wrap
// column; nothing is hard-truncated here so the user can still scroll
// the full content in the expanded view.
func stringifyExpanded(v any, maxLen int) string {
	_ = maxLen
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		if pretty, ok := prettyJSONString(val); ok {
			return pretty
		}
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	}
	if pretty, ok := prettyStructured(v); ok {
		return pretty
	}
	// Fallback for numbers, time and anything json can't represent.
	return stringifyAny(v)
}

// prettyStructured marshals maps and slices into indented JSON. It
// returns ("", false) for everything else (and on marshal failure) so
// the caller can fall back to the compact stringifyAny form.
func prettyStructured(v any) (string, bool) {
	switch v.(type) {
	case map[string]any, []any:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return "", false
}

// prettyJSONString detects a string whose content is itself a JSON
// object or array and re-indents it. Plain strings (paths, prose,
// single tokens) are left untouched — returns ("", false) — so they
// keep rendering naturally. The leading byte check avoids paying the
// Unmarshal cost on the common non-JSON case.
func prettyJSONString(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if len(t) < 2 || (t[0] != '{' && t[0] != '[') {
		return "", false
	}
	var v any
	if err := json.Unmarshal([]byte(t), &v); err != nil {
		return "", false
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", false
	}
	return string(b), true
}

// unwrapResultEnvelope peels the single-key wrapper that ADK's
// functiontool puts around a tool's return value. Most tools return a
// Go struct that ADK marshals into {"result": {...}}; rendering that
// outer "result" key adds a useless layer of nesting. When the map
// has exactly one key drawn from a small set of conventional wrapper
// names AND its value is itself a map, we promote the inner map so the
// user sees the actual fields directly. Anything else (multi-key
// results, scalar payloads) is returned unchanged.
func unwrapResultEnvelope(m map[string]any) map[string]any {
	if len(m) != 1 {
		return m
	}
	for k, v := range m {
		switch k {
		case "result", "output", "response", "data", "content":
			if inner, ok := v.(map[string]any); ok && len(inner) > 0 {
				return inner
			}
		}
	}
	return m
}

// splitValueLines turns a possibly-multiline string into a slice of
// physical lines. It splits on the literal newline first (so JSON
// pretty-printing and multi-line file content keep their shape), then
// soft-wraps any line that still exceeds the wrap column so a very
// long single line (a 4 KB blob, a long URL) renders as several rows.
//
// Soft wrapping prefers a word boundary (the last space inside the
// window) over a mid-word cut, and continuation lines inherit the
// leading whitespace of their source line so wrapped JSON values stay
// visually nested under their key.
func splitValueLines(s string, wrapWidth int) []string {
	if s == "" {
		return []string{""}
	}
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, wrapPlainLine(line, wrapWidth)...)
	}
	return out
}

// wrapPlainLine soft-wraps a single logical line to wrapWidth columns,
// breaking on the last space within the window when one exists and
// hard-cutting only when a single token is wider than the window.
// Continuation rows keep the source line's leading indentation so the
// wrapped fragments stay aligned under it.
func wrapPlainLine(line string, wrapWidth int) []string {
	if lipgloss.Width(line) <= wrapWidth {
		return []string{line}
	}

	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	// Never let the hanging indent eat the whole window.
	if lipgloss.Width(indent)+4 > wrapWidth {
		indent = ""
	}

	var out []string
	first := true
	for lipgloss.Width(line) > wrapWidth {
		prefix := ""
		if !first {
			prefix = indent
		}
		budget := wrapWidth - lipgloss.Width(prefix)
		if budget < 1 {
			budget = 1
		}
		cut := budget
		if cut > len(line) {
			cut = len(line)
		}
		// Prefer a word boundary, but only if it doesn't shove the
		// break unreasonably far back into the window.
		if brk := strings.LastIndexByte(line[:cut], ' '); brk > budget/2 {
			cut = brk
		}
		out = append(out, prefix+strings.TrimRight(line[:cut], " "))
		line = strings.TrimLeft(line[cut:], " ")
		first = false
	}
	if line != "" {
		out = append(out, indent+line)
	}
	return out
}

// estimateWrappedLines estimates the number of wrapped lines in a string
// without calling lipgloss.Width, making it extremely fast for very large strings.
func estimateWrappedLines(s string, wrapWidth int) int {
	if s == "" {
		return 0
	}
	if wrapWidth <= 0 {
		wrapWidth = 80
	}
	lines := strings.Split(s, "\n")
	count := 0
	for _, line := range lines {
		l := len(line)
		if l <= wrapWidth {
			count++
		} else {
			count += (l + wrapWidth - 1) / wrapWidth
		}
	}
	return count
}

func (c *chatView) withToolBG(style lipgloss.Style) lipgloss.Style {
	return style.Background(colorBGFocus)
}
