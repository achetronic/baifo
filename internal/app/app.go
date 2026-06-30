// SPDX-License-Identifier: Apache-2.0

// Package app is the top-level wiring of baifo: it owns the config, the
// providers, the MCPs registry, the storage layer, the builder, and the
// runner. The TUI talks to the rest of the system exclusively through
// the facade.Facade interface declared in this package, which lets the TUI
// stay testable in isolation and lets CLI sub-commands drive the same
// core without going through BubbleTea.
package app

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"

	"gopkg.in/yaml.v3"

	baifoagent "github.com/achetronic/baifo/internal/agent"
	"github.com/achetronic/baifo/internal/audit"
	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/config/yamledit"
	"github.com/achetronic/baifo/internal/embeddings"
	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/facts"
	"github.com/achetronic/baifo/internal/logging"
	"github.com/achetronic/baifo/internal/mcps"
	"github.com/achetronic/baifo/internal/providers"
	"github.com/achetronic/baifo/internal/scaffolds"
	"github.com/achetronic/baifo/internal/secrets"
	"github.com/achetronic/baifo/internal/sessions"
	"github.com/achetronic/baifo/internal/skills"
	"github.com/achetronic/baifo/internal/storage"
	memorytools "github.com/achetronic/baifo/internal/tools/memory"
	modelstools "github.com/achetronic/baifo/internal/tools/models"
	skilltools "github.com/achetronic/baifo/internal/tools/skills"
	spawntools "github.com/achetronic/baifo/internal/tools/spawn"
	todostools "github.com/achetronic/baifo/internal/tools/todos"
	"github.com/achetronic/baifo/internal/watcher"
	"github.com/achetronic/baifo/internal/workers"
)

// The public surface — the facade.Facade interface and every DTO it
// exchanges with clients (TUI, HTTP server, CLI sub-commands) —
// lives in internal/facade. This package implements that contract
// on the *App type defined below.
//
// We deliberately do not re-export the facade.* types here. Clients
// must import internal/facade directly so the dependency direction
// stays: facade → (nothing); app → facade. That lets the TUI test
// against a stubbed facade.Facade without pulling in app's transitive
// 15-package boot graph (storage, mcps, providers, workers, ...).

// App is the concrete facade.Facade. It owns every long-lived resource and
// orchestrates the boot sequence described in ARCHITECTURE.md.
type App struct {
	// mu protects the in-memory state that ReloadFromDisk swaps out
	// while SendMessage / ListMCPs / ... are reading it. The lock is
	// held in read mode for short bursts by every facade.Facade method that
	// touches cfg / root / runner / providers / mcps / agentTmpl, and
	// in write mode by ReloadFromDisk itself.
	mu sync.RWMutex

	cfg       *config.Config
	configDir string

	db        *storage.DB
	secrets   *secrets.Store
	providers *providers.Registry
	mcps      *mcps.Registry
	audit     *audit.Recorder
	sessions  *sessions.Service

	// titler runs the background auto-titler that turns
	// "(untitled)" sessions into something readable a couple of
	// turns into the conversation. See internal/app/title.go.
	titler *sessionTitler

	workers   *workers.Manager
	agentTmpl *agentTemplateIndex
	skills    *skills.Loader
	facts     *facts.Store

	root *baifoagent.Instance

	// rootRequestHandler is the A2A RequestHandler wrapping the root
	// agent. SendMessage drives the agentic loop through this handler
	// so the TUI and the `baifo server` endpoint share one execution
	// path. nil while the root failed to build.
	rootRequestHandler a2asrv.RequestHandler

	// rootBuildErr captures the error from the last buildRoot() call.
	// nil when the root agent built cleanly. The TUI reads this via
	// RootBuildError() to surface a precise reason when the user
	// tries to chat without a working root (e.g. "provider 'gemini'
	// not registered", "unknown model 'gemini-3-pro-preview'").
	rootBuildErr error

	// watcher fans fsnotify events from .baifo/ into reload callbacks.
	// Constructed lazily in New; nil-safe in Close.
	watcher  *watcher.Watcher
	reloadCh chan facade.ReloadEvent

	// userID and sessionID identify the active session. userID is fixed
	// for the lifetime of the process (baifo is single-user); sessionID
	// is rotated by NewSession / SwitchSession.
	userID    string
	sessionID string

	logFile *os.File

	// logRedactor is the live secrets-aware log redactor. It is created
	// in New with a nil store (logging must start before the store is
	// opened) and updated via SetStore once the store is ready. A
	// pointer so ReloadFromDisk can call SetConfig/SetStore on the
	// same instance that is already wired into the slog handler chain,
	// avoiding any window where log lines go unredacted.
	logRedactor *secrets.LogRedactor
}

// agentTemplateIndex implements spawntools.TemplateResolver against
// the parsed agents.yaml. Kept private to internal/app because it
// only exists to glue config to the spawn tools.
//
// The root entry is stored separately and intentionally excluded
// from the spawnable lookup paths (Resolve / ListTemplates): the
// root is the user-facing coordinator, not a worker; trying to
// spawn it would create a recursive supervision tree we don't
// want. Reach it through RootAgent() instead.
type agentTemplateIndex struct {
	byName  map[string]config.AgentTemplate
	root    *config.AgentTemplate
	utility *config.AgentTemplate
}

func newAgentTemplateIndex(file *config.AgentsFile) *agentTemplateIndex {
	idx := &agentTemplateIndex{byName: make(map[string]config.AgentTemplate)}
	if file == nil {
		return idx
	}
	for _, a := range file.Agents {
		if a.Utility {
			tmpl := a
			idx.utility = &tmpl
		}
		if a.Root {
			tmpl := a
			idx.root = &tmpl
			continue
		}
		idx.byName[a.Name] = a
	}
	return idx
}

// RootAgent returns the root template, or nil when none is
// registered. Mirrors AgentsFile.RootAgent so the rest of internal
// /app can reach the root via the live index without touching the
// raw config slice.
func (i *agentTemplateIndex) RootAgent() *config.AgentTemplate {
	if i == nil {
		return nil
	}
	return i.root
}

// UtilityAgent returns the utility template, or nil when none is
// registered. See AgentsFile.UtilityAgent for the contract.
func (i *agentTemplateIndex) UtilityAgent() *config.AgentTemplate {
	if i == nil {
		return nil
	}
	return i.utility
}

func (i *agentTemplateIndex) Resolve(name string) (config.AgentTemplate, bool) {
	t, ok := i.byName[name]
	return t, ok
}

// ListTemplates returns every spawnable (non-root) agent template,
// sorted by name so the output is deterministic across boots. The
// slice is freshly allocated on each call — callers can mutate it
// without affecting the index.
func (i *agentTemplateIndex) ListTemplates() []config.AgentTemplate {
	if i == nil || len(i.byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(i.byName))
	for name := range i.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]config.AgentTemplate, 0, len(names))
	for _, n := range names {
		out = append(out, i.byName[n])
	}
	return out
}

// Compile-time check: App implements facade.Facade.
var _ facade.Facade = (*App)(nil)

// appName is the AppName used in every session.Service call. ADK
// scopes sessions by (appName, userID, sessionID); baifo has a single
// app and a single user, so we keep this fixed.
const appName = "baifo"

// ErrNoRoot is returned by SendMessage when the App was constructed
// without a root agent (e.g. because the config has no root.llm). The
// TUI uses this to render a friendly "configure a provider first"
// state instead of crashing.
var ErrNoRoot = errors.New("no root agent configured")

// New constructs an App from a loaded config and the active config dir.
// All collaborators are built lazily so a partially configured baifo
// (e.g. no encryption key) still boots — the missing pieces just
// degrade gracefully when accessed.
func New(ctx context.Context, cfg *config.Config, configDir string) (*App, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	logFilePath := cfg.Runtime.LogFile
	if logFilePath == "" {
		logFilePath = filepath.Join(configDir, "baifo.log")
	} else if !filepath.IsAbs(logFilePath) {
		logFilePath = filepath.Join(configDir, logFilePath)
	}

	// Construct the log redactor with a nil store so logging can start
	// before the secrets store is opened. SetStore is called below once
	// the store is ready.
	logRedactor := secrets.NewLogRedactor(nil, cfg.Secrets.LogRedactionEnabled(), cfg.Secrets.EffectiveMinScrubLength())

	lf, err := logging.Init(logFilePath, cfg.Runtime.LogLevel, cfg.Runtime.LogFormat, logRedactor)
	if err != nil {
		return nil, fmt.Errorf("initialise logging: %w", err)
	}

	db, err := storage.Open(configDir)
	if err != nil {
		if lf != nil {
			if cerr := lf.Close(); cerr != nil {
				slog.Warn("closing log file after storage open failure", "error", cerr)
			}
		}
		return nil, fmt.Errorf("open storage: %w", err)
	}

	app := &App{
		cfg:         cfg,
		configDir:   configDir,
		db:          db,
		audit:       audit.NewRecorder(db),
		userID:      "user",
		logFile:     lf,
		logRedactor: logRedactor,
	}

	sessSvc, err := sessions.New(db)
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after sessions service failure", "error", cerr)
		}
		return nil, fmt.Errorf("open sessions service: %w", err)
	}
	app.sessions = sessSvc

	// Wire the auto-titler so untitled sessions get a meaningful
	// name once the user has done a couple of turns. The hook
	// fires on every AppendEvent commit; the titler decides
	// whether to run a one-shot LLM call off-thread.
	app.titler = newSessionTitler(app)
	app.sessions.SetAppendHook(app.titler.onAppend)

	// Secrets store is always initialised. When encryption_key is empty
	// it runs in plaintext mode (intended for local development); when
	// set, AES-256-GCM is used. The mode is recorded in the file and a
	// mismatch on reopen surfaces a clear error.
	store, err := secrets.NewStore(configDir, cfg.EncryptionKey)
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after secrets store failure", "error", cerr)
		}
		return nil, fmt.Errorf("open secrets store: %w", err)
	}
	app.secrets = store
	// Now the store is ready: wire it into the log redactor so it can
	// start redacting secret values from log lines.
	logRedactor.SetStore(store)

	// Providers need their api_key / headers ${secret:NAME} placeholders
	// resolved BEFORE the registry builds the model clients (those SDKs
	// cache the key internally). Boot fails loudly if a referenced
	// secret is missing — dragging that error through to runtime would
	// just surface as "api key is required" with no context.
	expandedProviders, err := providers.ExpandSecrets(cfg.Providers, store)
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after secrets expansion failure", "error", cerr)
		}
		return nil, fmt.Errorf("expand provider secrets: %w", err)
	}
	app.providers, err = providers.NewRegistry(expandedProviders, providers.WithRetry(retryPolicyFromConfig(cfg.Runtime.Retry)), providers.WithConfigDir(configDir))
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after providers registry failure", "error", cerr)
		}
		return nil, fmt.Errorf("build providers registry: %w", err)
	}

	app.mcps, err = mcps.NewRegistry(cfg.MCPs, mcps.Builders{})
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after mcps registry failure", "error", cerr)
		}
		return nil, fmt.Errorf("build mcps registry: %w", err)
	}
	app.mcps.WithSecrets(app.secrets)
	app.mcps.WithTokenStore(mcps.NewTokenStore(db))
	app.mcps.WithDCRClientStore(mcps.NewDCRClientStore(db))

	// Load static agent templates. A missing agents.yaml is fine
	// (results in an empty index); a malformed one is a boot error.
	agentsFile, err := config.LoadAgents(filepath.Join(configDir, config.AgentsFileName))
	if err != nil {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("closing db after agents load failure", "error", cerr)
		}
		return nil, fmt.Errorf("load agents.yaml: %w", err)
	}
	app.agentTmpl = newAgentTemplateIndex(agentsFile)
	app.skills = skills.NewLoader(configDir)

	// Best-effort embeddings engine loading.
	// A failure here is non-fatal: the facts store still works, it just
	// won't use semantic embeddings for search.
	eng, err := embeddings.New()
	if err != nil {
		slog.Warn("embeddings engine unavailable; falling back to keyword search", "error", err)
	}
	app.facts = facts.New(db, eng)

	// Worker Manager. The DriverFactory closes over the App so each
	// worker is built by the same agent.Builder the root uses.
	app.workers = workers.NewManager(workers.ManagerConfig{
		Sandbox:        &workers.SandboxAllocator{DataDir: filepath.Join(configDir, "data")},
		DriverFactory:  app.newA2AWorkerDriverFactory(),
		CollectTimeout: cfg.Spawn.CollectTimeout,
	})

	if err := app.buildRoot(ctx); err != nil {
		// Boot succeeds even when the root cannot be built so the TUI
		// can show a configuration-required state with a precise
		// reason. The error is stashed on the App so the TUI can read
		// it via RootBuildError(); ErrNoRoot is treated as "empty
		// config" (not an error), anything else is a real failure.
		if !errors.Is(err, ErrNoRoot) {
			app.rootBuildErr = err
		}
	}

	if err := app.resolveActiveSession(ctx); err != nil {
		if cerr := app.Close(); cerr != nil {
			slog.Warn("closing app after session resolve failure", "error", cerr)
		}
		return nil, fmt.Errorf("resolve active session: %w", err)
	}

	app.reloadCh = make(chan facade.ReloadEvent, 1)
	if err := app.startWatcher(); err != nil {
		// File-watching is a developer ergonomic, not a hard
		// requirement. If we can't subscribe to fsnotify (rare:
		// inotify exhaustion, missing kernel support, sandboxed
		// container without /proc) the App still boots — hot
		// reload just becomes a manual restart. The audit log
		// would surface this, but the recorder is best-effort
		// itself; log at warn and continue.
		slog.Warn("file watcher unavailable; hot-reload disabled", "error", err)
	}

	return app, nil
}

// startWatcher creates the fsnotify watcher and wires the per-file
// callbacks. The handlers run on the watcher's goroutine; they grab
// a fresh context (boot ctx may be cancelled by the time a save
// arrives) and never block — heavy work goes through ReloadFromDisk
// which guards itself with mu.
func (a *App) startWatcher() error {
	w, err := watcher.New()
	if err != nil {
		return err
	}
	a.watcher = w

	reload := func(string) {
		if err := a.ReloadFromDisk(context.Background()); err != nil {
			// Reload failures are non-fatal: the previous state
			// is still in memory.
			slog.Error("config reload failed", "err", err)
		}
	}

	// Both files live directly under configDir. We register them by
	// absolute path; the watcher watches their parent directory under
	// the hood, so it survives the temp-file+rename atomic saves that
	// editors perform (an inode-level watch would go deaf after the
	// first save — that was the long-standing hot-reload bug).
	baifoPath := config.FilePath(a.configDir)
	agentsPath := filepath.Join(a.configDir, config.AgentsFileName)
	if err := w.OnChange(baifoPath, reload); err != nil {
		return fmt.Errorf("watch baifo.yaml: %w", err)
	}
	if err := w.OnChange(agentsPath, reload); err != nil {
		// agents.yaml may legitimately not exist; hot-reload for it is
		// best-effort, so we log at Warn and continue instead of failing.
		slog.Warn("cannot watch agents.yaml; changes require a manual restart",
			"path", agentsPath, "error", err)
	}
	w.Start()
	return nil
}

// resolveActiveSession picks the session baifo starts in, respecting
// runtime.auto_resume_session: when enabled and at least one session
// exists for this user, the most recent one is resumed; otherwise a
// fresh session is created. The chosen ID lands in app.sessionID.
func (a *App) resolveActiveSession(ctx context.Context) error {
	if a.sessions == nil {
		return errors.New("sessions service not initialised")
	}

	if a.cfg.Runtime.AutoResumeSessionEnabled() {
		if entry, ok, err := a.sessions.MostRecent(ctx, appName, a.userID); err == nil && ok {
			a.sessionID = entry.ID
			// Resuming a pre-titler session at boot: nudge the
			// titler so old (untitled) conversations get a name
			// the next time they hit the threshold.
			if a.titler != nil {
				a.titler.onSessionResumed(entry)
			}
			return nil
		}
	}

	resp, err := a.sessions.Create(ctx, &session.CreateRequest{
		AppName: appName,
		UserID:  a.userID,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	a.sessionID = resp.Session.ID()
	return nil
}

// buildRoot constructs the root agent from the entry flagged
// root: true in agents.yaml. Returns ErrNoRoot when no usable
// root entry exists (no flagged agent or the agent is missing
// provider/model), leaving app.root unset so SendMessage returns
// ErrNoRoot.
func (a *App) buildRoot(ctx context.Context) error {
	root := a.rootTemplate()
	if root == nil || root.LLM.Effective() == "" || root.LLM.Model == "" {
		return ErrNoRoot
	}
	// Soft-fail when the configured provider isn't registered:
	// the user pointed root.llm.provider at something that
	// doesn't exist in baifo.yaml. We treat that as "no usable
	// root" (degraded boot) rather than as a fatal error so the
	// TUI can still come up and let the user fix the typo
	// through /agent edit.
	known := a.listProvidersLocked()
	provider := root.LLM.Effective()
	hasProvider := false
	for _, p := range known {
		if p == provider {
			hasProvider = true
			break
		}
	}
	if !hasProvider {
		return ErrNoRoot
	}

	builder := &baifoagent.Builder{
		Providers: a.providers,
		Secrets:   a.secrets,
		Audit:     a.audit,
		MCPs:      a.mcps,
		// Spawn tools take agent specs as input; if we let the
		// expander rewrite ${secret:NAME} placeholders embedded in a
		// child's prompt or initial message, the raw value would be
		// baked into the child's prompt at construction time —
		// bypassing the child's own allowlist. Listing them here
		// tells the Builder's BeforeToolCallback to forward those
		// args verbatim. See spawn.OpaqueToolNames + SECRETS.md.
		OpaqueTools: spawntools.OpaqueToolNames(),
		// Tool results are scanned for any value in the store
		// (defense in depth against tools that leak secrets they
		// were not given). The min-length floor prevents short
		// values from triggering catastrophic false positives.
		// Both knobs are configurable via baifo.yaml's
		// `secrets.scrub_tool_results` and `secrets.min_scrub_length`.
		ScrubAllResponses: a.cfg.Secrets.ScrubToolResultsEnabled(),
		MinScrubLength:    a.cfg.Secrets.EffectiveMinScrubLength(),
	}
	// When the user configured debug logging we install a wrapper that
	// writes every outgoing LLMRequest to disk. The dump lives under
	// the config dir so the operator can `cat last_llm_request_*.json`
	// and share it when something looks off in the chat.
	if a.cfg.Runtime.LogLevel == "debug" {
		dumpDir := filepath.Join(a.configDir, "data", "llm-dumps")
		if err := os.MkdirAll(dumpDir, 0o700); err != nil {
			slog.Warn("cannot create llm-dumps directory", "dir", dumpDir, "error", err)
		}
		dumper := &debugLLM{dir: dumpDir}
		builder.ModelWrapper = func(m model.LLM) model.LLM {
			dumper.inner = m
			return dumper
		}
	}

	spec := baifoagent.Spec{
		Name:                root.Name,
		Description:         root.Description,
		Prompt:              a.buildRootPrompt(root.Prompt),
		Provider:            root.LLM.Effective(),
		Model:               root.LLM.Model,
		Reasoning:           root.LLM.Reasoning,
		ReasoningAPI:        root.LLM.ReasoningAPI,
		UnrestrictedSecrets: true,
		MCPs:                baifoagent.ResolveMCPs(root.MCPs, a.listMCPsLocked()),
		Skills:              baifoagent.ResolveSkills(root.Skills, a.ListSkills()),
		ExtraTools:          a.combinedRootTools(),
	}

	inst, err := builder.Build(ctx, "root", spec)
	if err != nil {
		return fmt.Errorf("build root agent: %w", err)
	}

	// Wrap the root in an A2A executor so SendMessage and the HTTP
	// server share a single agentic loop. The context-guard
	// summariser prefers the utility agent's cheap model (when one
	// is configured); compaction is a "compress this transcript"
	// chore that doesn't need the root's expensive model.
	guardLLM := inst.LLM
	if provider, modelID, ok := a.utilityLLMRef(); ok {
		if utilLLM, err := a.providers.Model(ctx, provider, modelID); err == nil {
			guardLLM = utilLLM
		} else {
			slog.Warn("utility LLM unavailable for context guard; using root LLM",
				"provider", provider, "model", modelID, "error", err)
		}
	}
	execCfg := adka2a.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        appName,
			Agent:          inst.Agent,
			SessionService: a.sessions,
			MemoryService:  memory.Service(nil),
			PluginConfig: baifoagent.WithContextTrim(
				baifoagent.BuildContextGuardConfig(guardLLM, a.contextGuardRows(spec.Name)),
				baifoagent.BuildContextTrimPlugin(a.cfg.Guardrails.TrimOversizedUserText.EffectiveCap()),
			),
		},
		RunConfig: baifoagent.RunConfigForStreaming(a.providers.StreamingEnabled(spec.Provider)),
	}
	// The A2A execution manager detaches the run from the caller's
	// context, so Esc in the TUI could never abort a turn. The custom
	// provider re-attaches the cancellation smuggled in by SendMessage
	// (see run_cancel.go); remote HTTP clients are unaffected.
	execCfg.RunnerProvider = cancellableRunnerProvider(execCfg.RunnerConfig)
	executor := adka2a.NewExecutor(execCfg)
	rootHandler := a2asrv.NewHandler(executor)

	a.root = inst
	a.rootRequestHandler = rootHandler
	return nil
}

// SendMessage implements facade.Facade by delegating to the A2A request
// handler the App already builds for the `baifo server` endpoint. This
// gives the TUI and any remote A2A client identical semantics: same
// executor, same Task / Message / Artifact event stream, same
// translation layer. No HTTP — the call is a direct method invocation
// against the in-process handler.
//
// The previous implementation called runner.Run directly and tried to
// dedup partial / final events with hand-rolled rules; those rules
// dropped the final answer of multi-round turns, surfacing as broken
// replies. adka2a.Executor already owns the right semantics, so we
// let it drive.
func (a *App) SendMessage(ctx context.Context, text string) iter.Seq2[*facade.Event, error] {
	a.mu.RLock()
	h := a.rootRequestHandler
	sid := a.sessionID
	a.mu.RUnlock()

	if h == nil {
		return func(yield func(*facade.Event, error) bool) {
			yield(nil, ErrNoRoot)
		}
	}

	// Bind the A2A context id to our session id so adka2a routes the
	// run through the existing ADK session. The handler ignores empty
	// contextIds and starts a new one, which would orphan the chat.
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: text})
	msg.ContextID = sid
	params := &a2a.MessageSendParams{Message: msg}

	// Pin the A2A-side userID to baifo's a.userID. Without this the
	// adka2a executor falls back to "A2A_USER_" + ContextID (see
	// google.golang.org/adk/server/adka2a/metadata.go:61) and every
	// event ends up persisted under a synthetic user that nobody on
	// the read side knows about — so ListSessions, SessionEvents,
	// MsgCount and SwitchSession all look at the wrong bucket and
	// silently return empty. The metadata.go path explicitly checks
	// for a CallContext.User with a non-empty Name() and uses it
	// instead, which is exactly what we want.
	ctxWithUser, callCtx := a2asrv.WithCallContext(ctx, nil)
	callCtx.User = &a2asrv.AuthenticatedUser{UserName: a.userID}
	ctx = ctxWithUser

	// Make Esc actually stop the agent: the A2A execution manager
	// detaches the run from this context (context.WithoutCancel), so
	// we stash the cancellable context as a VALUE — values survive the
	// detachment — and cancellableRunner re-attaches the cancellation
	// on the other side. See run_cancel.go.
	ctx = withCallerCancel(ctx)

	return func(yield func(*facade.Event, error) bool) {
		for ev, err := range h.OnSendMessageStream(ctx, params) {
			if err != nil {
				slog.Debug("a2a stream error", "error", err)
				if !yield(nil, err) {
					return
				}
				continue
			}
			appEv := eventFromA2A(ev)
			logA2AEvent(ev, appEv)
			if appEv == nil {
				continue
			}
			if !yield(appEv, nil) {
				return
			}
		}
	}
}

// logA2AEvent traces every event the executor produced and how it was
// translated for the TUI, at debug level only. This is the first place
// to look when a reply renders wrong: it answers "what did the agent
// actually emit?" without attaching a debugger.
func logA2AEvent(ev a2a.Event, out *facade.Event) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	attrs := []any{"type", fmt.Sprintf("%T", ev)}
	switch e := ev.(type) {
	case *a2a.TaskStatusUpdateEvent:
		attrs = append(attrs, "state", string(e.Status.State))
	case *a2a.TaskArtifactUpdateEvent:
		attrs = append(attrs, "append", e.Append, "last_chunk", e.LastChunk)
		if partial, ok := lookupPartialMeta(e.Metadata); ok {
			attrs = append(attrs, "adk_partial", partial)
		}
	}
	if out == nil {
		attrs = append(attrs, "translated", "dropped")
	} else {
		attrs = append(attrs,
			"text_len", len(out.Text),
			"replace", out.Replace,
			"tool_calls", len(out.ToolCalls),
			"tool_results", len(out.ToolResults),
		)
	}
	slog.Debug("a2a event", attrs...)
}

// eventFromA2A translates one a2a.Event into baifo's app.Event. The
// executor emits four event kinds; we surface them as follows:
//
//   - *a2a.Message — the agent talking to the user. Maps to Text plus
//     any FunctionCall / FunctionResponse parts pulled out of DataPart
//     wrappers by adka2a.ToGenAIParts.
//
//   - *a2a.TaskArtifactUpdateEvent — an incremental chunk of the
//     agent's reply (the streaming case). Same translation as Message.
//
//   - *a2a.TaskStatusUpdateEvent — a task lifecycle marker. Most
//     states (submitted, working, completed) carry no user-facing
//     content and are skipped. A *failed* state is the exception:
//     the adka2a executor reports agent-run errors, processor
//     errors and non-zero LLMResponse error codes NOT as the
//     iterator's err value but as a TaskStateFailed status event
//     whose Status.Message holds the error text (see
//     adka2a/processor.go toTaskFailedUpdateEvent). If we dropped
//     it like the other states, a turn that errors out — very
//     commonly the LLM round right after a large `exec` tool
//     result — would end the stream with no text and no error, so
//     the TUI's "thinking" indicator just vanishes and nothing is
//     shown. We surface the failure message as a visible event
//     instead.
//
//   - *a2a.Task — a task snapshot. The TUI does not render it; we
//     return nil so the iterator skips it.
//
// Unknown event types are dropped silently to keep forward
// compatibility with future a2a-go releases.
func eventFromA2A(ev a2a.Event) *facade.Event {
	switch e := ev.(type) {
	case *a2a.Message:
		return eventFromA2AParts(e.Parts)
	case *a2a.TaskArtifactUpdateEvent:
		if e.Artifact == nil {
			return nil
		}
		out := eventFromA2AParts(e.Artifact.Parts)
		if out != nil {
			out.Replace = artifactReplaces(e)
		}
		return out
	case *a2a.TaskStatusUpdateEvent:
		if e.Status.State != a2a.TaskStateFailed {
			return nil
		}
		return failedTaskEvent(e)
	default:
		return nil
	}
}

// adkPartialMetaKey is the metadata key the ADK adka2a executor stamps
// on every artifact update to flag whether the chunk is a streaming
// partial (true) or the final, fully-aggregated turn text (false). It
// mirrors adka2a's ToA2AMetaKey("partial") — we hardcode the resolved
// string rather than import the executor's unexported constant.
const adkPartialMetaKey = "adk_partial"

// artifactReplaces decides whether an artifact-update chunk REPLACES the
// running reply (true) or APPENDS to it (false).
//
// Why this is not just `!Append`: baifo drives the adka2a executor in its
// default OutputArtifactPerRun mode, whose legacy artifact maker streams
// the assistant text as a sequence of INCREMENTAL deltas and then emits
// one final, NON-partial artifact carrying the COMPLETE turn text. Its
// Append flag does not track "is this the full text": partial deltas go
// out with Append=false, and the final aggregate can go out with
// Append=true when an earlier non-partial artifact already opened the
// response. Mapping Replace=!Append therefore made the streamed deltas
// clobber each other and the final full copy get concatenated onto the
// last delta — the "[tail of the reply] + [whole reply]" duplication.
//
// The reliable signal is the adk_partial metadata the executor stamps on
// every chunk: true for the incremental deltas (append them), false for
// the final aggregated artifact (replace the accumulated deltas with the
// authoritative full text). The flag lives on either the event or the
// artifact; we check both. When it is absent (a non-adka2a producer, or
// a pure A2A peer over HTTP), we fall back to the historical !Append
// heuristic so external interop is unchanged.
func artifactReplaces(e *a2a.TaskArtifactUpdateEvent) bool {
	if partial, ok := lookupPartialMeta(e.Metadata); ok {
		return !partial
	}
	if e.Artifact != nil {
		if partial, ok := lookupPartialMeta(e.Artifact.Metadata); ok {
			return !partial
		}
	}
	return !e.Append
}

// lookupPartialMeta extracts the adk_partial boolean from a metadata
// map, reporting whether it was present at all so callers can tell
// "explicitly partial=false" from "not set".
func lookupPartialMeta(meta map[string]any) (partial bool, ok bool) {
	if meta == nil {
		return false, false
	}
	v, present := meta[adkPartialMetaKey]
	if !present {
		return false, false
	}
	b, isBool := v.(bool)
	if !isBool {
		return false, false
	}
	return b, true
}

// failedTaskEvent turns a TaskStateFailed status update into a
// user-visible app.Event. The error text lives in the status
// Message parts (toTaskFailedUpdateEvent wraps cause.Error() in a
// TextPart). We tag it with Role "error" so the TUI renders it with
// its dedicated agent-error styling (the visual label/glyph is the
// renderer's job, so we keep the text itself clean), and always
// return a non-nil event with non-empty Text so the turn never ends
// silently — even if, for some reason, the status carried no message.
func failedTaskEvent(e *a2a.TaskStatusUpdateEvent) *facade.Event {
	msg := ""
	if e.Status.Message != nil {
		if parsed := eventFromA2AParts(e.Status.Message.Parts); parsed != nil {
			msg = parsed.Text
		}
	}
	if msg == "" {
		msg = "the agent run failed without a reported reason"
	}
	return &facade.Event{
		Role: "error",
		Text: msg,
	}
}

// eventFromA2AParts converts an a2a parts slice into an app.Event by
// going through adka2a's reverse converter, which already knows how
// to unwrap function_call / function_response DataParts back into
// genai.Parts. We then reuse eventFromSession's classification by
// inlining the same switch — cheaper than allocating a fake
// session.Event around the parts.
func eventFromA2AParts(parts []a2a.Part) *facade.Event {
	if len(parts) == 0 {
		return nil
	}
	genaiParts, err := adka2a.ToGenAIParts(parts)
	if err != nil || len(genaiParts) == 0 {
		return nil
	}
	out := &facade.Event{}
	for _, p := range genaiParts {
		if p == nil {
			continue
		}
		switch {
		case p.Thought:
			// Reasoning summaries (Gemini thinking, etc.) ride the
			// stream as text parts flagged Thought=true. They are NOT
			// part of the reply: concatenating them onto Text mixes
			// the model's internal monologue into the visible answer
			// (and the final aggregated artifact repeats them), which
			// renders as a garbled, duplicated reply in the chat.
			continue
		case p.FunctionCall != nil:
			out.ToolCalls = append(out.ToolCalls, facade.ToolCallInfo{
				CallID: p.FunctionCall.ID,
				Name:   p.FunctionCall.Name,
				Args:   p.FunctionCall.Args,
			})
		case p.FunctionResponse != nil:
			out.ToolResults = append(out.ToolResults, facade.ToolResultInfo{
				CallID: p.FunctionResponse.ID,
				Name:   p.FunctionResponse.Name,
				Result: p.FunctionResponse.Response,
			})
		default:
			out.Text += p.Text
		}
	}
	if out.Text == "" && len(out.ToolCalls) == 0 && len(out.ToolResults) == 0 {
		return nil
	}
	return out
}

// rootTemplate returns the agents.yaml entry flagged root: true,
// or nil when none exists. Callers MUST hold a.mu for read at
// least; this helper does NOT take the lock so it can be reused
// from inside both locked and lock-holding code paths.
func (a *App) rootTemplate() *config.AgentTemplate {
	if a.agentTmpl == nil {
		return nil
	}
	return a.agentTmpl.RootAgent()
}

// utilityLLMRef resolves which (provider, model) baifo should use for
// internal chores: session titling and context-guard summarising.
// Preference order:
//
//  1. The agents.yaml entry flagged utility: true, when it has a
//     complete llm block. This is the user's "use my cheap model for
//     the boring stuff" knob.
//  2. The root's llm. Always works, just spends the expensive model
//     on trivial work.
//
// Returns ok=false when neither has a usable llm (degraded boot).
// Callers MUST hold a.mu for read at least; this helper does NOT
// take the lock, mirroring rootTemplate.
func (a *App) utilityLLMRef() (provider, model string, ok bool) {
	if a.agentTmpl != nil {
		if u := a.agentTmpl.UtilityAgent(); u != nil && u.LLM.Effective() != "" && u.LLM.Model != "" {
			return u.LLM.Effective(), u.LLM.Model, true
		}
	}
	if root := a.rootTemplate(); root != nil && root.LLM.Effective() != "" && root.LLM.Model != "" {
		return root.LLM.Effective(), root.LLM.Model, true
	}
	return "", "", false
}

// RootName implements facade.Facade.
func (a *App) RootName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if root := a.rootTemplate(); root != nil && root.Name != "" {
		return root.Name
	}
	return "baifo"
}

// RootBuildError implements facade.Facade.
func (a *App) RootBuildError() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rootBuildErr
}

// ConfigDir implements facade.Facade.
func (a *App) ConfigDir() string { return a.configDir }

// ModelName implements facade.Facade.
func (a *App) ModelName() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	root := a.rootTemplate()
	if root == nil {
		return ""
	}
	return root.LLM.Effective() + "/" + root.LLM.Model
}

// SessionID implements facade.Facade.
func (a *App) SessionID() string { return a.sessionID }

// RootAgent returns the underlying ADK agent of the root instance.
// Exposed for headless callers that drive runner.Run directly and
// don't want to go through SendMessage / A2A. Returns nil while the
// root failed to build.
func (a *App) RootAgent() agent.Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.root == nil {
		return nil
	}
	return a.root.Agent
}

// UserID returns the fixed user id this App uses for every session.
func (a *App) UserID() string { return a.userID }

// ListSessions implements facade.Facade.
func (a *App) ListSessions(ctx context.Context) ([]facade.SessionInfo, error) {
	if a.sessions == nil {
		return nil, nil
	}
	entries, err := a.sessions.ListIndex(ctx, appName, a.userID)
	if err != nil {
		return nil, err
	}
	out := make([]facade.SessionInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, facade.SessionInfo{
			ID:        e.ID,
			Title:     e.Title,
			CreatedAt: e.CreatedAt.Format("2006-01-02 15:04"),
			LastAt:    e.LastAt.Format("2006-01-02 15:04"),
			MsgCount:  e.MsgCount,
		})
	}
	return out, nil
}

// NewSession implements facade.Facade.
func (a *App) NewSession(ctx context.Context) (string, error) {
	if a.sessions == nil {
		return "", errors.New("sessions service not initialised")
	}
	resp, err := a.sessions.Create(ctx, &session.CreateRequest{
		AppName: appName,
		UserID:  a.userID,
	})
	if err != nil {
		return "", err
	}
	a.sessionID = resp.Session.ID()
	return a.sessionID, nil
}

// SwitchSession implements facade.Facade.
func (a *App) SwitchSession(ctx context.Context, id string) error {
	if a.sessions == nil {
		return errors.New("sessions service not initialised")
	}
	_, err := a.sessions.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: a.userID, SessionID: id,
	})
	if err != nil {
		return err
	}
	a.sessionID = id

	// Old sessions persisted before the auto-titler existed have
	// no Title. Now that the user is back on one of them, give
	// the titler a chance to give it a name; the work happens
	// off-thread so SwitchSession remains a fast pointer swap.
	if a.titler != nil {
		if entry, ok, gerr := a.sessions.GetIndexEntry(ctx, appName, a.userID, id); gerr == nil && ok {
			a.titler.onSessionResumed(entry)
		}
	}
	return nil
}

// RenameSession implements facade.Facade.
func (a *App) RenameSession(ctx context.Context, id, title string) error {
	if a.sessions == nil {
		return errors.New("sessions service not initialised")
	}
	return a.sessions.SetTitle(ctx, appName, a.userID, id, title)
}

// DeleteSession implements facade.Facade. When the deleted session was the
// active one, a fresh session is created and its ID is returned.
func (a *App) DeleteSession(ctx context.Context, id string) (string, error) {
	if a.sessions == nil {
		return "", errors.New("sessions service not initialised")
	}
	if err := a.sessions.Delete(ctx, &session.DeleteRequest{
		AppName: appName, UserID: a.userID, SessionID: id,
	}); err != nil {
		return "", err
	}
	if a.sessionID == id {
		return a.NewSession(ctx)
	}
	return a.sessionID, nil
}

// SessionEvents implements facade.Facade.
//
// Reads the full event log of a session from SQLite and translates
// each ADK session.Event into a facade.Event the TUI can render.
// The session store already drops streaming partials on persist,
// so the returned slice contains only committed turns.
//
// Each Event carries Role ("user" / "model" / "") so the renderer
// can differentiate user bubbles from agent bubbles when repainting
// a resumed conversation. Tool calls and tool results are returned
// as separate Events (the TUI's existing chat renderer already pairs
// them by CallID for the bordered card).
//
// Events that yielded no renderable payload after the Part walk
// (e.g. a system-only state delta) are skipped so the chat doesn't
// fill with empty rows.
func (a *App) SessionEvents(ctx context.Context, id string) ([]facade.Event, error) {
	if a.sessions == nil {
		return nil, errors.New("sessions service not initialised")
	}
	resp, err := a.sessions.Get(ctx, &session.GetRequest{
		AppName: appName, UserID: a.userID, SessionID: id,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Session == nil {
		return nil, nil
	}
	out := make([]facade.Event, 0, 16)
	for ev := range resp.Session.Events().All() {
		fe := eventFromSessionEvent(ev)
		if fe == nil {
			continue
		}
		out = append(out, *fe)
	}
	return out, nil
}

// eventFromSessionEvent translates one persisted ADK session.Event
// into a facade.Event. The Part-switch mirrors eventFromA2AParts but
// works on *genai.Part values directly (session.Event already holds
// them in ev.Content.Parts, no a2a → genai conversion needed).
//
// Returns nil when the event carries no renderable payload, so the
// caller can skip it. Role is forwarded from ev.Content.Role and
// passed through verbatim; ADK uses "user" / "model" today.
func eventFromSessionEvent(ev *session.Event) *facade.Event {
	if ev == nil || ev.Content == nil || len(ev.Content.Parts) == 0 {
		return nil
	}
	out := &facade.Event{
		Role: ev.Content.Role,
		Raw:  ev,
	}
	for _, p := range ev.Content.Parts {
		if p == nil {
			continue
		}
		switch {
		case p.Thought:
			// Same rule as eventFromA2AParts: reasoning text is not
			// reply text. Skipping here keeps a resumed session's
			// transcript identical to what streaming showed live.
			continue
		case p.FunctionCall != nil:
			out.ToolCalls = append(out.ToolCalls, facade.ToolCallInfo{
				CallID: p.FunctionCall.ID,
				Name:   p.FunctionCall.Name,
				Args:   p.FunctionCall.Args,
			})
		case p.FunctionResponse != nil:
			out.ToolResults = append(out.ToolResults, facade.ToolResultInfo{
				CallID: p.FunctionResponse.ID,
				Name:   p.FunctionResponse.Name,
				Result: p.FunctionResponse.Response,
			})
		default:
			out.Text += p.Text
		}
	}
	if out.Text == "" && len(out.ToolCalls) == 0 && len(out.ToolResults) == 0 {
		return nil
	}
	return out
}

// Close implements facade.Facade.
func (a *App) Close() error {
	if a.watcher != nil {
		if err := a.watcher.Close(); err != nil {
			slog.Warn("closing file watcher", "error", err)
		}
	}
	if a.workers != nil {
		if err := a.workers.Shutdown(5 * time.Second); err != nil {
			slog.Warn("shutting down workers", "error", err)
		}
	}
	if a.logFile != nil {
		if err := a.logFile.Close(); err != nil {
			slog.Warn("closing log file", "error", err)
		}
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// SubscribeReload implements facade.Facade.
func (a *App) SubscribeReload() <-chan facade.ReloadEvent {
	return a.reloadCh
}

// ReloadFromDisk implements facade.Facade. It re-reads baifo.yaml + agents.yaml
// and rebuilds providers, mcps, agent templates and the root agent.
// The active sessions, the workers manager and the storage layer are
// preserved — a config change is not a reason to evict live state.
//
// Failure semantics: if any step fails the previous state remains
// untouched. The caller learns about the failure through the returned
// error but the App keeps serving requests with the old config.
func (a *App) ReloadFromDisk(ctx context.Context) error {
	// Load the new config OUTSIDE the lock: parsing is the slow part
	// and SendMessage callers don't need to wait for it. Only the
	// final swap requires exclusive access.
	newCfg, err := config.Load(config.FilePath(a.configDir))
	if err != nil {
		return fmt.Errorf("reload baifo.yaml: %w", err)
	}

	expandedProviders, err := providers.ExpandSecrets(newCfg.Providers, a.secrets)
	if err != nil {
		return fmt.Errorf("reload providers: expand secrets: %w", err)
	}
	newProviders, err := providers.NewRegistry(expandedProviders, providers.WithRetry(retryPolicyFromConfig(newCfg.Runtime.Retry)), providers.WithConfigDir(a.configDir))
	if err != nil {
		return fmt.Errorf("reload providers: %w", err)
	}

	newMCPs, err := mcps.NewRegistry(newCfg.MCPs, mcps.Builders{})
	if err != nil {
		return fmt.Errorf("reload mcps: %w", err)
	}
	newMCPs.WithSecrets(a.secrets)
	newMCPs.WithTokenStore(mcps.NewTokenStore(a.db))
	newMCPs.WithDCRClientStore(mcps.NewDCRClientStore(a.db))

	newAgentsFile, err := config.LoadAgents(filepath.Join(a.configDir, config.AgentsFileName))
	if err != nil {
		return fmt.Errorf("reload agents.yaml: %w", err)
	}
	newAgentTmpl := newAgentTemplateIndex(newAgentsFile)

	// Swap the lightweight state under the write lock, then drop
	// the lock before rebuilding the root agent. The rebuild may
	// take noticeable time (MCP handshakes) and itself calls back
	// into facade methods (ListMCPs, ListProviders, ...) that take
	// the read lock — holding the write lock across buildRoot
	// would deadlock against those callbacks.
	//
	// The tradeoff is that for a brief window after the swap and
	// before buildRoot completes, a concurrent SendMessage sees
	// the OLD root (a.root / a.rootRequestHandler are still the
	// pre-reload pointers). That window is small (the rebuild is
	// already paying for the MCP handshakes), and a few stale
	// turns are far cheaper than a wedged TUI.
	a.mu.Lock()
	a.cfg = newCfg
	a.providers = newProviders
	a.mcps = newMCPs
	a.agentTmpl = newAgentTmpl

	logFilePath := newCfg.Runtime.LogFile
	if logFilePath == "" {
		logFilePath = filepath.Join(a.configDir, "baifo.log")
	} else if !filepath.IsAbs(logFilePath) {
		logFilePath = filepath.Join(a.configDir, logFilePath)
	}

	// Update the live redactor in place so the handler chain already
	// wired into slog picks up the new config without any gap.
	a.logRedactor.SetConfig(newCfg.Secrets.LogRedactionEnabled(), newCfg.Secrets.EffectiveMinScrubLength())
	// a.secrets still points at the store that was valid before reload;
	// the store itself is not rebuilt on a config-only reload, so we
	// keep it as-is. SetStore is a no-op when the pointer is the same.
	a.logRedactor.SetStore(a.secrets)

	lf, err := logging.Init(logFilePath, newCfg.Runtime.LogLevel, newCfg.Runtime.LogFormat, a.logRedactor)
	if err == nil {
		if a.logFile != nil {
			if cerr := a.logFile.Close(); cerr != nil {
				slog.Warn("closing previous log file on reload", "error", cerr)
			}
		}
		a.logFile = lf
	} else {
		slog.Error("Failed to re-initialize logger on config reload", "error", err)
	}

	a.mu.Unlock()

	// skills are read fresh from disk every time the loader is
	// asked, so no swap is needed there.
	buildErr := a.buildRoot(ctx)
	a.mu.Lock()
	if buildErr != nil && !errors.Is(buildErr, ErrNoRoot) {
		a.rootBuildErr = buildErr
	} else {
		a.rootBuildErr = nil
	}
	a.mu.Unlock()

	// Best-effort notification. The channel is buffered (1) so a
	// burst of reloads collapses into one observable event for the
	// TUI — exactly what we want.
	select {
	case a.reloadCh <- facade.ReloadEvent{At: time.Now()}:
	default:
	}

	if buildErr != nil && !errors.Is(buildErr, ErrNoRoot) {
		return fmt.Errorf("rebuild root: %w", buildErr)
	}
	return nil
}

// ListWorkers implements facade.Facade.
func (a *App) ListWorkers() []facade.WorkerInfo {
	if a.workers == nil {
		return nil
	}
	infos := a.workers.List()
	out := make([]facade.WorkerInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, facade.WorkerInfo{
			ID:        info.ID,
			Name:      info.Name,
			Kind:      workerKindString(info.Kind),
			Status:    info.Status.String(),
			Elapsed:   time.Since(info.StartedAt).Round(time.Second).String(),
			LastEvent: info.LastEvent,
		})
	}
	return out
}

// workerKindString matches the spawn-tools formatter; duplicated here
// because that package is a leaf and shouldn't be imported from app.
func workerKindString(k workers.Kind) string {
	if k == workers.KindStatic {
		return "static"
	}
	return "dynamic"
}

// KillWorker implements facade.Facade.
func (a *App) KillWorker(id string, reason string) error {
	if a.workers == nil {
		return errors.New("workers manager not initialised")
	}
	return a.workers.Kill(id, reason)
}

// CollectWorker implements facade.Facade.
func (a *App) CollectWorker(ctx context.Context, id string) (string, error) {
	if a.workers == nil {
		return "", errors.New("workers manager not initialised")
	}
	info, err := a.workers.Collect(ctx, id, 0)
	if err != nil {
		return info.Output, err
	}
	return info.Output, nil
}

// ListSkills implements facade.Facade.
func (a *App) ListSkills() []string {
	if a.skills == nil {
		return nil
	}
	list, err := a.skills.List()
	if err != nil {
		return nil
	}
	return list
}

// ListMCPs implements facade.Facade.
func (a *App) ListMCPs() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listMCPsLocked()
}

// listMCPsLocked is the lock-free body of ListMCPs. Callers must
// already hold a.mu (read or write). Used by buildRoot during
// ReloadFromDisk, which holds the write lock for the whole swap.
func (a *App) listMCPsLocked() []string {
	if a.cfg == nil {
		return nil
	}
	out := make([]string, 0, len(a.cfg.MCPs))
	for _, m := range a.cfg.MCPs {
		out = append(out, m.Name)
	}
	return out
}

// MCPTools implements spawn.Universe. Resolves the named MCP through
// the registry and returns the tool names it exposes. The list is
// embedded in the spawn_dynamic_agent description so the LLM can
// see, for instance, that the filesystem MCP ships with `exec`,
// `process_status` and `process_kill` in addition to read/write —
// without this enumeration the model hallucinates capabilities from
// the MCP name. Returns nil for unknown MCPs and for transports that
// have not been wired yet (HTTP/stdio).
func (a *App) MCPTools(name string) []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.mcps == nil {
		return nil
	}
	tools, err := a.mcps.Tools(name)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		out = append(out, t.Name())
	}
	return out
}

// MCPExternal implements spawn.Universe. Reports whether the named MCP
// uses an external transport (http / stdio), whose tools are resolved
// lazily and therefore can't be enumerated by MCPTools at boot.
func (a *App) MCPExternal(name string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.mcps == nil {
		return false
	}
	external, err := a.mcps.IsExternal(name)
	if err != nil {
		return false
	}
	return external
}

// MCPYAML implements facade.Facade. Reads baifo.yaml from disk and returns
// the exact YAML chunk of the named MCP entry, comments and all.
// Falls back to a struct-reconstructed view (no comments) when the
// entry exists in memory but not on disk — e.g. for a brand-new
// entry that was just upserted via a different path.
func (a *App) MCPYAML(name string) (string, error) {
	root, err := yamledit.LoadFile(config.FilePath(a.configDir))
	if err != nil {
		return "", fmt.Errorf("load yaml: %w", err)
	}
	if node := yamledit.FindMCP(root, name); node != nil {
		data, err := yamlMarshal(node)
		if err != nil {
			return "", fmt.Errorf("marshal: %w", err)
		}
		return data, nil
	}

	// Fallback: render from the in-memory struct. Comments are lost
	// in this branch, but the entry is at least editable.
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return "", fmt.Errorf("no config loaded")
	}
	for _, m := range a.cfg.MCPs {
		if m.Name == name {
			node := yamledit.BuildMCPEntry(m)
			data, err := yamlMarshal(node)
			if err != nil {
				return "", fmt.Errorf("marshal: %w", err)
			}
			return data, nil
		}
	}
	return "", fmt.Errorf("mcp %q not found", name)
}

// MCPScaffold implements facade.Facade by delegating to the package-level
// scaffold function. It's a method on App for facade.Facade conformance.
func (a *App) MCPScaffold(suggestedName string) string {
	return scaffolds.MCP(suggestedName)
}

// UpsertMCPFromDisk implements facade.Facade. Parses yamlText as one
// MCPEntry, validates the schema (per-type invariants enforced by
// mcps package), and writes it through yamledit to preserve
// comments in the rest of baifo.yaml. Triggers a reload so the
// in-memory registry stays in sync.
//
// The buffer is parsed TWICE: once into a config.MCPEntry struct so
// we can validate it via mcps.NewRegistry, and once into a
// *yaml.Node so the comments the user wrote in the editor land in
// baifo.yaml verbatim. The struct path is throw-away; only the Node
// is persisted.
func (a *App) UpsertMCPFromDisk(ctx context.Context, yamlText string) error {
	var entry config.MCPEntry
	if err := yamlUnmarshal(yamlText, &entry); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if entry.Name == "" {
		return fmt.Errorf("missing name")
	}

	// Schema validation via a throw-away registry.
	if _, err := mcps.NewRegistry([]config.MCPEntry{entry}, mcps.Builders{}); err != nil {
		return fmt.Errorf("validate: %w", err)
	}

	// Parse the buffer a second time into a *yaml.Node so comments
	// and ordering survive the round trip. yaml.Unmarshal on a top-
	// level mapping yields a DocumentNode whose first child is the
	// mapping; we extract that mapping for UpsertMCP.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return fmt.Errorf("parse node: %w", err)
	}
	node := mappingOfDoc(&doc)
	if node == nil {
		return fmt.Errorf("expected a mapping at the top of the buffer")
	}

	path := config.FilePath(a.configDir)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load yaml: %w", err)
	}
	if err := yamledit.UpsertMCP(root, entry.Name, node); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// mappingOfDoc extracts the top-level mapping from a parsed
// DocumentNode. Returns nil if the document isn't a mapping (a YAML
// list, scalar, or empty doc), which the caller treats as an error.
func mappingOfDoc(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// MCPDetails implements facade.Facade.
func (a *App) MCPDetails() []facade.MCPDetail {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return nil
	}
	out := make([]facade.MCPDetail, 0, len(a.cfg.MCPs))
	for _, m := range a.cfg.MCPs {
		detail := facade.MCPDetail{
			Name:     m.Name,
			Type:     m.Type,
			AuthKind: m.Auth.EffectiveKind(),
			HasAuth:  m.Auth.EffectiveKind() != config.MCPAuthKindNone,
		}
		// Render a one-token "where it points" string depending on
		// the transport. The TUI uses it to give the user a hint
		// without showing the full entry.
		switch m.Type {
		case "builtin":
			detail.Endpoint = m.Builtin
		case "http":
			detail.Endpoint = m.Endpoint
		case "stdio":
			detail.Endpoint = m.Command
		}
		out = append(out, detail)
	}
	return out
}

// DeleteMCPFromDisk implements facade.Facade. It rewrites baifo.yaml without
// the named entry, preserving comments and unrelated keys via the
// yamledit package. The file watcher will fire ReloadFromDisk
// asynchronously; we also call it inline so the in-memory state is
// updated by the time DeleteMCPFromDisk returns — the watcher event
// then becomes a (cheap) no-op refresh.
func (a *App) DeleteMCPFromDisk(ctx context.Context, name string) error {
	path := config.FilePath(a.configDir)
	root, err := yamledit.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load yaml: %w", err)
	}
	if err := yamledit.DeleteMCP(root, name); err != nil {
		return err
	}
	if err := yamledit.SaveFile(path, root); err != nil {
		return fmt.Errorf("save yaml: %w", err)
	}
	return a.ReloadFromDisk(ctx)
}

// AuthenticateMCP implements facade.Facade. Delegates to the mcps registry,
// which knows how to run the right OAuth flow for this MCP's auth
// configuration.
func (a *App) AuthenticateMCP(ctx context.Context, name string, force bool) error {
	a.mu.RLock()
	registry := a.mcps
	a.mu.RUnlock()
	if registry == nil {
		return fmt.Errorf("mcps registry not initialised")
	}
	_, err := registry.Authenticate(ctx, name, mcps.AuthenticateOptions{Force: force})
	return err
}

// TestMCPConnection implements facade.Facade. Returns a single
// human-readable line describing the outcome — caller surfaces it
// in the chat row verbatim. Built-in MCPs report "in-process"
// since there's nothing remote to test; HTTP / stdio go through
// a full connect + ListTools round trip.
func (a *App) TestMCPConnection(ctx context.Context, name string) (string, error) {
	a.mu.RLock()
	registry := a.mcps
	a.mu.RUnlock()
	if registry == nil {
		return "", fmt.Errorf("mcps registry not initialised")
	}
	res, err := registry.TestConnection(ctx, name)
	if err != nil {
		return "✗ " + err.Error(), nil
	}
	// Status line shape: "✓ connected · 12 tools · 230ms · serverName/1.4"
	// Server name is appended only when supplied by the MCP.
	out := fmt.Sprintf("✓ connected · %d tool", res.ToolCount)
	if res.ToolCount != 1 {
		out += "s"
	}
	out += fmt.Sprintf(" · %s", res.Elapsed.Round(time.Millisecond))
	if res.ServerName != "" {
		ver := res.ServerVersion
		if ver == "" {
			ver = "?"
		}
		out += " · " + res.ServerName + "/" + ver
	}
	return out, nil
}

// ClearMCPAuth implements facade.Facade. Delegates to the registry,
// which knows about both token and DCR-client stores.
func (a *App) ClearMCPAuth(_ context.Context, name string) error {
	a.mu.RLock()
	registry := a.mcps
	a.mu.RUnlock()
	if registry == nil {
		return fmt.Errorf("mcps registry not initialised")
	}
	return registry.ClearAuth(name)
}

// ListProviders implements facade.Facade.
func (a *App) ListProviders() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listProvidersLocked()
}

// listProvidersLocked is the lock-free body of ListProviders. See
// listMCPsLocked for the rationale: buildRoot already holds the
// outer lock during ReloadFromDisk and needs to read providers
// without recursing.
func (a *App) listProvidersLocked() []string {
	if a.cfg == nil {
		return nil
	}
	out := make([]string, 0, len(a.cfg.Providers))
	for _, p := range a.cfg.Providers {
		out = append(out, p.Name)
	}
	return out
}

// ConfiguredProviders implements modelstools.Catalog. It returns the
// (name, type, url) of every provider declared in baifo.yaml so the
// list_models tool can map each one onto the embedded model catalogue
// (or flag a custom endpoint, when url is set).
func (a *App) ConfiguredProviders() []modelstools.ProviderRef {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.cfg == nil {
		return nil
	}
	out := make([]modelstools.ProviderRef, 0, len(a.cfg.Providers))
	for _, p := range a.cfg.Providers {
		out = append(out, modelstools.ProviderRef{Name: p.Name, Type: p.Type, URL: p.URL})
	}
	return out
}

// ListSecretNames implements facade.Facade.
func (a *App) ListSecretNames() []string {
	if a.secrets == nil {
		return nil
	}
	entries, err := a.secrets.List()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// SecretDetails implements spawn.Universe. It enriches the
// spawn_dynamic_agent description with the human-readable purpose of
// each secret (stored in secrets.yaml as `description:` per entry)
// so the LLM picks the right secret without trial and error. Returns
// nil when the secrets store is not initialised; values themselves
// are never included.
func (a *App) SecretDetails() []spawntools.NamedDescription {
	if a.secrets == nil {
		return nil
	}
	entries, err := a.secrets.List()
	if err != nil {
		return nil
	}
	out := make([]spawntools.NamedDescription, 0, len(entries))
	for _, e := range entries {
		out = append(out, spawntools.NamedDescription{
			Name:        e.Name,
			Description: e.Description,
		})
	}
	return out
}

// SpawnSkillDetails is spawn.Universe.SkillDetails. The richer
// facade.Facade.SkillDetails returns a different (app-local) struct used by
// the TUI; this one shapes the same data into spawn's vocabulary so
// the spawn_dynamic_agent description carries skill names AND their
// frontmatter description — without that, the LLM has no idea what
// each skill actually does.
func (a *App) SpawnSkillDetails() []spawntools.NamedDescription {
	details := a.SkillDetails()
	out := make([]spawntools.NamedDescription, 0, len(details))
	for _, d := range details {
		out = append(out, spawntools.NamedDescription{
			Name:        d.Name,
			Description: d.Description,
		})
	}
	return out
}

// ListAgentTemplates implements facade.Facade.
func (a *App) ListAgentTemplates() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.agentTmpl == nil {
		return nil
	}
	out := make([]string, 0, len(a.agentTmpl.byName))
	for name := range a.agentTmpl.byName {
		out = append(out, name)
	}
	return out
}

// ListFacts implements facade.Facade.
func (a *App) ListFacts() []string {
	if a.facts == nil {
		return nil
	}
	entries, err := a.facts.List(appName, a.userID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		// Truncate long content so the overlay row stays readable.
		text := e.Content
		if len(text) > 80 {
			text = text[:77] + "..."
		}
		out = append(out, text)
	}
	return out
}

// spawnToolsForRoot returns the spawn / supervise tools the root
// agent should receive, based on cfg.Spawn.Mode. Static and dynamic
// are independently togglable; when both are disabled (mode=none)
// the function returns nil and no spawn tools are exposed.
func (a *App) spawnToolsForRoot() []tool.Tool {
	if a.workers == nil {
		return nil
	}
	if !a.cfg.Spawn.StaticEnabled() && !a.cfg.Spawn.DynamicEnabled() {
		return nil
	}
	root := a.rootTemplate()
	rootDefaults := spawntools.RootDefaults{}
	if root != nil {
		rootDefaults.Provider = root.LLM.Effective()
		rootDefaults.Model = root.LLM.Model
	}
	tools := &spawntools.Tools{
		Manager:       a.workers,
		Templates:     a.agentTmpl,
		Universe:      a,
		EnableDynamic: a.cfg.Spawn.DynamicEnabled(),
		RootDefaults:  rootDefaults,
		// ParentAllowedSecrets is intentionally nil here: the root
		// is the user-facing coordinator and the frontier of trust
		// (see SECRETS.md). Sub-agents validated by this Tools may
		// request any secret in the global universe; only future
		// nested-spawn scenarios (where the parent is a sub-agent
		// with its own allowlist) need a non-nil value.
		ParentAllowedSecrets: nil,
	}
	list, err := tools.ADKTools()
	if err != nil {
		return nil
	}
	return list
}

// combinedRootTools concatenates every extra-tool source the root
// agent should receive: spawn / supervise, memory, todos, skills and
// model discovery. Each source decides internally whether to
// contribute or stay quiet.
func (a *App) combinedRootTools() []tool.Tool {
	var tools []tool.Tool
	tools = append(tools, a.spawnToolsForRoot()...)
	tools = append(tools, a.memoryToolsForRoot()...)
	tools = append(tools, a.todosToolsForRoot()...)
	tools = append(tools, a.skillToolsForRoot()...)
	tools = append(tools, a.modelToolsForRoot()...)
	return tools
}

// modelToolsForRoot exposes the list_models tool so the root can
// discover which models each configured provider offers. The root has
// it unconditionally: it is useful both when composing a dynamic
// worker on the fly AND when the user asks the root to author a static
// agent template (it needs to pick a sensible model id either way).
// Errors during construction surface as zero tools rather than a boot
// failure.
func (a *App) modelToolsForRoot() []tool.Tool {
	out, err := (&modelstools.Tools{Catalog: a}).ADKTools()
	if err != nil {
		return nil
	}
	return out
}

// todosToolsForRoot returns the todos_list / todos_write /
// todos_update / todos_clear tools. The list is stored under the
// session-state key "todos", which contextguard already inspects
// on every summarisation pass — so a plan written here survives
// context-window compaction without any baifo-side bookkeeping.
// Errors during toolset construction surface as zero tools so a
// future per-tool init failure doesn't take the whole chat down.
func (a *App) todosToolsForRoot() []tool.Tool {
	out, err := todostools.New().ADKTools()
	if err != nil {
		return nil
	}
	return out
}

// skillToolsForRoot exposes the ADK skill toolset (list_skills,
// load_skill, load_skill_resource) to the root agent so it can
// discover and use the skills installed under .baifo/skills/. Any
// error building the toolset surfaces as zero tools rather than a
// boot failure: a missing skills dir or a malformed SKILL.md should
// never prevent the chat from coming up.
func (a *App) skillToolsForRoot() []tool.Tool {
	if a.skills == nil {
		return nil
	}
	src, err := a.skills.Source()
	if err != nil {
		return nil
	}
	out, err := skilltools.New(src).ADKTools()
	if err != nil {
		return nil
	}
	return out
}

// memoryToolsForRoot returns the search_memory / save_to_memory /
// update_memory / delete_memory tools backed by the facts store. The
// tools are always registered when a facts store exists; we do not
// gate them on a config flag because long-term memory is part of the
// root agent's contract (ARCHITECTURE.md).
func (a *App) memoryToolsForRoot() []tool.Tool {
	if a.facts == nil {
		return nil
	}
	out, err := memorytools.New(a.facts, appName).ADKTools()
	if err != nil {
		return nil
	}
	return out
}

// providerStreamingEnabled reports whether the named provider runs in
// streaming mode. Wired into the worker driver factory so each spawned
// worker honours its provider's `streaming` config like the root does.
// Guards a nil registry (degraded boot) by defaulting to streaming.
func (a *App) providerStreamingEnabled(provider string) bool {
	a.mu.RLock()
	p := a.providers
	a.mu.RUnlock()
	if p == nil {
		return true
	}
	return p.StreamingEnabled(provider)
}

// buildWorkerAgent is the hook the workers package calls to materialise
// each spawned worker. It funnels every worker through the same
// agent.Builder the root uses so the secrets pipeline, audit and
// MCP registry apply uniformly.
func (a *App) buildWorkerAgent(ctx context.Context, workerID string, spec workers.Spec, _ string) (agent.Agent, error) {
	builder := &baifoagent.Builder{
		Providers: a.providers,
		Secrets:   a.secrets,
		Audit:     a.audit,
		MCPs:      a.mcps,
		// Spawn tools take agent specs as input; if we let the
		// expander rewrite ${secret:NAME} placeholders embedded in a
		// child's prompt or initial message, the raw value would be
		// baked into the child's prompt at construction time —
		// bypassing the child's own allowlist. Listing them here
		// tells the Builder's BeforeToolCallback to forward those
		// args verbatim. See spawn.OpaqueToolNames + SECRETS.md.
		OpaqueTools: spawntools.OpaqueToolNames(),
		// Tool results are scanned for any value in the store
		// (defense in depth against tools that leak secrets they
		// were not given). The min-length floor prevents short
		// values from triggering catastrophic false positives.
		// Both knobs are configurable via baifo.yaml's
		// `secrets.scrub_tool_results` and `secrets.min_scrub_length`.
		ScrubAllResponses: a.cfg.Secrets.ScrubToolResultsEnabled(),
		MinScrubLength:    a.cfg.Secrets.EffectiveMinScrubLength(),
	}
	agentSpec := baifoagent.Spec{
		Name:           spec.Name,
		Description:    spec.Description,
		Prompt:         spec.Prompt,
		Provider:       spec.Provider,
		Model:          spec.Model,
		Reasoning:      spec.Reasoning,
		ReasoningAPI:   spec.ReasoningAPI,
		AllowedSecrets: spec.AllowedSecrets,
		MCPs:           spec.MCPs,
		Skills:         spec.Skills,
	}
	inst, err := builder.Build(ctx, workerID, agentSpec)
	if err != nil {
		return nil, err
	}
	return inst.Agent, nil
}

// contextGuardRows assembles the input for
// baifoagent.BuildContextGuardConfig from the App's current cfg +
// agent-template index. We register the root agent first so it
// goes through summarisation under its own name; then every
// static-template agent that opted in. Built fresh on each
// buildRoot call so ReloadFromDisk picks up new templates.
func (a *App) contextGuardRows(rootName string) []baifoagent.AgentGuardSpec {
	var rootGuard config.ContextGuardConfig
	if root := a.rootTemplate(); root != nil {
		rootGuard = root.ContextGuard
	}
	rows := []baifoagent.AgentGuardSpec{
		{Name: rootName, Config: rootGuard},
	}
	if a.agentTmpl != nil {
		for _, t := range a.agentTmpl.byName {
			rows = append(rows, baifoagent.AgentGuardSpec{
				Name:   t.Name,
				Config: t.ContextGuard,
			})
		}
	}
	return rows
}

// retryPolicyFromConfig translates the user-facing RetryConfig from
// baifo.yaml into the providers package's resolved RetryPolicy. A
// disabled (or absent) block yields a zero policy, which the providers
// registry treats as "no retries" (models are returned unwrapped).
func retryPolicyFromConfig(rc config.RetryConfig) providers.RetryPolicy {
	if !rc.RetryEnabled() {
		return providers.RetryPolicy{}
	}
	return providers.RetryPolicy{
		MaxAttempts:    rc.Attempts(),
		InitialBackoff: rc.InitialBackoffDuration(),
		MaxBackoff:     rc.MaxBackoffDuration(),
		Multiplier:     rc.MultiplierOr(),
		Jitter:         rc.JitterEnabled(),
		Strategy:       rc.StrategyOr(),
	}
}
