package agents

import (
	"context"
	"encoding/json"
	"fmt"
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
	contextTool := tools.NewContextTool(st, bundle.References, cfg.Style)
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
		simulator := agentcore.NewAgent(
			agentcore.WithModel(model),
			agentcore.WithSystemPrompt(worldSimulatorSystemPrompt+projectAllSimulationBoundary),
			agentcore.WithTools(contextTool, tools.NewSimulateChapterWorldTool(st)),
			agentcore.WithMaxTurns(cappedMaxTurns(cfg.ResolveMaxTurns("writer", 20), 20)),
			agentcore.WithToolsAreIdempotent(false),
			agentcore.WithMaxToolErrors(0),
			agentcore.WithMaxRetries(subagentMaxRetries),
			agentcore.WithStopGuard(reminder.NewWorldSimulatorStopGuard(st)),
		)
		thinking, _ := ResolveThinkingForModel(model, roleThinking(cfg, "writer"))
		simulator.SetThinkingLevel(thinking)
		if err := simulator.Prompt(ctx, fmt.Sprintf(
			"Project-Arc 正在隔离工作区推演 V%dA%d《%s》（第%d-%d章），本弧目标：%s。当前只处理第 %d 章：先调用 novel_context(chapter=%d, profile=world_simulation)，再分批完成并 finalize simulate_chapter_world。不得写正文、不得修改设定或进度、不得处理其他弧。若本章是本弧末章，必须闭合本弧内部因果并把跨弧影响明确标为 carried-forward，不能为了封弧把它提前兑现；只有第%d章才是全书末章，届时不得指向书外章节。",
			arcBoundary.Volume,
			arcBoundary.Arc,
			arcBoundary.Title,
			arcBoundary.FirstChapter,
			arcBoundary.LastChapter,
			arcBoundary.Goal,
			chapter,
			chapter,
			arcBoundary.BookLastChapter,
		)); err != nil {
			return nil, fmt.Errorf("project-all world simulation chapter %d: %w", chapter, err)
		}
		simulator.WaitForIdle()
		simulation, simulationCP, err = loadCurrentProjectedSimulation(st, chapter)
		if err != nil {
			return nil, err
		}
		if simulation == nil || simulationCP == nil {
			return nil, fmt.Errorf("project-all world simulation chapter %d did not finalize", chapter)
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
			"Project-Arc 已完成 V%dA%d《%s》中第 %d 章的全角色世界推演。本弧范围第%d-%d章，整体目标：%s。只规划第 %d 章：先调用 novel_context(chapter=%d, profile=planning)，必须消费当前 content-addressed craft receipt；有 hits 的每个 need 都要按 receipt pack 精确转化进 external_reference_plan，fact receipt 有 hits 时同理；no_material 只绑定来源，禁止伪造材料。然后用 plan_structure + plan_details 分批生成并 finalize 完整 POV plan。若 project_all_state 有 predecessor_contract，arc_transition_contract 的 incoming id/text 必须逐字复制，consumed_by_cause 必须逐字等于本章一个 causal_beats[].cause；弧首章 incoming 留空。每章都必须另写弧内唯一的 outgoing consequence id/text，禁止用 goal/hook 冒充。render_capacity 必须给出3-6个有主动阻力、转折、退出后果和具体行动证据的场景单元，总量自然支撑 user_rules.chapter_words，不得靠手续、复述或总结注水。不得读取或生成正文，不得转去其他章节。跨弧 payoff/reveal/reward 必须保留为 carried-forward，不能挤到本弧末章提前兑现；只有第%d章才是全书末章。",
			arcBoundary.Volume,
			arcBoundary.Arc,
			arcBoundary.Title,
			chapter,
			arcBoundary.FirstChapter,
			arcBoundary.LastChapter,
			arcBoundary.Goal,
			chapter,
			chapter,
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
		return nil, fmt.Errorf("project-all chapter %d craft consumption: %w", chapter, err)
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

// ProjectAllPlanningProtocolDigest exposes the complete model-visible
// simulator/planner protocol to the generation identity without exporting the
// prompts themselves. Any boundary or system-prompt change creates a different
// planning dependency root.
func ProjectAllPlanningProtocolDigest(plannerPrompt string) string {
	digest, err := domain.DeterministicPlanningHash(struct {
		Version            string `json:"version"`
		WorldSimulator     string `json:"world_simulator"`
		SimulationBoundary string `json:"simulation_boundary"`
		Planner            string `json:"planner"`
		PlannerBoundary    string `json:"planner_boundary"`
	}{
		Version:            "project-all-agent-protocol.v1",
		WorldSimulator:     worldSimulatorSystemPrompt,
		SimulationBoundary: projectAllSimulationBoundary,
		Planner:            plannerPrompt,
		PlannerBoundary:    projectAllPlannerBoundary,
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
	if err != nil || cp == nil {
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
	if err != nil || cp == nil {
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
