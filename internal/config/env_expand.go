// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"strings"
)

// expandEnvPreservingSecrets behaves like os.ExpandEnv (substitutes
// $VAR and ${VAR} with the matching environment variable, blank when
// undefined) EXCEPT that placeholders of the form ${secret:NAME} are
// left untouched so the secrets pipeline can resolve them later.
//
// Why this exists: baifo.yaml supports two unrelated substitution
// schemes that share the ${...} shape:
//
//   - ${HOME}, ${OPENAI_API_KEY}, ... — classic env interpolation,
//     resolved at config-load time by this helper.
//   - ${secret:GEMINI_API_KEY}, ... — references into the encrypted
//     secrets store, resolved by the secrets pipeline at use time
//     (or by expandProviderSecrets in package app for early-bound
//     fields like a provider's api_key).
//
// Naively calling os.ExpandEnv would erase the second form because
// `secret:GEMINI_API_KEY` is not a valid env var name (`:` is not in
// the allowed character set) and os.Expand replaces it with an empty
// string. The substitution is selective: anything starting with
// "secret:" is preserved verbatim including the surrounding `${ }`,
// everything else is treated as a regular env var name.
func expandEnvPreservingSecrets(s string) string {
	return os.Expand(s, func(name string) string {
		if strings.HasPrefix(name, "secret:") {
			// Reconstruct the original placeholder so downstream
			// consumers see the literal text they wrote.
			return "${" + name + "}"
		}
		return os.Getenv(name)
	})
}
