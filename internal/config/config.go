// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package config loads and resolves the on-disk configuration of baifo.
// See .agents/AGENTS.md and .agents/CONFIG.md for the full reference.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	yaml "gopkg.in/yaml.v3"
)

// FileName is the canonical filename of the main config inside .baifo/.
const FileName = "baifo.yaml"

// Config is the root of baifo.yaml.
type Config struct {
	Version       int              `yaml:"version"`
	EncryptionKey string           `yaml:"encryption_key"`
	Runtime       RuntimeConfig    `yaml:"runtime"`
	Guardrails    GuardrailsConfig `yaml:"guardrails"`
	Theme         ThemeConfig      `yaml:"theme"`
	A2A           A2AConfig        `yaml:"a2a"`
	Providers     []ProviderEntry  `yaml:"providers"`
	MCPs          []MCPEntry       `yaml:"mcps"`
	Spawn         SpawnConfig      `yaml:"spawn"`
	Secrets       SecretsConfig    `yaml:"secrets"`
}

// RuntimeConfig groups process-level knobs.
type RuntimeConfig struct {
	LogLevel              string `yaml:"log_level"`
	LogFormat             string `yaml:"log_format"`
	LogFile               string `yaml:"log_file"`
	RedactLogs            *bool  `yaml:"redact_logs"`
	AutoResumeSession     *bool  `yaml:"auto_resume_session"`
	ChatAutoScroll        *bool  `yaml:"chat_auto_scroll"`
	ChatKeepToolsExpanded *bool  `yaml:"chat_keep_tools_expanded"`

	// Retry controls how baifo retries failing LLM provider API calls
	// (rate limits, overloads, transient network errors) with an
	// incremental exponential backoff. Absent / disabled means no
	// retries (the historical behaviour).
	Retry RetryConfig `yaml:"retry"`
}

// RetryConfig describes the exponential-backoff retry policy applied to
// every LLM provider call. The delay before attempt N is:
//
//	delay = min(InitialBackoff * Multiplier^(N-1), MaxBackoff)
//
// optionally jittered. With MaxAttempts=4, InitialBackoff=1s and
// Multiplier=2 the waits are roughly 1s, 2s, 4s before giving up — so a
// flaky provider gets several escalating chances before the turn fails.
type RetryConfig struct {
	// Enabled turns retries on. Default false (no retries) when the
	// whole block is absent; the loader flips it on when the user
	// provides any retry field, so writing a block implies intent.
	Enabled *bool `yaml:"enabled"`

	// MaxAttempts is the TOTAL number of tries, including the first.
	// 1 means "no retry". Values < 1 are treated as the default.
	MaxAttempts int `yaml:"max_attempts"`

	// Strategy selects how the wait before each retry is computed:
	//
	//   "backoff"      exponential backoff from the Backoff block (default).
	//   "retry-after"  honour the provider's Retry-After header when the
	//                  failure carries one (e.g. a 429 rate limit), and
	//                  fall back to the exponential backoff otherwise.
	Strategy string `yaml:"strategy"`

	// Backoff holds the exponential-backoff knobs. They drive the
	// "backoff" strategy and act as the fallback for "retry-after"
	// when no header is present.
	Backoff BackoffConfig `yaml:"backoff"`
}

// BackoffConfig groups the exponential-backoff tunables under the
// retry.backoff block. The delay before retry N is:
//
//	delay = min(Initial * Multiplier^(N-1), Max)
//
// optionally jittered.
type BackoffConfig struct {
	// Initial is the wait before the first retry (a duration string
	// like "1s", "500ms"). Subsequent waits grow by Multiplier.
	Initial string `yaml:"initial"`

	// Max caps how long a single wait can grow to, so the exponential
	// growth does not blow up to minutes. Duration string.
	Max string `yaml:"max"`

	// Multiplier is the exponential growth factor between attempts.
	// Values <= 1 fall back to the default so the backoff still grows.
	Multiplier float64 `yaml:"multiplier"`

	// Jitter, when true, randomises each wait in the range
	// [delay/2, delay] to avoid synchronised retry storms across
	// concurrent workers. Default true.
	Jitter *bool `yaml:"jitter"`
}

// Retry strategy identifiers for RetryConfig.Strategy.
const (
	RetryStrategyBackoff    = "backoff"
	RetryStrategyRetryAfter = "retry-after"
)

// Retry policy defaults. Exposed as constants so the decorator and the
// config accessors agree on the same numbers.
const (
	DefaultRetryMaxAttempts    = 4
	DefaultRetryInitialBackoff = time.Second
	DefaultRetryMaxBackoff     = 30 * time.Second
	DefaultRetryMultiplier     = 2.0
	DefaultRetryStrategy       = RetryStrategyBackoff
)

// RetryEnabled reports whether retries are on. A wholly-absent block is
// off; an explicit enabled flag wins; otherwise providing any field
// implies the user wants retries.
func (r RetryConfig) RetryEnabled() bool {
	if r.Enabled != nil {
		return *r.Enabled
	}
	return r.MaxAttempts > 0 || r.Backoff.Initial != "" || r.Backoff.Max != "" || r.Backoff.Multiplier != 0
}

// Attempts returns the effective total attempt count, applying the
// default and a floor of 1.
func (r RetryConfig) Attempts() int {
	if r.MaxAttempts < 1 {
		return DefaultRetryMaxAttempts
	}
	return r.MaxAttempts
}

// InitialBackoffDuration parses the backoff initial wait, falling back
// to the default on empty or invalid input.
func (r RetryConfig) InitialBackoffDuration() time.Duration {
	return parseDurationOr(r.Backoff.Initial, DefaultRetryInitialBackoff)
}

// MaxBackoffDuration parses the backoff cap, falling back to the default.
func (r RetryConfig) MaxBackoffDuration() time.Duration {
	return parseDurationOr(r.Backoff.Max, DefaultRetryMaxBackoff)
}

// MultiplierOr returns the effective multiplier, applying the default
// when the configured value would not grow the backoff (<= 1).
func (r RetryConfig) MultiplierOr() float64 {
	if r.Backoff.Multiplier <= 1 {
		return DefaultRetryMultiplier
	}
	return r.Backoff.Multiplier
}

// JitterEnabled returns the effective jitter flag, defaulting to true.
func (r RetryConfig) JitterEnabled() bool {
	if r.Backoff.Jitter == nil {
		return true
	}
	return *r.Backoff.Jitter
}

// StrategyOr returns the effective retry strategy, normalising the input
// and falling back to the default on empty or unknown values.
func (r RetryConfig) StrategyOr() string {
	switch strings.ToLower(strings.TrimSpace(r.Strategy)) {
	case RetryStrategyRetryAfter:
		return RetryStrategyRetryAfter
	case RetryStrategyBackoff:
		return RetryStrategyBackoff
	default:
		return DefaultRetryStrategy
	}
}

// parseDurationOr parses a Go duration string, returning def on empty
// or parse error.
func parseDurationOr(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// RedactLogsEnabled returns the effective value of RedactLogs, defaulting
// to true when the field is absent.
func (r RuntimeConfig) RedactLogsEnabled() bool {
	if r.RedactLogs == nil {
		return true
	}
	return *r.RedactLogs
}

// AutoResumeSessionEnabled returns the effective value of
// AutoResumeSession, defaulting to true when the field is absent.
func (r RuntimeConfig) AutoResumeSessionEnabled() bool {
	if r.AutoResumeSession == nil {
		return true
	}
	return *r.AutoResumeSession
}

// ChatAutoScrollEnabled returns the effective value of ChatAutoScroll,
// defaulting to true when the field is absent. The TUI calls this to
// decide whether new events should pin the chat viewport to the bottom.
func (r RuntimeConfig) ChatAutoScrollEnabled() bool {
	if r.ChatAutoScroll == nil {
		return true
	}
	return *r.ChatAutoScroll
}

// ChatKeepToolsExpandedEnabled returns the effective value of ChatKeepToolsExpanded,
// defaulting to false when the field is absent.
func (r RuntimeConfig) ChatKeepToolsExpandedEnabled() bool {
	if r.ChatKeepToolsExpanded == nil {
		return false
	}
	return *r.ChatKeepToolsExpanded
}

// ThemeConfig controls terminal-capability rendering options. The
// colour palette itself is fixed (the Canarias theme) and is NOT
// user-configurable; the only knob here is whether the terminal can
// render Nerd Font glyphs.
type ThemeConfig struct {
	NerdFonts bool `yaml:"nerd_fonts"`
}

// A2AConfig describes the A2A server endpoint.
type A2AConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	PublicURL string `yaml:"public_url"`

	// Credentials gates incoming A2A requests. When empty the server
	// is unauthenticated (the historical behaviour); when a token is
	// set, every request must present it as a bearer token.
	Credentials A2ACredentials `yaml:"credentials"`
}

// A2ACredentials carries the optional bearer token that protects the
// A2A endpoints. Auth is opt-in: set Token to turn it on, leave it
// empty to serve without authentication.
type A2ACredentials struct {
	// Token is the expected bearer credential. A literal string is
	// used verbatim; a ${secret:NAME} placeholder is expanded from
	// the secrets store at boot so the token never lives in
	// baifo.yaml in plaintext. Empty disables authentication.
	Token string `yaml:"token"`
}

// ProviderEntry is one LLM provider declaration. Reference by `Name` from
// root.llm and agent specs.
type ProviderEntry struct {
	Name    string            `yaml:"name"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	Auth    string            `yaml:"auth"` // "api_key" (default) | "oauth"
	APIKey  string            `yaml:"api_key"`
	Headers map[string]string `yaml:"headers"`

	// Streaming controls whether agents backed by this provider run
	// in SSE streaming mode. A pointer so an omitted field means "use
	// the default" (streaming ON) rather than the zero value. Set it
	// to false only for OpenAI-compatible endpoints that do not
	// implement Server-Sent Events; those reject a streaming request
	// or hang. Streaming is required for long Anthropic turns (the API
	// refuses non-streamed responses that may exceed 10 minutes, which
	// high reasoning budgets can), so leave it on for Anthropic.
	Streaming *bool `yaml:"streaming"`
}

// StreamingEnabled reports the resolved streaming setting: true unless
// the operator explicitly set streaming: false. Centralised so every
// consumer agrees on the default.
func (p ProviderEntry) StreamingEnabled() bool {
	return p.Streaming == nil || *p.Streaming
}

// MCPEntry is one MCP server declaration. Built-in MCPs use Type=builtin
// and a Builtin slug; HTTP and stdio MCPs use the corresponding fields.
type MCPEntry struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`

	// builtin
	Builtin string `yaml:"builtin"`

	// http
	Endpoint string            `yaml:"endpoint"`
	Headers  map[string]string `yaml:"headers"`
	Insecure bool              `yaml:"insecure"`

	// stdio
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Workdir string            `yaml:"workdir"`

	// Auth holds the optional authentication block. When absent or with
	// kind=none, the MCP is treated as unauthenticated (Headers may still
	// carry static tokens — they are orthogonal to auth.kind).
	Auth MCPAuth `yaml:"auth"`

	// Options carries per-entry tuning for builtin MCPs. Flat by design:
	// option keys are prefixed by category (limit_*) instead of nested,
	// so the block stays one level deep and easy to autocomplete. Only
	// meaningful for type=builtin (each builtin reads the keys it knows;
	// unknown-to-that-builtin keys are ignored).
	Options MCPOptions `yaml:"options"`
}

// MCPOptions is the flat option set for builtin MCPs. The limit_*
// fields follow a three-state convention: 0 / absent = built-in
// default, positive = explicit cap in characters, -1 = unlimited
// (no cap). Disabling a single limit is done with -1; there is no
// per-option enabled flag (unlike guardrails) because granularity
// here is per-field.
type MCPOptions struct {
	// LimitExecOutputChars caps stdout and stderr (each, separately)
	// returned by the exec and process_status tools of the builtin
	// filesystem MCP. 0 = default (48000). -1 = unlimited.
	LimitExecOutputChars int `yaml:"limit_exec_output_chars"`

	// LimitReadFileChars caps the total content returned by a single
	// read_file call of the builtin filesystem MCP. 0 = default
	// (120000). -1 = unlimited.
	LimitReadFileChars int `yaml:"limit_read_file_chars"`

	// LimitSearchOutputChars caps the total content (matched lines plus
	// their context) returned by a single search call of the builtin
	// filesystem MCP. Independent of max_results, which only bounds the
	// number of matches, not their size. 0 = default (50000).
	// -1 = unlimited.
	LimitSearchOutputChars int `yaml:"limit_search_output_chars"`
}

// Output-cap defaults for the built-in filesystem MCP. These are the
// values returned by EffectiveExecOutputChars and EffectiveReadFileChars
// when the operator did not set a field explicitly (0 / absent).
const (
	// DefaultLimitExecOutputChars is the default per-stream (stdout / stderr)
	// character cap applied to the exec and process_status tools.
	DefaultLimitExecOutputChars = 48000

	// DefaultLimitReadFileChars is the default total-character cap applied to
	// a single read_file call across all its fragments.
	DefaultLimitReadFileChars = 120000

	// DefaultLimitSearchOutputChars is the default total-character cap applied
	// to a single search call across all its matches and their context.
	DefaultLimitSearchOutputChars = 50000
)

// GuardrailsConfig groups the optional protections baifo can apply
// around the model call. Each guardrail is independently toggleable and
// names the threat it mitigates. Guardrails are OPT-IN: every sub-block
// defaults to disabled when absent, because they may alter what the
// user's own content looks like to the model and that must never happen
// silently. (Producer-side caps — mcps[].options limit_* — are the
// always-on layer instead: they only bound tool outputs, which the
// model knows how to re-fetch.)
type GuardrailsConfig struct {
	// TrimOversizedUserText truncates oversized user-role text parts
	// in the request sent to the model. "User text" covers more than
	// what the human types: when a session is resumed under a different
	// agent name, the ADK injects the old transcript (tool dumps
	// included) AS user-role text ("For context: [agent] ..."), which
	// can flood the context window. Model turns and native tool-result
	// blocks are never touched. The trim is ephemeral per-request; the
	// stored session keeps full fidelity.
	TrimOversizedUserText TrimOversizedUserTextConfig `yaml:"trim_oversized_user_text"`
}

// TrimOversizedUserTextConfig controls the guardrail that caps oversized
// user-role text parts before each model call.
type TrimOversizedUserTextConfig struct {
	// Enabled toggles this guardrail. Default FALSE when absent:
	// guardrails are opt-in (pointer-bool kept for symmetry with the
	// rest of the config and so an explicit `enabled: false` is
	// distinguishable from an absent block in future migrations).
	Enabled *bool `yaml:"enabled"`

	// MaxChars caps each user-role text part, in characters.
	// 0 / absent = default (DefaultTrimOversizedUserTextMaxChars).
	// Positive = explicit cap.
	// Disabling is done via enabled: false, not via this field.
	MaxChars int `yaml:"max_chars"`
}

// DefaultTrimOversizedUserTextMaxChars is the per-part character cap used
// when MaxChars is 0 (absent / unset). Value: 30000.
const DefaultTrimOversizedUserTextMaxChars = 30000

// IsEnabled reports whether the trim_oversized_user_text guardrail is
// active. The default (nil Enabled pointer) is FALSE — guardrails are
// opt-in; the operator turns this one on with enabled: true.
func (c TrimOversizedUserTextConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return false
	}
	return *c.Enabled
}

// EffectiveMaxChars returns the resolved per-part cap:
//
//   - 0 / absent (unset)  → DefaultTrimOversizedUserTextMaxChars
//   - negative            → DefaultTrimOversizedUserTextMaxChars
//     (negative no longer means disabled; use enabled: false for that)
//   - positive            → the value as-is
func (c TrimOversizedUserTextConfig) EffectiveMaxChars() int {
	if c.MaxChars <= 0 {
		return DefaultTrimOversizedUserTextMaxChars
	}
	return c.MaxChars
}

// EffectiveCap returns 0 when the guardrail is disabled (IsEnabled()==false),
// and EffectiveMaxChars() otherwise. Callers can pass the single returned
// int to BuildContextTrimPlugin: 0 disables the plugin, positive caps it.
func (c TrimOversizedUserTextConfig) EffectiveCap() int {
	if !c.IsEnabled() {
		return 0
	}
	return c.EffectiveMaxChars()
}

// EffectiveExecOutputChars returns the resolved stdout/stderr cap:
//
//   - 0 (absent/unset) → DefaultLimitExecOutputChars
//   - negative         → 0, meaning unlimited
//   - positive         → the value as-is
func (o MCPOptions) EffectiveExecOutputChars() int {
	if o.LimitExecOutputChars == 0 {
		return DefaultLimitExecOutputChars
	}
	if o.LimitExecOutputChars < 0 {
		return 0
	}
	return o.LimitExecOutputChars
}

// EffectiveReadFileChars returns the resolved read_file total-output cap:
//
//   - 0 (absent/unset) → DefaultLimitReadFileChars
//   - negative         → 0, meaning unlimited
//   - positive         → the value as-is
func (o MCPOptions) EffectiveReadFileChars() int {
	if o.LimitReadFileChars == 0 {
		return DefaultLimitReadFileChars
	}
	if o.LimitReadFileChars < 0 {
		return 0
	}
	return o.LimitReadFileChars
}

// EffectiveSearchOutputChars returns the resolved search total-output cap:
//
//   - 0 (absent/unset): DefaultLimitSearchOutputChars
//   - negative: 0, meaning unlimited
//   - positive: the value as-is
func (o MCPOptions) EffectiveSearchOutputChars() int {
	if o.LimitSearchOutputChars == 0 {
		return DefaultLimitSearchOutputChars
	}
	if o.LimitSearchOutputChars < 0 {
		return 0
	}
	return o.LimitSearchOutputChars
}

// MCPAuth describes how baifo authenticates against an HTTP MCP. The
// zero value means no authentication. For oauth, leaving ClientID and
// ClientSecretRef empty opts into discovery: baifo first tries CIMD
// (its hardcoded Client ID Metadata Document URL) and falls back to
// Dynamic Client Registration (RFC 7591) if CIMD is not supported by
// the authorization server. When ClientID/ClientSecretRef are set,
// the OAuth client_credentials flow is used directly.
//
// Scopes are NOT modelled here on purpose: they are discovered at
// authentication time from the protected resource metadata
// (RFC 9728, /.well-known/oauth-protected-resource).
type MCPAuth struct {
	// Kind selects the auth mechanism: "none" (default), "oauth".
	Kind string `yaml:"kind"`

	// ClientID is the OAuth client identifier. Optional; empty enables
	// CIMD/DCR discovery on first authenticate.
	ClientID string `yaml:"client_id"`

	// ClientSecretRef is the name of an entry in secrets.yaml whose
	// value is the OAuth client secret. Never the secret itself.
	ClientSecretRef string `yaml:"client_secret_ref"`

	// Registration selects how baifo obtains an OAuth client when no
	// pre-registered ClientID is set. One of:
	//
	//   - "auto" (default): advertise both CIMD (Client ID Metadata
	//     Document) and DCR (RFC 7591). The MCP SDK prefers CIMD when
	//     the authorization server announces support for it, otherwise
	//     it registers dynamically.
	//   - "cimd": advertise ONLY CIMD. Use when you know the AS
	//     supports it and want to avoid creating a DCR client.
	//   - "dcr": advertise ONLY DCR. Use this when the AS announces
	//     CIMD support but rejects our client_id URL (e.g. the domain
	//     is not whitelisted in the IdP), which would otherwise make
	//     the SDK pick CIMD and fail without falling back.
	//
	// Ignored when ClientID/ClientSecretRef are set (those force the
	// client_credentials grant).
	Registration string `yaml:"registration"`
}

// MCP OAuth client-registration mode constants.
const (
	MCPRegistrationAuto = "auto"
	MCPRegistrationCIMD = "cimd"
	MCPRegistrationDCR  = "dcr"
)

// EffectiveRegistration returns the registration mode with the
// documented default ("auto") applied when the user left it blank.
func (a MCPAuth) EffectiveRegistration() string {
	if a.Registration == "" {
		return MCPRegistrationAuto
	}
	return a.Registration
}

// MCP auth kind constants.
const (
	MCPAuthKindNone  = "none"
	MCPAuthKindOAuth = "oauth"
)

// EffectiveKind returns the auth kind with the documented default
// ("none") applied when the user left it blank.
func (a MCPAuth) EffectiveKind() string {
	if a.Kind == "" {
		return MCPAuthKindNone
	}
	return a.Kind
}

// UsesDiscovery reports whether this OAuth entry should try CIMD/DCR
// instead of using statically configured credentials. It returns
// true only for oauth entries with both ClientID and ClientSecretRef
// empty; anything else is treated as a pre-registered client.
func (a MCPAuth) UsesDiscovery() bool {
	return a.EffectiveKind() == MCPAuthKindOAuth &&
		a.ClientID == "" &&
		a.ClientSecretRef == ""
}

// SpawnConfig governs which spawn tools the root agent receives.
// mode is one of: none, static, dynamic, both. See DECISIONS.md #6.
type SpawnConfig struct {
	Mode           string        `yaml:"mode"`
	CollectTimeout time.Duration `yaml:"collect_timeout"`
}

// Spawn mode constants.
const (
	SpawnModeNone    = "none"
	SpawnModeStatic  = "static"
	SpawnModeDynamic = "dynamic"
	SpawnModeBoth    = "both"
)

// EffectiveMode returns the spawn mode with the documented default
// ("both") applied when the user did not set one.
func (s SpawnConfig) EffectiveMode() string {
	if s.Mode == "" {
		return SpawnModeBoth
	}
	return s.Mode
}

// StaticEnabled reports whether the root should receive the static
// spawn / supervise tools ("static" or "both").
func (s SpawnConfig) StaticEnabled() bool {
	m := s.EffectiveMode()
	return m == SpawnModeStatic || m == SpawnModeBoth
}

// DynamicEnabled reports whether the root should receive the dynamic
// spawn tools ("dynamic" or "both"). Dynamic tools land in Phase 6;
// the helper is exposed already so the wiring is symmetric.
func (s SpawnConfig) DynamicEnabled() bool {
	m := s.EffectiveMode()
	return m == SpawnModeDynamic || m == SpawnModeBoth
}

// SecretsConfig groups runtime behaviour of the secrets pipeline.
type SecretsConfig struct {
	RedactInLogs *bool `yaml:"redact_in_logs"`
	RedactInTUI  *bool `yaml:"redact_in_tui"`

	// ScrubToolResults toggles the comprehensive-redaction pass that
	// runs after every tool call. The targeted pass (scrubbing the
	// raw values the BeforeToolCallback just substituted) is always
	// on; this knob controls the *additional* sweep that scans the
	// result for ANY value currently in `secrets.yaml`, even ones
	// the model never asked for.
	//
	// Recommended: leave it on. It defends against the "tools that
	// emit secrets they were not given" class of leak — e.g. an MCP
	// echoing a debug header that happens to contain the value of a
	// secret stored under a different name. The cost is a small
	// number of substring scans per tool call; for typical
	// deployments (a few dozen secrets, tool results under a few
	// hundred KB) it adds milliseconds.
	//
	// Default when omitted: true.
	ScrubToolResults *bool `yaml:"scrub_tool_results"`

	// MinScrubLength is the minimum length (in bytes) a stored
	// secret value must have to be eligible for the comprehensive
	// pass. Below this floor the value is skipped to avoid
	// catastrophic false positives — e.g. a secret stored with
	// value "1234" would otherwise redact every "1234" anywhere in
	// any tool result, mangling file contents, JSON numbers, port
	// numbers, you name it.
	//
	// Choose a floor high enough that legitimate substrings of that
	// length almost never appear by accident in tool output, but
	// not so high that real, short secrets stop being protected.
	// The trade-off is direct: HIGHER values → FEWER false
	// positives but MORE secrets bypassed; LOWER values → MORE
	// values protected but MORE chance of redacting unrelated text.
	//
	// Reference points:
	//   -  8 bytes (default) — protects every API key worth
	//      protecting; lets values up to 7 bytes (PINs, short
	//      passwords) escape the comprehensive pass.
	//   - 12 bytes — most modern API keys fit (`ghp_*`, `sk-*`,
	//      `AKIA…`). Short personal secrets are filtered out.
	//   - 16 bytes — only long, high-entropy values are scanned.
	//
	// Short secrets (below the floor) remain protected by the
	// targeted pass when the LLM explicitly references them via
	// `${secret:NAME}`. Only the "leaked from nowhere" scenario is
	// excluded for those values — which is generally acceptable
	// because short, low-entropy secrets are not the threat model
	// the comprehensive pass exists for.
	//
	// 0 or negative disables the floor entirely (every length is
	// scrubbed). Documented as dangerous and not recommended.
	//
	// Default when omitted: 8.
	MinScrubLength *int `yaml:"min_scrub_length"`
}

// LogRedactionEnabled returns the effective value of RedactInLogs,
// defaulting to true when the field is absent.
func (c SecretsConfig) LogRedactionEnabled() bool {
	if c.RedactInLogs == nil {
		return true
	}
	return *c.RedactInLogs
}

// TUIRedactionEnabled returns the effective value of RedactInTUI,
// defaulting to true when the field is absent.
func (c SecretsConfig) TUIRedactionEnabled() bool {
	if c.RedactInTUI == nil {
		return true
	}
	return *c.RedactInTUI
}

// ScrubToolResultsEnabled returns the effective value of
// ScrubToolResults, defaulting to true when the field is absent.
// Defense in depth is the right default; operators opt out
// explicitly when a debugging session needs raw output.
func (c SecretsConfig) ScrubToolResultsEnabled() bool {
	if c.ScrubToolResults == nil {
		return true
	}
	return *c.ScrubToolResults
}

// EffectiveMinScrubLength returns the effective value of
// MinScrubLength, defaulting to 8 when the field is absent. The
// default balances real API keys (always 16+ bytes) against
// accidental matches on short values. Values below 8 are accepted
// but documented as dangerous because they raise the false-positive
// rate sharply.
func (c SecretsConfig) EffectiveMinScrubLength() int {
	if c.MinScrubLength == nil {
		return 8
	}
	return *c.MinScrubLength
}

// ContextGuardConfig wires the adk-utils-go context guard.
type ContextGuardConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Strategy  string `yaml:"strategy"`
	MaxTokens int    `yaml:"max_tokens"`
	MaxTurns  int    `yaml:"max_turns"`
}

// Defaults applied when fields are missing.
const (
	defaultLogLevel      = "info"
	defaultLogFormat     = "console"
	defaultA2AHost       = "127.0.0.1"
	defaultA2APort       = 7777
	defaultSchemaVersion = 1
)

// FilePath returns the absolute path of baifo.yaml inside dir.
func FilePath(dir string) string {
	return filepath.Join(dir, FileName)
}

// Load reads baifo.yaml from path. Environment variables are expanded
// before parsing. A missing file yields the default configuration so
// the rest of the program can run on a fresh install.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	expanded := []byte(expandEnvPreservingSecrets(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// Default returns a Config populated with sane defaults.
func Default() *Config {
	cfg := &Config{}
	applyDefaults(cfg)
	return cfg
}

// applyDefaults fills in fields the user did not specify. It is the
// single place where default values are set; tests and callers must
// rely on this rather than duplicating the defaults inline.
func applyDefaults(cfg *Config) {
	if cfg.Version == 0 {
		cfg.Version = defaultSchemaVersion
	}
	if cfg.Runtime.LogLevel == "" {
		cfg.Runtime.LogLevel = defaultLogLevel
	}
	if cfg.Runtime.LogFormat == "" {
		cfg.Runtime.LogFormat = defaultLogFormat
	}
	if cfg.A2A.Host == "" {
		cfg.A2A.Host = defaultA2AHost
	}
	if cfg.A2A.Port == 0 {
		cfg.A2A.Port = defaultA2APort
	}
}
