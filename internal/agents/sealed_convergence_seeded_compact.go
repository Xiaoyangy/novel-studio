package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

const (
	sealedConvergenceSeededCompactFinalizeAgentName    = "convergence_planner_seeded_compact_finalize"
	sealedConvergenceExhaustedCompactFinalizeAgentName = "convergence_planner_exhausted_compact_finalize"
	sealedConvergenceSeededCompactMaxTurns             = 2
	sealedConvergenceSeededCompactMaxRetries           = 0
	// One semantic finalize rejection is the only reason this lane has a
	// second turn. agentcore disables a tool when errors reach MaxToolErrors,
	// so the ceiling must be 2 to let that one repair call execute.
	sealedConvergenceSeededCompactMaxToolErrors = 2
	sealedConvergenceSeededCompactTimeout       = 5 * time.Minute
	sealedConvergenceSeededCompactCallTimeout   = 4 * time.Minute
)

// This deliberately does not extend the ordinary Planner prompt. The host has
// already seeded the durable partial and supplied the exact, compact authority
// packet in the user message, so loading the full bundle Planner instructions
// here would only re-open completed planning work and inflate the model call.
const sealedConvergenceSeededCompactFinalizeSystemPrompt = `你是 sealed convergence 的极简收口器，只完成已经 seed 的章节计划 partial。

硬边界：
1. partial 是唯一 staged truth；禁止重做结构、推演、检索或正文。
2. 唯一可用工具是 plan_details。只提交用户消息和工具 schema 明确允许的 mutable patch，然后 finalize=true。
3. context_sources 与其余 seeded fields 已由 Host 固化，禁止提交、覆盖或重写；只补明确列出的 allowed mutable keys。
4. 不得猜测缺失事实，不得调用任何未提供的工具，不得输出计划或正文文本。
5. plan_details 返回 planned=true 后立即结束。`

// SealedConvergencePlannerSeededCompactRunError exposes the exact terminal
// cause together with activity observed before termination. Controllers can
// combine this in-memory trace with the durable JSONL session when deciding
// whether any further recovery is safe; error text alone is never evidence.
type SealedConvergencePlannerSeededCompactRunError struct {
	Cause    error
	Activity SealedConvergencePlannerRunActivity
}

func (e *SealedConvergencePlannerSeededCompactRunError) Error() string {
	if e == nil || e.Cause == nil {
		return "sealed convergence seeded compact finalize failed"
	}
	return e.Cause.Error()
}

func (e *SealedConvergencePlannerSeededCompactRunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// SealedConvergencePlannerSeededCompactActivity extracts the durable-run
// companion trace without exposing implementation-specific loop internals.
func SealedConvergencePlannerSeededCompactActivity(err error) (SealedConvergencePlannerRunActivity, bool) {
	var runErr *SealedConvergencePlannerSeededCompactRunError
	if !errors.As(err, &runErr) || runErr == nil {
		return SealedConvergencePlannerRunActivity{}, false
	}
	return runErr.Activity, true
}

// RunSealedConvergencePlannerSeededCompactFinalize runs the terminal compact
// Planner lane. bundle remains in the signature so the controller can switch
// runners without rebuilding its dispatch inputs; it is intentionally ignored
// and its long Planner prompt is never loaded.
func RunSealedConvergencePlannerSeededCompactFinalize(
	ctx context.Context,
	cfg bootstrap.Config,
	_ assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
	binaryPath string,
	planDetailsTool agentcore.Tool,
) error {
	return runSealedConvergencePlannerCompactFinalize(
		ctx, cfg, outputDir, chapter, prompt, binaryPath, planDetailsTool,
		sealedConvergenceSeededCompactFinalizeAgentName,
	)
}

// RunSealedConvergencePlannerExhaustedCompactFinalize is the distinct final
// sidecar used after an ordinary paid Planner attempt polluted/compacted its
// generic writer transcript. It shares the same hard turn/tool/time bounds as
// the seeded compact lane, but must never append to that earlier audit stream.
func RunSealedConvergencePlannerExhaustedCompactFinalize(
	ctx context.Context,
	cfg bootstrap.Config,
	_ assets.Bundle,
	outputDir string,
	chapter int,
	prompt string,
	binaryPath string,
	planDetailsTool agentcore.Tool,
) error {
	return runSealedConvergencePlannerCompactFinalize(
		ctx, cfg, outputDir, chapter, prompt, binaryPath, planDetailsTool,
		sealedConvergenceExhaustedCompactFinalizeAgentName,
	)
}

func runSealedConvergencePlannerCompactFinalize(
	ctx context.Context,
	cfg bootstrap.Config,
	outputDir string,
	chapter int,
	prompt string,
	binaryPath string,
	planDetailsTool agentcore.Tool,
	agentName string,
) error {
	if chapter <= 0 || strings.TrimSpace(outputDir) == "" || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("sealed convergence seeded compact finalize identity is incomplete")
	}
	if planDetailsTool == nil || planDetailsTool.Name() != "plan_details" {
		return fmt.Errorf("sealed convergence seeded compact finalize requires the single allowlisted plan_details tool")
	}
	if !validSealedConvergencePlannerSessionName(agentName) {
		return fmt.Errorf("sealed convergence seeded compact finalize session identity is invalid")
	}

	bound, err := bindSealedConvergenceSeededCompactWriter(cfg, binaryPath)
	if err != nil {
		return err
	}
	bound.OutputDir = outputDir
	bound.DisableModelFailover = true
	bound.DisableLiveRAG = true

	models, err := bootstrap.NewModelSet(bound)
	if err != nil {
		return fmt.Errorf("sealed convergence seeded compact finalize model set: %w", err)
	}
	model := models.ForRole("writer")
	provider, modelName, _ := models.CurrentSelection("writer")
	if model == nil || strings.TrimSpace(provider) == "" || strings.TrimSpace(modelName) == "" {
		return fmt.Errorf("sealed convergence seeded compact finalize writer model is unavailable")
	}

	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		return fmt.Errorf("sealed convergence seeded compact finalize store: %w", err)
	}
	if err := validateSealedConvergenceContinuationLock(st, chapter); err != nil {
		return err
	}

	return runSealedConvergencePlannerCompactFinalizeWithModel(
		ctx,
		st,
		chapter,
		prompt,
		model,
		provider,
		modelName,
		planDetailsTool,
		agentName,
	)
}

func bindSealedConvergenceSeededCompactWriter(cfg bootstrap.Config, binaryPath string) (bootstrap.Config, error) {
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" || !filepath.IsAbs(binaryPath) {
		return cfg, fmt.Errorf("sealed convergence seeded compact finalize requires an absolute codex-cli path")
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return cfg, fmt.Errorf("sealed convergence seeded compact finalize codex-cli unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return cfg, fmt.Errorf("sealed convergence seeded compact finalize codex-cli is not an executable regular file")
	}
	bound, err := bindSealedConvergenceWriterCodexBinary(cfg, binaryPath)
	if err != nil {
		return cfg, fmt.Errorf("sealed convergence seeded compact finalize bind writer: %w", err)
	}
	return bound, nil
}

func runSealedConvergencePlannerSeededCompactFinalizeWithModel(
	ctx context.Context,
	st *store.Store,
	chapter int,
	prompt string,
	model agentcore.ChatModel,
	provider string,
	modelName string,
	planDetailsTool agentcore.Tool,
) error {
	return runSealedConvergencePlannerCompactFinalizeWithModel(
		ctx, st, chapter, prompt, model, provider, modelName, planDetailsTool,
		sealedConvergenceSeededCompactFinalizeAgentName,
	)
}

func runSealedConvergencePlannerCompactFinalizeWithModel(
	ctx context.Context,
	st *store.Store,
	chapter int,
	prompt string,
	model agentcore.ChatModel,
	provider string,
	modelName string,
	planDetailsTool agentcore.Tool,
	agentName string,
) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(prompt) == "" || model == nil ||
		planDetailsTool == nil || planDetailsTool.Name() != "plan_details" {
		return fmt.Errorf("sealed convergence seeded compact finalize dependencies are incomplete")
	}
	if !validSealedConvergencePlannerSessionName(agentName) {
		return fmt.Errorf("sealed convergence seeded compact finalize session identity is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, sealedConvergenceSeededCompactTimeout)
	defer cancel()

	model = withModelCallTimeout(
		model,
		agentName,
		sealedConvergenceSeededCompactCallTimeout,
	)
	logger := st.Sessions.SubAgentLogger(func(string) (string, string) {
		return provider, modelName
	})
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
			SystemPrompt: sealedConvergenceSeededCompactFinalizeSystemPrompt,
			Tools:        []agentcore.Tool{planDetailsTool},
		},
		agentcore.LoopConfig{
			Model:               model,
			MaxTurns:            sealedConvergenceSeededCompactMaxTurns,
			MaxRetries:          sealedConvergenceSeededCompactMaxRetries,
			MaxToolErrors:       sealedConvergenceSeededCompactMaxToolErrors,
			ThinkingLevel:       agentcore.ThinkingLow,
			ToolsAreIdempotent:  false,
			Middlewares:         []agentcore.ToolMiddleware{failFast},
			StopAfterToolResult: plannerShouldStopAfterToolResult,
			OnMessage: func(msg agentcore.AgentMessage) {
				logger(
					agentName,
					fmt.Sprintf("seeded compact finalize 第 %d 章", chapter),
					msg,
				)
			},
		},
	)

	var loopErr error
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
			loopErr = event.Err
		}
	}

	fatalMu.Lock()
	fatal := fatalErr
	fatalMu.Unlock()
	if fatal != nil {
		return &SealedConvergencePlannerSeededCompactRunError{
			Cause:    fmt.Errorf("sealed convergence seeded compact finalize deterministic precondition: %w", fatal),
			Activity: activity,
		}
	}
	if loopErr != nil {
		return &SealedConvergencePlannerSeededCompactRunError{
			Cause:    fmt.Errorf("sealed convergence seeded compact finalize: %w", loopErr),
			Activity: activity,
		}
	}
	return nil
}

// validSealedConvergencePlannerSessionName is the complete Planner sidecar
// session-name validator. Existing continuation identities retain their exact
// validator; the seeded compact lane adds one fixed, non-chapter-suffixed agent
// name whose logger still appends the chapter suffix to the JSONL filename.
func validSealedConvergencePlannerSessionName(agentName string) bool {
	return validSealedConvergenceContinuationAgentName(agentName) ||
		agentName == sealedConvergenceSeededCompactFinalizeAgentName ||
		agentName == sealedConvergenceExhaustedCompactFinalizeAgentName
}
