package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	toolspkg "github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const (
	deepseekAIJudgeReasoningEffort       = agentcore.ThinkingMax
	deepseekAIJudgeReviewProtocolVersion = "review-existing/deepseek-ai-judge/v3"
)

const deepseekAIJudgeSystemPrompt = `你是中文小说 AI 写作痕迹审核员。你会收到一整章小说正文；用户消息只包含正文，不包含说明、标题解释、检测要求或元数据。请把它当作约 3000 字整段检测场景，判断是否像 AI 写作，并给出可执行修改方案。

评审口径校准：
- 不预设作者的年龄、性别、职业、题材和平台，不得要求正文补入程序员、女性职场或其他未出现的作者画像。
- 只从本章已经呈现的类型承诺与人物声口判断自然度；author_voice_plan 只能强化现有声口，不能移植其他项目身份。
- 系统文的明确数字任务、任务卡和结算卡属于常见类型装置，不能仅凭其存在判为 AI；应判断它是否被人物反应、现场因果和个性化系统对白承接。
- “典型系统文框架”“任务链”“NPC 登场”“遭遇-解决-新问题”都是题材或情节标签，不是语言模型证据，禁止把它们单独列入 reasons 或据此提高 AI 概率。每条 AI 味理由都必须落到不依赖题材的正文语言证据，例如可复核的同型句法、机械复述、异常均匀段落、无人物目的的信息对白或连续即时响应。
- 类型爽文允许清晰因果、明确任务和及时兑现；不能因为故事推进顺畅、主角执行任务或配角承担情节功能就判 AI。若对白已有打断、讨价、误解、生活口气和人物边界，应按实际文本降风险，不得用“模块化”覆盖这些人工特征。
- 面向读者不一定有相关经验；专业信息要靠动作、界面、制度压力、误判和对话后果让读者读懂，不写说明书。

重点检查：
- 段落结构是否过于工整，句长/段长/对话节奏是否过平。
- 是否有连续“角色精准接话 -> 补口径/讲规则 -> 物件即时响应”的模板链。
- 对话是否像流程节点，不像带目标、隐瞒、误解、打断、地位差和潜台词的人。
- 是否存在“人：对话”式剧本口吻、过密说话标签、过度动作拍、人人说完整书面句。
- 专业名词是否被解释成教材，或反过来没有落到读者可见的现场证据。

只输出一个 JSON 对象，不要 Markdown，不要代码围栏，不要寒暄。字段：
{
  "verdict": "ai_like | mixed | human_like",
  "risk_level": "low | medium | high",
  "ai_probability_percent": 0,
  "confidence": "low | medium | high",
  "summary": "一句话判断",
  "reasons": ["按严重度列 AI 味原因"],
  "evidence": ["短证据，避免长段引用"],
  "revision_plan": ["按执行顺序列整章修改方案"],
  "dialogue_fix_plan": ["对白专项修改方案"],
  "author_voice_plan": ["只按本章已有声口补强叙述气质的方案"],
  "rag_rules": ["沉淀给后续章节避免复发的格式化规则"]
}`

type deepseekAIJudgeModelSelection struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Explicit bool   `json:"explicit"`
}

type deepseekAIJudgeModelOutput struct {
	Verdict              string   `json:"verdict"`
	RiskLevel            string   `json:"risk_level"`
	AIProbabilityPercent int      `json:"ai_probability_percent"`
	Confidence           string   `json:"confidence"`
	Summary              string   `json:"summary"`
	Reasons              []string `json:"reasons"`
	Evidence             []string `json:"evidence"`
	RevisionPlan         []string `json:"revision_plan"`
	DialogueFixPlan      []string `json:"dialogue_fix_plan"`
	AuthorVoicePlan      []string `json:"author_voice_plan"`
	RAGRules             []string `json:"rag_rules"`
}

type deepseekAIJudgeArtifact struct {
	Chapter              int                           `json:"chapter"`
	GeneratedAt          string                        `json:"generated_at"`
	CacheKey             string                        `json:"cache_key"`
	CachePolicy          reviewExistingCachePolicy     `json:"cache_policy"`
	Provider             string                        `json:"provider,omitempty"`
	Model                string                        `json:"model,omitempty"`
	ReviewerExplicit     bool                          `json:"reviewer_explicit"`
	ReasoningEffort      string                        `json:"reasoning_effort"`
	RawBodyOnly          bool                          `json:"raw_body_only"`
	UserPayloadKind      string                        `json:"user_payload_kind"`
	BodySHA256           string                        `json:"body_sha256"`
	Verdict              string                        `json:"verdict"`
	RiskLevel            string                        `json:"risk_level"`
	AIProbabilityPercent int                           `json:"ai_probability_percent"`
	Confidence           string                        `json:"confidence,omitempty"`
	Blocking             bool                          `json:"blocking"`
	Summary              string                        `json:"summary,omitempty"`
	Reasons              []string                      `json:"reasons,omitempty"`
	Evidence             []string                      `json:"evidence,omitempty"`
	RevisionPlan         []string                      `json:"revision_plan,omitempty"`
	DialogueFixPlan      []string                      `json:"dialogue_fix_plan,omitempty"`
	AuthorVoicePlan      []string                      `json:"author_voice_plan,omitempty"`
	RAGRules             []string                      `json:"rag_rules,omitempty"`
	ProjectGuardWarnings []string                      `json:"project_guard_warnings,omitempty"`
	ParseWarning         string                        `json:"parse_warning,omitempty"`
	RawResponse          string                        `json:"raw_response,omitempty"`
	ModelSelection       deepseekAIJudgeModelSelection `json:"model_selection"`
}

func newDeepSeekAIJudgeCachePolicy(selection deepseekAIJudgeModelSelection, chapter int, chapterBody string) reviewExistingCachePolicy {
	return reviewExistingCachePolicy{
		Branch:                deepseekAIJudgeCacheBranch,
		ReviewProtocolVersion: deepseekAIJudgeReviewProtocolVersion,
		Chapter:               chapter,
		BodySHA256:            reviewreport.BodySHA256(chapterBody),
		Provider:              selection.Provider,
		Model:                 selection.Model,
		SystemPromptSHA256:    reviewExistingSHA256(deepseekAIJudgeSystemPrompt),
		ReasoningEffort:       string(deepseekAIJudgeReasoningEffort),
		UserPayloadKind:       "chapter_body_only",
	}
}

func loadOrGenerateDeepSeekAIJudge(
	projectDir string,
	model agentcore.ChatModel,
	selection deepseekAIJudgeModelSelection,
	chapter int,
	chapterBody string,
	budget time.Duration,
) deepseekAIJudgeBranchResult {
	policy := newDeepSeekAIJudgeCachePolicy(selection, chapter, chapterBody)
	cached, loadErr := loadDeepSeekAIJudgeCache(projectDir, policy)
	if loadErr == nil && cached != nil {
		// Explicitness describes this invocation's role selection, not the cached
		// model response identity. Keep final artifact metadata current on a hit.
		cached.ReviewerExplicit = selection.Explicit
		cached.ModelSelection = selection
		return deepseekAIJudgeBranchResult{Artifact: cached, CacheHit: true}
	}
	artifact, err := runDeepSeekAIJudge(model, selection, chapter, chapterBody, budget)
	return deepseekAIJudgeBranchResult{
		Artifact:     artifact,
		CacheLoadErr: loadErr,
		Err:          err,
	}
}

func loadDeepSeekAIJudgeCache(projectDir string, expected reviewExistingCachePolicy) (*deepseekAIJudgeArtifact, error) {
	key := reviewExistingCacheKey(expected)
	path := reviewExistingCachePath(projectDir, deepseekAIJudgeCacheBranch, key)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 DeepSeek 缓存 %s: %w", path, err)
	}
	var artifact deepseekAIJudgeArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return nil, fmt.Errorf("解析 DeepSeek 缓存 %s: %w", path, err)
	}
	if err := validateDeepSeekAIJudgeCacheArtifact(&artifact, expected); err != nil {
		return nil, fmt.Errorf("校验 DeepSeek 缓存 %s: %w", path, err)
	}
	return &artifact, nil
}

func saveDeepSeekAIJudgeCache(projectDir string, artifact *deepseekAIJudgeArtifact) error {
	if artifact == nil {
		return fmt.Errorf("DeepSeek 缓存 artifact 为空")
	}
	if err := validateDeepSeekAIJudgeCacheArtifact(artifact, artifact.CachePolicy); err != nil {
		return err
	}
	path := reviewExistingCachePath(projectDir, deepseekAIJudgeCacheBranch, artifact.CacheKey)
	return writeReviewExistingCacheJSON(path, artifact)
}

func validateDeepSeekAIJudgeCacheArtifact(artifact *deepseekAIJudgeArtifact, expected reviewExistingCachePolicy) error {
	if err := validateDeepSeekAIJudgeArtifactIdentity(artifact, expected); err != nil {
		return err
	}
	if strings.TrimSpace(artifact.RawResponse) == "" {
		return fmt.Errorf("raw_response 为空，不能作为模型响应缓存")
	}
	return nil
}

func validateDeepSeekAIJudgeArtifactIdentity(artifact *deepseekAIJudgeArtifact, expected reviewExistingCachePolicy) error {
	if artifact == nil {
		return fmt.Errorf("artifact 为空")
	}
	if expected.Branch != deepseekAIJudgeCacheBranch {
		return fmt.Errorf("branch=%q, want %q", expected.Branch, deepseekAIJudgeCacheBranch)
	}
	if artifact.CachePolicy != expected {
		return fmt.Errorf("cache policy mismatch")
	}
	expectedKey := reviewExistingCacheKey(expected)
	if artifact.CacheKey != expectedKey {
		return fmt.Errorf("cache_key=%q, want %q", artifact.CacheKey, expectedKey)
	}
	if artifact.Chapter != expected.Chapter {
		return fmt.Errorf("chapter=%d, want %d", artifact.Chapter, expected.Chapter)
	}
	if artifact.BodySHA256 != expected.BodySHA256 {
		return fmt.Errorf("body_sha256=%q, want %q", artifact.BodySHA256, expected.BodySHA256)
	}
	if artifact.Provider != expected.Provider || artifact.Model != expected.Model {
		return fmt.Errorf("provider/model=%s/%s, want %s/%s", artifact.Provider, artifact.Model, expected.Provider, expected.Model)
	}
	if artifact.ModelSelection.Provider != expected.Provider || artifact.ModelSelection.Model != expected.Model {
		return fmt.Errorf("model_selection=%s/%s, want %s/%s", artifact.ModelSelection.Provider, artifact.ModelSelection.Model, expected.Provider, expected.Model)
	}
	if artifact.ReasoningEffort != expected.ReasoningEffort {
		return fmt.Errorf("reasoning_effort=%q, want %q", artifact.ReasoningEffort, expected.ReasoningEffort)
	}
	if !artifact.RawBodyOnly || artifact.UserPayloadKind != expected.UserPayloadKind {
		return fmt.Errorf("payload identity mismatch")
	}
	if strings.TrimSpace(artifact.GeneratedAt) == "" {
		return fmt.Errorf("generated_at 为空")
	}
	return nil
}

func runDeepSeekAIJudge(
	model agentcore.ChatModel,
	selection deepseekAIJudgeModelSelection,
	chapter int,
	chapterBody string,
	budget time.Duration,
) (*deepseekAIJudgeArtifact, error) {
	if strings.TrimSpace(chapterBody) == "" {
		return nil, fmt.Errorf("第 %d 章正文为空，无法做 DeepSeek 裸正文 AI 判定", chapter)
	}
	cachePolicy := newDeepSeekAIJudgeCachePolicy(selection, chapter, chapterBody)
	if budget <= 0 {
		budget = 180 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	resp, err := model.Generate(ctx,
		[]agentcore.Message{
			{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: deepseekAIJudgeSystemPrompt}}},
			{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: chapterBody}}},
		},
		nil,
		agentcore.WithThinking(deepseekAIJudgeReasoningEffort),
	)
	if err != nil {
		return nil, err
	}
	if resp == nil || strings.TrimSpace(resp.Message.TextContent()) == "" {
		return nil, fmt.Errorf("DeepSeek AI 判定返回空响应")
	}

	raw := strings.TrimSpace(resp.Message.TextContent())
	artifact := &deepseekAIJudgeArtifact{
		Chapter:          chapter,
		GeneratedAt:      time.Now().Format(time.RFC3339),
		CacheKey:         reviewExistingCacheKey(cachePolicy),
		CachePolicy:      cachePolicy,
		Provider:         selection.Provider,
		Model:            selection.Model,
		ReviewerExplicit: selection.Explicit,
		ReasoningEffort:  string(deepseekAIJudgeReasoningEffort),
		RawBodyOnly:      true,
		UserPayloadKind:  "chapter_body_only",
		BodySHA256:       reviewreport.BodySHA256(chapterBody),
		RawResponse:      raw,
		ModelSelection:   selection,
	}
	if !strings.EqualFold(selection.Provider, "deepseek") || selection.Model == "" || !strings.Contains(strings.ToLower(selection.Model), "deepseek") {
		artifact.ParseWarning = "reviewer role is not configured to a DeepSeek model"
	}

	jsonText := extractJSONObject(raw)
	if jsonText == "" {
		artifact.Verdict = "parse_failed"
		artifact.RiskLevel = "unknown"
		artifact.Blocking = true
		artifact.Summary = "DeepSeek 返回未能解析为 JSON；保留原始响应并按需人工复核。"
		if artifact.ParseWarning == "" {
			artifact.ParseWarning = "no JSON object found in model response"
		}
		return artifact, nil
	}
	var parsed deepseekAIJudgeModelOutput
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		artifact.Verdict = "parse_failed"
		artifact.RiskLevel = "unknown"
		artifact.Blocking = true
		artifact.Summary = "DeepSeek JSON 解析失败；保留原始响应并按需人工复核。"
		if artifact.ParseWarning == "" {
			artifact.ParseWarning = err.Error()
		}
		return artifact, nil
	}

	artifact.Verdict = normalizeDeepSeekVerdict(parsed.Verdict)
	artifact.RiskLevel = normalizeDeepSeekRisk(parsed.RiskLevel)
	artifact.AIProbabilityPercent = clampPercent(parsed.AIProbabilityPercent)
	artifact.Confidence = strings.TrimSpace(parsed.Confidence)
	artifact.Summary = strings.TrimSpace(parsed.Summary)
	artifact.Reasons = cleanStringList(parsed.Reasons, 8)
	artifact.Evidence = cleanStringList(parsed.Evidence, 8)
	artifact.RevisionPlan = cleanStringList(parsed.RevisionPlan, 12)
	artifact.DialogueFixPlan = cleanStringList(parsed.DialogueFixPlan, 10)
	artifact.AuthorVoicePlan = cleanStringList(parsed.AuthorVoicePlan, 8)
	artifact.RAGRules = cleanStringList(parsed.RAGRules, 12)
	artifact.Blocking = deepseekJudgeBlocking(*artifact)
	return artifact, nil
}

func sanitizeDeepSeekAIJudgeForProject(st *store.Store, artifact *deepseekAIJudgeArtifact) {
	if st == nil || artifact == nil {
		return
	}
	companionSystem := false
	if snapshot, err := st.UserRules.Load(); err == nil && snapshot != nil {
		companionSystem = domain.SystemCompanionVoiceRequested(snapshot.Preferences)
	}
	chapterPlan, _ := st.Drafts.LoadChapterPlan(artifact.Chapter)
	removedProject, removedUserRules, removedTrendRules := 0, 0, 0
	filter := func(values []string) []string {
		if len(values) == 0 {
			return values
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			if len(toolspkg.SecondAlgorithmProjectContaminationViolations(st, value)) > 0 {
				removedProject++
				continue
			}
			if companionSystem && domain.SystemCompanionFeedbackContradicts(value) {
				removedUserRules++
				continue
			}
			if deepSeekTrendAdviceContradictsPlan(chapterPlan, value) {
				removedTrendRules++
				continue
			}
			out = append(out, value)
		}
		return out
	}
	artifact.Reasons = filter(artifact.Reasons)
	artifact.Evidence = filter(artifact.Evidence)
	artifact.RevisionPlan = filter(artifact.RevisionPlan)
	artifact.DialogueFixPlan = filter(artifact.DialogueFixPlan)
	artifact.AuthorVoicePlan = filter(artifact.AuthorVoicePlan)
	artifact.RAGRules = filter(artifact.RAGRules)
	if companionSystem && domain.SystemCompanionFeedbackContradicts(artifact.Summary) {
		removedUserRules++
		artifact.Summary = "AI 痕迹结论保留；与用户系统人格硬设定冲突的风格建议已从后续写作输入中移除。"
	}
	if strings.TrimSpace(artifact.RawResponse) != "" {
		if len(toolspkg.SecondAlgorithmProjectContaminationViolations(st, artifact.RawResponse)) > 0 {
			removedProject++
			artifact.RawResponse = ""
		} else if companionSystem && domain.SystemCompanionFeedbackContradicts(artifact.RawResponse) {
			removedUserRules++
			artifact.RawResponse = ""
		} else if deepSeekTrendAdviceContradictsPlan(chapterPlan, artifact.RawResponse) {
			removedTrendRules++
			artifact.RawResponse = ""
		}
	}
	if removedProject > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条与本书禁用旧引擎冲突的建议。", removedProject),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"项目门禁：禁止把旧版硬核取证术语当作专业隐喻；用职场后果、岗位合并、项目权限、同事求助和会后约谈替代。",
		)
	}
	if removedUserRules > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条与本书系统人格硬设定冲突的建议。", removedUserRules),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"用户硬设定：系统必须能短促接话、吐槽、撑腰并始终支持林澈；可控制频率和长度，不得改成冷硬静默的任务机器人。",
		)
	}
	if removedTrendRules > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条与本章热梗句法或承载人 plan 冲突的建议。", removedTrendRules),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"本章热梗门禁：已批准的‘呱，’必须保持逗号起手并后接完整吐槽，承载人以 chapter plan 为准；不得改成‘呱——’、拟声动作或另换角色。",
		)
	}
}

func deepSeekTrendAdviceContradictsPlan(plan *domain.ChapterPlan, text string) bool {
	if plan == nil || !strings.Contains(text, "呱") {
		return false
	}
	approvedGua := false
	for _, item := range plan.CausalSimulation.TrendLanguage {
		value := strings.Trim(strings.TrimSpace(item.Item), "`'\"“”‘’")
		if strings.HasPrefix(value, "呱，") || strings.HasPrefix(value, "呱,") {
			approvedGua = true
			break
		}
	}
	if !approvedGua {
		return false
	}
	for _, contradiction := range []string{
		"呱——", "呱—", "拖长音", "呱了一声", "单独一个‘呱’", "单独一个“呱”", "改成拟声", "改为拟声",
	} {
		if strings.Contains(text, contradiction) {
			return true
		}
	}
	return false
}

func saveDeepSeekAIJudge(projectDir string, artifact *deepseekAIJudgeArtifact) error {
	if artifact == nil {
		return nil
	}
	if err := validateDeepSeekAIJudgeArtifactIdentity(artifact, artifact.CachePolicy); err != nil {
		return fmt.Errorf("DeepSeek artifact cache identity invalid: %w", err)
	}
	reviewsDir := filepath.Join(projectDir, "reviews")
	if err := os.MkdirAll(reviewsDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(reviewsDir, fmt.Sprintf("%02d_deepseek_ai_judge.json", artifact.Chapter)), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(reviewsDir, fmt.Sprintf("%02d_deepseek_ai_judge.md", artifact.Chapter)), []byte(renderDeepSeekAIJudgeMarkdown(*artifact)), 0o644)
}

func appendDeepSeekAIJudgeToUnifiedMarkdown(projectDir string, artifact *deepseekAIJudgeArtifact) error {
	if artifact == nil {
		return nil
	}
	path := filepath.Join(projectDir, "reviews", fmt.Sprintf("%02d.md", artifact.Chapter))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	body := string(data)
	section := renderDeepSeekAIJudgeUnifiedSection(*artifact)
	if idx := strings.Index(body, "\n## DeepSeek 裸正文 AI 判定\n"); idx >= 0 {
		body = strings.TrimRight(body[:idx], "\n") + "\n\n" + section
	} else {
		body = strings.TrimRight(body, "\n") + "\n\n" + section
	}
	return os.WriteFile(path, []byte(body+"\n"), 0o644)
}

func sedimentDeepSeekAIJudgeRAG(
	ctx context.Context,
	st *store.Store,
	embedder rag.Embedder,
	vectorWriter rag.VectorWriter,
	artifact *deepseekAIJudgeArtifact,
) error {
	if artifact == nil {
		return nil
	}
	return toolspkg.UpsertRAGChunks(ctx, st, embedder, vectorWriter, deepseekAIJudgeRAGChunks(*artifact), domain.RAGIndexConfig{})
}

func deepSeekExternalAIJudge(artifact *deepseekAIJudgeArtifact) *reviewreport.ExternalAIJudge {
	if artifact == nil {
		return nil
	}
	return &reviewreport.ExternalAIJudge{
		Name:                 "DeepSeek 裸正文",
		Source:               fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", artifact.Chapter),
		Verdict:              artifact.Verdict,
		RiskLevel:            artifact.RiskLevel,
		AIProbabilityPercent: artifact.AIProbabilityPercent,
		Blocking:             artifact.Blocking,
		Summary:              artifact.Summary,
	}
}

func deepseekAIJudgeRAGChunks(artifact deepseekAIJudgeArtifact) []domain.RAGChunk {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第 %d 章 DeepSeek 裸正文 AI 判定沉淀\n", artifact.Chapter)
	fmt.Fprintf(&b, "模型：%s/%s，reasoning_effort=%s，raw_body_only=%t\n", artifact.Provider, artifact.Model, artifact.ReasoningEffort, artifact.RawBodyOnly)
	fmt.Fprintf(&b, "结论：%s，风险：%s，AI 概率：%d%%，阻断：%t\n", artifact.Verdict, artifact.RiskLevel, artifact.AIProbabilityPercent, artifact.Blocking)
	if artifact.Summary != "" {
		fmt.Fprintf(&b, "摘要：%s\n", artifact.Summary)
	}
	appendRAGList := func(title string, values []string) {
		if len(values) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s：\n", title)
		for _, value := range values {
			fmt.Fprintf(&b, "- %s\n", value)
		}
	}
	appendRAGList("AI 味原因", artifact.Reasons)
	appendRAGList("修改方案", artifact.RevisionPlan)
	appendRAGList("对白专项方案", artifact.DialogueFixPlan)
	appendRAGList("作者声口方案", artifact.AuthorVoicePlan)
	appendRAGList("项目门禁提示", artifact.ProjectGuardWarnings)
	appendRAGList("后续 RAG 规避规则", artifact.RAGRules)
	text := strings.TrimSpace(b.String())
	if text == "" {
		return nil
	}
	return []domain.RAGChunk{rag.NormalizeChunk(domain.RAGChunk{
		ID:         fmt.Sprintf("chapter:%03d:deepseek_ai_judge", artifact.Chapter),
		SourcePath: fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", artifact.Chapter),
		SourceKind: "review",
		Facet:      "review",
		Context:    fmt.Sprintf("第 %d 章 DeepSeek 裸正文 AI 判定", artifact.Chapter),
		Text:       text,
		Summary:    fmt.Sprintf("第 %d 章 DeepSeek 判定 %s/%s，AI 概率 %d%%。", artifact.Chapter, artifact.Verdict, artifact.RiskLevel, artifact.AIProbabilityPercent),
		Keywords: []string{
			"deepseek_ai_judge",
			"raw_body_only",
			"ai_voice_detection",
			"dialogue_fix",
			"author_voice",
			fmt.Sprintf("chapter_%03d", artifact.Chapter),
		},
		Metadata: map[string]any{
			"source":                 "deepseek_ai_judge",
			"chapter":                artifact.Chapter,
			"verdict":                artifact.Verdict,
			"risk_level":             artifact.RiskLevel,
			"ai_probability_percent": artifact.AIProbabilityPercent,
			"blocking":               artifact.Blocking,
			"provider":               artifact.Provider,
			"model":                  artifact.Model,
			"raw_body_only":          artifact.RawBodyOnly,
		},
	})}
}

func renderDeepSeekAIJudgeMarkdown(artifact deepseekAIJudgeArtifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ch%02d DeepSeek 裸正文 AI 判定\n\n", artifact.Chapter)
	writeDeepSeekAIJudgeSectionBody(&b, artifact)
	if strings.TrimSpace(artifact.RawResponse) != "" {
		b.WriteString("\n## 原始响应\n\n")
		b.WriteString("```json\n")
		b.WriteString(strings.TrimSpace(artifact.RawResponse))
		b.WriteString("\n```\n")
	}
	return b.String()
}

func renderDeepSeekAIJudgeUnifiedSection(artifact deepseekAIJudgeArtifact) string {
	var b strings.Builder
	b.WriteString("## DeepSeek 裸正文 AI 判定\n\n")
	writeDeepSeekAIJudgeSectionBody(&b, artifact)
	return strings.TrimRight(b.String(), "\n")
}

func writeDeepSeekAIJudgeSectionBody(b *strings.Builder, artifact deepseekAIJudgeArtifact) {
	fmt.Fprintf(b, "- 模型：%s/%s（reviewer_explicit=%t）\n", valueOrDash(artifact.Provider), valueOrDash(artifact.Model), artifact.ReviewerExplicit)
	fmt.Fprintf(b, "- 缓存策略：%s（cache_key=%s）\n", valueOrDash(artifact.CachePolicy.ReviewProtocolVersion), valueOrDash(artifact.CacheKey))
	fmt.Fprintf(b, "- reasoning_effort：%s\n", artifact.ReasoningEffort)
	fmt.Fprintf(b, "- 输入口径：完整章节正文裸传（raw_body_only=%t，sha256=%s）\n", artifact.RawBodyOnly, artifact.BodySHA256)
	fmt.Fprintf(b, "- 判定：%s / %s / %d%%\n", valueOrDash(artifact.Verdict), valueOrDash(artifact.RiskLevel), artifact.AIProbabilityPercent)
	fmt.Fprintf(b, "- 阻断重写：%t\n", artifact.Blocking)
	if strings.TrimSpace(artifact.Summary) != "" {
		fmt.Fprintf(b, "- 摘要：%s\n", artifact.Summary)
	}
	if strings.TrimSpace(artifact.ParseWarning) != "" {
		fmt.Fprintf(b, "- 解析/配置提示：%s\n", artifact.ParseWarning)
	}
	writeMarkdownListSection(b, "项目门禁提示", artifact.ProjectGuardWarnings)
	writeMarkdownListSection(b, "AI 味原因", artifact.Reasons)
	writeMarkdownListSection(b, "短证据", artifact.Evidence)
	writeMarkdownListSection(b, "修改方案", artifact.RevisionPlan)
	writeMarkdownListSection(b, "对白专项修改方案", artifact.DialogueFixPlan)
	writeMarkdownListSection(b, "作者声口方案", artifact.AuthorVoicePlan)
	writeMarkdownListSection(b, "RAG 后续规避规则", artifact.RAGRules)
}

func writeMarkdownListSection(b *strings.Builder, title string, values []string) {
	b.WriteString("\n### " + title + "\n\n")
	if len(values) == 0 {
		b.WriteString("- 无\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
}

func deepseekJudgeBlocking(artifact deepseekAIJudgeArtifact) bool {
	switch strings.ToLower(strings.TrimSpace(artifact.Verdict)) {
	case "ai_like":
		return true
	case "mixed":
		return artifact.AIProbabilityPercent >= 20 || artifact.RiskLevel == "medium" || artifact.RiskLevel == "high"
	case "parse_failed":
		return true
	}
	switch artifact.RiskLevel {
	case "high":
		return true
	case "medium":
		return artifact.AIProbabilityPercent >= 30
	}
	return artifact.AIProbabilityPercent >= 45
}

func normalizeDeepSeekVerdict(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ai_like", "ai", "ai-like", "generated":
		return "ai_like"
	case "mixed", "uncertain", "likely_mixed":
		return "mixed"
	case "human_like", "human", "human-like":
		return "human_like"
	default:
		if strings.TrimSpace(value) == "" {
			return "unknown"
		}
		return strings.TrimSpace(value)
	}
}

func normalizeDeepSeekRisk(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		if strings.TrimSpace(value) == "" {
			return "unknown"
		}
		return strings.TrimSpace(value)
	}
}

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func cleanStringList(values []string, limit int) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "—" {
			continue
		}
		if len([]rune(value)) > 260 {
			value = string([]rune(value)[:260]) + "..."
		}
		out = appendUnique(out, value)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(s, "```"); ok {
		s = after
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimPrefix(s, "JSON")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

func valueOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	return value
}
