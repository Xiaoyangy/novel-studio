package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → write → review → rewrite → deliver（默认不含 cocreate）。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func settlePipelineDelivery(outputDir string, flags pipelineFlags) error {
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return err
	}
	chapters = filterChaptersForPipelineRange(chapters, flags)
	if len(chapters) == 0 {
		return fmt.Errorf("交付沉淀找不到章节")
	}
	st := store.NewStore(outputDir)
	now := time.Now()
	deliveredAt := now.Format(time.RFC3339)
	stamp := now.Format("20060102T150405")
	snapshotDir := filepath.Join(outputDir, "meta", "delivery_snapshots")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return err
	}

	var snapshots []pipelineDeliverySnapshot
	for _, ch := range chapters {
		snap := pipelineDeliverySnapshot{Version: 1, DeliveredAt: deliveredAt, Chapter: ch}
		review, err := st.World.LoadReview(ch)
		if err != nil {
			return fmt.Errorf("读取第 %d 章 review 失败: %w", ch, err)
		}
		if review != nil {
			snap.ReviewVerdict = review.Verdict
			snap.ReviewSummary = review.Summary
			if review.Scope == "chapter" && review.Verdict == "accept" {
				if _, err := st.RefreshChapterProgressLedger(ch, review); err != nil {
					return fmt.Errorf("刷新第 %d 章推进台账失败: %w", ch, err)
				}
				snap.ChapterProgressRefreshed = true
				snap.ProjectProgressRefreshed = true
				snap.EvolutionReportRefreshed = true
				snap.Artifacts = append(snap.Artifacts,
					"meta/chapter_progress.json",
					"meta/chapter_progress.md",
					"meta/project_progress.json",
					"meta/project_progress.md",
					"meta/evolution_report.json",
					"meta/evolution_report.md",
				)
			}
		}
		present, added, err := settlePipelineRAGFacts(st, ch, deliveredAt)
		if err != nil {
			return fmt.Errorf("沉淀第 %d 章 RAG 事实失败: %w", ch, err)
		}
		snap.RAGFactChunkPresent = present
		snap.RAGFactChunkAdded = added
		if present || added {
			snap.Artifacts = append(snap.Artifacts, "meta/rag/index_state.json", "meta/rag/index_state.md")
		}
		completion, err := buildPipelineChapterCompletion(st, ch, deliveredAt, present, added)
		if err != nil {
			return fmt.Errorf("生成第 %d 章交付完成包失败: %w", ch, err)
		}
		snap.Completion = completion
		path := filepath.Join(snapshotDir, fmt.Sprintf("ch%02d_%s.json", ch, stamp))
		data, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			return err
		}
		snap.Artifacts = append(snap.Artifacts, filepath.ToSlash(filepath.Join("meta", "delivery_snapshots", filepath.Base(path))))
		snapshots = append(snapshots, snap)
	}
	if err := appendPipelineDeliveryLog(outputDir, snapshots); err != nil {
		return err
	}
	if err := writePipelineDeliveryMarkdown(outputDir, snapshots); err != nil {
		return err
	}
	return nil
}

func buildPipelineChapterCompletion(st *store.Store, chapter int, deliveredAt string, ragPresent, ragAdded bool) (*pipelineChapterCompletion, error) {
	completion := &pipelineChapterCompletion{
		Version:       1,
		GeneratedAt:   deliveredAt,
		Chapter:       chapter,
		SummarySource: fmt.Sprintf("summaries/%02d.json", chapter),
		RAG: pipelineRAGCompletion{
			Present:     ragPresent,
			Added:       ragAdded,
			SourcePath:  fmt.Sprintf("summaries/%02d.json", chapter),
			SourceKind:  "chapter_summary_facts",
			UsesBody:    false,
			GeneratedAt: deliveredAt,
		},
		ArtifactRefs: []string{
			"timeline.json",
			"meta/state_changes.json",
			"resource_ledger.json",
			"summaries/" + fmt.Sprintf("%02d.json", chapter),
			"world_rules.json",
			"meta/chapter_progress.json",
			"meta/project_progress.json",
			"meta/evolution_report.json",
			"meta/rag/index_state.json",
		},
	}
	if sum, err := st.Summaries.LoadSummary(chapter); err != nil {
		return nil, err
	} else if sum != nil {
		cp := *sum
		completion.Summary = &cp
	}
	ledger, err := st.LoadChapterProgressLedger()
	if err != nil {
		return nil, err
	}
	var entry *domain.ChapterProgressEntry
	if ledger != nil {
		for i := range ledger.Entries {
			if ledger.Entries[i].Chapter == chapter {
				entry = &ledger.Entries[i]
				break
			}
		}
		if ledger.NextPlan != nil {
			completion.DynamicOutlineRecommendation = digestNextPlan(ledger.NextPlan)
			completion.PlanningRecommendations = appendLimitedStrings(completion.PlanningRecommendations, ledger.NextPlan.PlanningInstructions, 8)
			completion.CharacterStateRecommendations = append(completion.CharacterStateRecommendations, ledger.NextPlan.CharacterContinuity...)
			completion.ResourceFocus = append(completion.ResourceFocus, ledger.NextPlan.ResourceFocus...)
		}
	}
	if entry != nil {
		completion.TimelineProgress = append(completion.TimelineProgress, entry.TimelineEvents...)
		completion.StateChanges = append(completion.StateChanges, entry.StateChanges...)
		completion.ProtagonistChanges = append(completion.ProtagonistChanges, entry.ProtagonistChanges...)
		completion.ResourceLedgerUpdates = append(completion.ResourceLedgerUpdates, entry.ResourceChanges...)
	}
	if len(completion.ResourceLedgerUpdates) == 0 {
		completion.ResourceLedgerRecommendations = deriveResourceLedgerRecommendations(chapter, completion.Summary, entry, deliveredAt)
	}
	if charLedger, err := st.LoadCharacterContinuityLedger(); err == nil && charLedger != nil {
		completion.CharacterStateRecommendations = mergeCharacterHints(completion.CharacterStateRecommendations, charLedger.NextChapterFocus, 12)
	} else if err != nil {
		return nil, err
	}
	if projectLedger, err := st.LoadProjectProgressLedger(); err == nil && projectLedger != nil {
		completion.ProjectPlanningRecommendations = appendLimitedStrings(completion.ProjectPlanningRecommendations, projectLedger.NextChapterActions, 12)
	} else if err != nil {
		return nil, err
	}
	if report, err := st.LoadEvolutionReport(); err == nil && report != nil {
		completion.EvolutionRecommendations = evolutionRecommendationLines(report, 8)
	} else if err != nil {
		return nil, err
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return nil, err
	}
	completion.WorldRuleProgress = worldRuleProgressForCompletion(rules, completion.Summary, entry)
	return completion, nil
}

func digestNextPlan(plan *domain.NextChapterProgressPlan) *pipelineNextPlanDigest {
	if plan == nil {
		return nil
	}
	return &pipelineNextPlanDigest{
		Chapter:          plan.Chapter,
		Title:            plan.Title,
		Position:         plan.Position,
		CoreEvent:        pipelineCompactText(plan.CoreEvent, 360),
		Hook:             pipelineCompactText(plan.Hook, 220),
		RequiredBeats:    appendLimitedStrings(nil, plan.RequiredBeats, 8),
		ContinuityInputs: appendLimitedStrings(nil, plan.ContinuityInputs, 16),
	}
}

func mergeCharacterHints(base, extra []domain.CharacterHint, limit int) []domain.CharacterHint {
	if limit <= 0 {
		return nil
	}
	out := append([]domain.CharacterHint(nil), base...)
	seen := map[string]struct{}{}
	for _, hint := range out {
		seen[hint.Name+"|"+hint.UsageType] = struct{}{}
	}
	for _, hint := range extra {
		if len(out) >= limit {
			break
		}
		key := hint.Name + "|" + hint.UsageType
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, hint)
	}
	return out
}

func deriveResourceLedgerRecommendations(chapter int, summary *domain.ChapterSummary, entry *domain.ChapterProgressEntry, deliveredAt string) []domain.ResourceClaim {
	var changes []domain.StateChange
	if entry != nil {
		changes = append(changes, entry.ProtagonistChanges...)
		changes = append(changes, entry.StateChanges...)
	}
	seen := map[string]struct{}{}
	var out []domain.ResourceClaim
	for _, change := range changes {
		text := change.Field + change.NewValue + change.Reason
		if !looksLikeResourceState(text) {
			continue
		}
		name := pipelineCompactText(firstNonEmptyString(change.NewValue, change.Field), 80)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, domain.ResourceClaim{
			ID:        fmt.Sprintf("ch%02d-resource-recommendation-%d", chapter, len(out)+1),
			Name:      name,
			Owner:     change.Entity,
			Kind:      "derived_state_resource",
			Status:    "pending",
			Risk:      "pipeline_delivery 根据已接受章节状态变化提出入账建议；写后续正文前应由 commit/review 确认是否写入正式资源账本。",
			Evidence:  pipelineCompactText(change.Reason, 140),
			Chapter:   chapter,
			UpdatedAt: deliveredAt,
		})
		if len(out) >= 6 {
			return out
		}
	}
	if len(out) == 0 && summary != nil {
		for _, event := range summary.KeyEvents {
			if !looksLikeResourceState(event) {
				continue
			}
			out = append(out, domain.ResourceClaim{
				ID:        fmt.Sprintf("ch%02d-resource-recommendation-1", chapter),
				Name:      pipelineCompactText(event, 80),
				Kind:      "derived_summary_resource",
				Status:    "pending",
				Risk:      "pipeline_delivery 根据章节摘要提出入账建议；写后续正文前应由 commit/review 确认是否写入正式资源账本。",
				Evidence:  pipelineCompactText(summary.Summary, 140),
				Chapter:   chapter,
				UpdatedAt: deliveredAt,
			})
			break
		}
	}
	return out
}

func looksLikeResourceState(text string) bool {
	for _, term := range []string{"持有物", "资产", "权限", "账户", "黑卡", "欠费单", "收据", "债权", "产权", "凭证", "名额", "资格", "押金", "租约", "账单"} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func appendLimitedStrings(dst, src []string, limit int) []string {
	for _, item := range src {
		if limit > 0 && len(dst) >= limit {
			break
		}
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		dst = append(dst, pipelineCompactText(item, 220))
	}
	return dst
}

func evolutionRecommendationLines(report *domain.EvolutionReport, limit int) []string {
	var lines []string
	for _, c := range report.Candidates {
		if len(lines) >= limit {
			break
		}
		line := strings.TrimSpace(c.Change)
		if line == "" {
			line = strings.TrimSpace(c.Level + " " + c.Target)
		}
		if c.Rationale != "" {
			line += "：" + pipelineCompactText(c.Rationale, 160)
		}
		if line != "" {
			lines = append(lines, pipelineCompactText(line, 240))
		}
	}
	for _, guardrail := range report.Guardrails {
		if len(lines) >= limit {
			break
		}
		if strings.TrimSpace(guardrail) != "" {
			lines = append(lines, "护栏："+pipelineCompactText(guardrail, 220))
		}
	}
	return lines
}

func worldRuleProgressForCompletion(rules []domain.WorldRule, summary *domain.ChapterSummary, entry *domain.ChapterProgressEntry) []pipelineWorldRuleProgress {
	corpus := completionCorpus(summary, entry)
	var out []pipelineWorldRuleProgress
	for _, rule := range rules {
		hits := worldRuleHits(corpus, rule)
		if len(hits) == 0 {
			continue
		}
		out = append(out, pipelineWorldRuleProgress{
			Category: rule.Category,
			Rule:     rule.Rule,
			Boundary: rule.Boundary,
			Evidence: hits,
			Source:   "world_rules.json + chapter facts",
		})
		if len(out) >= 8 {
			break
		}
	}
	if len(out) == 0 && summary != nil {
		evidence := append([]string{}, summary.KeyEvents...)
		if len(evidence) == 0 && summary.Summary != "" {
			evidence = append(evidence, summary.Summary)
		}
		out = append(out, pipelineWorldRuleProgress{
			Category: "derived",
			Rule:     "本章已有摘要事实，但未匹配到 world_rules.json 中的具体规则；下一轮规划需补充世界规则推进或确认本章只做情节推进。",
			Evidence: appendLimitedStrings(nil, evidence, 3),
			Source:   "summaries + delivery audit",
		})
	}
	return out
}

func completionCorpus(summary *domain.ChapterSummary, entry *domain.ChapterProgressEntry) string {
	var parts []string
	if summary != nil {
		parts = append(parts, summary.Summary)
		parts = append(parts, summary.KeyEvents...)
	}
	if entry != nil {
		parts = append(parts, entry.Summary)
		parts = append(parts, entry.KeyEvents...)
		for _, event := range entry.TimelineEvents {
			parts = append(parts, event.Time, event.Event)
			parts = append(parts, event.Characters...)
		}
		for _, change := range entry.StateChanges {
			parts = append(parts, change.Entity, change.Field, change.OldValue, change.NewValue, change.Reason)
		}
		for _, claim := range entry.ResourceChanges {
			parts = append(parts, claim.Name, claim.Kind, claim.Status, claim.Risk, claim.Evidence)
		}
	}
	return strings.Join(parts, "\n")
}

func worldRuleHits(corpus string, rule domain.WorldRule) []string {
	corpus = strings.TrimSpace(corpus)
	if corpus == "" {
		return nil
	}
	var hits []string
	for _, term := range []string{
		"冥雾", "夜租", "冥钞", "普通现金", "契约", "交易", "确认", "冥府黑卡", "黑卡",
		"账单", "审计", "收租鬼", "阴阳公寓", "名字", "影子", "人格资产", "欠费单",
		"规则", "产权", "债权", "镇厄局", "红伞医院", "午夜便利店", "鬼市",
	} {
		if strings.Contains(rule.Category+rule.Rule+rule.Boundary, term) && strings.Contains(corpus, term) {
			hits = append(hits, term)
		}
		if len(hits) >= 5 {
			break
		}
	}
	return hits
}

func settlePipelineRAGFacts(st *store.Store, chapter int, deliveredAt string) (bool, bool, error) {
	sourcePath := fmt.Sprintf("summaries/%02d.json", chapter)
	state, err := st.RAG.LoadIndexState()
	if err != nil {
		return false, false, err
	}
	if state != nil {
		for _, chunk := range state.Chunks {
			if chunk.SourcePath == sourcePath {
				return true, false, nil
			}
		}
	}
	sum, err := st.Summaries.LoadSummary(chapter)
	if err != nil || sum == nil || strings.TrimSpace(sum.Summary) == "" {
		return false, false, err
	}
	if state == nil {
		state = &domain.RAGIndexState{Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	if strings.TrimSpace(state.Config.Collection) == "" {
		state.Config.Collection = "local_keyword"
	}
	text := pipelineRAGSummaryText(*sum, deliveredAt)
	chunk := rag.NormalizeChunk(domain.RAGChunk{
		ID:         fmt.Sprintf("chapter:%03d:pipeline_delivery_facts", chapter),
		SourcePath: sourcePath,
		SourceKind: "chapter",
		Facet:      "plot",
		Context:    fmt.Sprintf("第 %d 章交付确认 | pipeline deliver", chapter),
		Text:       text,
		Summary:    truncateForContext(strings.TrimSpace(sum.Summary), 120),
		Keywords:   append(append([]string{}, sum.Characters...), sum.KeyEvents...),
		Metadata: map[string]any{
			"chapter":      chapter,
			"source":       "pipeline_delivery",
			"delivered_at": deliveredAt,
		},
	})
	state.Chunks = append(state.Chunks, chunk)
	state.ChunkHashes = pipelineRAGChunkHashes(state.Chunks)
	state.UpdatedAt = deliveredAt
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return false, false, err
	}
	return true, true, nil
}

func pipelineRAGSummaryText(sum domain.ChapterSummary, deliveredAt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第 %d 章交付事实\n", sum.Chapter)
	fmt.Fprintf(&b, "交付时间：%s\n", deliveredAt)
	if strings.TrimSpace(sum.Summary) != "" {
		fmt.Fprintf(&b, "摘要：%s\n", sum.Summary)
	}
	if len(sum.Characters) > 0 {
		fmt.Fprintf(&b, "出场人物：%s\n", strings.Join(sum.Characters, "、"))
	}
	if len(sum.KeyEvents) > 0 {
		fmt.Fprintf(&b, "关键事件：%s\n", strings.Join(sum.KeyEvents, "；"))
	}
	if sum.OpeningDevice != "" {
		fmt.Fprintf(&b, "开头装置：%s\n", sum.OpeningDevice)
	}
	if sum.EndingDevice != "" {
		fmt.Fprintf(&b, "结尾装置：%s\n", sum.EndingDevice)
	}
	return strings.TrimSpace(b.String())
}

func pipelineRAGChunkHashes(chunks []domain.RAGChunk) []string {
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		normalized := rag.NormalizeChunk(chunk)
		if normalized.Hash != "" {
			seen[normalized.Hash] = struct{}{}
		}
	}
	hashes := make([]string, 0, len(seen))
	for hash := range seen {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}

func appendPipelineDeliveryLog(outputDir string, snapshots []pipelineDeliverySnapshot) error {
	path := filepath.Join(outputDir, "meta", "delivery_log.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, snap := range snapshots {
		data, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func writePipelineDeliveryMarkdown(outputDir string, snapshots []pipelineDeliverySnapshot) error {
	var b strings.Builder
	b.WriteString("# Pipeline 交付沉淀\n\n")
	for _, snap := range snapshots {
		fmt.Fprintf(&b, "## 第 %02d 章\n\n", snap.Chapter)
		fmt.Fprintf(&b, "- 交付时间：%s\n", snap.DeliveredAt)
		if snap.ReviewVerdict != "" {
			fmt.Fprintf(&b, "- 审核结论：%s\n", snap.ReviewVerdict)
		}
		if snap.ReviewSummary != "" {
			fmt.Fprintf(&b, "- 审核摘要：%s\n", snap.ReviewSummary)
		}
		fmt.Fprintf(&b, "- 章节推进台账刷新：%t\n", snap.ChapterProgressRefreshed)
		fmt.Fprintf(&b, "- 项目进度刷新：%t\n", snap.ProjectProgressRefreshed)
		fmt.Fprintf(&b, "- 演化报告刷新：%t\n", snap.EvolutionReportRefreshed)
		fmt.Fprintf(&b, "- RAG 事实 chunk 存在：%t\n", snap.RAGFactChunkPresent)
		fmt.Fprintf(&b, "- RAG 事实 chunk 新增：%t\n", snap.RAGFactChunkAdded)
		if snap.Completion != nil {
			c := snap.Completion
			fmt.Fprintf(&b, "- 完成包版本：v%d｜生成时间：%s\n", c.Version, c.GeneratedAt)
			if c.Summary != nil && c.Summary.Summary != "" {
				fmt.Fprintf(&b, "- 摘要沉淀：%s（来源：%s）\n", c.Summary.Summary, c.SummarySource)
			}
			if len(c.TimelineProgress) > 0 {
				fmt.Fprintf(&b, "- 时间线推进：%s\n", renderTimelineProgressInline(c.TimelineProgress))
			}
			if len(c.ProtagonistChanges) > 0 {
				fmt.Fprintf(&b, "- 主角状态推进：%s\n", renderStateChangesInline(c.ProtagonistChanges))
			}
			if len(c.CharacterStateRecommendations) > 0 {
				fmt.Fprintf(&b, "- 人物状态推荐：%s\n", renderCharacterHintsInline(c.CharacterStateRecommendations, 4))
			}
			if len(c.ResourceLedgerUpdates) > 0 {
				fmt.Fprintf(&b, "- 本章资源账台更新：%s\n", renderResourceClaimsInline(c.ResourceLedgerUpdates, 4))
			}
			if len(c.ResourceLedgerRecommendations) > 0 {
				fmt.Fprintf(&b, "- 本章资源账台建议：%s\n", renderResourceClaimsInline(c.ResourceLedgerRecommendations, 4))
			}
			if len(c.ResourceFocus) > 0 {
				fmt.Fprintf(&b, "- 后续资源关注：%s\n", renderResourceClaimsInline(c.ResourceFocus, 4))
			}
			if len(c.WorldRuleProgress) > 0 {
				fmt.Fprintf(&b, "- 世界规则推进：%s\n", renderWorldRuleProgressInline(c.WorldRuleProgress, 4))
			}
			if c.DynamicOutlineRecommendation != nil {
				fmt.Fprintf(&b, "- 动态大纲推荐：第 %d 章 %s｜%s\n",
					c.DynamicOutlineRecommendation.Chapter,
					c.DynamicOutlineRecommendation.Title,
					c.DynamicOutlineRecommendation.CoreEvent,
				)
			}
			if len(c.PlanningRecommendations) > 0 {
				fmt.Fprintf(&b, "- 规划推荐：%s\n", strings.Join(c.PlanningRecommendations, "；"))
			}
			fmt.Fprintf(&b, "- RAG 沉淀：source=%s；uses_body=%t；present=%t；added=%t\n",
				c.RAG.SourcePath, c.RAG.UsesBody, c.RAG.Present, c.RAG.Added)
		}
		if len(snap.Artifacts) > 0 {
			fmt.Fprintf(&b, "- 证据文件：%s\n", strings.Join(snap.Artifacts, "、"))
		}
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(outputDir, "meta", "delivery_log.md"), []byte(b.String()), 0o644)
}

func renderTimelineProgressInline(events []domain.TimelineEvent) string {
	var parts []string
	for _, event := range events {
		label := strings.TrimSpace(event.Time)
		if label != "" {
			label += "："
		}
		parts = append(parts, pipelineCompactText(label+event.Event, 120))
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, "；")
}

func renderStateChangesInline(changes []domain.StateChange) string {
	var parts []string
	for _, change := range changes {
		label := strings.TrimSpace(change.Entity)
		if label != "" && change.Field != "" {
			label += "/"
		}
		label += change.Field
		if label != "" {
			label += "："
		}
		parts = append(parts, pipelineCompactText(label+change.NewValue, 120))
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, "；")
}

func renderCharacterHintsInline(hints []domain.CharacterHint, limit int) string {
	var parts []string
	for _, hint := range hints {
		parts = append(parts, pipelineCompactText(hint.Name+"("+hint.UsageType+")："+hint.Suggestion, 120))
		if limit > 0 && len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, "；")
}

func renderResourceClaimsInline(claims []domain.ResourceClaim, limit int) string {
	var parts []string
	for _, claim := range claims {
		label := claim.Name
		if claim.Status != "" {
			label += "(" + claim.Status + ")"
		}
		parts = append(parts, pipelineCompactText(label, 100))
		if limit > 0 && len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, "；")
}

func renderWorldRuleProgressInline(items []pipelineWorldRuleProgress, limit int) string {
	var parts []string
	for _, item := range items {
		label := item.Category
		if label != "" {
			label += "："
		}
		label += item.Rule
		if len(item.Evidence) > 0 {
			label += "（证据：" + strings.Join(item.Evidence, "、") + "）"
		}
		parts = append(parts, pipelineCompactText(label, 140))
		if limit > 0 && len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, "；")
}

func pipelineCompactText(s string, limit int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

func chapterNumbersFromFiles(dir string) ([]int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "[0-9][0-9].md"))
	if err != nil {
		return nil, fmt.Errorf("列章节失败: %w", err)
	}
	chapters := make([]int, 0, len(matches))
	for _, path := range matches {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		n, err := strconv.Atoi(base)
		if err != nil || n <= 0 {
			continue
		}
		chapters = append(chapters, n)
	}
	sort.Ints(chapters)
	return chapters, nil
}

func nonEmptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// pipelineWrite 跑创作阶段：已完结则跳过；已有进度则恢复；全新项目用创作指令起新书。
//
// 工程卡点与自愈（用户不变量）：
//  1. 第 1 章从未写完 && foundation 已齐 && 零章初始化未就绪 → 自动进程内执行
//     --zero-init 并校验 readiness，通过才允许写第 1 章（writerZeroInitGate 双保险）。
//  2. Coordinator 因瞬时错误/卡点停止而未达标时，在有界次数内自动续跑，
//     不再阶段失败等人工重跑。
func pipelineWrite(opts cliOptions, flags pipelineFlags, state *domain.PipelineState) error {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[pipeline:write] RAG 写作前检查失败：%v\n", err)
		return err
	}
	const maxWriteRuns = 4
	for run := 1; run <= maxWriteRuns; run++ {
		prog, _ := store.NewStore(cfg.OutputDir).Progress.Load()
		if pipelineWriteGoalReached(prog, flags.WriteTo) {
			return nil
		}
		if err := pipelineEnsureZeroInit(opts, cfg.OutputDir); err != nil {
			return err
		}
		if err := ensurePipelineSimulationRestartReady(cfg.OutputDir, prog); err != nil {
			return err
		}
		hasProgress := prog != nil && (strings.TrimSpace(prog.NovelName) != "" || prog.CurrentChapter > 0 || len(prog.CompletedChapters) > 0)
		prompt := ""
		if !hasProgress {
			if strings.TrimSpace(state.Prompt) == "" {
				return fmt.Errorf("write 阶段需要创作指令：用 --prompt/--prompt-file，或在 stages 前加 cocreate")
			}
			prompt = state.Prompt
			fmt.Fprintln(os.Stderr, "[pipeline:write] 全新项目，按创作指令起新书")
		} else if run == 1 {
			fmt.Fprintln(os.Stderr, "[pipeline:write] 检测到已有进度，恢复创作")
		}
		if err := headless.Run(cfg, bundle, headless.Options{Prompt: prompt, StopAfterChapter: flags.WriteTo}); err != nil {
			return err
		}
		prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
		if pipelineWriteGoalReached(prog, flags.WriteTo) {
			return nil
		}
		// 未达标：可能是零章卡点收工（下轮循环顶部自动 zero-init），
		// 也可能是 provider/工具瞬时错误——都走同一条自愈路径：续跑。
		fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d/%d 次运行未达标，自动续跑\n", run, maxWriteRuns)
	}
	return fmt.Errorf("write 阶段自愈续跑 %d 次后仍未达标（详见 logs/headless.log）", maxWriteRuns)
}

// pipelineWriteGoalReached 判定 write 阶段目标是否已达成。
func pipelineWriteGoalReached(prog *domain.Progress, writeTo int) bool {
	if prog == nil {
		return false
	}
	if prog.Phase == domain.PhaseComplete {
		fmt.Fprintln(os.Stderr, "[pipeline:write] 本书已完结")
		return true
	}
	if writeTo > 0 && !hasPendingRewriteAtOrBefore(prog, writeTo) {
		for _, ch := range prog.CompletedChapters {
			if ch == writeTo {
				fmt.Fprintf(os.Stderr, "[pipeline:write] 已完成到 --write-to=%d\n", writeTo)
				return true
			}
		}
	}
	return false
}

// pipelineEnsureZeroInit 第 1 章前的零章初始化自动编排：
// foundation 未齐 → 放行（先让 Architect 干活）；readiness 就绪 → 放行；
// 否则进程内执行 --zero-init（只补缺失 + 切换推演线，不 --overwrite 以免
// 模板覆盖 Architect 特化资产），并要求 readiness 必须通过。
func pipelineEnsureZeroInit(opts cliOptions, outputDir string) error {
	st := store.NewStore(outputDir)
	if !tools.ChapterOnePendingFirstWrite(st) || !tools.FoundationCoreComplete(outputDir) {
		return nil
	}
	if ok, _ := tools.ZeroInitReadinessState(outputDir); ok {
		return nil
	}
	_, reason := tools.ZeroInitReadinessState(outputDir)
	fmt.Fprintf(os.Stderr, "[pipeline:write] 零章卡点：%s → 自动执行 --zero-init\n", reason)
	if err := zeroInitPipeline(opts, []string{"--dir", outputDir, "--reset-simulation-state"}); err != nil {
		return fmt.Errorf("自动 zero-init 失败（第 1 章前硬卡点）: %w", err)
	}
	if ok, why := tools.ZeroInitReadinessState(outputDir); !ok {
		return fmt.Errorf("zero-init 后 readiness 仍未就绪：%s（处理后重跑 --pipeline 即从 write 阶段继续）", why)
	}
	fmt.Fprintln(os.Stderr, "[pipeline:write] 零章初始化就绪 ✓ 继续写第 1 章")
	return nil
}

func ensurePipelineSimulationRestartReady(outputDir string, progress *domain.Progress) error {
	st := store.NewStore(outputDir)
	policy, err := st.LoadSimulationRestartPolicy()
	if err != nil || policy == nil || !policy.Active {
		return err
	}
	want := strings.TrimSpace(policy.GenerationID)
	got := ""
	if progress != nil {
		got = strings.TrimSpace(progress.GenerationID)
	}
	if want == "" || got == want {
		return nil
	}
	return fmt.Errorf("检测到推演重启策略 generation_id=%s，但当前 progress.generation_id=%q。旧章节/旧资源只允许作为背景种子，不能恢复旧进度；请先运行 novel-studio --zero-init --reset-simulation-state --overwrite --dir %s，再用 --pipeline --restart 从第1章生成", want, got, outputDir)
}

// resolveStages 解析 --stages，校验阶段名，缺省返回默认序列。
func resolveStages(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string(nil), defaultPipelineStages...), nil
	}
	var stages []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !knownPipelineStages[s] {
			return nil, fmt.Errorf("未知阶段 %q（可用：cocreate / write / review / rewrite / deliver）", s)
		}
		stages = append(stages, s)
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("--stages 为空")
	}
	return stages, nil
}

func resolvePipelinePrompt(flags pipelineFlags, opts cliOptions) (string, error) {
	flagHasPrompt := flags.Prompt != "" || flags.PromptFile != ""
	globalHasPrompt := opts.Prompt != "" || opts.PromptFile != ""
	if flagHasPrompt && globalHasPrompt {
		return "", fmt.Errorf("--prompt/--prompt-file 只能指定一次")
	}
	if globalHasPrompt {
		return loadPrompt(opts)
	}
	return resolvePipelinePromptFromFlags(flags)
}

func resolvePipelinePromptFromFlags(flags pipelineFlags) (string, error) {
	if flags.Prompt != "" && flags.PromptFile != "" {
		return "", fmt.Errorf("--prompt 和 --prompt-file 不能同时使用")
	}
	if flags.PromptFile != "" {
		path := flags.PromptFile
		var data []byte
		var err error
		if path == "-" {
			data, err = os.ReadFile("/dev/stdin")
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return "", fmt.Errorf("读取 prompt 文件失败: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return strings.TrimSpace(flags.Prompt), nil
}

// loadOrInitPipelineState 读取已有状态；--restart 或阶段列表变化时重置。
func loadOrInitPipelineState(path string, stages []string, prompt string, restart bool) (*domain.PipelineState, error) {
	fresh := &domain.PipelineState{Stages: stages, Prompt: prompt}
	if restart {
		return fresh, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fresh, nil
		}
		return nil, fmt.Errorf("读取流水线状态失败: %w", err)
	}
	var prev domain.PipelineState
	if err := json.Unmarshal(data, &prev); err != nil {
		return nil, fmt.Errorf("流水线状态文件损坏（可加 --restart 重置）: %w", err)
	}
	// 阶段列表变了：旧的 completed 不再适用，按新列表重来（但保留已捕获的 prompt）。
	if !sameStages(prev.Stages, stages) {
		fmt.Fprintln(os.Stderr, "[pipeline] 阶段列表已变化，重置进度（保留已有创作指令）")
		next := &domain.PipelineState{Stages: stages, Prompt: prev.Prompt}
		if prompt != "" {
			next.Prompt = prompt
		}
		return next, nil
	}
	// 命令行新给了 prompt 则覆盖（允许中途修订创作指令）。
	if prompt != "" {
		prev.Prompt = prompt
	}
	return &prev, nil
}

func savePipelineState(path string, state *domain.PipelineState) error {
	state.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// 临时文件 + rename，避免写一半崩溃损坏状态。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func sameStages(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
