package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type deepseekJudgeCaptureModel struct {
	messages []agentcore.Message
	opts     []agentcore.CallOption
}

func (m *deepseekJudgeCaptureModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = append([]agentcore.Message(nil), messages...)
	m.opts = append([]agentcore.CallOption(nil), opts...)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{
			"verdict":"ai_like",
			"risk_level":"high",
			"ai_probability_percent":70,
			"confidence":"high",
			"summary":"对白和段落过于规整。",
			"reasons":["连续对话像流程节点"],
			"revision_plan":["打散连续说明段"],
			"dialogue_fix_plan":["省略可辨识说话标签"],
			"author_voice_plan":["保留程序员判断但落到现场证据"],
			"rag_rules":["后续避免每行都写说问答"]
		}`)},
	}}, nil
}

func (m *deepseekJudgeCaptureModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *deepseekJudgeCaptureModel) SupportsTools() bool { return false }

func TestRunDeepSeekAIJudgeUsesRawChapterBodyAndMaxThinking(t *testing.T) {
	model := &deepseekJudgeCaptureModel{}
	chapter := "第一章 样本M17\n\n许闻溪把审计盒接上。\n\n“先别解释。”"

	artifact, err := runDeepSeekAIJudge(model, deepseekAIJudgeModelSelection{
		Provider: "deepseek",
		Model:    "deepseek-v4-pro",
		Explicit: true,
	}, 1, chapter, time.Second)
	if err != nil {
		t.Fatalf("runDeepSeekAIJudge: %v", err)
	}
	if artifact == nil || !artifact.RawBodyOnly || artifact.BodySHA256 == "" {
		t.Fatalf("artifact raw-body metadata missing: %+v", artifact)
	}
	if artifact.CacheKey == "" || artifact.CachePolicy.ReviewProtocolVersion != deepseekAIJudgeReviewProtocolVersion {
		t.Fatalf("artifact cache identity missing: %+v", artifact)
	}
	if artifact.CachePolicy.SystemPromptSHA256 != reviewExistingSHA256(deepseekAIJudgeSystemPrompt) {
		t.Fatalf("artifact system prompt fingerprint = %q", artifact.CachePolicy.SystemPromptSHA256)
	}
	if len(model.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(model.messages))
	}
	if got := model.messages[1].TextContent(); got != chapter {
		t.Fatalf("user message must be raw chapter body only\ngot: %q\nwant:%q", got, chapter)
	}
	if strings.Contains(model.messages[1].TextContent(), "判断是否为ai") {
		t.Fatalf("user message leaked task description: %q", model.messages[1].TextContent())
	}
	cfg := agentcore.ResolveCallConfig(model.opts)
	if cfg.ThinkingLevel != agentcore.ThinkingMax {
		t.Fatalf("thinking level = %q, want max", cfg.ThinkingLevel)
	}
	if !artifact.Blocking || artifact.Verdict != "ai_like" || artifact.AIProbabilityPercent != 70 {
		t.Fatalf("artifact verdict/blocking = %+v", artifact)
	}
}

func TestDeepSeekAIJudgePromptDoesNotImposeForeignAuthorProfile(t *testing.T) {
	for _, forbidden := range []string{"30 岁左右", "有文学素养的程序员", "目标作者画像"} {
		if strings.Contains(deepseekAIJudgeSystemPrompt, forbidden) {
			t.Fatalf("DeepSeek judge prompt still imposes %q", forbidden)
		}
	}
	for _, want := range []string{
		"不预设作者的年龄、性别、职业",
		"不能移植其他项目身份",
		"明确数字任务",
		"不能仅凭其存在判为 AI",
		"不是语言模型证据",
		"禁止把它们单独列入 reasons",
		"类型爽文允许清晰因果",
		"不得用“模块化”覆盖这些人工特征",
	} {
		if !strings.Contains(deepseekAIJudgeSystemPrompt, want) {
			t.Fatalf("DeepSeek judge prompt missing %q", want)
		}
	}
}

func TestDeepSeekAIJudgeCacheHitSkipsModelCall(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把账本合上。"
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	model := &reviewCacheModel{response: `{"verdict":"human_like","risk_level":"low","ai_probability_percent":5,"confidence":"high","summary":"自然"}`}

	first := loadOrGenerateDeepSeekAIJudge(dir, model, selection, 1, body, time.Second)
	if first.Err != nil || first.CacheHit || first.Artifact == nil {
		t.Fatalf("first DeepSeek branch = %+v", first)
	}
	if err := saveDeepSeekAIJudgeCache(dir, first.Artifact); err != nil {
		t.Fatalf("saveDeepSeekAIJudgeCache: %v", err)
	}
	second := loadOrGenerateDeepSeekAIJudge(dir, model, selection, 1, body, time.Second)
	if second.Err != nil || !second.CacheHit || second.Artifact == nil {
		t.Fatalf("second DeepSeek branch = %+v", second)
	}
	if model.callCount() != 1 {
		t.Fatalf("DeepSeek Generate calls = %d, want 1", model.callCount())
	}
	if second.Artifact.CacheKey != first.Artifact.CacheKey {
		t.Fatalf("cache key changed on hit: first=%s second=%s", first.Artifact.CacheKey, second.Artifact.CacheKey)
	}
	if err := saveDeepSeekAIJudge(dir, second.Artifact); err != nil {
		t.Fatalf("saveDeepSeekAIJudge: %v", err)
	}
	var saved deepseekAIJudgeArtifact
	raw, err := os.ReadFile(filepath.Join(dir, "reviews", "01_deepseek_ai_judge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.CacheKey == "" || saved.CachePolicy != first.Artifact.CachePolicy {
		t.Fatalf("saved DeepSeek artifact cache identity missing: %+v", saved)
	}
}

func TestDeepSeekAIJudgeCacheDriftCausesModelMiss(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈推开窗。"
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	response := `{"verdict":"human_like","risk_level":"low","ai_probability_percent":5,"confidence":"high","summary":"自然"}`
	seedModel := &reviewCacheModel{response: response}
	seed := loadOrGenerateDeepSeekAIJudge(dir, seedModel, selection, 1, body, time.Second)
	if seed.Err != nil || seed.Artifact == nil {
		t.Fatalf("seed DeepSeek branch = %+v", seed)
	}
	if err := saveDeepSeekAIJudgeCache(dir, seed.Artifact); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		selection deepseekAIJudgeModelSelection
		body      string
	}{
		{name: "body", selection: selection, body: body + "\n风进来了。"},
		{name: "provider", selection: deepseekAIJudgeModelSelection{Provider: "openrouter", Model: "deepseek-v4-pro", Explicit: true}, body: body},
		{name: "model", selection: deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v5", Explicit: true}, body: body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &reviewCacheModel{response: response}
			result := loadOrGenerateDeepSeekAIJudge(dir, model, tt.selection, 1, tt.body, time.Second)
			if result.Err != nil || result.CacheHit {
				t.Fatalf("drifted DeepSeek branch = %+v", result)
			}
			if model.callCount() != 1 {
				t.Fatalf("DeepSeek Generate calls = %d, want 1", model.callCount())
			}
		})
	}

	driftedPolicy := seed.Artifact.CachePolicy
	driftedPolicy.ReviewProtocolVersion += "-drift"
	if reviewExistingCacheKey(driftedPolicy) == seed.Artifact.CacheKey {
		t.Fatal("DeepSeek review protocol drift must change the cache key")
	}
	if cached, err := loadDeepSeekAIJudgeCache(dir, driftedPolicy); err != nil || cached != nil {
		t.Fatalf("protocol-drifted DeepSeek cache load = artifact:%+v err:%v, want miss", cached, err)
	}
}

func TestDeepSeekAIJudgeCacheRejectsTamperedArtifactAndRegenerates(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈停了两秒。"
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	response := `{"verdict":"human_like","risk_level":"low","ai_probability_percent":5,"confidence":"high","summary":"自然"}`
	seedModel := &reviewCacheModel{response: response}
	seed := loadOrGenerateDeepSeekAIJudge(dir, seedModel, selection, 1, body, time.Second)
	if seed.Err != nil || seed.Artifact == nil {
		t.Fatalf("seed DeepSeek branch = %+v", seed)
	}
	if err := saveDeepSeekAIJudgeCache(dir, seed.Artifact); err != nil {
		t.Fatal(err)
	}

	tampered := *seed.Artifact
	tampered.BodySHA256 = reviewreport.BodySHA256("other body")
	path := reviewExistingCachePath(dir, deepseekAIJudgeCacheBranch, seed.Artifact.CacheKey)
	if err := writeReviewExistingCacheJSON(path, &tampered); err != nil {
		t.Fatal(err)
	}
	if cached, err := loadDeepSeekAIJudgeCache(dir, seed.Artifact.CachePolicy); err == nil || cached != nil {
		t.Fatalf("tampered cache load = artifact:%+v err:%v, want validation error", cached, err)
	}

	recoveryModel := &reviewCacheModel{response: response}
	recovered := loadOrGenerateDeepSeekAIJudge(dir, recoveryModel, selection, 1, body, time.Second)
	if recovered.Err != nil || recovered.CacheHit || recovered.CacheLoadErr == nil {
		t.Fatalf("recovered DeepSeek branch = %+v", recovered)
	}
	if recoveryModel.callCount() != 1 {
		t.Fatalf("DeepSeek Generate calls after invalid cache = %d, want 1", recoveryModel.callCount())
	}
}

func TestSanitizeDeepSeekAIJudgeForProjectRemovesDeprecatedEngineAdvice(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Outline.SavePremise("《她的第二算法》女频女性职场成长文，主角许闻溪。"); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 1,
		AuthorVoicePlan: []string{
			"强化程序员视角，可用版本差异和日志窗口作隐喻。",
			"保留许闻溪的逻辑判断，但落到职场动作。",
		},
		RAGRules:    []string{"关键冲突里少用完整明喻。"},
		RawResponse: `{"author_voice_plan":["梁渡眼看日志滚动速度。"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)

	if len(artifact.AuthorVoicePlan) != 1 || !strings.Contains(artifact.AuthorVoicePlan[0], "许闻溪") {
		t.Fatalf("expected deprecated advice removed, got %+v", artifact.AuthorVoicePlan)
	}
	if len(artifact.ProjectGuardWarnings) != 1 {
		t.Fatalf("expected project guard warning, got %+v", artifact.ProjectGuardWarnings)
	}
	joinedRules := strings.Join(artifact.RAGRules, "\n")
	if !strings.Contains(joinedRules, "项目门禁") {
		t.Fatalf("expected safe project guard rag rule, got %+v", artifact.RAGRules)
	}
	if strings.Contains(joinedRules, "日志窗口") || strings.Contains(joinedRules, "版本差异") {
		t.Fatalf("sanitized rag rules must not keep deprecated terms: %+v", artifact.RAGRules)
	}
	if artifact.RawResponse != "" {
		t.Fatalf("raw_response should be dropped when it contains deprecated terms: %q", artifact.RawResponse)
	}
}

func TestSanitizeDeepSeekAIJudgeRespectsCompanionSystemRule(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Preferences: "系统会和林澈交流解闷、短促吐槽并始终支持林澈，不能写成纯任务机器人。"}); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 1,
		Reasons: []string{
			"部分系统拟人台词偏暖，建议强化系统冷硬感。",
			"两次任务结构略对称。",
		},
		DialogueFixPlan: []string{
			"系统不予回应，减少系统拟人化玩笑。",
			"人物问答之间增加一次环境打断。",
		},
		RawResponse: `{"dialogue_fix_plan":["系统不予回应，减少系统拟人化玩笑。"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)

	joined := strings.Join(append(append([]string{}, artifact.Reasons...), artifact.DialogueFixPlan...), "\n")
	if strings.Contains(joined, "系统不予回应") || strings.Contains(joined, "强化系统冷硬") {
		t.Fatalf("contradictory advice survived: %s", joined)
	}
	if !strings.Contains(joined, "任务结构") || !strings.Contains(joined, "环境打断") {
		t.Fatalf("aligned advice was removed: %s", joined)
	}
	if artifact.RawResponse != "" {
		t.Fatalf("contradictory raw response must not enter later inputs: %q", artifact.RawResponse)
	}
	if !strings.Contains(strings.Join(artifact.RAGRules, "\n"), "系统必须能短促接话") {
		t.Fatalf("missing corrective RAG rule: %+v", artifact.RAGRules)
	}
}

func TestSanitizeDeepSeekAIJudgeRespectsApprovedTrendLanguagePlan(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		CausalSimulation: domain.ChapterCausalSimulation{
			TrendLanguage: []domain.TrendLanguagePlan{{Item: "呱，", CharacterCarrier: "赵航"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 1,
		DialogueFixPlan: []string{
			"赵航的台词改为更口语的拖长音，如‘呱——拿别人日子当标准答案’。",
			"老丁报价前可以先看一眼材料。",
		},
		RawResponse: `{"dialogue_fix_plan":["把呱，改成呱——"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)

	joined := strings.Join(artifact.DialogueFixPlan, "\n")
	if strings.Contains(joined, "呱——") || strings.Contains(joined, "拖长音") {
		t.Fatalf("trend-language contradiction survived: %s", joined)
	}
	if !strings.Contains(joined, "老丁") {
		t.Fatalf("aligned dialogue advice was removed: %s", joined)
	}
	if artifact.RawResponse != "" {
		t.Fatalf("contradictory raw response must be removed: %q", artifact.RawResponse)
	}
	if !strings.Contains(strings.Join(artifact.RAGRules, "\n"), "必须保持逗号起手") {
		t.Fatalf("missing corrective trend RAG rule: %+v", artifact.RAGRules)
	}
}

func TestBuildRevisionPlanIncludesDeepSeekBlockingJudge(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reviews"), 0o755); err != nil {
		t.Fatal(err)
	}
	json := `{
		"chapter":1,
		"provider":"deepseek",
		"model":"deepseek-v4-pro",
		"reasoning_effort":"max",
		"raw_body_only":true,
		"verdict":"ai_like",
		"risk_level":"high",
		"ai_probability_percent":70,
		"blocking":true,
		"reasons":["对白像流程节点"],
		"revision_plan":["把说明藏进动作和误判"],
		"dialogue_fix_plan":["去掉不必要的说话标签"],
		"author_voice_plan":["保留程序员判断但别写成说明书"],
		"rag_rules":["连续双人对白可省标签"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "reviews", "01_deepseek_ai_judge.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := buildRevisionPlan(dir, 1, "第一章\n\n许闻溪把线接好。\n\n“先别解释。”", "")
	if !plan.HasRed {
		t.Fatalf("expected DeepSeek blocking judge to set red, got %+v", plan)
	}
	if !containsString(plan.Sources, "reviews/01_deepseek_ai_judge.json") {
		t.Fatalf("expected deepseek source, got %+v", plan.Sources)
	}
	if !strings.Contains(plan.Brief, "DeepSeek 裸正文 AI 判定") || !strings.Contains(plan.Brief, "去掉不必要的说话标签") {
		t.Fatalf("brief missing DeepSeek guidance:\n%s", plan.Brief)
	}
}

func TestBuildRevisionPlanKeepsLowRiskDeepSeekSuggestionsNonBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reviews"), 0o755); err != nil {
		t.Fatal(err)
	}
	json := `{
		"chapter":1,
		"provider":"deepseek",
		"model":"deepseek-v4-pro",
		"reasoning_effort":"max",
		"raw_body_only":true,
		"verdict":"human_like",
		"risk_level":"low",
		"ai_probability_percent":12,
		"blocking":false,
		"revision_plan":["可微调一处重复动作"],
		"dialogue_fix_plan":["对白已自然，无需系统修改"],
		"author_voice_plan":["保持程序员观察式声口"],
		"rag_rules":["专业名词通过现场动作呈现"]
	}`
	if err := os.WriteFile(filepath.Join(dir, "reviews", "01_deepseek_ai_judge.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := buildRevisionPlan(dir, 1, "", "")
	if plan.HasRed || plan.HasYellow {
		t.Fatalf("low-risk DeepSeek suggestions should not trigger rewrite flags: %+v", plan)
	}
	if !strings.Contains(plan.Brief, "DeepSeek 修改方案: 可微调一处重复动作") {
		t.Fatalf("brief should still preserve low-risk suggestions:\n%s", plan.Brief)
	}
}

func TestExtractJSONObjectFromFencedResponse(t *testing.T) {
	got := extractJSONObject("```json\n{\"verdict\":\"mixed\"}\n```")
	if got != `{"verdict":"mixed"}` {
		t.Fatalf("extractJSONObject = %q", got)
	}
}
