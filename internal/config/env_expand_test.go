// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package config

import "testing"

func TestExpandEnvPreservingSecrets(t *testing.T) {
	t.Setenv("BAIFO_TEST_VAR", "hello")
	t.Setenv("BAIFO_EMPTY", "")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "regular env var is expanded",
			in:   "value=${BAIFO_TEST_VAR}",
			want: "value=hello",
		},
		{
			name: "missing env var becomes empty",
			in:   "value=${BAIFO_UNSET_VAR}",
			want: "value=",
		},
		{
			name: "secret placeholder is preserved verbatim",
			in:   "api_key: ${secret:GEMINI_API_KEY}",
			want: "api_key: ${secret:GEMINI_API_KEY}",
		},
		{
			name: "mixed env and secret in the same line",
			in:   "${BAIFO_TEST_VAR} ${secret:TOKEN}",
			want: "hello ${secret:TOKEN}",
		},
		{
			name: "dollar-prefix (no braces) still works for env",
			in:   "$BAIFO_TEST_VAR",
			want: "hello",
		},
		{
			name: "no substitutions leaves the string alone",
			in:   "plain text",
			want: "plain text",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandEnvPreservingSecrets(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
