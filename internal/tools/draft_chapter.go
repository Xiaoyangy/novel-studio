package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	editrules "github.com/chenhongyang/novel-studio/internal/editor/rules"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	qualityrules "github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// DraftChapterTool 写入整章草稿，替代旧的 write_scene + polish_chapter 流水线。
// Agent 自主决定一次写完还是分批续写。
type DraftChapterTool struct {
	store *store.Store
}

func NewDraftChapterTool(store *store.Store) *DraftChapterTool {
	return &DraftChapterTool{store: store}
}

func (t *DraftChapterTool) Name() string { return "draft_chapter" }
func (t *DraftChapterTool) Description() string {
	return "写入章节正文。mode=write 覆盖写入整章，mode=append 追加到现有草稿（续写/修改）。用户报告的外部平台结果只用于抽查：当前哈希高分会触发返工，替换哈希不等待人工复测。仅显式配置 automated_hard 的自动外部门禁通过后才会冻结精确载荷。"
}
func (t *DraftChapterTool) Label() string { return "写入章节" }

// 写工具，禁止并发（读-改-写竞态）。
func (t *DraftChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *DraftChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *DraftChapterTool) Schema() map[string]any {
	// mode 标 required 是为了兼容 OpenAI strict tool calling——strict 模式
	// 要求所有 properties 都在 required 列表中。原来的"省略 mode 走 write
	// 默认"行为现在需要模型显式传 mode="write"，Execute 的 default 分支不变。
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("content", schema.String("章节正文")).Required(),
		schema.Property("mode", schema.Enum("写入模式", "write", "append")).Required(),
	)
}

// StrictSchema 启用 OpenAI 的 strict tool calling，让模型必须严格遵守
// schema：所有 required 字段必填，arguments 不能"提前 EOT"出现空对象。
// litellm 透传 strict 字段；OpenAI / xAI 等支持的后端会强制执行，其他后端
// 按 HTTP/JSON 惯例忽略未知字段。Anthropic/Gemini/Bedrock 走各自的转换链路
// 自然不会看到这个字段。
func (t *DraftChapterTool) StrictSchema() bool { return true }

func (t *DraftChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter  int                    `json:"chapter"`
		Content  string                 `json:"content"`
		Mode     string                 `json:"mode"`
		Sampling *domain.SamplingRecord `json:"sampling"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if err := guardPipelineProseExecution(t.store, a.Chapter, t.Name()); err != nil {
		return nil, err
	}
	if a.Content == "" {
		return nil, fmt.Errorf("content must not be empty: %w", errs.ErrToolArgs)
	}
	if err := validateDraftProsePayload(a.Content); err != nil {
		return nil, fmt.Errorf("draft content is not chapter prose: %w: %w", err, errs.ErrToolPrecondition)
	}
	if err := t.recoverDraftWriteIntent(a.Chapter); err != nil {
		return nil, fmt.Errorf("recover interrupted draft write: %w: %w", err, errs.ErrStoreWrite)
	}
	explicitRerender := ExplicitRerenderRequestActive(t.store, a.Chapter)
	externalGate, requirementErr := InspectDraftExternalGateWithStore(t.store, a.Chapter)
	if requirementErr != nil {
		return nil, fmt.Errorf("读取草稿 AIGC 门禁: %w: %w", requirementErr, errs.ErrStoreRead)
	}
	requirement := externalGate.Requirement
	candidateSHA := reviewreport.BodySHA256(a.Content)
	if draftCurrentHashNamedPassFrozen(externalGate) && !explicitRerender {
		return nil, fmt.Errorf("第 %d 章当前草稿精确哈希已通过显式配置 automated_hard 的自动 detector/mode 严格 <4%% 门禁，正文已冻结；普通 draft_chapter 不得改变该载荷，只允许继续 check_consistency/commit_chapter。用户手工抽查不会进入此状态。若确需换稿，必须先产生新的整章重渲染授权或新的阻断要求: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if externalGate.Status == DraftExternalGateRejudgePending && !explicitRerender {
		return nil, fmt.Errorf("第 %d 章上一轮整章重渲染已产生新哈希，必须先运行 DeepSeek provider judge；该判定完成前禁止再次 draft_chapter: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if externalGate.Status == DraftExternalGateAdviceIncomplete && !explicitRerender {
		return nil, fmt.Errorf("第 %d 章 DeepSeek provider judge 阻断但修改建议不完整，必须先重新运行该 provider judge；禁止盲目重渲染: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if a.Mode == "append" && (requirement != nil || explicitRerender) {
		if requirement != nil {
			return nil, fmt.Errorf("%s；append 不能解除该要求: %w", draftExternalRerenderInstruction(requirement), errs.ErrToolPrecondition)
		}
		return nil, fmt.Errorf("第 %d 章已有显式整章重渲染授权；append 不能消费该授权，必须用 mode=write 提交完整新稿: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if requirement != nil {
		if candidateSHA == requirement.EvaluatedBodySHA256 {
			return nil, fmt.Errorf("第 %d 章整章重渲染结果与当前 AIGC 阻断版本（DeepSeek provider judge 或用户抽查高分）哈希相同，未产生有效新稿: %w", a.Chapter, errs.ErrToolPrecondition)
		}
		wordContract := inspectChapterWordContract(t.store, a.Content)
		if !wordContract.Passed {
			return nil, fmt.Errorf(
				"第 %d 章当前 AIGC 阻断要求整章重渲染，但候选正文仅 %d 字，未满足 %d-%d 字合同；旧草稿与重渲染标记均保持不变，必须一次提交完整小说正文: %w",
				a.Chapter, wordContract.Actual, wordContract.Min, wordContract.Max, errs.ErrToolPrecondition,
			)
		}
		// A current-hash registered platform result can synthesize its marker at
		// inspection time. Persist it before replacing the judged bytes so the
		// new hash cannot later be approved by the independent model alone.
		if RequiresRegisteredExternalRetest(requirement) || isRegisteredExternalSamplingTrigger(requirement) {
			if err := SetDraftExternalRerenderRequirement(t.store.Dir(), *requirement); err != nil {
				return nil, fmt.Errorf("persist registered external rewrite trigger: %w: %w", err, errs.ErrStoreWrite)
			}
		}
	}
	if currentSHA := strings.TrimSpace(externalGate.CurrentBodySHA256); (requirement != nil || explicitRerender) && currentSHA != "" && candidateSHA == currentSHA {
		return nil, fmt.Errorf("第 %d 章整章重渲染结果与当前草稿哈希相同，单次授权尚未被有效消费: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}
	isRewrite := false
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		// 打磨/重写路径：章节虽已完成，但仍在 pending_rewrites 中，允许覆盖草稿
		progress, _ := t.store.Progress.Load()
		inRewriteQueue := progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter)
		isRewrite = inRewriteQueue
		if !inRewriteQueue {
			return json.Marshal(map[string]any{
				"chapter":   a.Chapter,
				"skipped":   true,
				"completed": true,
				"reason":    fmt.Sprintf("第 %d 章已提交完成，不能覆盖", a.Chapter),
			})
		}
	}
	if isRewrite && BlockingReviewRejectsBody(t.store, a.Chapter, a.Content) {
		return nil, fmt.Errorf("第 %d 章候选正文与正式 review=%q 拒绝的正文哈希完全相同，未产生有效新稿；必须按当前 rewrite_brief 重新渲染不同正文: %w", a.Chapter, "rewrite", errs.ErrToolPrecondition)
	}
	if escalation := InspectRenderOnlyReplanEscalation(t.store, a.Chapter); escalation.Required {
		return nil, fmt.Errorf("第 %d 章 render-only 已连续结构失败，禁止继续沿旧 plan 生成：%s；必须先重新完成 chapter_world_simulation 与 POV plan: %w", a.Chapter, escalation.Reason, errs.ErrToolPrecondition)
	}
	planGuard, err := validateCurrentChapterRenderPlan(t.store, a.Chapter)
	if err != nil {
		return nil, err
	}
	renderOnlyRerender := planGuard.RenderOnly
	if a.Mode == "append" && renderOnlyRerender {
		return nil, fmt.Errorf("第 %d 章当前只允许复用既有 plan 做一次整章重渲染；append 不能消费该授权，必须用 mode=write 提交完整新稿: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if currentSHA := strings.TrimSpace(externalGate.CurrentBodySHA256); renderOnlyRerender && currentSHA != "" && candidateSHA == currentSHA {
		return nil, fmt.Errorf("第 %d 章 render-only 重渲染结果与当前草稿哈希相同，未产生有效新稿: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if plan := planGuard.Plan; plan != nil {
		if err := validateDraftChapterHeading(*plan, a.Content); err != nil {
			return nil, err
		}
		if err := validateDraftWorldVisibility(t.store, a.Chapter, a.Content); err != nil {
			return nil, err
		}
	}
	if a.Mode == "append" {
		if err := validateAppendBaseCurrentPlanEpoch(t.store, a.Chapter); err != nil {
			return nil, err
		}
	}
	if draftNeedsConsistencyCheck(t.store, a.Chapter) && requirement == nil {
		return nil, fmt.Errorf(
			"第 %d 章已有草稿且尚未执行 check_consistency，禁止连续 draft_chapter；请先 read_chapter(source=draft)，再调用 check_consistency，若无硬伤直接 commit_chapter: %w",
			a.Chapter, errs.ErrToolPrecondition,
		)
	}
	hardFactCandidate := a.Content
	if a.Mode == "append" {
		existing, loadErr := t.store.Drafts.LoadDraft(a.Chapter)
		if loadErr != nil {
			return nil, fmt.Errorf("load draft before append hard-fact gate: %w: %w", errs.ErrStoreRead, loadErr)
		}
		hardFactCandidate = existing
		if hardFactCandidate != "" && a.Content != "" {
			hardFactCandidate += "\n\n"
		}
		hardFactCandidate += a.Content
	}
	if err := requireDraftHardFactAnchors(t.store, a.Chapter, hardFactCandidate); err != nil {
		return nil, fmt.Errorf("第 %d 章 draft_chapter 候选未通过 hard-fact anchor 门禁，真实草稿与 checkpoint 均未改变: %w", a.Chapter, err)
	}
	if requirement == nil && (explicitRerender || renderOnlyRerender) {
		requirement, err = SetRenderOnlyRejudgeRequirement(t.store, a.Chapter, strings.TrimSpace(externalGate.CurrentBodySHA256))
		if err != nil {
			return nil, fmt.Errorf("persist render-only rejudge contract before draft write: %w: %w", err, errs.ErrStoreWrite)
		}
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	switch a.Mode {
	case "append":
		existing, _ := t.store.Drafts.LoadDraft(a.Chapter)
		combined := existing
		if combined != "" && a.Content != "" {
			combined += "\n\n"
		}
		combined += a.Content
		if err := validateProjectContaminationFinal(t.store, "draft_chapter", combined); err != nil {
			return nil, err
		}
		if err := beginDraftWriteIntent(t.store, a.Chapter, existing, combined, a.Mode, a.Sampling); err != nil {
			return nil, fmt.Errorf("begin draft write: %w", err)
		}
		if err := t.store.Drafts.AppendDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("append draft: %w", err)
		}
		full, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load draft after append: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
			"plan", "rerender-request", "draft", "edit",
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, full, a.Sampling)
		if err != nil {
			return nil, err
		}
		wordContract := inspectChapterWordContract(t.store, full)
		aigcReport, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, full)
		rawAIGCGate := draftAIGCRawLocalGateResult(aigcReport, aigcGate)
		if err := checkpointDraftStructuralBlock(t.store, a.Chapter, full, aigcReport, aigcGate); err != nil {
			return nil, fmt.Errorf("checkpoint draft structural block: %w", err)
		}
		if !rawAIGCGate.Passed {
			if err := persistDraftAIGCRerenderRequirement(t.store, a.Chapter, full, aigcReport, aigcGate); err != nil {
				return nil, fmt.Errorf("persist draft AIGC rerender requirement: %w", err)
			}
		}
		nextStep := draftQualityGateNextStep(wordContract, aigcGate)
		hardGatePassed := wordContract.Passed && rawAIGCGate.Passed
		localStructuralRerender := draftAIGCHasWholeTextStructuralBlock(full, aigcReport, aigcGate)
		if localStructuralRerender {
			nextStep = "停止本次正文修改；append 后的精确整章哈希触发本地 whole-text/segment 结构阻断。append 不能继续叠加修补，立即把控制权交还外层 pipeline 做有界整章重渲染或重规划。"
			hardGatePassed = false
		}
		if err := clearDraftWriteIntent(t.store.Dir(), a.Chapter); err != nil {
			return nil, fmt.Errorf("complete draft write: %w", err)
		}
		return json.Marshal(map[string]any{
			"written":                            true,
			"chapter":                            a.Chapter,
			"mode":                               "append",
			"word_count":                         utf8.RuneCountInString(full),
			"word_contract":                      wordContract,
			"aigc_gate":                          aigcGate,
			"aigc_raw_local_gate":                rawAIGCGate,
			"hard_gate_passed":                   hardGatePassed,
			"external_rejudge_required":          localStructuralRerender,
			"external_rejudge_required_now":      false,
			"local_structural_rerender_required": localStructuralRerender,
			"stop_prose_modification":            localStructuralRerender,
			"ai_voice_score":                     analysis.Metrics.AIVoiceScore,
			"figurative_density":                 analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":                     analysis.Metrics.DialogueRatio,
			"next_step":                          nextStep,
		})
	default: // write
		if err := validateProjectContaminationFinal(t.store, "draft_chapter", a.Content); err != nil {
			return nil, err
		}
		prior, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load prior draft: %w", err)
		}
		if err := beginDraftWriteIntent(t.store, a.Chapter, prior, a.Content, a.Mode, a.Sampling); err != nil {
			return nil, fmt.Errorf("begin draft write: %w", err)
		}
		if err := t.store.Drafts.SaveDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("save draft: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
			"plan", "rerender-request", "draft", "edit",
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, a.Content, a.Sampling)
		if err != nil {
			return nil, err
		}
		wordContract := inspectChapterWordContract(t.store, a.Content)
		aigcReport, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, a.Content)
		rawAIGCGate := draftAIGCRawLocalGateResult(aigcReport, aigcGate)
		if err := checkpointDraftStructuralBlock(t.store, a.Chapter, a.Content, aigcReport, aigcGate); err != nil {
			return nil, fmt.Errorf("checkpoint draft structural block: %w", err)
		}
		if !rawAIGCGate.Passed {
			if err := persistDraftAIGCRerenderRequirement(t.store, a.Chapter, a.Content, aigcReport, aigcGate); err != nil {
				return nil, fmt.Errorf("persist draft AIGC rerender requirement: %w", err)
			}
		}
		nextStep := draftQualityGateNextStep(wordContract, aigcGate)
		hardGatePassed := wordContract.Passed && rawAIGCGate.Passed
		localStructuralRerender := draftAIGCHasWholeTextStructuralBlock(a.Content, aigcReport, aigcGate)
		if err := clearDraftWriteIntent(t.store.Dir(), a.Chapter); err != nil {
			return nil, fmt.Errorf("complete draft write: %w", err)
		}
		managedJudgePending, err := pipelineManagedCurrentDraftNeedsDeepSeekJudge(t.store, a.Chapter, candidateSHA)
		if err != nil {
			return nil, fmt.Errorf("inspect current-hash DeepSeek gate after draft write: %w: %w", err, errs.ErrStoreRead)
		}
		eventualExternalRejudge := requirement != nil || renderOnlyRerender || managedJudgePending
		externalRejudgeRequired := eventualExternalRejudge && !localStructuralRerender
		registeredRetestDeferred := RequiresRegisteredExternalRetest(requirement)
		if localStructuralRerender {
			nextStep = "停止本次正文修改；当前哈希仍触发本地 whole-text/segment 结构阻断，外层 pipeline 将在有界次数内先整章重渲染或重做因果计划。"
			if labels := RegisteredExternalRetestLabels(requirement); RequiresRegisteredExternalRetest(requirement) && len(labels) > 0 {
				nextStep += strings.Join(labels, ", ") + " 的同哈希复测义务保留到本地结构门禁与 DeepSeek 当前哈希判断均通过的候选稿"
			}
			hardGatePassed = false
		} else if managedJudgePending {
			nextStep = fmt.Sprintf("停止正文修改；当前 pipeline 新稿 drafts/%02d.draft.md 尚无可批准当前哈希的 DeepSeek provider judge 裸正文结论，立即把控制权交还外层 pipeline 运行该判定；判定前禁止 check_consistency、edit 或 commit", a.Chapter)
			if labels := RegisteredExternalRetestLabels(requirement); RequiresRegisteredExternalRetest(requirement) && len(labels) > 0 {
				nextStep += "；" + strings.Join(labels, ", ") + " 的注册平台同哈希复测暂缓，只有本地门禁与 DeepSeek 都通过后才可执行"
			}
			hardGatePassed = false
		} else if externalRejudgeRequired {
			nextStep = fmt.Sprintf("停止正文修改；先由外层 pipeline 对 drafts/%02d.draft.md 的新哈希运行 DeepSeek provider judge，只有结果严格低于 %.0f%% 才能检查并提交。用户外部平台不要求跟随复测", a.Chapter, aigcGate.PassExclusivePercent)
			if labels := RegisteredExternalRetestLabels(requirement); RequiresRegisteredExternalRetest(requirement) && len(labels) > 0 {
				nextStep += "；显式 automated_hard 还要求自动 detector/mode 完成同哈希门禁：" + strings.Join(labels, ", ")
			}
			hardGatePassed = false
		}
		return json.Marshal(map[string]any{
			"written":                             true,
			"chapter":                             a.Chapter,
			"mode":                                "write",
			"word_count":                          utf8.RuneCountInString(a.Content),
			"word_contract":                       wordContract,
			"aigc_gate":                           aigcGate,
			"aigc_raw_local_gate":                 rawAIGCGate,
			"hard_gate_passed":                    hardGatePassed,
			"external_rejudge_required":           eventualExternalRejudge,
			"external_rejudge_required_now":       externalRejudgeRequired,
			"local_structural_rerender_required":  localStructuralRerender,
			"registered_external_retest_deferred": registeredRetestDeferred,
			"stop_prose_modification":             eventualExternalRejudge || localStructuralRerender,
			"ai_voice_score":                      analysis.Metrics.AIVoiceScore,
			"figurative_density":                  analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":                      analysis.Metrics.DialogueRatio,
			"next_step":                           nextStep,
		})
	}
}

func draftNeedsConsistencyCheck(st *store.Store, chapter int) bool {
	if st == nil || chapter <= 0 {
		return false
	}
	scope := domain.ChapterScope(chapter)
	var latestBodyEvent int64
	for _, step := range []string{"draft", "edit", "draft-structural-block"} {
		if cp := st.Checkpoints.LatestByStep(scope, step); cp != nil && cp.Seq > latestBodyEvent {
			latestBodyEvent = cp.Seq
		}
	}
	if latestBodyEvent == 0 {
		return false
	}
	var clearedThrough int64
	for _, step := range []string{"consistency_check", "consistency_check_failed", "rerender-request", "plan", "chapter_world_simulation", "commit"} {
		if cp := st.Checkpoints.LatestByStep(scope, step); cp != nil && cp.Seq > clearedThrough {
			clearedThrough = cp.Seq
		}
	}
	return latestBodyEvent > clearedThrough
}

func validateDraftChapterHeading(plan domain.ChapterPlan, content string) error {
	firstLine := strings.TrimSpace(strings.SplitN(strings.TrimSpace(content), "\n", 2)[0])
	title := strings.TrimSpace(plan.Title)
	if plan.Chapter <= 0 || title == "" {
		return nil
	}
	prefixes := []string{fmt.Sprintf("第%d章", plan.Chapter)}
	if numeral := map[int]string{1: "一", 2: "二", 3: "三", 4: "四", 5: "五", 6: "六", 7: "七", 8: "八", 9: "九", 10: "十"}[plan.Chapter]; numeral != "" {
		prefixes = append(prefixes, "第"+numeral+"章")
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(firstLine, prefix) && strings.TrimSpace(strings.TrimPrefix(firstLine, prefix)) == title {
			return nil
		}
	}
	return fmt.Errorf("第 %d 章草稿首行必须是‘第N章 %s’，当前为 %q: %w", plan.Chapter, title, firstLine, errs.ErrToolPrecondition)
}

func validateDraftWorldVisibility(s *store.Store, chapter int, content string) error {
	if s == nil || chapter <= 0 {
		return nil
	}
	sim, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil || sim == nil {
		return err
	}
	var leaked []string
	for _, decision := range sim.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if name != "" && !decision.VisibleToPOV && strings.Contains(content, name) {
			leaked = append(leaked, name)
		}
	}
	leaked = compactStrings(leaked)
	if len(leaked) > 0 {
		return fmt.Errorf("第 %d 章正文写入了 world simulation 中 visible_to_pov=false 的实名角色：%s；不得拿离屏人物给无名摊主或现场岗位补位: %w", chapter, strings.Join(leaked, "、"), errs.ErrToolPrecondition)
	}
	return nil
}

func validateDraftProsePayload(content string) error {
	trimmed := strings.TrimSpace(content)
	if err := validateFictionProseMetadataFree(trimmed); err != nil {
		return err
	}
	if err := validateFictionProseTypography(trimmed); err != nil {
		return err
	}
	for _, marker := range []string{
		"当前工作区为只读", "operation not permitted", "Qdrant", "未挂载 draft_chapter",
		"未挂载 `draft_chapter", "本会话未挂载", "流水线写入", "门禁均无法重跑",
		"请恢复项目写权限", "启用 Qdrant/RAG", "重新挂载小说工具", "阻塞原因：",
		"不能沿用缺少", "角色台账",
	} {
		if strings.Contains(trimmed, marker) {
			return fmt.Errorf("包含运行状态/工具错误说明 %q，不能写入小说草稿", marker)
		}
	}
	for _, violation := range qualityrules.Lint(trimmed) {
		if violation.Rule != "abstract_system_reassurance" && violation.Rule != "aphoristic_narrative_summary" {
			continue
		}
		return fmt.Errorf("命中高置信正文模板 %s：%s", violation.Rule, violation.Target)
	}
	return nil
}

func (t *DraftChapterTool) saveDraftAIVoice(chapter int, content string, sampling *domain.SamplingRecord) (domain.AIVoiceAnalysis, error) {
	history, _ := t.store.AIVoice.LoadAllChapterMetrics()
	analysis := editrules.AnalyzeChapter(chapter, content, history)
	if err := t.store.AIVoice.SaveChapterMetrics(analysis.Metrics, true); err != nil {
		return analysis, fmt.Errorf("save draft ai voice metrics: %w: %w", errs.ErrStoreWrite, err)
	}
	if sampling != nil {
		sampling.Chapter = chapter
		if err := t.store.AIVoice.SaveSamplingRecord(*sampling); err != nil {
			return analysis, fmt.Errorf("save sampling record: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	return analysis, nil
}
