package store

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	evolutionReportJSON = "meta/evolution_report.json"
	evolutionReportMD   = "meta/evolution_report.md"
)

// LoadEvolutionReport reads the auditable self-improvement report.
func (s *Store) LoadEvolutionReport() (*domain.EvolutionReport, error) {
	var report domain.EvolutionReport
	if err := s.Progress.io.ReadJSON(evolutionReportJSON, &report); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &report, nil
}

// RefreshEvolutionReport rebuilds a deterministic "observe -> diagnose ->
// propose -> verify" report. It deliberately proposes changes only; adoption
// belongs to a later, explicit promotion step.
func (s *Store) RefreshEvolutionReport(
	chapterLedger *domain.ChapterProgressLedger,
	projectLedger *domain.ProjectProgressLedger,
) (*domain.EvolutionReport, error) {
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return nil, fmt.Errorf("missing progress")
	}
	if chapterLedger == nil {
		chapterLedger, err = s.LoadChapterProgressLedger()
		if err != nil {
			return nil, fmt.Errorf("load chapter progress: %w", err)
		}
	}
	if projectLedger == nil {
		projectLedger, err = s.LoadProjectProgressLedger()
		if err != nil {
			return nil, fmt.Errorf("load project progress: %w", err)
		}
	}

	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)
	window := recentInts(completed, 8)
	reviews, err := s.reviewsForChapters(window)
	if err != nil {
		return nil, err
	}
	metrics, err := s.AIVoice.LoadAllChapterMetrics()
	if err != nil {
		return nil, fmt.Errorf("load ai voice metrics: %w", err)
	}
	metricsByChapter := map[int]domain.ChapterAIVoiceMetrics{}
	for _, m := range metrics {
		metricsByChapter[m.Chapter] = m
	}

	patterns := buildEvolutionPatterns(progress, window, reviews, metricsByChapter, chapterLedger, projectLedger)
	candidates := buildEvolutionCandidates(patterns)
	report := &domain.EvolutionReport{
		Version:           1,
		NovelName:         progress.NovelName,
		GeneratedAt:       time.Now().Format(time.RFC3339),
		CurrentChapter:    progress.CurrentChapter,
		CompletedChapters: completed,
		WindowChapters:    window,
		Health:            buildEvolutionHealth(progress, completed, window, reviews, metricsByChapter, patterns),
		Patterns:          patterns,
		Candidates:        candidates,
		Guardrails: []string{
			"本报告只提出候选进化，不自动改 prompts、lint、代码或大纲。",
			"L1/L2 候选可优先落到本书规则或下一章计划；L3/L4 必须经过测试和人工确认。",
			"任何进化都不能绕过 chapter accept 门禁、资源账本边界和已发布章节事实。",
		},
		VerificationPlan: []string{
			"go test ./...",
			"python3 scripts/validate_skill_context.py",
			"jq empty meta/evolution_report.json meta/project_progress.json meta/chapter_progress.json",
			"用 --refresh-progress 回放一次，确认 evolution_report 与 project_progress 同步刷新。",
		},
		SourceArtifacts: []string{
			"reviews/*.json",
			"meta/chapter_metrics/*.json",
			"meta/chapter_progress.json",
			"meta/project_progress.json",
			"meta/progress.json",
		},
	}
	if err := s.writeEvolutionReport(report); err != nil {
		return nil, err
	}
	return report, nil
}

func (s *Store) writeEvolutionReport(report *domain.EvolutionReport) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(evolutionReportJSON, report); err != nil {
			return err
		}
		return s.Progress.io.WriteMarkdownUnlocked(evolutionReportMD, renderEvolutionReport(report))
	})
}

func (s *Store) reviewsForChapters(chapters []int) ([]domain.ReviewEntry, error) {
	var out []domain.ReviewEntry
	for _, ch := range chapters {
		review, err := s.World.LoadReview(ch)
		if err != nil {
			return nil, fmt.Errorf("load review ch%d: %w", ch, err)
		}
		if review != nil {
			out = append(out, *review)
		}
	}
	return out, nil
}

func buildEvolutionPatterns(
	progress *domain.Progress,
	window []int,
	reviews []domain.ReviewEntry,
	metricsByChapter map[int]domain.ChapterAIVoiceMetrics,
	chapterLedger *domain.ChapterProgressLedger,
	projectLedger *domain.ProjectProgressLedger,
) []domain.EvolutionPattern {
	var patterns []domain.EvolutionPattern
	add := func(p domain.EvolutionPattern) {
		patterns = append(patterns, p)
	}

	if progress != nil && len(progress.PendingRewrites) > 0 {
		add(domain.EvolutionPattern{
			ID:             "active_rewrite_queue",
			Category:       "review",
			Severity:       "action",
			Chapters:       append([]int(nil), progress.PendingRewrites...),
			Evidence:       []string{"pending_rewrites 非空：" + intList(progress.PendingRewrites), progress.RewriteReason},
			Diagnosis:      "当前仍有章节处于返工队列，继续续写会扩大状态漂移。",
			RecommendedFix: "先完成队列内章节打磨/重写并复审 accept，再考虑提示词或规则升级。",
		})
	}

	if projectLedger != nil {
		for _, w := range projectLedger.ScopeWarnings {
			add(domain.EvolutionPattern{
				ID:             "project_scope_" + w.Code,
				Category:       "project",
				Severity:       severityFromPlanning(w.Severity),
				Evidence:       []string{w.Message},
				Diagnosis:      "项目级口径正在漂移，后续自动写作可能按错误章数或旧指南针推进。",
				RecommendedFix: firstNonEmpty(w.Suggestion, "刷新 progress/compass/layered_outline 的一致口径。"),
			})
		}
		if len(projectLedger.ResourceHygiene.Actions) > 0 {
			add(domain.EvolutionPattern{
				ID:             "resource_pending_hygiene",
				Category:       "memory",
				Severity:       "watch",
				Evidence:       append([]string(nil), projectLedger.ResourceHygiene.Actions...),
				Diagnosis:      "资源账本存在 pending 清账压力，容易把未成交资源写成既成事实。",
				RecommendedFix: "在下一章计划前先清理或重标 pending，或把清账动作列入 chapter plan。",
			})
		}
		if len(projectLedger.HookAnalysis.Warnings) > 0 {
			add(domain.EvolutionPattern{
				ID:             "hook_fatigue",
				Category:       "prompt",
				Severity:       "watch",
				Evidence:       append([]string(nil), projectLedger.HookAnalysis.Warnings...),
				Diagnosis:      "近期章尾钩子形态/功能重复，读者会感到机械化。",
				RecommendedFix: "下一章计划中显式指定不同承载物、不同情绪功能和不同推进类型。",
			})
		}
		if repeatedMissing := repeatedPromiseGaps(projectLedger.PromiseEntries, 6); len(repeatedMissing) > 0 {
			add(domain.EvolutionPattern{
				ID:             "promise_gap_repetition",
				Category:       "review",
				Severity:       "watch",
				Evidence:       repeatedMissing,
				Diagnosis:      "近章承诺兑现缺口重复出现，说明单章 plan 没把缺失项转成硬约束。",
				RecommendedFix: "把重复缺口写进下一章 required_beats 或 editor 检查清单。",
			})
		}
	}

	if chapterLedger != nil && chapterLedger.NextPlan != nil {
		for _, inst := range chapterLedger.NextPlan.PlanningInstructions {
			if strings.Contains(inst, "不在当前 outline") {
				add(domain.EvolutionPattern{
					ID:             "missing_next_outline",
					Category:       "outline",
					Severity:       "action",
					Chapters:       []int{chapterLedger.NextPlan.Chapter},
					Evidence:       []string{inst},
					Diagnosis:      "下一章缺章级大纲，继续写会降低整体结构可控性。",
					RecommendedFix: "先 expand_arc 或 append_volume，再进入 writer。",
				})
				break
			}
		}
	}

	metricsWindow := metricsForWindow(window, metricsByChapter)
	if len(metricsWindow) > 0 {
		aiAvg := avgAIVoiceScore(metricsWindow)
		dialogueAvg := avgDialogueRatio(metricsWindow)
		if aiAvg >= 0.25 {
			severity := "watch"
			if aiAvg >= 0.55 {
				severity = "action"
			}
			add(domain.EvolutionPattern{
				ID:             "ai_voice_risk",
				Category:       "lint",
				Severity:       severity,
				Chapters:       metricChapters(metricsWindow),
				Evidence:       []string{fmt.Sprintf("近窗 ai_voice_score 平均 %.3f", aiAvg)},
				Diagnosis:      "AI腔风险已在近窗抬头，单章靠人工提醒不够稳定。",
				RecommendedFix: "将高风险红旗转成下一章采样/自检硬约束；必要时升级 lint 阈值或 prompt 禁用项。",
			})
		}
		if dialogueAvg > 0 && dialogueAvg < 0.30 {
			add(domain.EvolutionPattern{
				ID:             "supporting_dialogue_low",
				Category:       "prompt",
				Severity:       "watch",
				Chapters:       metricChapters(metricsWindow),
				Evidence:       []string{fmt.Sprintf("近窗配角对话占比平均 %.3f，低于 0.30", dialogueAvg)},
				Diagnosis:      "配角对话偏低，容易让主角解释过多、章节像规则说明。",
				RecommendedFix: "下一章要求至少一个配角用行动/短句贡献信息、阻力或误判纠正。",
			})
		}
		if noWaver := chaptersWithoutWaver(metricsWindow); len(noWaver) >= 3 {
			add(domain.EvolutionPattern{
				ID:             "protagonist_waver_absent",
				Category:       "prompt",
				Severity:       "watch",
				Chapters:       noWaver,
				Evidence:       []string{"近窗至少 3 章 protagonist_waver=false"},
				Diagnosis:      "主角连续过稳会削弱风险感和人味，即使爽点成立也容易机械。",
				RecommendedFix: "下一章让主角出现一次可验证误判、成本犹豫或局部让步，但最终用行动解决。",
			})
		}
		if revised := revisedChapters(metricsWindow); len(revised) > 0 {
			add(domain.EvolutionPattern{
				ID:             "rewrite_pressure",
				Category:       "review",
				Severity:       "watch",
				Chapters:       revised,
				Evidence:       []string{"近窗存在 revision_round > 0 的章节：" + intList(revised)},
				Diagnosis:      "已有章节需要返工才通过，说明前置计划或 writer 自检没有提前捕捉风险。",
				RecommendedFix: "把返工原因沉淀成 evolution candidate，优先升级本书规则或下一章 planning checklist。",
			})
		}
	}

	if repeated := repeatedReviewDimensions(reviews); len(repeated) > 0 {
		add(domain.EvolutionPattern{
			ID:             "review_dimension_repetition",
			Category:       "review",
			Severity:       "watch",
			Evidence:       repeated,
			Diagnosis:      "同一评审维度多次 warning/fail，说明审稿发现的问题没有回流到写作前置约束。",
			RecommendedFix: "将重复维度转成 writer 自检条目和 editor 复核重点。",
		})
	}

	sort.SliceStable(patterns, func(i, j int) bool {
		ri, rj := evolutionSeverityRank(patterns[i].Severity), evolutionSeverityRank(patterns[j].Severity)
		if ri != rj {
			return ri < rj
		}
		return patterns[i].ID < patterns[j].ID
	})
	return patterns
}

func buildEvolutionCandidates(patterns []domain.EvolutionPattern) []domain.EvolutionCandidate {
	var out []domain.EvolutionCandidate
	for _, p := range patterns {
		c := domain.EvolutionCandidate{
			ID:           "candidate_" + p.ID,
			Level:        candidateLevelForPattern(p),
			Target:       candidateTargetForPattern(p),
			Impact:       candidateImpactForPattern(p),
			Status:       "proposed",
			Change:       p.RecommendedFix,
			Rationale:    p.Diagnosis,
			Risk:         "可能过度拟合最近窗口，采纳前需看是否符合当前卷弧目标。",
			Validation:   []string{"go test ./...", "python3 scripts/validate_skill_context.py", "回放 --refresh-progress 并检查 meta/evolution_report.md"},
			Verification: "未采纳，尚未执行候选验证；当前只完成诊断与提案。",
		}
		switch p.ID {
		case "active_rewrite_queue":
			c.PromotionTrigger = "返工队列清空且同类问题在后续 3 章仍重复出现。"
		case "hook_fatigue", "supporting_dialogue_low", "protagonist_waver_absent":
			c.PromotionTrigger = "连续 2 个刷新窗口仍命中该模式时，提升为本书 user_rules 或 writer prompt 补丁。"
		default:
			c.PromotionTrigger = "下一次章节 accept 后仍命中且证据更强时再采纳。"
		}
		out = append(out, c)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func buildEvolutionHealth(
	progress *domain.Progress,
	completed []int,
	window []int,
	reviews []domain.ReviewEntry,
	metricsByChapter map[int]domain.ChapterAIVoiceMetrics,
	patterns []domain.EvolutionPattern,
) domain.EvolutionHealth {
	metricsWindow := metricsForWindow(window, metricsByChapter)
	score := 100
	actionCount, watchCount := 0, 0
	for _, p := range patterns {
		switch p.Severity {
		case "action":
			actionCount++
			score -= 14
		case "watch":
			watchCount++
			score -= 7
		}
	}
	pending := 0
	if progress != nil {
		pending = len(progress.PendingRewrites)
		score -= pending * 8
	}
	if score < 0 {
		score = 0
	}
	status := "stable"
	if actionCount > 0 || score < 70 {
		status = "intervene"
	} else if watchCount > 0 || score < 88 {
		status = "watch"
	}
	return domain.EvolutionHealth{
		Status:              status,
		Score:               score,
		AcceptedReviewed:    acceptedReviewCount(reviews),
		Completed:           len(completed),
		RecentAIVoiceScore:  avgAIVoiceScore(metricsWindow),
		RecentDialogueRatio: avgDialogueRatio(metricsWindow),
		PendingRewriteCount: pending,
		WarningCount:        watchCount + actionCount,
	}
}

func renderEvolutionReport(report *domain.EvolutionReport) string {
	var b strings.Builder
	b.WriteString("# 自动进化报告\n\n")
	if report.NovelName != "" {
		fmt.Fprintf(&b, "- 书名：%s\n", report.NovelName)
	}
	fmt.Fprintf(&b, "- 当前章节：第 %d 章\n", report.CurrentChapter)
	fmt.Fprintf(&b, "- 已完成章节：%d\n", len(report.CompletedChapters))
	if len(report.WindowChapters) > 0 {
		fmt.Fprintf(&b, "- 诊断窗口：%s\n", intList(report.WindowChapters))
	}
	fmt.Fprintf(&b, "- 健康状态：%s / %d\n", report.Health.Status, report.Health.Score)
	if report.Health.RecentAIVoiceScore > 0 || report.Health.RecentDialogueRatio > 0 {
		fmt.Fprintf(&b, "- 近窗 AI味均值：%.3f；配角对话均值：%.3f\n", report.Health.RecentAIVoiceScore, report.Health.RecentDialogueRatio)
	}
	fmt.Fprintf(&b, "- 生成时间：%s\n\n", report.GeneratedAt)

	if len(report.Patterns) > 0 {
		b.WriteString("## 诊断模式\n\n")
		b.WriteString("| ID | 等级 | 类别 | 章节 | 诊断 | 建议 |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, p := range report.Patterns {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				escapeTable(p.ID),
				escapeTable(p.Severity),
				escapeTable(p.Category),
				escapeTable(intList(p.Chapters)),
				escapeTable(compactProgressText(p.Diagnosis, 90)),
				escapeTable(compactProgressText(p.RecommendedFix, 110)),
			)
		}
		b.WriteString("\n")
	}

	if len(report.Candidates) > 0 {
		b.WriteString("## 候选进化\n\n")
		for _, c := range report.Candidates {
			fmt.Fprintf(&b, "- **%s** [%s/%s] -> `%s`：%s\n", c.ID, c.Level, c.Status, c.Target, c.Change)
			if c.Impact != "" {
				fmt.Fprintf(&b, "  - 影响范围：%s\n", c.Impact)
			}
			if c.Verification != "" {
				fmt.Fprintf(&b, "  - 验证状态：%s\n", c.Verification)
			}
			if c.PromotionTrigger != "" {
				fmt.Fprintf(&b, "  - 采纳触发：%s\n", c.PromotionTrigger)
			}
		}
		b.WriteString("\n")
	}

	if len(report.Guardrails) > 0 {
		b.WriteString("## 护栏\n\n")
		for _, item := range report.Guardrails {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}

	if len(report.VerificationPlan) > 0 {
		b.WriteString("## 验证计划\n\n")
		for _, item := range report.VerificationPlan {
			fmt.Fprintf(&b, "- `%s`\n", item)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func recentInts(items []int, limit int) []int {
	if limit <= 0 || len(items) <= limit {
		return append([]int(nil), items...)
	}
	return append([]int(nil), items[len(items)-limit:]...)
}

func severityFromPlanning(sev string) string {
	switch sev {
	case "high":
		return "action"
	case "medium":
		return "watch"
	default:
		return "info"
	}
}

func repeatedPromiseGaps(entries []domain.ChapterPromiseEntry, window int) []string {
	if len(entries) == 0 {
		return nil
	}
	start := max(0, len(entries)-window)
	counts := map[string]int{}
	for _, e := range entries[start:] {
		for _, gap := range e.MissingSignals {
			counts[gap]++
		}
	}
	var out []string
	for gap, n := range counts {
		if n >= 3 {
			out = append(out, fmt.Sprintf("%s：近%d章出现%d次", gap, window, n))
		}
	}
	sort.Strings(out)
	return out
}

func metricsForWindow(window []int, byChapter map[int]domain.ChapterAIVoiceMetrics) []domain.ChapterAIVoiceMetrics {
	var out []domain.ChapterAIVoiceMetrics
	for _, ch := range window {
		if m, ok := byChapter[ch]; ok {
			out = append(out, m)
		}
	}
	return out
}

func avgAIVoiceScore(metrics []domain.ChapterAIVoiceMetrics) float64 {
	if len(metrics) == 0 {
		return 0
	}
	var sum float64
	for _, m := range metrics {
		sum += m.AIVoiceScore
	}
	return sum / float64(len(metrics))
}

func avgDialogueRatio(metrics []domain.ChapterAIVoiceMetrics) float64 {
	if len(metrics) == 0 {
		return 0
	}
	var sum float64
	for _, m := range metrics {
		sum += m.DialogueRatio
	}
	return sum / float64(len(metrics))
}

func metricChapters(metrics []domain.ChapterAIVoiceMetrics) []int {
	var out []int
	for _, m := range metrics {
		out = append(out, m.Chapter)
	}
	return out
}

func chaptersWithoutWaver(metrics []domain.ChapterAIVoiceMetrics) []int {
	var out []int
	for _, m := range metrics {
		if !m.ProtagonistWaver {
			out = append(out, m.Chapter)
		}
	}
	return out
}

func revisedChapters(metrics []domain.ChapterAIVoiceMetrics) []int {
	var out []int
	for _, m := range metrics {
		if m.RevisionRound > 0 {
			out = append(out, m.Chapter)
		}
	}
	return out
}

func repeatedReviewDimensions(reviews []domain.ReviewEntry) []string {
	counts := map[string]int{}
	for _, r := range reviews {
		for _, d := range r.Dimensions {
			if d.Verdict == "warning" || d.Verdict == "fail" {
				counts[d.Dimension]++
			}
		}
	}
	var out []string
	for dim, n := range counts {
		if n >= 3 {
			out = append(out, fmt.Sprintf("%s：近窗 %d 次 warning/fail", dim, n))
		}
	}
	sort.Strings(out)
	return out
}

func acceptedReviewCount(reviews []domain.ReviewEntry) int {
	n := 0
	for _, r := range reviews {
		if r.Verdict == "accept" {
			n++
		}
	}
	return n
}

func candidateLevelForPattern(p domain.EvolutionPattern) string {
	switch p.Category {
	case "project", "outline", "memory", "review":
		return "L2 book_rule"
	case "prompt", "lint":
		return "L1 report"
	default:
		return "L1 report"
	}
}

func candidateTargetForPattern(p domain.EvolutionPattern) string {
	switch p.Category {
	case "project":
		return "meta/project_progress.md"
	case "outline":
		return "outline/layered_outline"
	case "memory":
		return "resource_ledger / chapter_progress"
	case "review":
		return "editor checklist"
	case "prompt":
		return "assets/prompts/writer.md or book user_rules"
	case "lint":
		return "ai_voice metrics / commit self-check"
	default:
		return "evolution_report"
	}
}

func candidateImpactForPattern(p domain.EvolutionPattern) string {
	switch p.Category {
	case "project":
		return "影响项目口径、指南针和下一章调度；不直接改正文。"
	case "outline":
		return "影响后续章纲和 writer 任务单；必须保持已完成章节事实不变。"
	case "memory":
		return "影响资源账本、章节进度台账和后续连续性输入。"
	case "review":
		return "影响 editor 检查清单、返工入队标准和下一章前置自检。"
	case "prompt":
		return "影响 writer 写作前置约束和本书规则候选；未采纳前只是提醒。"
	case "lint":
		return "影响机械检查阈值、采样自检和 AI味返工建议；未采纳前不改变门禁。"
	default:
		return "影响自动进化报告本身，不改变生产规则。"
	}
}

func evolutionSeverityRank(sev string) int {
	switch sev {
	case "action":
		return 0
	case "watch":
		return 1
	default:
		return 2
	}
}

func intList(items []int) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("%d", item))
	}
	return strings.Join(parts, ",")
}
