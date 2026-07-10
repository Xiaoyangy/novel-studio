package tools

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
)

// methodologyCommitExtras 是 commit_chapter 新增的可选自报参数组。
// 全部可缺省：缺失时对应检查跳过（向后兼容）。
type methodologyCommitExtras struct {
	// ChapterContent 由 commit 内部填充（不经 LLM args），供黄金三章等文本检查使用。
	ChapterContent string `json:"-"`

	SceneDynamics    *domain.SceneDynamics             `json:"scene_dynamics,omitempty"`
	POV              string                            `json:"pov,omitempty"`
	Confidence       *domain.ConfidenceReport          `json:"confidence,omitempty"`
	ExpressionChecks []domain.CharacterExpressionCheck `json:"character_expression_check,omitempty"`
}

// methodologyViolations 跑方法论批次引入的全部确定性软检查，产出 warning 级
// Violation 随现有 rule_violations 透传（Editor 语义裁定；绝不阻塞 commit、
// 绝不触碰 PendingRewrites / flow 等控制流）。
func (t *CommitChapterTool) methodologyViolations(chapter, wordCount int, hookType string, extras methodologyCommitExtras) []rules.Violation {
	var violations []rules.Violation
	warn := func(rule, target string, actual any) {
		violations = append(violations, rules.Violation{
			Rule:     rule,
			Target:   target,
			Actual:   actual,
			Severity: rules.SeverityWarning,
		})
	}

	// --- 场景动力（Task 001）：落盘 + 跨章趋势 lint ---
	if d := extras.SceneDynamics; d != nil {
		d.Chapter = chapter
		if err := d.Validate(); err != nil {
			warn("scene_dynamics_invalid", err.Error(), fmt.Sprintf("%+v", *d))
		} else {
			history := t.store.Methodology.LoadRecentSceneDynamics(chapter, 5)
			if err := t.store.Methodology.SaveSceneDynamics(*d); err != nil {
				slog.Warn("场景动力落盘失败，跳过", "module", "commit", "chapter", chapter, "err", err)
			}
			for _, issue := range domain.LintSceneDynamicsTrend(*d, history) {
				warn("scene_dynamics_trend", issue, d.PressureIndex)
			}
		}
	}

	// --- 节奏契约（Task 002）：体裁硬数字确定性 lint ---
	if contract, err := t.store.Methodology.LoadPacingContract(); err == nil && contract != nil {
		// 明确的 user_rules.chapter_words 是本书唯一字数事实源。体裁预设仍负责
		// 钩子和冲突节奏，但不能再用另一套字数带制造互相矛盾的双重门禁。
		if snapshot, loadErr := t.store.UserRules.Load(); loadErr == nil && snapshot != nil && snapshot.Structured.ChapterWords != nil {
			contract.ChapterWordMin = 0
			contract.ChapterWordMax = 0
		}
		hookHistory := t.hookHistoryWithCurrent(chapter, hookType)
		for _, issue := range contract.LintPacing(chapter, wordCount, hookType, hookHistory) {
			warn("pacing_contract", issue, wordCount)
		}
	}

	// --- POV 契约（Task 016）：越界/轮换/汇合检查 + 按章历史 ---
	if extras.POV != "" {
		if progress, err := t.store.Progress.Load(); err == nil && progress != nil {
			for _, issue := range progress.POV.CheckPOV(chapter, extras.POV) {
				warn("pov_contract", issue, extras.POV)
			}
		}
		if err := t.store.Progress.SetChapterPOV(chapter, extras.POV); err != nil {
			slog.Warn("POV 历史记录失败，跳过", "module", "commit", "chapter", chapter, "err", err)
		}
	}

	// --- 置信度（Task 037）：纯观测信号，低分只 warning ---
	if c := extras.Confidence; c != nil {
		if err := c.Validate(); err != nil {
			warn("confidence_invalid", err.Error(), c.Overall)
		} else {
			if err := t.store.Methodology.SaveConfidence(chapter, *c); err != nil {
				slog.Warn("置信度报告落盘失败，跳过", "module", "commit", "chapter", chapter, "err", err)
			}
			if c.Overall < domain.ConfidenceWarnThreshold {
				target := fmt.Sprintf("Writer 自报置信度 %.2f 低于 %.2f", c.Overall, domain.ConfidenceWarnThreshold)
				if len(c.Doubts) > 0 {
					target += "；疑点: " + c.Doubts[0]
				}
				warn("low_confidence", target, c.Overall)
			}
		}
	}

	// --- 人设一致性（Task 026）：自报表现强度 vs BigFive 期望区间 ---
	if len(extras.ExpressionChecks) > 0 {
		violations = append(violations, t.personalityViolations(chapter, extras.ExpressionChecks)...)
	}

	// --- 黄金三章（Task 076）：前 3 章留存信号确定性检查 ---
	if chapter <= 3 {
		violations = append(violations, t.goldenThreeViolations(chapter, extras.ChapterContent, hookType)...)
	}

	// --- 事件编织（Task 078）：线索连续静默超限 warning（默认阈值 6 章） ---
	if weave, err := t.store.WorldSim.LoadEventWeave(); err == nil && weave != nil && len(weave.Events) > 0 {
		for _, issue := range weave.SilentThreadOverruns(chapter, 6) {
			warn("event_weave_silent_thread", issue, chapter)
		}
	}

	// --- 世界推演落后（Task 058）：与 diag OffscreenWorldStale 互补的提交时提醒 ---
	if tick, err := t.store.WorldSim.LoadTick(); err == nil && tick != nil {
		const arcLenDefault = 8 // 一个弧的默认章数
		if gap := chapter - tick.ThroughChapter; gap > arcLenDefault {
			warn("world_tick_lag",
				fmt.Sprintf("世界推演游标停在第%d章，已落后本章 %d 章（>一个弧长 %d）：镜头外世界停摆，离屏事件与伏笔素材断供，下次弧边界应先 save_world_tick", tick.ThroughChapter, gap, arcLenDefault),
				gap)
		}
	}

	return violations
}

// hookHistoryWithCurrent 返回"截至本章（含）"的钩子历史：progress 里已记录的
// 历史此刻尚未包含本章（MarkChapterComplete 在前面步骤已写入，但保守起见按位补齐）。
func (t *CommitChapterTool) hookHistoryWithCurrent(chapter int, hookType string) []string {
	progress, err := t.store.Progress.Load()
	var history []string
	if err == nil && progress != nil {
		history = append(history, progress.HookHistory...)
	}
	for len(history) < chapter-1 {
		history = append(history, "")
	}
	if len(history) < chapter {
		history = append(history, hookType)
	} else {
		history[chapter-1] = hookType
	}
	return history
}

// personalityViolations 把 Writer 自报的角色表现强度与角色 BigFive 推导的期望区间
// 做确定性对比；缺画像的角色跳过。偏差阈值 0.4（宽松：只抓明显 OOC）。
func (t *CommitChapterTool) personalityViolations(chapter int, checks []domain.CharacterExpressionCheck) []rules.Violation {
	chars, err := t.store.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	byName := make(map[string]domain.Character, len(chars))
	for _, c := range chars {
		byName[c.Name] = c
	}
	var violations []rules.Violation
	for _, check := range checks {
		c, ok := byName[check.Name]
		if !ok || c.Psych == nil || c.Psych.BigFive == nil {
			continue
		}
		if check.EmotionIntensity < 0 || check.EmotionIntensity > 1 {
			continue
		}
		low, high := c.Psych.BigFive.ExpectedEmotionRange()
		const tolerance = 0.1 // 区间外再留一点余量，只抓明显偏离
		if check.EmotionIntensity < low-tolerance || check.EmotionIntensity > high+tolerance {
			violations = append(violations, rules.Violation{
				Rule:     "personality_consistency",
				Target:   fmt.Sprintf("第%d章 %s 情绪表现强度 %.2f 偏离大五画像期望 [%.2f,%.2f]（%s）", chapter, check.Name, check.EmotionIntensity, low, high, c.Psych.BigFive.GenerateProfile()),
				Actual:   check.EmotionIntensity,
				Severity: rules.SeverityWarning,
			})
		}
	}
	return violations
}

// goldenThreeViolations Task 076：前三章留存口径的确定性信号（warning 级）。
// 阈值：首段应对白/动作开场；前 300 字专有名词 >8 = 设定倾泻；章末钩子与前章同型提醒。
func (t *CommitChapterTool) goldenThreeViolations(chapter int, content, hookType string) []rules.Violation {
	var violations []rules.Violation
	if content == "" {
		return nil
	}
	warn := func(rule, target string) {
		violations = append(violations, rules.Violation{Rule: rule, Target: target, Actual: chapter, Severity: rules.SeverityWarning})
	}
	paras := splitNonEmptyParas(content)
	if len(paras) > 0 {
		first := paras[0]
		if !strings.HasPrefix(first, "“") && !strings.HasPrefix(first, "\"") && !startsWithAction(first) {
			warn("golden3_opening", fmt.Sprintf("第%d章首段非对白/动作开场（首段：%s）——留存口径建议动作或对白入戏", chapter, compactProgressTextTool(first)))
		}
	}
	// 前 300 字专有名词密度：用角色名+地点名+势力名做词典计数。
	head := headRunes(content, 300)
	proper := 0
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			proper += strings.Count(head, c.Name)
		}
	}
	if world, err := t.store.World.LoadBookWorld(); err == nil && world != nil {
		for _, p := range world.Places {
			proper += strings.Count(head, p.Name)
		}
		for _, f := range world.Factions {
			proper += strings.Count(head, f.Name)
		}
	}
	if proper > 8 {
		warn("golden3_lore_dump", fmt.Sprintf("第%d章前300字专有名词出现 %d 次（>8）：疑似设定倾泻压过事件", chapter, proper))
	}
	// 钩子同型：与上一章 hook_type 相同则提醒换型。
	if chapter > 1 && hookType != "" {
		if progress, err := t.store.Progress.Load(); err == nil && progress != nil && len(progress.HookHistory) >= chapter-1 {
			if prev := progress.HookHistory[chapter-2]; prev == hookType {
				warn("golden3_hook_repeat", fmt.Sprintf("第%d章章末钩子类型 %q 与前章相同：前三章钩子应换型（危机/悬念/承诺/反转）", chapter, hookType))
			}
		}
	}
	return violations
}

func splitNonEmptyParas(text string) []string {
	var out []string
	for _, p := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func startsWithAction(p string) bool {
	// 动作开场启发：以角色行为动词/拟声/短促句开头（非"是/在/有/这/那/自从/当"等叙述引导）。
	for _, lead := range []string{"这", "那", "自从", "当", "在", "从", "作为", "已经", "曾经"} {
		if strings.HasPrefix(p, lead) {
			return false
		}
	}
	return true
}

func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
