package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a"
	"google.golang.org/adk/session"

	baifoagent "github.com/achetronic/baifo/internal/agent"
	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/workers"
)

type a2aWorkerDriver struct {
	handler   a2asrv.RequestHandler
	sessionID string
	userID    string

	mu        sync.Mutex
	lastReply string
	idleCh    chan struct{}
}

func (a *App) newA2AWorkerDriverFactory() workers.DriverFactory {
	return func(workerID string, spec workers.Spec, sandbox string) (workers.Driver, error) {
		ctx := context.Background()

		agentInst, err := a.buildWorkerAgent(ctx, workerID, spec, sandbox)
		if err != nil {
			return nil, fmt.Errorf("build agent: %w", err)
		}

		sess := session.InMemoryService()
		sessionID := workerID

		execCfg := adka2a.ExecutorConfig{
			RunnerConfig: runner.Config{
				AppName:        "baifo-worker",
				Agent:          agentInst,
				SessionService: sess,
				MemoryService:  memory.Service(nil),
				PluginConfig: baifoagent.WithContextTrim(
					baifoagent.BuildContextGuardConfig(nil, nil), // Workers don't need context guard yet
					baifoagent.BuildContextTrimPlugin(a.cfg.Guardrails.TrimOversizedUserText.EffectiveCap()),
				),
			},
			RunConfig: baifoagent.RunConfigForStreaming(a.providerStreamingEnabled(spec.Provider)),
		}
		executor := adka2a.NewExecutor(execCfg)
		handler := a2asrv.NewHandler(executor)

		return &a2aWorkerDriver{
			handler:   handler,
			sessionID: sessionID,
			userID:    a.userID,
			idleCh:    make(chan struct{}, 1),
		}, nil
	}
}

func (d *a2aWorkerDriver) Send(_ context.Context, message string, bus *workers.EventBus, workerID string) error {
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.TextPart{Text: message})
	msg.ContextID = d.sessionID
	params := &a2a.MessageSendParams{Message: msg}

	select {
	case <-d.idleCh:
	default:
	}

	go func() {
		var assistantText strings.Builder
		// Tool calls/results are published as they appear, but providers
		// differ in WHEN they appear: gemini emits them in incremental
		// events, while the adk-utils anthropic/openai adapters emit them
		// only in the final aggregate (Replace=true) event. We therefore
		// publish from every event and dedupe by call ID so neither path
		// drops nor duplicates a card.
		publishedCalls := make(map[string]bool)
		publishedResults := make(map[string]bool)
		ctxWithUser, callCtx := a2asrv.WithCallContext(context.Background(), nil)
		callCtx.User = &a2asrv.AuthenticatedUser{UserName: d.userID}
		ctx := ctxWithUser

		for ev, err := range d.handler.OnSendMessageStream(ctx, params) {
			if err != nil {
				bus.Publish(workers.WorkerEvent{
					WorkerID: workerID,
					Kind:     workers.EventToolResult,
					Payload:  err.Error(),
				})
				continue
			}

			// We reuse the exact same translator the Root chat uses!
			appEv := eventFromA2A(ev)
			if appEv == nil {
				continue
			}

			publishWorkerStreamEvent(bus, workerID, appEv, publishedCalls, publishedResults, &assistantText)
		}

		d.mu.Lock()
		d.lastReply = strings.TrimSpace(assistantText.String())
		d.mu.Unlock()

		select {
		case d.idleCh <- struct{}{}:
		default:
		}
	}()
	return nil
}

func (d *a2aWorkerDriver) WaitIdle(ctx context.Context) error {
	select {
	case <-d.idleCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *a2aWorkerDriver) Output() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastReply
}

func (d *a2aWorkerDriver) Close() error {
	return nil
}

// publishWorkerStreamEvent translates one app.Event into worker-bus
// events. Tool calls and results are published from ANY event and deduped
// by call ID: the adk-utils anthropic/openai adapters carry them only in
// the final Replace=true aggregate (so dropping that event would hide the
// cards entirely), while gemini carries them incrementally and repeats
// them in the aggregate (so the dedupe stops a double row). Plain
// assistant text is published only from incremental (non-Replace) events,
// since the aggregate repeats text already streamed; errors are always
// surfaced. publishedCalls/publishedResults are the per-turn dedupe sets
// the caller owns across the whole stream.
func publishWorkerStreamEvent(
	bus *workers.EventBus,
	workerID string,
	appEv *facade.Event,
	publishedCalls, publishedResults map[string]bool,
	assistantText *strings.Builder,
) {
	for _, call := range appEv.ToolCalls {
		if call.CallID != "" {
			if publishedCalls[call.CallID] {
				continue
			}
			publishedCalls[call.CallID] = true
		}
		bus.Publish(workers.WorkerEvent{
			WorkerID: workerID,
			Kind:     workers.EventToolCall,
			Payload: workers.ToolCallPayload{
				Name: call.Name,
				ID:   call.CallID,
				Args: call.Args,
			},
		})
	}

	for _, res := range appEv.ToolResults {
		if res.CallID != "" {
			if publishedResults[res.CallID] {
				continue
			}
			publishedResults[res.CallID] = true
		}
		bus.Publish(workers.WorkerEvent{
			WorkerID: workerID,
			Kind:     workers.EventToolResult,
			Payload: workers.ToolResultPayload{
				Name:   res.Name,
				ID:     res.CallID,
				Result: res.Result,
			},
		})
	}

	if appEv.Text != "" {
		if appEv.Role == "error" {
			bus.Publish(workers.WorkerEvent{
				WorkerID: workerID,
				Kind:     workers.EventToolResult, // Treat internal executor errors as tool results in the worker bus
				Payload:  appEv.Text,
			})
		} else if !appEv.Replace {
			assistantText.WriteString(appEv.Text)
			bus.Publish(workers.WorkerEvent{
				WorkerID: workerID,
				Kind:     workers.EventAssistantMessage,
				Payload:  appEv.Text,
			})
		}
	}
}
