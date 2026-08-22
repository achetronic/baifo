// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"runtime"
	"testing"
)

func TestStripVariationSelectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii untouched", "hello world", "hello world"},
		{"unicode without selector untouched", "✅ done", "✅ done"},
		{"emoji presentation selector removed", "✅\uFE0F done", "✅ done"},
		{"text presentation selector removed", "▶\uFE0E play", "▶ play"},
		{"multiple selectors", "\uFE0F✅\uFE0F\uFE0E", "✅"},
		{"zwj sequence kept intact", "👨‍👩‍👧 family", "👨‍👩‍👧 family"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := StripVariationSelectors(tt.in); got != tt.want {
				t.Errorf("StripVariationSelectors(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDetectBoxDrawing(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	tests := []struct {
		name     string
		windows  bool // only meaningful when GOOS=windows; see note below
		vars     map[string]string
		wantUnix bool // expected result on POSIX
		wantWin  bool // expected result on Windows
	}{
		{"no vars", false, nil, true, false},
		{"windows terminal", true, map[string]string{"WT_SESSION": "abc"}, true, true},
		{"conemu ansi on", true, map[string]string{"ConEmuANSI": "ON"}, true, true},
		{"conemu ansi off", true, map[string]string{"ConEmuANSI": "OFF"}, true, false},
		{"ansicon present", true, map[string]string{"ANSICON": "1"}, true, true},
		{"git bash term", true, map[string]string{"TERM": "xterm-256color"}, true, true},
		{"dumb term", true, map[string]string{"TERM": "dumb"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectBoxDrawing(env(tt.vars))
			// The function branches on runtime.GOOS internally; on this
			// CI (linux) only the POSIX path executes. We still assert
			// the Windows expectations via the table so a Windows CI
			// runner (or a developer machine) validates the other half.
			want := tt.wantUnix
			if runtime.GOOS == "windows" {
				want = tt.wantWin
			}
			if got != want {
				t.Errorf("detectBoxDrawing(%v) = %v, want %v", tt.vars, got, want)
			}
		})
	}
}
