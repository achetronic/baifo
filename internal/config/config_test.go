// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadAppliesDefaultsOnEmptyFile(t *testing.T) {
	path := writeYAML(t, "{}")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Runtime.LogLevel != defaultLogLevel {
		t.Errorf("default runtime.log_level: got %q, want %q", cfg.Runtime.LogLevel, defaultLogLevel)
	}
	if cfg.A2A.Host != defaultA2AHost {
		t.Errorf("default a2a.host: got %q, want %q", cfg.A2A.Host, defaultA2AHost)
	}
	if cfg.A2A.Port != defaultA2APort {
		t.Errorf("default a2a.port: got %d, want %d", cfg.A2A.Port, defaultA2APort)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load (missing): %v", err)
	}
	if cfg.Runtime.LogLevel != defaultLogLevel {
		t.Errorf("default runtime.log_level on missing file: got %q, want %q", cfg.Runtime.LogLevel, defaultLogLevel)
	}
}

func TestLoadExpandsEnvVars(t *testing.T) {
	t.Setenv("BAIFO_TEST_LOGLEVEL", "debug")
	path := writeYAML(t, `
runtime:
  log_level: "${BAIFO_TEST_LOGLEVEL}"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Runtime.LogLevel != "debug" {
		t.Errorf("env-expanded log_level: got %q, want %q", cfg.Runtime.LogLevel, "debug")
	}
}

func TestLogRedactionEnabledDefaultsTrue(t *testing.T) {
	cfg := Default()
	if !cfg.Secrets.LogRedactionEnabled() {
		t.Error("default LogRedactionEnabled: got false, want true")
	}
	if !cfg.Runtime.RedactLogsEnabled() {
		t.Error("default RedactLogsEnabled: got false, want true")
	}
}

func TestLogRedactionEnabledExplicitFalse(t *testing.T) {
	path := writeYAML(t, `
secrets:
  redact_in_logs: false
runtime:
  redact_logs: false
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Secrets.LogRedactionEnabled() {
		t.Error("explicit false LogRedactionEnabled: got true, want false")
	}
	if cfg.Runtime.RedactLogsEnabled() {
		t.Error("explicit false RedactLogsEnabled: got true, want false")
	}
}

func TestLoadParsesProvidersAndMCPs(t *testing.T) {
	path := writeYAML(t, `
providers:
  - name: anthropic-main
    type: anthropic
    api_key: sk-test
  - name: openai-main
    type: openai
    api_key: sk-openai
mcps:
  - name: filesystem
    type: builtin
    builtin: filesystem
  - name: github
    type: http
    endpoint: https://example.com/mcp
    headers:
      Authorization: "Bearer abc"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Providers) != 2 {
		t.Fatalf("providers: got %d, want 2", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "anthropic-main" || cfg.Providers[0].Type != "anthropic" {
		t.Errorf("providers[0]: %+v", cfg.Providers[0])
	}

	if len(cfg.MCPs) != 2 {
		t.Fatalf("mcps: got %d, want 2", len(cfg.MCPs))
	}
	if cfg.MCPs[0].Builtin != "filesystem" {
		t.Errorf("mcps[0].builtin: got %q, want filesystem", cfg.MCPs[0].Builtin)
	}
	if cfg.MCPs[1].Endpoint != "https://example.com/mcp" {
		t.Errorf("mcps[1].endpoint: got %q", cfg.MCPs[1].Endpoint)
	}
}

func TestRetryConfigDefaultsAndAccessors(t *testing.T) {
	// Absent block: disabled, accessors still return sane defaults.
	var zero RetryConfig
	if zero.RetryEnabled() {
		t.Error("absent retry block should be disabled")
	}
	if zero.Attempts() != DefaultRetryMaxAttempts {
		t.Errorf("Attempts default = %d, want %d", zero.Attempts(), DefaultRetryMaxAttempts)
	}
	if zero.InitialBackoffDuration() != DefaultRetryInitialBackoff {
		t.Errorf("InitialBackoff default wrong: %v", zero.InitialBackoffDuration())
	}
	if zero.MultiplierOr() != DefaultRetryMultiplier {
		t.Errorf("Multiplier default wrong: %v", zero.MultiplierOr())
	}
	if !zero.JitterEnabled() {
		t.Error("jitter should default to true")
	}
	if zero.StrategyOr() != DefaultRetryStrategy {
		t.Errorf("Strategy default = %q, want %q", zero.StrategyOr(), DefaultRetryStrategy)
	}

	// Providing any field implies enabled.
	r := RetryConfig{MaxAttempts: 5, Backoff: BackoffConfig{Initial: "2s", Max: "1m", Multiplier: 3}}
	if !r.RetryEnabled() {
		t.Error("a block with fields should be enabled")
	}
	if r.Attempts() != 5 {
		t.Errorf("Attempts = %d, want 5", r.Attempts())
	}
	if r.InitialBackoffDuration() != 2*time.Second {
		t.Errorf("InitialBackoff = %v, want 2s", r.InitialBackoffDuration())
	}
	if r.MaxBackoffDuration() != time.Minute {
		t.Errorf("MaxBackoff = %v, want 1m", r.MaxBackoffDuration())
	}

	// Explicit disable wins even with fields present.
	no := false
	off := RetryConfig{Enabled: &no, MaxAttempts: 9}
	if off.RetryEnabled() {
		t.Error("explicit enabled=false should win")
	}

	// Invalid duration strings fall back to defaults.
	bad := RetryConfig{MaxAttempts: 2, Backoff: BackoffConfig{Initial: "nonsense"}}
	if bad.InitialBackoffDuration() != DefaultRetryInitialBackoff {
		t.Errorf("invalid duration should fall back, got %v", bad.InitialBackoffDuration())
	}

	// Strategy is normalised and falls back to the default on unknowns.
	for in, want := range map[string]string{
		"":            DefaultRetryStrategy,
		"backoff":     RetryStrategyBackoff,
		"retry-after": RetryStrategyRetryAfter,
		"Retry-After": RetryStrategyRetryAfter,
		"  backoff  ": RetryStrategyBackoff,
		"bogus":       DefaultRetryStrategy,
	} {
		if got := (RetryConfig{Strategy: in}).StrategyOr(); got != want {
			t.Errorf("StrategyOr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMCPOptionsEffectiveAccessors(t *testing.T) {
	// Zero value returns the built-in defaults.
	var zero MCPOptions
	if got := zero.EffectiveExecOutputChars(); got != DefaultLimitExecOutputChars {
		t.Errorf("zero EffectiveExecOutputChars = %d, want %d", got, DefaultLimitExecOutputChars)
	}
	if got := zero.EffectiveReadFileChars(); got != DefaultLimitReadFileChars {
		t.Errorf("zero EffectiveReadFileChars = %d, want %d", got, DefaultLimitReadFileChars)
	}
	if got := zero.EffectiveSearchOutputChars(); got != DefaultLimitSearchOutputChars {
		t.Errorf("zero EffectiveSearchOutputChars = %d, want %d", got, DefaultLimitSearchOutputChars)
	}

	// Negative value maps to 0 (unlimited).
	neg := MCPOptions{LimitExecOutputChars: -1, LimitReadFileChars: -99, LimitSearchOutputChars: -3}
	if got := neg.EffectiveExecOutputChars(); got != 0 {
		t.Errorf("-1 EffectiveExecOutputChars = %d, want 0 (unlimited)", got)
	}
	if got := neg.EffectiveReadFileChars(); got != 0 {
		t.Errorf("-99 EffectiveReadFileChars = %d, want 0 (unlimited)", got)
	}
	if got := neg.EffectiveSearchOutputChars(); got != 0 {
		t.Errorf("-3 EffectiveSearchOutputChars = %d, want 0 (unlimited)", got)
	}

	// Positive value is returned as-is.
	pos := MCPOptions{LimitExecOutputChars: 9999, LimitReadFileChars: 12345, LimitSearchOutputChars: 7777}
	if got := pos.EffectiveExecOutputChars(); got != 9999 {
		t.Errorf("9999 EffectiveExecOutputChars = %d, want 9999", got)
	}
	if got := pos.EffectiveReadFileChars(); got != 12345 {
		t.Errorf("12345 EffectiveReadFileChars = %d, want 12345", got)
	}
	if got := pos.EffectiveSearchOutputChars(); got != 7777 {
		t.Errorf("7777 EffectiveSearchOutputChars = %d, want 7777", got)
	}

	// Constants themselves are the documented values.
	if DefaultLimitExecOutputChars != 48000 {
		t.Errorf("DefaultLimitExecOutputChars = %d, want 48000", DefaultLimitExecOutputChars)
	}
	if DefaultLimitReadFileChars != 120000 {
		t.Errorf("DefaultLimitReadFileChars = %d, want 120000", DefaultLimitReadFileChars)
	}
	if DefaultLimitSearchOutputChars != 50000 {
		t.Errorf("DefaultLimitSearchOutputChars = %d, want 50000", DefaultLimitSearchOutputChars)
	}
}

func TestTrimOversizedUserTextIsEnabled(t *testing.T) {
	true_ := true
	false_ := false
	cases := []struct {
		name   string
		cfg    TrimOversizedUserTextConfig
		wantOn bool
	}{
		{"nil pointer defaults false (opt-in)", TrimOversizedUserTextConfig{}, false},
		{"explicit true", TrimOversizedUserTextConfig{Enabled: &true_}, true},
		{"explicit false", TrimOversizedUserTextConfig{Enabled: &false_}, false},
	}
	for _, tc := range cases {
		if got := tc.cfg.IsEnabled(); got != tc.wantOn {
			t.Errorf("%s: IsEnabled() = %v, want %v", tc.name, got, tc.wantOn)
		}
	}
}

func TestTrimOversizedUserTextEffectiveMaxChars(t *testing.T) {
	// Zero value returns the built-in default.
	var zero TrimOversizedUserTextConfig
	if got := zero.EffectiveMaxChars(); got != DefaultTrimOversizedUserTextMaxChars {
		t.Errorf("zero EffectiveMaxChars = %d, want %d", got, DefaultTrimOversizedUserTextMaxChars)
	}

	// Negative value maps to the default (negative no longer means disabled;
	// use enabled: false to disable the guardrail).
	neg := TrimOversizedUserTextConfig{MaxChars: -1}
	if got := neg.EffectiveMaxChars(); got != DefaultTrimOversizedUserTextMaxChars {
		t.Errorf("-1 EffectiveMaxChars = %d, want default %d", got, DefaultTrimOversizedUserTextMaxChars)
	}
	neg2 := TrimOversizedUserTextConfig{MaxChars: -99}
	if got := neg2.EffectiveMaxChars(); got != DefaultTrimOversizedUserTextMaxChars {
		t.Errorf("-99 EffectiveMaxChars = %d, want default %d", got, DefaultTrimOversizedUserTextMaxChars)
	}

	// Positive value is returned as-is.
	pos := TrimOversizedUserTextConfig{MaxChars: 8192}
	if got := pos.EffectiveMaxChars(); got != 8192 {
		t.Errorf("8192 EffectiveMaxChars = %d, want 8192", got)
	}

	// Constant itself is the documented value.
	if DefaultTrimOversizedUserTextMaxChars != 30000 {
		t.Errorf("DefaultTrimOversizedUserTextMaxChars = %d, want 30000", DefaultTrimOversizedUserTextMaxChars)
	}
}

func TestTrimOversizedUserTextEffectiveCap(t *testing.T) {
	false_ := false
	true_ := true

	// Disabled guardrail returns 0 (plugin off).
	disabled := TrimOversizedUserTextConfig{Enabled: &false_}
	if got := disabled.EffectiveCap(); got != 0 {
		t.Errorf("disabled EffectiveCap = %d, want 0", got)
	}

	// Enabled + zero MaxChars returns the default cap.
	enabled := TrimOversizedUserTextConfig{Enabled: &true_}
	if got := enabled.EffectiveCap(); got != DefaultTrimOversizedUserTextMaxChars {
		t.Errorf("enabled+zero EffectiveCap = %d, want %d", got, DefaultTrimOversizedUserTextMaxChars)
	}

	// Nil Enabled (absent block) is disabled — guardrails are opt-in —
	// so EffectiveCap returns 0 and the plugin is not wired.
	var nilEnabled TrimOversizedUserTextConfig
	if got := nilEnabled.EffectiveCap(); got != 0 {
		t.Errorf("nil-enabled EffectiveCap = %d, want 0 (opt-in default)", got)
	}

	// Enabled + explicit positive cap returns that value.
	explicit := TrimOversizedUserTextConfig{Enabled: &true_, MaxChars: 12345}
	if got := explicit.EffectiveCap(); got != 12345 {
		t.Errorf("enabled+12345 EffectiveCap = %d, want 12345", got)
	}
}
