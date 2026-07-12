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

const deepseekCompleteHumanResponse = `{
	"verdict":"human_like",
	"risk_level":"low",
	"ai_probability_percent":3,
	"confidence":"high",
	"summary":"整体自然，仍给出可执行保养建议。",
	"reasons":["局部两句节奏接近"],
	"evidence":["饭桌段有一处连续短问答","结尾两句承担相近功能"],
	"revision_plan":["保留剧情事实，调整局部信息释放","让结尾落在人物选择后的现场余波"],
	"dialogue_fix_plan":["只让有迫切目标的人开口"],
	"author_voice_plan":["保留当前口语化限知声口"],
	"rag_rules":["多人在场不等于人人发言","情绪必须改变选择或关系判断"]
}`

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
				"evidence":["连续四段由不同人物各补一条信息","主角在任务兑现后立即进入下一流程"],
				"revision_plan":["删去不必当场说的信息","让主角的震惊改变下一步选择"],
				"dialogue_fix_plan":["省略可辨识说话标签"],
				"author_voice_plan":["保留程序员判断但落到现场证据"],
				"rag_rules":["后续避免每行都写说问答","一组对白只完成一个局面变化"]
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
		"不得为了制造所谓“真人毛边”新增争执、误会、反悔",
		"优先删除、合并、重排流程",
		"误解、抢话、走神和打断不是自然对白的必备项",
		"数值必须和结论、理由、证据同向",
		"10% 以上至少需要两条互相独立",
		"evidence 必须直接支撑 ai_probability_percent",
		"0-3% 是有效区间",
		"不得把 5% 当成默认最低档",
		"\u3010\u3011是中文系统文区分系统对白的常见排版",
		"不得仅因它没写成手机 UI",
	} {
		if !strings.Contains(deepseekAIJudgeSystemPrompt, want) {
			t.Fatalf("DeepSeek judge prompt missing %q", want)
		}
	}
	if strings.Contains(deepseekAIJudgeSystemPrompt, "才通过") || strings.Contains(deepseekAIJudgeSystemPrompt, "目标 <") {
		t.Fatal("DeepSeek judge must not see the caller's pass threshold")
	}
	if !strings.Contains(deepseekAIJudgeSystemPrompt, "工程门禁会在模型响应之外执行") {
		t.Fatal("prompt must require threshold-independent scoring")
	}
}

func TestDeepSeekJudgeRejectsHumanLikeLowRiskScoreNarrativeMismatch(t *testing.T) {
	artifact := deepseekAIJudgeArtifact{
		Verdict:              "human_like",
		RiskLevel:            "low",
		AIProbabilityPercent: 22,
		Summary:              "整体叙事接近真人写作。",
		Reasons: []string{
			"流程转场略显平顺，但未形成模板复述。",
			"群像配合度偏高；不过仍分出层次，未沦为同意模板。",
		},
		Evidence:        []string{"人工反证一", "人工反证二"},
		RevisionPlan:    []string{"方案一", "方案二"},
		DialogueFixPlan: []string{"对白方案"},
		AuthorVoicePlan: []string{"声口方案"},
		RAGRules:        []string{"规则一", "规则二"},
	}
	if got := deepseekJudgeAdviceWarning(artifact); !strings.Contains(got, "score/reasons inconsistent") {
		t.Fatalf("score narrative mismatch must trigger retry, got %q", got)
	}
	artifact.AIProbabilityPercent = 7
	if got := deepseekJudgeAdviceWarning(artifact); !strings.Contains(got, "score/reasons inconsistent") {
		t.Fatalf("single-digit score with only weak reasons should retry, got %q", got)
	}
	artifact.Reasons = []string{"连续十二段均为同长度的解释型对白，且发言者均无个人目标。"}
	artifact.Evidence = []string{
		"连续十二段均为十五字左右，全部由角色接力解释下一步。",
		"三个发言者连续复述同一条规则，删掉姓名后无法分辨声口。",
	}
	if got := deepseekJudgeAdviceWarning(artifact); got != "" {
		t.Fatalf("single-digit low-risk judgment with substantive evidence should remain valid, got %q", got)
	}
}

func TestDeepSeekJudgeStrictFourPercentBoundary(t *testing.T) {
	base := deepseekAIJudgeArtifact{
		Verdict: "human_like", RiskLevel: "low", AdviceComplete: true,
		Evidence: []string{"证据一", "证据二"}, RevisionPlan: []string{"方案一", "方案二"},
		DialogueFixPlan: []string{"对白方案"}, AuthorVoicePlan: []string{"声口方案"}, RAGRules: []string{"规则一", "规则二"},
	}
	base.AIProbabilityPercent = 4
	if !deepseekJudgeBlocking(base) {
		t.Fatal("4% must fail the strict <4% external gate")
	}
	base.AIProbabilityPercent = 3
	if deepseekJudgeBlocking(base) {
		t.Fatal("3% with complete advice should pass")
	}
}

func TestDeepSeekJudgeRejectsFivePercentFloorWithHumanEvidence(t *testing.T) {
	artifact := deepseekAIJudgeArtifact{
		Verdict:              "human_like",
		RiskLevel:            "low",
		AIProbabilityPercent: 5,
		Summary:              "整体写作质感接近真人，仅有微量可优化的规整表述。",
		Reasons: []string{
			"角色对话符合各自身份与语境，未出现模板化接话。",
			"场景细节具体且具有选择性，避免了均匀的流程记录。",
			"系统提示融入情境且带有人情味，但句式仍可更口语化。",
		},
		Evidence: []string{
			"人工特征：贺骁的打趣带有职业烙印。",
			"人工特征：梁广财的连续动作形成完整次要人物印象。",
			"微小疑点：一句系统提示稍显说教。",
		},
		RevisionPlan:    []string{"方案一", "方案二"},
		DialogueFixPlan: []string{"对白方案"},
		AuthorVoicePlan: []string{"声口方案"},
		RAGRules:        []string{"规则一", "规则二"},
	}
	if got := deepseekJudgeAdviceWarning(artifact); !strings.Contains(got, "score/reasons inconsistent") {
		t.Fatalf("human-like five-percent floor must retry, got %q", got)
	}
}

func TestDeepSeekJudgeRejectsHighProbabilityWithLowRisk(t *testing.T) {
	artifact := deepseekAIJudgeArtifact{
		Verdict:              "mixed",
		RiskLevel:            "low",
		AIProbabilityPercent: 25,
		Summary:              "存在中等痕迹。",
		Reasons: []string{
			"连续十二段为同长度的解释型对白。",
			"两个场景都用相同的对称总结句收束。",
		},
		Evidence: []string{
			"连续十二段长度均为十五字左右。",
			"两处结尾均使用不是A而是B的对称句。",
		},
		RevisionPlan:    []string{"方案一", "方案二"},
		DialogueFixPlan: []string{"对白方案"},
		AuthorVoicePlan: []string{"声口方案"},
		RAGRules:        []string{"规则一", "规则二"},
	}
	if got := deepseekJudgeAdviceWarning(artifact); !strings.Contains(got, "score/reasons inconsistent") {
		t.Fatalf("high probability with low risk must retry, got %q", got)
	}
	artifact.RiskLevel = "medium"
	if got := deepseekJudgeAdviceWarning(artifact); got != "" {
		t.Fatalf("medium-risk score with two substantive signals should remain valid, got %q", got)
	}
}

func TestDeepSeekCraftAdviceRejectsFakeFrictionAndDataOnlySystem(t *testing.T) {
	for _, advice := range []string{
		"让贺骁搬桌子前先被桌脚绊了一下，制造执行上的小反复。",
		"把亲戚群改口延迟到下一章开头。",
		"系统改为纯数据反馈，例如当前状态：付款53笔。",
		"让摊主暗中较劲，拍照者另有目的，保留功能冗余或延迟。",
		"余额改变用触感或心跳外化，让秘密更靠人物反应。",
		"让林澈先默想一句暗处摊棚的私人记忆。",
		"让沈知遥顺势理了理取餐口的筷子或纸袋，用视觉停顿替代话轮。",
		"展望宜用疑问或不确定的观察替代，保持犹豫。",
		"任何任务或交易过程，必须设置至少一个基于现实逻辑的意外阻碍。",
	} {
		if !deepSeekCraftAdviceRecreatesConveyor(advice) {
			t.Fatalf("harmful advice not rejected: %q", advice)
		}
	}
}

func TestRunDeepSeekAIJudgeRetriesAndBlocksIncompleteAdvice(t *testing.T) {
	model := &reviewCacheModel{response: `{"verdict":"human_like","risk_level":"low","ai_probability_percent":3,"summary":"自然"}`}
	artifact, err := runDeepSeekAIJudge(model, deepseekAIJudgeModelSelection{
		Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true,
	}, 1, "第一章\n\n林澈关上门。", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if model.callCount() != deepseekAIJudgeMaxAttempts {
		t.Fatalf("Generate calls=%d, want %d", model.callCount(), deepseekAIJudgeMaxAttempts)
	}
	if artifact.AdviceComplete || !artifact.Blocking || artifact.AdviceWarning == "" {
		t.Fatalf("incomplete advice must block after retry: %+v", artifact)
	}
}

func TestDeepSeekAIJudgeCacheHitSkipsModelCall(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n林澈把账本合上。"
	selection := deepseekAIJudgeModelSelection{Provider: "deepseek", Model: "deepseek-v4-pro", Explicit: true}
	model := &reviewCacheModel{response: deepseekCompleteHumanResponse}

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
	response := deepseekCompleteHumanResponse
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
	response := deepseekCompleteHumanResponse
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
			"系统以【】直接嵌入叙事，缺乏视角锚定。",
			"两次任务结构略对称。",
		},
		DialogueFixPlan: []string{
			"系统不予回应，减少系统拟人化玩笑。",
			"人物问答之间增加一次环境打断。",
		},
		RAGRules:    []string{"系统类信息必须绑定界面/载体，禁止以【】作为独立叙事段。"},
		RawResponse: `{"dialogue_fix_plan":["系统不予回应，减少系统拟人化玩笑。"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)

	joined := strings.Join(append(append([]string{}, artifact.Reasons...), artifact.DialogueFixPlan...), "\n")
	if strings.Contains(joined, "系统不予回应") || strings.Contains(joined, "强化系统冷硬") || strings.Contains(joined, "缺乏视角锚定") {
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
	if strings.Contains(strings.Join(artifact.RAGRules, "\n"), "必须绑定界面") {
		t.Fatalf("independent system-paragraph contradiction survived: %+v", artifact.RAGRules)
	}
}

func TestSanitizeDeepSeekAIJudgeFindsCompanionSystemInWorldRules(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{
		Category: "system",
		Rule:     "系统是林澈的稳定吐槽搭子和情绪支持者，会聊天、提醒风险并接话解闷。",
		Boundary: "不替林澈做决定。",
	}}); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 1,
		Reasons: []string{
			"系统以【】直接嵌入叙事，缺乏视角锚定。",
		},
		RAGRules: []string{
			"系统类信息必须绑定界面/载体，禁止以【】作为独立叙事段。",
		},
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)

	joined := strings.Join(append(append([]string{}, artifact.Reasons...), artifact.RAGRules...), "\n")
	if strings.Contains(joined, "缺乏视角锚定") || strings.Contains(joined, "必须绑定界面") {
		t.Fatalf("world-rules companion contract was ignored: %s", joined)
	}
	if !strings.Contains(joined, "系统必须能短促接话") {
		t.Fatalf("corrective companion rule missing: %s", joined)
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

func TestSanitizeDeepSeekAIJudgeRemovesAdviceThatRecreatesDialogueConveyor(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 1,
		Reasons: []string{
			"章节结构是典型的'问题-解决-新问题'闭环。",
			"角色均按最优路径行动，缺乏现实场景中常见的短暂迟滞或沟通偏差。",
			"沈知遥指挥时缺乏短暂迟疑或操作失误，指令落地零延迟。",
			"系统提示的行为-反馈-奖励与常见系统文结构高度一致。",
			"对白段落长度过稳。",
		},
		Evidence: []string{"贺骁解释后沈知遥立即催促，三人行动无缝衔接", "沈知遥、贺骁、摊主形成一人一把的接力链", "五个摊主逐一表态，像预设的关卡对话", "后面的决定快了许多，却没人为了同一个理由答应", "便宜不等于省事", "时间线索串成均匀推进节奏", "其余四处没再照一个样子摆，像进度汇报", "沈知遥下令后指令落地零延迟，缺少个体摩擦", "系统在正确行为后立即结算奖励，形成正确行为→即时反馈"},
		RevisionPlan: []string{
			"插入1-2处非功能性细节打破节奏。",
			"让他踢到石子，再让老丁找零钱制造毛边。",
			"插入一段对配角的过往回忆，并增加一个小意外。",
			"让摊主突然提出完全相反的要求，迫使林澈陷入两难。",
			"让林澈回忆起过往类似情境，再安排一个突发小意外制造短暂混乱。",
			"每完成一个小目标就引入微小的代价或关系的新裂痕。",
			"加入个人感受或记忆，例如回忆曾被客户当众否定。",
			"加入一段闪回，让主角表现出对系统的厌烦感。",
			"在结尾快速估算剩余材料和人力。",
			"插入与任务无关的意外事件，让摊主孩子突然哭闹找不见。",
			"让空箱在晚高峰被人意外踢翻，水果滚出。",
			"增加一次沟通误解，差点碰倒果箱，再让沈知遥介入解围。",
			"让摊主同意后又临时反悔，并承诺免费清洁作为额外让步。",
			"贺骁说你俩昨天才认识后，补主角对关系的个人情感体验。",
			"增写冷饮老板与林澈的具体对话，通过至少两个独立对话场景平行对比。",
			"让林澈摸到口袋里的介绍折页犹豫片刻，再指着油脚印问。",
			"冷饮车问题后增加一轮失败，再让摊主否决一次。",
			"把午饭与某位摊主的临时质疑嫁接，让林澈边吃边回应，增加主视角的选择压力。",
			"给林澈的观察加一笔，利用这个微小动作把群像织回画面，并把纸箱多推了半寸。",
			"让林澈先做一个无用尝试，卡住过道更严重，再由贺骁纠正。",
			"让沈知遥向林澈低声点出瓶颈，林澈做出两个手势，挥手划通道并指向贺骁，再快速估算客流密度。",
			"闪过前期拉人不易的回忆，让沈知遥握住他的手腕一秒。",
			"在沈知遥指挥后，插入一个摊主反应慢半拍或桌脚被卡住又调整的细节，打破集体响应的完美同步。",
			"将系统提示的出现时机推后，让林澈在忙碌中忽略提示，事后才在脑中回响。",
			"亲戚群再出现一条岔开话题或质疑的语音，增加群聊的真实杂音。",
		},
		DialogueFixPlan: []string{
			"给每个次要角色留一个动作或短句。",
			"补一句早饭还没吃，再让二人简短拌嘴。",
			"增加金钱分歧上真实的摩擦。",
			"让团队聊一次旧事梗和合伙搞砸的经历。",
			"先说半句带刺的话，再让她自觉失言。",
			"加入极短暂的停顿动作，例如指节轻叩或视线扫过票据袋。",
			"让贺骁意外出声，林澈把手机屏幕转向自己，再补沈知遥未完全相信的眼神。",
			"对白掺入抢白和迟疑，让女主说聋了？收，制造短暂困惑。",
			"给马玉芬一句半截子话，并在擦手的动作中犹豫半秒。",
			"增加一个极短的沉默，让他把票据袋角捏皱又抚平，用物品接触和动作外化让回答自然延迟半秒。",
			"让贺骁自己追加一句，把三人关系短暂定调。",
			"先用半开玩笑的话拖延一拍，再说‘怎么，怕我坑你？’。",
			"插入赵启明来回翻册子的动作，让林澈先看一眼沈知遥，再缓缓开口。",
			"‘先别谢’前被旁边顾客擦身时让半步，再继续说话。",
		},
		AuthorVoicePlan: []string{"补掌心微汗和胸口松快等身体反应。", "系统弹出体力值浮动和关系进展提示。", "增加半句掐断的吐槽，维持话不说完的节奏。", "把价牌写成像一排突然睁开的眼睛之类通感。", "强化市井评书口吻：四十七笔顺当，偏在四十九笔杀出筷子，别把账本活成账簿。"},
		RAGRules: []string{
			"每个配角都要说一句。",
			"每两轮插入环境声音。",
			"配角必须聊与主线无直接关联的天气或身体状态。",
			"每三个行动段插入纯观察或回忆片段。",
			"每段对话须检视并植入一层潜台词或无关任务的私人目的。",
			"每段至少插入一句感官细节，并加入至少一个非语言反应或无意识摆弄。",
			"至少有一个摊主或路人不完成情节功能；信息对白必须伴随干扰，并插入听者提问偏题。",
			"多人对话至少出现一次无对话的沉默、动作打断或话题偏移，通过具体行为、回忆或对话呈现。",
			"每3000字加入无目的闲聊，每500字加入误判或错觉，例如听错报价。",
			"每千字加入一次感性评判，至少安排一次因误解产生的中断。",
			"结尾拒绝直接收束，加入某个摊主的不满或系统的延迟提示。",
			"群像差异必须通过至少两个独立对话场景的平行对比让读者发现。",
			"需要反复调试的障碍，或引发一个需马上处理的新麻烦，例如工具滑脱、另一处部件连带松动。",
			"取2-3个有摩擦、有对话冲突的个体详写。",
			"人物职业细节必须在下一次出场时延续或变更状态。",
			"群体接受方案时必须保证至少一家给出条件，并让该条件参与后续场面。",
			"至少在两个时间过渡处使用人物的感官触觉或情绪变化作为切换信号。",
			"务必保留至少一个'未解决'的余音，打破提出-响应-完毕，增加真实生活的粘滞感。",
			"每次高效协作后让旁人嘀咕，点出其幸运/巧合成分。",
			"杜绝让角色以对白宣布‘下一步’，将计划内化为个人欲望。",
		},
		RawResponse: `{"revision_plan":["插入非功能性细节"],"dialogue_fix_plan":["每个次要角色留一句"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)
	joined := strings.Join(append(append(append([]string{}, artifact.RevisionPlan...), artifact.DialogueFixPlan...), artifact.AuthorVoicePlan...), "\n")
	for _, forbidden := range []string{"非功能性细节", "每个次要角色", "掌心微汗", "胸口松快", "踢到石子", "找零钱", "过往回忆", "个人感受或记忆", "回忆曾", "闪回", "厌烦感", "小意外", "突发小意外", "与任务无关的意外事件", "孩子突然哭闹", "意外踢翻", "水果滚出", "短暂混乱", "旧事梗", "合伙搞砸", "带刺的话", "自觉失言", "极短暂的停顿动作", "指节轻叩", "视线扫过票据袋", "快速估算剩余材料", "贺骁的意外出声", "手机屏幕转向自己", "未完全相信的眼神", "增加一次沟通误解", "介入解围", "临时反悔", "承诺免费清洁", "抢白和迟疑", "聋了？收", "体力值浮动", "关系进展提示", "结尾拒绝直接收束", "某个摊主的不满", "系统的延迟提示", "增写冷饮老板与林澈的具体对话", "至少两个独立对话场景", "平行对比", "摸到口袋里的介绍折页犹豫片刻", "指着油脚印问", "半截子话", "擦手的动作中犹豫半秒", "早饭还没吃", "简短拌嘴", "与主线无直接关联", "每三个行动段", "每段至少插入", "至少一个非语言反应", "无意识摆弄", "至少有一个摊主或路人", "信息对白必须伴随干扰", "听者提问偏题", "无对话的沉默", "通过具体行为、回忆或对话", "每3000字", "每500字", "每千字", "无目的闲聊", "误判或错觉", "至少安排一次因误解", "完全相反的要求", "陷入两难", "金钱分歧", "每段对白须检视", "植入一层潜台词", "每完成一个小目标就引入", "微小的代价", "关系的新裂痕", "增加一轮失败", "反复调试", "马上处理的新麻烦", "工具滑脱", "部件连带松动", "极短的沉默", "票据袋角捏皱", "动作外化", "自然延迟半秒", "有对话冲突的个体", "取2-3个有摩擦", "临时质疑嫁接", "边吃边回应", "观察加一笔", "微小动作", "多推了半寸", "自己追加一句", "半句掐断的吐槽", "话不说完的节奏", "下一次出场时延续或变更状态", "必须保证至少一家", "让该条件参与后续场面", "无用尝试", "卡住过道更严重", "由贺骁纠正", "半开玩笑的话拖延一拍", "怎么，怕我坑你", "像一排突然睁开的眼睛", "通感", "至少在两个时间过渡", "感官触觉或情绪变化作为切换信号", "务必保留至少一个", "提出-响应-完毕", "真实生活的粘滞感", "向林澈低声点出瓶颈", "做出两个手势", "挥手划通道", "快速估算客流密度", "拉人不易的回忆", "握住他的手腕一秒", "来回翻册子", "先看一眼沈知遥", "再缓缓开口", "市井评书", "四十七笔顺当", "偏在四十九笔", "别把账本活成账簿", "幸运/巧合成分", "旁人的嘀咕", "杜绝让角色以对白宣布", "将计划内化为个人欲望", "反应慢半拍", "桌脚被卡住又调整", "打破集体响应", "出现时机推后", "忙碌中忽略提示", "事后才在脑中回响", "岔开话题", "质疑的语音", "群聊的真实杂音", "被旁边顾客擦身", "让半步"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("harmful craft advice survived %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "删掉没有迫切目标的发言者") || !strings.Contains(joined, "改变判断或选择") {
		t.Fatalf("safe replacements missing: %s", joined)
	}
	for _, want := range []string{"多人开场删去一位角色", "开篇疏导只保留一个主导发言者", "多对象授权只完整写一个", "朋友点破男女主默契后", "删掉概括群像差异", "删掉‘便宜不等于省事’", "删除中间连续报时", "删掉‘A如何、B如何、C如何’式安装成果汇总"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("evidence-specific safe replacement missing %q: %s", want, joined)
		}
	}
	ragJoined := strings.Join(artifact.RAGRules, "\n")
	if !strings.Contains(joined, "只完整渲染一到两个") || !strings.Contains(joined, "只跟住一组顾客") || !strings.Contains(ragJoined, "同类任务压缩规则") || !strings.Contains(ragJoined, "系统接话规则") || !strings.Contains(ragJoined, "代表操作压缩规则") || !strings.Contains(ragJoined, "心理去金句规则") || !strings.Contains(ragJoined, "成果非排比规则") || !strings.Contains(ragJoined, "配角关系功能规则") {
		t.Fatalf("representative-scene compression rule missing: %+v", artifact)
	}
	if !artifact.AdviceComplete || artifact.RawResponse != "" || len(artifact.ProjectGuardWarnings) == 0 {
		t.Fatalf("sanitized artifact contract invalid: %+v", artifact)
	}
	invalidJoined := strings.Join(append(append([]string{}, artifact.Reasons...), artifact.Evidence...), "\n")
	for _, forbidden := range []string{"问题-解决-新问题", "指令落地零延迟", "缺少个体摩擦", "行为-反馈-奖励", "正确行为→即时反馈"} {
		if strings.Contains(invalidJoined, forbidden) {
			t.Fatalf("forbidden genre/fake-friction evidence survived %q: %s", forbidden, invalidJoined)
		}
	}
	if strings.Contains(strings.Join(artifact.Reasons, "\n"), "问题-解决-新问题") {
		t.Fatalf("forbidden genre label survived as AI reason: %+v", artifact.Reasons)
	}
}

func TestSanitizeDeepSeekAIJudgeRejectsCrowdChatterAsHumanizer(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 2, AdviceComplete: true,
		Reasons:  []string{"沈知遥宣布规则时缺少真实现场的七嘴八舌。"},
		Evidence: []string{"五家摊主依次反应，缺少真实现场的七嘴八舌。"},
		RevisionPlan: []string{
			"插入一两个摊主的插话或摊主间的低声嘀咕。",
			"结尾增加有人抢先写名字、有人拽林澈袖子。",
		},
		DialogueFixPlan: []string{"将部分规则转为问答式，让摊主问‘拆要我们自己拆吗？’。"},
		RAGRules:        []string{"通过对话切分或现场打断来释放信息。"},
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)
	joined := strings.Join(append(append(append([]string{}, artifact.RevisionPlan...), artifact.DialogueFixPlan...), artifact.RAGRules...), "\n")
	for _, forbidden := range []string{"插入一两个摊主", "低声嘀咕", "抢先写名字", "拽林澈袖子", "转为问答式", "通过对话切分或现场打断"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("crowd-chatter humanizer survived %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "删掉没有迫切目标的发言者") || !strings.Contains(joined, "只完整渲染一到两个") {
		t.Fatalf("safe compression replacements missing: %s", joined)
	}
	if strings.Contains(strings.Join(append(artifact.Reasons, artifact.Evidence...), "\n"), "七嘴八舌") {
		t.Fatalf("invalid crowd-chatter reason survived: %+v", artifact)
	}
}

func TestSanitizeDeepSeekAIJudgeProtectsHighSugarLeadAlliance(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.UserRules.Save(&rules.Snapshot{Preferences: "感情线是单女主高糖事业搭档；男女主不冷战分手，关系铁律不可违背。"}); err != nil {
		t.Fatal(err)
	}
	artifact := &deepseekAIJudgeArtifact{
		Chapter: 2,
		RevisionPlan: []string{
			"让林澈与沈知遥产生短暂分歧，再由贺骁调停。",
			"让沈知遥对角度提出不同意见，导致短暂僵持，最后由贺骁打岔才和解。",
			"让沈知遥偷偷用手机查询林澈，再试探贺骁，埋下人物关系猜疑。",
			"把五家摊位的同型验收压缩成两处页面结果。",
		},
		DialogueFixPlan: []string{"二人发生争执后再相互理解。"},
		AuthorVoicePlan: []string{"保留林澈对县城人情的偏见。"},
		RAGRules: []string{
			"多角色交涉要有一次误判。",
			"现场后果必须改变下一步选择。",
		},
		RawResponse: `{"revision_plan":["二人产生短暂分歧"]}`,
	}

	sanitizeDeepSeekAIJudgeForProject(st, artifact)
	joined := strings.Join(append(append([]string{}, artifact.RevisionPlan...), artifact.DialogueFixPlan...), "\n")
	for _, forbidden := range []string{"产生短暂分歧", "发生争执", "贺骁调停", "提出不同意见", "短暂僵持", "才和解", "偷偷用手机查询林澈", "试探贺骁", "关系猜疑"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("lead-conflict advice survived %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "基于信任立即配合") || !strings.Contains(joined, "不制造关系分歧") {
		t.Fatalf("aligned replacement missing: %s", joined)
	}
	if artifact.RawResponse != "" || len(artifact.ProjectGuardWarnings) == 0 {
		t.Fatalf("raw conflict advice must be removed: %+v", artifact)
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
		"pass_exclusive_percent":4,
		"advice_complete":true,
		"blocking":true,
		"reasons":["对白像流程节点"],
		"evidence":["连续四段一人一句","主角只执行没有体验"],
		"revision_plan":["删掉不必发言的人","让主角感受改变选择"],
		"dialogue_fix_plan":["去掉不必要的说话标签"],
		"author_voice_plan":["保留程序员判断但别写成说明书"],
		"rag_rules":["连续双人对白可省标签","多人在场不等于人人说话"]
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
		"ai_probability_percent":3,
		"pass_exclusive_percent":4,
		"advice_complete":true,
		"blocking":false,
		"evidence":["一处动作连续复现","结尾两句功能接近"],
		"revision_plan":["可微调一处重复动作","保留当前情绪因果"],
		"dialogue_fix_plan":["对白已自然，无需系统修改"],
		"author_voice_plan":["保持程序员观察式声口"],
		"rag_rules":["专业名词通过现场动作呈现","低风险建议不得改变人物身份"]
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
