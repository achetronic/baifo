// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package overlays

import (
	"errors"
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
	"gopkg.in/yaml.v3"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/mcps"
	"github.com/achetronic/baifo/internal/modelcatalog"
	"github.com/achetronic/baifo/internal/providers"
	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// ValidateMCPYAML is the Validator the editor runs on Ctrl+S when
// editing an MCP entry. It parses the buffer as a single MCPEntry,
// then runs it through mcps.NewRegistry to apply the per-type
// invariants (endpoint required for http, command required for
// stdio, supported builtin slug, ...). The same validation is
// reapplied on persist by UpsertMCPFromDisk, but doing it here too
// lets us short-circuit before the editor closes so the user sees
// the error on the same buffer.
func ValidateMCPYAML(buf string) []error {
	var entry config.MCPEntry
	if err := yaml.Unmarshal([]byte(buf), &entry); err != nil {
		return []error{err}
	}
	if entry.Name == "" {
		return []error{errors.New("missing name")}
	}
	if _, err := mcps.NewRegistry([]config.MCPEntry{entry}, mcps.Builders{}); err != nil {
		return []error{err}
	}
	return nil
}

// ValidateAgentYAML parses the buffer as a config.AgentTemplate and
// runs it through the same validateAgents helper LoadAgents uses at
// boot. Both paths now share the rules; the editor refuses to save
// any document that LoadAgents would have rejected later anyway.
func ValidateAgentYAML(buf string) []error {
	var tmpl config.AgentTemplate
	if err := yaml.Unmarshal([]byte(buf), &tmpl); err != nil {
		return []error{err}
	}
	if tmpl.Name == "" {
		return []error{errors.New("missing name")}
	}
	if err := config.ValidateAgents([]config.AgentTemplate{tmpl}); err != nil {
		return []error{err}
	}
	return nil
}

// ValidateFactYAML parses the buffer as a fact document and reports
// schema errors. The shape is intentionally simple: a top-level
// 'content' string (required, non-empty) and an optional 'category'
// string. Multi-line content uses YAML's literal block scalar; the
// parser handles that transparently.
func ValidateFactYAML(buf string) []error {
	content, _, err := ParseFactYAML(buf)
	if err != nil {
		return []error{err}
	}
	if content == "" {
		return []error{errors.New("content is required")}
	}
	return nil
}

// ParseFactYAML extracts content and category from the buffer.
// Used by both the editor validator and the save handler so the
// two paths agree on the schema. The buffer is parsed with yaml.v3
// to honour multi-line strings under the '|' block scalar form.
func ParseFactYAML(buf string) (content, category string, err error) {
	var doc struct {
		Content  string `yaml:"content"`
		Category string `yaml:"category"`
	}
	if err := yaml.Unmarshal([]byte(buf), &doc); err != nil {
		return "", "", err
	}
	// Trim trailing whitespace that the literal block scalar
	// sometimes adds; the agent doesn't need it.
	return strings.TrimSpace(doc.Content), strings.TrimSpace(doc.Category), nil
}

// ValidateProviderYAML parses the buffer as a config.ProviderEntry
// and runs it through providers.NewRegistry. Same posture as the
// MCP and agent validators: share the boot-time rules so the editor
// refuses anything the next reload would reject anyway.
func ValidateProviderYAML(buf string) []error {
	var entry config.ProviderEntry
	if err := yaml.Unmarshal([]byte(buf), &entry); err != nil {
		return []error{err}
	}
	if entry.Name == "" {
		return []error{errors.New("missing name")}
	}
	if _, err := providers.NewRegistry([]config.ProviderEntry{entry}); err != nil {
		return []error{err}
	}
	return nil
}

// SecretCompletionProvider returns a CompletionProvider that lists
// the names of every secret stored in secrets.yaml. The provider
// closes over the facade so the list refreshes whenever the user
// adds new secrets (the facade reads through the secrets store on
// every call, which is cheap and avoids stale snapshots).
//
// The completer's contract is "Insert replaces the trigger", so we
// hand back the WHOLE ${secret:NAME} expression (including the
// `${secret:` prefix the user already typed and the closing `}`).
// Returning only `NAME}` would leave the buffer with the bare
// name and an orphan brace.
func SecretCompletionProvider(f facade.Facade) editor.CompletionProvider {
	return func(_ string, _ editor.CompletionContext) []editor.Completion {
		if f == nil {
			return nil
		}
		names := f.ListSecretNames()
		out := make([]editor.Completion, 0, len(names))
		for _, n := range names {
			out = append(out, editor.Completion{
				View:   n,
				Insert: "${secret:" + n + "}",
			})
		}
		return out
	}
}

// ModelCompletionProvider returns a CompletionProvider that
// surfaces LLM models. It closes over the facade so it can detect
// which provider the active agent is configured to use. If a
// provider name is declared on a sibling `provider:` line and resolves
// via the facade, only that matched provider's models are suggested.
// Otherwise, the full catwalk catalogue is offered as fallback.
func ModelCompletionProvider(f facade.Facade) editor.CompletionProvider {
	return func(_ string, ctx editor.CompletionContext) []editor.Completion {
		if f != nil {
			providerName := providerNameFromLines(ctx.Lines, ctx.Line)
			if providerName != "" {
				var targetDetail facade.ProviderDetail
				found := false
				for _, d := range f.ProviderDetails() {
					if strings.EqualFold(d.Name, providerName) {
						targetDetail = d
						found = true
						break
					}
				}
				if found {
					cw, matchKind, ok := modelcatalog.Resolve(targetDetail.Type, targetDetail.URL)
					if ok && matchKind != modelcatalog.MatchNone {
						out := make([]editor.Completion, 0, len(cw.Models))
						for _, m := range cw.Models {
							view := string(cw.ID) + "/" + m.ID
							if m.Name != "" && m.Name != m.ID {
								view += "  " + m.Name
							}
							out = append(out, editor.Completion{
								View:   view,
								Insert: "model: " + m.ID,
							})
						}
						return out
					}
				}
			}
		}

		// Fallback path: full catwalk catalogue
		providers := embedded.GetAll()
		total := 0
		for _, p := range providers {
			total += len(p.Models)
		}
		out := make([]editor.Completion, 0, total)
		for _, p := range providers {
			for _, m := range p.Models {
				view := string(p.ID) + "/" + m.ID
				if m.Name != "" && m.Name != m.ID {
					view += "  " + m.Name
				}
				out = append(out, editor.Completion{
					View:   view,
					Insert: "model: " + m.ID,
				})
			}
		}
		return out
	}
}

// ProviderTypeCompletionProvider returns a CompletionProvider that
// surfaces the provider types baifo supports (anthropic, openai,
// gemini). Used in the provider-edit flow where the user writes
// `type:`. Any OpenAI-compatible endpoint (Ollama, OpenRouter, vLLM,
// ...) is declared as openai with a custom url, so it needs no type of
// its own.
//
// The Insert re-emits the full `type: <id>` line because the completer
// replaces the trigger verbatim.
func ProviderTypeCompletionProvider() editor.CompletionProvider {
	return func(_ string, _ editor.CompletionContext) []editor.Completion {
		types := []string{"anthropic", "openai", "gemini"}
		out := make([]editor.Completion, 0, len(types))
		for _, id := range types {
			out = append(out, editor.Completion{
				View:   id,
				Insert: "type: " + id,
			})
		}
		return out
	}
}

// baifoReasoningLevels is the effort vocabulary baifo actually honours,
// in increasing order. It is intentionally a subset of what some
// catwalk models advertise (a few list "xhigh" / "max"): the
// request-level path for openai and gemini goes through
// genai.ThinkingLevel, which only defines minimal/low/medium/high, so
// those are the only values baifo can faithfully deliver to every
// provider. Keep this in sync with internal/agent.ValidReasoning.
var baifoReasoningLevels = []string{"minimal", "low", "medium", "high"}

// ReasoningCompletionProvider returns a CompletionProvider for the
// `reasoning:` key in the agent editor. It tailors the suggestions to
// the model declared on the nearest `model:` line above the cursor:
//
//   - Known reasoning model to only the levels that model supports
//     (intersection of its catwalk reasoning_levels with the baifo
//     vocabulary), with its default effort flagged.
//   - Known non-reasoning model to just "off", flagged, because sending
//     a reasoning effort to such a model makes the provider API reject
//     the call.
//   - Unknown model (e.g. a custom-endpoint id catwalk
//     doesn't catalogue, or no model typed yet) to the full vocabulary,
//     since baifo can't tell what the endpoint supports and the user
//     may know better.
//
// "off" is always offered first as the explicit "use the model's
// default" choice.
func ReasoningCompletionProvider() editor.CompletionProvider {
	return func(_ string, ctx editor.CompletionContext) []editor.Completion {
		modelID := modelIDFromLines(ctx.Lines, ctx.Line)

		out := []editor.Completion{
			{View: "off  (use the model's default)", Insert: "reasoning: off"},
		}

		m, known := catwalkModelByID(modelID)
		switch {
		case !known:
			// Unknown / not yet typed: offer everything baifo accepts.
			for _, lvl := range baifoReasoningLevels {
				out = append(out, editor.Completion{View: lvl, Insert: "reasoning: " + lvl})
			}
		case !m.CanReason || len(m.ReasoningLevels) == 0:
			// Known model that does not reason: only "off" is safe; the
			// single annotated entry already explains why.
			out[0].View = "off  (" + modelID + " is not a reasoning model)"
		default:
			supported := intersectReasoning(m.ReasoningLevels)
			for _, lvl := range supported {
				view := lvl
				if lvl == strings.ToLower(strings.TrimSpace(m.DefaultReasoningEffort)) {
					view += "  (model default)"
				}
				out = append(out, editor.Completion{View: view, Insert: "reasoning: " + lvl})
			}
		}
		return out
	}
}

// modelIDFromLines extracts the model id from the nearest `model:`
// line at or above cursorLine. Falls back to the first `model:` line
// anywhere in the buffer, then to "". Tolerant of the leading
// indentation the llm block uses.
func modelIDFromLines(lines []string, cursorLine int) string {
	pick := func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "model:") {
			return "", false
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "model:"))
		// Drop an inline comment if present.
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		return v, v != ""
	}
	// Search upward from the cursor first (the model line normally sits
	// just above reasoning inside the same llm block).
	for i := min(cursorLine, len(lines)-1); i >= 0; i-- {
		if v, ok := pick(lines[i]); ok {
			return v
		}
	}
	for _, line := range lines {
		if v, ok := pick(line); ok {
			return v
		}
	}
	return ""
}

// providerNameFromLines extracts the provider name from the nearest `provider:`
// line at or above cursorLine. Falls back to the first `provider:` line
// anywhere in the buffer, then to "". Tolerant of the leading
// indentation the llm block uses.
func providerNameFromLines(lines []string, cursorLine int) string {
	pick := func(line string) (string, bool) {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "provider:") {
			return "", false
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "provider:"))
		// Drop an inline comment if present.
		if i := strings.Index(v, "#"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		return v, v != ""
	}
	// Search upward from the cursor first (the provider line normally sits
	// just above model inside the same llm block).
	for i := min(cursorLine, len(lines)-1); i >= 0; i-- {
		if v, ok := pick(lines[i]); ok {
			return v
		}
	}
	for _, line := range lines {
		if v, ok := pick(line); ok {
			return v
		}
	}
	return ""
}

// catwalkModelByID finds a model by exact id across every provider in
// the embedded catalogue. Returns ok=false for an empty id or a miss.
func catwalkModelByID(modelID string) (catwalk.Model, bool) {
	if modelID == "" {
		return catwalk.Model{}, false
	}
	for _, p := range embedded.GetAll() {
		for _, m := range p.Models {
			if m.ID == modelID {
				return m, true
			}
		}
	}
	return catwalk.Model{}, false
}

// intersectReasoning returns the baifo-supported levels that the model
// advertises, preserving baifo's increasing-effort order. Catalogue
// levels outside the baifo vocabulary (e.g. "xhigh", "max") are dropped
// because baifo cannot deliver them through genai.ThinkingLevel.
func intersectReasoning(modelLevels []string) []string {
	have := make(map[string]struct{}, len(modelLevels))
	for _, l := range modelLevels {
		have[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
	}
	var out []string
	for _, lvl := range baifoReasoningLevels {
		if _, ok := have[lvl]; ok {
			out = append(out, lvl)
		}
	}
	// A reasoning model whose advertised levels are all outside our
	// vocabulary still benefits from the full set rather than nothing.
	if len(out) == 0 {
		return append([]string(nil), baifoReasoningLevels...)
	}
	return out
}
