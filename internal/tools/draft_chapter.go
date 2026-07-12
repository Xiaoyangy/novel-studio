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
	return "写入章节正文。mode=write 覆盖写入整章，mode=append 追加到现有草稿（续写/修改）"
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
	if a.Content == "" {
		return nil, fmt.Errorf("content must not be empty: %w", errs.ErrToolArgs)
	}
	if err := validateDraftProsePayload(a.Content); err != nil {
		return nil, fmt.Errorf("draft content is not chapter prose: %w: %w", err, errs.ErrToolPrecondition)
	}
	explicitRerender := ExplicitRerenderRequestActive(t.store, a.Chapter)
	externalGate, requirementErr := InspectDraftExternalGate(t.store.Dir(), a.Chapter)
	if requirementErr != nil {
		return nil, fmt.Errorf("读取草稿外审门禁: %w: %w", requirementErr, errs.ErrStoreRead)
	}
	requirement := externalGate.Requirement
	if externalGate.Status == DraftExternalGateRejudgePending && !explicitRerender {
		return nil, fmt.Errorf("第 %d 章上一轮整章重渲染已产生新哈希，必须先运行外部草稿复判；复判前禁止再次 draft_chapter: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if externalGate.Status == DraftExternalGateAdviceIncomplete && !explicitRerender {
		return nil, fmt.Errorf("第 %d 章外判阻断但修改建议不完整，必须先重新外判；禁止盲目重渲染: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if requirement != nil && a.Mode == "append" {
		return nil, fmt.Errorf("%s；append 不能解除该要求: %w", draftExternalRerenderInstruction(requirement), errs.ErrToolPrecondition)
	}
	if requirement != nil {
		candidateSHA := reviewreport.BodySHA256(a.Content)
		if candidateSHA == requirement.EvaluatedBodySHA256 {
			return nil, fmt.Errorf("第 %d 章整章重渲染结果与外判阻断版本哈希相同，未产生有效新稿: %w", a.Chapter, errs.ErrToolPrecondition)
		}
		wordContract := inspectChapterWordContract(t.store, a.Content)
		if !wordContract.Passed {
			return nil, fmt.Errorf(
				"第 %d 章外审要求整章重渲染，但候选正文仅 %d 字，未满足 %d-%d 字合同；旧草稿与重渲染标记均保持不变，必须一次提交完整小说正文: %w",
				a.Chapter, wordContract.Actual, wordContract.Min, wordContract.Max, errs.ErrToolPrecondition,
			)
		}
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
	renderOnlyRerender := isRewrite && RenderOnlyRerenderReady(t.store, a.Chapter)
	// 真实流水线中计划存在时，写正文前重新执行完整门禁。这样旧 plan 即使文件仍在，
	// 只要缺少当前项目要求的首屏抓力、喜剧节拍、热梗落点或长篇开局设计，也不能旁路写作。
	if plan, err := t.store.Drafts.LoadChapterPlan(a.Chapter); err != nil {
		return nil, fmt.Errorf("load chapter plan: %w: %w", errs.ErrStoreRead, err)
	} else if plan != nil {
		if err := validateDraftChapterHeading(*plan, a.Content); err != nil {
			return nil, err
		}
		if err := validateDraftWorldVisibility(t.store, a.Chapter, a.Content); err != nil {
			return nil, err
		}
		if renderOnlyRerender {
			if err := ValidateReusableCausalPlanForRerender(t.store, a.Chapter); err != nil {
				return nil, fmt.Errorf("第 %d 章显式 render-only 复用门禁失败: %w", a.Chapter, err)
			}
		} else {
			if err := validateChapterPrewriteSimulation(t.store, *plan, isRewrite); err != nil {
				return nil, err
			}
		}
	}
	if latest := t.store.Checkpoints.Latest(domain.ChapterScope(a.Chapter)); latest != nil && latest.Step == "draft" && requirement == nil {
		return nil, fmt.Errorf(
			"第 %d 章已有草稿且尚未执行 check_consistency，禁止连续 draft_chapter；请先 read_chapter(source=draft)，再调用 check_consistency，若无硬伤直接 commit_chapter: %w",
			a.Chapter, errs.ErrToolPrecondition,
		)
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
		if err := t.store.Drafts.AppendDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("append draft: %w", err)
		}
		full, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load draft after append: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, full, a.Sampling)
		if err != nil {
			return nil, err
		}
		wordContract := inspectChapterWordContract(t.store, full)
		_, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, full)
		return json.Marshal(map[string]any{
			"written":            true,
			"chapter":            a.Chapter,
			"mode":               "append",
			"word_count":         utf8.RuneCountInString(full),
			"word_contract":      wordContract,
			"aigc_gate":          aigcGate,
			"hard_gate_passed":   wordContract.Passed && aigcGate.Passed,
			"ai_voice_score":     analysis.Metrics.AIVoiceScore,
			"figurative_density": analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":     analysis.Metrics.DialogueRatio,
			"next_step":          draftQualityGateNextStep(wordContract, aigcGate),
		})
	default: // write
		if err := validateProjectContaminationFinal(t.store, "draft_chapter", a.Content); err != nil {
			return nil, err
		}
		if err := t.store.Drafts.SaveDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("save draft: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("checkpoint draft: %w", err)
		}
		analysis, err := t.saveDraftAIVoice(a.Chapter, a.Content, a.Sampling)
		if err != nil {
			return nil, err
		}
		wordContract := inspectChapterWordContract(t.store, a.Content)
		_, aigcGate := inspectDraftAIGCGate(t.store, a.Chapter, a.Content)
		nextStep := draftQualityGateNextStep(wordContract, aigcGate)
		hardGatePassed := wordContract.Passed && aigcGate.Passed
		if requirement != nil || renderOnlyRerender {
			nextStep = fmt.Sprintf("停止正文修改；先对 drafts/%02d.draft.md 的新哈希运行外部草稿复判，只有结果严格低于 %.0f%% 才能检查并提交", a.Chapter, aigcGate.PassExclusivePercent)
			hardGatePassed = false
		}
		return json.Marshal(map[string]any{
			"written":                   true,
			"chapter":                   a.Chapter,
			"mode":                      "write",
			"word_count":                utf8.RuneCountInString(a.Content),
			"word_contract":             wordContract,
			"aigc_gate":                 aigcGate,
			"hard_gate_passed":          hardGatePassed,
			"external_rejudge_required": requirement != nil || renderOnlyRerender,
			"ai_voice_score":            analysis.Metrics.AIVoiceScore,
			"figurative_density":        analysis.Metrics.FigurativeDensity,
			"dialogue_ratio":            analysis.Metrics.DialogueRatio,
			"next_step":                 nextStep,
		})
	}
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
