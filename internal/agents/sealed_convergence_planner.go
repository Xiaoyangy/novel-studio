package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const (
	sealedConvergenceContinuationMaxTurns    = 4
	sealedConvergenceContinuationMaxRetries  = 1
	sealedConvergenceContinuationMaxErrors   = 2
	sealedConvergenceContinuationTimeout     = 8 * time.Minute
	sealedConvergenceContinuationCallTimeout = 3 * time.Minute
)

const (
	sealedConvergenceContinuationAgentName   = "convergence_planner_continuation"
	sealedConvergenceReplacementAgentName    = "convergence_planner_continuation_replacement"
	sealedConvergenceBinaryFailoverAgentName = "convergence_planner_continuation_binary_failover"
)

// SealedConvergencePlannerRunActivity is attached to terminal sidecar errors
// so the controller can prove that a typed model-call timeout happened before
// any assistant output or tool execution. Session-file inspection remains a
// second, durable check; this observation also catches partial streamed output
// and streamed tool calls which are intentionally not committed to JSONL.
type SealedConvergencePlannerRunActivity struct {
	AssistantMessageStarts int
	ToolMessageStarts      int
	ToolExecutions         int
}

type sealedConvergencePlannerRunError struct {
	cause    error
	activity SealedConvergencePlannerRunActivity
}

func (e *sealedConvergencePlannerRunError) Error() string {
	if e == nil || e.cause == nil {
		return "sealed convergence Planner continuation failed"
	}
	return e.cause.Error()
}

func (e *sealedConvergencePlannerRunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// SealedConvergencePlannerTimedOutBeforeSideEffects accepts only the typed
// per-call timeout and an execution trace with no assistant stream start, tool
// result message, or tool execution. It never classifies errors by text.
func SealedConvergencePlannerTimedOutBeforeSideEffects(err error) bool {
	if !IsModelCallTimeout(err) {
		return false
	}
	var runErr *sealedConvergencePlannerRunError
	if !errors.As(err, &runErr) {
		return false
	}
	a := runErr.activity
	return a.AssistantMessageStarts == 0 && a.ToolMessageStarts == 0 && a.ToolExecutions == 0
}

const sealedConvergenceContinuationBoundary = `

你处于 sealed convergence Planner 的一次性续跑 sidecar，不是普通写作会话：
- drafts/NN.plan.partial.json 是本轮唯一 staged truth；禁止调用或重做 plan_structure。
- Host 已预取新的 planning context。只调用 plan_details，先把当前 access source_token 放入 context_sources，再按 chapter_plan_stage.gap_summary 补最小缺口并 finalize。
- 不得调用 novel_context、simulate_chapter_world、craft_recall、read_chapter，且没有任何正文、正史、游标或提交工具。
- 工具成功返回 planned=true 后立即结束。`

// RunSealedConvergencePlannerContinuation starts one fresh, chapter-scoped
// Planner sidecar. It intentionally does not restore the large failed writer
// transcript: the durable plan partial and the host-prefetched exact context
// are the only inputs. The controller must journal the one allowed dispatch
// before calling this function.
func RunSealedConvergencePlannerContinuation(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
) error {
	return runSealedConvergencePlannerContinuation(
		ctx, cfg, bundle, outputDir, chapter, prompt,
		sealedConvergenceContinuationAgentName,
	)
}

// RunSealedConvergencePlannerContinuationReplacement uses a distinct session
// identity. It is called only after the controller has durably proven the
// original dispatch had zero effects and has persisted replacement dispatch
// 1/1 under a fresh pipeline lock.
func RunSealedConvergencePlannerContinuationReplacement(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
) error {
	return runSealedConvergencePlannerContinuation(
		ctx, cfg, bundle, outputDir, chapter, prompt,
		sealedConvergenceReplacementAgentName,
	)
}

// RunSealedConvergencePlannerContinuationBinaryFailover binds the writer's
// codex-cli provider to the already health-probed absolute binary path and
// writes to a third, distinct one-shot session.
func RunSealedConvergencePlannerContinuationBinaryFailover(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
	binaryPath string,
) error {
	bound, err := bindSealedConvergenceWriterCodexBinary(cfg, binaryPath)
	if err != nil {
		return err
	}
	return runSealedConvergencePlannerContinuation(
		ctx, bound, bundle, outputDir, chapter, prompt,
		sealedConvergenceBinaryFailoverAgentName,
	)
}

func bindSealedConvergenceWriterCodexBinary(cfg bootstrap.Config, binaryPath string) (bootstrap.Config, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return cfg, fmt.Errorf("sealed convergence binary failover path is empty")
	}
	provider := cfg.Provider
	if role, ok := cfg.Roles["writer"]; ok {
		provider = role.Provider
	}
	pc, ok := cfg.Providers[provider]
	if !ok {
		return cfg, fmt.Errorf("sealed convergence binary failover writer provider %q is unavailable", provider)
	}
	providerType, err := pc.ProviderType(provider)
	if err != nil || (providerType != "codex-cli" && providerType != "codex") {
		return cfg, fmt.Errorf("sealed convergence binary failover requires writer codex-cli provider: %w", err)
	}
	providers := make(map[string]bootstrap.ProviderConfig, len(cfg.Providers))
	for name, value := range cfg.Providers {
		providers[name] = value
	}
	pc.BaseURL = binaryPath
	providers[provider] = pc
	cfg.Providers = providers
	return cfg, nil
}

func runSealedConvergencePlannerContinuation(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
	agentName string,
) error {
	if chapter <= 0 || strings.TrimSpace(outputDir) == "" || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("sealed convergence Planner continuation identity is incomplete")
	}
	if !validSealedConvergenceContinuationAgentName(agentName) {
		return fmt.Errorf("sealed convergence Planner continuation session identity is invalid")
	}
	cfg.OutputDir = outputDir
	cfg.DisableModelFailover = true
	cfg.DisableLiveRAG = true
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return fmt.Errorf("sealed convergence Planner continuation model set: %w", err)
	}
	model := models.ForRole("writer")
	provider, name, _ := models.CurrentSelection("writer")
	if model == nil || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("sealed convergence Planner continuation writer model is unavailable")
	}
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		return fmt.Errorf("sealed convergence Planner continuation store: %w", err)
	}
	if err := validateSealedConvergenceContinuationLock(st, chapter); err != nil {
		return err
	}
	return runSealedConvergencePlannerContinuationWithModelAndSession(
		ctx,
		cfg,
		bundle,
		st,
		chapter,
		prompt,
		model,
		provider,
		name,
		tools.NewPlanDetailsTool(st),
		agentName,
	)
}

func runSealedConvergencePlannerContinuationWithModel(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	st *store.Store,
	chapter int,
	prompt string,
	model agentcore.ChatModel,
	provider string,
	modelName string,
	planDetails agentcore.Tool,
) error {
	return runSealedConvergencePlannerContinuationWithModelAndSession(
		ctx, cfg, bundle, st, chapter, prompt, model, provider, modelName,
		planDetails, sealedConvergenceContinuationAgentName,
	)
}

func runSealedConvergencePlannerContinuationWithModelAndSession(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	st *store.Store,
	chapter int,
	prompt string,
	model agentcore.ChatModel,
	provider string,
	modelName string,
	planDetails agentcore.Tool,
	agentName string,
) error {
	if st == nil || model == nil || planDetails == nil || planDetails.Name() != "plan_details" {
		return fmt.Errorf("sealed convergence Planner continuation dependencies are incomplete")
	}
	if !validSealedConvergenceContinuationAgentName(agentName) {
		return fmt.Errorf("sealed convergence Planner continuation session identity is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, sealedConvergenceContinuationTimeout)
	defer cancel()

	logger := st.Sessions.SubAgentLogger(func(string) (string, string) {
		return provider, modelName
	})
	model = withModelCallTimeout(
		model,
		agentName,
		sealedConvergenceContinuationCallTimeout,
	)
	activity := SealedConvergencePlannerRunActivity{}

	var fatalMu sync.Mutex
	var fatalErr error
	failFast := func(
		ctx context.Context,
		call agentcore.ToolCall,
		next agentcore.ToolExecuteFunc,
	) (json.RawMessage, error) {
		result, err := next(ctx, call.Args)
		if err != nil && sealedConvergenceContinuationDeterministicError(err) {
			fatalMu.Lock()
			if fatalErr == nil {
				fatalErr = err
			}
			fatalMu.Unlock()
			cancel()
		}
		return result, err
	}

	events := agentcore.AgentLoop(
		runCtx,
		[]agentcore.AgentMessage{agentcore.UserMsg(prompt)},
		agentcore.AgentContext{
			SystemPrompt: strings.TrimSpace(bundle.Prompts.Planner) + sealedConvergenceContinuationBoundary,
			Tools:        []agentcore.Tool{planDetails},
		},
		agentcore.LoopConfig{
			Model:               model,
			MaxTurns:            cappedMaxTurns(cfg.ResolveMaxTurns("writer", sealedConvergenceContinuationMaxTurns), sealedConvergenceContinuationMaxTurns),
			MaxRetries:          sealedConvergenceContinuationMaxRetries,
			MaxToolErrors:       sealedConvergenceContinuationMaxErrors,
			ThinkingLevel:       resolvedRoleThinking(model, cfg, "writer"),
			ToolsAreIdempotent:  false,
			Middlewares:         []agentcore.ToolMiddleware{failFast},
			StopAfterToolResult: plannerShouldStopAfterToolResult,
			OnMessage: func(msg agentcore.AgentMessage) {
				logger(
					agentName,
					fmt.Sprintf("sealed convergence continuation 第 %d 章", chapter),
					msg,
				)
			},
		},
	)
	var runErr error
	for event := range events {
		if event.Type == agentcore.EventMessageStart && event.Message != nil {
			switch event.Message.GetRole() {
			case agentcore.RoleAssistant:
				activity.AssistantMessageStarts++
			case agentcore.RoleTool:
				activity.ToolMessageStarts++
			}
		}
		if event.Type == agentcore.EventToolExecStart {
			activity.ToolExecutions++
		}
		if event.Type == agentcore.EventError && event.Err != nil {
			runErr = event.Err
		}
	}
	fatalMu.Lock()
	fatal := fatalErr
	fatalMu.Unlock()
	if fatal != nil {
		return &sealedConvergencePlannerRunError{
			cause:    fmt.Errorf("sealed convergence Planner continuation deterministic precondition: %w", fatal),
			activity: activity,
		}
	}
	if runErr != nil {
		return &sealedConvergencePlannerRunError{
			cause:    fmt.Errorf("sealed convergence Planner continuation: %w", runErr),
			activity: activity,
		}
	}
	return nil
}

func validSealedConvergenceContinuationAgentName(agentName string) bool {
	return agentName == sealedConvergenceContinuationAgentName ||
		agentName == sealedConvergenceReplacementAgentName ||
		agentName == sealedConvergenceBinaryFailoverAgentName
}

func validateSealedConvergenceContinuationLock(st *store.Store, chapter int) error {
	if st == nil || chapter <= 0 {
		return fmt.Errorf("sealed convergence Planner continuation lock identity is incomplete")
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return fmt.Errorf("sealed convergence Planner continuation load execution lock: %w", err)
	}
	prefix := fmt.Sprintf("pipeline-convergence-replan-ch%06d-pid%d-", chapter, os.Getpid())
	if lock == nil || lock.Mode != domain.PipelineExecutionProjectAll ||
		lock.TargetChapter != chapter || lock.ProcessID != os.Getpid() ||
		!strings.HasPrefix(strings.TrimSpace(lock.Owner), prefix) ||
		!decimalSuffix(strings.TrimPrefix(strings.TrimSpace(lock.Owner), prefix)) {
		return fmt.Errorf("sealed convergence Planner continuation requires current process exact convergence-replan lock")
	}
	return nil
}

func decimalSuffix(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sealedConvergenceContinuationDeterministicError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	exactStateToken := strings.Contains(lower, "plan_details") &&
		strings.Contains(lower, "project-all-state") &&
		strings.Contains(lower, "source token")
	return exactStateToken ||
		strings.Contains(lower, "project-all planner must call novel_context and submit its exact authoritative context binding") ||
		strings.Contains(lower, "stored project-all authority receipt invalid") ||
		strings.Contains(lower, "sealed convergence authority overlay")
}
