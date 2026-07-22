package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host/reminder"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// ProjectedChapterArtifacts are full inference artifacts produced in an
// isolated project-all workspace. Callers package them into a non-canonical v2
// bundle; this runner never writes to the live novel output.
type ProjectedChapterArtifacts struct {
	WorldSimulation       *domain.ChapterWorldSimulation
	Plan                  *domain.ChapterPlan
	PlanCheckpoint        *domain.Checkpoint
	RAGFactReceipt        *domain.RAGFactReceipt
	CraftRecallReceipt    *domain.CraftRecallReceipt
	PlanningContextDigest string
	RenderContext         json.RawMessage
}

// ProjectedArcBoundary makes the host-selected arc transaction visible to the
// Simulator and Planner. The full outline remains available as future
// orientation, but neither agent may formally plan a chapter outside this
// boundary during the current generation.
type ProjectedArcBoundary struct {
	Volume          int
	Arc             int
	Title           string
	Goal            string
	FirstChapter    int
	LastChapter     int
	BookLastChapter int
}

// A simulator turn is intentionally bounded, but every successful tool call
// durably stages a partial. A large cast plus strict authority contracts can
// legitimately consume one agent's turn budget before the final two actors or
// projection fields are submitted. Project-Arc resumes that partial in a fresh
// context instead of making the operator rerun the whole pipeline. Total
// sessions stay bounded, while three consecutive sessions without a durable
// partial advance fail closed instead of penalizing sessions that did progress.
const (
	projectAllWorldSimulationPassLimit           = 8
	projectAllWorldSimulationStagnantPassLimit   = 3
	projectAllWorldSimulationInitialTurnCeiling  = 12
	projectAllWorldSimulationRecoveryTurnCeiling = 8
	projectAllWorldSimulationToolErrorLimit      = 3
	projectAllWorldSimulationToolErrorRuneLimit  = 480
)

const projectAllAuthorityPrefillBatchLimit = 8

type projectAllWorldSimulationExecutor interface {
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

type projectAllAuthorityPrefillResult struct {
	Characters    []string
	Batches       [][]string
	RemainingGaps []string
}

type projectAllAuthorityContextEntry struct {
	Character        string `json:"character"`
	SimulationStatus string `json:"simulation_status"`
	Blocking         bool   `json:"blocking"`
}

// prefillProjectedWorldSimulationAuthority moves server-authored blocking
// contracts into the durable partial before the model session. It never
// authors a grounded decision or projection and never finalizes a simulation.
func prefillProjectedWorldSimulationAuthority(
	ctx context.Context,
	contextTool projectAllWorldSimulationExecutor,
	simulationTool projectAllWorldSimulationExecutor,
	chapter int,
	remainingGaps func() []string,
) (projectAllAuthorityPrefillResult, error) {
	var result projectAllAuthorityPrefillResult
	if contextTool == nil || simulationTool == nil || chapter <= 0 {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := projectAllWorldSimulationContextError(ctx, chapter, "before authority prefill context"); err != nil {
		return result, err
	}
	contextArgs, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"profile": "world_simulation",
	})
	if err != nil {
		return result, err
	}
	contextRaw, err := contextTool.Execute(ctx, contextArgs)
	if err != nil {
		return result, fmt.Errorf("read world-simulation authority prefill context: %w", err)
	}
	if err := projectAllWorldSimulationContextError(ctx, chapter, "after authority prefill context"); err != nil {
		return result, err
	}
	characters, accessToken, projectAllToken, err := projectedWorldSimulationAuthorityPrefillInputs(contextRaw)
	if err != nil {
		return result, err
	}
	result.Characters = append([]string(nil), characters...)
	if len(characters) > 0 {
		if accessToken == "" {
			return result, fmt.Errorf("world-simulation authority prefill context did not issue an access source token")
		}
		if projectAllToken == "" {
			return result, fmt.Errorf("world-simulation authority prefill context did not expose the project-all state source token")
		}
	}
	for start := 0; start < len(characters); start += projectAllAuthorityPrefillBatchLimit {
		if err := projectAllWorldSimulationContextError(ctx, chapter, "before authority prefill write"); err != nil {
			return result, err
		}
		end := min(start+projectAllAuthorityPrefillBatchLimit, len(characters))
		batch := append([]string(nil), characters[start:end]...)
		args := map[string]any{
			"chapter":                       chapter,
			"authority_contract_characters": batch,
		}
		if start == 0 {
			args["sources"] = []string{projectAllToken, accessToken}
		}
		raw, marshalErr := json.Marshal(args)
		if marshalErr != nil {
			return result, marshalErr
		}
		responseRaw, executeErr := simulationTool.Execute(ctx, raw)
		if executeErr != nil {
			return result, fmt.Errorf("prefill world-simulation authority batch %d: %w", len(result.Batches)+1, executeErr)
		}
		result.Batches = append(result.Batches, batch)
		var response struct {
			Gaps []string `json:"gaps"`
		}
		if err := json.Unmarshal(responseRaw, &response); err != nil {
			return result, fmt.Errorf("decode world-simulation authority prefill batch %d: %w", len(result.Batches), err)
		}
		result.RemainingGaps = append([]string(nil), response.Gaps...)
		if err := projectAllWorldSimulationContextError(ctx, chapter, "after authority prefill write"); err != nil {
			return result, err
		}
	}
	if remainingGaps != nil {
		result.RemainingGaps = append([]string(nil), remainingGaps()...)
	}
	return result, nil
}

func projectedWorldSimulationAuthorityPrefillInputs(
	raw json.RawMessage,
) (characters []string, accessToken string, projectAllToken string, err error) {
	var envelope struct {
		Access struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
		ProjectAllToken string          `json:"project_all_state_source_token"`
		Authority       json.RawMessage `json:"simulation_character_authority"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, "", "", fmt.Errorf("decode world-simulation authority prefill context: %w", err)
	}
	accessToken = strings.TrimSpace(envelope.Access.SourceToken)
	projectAllToken = strings.TrimSpace(envelope.ProjectAllToken)
	authorityRaw := json.RawMessage(strings.TrimSpace(string(envelope.Authority)))
	if len(authorityRaw) == 0 || string(authorityRaw) == "null" {
		return nil, accessToken, projectAllToken, nil
	}
	var entries []projectAllAuthorityContextEntry
	if authorityRaw[0] == '[' {
		if err := json.Unmarshal(authorityRaw, &entries); err != nil {
			return nil, "", "", fmt.Errorf("decode world-simulation authority entries: %w", err)
		}
	} else {
		var layered struct {
			Entries []projectAllAuthorityContextEntry `json:"entries"`
		}
		if err := json.Unmarshal(authorityRaw, &layered); err != nil {
			return nil, "", "", fmt.Errorf("decode layered world-simulation authority entries: %w", err)
		}
		entries = layered.Entries
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Character)
		if !entry.Blocking || name == "" ||
			strings.TrimSpace(entry.SimulationStatus) == "already_present" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		characters = append(characters, name)
	}
	return characters, accessToken, projectAllToken, nil
}

func projectAllWorldSimulationPrompt(
	arcBoundary ProjectedArcBoundary,
	chapter int,
	pass int,
	gaps []string,
	recentToolErrors []string,
) string {
	gapSummary := "missing chapter world simulation"
	if compact := compactProjectAllPromptGaps(gaps); len(compact) > 0 {
		gapSummary = strings.Join(compact, "；")
	}
	prompt := fmt.Sprintf(
		"Project-Arc 正在隔离工作区推演 V%dA%d《%s》（第%d-%d章），本弧目标：%s。当前只处理第 %d 章，这是本次进程第 %d/%d 个有界 world-simulation 会话。先调用 novel_context(chapter=%d, profile=world_simulation)，并把本轮 planning_context_access_receipt.source_token 放入随后第一次 simulate_chapter_world.sources；若已有 partial，只补 durable gaps，禁止重发 locked/already_present 角色。当前缺口：%s。随后只调用 simulate_chapter_world，每批在不超过8名的前提下尽量填满；严格按每个 authority entry 分流：只有 blocking=true 才放入 authority_contract_characters，project_all_grounded/blocking=false 必须放入 character_decisions 并严格执行 grounded DecisionPolicy。project_all_grounded 只由服务端绑定主角 chosen_decision，模型必须自行补齐决定当下仍真正可用的 available_options、可见证据支持的 decision_reason、可见影响、隐藏压力、计划边界与因果链，不得把已失败或已过期动作当作当前选项。gaps 清零后立即单独 finalize；若本轮只需 finalize，也必须提交本轮唯一 access token，禁止沿用旧 sources。不得写正文、不得修改设定或进度、不得处理其他弧。若本章是本弧末章，必须闭合本弧内部因果并把跨弧影响明确标为 carried-forward；只有第%d章才是全书末章。",
		arcBoundary.Volume,
		arcBoundary.Arc,
		arcBoundary.Title,
		arcBoundary.FirstChapter,
		arcBoundary.LastChapter,
		arcBoundary.Goal,
		chapter,
		pass,
		projectAllWorldSimulationPassLimit,
		chapter,
		gapSummary,
		arcBoundary.BookLastChapter,
	)
	if toolErrors := compactProjectAllSimulationToolErrors(recentToolErrors); len(toolErrors) > 0 {
		prompt += " 上一有界会话最近的 simulate_chapter_world 工具错误（按顺序逐项修正，保留已落盘 partial，禁止再次提交同一失败参数）：" +
			strings.Join(toolErrors, "；") + "。"
	}
	return prompt
}

func compactProjectAllPromptGaps(gaps []string) []string {
	const maxGaps = 12
	compact := make([]string, 0, min(len(gaps), maxGaps))
	for _, gap := range gaps {
		gap = strings.TrimSpace(gap)
		if gap == "" {
			continue
		}
		compact = append(compact, gap)
		if len(compact) == maxGaps {
			break
		}
	}
	if len(gaps) > len(compact) {
		compact = append(compact, fmt.Sprintf("另有%d项，以 novel_context 返回为准", len(gaps)-len(compact)))
	}
	return compact
}

func projectAllWorldSimulationStartsFresh(gaps []string) bool {
	return len(gaps) == 1 && strings.TrimSpace(gaps[0]) == "missing chapter world simulation"
}

func projectAllWorldSimulationTurnCeiling(pass int, startsFresh bool) int {
	if pass == 1 && startsFresh {
		// One context read, up to two full actor batches, projection/finalize,
		// and several correction turns fit comfortably without keeping a stale
		// failing conversation alive for the previous 20-turn ceiling.
		return projectAllWorldSimulationInitialTurnCeiling
	}
	return projectAllWorldSimulationRecoveryTurnCeiling
}

type projectAllWorldSimulationProgress struct {
	CharacterDecisions int
	RewriteCoverage    int
	ProjectionFields   int
	HasTimeWindow      bool
	GapCount           int
	ContentDigest      string
}

func loadProjectAllWorldSimulationProgress(
	st *store.Store,
	chapter int,
) (projectAllWorldSimulationProgress, error) {
	var result projectAllWorldSimulationProgress
	if st == nil || chapter <= 0 {
		return result, nil
	}
	_, _, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
	result.GapCount = len(gaps)
	partial, err := st.LoadChapterWorldSimulationPartial(chapter)
	if err != nil || partial == nil {
		return result, err
	}
	result.CharacterDecisions = len(partial.CharacterDecisions)
	result.RewriteCoverage = len(partial.RewriteFactCoverage)
	result.ProjectionFields = projectAllProjectionProgressFields(
		partial.ProtagonistProjection,
	)
	result.HasTimeWindow = strings.TrimSpace(partial.TimeWindow) != ""
	if result.CharacterDecisions > 0 || result.RewriteCoverage > 0 ||
		result.ProjectionFields > 0 || result.HasTimeWindow {
		durable, marshalErr := json.Marshal(struct {
			TimeWindow            string                               `json:"time_window"`
			CharacterDecisions    []domain.CharacterWorldDecision      `json:"character_decisions"`
			ProtagonistProjection domain.ProtagonistDecisionProjection `json:"protagonist_projection"`
			RewriteFactCoverage   []domain.ChapterRewriteFactCoverage  `json:"rewrite_fact_coverage"`
		}{
			TimeWindow:            partial.TimeWindow,
			CharacterDecisions:    partial.CharacterDecisions,
			ProtagonistProjection: partial.ProtagonistProjection,
			RewriteFactCoverage:   partial.RewriteFactCoverage,
		})
		if marshalErr != nil {
			return result, marshalErr
		}
		result.ContentDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(durable))
	}
	return result, nil
}

func projectAllProjectionProgressFields(
	projection domain.ProtagonistDecisionProjection,
) int {
	fields := 0
	if strings.TrimSpace(projection.Protagonist) != "" {
		fields++
	}
	if len(projection.ObservableEffects) > 0 {
		fields++
	}
	if len(projection.HiddenPressures) > 0 {
		fields++
	}
	if len(projection.AvailableOptions) > 0 {
		fields++
	}
	if strings.TrimSpace(projection.ChosenDecision) != "" {
		fields++
	}
	if strings.TrimSpace(projection.DecisionReason) != "" {
		fields++
	}
	if len(projection.PlanConstraints) > 0 {
		fields++
	}
	if len(projection.CausalChain) > 0 {
		fields++
	}
	return fields
}

func (current projectAllWorldSimulationProgress) advancedFrom(
	previous projectAllWorldSimulationProgress,
) bool {
	shapeAdvanced := current.CharacterDecisions > previous.CharacterDecisions ||
		current.RewriteCoverage > previous.RewriteCoverage ||
		current.ProjectionFields > previous.ProjectionFields ||
		(current.HasTimeWindow && !previous.HasTimeWindow)
	gapsReduced := previous.GapCount > 0 && current.GapCount < previous.GapCount
	contentCorrected := current.ContentDigest != "" &&
		current.ContentDigest != previous.ContentDigest &&
		current.GapCount <= previous.GapCount
	return shapeAdvanced || gapsReduced || contentCorrected
}

func recentProjectAllSimulationToolErrors(messages []agentcore.AgentMessage) []string {
	var errorsNewestFirst []string
	for i := len(messages) - 1; i >= 0; i-- {
		msg, ok := messages[i].(agentcore.Message)
		if !ok || msg.Role != agentcore.RoleTool || msg.Metadata == nil {
			continue
		}
		isError, _ := msg.Metadata["is_error"].(bool)
		toolName, _ := msg.Metadata["tool_name"].(string)
		if !isError || strings.TrimSpace(toolName) != "simulate_chapter_world" {
			continue
		}
		errorsNewestFirst = append(errorsNewestFirst, msg.TextContent())
	}
	return compactProjectAllSimulationToolErrors(errorsNewestFirst)
}

func compactProjectAllSimulationToolErrors(toolErrors []string) []string {
	compact := make([]string, 0, min(len(toolErrors), projectAllWorldSimulationToolErrorLimit))
	seen := make(map[string]struct{}, len(toolErrors))
	for _, raw := range toolErrors {
		raw = strings.TrimSpace(raw)
		var decoded string
		if raw != "" && json.Unmarshal([]byte(raw), &decoded) == nil {
			raw = decoded
		}
		raw = strings.Join(strings.Fields(raw), " ")
		if raw == "" {
			continue
		}
		runes := []rune(raw)
		if len(runes) > projectAllWorldSimulationToolErrorRuneLimit {
			raw = string(runes[:projectAllWorldSimulationToolErrorRuneLimit]) + "…"
		}
		if _, duplicate := seen[raw]; duplicate {
			continue
		}
		seen[raw] = struct{}{}
		compact = append(compact, raw)
		if len(compact) == projectAllWorldSimulationToolErrorLimit {
			break
		}
	}
	return compact
}

func projectAllWorldSimulationContextError(ctx context.Context, chapter int, stage string) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf(
			"project-all world simulation chapter %d canceled %s: %w",
			chapter,
			stage,
			err,
		)
	}
	return nil
}

// finalizeReadyProjectedWorldSimulation closes a gap-free partial without
// spending another model turn. It deliberately refreshes novel_context first:
// a planning-context access token is process/lease bound and a restarted
// Project-Arc process must not reuse the opaque token staged by an older PID.
func finalizeReadyProjectedWorldSimulation(
	ctx context.Context,
	contextTool *tools.ContextTool,
	simulationTool *tools.SimulateChapterWorldTool,
	st *store.Store,
	chapter int,
) (bool, error) {
	if contextTool == nil || simulationTool == nil || st == nil || chapter <= 0 {
		return false, nil
	}
	if err := projectAllWorldSimulationContextError(ctx, chapter, "before host finalize"); err != nil {
		return false, err
	}
	if partial, err := st.LoadChapterWorldSimulationPartial(chapter); err != nil {
		return false, err
	} else if partial == nil {
		return false, nil
	}
	_, _, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
	if len(gaps) > 0 {
		return false, nil
	}
	contextArgs, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"profile": "world_simulation",
	})
	if err != nil {
		return false, err
	}
	contextRaw, err := contextTool.Execute(ctx, contextArgs)
	if err != nil {
		return false, fmt.Errorf("refresh ready world-simulation context receipt: %w", err)
	}
	if err := projectAllWorldSimulationContextError(ctx, chapter, "after context receipt refresh"); err != nil {
		return false, err
	}
	var contextResult struct {
		Access struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
	}
	if err := json.Unmarshal(contextRaw, &contextResult); err != nil {
		return false, fmt.Errorf("decode ready world-simulation context receipt: %w", err)
	}
	sourceToken := strings.TrimSpace(contextResult.Access.SourceToken)
	if sourceToken == "" {
		return false, fmt.Errorf("ready world-simulation context did not issue an access source token")
	}
	finalizeArgs, err := json.Marshal(map[string]any{
		"chapter":  chapter,
		"sources":  []string{sourceToken},
		"finalize": true,
	})
	if err != nil {
		return false, err
	}
	if err := projectAllWorldSimulationContextError(ctx, chapter, "before formal simulation write"); err != nil {
		return false, err
	}
	finalizeRaw, err := simulationTool.Execute(ctx, finalizeArgs)
	if err != nil {
		return false, fmt.Errorf("host finalize ready world simulation: %w", err)
	}
	var result struct {
		Simulated bool `json:"simulated"`
	}
	if err := json.Unmarshal(finalizeRaw, &result); err != nil {
		return false, fmt.Errorf("decode host world-simulation finalize: %w", err)
	}
	if !result.Simulated {
		return false, fmt.Errorf("host world-simulation finalize returned simulated=false")
	}
	return true, nil
}

func (b ProjectedArcBoundary) validate(chapter int) error {
	if b.Volume <= 0 || b.Arc <= 0 || strings.TrimSpace(b.Title) == "" ||
		strings.TrimSpace(b.Goal) == "" || b.FirstChapter <= 0 ||
		b.LastChapter < b.FirstChapter || b.BookLastChapter < b.LastChapter {
		return fmt.Errorf("project-arc boundary is incomplete")
	}
	if chapter < b.FirstChapter || chapter > b.LastChapter {
		return fmt.Errorf("chapter %d is outside V%dA%d range %d..%d", chapter, b.Volume, b.Arc, b.FirstChapter, b.LastChapter)
	}
	return nil
}

// RunProjectedChapterPlanning reuses the production World Simulator and
// Planner validators inside an isolated output directory. It deliberately
// avoids Coordinator, prose tools, web research, live Qdrant, and configured
// fallbacks: project-all is a planning-only pass, and the selected writer model
// must remain the model the user configured.
func RunProjectedChapterPlanning(
	ctx context.Context,
	cfg bootstrap.Config,
	bundle assets.Bundle,
	isolatedOutputDir string,
	chapter int,
	planningContextDigest string,
	arcBoundary ProjectedArcBoundary,
) (_ *ProjectedChapterArtifacts, returnErr error) {
	contextToken, err := domain.ProjectedPlanningContextSourceTokenV2(planningContextDigest)
	if err != nil {
		return nil, fmt.Errorf("project-all planning context digest: %w", err)
	}
	if chapter <= 0 || strings.TrimSpace(isolatedOutputDir) == "" {
		return nil, fmt.Errorf("project-all planning requires output dir, chapter and context digest")
	}
	if err := arcBoundary.validate(chapter); err != nil {
		return nil, err
	}
	cfg.OutputDir = isolatedOutputDir
	cfg.DisableLiveRAG = true
	st := store.NewStore(isolatedOutputDir)
	if err := st.Init(); err != nil {
		return nil, fmt.Errorf("init project-all workspace: %w", err)
	}

	owner := fmt.Sprintf("project-all-ch%06d", chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: chapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(6 * time.Hour),
	}); err != nil {
		return nil, fmt.Errorf("acquire project-all execution lock: %w", err)
	}
	defer func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("release project-all execution lock: %w", err)
		}
	}()

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("create project-all models: %w", err)
	}
	model := models.ForRole("writer")
	if model == nil {
		return nil, fmt.Errorf("project-all writer model is unavailable")
	}
	resolvedStyle := bundle.ResolveStyle(cfg.Style)
	contextTool := tools.NewContextTool(st, bundle.References, resolvedStyle.ID).
		WithConfiguredStyle(resolvedStyle.Body)
	// Project-all never queries live Qdrant, but it may use the immutable local
	// vector_store copied into this generation workspace. This keeps semantic
	// RAG in the planning phase while render remains receipt-only.
	if embedder, enabled, embedErr := bootstrap.NewRAGEmbedder(cfg); embedErr != nil {
		return nil, fmt.Errorf("project-all snapshot embedding model: %w", embedErr)
	} else if enabled {
		contextTool.WithRAGEmbedder(embedder)
	}

	simulation, simulationCP, simulationErr := loadCurrentProjectedSimulation(st, chapter)
	if simulationErr != nil {
		return nil, simulationErr
	}
	if simulation == nil {
		var lastSimulatorError string
		var lastHostFinalizeError string
		var recentSimulatorToolErrors []string
		sessionsUsed := 0
		stagnantSessions := 0
		for pass := 1; pass <= projectAllWorldSimulationPassLimit &&
			simulation == nil &&
			stagnantSessions < projectAllWorldSimulationStagnantPassLimit; pass++ {
			sessionsUsed = pass
			if err := projectAllWorldSimulationContextError(ctx, chapter, "before recovery session"); err != nil {
				return nil, err
			}
			_, _, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
			startsFresh := projectAllWorldSimulationStartsFresh(gaps)
			simulationTool := tools.NewSimulateChapterWorldTool(st)
			if len(gaps) == 0 {
				finalized, finalizeErr := finalizeReadyProjectedWorldSimulation(
					ctx,
					contextTool,
					simulationTool,
					st,
					chapter,
				)
				if finalizeErr != nil {
					if err := projectAllWorldSimulationContextError(ctx, chapter, "during host finalize"); err != nil {
						return nil, err
					}
					lastHostFinalizeError = finalizeErr.Error()
				} else if finalized {
					simulation, simulationCP, err = loadCurrentProjectedSimulation(st, chapter)
					if err != nil {
						return nil, err
					}
					if simulation != nil && simulationCP != nil {
						break
					}
				}
			}
			if len(gaps) > 0 {
				prefill, prefillErr := prefillProjectedWorldSimulationAuthority(
					ctx,
					contextTool,
					simulationTool,
					chapter,
					func() []string {
						_, _, remaining := tools.ChapterWorldSimulationStatus(st, chapter)
						return remaining
					},
				)
				if prefillErr != nil {
					return nil, fmt.Errorf(
						"project-all world simulation chapter %d authority prefill: %w",
						chapter,
						prefillErr,
					)
				}
				gaps = prefill.RemainingGaps
				if len(gaps) == 0 {
					finalized, finalizeErr := finalizeReadyProjectedWorldSimulation(
						ctx,
						contextTool,
						simulationTool,
						st,
						chapter,
					)
					if finalizeErr != nil {
						if err := projectAllWorldSimulationContextError(ctx, chapter, "during host finalize after authority prefill"); err != nil {
							return nil, err
						}
						lastHostFinalizeError = finalizeErr.Error()
					} else if finalized {
						simulation, simulationCP, err = loadCurrentProjectedSimulation(st, chapter)
						if err != nil {
							return nil, err
						}
						if simulation != nil && simulationCP != nil {
							break
						}
					}
				}
			}
			progressBefore, progressErr := loadProjectAllWorldSimulationProgress(st, chapter)
			if progressErr != nil {
				return nil, fmt.Errorf(
					"project-all world simulation chapter %d read progress before pass %d: %w",
					chapter,
					pass,
					progressErr,
				)
			}
			turnCeiling := projectAllWorldSimulationTurnCeiling(pass, startsFresh)
			simulator := agentcore.NewAgent(
				agentcore.WithModel(model),
				agentcore.WithSystemPrompt(worldSimulatorSystemPrompt+projectAllSimulationBoundary),
				agentcore.WithTools(contextTool, simulationTool),
				agentcore.WithMaxTurns(cappedMaxTurns(cfg.ResolveMaxTurns("writer", turnCeiling), turnCeiling)),
				agentcore.WithToolsAreIdempotent(false),
				agentcore.WithMaxToolErrors(0),
				agentcore.WithMaxRetries(subagentMaxRetries),
				agentcore.WithStopGuard(reminder.NewWorldSimulatorStopGuard(st)),
			)
			thinking, _ := ResolveThinkingForModel(model, roleThinking(cfg, "writer"))
			simulator.SetThinkingLevel(thinking)
			if err := projectAllWorldSimulationContextError(ctx, chapter, "before simulator prompt"); err != nil {
				return nil, err
			}
			if err := simulator.Prompt(ctx, projectAllWorldSimulationPrompt(
				arcBoundary,
				chapter,
				pass,
				gaps,
				recentSimulatorToolErrors,
			)); err != nil {
				return nil, fmt.Errorf("project-all world simulation chapter %d pass %d: %w", chapter, pass, err)
			}
			simulator.WaitForIdle()
			if err := projectAllWorldSimulationContextError(ctx, chapter, "after simulator session"); err != nil {
				return nil, err
			}
			state := simulator.State()
			if strings.TrimSpace(state.Error) != "" {
				lastSimulatorError = strings.TrimSpace(state.Error)
			}
			if toolErrors := recentProjectAllSimulationToolErrors(state.Messages); len(toolErrors) > 0 {
				// A session that merely stops without another tool call must not erase
				// the last concrete validator feedback before the final recovery pass.
				recentSimulatorToolErrors = toolErrors
			}
			simulation, simulationCP, err = loadCurrentProjectedSimulation(st, chapter)
			if err != nil {
				return nil, err
			}
			if simulation == nil {
				_, _, remainingGaps := tools.ChapterWorldSimulationStatus(st, chapter)
				if len(remainingGaps) == 0 {
					finalized, finalizeErr := finalizeReadyProjectedWorldSimulation(
						ctx,
						contextTool,
						simulationTool,
						st,
						chapter,
					)
					if finalizeErr != nil {
						if err := projectAllWorldSimulationContextError(ctx, chapter, "during host finalize"); err != nil {
							return nil, err
						}
						lastHostFinalizeError = finalizeErr.Error()
					} else if finalized {
						simulation, simulationCP, err = loadCurrentProjectedSimulation(st, chapter)
						if err != nil {
							return nil, err
						}
					}
				}
				if simulation == nil {
					progressAfter, progressErr := loadProjectAllWorldSimulationProgress(st, chapter)
					if progressErr != nil {
						return nil, fmt.Errorf(
							"project-all world simulation chapter %d read progress after pass %d: %w",
							chapter,
							pass,
							progressErr,
						)
					}
					if progressAfter.advancedFrom(progressBefore) {
						stagnantSessions = 0
					} else {
						stagnantSessions++
					}
				}
			}
		}
		if simulation == nil || simulationCP == nil {
			_, _, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
			remaining := strings.Join(compactProjectAllPromptGaps(gaps), "；")
			if remaining == "" {
				remaining = "formal simulation/checkpoint absent"
			}
			diagnostics := ""
			if lastSimulatorError != "" {
				diagnostics += "; agent error: " + lastSimulatorError
			}
			if lastHostFinalizeError != "" {
				diagnostics += "; host finalize error: " + lastHostFinalizeError
			}
			if len(recentSimulatorToolErrors) > 0 {
				diagnostics += "; recent simulate_chapter_world errors: " + strings.Join(recentSimulatorToolErrors, " | ")
			}
			if stagnantSessions >= projectAllWorldSimulationStagnantPassLimit {
				diagnostics += fmt.Sprintf(
					"; stopped after %d consecutive sessions without durable partial progress",
					stagnantSessions,
				)
			}
			return nil, fmt.Errorf(
				"project-all world simulation chapter %d did not finalize after %d bounded sessions; remaining gaps: %s%s",
				chapter,
				sessionsUsed,
				remaining,
				diagnostics,
			)
		}
	}

	craftReceipt, err := tools.EnsureProjectAllCraftReceiptForCurrentContext(st, chapter)
	if err != nil {
		return nil, fmt.Errorf("project-all chapter %d craft receipt: %w", chapter, err)
	}
	plan, planCP, planErr := loadCurrentProjectedPlan(st, chapter)
	if planErr != nil {
		return nil, planErr
	}
	if plan == nil {
		bookBudget := ""
		if receipt, receiptErr := st.LoadOutlineAllExecutionReceipt(); receiptErr == nil && receipt != nil && receipt.TargetWords > 0 {
			bookBudget = fmt.Sprintf(
				"全书正文目标总量%d字、共%d章，平均约%d字；本章容量应围绕全书剩余预算分配，不要连续顶到单章上限，最终工具会校验累计可行性。",
				receipt.TargetWords,
				receipt.TargetChapters,
				receipt.TargetWordsPerChapter,
			)
		}
		planningContextArgs, err := json.Marshal(map[string]any{
			"chapter": chapter,
			"profile": "planning",
		})
		if err != nil {
			return nil, fmt.Errorf("project-all planner context args chapter %d: %w", chapter, err)
		}
		planningContextRaw, err := contextTool.Execute(ctx, planningContextArgs)
		if err != nil {
			return nil, fmt.Errorf("project-all planner host context chapter %d: %w", chapter, err)
		}
		if len(planningContextRaw) == 0 {
			return nil, fmt.Errorf("project-all planner host context chapter %d is empty", chapter)
		}
		planner := agentcore.NewAgent(
			agentcore.WithModel(model),
			agentcore.WithSystemPrompt(bundle.Prompts.Planner+projectAllPlannerBoundary),
			agentcore.WithTools(
				contextTool,
				tools.NewCraftRecallTool(st),
				tools.NewPlanStructureTool(st),
				tools.NewPlanDetailsTool(st),
			),
			agentcore.WithMaxTurns(cappedMaxTurns(cfg.ResolveMaxTurns("writer", 36), 36)),
			agentcore.WithToolsAreIdempotent(false),
			agentcore.WithMaxToolErrors(0),
			agentcore.WithMaxRetries(subagentMaxRetries),
			agentcore.WithStopGuard(reminder.NewPlannerStopGuard(st)),
		)
		thinking, _ := ResolveThinkingForModel(model, roleThinking(cfg, "writer"))
		planner.SetThinkingLevel(thinking)
		if err := planner.Prompt(ctx, fmt.Sprintf(
			"Host 已代你完成本章唯一一次 novel_context(chapter=%d, profile=planning) 调用并签发当前访问收据；不要再次调用 novel_context，也不要解释或结束，直接消费下列权威 JSON 并调用 plan_structure，然后用 plan_details 分批 finalize：\n<host_prefetched_novel_context>\n%s\n</host_prefetched_novel_context>\n\nProject-Arc 已完成 V%dA%d《%s》中第 %d 章的全角色世界推演。本弧范围第%d-%d章，整体目标：%s。只规划第 %d 章：必须消费当前 content-addressed craft receipt；有 hits 的每个 need 都要按 receipt pack 精确转化进 external_reference_plan，fact receipt 有 hits 时同理；no_material 只绑定来源，禁止伪造材料。用 plan_structure + plan_details 分批生成并 finalize 完整 POV plan。若 project_all_state 有 predecessor_contract，arc_transition_contract 的 incoming id/text 必须逐字复制，consumed_by_cause 必须逐字等于本章一个 causal_beats[].cause；弧首章 incoming 留空。每章都必须另写弧内唯一的 outgoing consequence id/text，禁止用 goal/hook 冒充。render_capacity 必须给出3-6个有主动阻力、转折、退出后果和具体行动证据的场景单元，总量自然支撑 user_rules.chapter_words，不得靠手续、复述或总结注水。%s不得读取或生成正文，不得转去其他章节。跨弧 payoff/reveal/reward 必须保留为 carried-forward，不能挤到本弧末章提前兑现；只有第%d章才是全书末章。",
			chapter,
			string(planningContextRaw),
			arcBoundary.Volume,
			arcBoundary.Arc,
			arcBoundary.Title,
			chapter,
			arcBoundary.FirstChapter,
			arcBoundary.LastChapter,
			arcBoundary.Goal,
			chapter,
			bookBudget,
			arcBoundary.BookLastChapter,
		)); err != nil {
			return nil, fmt.Errorf("project-all POV plan chapter %d: %w", chapter, err)
		}
		planner.WaitForIdle()
		plan, planCP, err = loadCurrentProjectedPlan(st, chapter)
		if err != nil {
			return nil, err
		}
		if plan == nil || planCP == nil {
			return nil, fmt.Errorf("project-all POV plan chapter %d did not finalize", chapter)
		}
	}

	if simulationCP != nil && planCP.Seq <= simulationCP.Seq {
		return nil, fmt.Errorf(
			"project-all chapter %d plan checkpoint #%d does not consume simulation checkpoint #%d",
			chapter,
			planCP.Seq,
			simulationCP.Seq,
		)
	}
	if err := tools.ValidateProjectAllCraftPlanCurrent(st, *plan, craftReceipt); err != nil {
		repaired, repairErr := tools.RepairProjectAllCraftPlanCurrent(st, plan, craftReceipt)
		if repairErr != nil {
			return nil, fmt.Errorf("project-all chapter %d craft consumption repair: %w", chapter, repairErr)
		}
		if !repaired {
			return nil, fmt.Errorf("project-all chapter %d craft consumption: %w", chapter, err)
		}
		plan, planCP, repairErr = loadCurrentProjectedPlan(st, chapter)
		if repairErr != nil {
			return nil, repairErr
		}
		if plan == nil || planCP == nil {
			return nil, fmt.Errorf("project-all chapter %d repaired plan did not produce a current checkpoint", chapter)
		}
	}
	if !exactProjectAllSourceToken(simulation.Sources, contextToken) {
		return nil, fmt.Errorf(
			"project-all chapter %d world simulation did not attest exact projected context %s",
			chapter,
			contextToken,
		)
	}
	if !exactProjectAllSourceToken(plan.CausalSimulation.ContextSources, contextToken) {
		return nil, fmt.Errorf(
			"project-all chapter %d POV plan did not attest exact projected context %s",
			chapter,
			contextToken,
		)
	}
	renderContext, err := tools.BuildDraftRenderContextPayload(ctx, contextTool, chapter)
	if err != nil {
		return nil, fmt.Errorf("project-all chapter %d build frozen render payload: %w", chapter, err)
	}
	factReceipt, err := st.RAG.LoadLatestRAGFactReceipt(chapter)
	if err != nil {
		return nil, fmt.Errorf("project-all chapter %d load fact receipt: %w", chapter, err)
	}
	if factReceipt == nil {
		return nil, fmt.Errorf("project-all chapter %d did not persist an explicit RAG fact receipt", chapter)
	}
	return &ProjectedChapterArtifacts{
		WorldSimulation:       simulation,
		Plan:                  plan,
		PlanCheckpoint:        planCP,
		RAGFactReceipt:        factReceipt,
		CraftRecallReceipt:    craftReceipt,
		PlanningContextDigest: strings.TrimSpace(planningContextDigest),
		RenderContext:         renderContext,
	}, nil
}

func exactProjectAllSourceToken(sources []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, source := range sources {
		if strings.TrimSpace(source) == expected {
			return true
		}
	}
	return false
}

type projectAllModelToolProtocol interface {
	Name() string
	Description() string
	Schema() map[string]any
}

func projectAllToolContractsDigest() string {
	contracts := make([]struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Schema      map[string]any `json:"schema"`
	}, 0, 5)
	for _, tool := range []projectAllModelToolProtocol{
		tools.NewContextTool(nil, tools.References{}, ""),
		tools.NewSimulateChapterWorldTool(nil),
		tools.NewCraftRecallTool(nil),
		tools.NewPlanStructureTool(nil),
		tools.NewPlanDetailsTool(nil),
	} {
		contracts = append(contracts, struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Schema      map[string]any `json:"schema"`
		}{
			Name:        tool.Name(),
			Description: tool.Description(),
			Schema:      tool.Schema(),
		})
	}
	digest, err := domain.DeterministicPlanningHash(contracts)
	if err != nil {
		return ""
	}
	return digest
}

// ProjectAllPlanningProtocolDigest exposes the complete model-visible
// simulator/planner prompt and tool protocol to the generation identity
// without exporting those inputs. Any model contract change creates a new
// planning dependency root; bounded host recovery mechanics remain outside it.
func ProjectAllPlanningProtocolDigest(plannerPrompt string) string {
	toolContracts := projectAllToolContractsDigest()
	if toolContracts == "" {
		return ""
	}
	authorityPolicy := tools.ProjectAllSimulationAuthorityProtocolDigest()
	if authorityPolicy == "" {
		return ""
	}
	digest, err := domain.DeterministicPlanningHash(struct {
		Version            string `json:"version"`
		WorldSimulator     string `json:"world_simulator"`
		SimulationBoundary string `json:"simulation_boundary"`
		Planner            string `json:"planner"`
		PlannerBoundary    string `json:"planner_boundary"`
		ToolContracts      string `json:"tool_contracts"`
		AuthorityPolicy    string `json:"authority_policy"`
	}{
		Version:            "project-all-agent-protocol.v2",
		WorldSimulator:     worldSimulatorSystemPrompt,
		SimulationBoundary: projectAllSimulationBoundary,
		Planner:            plannerPrompt,
		PlannerBoundary:    projectAllPlannerBoundary,
		ToolContracts:      toolContracts,
		AuthorityPolicy:    authorityPolicy,
	})
	if err != nil {
		return ""
	}
	return digest
}

func loadCurrentProjectedSimulation(
	st *store.Store,
	chapter int,
) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	cp, err := tools.CurrentChapterWorldSimulationCheckpoint(st, chapter)
	if err != nil {
		return nil, nil, err
	}
	if cp == nil {
		return nil, nil, nil
	}
	// A checkpoint proves exact bytes, not that a stricter semantic invariant
	// still accepts those bytes. Reopen an unpublished projected chapter when
	// its finalized simulation now has gaps; simulate_chapter_world will retain
	// valid prior-chapter state and replace only this chapter's invalid epoch.
	_, ready, _ := tools.ChapterWorldSimulationStatus(st, chapter)
	if !ready {
		return nil, nil, nil
	}
	simulation, err := st.LoadChapterWorldSimulation(chapter)
	if err != nil {
		return nil, nil, err
	}
	return simulation, cp, nil
}

func loadCurrentProjectedPlan(
	st *store.Store,
	chapter int,
) (*domain.ChapterPlan, *domain.Checkpoint, error) {
	cp, err := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if cp == nil {
		return nil, nil, nil
	}
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return nil, nil, err
	}
	return plan, cp, nil
}

const projectAllSimulationBoundary = `

你处于 Project-Arc 单弧隔离推演，不是正文写作：
- 工作区只是非正史投影；不得调用、要求或暗示任何正文工具。
- 必须完成当前章全部实名关键角色的自主决定、蝴蝶效应、可见性和主角投影。
- 上一章的 projected summary/world ledgers 是本章唯一前态；未来大纲不能倒灌成角色已知事实。
- 不得改 user rules、foundation、outline、world tick 或 progress；只允许 finalize 当前章 world simulation。
`

const projectAllPlannerBoundary = `

你处于 Project-Arc 单弧隔离规划，不是正文写作：
- 当前章完整 world simulation 已是唯一因果输入；只把主角可知部分投影为 POV plan。
- 必须给出完整章节合同、人物选择链、读者奖励/留存、声口、情绪、文学渲染和结构化结果变化；不得把 coarse outline 当成完成品。
- reveal_budget 每一项、每个用逗号或分号分开的分句，都必须写成可机械检查的明确禁揭事实，例如“不揭示 X / 不解释 Y / 不提前给出 Z”，否定词后至少保留 4 个有效字符；禁止混入“只露一部分”“仅展示已知信息”“控制揭示程度”这类正向口号，也禁止只写“不解释”而不点名被禁事实。
- continuity_checks 必须写成可核对的具体事实或具体禁行变化，不能只写“保持连续”“遵守前态”。
- 每章都必须绑定本隔离工作区当前 planning context 生成的 content-addressed fact receipt 与 craft receipt；禁止缺省、沿用其他章节或沿用其他 generation 的 receipt。
- craft receipt 有 hits 时，每个命中的 need 都必须在 external_reference_plan 中用 receipt pack 的 usable_details、transformation_rule、do_not_use 精确转化；fact receipt 有 hits 时同理。no_material 只能绑定来源，禁止伪造材料或引用。
- RAG/craft 原始召回不得交给未来 Drafter；未来渲染只消费本章正式 plan 中已转化且受 receipt 约束的方法与细节。
- arc_transition_contract 是弧内承接硬合同：弧首章 incoming 三字段留空；其余章节必须逐字复制 project_all_state.predecessor_contract 的 outgoing_consequence_id/text，并让 consumed_by_cause 逐字等于本章某个 causal_beats[].cause。每章必须发布弧内唯一 outgoing_consequence_id 和具体、已发生的 outgoing_consequence_text；不得从 goal、hook 或相邻章标题猜测承接。
- render_capacity 是硬合同：必须用3-6个有角色目标、主动阻力、递进动作、转折和退出后果的场景单元自然支撑本书单章字数区间；禁止用说明、复述、检查清单、手续流水或重复反应凑长度。
- 不得读取、生成、编辑或提交正文；只允许 finalize 当前章正式 plan。
`
