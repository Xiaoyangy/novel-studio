package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const editorCacheTestMarkdown = `# ch01 评审

## 总体评分：35 / 40
## 是否需要改写：否
## 一句话诊断：正文通过。

## 八维打分
| 维度 | 分 | 证据 |
|---|---|---|
| 1 设定一致性 | 1 | 设定与已知事实一致。 |
| 2 角色行为 | 1 | 人物行为有明确动机。 |
| 3 节奏 | 1 | 信息与动作节奏清晰。 |
| 4 叙事连贯 | 1 | 时间线与视角连贯。 |
| 5 伏笔 | 1 | 伏笔的露出程度合理。 |
| 6 钩子 | 0 | 章末有具体的下一步拉力。 |
| 7 审美品质 | 0 | 叙述与对话质感稳定。 |
| 8 AI 腔检测 | 0 | 未发现阻断性模板化表达。 |

## 主要问题（按严重度排序）
1. 无严重问题，无需改写。`

type reviewCacheModel struct {
	response string
	calls    atomic.Int32
}

func (m *reviewCacheModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.response)},
	}}, nil
}

func (m *reviewCacheModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *reviewCacheModel) SupportsTools() bool { return false }

func (m *reviewCacheModel) callCount() int { return int(m.calls.Load()) }

// defaultReviewDimensions is test fixture shorthand only. Production parsing
// no longer has a synthetic passing-dimension fallback.
func defaultReviewDimensions() []domain.DimensionScore {
	dims := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		dims = append(dims, domain.DimensionScore{
			Dimension: name,
			Score:     80,
			Verdict:   "pass",
			Comment:   "test fixture",
		})
	}
	return dims
}

func TestStructuredReviewFromMarkdownRequiresCompleteUniqueValidContract(t *testing.T) {
	entry, err := structuredReviewFromMarkdown(1, editorCacheTestMarkdown)
	if err != nil {
		t.Fatalf("valid Editor Markdown rejected: %v", err)
	}
	if entry.Verdict != "accept" || len(entry.Dimensions) != len(reviewDimensionNames) {
		t.Fatalf("valid Editor Markdown parsed unexpectedly: %+v", entry)
	}
	optionalMarkdown := strings.Replace(
		editorCacheTestMarkdown,
		"## 是否需要改写：否",
		"## 是否需要改写：可选",
		1,
	)
	if optional, err := structuredReviewFromMarkdown(1, optionalMarkdown); err != nil || optional.Verdict != "accept" {
		t.Fatalf("valid optional Editor disposition changed behavior: entry=%+v err=%v", optional, err)
	}
	rewriteMarkdown := strings.Replace(
		editorCacheTestMarkdown,
		"## 是否需要改写：否",
		"## 是否需要改写：是",
		1,
	)
	if rewrite, err := structuredReviewFromMarkdown(1, rewriteMarkdown); err != nil || rewrite.Verdict != "rewrite" {
		t.Fatalf("explicit Editor rewrite was not preserved: entry=%+v err=%v", rewrite, err)
	}
	shadowedRewrite := "<!-- ## 是否需要改写：否 -->\n" + rewriteMarkdown
	if rewrite, err := structuredReviewFromMarkdown(1, shadowedRewrite); err != nil || rewrite.Verdict != "rewrite" {
		t.Fatalf("comment shadow changed exact Editor disposition: entry=%+v err=%v", rewrite, err)
	}

	tests := []struct {
		name     string
		markdown string
	}{
		{
			name:     "partial dimension table",
			markdown: strings.Replace(editorCacheTestMarkdown, "| 8 AI 腔检测 | 0 | 未发现阻断性模板化表达。 |\n", "", 1),
		},
		{
			name:     "duplicate rewrite disposition",
			markdown: editorCacheTestMarkdown + "\n## 是否需要改写：否\n",
		},
		{
			name:     "duplicate dimension",
			markdown: strings.Replace(editorCacheTestMarkdown, "## 主要问题", "| 1 设定一致性 | 0 | 重复行。 |\n\n## 主要问题", 1),
		},
		{
			name:     "out of range dimension score",
			markdown: strings.Replace(editorCacheTestMarkdown, "| 1 设定一致性 | 1 |", "| 1 设定一致性 | 6 |", 1),
		},
		{
			name:     "non integer dimension score",
			markdown: strings.Replace(editorCacheTestMarkdown, "| 1 设定一致性 | 1 |", "| 1 设定一致性 | 1.5 |", 1),
		},
		{
			name:     "out of range overall score",
			markdown: strings.Replace(editorCacheTestMarkdown, "35 / 40", "41 / 40", 1),
		},
		{
			name:     "malformed overall score",
			markdown: strings.Replace(editorCacheTestMarkdown, "35 / 40", "三十五分", 1),
		},
		{
			name:     "missing diagnosis",
			markdown: strings.Replace(editorCacheTestMarkdown, "## 一句话诊断：正文通过。\n", "", 1),
		},
		{
			name:     "wrong chapter title",
			markdown: strings.Replace(editorCacheTestMarkdown, "# ch01 评审", "# ch02 评审", 1),
		},
		{
			name:     "swapped canonical dimension label",
			markdown: strings.Replace(editorCacheTestMarkdown, "| 1 设定一致性 |", "| 1 角色行为 |", 1),
		},
		{
			name:     "dimension row outside dimension section",
			markdown: strings.Replace(editorCacheTestMarkdown, "## 八维打分\n", "| 1 设定一致性 | 0 | shadow |\n\n## 八维打分\n", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if entry, err := structuredReviewFromMarkdown(1, tt.markdown); err == nil {
				t.Fatalf("invalid Editor Markdown synthesized entry: %+v", entry)
			}
		})
	}
}

func TestLoadOrGenerateEditorReviewPreservesRewriteBeforeCaching(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把手机翻过来。"
	analysis := editorCacheTestAnalysis(body, "2026-07-22T10:00:00+08:00")
	rewriteMarkdown := strings.Replace(
		editorCacheTestMarkdown,
		"## 是否需要改写：否",
		"## 是否需要改写：是",
		1,
	)
	model := &reviewCacheModel{response: rewriteMarkdown}

	result := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if result.Err != nil {
		t.Fatalf("valid explicit rewrite was rejected: %+v", result)
	}
	entry, err := structuredReviewFromMarkdown(1, result.Review)
	if err != nil || entry.Verdict != "rewrite" {
		t.Fatalf("explicit rewrite was softened after model response: entry=%+v err=%v", entry, err)
	}
	if result.CacheArtifact == nil || !result.CachePersisted || result.CacheHit || result.ModelCalls != 1 {
		t.Fatalf("valid rewrite did not reach the exact-response cache path: %+v", result)
	}
	cacheFiles, err := filepath.Glob(filepath.Join(dir, "reviews", reviewExistingCacheDirectoryName, editorReviewCacheBranch, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cacheFiles) != 1 {
		t.Fatalf("valid Editor rewrite cache files=%v", cacheFiles)
	}
}

func TestBuildEditorChapterReviewContextUsesResultLevelContract(t *testing.T) {
	st := store.NewStore(t.TempDir())
	plan := domain.ChapterPlan{
		Chapter: 1,
		Title:   "失业饭桌",
		Contract: domain.ChapterContract{RequiredBeats: []string{
			"系统显示一百万元额度；林澈用旧债等两至三个短动作验证边界。",
			"系统绑定一百万元额度。",
		}},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	context := buildEditorChapterReviewContext(st, 1)
	if strings.Contains(context, "两至三个短动作") {
		t.Fatalf("Editor context leaked process recipe: %s", context)
	}
	for _, want := range []string{"系统显示一百万元额度", "结果级要求", "逐项照抄 plan"} {
		if !strings.Contains(context, want) {
			t.Fatalf("Editor context missing %q: %s", want, context)
		}
	}
	if strings.Contains(context, "系统绑定一百万元额度") {
		t.Fatalf("Editor context kept duplicate staging language instead of the visible result: %s", context)
	}
}

func TestEditorReviewPayloadNeverTruncatesStyleContractAndHashesProviderBytes(t *testing.T) {
	styleMarker := "STYLE-TAIL-" + strings.Repeat("风格证据", 2500)
	raw, err := json.Marshal(map[string]any{
		"style_contract": map[string]any{
			"version": 3,
			"configured_style": map[string]any{
				"id":    "large-style",
				"rules": []string{styleMarker},
			},
		},
		"chapter": 1,
		"contract": map[string]any{
			"required_beats": []string{strings.Repeat("冻结结果合同", 3000)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	providerPayload := editorReviewChapterContextPayload(string(raw))
	if !strings.Contains(providerPayload, styleMarker) {
		t.Fatal("untruncated style contract tail is missing from Editor provider payload")
	}
	if !strings.Contains(providerPayload, "同源只读 style_contract（不可截断）") ||
		!strings.Contains(providerPayload, "结构化预算 8000 bytes") {
		t.Fatalf("Editor payload did not separate style and plan budgets: %s", providerPayload[:min(len(providerPayload), 500)])
	}
	policy := newEditorReviewCachePolicy(
		"openai", "editor-v1", "premise", "rules", string(raw),
		1, "chapter body", "ai-voice-context",
	)
	if policy.ChapterReviewContextSHA256 != reviewExistingSHA256(providerPayload) {
		t.Fatalf("Editor cache hashed different bytes than provider payload: policy=%s payload=%s",
			policy.ChapterReviewContextSHA256, reviewExistingSHA256(providerPayload))
	}
}

func TestSealedDrafterAndEditorConsumeExactStyleReceiptBytes(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Title: "同源风格"}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	base := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{
			"version":11,"chapter":1,"title":"同源风格",
			"required_beats":["冻结事件"],
			"style_contract":{"version":3}
		}}
	}`)
	frozenContext, err := toolspkg.PublishFrozenDraftRenderContext(st, 1, checkpoint.Digest, base)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(seed string) string { return "sha256:" + strings.Repeat(seed, 64) }
	frozen := &pipelineFrozenPlan{
		Version: pipelinePlanningSchema, Chapter: 1,
		PlanDigest: checkpoint.Digest, PlanCheckpointSeq: checkpoint.Seq,
		PlanningGenerationID: "pg2_editor-style-receipt", ProjectionBinding: "sealed_v2",
		ProjectedBundleDigest: digest("2"), PromotionReceiptDigest: digest("3"),
		PipelineRunInputDigest: digest("4"), RenderContextSHA256: frozenContext.PayloadSHA256,
	}
	if _, err := writePipelinePlanningJSON(
		filepath.Join(st.Dir(), filepath.FromSlash(pipelineFrozenPlanPath)), frozen,
	); err != nil {
		t.Fatal(err)
	}
	manifest := pipelineRenderCandidateManifest{
		Version:     pipelineRenderCandidatePreStyleManifestVersion,
		CandidateID: "render-ch0001-editor-style-receipt", GenerationID: frozen.PlanningGenerationID,
		Chapter: 1, PlanDigest: frozen.PlanDigest, PlanCheckpointSeq: frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
		PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
		RenderContextSHA256:       frozen.RenderContextSHA256,
		SourceOutputDir:           st.Dir(),
	}
	manifestPath := filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json")
	if _, err := writePipelinePlanningJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	receipt, err := toolspkg.PublishEffectiveRenderStyleContract(
		st,
		toolspkg.EffectiveRenderStyleContractIdentity{
			GenerationID: frozen.PlanningGenerationID, Chapter: 1,
			PlanDigest: frozen.PlanDigest, PlanCheckpointSeq: frozen.PlanCheckpointSeq,
			BaseRenderContextSHA256:   frozen.RenderContextSHA256,
			PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
			ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
			PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
			CandidateID:               manifest.CandidateID,
		},
		"realist",
		"# 同源风格\n- 叙述声音：克制具体。\n- 句法：压力处缩短。\n- 冲突设计：不得进入表达合同。",
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = pipelineRenderCandidateManifestVersion
	manifest.EffectiveStyleReceiptDigest = receipt.ReceiptDigest
	if _, err := writePipelinePlanningJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 1,
		PlanDigest: frozen.PlanDigest, Owner: "editor-style-receipt-test",
	}); err != nil {
		t.Fatal(err)
	}

	drafterContext, err := toolspkg.NewContextTool(st, toolspkg.References{}, "changed-current-style").
		WithConfiguredStyle("# changed\n- 叙述声音：不应出现。").
		Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`))
	if err != nil {
		t.Fatal(err)
	}
	drafterStyle, err := toolspkg.ExtractRenderStyleContract(drafterContext)
	if err != nil {
		t.Fatal(err)
	}
	drafterStyleRaw, err := json.Marshal(drafterStyle)
	if err != nil {
		t.Fatal(err)
	}
	editorStyleRaw, err := buildEditorReviewStyleContract(
		st,
		1,
		toolspkg.References{},
		"changed-current-style",
		"# changed\n- 叙述声音：也不应出现。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(drafterStyleRaw, receipt.StyleContract) ||
		!bytes.Equal(editorStyleRaw, receipt.StyleContract) {
		t.Fatalf("Drafter/Editor/receipt style bytes differ\nreceipt=%s\ndrafter=%s\neditor=%s",
			receipt.StyleContract, drafterStyleRaw, editorStyleRaw)
	}
	if err := st.Runtime.ReleasePipelineExecution("editor-style-receipt-test"); err != nil {
		t.Fatal(err)
	}
	standaloneEditorStyle, err := buildEditorReviewStyleContract(
		st,
		1,
		toolspkg.References{},
		"changed-again-after-render-lock",
		"# changed again\n- 叙述声音：standalone Editor 也不得采用。",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(standaloneEditorStyle, receipt.StyleContract) {
		t.Fatalf("standalone Editor ignored the chapter-owned style archive\nreceipt=%s\neditor=%s",
			receipt.StyleContract, standaloneEditorStyle)
	}
}

func TestEditorReviewRequiredOutcomesDropsOnlyProcessRecipes(t *testing.T) {
	compound := "68000元取货款必须继续阻断；只准落地五摊；灯具材料680元、五金360元、老丁人工300元分别准确；往返43公里、油费86元、半日人工180元全部留痕；冷饮支架只允许唯一一次失败复测，不得增加第六套。"
	plan := domain.ChapterPlan{Contract: domain.ChapterContract{RequiredBeats: []string{
		"系统显示一百万元额度；林澈用旧债等两至三个短动作验证边界；旧债测试被明确拒绝。",
		compound,
		"逐笔票据核查",
	}}}

	complete := toolspkg.RenderRequiredOutcomes(plan)
	if len(complete) == 0 || !strings.Contains(complete[0], "两至三个短动作") {
		t.Fatalf("Drafter-facing hard outcome was unexpectedly shortened: %#v", complete)
	}
	got := editorReviewRequiredOutcomes(plan)
	if len(got) != 2 || got[0] != "系统显示一百万元额度；旧债测试被明确拒绝" || got[1] != strings.TrimSuffix(compound, "。") {
		t.Fatalf("Editor result projection removed a hard result or kept a recipe: %#v", got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"68000元", "五摊", "680元", "360元", "300元", "43公里", "86元", "180元", "唯一一次失败复测",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Editor projection lost hard result %q: %s", want, joined)
		}
	}
	for _, recipe := range []string{"两至三个短动作", "逐笔票据核查"} {
		if strings.Contains(joined, recipe) {
			t.Fatalf("Editor projection kept process recipe %q: %s", recipe, joined)
		}
	}
}

func TestSanitizeEditorReviewRemovesMissingPayloadAndAbsentOptionalTrendClaims(t *testing.T) {
	st := store.NewStore(t.TempDir())
	plan := domain.ChapterPlan{
		Chapter: 1,
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，……", CharacterCarrier: "赵航"}},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	body := "第一章 失业饭桌\n\n赵航替林澈挡了一句，饭桌安静下来。"
	analysis := domain.AIVoiceAnalysis{
		Chapter:    1,
		BodySHA256: reviewreport.BodySHA256(body),
		Label:      "✅ 可通过",
		Summary:    "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{
			Chapter: 1, ParagraphCount: 2, SentenceCount: 2,
			FigurativeDensity: 0.02, DialogueRatio: 0.31, ProtagonistWaver: true,
		},
	}
	dimensions := make([]domain.DimensionScore, 0, len(reviewDimensionNames))
	for _, name := range reviewDimensionNames {
		score, comment := 90, "通过"
		verdict := "pass"
		if name == "ai_voice_detection" {
			score = 70
			verdict = "warning"
			comment = "未读取red flag JSON，需补检。"
		}
		dimensions = append(dimensions, domain.DimensionScore{Dimension: name, Score: score, Verdict: verdict, Comment: comment})
	}
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "polish", Summary: "AI腔检测流程缺失，需补检。",
		Dimensions: dimensions,
		Issues: []domain.ConsistencyIssue{
			{Type: "aesthetic", Severity: "warning", Description: "未读取red flag JSON，需补检。"},
			{Type: "aesthetic", Severity: "warning", Description: "赵航热梗未完全落地：呱，功能仅完成50%。"},
		},
	}

	removed := sanitizeEditorReviewForProject(st, 1, body, analysis, &entry)
	if len(removed) != 2 || len(entry.Issues) != 0 {
		t.Fatalf("sanitized issues = %#v removed=%#v", entry.Issues, removed)
	}
	if entry.Verdict != "accept" || strings.Contains(entry.Summary, "需补检") {
		t.Fatalf("sanitized review did not recover accept: %+v", entry)
	}
	for _, dimension := range entry.Dimensions {
		if dimension.Dimension == "ai_voice_detection" && (dimension.Score < 90 || dimension.Verdict != "pass") {
			t.Fatalf("AI voice dimension not repaired from deterministic payload: %+v", dimension)
		}
	}
}

func TestReconcileWarningOnlyEditorReviewUsesIndependentSameHashGates(t *testing.T) {
	body := "第一章 失业饭桌\n\n林澈把事情办成了。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, BodySHA256: hash, ContractStatus: "met", Verdict: "polish",
		Issues:     []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "可再打磨"}},
		Dimensions: defaultReviewDimensions(),
	}
	mechanical := &reviewreport.MechanicalGatePayload{Chapter: 1, BodySHA256: hash}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "✅ 可通过", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true, Blocking: false,
	}

	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("same-hash independent passing gates should reconcile warning-only Editor drift")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || entry.Dimensions[0].Score < 80 {
		t.Fatalf("reconciled entry = %+v", entry)
	}

	blocked := entry
	blocked.Verdict = "polish"
	blockedJudge := *judge
	blockedJudge.Blocking = true
	if reconcileWarningOnlyEditorReview(&blocked, editorCacheTestMarkdown, hash, mechanical, analysis, &blockedJudge) {
		t.Fatal("blocking independent judge must never be overridden")
	}
}

type c8FormalReviewFixture struct {
	markdown   string
	body       string
	hash       string
	entry      domain.ReviewEntry
	mechanical *reviewreport.MechanicalGatePayload
	analysis   domain.AIVoiceAnalysis
	judge      *deepseekAIJudgeArtifact
}

func newC8FormalReviewFixture() c8FormalReviewFixture {
	body := "第8章 西侧哪来的客梯\n\n程野几乎想开麦追问，最后关掉字幕，把纸推回镜头能照见的位置。"
	hash := reviewreport.BodySHA256(body)
	dimensions := defaultReviewDimensions()
	for i := range dimensions {
		dimensions[i].Score = 100
		dimensions[i].Verdict = "pass"
		dimensions[i].Comment = "本维度通过。"
	}
	dimensions[7].Comment = "mechanical_prose_violations 清除：pov_interiority_thin：程野从几乎想开麦到关掉字幕，主观体验改变了她的行动选择与判断，target 已满足，warning 清除。"

	return c8FormalReviewFixture{
		markdown: "# ch08 评审\n\n## 是否需要改写：否\n",
		body:     body,
		hash:     hash,
		entry: domain.ReviewEntry{
			Chapter: 8, BodySHA256: hash, Scope: "chapter", ContractStatus: "met", Verdict: "accept",
			Summary: "合同与八维均通过。", Dimensions: dimensions,
		},
		mechanical: &reviewreport.MechanicalGatePayload{
			Chapter: 8, BodySHA256: hash,
			AIGCReport: aigc.Report{
				AIGCPercent: 79.33, WholeTextSegmentGate: 79.33, SegmentRiskFloor: 79.33,
				Stats: aigc.Stats{Hanzi: 2154, HumanAnchor: map[string]any{
					"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
				}},
			},
			RuleViolations: []rules.Violation{
				{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning, Actual: 1},
				{Rule: "aigc_ratio", Severity: rules.SeverityError, Actual: 79.33},
			},
		},
		analysis: domain.AIVoiceAnalysis{
			Chapter: 8, BodySHA256: hash, Label: "需打磨", Summary: "仅有 warning。",
			Metrics: domain.ChapterAIVoiceMetrics{Chapter: 8, ParagraphCount: 41, SentenceCount: 120},
			RedFlags: []domain.AIVoiceRedFlag{
				{Rule: "protagonist_waver_missing", Severity: "warning"},
				{Rule: "dialogue_info_dump", Severity: "warning"},
			},
		},
		judge: &deepseekAIJudgeArtifact{
			Chapter: 8, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
			AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
		},
	}
}

func TestReconcileC8ExplicitNoRewriteClearsOnlyExactBodyStatisticalFalsePositives(t *testing.T) {
	fixture := newC8FormalReviewFixture()
	fixture.entry.Issues = []domain.ConsistencyIssue{
		{
			Type: "aesthetic", Severity: "warning",
			Description: "许知遥的承认对话存在轻微的信息倾倒节奏，但不阻断本章通过。建议后续章注意对话气口。",
			Evidence:    "当前受控直播场景中仍有剧情必要性。",
		},
		{Type: "aesthetic", Severity: "warning", Description: "（无其他问题）", Evidence: "（无其他问题）"},
	}
	removed := sanitizeEditorReviewForProject(nil, 8, fixture.body, fixture.analysis, &fixture.entry)
	if len(removed) != 1 || len(fixture.entry.Issues) != 0 {
		t.Fatalf("C8 explicit nonblocking issues were not normalized: removed=%v issues=%+v", removed, fixture.entry.Issues)
	}
	if reviewHasStructuralProseFailure(&fixture.entry, fixture.mechanical) {
		t.Fatal("C8 warning-clearance wording should not be preempted as a structural failure")
	}
	if !reconcileWarningOnlyEditorReview(&fixture.entry, fixture.markdown, fixture.hash, fixture.mechanical, fixture.analysis, fixture.judge) {
		t.Fatal("C8 exact-body Editor no-rewrite and complete DeepSeek 2% pass should reconcile the probability-only false positive")
	}
	if fixture.entry.Verdict != "accept" || len(fixture.entry.Issues) != 0 || !fixture.mechanical.ExternalCorroborated ||
		reviewExistingAIGCGatePercent(fixture.mechanical.AIGCReport) != 2 {
		t.Fatalf("C8 reconciliation did not persist a calibrated accept: entry=%+v mechanical=%+v", fixture.entry, fixture.mechanical)
	}
	if !reviewreport.AcceptedWarningOnlyGate(fixture.mechanical, &fixture.analysis, &fixture.entry) {
		t.Fatal("shared save_review/delivery gate did not retain the exact-body C8 acceptance")
	}
}

func TestReconcileC8ExplicitNoRewriteRemainsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*c8FormalReviewFixture)
	}{
		{name: "editor did not explicitly say no", mutate: func(f *c8FormalReviewFixture) { f.markdown = "## 是否需要改写：是" }},
		{name: "wrong editor body hash", mutate: func(f *c8FormalReviewFixture) { f.entry.BodySHA256 = "wrong" }},
		{name: "external four percent", mutate: func(f *c8FormalReviewFixture) { f.judge.AIProbabilityPercent = 4 }},
		{name: "external advice incomplete", mutate: func(f *c8FormalReviewFixture) { f.judge.AdviceComplete = false }},
		{name: "contract missed", mutate: func(f *c8FormalReviewFixture) {
			f.entry.ContractStatus = "missed"
			f.entry.ContractMisses = []string{"required beat missing"}
		}},
		{name: "dimension failed", mutate: func(f *c8FormalReviewFixture) {
			f.entry.Dimensions[2].Score = 50
			f.entry.Dimensions[2].Verdict = "fail"
		}},
		{name: "error issue", mutate: func(f *c8FormalReviewFixture) {
			f.entry.Issues = []domain.ConsistencyIssue{{Type: "continuity", Severity: "error", Description: "时间线断裂", Evidence: "顺序倒置"}}
		}},
		{name: "critical issue", mutate: func(f *c8FormalReviewFixture) {
			f.entry.Issues = []domain.ConsistencyIssue{{Type: "consistency", Severity: "critical", Description: "身份矛盾", Evidence: "同一人物两种身份"}}
		}},
		{name: "deterministic mechanical error", mutate: func(f *c8FormalReviewFixture) {
			f.mechanical.RuleViolations = append(f.mechanical.RuleViolations, rules.Violation{Rule: "impossible_line_of_sight", Severity: rules.SeverityError})
		}},
		{name: "content integrity blocker", mutate: func(f *c8FormalReviewFixture) {
			f.mechanical.AIGCReport.ContentIntegrityFloor = 25
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newC8FormalReviewFixture()
			tt.mutate(&fixture)
			if reconcileWarningOnlyEditorReview(&fixture.entry, fixture.markdown, fixture.hash, fixture.mechanical, fixture.analysis, fixture.judge) {
				t.Fatalf("unsafe C8 case was reconciled: %+v", fixture.entry)
			}
			if fixture.entry.Verdict != "accept" {
				// The fixture starts at the Editor's structured accept. Reconciliation
				// must fail without inventing a replacement verdict or mutating evidence.
				t.Fatalf("failed reconciliation mutated Editor verdict: %s", fixture.entry.Verdict)
			}
		})
	}
}

func TestReconcileC12ExplicitNonblockingDialogueInfoDump(t *testing.T) {
	body := "第12章 三个月后，镜头没有开\n\n许知遥在关机后当面认责，程野等她说完才回答。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 12, BodySHA256: hash, Scope: "chapter", ContractStatus: "met", Verdict: "rewrite",
		Dimensions: defaultReviewDimensions(),
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "dialogue_info_dump（warning，不触发返工）——认责对白有叙事必要性，无需改写。",
			Evidence:    "关机后当面对质，声音在最后几个字慢下来。",
		}},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 12, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 2.45, Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 12, BodySHA256: hash, Label: "⚠️ 需打磨", Summary: "命中 1 项 warning。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 12, ParagraphCount: 45, SentenceCount: 90, DialogueRatio: 0.14},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "dialogue_info_dump", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 12, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
	}
	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("an explicitly nonblocking dialogue-info warning must not become a structural rewrite")
	}
	if !reconcileWarningOnlyEditorReview(&entry, "## 是否需要改写：否", hash, mechanical, analysis, judge) {
		t.Fatal("C12 exact-body no-rewrite Editor and same-hash 2% reviewer should reconcile")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated {
		t.Fatalf("C12 nonblocking warning did not persist accept: entry=%+v mechanical=%+v", entry, mechanical)
	}
}

func TestExplicitNonblockingIssueDoesNotClearCurrentChapterAction(t *testing.T) {
	issue := domain.ConsistencyIssue{
		Type: "aesthetic", Severity: "warning",
		Description: "dialogue_info_dump warning，不触发返工，但当前章该段仍需修改，必须改写后再审。",
		Evidence:    "承认台词连续列举四项。",
	}
	if reviewIssueIsExplicitlyNonActionable(issue) {
		t.Fatal("a current-chapter edit demand must win over an earlier nonblocking phrase")
	}
	if !reviewIssueIsStructuralProseFailure(issue) {
		t.Fatal("the surviving information-dump issue should remain structurally blocking")
	}
}

func TestEditorAIVoicePayloadPassedAllowsNonblockingChapterFunctionAdvice(t *testing.T) {
	body := "第三章\n\n林澈把车停在桥头，改口问摊主愿不愿意先试一晚。"
	for _, severity := range []string{"info", "warning"} {
		t.Run(severity, func(t *testing.T) {
			analysis := domain.AIVoiceAnalysis{
				Chapter: 3, BodySHA256: reviewreport.BodySHA256(body), Label: "✅ 可通过",
				Summary:  "规则引擎未发现硬性 AI 腔红旗；记录 1 项非阻断跨章规划建议。",
				Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 3, ParagraphCount: 2, SentenceCount: 2},
				RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: severity}},
			}
			if !editorAIVoicePayloadPassed(3, body, analysis) {
				t.Fatalf("nonblocking planning advice severity=%q invalidated an otherwise clean exact-body payload", severity)
			}
		})
	}
}

func TestEditorSystemPromptKeepsCrossChapterAdviceNonblocking(t *testing.T) {
	for _, want := range []string{"chapter_function_repetition", "非阻断规划建议", "不得降低当前章任何维度评分", "不得写入当前章主要问题"} {
		if !strings.Contains(editorSystemPrompt, want) {
			t.Fatalf("embedded review-existing prompt missing cross-chapter boundary %q", want)
		}
	}
}

func TestEditorSystemPromptTreatsCanonicalChapterHeadingAsMetadata(t *testing.T) {
	for _, want := range []string{
		"规范章标题元数据",
		"不是叙事段落",
		"不得把标题文字当作 opening_single_sentence_aphorism",
		"不得要求删除标题",
		"检测范围错位",
	} {
		if !strings.Contains(editorSystemPrompt, want) {
			t.Fatalf("embedded review-existing prompt missing chapter-heading boundary %q", want)
		}
	}
	if editorReviewProtocolVersion != "review-existing/editor/v6-style-contract" {
		t.Fatalf("editor review protocol = %q, want v6 style contract", editorReviewProtocolVersion)
	}
	policy := newEditorReviewCachePolicy(
		"openai", "editor-v1", "premise", "rules", "chapter-context",
		6, "第6章 他等的从来不是外卖\n\n零点零三分，她收回了手。", "ai-context",
	)
	if policy.UserPayloadKind != editorReviewUserPayloadKind || policy.UserPayloadKind == "" {
		t.Fatalf("editor cache user payload kind = %q", policy.UserPayloadKind)
	}
}

func TestReconcileWarningOnlyEditorReviewKeepsStructuralProseFailureBlocking(t *testing.T) {
	body := "第一章\n\n众人轮流把流程说完。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "dialogue_conveyor_overuse：对白传送带让场景像工作汇报。",
		}},
		Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: 75, Verdict: "warning"}},
	}
	mechanical := &reviewreport.MechanicalGatePayload{Chapter: 1, BodySHA256: hash}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "可通过", Summary: "未发现机械硬伤",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("external low score must not erase a structural prose failure")
	}
	if entry.Verdict != "rewrite" || len(entry.Issues) != 1 {
		t.Fatalf("structural warning was mutated: %+v", entry)
	}
}

func TestMechanicalHasStructuralProseWarning(t *testing.T) {
	if mechanicalHasStructuralProseWarning(nil) {
		t.Fatal("nil mechanical gate must not report a structural warning")
	}
	plain := &reviewreport.MechanicalGatePayload{RuleViolations: []rules.Violation{{
		Rule: "object_response_overuse", Severity: rules.SeverityWarning,
	}}}
	if mechanicalHasStructuralProseWarning(plain) {
		t.Fatal("ordinary style warning must not enter Editor structural calibration path")
	}
	plain.RuleViolations = append(plain.RuleViolations, rules.Violation{
		Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning,
	})
	if !mechanicalHasStructuralProseWarning(plain) {
		t.Fatal("structural warning should enter the narrow Editor calibration path")
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsExplicitlyClearedStructuralWarning(t *testing.T) {
	body := "第三章\n\n林澈把手机塞回口袋，先看了一眼桥口的人群。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 3, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "aesthetic", Severity: "warning",
			Description: "一句现场调侃略硬，但当前表达不伤害阅读体验。",
		}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass", Comment: "合同一致。"},
			{Dimension: "character", Score: 100, Verdict: "pass", Comment: "人物一致。"},
			{Dimension: "pacing", Score: 100, Verdict: "pass", Comment: "节奏通过。"},
			{Dimension: "continuity", Score: 100, Verdict: "pass", Comment: "连续性通过。"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass", Comment: "伏笔通过。"},
			{Dimension: "hook", Score: 100, Verdict: "pass", Comment: "钩子通过。"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass", Comment: "审美通过。"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "dialogue_conveyor_overuse 提示为 warning，原文已通过主视角停留有效打断，无需改写。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 3, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 2.57, BlendedAIGCPercent: 2.57,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(98), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{
			Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 1,
		}},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 3, BodySHA256: hash, Label: "需打磨", Summary: "命中 1 项 warning 级红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 3, ParagraphCount: 40, SentenceCount: 80},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 3, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
	}

	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("an explicitly inspected and effectively interrupted structural warning should not preempt consensus reconciliation")
	}
	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("same-hash independent passing gates should reconcile the explicitly cleared warning")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 {
		t.Fatalf("reconciled entry = %+v", entry)
	}
	if !reviewreport.ApplyExternalCorroborationWithEditor(mechanical, deepSeekExternalAIJudge(judge), &entry) {
		t.Fatal("accepted exact-body Editor evidence should permit same-hash external calibration")
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("shared artifact gate disposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsChineseExactBodyClearance(t *testing.T) {
	body := "第一章\n\n饭桌上的快接话停在父亲推开的鱼盘旁。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{
			Type: "pacing", Severity: "warning",
			Description: "对白传送带在家庭饭桌快接话中成立，但本章不构成强制改写。",
		}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 90, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "物件回应延迟命中3次、对白传送带19个重叠窗口，均为警示级别；正文已有动作与沉默作为有效打断，且有物件延迟与缺席；不影响本单章阅读，无需改写。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 7.17, ZhuqueCompositePercent: 2.02, LegacyHeuristicPercent: 1.49,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 3},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 19},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 7.17},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "需打磨", Summary: "仅有 advisory warning。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 52, SentenceCount: 105},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}

	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("exact-body Chinese clearance and passing external evidence should reconcile statistical warnings")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated ||
		reviewExistingAIGCGatePercent(mechanical.AIGCReport) >= deepseekAIJudgePassExclusive {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
}

func TestReconcileC10PassesExplicitRuleEvidenceBeforeAggregateCalibration(t *testing.T) {
	body := "第十章\n\n程野把两路音轨重新对齐，仍没有替任何一列下结论。"
	hash := reviewreport.BodySHA256(body)
	dimensions := defaultReviewDimensions()
	for i := range dimensions {
		dimensions[i].Score = 100
		dimensions[i].Verdict = "pass"
		dimensions[i].Comment = "本维度通过。"
	}
	dimensions[5].Score = 90
	dimensions[7].Comment = "机械门禁项：not_but_overuse 2 次，主体两处间隔超过15段，语境不同且无模板感，判通过；object_response_rhythm_flat 显示4处延迟，但原文中的没有追问、没有立即圈它、没有偏向任何一列全部嵌入合理人物决策步骤，并非单调重复节奏，不影响叙事推进，不触发返工。"
	entry := domain.ReviewEntry{
		Chapter: 10, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues:     []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "仅保留后续章观察项。"}},
		Dimensions: dimensions,
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 10, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 1.96, BlendedAIGCPercent: 1.96,
			Dimensions: map[string]aigc.Dimension{
				"structure_fingerprint": {Name: "结构指纹", Score: 48},
			},
			Stats: aigc.Stats{Hanzi: 2500, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "not_but_overuse", Severity: rules.SeverityWarning, Actual: 2, Limit: 1},
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 4},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 10, BodySHA256: hash, Label: "需打磨", Summary: "仅有 advisory warning。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 10, ParagraphCount: 50, SentenceCount: 100},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "protagonist_waver_missing", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 10, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
	}

	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("C10 explicit rule evidence should reach consensus reconciliation instead of preemptive rewrite")
	}
	if !reconcileWarningOnlyEditorReview(&entry, "# ch10 评审\n\n## 是否需要改写：否\n", hash, mechanical, analysis, judge) {
		t.Fatalf("C10 exact-body Editor no-rewrite and DeepSeek 2%% pass did not reconcile; blockers=%v", mechanical.CorroborationBlockedBy)
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated {
		t.Fatalf("C10 reconciliation result entry=%+v mechanical=%+v", entry, mechanical)
	}
	if got := reviewreport.BlockingAIGCDimensionReasons(mechanical.AIGCReport); len(got) != 0 {
		t.Fatalf("C10 calibrated 48%% aggregate remained blocking: %v", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsRuleIDClearanceAndExactExternalPass(t *testing.T) {
	body := "第一章\n\n壶面先映出画外的手，程野压住猜测，等姜岚追问后才回答。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, BodySHA256: hash, ContractStatus: "met", Verdict: "accept",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 100, Verdict: "pass", Comment: "object_response_rhythm_flat（延迟）：壶面倒影先出现画外手势，数段后再独立确认，延迟与缺席均已发生，rule ID清除。dialogue_conveyor_overuse：对白中有姜岚追问、程野应声与保留的沉默，后续未连续滚九段以上，不触发。pov_interiority_thin：这个念头压住了她报出猜测的冲动，已超过单纯情绪标签，rule ID清除。无 structural warning 阻断本章。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 10.76, AIRatioPercent: 10.76, BlendedAIGCPercent: 10.76,
			ZhuqueCompositePercent: 0.65, LegacyHeuristicPercent: 3.24,
			Stats: aigc.Stats{Hanzi: 2300, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_rhythm_flat", Severity: rules.SeverityWarning, Actual: 4},
			{Rule: "dialogue_conveyor_overuse", Severity: rules.SeverityWarning, Actual: 2},
			{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning, Actual: 3},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 10.76},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "通过", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 37, SentenceCount: 96},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("exact-body rule-ID clearance should prevent pre-reconciliation rewrite")
	}
	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("exact-body Editor and DeepSeek pass should reconcile local statistical warnings")
	}
	if entry.Verdict != "accept" || !mechanical.ExternalCorroborated || reviewExistingAIGCGatePercent(mechanical.AIGCReport) >= deepseekAIJudgePassExclusive {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsCurrentChapterTwoExactAIVoiceClearance(t *testing.T) {
	body := "第二章\n\n程野没有把任何一个地名填进记录。"
	hash := reviewreport.BodySHA256(body)
	catalogEvidence := "镜头稍稍偏过，餐盒侧面的封签露出半截分店字样；汤面不再起雾，却不能判断送达多久；后方提示灯的颜色可能属于货梯，也可能只是设备待机；壶面和桌边那道金属反光，只能说明空间里有对应材质"
	comment := "red flag 1：supporting_dialogue_ratio 实际 0.07，低于 0.12 限制，rule ID supporting_dialogue_ratio，severity warning。本章协作通道回复为归纳转述而非直接引用对话，现场无其他配角主动误解、打断或拒绝的对白；但本章场景为程野独处停车场、仅接收姜岚转达结论，叙事限定程野视角，姜岚信息的转述方式和角色位置（联合处置组）构成有效打断（她只在通道里给结论，不直接通话），无需改写。" +
		"red flag 2：catalog_stuffing 第 44 段「" + catalogEvidence + "」共 8 个物件，超过 7 限制，rule ID catalog_stuffing，severity warning。但该段随即接「程野没有把任何一个地名填进记录」及后续动作（将四项并栏、另起一行待交叉、退出地址栏），四项物件被明确压入待核而非直接入账或触发规则，并在章末形成第 3 章交叉入口，不构成检测投机清单；不触发返工。" +
		"red flag 3：chapter_function_repetition 为面向下一章的非阻断建议，不纳入当前章节评分。mechanical_prose_violations：semicolon_overuse 实际 9，limit 6，severity warning，分号集中于第 44 段和少量复合句，与 catalog_stuffing 同源，建议后续拆分；isolated_sentence_overuse 实际未超连续限制，该段组以动作锚点推动叙事、不构成情绪断档，不触发返工；pov_interiority_thin 仅命中 1 处，不满足 threshold，本章已有「程野险些把熟悉当成答案」「可认得不能代替顺序，更不能代替地点」两处主观体验改变判断，不构成阻断。"
	entry := domain.ReviewEntry{Chapter: 2, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "章末钩子可在后续章加强。"}},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"}, {Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"}, {Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"}, {Dimension: "hook", Score: 90, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 80, Verdict: "pass", Comment: comment},
		}}
	mechanical := &reviewreport.MechanicalGatePayload{Chapter: 2, BodySHA256: hash,
		AIGCReport: aigc.Report{AIGCPercent: 6.71, ZhuqueCompositePercent: 1.5, LegacyHeuristicPercent: 3.24,
			Stats: aigc.Stats{Hanzi: 2300, HumanAnchor: map[string]any{"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{}}}},
		RuleViolations: []rules.Violation{
			{Rule: "semicolon_overuse", Severity: rules.SeverityWarning}, {Rule: "isolated_sentence_overuse", Severity: rules.SeverityWarning},
			{Rule: "pov_interiority_thin", Severity: rules.SeverityWarning}, {Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 6.71},
		}}
	analysis := domain.AIVoiceAnalysis{Chapter: 2, BodySHA256: hash, Metrics: domain.ChapterAIVoiceMetrics{Chapter: 2, ParagraphCount: 50, SentenceCount: 108}, RedFlags: []domain.AIVoiceRedFlag{
		{Rule: "supporting_dialogue_ratio", Severity: "warning", Actual: 0.07197290431837426, Limit: 0.12, Suggestion: "补一组配角主动误解、打断或拒绝的对话。"},
		{Rule: "catalog_stuffing", Severity: "warning", Paragraph: 44, Evidence: catalogEvidence, Actual: 8, Limit: 7, Suggestion: "删掉检测投机式长清单。"},
	}}
	judge := &deepseekAIJudgeArtifact{Chapter: 2, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low", AIProbabilityPercent: 2, AdviceComplete: true}

	if reviewHasStructuralProseFailure(&entry, mechanical) {
		t.Fatal("current C2 exact-body POV evidence should clear the structural warning")
	}
	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("current C2 exact-body Editor clearances and same-hash 2% external pass should reconcile")
	}
	if entry.Verdict != "accept" || !mechanical.ExternalCorroborated || reviewExistingAIGCGatePercent(mechanical.AIGCReport) >= deepseekAIJudgePassExclusive {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsContextualDialogueRatioErrorAfterExactBodyConsensus(t *testing.T) {
	body := "第四章\n\n程野盯着七条配送轨迹，姜岚问她：你漏了什么？"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 4, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{
			{Type: "aesthetic", Severity: "warning", Description: "一段自省可再压缩，不影响整体。"},
			{Type: "aesthetic", Severity: "warning", Description: "对话占比偏低为场景必然，姜岚声口可在后续加强。"},
		},
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 90, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "对话占比 0.0349 低于阈值，但本章为实况配送监控场景，全程仅两名人物同处一室，外部骑手静默履约、许知遥无对话机会；姜岚的主动提问已打断程野的抢答冲动，形成信息碰撞；rule ID: supporting_dialogue_ratio, actual 0.0349 < 0.12, severity: error —— 本章仅二人同室、骑手静默、许知遥受控无声，姜岚两次主动提问已构成必要冲突，无需改写，不触发返工。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 4, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 7.2, ZhuqueCompositePercent: 0.65, LegacyHeuristicPercent: 2.75,
			Stats: aigc.Stats{Hanzi: 2300, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "minor_mistake_overuse", Severity: rules.SeverityWarning, Actual: 3},
			{Rule: "vague_quantifier_overuse", Severity: rules.SeverityWarning, Actual: 5},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 7.2},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 4, BodySHA256: hash, Label: "需返工", Summary: "命中 1 项红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 4, ParagraphCount: 37, SentenceCount: 101, DialogueRatio: 0.03486870426173052},
		RedFlags: []domain.AIVoiceRedFlag{{
			Rule: "supporting_dialogue_ratio", Severity: "error", Actual: 0.03486870426173052, Limit: 0.12,
		}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 4, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true, Blocking: false,
	}

	if !reviewreport.ApplyExternalCorroborationWithEditor(mechanical, deepSeekExternalAIJudge(judge), &entry) {
		t.Fatal("same-hash 2% judge should calibrate the clean mechanical gate")
	}
	if !reviewreport.EditorExplicitlySupportsContextualDialogueRatioErrorClearance(&entry, mechanical, &analysis, analysis.RedFlags[0]) {
		t.Fatal("exact-body all-pass Editor evidence should clear only the contextual dialogue-ratio error")
	}
	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("all-pass Editor with explicit scene evidence and same-hash 2% judge should normalize the contextual dialogue-ratio error")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewAcceptsCalibratedLocalFalsePositive(t *testing.T) {
	body := "第一章\n\n林澈回到青山县，先把眼前的事做完。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 1, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues:     []domain.ConsistencyIssue{{Type: "pacing", Severity: "warning", Description: "饭桌对白略平"}},
		Dimensions: defaultReviewDimensions(),
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 1, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 11.19, ZhuqueCompositePercent: 3.36, LegacyHeuristicPercent: 4.3,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{
			{Rule: "object_response_overuse", Severity: rules.SeverityWarning},
			{Rule: "aigc_ratio", Severity: rules.SeverityWarning, Actual: 11.19},
		},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 1, BodySHA256: hash, Label: "需打磨", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 1, ParagraphCount: 2, SentenceCount: 2},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 1, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 3, AdviceComplete: true,
	}

	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("current-hash external pass should reconcile local heuristic false positive")
	}
	if entry.Verdict != "accept" || len(entry.Issues) != 0 || !mechanical.ExternalCorroborated {
		t.Fatalf("reconciled entry=%+v mechanical=%+v", entry, mechanical)
	}
}

func TestReconcileAllPassEditorAcceptsExactProviderOverWholeTextProbabilityProxy(t *testing.T) {
	body := "第五章\n\n程野把桥湾标记撤回，留在公共监控范围内等待原声。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 5, BodySHA256: hash, ContractStatus: "met", Verdict: "accept",
		Dimensions: []domain.DimensionScore{
			{Dimension: "consistency", Score: 100, Verdict: "pass"},
			{Dimension: "character", Score: 100, Verdict: "pass"},
			{Dimension: "pacing", Score: 100, Verdict: "pass"},
			{Dimension: "continuity", Score: 100, Verdict: "pass"},
			{Dimension: "foreshadow", Score: 100, Verdict: "pass"},
			{Dimension: "hook", Score: 100, Verdict: "pass"},
			{Dimension: "aesthetic", Score: 100, Verdict: "pass"},
			{Dimension: "ai_voice_detection", Score: 90, Verdict: "pass", Comment: "正文八维通过，无结构性返工项。"},
		},
	}
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 5, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 79.07, WholeTextSegmentGate: 79.07, SegmentRiskFloor: 79.07,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(100), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "aigc_ratio", Severity: rules.SeverityError, Actual: 79.07}},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 5, BodySHA256: hash, Label: "通过", Summary: "规则引擎未发现硬性 AI 腔红旗。",
		Metrics: domain.ChapterAIVoiceMetrics{Chapter: 5, ParagraphCount: 40, SentenceCount: 80},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 5, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}

	if !reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("all-pass exact-body Editor and 2% provider result should resolve a pure probability proxy")
	}
	if !mechanical.ExternalCorroborated || reviewExistingAIGCGatePercent(mechanical.AIGCReport) != 2 {
		t.Fatalf("probability proxy was not calibrated: %+v", mechanical)
	}
	if got := reviewreport.RewriteDisposition(mechanical, &analysis, deepSeekExternalAIJudge(judge), &entry); got != "可选" {
		t.Fatalf("RewriteDisposition = %q, want 可选", got)
	}
}

func TestReconcileWarningOnlyEditorReviewRejectsAnyFailedDimension(t *testing.T) {
	body := "第二章\n\n林澈借来皮卡，和沈知遥把五块价牌立在桥头。"
	hash := reviewreport.BodySHA256(body)
	entry := domain.ReviewEntry{
		Chapter: 2, BodySHA256: hash, ContractStatus: "met", Verdict: "rewrite",
		Issues: []domain.ConsistencyIssue{
			{Type: "aesthetic", Severity: "warning", Description: "可删一处不是而是句式"},
			{Type: "aesthetic", Severity: "warning", Description: "章末共同动作可再收声"},
		},
		Dimensions: defaultReviewDimensions(),
	}
	entry.Dimensions[7] = domain.DimensionScore{Dimension: "ai_voice_detection", Score: 50, Verdict: "fail", Comment: "命中 warning 级章节功能重复"}
	cap := 2.0
	mechanical := &reviewreport.MechanicalGatePayload{
		Chapter: 2, BodySHA256: hash,
		AIGCReport: aigc.Report{
			AIGCPercent: 9.18, HumanAnchorFinalCap: &cap,
			Stats: aigc.Stats{Hanzi: 2400, HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene", "score": float64(88), "blockers": []string{},
			}},
		},
		RuleViolations: []rules.Violation{{Rule: "object_response_overuse", Severity: rules.SeverityWarning}},
	}
	analysis := domain.AIVoiceAnalysis{
		Chapter: 2, BodySHA256: hash, Label: "需打磨", Summary: "命中 1 项 warning 级红旗。",
		Metrics:  domain.ChapterAIVoiceMetrics{Chapter: 2, ParagraphCount: 40, SentenceCount: 80},
		RedFlags: []domain.AIVoiceRedFlag{{Rule: "chapter_function_repetition", Severity: "warning"}},
	}
	judge := &deepseekAIJudgeArtifact{
		Chapter: 2, BodySHA256: hash, Verdict: "human_like", RiskLevel: "low",
		AIProbabilityPercent: 2, AdviceComplete: true,
	}
	if reconcileWarningOnlyEditorReview(&entry, editorCacheTestMarkdown, hash, mechanical, analysis, judge) {
		t.Fatal("a failed Editor dimension must remain blocking even when the external reviewer passes")
	}
	if entry.Verdict != "rewrite" || entry.Dimensions[7].Score != 50 || entry.Dimensions[7].Verdict != "fail" {
		t.Fatalf("failed dimension was mutated: %+v", entry)
	}
}

func TestEditorReviewCacheHitSkipsModelCallAndUsesDedicatedArtifact(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把手机翻过来。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	model := &reviewCacheModel{response: editorCacheTestMarkdown}

	first := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if first.Err != nil || first.CacheHit || first.CacheArtifact == nil {
		t.Fatalf("first editor branch = %+v", first)
	}
	if err := saveEditorReviewCache(dir, first.CacheArtifact); err != nil {
		t.Fatalf("saveEditorReviewCache: %v", err)
	}

	// Generated timestamps are persistence metadata, not review context. A fresh
	// deterministic analysis for the same body must still hit the same cache.
	secondAnalysis := editorCacheTestAnalysis(body, "2026-07-11T10:05:00+08:00")
	second := loadOrGenerateEditorReview(
		dir, model, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, secondAnalysis, time.Second,
	)
	if second.Err != nil || !second.CacheHit {
		t.Fatalf("second editor branch = %+v", second)
	}
	if model.callCount() != 1 {
		t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
	}
	cachePath := reviewExistingCachePath(dir, editorReviewCacheBranch, first.CacheArtifact.CacheKey)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("dedicated Editor cache artifact missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviews", "01.json")); !os.IsNotExist(err) {
		t.Fatalf("Editor cache must not reuse or create final ReviewEntry, stat err=%v", err)
	}
}

func TestEditorReviewCacheMissesWhenSameBodyAIVoiceRulesAreRefreshed(t *testing.T) {
	dir := t.TempDir()
	body := "第一章 他等的从来不是外卖\n\n零点零三分，林澈把伸向门把的手收了回来。"
	flagged := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	flagged.Label = "需返工"
	flagged.Summary = "规范章标题被旧规则误判为开篇格言。"
	flagged.RedFlags = []domain.AIVoiceRedFlag{{
		Rule: "opening_single_sentence_aphorism", Severity: "error",
		Evidence: "第一章 他等的从来不是外卖",
	}}
	seedModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	seed := loadOrGenerateEditorReview(
		dir, seedModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, flagged, time.Second,
	)
	if seed.Err != nil || seed.CacheHit || seed.CacheArtifact == nil {
		t.Fatalf("seed editor branch = %+v", seed)
	}
	if err := saveEditorReviewCache(dir, seed.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	// The chapter bytes are intentionally unchanged. A corrected deterministic
	// analysis is a different model request and must not reuse the old verdict.
	clean := editorCacheTestAnalysis(body, "2026-07-11T10:05:00+08:00")
	refreshModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	refreshed := loadOrGenerateEditorReview(
		dir, refreshModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, clean, time.Second,
	)
	if refreshed.Err != nil || refreshed.CacheHit || refreshed.CacheArtifact == nil {
		t.Fatalf("refreshed editor branch = %+v", refreshed)
	}
	if refreshModel.callCount() != 1 {
		t.Fatalf("Editor Generate calls after rule refresh = %d, want 1", refreshModel.callCount())
	}
	if refreshed.CacheArtifact.CacheKey == seed.CacheArtifact.CacheKey ||
		refreshed.CacheArtifact.CachePolicy.AIVoiceContextSHA256 == seed.CacheArtifact.CachePolicy.AIVoiceContextSHA256 {
		t.Fatal("corrected exact-body AI-voice context reused stale Editor cache identity")
	}
}

func TestEditorReviewCacheDriftCausesModelMiss(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈停在门口。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	baseModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	base := loadOrGenerateEditorReview(
		dir, baseModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if base.Err != nil || base.CacheArtifact == nil {
		t.Fatalf("base editor branch = %+v", base)
	}
	if err := saveEditorReviewCache(dir, base.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		provider       string
		model          string
		premise        string
		userRules      string
		chapterContext string
		body           string
	}{
		{name: "body", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body + "\n新一句。"},
		{name: "provider", provider: "anthropic", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "model", provider: "openai", model: "editor-v2", premise: "premise", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "premise", provider: "openai", model: "editor-v1", premise: "premise drift", userRules: "rules", chapterContext: "chapter-context", body: body},
		{name: "user rules", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules drift", chapterContext: "chapter-context", body: body},
		{name: "chapter context", provider: "openai", model: "editor-v1", premise: "premise", userRules: "rules", chapterContext: "chapter-context drift", body: body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &reviewCacheModel{response: editorCacheTestMarkdown}
			caseAnalysis := editorCacheTestAnalysis(tt.body, "2026-07-11T10:10:00+08:00")
			result := loadOrGenerateEditorReview(
				dir, model, tt.provider, tt.model, tt.premise, tt.userRules, tt.chapterContext,
				1, tt.body, caseAnalysis, time.Second,
			)
			if result.Err != nil || result.CacheHit {
				t.Fatalf("drifted editor branch = %+v", result)
			}
			if model.callCount() != 1 {
				t.Fatalf("Editor Generate calls = %d, want 1", model.callCount())
			}
		})
	}

	driftedPolicy := base.CacheArtifact.CachePolicy
	driftedPolicy.ReviewProtocolVersion += "-drift"
	if reviewExistingCacheKey(driftedPolicy) == base.CacheArtifact.CacheKey {
		t.Fatal("review protocol drift must change the cache key")
	}
	if cached, err := loadEditorReviewCache(dir, driftedPolicy); err != nil || cached != nil {
		t.Fatalf("protocol-drifted cache load = artifact:%+v err:%v, want miss", cached, err)
	}
}

func TestReviewBranchCachesAreIndependent(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把门推开。"
	analysis := editorCacheTestAnalysis(body, "2026-07-11T10:00:00+08:00")
	seedModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	seed := loadOrGenerateEditorReview(
		dir, seedModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
		1, body, analysis, time.Second,
	)
	if seed.Err != nil || seed.CacheArtifact == nil {
		t.Fatalf("seed editor branch = %+v", seed)
	}
	if err := saveEditorReviewCache(dir, seed.CacheArtifact); err != nil {
		t.Fatal(err)
	}

	editorModel := &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel := &reviewCacheModel{response: deepseekCompleteHumanResponse}
	editorResult, deepseekResult := runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || !editorResult.CacheHit || editorModel.callCount() != 0 {
		t.Fatalf("Editor independent hit failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || deepseekResult.CacheHit || deepseekModel.callCount() != 1 {
		t.Fatalf("DeepSeek independent miss failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
	if err := saveDeepSeekAIJudgeCache(dir, deepseekResult.Artifact); err != nil {
		t.Fatal(err)
	}

	editorModel = &reviewCacheModel{response: editorCacheTestMarkdown}
	deepseekModel = &reviewCacheModel{response: deepseekCompleteHumanResponse}
	editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
		func() editorReviewBranchResult {
			return loadOrGenerateEditorReview(
				dir, editorModel, "openai", "editor-v1", "premise", "rules", "changed-chapter-context",
				1, body, analysis, time.Second,
			)
		},
		func() deepseekAIJudgeBranchResult {
			return loadOrGenerateDeepSeekAIJudge(
				dir, deepseekModel,
				deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true},
				1, body, time.Second,
			)
		},
	)
	if editorResult.Err != nil || editorResult.CacheHit || editorModel.callCount() != 1 {
		t.Fatalf("Editor independent miss failed: result=%+v calls=%d", editorResult, editorModel.callCount())
	}
	if deepseekResult.Err != nil || !deepseekResult.CacheHit || deepseekModel.callCount() != 0 {
		t.Fatalf("DeepSeek independent hit failed: result=%+v calls=%d", deepseekResult, deepseekModel.callCount())
	}
}

func TestRunReviewExistingBranchesConcurrentlyStartsBoth(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var editorResult editorReviewBranchResult
	var deepseekResult deepseekAIJudgeBranchResult
	go func() {
		editorResult, deepseekResult = runReviewExistingBranchesConcurrently(
			func() editorReviewBranchResult {
				started <- "editor"
				<-release
				return editorReviewBranchResult{Review: "editor-done"}
			},
			func() deepseekAIJudgeBranchResult {
				started <- "deepseek"
				<-release
				return deepseekAIJudgeBranchResult{Artifact: &deepseekAIJudgeArtifact{Summary: "deepseek-done"}}
			},
		)
		close(done)
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case branch := <-started:
			seen[branch] = true
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("both review branches did not start before release")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parallel review helper did not return")
	}
	if editorResult.Review != "editor-done" || deepseekResult.Artifact == nil || deepseekResult.Artifact.Summary != "deepseek-done" {
		t.Fatalf("unexpected branch results: editor=%+v deepseek=%+v", editorResult, deepseekResult)
	}
}

func editorCacheTestAnalysis(body, generatedAt string) domain.AIVoiceAnalysis {
	return domain.AIVoiceAnalysis{
		Chapter:     1,
		BodySHA256:  reviewreport.BodySHA256(body),
		Label:       "可通过",
		Summary:     "规则分析稳定",
		GeneratedAt: generatedAt,
		Metrics: domain.ChapterAIVoiceMetrics{
			Chapter:     1,
			GeneratedAt: generatedAt,
			AIVoiceScoreHistory: []domain.AIVoiceScorePoint{{
				Round: 1, Source: "rules", Score: 0.1, At: generatedAt,
			}},
		},
	}
}
