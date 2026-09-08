package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const characterAgentSystemPrompt = `你是一个小说角色本人的决策 Agent，不是作者、编剧或旁白。

你只能使用用户消息里的 character_observation_packet：
- 只依据其中明确列出的自身目标、压力、资源、关系、承诺、已知事实、感知事件、公共规则、公开机制和记忆。
- public_rules 与 public_mechanisms 仅是显式角色可知投影；缺失的机制细节不代表没有世界约束，实际成败由裁判确定，不得自行补写隐藏条件或预定结果。
- 作者侧人物弧和知识禁区描述不是本人记忆；角色知识只来自明确列出的已知事实、自身状态、实际感知与有来源的个人记忆。
- 不得猜测未来大纲、章节钩子、叙事任务、其他角色秘密或未被你感知的世界事实。
- 先考虑至少两个当下真实可行的选项，再以此角色的性格、误判、利益和风险承受作出选择。
- knowledge_refs 只能逐字引用 observation 中存在的 fact id。
- 行动使用了 public_mechanisms 时，mechanism_refs 只能引用其 id；不得猜测或引用未公开机制。
- 只调用 submit_character_decision 一次；decision_reason 写可审计的简明动因，不输出思维链、分析过程或作者说明。
- 决定可以是等待、拒绝、观察或继续已有行动，但不能为了配合大纲而失去人物自主性。`

const worldArbiterSystemPrompt = `你是单一世界的规则裁判，不是作者，也不是任何角色。

你会收到世界刺激、硬合同、软方向和全部角色的独立提案：
- decision 与 intended_action 必须逐字复制对应提案，绝对不得替角色改意图。
- 只裁决行动顺序、时间、地点、资源、知识、物理/社会规则、碰撞、完成度和结果。
- operational_world 是地点、路线耗时、势力资源与进度钟的权威快照；mechanisms 给出触发、前置、输入、代价、效果、失败、可观测性和时间。每条 resolution 用 mechanism_refs 记录实际适用的机制。
- stimulus.story_clock 存在时，current_day 是宿主绑定的实际开局时刻（单位天），不是章均估算。必须提交 story_time 的 chapter/start_day/end_day；start_day 原样等于 current_day，end_day 根据并行、顺序、移动和等待的实际耗时裁决，不超过全书最大期限。分钟除以1440，秒除以86400，不能把章号当已过去时间。
- story_clock.nominal_budget=true 时，duration_days_min/max 只是未明确时限项目的密度估算，不是作者硬期限，不得因此阻断角色行动或强制时长；current_day 仍只来自已确认的实际时间。
- counterfactual_tests.forbidden_outcome 是硬性反捷径约束；即使软大纲需要，也不能裁决出该结果。
- 软方向不是预定结果；角色选择破坏软情节时，保留真实结果交给 Planner 重算。
- 不得把一个角色的私有理由或知识泄露给另一个角色。
- 每个活跃角色恰好一条 resolution，butterfly_effects 至少一条。
- 第一轮若存在需要角色修订的冲突，finalized=false，并只给相关 agent 最小冲突反馈；没有未解决冲突时 finalized=true。
- 第二轮必须闭合；无法闭合时让工具拒绝，禁止伪造可行结果。
- 必须报告 hard_contract_status。角色选择让当前弧硬义务不可实现时标为 infeasible，列出冲突硬合同并拒绝生成章节计划，由 Architect successor generation 重排软大纲。
- 只调用 resolve_chapter_world，不输出思维链或正文。`

const characterAgentSuccessorArchitectPrompt = `你是角色独立决策协议中的 Architect，只处理硬合同冲突后的 successor generation 软大纲。

你必须完整保留宿主绑定的已接受正史、结局方向、全书与当前弧篇幅、不可协商设定、硬合同以及 World Arbiter 原样保留的角色选择。
你只能重写冲突章到当前弧末每章的 title、core_event、hook、scenes；不得增删或调换章节号，不得编辑 contract_refs，不得让角色改选，也不得把角色未来选择写成既定事实。
revised_chapters 必须覆盖连续的完整剩余章位。只调用 submit_character_agent_successor_plan 一次；architect_summary 写简短、可审计的重排说明，不输出思维链或正文。`

const characterAgentCoordinatorPrompt = `你是角色独立决策协议的调度入口，不替任何角色作决定。

从任务中取得章节号，只调用 simulate_chapter_world 一次。该工具会在服务端完成角色筛选、严格观察包、并发角色 Agent、World Arbiter 两轮裁决、恢复和持久化。工具返回 simulated=true 后立即停止；不得生成 POV plan、正文或解释。`

type characterAgentSimulationFacade struct {
	cfg         bootstrap.Config
	store       *store.Store
	models      *bootstrap.ModelSet
	contextTool *tools.ContextTool
}

func newCharacterAgentSimulationFacade(cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, contextTool *tools.ContextTool) agentcore.Tool {
	return &characterAgentSimulationFacade{cfg: cfg, store: st, models: models, contextTool: contextTool}
}

func (t *characterAgentSimulationFacade) Name() string { return "simulate_chapter_world" }
func (t *characterAgentSimulationFacade) Description() string {
	return "运行角色独立 Agent 决策和 World Arbiter 裁决，并保存兼容的 chapter world simulation v2。"
}
func (t *characterAgentSimulationFacade) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter": map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []string{"chapter"},
		"additionalProperties": false,
	}
}
func (t *characterAgentSimulationFacade) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Chapter int `json:"chapter"`
	}
	if err := json.Unmarshal(args, &input); err != nil || input.Chapter <= 0 {
		return nil, fmt.Errorf("simulate_chapter_world requires chapter > 0")
	}
	boundary, err := deriveCharacterAgentArcBoundary(t.store, input.Chapter)
	if err != nil {
		return nil, err
	}
	simulation, checkpoint, err := runCharacterAgentWorldSimulation(ctx, t.cfg, t.store, t.models, t.contextTool, input.Chapter, boundary)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"simulated":           true,
		"chapter":             input.Chapter,
		"version":             simulation.Version,
		"simulation_id":       simulation.SimulationID,
		"checkpoint_sequence": checkpoint.Seq,
		"protocol":            simulation.CharacterAgentProtocol.Version,
	})
}

func deriveCharacterAgentArcBoundary(st *store.Store, chapter int) (ProjectedArcBoundary, error) {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return ProjectedArcBoundary{}, err
	}
	bookLast := 0
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			for _, entry := range arc.Chapters {
				if entry.Chapter > bookLast {
					bookLast = entry.Chapter
				}
			}
		}
	}
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			if len(arc.Chapters) == 0 {
				continue
			}
			first := arc.Chapters[0].Chapter
			last := first
			contains := false
			for _, entry := range arc.Chapters {
				if entry.Chapter < first {
					first = entry.Chapter
				}
				if entry.Chapter > last {
					last = entry.Chapter
				}
				if entry.Chapter == chapter {
					contains = true
				}
			}
			if contains {
				title := firstAgentText(arc.Title, volume.Title, fmt.Sprintf("第%d章所在故事弧", chapter))
				goal := firstAgentText(arc.Goal, volume.Theme, "推进当前弧并保持既有正史连续")
				return ProjectedArcBoundary{
					Volume: volume.Index, Arc: arc.Index, Title: title, Goal: goal,
					BaseCanonChapter: max(0, first-1), BaseCanonRoot: fmt.Sprintf("accepted-canon:chapter-%06d", max(0, first-1)),
					FirstChapter: first, LastChapter: last, BookLastChapter: max(bookLast, last),
				}, nil
			}
		}
	}
	entries, err := st.Outline.LoadOutline()
	if err != nil {
		return ProjectedArcBoundary{}, err
	}
	if len(entries) == 0 {
		return ProjectedArcBoundary{}, fmt.Errorf("cannot locate chapter %d in outline", chapter)
	}
	first, last := entries[0].Chapter, entries[0].Chapter
	var current *domain.OutlineEntry
	for i := range entries {
		entry := &entries[i]
		if entry.Chapter < first {
			first = entry.Chapter
		}
		if entry.Chapter > last {
			last = entry.Chapter
		}
		if entry.Chapter == chapter {
			current = entry
		}
	}
	if current == nil {
		return ProjectedArcBoundary{}, fmt.Errorf("cannot locate chapter %d in outline", chapter)
	}
	return ProjectedArcBoundary{
		Volume: 1, Arc: 1, Title: firstAgentText(current.Title, "当前故事弧"),
		Goal:             firstAgentText(current.CoreEvent, "推进当前弧并保持既有正史连续"),
		BaseCanonChapter: max(0, first-1), BaseCanonRoot: fmt.Sprintf("accepted-canon:chapter-%06d", max(0, first-1)),
		FirstChapter: first, LastChapter: last, BookLastChapter: last,
	}, nil
}

type characterAgentProfile struct {
	Character  domain.Character
	Dossier    *domain.CharacterDossier
	Continuity *domain.CharacterContinuityEntry
	Agenda     *domain.CharacterAgenda
	Record     domain.CharacterAgentRecord
}

type characterAgentChapterInputs struct {
	Stimulus     domain.WorldStimulusPacket
	Activation   domain.CharacterAgentActivation
	Observations map[string]domain.CharacterObservationPacket
	Sources      []string
	// Host-only execution binding, never included in a character model packet.
	CycleSession          *domain.CharacterActivationSession
	ContinuationSelection *CharacterWorkContinuationSelection
	ContinuationProof     *store.CharacterContinuationArbitration
	ArbitrationV3         *store.CharacterArbitrationV3
	ArbitrationRoundV3    int
}

// CharacterAgentHardContractConflictError carries the immutable Architect
// successor plan back across the isolated project-all boundary. Callers must
// start a new generation from this plan; they must not ask Planner to continue
// the infeasible generation.
type CharacterAgentHardContractConflictError struct {
	GenerationID      string
	Chapter           int
	ArbitrationDigest string
	Conflicts         []string
	SuccessorPlan     domain.CharacterAgentSuccessorPlan
}

func (e *CharacterAgentHardContractConflictError) Error() string {
	if e == nil {
		return "character-agent hard contract conflict"
	}
	return fmt.Sprintf("character-agent hard contract conflict at chapter %d (%s); Architect successor %s is required", e.Chapter, strings.Join(e.Conflicts, "; "), e.SuccessorPlan.Digest)
}

func CharacterAgentProtocolDigest() string {
	submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{})
	resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
	successor := tools.NewSubmitCharacterAgentSuccessorPlanTool(nil, domain.CharacterAgentSuccessorPlan{}, nil)
	digest, err := domain.DeterministicPlanningHash(struct {
		Version               string         `json:"version"`
		ObservationProjection string         `json:"observation_projection"`
		CharacterPrompt       string         `json:"character_prompt"`
		ArbiterPrompt         string         `json:"arbiter_prompt"`
		SuccessorPrompt       string         `json:"successor_prompt"`
		SubmitSchema          map[string]any `json:"submit_schema"`
		ResolveSchema         map[string]any `json:"resolve_schema"`
		SuccessorPlanSchema   map[string]any `json:"successor_plan_schema"`
	}{
		Version:               domain.CharacterAgentDecisionProtocolVersion,
		ObservationProjection: "character-observation-view.v3-actual-story-clock",
		CharacterPrompt:       characterAgentSystemPrompt,
		ArbiterPrompt:         worldArbiterSystemPrompt,
		SuccessorPrompt:       characterAgentSuccessorArchitectPrompt,
		SubmitSchema:          submit.Schema(),
		ResolveSchema:         resolve.Schema(),
		SuccessorPlanSchema:   successor.Schema(),
	})
	if err != nil {
		return ""
	}
	return "sha256:" + digest
}

func runCharacterAgentWorldSimulation(
	ctx context.Context,
	cfg bootstrap.Config,
	st *store.Store,
	models *bootstrap.ModelSet,
	contextTool *tools.ContextTool,
	chapter int,
	arcBoundary ProjectedArcBoundary,
) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	if st == nil || models == nil || contextTool == nil {
		return nil, nil, fmt.Errorf("character-agent simulation dependencies are incomplete")
	}
	// Reject an incompatible stored protocol before novel_context can append
	// an access receipt, and before canonical registration, usage or proposals
	// can be written. Normal project-all derives a new generation identity from
	// the changed prompt; direct/interactive recovery needs the same boundary.
	preflightGeneration, err := characterAgentExecutionGeneration(st, chapter)
	if err != nil {
		return nil, nil, err
	}
	protocol := cfg.CharacterAgentsProtocolVersion()
	activationLimit, err := characterActivationExecutionLimit(st, cfg, preflightGeneration, arcBoundary)
	if err != nil {
		return nil, nil, err
	}
	if activationLimit <= 1 {
		if err := requireCharacterAgentGenerationProtocol(st, preflightGeneration, chapter, protocol); err != nil {
			return nil, nil, err
		}
	}
	executionContext, err := contextTool.PrepareCharacterAgentExecutionContext(ctx, chapter)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare character-agent world context: %w", err)
	}
	var projected domain.ProjectedPlanningContextV2
	if executionContext.ProjectAllState != nil {
		projected = *executionContext.ProjectAllState
	}
	generationID := strings.TrimSpace(projected.GenerationID)
	if generationID == "" {
		if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil {
			generationID = strings.TrimSpace(progress.GenerationID)
		}
	}
	if generationID == "" {
		generationID = "live_v1"
	}
	if generationID != preflightGeneration {
		return nil, nil, fmt.Errorf("character-agent generation changed during context preparation: before=%s after=%s", preflightGeneration, generationID)
	}
	sources := compactAgentStrings([]string{
		strings.TrimSpace(executionContext.ProjectAllSourceToken),
		strings.TrimSpace(executionContext.AccessSourceToken),
		"character-agent-protocol:" + CharacterAgentProtocolDigestForVersion(protocol),
	})
	if activationLimit > 1 {
		if !arcBoundary.CharacterProtocolPinned && arcBoundary.CharacterActivationPolicy == "" {
			arcBoundary.CharacterActivationPolicy = cfg.CharacterActivationPolicy()
		}
		if _, err := runCharacterActivationChapter(ctx, cfg, st, models, generationID, chapter, arcBoundary, projected, sources, activationLimit); err != nil {
			var conflict *CharacterActivationChapterConflictError
			if errors.As(err, &conflict) {
				return characterActivationHardContractFailure(ctx, cfg, st, models, arcBoundary, *conflict)
			}
			return nil, nil, err
		}
		return tools.PublishCharacterActivationSimulation(ctx, st, generationID, chapter, sources)
	}
	inputs, err := loadOrPrepareCharacterAgentInputs(st, generationID, chapter, arcBoundary, projected, sources, protocol)
	if err != nil {
		return nil, nil, err
	}
	proposals, receipt, err := runCharacterAgentDecisionProtocol(ctx, cfg, st, models, inputs)
	if err != nil {
		return nil, nil, err
	}
	if receipt.HardContractStatus == "infeasible" {
		return characterAgentHardContractFailure(ctx, cfg, st, models, arcBoundary, inputs, proposals, *receipt)
	}
	if receipt == nil || !receipt.Finalized {
		return nil, nil, fmt.Errorf("world arbitration did not finalize")
	}
	simulation, checkpoint, err := loadCurrentProjectedSimulation(st, chapter)
	if err != nil || simulation == nil || checkpoint == nil {
		return nil, nil, fmt.Errorf("load finalized character-agent simulation: %w", err)
	}
	if err := updateProjectedCharacterAgentMemories(st, generationID, chapter, proposals, *receipt); err != nil {
		return nil, nil, err
	}
	return simulation, checkpoint, nil
}

func characterAgentHardContractFailure(
	ctx context.Context,
	cfg bootstrap.Config,
	st *store.Store,
	models *bootstrap.ModelSet,
	boundary ProjectedArcBoundary,
	inputs characterAgentChapterInputs,
	proposals []domain.CharacterDecisionProposal,
	receipt domain.WorldArbitrationReceipt,
) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	plan, err := runCharacterAgentSuccessorArchitect(ctx, cfg, st, models, boundary, inputs, proposals, receipt)
	if err != nil {
		return nil, nil, fmt.Errorf("hard contract conflict could not create Architect successor plan: %w", err)
	}
	return nil, nil, &CharacterAgentHardContractConflictError{
		GenerationID: receipt.GenerationID, Chapter: receipt.Chapter, ArbitrationDigest: receipt.Digest,
		Conflicts: append([]string(nil), receipt.HardContractConflicts...), SuccessorPlan: *plan,
	}
}

func runCharacterAgentSuccessorArchitect(
	ctx context.Context,
	cfg bootstrap.Config,
	st *store.Store,
	models *bootstrap.ModelSet,
	boundary ProjectedArcBoundary,
	inputs characterAgentChapterInputs,
	proposals []domain.CharacterDecisionProposal,
	receipt domain.WorldArbitrationReceipt,
) (*domain.CharacterAgentSuccessorPlan, error) {
	return runCharacterAgentSuccessorArchitectForCause(ctx, cfg, st, models, boundary, inputs, proposals, receipt, nil)
}

func runCharacterAgentSuccessorArchitectForCause(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, boundary ProjectedArcBoundary, inputs characterAgentChapterInputs, proposals []domain.CharacterDecisionProposal, receipt domain.WorldArbitrationReceipt, cause *characterActivationSuccessorCause) (*domain.CharacterAgentSuccessorPlan, error) {
	readinessDigest := ""
	conflicts := receipt.HardContractConflicts
	if cause != nil {
		readinessDigest, conflicts = cause.Readiness.Digest, cause.Conflicts
	}
	if existing, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil {
		return nil, err
	} else if existing != nil && existing.ParentGenerationID == receipt.GenerationID && existing.ArbitrationDigest == receipt.Digest && existing.ReadinessDigest == readinessDigest {
		return existing, nil
	}
	if err := projectedAccountingBefore(ctx); err != nil {
		return nil, err
	}
	model := models.ForRole("architect")
	if model == nil {
		return nil, fmt.Errorf("architect model is unavailable")
	}
	original := make([]domain.OutlineEntry, 0, boundary.LastChapter-receipt.Chapter+1)
	for chapter := receipt.Chapter; chapter <= boundary.LastChapter; chapter++ {
		entry, err := st.Outline.GetChapterOutline(chapter)
		if err != nil || entry == nil {
			return nil, fmt.Errorf("load successor soft outline chapter %d: %w", chapter, err)
		}
		original = append(original, *entry)
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil {
		return nil, err
	}
	ending := "保留既有结局方向"
	nonNegotiables := append([]string(nil), inputs.Stimulus.HardContracts...)
	if cause != nil {
		nonNegotiables = append(nonNegotiables, cause.NonNegotiables...)
	}
	if compass != nil {
		ending = firstAgentText(compass.EndingDirection, ending)
		nonNegotiables = append(nonNegotiables, compass.NonNegotiables...)
	}
	nonNegotiables = compactAgentStrings(nonNegotiables)
	if len(nonNegotiables) == 0 {
		return nil, fmt.Errorf("hard-contract-infeasible receipt has no host-bound non-negotiables")
	}
	acceptedRoot := strings.TrimSpace(boundary.BaseCanonRoot)
	if acceptedRoot == "" {
		acceptedRoot = "accepted-canon:chapter-" + fmt.Sprintf("%06d", max(0, boundary.BaseCanonChapter))
	}
	base := domain.CharacterAgentSuccessorPlan{
		Version: domain.CharacterAgentSuccessorPlanVersion, ParentGenerationID: receipt.GenerationID,
		BaseCanonChapter: max(0, boundary.BaseCanonChapter), TriggerChapter: receipt.Chapter,
		ArcFirstChapter: boundary.FirstChapter, ArcLastChapter: boundary.LastChapter, BookLastChapter: boundary.BookLastChapter,
		ArbitrationDigest: receipt.Digest, AcceptedCanonRoot: acceptedRoot, EndingDirection: ending,
		NonNegotiables: nonNegotiables, HardContractConflicts: append([]string(nil), conflicts...), ReadinessDigest: readinessDigest,
	}
	tool := tools.NewSubmitCharacterAgentSuccessorPlanTool(st, base, original)
	payload, _ := json.Marshal(struct {
		Constraints domain.CharacterAgentSuccessorPlan `json:"immutable_constraints"`
		Original    []domain.OutlineEntry              `json:"current_soft_outline"`
		Proposals   []domain.CharacterDecisionProposal `json:"character_choices"`
		Arbitration domain.WorldArbitrationReceipt     `json:"world_arbitration"`
		Readiness   *domain.CharacterChapterReadiness  `json:"readiness,omitempty"`
	}{base, original, proposals, receipt, func() *domain.CharacterChapterReadiness {
		if cause != nil {
			return &cause.Readiness
		}
		return nil
	}()})
	inputMessage, err := modelinput.NewExactAgentPacketMessage(modelinput.KindCharacterSuccessor, "只重排软章位并提交 successor plan：\n<successor_input>\n"+string(payload)+"\n</successor_input>")
	if err != nil {
		return nil, err
	}
	accounted, onMessage := projectedAccountingModel(ctx, model, "character_successor_architect", "")
	events := agentcore.AgentLoop(
		ctx,
		[]agentcore.AgentMessage{inputMessage},
		agentcore.AgentContext{SystemPrompt: characterAgentSuccessorArchitectPrompt, Tools: []agentcore.Tool{tool}},
		agentcore.LoopConfig{
			Model: accounted, OnMessage: onMessage, MaxTurns: cappedMaxTurns(cfg.ResolveMaxTurns("architect", 8), 10), MaxRetries: subagentMaxRetries,
			MaxToolErrors: 0, ThinkingLevel: resolvedRoleThinking(model, cfg, "architect"), ToolsAreIdempotent: false,
			CacheLastMessage: promptCacheControl,
			PromptCacheKey:   agentPromptCacheKey("character_successor_architect", st.Dir(), receipt.GenerationID, receipt.Digest),
			StopAfterTool:    func(name string) bool { return name == tool.Name() },
		},
	)
	var runErr error
	for event := range events {
		if event.Type == agentcore.EventError && event.Err != nil {
			runErr = event.Err
		}
	}
	if err := errors.Join(runErr, projectedAccountingAfter(ctx)); err != nil {
		return nil, err
	}
	plan, err := st.CharacterAgents.LoadCurrentSuccessorPlan()
	if err != nil {
		return nil, fmt.Errorf("load Architect successor plan after execution: %w", err)
	}
	if plan == nil {
		return nil, errors.New("Architect returned without a successor plan")
	}
	if plan.ParentGenerationID != receipt.GenerationID || plan.ArbitrationDigest != receipt.Digest || plan.ReadinessDigest != readinessDigest {
		return nil, fmt.Errorf("Architect successor plan is not bound to failed arbitration")
	}
	return plan, nil
}

func loadOrPrepareCharacterAgentInputs(
	st *store.Store,
	generationID string,
	chapter int,
	arcBoundary ProjectedArcBoundary,
	projected domain.ProjectedPlanningContextV2,
	sources []string,
	protocols ...string,
) (characterAgentChapterInputs, error) {
	protocol := domain.CharacterAgentDecisionProtocolVersion
	if len(protocols) > 0 {
		protocol = protocols[0]
	}
	if err := requireCharacterAgentGenerationProtocol(st, generationID, chapter, protocol); err != nil {
		return characterAgentChapterInputs{}, err
	}
	if stimulus, err := st.CharacterAgents.LoadStimulus(generationID, chapter); err != nil {
		return characterAgentChapterInputs{}, err
	} else if stimulus != nil {
		activation, loadErr := st.CharacterAgents.LoadActivation(generationID, chapter)
		if loadErr != nil || activation == nil {
			return characterAgentChapterInputs{}, fmt.Errorf("character-agent stimulus exists without activation: %w", loadErr)
		}
		observations := make(map[string]domain.CharacterObservationPacket)
		for _, entry := range activation.Entries {
			if entry.State != domain.CharacterAgentActive {
				continue
			}
			observation, observationErr := st.CharacterAgents.LoadObservation(generationID, chapter, 1, entry.AgentID)
			if observationErr != nil || observation == nil || observation.Digest != entry.ObservationDigest {
				return characterAgentChapterInputs{}, fmt.Errorf("load character observation %s: %w", entry.AgentID, observationErr)
			}
			if stimulus.Version == domain.WorldStimulusPacketV2Version {
				if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(*stimulus, *observation); err != nil {
					return characterAgentChapterInputs{}, fmt.Errorf("cached character observation source binding: %w", err)
				}
			}
			observations[entry.AgentID] = *observation
		}
		return characterAgentChapterInputs{Stimulus: *stimulus, Activation: *activation, Observations: observations, Sources: sources}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := st.EnsureCharacterAgentCanon(max(0, chapter-1)); err != nil {
		return characterAgentChapterInputs{}, err
	}
	profiles, protagonist, err := selectCharacterAgentRoster(st, chapter, arcBoundary)
	if err != nil {
		return characterAgentChapterInputs{}, err
	}
	registry, err := st.CharacterAgents.LoadRegistry()
	if err != nil {
		return characterAgentChapterInputs{}, err
	}
	if registry == nil {
		registry = &domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
		if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil {
			registry.Project = progress.NovelName
		}
	}
	for i := range registry.Entries {
		if registry.Entries[i].Status != domain.CharacterAgentRetired {
			registry.Entries[i].Status = domain.CharacterAgentSleeping
		}
	}
	for i := range profiles {
		updated, record, upsertErr := registry.UpsertCharacter(profiles[i].Character.Name, profiles[i].Character.Aliases, profiles[i].Character.Tier, chapter, now)
		if upsertErr != nil {
			return characterAgentChapterInputs{}, upsertErr
		}
		registry = &updated
		profiles[i].Record = record
		if err := ensureCharacterAgentMemory(st, generationID, profiles[i], chapter, now); err != nil {
			return characterAgentChapterInputs{}, err
		}
	}
	stimulus, err := buildWorldStimulus(st, generationID, chapter, arcBoundary, projected, sources, now, protocol)
	if err != nil {
		return characterAgentChapterInputs{}, err
	}
	if err := st.CharacterAgents.SaveStimulus(stimulus); err != nil {
		return characterAgentChapterInputs{}, err
	}
	activation := domain.CharacterAgentActivation{
		Version: domain.CharacterAgentActivationVersion, GenerationID: generationID,
		Chapter: chapter, GeneratedAt: now,
	}
	observations := make(map[string]domain.CharacterObservationPacket)
	for i := range profiles {
		reasons := characterActivationReasons(st, profiles[i], protagonist, chapter, projected)
		state := domain.CharacterAgentSleeping
		if len(reasons) > 0 {
			state = domain.CharacterAgentActive
		}
		entry := domain.CharacterAgentActivationEntry{
			AgentID: profiles[i].Record.AgentID, Character: profiles[i].Character.Name,
			Tier: profiles[i].Character.Tier, State: state, Reasons: reasons,
		}
		if state == domain.CharacterAgentActive {
			observation, buildErr := buildCharacterObservation(st, generationID, chapter, profiles[i], stimulus, projected, now)
			if buildErr != nil {
				return characterAgentChapterInputs{}, buildErr
			}
			if err := st.CharacterAgents.SaveObservation(observation); err != nil {
				return characterAgentChapterInputs{}, err
			}
			entry.ObservationDigest = observation.Digest
			observations[entry.AgentID] = observation
		}
		activation.Entries = append(activation.Entries, entry)
		for j := range registry.Entries {
			if registry.Entries[j].AgentID != entry.AgentID {
				continue
			}
			registry.Entries[j].Status = state
			registry.Entries[j].UpdatedAt = now
			if state == domain.CharacterAgentActive {
				registry.Entries[j].LastActivatedChapter = chapter
			}
		}
	}
	finalizedRegistry, err := domain.FinalizeCharacterAgentRegistry(*registry)
	if err != nil {
		return characterAgentChapterInputs{}, err
	}
	activation.RegistryRoot = finalizedRegistry.RegistryRoot
	activation, err = domain.FinalizeCharacterAgentActivation(activation)
	if err != nil {
		return characterAgentChapterInputs{}, err
	}
	if err := st.CharacterAgents.SaveRegistry(finalizedRegistry); err != nil {
		return characterAgentChapterInputs{}, err
	}
	if err := st.CharacterAgents.SaveRegistrySnapshot(generationID, chapter, finalizedRegistry); err != nil {
		return characterAgentChapterInputs{}, err
	}
	if err := st.CharacterAgents.SaveActivation(activation); err != nil {
		return characterAgentChapterInputs{}, err
	}
	return characterAgentChapterInputs{Stimulus: stimulus, Activation: activation, Observations: observations, Sources: sources}, nil
}

func characterAgentExecutionGeneration(st *store.Store, chapter int) (string, error) {
	projected, _, err := tools.LoadProjectAllStateForExecution(st, chapter)
	if err != nil {
		return "", err
	}
	if projected != nil {
		return projected.GenerationID, nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return "", err
	}
	if progress != nil && strings.TrimSpace(progress.GenerationID) != "" {
		return strings.TrimSpace(progress.GenerationID), nil
	}
	return "live_v1", nil
}

func requireCharacterAgentGenerationProtocol(st *store.Store, generationID string, chapter int, protocols ...string) error {
	protocol := domain.CharacterAgentDecisionProtocolVersion
	if len(protocols) > 0 {
		protocol = protocols[0]
	}
	current := CharacterAgentProtocolDigestForVersion(protocol)
	if current == "" {
		return fmt.Errorf("character-agent current protocol digest is unavailable")
	}
	check := func(stimulus *domain.WorldStimulusPacket, storedChapter int) error {
		if stimulus != nil && characterProtocolForStimulus(*stimulus) != protocol {
			return fmt.Errorf("character-agent protocol mismatch: generation=%s chapter=%d existing=%s requested=%s；已保留旧观察和提案。请用生成这些工件的原版 novel-studio 恢复，或通过 --pipeline --stages preplan,project-all,seal 在合法弧边界创建新 generation；已封存弧使用 successor/rebase 流程，不要手改 JSON", generationID, storedChapter, characterProtocolForStimulus(*stimulus), protocol)
		}
		stored := ""
		if stimulus != nil {
			for _, source := range stimulus.Sources {
				const prefix = "character-agent-protocol:"
				if !strings.HasPrefix(source, prefix) {
					continue
				}
				digest := strings.TrimPrefix(source, prefix)
				if stored != "" && stored != digest {
					stored = "ambiguous"
					break
				}
				stored = digest
			}
		}
		if stored != current {
			return fmt.Errorf("character-agent protocol mismatch: generation=%s chapter=%d stored=%q current=%q；已保留旧观察和提案。请用生成这些工件的原版 novel-studio 恢复，或通过 --pipeline --stages preplan,project-all,seal 在合法弧边界创建新 generation；已封存弧使用 successor/rebase 流程，不要手改 JSON", generationID, storedChapter, stored, current)
		}
		if protocol == domain.CharacterAgentDecisionProtocolV2Version && !domain.HasCharacterSourceRefPolicyV2(stimulus.Sources) {
			return fmt.Errorf("character-agent source-ref policy mismatch: generation=%s chapter=%d；已保留旧观察和提案。请通过 --pipeline --stages preplan,project-all,seal 在合法弧边界创建新 generation，不要重签或复用旧观察", generationID, storedChapter)
		}
		if protocol == domain.CharacterAgentDecisionProtocolV2Version && !domain.HasCharacterSelfExperiencePolicyV2(stimulus.Sources) {
			return fmt.Errorf("character-agent self-experience policy mismatch: generation=%s chapter=%d；旧观察保留审计，请通过 --pipeline 在合法边界创建新 generation，不得重签复用", generationID, storedChapter)
		}
		return nil
	}
	// LoadStimulus validates the generation path before using it for the
	// read-only inventory below. Do not infer a fresh protocol from a missing
	// first-chapter receipt: inspect every existing chapter in this generation,
	// including the latest surviving evidence after an interrupted recovery.
	stimulus, err := st.CharacterAgents.LoadStimulus(generationID, chapter)
	if err != nil {
		return err
	}
	if stimulus != nil {
		if err := check(stimulus, chapter); err != nil {
			return err
		}
	}
	root := filepath.Join(st.Dir(), "meta", "character_agents", "projected", generationID, "chapters")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		storedChapter, parseErr := strconv.Atoi(entry.Name())
		if !entry.IsDir() || parseErr != nil || storedChapter <= 0 || entry.Name() != fmt.Sprintf("%06d", storedChapter) {
			continue
		}
		if storedChapter == chapter && stimulus != nil {
			continue
		}
		stored, err := st.CharacterAgents.LoadStimulus(generationID, storedChapter)
		if err != nil {
			return err
		}
		if err := check(stored, storedChapter); err != nil {
			return err
		}
	}
	return nil
}

func selectCharacterAgentRoster(st *store.Store, chapter int, boundary ProjectedArcBoundary) ([]characterAgentProfile, string, error) {
	characters, err := st.Characters.Load()
	if err != nil {
		return nil, "", err
	}
	dossiers, _ := st.LoadAllCharacterDossiers()
	continuity, _ := st.LoadCharacterContinuityLedger()
	agendas, _ := st.WorldSim.LoadAgendaLedger()
	dossierByName := map[string]*domain.CharacterDossier{}
	for i := range dossiers {
		dossierByName[agentIdentityKey(dossiers[i].Character)] = &dossiers[i]
	}
	continuityByName := map[string]*domain.CharacterContinuityEntry{}
	if continuity != nil {
		for i := range continuity.Entries {
			continuityByName[agentIdentityKey(continuity.Entries[i].Name)] = &continuity.Entries[i]
		}
	}
	agendaByName := map[string]*domain.CharacterAgenda{}
	for i := range agendas.Agendas {
		agendaByName[agentIdentityKey(agendas.Agendas[i].Name)] = &agendas.Agendas[i]
	}
	arcText := boundary.Title + "\n" + boundary.Goal
	for ch := boundary.FirstChapter; ch <= boundary.LastChapter; ch++ {
		entry, _ := st.Outline.GetChapterOutline(ch)
		if entry != nil {
			arcText += "\n" + outlineAgentText(*entry)
		}
	}
	protagonist := ""
	profiles := make([]characterAgentProfile, 0)
	seen := map[string]struct{}{}
	add := func(character domain.Character) {
		name := strings.TrimSpace(character.Name)
		if name == "" || characterAgentIsCrowdOrDecorative(character) {
			return
		}
		key := agentIdentityKey(name)
		if _, ok := seen[key]; ok {
			return
		}
		isProtagonist := characterAgentIsProtagonist(character)
		if isProtagonist && protagonist == "" {
			protagonist = name
		}
		tier := strings.TrimSpace(character.Tier)
		if tier == "" {
			tier = "important"
		}
		character.Tier = tier
		mentioned := agentCharacterMentioned(arcText, character.Name, character.Aliases)
		due := false
		if agenda := agendaByName[key]; agenda != nil && (agenda.Status == "" || agenda.Status == "active" || agenda.Status == "blocked") {
			due = true
		}
		if item := continuityByName[key]; item != nil && item.ReturnPlan.SuggestedChapter > 0 && item.ReturnPlan.SuggestedChapter <= boundary.LastChapter {
			due = true
		}
		if !isProtagonist && tier != "core" && !(tier == "important" && (mentioned || due)) {
			return
		}
		seen[key] = struct{}{}
		profiles = append(profiles, characterAgentProfile{
			Character: character, Dossier: dossierByName[key], Continuity: continuityByName[key], Agenda: agendaByName[key],
		})
	}
	for _, character := range characters {
		add(character)
	}
	for _, dossier := range dossiers {
		if _, ok := seen[agentIdentityKey(dossier.Character)]; ok {
			continue
		}
		add(domain.Character{Name: dossier.Character, Aliases: dossier.Aliases, Role: dossier.Role, Description: dossier.Profile.Description, Arc: dossier.Profile.Arc, Traits: dossier.Profile.Traits, Tier: dossier.Tier})
	}
	if protagonist == "" && len(profiles) > 0 {
		protagonist = profiles[0].Character.Name
	}
	if len(profiles) == 0 || protagonist == "" {
		return nil, "", fmt.Errorf("character-agent roster has no protagonist")
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Character.Name < profiles[j].Character.Name })
	return profiles, protagonist, nil
}

func characterAgentIsCrowdOrDecorative(character domain.Character) bool {
	if characterAgentIsProtagonist(character) {
		return false
	}
	tier := strings.ToLower(strings.TrimSpace(character.Tier))
	if tier == "decorative" || tier == "secondary" {
		return true
	}
	role := strings.TrimSpace(character.Role)
	if domain.IsCrowdRoleLabel(character.Name) || domain.IsCrowdRoleLabel(role) {
		return true
	}
	for _, marker := range []string{"群众", "群演", "路人", "背景角色", "装饰性角色"} {
		if strings.Contains(role, marker) {
			return true
		}
	}
	return false
}

func buildWorldStimulus(st *store.Store, generationID string, chapter int, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string, now string, protocols ...string) (domain.WorldStimulusPacket, error) {
	packet, err := buildWorldStimulusDraft(st, generationID, chapter, boundary, projected, sources, now, protocols...)
	if err != nil {
		return packet, err
	}
	return domain.FinalizeWorldStimulusPacket(packet)
}

// Host-only two-phase assembly lets a new policy install its exact session
// binding before finalization. No unfinalized packet is sent to a model/store.
func buildWorldStimulusDraft(st *store.Store, generationID string, chapter int, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string, now string, protocols ...string) (domain.WorldStimulusPacket, error) {
	packet := domain.WorldStimulusPacket{
		Version: domain.WorldStimulusPacketVersion, GenerationID: generationID, Chapter: chapter,
		TimeWindow: fmt.Sprintf("chapter-%06d", chapter), Sources: sources, GeneratedAt: now,
	}
	if len(protocols) > 0 && protocols[0] == domain.CharacterAgentDecisionProtocolV2Version {
		physical, err := loadCharacterPhysicalPreState(st, chapter, projected)
		if err != nil {
			return packet, err
		}
		physical, err = domain.PrepareCharacterSelfExperienceStateV2(physical)
		if err != nil {
			return packet, err
		}
		packet.Version = domain.WorldStimulusPacketV2Version
		packet.PhysicalState = &physical
		packet.Sources = append(packet.Sources, domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.PlanGroundingPolicyV1)
	}
	clock, clockSources, err := buildCharacterStoryClock(st, chapter, projected)
	if err != nil {
		return packet, err
	}
	packet.StoryClock = clock
	packet.Sources = append(packet.Sources, clockSources...)
	if clock != nil {
		packet.TimeWindow = characterStoryClockText(clock.CurrentDay)
	}
	if entry, _ := st.Outline.GetChapterOutline(chapter); entry != nil {
		packet.SoftGuidance = append(packet.SoftGuidance, entry.Title, entry.CoreEvent)
		packet.SoftGuidance = append(packet.SoftGuidance, entry.Scenes...)
	}
	packet.SoftGuidance = append(packet.SoftGuidance, "arc_goal: "+boundary.Goal)
	if compass, _ := st.Outline.LoadCompass(); compass != nil {
		packet.HardContracts = append(packet.HardContracts, compass.NonNegotiables...)
		if compass.EndingDirection != "" {
			packet.HardContracts = append(packet.HardContracts, "ending_direction: "+compass.EndingDirection)
		}
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return packet, fmt.Errorf("load world rules for character agents: %w", err)
	}
	if len(rules) > 0 {
		for _, rule := range rules {
			text := strings.TrimSpace(rule.Rule + "；边界：" + rule.Boundary)
			packet.HardContracts = append(packet.HardContracts, text)
			// Visibility describes an authored rule, not permission to reveal
			// its entire narrative contract. Only the explicit character-facing
			// view can become shared character knowledge; keep the complete
			// original exclusively in the Arbiter's hard contracts.
			view := strings.TrimSpace(rule.CharacterView)
			visibility, public := characterViewVisibility(rule.Visibility)
			if view != "" && public {
				packet.PublicFacts = append(packet.PublicFacts, newCharacterAgentFact(characterWorldRuleViewFactKind, view, "world_rules.json", visibility))
			}
		}
	}
	codex, err := st.LoadWorldCodex()
	if err != nil {
		return packet, fmt.Errorf("load world codex for character agents: %w", err)
	}
	world, err := st.World.LoadBookWorld()
	if err != nil {
		return packet, fmt.Errorf("load book world for character agents: %w", err)
	}
	report, err := st.LoadWorldCoherenceReport()
	if err != nil {
		return packet, fmt.Errorf("load world coherence report for character agents: %w", err)
	}
	strictWorld := (codex != nil && codex.SchemaVersion >= domain.CurrentWorldCodexSchemaVersion) ||
		(world != nil && world.Version >= domain.CurrentBookWorldSchemaVersion)
	if report == nil && strictWorld {
		return packet, fmt.Errorf("world coherence report is required for v2 character-agent arbitration")
	}
	if report != nil {
		if err := domain.VerifyWorldCoherenceReport(*report, rules, codex, world); err != nil {
			return packet, fmt.Errorf("verify world coherence before character-agent arbitration: %w", err)
		}
		if !report.Ready {
			return packet, fmt.Errorf("world coherence report is not ready")
		}
		packet.WorldCoherenceDigest = report.ReportDigest
	}
	if codex != nil {
		packet.Mechanisms = append([]domain.CodexMechanism(nil), codex.Mechanisms...)
		packet.CounterfactualTests = append([]domain.CodexCounterfactualProbe(nil), codex.CounterfactualTests...)
	}
	packet.OperationalWorld = characterAgentOperationalWorld(world)
	for _, fact := range projected.CumulativeState {
		text := strings.TrimSpace(fmt.Sprintf("%s的%s=%s", fact.Subject, fact.Field, fact.Value))
		packet.CurrentEvents = append(packet.CurrentEvents, newCharacterAgentFact("projected_state", text, fact.StableID, "arbiter"))
	}
	return packet, nil
}

func characterAgentOperationalWorld(world *domain.BookWorld) *domain.WorldOperationalState {
	if world == nil {
		return nil
	}
	state := &domain.WorldOperationalState{
		Version: world.Version,
		Name:    strings.TrimSpace(world.Name),
		Routes:  append([]domain.WorldRoute(nil), world.Routes...),
	}
	for _, place := range world.Places {
		state.Places = append(state.Places, domain.WorldOperationalPlace{
			ID: strings.TrimSpace(place.ID), Name: strings.TrimSpace(place.Name),
			Rules: append([]string(nil), place.Rules...), Factions: append([]string(nil), place.Factions...), Tags: append([]string(nil), place.Tags...),
		})
	}
	for _, faction := range world.Factions {
		entry := domain.WorldOperationalFaction{
			ID: strings.TrimSpace(faction.ID), Name: strings.TrimSpace(faction.Name),
			Aliases: append([]string(nil), faction.Aliases...), Goal: strings.TrimSpace(faction.Goal),
			Resources: append([]string(nil), faction.Resources...), Relations: append([]domain.FactionRelation(nil), faction.Relations...),
			Stance: strings.TrimSpace(faction.Stance), InternalTension: strings.TrimSpace(faction.InternalTension),
		}
		if faction.Clock != nil {
			clock := *faction.Clock
			entry.Clock = &clock
		}
		state.Factions = append(state.Factions, entry)
	}
	return state
}

const characterWorldRuleViewFactKind = "world_rule_character_view"

func characterViewVisibility(value string) (string, bool) {
	visibility := strings.ToLower(strings.TrimSpace(value))
	if visibility == "" {
		// Explicit views keep the legacy domain default of formal. Missing
		// views never reach this publication path, and unknown nonempty labels
		// must not inherit that permissive legacy default.
		visibility = "formal"
	}
	return visibility, visibility == "formal" || visibility == "informal"
}

// Rebuild the public object from the explicit view. Copying the authored
// mechanism and blanking effects is insufficient: secrets or planned outcomes
// can also appear in names, prerequisites, input names and failure conditions.
func characterFacingMechanism(mechanism domain.CodexMechanism) (domain.CodexMechanism, bool) {
	visibility, public := characterViewVisibility(mechanism.Visibility)
	view := mechanism.CharacterView
	if view == nil || strings.TrimSpace(view.Name) == "" || !public {
		return domain.CodexMechanism{}, false
	}
	return domain.CodexMechanism{
		ID: mechanism.ID, Name: view.Name, Visibility: visibility,
		ActorScope:    append([]string(nil), view.ActorScope...),
		Trigger:       view.Trigger,
		Preconditions: append([]string(nil), view.Preconditions...),
		Inputs:        append([]string(nil), view.Inputs...),
		Costs:         append([]string(nil), view.Costs...),
		Effects:       append([]string(nil), view.Effects...),
		FailureModes:  append([]string(nil), view.FailureModes...),
		Observability: append([]string(nil), view.Observability...),
		Timing:        view.Timing,
	}, true
}

func buildCharacterObservation(st *store.Store, generationID string, chapter int, profile characterAgentProfile, stimulus domain.WorldStimulusPacket, projected domain.ProjectedPlanningContextV2, now string) (domain.CharacterObservationPacket, error) {
	observation, err := buildCharacterObservationDraft(st, generationID, chapter, profile, stimulus, projected, now)
	if err != nil {
		return observation, err
	}
	return domain.FinalizeCharacterObservationPacket(observation)
}

// A cycle-aware caller must bind its current host clock before finalizing:
// prior-chapter self evaluations cannot be authorized by a nil cycle context.
func buildCharacterObservationDraft(st *store.Store, generationID string, chapter int, profile characterAgentProfile, stimulus domain.WorldStimulusPacket, projected domain.ProjectedPlanningContextV2, now string) (domain.CharacterObservationPacket, error) {
	physicalV2 := stimulus.Version == domain.WorldStimulusPacketV2Version
	observation := domain.CharacterObservationPacket{
		Version: domain.CharacterObservationVersion, GenerationID: generationID, Chapter: chapter, Round: 1,
		AgentID: profile.Record.AgentID, Character: profile.Character.Name, Tier: profile.Character.Tier,
		TimeWindow: stimulus.TimeWindow, StimulusDigest: stimulus.Digest, GeneratedAt: now,
		Sources: compactAgentStrings(append([]string{"characters.json"}, stimulus.Sources...)),
	}
	selfProfile := strings.TrimSpace(strings.Join(compactAgentStrings([]string{
		"身份：" + profile.Character.Name,
		"角色：" + profile.Character.Role,
		"特征：" + strings.Join(profile.Character.Traits, "、"),
	}), "；"))
	observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("self_profile", selfProfile, "characters.json", "private"))
	if initial := profile.Character.InitialState; initial != nil && chapter == 1 {
		if stimulus.StoryClock == nil {
			observation.TimeWindow = firstAgentText(initial.Time, observation.TimeWindow)
		} else if initial.Time != "" {
			observation.TimeWindow = initial.Time + "；" + observation.TimeWindow
		}
		observation.Location = initial.Location
		observation.CurrentGoal = firstAgentText(initial.CurrentGoal, initial.CurrentAction)
		observation.Pressure = initial.Pressure
		observation.Resources = append([]string(nil), initial.Resources...)
		observation.Relationships = append([]string(nil), initial.Relationships...)
		observation.Commitments = append([]string(nil), initial.Commitments...)
		for _, fact := range initial.KnownFacts {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("initial_known", fact, "characters.json#initial_state", "private"))
		}
		if strings.TrimSpace(initial.CurrentAction) != "" {
			observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("own_current_action", initial.CurrentAction, "characters.json#initial_state", "private"))
		}
	}
	// Story-start dossiers are a legacy chapter-one fallback, never a source
	// that resets the actor to starting resources/location on later chapters.
	if profile.Dossier != nil && profile.Character.InitialState == nil && chapter == 1 {
		dossier := profile.Dossier
		observation.Location = dossier.CurrentAtStoryStart.Location
		observation.CurrentGoal = firstAgentText(dossier.CurrentAtStoryStart.NextIndependentMove, dossier.CurrentAtStoryStart.CurrentAction)
		observation.Pressure = firstAgentText(dossier.CurrentAtStoryStart.Pressure, "按自身目标与已知边界行动")
		for _, resource := range dossier.Resources {
			observation.Resources = append(observation.Resources, strings.TrimSpace(resource.Name+" "+resource.Status))
		}
		for _, relation := range dossier.Relationships {
			observation.Relationships = append(observation.Relationships, strings.TrimSpace(relation.Other+"："+relation.CurrentTie+"；"+relation.DebtOrTrust))
		}
		for _, anchor := range dossier.LifeAnchors {
			if anchor.Obligation != "" {
				observation.Commitments = append(observation.Commitments, anchor.Obligation)
			}
		}
		for _, fact := range compactAgentStrings(dossier.KnownFactsAtStoryStart) {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("initial_known", fact, "character_dossier#known_facts_at_story_start", "private"))
		}
	}
	if profile.Continuity != nil && (profile.Character.InitialState == nil || chapter > 1 || profile.Continuity.LastSeenChapter > 0) {
		dynamics := profile.Continuity.Dynamics
		observation.CurrentGoal = firstAgentText(dynamics.CurrentGoal, observation.CurrentGoal)
		observation.Pressure = firstAgentText(dynamics.PrimaryPressure, observation.Pressure, "按自身利益行动")
		if !physicalV2 && (chapter > 1 || dynamics.Resources != nil) {
			observation.Resources = append([]string(nil), dynamics.Resources...)
		}
		if chapter > 1 || dynamics.RelationshipForces != nil {
			observation.Relationships = append([]string(nil), dynamics.RelationshipForces...)
		}
		for _, relation := range dynamics.RelationshipContract {
			if relation.Promise != "" {
				observation.Commitments = append(observation.Commitments, relation.Counterpart+"："+relation.Promise)
			}
		}
		for _, fact := range dynamics.KnowledgeLedger.KnownFacts {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("known", fact, "character_continuity", "private"))
		}
		for _, fact := range dynamics.KnowledgeLedger.Suspicions {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("suspicion", fact, "character_continuity", "private"))
		}
		for _, fact := range dynamics.KnowledgeLedger.FalseBeliefs {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("false_belief", fact, "character_continuity", "private"))
		}
		for _, fact := range profile.Continuity.CurrentFacts {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("self_state", fact, "character_continuity", "private"))
		}
	}
	if profile.Agenda != nil && (profile.Character.InitialState == nil || chapter > 1) {
		observation.CurrentGoal = firstAgentText(profile.Agenda.CurrentGoal, observation.CurrentGoal)
		observation.Pressure = firstAgentText(profile.Agenda.BlockedBy, profile.Agenda.Motivation, observation.Pressure)
		observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("own_agenda", profile.Agenda.CurrentGoal, "offscreen_agenda", "private"))
	}
	if observation.CurrentGoal == "" {
		observation.CurrentGoal = "维持自身处境并回应当前压力"
	}
	if observation.Pressure == "" {
		observation.Pressure = "信息与资源有限，必须自行权衡"
	}
	if observation.Location == "" {
		observation.Location = "当前位置未知"
	}
	for _, fact := range stimulus.PublicFacts {
		if fact.Kind == characterWorldRuleViewFactKind && (fact.Visibility == "formal" || fact.Visibility == "informal") {
			observation.PublicRules = append(observation.PublicRules, fact)
		}
	}
	for _, mechanism := range stimulus.Mechanisms {
		if publicView, ok := characterFacingMechanism(mechanism); ok {
			observation.PublicMechanisms = append(observation.PublicMechanisms, publicView)
		}
	}
	for _, fact := range projected.CumulativeState {
		if physicalV2 {
			continue
		} // No arbitrary world StateAfter/encoded balance becomes owner knowledge.
		if agentIdentityKey(fact.Subject) != agentIdentityKey(profile.Character.Name) {
			continue
		}
		text := strings.TrimSpace(fmt.Sprintf("自身%s=%s", fact.Field, fact.Value))
		observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("projected_self_state", text, fact.StableID, "private"))
		switch strings.ToLower(strings.TrimSpace(fact.Field)) {
		case "location", "current_location":
			observation.Location = fact.Value
		case "current_goal", "goal":
			observation.CurrentGoal = fact.Value
		case "pressure", "primary_pressure":
			observation.Pressure = fact.Value
		}
	}
	for _, transition := range projected.RecentTransitions {
		if physicalV2 {
			continue
		} // v2 knowledge travels only through its explicit perception projection.
		for _, mutations := range [][]domain.StateMutationV2{transition.Delta.CharacterState, transition.Delta.Resources, transition.Delta.Relationships, transition.Delta.Knowledge} {
			for _, mutation := range mutations {
				if agentIdentityKey(mutation.Subject) != agentIdentityKey(profile.Character.Name) && agentIdentityKey(mutation.Object) != agentIdentityKey(profile.Character.Name) {
					continue
				}
				text := strings.TrimSpace(fmt.Sprintf("%s的%s从%s变为%s", mutation.Subject, mutation.Field, mutation.Before, mutation.After))
				observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("received_change", text, mutation.StableID, "private"))
			}
		}
	}
	memory, err := st.CharacterAgents.LoadProjectedMemory(generationID, profile.Record.AgentID)
	if err != nil {
		return observation, err
	}
	if memory != nil {
		for _, fact := range memory.Facts {
			if profile.Dossier != nil && legacyKnowledgeBoundarySeed(fact, profile.Record.AgentID, profile.Dossier.KnowledgeBoundary) {
				continue
			}
			observation.Memory = append(observation.Memory, fact)
		}
		observation.MemoryRoot = memory.MemoryRoot
	}
	if physicalV2 {
		if err := applyCharacterPhysicalObservation(&observation, profile, stimulus); err != nil {
			return observation, err
		}
	}
	return observation, nil
}

// Old canon migration incorrectly treated the author's knowledge-boundary
// paragraph as a positive character memory. Filter only that exact, provably
// migration-created fact in fresh observations; never rewrite historical
// memory, strip words, or suppress a character's legitimate known secrets.
func legacyKnowledgeBoundarySeed(fact domain.CharacterAgentMemoryFact, agentID, boundary string) bool {
	boundary = strings.TrimSpace(boundary)
	if boundary == "" || fact.Kind != "accepted_continuity" || fact.Text != boundary {
		return false
	}
	digest := sha256.Sum256([]byte("character-agent-memory-migration.v1\x00" + agentID + "\x00" + boundary))
	return fact.ID == "mem_"+hex.EncodeToString(digest[:8]) && fact.SourceDigest == "sha256:"+hex.EncodeToString(digest[:])
}

func characterActivationReasons(st *store.Store, profile characterAgentProfile, protagonist string, chapter int, projected domain.ProjectedPlanningContextV2) []string {
	var reasons []string
	if agentIdentityKey(profile.Character.Name) == agentIdentityKey(protagonist) {
		reasons = append(reasons, "protagonist")
	}
	if entry, _ := st.Outline.GetChapterOutline(chapter); entry != nil && agentCharacterMentioned(outlineAgentText(*entry), profile.Character.Name, profile.Character.Aliases) {
		reasons = append(reasons, "scene_appearance")
	}
	if profile.Agenda != nil && (profile.Agenda.Status == "" || profile.Agenda.Status == "active" || profile.Agenda.Status == "blocked") && profile.Agenda.LastAdvancedChapter < chapter {
		reasons = append(reasons, "due_action")
	}
	if profile.Continuity != nil {
		plan := profile.Continuity.ReturnPlan
		if plan.SuggestedChapter > 0 && plan.SuggestedChapter <= chapter && (plan.ReturnPriority == "required" || plan.ReturnPriority == "near_future") {
			reasons = append(reasons, "return_due")
		}
		if len(profile.Continuity.Dynamics.RelationshipForces) > 0 {
			for _, use := range profile.Continuity.FutureUses {
				if use.Chapter == chapter {
					reasons = append(reasons, "relationship_or_promise_trigger")
					break
				}
			}
		}
	}
	nameKey := agentIdentityKey(profile.Character.Name)
	for _, transition := range projected.RecentTransitions {
		groups := []struct {
			kind      string
			mutations []domain.StateMutationV2
		}{
			{"resource_change", transition.Delta.Resources},
			{"relationship_change", transition.Delta.Relationships},
			{"received_information", transition.Delta.Knowledge},
		}
		for _, group := range groups {
			for _, mutation := range group.mutations {
				if agentIdentityKey(mutation.Subject) == nameKey || agentIdentityKey(mutation.Object) == nameKey {
					reasons = append(reasons, group.kind)
					break
				}
			}
		}
	}
	for _, obligation := range projected.OpenObligations {
		if obligation.DueNow && agentCharacterMentioned(obligation.Contract, profile.Character.Name, profile.Character.Aliases) {
			reasons = append(reasons, "commitment_trigger")
		}
	}
	return compactAgentStrings(reasons)
}

func ensureCharacterAgentMemory(st *store.Store, generationID string, profile characterAgentProfile, chapter int, now string) error {
	canonical, err := st.CharacterAgents.LoadCanonicalMemory(profile.Record.AgentID)
	if err != nil {
		return err
	}
	if canonical == nil {
		memory := domain.CharacterAgentMemory{
			Version: domain.CharacterAgentMemoryVersion, AgentID: profile.Record.AgentID,
			Character: profile.Character.Name, State: "canonical", UpdatedAt: now,
		}
		seenFacts := make(map[string]bool)
		add := func(chapter int, kind, text, source string) {
			text = strings.TrimSpace(text)
			if text == "" || seenFacts[text] {
				return
			}
			seenFacts[text] = true
			memory.Facts = append(memory.Facts, newCharacterMemoryFact(chapter, kind, text, source, true))
		}
		if profile.Dossier != nil {
			for _, event := range profile.Dossier.PreStoryTimeline {
				add(max(1, chapter-1), "pre_story", event.Event, "character_dossier")
			}
			for _, fact := range profile.Dossier.KnownFactsAtStoryStart {
				add(max(1, chapter-1), "known_at_story_start", fact, "character_dossier#known_facts_at_story_start")
			}
		}
		if profile.Continuity != nil {
			memory.LastAcceptedChapter = profile.Continuity.LastSeenChapter
			for _, fact := range profile.Continuity.CurrentFacts {
				add(max(1, profile.Continuity.LastSeenChapter), "accepted_state", fact, "character_continuity")
			}
		}
		finalized, finalizeErr := domain.FinalizeCharacterAgentMemory(memory)
		if finalizeErr != nil {
			return finalizeErr
		}
		if err := st.CharacterAgents.SaveCanonicalMemory(finalized); err != nil {
			return err
		}
		canonical = &finalized
	}
	projected, err := st.CharacterAgents.LoadProjectedMemory(generationID, profile.Record.AgentID)
	if err != nil {
		return err
	}
	if projected != nil {
		return nil
	}
	clone := *canonical
	clone.State = "projected"
	clone.GenerationID = generationID
	clone.UpdatedAt = now
	clone.MemoryRoot = ""
	return st.CharacterAgents.SaveProjectedMemory(clone)
}

func runCharacterProposalRound(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, observations map[string]domain.CharacterObservationPacket, agentIDs []string, round int, sessions ...*domain.CharacterActivationSession) ([]domain.CharacterDecisionProposal, error) {
	model := models.ForRole("character")
	if model == nil {
		return nil, fmt.Errorf("character model is unavailable")
	}
	return runCharacterProposalRoundWithModel(ctx, cfg, st, model, observations, agentIDs, round, sessions...)
}

func runCharacterProposalRoundWithModel(ctx context.Context, cfg bootstrap.Config, st *store.Store, model agentcore.ChatModel, observations map[string]domain.CharacterObservationPacket, agentIDs []string, round int, sessions ...*domain.CharacterActivationSession) ([]domain.CharacterDecisionProposal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	proofs, cycleSession, err := characterExecutionProofs(st, sessions...)
	if err != nil {
		return nil, err
	}
	requestedIDs := make([]string, 0, len(agentIDs))
	pending := make([]domain.CharacterObservationPacket, 0, len(agentIDs))
	seen := make(map[string]bool, len(agentIDs))
	var first domain.CharacterObservationPacket
	var sourceStimulus *domain.WorldStimulusPacket
	var v3View *store.CharacterArbitrationV3
	// Finish identity and persisted-evidence checks before starting any model
	// calls. A late corrupt proposal must not leave earlier goroutines writing
	// after this function has already returned to the generation coordinator.
	for _, agentID := range agentIDs {
		if seen[agentID] {
			continue
		}
		seen[agentID] = true
		observation, ok := observations[agentID]
		if !ok || observation.Round != round || observation.AgentID != agentID {
			return nil, fmt.Errorf("character agent %s has no matching observation for round %d", agentID, round)
		}
		if len(requestedIDs) == 0 {
			first = observation
			if hasCharacterActivationPolicyV3(observation.Sources) {
				v3View, err = st.LoadCharacterArbitrationV3(observation.GenerationID, observation.Chapter)
				if err != nil {
					return nil, err
				}
			}
			if observation.Version == domain.CharacterObservationV2Version {
				var err error
				sourceStimulus, err = proofs.LoadStimulus(first.GenerationID, first.Chapter)
				if err != nil {
					return nil, err
				}
			}
		} else if observation.GenerationID != first.GenerationID || observation.Chapter != first.Chapter {
			return nil, fmt.Errorf("character proposal round mixes generations or chapters")
		}
		verified, err := domain.FinalizeCharacterObservationPacket(observation)
		if err != nil || verified.Digest != observation.Digest {
			return nil, fmt.Errorf("character agent %s observation is invalid or has changed", agentID)
		}
		if hasCharacterActivationPolicyV3(observation.Sources) {
			if err := validateCharacterDispatchV3(st, cycleSession, v3View, observation); err != nil {
				return nil, err
			}
		}
		if observation.Version == domain.CharacterObservationV2Version && (sourceStimulus != nil || domain.HasCharacterSourceRefPolicyV2(observation.Sources)) {
			if sourceStimulus == nil {
				return nil, fmt.Errorf("character agent %s lacks its source-bound world stimulus", agentID)
			}
			if err := domain.ValidateCharacterResourceViewsAgainstStimulusV2(*sourceStimulus, observation); err != nil {
				return nil, fmt.Errorf("character agent %s source visibility is invalid: %w", agentID, err)
			}
		}
		persisted, err := proofs.LoadObservation(observation.GenerationID, observation.Chapter, round, agentID)
		if err != nil {
			return nil, err
		}
		if persisted == nil || persisted.Digest != observation.Digest {
			return nil, fmt.Errorf("character agent %s observation is not bound to persisted evidence", agentID)
		}
		requestedIDs = append(requestedIDs, agentID)
		existing, err := proofs.LoadProposal(observation.GenerationID, observation.Chapter, round, agentID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.ObservationDigest != observation.Digest {
				return nil, fmt.Errorf("character agent %s proposal is bound to a different observation", agentID)
			}
			continue
		}
		pending = append(pending, observation)
	}
	if len(requestedIDs) == 0 {
		return nil, nil
	}
	// This capability is created only after this pool has authenticated every
	// pending owner's exact observation and admission. It never leaves this
	// dispatch or replaces the Store's write-time refresh/source checks.
	var dispatchView *characterDispatchViewV3
	if v3View != nil {
		dispatchView = &characterDispatchViewV3{view: v3View, sessionDigest: cycleSession.Digest, round: round, observations: make(map[string]string, len(pending))}
		for _, observation := range pending {
			dispatchView.observations[observation.AgentID] = observation.Digest
		}
	}
	limit := cfg.CharacterAgents.MaxConcurrency
	if limit <= 0 {
		limit = 4
	} else if limit > 4 {
		limit = 4
	}
	jobs := make(chan domain.CharacterObservationPacket)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	// A fixed worker pool bounds goroutines as well as provider concurrency;
	// large casts do not allocate one waiting goroutine per character.
	for range min(limit, len(pending)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for observation := range jobs {
				if runCtx.Err() != nil {
					return
				}
				if err := runOneCharacterAgentWithDispatchView(runCtx, cfg, st, model, observation, dispatchView, cycleSession); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					mu.Unlock()
					return
				}
			}
		}()
	}
dispatch:
	for _, observation := range pending {
		select {
		case jobs <- observation:
		case <-runCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	proposals, err := proofs.LoadProposals(first.GenerationID, first.Chapter, round, requestedIDs)
	if err != nil {
		return nil, err
	}
	if len(proposals) != len(requestedIDs) {
		return nil, fmt.Errorf("character proposal round %d is incomplete: got %d of %d decisions", round, len(proposals), len(requestedIDs))
	}
	return proposals, nil
}

func runOneCharacterAgent(ctx context.Context, cfg bootstrap.Config, st *store.Store, model agentcore.ChatModel, observation domain.CharacterObservationPacket, sessions ...*domain.CharacterActivationSession) error {
	return runOneCharacterAgentWithDispatchView(ctx, cfg, st, model, observation, nil, sessions...)
}

// Private, non-serializable authority held only by one proposal pool. A caller
// cannot turn JSON, a digest, or a zero Store view into an authenticated view.
type characterDispatchViewV3 struct {
	view          *store.CharacterArbitrationV3
	sessionDigest string
	round         int
	observations  map[string]string
}

func (d *characterDispatchViewV3) validate(st *store.Store, session *domain.CharacterActivationSession, observation domain.CharacterObservationPacket) error {
	if d == nil || d.view == nil || !d.view.BelongsTo(st) || session == nil || d.sessionDigest != session.Digest || d.round != observation.Round || d.observations[observation.AgentID] != observation.Digest || observation.Digest == "" {
		return fmt.Errorf("character worker lacks its exact pool-owned dispatch view")
	}
	input := d.view.Input()
	if input.Stimulus.SelfEvaluationContext == nil || input.Stimulus.SelfEvaluationContext.SessionDigest != session.Digest || input.Stimulus.Digest != observation.StimulusDigest || input.Stimulus.GenerationID != observation.GenerationID || input.Stimulus.Chapter != observation.Chapter {
		return fmt.Errorf("character worker dispatch view differs from its frozen input/session")
	}
	return nil
}

func runOneCharacterAgentWithDispatchView(ctx context.Context, cfg bootstrap.Config, st *store.Store, model agentcore.ChatModel, observation domain.CharacterObservationPacket, dispatch *characterDispatchViewV3, sessions ...*domain.CharacterActivationSession) error {
	proofs, cycleSession, err := characterExecutionProofs(st, sessions...)
	if err != nil {
		return err
	}
	if hasCharacterActivationPolicyV3(observation.Sources) && cycleSession == nil {
		return fmt.Errorf("v3 character call cannot use one-shot execution")
	}
	tool := tools.NewSubmitCharacterDecisionTool(st, observation)
	if cycleSession != nil {
		if hasCharacterActivationPolicyV3(observation.Sources) {
			var view *store.CharacterArbitrationV3
			if dispatch != nil {
				if err := dispatch.validate(st, cycleSession, observation); err != nil {
					return err
				}
				view = dispatch.view
			} else {
				var loadErr error
				view, loadErr = st.LoadCharacterArbitrationV3(observation.GenerationID, observation.Chapter)
				if loadErr != nil {
					return loadErr
				}
			}
			if view == nil {
				return fmt.Errorf("v3 character call requires its prior immutable admission")
			}
			tool, err = tools.NewSubmitCharacterActivationV3DecisionTool(st, *cycleSession, observation, view)
		} else {
			tool, err = tools.NewSubmitCharacterActivationDecisionTool(st, *cycleSession, observation)
		}
		if err != nil {
			return err
		}
	}
	ctx, usageRecord, prepareErr := prepareCharacterAccounting(ctx, domain.CharacterAgentUsage{
		GenerationID: observation.GenerationID, Role: "character", AgentID: observation.AgentID, Character: observation.Character, Chapter: observation.Chapter, Round: observation.Round,
		Cycle: proofs.ActivationCycleIndex(),
	})
	if prepareErr != nil {
		return prepareErr
	}
	guardBlocks := 0
	guard := func(_ context.Context, _ agentcore.StopInfo) agentcore.StopDecision {
		proposal, _ := proofs.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
		if proposal != nil {
			return agentcore.StopDecision{Allow: true}
		}
		guardBlocks++
		if guardBlocks >= 2 {
			return agentcore.StopDecision{Escalate: true}
		}
		return agentcore.StopDecision{InjectMessage: "尚未提交决定。现在只调用 submit_character_decision。"}
	}
	raw, _ := json.Marshal(observation)
	characterPrompt := characterAgentSystemPrompt
	if observation.Version == domain.CharacterObservationV2Version {
		characterPrompt = characterAgentSystemPromptV2
		if domain.HasCharacterSelfExperiencePolicyV2(observation.Sources) {
			characterPrompt += characterSelfExperiencePromptV2
		}
		if domain.HasCharacterOperationalAvailabilityPolicyV1(observation.Sources) {
			characterPrompt += characterOperationalAvailabilityPromptV1
		}
		if domain.HasCharacterWorkArtifactPolicyV1(observation.Sources) {
			characterPrompt += characterWorkArtifactPromptV1
		}
		if domain.HasCharacterResourceObservationTimePolicyV1(observation.Sources) {
			characterPrompt += characterResourceObservationTimePromptV1
		}
		if domain.HasCharacterWorkContinuationHistoryPolicyV1(observation.Sources) {
			characterPrompt += characterCarryAPIHelpV1
		}
	}
	var executionTool agentcore.Tool = tool
	if domain.HasCharacterSelfChronologyPolicyV1(observation.Sources) {
		makeCodec := modelinput.NewScopedReferenceCodec
		if domain.HasCharacterWorkArtifactPolicyV1(observation.Sources) {
			makeCodec = modelinput.NewScopedArtifactReferenceCodecV1
		}
		codec, err := makeCodec(modelinput.KindCharacterObservation, observation)
		if err != nil {
			return err
		}
		executionTool, err = newScopedReferenceTool(tool, codec)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(codec.ModelView())
		if err != nil {
			return err
		}
		characterPrompt += scopedReferencePrompt + characterWorkContinuationPrompt
	}
	usage, err := runCharacterAgentTerminalLoop(
		withCharacterToolDiagnosticScope(ctx, usageRecord), model, characterPrompt,
		"你是 "+observation.Character+"。这是你唯一可见的观察包：\n<character_observation_packet>\n"+string(raw)+"\n</character_observation_packet>\n现在只调用 submit_character_decision。",
		executionTool, tool.Name(), cappedMaxTurns(cfg.ResolveMaxTurns("character", 6), 8), roleThinking(cfg, "character"), guard,
		characterCyclePromptCacheKey(agentPromptCacheKey("character", st.Dir(), observation.GenerationID, fmt.Sprint(observation.Chapter), fmt.Sprint(observation.Round), observation.AgentID), proofs),
		st,
	)
	proposal, loadErr := proofs.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err == nil {
		if loadErr != nil {
			err = loadErr
		} else if proposal == nil {
			err = fmt.Errorf("character agent %s did not submit a proposal", observation.AgentID)
		}
	}
	if loadErr == nil && proposal != nil {
		reportDurablePlanningProgress(ctx, DurablePlanningProgress{GenerationID: proposal.GenerationID, Chapter: proposal.Chapter, Cycle: proofs.ActivationCycleIndex(), Round: proposal.Round, Kind: PlanningProposalCommitted, ArtifactDigest: proposal.Digest})
	}
	return errors.Join(err, appendCharacterLoopUsage(st, usageRecord, usage, err, projectedAccounting(ctx).ImportCharacterUsage), projectedAccountingAfter(ctx))
}

func runCharacterAgentTerminalLoop(
	ctx context.Context,
	model agentcore.ChatModel,
	systemPrompt, prompt string,
	tool agentcore.Tool,
	terminalTool string,
	maxTurns int,
	thinking agentcore.ThinkingLevel,
	guard agentcore.StopGuard,
	promptCacheKey string,
	diagnosticStores ...*store.Store,
) (characterAgentLoopUsage, error) {
	inputMessage := agentcore.UserMsg(prompt)
	var packetKind modelinput.ExactAgentPacketKind
	switch terminalTool {
	case "submit_character_decision":
		packetKind = modelinput.KindCharacterObservation
	case "resolve_chapter_world":
		packetKind = modelinput.KindWorldArbitration
	case "submit_chapter_readiness":
		packetKind = modelinput.KindChapterReadiness
	}
	if packetKind != "" {
		var err error
		inputMessage, err = modelinput.NewExactAgentPacketMessage(packetKind, prompt)
		if err != nil {
			return characterAgentLoopUsage{}, err
		}
	}
	resolvedThinking, _ := ResolveThinkingForModel(model, thinking)
	maxTurns = characterArbiterDiagnosticTurnLimit(terminalTool, maxTurns)
	trackedModel := &characterUsageModel{ChatModel: model}
	var onMessage func(agentcore.AgentMessage)
	var loopModel agentcore.ChatModel = trackedModel
	if group, ok := ctx.Value(characterAccountingGroupKey{}).(domain.CharacterAgentUsage); ok {
		agentName := group.Role
		if group.Role == "character" {
			agentName = "character_" + group.AgentID
		}
		loopModel, onMessage = projectedAccountingModel(ctx, trackedModel, agentName, group.UsageID)
	}
	events := agentcore.AgentLoop(
		ctx,
		[]agentcore.AgentMessage{inputMessage},
		agentcore.AgentContext{SystemPrompt: systemPrompt, Tools: []agentcore.Tool{tool}},
		agentcore.LoopConfig{
			Model: loopModel, OnMessage: onMessage, MaxTurns: maxTurns, MaxRetries: subagentMaxRetries, MaxToolErrors: 0,
			ThinkingLevel: resolvedThinking, ToolsAreIdempotent: false, StopGuard: guard,
			CacheLastMessage: promptCacheControl, PromptCacheKey: promptCacheKey,
			StopAfterTool: func(name string) bool { return name == terminalTool },
		},
	)
	var usage characterAgentLoopUsage
	var runErr error
	diagnostics := newCharacterToolDiagnosticObserver(ctx, terminalTool, diagnosticStores)
	for event := range events {
		diagnostics.observe(event)
		if event.Type == agentcore.EventModelResponse {
			switch message := event.Message.(type) {
			case agentcore.Message:
				usage.observe(model, message.Usage, message.Metadata)
			case *agentcore.Message:
				usage.observe(model, message.Usage, message.Metadata)
			}
		}
		if event.Type == agentcore.EventError && event.Err != nil {
			usage.observeError(model, event.Err)
			runErr = event.Err
		}
		if event.Type == agentcore.EventRetry && event.RetryInfo != nil {
			usage.observeError(model, event.RetryInfo.Err)
		}
	}
	usage.finish(model, int(trackedModel.started.Load()))
	return usage, runErr
}

func runWorldArbitration(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, inputs characterAgentChapterInputs, proposals []domain.CharacterDecisionProposal) (*domain.WorldArbitrationReceipt, error) {
	proofs, cycleSession, err := characterExecutionProofs(st, inputs.CycleSession)
	if err != nil {
		return nil, err
	}
	model := models.ForRole("world_arbiter")
	if model == nil {
		return nil, fmt.Errorf("world arbiter model is unavailable")
	}
	round := 1
	for _, proposal := range proposals {
		if proposal.Round > round {
			round = proposal.Round
		}
	}
	if inputs.ArbitrationV3 != nil {
		if cycleSession == nil || !hasCharacterActivationPolicyV3(inputs.Stimulus.Sources) || inputs.ArbitrationRoundV3 < 1 || inputs.ArbitrationRoundV3 > 2 {
			return nil, fmt.Errorf("v3 arbiter requires its explicit current source coordinate")
		}
		round = inputs.ArbitrationRoundV3
		sources, err := inputs.ArbitrationV3.Sources(round)
		if err != nil {
			return nil, err
		}
		if !sameCharacterCycleValue(proposals, sources.EffectiveProposals()) {
			return nil, fmt.Errorf("v3 arbiter proposals differ from exact current sources")
		}
	} else if hasCharacterActivationPolicyV3(inputs.Stimulus.Sources) {
		return nil, fmt.Errorf("v3 arbiter cannot use a legacy unbound source path")
	}
	loadArbitration := func() (*domain.WorldArbitrationReceipt, error) {
		return proofs.LoadArbitration(inputs.Stimulus.GenerationID, inputs.Stimulus.Chapter, round)
	}
	if inputs.ContinuationProof != nil {
		loadArbitration = inputs.ContinuationProof.LoadArbitration
	}
	if inputs.ArbitrationV3 != nil {
		loadArbitration = func() (*domain.WorldArbitrationReceipt, error) { return inputs.ArbitrationV3.LoadArbitration(round) }
	}
	if existing, err := loadArbitration(); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	protocolDigest := CharacterAgentProtocolDigestForVersion(characterProtocolForStimulus(inputs.Stimulus))
	if cycleSession != nil {
		protocolDigest = characterActivationProtocolForStimulus(inputs.Stimulus)
	}
	tool := tools.NewResolveChapterWorldTool(st, inputs.Stimulus, inputs.Activation, proposals, protocolDigest, inputs.Sources, cfg.CharacterAgents.MaxRevisionRounds)
	if inputs.ArbitrationV3 != nil {
		tool, err = tools.NewResolveCharacterArbitrationV3Tool(st, *cycleSession, inputs.ArbitrationV3, round, protocolDigest)
		if err != nil {
			return nil, err
		}
	} else if inputs.ContinuationProof != nil {
		tool, err = tools.NewResolveCharacterContinuationTool(st, *cycleSession, inputs.ContinuationProof, protocolDigest)
		if err != nil {
			return nil, err
		}
	} else if cycleSession != nil {
		tool, err = tools.NewResolveCharacterActivationTool(st, *cycleSession, inputs.Stimulus, inputs.Activation, proposals, protocolDigest, inputs.Sources, cfg.CharacterAgents.MaxRevisionRounds)
		if err != nil {
			return nil, err
		}
	}
	ctx, usageRecord, prepareErr := prepareCharacterAccounting(ctx, domain.CharacterAgentUsage{
		GenerationID: inputs.Stimulus.GenerationID, Role: "world_arbiter", AgentID: "world_arbiter", Character: "World Arbiter",
		Chapter: inputs.Stimulus.Chapter, Round: round,
		Cycle: proofs.ActivationCycleIndex(),
	})
	if prepareErr != nil {
		return nil, prepareErr
	}
	guardBlocks := 0
	guard := func(_ context.Context, _ agentcore.StopInfo) agentcore.StopDecision {
		receipt, _ := loadArbitration()
		if receipt != nil {
			return agentcore.StopDecision{Allow: true}
		}
		guardBlocks++
		if guardBlocks >= 2 {
			return agentcore.StopDecision{Escalate: true}
		}
		return agentcore.StopDecision{InjectMessage: "尚未完成裁决。现在只调用 resolve_chapter_world。"}
	}
	arbiterPrompt, userPrompt, executionTool, err := prepareCharacterArbitrationRequest(inputs, proposals, tool)
	if err != nil {
		return nil, err
	}
	usage, err := runCharacterAgentTerminalLoop(
		withCharacterToolDiagnosticScope(ctx, usageRecord), model, arbiterPrompt,
		userPrompt,
		executionTool, tool.Name(), cappedMaxTurns(cfg.ResolveMaxTurns("world_arbiter", 6), 8), roleThinking(cfg, "world_arbiter"), guard,
		characterCyclePromptCacheKey(agentPromptCacheKey("world_arbiter", st.Dir(), inputs.Stimulus.GenerationID, fmt.Sprint(inputs.Stimulus.Chapter), fmt.Sprint(round)), proofs),
		st,
	)
	var receipt *domain.WorldArbitrationReceipt
	if err == nil {
		receipt, err = loadArbitration()
		if err == nil && receipt == nil {
			err = fmt.Errorf("world arbiter round %d did not persist a receipt", round)
		}
	}
	// The loop may fail after its terminal tool durably saved a valid result.
	// Observing that result does not change the original business error.
	observed, observeErr := receipt, error(nil)
	if observed == nil {
		observed, observeErr = loadArbitration()
	}
	if observeErr == nil && observed != nil {
		reportDurablePlanningProgress(ctx, DurablePlanningProgress{GenerationID: observed.GenerationID, Chapter: observed.Chapter, Cycle: proofs.ActivationCycleIndex(), Round: observed.Round, Kind: PlanningArbitrationCommitted, ArtifactDigest: observed.Digest})
	}
	if err := errors.Join(err, appendCharacterLoopUsage(st, usageRecord, usage, err, projectedAccounting(ctx).ImportCharacterUsage), projectedAccountingAfter(ctx)); err != nil {
		return nil, err
	}
	return receipt, nil
}

func updateProjectedCharacterAgentMemories(st *store.Store, generationID string, chapter int, proposals []domain.CharacterDecisionProposal, receipt domain.WorldArbitrationReceipt) error {
	var physical *domain.WorldPhysicalStateV2
	if receipt.Version == domain.WorldArbitrationReceiptV2Version {
		stimulus, err := st.CharacterAgents.LoadStimulus(generationID, chapter)
		if err != nil {
			return err
		}
		if stimulus == nil {
			return fmt.Errorf("v2 memory lacks source stimulus")
		}
		state, err := domain.ApplyArbitrationPhysicalStateV2(receipt, *stimulus, proposals...)
		if err != nil {
			return err
		}
		physical = &state
	}
	byAgent := make(map[string]domain.CharacterDecisionProposal, len(proposals))
	for _, proposal := range proposals {
		byAgent[proposal.AgentID] = proposal
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, resolution := range receipt.Resolutions {
		proposal := byAgent[resolution.AgentID]
		memory, err := st.CharacterAgents.LoadProjectedMemory(generationID, resolution.AgentID)
		if err != nil || memory == nil {
			return fmt.Errorf("load projected memory for %s: %w", resolution.AgentID, err)
		}
		text := strings.TrimSpace(proposal.Decision + "；" + resolution.ImmediateResult + "；" + resolution.StateAfter)
		if physical != nil {
			var privateErr error
			text, privateErr = domain.CharacterPrivateOutcomeV2(proposal, resolution, *physical, receipt)
			if privateErr != nil {
				return privateErr
			}
		}
		if err := st.AppendProjectedCharacterMemoryFact(generationID, resolution.AgentID, newCharacterMemoryFact(chapter, "projected_decision", text, receipt.Digest, false), now); err != nil {
			return err
		}
	}
	return nil
}

func arbitrationFeedbackByAgent(receipt domain.WorldArbitrationReceipt) map[string][]string {
	out := map[string][]string{}
	for _, conflict := range receipt.Conflicts {
		if conflict.Resolved {
			continue
		}
		for _, agentID := range conflict.AffectedAgentIDs {
			out[agentID] = append(out[agentID], conflict.Kind+"："+conflict.Feedback)
		}
	}
	return out
}

func activeCharacterAgentIDs(activation domain.CharacterAgentActivation) []string {
	var ids []string
	for _, entry := range activation.Entries {
		if entry.State == domain.CharacterAgentActive {
			ids = append(ids, entry.AgentID)
		}
	}
	sort.Strings(ids)
	return ids
}

func activeObservationAgentIDs(observations map[string]domain.CharacterObservationPacket) []string {
	ids := make([]string, 0, len(observations))
	for id := range observations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func characterAgentIsProtagonist(character domain.Character) bool {
	role := strings.ToLower(strings.TrimSpace(character.Role))
	return character.Tier == "core" || strings.Contains(role, "主角") || strings.Contains(role, "男主") || strings.Contains(role, "女主") || strings.Contains(role, "protagonist")
}

func agentCharacterMentioned(text, name string, aliases []string) bool {
	for _, candidate := range append([]string{name}, aliases...) {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func outlineAgentText(entry domain.OutlineEntry) string {
	return entry.Title + "\n" + entry.CoreEvent + "\n" + entry.Hook + "\n" + strings.Join(entry.Scenes, "\n")
}

func agentIdentityKey(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func newCharacterAgentFact(kind, text, source, visibility string) domain.CharacterAgentFact {
	text = strings.TrimSpace(text)
	sum := sha256.Sum256([]byte(kind + "\x00" + source + "\x00" + text))
	return domain.CharacterAgentFact{ID: "fact_" + hex.EncodeToString(sum[:8]), Kind: kind, Text: text, Source: source, Visibility: visibility}
}

func newCharacterMemoryFact(chapter int, kind, text, source string, accepted bool) domain.CharacterAgentMemoryFact {
	text = strings.TrimSpace(text)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", chapter, kind, source, text)))
	sourceDigest := source
	if !strings.HasPrefix(sourceDigest, "sha256:") {
		sourceSum := sha256.Sum256([]byte(source))
		sourceDigest = "sha256:" + hex.EncodeToString(sourceSum[:])
	}
	return domain.CharacterAgentMemoryFact{ID: "mem_" + hex.EncodeToString(sum[:8]), Chapter: chapter, Kind: kind, Text: text, SourceDigest: sourceDigest, Accepted: accepted}
}

func firstAgentText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func compactAgentStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func loadCharacterAgentEvidence(st *store.Store, simulation domain.ChapterWorldSimulation) (*domain.CharacterAgentEvidenceBundle, error) {
	if simulation.Version < 2 {
		return nil, nil
	}
	receipt := simulation.CharacterAgentProtocol
	if receipt == nil {
		return nil, fmt.Errorf("simulation v2 has no character-agent protocol receipt")
	}
	registry, err := st.CharacterAgents.LoadRegistrySnapshot(simulation.GenerationID, simulation.Chapter)
	if err != nil || registry == nil {
		return nil, fmt.Errorf("load registry snapshot: %w", err)
	}
	stimulus, err := st.CharacterAgents.LoadStimulus(simulation.GenerationID, simulation.Chapter)
	if err != nil || stimulus == nil {
		return nil, fmt.Errorf("load stimulus: %w", err)
	}
	activation, err := st.CharacterAgents.LoadActivation(simulation.GenerationID, simulation.Chapter)
	if err != nil || activation == nil {
		return nil, fmt.Errorf("load activation: %w", err)
	}
	activeIDs := make([]string, 0)
	for _, entry := range activation.Entries {
		if entry.State == domain.CharacterAgentActive {
			activeIDs = append(activeIDs, entry.AgentID)
		}
	}
	observations := make([]domain.CharacterObservationPacket, 0)
	proposals := make([]domain.CharacterDecisionProposal, 0)
	for round := 1; round <= receipt.ArbitrationRound; round++ {
		for _, agentID := range activeIDs {
			proposal, loadErr := st.CharacterAgents.LoadProposal(simulation.GenerationID, simulation.Chapter, round, agentID)
			if loadErr != nil {
				return nil, loadErr
			}
			if proposal == nil {
				continue
			}
			observation, loadErr := st.CharacterAgents.LoadObservation(simulation.GenerationID, simulation.Chapter, round, agentID)
			if loadErr != nil || observation == nil {
				return nil, fmt.Errorf("proposal %s round %d has no observation: %w", agentID, round, loadErr)
			}
			proposals = append(proposals, *proposal)
			observations = append(observations, *observation)
		}
	}
	arbitrations := make([]domain.WorldArbitrationReceipt, 0, receipt.ArbitrationRound)
	for round := 1; round <= receipt.ArbitrationRound; round++ {
		arbitration, loadErr := st.CharacterAgents.LoadArbitration(simulation.GenerationID, simulation.Chapter, round)
		if loadErr != nil || arbitration == nil {
			return nil, fmt.Errorf("missing arbitration round %d: %w", round, loadErr)
		}
		arbitrations = append(arbitrations, *arbitration)
	}
	allUsage, err := st.CharacterAgents.LoadUsage()
	if err != nil {
		return nil, err
	}
	activeSet := make(map[string]struct{}, len(activeIDs))
	for _, agentID := range activeIDs {
		activeSet[agentID] = struct{}{}
	}
	usage := make([]domain.CharacterAgentUsage, 0)
	for _, item := range allUsage {
		if item.GenerationID == simulation.GenerationID && item.Chapter == simulation.Chapter {
			if _, active := activeSet[item.AgentID]; active || item.Role == "world_arbiter" {
				usage = append(usage, item)
			}
		}
	}
	evidence, err := domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{
		Version:        domain.CharacterAgentEvidenceVersion,
		GenerationID:   simulation.GenerationID,
		Chapter:        simulation.Chapter,
		Registry:       *registry,
		Stimulus:       *stimulus,
		Activation:     *activation,
		Observations:   observations,
		Proposals:      proposals,
		Arbitrations:   arbitrations,
		MemoryRoots:    append([]string(nil), receipt.MemoryRoots...),
		Usage:          usage,
		ProtocolDigest: receipt.ProtocolDigest,
	})
	if err != nil {
		return nil, err
	}
	return &evidence, nil
}
