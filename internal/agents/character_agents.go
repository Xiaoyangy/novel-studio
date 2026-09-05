package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const characterAgentSystemPrompt = `你是一个小说角色本人的决策 Agent，不是作者、编剧或旁白。

你只能使用用户消息里的 character_observation_packet：
- 只依据其中明确列出的自身目标、压力、资源、关系、承诺、已知事实、感知事件、公共规则和记忆。
- 不得猜测未来大纲、章节钩子、叙事任务、其他角色秘密或未被你感知的世界事实。
- 先考虑至少两个当下真实可行的选项，再以此角色的性格、误判、利益和风险承受作出选择。
- knowledge_refs 只能逐字引用 observation 中存在的 fact id。
- 只调用 submit_character_decision 一次；decision_reason 写可审计的简明动因，不输出思维链、分析过程或作者说明。
- 决定可以是等待、拒绝、观察或继续已有行动，但不能为了配合大纲而失去人物自主性。`

const worldArbiterSystemPrompt = `你是单一世界的规则裁判，不是作者，也不是任何角色。

你会收到世界刺激、硬合同、软方向和全部角色的独立提案：
- decision 与 intended_action 必须逐字复制对应提案，绝对不得替角色改意图。
- 只裁决行动顺序、时间、地点、资源、知识、物理/社会规则、碰撞、完成度和结果。
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
		"protocol":            domain.CharacterAgentDecisionProtocolVersion,
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
		Version             string         `json:"version"`
		CharacterPrompt     string         `json:"character_prompt"`
		ArbiterPrompt       string         `json:"arbiter_prompt"`
		SuccessorPrompt     string         `json:"successor_prompt"`
		SubmitSchema        map[string]any `json:"submit_schema"`
		ResolveSchema       map[string]any `json:"resolve_schema"`
		SuccessorPlanSchema map[string]any `json:"successor_plan_schema"`
	}{
		Version:             domain.CharacterAgentDecisionProtocolVersion,
		CharacterPrompt:     characterAgentSystemPrompt,
		ArbiterPrompt:       worldArbiterSystemPrompt,
		SuccessorPrompt:     characterAgentSuccessorArchitectPrompt,
		SubmitSchema:        submit.Schema(),
		ResolveSchema:       resolve.Schema(),
		SuccessorPlanSchema: successor.Schema(),
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
	contextArgs, _ := json.Marshal(map[string]any{"chapter": chapter, "profile": "world_simulation"})
	contextRaw, err := contextTool.Execute(ctx, contextArgs)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare character-agent world context: %w", err)
	}
	var envelope struct {
		Access struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
		ProjectAllToken string                            `json:"project_all_state_source_token"`
		ProjectAllState domain.ProjectedPlanningContextV2 `json:"project_all_state"`
	}
	if err := json.Unmarshal(contextRaw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode character-agent world context: %w", err)
	}
	generationID := strings.TrimSpace(envelope.ProjectAllState.GenerationID)
	if generationID == "" {
		if progress, loadErr := st.Progress.Load(); loadErr == nil && progress != nil {
			generationID = strings.TrimSpace(progress.GenerationID)
		}
	}
	if generationID == "" {
		generationID = "live_v1"
	}
	sources := compactAgentStrings([]string{
		strings.TrimSpace(envelope.ProjectAllToken),
		strings.TrimSpace(envelope.Access.SourceToken),
		"character-agent-protocol:" + CharacterAgentProtocolDigest(),
	})
	inputs, err := loadOrPrepareCharacterAgentInputs(st, generationID, chapter, arcBoundary, envelope.ProjectAllState, sources)
	if err != nil {
		return nil, nil, err
	}
	activeIDs := activeCharacterAgentIDs(inputs.Activation)
	if len(activeIDs) == 0 {
		return nil, nil, fmt.Errorf("character-agent activation has no active character")
	}
	proposals, err := runCharacterProposalRound(ctx, cfg, st, models, inputs.Observations, activeIDs, 1)
	if err != nil {
		return nil, nil, err
	}
	receipt, err := runWorldArbitration(ctx, cfg, st, models, inputs, proposals)
	if err != nil {
		return nil, nil, err
	}
	if receipt.HardContractStatus == "infeasible" {
		return characterAgentHardContractFailure(ctx, cfg, st, models, arcBoundary, inputs, proposals, *receipt)
	}
	if !receipt.Finalized {
		if cfg.CharacterAgents.MaxRevisionRounds < 1 {
			return nil, nil, fmt.Errorf("world arbitration requires a revision but revisions are disabled")
		}
		feedback := arbitrationFeedbackByAgent(*receipt)
		revisionIDs := make([]string, 0, len(feedback))
		for agentID, items := range feedback {
			base, ok := inputs.Observations[agentID]
			if !ok {
				return nil, nil, fmt.Errorf("arbiter requested unknown character revision %s", agentID)
			}
			base.Round = 2
			base.ConflictFeedback = items
			base.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
			base.Digest = ""
			revised, finalizeErr := domain.FinalizeCharacterObservationPacket(base)
			if finalizeErr != nil {
				return nil, nil, finalizeErr
			}
			if err := st.CharacterAgents.SaveObservation(revised); err != nil {
				return nil, nil, err
			}
			inputs.Observations[agentID] = revised
			revisionIDs = append(revisionIDs, agentID)
		}
		if len(revisionIDs) == 0 {
			return nil, nil, fmt.Errorf("non-final arbitration supplied no affected character")
		}
		if _, err := runCharacterProposalRound(ctx, cfg, st, models, inputs.Observations, revisionIDs, 2); err != nil {
			return nil, nil, err
		}
		proposals, err = st.CharacterAgents.LoadLatestProposals(generationID, chapter, 2, activeIDs)
		if err != nil || len(proposals) != len(activeIDs) {
			return nil, nil, fmt.Errorf("load revised character proposals: %w", err)
		}
		receipt, err = runWorldArbitration(ctx, cfg, st, models, inputs, proposals)
		if err != nil {
			return nil, nil, err
		}
		if receipt.HardContractStatus == "infeasible" {
			return characterAgentHardContractFailure(ctx, cfg, st, models, arcBoundary, inputs, proposals, *receipt)
		}
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
	if existing, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil {
		return nil, err
	} else if existing != nil && existing.ParentGenerationID == receipt.GenerationID && existing.ArbitrationDigest == receipt.Digest {
		return existing, nil
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
		NonNegotiables: nonNegotiables, HardContractConflicts: append([]string(nil), receipt.HardContractConflicts...),
	}
	tool := tools.NewSubmitCharacterAgentSuccessorPlanTool(st, base, original)
	payload, _ := json.Marshal(struct {
		Constraints domain.CharacterAgentSuccessorPlan `json:"immutable_constraints"`
		Original    []domain.OutlineEntry              `json:"current_soft_outline"`
		Proposals   []domain.CharacterDecisionProposal `json:"character_choices"`
		Arbitration domain.WorldArbitrationReceipt     `json:"world_arbitration"`
	}{base, original, proposals, receipt})
	events := agentcore.AgentLoop(
		ctx,
		[]agentcore.AgentMessage{agentcore.UserMsg("只重排软章位并提交 successor plan：\n<successor_input>\n" + string(payload) + "\n</successor_input>")},
		agentcore.AgentContext{SystemPrompt: characterAgentSuccessorArchitectPrompt, Tools: []agentcore.Tool{tool}},
		agentcore.LoopConfig{
			Model: model, MaxTurns: cappedMaxTurns(cfg.ResolveMaxTurns("architect", 8), 10), MaxRetries: subagentMaxRetries,
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
	if runErr != nil {
		return nil, runErr
	}
	plan, err := st.CharacterAgents.LoadCurrentSuccessorPlan()
	if err != nil || plan == nil {
		return nil, fmt.Errorf("Architect returned without a successor plan: %w", err)
	}
	if plan.ParentGenerationID != receipt.GenerationID || plan.ArbitrationDigest != receipt.Digest {
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
) (characterAgentChapterInputs, error) {
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
	stimulus, err := buildWorldStimulus(st, generationID, chapter, arcBoundary, projected, sources, now)
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

func buildWorldStimulus(st *store.Store, generationID string, chapter int, boundary ProjectedArcBoundary, projected domain.ProjectedPlanningContextV2, sources []string, now string) (domain.WorldStimulusPacket, error) {
	packet := domain.WorldStimulusPacket{
		Version: domain.WorldStimulusPacketVersion, GenerationID: generationID, Chapter: chapter,
		TimeWindow: fmt.Sprintf("chapter-%06d", chapter), Sources: sources, GeneratedAt: now,
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
	if rules, _ := st.World.LoadWorldRules(); len(rules) > 0 {
		for _, rule := range rules {
			text := strings.TrimSpace(rule.Rule + "；边界：" + rule.Boundary)
			if domain.WorldRuleVisibility(rule) == "secret" {
				packet.HardContracts = append(packet.HardContracts, text)
				continue
			}
			packet.PublicFacts = append(packet.PublicFacts, newCharacterAgentFact("world_rule", text, "world_rules.json", domain.WorldRuleVisibility(rule)))
			packet.HardContracts = append(packet.HardContracts, text)
		}
	}
	for _, fact := range projected.CumulativeState {
		text := strings.TrimSpace(fmt.Sprintf("%s的%s=%s", fact.Subject, fact.Field, fact.Value))
		packet.CurrentEvents = append(packet.CurrentEvents, newCharacterAgentFact("projected_state", text, fact.StableID, "arbiter"))
	}
	return domain.FinalizeWorldStimulusPacket(packet)
}

func buildCharacterObservation(st *store.Store, generationID string, chapter int, profile characterAgentProfile, stimulus domain.WorldStimulusPacket, projected domain.ProjectedPlanningContextV2, now string) (domain.CharacterObservationPacket, error) {
	observation := domain.CharacterObservationPacket{
		Version: domain.CharacterObservationVersion, GenerationID: generationID, Chapter: chapter, Round: 1,
		AgentID: profile.Record.AgentID, Character: profile.Character.Name, Tier: profile.Character.Tier,
		TimeWindow: stimulus.TimeWindow, StimulusDigest: stimulus.Digest, GeneratedAt: now,
		Sources: compactAgentStrings(append([]string{"characters.json"}, stimulus.Sources...)),
	}
	selfProfile := strings.TrimSpace(strings.Join(compactAgentStrings([]string{
		"身份：" + profile.Character.Name,
		"角色：" + profile.Character.Role,
		"人物弧：" + profile.Character.Arc,
		"特征：" + strings.Join(profile.Character.Traits, "、"),
	}), "；"))
	observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("self_profile", selfProfile, "characters.json", "private"))
	if profile.Dossier != nil {
		dossier := profile.Dossier
		observation.Location = dossier.CurrentAtStoryStart.Location
		observation.CurrentGoal = firstAgentText(dossier.CurrentAtStoryStart.NextIndependentMove, dossier.Profile.Arc, profile.Character.Arc)
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
		if dossier.KnowledgeBoundary != "" {
			observation.KnownFacts = append(observation.KnownFacts, newCharacterAgentFact("knowledge_boundary", dossier.KnowledgeBoundary, "character_dossier", "private"))
		}
	}
	if profile.Continuity != nil {
		dynamics := profile.Continuity.Dynamics
		observation.CurrentGoal = firstAgentText(dynamics.CurrentGoal, observation.CurrentGoal, profile.Character.Arc)
		observation.Pressure = firstAgentText(dynamics.PrimaryPressure, observation.Pressure, "按自身利益行动")
		observation.Resources = append(observation.Resources, dynamics.Resources...)
		observation.Relationships = append(observation.Relationships, dynamics.RelationshipForces...)
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
	if profile.Agenda != nil {
		observation.CurrentGoal = firstAgentText(profile.Agenda.CurrentGoal, observation.CurrentGoal)
		observation.Pressure = firstAgentText(profile.Agenda.BlockedBy, profile.Agenda.Motivation, observation.Pressure)
		observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("own_agenda", profile.Agenda.CurrentGoal, "offscreen_agenda", "private"))
	}
	if observation.CurrentGoal == "" {
		observation.CurrentGoal = firstAgentText(profile.Character.Arc, "维持自身处境并回应当前压力")
	}
	if observation.Pressure == "" {
		observation.Pressure = "信息与资源有限，必须自行权衡"
	}
	if observation.Location == "" {
		observation.Location = "当前位置未知"
	}
	for _, fact := range stimulus.PublicFacts {
		observation.PublicRules = append(observation.PublicRules, fact)
	}
	for _, fact := range projected.CumulativeState {
		if agentIdentityKey(fact.Subject) != agentIdentityKey(profile.Character.Name) {
			continue
		}
		text := strings.TrimSpace(fmt.Sprintf("自身%s=%s", fact.Field, fact.Value))
		observation.PerceivedEvents = append(observation.PerceivedEvents, newCharacterAgentFact("projected_self_state", text, fact.StableID, "private"))
	}
	for _, transition := range projected.RecentTransitions {
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
		observation.Memory = append(observation.Memory, memory.Facts...)
		observation.MemoryRoot = memory.MemoryRoot
	}
	return domain.FinalizeCharacterObservationPacket(observation)
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
		if profile.Dossier != nil {
			for _, event := range profile.Dossier.PreStoryTimeline {
				memory.Facts = append(memory.Facts, newCharacterMemoryFact(max(1, chapter-1), "pre_story", event.Event, "character_dossier", true))
			}
		}
		if profile.Continuity != nil {
			memory.LastAcceptedChapter = profile.Continuity.LastSeenChapter
			for _, fact := range profile.Continuity.CurrentFacts {
				memory.Facts = append(memory.Facts, newCharacterMemoryFact(max(1, profile.Continuity.LastSeenChapter), "accepted_state", fact, "character_continuity", true))
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

func runCharacterProposalRound(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, observations map[string]domain.CharacterObservationPacket, agentIDs []string, round int) ([]domain.CharacterDecisionProposal, error) {
	model := models.ForRole("character")
	if model == nil {
		return nil, fmt.Errorf("character model is unavailable")
	}
	return runCharacterProposalRoundWithModel(ctx, cfg, st, model, observations, agentIDs, round)
}

func runCharacterProposalRoundWithModel(ctx context.Context, cfg bootstrap.Config, st *store.Store, model agentcore.ChatModel, observations map[string]domain.CharacterObservationPacket, agentIDs []string, round int) ([]domain.CharacterDecisionProposal, error) {
	requestedIDs := make([]string, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		if observation, ok := observations[agentID]; ok && observation.Round == round {
			requestedIDs = append(requestedIDs, agentID)
		}
	}
	if len(requestedIDs) == 0 {
		return nil, nil
	}
	limit := cfg.CharacterAgents.MaxConcurrency
	if limit <= 0 {
		limit = 4
	} else if limit > 4 {
		limit = 4
	}
	sem := make(chan struct{}, limit)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, agentID := range requestedIDs {
		observation, ok := observations[agentID]
		if !ok || observation.Round != round {
			continue
		}
		if existing, loadErr := st.CharacterAgents.LoadProposal(observation.GenerationID, observation.Chapter, round, agentID); loadErr != nil {
			return nil, loadErr
		} else if existing != nil {
			continue
		}
		wg.Add(1)
		go func(observation domain.CharacterObservationPacket) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-runCtx.Done():
				return
			}
			if err := runOneCharacterAgent(runCtx, cfg, st, model, observation); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}(observation)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	first := observations[requestedIDs[0]]
	return st.CharacterAgents.LoadLatestProposals(first.GenerationID, first.Chapter, round, requestedIDs)
}

func runOneCharacterAgent(ctx context.Context, cfg bootstrap.Config, st *store.Store, model agentcore.ChatModel, observation domain.CharacterObservationPacket) error {
	tool := tools.NewSubmitCharacterDecisionTool(st, observation)
	guardBlocks := 0
	guard := func(_ context.Context, _ agentcore.StopInfo) agentcore.StopDecision {
		proposal, _ := st.CharacterAgents.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
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
	usage, err := runCharacterAgentTerminalLoop(
		ctx, model, characterAgentSystemPrompt,
		"你是 "+observation.Character+"。这是你唯一可见的观察包：\n<character_observation_packet>\n"+string(raw)+"\n</character_observation_packet>\n现在只调用 submit_character_decision。",
		tool, tool.Name(), cappedMaxTurns(cfg.ResolveMaxTurns("character", 6), 8), roleThinking(cfg, "character"), guard,
		agentPromptCacheKey("character", st.Dir(), observation.GenerationID, fmt.Sprint(observation.Chapter), fmt.Sprint(observation.Round), observation.AgentID),
	)
	if err != nil {
		return err
	}
	proposal, err := st.CharacterAgents.LoadProposal(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err != nil || proposal == nil {
		return fmt.Errorf("character agent %s did not submit a proposal: %w", observation.AgentID, err)
	}
	usageRecord := domain.CharacterAgentUsage{
		GenerationID: observation.GenerationID, Role: "character", AgentID: observation.AgentID, Character: observation.Character, Chapter: observation.Chapter, Round: observation.Round,
		Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
	}
	if usage.Cost != nil {
		usageRecord.CostUSD = usage.Cost.Total
	}
	return st.CharacterAgents.AppendUsage(usageRecord)
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
) (agentcore.Usage, error) {
	resolvedThinking, _ := ResolveThinkingForModel(model, thinking)
	events := agentcore.AgentLoop(
		ctx,
		[]agentcore.AgentMessage{agentcore.UserMsg(prompt)},
		agentcore.AgentContext{SystemPrompt: systemPrompt, Tools: []agentcore.Tool{tool}},
		agentcore.LoopConfig{
			Model: model, MaxTurns: maxTurns, MaxRetries: subagentMaxRetries, MaxToolErrors: 0,
			ThinkingLevel: resolvedThinking, ToolsAreIdempotent: false, StopGuard: guard,
			CacheLastMessage: promptCacheControl, PromptCacheKey: promptCacheKey,
			StopAfterTool: func(name string) bool { return name == terminalTool },
		},
	)
	var usage agentcore.Usage
	var runErr error
	for event := range events {
		if event.Type == agentcore.EventModelResponse {
			switch message := event.Message.(type) {
			case agentcore.Message:
				usage.Add(message.Usage)
			case *agentcore.Message:
				usage.Add(message.Usage)
			}
		}
		if event.Type == agentcore.EventError && event.Err != nil {
			runErr = event.Err
		}
	}
	return usage, runErr
}

func runWorldArbitration(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, inputs characterAgentChapterInputs, proposals []domain.CharacterDecisionProposal) (*domain.WorldArbitrationReceipt, error) {
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
	if existing, err := st.CharacterAgents.LoadArbitration(inputs.Stimulus.GenerationID, inputs.Stimulus.Chapter, round); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}
	tool := tools.NewResolveChapterWorldTool(st, inputs.Stimulus, inputs.Activation, proposals, CharacterAgentProtocolDigest(), inputs.Sources, cfg.CharacterAgents.MaxRevisionRounds)
	guardBlocks := 0
	guard := func(_ context.Context, _ agentcore.StopInfo) agentcore.StopDecision {
		receipt, _ := st.CharacterAgents.LoadArbitration(inputs.Stimulus.GenerationID, inputs.Stimulus.Chapter, round)
		if receipt != nil {
			return agentcore.StopDecision{Allow: true}
		}
		guardBlocks++
		if guardBlocks >= 2 {
			return agentcore.StopDecision{Escalate: true}
		}
		return agentcore.StopDecision{InjectMessage: "尚未完成裁决。现在只调用 resolve_chapter_world。"}
	}
	payload, _ := json.Marshal(struct {
		Stimulus   domain.WorldStimulusPacket         `json:"world_stimulus"`
		Activation domain.CharacterAgentActivation    `json:"activation"`
		Proposals  []domain.CharacterDecisionProposal `json:"proposals"`
	}{inputs.Stimulus, inputs.Activation, proposals})
	usage, err := runCharacterAgentTerminalLoop(
		ctx, model, worldArbiterSystemPrompt,
		"裁决以下单一世界输入。soft_guidance 只能作为方向，不得覆盖角色选择：\n<world_arbitration_input>\n"+string(payload)+"\n</world_arbitration_input>\n现在只调用 resolve_chapter_world。",
		tool, tool.Name(), cappedMaxTurns(cfg.ResolveMaxTurns("world_arbiter", 6), 8), roleThinking(cfg, "world_arbiter"), guard,
		agentPromptCacheKey("world_arbiter", st.Dir(), inputs.Stimulus.GenerationID, fmt.Sprint(inputs.Stimulus.Chapter), fmt.Sprint(round)),
	)
	if err != nil {
		return nil, err
	}
	receipt, err := st.CharacterAgents.LoadArbitration(inputs.Stimulus.GenerationID, inputs.Stimulus.Chapter, round)
	if err != nil || receipt == nil {
		return nil, fmt.Errorf("world arbiter round %d did not persist a receipt: %w", round, err)
	}
	usageRecord := domain.CharacterAgentUsage{
		GenerationID: inputs.Stimulus.GenerationID, Role: "world_arbiter", AgentID: "world_arbiter", Character: "World Arbiter",
		Chapter: inputs.Stimulus.Chapter, Round: round, Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheWrite: usage.CacheWrite,
	}
	if usage.Cost != nil {
		usageRecord.CostUSD = usage.Cost.Total
	}
	if err := st.CharacterAgents.AppendUsage(usageRecord); err != nil {
		return nil, err
	}
	return receipt, nil
}

func updateProjectedCharacterAgentMemories(st *store.Store, generationID string, chapter int, proposals []domain.CharacterDecisionProposal, receipt domain.WorldArbitrationReceipt) error {
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
		memory.Facts = append(memory.Facts, newCharacterMemoryFact(chapter, "projected_decision", text, receipt.Digest, false))
		memory.UpdatedAt = now
		memory.MemoryRoot = ""
		if err := st.CharacterAgents.SaveProjectedMemory(*memory); err != nil {
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
