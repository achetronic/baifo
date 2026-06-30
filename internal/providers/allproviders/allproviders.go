// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package allproviders blank-imports every built-in provider so that
// init() side effects register them with the providers registry.
// Importing this package once (from cmd/baifo) is enough to make
// "anthropic", "openai" and "gemini"
// available as provider types in baifo.yaml. Any OpenAI-compatible
// endpoint is an "openai" provider with a custom url; no separate type.
//
// Users who want a stripped-down build can replace this import with a
// subset of provider packages without touching any other code.
package allproviders

import (
	_ "github.com/achetronic/baifo/internal/providers/anthropic"
	_ "github.com/achetronic/baifo/internal/providers/gemini"
	_ "github.com/achetronic/baifo/internal/providers/openai"
)
