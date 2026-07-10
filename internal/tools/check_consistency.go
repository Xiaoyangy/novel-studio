package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// CheckConsistencyTool 返回章节内容和全部状态数据，供 Agent 自行对照判断。
// 纯 IO 工具：只负责加载数据，不注入指令。
type CheckConsistencyTool struct {
	store *store.Store
}

func NewCheckConsistencyTool(store *store.Store) *CheckConsistencyTool {
	return &CheckConsistencyTool{store: store}
}

func (t *CheckConsistencyTool) Name() string { return "check_consistency" }
func (t *CheckConsistencyTool) Description() string {
	return "加载已写草稿和对照数据（世界规则、伏笔、关系、别名、最近摘要）+ 本章计划范围，供你检查一致性并核对正文是否落在计划范围内。必须在 draft_chapter 之后调用"
}
func (t *CheckConsistencyTool) Label() string { return "一致性检查" }

// 只读工具（仅追加 checkpoint 事件，不改状态），可被并发调度。
func (t *CheckConsistencyTool) ReadOnly(_ json.RawMessage) bool        { return true }
func (t *CheckConsistencyTool) ConcurrencySafe(_ json.RawMessage) bool { return true }

func (t *CheckConsistencyTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("要检查的章节号")).Required(),
	)
}

func (t *CheckConsistencyTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int `json:"chapter"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}

	result := map[string]any{"chapter": a.Chapter}

	// 章节内容
	content, wordCount, err := t.store.Drafts.LoadChapterContent(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("no content found for chapter %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	result["content"] = content
	result["word_count"] = wordCount
	wordContract := inspectChapterWordContract(t.store, content)
	result["chapter_words_contract"] = wordContract
	var hardGateViolations []string
	if wordContract.Configured && !wordContract.Passed {
		hardGateViolations = append(hardGateViolations, fmt.Sprintf(
			"chapter_words: actual=%d, required=%d-%d；必须先用 edit_chapter 调整草稿，当前不可 commit_chapter",
			wordContract.Actual, wordContract.Min, wordContract.Max))
	}

	// 对照数据：保留全局性的一致性检查数据，避免重复加载 novel_context 已有的窗口数据
	if rules, _ := t.store.World.LoadWorldRules(); len(rules) > 0 {
		result["world_rules"] = rules
	}
	if world, _ := t.store.World.LoadBookWorld(); world != nil {
		result["book_world"] = world
	}
	if foreshadow, _ := t.store.World.LoadActiveForeshadow(); len(foreshadow) > 0 {
		result["foreshadow_ledger"] = foreshadow
	}
	if relationships, _ := t.store.World.LoadRelationships(); len(relationships) > 0 {
		result["relationships"] = relationships
	}
	if chars, _ := t.store.Characters.Load(); len(chars) > 0 {
		aliasMap := make(map[string]string)
		for _, c := range chars {
			for _, alias := range c.Aliases {
				aliasMap[alias] = c.Name
			}
		}
		if len(aliasMap) > 0 {
			result["alias_map"] = aliasMap
		}
	}
	if summaries, _ := t.store.Summaries.LoadRecentSummaries(a.Chapter, 2); len(summaries) > 0 {
		result["recent_summaries"] = summaries
	}
	if participants := participantsFromConsistencyResult(result); len(participants) > 0 {
		if audit, _ := t.store.ResourceLedger.AuditForParticipants(participants); len(audit.Booked) > 0 || len(audit.Pending) > 0 {
			result["resource_audit"] = audit
		}
	}
	if warnings, _ := t.store.ResourceLedger.AuditTextForPendingFacts(content); len(warnings) > 0 {
		result["resource_warnings"] = warnings
	}
	// Task 074：确定性对账结果先行——存亡/位置/资源/时序/别名五类机器筛查，
	// 每条带原文短引证据；你（LLM）的职责是复核这些并补机器看不见的语义矛盾。
	if reconcile := consistencyReconcile(t.store, a.Chapter, content, nil); len(reconcile) > 0 {
		result["machine_reconcile"] = reconcile
		result["machine_reconcile_usage"] = "机器对账（warning 级事实）：逐条对照原文证据确认真伪；确认为真的矛盾必须在返回问题里列出并给修复建议"
	}

	// 计划范围核对：计划是正文的唯一范围依据，正文不得超出。加载本章计划 + finalize 阶段
	// 遗留的一致性疑点，供 drafter 逐条核对——必须覆盖 required_beats、不得触犯
	// forbidden_moves、不得引入计划外的情节/角色/场景。
	if plan, _ := t.store.Drafts.LoadChapterPlan(a.Chapter); plan != nil {
		scope, flags := chapterPlanScopeCheck(*plan, content)
		result["chapter_plan_scope"] = scope
		result["chapter_plan_scope_usage"] = "范围契约：核对正文是否 (1) 落实全部 required_beats (2) 未触犯任何 forbidden_moves (3) 未引入计划外的重大情节/新角色/新场景。越界的必须在返回问题里列出并修正正文回到计划内"
		if len(flags) > 0 {
			result["plan_scope_flags"] = flags
		}
		attractionEvidence := inspectChapterAttractionEvidence(*plan, content)
		result["reader_attraction_check"] = attractionEvidence
		result["reader_attraction_check_usage"] = "逐项核对 opening_beat、humor_beat_targets、immediate_payoffs 是否已在页面形成事件与人物反应；trend_language_plan 要由指定角色自然说出，不能只出现相似词"
		if attractionRequirementsForChapter(t.store, a.Chapter).Trend && !attractionEvidence.TrendPassed {
			hardGateViolations = append(hardGateViolations, fmt.Sprintf(
				"trend_language_plan: 正文未原样兑现任何已规划短梗（候选：%s）；须按 character_carrier 放进有上下文的人物对白/系统交流/群聊反应",
				strings.Join(attractionEvidence.TrendItems, "、")))
		}
	}
	if warnings, _ := t.store.Drafts.LoadChapterPlanConsistencyWarnings(a.Chapter); len(warnings) > 0 {
		result["plan_consistency_warnings"] = warnings
		result["plan_consistency_warnings_usage"] = "计划阶段遗留的一致性疑点：正文必须已妥善处理（如新角色已交代来历、别名统一）；未处理的要修正"
	}
	if len(hardGateViolations) > 0 {
		result["hard_gate_violations"] = hardGateViolations
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "consistency_check",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint consistency check: %w", err)
	}

	return json.Marshal(result)
}

// chapterPlanScopeCheck 返回本章计划的范围契约摘要 + 机器可查的越界疑点（flags）。
// scope 供 drafter 逐条自查；flags 是确定性命中的疑点（如正文出现 forbidden_move 的
// 关键短语），需 LLM 复核。范围契约的语义级判断（是否引入计划外情节）交给 drafter。
func chapterPlanScopeCheck(plan domain.ChapterPlan, content string) (map[string]any, []string) {
	scope := map[string]any{
		"chapter":         plan.Chapter,
		"title":           plan.Title,
		"goal":            plan.Goal,
		"conflict":        plan.Conflict,
		"hook":            plan.Hook,
		"required_beats":  plan.Contract.RequiredBeats,
		"forbidden_moves": plan.Contract.ForbiddenMoves,
	}
	if len(plan.Contract.PayoffPoints) > 0 {
		scope["payoff_points"] = plan.Contract.PayoffPoints
	}

	// 确定性越界筛查：forbidden_move 的关键短语若出现在正文，提示可能触犯（供复核，非定论）。
	var flags []string
	if planTitle := strings.TrimSpace(plan.Title); planTitle != "" {
		if heading := firstChapterHeading(content); heading != "" && !chapterTitleEquivalent(heading, planTitle) {
			flags = append(flags, fmt.Sprintf("正文标题与计划标题不一致：正文首行=%q，plan.title=%q —— 本章标题必须继承计划/大纲", heading, planTitle))
		}
	}
	for _, fm := range plan.Contract.ForbiddenMoves {
		for _, kw := range distinctiveKeywords(fm) {
			if strings.Contains(content, kw) {
				flags = append(flags, fmt.Sprintf("正文疑似触犯 forbidden_move %q（命中关键词 %q）——复核：是否真的写了计划禁止的推进", strings.TrimSpace(fm), kw))
				break
			}
		}
	}
	return scope, flags
}

func firstChapterHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(line, "#"))
	}
	return ""
}

// distinctiveKeywords 从一条禁止项里抽取可用于原文命中的片段。禁止项常写成自然语句
// （"主角提前觉醒神力"），正文会用不同措辞表达同一推进（"竟然提前觉醒神力"），整句字面
// 命中会漏。故对去标点后的短语取 4 字滑动窗口——4 字中文窗口足够具体、不易误伤，又能
// 命中部分字面重合。命中只是给 LLM 的复核提示，非定论。
func distinctiveKeywords(s string) []string {
	const windowLen = 4
	// 去掉标点/空白/极常见虚词，保留实词连续块。
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '，', '。', '、', '；', '：', '（', '）', ' ', '\t', '"', '“', '”':
			return true
		}
		return false
	})
	seen := map[string]bool{}
	var out []string
	add := func(kw string) {
		if kw != "" && !seen[kw] {
			seen[kw] = true
			out = append(out, kw)
		}
	}
	for _, f := range fields {
		runes := []rune(f)
		if len(runes) < windowLen {
			if len(runes) >= 3 {
				add(f)
			}
			continue
		}
		for i := 0; i+windowLen <= len(runes); i++ {
			add(string(runes[i : i+windowLen]))
		}
	}
	return out
}

func participantsFromConsistencyResult(result map[string]any) []string {
	var out []string
	if summaries, ok := result["recent_summaries"].([]domain.ChapterSummary); ok && len(summaries) > 0 {
		for _, sum := range summaries {
			out = append(out, sum.Characters...)
		}
	}
	return out
}
