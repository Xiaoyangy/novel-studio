package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const outlineAllOperationSystemBoundary = `你是 outline-all 的 Architect 主模型执行器。
宿主已经把本次冻结 operation 的完整任务与全部可见上下文直接交给你；不得转派、改派或请求其他代理。
你唯一拥有的工具是 save_foundation。严格执行任务中的 OUTLINE_ALL_INTENT，只完成一次回执授权的结构变更。
不得生成正文，不得调用动态检索，不得修改任务未授权的任何设定。save_foundation 明确返回 saved=true 且 outline_all=true 后立即结束。`

// OutlineAllOperationProtocolDigest binds the direct Architect system
// boundary to outline-all's generation identity without exposing prompt text
// in receipts. Coordinator prompts are intentionally not part of this root.
func OutlineAllOperationProtocolDigest(architectLongPrompt string) (string, error) {
	return domain.DeterministicPlanningHash(struct {
		Version           string `json:"version"`
		ArchitectLong     string `json:"architect_long"`
		OperationBoundary string `json:"operation_boundary"`
	}{
		Version:           "outline-all-direct-architect.v1",
		ArchitectLong:     architectLongPrompt,
		OperationBoundary: outlineAllOperationSystemBoundary,
	})
}

type outlineAllOperationModel struct {
	ChatModel agentcore.ChatModel
	Provider  string
	Name      string
}

// RunOutlineAllOperation runs one frozen outline-all operation directly on
// the configured Architect primary model. It does not construct a
// Coordinator, does not expose delegation/research/prose capabilities, and
// never resolves configured fallback targets.
func RunOutlineAllOperation(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	candidateOutputDir string,
	prompt string,
) error {
	candidateOutputDir = strings.TrimSpace(candidateOutputDir)
	prompt = strings.TrimSpace(prompt)
	if candidateOutputDir == "" || prompt == "" {
		return fmt.Errorf("outline-all direct Architect requires candidate output dir and operation prompt")
	}
	absOutputDir, err := filepath.Abs(candidateOutputDir)
	if err != nil {
		return fmt.Errorf("outline-all direct Architect candidate path: %w", err)
	}
	cfg.OutputDir = absOutputDir
	cfg.DisableModelFailover = true
	cfg.DisableLiveRAG = true

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return fmt.Errorf("outline-all direct Architect model set: %w", err)
	}
	model := models.ForRole("architect") // primary only: never ForRoleWithFailover
	provider, name, _ := models.CurrentSelection("architect")
	if model == nil || strings.TrimSpace(provider) == "" || strings.TrimSpace(name) == "" {
		return fmt.Errorf("outline-all direct Architect primary model is unavailable")
	}

	st := store.NewStore(absOutputDir)
	if err := st.Init(); err != nil {
		return fmt.Errorf("outline-all direct Architect candidate init: %w", err)
	}
	return runOutlineAllOperationWithModel(ctx, cfg, bundle, st, prompt, outlineAllOperationModel{
		ChatModel: model,
		Provider:  provider,
		Name:      name,
	}, tools.NewSaveFoundationTool(st))
}

func runOutlineAllOperationWithModel(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	st *store.Store,
	prompt string,
	resolved outlineAllOperationModel,
	saveFoundation agentcore.Tool,
) error {
	if st == nil || resolved.ChatModel == nil || saveFoundation == nil {
		return fmt.Errorf("outline-all direct Architect dependencies are incomplete")
	}
	if saveFoundation.Name() != "save_foundation" {
		return fmt.Errorf("outline-all direct Architect rejects capability %q", saveFoundation.Name())
	}
	if strings.TrimSpace(resolved.Provider) == "" || strings.TrimSpace(resolved.Name) == "" {
		return fmt.Errorf("outline-all direct Architect identity is incomplete")
	}

	systemPrompt := strings.TrimSpace(bundle.Prompts.ArchitectLong) + "\n\n" + outlineAllOperationSystemBoundary
	finalAuthorization, err := outlineAllFinalAuthorization(prompt)
	if err != nil {
		return err
	}
	logger := st.Sessions.SubAgentLogger(func(string) (string, string) {
		return resolved.Provider, resolved.Name
	})

	var mutationMu sync.Mutex
	mutationComplete := false
	stopAfterSuccessfulSave := func(toolName string, result json.RawMessage) bool {
		if toolName != "save_foundation" || !successfulOutlineAllSave(result) {
			return false
		}
		mutationMu.Lock()
		mutationComplete = true
		mutationMu.Unlock()
		return true
	}
	preventSecondMutation := func(
		ctx context.Context,
		call agentcore.ToolCall,
		next agentcore.ToolExecuteFunc,
	) (json.RawMessage, error) {
		mutationMu.Lock()
		alreadyComplete := mutationComplete
		mutationMu.Unlock()
		if alreadyComplete {
			return nil, fmt.Errorf("outline-all direct Architect already completed its one authorized save_foundation mutation")
		}
		result, err := next(ctx, call.Args)
		if err == nil && successfulOutlineAllSave(result) {
			mutationMu.Lock()
			mutationComplete = true
			mutationMu.Unlock()
		}
		return result, err
	}

	events := agentcore.AgentLoop(
		ctx,
		[]agentcore.AgentMessage{
			// Keep the persisted operation prompt byte-for-byte intact. The final
			// message is derived only from its host-issued intent marker, so a
			// large historical context cannot become the model's effective target.
			agentcore.UserMsg(prompt),
			agentcore.UserMsg(finalAuthorization),
		},
		agentcore.AgentContext{
			SystemPrompt: systemPrompt,
			Tools:        []agentcore.Tool{saveFoundation},
		},
		agentcore.LoopConfig{
			Model:               resolved.ChatModel,
			MaxTurns:            cappedMaxTurns(cfg.ResolveMaxTurns("architect", 20), 20),
			MaxRetries:          subagentMaxRetries,
			MaxToolErrors:       0,
			ThinkingLevel:       resolvedRoleThinking(resolved.ChatModel, cfg, "architect"),
			ToolsAreIdempotent:  false,
			Middlewares:         []agentcore.ToolMiddleware{preventSecondMutation},
			StopAfterToolResult: stopAfterSuccessfulSave,
			OnMessage: func(msg agentcore.AgentMessage) {
				logger("architect_outline_all", prompt, msg)
			},
		},
	)
	var runErr error
	for event := range events {
		if event.Type == agentcore.EventError && event.Err != nil {
			runErr = event.Err
		}
	}
	if runErr != nil {
		return fmt.Errorf("outline-all direct Architect run: %w", runErr)
	}
	mutationMu.Lock()
	completed := mutationComplete
	mutationMu.Unlock()
	if !completed {
		return fmt.Errorf("outline-all direct Architect returned without a successful outline-all save_foundation mutation")
	}
	return nil
}

func outlineAllFinalAuthorization(prompt string) (string, error) {
	action, err := domain.ParseOutlineAllIntent(prompt)
	if err != nil {
		return "", fmt.Errorf("outline-all direct Architect final authorization: %w", err)
	}

	var summary, target string
	switch action.Type {
	case domain.OutlineAllActionAppendVolume:
		summary = fmt.Sprintf(
			"operation=%d type=%s volume=%d expected_chapter_span=%d expected_arc_spans=%s final_skeleton=%t",
			action.Operation,
			action.Type,
			action.Volume,
			action.ExpectedChapterSpan,
			action.ExpectedArcSpans,
			action.FinalSkeleton,
		)
		target = fmt.Sprintf(
			"下一步且唯一写操作：save_foundation(type=%q, volume=%d, content=<恰好预留%d章、弧跨度严格为[%s]的VolumeOutline>)。不得提供arc。",
			action.Type,
			action.Volume,
			action.ExpectedChapterSpan,
			action.ExpectedArcSpans,
		)
	case domain.OutlineAllActionMapContracts:
		summary = fmt.Sprintf(
			"operation=%d type=%s expected_chapter_span=%d",
			action.Operation,
			action.Type,
			action.ExpectedChapterSpan,
		)
		target = fmt.Sprintf(
			"下一步且唯一写操作：save_foundation(type=%q, content=<覆盖冻结全书%d章的ArcContractAssignment数组>)。不得提供volume或arc。",
			action.Type,
			action.ExpectedChapterSpan,
		)
	case domain.OutlineAllActionExpandArc, domain.OutlineAllActionReviseArc:
		summary = fmt.Sprintf(
			"operation=%d type=%s volume=%d arc=%d expected_chapter_span=%d",
			action.Operation,
			action.Type,
			action.Volume,
			action.Arc,
			action.ExpectedChapterSpan,
		)
		target = fmt.Sprintf(
			"下一步且唯一写操作：save_foundation(type=%q, volume=%d, arc=%d, content=<恰好%d个OutlineEntry>)。",
			action.Type,
			action.Volume,
			action.Arc,
			action.ExpectedChapterSpan,
		)
	default:
		// ParseOutlineAllIntent validates the action type. Keep this defensive
		// branch fail-closed if validation and formatting ever drift apart.
		return "", fmt.Errorf("outline-all direct Architect final authorization rejects action type %q", action.Type)
	}

	return fmt.Sprintf(
		"[FINAL AUTHORIZED ACTION / HOST ENFORCED]\n"+
			"前文 MODEL_VISIBLE_CONTEXT 中出现的其他卷、弧和历史 operation 全部只读，绝不是可选目标。\n"+
			"%s\n"+
			"%s\n"+
			"任何其他 type、volume、arc 或 content 数量都无效；不得修改前文中的其他弧。工具返回 saved=true 后立即结束。",
		summary,
		target,
	), nil
}

func successfulOutlineAllSave(result json.RawMessage) bool {
	var decoded struct {
		Saved      bool   `json:"saved"`
		OutlineAll bool   `json:"outline_all"`
		Type       string `json:"type"`
	}
	if json.Unmarshal(result, &decoded) != nil || !decoded.Saved || !decoded.OutlineAll {
		return false
	}
	switch decoded.Type {
	case "append_volume", "map_contracts", "expand_arc", "revise_arc":
		return true
	default:
		return false
	}
}
