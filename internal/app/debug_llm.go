// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"google.golang.org/adk/model"
)

// debugLLM wraps a model.LLM and dumps every LLMRequest to a JSON
// file under the App's configDir before forwarding the call. The
// dump is intentionally simple: one file per request, named
// last_llm_request_N.json where N is a counter, so Alby can diff
// successive turns without losing the previous one.
//
// Activated only when runtime.log_level == "debug" in baifo.yaml. The
// overhead in normal runs is exactly zero (the wrap is not applied).
type debugLLM struct {
	inner   model.LLM
	dir     string
	counter atomic.Uint64
}

var _ model.LLM = (*debugLLM)(nil)

func (d *debugLLM) Name() string { return d.inner.Name() }

func (d *debugLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	d.dumpRequest(req)
	return d.inner.GenerateContent(ctx, req, stream)
}

// dumpRequest writes req as pretty JSON to last_llm_request_N.json
// inside the dump directory. Errors are silently ignored: this is a
// diagnostic side-channel and must never break a real LLM call.
func (d *debugLLM) dumpRequest(req *model.LLMRequest) {
	if d.dir == "" || req == nil {
		return
	}
	n := d.counter.Add(1)
	raw, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(d.dir, fmt.Sprintf("last_llm_request_%03d.json", n))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		slog.Warn("cannot dump LLM request", "path", path, "error", err)
	}
}
