// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

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
