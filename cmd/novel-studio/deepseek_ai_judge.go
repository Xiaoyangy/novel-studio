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
	deepseekAIJudgeReviewProtocolVersion = "review-existing/deepseek-ai-judge/v12"
	deepseekAIJudgePassExclusive         = 4
	deepseekAIJudgeMaxAttempts           = 2
)

const deepseekAIJudgeSystemPrompt = `你是中文小说 AI 写作痕迹审核员。你会收到一整章小说正文；用户消息只包含正文，不包含说明、标题解释、检测要求或元数据。请把它当作约 3000 字整段检测场景，判断是否像 AI 写作，并给出可执行修改方案。

数值与建议硬契约：
- ai_probability_percent 必须是 0-100 的整数，不得固定按 5 或 10 取整；3 与 4 是不同结果。
- 0-3% 是有效区间：若文本整体呈现真人质感，两条主要 evidence 都是人工特征，只剩一个“稍显说教/略显规整”的微小疑点，不得把 5% 当成默认最低档。
- 请独立估计概率，不猜测调用方门槛，也不要为了让文本通过而压低分数。工程门禁会在模型响应之外执行。
- 数值必须和结论、理由、证据同向：若 summary 判断“整体接近真人”，reasons 只有“轻微”“未形成模板”等弱问题，evidence 又主要是人物独占细节或自然选择，不得给出两位数中高概率。10% 以上至少需要两条互相独立、可复核且达到中等强度的 AI 特异证据；25% 以上必须有强结构或强语言证据，不能只靠“流程略顺”“配合度偏高”。
- 每次判定都必须返回至少 2 条短证据、2 条整章修改方案、1 条对白专项方案、1 条作者声口方案和 2 条可复用 rag_rules；不得因 low/human_like 省略建议。
- 低 AI 概率不等于可读性合格。即使 verdict=human_like，也必须逐句检查错词、硬拼搭配、指代不清、问答错位和说话人身份；发现硬语义错误必须写入 evidence 与 revision_plan，不能因整体分数低而忽略。
- evidence 必须直接支撑 ai_probability_percent 与 reasons；若最终只找到人工反证而没有两条中等 AI 证据，应明确写“人工反证”并相应降低概率，不能拿人工反证托举高分。
- 建议必须指向正文已有段落功能和人物体验，不能用“随机换词、故意写错、乱加口语、拆碎句子”一类绕检手段，也不能靠给每个配角分一句台词、撒通用微动作或插入无功能细节伪装自然。
- 每条 revision_plan 必须说明它会改变哪个判断、选择、关系位置或场景后果。踢石子、屏幕反光、找零钱、工具滑脱、围观者插嘴、环境声、指尖发凉、心跳加速、干咳和拨筷子若只负责制造“毛边”，都是无效建议，不得返回。
- 不得为了制造所谓“真人毛边”新增争执、误会、反悔、立场分歧、无关闲聊、随机事故、第二轮失败或临时麻烦。若问题是同类过程太顺滑，优先删除、合并、重排流程，把篇幅交给主视角的选择变化、关系推进和结果余波；不要把一个机械流程扩写成更长的机械流程。
- 修改方案必须保留正文已经成立的人物关系方向，不得凭空让合作角色互相不信任、僵持后和解或靠第三人调停。

评审口径校准：
- 不预设作者的年龄、性别、职业、题材和平台，不得要求正文补入程序员、女性职场或其他未出现的作者画像。
- 只从本章已经呈现的类型承诺与人物声口判断自然度；author_voice_plan 只能强化现有声口，不能移植其他项目身份。
- 系统文的明确数字任务、任务卡和结算卡属于常见类型装置，不能仅凭其存在判为 AI；应判断它是否被人物反应、现场因果和个性化系统对白承接。
- 【】是中文系统文区分系统对白的常见排版，独立成段也可以是项目风格。不得仅因它没写成手机 UI、意识流或界面提示就判 AI，也不得要求改成界面载体；只检查括号内的话是否个性化、是否与人物的实际选择重复解释。
- “典型系统文框架”“任务链”“NPC 登场”“遭遇-解决-新问题”都是题材或情节标签，不是语言模型证据，禁止把它们单独列入 reasons 或据此提高 AI 概率。每条 AI 味理由都必须落到不依赖题材的正文语言证据，例如可复核的同型句法、机械复述、异常均匀段落、无人物目的的信息对白或连续即时响应。
- 类型爽文允许清晰因果、明确任务和及时兑现；不能因为故事推进顺畅、主角执行任务或配角承担情节功能就判 AI。若对白已有打断、讨价、误解、生活口气和人物边界，应按实际文本降风险，不得用“模块化”覆盖这些人工特征。
- 面向读者不一定有相关经验；专业信息要靠动作、界面、制度压力、误判和对话后果让读者读懂，不写说明书。

重点检查：
- 段落结构是否过于工整，句长/段长/对话节奏是否过平。
- 是否有连续“角色精准接话 -> 补口径/讲规则 -> 物件即时响应”的模板链。
- 对话是否像流程节点，不像带目标、保留、地位差和言外之意的人；误解、抢话、走神和打断不是自然对白的必备项，不能因正文没有这些现象就提高 AI 概率。
- 是否存在“人：对话”式剧本口吻、过密说话标签、过度动作拍、人人说完整书面句。
- 逐句确认谁在说话，核对姓名、职务、人称和信息边界。人物无明确表演目的时，不会用姓名或职务第三人称指自己；某会长本人说“某会长正在……”一类身份错位是高优先级证据。
- 普通词组是否能顺口朗读，是否出现错词、字面可懂却没人这样搭配的句子、动作没有对象或前后语义接不上。不要把这些基础不通顺误归为“个性化表达”。
- 专业名词是否被解释成教材，或反过来没有落到读者可见的现场证据。
- 是否把群像写成一人一句轮流推进，每个角色都精准完成剧情分工；若存在此问题，优先建议删掉话轮、合并同型人物或转换场景焦点，不得建议给每个人补抢话、漏答、误会、走神或私人琐事。
- 主视角是否只观察任务与流程，缺少触发后的误判、欲望冲突、选择变化、关系判断和事后余波；不能把“补身体反应”当作人物体验。
- 语气是否过度安全、正确、清楚和中性，缺少真正好笑或令人意外的观察，人物对白是否与旁白同质。
- 开头是否泛化，结尾是否只总结下一步；情绪是否始终同一温度，爽点是否只靠别人改口而没有主角的真实感受。

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

const deepseekAIJudgeRetrySuffix = `

上一轮响应未满足输出或“分数-理由”一致性契约。本轮仍只审核用户给出的完整正文，必须重新独立估分并补齐所有 JSON 字段。若结论是 human_like / low，summary 说“整体自然、AI痕迹很低”，reasons 又只有“略显、轻微、稍显”，必须把概率同向下调；不得拿系统文常见的任务反馈、及时奖励或没有故意操作失误当 AI 语言证据。尤其不得省略 evidence、revision_plan、dialogue_fix_plan、author_voice_plan 和 rag_rules；建议要能直接交给 Writer 重渲染。`

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
	PassExclusivePercent int                           `json:"pass_exclusive_percent"`
	Confidence           string                        `json:"confidence,omitempty"`
	Blocking             bool                          `json:"blocking"`
	AdviceComplete       bool                          `json:"advice_complete"`
	AttemptCount         int                           `json:"attempt_count"`
	AdviceWarning        string                        `json:"advice_warning,omitempty"`
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
	if artifact.PassExclusivePercent != deepseekAIJudgePassExclusive {
		return fmt.Errorf("pass_exclusive_percent=%d, want %d", artifact.PassExclusivePercent, deepseekAIJudgePassExclusive)
	}
	if artifact.AttemptCount < 1 {
		return fmt.Errorf("attempt_count=%d, want >=1", artifact.AttemptCount)
	}
	if !artifact.AdviceComplete {
		return fmt.Errorf("外审建议不完整，不能缓存: %s", artifact.AdviceWarning)
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

	artifact := &deepseekAIJudgeArtifact{
		Chapter:              chapter,
		GeneratedAt:          time.Now().Format(time.RFC3339),
		CacheKey:             reviewExistingCacheKey(cachePolicy),
		CachePolicy:          cachePolicy,
		Provider:             selection.Provider,
		Model:                selection.Model,
		ReviewerExplicit:     selection.Explicit,
		ReasoningEffort:      string(deepseekAIJudgeReasoningEffort),
		RawBodyOnly:          true,
		UserPayloadKind:      "chapter_body_only",
		BodySHA256:           reviewreport.BodySHA256(chapterBody),
		PassExclusivePercent: deepseekAIJudgePassExclusive,
		ModelSelection:       selection,
	}
	if !strings.EqualFold(selection.Provider, "deepseek") || selection.Model == "" || !strings.Contains(strings.ToLower(selection.Model), "deepseek") {
		artifact.ParseWarning = "reviewer role is not configured to a DeepSeek model"
	}

	for attempt := 1; attempt <= deepseekAIJudgeMaxAttempts; attempt++ {
		systemPrompt := deepseekAIJudgeSystemPrompt
		if attempt > 1 {
			systemPrompt += deepseekAIJudgeRetrySuffix
		}
		resp, err := model.Generate(ctx,
			[]agentcore.Message{
				{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: systemPrompt}}},
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
		artifact.AttemptCount = attempt
		artifact.RawResponse = strings.TrimSpace(resp.Message.TextContent())
		parseDeepSeekAIJudgeResponse(artifact)
		if artifact.AdviceComplete && artifact.Verdict != "parse_failed" {
			break
		}
	}
	artifact.Blocking = deepseekJudgeBlocking(*artifact)
	return artifact, nil
}

func parseDeepSeekAIJudgeResponse(artifact *deepseekAIJudgeArtifact) {
	if artifact == nil {
		return
	}
	artifact.Verdict = "parse_failed"
	artifact.RiskLevel = "unknown"
	artifact.Blocking = true
	artifact.AdviceComplete = false
	artifact.AdviceWarning = ""
	artifact.Summary = "DeepSeek 返回未满足结构化判定契约；保留原始响应并阻断交付。"
	artifact.Reasons = nil
	artifact.Evidence = nil
	artifact.RevisionPlan = nil
	artifact.DialogueFixPlan = nil
	artifact.AuthorVoicePlan = nil
	artifact.RAGRules = nil

	jsonText := extractJSONObject(artifact.RawResponse)
	if jsonText == "" {
		artifact.ParseWarning = appendDeepSeekWarning(artifact.ParseWarning, "no JSON object found in model response")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonText), &fields); err != nil {
		artifact.ParseWarning = appendDeepSeekWarning(artifact.ParseWarning, err.Error())
		return
	}
	if _, ok := fields["ai_probability_percent"]; !ok {
		artifact.ParseWarning = appendDeepSeekWarning(artifact.ParseWarning, "missing ai_probability_percent")
		return
	}
	var parsed deepseekAIJudgeModelOutput
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		artifact.ParseWarning = appendDeepSeekWarning(artifact.ParseWarning, err.Error())
		return
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
	artifact.AdviceWarning = deepseekJudgeAdviceWarning(*artifact)
	artifact.AdviceComplete = artifact.AdviceWarning == ""
	artifact.Blocking = deepseekJudgeBlocking(*artifact)
}

func deepseekJudgeAdviceWarning(artifact deepseekAIJudgeArtifact) string {
	var missing []string
	if len(artifact.Evidence) < 2 {
		missing = append(missing, "evidence>=2")
	}
	if len(artifact.RevisionPlan) < 2 {
		missing = append(missing, "revision_plan>=2")
	}
	if len(artifact.DialogueFixPlan) < 1 {
		missing = append(missing, "dialogue_fix_plan>=1")
	}
	if len(artifact.AuthorVoicePlan) < 1 {
		missing = append(missing, "author_voice_plan>=1")
	}
	if len(artifact.RAGRules) < 2 {
		missing = append(missing, "rag_rules>=2")
	}
	if warning := deepseekJudgeScoreNarrativeWarning(artifact); warning != "" {
		missing = append(missing, warning)
	}
	if len(missing) == 0 {
		return ""
	}
	return "外审修改建议不完整: " + strings.Join(missing, ", ")
}

func deepseekJudgeScoreNarrativeWarning(artifact deepseekAIJudgeArtifact) string {
	if artifact.AIProbabilityPercent < deepseekAIJudgePassExclusive {
		return ""
	}
	substantiveReasons := make([]string, 0, len(artifact.Reasons))
	for _, reason := range artifact.Reasons {
		if !deepSeekForbiddenAIReason(reason) {
			substantiveReasons = append(substantiveReasons, reason)
		}
	}
	if len(substantiveReasons) == 0 {
		return "score/reasons inconsistent"
	}
	aiSpecificEvidence := 0
	for _, evidence := range artifact.Evidence {
		if deepSeekForbiddenAIReason(evidence) {
			continue
		}
		if containsAnyDeepSeekPhrase(evidence, []string{"人工反证", "非AI", "非 AI", "体现人工", "真人写作", "真人质感", "人类观察", "现场感"}) {
			continue
		}
		aiSpecificEvidence++
	}
	if aiSpecificEvidence == 0 {
		return "score/reasons inconsistent"
	}
	if artifact.AIProbabilityPercent >= 10 && (artifact.RiskLevel == "low" || len(substantiveReasons) < 2 || aiSpecificEvidence < 2) {
		return "score/reasons inconsistent"
	}
	if artifact.Verdict != "human_like" || artifact.RiskLevel != "low" {
		return ""
	}
	summary := strings.TrimSpace(artifact.Summary)
	humanSummary := false
	for _, marker := range []string{"整体接近真人", "接近真人", "接近真人写作", "整体自然", "整体叙述自然", "整体呈现真人写作质感", "真人写作特征", "真人质感", "较高叙事水准", "AI痕迹很低", "AI 痕迹很低"} {
		if strings.Contains(summary, marker) {
			humanSummary = true
			break
		}
	}
	if !humanSummary {
		return ""
	}
	weakReasons := 0
	for _, reason := range substantiveReasons {
		for _, marker := range []string{"轻微", "略显", "略微", "稍显", "稍显", "个别", "仅", "微量", "未形成", "未沦为", "未陷入", "未出现", "未出现明显", "不过", "但未", "偏高；但", "接近", "弱化了意外感", "符合各自身份", "避免了", "融入情境", "带有人情味"} {
			if strings.Contains(reason, marker) {
				weakReasons++
				break
			}
		}
	}
	if weakReasons == len(substantiveReasons) {
		return "score/reasons inconsistent"
	}
	return ""
}

func appendDeepSeekWarning(existing, next string) string {
	existing = strings.TrimSpace(existing)
	next = strings.TrimSpace(next)
	if existing == "" {
		return next
	}
	if next == "" || strings.Contains(existing, next) {
		return existing
	}
	return existing + "; " + next
}

func sanitizeDeepSeekAIJudgeForProject(st *store.Store, artifact *deepseekAIJudgeArtifact) {
	if st == nil || artifact == nil {
		return
	}
	companionSystem := deepSeekProjectRequestsCompanionSystem(st)
	chapterPlan, _ := st.Drafts.LoadChapterPlan(artifact.Chapter)
	protectLeadAlliance := deepSeekProjectProtectsLeadAlliance(st)
	removedProject, removedUserRules, removedTrendRules, removedCraftAdvice, removedRelationshipAdvice, removedInvalidReason := 0, 0, 0, 0, 0, 0
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
	filterInvalidReasons := func(values []string) []string {
		if len(values) == 0 {
			return values
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			if deepSeekForbiddenAIReason(value) {
				removedInvalidReason++
				continue
			}
			out = append(out, value)
		}
		return out
	}
	artifact.Reasons = filterInvalidReasons(filter(artifact.Reasons))
	artifact.Evidence = filterInvalidReasons(filter(artifact.Evidence))
	filterCraftAdvice := func(values []string) []string {
		values = filter(values)
		out := make([]string, 0, len(values))
		for _, value := range values {
			if deepSeekCraftAdviceRecreatesConveyor(value) {
				removedCraftAdvice++
				continue
			}
			if protectLeadAlliance && deepSeekLeadConflictAdvice(value) {
				removedRelationshipAdvice++
				continue
			}
			out = append(out, value)
		}
		return out
	}
	artifact.RevisionPlan = filterCraftAdvice(artifact.RevisionPlan)
	artifact.DialogueFixPlan = filterCraftAdvice(artifact.DialogueFixPlan)
	artifact.AuthorVoicePlan = filterCraftAdvice(artifact.AuthorVoicePlan)
	artifact.RAGRules = filterCraftAdvice(artifact.RAGRules)
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
		} else if deepSeekCraftAdviceRecreatesConveyor(artifact.RawResponse) {
			removedCraftAdvice++
			artifact.RawResponse = ""
		} else if protectLeadAlliance && deepSeekLeadConflictAdvice(artifact.RawResponse) {
			removedRelationshipAdvice++
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
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"只围绕当前项目的人物欲望、现场物件和已发生后果重渲染，不引入其他作品的职业、术语或冲突模板。",
		)
	}
	if removedUserRules > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条与本书系统人格硬设定冲突的建议。", removedUserRules),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"用户硬设定：系统必须能短促接话、吐槽、撑腰并始终支持主角；可控制频率和长度，不得改成冷硬静默的任务机器人。",
		)
		artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
			"保留系统接话和支持性人格，但每次只回应主角眼前一个具体情绪或选择，独立成段，不改成通知播报。",
		)
	}
	if removedTrendRules > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条与本章热梗句法或承载人 plan 冲突的建议。", removedTrendRules),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"本章热梗门禁：已批准的‘呱，’必须保持逗号起手并后接完整吐槽，承载人以 chapter plan 为准；不得改成‘呱——’、拟声动作或另换角色。",
		)
		artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
			"热梗只在既定承载人的自然说话时机出现一次；若现场不顺则整句删除，不改写成拟声动作。",
		)
	}
	if removedCraftAdvice > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条会复制造作微动作、无功能细节或配角轮流发言的建议。", removedCraftAdvice),
		)
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"重排场景承载：删去不必当场说的信息，让主角的主观体验改变判断或选择，再用该选择造成的现场后果完成换挡。",
		)
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"保留事实结果，重新筛选二到四个页面节拍；流程步骤一句带过，把篇幅还给超常兑现、人物关系和选择后的余波。",
		)
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"多人或多摊执行同类任务时，只完整渲染一到两个真正改变主角策略的代表场景，其余结果合并成一段；不得逐人、逐摊、逐项报到。",
		)
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"同类成果只完整跟住一个真正改变主角判断的代表事件；其他同类结果退成背景后果或简短合并，不用多组平行对象逐项验收。",
		)
		artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
			"删掉没有迫切目标的发言者；一组对白只完成一个局面变化，随后转入主角判断、后果行动或未被回答的沉默。",
		)
		artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
			"关系角色的玩笑不要重复任务数据或进度；让其自然看出核心关系的变化并轻轻点破，主角的反应把关系往前推半步。",
		)
		artifact.AuthorVoicePlan = appendUnique(artifact.AuthorVoicePlan,
			"作者声口从主角的偏见、误判、自嘲和选择余波里生长，不用掌心出汗、手指一顿、环境声插针等通用补丁。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"反传送带规则：不得给每个配角分配一句推进台词，也不得用非功能性细节或通用微动作打断对白；先删话轮，再补会改变选择的主观体验。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"情绪因果规则：重要刺激必须经过主角的感知或误判，改变其下一步行动或关系判断；情绪标签和身体小动作不能单独算情感推进。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"同类任务压缩规则：群像决定仍在模拟层完整保留，正文只展开一到两个改变主角策略的代表事件，其余用结果与差异合并承载。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"系统接话规则：吐槽必须点中主角刚才具体钻了什么空子或误判了什么，不能只发一条可移植到任何场景的泛化拒绝。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"代表操作压缩规则：一次失败若已经改变主角选择，后续只保留人物反应和可见结果，不按调整、复测、通过的标准步骤逐拍复述。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"心理去金句规则：主角的实时判断不得提炼成‘X只能Y、不能Z’‘理由一条比一条正确、只有……’等对称结论；保留具体误判，并让它直接改变下一句或下一动作。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"成果非排比规则：同类结果只选一个完整代表事件作为焦点，其他结果用背景变化和人物忙碌合并承载，不按对象轮流证明成功。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"配角关系功能规则：朋友和闺蜜的台词优先改变主角关系位置或现场气氛，不重复金额、流程和审核口径，也不靠无关闲聊与随机事故伪装生活感。",
		)
		evidenceText := strings.Join(artifact.Evidence, "\n")
		if strings.Contains(evidenceText, "时间线索串") || strings.Contains(evidenceText, "均匀推进节奏") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"保留开场约定和傍晚营业的必要时间锚点，删除中间连续报时；两趟运输的现实耗时由现货批次、午饭和第二趟到场后的工作状态承载，不按上午、十一点、五点逐站报时。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"时间非报站规则：复杂项目必须符合现实耗时，但正文只保留会影响选择的时限；中间时间用已完成的工作状态自然跨越，禁止整章按钟点报站。",
			)
		}
		if strings.Contains(evidenceText, "进度汇报") || strings.Contains(evidenceText, "其余四处没再照一个样子摆") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"删掉‘A如何、B如何、C如何’式成果汇总；回到现场后只跟主角眼前一个已经改变的状态，整体完成由一个可见总结果证明，其余差异不再逐项复述。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"群像压缩非清单规则：压缩同类对象不等于把每个人的结果塞进一个排比长句；只留一个连续视点和最终总结果，其他模拟事实继续保存在 plan。",
			)
		}
		if strings.Contains(evidenceText, "三人行动无缝衔接") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"多人开场删去一位角色的推进台词：让一个人主导交锋，另一人只用到场、看时间或上车改变节奏；主角先消化上一句，再进入行动。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"多人开场去接力规则：同一目标下只保留一个主导发言者，其他角色用可见选择改变场面，不按角色顺序各说一句再集体出发。",
			)
		}
		if strings.Contains(evidenceText, "一人一把") || strings.Contains(evidenceText, "接力链") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"开篇同一目标只保留一个主导发言者；其他人的配合用会改变空间或结果的行动承载，不再让参与者按顺序各接一句。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"开篇去接力规则：同一轮现场调整只留一人的关键原话，其余角色以选择和结果改变局面，禁止三人依次接话完成同一目标。",
			)
		}
		if strings.Contains(evidenceText, "这个空子别钻") || strings.Contains(evidenceText, "像导师或编辑在改稿") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"删掉系统对主角的泛化训话；忙中漏看不等于故意钻空子，系统只指出眼前漏掉的人、物或后果，不换成数据面板或客服话。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"误判不等于钻空子：只有主角明知规则仍故意规避时，系统才能用‘钻空子’评价；忙中疏漏要直接指出漏掉的人或东西。",
			)
		}
		if strings.Contains(evidenceText, "这个“好”却比") || strings.Contains(evidenceText, "话不算甜，却让他") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"删掉‘一句话比所有解释都重’‘话不甜却让他踏实’这类情感标注；只保留会改变关系位置的选择或动作，做完就转场，不再解释它有多重。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"关系动作不再标注重量：‘一句话比所有解释都重’‘话不甜却让他踏实’等句子直接删除，保留收纸、少开玩笑或改口的结果。",
			)
		}
		if strings.Contains(evidenceText, "梁广财观察") && strings.Contains(evidenceText, "亲戚群") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"章末熟人圈反转只保留一名亲眼见证者和群里一条明确改口；其余消息与围观反应不必同时交付，但要让外界态度已改变的结果在本章成立。",
			)
		}
		if strings.Contains(evidenceText, "五个摊主逐一表态") || strings.Contains(evidenceText, "预设的关卡对话") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"多对象授权只完整写一个真正改变主角说法的拒绝者；其他人的加入合成一段结果，只保留一两个差异物件，不逐家解释同意理由。",
			)
		}
		if strings.Contains(evidenceText, "后面的决定快了许多") || strings.Contains(evidenceText, "无人为了同一理由答应") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"删掉概括群像差异的开场判断，不补第二轮对话；直接让剩余摊位的两个差异物件和腾出的空角落在同一段，随后进入下一场。",
			)
		}
		if strings.Contains(evidenceText, "便宜不等于省事") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"删掉‘便宜不等于省事’这类教训句，不换成另一句道理；让主角停止追劝并转身处理已经成立的选择，行动本身就是认知变化。",
			)
			artifact.RAGRules = appendUnique(artifact.RAGRules,
				"不等式金句规则：‘X不等于Y’若只负责总结人物刚学到的道理，整句删除；认知变化由紧接着的改口、放弃或行动承担。",
			)
		}
		retainedRevisionText := strings.Join(artifact.RevisionPlan, "\n")
		if strings.Contains(retainedRevisionText, "你俩昨天才认识") || strings.Contains(retainedRevisionText, "任务场景落地为个人情感体验") {
			artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
				"关系角色点破核心同盟的默契后，先写主角对这段关系的新注意与对方是否介意的判断，再让对方用一句或一个选择接住；不要立刻回到任务流程。",
			)
		}
	}
	if removedRelationshipAdvice > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条会给男女主新增分歧、争执或误会的建议。", removedRelationshipAdvice),
		)
		artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
			"需要打断线性流程时，把阻力放在现场条件：允许主角先误判，核心同盟用证据补位，主角基于既有信任修正；双方共同承担后果，不凭空制造关系分歧。",
		)
		artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
			"男女主对话保持同盟感：可以互相提醒、打趣、拆穿逞强，但不靠争执、误会或冷战制造戏剧性。",
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"单女主同盟规则：外部困难负责施压，男女主共同决策并基本同行；感情推进来自信任、补位、吃醋和温柔反差，不来自内部矛盾。",
		)
	}
	if removedInvalidReason > 0 {
		artifact.ProjectGuardWarnings = append(artifact.ProjectGuardWarnings,
			fmt.Sprintf("已移除 DeepSeek 输出中 %d 条把题材结构或通用情节闭环误当成 AI 证据的理由。", removedInvalidReason),
		)
		artifact.RAGRules = appendUnique(artifact.RAGRules,
			"检测证据门禁：系统文框架、任务链和‘问题-解决-新问题’等情节标签不是 AI 语言证据；只保留可复核的句法、节奏、语义或人物体验信号。",
		)
	}
	artifact.AdviceWarning = deepseekJudgeAdviceWarning(*artifact)
	artifact.AdviceComplete = artifact.AdviceWarning == ""
	artifact.Blocking = deepseekJudgeBlocking(*artifact)
}

func deepSeekProjectRequestsCompanionSystem(st *store.Store) bool {
	if st == nil {
		return false
	}
	if toolspkg.ProjectRequiresSystemCompanion(st) {
		return true
	}
	var b strings.Builder
	if snapshot, err := st.UserRules.Load(); err == nil && snapshot != nil {
		b.WriteString(snapshot.Preferences)
		b.WriteByte('\n')
	}
	if premise, err := st.Outline.LoadPremise(); err == nil {
		b.WriteString(premise)
		b.WriteByte('\n')
	}
	if worldRules, err := st.World.LoadWorldRules(); err == nil {
		for _, rule := range worldRules {
			b.WriteString(rule.Rule)
			b.WriteByte('\n')
			b.WriteString(rule.Boundary)
			b.WriteByte('\n')
		}
	}
	if codex, err := st.LoadWorldCodex(); err == nil && codex != nil {
		for _, section := range codex.Sections {
			b.WriteString(section.Content)
			b.WriteByte('\n')
			b.WriteString(strings.Join(section.Rules, "\n"))
			b.WriteByte('\n')
		}
	}
	return domain.SystemCompanionVoiceRequested(b.String())
}

func deepSeekProjectProtectsLeadAlliance(st *store.Store) bool {
	if st == nil {
		return false
	}
	var b strings.Builder
	if snapshot, err := st.UserRules.Load(); err == nil && snapshot != nil {
		b.WriteString(snapshot.Preferences)
		b.WriteByte('\n')
	}
	if codex, err := st.LoadWorldCodex(); err == nil && codex != nil {
		for _, section := range codex.Sections {
			b.WriteString(section.Content)
			b.WriteByte('\n')
			b.WriteString(strings.Join(section.Rules, "\n"))
			b.WriteByte('\n')
		}
	}
	text := b.String()
	for _, marker := range []string{"男女主不应该有矛盾", "男女主不制造矛盾", "男女主无内部矛盾", "不靠男女主矛盾推进"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return strings.Contains(text, "高糖事业搭档") &&
		(strings.Contains(text, "不冷战分手") || strings.Contains(text, "关系铁律"))
}

func deepSeekLeadConflictAdvice(text string) bool {
	for _, marker := range []string{
		"产生短暂分歧", "二人产生分歧", "两人产生分歧", "男女主产生分歧",
		"二人发生争执", "两人发生争执", "男女主争执", "制造误会", "产生误会", "短暂冷战",
		"关系裂痕", "出现裂痕", "感情冲突", "内部矛盾",
		"不同意见，导致短暂僵持", "提出不同意见", "短暂僵持", "才和解", "第三人调停",
		"偷偷用手机查询林澈", "暗中查询林澈", "试探贺骁", "关系猜疑", "人物关系猜疑",
		"未完全相信", "对林澈产生猜疑", "调查林澈",
		"怕我坑你", "怕我坑你？", "先以‘怎么，怕我坑你？’", "先以“怎么，怕我坑你？”",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func deepSeekCraftAdviceRecreatesConveyor(text string) bool {
	for _, marker := range []string{
		"非功能性细节", "无情节任务", "每个次要角色", "每位次要角色", "每个配角", "每位配角",
		"微表情", "掌心微汗", "胸口松快", "手指一顿", "指尖一顿", "半秒停顿", "随手抹一下",
		"身体的直接反应", "身体微反应", "身体反应", "身体动作打断", "加一句内心腹诽", "补一个细微动作",
		"插入环境声音", "环境声打断", "感官杂讯", "感官噪声", "手机屏幕反光", "踢到石子", "脚边踢",
		"找零钱", "翻遍口袋", "扳手滑脱", "工具不顺手", "围观居民插嘴", "围观者插嘴", "真实交互瑕疵",
		"计划外的‘毛边’", "计划外的\"毛边\"", "指尖发凉", "心跳加速", "干咳一声", "筷子在碗里拨",
		"心跳漏拍", "生理惊吓", "突发干扰", "意外碰翻", "加入一次抢白", "同时出声", "两人同时开口",
		"隔着过道插嘴", "来不及回应", "迫使沈知遥代为接话", "放慢半步", "肢体细节暗示", "半截腹诽",
		"诉求被暂时搁置", "至少安排一处",
		"反复核对的细节", "凑近看清", "输入后又删掉半句", "更碎、带手势", "被打断的口语",
		"其他摊主插嘴", "抬手挡一下", "再补后半句", "哪怕半秒", "应至少设置一次", "孩子哭闹",
		"插入一两个摊主的插话", "摊主间的低声嘀咕", "真实现场的七嘴八舌", "转为问答式", "通过对话切分或现场打断",
		"摊主问‘拆要我们自己拆吗", "摊主问“拆要我们自己拆吗", "有人抢先写名字", "有人拽林澈袖子",
		"视角在不同角色上停留不均", "制造自然错落",
		"无关者打岔", "离题插话", "对该提示的抵触、延误或误用", "毛利估算", "两分钟内递不出餐",
		"不服气或面子挂不住", "旁人（如", "私下对母亲周曼的耳语", "可补一句", "可加一句",
		"抛出一个她无法当场回答的新问题", "后续建立管理制度",
		"与主线无直接关联", "与主线无关的日常", "提及天气、身体状态", "相互推卸小事",
		"每三个行动段", "插入纯观察", "环境渲染或回忆片段", "插入一段对", "过往回忆（例如",
		"增加一个小意外", "风将牌吹歪", "提前占位引发口角", "早饭还没吃", "简短拌嘴",
		"必须设置至少一个", "必须设置至少一处", "至少设置一个意外", "至少设置一处意外",
		"完全相反的要求", "迫使林澈陷入两难", "金钱分歧上真实的摩擦", "次要角色的有限视角",
		"嘀咕一句", "每段对话须检视", "带有无关任务的私人目的", "植入一层潜台词",
		"回忆起过往类似情境", "突发小意外", "短暂混乱", "旧事梗", "合伙搞砸", "合夥搞砸",
		"拌嘴", "每完成一个小目标就引入", "微小的代价", "关系的新裂痕",
		"个人感受或记忆", "曾在类似", "回忆曾", "带刺的话", "自觉失言",
		"每段至少插入", "至少一个非语言反应", "无意识摆弄", "动作停顿",
		"闪回", "厌烦感", "极短暂的停顿动作", "指节轻叩", "视线扫过票据袋",
		"快速估算剩余材料", "至少有一个摊主或路人", "信息对白必须伴随干扰",
		"必须插入一个打断因素", "听者提问偏题",
		"贺骁的意外出声", "手机屏幕转向自己", "未完全相信的眼神",
		"无对话的沉默、动作打断或话题偏移", "通过具体行为、回忆或对话",
		"与任务无关的意外事件", "孩子突然哭闹", "找不见", "无目的闲聊", "每3000字",
		"每500字", "过往某次失败", "误判或错觉", "听错摊主报价", "看错沈知遥表情",
		"微小但可察觉", "不可统一的个性化难题", "暴露一项自身弱点", "与主线目的相悖",
		"空箱在晚高峰时被人意外踢翻", "水果滚出", "刻薄观察式内心点评",
		"他踢的是纸箱", "疼的是面子",
		"增加一次沟通误解", "沟通误解", "误解“往里收”", "差点碰倒", "介入解围",
		"反思配合方式", "同意后又", "临时反悔", "额外让步", "承诺免费清洁",
		"掺入抢白和迟疑", "抢白和迟疑", "聋了？收", "指令的跳跃感", "短暂困惑",
		"至少安排一次因误解", "每千字", "感性评判", "体力值浮动", "关系进展提示",
		"走开两步又转身", "犹豫片刻后走开", "自言自语再发问",
		"这个人不问废话", "贺骁的笑总卡在点上",
		"结尾拒绝直接收束", "某个摊主的不满", "系统的延迟提示",
		"增写冷饮老板与林澈的具体对话", "各个摊主的动机通过话语", "至少两个独立对话场景",
		"平行对比让读者", "摸到口袋里的介绍折页犹豫片刻", "指着油脚印问",
		"半截子话", "擦手的动作中犹豫半秒",
		"增加一轮失败", "再增加一轮失败", "需要反复调试", "反复调试的障碍",
		"马上处理的新麻烦", "需马上处理的新麻烦", "另一处部件连带松动", "工具滑脱",
		"极短的沉默", "物品接触", "票据袋角捏皱", "自然延迟半秒", "动作外化",
		"有对话冲突的个体", "取2-3个有摩擦", "取 2-3 个有摩擦",
		"与某位摊主的临时质疑嫁接", "边吃边回应", "增加主视角的选择压力",
		"观察加一笔", "利用这个微小动作", "多推了半寸", "自己追加一句",
		"半句掐断的吐槽", "话不说完的节奏", "必须在下一次出场时延续或变更状态",
		"必须保证至少一家给出", "让该条件参与后续场面",
		"无用尝试", "卡住过道更严重", "由贺骁纠正", "半开玩笑的话拖延一拍",
		"像一排突然睁开的眼睛", "之类通感", "至少在两个时间过渡处",
		"感官触觉或情绪变化作为切换信号", "务必保留至少一个'未解决'",
		"务必保留至少一个“未解决”", "提出-响应-完毕", "真实生活的粘滞感",
		"向林澈低声点出瓶颈", "做出两个手势", "挥手划通道", "指向贺骁",
		"快速估算客流密度", "闪过前期拉人不易的回忆", "握住他的手腕一秒",
		"来回翻册子的动作", "插入赵启明", "再缓缓开口", "先看一眼沈知遥",
		"市井评书", "四十七笔顺当", "偏在四十九笔", "别把账本活成账簿",
		"点出其幸运/巧合成分", "旁人的嘀咕", "杜绝让角色以对白宣布‘下一步’",
		"杜绝让角色以对白宣布“下一步”", "将计划内化为个人欲望",
		"反应慢半拍", "桌脚被卡住又调整", "打破集体响应的完美同步",
		"将系统提示的出现时机推后", "忙碌中忽略提示", "事后才在脑中回响",
		"一条岔开话题", "质疑的语音", "群聊的真实杂音", "被旁边顾客擦身", "让半步",
		"加入一层气味或温度感知", "打断连续动作的快速切换", "低声快速确认",
		"插一句他与沈知遥", "无直接任务功用", "与主线无关的小动作",
		"最小化抗拒", "延迟执行或微调动作", "避免用【】直接输出",
		"执行上的小反复", "搬桌子前先被桌脚绊了一下", "被某位摊主先打断一次",
		"不同时间点呈现", "延迟到下一章", "下一章开头", "无意中看到",
		"数钱时手指停了一下", "下意识看了一眼沈知遥站的位置",
		"改为纯数据反馈", "当前状态：", "只能以状态报告", "数据更新、错误日志",
		"拥有与主角不同的隐藏动机", "摊主暗中较劲", "拍照另有目的", "功能冗余或延迟",
		"拆散到不同场景或时间点",
		"余额改变用触感或心跳外化", "暗处摊棚的私人记忆", "顺势理了理取餐口的筷子或纸袋",
		"那点兴奋像被冰桶里的水浇了一下", "肩膀终于能从耳朵旁边放下来",
		"将她的号召转化为两人共同看向的镜头", "展望宜用疑问或不确定的观察替代",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func deepSeekForbiddenAIReason(text string) bool {
	for _, marker := range []string{
		"典型的'问题-解决-新问题'", "典型的“问题-解决-新问题”", "问题-解决-新问题闭环",
		"遭遇-解决-新问题", "典型系统文框架", "NPC 登场", "NPC登场",
		"缺乏现实场景中常见的短暂迟滞或沟通偏差", "角色均按最优路径行动",
		"缺乏短暂迟疑或操作失误", "指令落地零延迟", "缺少个体摩擦",
		"与常见系统文结构高度一致", "行为后立即结算奖励", "行为-反馈-奖励",
		"正确行为→即时反馈", "常见系统文的任务结算", "系统文常见的任务反馈",
		"系统提示使用【】符号", "系统提示的格式", "减少游戏界面感", "避免用【】直接输出",
		"缺少真实协作中的停顿、误解或冗余动作", "未出现任何配合上的迟滞或越界",
		"缺乏现实场景的随机性", "缺乏枝节",
		"缺少真实现场的七嘴八舌", "缺乏现场交互，接近信息交付块",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsAnyDeepSeekPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
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
	if artifact == nil || !artifact.AdviceComplete {
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
		BodySHA256:           artifact.BodySHA256,
		Verdict:              artifact.Verdict,
		RiskLevel:            artifact.RiskLevel,
		AIProbabilityPercent: artifact.AIProbabilityPercent,
		Blocking:             artifact.Blocking,
		AdviceComplete:       artifact.AdviceComplete,
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
	fmt.Fprintf(b, "- 数值门槛：AI 概率必须 < %d%%\n", valueOrDefaultInt(artifact.PassExclusivePercent, deepseekAIJudgePassExclusive))
	fmt.Fprintf(b, "- 阻断重写：%t\n", artifact.Blocking)
	fmt.Fprintf(b, "- 修改建议完整：%t（attempts=%d）\n", artifact.AdviceComplete, artifact.AttemptCount)
	if strings.TrimSpace(artifact.Summary) != "" {
		fmt.Fprintf(b, "- 摘要：%s\n", artifact.Summary)
	}
	if strings.TrimSpace(artifact.ParseWarning) != "" {
		fmt.Fprintf(b, "- 解析/配置提示：%s\n", artifact.ParseWarning)
	}
	if strings.TrimSpace(artifact.AdviceWarning) != "" {
		fmt.Fprintf(b, "- 建议契约提示：%s\n", artifact.AdviceWarning)
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
	if !artifact.AdviceComplete {
		return true
	}
	if artifact.AIProbabilityPercent >= deepseekAIJudgePassExclusive {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(artifact.Verdict)) {
	case "ai_like":
		return true
	case "mixed":
		return artifact.RiskLevel == "medium" || artifact.RiskLevel == "high"
	case "parse_failed":
		return true
	}
	switch artifact.RiskLevel {
	case "high":
		return true
	case "medium":
		return true
	}
	return false
}

func valueOrDefaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
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
