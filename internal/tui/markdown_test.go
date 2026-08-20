// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
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

// problematicMarkdown is distilled from the real transcript that made the
// chat "go crazy" ( Downloads/DESCUAJE.txt ): a long block quote, inline
// LaTeX-ish $...$ with braces and carets, a $$...$$ block, accented words,
// ¿, ×, % and headings. Every construct here was observed to make glamour
// v0.7.0 emit lines wider than the wrap budget.
const problematicMarkdown = `## Las tres ideas de la frase

**1. "Cinco puntos de carga normalizados: 10 %, 25 %, 50 %, 75 %, 100 % de $P_{aco}$"**

Esto es solo un truco algebraico para no escribir paréntesis anidados. La ecuación de Sandia solo contiene $(P_{dc} - P_{so})^2$, nunca $P_{dc}$ suelta. Así que llamas $x = P_{dc} - P_{so}$, resuelves la cuadrática en $x$ (con la fórmula de toda la vida), y al final recuperas $P_{dc} = x + P_{so}$.

$$\eta_i = \frac{P_{ac,i}}{P_{dc,i}} \cdot 100$$

Ejemplo: 500 / 523,26 × 100 = 95,55 % (justo el primer número de la Tabla 1).

> Fijas cinco salidas AC (las cargas), resuelves la cuadrática de Sandia para saber qué entrada DC produce cada una, y divides salida entre entrada para obtener cinco eficiencias. Esos cinco puntos son los que luego se ajustan para obtener $k_0, k_1, k_2$. ¿Vale?`

// TestMarkdownRenderNeverExceedsWidth is the regression guard for the
// "interface goes crazy" report: no line of the rendered output may exceed
// the wrap budget, because every overflowing line wraps onto an extra
// terminal row that renderMessages' rowSpans never counted, desyncing
// selection and scroll. Exercises the cache end-to-end (render()), so the
// clamp inside it is what makes this pass.
func TestMarkdownRenderNeverExceedsWidth(t *testing.T) {
	c := newMarkdownCache()
	for _, width := range []int{80, 100, 120, 157} {
		out := c.render("k", problematicMarkdown, width, true)
		for i, line := range strings.Split(out, "\n") {
			if w := xansi.StringWidth(line); w > width {
				t.Errorf("width=%d line %d is %d printable cells (> %d): %q",
					width, i, w, width, xansi.Strip(line))
			}
		}
	}
}

// TestClampLineBreaksLongUnspacedToken covers the failure mode ansi.Wrap
// alone can't fix: a token with no spaces (a long LaTeX-ish blob). The
// safety net must hard-wrap it, and no character may be lost in doing so.
func TestClampLineBreaksLongUnspacedToken(t *testing.T) {
	const width = 50
	in := "prefijo " + strings.Repeat("a_b", 30) + " sufijo final"
	lines := clampLine(in, width)
	if len(lines) < 2 {
		t.Fatalf("expected the 90-cell token to be split, got %d line(s)", len(lines))
	}
	var joined string
	for i, l := range lines {
		if w := xansi.StringWidth(l); w > width {
			t.Errorf("line %d is %d cells (> %d): %q", i, w, width, l)
		}
		joined += xansi.Strip(l)
	}
	// Hard-wrap preserves every printable character (no truncation).
	if strings.ReplaceAll(in, " ", "") != strings.ReplaceAll(joined, " ", "") {
		t.Errorf("characters lost in clamp:\n  in:  %q\n  out: %q", in, joined)
	}
}

// TestClampLinePreservesANSIAndContent clamps a styled line wider than
// the budget and asserts the re-wrapped pieces stay within budget and keep
// every printable character (the clamp re-wraps; it must never truncate).
func TestClampLinePreservesANSIAndContent(t *testing.T) {
	const width = 40
	// Red word run longer than the budget, glamour-style: SGR..reset chunks.
	in := "\x1b[31m" + strings.Repeat("rojo ", 20) + "\x1b[0m"
	lines := clampLine(in, width)
	if len(lines) < 2 {
		t.Fatalf("expected the over-long line to be split, got %d line(s)", len(lines))
	}
	var joined string
	for i, l := range lines {
		if w := xansi.StringWidth(l); w > width {
			t.Errorf("line %d is %d cells (> %d): %q", i, w, width, l)
		}
		joined += xansi.Strip(l)
	}
	if strings.ReplaceAll(strings.TrimSpace(xansi.Strip(in)), " ", "") !=
		strings.ReplaceAll(joined, " ", "") {
		t.Errorf("printable content changed by clamp:\n  in:  %q\n  out: %q", xansi.Strip(in), joined)
	}
}

// TestClampLineLeavesShortLinesAlone pins the cheap path: a line already
// within budget comes back untouched and unsplit.
func TestClampLineLeavesShortLinesAlone(t *testing.T) {
	in := "\x1b[32mcorta y con estilo\x1b[0m"
	got := clampLine(in, 80)
	if len(got) != 1 || got[0] != in {
		t.Fatalf("short line was modified: %q", got)
	}
}
