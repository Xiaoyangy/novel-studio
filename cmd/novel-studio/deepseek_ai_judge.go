package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
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
	deepseekAIJudgeReasoningEffort       = agentcore.ThinkingLow
	deepseekAIJudgeReviewProtocolVersion = "review-existing/deepseek-ai-judge/v14"
	deepseekAIJudgeMaxOutputTokens       = 4096
	deepseekAIJudgePassExclusive         = 4
	deepseekAIJudgeMaxAttempts           = 2
	// Existing production evidence shows a healthy full-chapter v4-pro judge can
	// run close to two minutes. Never split the managed three-minute operation
	// into two 90s calls: the primary call receives the whole wall-clock window,
	// and only a malformed response that returns early may use the remainder for
	// one same-body format repair. Longer explicitly configured operations may
	// reserve retries, but every reserved attempt must receive at least 120s.
	deepseekAIJudgeMinAttemptBudget = 120 * time.Second
)

// A malformed-but-fast response should not consume the caller's entire judge
// operation. The first request still receives the full production window; when
// it returns before that window with an invalid structure, the judge may spend
// the remaining wall-clock budget on one format repair. This small floor only
// prevents launching a provider call after the global deadline is effectively
// exhausted.
func deepseekAIJudgeParseRetryFloor(minAttemptBudget time.Duration) time.Duration {
	floor := minAttemptBudget / 10
	if floor > 5*time.Second {
		floor = 5 * time.Second
	}
	if floor < 10*time.Millisecond {
		floor = 10 * time.Millisecond
	}
	return floor
}

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
	started := time.Now()
	finish := func(result deepseekAIJudgeBranchResult) deepseekAIJudgeBranchResult {
		result.Elapsed = time.Since(started)
		return result
	}
	policy := newDeepSeekAIJudgeCachePolicy(selection, chapter, chapterBody)
	cached, loadErr := loadDeepSeekAIJudgeCache(projectDir, policy)
	if loadErr == nil && cached != nil {
		// Explicitness describes this invocation's role selection, not the cached
		// model response identity. Keep final artifact metadata current on a hit.
		cached.ReviewerExplicit = selection.Explicit
		cached.ModelSelection = selection
		return finish(deepseekAIJudgeBranchResult{Artifact: cached, CacheHit: true})
	}
	key := reviewExistingCacheKey(policy)
	release, lockErr := acquireReviewCacheKeyLock(projectDir, deepseekAIJudgeCacheBranch, key, budget)
	if lockErr != nil {
		return finish(deepseekAIJudgeBranchResult{CacheLoadErr: loadErr, Err: lockErr})
	}
	defer func() { _ = release() }()

	lockedCached, lockedLoadErr := loadDeepSeekAIJudgeCache(projectDir, policy)
	if lockedLoadErr == nil && lockedCached != nil {
		lockedCached.ReviewerExplicit = selection.Explicit
		lockedCached.ModelSelection = selection
		return finish(deepseekAIJudgeBranchResult{Artifact: lockedCached, CacheHit: true, CacheLoadErr: loadErr})
	}
	if lockedLoadErr != nil {
		loadErr = lockedLoadErr
	}
	remainingBudget, budgetErr := remainingReviewCacheBudget(started, budget)
	if budgetErr != nil {
		return finish(deepseekAIJudgeBranchResult{CacheLoadErr: loadErr, Err: budgetErr})
	}
	artifact, err := runDeepSeekAIJudge(model, selection, chapter, chapterBody, remainingBudget)
	modelCalls := 1
	if artifact != nil && artifact.AttemptCount > 0 {
		modelCalls = artifact.AttemptCount
	}
	result := deepseekAIJudgeBranchResult{
		Artifact:     artifact,
		CacheLoadErr: loadErr,
		Err:          err,
		ModelCalls:   modelCalls,
	}
	if err == nil {
		if saveErr := saveDeepSeekAIJudgeCache(projectDir, artifact); saveErr != nil {
			result.Err = fmt.Errorf("持久化 DeepSeek 精确正文缓存: %w", saveErr)
		} else {
			result.CachePersisted = true
		}
	}
	return finish(result)
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
	return runDeepSeekAIJudgeWithMinAttemptBudget(
		model,
		selection,
		chapter,
		chapterBody,
		budget,
		deepseekAIJudgeMinAttemptBudget,
	)
}

// deepseekAIJudgeAttemptBudgets returns the fixed per-attempt windows for one
// judge run. A total budget that cannot fund every attempt for at least
// minAttemptBudget stays a single attempt and receives the whole budget.
func deepseekAIJudgeAttemptBudgets(totalBudget, minAttemptBudget time.Duration, maxAttempts int) []time.Duration {
	if totalBudget <= 0 || maxAttempts <= 0 {
		return nil
	}
	attempts := 1
	if minAttemptBudget <= 0 {
		attempts = maxAttempts
	} else if feasible := int(totalBudget / minAttemptBudget); feasible > 1 {
		attempts = feasible
	}
	if attempts > maxAttempts {
		attempts = maxAttempts
	}

	perAttempt := totalBudget / time.Duration(attempts)
	budgets := make([]time.Duration, attempts)
	for i := range budgets {
		budgets[i] = perAttempt
	}
	// Preserve the caller's exact total when integer duration division leaves a
	// remainder. The final attempt is never made smaller than the earlier ones.
	budgets[len(budgets)-1] += totalBudget - perAttempt*time.Duration(attempts)
	return budgets
}

func runDeepSeekAIJudgeWithMinAttemptBudget(
	model agentcore.ChatModel,
	selection deepseekAIJudgeModelSelection,
	chapter int,
	chapterBody string,
	budget time.Duration,
	minAttemptBudget time.Duration,
) (*deepseekAIJudgeArtifact, error) {
	if strings.TrimSpace(chapterBody) == "" {
		return nil, fmt.Errorf("第 %d 章正文为空，无法做 DeepSeek 裸正文 AI 判定", chapter)
	}
	cachePolicy := newDeepSeekAIJudgeCachePolicy(selection, chapter, chapterBody)
	if budget <= 0 {
		budget = 180 * time.Second
	}
	attemptBudgets := deepseekAIJudgeAttemptBudgets(budget, minAttemptBudget, deepseekAIJudgeMaxAttempts)

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

	parseRetryRequested := false
	for attempt := 1; attempt <= deepseekAIJudgeMaxAttempts; attempt++ {
		attemptStarted := time.Now()
		remaining := time.Duration(0)
		if deadline, ok := ctx.Deadline(); ok {
			remaining = time.Until(deadline)
			if remaining < 0 {
				remaining = 0
			}
		}
		plannedAttemptBudget := time.Duration(0)
		if attempt <= len(attemptBudgets) {
			plannedAttemptBudget = attemptBudgets[attempt-1]
		} else {
			// A managed operation intentionally starts as one full-window attempt.
			// If it returns malformed JSON early, use only the actual remaining
			// time for one structural retry; never extend the caller's wall-clock
			// deadline.
			if !parseRetryRequested || remaining < deepseekAIJudgeParseRetryFloor(minAttemptBudget) {
				break
			}
			plannedAttemptBudget = remaining
		}
		attemptBudget := plannedAttemptBudget
		if remaining < attemptBudget {
			attemptBudget = remaining
		}
		slog.Info("DeepSeek judge attempt started",
			"module", "review",
			"chapter", chapter,
			"body_sha", shortReviewCacheKey(artifact.BodySHA256),
			"attempt", attempt,
			"max_attempts", deepseekAIJudgeMaxAttempts,
			"remaining_budget_ms", remaining.Milliseconds(),
			"attempt_budget_ms", attemptBudget.Milliseconds(),
		)
		systemPrompt := deepseekAIJudgeSystemPrompt
		if attempt > 1 {
			systemPrompt += deepseekAIJudgeRetrySuffix
		}
		attemptCtx, attemptCancel := context.WithTimeout(ctx, attemptBudget)
		resp, err := model.Generate(attemptCtx,
			[]agentcore.Message{
				{Role: "system", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: systemPrompt}}},
				{Role: "user", Content: []agentcore.ContentBlock{{Type: agentcore.ContentText, Text: chapterBody}}},
			},
			nil,
			agentcore.WithThinking(deepseekAIJudgeReasoningEffort),
			agentcore.WithMaxTokens(deepseekAIJudgeMaxOutputTokens),
		)
		attemptCancel()
		if err != nil {
			slog.Warn("DeepSeek judge attempt finished",
				"module", "review",
				"chapter", chapter,
				"body_sha", shortReviewCacheKey(artifact.BodySHA256),
				"attempt", attempt,
				"status", "error",
				"elapsed_ms", time.Since(attemptStarted).Milliseconds(),
				"err", err,
			)
			if attempt < len(attemptBudgets) && ctx.Err() == nil &&
				errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			return nil, err
		}
		if resp == nil || strings.TrimSpace(resp.Message.TextContent()) == "" {
			slog.Warn("DeepSeek judge attempt finished",
				"module", "review",
				"chapter", chapter,
				"body_sha", shortReviewCacheKey(artifact.BodySHA256),
				"attempt", attempt,
				"status", "empty",
				"elapsed_ms", time.Since(attemptStarted).Milliseconds(),
			)
			return nil, fmt.Errorf("DeepSeek AI 判定返回空响应")
		}
		artifact.AttemptCount = attempt
		artifact.RawResponse = strings.TrimSpace(resp.Message.TextContent())
		parseDeepSeekAIJudgeResponse(artifact)
		parseRetryRequested = !artifact.AdviceComplete || artifact.Verdict == "parse_failed"
		status := "parse_retry"
		if artifact.AdviceComplete && artifact.Verdict != "parse_failed" {
			status = "complete"
		}
		slog.Info("DeepSeek judge attempt finished",
			"module", "review",
			"chapter", chapter,
			"body_sha", shortReviewCacheKey(artifact.BodySHA256),
			"attempt", attempt,
			"status", status,
			"elapsed_ms", time.Since(attemptStarted).Milliseconds(),
		)
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
		markDeepSeekAIJudgeParseFailure(artifact, "no JSON object found in model response")
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonText), &fields); err != nil {
		markDeepSeekAIJudgeParseFailure(artifact, err.Error())
		return
	}
	if _, ok := fields["ai_probability_percent"]; !ok {
		markDeepSeekAIJudgeParseFailure(artifact, "missing ai_probability_percent")
		return
	}
	var parsed deepseekAIJudgeModelOutput
	if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
		markDeepSeekAIJudgeParseFailure(artifact, err.Error())
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
	normalizeDeepSeekExplicitCleanPassAdvice(artifact)
	artifact.AdviceWarning = deepseekJudgeAdviceWarning(*artifact)
	artifact.AdviceComplete = artifact.AdviceWarning == ""
	artifact.Blocking = deepseekJudgeBlocking(*artifact)
}

func markDeepSeekAIJudgeParseFailure(artifact *deepseekAIJudgeArtifact, warning string) {
	if artifact == nil {
		return
	}
	warning = strings.TrimSpace(warning)
	artifact.ParseWarning = appendDeepSeekWarning(artifact.ParseWarning, warning)
	artifact.AdviceWarning = "外审响应无法解析"
	if warning != "" {
		artifact.AdviceWarning += ": " + warning
	}
}

// A clean pass has no defect to rewrite. Some reviewers correctly return two
// human-writing evidence points but leave the modification arrays empty rather
// than inventing damage. Normalize only that explicit, independently
// non-blocking result to deterministic "no change" advice. Missing evidence,
// an unknown parse disposition, a blocking score, or any substantive partial
// edit plan remains incomplete and therefore fail-closed.
func normalizeDeepSeekExplicitCleanPassAdvice(artifact *deepseekAIJudgeArtifact) {
	if artifact == nil || !deepseekJudgeIsExplicitCleanPass(*artifact) {
		return
	}
	if !deepseekJudgeListsContainOnlyNoChangeAdvice(
		artifact.RevisionPlan,
		artifact.DialogueFixPlan,
		artifact.AuthorVoicePlan,
	) {
		return
	}
	artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
		"无需修改：保留当前正文的事件顺序、人物选择与场景后果。",
	)
	artifact.RevisionPlan = appendUnique(artifact.RevisionPlan,
		"无需修改：保留当前叙述节奏与信息释放方式。",
	)
	artifact.DialogueFixPlan = appendUnique(artifact.DialogueFixPlan,
		"无需修改：保留当前对白的目标、声口与话轮。",
	)
	artifact.AuthorVoicePlan = appendUnique(artifact.AuthorVoicePlan,
		"无需修改：保留当前章节已有的限知声口与叙述质地。",
	)
	artifact.RAGRules = appendUnique(artifact.RAGRules,
		"明确通过且未发现问题时，不为制造所谓毛边改写正文。",
	)
	artifact.RAGRules = appendUnique(artifact.RAGRules,
		"后续章节延续当前人物声口、因果密度与信息释放方式。",
	)
}

func deepseekJudgeIsExplicitCleanPass(artifact deepseekAIJudgeArtifact) bool {
	if artifact.Verdict != "human_like" || artifact.RiskLevel != "low" ||
		artifact.AIProbabilityPercent >= deepseekAIJudgePassExclusive ||
		len(artifact.Evidence) < 2 {
		return false
	}
	summary := strings.ReplaceAll(strings.TrimSpace(artifact.Summary), " ", "")
	if !containsAnyDeepSeekPhrase(summary, []string{
		"无需修改", "无须修改", "不需要修改", "未发现需要修改",
		"未发现明显AI", "无明显AI", "没有明显AI", "不存在明显AI",
		"未发现AI写作痕迹", "无AI写作痕迹",
	}) {
		return false
	}
	for _, reason := range artifact.Reasons {
		reason = strings.ReplaceAll(strings.TrimSpace(reason), " ", "")
		if !containsAnyDeepSeekPhrase(reason, []string{
			"未发现", "无明显", "没有明显", "不存在", "无需修改", "无须修改",
			"人工反证", "人类写作", "真人写作", "真人质感", "整体自然",
		}) {
			return false
		}
	}
	return true
}

func deepseekJudgeListsContainOnlyNoChangeAdvice(lists ...[]string) bool {
	for _, values := range lists {
		for _, value := range values {
			value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
			if !containsAnyDeepSeekPhrase(value, []string{
				"无需修改", "无须修改", "不需要修改", "无需专项修改", "保持原文", "保留当前",
			}) {
				return false
			}
		}
	}
	return true
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
			if len(toolspkg.ProjectContaminationViolations(st, value)) > 0 {
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
		if len(toolspkg.ProjectContaminationViolations(st, artifact.RawResponse)) > 0 {
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
			"项目门禁：修改建议只能使用当前项目 user_rules、章节大纲与已冻结契约中的人物、世界和冲突证据，不得引入历史项目术语或情节模板。",
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
	// This guard is intentionally semantic. Older code accumulated exact lines
	// from one manuscript (including character names), which both leaked story
	// data into production and missed the same bad instruction after paraphrase.
	conflict := containsAnyDeepSeekPhrase(text, []string{
		"分歧", "争执", "误会", "冷战", "裂痕", "感情冲突", "内部矛盾",
		"僵持", "和解", "调停", "猜疑", "不信任", "未完全相信", "怀疑",
	})
	covertInvestigation := containsAnyDeepSeekPhrase(text, []string{"偷偷", "暗中", "背着"}) &&
		containsAnyDeepSeekPhrase(text, []string{"查询", "调查", "试探", "核查"})
	return conflict || covertInvestigation
}

var deepSeekCraftQuotaRe = regexp.MustCompile(`(?:每(?:隔)?\s*\d*\s*(?:字|段|轮|次|处|章)|至少|必须|务必|一律|逐一|每个|每位|所有)[^。！？\n]{0,32}(?:插入|加入|增加|安排|设置|保留|出现|给出|说|动作|反应|细节|对话|场景)`)
var deepSeekCraftCadenceRe = regexp.MustCompile(`每[^。！？\n]{0,24}就(?:插入|引入|加入|增加|安排|设置|出现)`)

func deepSeekCraftAdviceRecreatesConveyor(text string) bool {
	if deepSeekCraftQuotaRe.MatchString(text) || deepSeekCraftCadenceRe.MatchString(text) {
		return true
	}
	additiveDirective := containsAnyDeepSeekPhrase(text, []string{
		"插入", "加入", "增加", "增写", "补", "安排", "设置", "制造", "改为", "转为", "替换", "替代", "让", "把", "给", "强化", "通过",
		"在开头", "在结尾", "先用", "再让", "系统弹出",
	})
	if !additiveDirective {
		return false
	}
	// Additive advice is only admissible when it names the causal effect it is
	// meant to repair. This is the same contract already stated in the judge
	// prompt and is stable across manuscripts. Deletion/compression advice is
	// not an additive humanizer and therefore is outside this guard.
	causalEffect := containsAnyDeepSeekPhrase(text, []string{
		"改变判断", "改变选择", "改变关系位置", "改变场景后果", "造成现场后果", "形成可验证后果", "兑现既有因果",
	})
	if !causalEffect {
		return true
	}
	// Reject advice whose proposed change is justified only as synthetic
	// imperfection. The categories are manuscript-independent; no character,
	// location, prop or previously observed sentence is embedded here.
	artificialNoise := containsAnyDeepSeekPhrase(text, []string{
		"非功能", "无情节", "无任务", "与主线无关", "无目的", "随机", "毛边", "枝节",
		"杂音", "噪声", "打岔", "偏题", "七嘴八舌", "真实感", "生活感", "自然错落",
	})
	manufacturedFriction := containsAnyDeepSeekPhrase(text, []string{
		"误解", "误判", "错觉", "失败", "意外", "混乱", "反悔", "裂痕", "争执", "僵持",
		"两难", "滑脱", "卡住", "打断", "迟疑", "停顿", "拖延", "慢半拍", "未解决",
		"疑问", "不确定", "犹豫",
	})
	cosmeticHumanizer := containsAnyDeepSeekPhrase(text, []string{
		"微表情", "身体反应", "生理", "感官", "回忆", "闪回", "腹诽", "手势", "视线",
		"心跳", "指尖", "掌心", "物件接触", "通感", "动作外化", "环境声音", "环境声",
	})
	return artificialNoise || manufacturedFriction || cosmeticHumanizer
}

func deepSeekForbiddenAIReason(text string) bool {
	// A reason is invalid when it treats genre/UI conventions or the absence of
	// deliberately injected friction as AI evidence. Match concepts instead of
	// memorizing sentences returned by a particular provider run.
	genreStereotype := containsAnyDeepSeekPhrase(text, []string{"典型", "常见", "高度一致"}) &&
		containsAnyDeepSeekPhrase(text, []string{"框架", "系统文", "NPC", "任务反馈", "界面", "格式", "问题-解决", "遭遇-解决"})
	fakeFriction := containsAnyDeepSeekPhrase(text, []string{"缺乏", "缺少", "未出现", "零延迟", "最优路径"}) &&
		containsAnyDeepSeekPhrase(text, []string{"迟", "误解", "偏差", "失误", "摩擦", "随机", "枝节", "七嘴八舌", "冗余", "交互"})
	rewardStereotype := containsAnyDeepSeekPhrase(text, []string{"行为-反馈-奖励", "正确行为→即时反馈", "立即结算奖励"})
	return genreStereotype || fakeFriction || rewardStereotype
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
