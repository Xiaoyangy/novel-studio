package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → architect → zero-init → write → review → rewrite → deliver（默认不含 cocreate）。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/entry/headless"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/chenhongyang/novel-studio/internal/userrules"
	writersampler "github.com/chenhongyang/novel-studio/internal/writer/sampler"
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
	if err := validatePipelineFullBookWordBudget(st, outputDir, chapters); err != nil {
		return err
	}
	if err := requirePipelineFinalizedShortBook(outputDir); err != nil {
		return err
	}
	if pending, err := st.RAG.LoadPendingUpserts(); err != nil {
		return fmt.Errorf("读取待回填 RAG 队列失败: %w", err)
	} else if pending != nil && len(pending.Chunks) > 0 {
		return fmt.Errorf("交付前仍有 %d 个 RAG chunks 待回填；先执行 RAG 就绪修复", len(pending.Chunks))
	}
	now := time.Now()
	deliveredAt := now.Format(time.RFC3339)
	stamp := now.Format("20060102T150405")
	snapshotDir := filepath.Join(outputDir, "meta", "delivery_snapshots")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return err
	}

	var snapshots []pipelineDeliverySnapshot
	for _, ch := range chapters {
		if issues := currentRegisteredExternalDeliveryIssues(outputDir, ch); len(issues) > 0 {
			return fmt.Errorf("第 %d 章不能交付：%s", ch, strings.Join(issues, ", "))
		}
		currentReview := inspectCurrentChapterReview(outputDir, ch)
		if len(currentReview.Issues) > 0 {
			return fmt.Errorf("第 %d 章不能交付：审核产物不是当前正文版本：%s", ch, strings.Join(currentReview.Issues, ", "))
		}
		if currentReview.Verdict != "accept" {
			return fmt.Errorf("第 %d 章不能交付：当前章级审核 verdict=%q，必须先完成 rewrite/复审并达到 accept", ch, currentReview.Verdict)
		}
		if currentReview.Disposition == "是" || currentReview.Disposition == "待定" {
			return fmt.Errorf("第 %d 章不能交付：统一审核裁决为‘是否需要改写=%s’，必须先完成 rewrite/复审", ch, currentReview.Disposition)
		}
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

func validatePipelineFullBookWordBudget(st *store.Store, outputDir string, chapters []int) error {
	if st == nil {
		return nil
	}
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil || receipt == nil || receipt.TargetChapters <= 0 {
		return err
	}
	if len(chapters) != receipt.TargetChapters {
		return nil
	}
	for index, chapter := range chapters {
		if chapter != index+1 {
			return nil
		}
	}
	target, err := domain.ResolveBookScaleTarget(
		receipt.EstimatedScale,
		receipt.TargetVolumes,
		receipt.TargetChapters,
	)
	if err != nil {
		return fmt.Errorf("交付前解析全书字数合同: %w", err)
	}
	if target.MinWords <= 0 || target.MaxWords <= 0 {
		return nil
	}
	totalRunes := 0
	for _, chapter := range chapters {
		raw, readErr := os.ReadFile(filepath.Join(outputDir, "chapters", fmt.Sprintf("%02d.md", chapter)))
		if readErr != nil {
			return readErr
		}
		totalRunes += utf8.RuneCount(raw)
	}
	return validatePipelineBookWordTotal(target, totalRunes)
}

func validatePipelineBookWordTotal(target domain.BookScaleTarget, totalRunes int) error {
	if target.MinWords <= 0 || target.MaxWords <= 0 {
		return nil
	}
	if totalRunes < target.MinWords || totalRunes > target.MaxWords {
		return fmt.Errorf(
			"全书正文总字数硬门禁未通过：实际%d字，要求%d-%d字；交付已停止，需通过正式 rewrite/render 流程调整章节，不能用合并稿注水或删改",
			totalRunes,
			target.MinWords,
			target.MaxWords,
		)
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
	for _, term := range []string{"持有物", "资产", "权限", "账户", "资金", "余额", "收据", "债权", "产权", "凭证", "名额", "资格", "押金", "租约", "账单", "库存", "额度"} {
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
	return sharedWorldRuleTerms(strings.Join([]string{rule.Category, rule.Rule, rule.Boundary}, "\n"), corpus, 5)
}

func sharedWorldRuleTerms(ruleText, corpus string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	stop := map[string]struct{}{
		"世界": {}, "规则": {}, "角色": {}, "本章": {}, "正文": {}, "故事": {},
		"必须": {}, "不得": {}, "不能": {}, "可以": {}, "需要": {}, "没有": {},
		"如果": {}, "任何": {}, "所有": {}, "只能": {}, "已经": {}, "当前": {},
	}
	var hits []string
	seen := map[string]struct{}{}
	add := func(term string) bool {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || !strings.Contains(corpus, term) {
			return false
		}
		if _, blocked := stop[term]; blocked {
			return false
		}
		if _, ok := seen[term]; ok {
			return false
		}
		for _, existing := range hits {
			if strings.Contains(existing, term) {
				return false
			}
		}
		seen[term] = struct{}{}
		hits = append(hits, term)
		return len(hits) >= limit
	}
	segments := strings.FieldsFunc(ruleText, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	for _, segment := range segments {
		runes := []rune(segment)
		if len(runes) < 2 {
			continue
		}
		allASCII := true
		for _, r := range runes {
			if r > unicode.MaxASCII {
				allASCII = false
				break
			}
		}
		if allASCII {
			if len(runes) >= 3 && add(segment) {
				return hits
			}
			continue
		}
		maxWidth := min(6, len(runes))
		for width := maxWidth; width >= 2; width-- {
			for start := 0; start+width <= len(runes); start++ {
				if add(string(runes[start : start+width])) {
					return hits
				}
			}
		}
	}
	return hits
}

func settlePipelineRAGFacts(st *store.Store, chapter int, deliveredAt string) (bool, bool, error) {
	_ = deliveredAt
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
		state = &domain.RAGIndexState{SchemaVersion: domain.CurrentRAGIndexSchemaVersion, Config: domain.RAGIndexConfig{Collection: "local_keyword"}}
	}
	if strings.TrimSpace(state.Config.Collection) == "" {
		state.Config.Collection = "local_keyword"
	}
	chunk := rag.NormalizeChunk(chunkFromChapterSummary(*sum))
	state.Chunks = append(state.Chunks, chunk)
	state.ChunkHashes = pipelineRAGChunkHashes(state.Chunks)
	state.UpdatedAt = deliveredAt
	if err := st.RAG.SaveIndexState(*state); err != nil {
		return false, false, err
	}
	return true, true, nil
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

func pipelineArchitect(opts cliOptions, flags pipelineFlags, state *domain.PipelineState) error {
	if strings.TrimSpace(flags.ArchitectTarget) != "" && !flags.RefreshArchitect {
		return fmt.Errorf("--architect-target 必须与 --refresh-architect 同时使用")
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	if err := ensurePipelineRAGReady(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[pipeline:architect] RAG 检查失败：%v\n", err)
		return err
	}
	if err := store.NewStore(cfg.OutputDir).Init(); err != nil {
		return err
	}
	if changed, err := pipelineReconcileExplicitPromptChapterWords(
		store.NewStore(cfg.OutputDir),
		state.Prompt,
	); err != nil {
		return err
	} else if changed {
		fmt.Fprintln(os.Stderr, "[pipeline:architect] 已从创作总令恢复明确的单章字数硬区间")
	}
	if tools.FoundationCoreComplete(cfg.OutputDir) {
		if flags.RefreshArchitect {
			return pipelineRefreshArchitectOpening(opts, cfg, bundle, state.Prompt, flags.ArchitectTarget)
		}
		if pipelineArchitectCompassNeedsRepair(cfg.OutputDir) {
			return pipelineRepairArchitectCompass(opts, cfg, bundle, state.Prompt, fmt.Errorf("compass.non_negotiables 缺失，outline-all 无法映射全书硬合同"))
		}
		if pipelineStageListContains(state.Stages, "outline-all") {
			normalizedScale, err := pipelineNormalizeOutlineAllCompassScale(cfg.OutputDir)
			if err != nil {
				return err
			}
			if normalizedScale {
				fmt.Fprintln(os.Stderr, "[pipeline:architect] 已为 compass.estimated_scale 补齐 outline-all 可解析的卷/章显式区间")
			}
			normalized, err := pipelineNormalizeOutlineAllArcSpans(cfg.OutputDir)
			if err != nil {
				return err
			}
			if normalized {
				fmt.Fprintln(os.Stderr, "[pipeline:architect] 已无损重组 layered_outline 弧容器以满足 outline-all 的 8—16 章跨度；逐章内容保持不变")
			}
		}
		fmt.Fprintln(os.Stderr, "[pipeline:architect] foundation 已齐，检查 Architect readiness")
		if err := pipelineEnsureArchitectReadiness(opts, cfg.OutputDir); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "[pipeline:architect] Architect readiness 未通过，进入 foundation 修复：%v\n", err)
			return pipelineRepairArchitectReadiness(opts, cfg, bundle, state.Prompt, err)
		}
	}
	prompt, err := pipelineArchitectPrompt(cfg.OutputDir, state.Prompt)
	if err != nil {
		return err
	}
	const maxArchitectRuns = 4
	for run := 1; run <= maxArchitectRuns; run++ {
		if tools.FoundationCoreComplete(cfg.OutputDir) {
			return pipelineEnsureArchitectReadiness(opts, cfg.OutputDir)
		}
		runPrompt := ""
		if run == 1 {
			runPrompt = prompt
			fmt.Fprintln(os.Stderr, "[pipeline:architect] 启动 Architect 初始化 foundation")
		} else {
			fmt.Fprintf(os.Stderr, "[pipeline:architect] 第 %d/%d 次恢复 Architect 补齐 foundation\n", run, maxArchitectRuns)
		}
		if err := headless.Run(cfg, bundle, headless.Options{Prompt: runPrompt, StopAfterFoundation: true}); err != nil {
			return err
		}
	}
	if !tools.FoundationCoreComplete(cfg.OutputDir) {
		return fmt.Errorf("architect 阶段运行 %d 次后 foundation 仍未齐：missing=%s", maxArchitectRuns, strings.Join(tools.FoundationCoreMissing(cfg.OutputDir), ", "))
	}
	return pipelineEnsureArchitectReadiness(opts, cfg.OutputDir)
}

func pipelineReconcileExplicitPromptChapterWords(st *store.Store, prompt string) (bool, error) {
	if st == nil {
		return false, nil
	}
	rangeRule := userrules.ExplicitChapterWords(prompt)
	if rangeRule == nil {
		return false, nil
	}
	snapshot, err := st.UserRules.Load()
	if err != nil || snapshot == nil {
		return false, err
	}
	current := snapshot.Structured.ChapterWords
	if current != nil && current.Min == rangeRule.Min && current.Max == rangeRule.Max {
		return false, nil
	}
	snapshot.Structured.ChapterWords = rangeRule
	snapshot.Sources = appendUniqueProjectAllString(snapshot.Sources, "pipeline_prompt_explicit")
	filtered := snapshot.Uncertain[:0]
	for _, item := range snapshot.Uncertain {
		lower := strings.ToLower(item)
		if strings.Contains(lower, "chapter_words") ||
			strings.Contains(item, "单章") ||
			strings.Contains(item, "每章") ||
			strings.Contains(item, "章节字数") {
			continue
		}
		filtered = append(filtered, item)
	}
	snapshot.Uncertain = filtered
	if err := st.UserRules.Save(snapshot); err != nil {
		return false, fmt.Errorf("保存创作总令的明确单章字数区间: %w", err)
	}
	return true, nil
}

func pipelineStageListContains(stages []string, target string) bool {
	for _, stage := range stages {
		if stage == target {
			return true
		}
	}
	return false
}

func pipelineNormalizeOutlineAllCompassScale(outputDir string) (bool, error) {
	st := store.NewStore(outputDir)
	compass, err := st.Outline.LoadCompass()
	if err != nil || compass == nil {
		return false, err
	}
	if _, err := domain.ParseBookScaleRange(compass.EstimatedScale); err == nil {
		return false, nil
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, err
	}
	volumeCount := domain.RealVolumeCount(volumes)
	chapterCount := domain.TotalChapters(volumes)
	if volumeCount <= 0 || chapterCount <= 0 {
		return false, fmt.Errorf("outline-all 无法从 layered_outline 推导 compass 规模：volumes=%d chapters=%d", volumeCount, chapterCount)
	}
	existing := strings.TrimSpace(compass.EstimatedScale)
	compass.EstimatedScale = fmt.Sprintf("%d-%d卷，%d-%d章", volumeCount, volumeCount, chapterCount, chapterCount)
	if existing != "" {
		compass.EstimatedScale += "；" + existing
	}
	if _, err := domain.ParseBookScaleRange(compass.EstimatedScale); err != nil {
		return false, fmt.Errorf("outline-all compass 规模规范化失败: %w", err)
	}
	if err := st.Outline.SaveCompass(*compass); err != nil {
		return false, fmt.Errorf("保存 outline-all 规范化 compass: %w", err)
	}
	return true, nil
}

// pipelineNormalizeOutlineAllArcSpans is a structural migration, not a story
// rewrite. Architect occasionally returns a complete short-fiction outline as
// three four-chapter dramatic acts, while outline-all's bounded mutation
// protocol requires every arc container to span 8-16 chapters. When every arc
// is already expanded, the host can repartition the exact ordered chapter
// entries without asking a model to regenerate titles, events, hooks or scenes.
func pipelineNormalizeOutlineAllArcSpans(outputDir string) (bool, error) {
	st := store.NewStore(outputDir)
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, err
	}
	if len(domain.OutlineAllArcSpanIssues(volumes)) == 0 {
		return false, nil
	}

	changed := false
	for volumeIndex := range volumes {
		volume := &volumes[volumeIndex]
		volumeSpan := 0
		needsRepair := false
		for _, arc := range volume.Arcs {
			span := arc.ChapterSpan()
			volumeSpan += span
			if span < domain.OutlineAllMinArcChapters || span > domain.OutlineAllMaxArcChapters {
				needsRepair = true
			}
		}
		if !needsRepair {
			continue
		}

		chapters := make([]domain.OutlineEntry, 0, volumeSpan)
		titles := make([]string, 0, len(volume.Arcs))
		goals := make([]string, 0, len(volume.Arcs))
		refs := make([]domain.StoryContractRef, 0)
		for _, arc := range volume.Arcs {
			if !arc.IsExpanded() {
				return false, fmt.Errorf(
					"outline-all 弧跨度不合格且包含未展开骨架 V%dA%d；需由 Architect 按 8—16 章重新预留",
					volume.Index, arc.Index,
				)
			}
			chapters = append(chapters, arc.Chapters...)
			if title := strings.TrimSpace(arc.Title); title != "" {
				titles = append(titles, title)
			}
			if goal := strings.TrimSpace(arc.Goal); goal != "" {
				goals = append(goals, goal)
			}
			refs = append(refs, arc.ContractRefs...)
		}
		spans, err := domain.RecommendedOutlineAllArcSpans(len(chapters))
		if err != nil {
			return false, fmt.Errorf("outline-all 无法无损重组第 %d 卷的 %d 章：%w", volume.Index, len(chapters), err)
		}
		if len(refs) > 0 {
			return false, fmt.Errorf("outline-all 拒绝自动重组带既有 contract_refs 的第 %d 卷；需显式 rebase 后重规划", volume.Index)
		}

		newArcs := make([]domain.ArcOutline, 0, len(spans))
		chapterOffset := 0
		for arcIndex, span := range spans {
			end := chapterOffset + span
			arcTitle := strings.TrimSpace(volume.Title)
			if len(spans) > 1 {
				arcTitle = fmt.Sprintf("%s（第%d段）", firstNonEmptyString(arcTitle, strings.Join(titles, " / ")), arcIndex+1)
			} else if arcTitle == "" {
				arcTitle = strings.Join(titles, " / ")
			}
			newArcs = append(newArcs, domain.ArcOutline{
				Index:    arcIndex + 1,
				Title:    arcTitle,
				Goal:     strings.Join(goals, "；"),
				Chapters: append([]domain.OutlineEntry(nil), chapters[chapterOffset:end]...),
			})
			chapterOffset = end
		}
		volume.Arcs = newArcs
		changed = true
	}
	if !changed {
		return false, nil
	}
	if issues := domain.OutlineAllArcSpanIssues(volumes); len(issues) > 0 {
		return false, fmt.Errorf("outline-all 弧跨度无损重组后仍不合格：%s", summarizeOutlineContractIssues(issues, 12))
	}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		return false, fmt.Errorf("保存 outline-all 弧跨度迁移后的 layered_outline: %w", err)
	}
	if err := st.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		return false, fmt.Errorf("保存 outline-all 弧跨度迁移后的 flat outline: %w", err)
	}
	return true, nil
}

func pipelineRefreshArchitectOpening(opts cliOptions, cfg bootstrap.Config, bundle assets.Bundle, prompt, requestedTarget string) error {
	refreshPrompt, err := pipelineArchitectRefreshPrompt(cfg.OutputDir, prompt)
	if err != nil {
		return err
	}
	before, err := pipelineOpeningFoundationDigest(cfg.OutputDir)
	if err != nil {
		return err
	}
	shortChapterZero, _ := pipelineArchitectShortChapterZero(cfg.OutputDir)
	if shortChapterZero {
		targets, err := pipelineArchitectShortSelectedTargets(requestedTarget)
		if err != nil {
			return err
		}
		for _, target := range targets {
			const maxTargetAttempts = 3
			updated := false
			for attempt := 1; attempt <= maxTargetAttempts; attempt++ {
				beforeTarget, err := pipelineArchitectShortTargetRevision(cfg.OutputDir, target)
				if err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "[pipeline:architect] 刷新 foundation %s（尝试 %d/%d）\n", target.Type, attempt, maxTargetAttempts)
				if err := headless.Run(cfg, bundle, headless.Options{
					Prompt:                    pipelineArchitectShortRefreshTargetPrompt(refreshPrompt, target),
					PreserveUserRules:         true,
					StopAfterFoundationChange: true,
				}); err != nil {
					return err
				}
				afterTarget, err := pipelineArchitectShortTargetRevision(cfg.OutputDir, target)
				if err != nil {
					return err
				}
				if afterTarget != beforeTarget {
					updated = true
					break
				}
				fmt.Fprintf(os.Stderr, "[pipeline:architect] %s 本轮未发生目标落盘，保持同一目标重试\n", target.Type)
			}
			if !updated {
				return fmt.Errorf("Architect 连续 %d 次未真正保存指定 foundation %s；拒绝把路由结束误判为刷新成功", maxTargetAttempts, target.Type)
			}
		}
		after, err := pipelineOpeningFoundationDigest(cfg.OutputDir)
		if err != nil {
			return err
		}
		if strings.TrimSpace(requestedTarget) == "" && after == before {
			return fmt.Errorf("Architect 已逐项落盘但开篇大纲指纹仍未变化")
		}
		return pipelineEnsureArchitectReadiness(opts, cfg.OutputDir)
	}
	if strings.TrimSpace(requestedTarget) != "" {
		return fmt.Errorf("--architect-target 只适用于第0章、3万字内的短篇 foundation 刷新")
	}
	const maxRefreshRuns = 3
	for run := 1; run <= maxRefreshRuns; run++ {
		fmt.Fprintf(os.Stderr, "[pipeline:architect] 第 %d/%d 次刷新 foundation\n", run, maxRefreshRuns)
		if err := headless.Run(cfg, bundle, headless.Options{
			Prompt:                    refreshPrompt,
			PreserveUserRules:         true,
			StopAfterFoundationChange: true,
		}); err != nil {
			return err
		}
		after, err := pipelineOpeningFoundationDigest(cfg.OutputDir)
		if err != nil {
			return err
		}
		if after != before {
			return pipelineEnsureArchitectReadiness(opts, cfg.OutputDir)
		}
	}
	return fmt.Errorf("Architect 刷新 %d 次后开篇大纲指纹仍未变化", maxRefreshRuns)
}

type pipelineArchitectShortRefreshTarget struct {
	Type        string
	Description string
	Artifacts   []string
}

var pipelineArchitectShortRefreshTargets = []pipelineArchitectShortRefreshTarget{
	{Type: "premise", Description: "恢复一句话故事、旧案统一引擎、双女主关系、两次反转与终局方向", Artifacts: []string{"premise.md"}},
	{Type: "characters", Description: "修复全部人物身份、关系、生死权限、欲望与当前行动边界", Artifacts: []string{"characters.json"}},
	{Type: "world_rules", Description: "修复数据灰产旧案、两次指定反转、证据流转与现实处置硬规则", Artifacts: []string{"world_rules.json"}},
	{Type: "book_world", Description: "修复组织、空间、经济利益与女性地址/路线数据灰产的同一世界引擎", Artifacts: []string{"book_world.json"}},
	{Type: "world_codex", Description: "修复旧案机制、资料来源与现实制度细节；调用时必须带 change_reason=用户创作总令纠偏、change_evidence=本轮 prompt 的具体硬合同", Artifacts: []string{"world_codex.json"}},
	{Type: "update_compass", Description: "修复 open_threads、non_negotiables、ending_direction 与明确规模", Artifacts: []string{"meta/compass.json"}},
	{Type: "layered_outline", Description: "最后重做完整短篇章纲；一卷一弧覆盖全书并同步 flat outline", Artifacts: []string{"layered_outline.json", "outline.json"}},
}

func pipelineArchitectShortRefreshRunPrompt(base string, run int) string {
	index := run - 1
	if index < 0 {
		index = 0
	}
	if index >= len(pipelineArchitectShortRefreshTargets) {
		index = len(pipelineArchitectShortRefreshTargets) - 1
	}
	return pipelineArchitectShortRefreshTargetPrompt(base, pipelineArchitectShortRefreshTargets[index])
}

func pipelineArchitectShortRefreshTargetPrompt(base string, target pipelineArchitectShortRefreshTarget) string {
	return fmt.Sprintf(
		"[宿主强制路由：Coordinator 必须逐字服从]\n当前顶层角色是 Coordinator。Coordinator 本轮唯一合法动作是立即调用 subagent(agent=\"architect_long\", task=<本提示中从“Architect 执行任务”开始的全部要求>)。Coordinator 自己禁止调用 novel_context 或任何 foundation/章节工具，禁止服从 flow router 的 writer 指令，禁止派 writer/drafter/editor，也禁止先输出分析；必须把本任务交给 architect_long。\n\n[Architect 执行任务]\n本回合只允许调用一次 save_foundation(type=%q)，任务：%s。禁止保存或重存任何其他 foundation 类型；即使其他项仍有问题也留给后续宿主回合。由 architect_long 先且只调用一次 novel_context(chapter=1, profile=planning) 获取紧凑上下文；若读取失败，直接依据下方创作总令完成本类型，不得改用无 chapter 的完整上下文。保存后立即停止，严禁派 writer。\n\n%s\n\n[再次确认本轮边界]\nCoordinator 只能派 architect_long；architect_long 唯一允许的持久化调用是 save_foundation(type=%q)。不得调用 premise/characters/world_rules/book_world/world_codex/update_compass/layered_outline 中的其他类型，保存后立即结束。\n",
		target.Type,
		target.Description,
		base,
		target.Type,
	)
}

func pipelineArchitectShortSelectedTargets(requested string) ([]pipelineArchitectShortRefreshTarget, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return append([]pipelineArchitectShortRefreshTarget(nil), pipelineArchitectShortRefreshTargets...), nil
	}
	for _, target := range pipelineArchitectShortRefreshTargets {
		if target.Type == requested {
			return []pipelineArchitectShortRefreshTarget{target}, nil
		}
	}
	return nil, fmt.Errorf("未知 --architect-target %q；可选：premise, characters, world_rules, book_world, world_codex, update_compass, layered_outline", requested)
}

func pipelineArchitectShortTargetRevision(outputDir string, target pipelineArchitectShortRefreshTarget) (string, error) {
	h := sha256.New()
	for _, rel := range target.Artifacts {
		path := filepath.Join(outputDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				_, _ = fmt.Fprintf(h, "%s\x00missing\x00", rel)
				continue
			}
			return "", fmt.Errorf("读取 Architect 目标产物 %s 失败: %w", rel, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("读取 Architect 目标产物 %s 失败: %w", rel, err)
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\x00", rel, info.ModTime().UnixNano(), info.Size())
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pipelineOpeningFoundationDigest(outputDir string) (string, error) {
	h := sha256.New()
	for _, rel := range []string{"outline.json", "layered_outline.json"} {
		raw, err := os.ReadFile(filepath.Join(outputDir, rel))
		if err != nil {
			return "", fmt.Errorf("读取 Architect 开篇产物 %s 失败: %w", rel, err)
		}
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func pipelineRepairArchitectReadiness(opts cliOptions, cfg bootstrap.Config, bundle assets.Bundle, prompt string, cause error) error {
	if pipelineArchitectCompassNeedsRepair(cfg.OutputDir) {
		return pipelineRepairArchitectCompass(opts, cfg, bundle, prompt, cause)
	}
	if repaired, err := pipelineAutoRepairBookWorldStructure(cfg.OutputDir); err != nil {
		return err
	} else if repaired {
		fmt.Fprintln(os.Stderr, "[pipeline:architect] 已自动修复 book_world 悬空关系/别名，重新检查 readiness")
		if err := pipelineEnsureArchitectReadiness(opts, cfg.OutputDir); err == nil {
			return nil
		} else {
			cause = err
		}
	}
	const maxRepairRuns = 3
	for run := 1; run <= maxRepairRuns; run++ {
		repairPrompt, err := pipelineArchitectRepairPrompt(cfg.OutputDir, prompt, cause)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "[pipeline:architect] 第 %d/%d 次 Architect readiness 修复\n", run, maxRepairRuns)
		if err := headless.Run(cfg, bundle, headless.Options{
			Prompt:            repairPrompt,
			PreserveUserRules: true,
		}); err != nil {
			return err
		}
		if err := pipelineEnsureArchitectReadiness(opts, cfg.OutputDir); err == nil {
			return nil
		} else {
			cause = err
		}
	}
	return fmt.Errorf("Architect readiness 修复 %d 次后仍未通过：%w", maxRepairRuns, cause)
}

func pipelineArchitectCompassNeedsRepair(outputDir string) bool {
	compass, err := store.NewStore(outputDir).Outline.LoadCompass()
	return err != nil || compass == nil || strings.TrimSpace(compass.EndingDirection) == "" || len(compass.OpenThreads) == 0 || len(compass.NonNegotiables) == 0
}

func pipelineRepairArchitectCompass(opts cliOptions, cfg bootstrap.Config, bundle assets.Bundle, prompt string, cause error) error {
	compassPath := filepath.Join(cfg.OutputDir, "meta", "compass.json")
	current, err := os.ReadFile(compassPath)
	if err != nil {
		return fmt.Errorf("读取 compass 失败，无法执行受限迁移: %w", err)
	}
	var b strings.Builder
	b.WriteString("[Pipeline Architect compass 受限迁移]\n")
	b.WriteString("当前 foundation 除 compass.non_negotiables 外均已完成。本轮只允许派 architect_long，最多读取一次 novel_context，然后只调用一次 save_foundation(type=\"update_compass\", scale=\"short\", content=<完整 compass JSON>)。严禁保存 premise、characters、world_rules、book_world、world_codex 或 outline，严禁写正文。\n")
	b.WriteString("完整 compass 必须保留当前 ending_direction、open_threads、estimated_scale，并新增 non_negotiables 数组。数组写 6—10 条从本项目创作总令与当前 foundation 逐项提取的具体硬合同，每条都必须能被 outline-all 映射到明确章位或兑现事件；至少覆盖用户明确指定的篇幅/章数、主角身份与能力边界、核心关系、故事发动机、关键时限或数字机关、反转与证据公平性、现实处置边界以及结局承诺。没有出现在本项目输入中的题材、角色、机制或结局不得补入；不得只写抽象主题词。保存后立即停止。\n")
	if cause != nil {
		b.WriteString("\n[当前 readiness 错误]\n" + cause.Error() + "\n")
	}
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n[创作总令，仅用于提取硬合同]\n" + prompt + "\n")
	}
	b.WriteString("\n[当前 compass.json]\n```json\n" + string(current) + "\n```\n")
	const maxRuns = 2
	for run := 1; run <= maxRuns; run++ {
		fmt.Fprintf(os.Stderr, "[pipeline:architect] 第 %d/%d 次 compass 受限迁移\n", run, maxRuns)
		if err := headless.Run(cfg, bundle, headless.Options{
			Prompt:                    b.String(),
			PreserveUserRules:         true,
			StopAfterFoundationChange: true,
		}); err != nil {
			return err
		}
		if pipelineArchitectCompassNeedsRepair(cfg.OutputDir) {
			cause = fmt.Errorf("compass.non_negotiables 迁移后仍为空")
			continue
		}
		if err := pipelineEnsureArchitectReadiness(opts, cfg.OutputDir); err == nil {
			return nil
		} else {
			cause = err
		}
	}
	return fmt.Errorf("Architect compass 迁移 %d 次后仍未通过：%w", maxRuns, cause)
}

func pipelineAutoRepairBookWorldStructure(outputDir string) (bool, error) {
	st := store.NewStore(outputDir)
	world, err := st.World.LoadBookWorld()
	if err != nil || world == nil {
		return false, err
	}
	changed := false
	known := pipelineBookWorldFactionNames(*world)
	for _, faction := range world.Factions {
		source := strings.TrimSpace(firstNonEmptyString(faction.ID, faction.Name))
		for _, rel := range faction.Relations {
			target := strings.TrimSpace(rel.Target)
			if target == "" {
				continue
			}
			if _, ok := known[target]; ok {
				continue
			}
			added := pipelineDefaultFactionForDanglingRelation(target, source, rel)
			world.Factions = append(world.Factions, added)
			for _, name := range pipelineFactionNames(added) {
				if strings.TrimSpace(name) != "" {
					known[strings.TrimSpace(name)] = struct{}{}
				}
			}
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := st.World.SaveBookWorld(*world); err != nil {
		return false, fmt.Errorf("自动修复 book_world 失败: %w", err)
	}
	return true, nil
}

func pipelineBookWorldFactionNames(world domain.BookWorld) map[string]struct{} {
	known := map[string]struct{}{}
	for _, faction := range world.Factions {
		for _, name := range pipelineFactionNames(faction) {
			name = strings.TrimSpace(name)
			if name != "" {
				known[name] = struct{}{}
			}
		}
	}
	return known
}

func pipelineFactionNames(faction domain.WorldFaction) []string {
	names := []string{faction.ID, faction.Name}
	names = append(names, faction.Aliases...)
	return names
}

func pipelineDefaultFactionForDanglingRelation(target, source string, rel domain.FactionRelation) domain.WorldFaction {
	name := pipelineDisplayNameForFactionID(target)
	note := strings.TrimSpace(rel.Note)
	goal := fmt.Sprintf("承接 %s 的 %s 关系，补齐 Architect 势力图谱中的结构缺口。", source, firstNonEmptyString(rel.Kind, "关联"))
	if note != "" {
		goal = fmt.Sprintf("承接 %s：%s", firstNonEmptyString(source, "既有势力"), note)
	}
	return domain.WorldFaction{
		ID:        target,
		Name:      name,
		Goal:      goal,
		Resources: []string{"现场反馈", "执行压力", "一线信息"},
		Relations: []domain.FactionRelation{{
			Target:        source,
			Kind:          firstNonEmptyString(rel.Kind, "linked"),
			Note:          "pipeline 自动补齐悬空 relation.target 后生成的反向关系；后续 Architect 可细化。",
			ConflictType:  firstNonEmptyString(rel.ConflictType, "资源"),
			ConflictState: firstNonEmptyString(rel.ConflictState, "truce"),
		}},
		Tags: []string{"pipeline_auto_repair"},
		Clock: &domain.FactionClock{
			Segments:    6,
			Progress:    0,
			Consequence: fmt.Sprintf("%s 的压力必须转化为可见的现场事件或反馈。", name),
			Pace:        "每弧 1 段；被主线直接触发时 2 段",
		},
	}
}

func pipelineDisplayNameForFactionID(id string) string {
	return strings.ReplaceAll(strings.TrimSpace(id), "_", " ")
}

func pipelineArchitectPrompt(outputDir, prompt string) (string, error) {
	brainstormPath := filepath.Join(ragProjectRoot(outputDir), "brainstorm.md")
	brainstorm := ""
	if data, err := os.ReadFile(brainstormPath); err == nil {
		brainstorm = strings.TrimSpace(string(data))
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("读取 brainstorm.md 失败: %w", err)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" && brainstorm == "" {
		return "", fmt.Errorf("architect 阶段需要创作指令：用 --prompt/--prompt-file，或提供项目根 brainstorm.md")
	}

	var b strings.Builder
	b.WriteString("[Pipeline Architect 阶段]\n")
	b.WriteString("本阶段只允许完成 Architect foundation，不允许进入正文写作。\n")
	b.WriteString("必须派 architect_long/architect_short，并通过 save_foundation 落盘完整核心设定：premise、characters、world_rules、book_world、world_codex、compass，以及 layered_outline 或 outline。\n")
	b.WriteString("这是可断点续跑阶段：Architect 首先读取一次 novel_context，已经存在且有效的 foundation 类型视为完成，禁止重新生成或覆盖；只保存缺失、过期或 readiness 明确判错的类型。每次恢复必须优先新增至少一个缺失类型，不能从 premise 开始重放。单项保持完成校验所需的最小充分篇幅，避免在 15 分钟子 Agent 硬时限内反复输出超长 JSON。\n")
	existing, missing := pipelineArchitectFoundationPresence(outputDir)
	b.WriteString("[本次断点资产清单]\n")
	b.WriteString("已存在且本轮禁止重存：" + strings.Join(existing, "、") + "。\n")
	b.WriteString("本轮只允许保存的缺失类型：" + strings.Join(missing, "、") + "。若缺失列表为空，直接结束并交给宿主 readiness，不得重放任何资产。\n")
	b.WriteString("保存 compass（save_foundation type=update_compass）时必须同时包含 ending_direction、open_threads、estimated_scale 和非空 non_negotiables；estimated_scale 必须包含机器可读的显式范围，例如固定单卷12章也要写成“1-1卷，12-12章”，不能只写“单卷12章”；non_negotiables 应是 6—10 条可由 outline-all 逐章映射、不得推迟或删除的本书具体硬合同，禁止只写抽象主题。\n")
	b.WriteString("下游 outline-all 要求 layered_outline 的每个弧占 8—16 章。若全书是 8—16 章短篇，必须保存为一卷一弧并让该弧覆盖全书（例如 12 章短篇只能是一卷一弧 12 章），不得拆成三个 4 章弧；起承转合仍写进逐章事件，不靠短弧分组表达。\n")
	b.WriteString("book_world 的形状固定：protagonist_position 必须是一句话字符串；vision_pillars 必须是对象 {color_palette:[], signature_elements:[], lighting:\"\", signature_scenes:[]}；world_pillars 必须是对象 {economic:{base,controlled_by,tension}, cultural:{...}, political:{...}, historical:{...}}，不得把两个 pillars 写成数组。\n")
	b.WriteString("完成 foundation 后立即停止，宿主会在下一阶段执行 zero-init；严禁派 writer/drafter/editor，严禁 plan_chapter、draft_chapter、commit_chapter。\n")
	b.WriteString("请特别落实用户硬规则：复杂项目按现实时间尺度合理压缩，不得把复杂工程写成和小项目同一时间节奏。\n")
	if prompt != "" {
		b.WriteString("\n[创作指令]\n")
		b.WriteString(prompt)
		b.WriteString("\n")
	}
	if brainstorm != "" {
		b.WriteString("\n[brainstorm.md]\n")
		b.WriteString(brainstorm)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func pipelineArchitectFoundationPresence(outputDir string) (existing, missing []string) {
	type asset struct {
		name  string
		paths []string
	}
	assets := []asset{
		{name: "premise", paths: []string{filepath.Join(outputDir, "premise.md")}},
		{name: "characters", paths: []string{filepath.Join(outputDir, "characters.json")}},
		{name: "world_rules", paths: []string{filepath.Join(outputDir, "world_rules.json")}},
		{name: "book_world", paths: []string{filepath.Join(outputDir, "book_world.json")}},
		{name: "world_codex", paths: []string{filepath.Join(outputDir, "world_codex.json")}},
		{name: "compass", paths: []string{filepath.Join(outputDir, "meta", "compass.json")}},
		{name: "layered_outline 或 outline", paths: []string{
			filepath.Join(outputDir, "layered_outline.json"),
			filepath.Join(outputDir, "outline.json"),
		}},
	}
	for _, item := range assets {
		present := false
		for _, path := range item.paths {
			if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
				present = true
				break
			}
		}
		if present {
			existing = append(existing, item.name)
		} else {
			missing = append(missing, item.name)
		}
	}
	if len(existing) == 0 {
		existing = []string{"无"}
	}
	if len(missing) == 0 {
		missing = []string{"无"}
	}
	return existing, missing
}

func pipelineArchitectRefreshPrompt(outputDir, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("--refresh-architect 需要 --prompt/--prompt-file 说明本次重规划目标")
	}
	shortChapterZero, totalChapters := pipelineArchitectShortChapterZero(outputDir)
	var b strings.Builder
	if shortChapterZero {
		fmt.Fprintf(&b, "[Pipeline Architect 章零短篇全书刷新阶段]\n这是尚未写正文的%d章短篇全书重规划，只允许 Architect 工作，不进入 zero-init、章节计划或正文。必须派 architect_long，先读取 novel_context 和当前 foundation。\n", totalChapters)
		b.WriteString("按以下顺序逐项检查并修复；每轮只调用一次 save_foundation 保存最靠前的未完成项，随后立即停止，宿主会以新回合继续：\n")
		b.WriteString("1. premise：恢复本项目创作总令的一句话故事、题材引擎与主角关系，不得用相邻但不同的故事替换。\n")
		b.WriteString("2. characters：逐人恢复创作总令明确的身份、能力、关系、知识边界与生死状态；角色只能执行其时点、权限和可见证据允许的行动。\n")
		b.WriteString("3. world_rules：纠正本项目的因果硬规则、反转公平性、信息权限、证据或资源来源及现实处置边界；未被创作总令要求的机制不得新增。\n")
		b.WriteString("4. book_world 与 world_codex：只在与创作总令冲突时依次修复，使组织、空间、历史事件和资源/材料流转支持同一个故事，不保留题材漂移。\n")
		b.WriteString("5. compass：同步纠正 open_threads/non_negotiables/ending_direction，使每条用户硬合同都有唯一章位且不被相邻合同冒名替代。\n")
		fmt.Fprintf(&b, "6. layered_outline/outline：最后重做完整%d章的结果级结构，不只改黄金三章；一次保存 layered_outline 并由工具同步 flat outline。必须逐章消除重复、错章、提前兑现和缺失的字面机关。\n", totalChapters)
		b.WriteString("每一项已经逐字服从本轮创作总令时才可跳过；不得因为文件存在就视为完成。保留创作总令明确给出的书名、主角姓名与职业、人物关系、时限/数字机关、题材边界和结局承诺；不得从示例、旧项目或模型偏好补入其他故事的事实。完成上述全部项目后停止。严禁 plan_chapter、draft_chapter、commit_chapter，严禁派 writer/drafter/editor。\n\n")
		b.WriteString("[本次用户与市场校准要求]\n")
		b.WriteString(prompt)
		b.WriteString("\n")
		return b.String(), nil
	}
	b.WriteString("[Pipeline Architect 开篇刷新阶段]\n")
	b.WriteString("这是已有长篇项目的开篇重规划，只允许 Architect 工作，不进入 zero-init、章节计划或正文。必须派 architect_long，先读取 novel_context 和当前 foundation，再通过 save_foundation 同步更新 layered_outline 与 outline。\n")
	b.WriteString("只重做前三章的结果级结构和必要标题，保留书名、总题材、人物关系、能力与秘密边界、长期卷弧和已经确认的世界设定；不得借机重写整本书。\n")
	b.WriteString("前三章必须形成移动阅读留存闭环：第一章首屏冲突、核心能力或故事发动机出现且本章首次兑现；第二章承接新债、核心关系角色同场行动或产生可见影响、限制升级并有小胜；第三章完成首个目标结算、让结果被故事世界中的普通人感知，再打开更大目标。禁止三章连续解释规则、列检查表或只做准备。\n")
	b.WriteString("若当前项目已有系统/能力界面，按既定人格和格式设计一问一答，每条【系统消息】独立成段；没有此类设定时严禁凭空新增。前三章的情绪、笑点或压力来源只服从当前 premise、user_rules 与人物关系，专业/经营信息只保留目标读者看得懂的现场后果。\n")
	b.WriteString("从当前 foundation、outline、已提交正文与用户本轮要求提取前三章不可漂移的角色、金额、时点、知识边界、因果结果和章末后果；允许压缩、并场和换序，但不得把任何一本示例书的人名、地点、职业、系统规则或项目流程注入当前项目。\n")
	b.WriteString("保存后立即停止；严禁 plan_chapter、draft_chapter、commit_chapter，严禁派 writer/drafter/editor。\n\n")
	b.WriteString("[本次用户与市场校准要求]\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	return b.String(), nil
}

func pipelineArchitectShortChapterZero(outputDir string) (bool, int) {
	progress, err := store.NewStore(outputDir).Progress.Load()
	if err != nil || progress == nil || progress.TotalChapters <= 0 || progress.TotalChapters > 16 {
		return false, 0
	}
	return progress.LatestCompleted() == 0 && len(progress.PendingRewrites) == 0,
		progress.TotalChapters
}

func pipelineArchitectRepairPrompt(outputDir, prompt string, cause error) (string, error) {
	_ = prompt
	readinessPath := filepath.Join(outputDir, "meta", "architect_readiness.md")
	readiness := ""
	if data, err := os.ReadFile(readinessPath); err == nil {
		readiness = strings.TrimSpace(string(data))
	}
	bookWorldPath := filepath.Join(outputDir, "book_world.json")
	bookWorld := ""
	if data, err := os.ReadFile(bookWorldPath); err == nil {
		bookWorld = strings.TrimSpace(string(data))
	} else {
		return "", fmt.Errorf("读取 book_world.json 失败，无法执行 Architect readiness 修复: %w", err)
	}
	var b strings.Builder
	b.WriteString("[Pipeline Architect readiness 修复阶段]\n")
	b.WriteString("当前 foundation 文件已经齐全，但 Architect readiness 未通过。本轮只修复 book_world 结构，不重新设计 premise/characters/world_rules/outline/compass，不进入 zero-init，不写章节，不调用 writer/drafter/editor。\n")
	b.WriteString("必须派 architect_long，并要求它最多读一次 novel_context；随后只调用一次 save_foundation(type=\"book_world\", scale=\"long\", content=<完整修复后的 book_world JSON>) 落盘。\n")
	b.WriteString("修复规则：\n")
	b.WriteString("1. book_world.factions 的每个 relation.target 必须指向已存在 faction 的 id/name/aliases；不得悬空。\n")
	b.WriteString("2. 每个 faction 必须保留或补齐 clock（segments/progress/consequence/pace）。新增 faction 时必须给 clock。\n")
	b.WriteString("3. 从当前 book_world.factions 的 id/name/aliases 与当前 outline 实际使用的称呼逐项核对：outline 中已出现的组织简称、系统名、群聊名或空间简称必须补进对应 faction.aliases；不得添加当前资产里没有的示例专名。\n")
	b.WriteString("4. 保留当前 book_world 的事实、名称、目标、地点、路线和既有进度钟，只做必要结构修复；如果 relation.target 指向缺失势力，优先按该 target 的原值新增势力，而不是删除关系。\n")
	b.WriteString("5. 保存后不得继续生成正文或 zero-init；等待宿主做 architect-check。\n")
	if cause != nil {
		b.WriteString("\n[当前 readiness 错误]\n")
		b.WriteString(cause.Error())
		b.WriteString("\n")
	}
	if readiness != "" {
		b.WriteString("\n[meta/architect_readiness.md]\n")
		b.WriteString(readiness)
		b.WriteString("\n")
	}
	b.WriteString("\n[当前 book_world.json]\n```json\n")
	b.WriteString(bookWorld)
	b.WriteString("\n```\n")
	return b.String(), nil
}

func pipelineZeroInit(opts cliOptions, flags pipelineFlags, state *domain.PipelineState) (returnErr error) {
	_ = state
	liveDir, releaseControl, err := acquirePublishedOutlineAllStageForInvocation(opts)
	if err != nil {
		return fmt.Errorf("zero-init requires published outline-all: %w", err)
	}
	defer releasePublishedOutlineAllStage(releaseControl, "pipeline zero-init", &returnErr)
	if liveDir != "" {
		if err := requirePublishedOutlineAllChapterZeroProgressWithControlHeld(liveDir); err != nil {
			return fmt.Errorf("zero-init requires chapter-zero published outline-all progress: %w", err)
		}
	}
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	st := store.NewStore(cfg.OutputDir)
	if !tools.ChapterOnePendingFirstWrite(st) {
		if flags.RefreshZeroInit {
			fmt.Fprintln(os.Stderr, "[pipeline:zero-init] 已有正文，安全刷新开篇 zero-init 计划")
			if err := zeroInitPipeline(opts, []string{"--dir", cfg.OutputDir, "--refresh-opening-plan"}); err != nil {
				return fmt.Errorf("zero-init 开篇计划刷新失败: %w", err)
			}
			return nil
		}
		fmt.Fprintln(os.Stderr, "[pipeline:zero-init] 第 1 章已完成，跳过 zero-init")
		return nil
	}
	if missing := tools.FoundationCoreMissing(cfg.OutputDir); len(missing) > 0 {
		return fmt.Errorf("zero-init 阶段必须在 Architect foundation 齐备后执行：missing=%s", strings.Join(missing, ", "))
	}
	if ok, reason := architectReadinessState(cfg.OutputDir); !ok {
		return fmt.Errorf("zero-init 阶段必须在 Architect readiness 通过后执行：%s", reason)
	}
	if ok, _ := pipelineCurrentZeroInitReadinessState(cfg.OutputDir); ok {
		fmt.Fprintln(os.Stderr, "[pipeline:zero-init] readiness 已就绪，跳过 zero-init")
		return pipelineEnsureInitialWorldTick(cfg, bundle)
	}
	_, reason := pipelineCurrentZeroInitReadinessState(cfg.OutputDir)
	fmt.Fprintf(os.Stderr, "[pipeline:zero-init] 执行 zero-init：%s\n", reason)
	if err := zeroInitPipeline(opts, pipelineZeroInitRegenerationArgs(cfg.OutputDir)); err != nil {
		return fmt.Errorf("zero-init 阶段失败: %w", err)
	}
	if ok, why := pipelineCurrentZeroInitReadinessState(cfg.OutputDir); !ok {
		return fmt.Errorf("zero-init 后 readiness 仍未就绪：%s", why)
	}
	return pipelineEnsureInitialWorldTick(cfg, bundle)
}

// pipelineCurrentZeroInitReadinessState combines the durable readiness
// receipt/foundation freshness guard with the current generator's semantic
// coverage contract. The latter matters when a new binary expands
// zeroInitialCharacters (for example, a secondary actor reserved by a later
// outline): an older ready:true receipt cannot prove that the existing
// initial_character_dynamics.json contains the newly required actor.
//
// Callers must first establish ChapterOnePendingFirstWrite. Published projects
// intentionally keep their historical zero-init evidence and are never
// silently rewritten through this check.
func pipelineCurrentZeroInitReadinessState(outputDir string) (bool, string) {
	if ok, reason := tools.ZeroInitReadinessState(outputDir); !ok {
		return false, reason
	}
	current := assessZeroInitReadiness(outputDir, zeroInitRAGStats{})
	if current.Ready {
		return true, ""
	}
	return false, fmt.Sprintf(
		"当前 zero-init 语义复核未通过（missing=%d issues=%d）：%s",
		len(current.Missing),
		len(current.Issues),
		strings.Join(current.Issues, "；"),
	)
}

func pipelineZeroInitRegenerationArgs(outputDir string) []string {
	return []string{
		"--dir", outputDir,
		"--reset-simulation-state",
		"--overwrite",
	}
}

func pipelineEnsureArchitectReadiness(opts cliOptions, outputDir string) error {
	if missing := tools.FoundationCoreMissing(outputDir); len(missing) > 0 {
		return fmt.Errorf("Architect readiness 需要先补齐 foundation：missing=%s", strings.Join(missing, ", "))
	}
	if ok, _ := architectReadinessState(outputDir); ok {
		fmt.Fprintln(os.Stderr, "[pipeline:architect] Architect readiness 已通过")
		return nil
	}
	if err := architectCheckPipeline(opts, []string{"--dir", outputDir}); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "[pipeline:architect] Architect readiness 已落盘")
	return nil
}

func pipelineEnsureInitialWorldTick(cfg bootstrap.Config, bundle assets.Bundle) (returnErr error) {
	st := store.NewStore(cfg.OutputDir)
	if err := tools.EnsureInitialWorldTickForChapterOne(st); err == nil {
		fmt.Fprintln(os.Stderr, "[pipeline:zero-init] 初始 world_tick 已就绪")
		return nil
	}
	userRulesDigest, err := pipelineInitialWorldTickUserRulesDigest(cfg.OutputDir)
	if err != nil {
		return err
	}
	restoreFoundationLock, err := pipelineAcquireInitialWorldTickExecution(st)
	if err != nil {
		return err
	}
	defer func() {
		if err := restoreFoundationLock(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	defer func() {
		if err := pipelineVerifyInitialWorldTickUserRulesDigest(cfg.OutputDir, userRulesDigest); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	prompt := pipelineInitialWorldTickPrompt(cfg.OutputDir)
	const maxWorldTickRuns = 3
	for run := 1; run <= maxWorldTickRuns; run++ {
		if err := tools.EnsureInitialWorldTickForChapterOne(store.NewStore(cfg.OutputDir)); err == nil {
			fmt.Fprintln(os.Stderr, "[pipeline:zero-init] 初始 world_tick 已就绪")
			return nil
		}
		if err := pipelineResetInvalidInitialWorldTick(cfg.OutputDir); err != nil {
			return err
		}
		if run == 1 {
			fmt.Fprintln(os.Stderr, "[pipeline:zero-init] 生成第 1 章前初始 world_tick")
		} else {
			fmt.Fprintf(os.Stderr, "[pipeline:zero-init] 第 %d/%d 次恢复初始 world_tick\n", run, maxWorldTickRuns)
		}
		if err := headless.Run(cfg, bundle, pipelineInitialWorldTickHeadlessOptions(prompt)); err != nil {
			return err
		}
	}
	if err := tools.EnsureInitialWorldTickForChapterOne(store.NewStore(cfg.OutputDir)); err != nil {
		return fmt.Errorf("zero-init 阶段未完成初始 world_tick：%w", err)
	}
	return nil
}

func pipelineAcquireInitialWorldTickExecution(st *store.Store) (func() error, error) {
	if st == nil {
		return nil, fmt.Errorf("initial world_tick execution requires a store")
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil {
		return nil, fmt.Errorf("load foundation execution lock for initial world_tick: %w", err)
	}
	if lock == nil || lock.Mode != domain.PipelineExecutionFoundation {
		return nil, fmt.Errorf("initial world_tick requires the active foundation execution lock")
	}
	worldTickLock := *lock
	worldTickLock.Mode = domain.PipelineExecutionWorldTick
	if err := st.Runtime.AcquirePipelineExecution(worldTickLock); err != nil {
		return nil, fmt.Errorf("acquire world_tick-only execution lock: %w", err)
	}
	return func() error {
		current, err := st.Runtime.LoadPipelineExecution()
		if err != nil {
			return fmt.Errorf("load world_tick-only execution lock for restore: %w", err)
		}
		if current == nil || current.Mode != domain.PipelineExecutionWorldTick || current.Owner != lock.Owner {
			return fmt.Errorf("world_tick-only execution lock changed before foundation restore")
		}
		restored := *current
		restored.Mode = domain.PipelineExecutionFoundation
		if err := st.Runtime.AcquirePipelineExecution(restored); err != nil {
			return fmt.Errorf("restore foundation execution lock after initial world_tick: %w", err)
		}
		return nil
	}, nil
}

func pipelineInitialWorldTickUserRulesDigest(outputDir string) (string, error) {
	path := filepath.Join(outputDir, "meta", "user_rules.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("snapshot read-only meta/user_rules.json before initial world_tick: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func pipelineVerifyInitialWorldTickUserRulesDigest(outputDir, before string) error {
	after, err := pipelineInitialWorldTickUserRulesDigest(outputDir)
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("initial world_tick changed read-only meta/user_rules.json (before=%s after=%s); stage rejected", before, after)
	}
	return nil
}

func pipelineInitialWorldTickHeadlessOptions(prompt string) headless.Options {
	return headless.Options{
		Prompt:                    prompt,
		StopAfterInitialWorldTick: true,
		PreserveUserRules:         true,
	}
}

func pipelineResetInvalidInitialWorldTick(outputDir string) error {
	st := store.NewStore(outputDir)
	if issues := tools.InitialWorldTickQualityIssues(st); len(issues) == 0 {
		return nil
	}
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil || strings.TrimSpace(tick.TickID) == "" || tick.TickID == "v0-a0" || tick.EventCount <= 0 {
		return err
	}
	fmt.Fprintf(os.Stderr, "[pipeline:zero-init] 清理不合格 world_tick，准备重跑：%s\n", strings.Join(tools.InitialWorldTickQualityIssues(st), "；"))
	if err := st.WorldSim.ResetActivityState(); err != nil {
		return fmt.Errorf("清理不合格 world_tick 失败: %w", err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v0-a0", ThroughChapter: 0}); err != nil {
		return fmt.Errorf("重置 world_tick 基线失败: %w", err)
	}
	return nil
}

func pipelineInitialWorldTickPrompt(outputDir string) string {
	var b strings.Builder
	b.WriteString("[Pipeline zero-init 初始 world_tick 阶段]\n")
	b.WriteString("Architect foundation 与 zero-init readiness 已完成；本阶段只补齐第 1 章写作前的离屏世界信息流。\n")
	b.WriteString("必须派 architect_long 调用 save_world_tick，为第 1 章前生成开局镜头外事件、可见路径、势力/角色 agenda 推进和信息回收路径。\n")
	b.WriteString("现有 meta/user_rules.json 是只读长期约束；严禁调用 save_user_rules，严禁用本阶段内部提示覆盖或改写用户规则。\n")
	b.WriteString("硬约束：events.actors 与 faction_clock_updates.target 只能使用下方角色名、势力 id/name/aliases；工具返回任何 warnings 都不算通过。不得引入 premise、user_rules、world_rules 与冻结首弧没有授权的题材、人物、组织或机制。\n")
	if forbidden := pipelineWorldTickForbiddenTopics(outputDir); len(forbidden) > 0 {
		b.WriteString("本书明确排除的题材元素：")
		b.WriteString(strings.Join(forbidden, "、"))
		b.WriteString("；只能作为禁用边界说明，不能成为事件事实。\n")
	}
	if brief := pipelineWorldTickCanonBrief(outputDir); brief != "" {
		b.WriteString("\n[本书 canon 锚点]\n")
		b.WriteString(brief)
		b.WriteString("\n")
	}
	b.WriteString("完成初始 world_tick 后立即停止；严禁派 writer/drafter/editor，严禁 plan_chapter、draft_chapter、commit_chapter，严禁进入正文写作。\n")
	return b.String()
}

func pipelineWorldTickCanonBrief(outputDir string) string {
	st := store.NewStore(outputDir)
	var b strings.Builder
	if policy, err := st.LoadSimulationRestartPolicy(); err == nil && policy != nil && strings.TrimSpace(policy.GenerationID) != "" {
		fmt.Fprintf(&b, "当前推演 generation_id：%s；world_tick 只能服务此 generation。\n", strings.TrimSpace(policy.GenerationID))
	}
	if snap, err := st.UserRules.Load(); err == nil && snap != nil {
		if genre := strings.TrimSpace(snap.Structured.Genre); genre != "" {
			fmt.Fprintf(&b, "题材：%s\n", genre)
		}
		if words := snap.Structured.ChapterWords; words != nil {
			fmt.Fprintf(&b, "单章承载：%d—%d 字（user_rules 唯一字数口径）。\n", words.Min, words.Max)
		}
		if preferences := strings.TrimSpace(snap.Preferences); preferences != "" {
			b.WriteString("用户长期约束摘要：")
			b.WriteString(pipelineCompactText(preferences, 2400))
			b.WriteString("\n")
		}
	}
	if premise, err := st.Outline.LoadPremise(); err == nil && strings.TrimSpace(premise) != "" {
		b.WriteString("premise 摘要：")
		b.WriteString(pipelineCompactText(premise, 900))
		b.WriteString("\n")
	}
	flatOutline := pipelineWorldTickFlatOutline(st)
	pipelineWriteFirstArcWorldTickBrief(&b, st, flatOutline)
	if chars, err := st.Characters.Load(); err == nil && len(chars) > 0 {
		firstMentions := zeroCharacterFirstMentions(flatOutline, chars)
		b.WriteString("角色与最早可见边界（角色卡描述是作者资料，不代表角色开局已知）：\n")
		for _, c := range chars {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				continue
			}
			first := firstMentions[name]
			boundary := "当前大纲未安排可见；不得让信息进入主角视野"
			if first > 0 {
				boundary = fmt.Sprintf("最早第%d章可见", first)
			}
			fmt.Fprintf(&b, "- %s｜%s｜%s", name, zeroFirstNonEmpty(strings.TrimSpace(c.Role), "未标角色"), boundary)
			if baseline := strings.TrimSpace(zeroOpeningCharacterFact(c)); baseline != "" {
				fmt.Fprintf(&b, "｜当下基线：%s", pipelineCompactText(baseline, 220))
			}
			b.WriteString("\n")
		}
	}
	if world, err := st.World.LoadBookWorld(); err == nil && world != nil && len(world.Factions) > 0 {
		b.WriteString("可用势力/别名：\n")
		for _, f := range world.Factions {
			parts := []string{f.ID, f.Name}
			parts = append(parts, f.Aliases...)
			b.WriteString("- ")
			b.WriteString(strings.Join(nonEmptyPipelineStrings(parts), " / "))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func pipelineWorldTickForbiddenTopics(outputDir string) []string {
	st := store.NewStore(outputDir)
	snap, err := st.UserRules.Load()
	if err != nil || snap == nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range snap.Structured.ForbiddenPhrases {
		term := strings.TrimSpace(raw)
		if term == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
	}
	return out
}

func pipelineWorldTickFlatOutline(st *store.Store) []domain.OutlineEntry {
	outline, _ := zeroAuthoritativeOutline(st)
	return outline
}

func pipelineWriteFirstArcWorldTickBrief(b *strings.Builder, st *store.Store, flat []domain.OutlineEntry) {
	if b == nil || st == nil {
		return
	}
	if layered, err := st.Outline.LoadLayeredOutline(); err == nil && len(layered) > 0 {
		chapterCursor := 1
		for _, volume := range layered {
			for _, arc := range volume.Arcs {
				if len(arc.Chapters) > 0 {
					fmt.Fprintf(b, "当前首弧：V%dA%d《%s》；弧目标：%s\n", volume.Index, arc.Index, strings.TrimSpace(arc.Title), pipelineCompactText(arc.Goal, 500))
					b.WriteString("首弧章节边界（world_tick 必须围绕此弧的现实因果，不得抢跑后续项目）：\n")
					for i, entry := range arc.Chapters {
						fmt.Fprintf(b, "- 第%d章《%s》：%s；钩子：%s\n",
							chapterCursor+i,
							strings.TrimSpace(entry.Title),
							pipelineCompactText(entry.CoreEvent, 260),
							pipelineCompactText(entry.Hook, 160),
						)
					}
					return
				}
				chapterCursor += arc.ChapterSpan()
			}
		}
	}
	if len(flat) == 0 {
		return
	}
	b.WriteString("开篇章节边界：\n")
	for i, entry := range flat {
		if i >= 12 {
			break
		}
		fmt.Fprintf(b, "- 第%d章《%s》：%s；钩子：%s\n",
			entry.Chapter,
			strings.TrimSpace(entry.Title),
			pipelineCompactText(entry.CoreEvent, 260),
			pipelineCompactText(entry.Hook, 160),
		)
	}
}

func nonEmptyPipelineStrings(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func pipelineRequirePrewritingReady(outputDir string) error {
	st := store.NewStore(outputDir)
	if !tools.ChapterOnePendingFirstWrite(st) {
		return nil
	}
	if missing := tools.FoundationCoreMissing(outputDir); len(missing) > 0 {
		return fmt.Errorf("write 阶段禁止代办 Architect：请先执行 --pipeline --stages architect（missing=%s）", strings.Join(missing, ", "))
	}
	if ok, reason := architectReadinessState(outputDir); !ok {
		return fmt.Errorf("write 阶段必须在 Architect readiness 通过后执行：请先执行 --pipeline --stages architect（%s）", reason)
	}
	if ok, reason := tools.ZeroInitReadinessState(outputDir); !ok {
		return fmt.Errorf("write 阶段必须在 zero-init 完整通过后执行：请先执行 --pipeline --stages zero-init（%s）", reason)
	}
	if err := tools.EnsureInitialWorldTickForChapterOne(st); err != nil {
		return fmt.Errorf("write 阶段必须在 zero-init 完整通过后执行：请先执行 --pipeline --stages zero-init（%w）", err)
	}
	return nil
}

// pipelineCausalRewrite keeps rewrites on the same route as first-pass prose:
// plan_chapter -> causal world/character simulation -> drafter -> commit. The
// standalone rewrite-existing command remains available for explicit brief-only
// or diagnostic use, but pipeline prose never bypasses the chapter plan.
func pipelineCausalRewrite(opts cliOptions, flags pipelineFlags, state *domain.PipelineState, reviewArgs, legacyRewriteArgs []string) error {
	if flags.RewriteBriefOnly {
		return rewriteExistingPipeline(opts, legacyRewriteArgs)
	}
	cfg, _, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	maxRounds := flags.MaxRewriteRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}
	st := store.NewStore(cfg.OutputDir)
	if queued, queueErr := pipelineQueueCurrentExternalSamplingFailures(st, flags.Start, flags.End); queueErr != nil {
		return queueErr
	} else if len(queued) > 0 {
		fmt.Fprintf(os.Stderr, "[pipeline:rewrite] 已发现当前精确 SHA 的用户外部抽查高分并送入整章返工队列：%v\n", queued)
	}
	if flags.ForceRerender {
		progress, loadErr := st.Progress.Load()
		if loadErr != nil {
			return loadErr
		}
		pending, pendingErr := pipelineForceRerenderTargets(progress, flags.Start, flags.End)
		if pendingErr != nil {
			return pendingErr
		}
		instruction := ""
		if state != nil {
			instruction = state.Prompt
		}
		requested, requestErr := pipelineRequestFullRerender(st, pending, instruction)
		if requestErr != nil {
			return requestErr
		}
		if len(requested) > 0 {
			// --from/--to only scopes the chapters whose draft is superseded in
			// this invocation. It must not silently erase an already queued rewrite
			// outside that range; those chapters still carry independent review and
			// external-detector failures bound to their own body hashes.
			queued := mergePendingRewriteChapters(progress.PendingRewrites, requested)
			if err := st.Progress.SetPendingRewritesAndFlow(
				queued,
				"用户显式要求整章重渲染；复用既有世界推演与 POV plan",
				domain.FlowRewriting,
			); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[pipeline:rewrite] 已显式使旧草稿失效，保留既有世界推演与 plan，整章重新渲染：%v\n", requested)
		}
	}
	if flags.PolishWarnings && !flags.ForceRerender {
		queued, queueErr := pipelineQueueAcceptedWarningPolish(st, flags.Start, flags.End)
		if queueErr != nil {
			return queueErr
		}
		if len(queued) > 0 {
			fmt.Fprintf(os.Stderr, "[pipeline:rewrite] 已将 accept 章节的可执行黄旗送入正文打磨队列（复用既有推演）：%v\n", queued)
		}
	}
	for round := 1; round <= maxRounds; round++ {
		progress, err := st.Progress.Load()
		if err != nil {
			return err
		}
		pending, err := pipelineCausalRewritePending(progress, flags.Start, flags.End)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			if round == 1 {
				fmt.Fprintln(os.Stderr, "[pipeline:rewrite] 当前范围没有 pending_rewrites，跳过正文改写")
			}
			return nil
		}
		if pipelineCausalRewriteAwaitingReview(st, pending) {
			fmt.Fprintf(os.Stderr, "[pipeline:rewrite] 检测到因果正文已 commit、复审尚未刷新，直接复审：%v\n", pending)
			for _, chapter := range pending {
				rel := filepath.ToSlash(filepath.Join("chapters", fmt.Sprintf("%02d.md", chapter)))
				if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "causal-rewrite", rel); err != nil {
					return fmt.Errorf("记录第 %d 章因果重写 checkpoint: %w", chapter, err)
				}
			}
			if err := reviewExistingPipeline(opts, reviewArgs); err != nil {
				return fmt.Errorf("因果重写恢复复审失败: %w", err)
			}
			continue
		}

		target := pending[len(pending)-1]
		writeFlags := flags
		writeFlags.WriteTo = target
		writeFlags.StopAfterCommit = target
		fmt.Fprintf(os.Stderr, "[pipeline:rewrite] 第 %d/%d 轮按单世界因果链处理返工（渲染问题复用既有 plan，事实问题重推演）：%v\n", round, maxRounds, pending)
		if err := pipelineWrite(opts, writeFlags, state); err != nil {
			return fmt.Errorf("因果重写第 %d 轮失败: %w", round, err)
		}
		for _, chapter := range pending {
			rel := filepath.ToSlash(filepath.Join("chapters", fmt.Sprintf("%02d.md", chapter)))
			if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "causal-rewrite", rel); err != nil {
				return fmt.Errorf("记录第 %d 章因果重写 checkpoint: %w", chapter, err)
			}
		}
		if err := reviewExistingPipeline(opts, reviewArgs); err != nil {
			return fmt.Errorf("因果重写第 %d 轮复审失败: %w", round, err)
		}
	}

	progress, err := st.Progress.Load()
	if err != nil {
		return err
	}
	pending, pendingErr := pipelineCausalRewritePending(progress, flags.Start, flags.End)
	if pendingErr != nil {
		return pendingErr
	}
	if len(pending) > 0 {
		return fmt.Errorf("达到最大因果重写轮数 %d 后仍有 pending_rewrites=%v", maxRounds, pending)
	}
	return nil
}

func pipelineForceRerenderTargets(progress *domain.Progress, start, end int) ([]int, error) {
	if progress == nil {
		return nil, nil
	}
	if len(progress.PendingRewrites) > 0 {
		return pipelineCausalRewritePending(progress, start, end)
	}
	selected := filterChaptersForPipelineRange(progress.CompletedChapters, pipelineFlags{Start: start, End: end})
	if start > 0 && len(selected) == 0 {
		return nil, fmt.Errorf("--force-rerender 指定范围 %d-%d 没有已完成章节", start, end)
	}
	return selected, nil
}

func mergePendingRewriteChapters(existing, requested []int) []int {
	seen := make(map[int]struct{}, len(existing)+len(requested))
	merged := make([]int, 0, len(existing)+len(requested))
	for _, chapters := range [][]int{existing, requested} {
		for _, chapter := range chapters {
			if chapter <= 0 {
				continue
			}
			if _, ok := seen[chapter]; ok {
				continue
			}
			seen[chapter] = struct{}{}
			merged = append(merged, chapter)
		}
	}
	sort.Ints(merged)
	return merged
}

const pipelineExternalSamplingRewriteReason = "用户报告的当前精确 SHA 外部抽查高分；整章返工后只走自动门禁"

// pipelineQueueCurrentExternalSamplingFailures reconciles the append-only
// user sampling journal into normal production routing. Only a blocking result
// bound to the exact current final body is actionable. Missing/unknown samples
// and unresolved identities from an explicit automated_hard contract are left
// to their own gate and never manufacture a rewrite request here.
func pipelineQueueCurrentExternalSamplingFailures(st *store.Store, start, end int) ([]int, error) {
	if st == nil {
		return nil, nil
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil, nil
	}
	chapters := append([]int(nil), progress.CompletedChapters...)
	chapters = filterChaptersForPipelineRange(chapters, pipelineFlags{Start: start, End: end})
	if len(chapters) == 0 {
		return nil, nil
	}

	var blocking []int
	for _, chapter := range chapters {
		body, readErr := os.ReadFile(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)))
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("读取第 %d 章当前终稿以核对外部抽查失败: %w", chapter, readErr)
		}
		if strings.TrimSpace(string(body)) == "" {
			continue
		}
		inspection, inspectErr := tools.InspectRegisteredExternalRetestsForBody(
			st.Dir(), chapter, reviewreport.BodySHA256(string(body)),
		)
		if inspectErr != nil {
			return nil, fmt.Errorf("核对第 %d 章当前终稿的外部抽查记录失败: %w", chapter, inspectErr)
		}
		if len(inspection.Blocking) > 0 {
			blocking = append(blocking, chapter)
		}
	}
	if len(blocking) == 0 {
		return nil, nil
	}

	merged := mergePendingRewriteChapters(progress.PendingRewrites, blocking)
	unchanged := progress.Flow == domain.FlowRewriting &&
		progress.RewriteReason == pipelineExternalSamplingRewriteReason &&
		len(progress.PendingRewrites) == len(merged)
	if unchanged {
		for i := range merged {
			if progress.PendingRewrites[i] != merged[i] {
				unchanged = false
				break
			}
		}
	}
	if !unchanged {
		if err := st.Progress.SetPendingRewritesAndFlow(
			merged,
			pipelineExternalSamplingRewriteReason,
			domain.FlowRewriting,
		); err != nil {
			return nil, err
		}
	}
	return blocking, nil
}

type pipelineRerenderRequest = domain.ChapterRerenderRequest

func pipelineRequestFullRerender(st *store.Store, chapters []int, instruction string) ([]int, error) {
	if st == nil || len(chapters) == 0 {
		return nil, nil
	}
	var requested []int
	for _, chapter := range chapters {
		if chapter <= 0 {
			continue
		}
		if escalation := tools.InspectRenderOnlyReplanEscalation(st, chapter); escalation.Required {
			return nil, fmt.Errorf("第 %d 章不能再次 --force-rerender：%s；必须先重做 chapter_world_simulation/plan", chapter, escalation.Reason)
		}
		if err := tools.ValidateReusableCausalPlanForRerender(st, chapter); err != nil {
			return nil, fmt.Errorf("第 %d 章不能只重渲染，必须先修复推演/plan: %w", chapter, err)
		}
		planRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.plan.json", chapter)))
		draftRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter)))
		planRaw, planErr := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(planRel)))
		draftRaw, draftErr := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(draftRel)))
		if planErr != nil || len(bytes.TrimSpace(planRaw)) == 0 {
			return nil, fmt.Errorf("第 %d 章 force-rerender 缺少正式 plan: %w", chapter, planErr)
		}
		if draftErr != nil || len(bytes.TrimSpace(draftRaw)) == 0 {
			return nil, fmt.Errorf("第 %d 章 force-rerender 缺少现有草稿: %w", chapter, draftErr)
		}
		planSum := sha256.Sum256(planRaw)
		draftSum := sha256.Sum256(draftRaw)
		instructionSum := sha256.Sum256([]byte(strings.TrimSpace(instruction)))
		request := pipelineRerenderRequest{
			Version:               1,
			Chapter:               chapter,
			PlanSHA256:            hex.EncodeToString(planSum[:]),
			SupersededDraftSHA256: hex.EncodeToString(draftSum[:]),
			Reason:                "explicit pipeline --force-rerender; reuse approved causal simulation and POV plan",
			RequestedAt:           time.Now().Format(time.RFC3339),
		}
		if strings.TrimSpace(instruction) != "" {
			request.Instruction = strings.TrimSpace(instruction)
			request.InstructionSHA256 = hex.EncodeToString(instructionSum[:])
		}
		raw, err := json.MarshalIndent(request, "", "  ")
		if err != nil {
			return nil, err
		}
		requestRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.rerender_request.json", chapter)))
		requestPath := filepath.Join(st.Dir(), filepath.FromSlash(requestRel))
		if err := os.MkdirAll(filepath.Dir(requestPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(requestPath, raw, 0o644); err != nil {
			return nil, err
		}
		if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "rerender-request", requestRel); err != nil {
			return nil, fmt.Errorf("记录第 %d 章整章重渲染请求: %w", chapter, err)
		}
		requested = append(requested, chapter)
	}
	return requested, nil
}

func pipelineQueueAcceptedWarningPolish(st *store.Store, start, end int) ([]int, error) {
	if st == nil {
		return nil, nil
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil, err
	}
	if len(progress.PendingRewrites) > 0 {
		for _, chapter := range append([]int(nil), progress.PendingRewrites...) {
			if pipelineWarningPolishAlreadyResolved(st, chapter) {
				if err := st.Progress.CompleteRewrite(chapter); err != nil {
					return nil, err
				}
			}
		}
		progress, err = st.Progress.Load()
		if err != nil || progress == nil || len(progress.PendingRewrites) > 0 {
			return nil, err
		}
	}
	chapters := filterChaptersForPipelineRange(progress.CompletedChapters, pipelineFlags{Start: start, End: end})
	selected := make([]int, 0, len(chapters))
	for _, chapter := range chapters {
		if pipelineWarningPolishAlreadyResolved(st, chapter) {
			continue
		}
		if err := currentChapterReviewError(st.Dir(), chapter); err != nil {
			return nil, fmt.Errorf("第 %d 章黄旗打磨要求当前审核：%w", chapter, err)
		}
		body, err := os.ReadFile(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)))
		if err != nil {
			return nil, err
		}
		reviewBody, _ := os.ReadFile(filepath.Join(st.Dir(), "reviews", fmt.Sprintf("%02d.md", chapter)))
		plan := buildRevisionPlan(st.Dir(), chapter, string(body), string(reviewBody))
		if plan.HasRed || !plan.HasYellow {
			continue
		}
		if err := writeRevisionBrief(st.Dir(), plan); err != nil {
			return nil, fmt.Errorf("刷新第 %d 章黄旗 rewrite brief: %w", chapter, err)
		}
		selected = append(selected, chapter)
	}
	if len(selected) == 0 {
		return nil, nil
	}
	sort.Ints(selected)
	if err := st.Progress.SetPendingRewritesAndFlow(selected, "已接受章节存在可执行正文黄旗，择优局部打磨", domain.FlowPolishing); err != nil {
		return nil, err
	}
	return selected, nil
}

func pipelineWarningPolishAlreadyResolved(st *store.Store, chapter int) bool {
	if st == nil || chapter <= 0 || currentChapterReviewError(st.Dir(), chapter) != nil {
		return false
	}
	body, err := os.ReadFile(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)))
	if err != nil {
		return false
	}
	cp := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "causal-rewrite")
	return cp != nil && cp.Digest == "sha256:"+reviewreport.BodySHA256(string(body))
}

func pipelineCausalRewriteAwaitingReview(st *store.Store, chapters []int) bool {
	if st == nil || len(chapters) == 0 {
		return false
	}
	for _, chapter := range chapters {
		commit := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit")
		if commit == nil {
			return false
		}
		body, err := os.ReadFile(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", chapter)))
		if err != nil || commit.Digest != "sha256:"+reviewreport.BodySHA256(string(body)) {
			return false
		}
		// reviewExistingPipeline uses its own Store, so this Store's checkpoint
		// cache does not observe the appended review checkpoint. Bind recovery to
		// the durable review artifacts and current chapter bytes instead.
		if currentChapterReviewError(st.Dir(), chapter) == nil {
			return false
		}
	}
	return true
}

func pipelineCausalRewritePending(progress *domain.Progress, start, end int) ([]int, error) {
	if progress == nil || len(progress.PendingRewrites) == 0 {
		return nil, nil
	}
	pending := append([]int(nil), progress.PendingRewrites...)
	sort.Ints(pending)
	if start > 0 && pending[0] < start {
		return nil, fmt.Errorf("第 %d 章仍在待返工队列；因果写作必须按章序处理，不能从 --from=%d 跳过", pending[0], start)
	}
	seen := map[int]bool{}
	selected := make([]int, 0, len(pending))
	for _, chapter := range pending {
		if chapter <= 0 || seen[chapter] {
			continue
		}
		if start > 0 && chapter < start {
			continue
		}
		if end > 0 && chapter > end {
			continue
		}
		selected = append(selected, chapter)
		seen[chapter] = true
	}
	return selected, nil
}

// pipelineWrite 跑创作阶段：已完结则跳过；已有进度则恢复；全新项目用创作指令起新书。
//
// 工程卡点与自愈（用户不变量）：
//  1. 第 1 章从未写完时，write 只验收 Architect foundation 与 zero-init readiness；
//     缺任一项都直接失败，禁止在 Writer 阶段代办前置阶段。
//  2. Coordinator 因瞬时错误/卡点停止而未达标时，在有界次数内自动续跑，
//     不再阶段失败等人工重跑。
func pipelineWrite(opts cliOptions, flags pipelineFlags, state *domain.PipelineState) error {
	cfg, bundle, err := loadCfgBundle(opts)
	if err != nil {
		return err
	}
	return pipelineWriteConfigured(opts, flags, state, cfg, bundle)
}

// pipelineWriteConfigured runs the ordinary writer route against an already
// normalized configuration. Sealed render uses this entrypoint with an exact
// candidate output directory, so a draft/commit/review failure cannot mutate
// the live canonical tree.
func pipelineWriteConfigured(
	opts cliOptions,
	flags pipelineFlags,
	state *domain.PipelineState,
	cfg bootstrap.Config,
	bundle assets.Bundle,
) error {
	if flags.RenderOnly {
		writeStore := store.NewStore(cfg.OutputDir)
		frozen, _, err := loadAndVerifyPipelineFrozenPlan(cfg.OutputDir)
		if err != nil {
			return fmt.Errorf("render-only zero-LLM gate load frozen plan: %w", err)
		}
		if frozen.ProjectionBinding == "sealed_v2" {
			if _, err := requirePipelineSealedRenderPreflight(writeStore, frozen, false); err != nil {
				return fmt.Errorf("render-only zero-LLM typed preflight: %w", err)
			}
			// The candidate/live render lease is already bound at this entrypoint,
			// so the shared plan guard must select the exact sealed RAG receipt.
			if err := tools.ValidateCurrentChapterRenderPlanForExecution(writeStore, frozen.Chapter); err != nil {
				return fmt.Errorf("render-only zero-LLM sealed plan gate: %w", err)
			}
		}
	}
	timingInvocationID := newPipelineTimingInvocationID(time.Now())
	if flags.RenderOnly {
		// A sealed prose pass is reproducible only when every Coordinator,
		// Drafter and sampling-Judge request stays on its configured primary
		// provider/model. Provider failure stops this attempt; it may not
		// silently change the model that realizes the immutable bundle.
		cfg.DisableModelFailover = true
	}
	if !flags.RenderOnly {
		if err := ensurePipelineRAGReady(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "[pipeline:write] RAG 写作前检查失败：%v\n", err)
			return err
		}
	}
	if err := pipelineRequirePrewritingReady(cfg.OutputDir); err != nil {
		return err
	}
	if !flags.RenderOnly {
		if queued, queueErr := pipelineQueueCurrentExternalSamplingFailures(store.NewStore(cfg.OutputDir), 0, 0); queueErr != nil {
			return queueErr
		} else if len(queued) > 0 {
			fmt.Fprintf(os.Stderr, "[pipeline:write] 已发现当前精确 SHA 的用户外部抽查高分并送入整章返工队列：%v\n", queued)
		}
	}
	maxWriteRuns := 4
	// A frozen render may need more than one Host turn even when it produces
	// only one accepted chapter body. Pipeline-managed prose must stop after the
	// first whole-body write, obtain a provider judgment for that exact hash,
	// then resume with draft_finalizer for check/commit. Keep those bounded turns
	// inside the same isolated render candidate; recreating the candidate would
	// discard the only draft the judge is required to inspect and deterministically
	// repeat generation forever. The render execution lock still forbids every
	// planner/world-simulator route, and the causal render-attempt limit is checked
	// before each turn below.
	for run := 1; run <= maxWriteRuns; run++ {
		currentStore := store.NewStore(cfg.OutputDir)
		if flags.RenderOnly && flags.StopAfterCommit > 0 {
			if err := pipelineRequireRenderAttemptAvailable(currentStore, flags.StopAfterCommit); err != nil {
				return err
			}
		}
		prog, _ := currentStore.Progress.Load()
		if pipelineWriteGoalReached(prog, flags.WriteTo) {
			return nil
		}
		if flags.RenderOnly {
			if judged, err := pipelineJudgePendingRenderOnlyDraftHash(opts, cfg.OutputDir, prog); err != nil {
				return err
			} else if judged {
				prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
			}
			// The judge may have converted this exact hash into the final
			// plan-budget failure. Persist and enforce that fact before Host can
			// dispatch another whole-chapter writer in this same process.
			currentStore = store.NewStore(cfg.OutputDir)
			if err := pipelineRequireRenderAttemptAvailable(currentStore, flags.StopAfterCommit); err != nil {
				return err
			}
		} else {
			if judged, err := pipelineJudgePendingDraftHash(opts, cfg.OutputDir, prog); err != nil {
				return err
			} else if judged {
				prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
			}
		}
		// zero-init（--reset-simulation-state）会切换 progress 的推演线，
		// 必须重载后再做推演线一致性检查，否则拿旧快照误报不一致。
		prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
		if err := ensurePipelineSimulationRestartReady(cfg.OutputDir, prog); err != nil {
			return err
		}
		if handled, err := pipelineTryMechanicalSealedFinalize(
			context.Background(),
			currentStore,
			flags,
			run,
		); err != nil {
			return err
		} else if handled {
			return nil
		}
		prompt := ""
		if flags.RenderOnly {
			fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章只消费已冻结 plan；禁止 fresh-session、staged-plan finalize 与任何临时规划\n", flags.StopAfterCommit)
		} else {
			needsFresh, err := pipelineNeedsFreshWritingSession(cfg.OutputDir, prog)
			if err != nil {
				return err
			}
			if needsFresh {
				if err := pipelinePrepareFreshWritingSession(cfg.OutputDir, 1); err != nil {
					return err
				}
				fmt.Fprintln(os.Stderr, "[pipeline:write] 第 1 章从当前 Architect/zero-init 事实启动写作路由")
			} else if run == 1 {
				if err := pipelineFinalizeStagedPlans(cfg.OutputDir, flags.WriteTo); err != nil {
					return err
				}
				prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
				if pipelineWriteGoalReached(prog, flags.WriteTo) {
					return nil
				}
				fmt.Fprintln(os.Stderr, "[pipeline:write] 检测到已有进度，恢复创作")
			} else {
				if err := pipelineFinalizeStagedPlans(cfg.OutputDir, flags.WriteTo); err != nil {
					return err
				}
				prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
				if pipelineWriteGoalReached(prog, flags.WriteTo) {
					return nil
				}
			}
		}
		commitSeqBefore := latestPipelineChapterCommitSeq(cfg.OutputDir, flags.StopAfterCommit)
		stopOnRenderReplan := 0
		if flags.RenderOnly {
			stopOnRenderReplan = flags.StopAfterCommit
		}
		hostTurnStarted := time.Now()
		if flags.RenderOnly {
			fmt.Fprintf(os.Stderr,
				"[pipeline:timing] ch%02d frozen_host_turn=%d/%d started\n",
				flags.StopAfterCommit, run, maxWriteRuns,
			)
		}
		hostTurnErr := runPipelineHostTurnWithDispatchBudget(
			flags.RenderOnly,
			cfg.OutputDir,
			timingInvocationID,
			flags.StopAfterCommit,
			run,
			func() error {
				return headless.Run(cfg, bundle, headless.Options{
					Prompt:                    prompt,
					StopAfterChapter:          flags.WriteTo,
					StopAfterRewriteCommit:    flags.StopAfterCommit,
					StopOnRenderReplanChapter: stopOnRenderReplan,
					SkipQueueReplay:           true,
					DisableLiveRAG:            flags.RenderOnly,
				})
			},
		)
		if flags.RenderOnly {
			status := "ok"
			if hostTurnErr != nil {
				status = "error"
			}
			recordPipelineChapterTiming(
				cfg.OutputDir,
				timingInvocationID,
				"frozen_host_turn",
				flags.StopAfterCommit,
				run,
				hostTurnStarted,
				0,
				status,
				hostTurnErr,
			)
			fmt.Fprintf(os.Stderr,
				"[pipeline:timing] ch%02d frozen_host_turn=%d/%d status=%s elapsed=%s\n",
				flags.StopAfterCommit, run, maxWriteRuns, status,
				time.Since(hostTurnStarted).Round(time.Millisecond),
			)
		}
		if hostTurnErr != nil {
			return hostTurnErr
		}
		if flags.StopAfterCommit > 0 && latestPipelineChapterCommitSeq(cfg.OutputDir, flags.StopAfterCommit) > commitSeqBefore {
			return nil
		}
		prog, _ = store.NewStore(cfg.OutputDir).Progress.Load()
		if pipelineWriteGoalReached(prog, flags.WriteTo) {
			return nil
		}
		// 未达标：可能是零章卡点收工（下轮循环顶部自动 zero-init），
		// 也可能是 provider/工具瞬时错误——都走同一条自愈路径：续跑。
		if flags.RenderOnly {
			resumable, reason, err := pipelineRenderOnlyCandidateResumeStatus(
				store.NewStore(cfg.OutputDir),
				flags.StopAfterCommit,
			)
			if err != nil {
				return err
			}
			if !resumable {
				return fmt.Errorf("render-only 第 %d 章冻结执行未产生目标 commit，且没有可安全恢复的 exact-body 草稿；已保留正式 plan，禁止自动重规划", flags.StopAfterCommit)
			}
			fmt.Fprintf(
				os.Stderr,
				"[pipeline:write] 第 %d 章候选已保留（%s）；第 %d/%d 个冻结 Host turn 结束，下轮先判定同一草稿哈希，再恢复 check/commit\n",
				flags.StopAfterCommit,
				reason,
				run,
				maxWriteRuns,
			)
			continue
		}
		fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d/%d 次运行未达标，自动续跑\n", run, maxWriteRuns)
	}
	if flags.RenderOnly {
		return fmt.Errorf(
			"render-only 第 %d 章在同一隔离候选内完成 %d 个冻结 Host turn 后仍未 commit；候选草稿与正式 plan 均保留，禁止重规划",
			flags.StopAfterCommit,
			maxWriteRuns,
		)
	}
	return fmt.Errorf("write 阶段自愈续跑 %d 次后仍未达标（详见 logs/headless.log）", maxWriteRuns)
}

type pipelineRenderHostTurnDispatchHooks struct {
	Needed  func(*store.Store, int) (bool, int64, error)
	Reserve func(string, string, int) (*pipelineRenderDispatchReservation, bool, error)
	Arm     func(string, *pipelineRenderDispatchReservation) error
	Finish  func(string, int, int64, *pipelineRenderDispatchReservation, error) error
	Clear   func(string, string) error
}

func runPipelineHostTurnWithDispatchBudget(
	renderOnly bool,
	outputDir string,
	invocationID string,
	chapter int,
	hostTurn int,
	hostRun func() error,
) error {
	return runPipelineHostTurnWithDispatchBudgetUsing(
		renderOnly,
		outputDir,
		invocationID,
		chapter,
		hostTurn,
		hostRun,
		pipelineRenderHostTurnDispatchHooks{
			Needed:  pipelineWholeBodyDispatchNeeded,
			Reserve: reservePipelineWholeBodyDispatch,
			Arm: func(outputDir string, reservation *pipelineRenderDispatchReservation) error {
				if reservation == nil {
					return fmt.Errorf("render prose permit requires a dispatch reservation")
				}
				return store.NewStore(outputDir).Runtime.ArmPipelineRenderProsePermit(
					reservation.AuthorizationDigest,
					reservation.Attempt,
				)
			},
			Finish: finishPipelineWholeBodyDispatchFromCandidate,
			Clear: func(outputDir, authorization string) error {
				return store.NewStore(outputDir).Runtime.ClearPipelineRenderProsePermit(authorization)
			},
		},
	)
}

// runPipelineHostTurnWithDispatchBudgetUsing is the last render-only boundary
// before Host may reach Drafter. Classification happens for every Host turn,
// but only a whole-body realization reserves a persistent slot. Once Host has
// been called, its reservation is finished on both success and failure; a
// finish failure is joined with (and never replaces) the Host error.
func runPipelineHostTurnWithDispatchBudgetUsing(
	renderOnly bool,
	outputDir string,
	invocationID string,
	chapter int,
	hostTurn int,
	hostRun func() error,
	hooks pipelineRenderHostTurnDispatchHooks,
) error {
	if hostRun == nil {
		return fmt.Errorf("pipeline Host turn runner is nil")
	}
	if !renderOnly {
		return hostRun()
	}
	if hooks.Needed == nil || hooks.Reserve == nil || hooks.Arm == nil || hooks.Finish == nil || hooks.Clear == nil {
		return fmt.Errorf("render-only Host turn dispatch budget hooks are incomplete")
	}
	// A prior process may have crashed after arming but before entering Drafter.
	// Every Host turn starts from an explicitly empty capability slot; the fresh
	// reservation below is the only path that can arm it again.
	if err := hooks.Clear(outputDir, ""); err != nil {
		return fmt.Errorf("clear stale render prose permit before Host turn %d: %w", hostTurn, err)
	}
	wholeBodyNeeded, baselineBodyCheckpointSeq, err := hooks.Needed(
		store.NewStore(outputDir),
		chapter,
	)
	if err != nil {
		return fmt.Errorf("classify render-only Host turn %d dispatch: %w", hostTurn, err)
	}
	var reservation *pipelineRenderDispatchReservation
	if wholeBodyNeeded {
		reservation, _, err = hooks.Reserve(outputDir, invocationID, hostTurn)
		if err != nil {
			// This return is deliberately before hostRun: an exhausted persistent
			// budget must never reach Drafter/provider dispatch.
			return fmt.Errorf("reserve render-only Host turn %d whole-body dispatch: %w", hostTurn, err)
		}
		if reservation == nil {
			return fmt.Errorf("reserve render-only Host turn %d whole-body dispatch returned nil", hostTurn)
		}
		if err := hooks.Arm(outputDir, reservation); err != nil {
			armErr := fmt.Errorf("arm render-only Host turn %d prose permit: %w", hostTurn, err)
			finishErr := hooks.Finish(
				outputDir,
				chapter,
				baselineBodyCheckpointSeq,
				reservation,
				armErr,
			)
			clearErr := hooks.Clear(outputDir, reservation.AuthorizationDigest)
			return errors.Join(armErr, finishErr, clearErr)
		}
	}
	hostErr := hostRun()
	var finishErr error
	authorization := ""
	if reservation != nil {
		authorization = reservation.AuthorizationDigest
		finishErr = hooks.Finish(
			outputDir,
			chapter,
			baselineBodyCheckpointSeq,
			reservation,
			hostErr,
		)
		if finishErr != nil {
			finishErr = fmt.Errorf("finish render-only Host turn %d whole-body dispatch: %w", hostTurn, finishErr)
		}
	}
	clearErr := hooks.Clear(outputDir, authorization)
	if clearErr != nil {
		clearErr = fmt.Errorf("clear render prose permit after Host turn %d: %w", hostTurn, clearErr)
	}
	return errors.Join(hostErr, finishErr, clearErr)
}

type pipelineMechanicalSealedFinalizeFunc func(
	context.Context,
	*store.Store,
	int,
) (tools.SealedMechanicalFinalizeResult, error)

// pipelineTryMechanicalSealedFinalize is a narrow Host fast path for an
// already-judged sealed draft. Every non-sealed, rewrite, edit or unapproved
// state returns control to the existing headless router unchanged.
func pipelineTryMechanicalSealedFinalize(
	ctx context.Context,
	st *store.Store,
	flags pipelineFlags,
	attempt int,
) (bool, error) {
	return pipelineTryMechanicalSealedFinalizeWith(
		ctx,
		st,
		flags,
		attempt,
		tools.FinalizeSealedDraftMechanically,
	)
}

func pipelineTryMechanicalSealedFinalizeWith(
	ctx context.Context,
	st *store.Store,
	flags pipelineFlags,
	attempt int,
	finalize pipelineMechanicalSealedFinalizeFunc,
) (bool, error) {
	if !flags.RenderOnly || flags.StopAfterCommit <= 0 {
		return false, nil
	}
	if st == nil || finalize == nil {
		return false, fmt.Errorf("render-only mechanical finalizer is not configured")
	}
	started := time.Now().UTC()
	result, finalizeErr := finalize(ctx, st, flags.StopAfterCommit)
	finished := time.Now().UTC()
	status := string(result.Disposition)
	if finalizeErr != nil {
		status = "error"
	} else if status == "" {
		status = "invalid_result"
	}
	timing := pipelineTimingRecord{
		InvocationID: newPipelineTimingInvocationID(started),
		RunIdentity:  fmt.Sprintf("sealed-ch%02d", flags.StopAfterCommit),
		Scope:        "chapter",
		Stage:        "mechanical_finalize",
		Chapter:      flags.StopAfterCommit,
		Attempt:      attempt,
		Status:       status,
		StartedAt:    started.Format(time.RFC3339Nano),
		FinishedAt:   finished.Format(time.RFC3339Nano),
		ElapsedMS:    finished.Sub(started).Milliseconds(),
	}
	if finalizeErr != nil {
		timing.Error = finalizeErr.Error()
	}
	if err := appendPipelineTiming(st.Dir(), timing); err != nil {
		fmt.Fprintf(os.Stderr,
			"[pipeline:timing] ch%02d mechanical_finalize 耗时记录失败：%v\n",
			flags.StopAfterCommit,
			err,
		)
	}
	if finalizeErr != nil {
		return false, fmt.Errorf(
			"render-only 第 %d 章 Host 机械 finalizer 失败（已失败关闭，禁止同轮 LLM 猜测恢复）：%w",
			flags.StopAfterCommit,
			finalizeErr,
		)
	}

	switch result.Disposition {
	case tools.SealedMechanicalFinalizeCommitted:
		fmt.Fprintf(os.Stderr,
			"[pipeline:write] 第 %d 章 exact-body DeepSeek 已批准；Host 机械 consistency+commit 完成（%s），跳过 draft_finalizer LLM\n",
			flags.StopAfterCommit,
			time.Since(started).Round(time.Millisecond),
		)
		return true, nil
	case tools.SealedMechanicalFinalizeNeedsAgent:
		fmt.Fprintf(os.Stderr,
			"[pipeline:write] 第 %d 章机械 consistency 需要正文处理（%s）；恢复原 draft_finalizer 路由\n",
			flags.StopAfterCommit,
			result.Reason,
		)
		return false, nil
	case tools.SealedMechanicalFinalizeNotApplicable:
		return false, nil
	default:
		return false, fmt.Errorf(
			"render-only 第 %d 章 Host 机械 finalizer 返回未知状态 %q",
			flags.StopAfterCommit,
			result.Disposition,
		)
	}
}

// pipelineJudgePendingRenderOnlyDraftHash runs the same exact-body managed
// preflight as ordinary pipeline writing, but keeps an isolated render
// candidate out of the live RAG index. The successful DeepSeek cache remains
// inside the candidate and is reused by the post-commit exact-body review.
func pipelineJudgePendingRenderOnlyDraftHash(
	opts cliOptions,
	outputDir string,
	progress *domain.Progress,
) (bool, error) {
	return pipelineJudgePendingRenderOnlyDraftHashWithJudge(
		opts,
		outputDir,
		progress,
		draftAIJudgePipeline,
	)
}

func pipelineJudgePendingRenderOnlyDraftHashWithJudge(
	opts cliOptions,
	outputDir string,
	progress *domain.Progress,
	judge pipelineDraftAIJudgeFunc,
) (bool, error) {
	if judge == nil {
		return pipelineJudgePendingDraftHashWithJudge(opts, outputDir, progress, nil)
	}
	noSedimentJudge := func(judgeOpts cliOptions, args []string) error {
		candidateArgs := append([]string(nil), args...)
		candidateArgs = append(candidateArgs, "--no-sediment", "--primary-only")
		return judge(judgeOpts, candidateArgs)
	}
	return pipelineJudgePendingDraftHashWithJudge(
		opts,
		outputDir,
		progress,
		noSedimentJudge,
	)
}

// pipelineRenderOnlyCandidateResumeStatus proves that another Host turn will
// operate on a durable exact-body draft in the same candidate. Merely finding
// drafts/NN.draft.md is insufficient: the checkpoint must bind its current
// bytes, otherwise a crash-written or stale payload must return to the outer
// recovery path instead of being treated as judgeable prose.
func pipelineRenderOnlyCandidateResumeStatus(
	st *store.Store,
	chapter int,
) (bool, string, error) {
	if st == nil || chapter <= 0 {
		return false, "", nil
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return false, "", fmt.Errorf("render-only 第 %d 章检查候选恢复状态: %w", chapter, err)
	}
	draft, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return false, "", fmt.Errorf("render-only 第 %d 章读取候选草稿: %w", chapter, err)
	}
	if strings.TrimSpace(draft) == "" {
		return false, "", nil
	}
	if _, err := tools.CurrentChapterBodyCheckpoint(st, chapter); err != nil {
		return false, "", fmt.Errorf("render-only 第 %d 章候选草稿没有 current exact-body checkpoint: %w", chapter, err)
	}
	reason := string(inspection.Status)
	if strings.TrimSpace(reason) == "" {
		reason = "exact-body draft pending finalization"
	}
	return true, reason, nil
}

// pipelineRequireRenderAttemptAvailable must run before pending-draft judging
// or causal quarantine. Once a frozen plan has exhausted its render budget, the
// split pipeline stops with every planning artifact intact; only an explicit
// plan stage may create a new causal epoch.
func pipelineRequireRenderAttemptAvailable(st *store.Store, chapter int) error {
	if err := requirePipelineRenderConvergenceAvailable(st); err != nil {
		return err
	}
	escalation := tools.InspectRenderOnlyReplanEscalation(st, chapter)
	if !escalation.Required {
		return nil
	}
	return fmt.Errorf(
		"第 %d 章 render-only 已有 %d 个不同整章哈希触发结构阻断（上限 %d）；冻结计划和 world simulation 保持不变，必须先单独执行 plan，禁止 judge/quarantine/自动重规划：%s",
		chapter,
		escalation.Attempts,
		escalation.Limit,
		escalation.Reason,
	)
}

type pipelineDraftAIJudgeFunc func(cliOptions, []string) error

// Three minutes is the hard wall-clock ceiling for the managed exact-body
// judge operation. The DeepSeek scheduler gives this whole window to the
// primary call; an early malformed response may spend only the remaining time
// on one same-hash format repair. Successful exact-body results are cached and
// never called again under the same review identity.
const pipelineManagedDraftJudgeBudget = 3 * time.Minute

type pipelineCausalQuarantineEntry struct {
	Source     string `json:"source"`
	Quarantine string `json:"quarantine"`
	SHA256     string `json:"sha256,omitempty"`
}

type pipelineCausalQuarantineManifest struct {
	Version          int                             `json:"version"`
	Chapter          int                             `json:"chapter"`
	DraftBodySHA256  string                          `json:"draft_body_sha256"`
	Reason           string                          `json:"reason"`
	PlanInvalidated  bool                            `json:"plan_invalidated"`
	WorldInvalidated bool                            `json:"world_simulation_invalidated"`
	CreatedAt        string                          `json:"created_at"`
	Entries          []pipelineCausalQuarantineEntry `json:"entries"`
}

type pipelinePendingDraftPreflight struct {
	HasDraft    bool
	Invalidated bool
	ManifestRel string
	Reason      string
}

func pipelineJudgePendingDraftHash(opts cliOptions, outputDir string, progress *domain.Progress) (bool, error) {
	return pipelineJudgePendingDraftHashWithJudge(opts, outputDir, progress, draftAIJudgePipeline)
}

func pipelineJudgePendingDraftHashWithJudge(opts cliOptions, outputDir string, progress *domain.Progress, judge pipelineDraftAIJudgeFunc) (bool, error) {
	if progress == nil {
		return false, nil
	}
	chapter := 0
	if len(progress.PendingRewrites) > 0 {
		chapter = progress.PendingRewrites[0]
	} else {
		chapter = progress.NextChapter()
	}
	if chapter <= 0 {
		return false, nil
	}
	st := store.NewStore(outputDir)
	isRewrite := len(progress.PendingRewrites) > 0
	// Every pipeline-managed retained draft, including an ordinary next chapter,
	// must prove that its exact body checkpoint belongs to the current plan
	// epoch before any provider-backed or named-platform judgment. Rewrites add
	// the stronger source/brief/canonical-state/instruction freshness proof.
	// Keep this causal check ahead of the explicit-rerender shortcut: an active
	// request may coexist with an even older plan/simulation that must be
	// quarantined losslessly before the Host repairs the causal inputs.
	preflight, preflightErr := pipelinePreflightManagedDraftCausal(st, chapter, isRewrite)
	if preflightErr != nil {
		return false, fmt.Errorf("第 %d 章候选因果新鲜度预检失败: %w", chapter, preflightErr)
	}
	if !preflight.HasDraft {
		return false, nil
	}
	if preflight.Invalidated {
		fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章旧草稿未绑定当前 plan/body epoch或 rewrite 因果输入，已隔离到 %s；跳过外判并回到推演/规划/渲染\n", chapter, preflight.ManifestRel)
		return false, nil
	}
	// A tombstone-bound formal rewrite seed exists only to hand its exact review
	// and rewrite brief to the next Drafter turn.  Re-running static hard-fact or
	// provider gates against bytes already rejected by that same fresh review is
	// both redundant and harmful: rewrite_source freshness inside those gates can
	// quarantine the sealed plan before Host has a chance to produce the required
	// new hash.  The strict expression-only classifier keeps factual, character
	// and contract failures on the ordinary preflight/replan path.
	if isRewrite && tools.ReviewRequiresFreshDraft(st, chapter) &&
		tools.RenderOnlyReviewAllowsPlanReuse(st, chapter) {
		return false, nil
	}
	// An explicit rerender request supersedes the current draft hash. Do this
	// after causal freshness but before exact-body delivery gates and every
	// external-gate inspection: a named detector obligation is
	// retained in the durable requirement for the replacement candidate, but it
	// must not force a pointless retest of bytes the Host is about to replace.
	if tools.ExplicitRerenderRequestActive(st, chapter) {
		return false, nil
	}
	staticPreflight, staticErr := pipelinePreflightManagedDraftStatic(st, chapter)
	if staticErr != nil {
		return false, fmt.Errorf("第 %d 章候选零成本正文预检失败: %w", chapter, staticErr)
	}
	if staticPreflight.Invalidated {
		fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章候选未通过 hard-fact/title/word 零成本正文门，已隔离到 %s；跳过外判并回到 Drafter。原因：%s\n", chapter, staticPreflight.ManifestRel, staticPreflight.Reason)
		return false, nil
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return false, fmt.Errorf("检查第 %d 章草稿外部门禁: %w", chapter, err)
	}
	if inspection.LocalSoftEditPending {
		// The exact current hash already passed the provider-backed judge. Let the
		// Host dispatch the one permitted deterministic local repair; the edit tool
		// will invalidate this hash and return control here before any second edit.
		return false, nil
	}
	if inspection.RequiresRegisteredRetest && inspection.Requirement != nil {
		return true, fmt.Errorf("第 %d 章启用了显式自动外部门禁，必须先用 %s 对精确 payload 做同哈希复测；用户手工抽查不会进入此分支",
			chapter, strings.Join(tools.RegisteredExternalRetestLabels(inspection.Requirement), ", "))
	}
	if !pipelineDraftNeedsExternalJudgeForChapterWithStore(st, chapter, inspection) {
		return false, nil
	}
	fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章当前草稿尚无完整外判，先暂停 Writer 并获取修改建议\n", chapter)
	judgeOpts := opts
	judgeOpts.Dir = outputDir
	if judge == nil {
		return true, fmt.Errorf("第 %d 章草稿外判执行器未配置，已保持复判锁", chapter)
	}
	judgeStarted := time.Now()
	if err := judge(judgeOpts, []string{
		"--chapter", strconv.Itoa(chapter),
		"--budget", pipelineManagedDraftJudgeBudget.String(),
	}); err != nil {
		recordPipelineChapterTiming(
			outputDir,
			newPipelineTimingInvocationID(judgeStarted),
			"exact_hash_judge",
			chapter,
			1,
			judgeStarted,
			pipelineManagedDraftJudgeBudget,
			"error",
			err,
		)
		fmt.Fprintf(os.Stderr,
			"[pipeline:timing] ch%02d exact_hash_judge status=error elapsed=%s budget=%s\n",
			chapter, time.Since(judgeStarted).Round(time.Millisecond), pipelineManagedDraftJudgeBudget,
		)
		return true, fmt.Errorf("第 %d 章草稿外判失败，已保持复判锁，未继续生成: %w", chapter, err)
	}
	recordPipelineChapterTiming(
		outputDir,
		newPipelineTimingInvocationID(judgeStarted),
		"exact_hash_judge",
		chapter,
		1,
		judgeStarted,
		pipelineManagedDraftJudgeBudget,
		"ok",
		nil,
	)
	fmt.Fprintf(os.Stderr,
		"[pipeline:timing] ch%02d exact_hash_judge status=ok elapsed=%s budget=%s\n",
		chapter, time.Since(judgeStarted).Round(time.Millisecond), pipelineManagedDraftJudgeBudget,
	)
	after, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return true, fmt.Errorf("复核第 %d 章草稿外部门禁: %w", chapter, err)
	}
	if after.LocalSoftEditPending {
		return true, nil
	}
	if after.RequiresRegisteredRetest && after.Requirement != nil {
		return true, fmt.Errorf("第 %d 章已通过本地门禁与 DeepSeek，但显式自动外部门禁仍要求 %s 对精确 payload 复测；用户手工抽查不会进入此分支",
			chapter, strings.Join(tools.RegisteredExternalRetestLabels(after.Requirement), ", "))
	}
	if after.Status == tools.DraftExternalGateRejudgePending || after.Status == tools.DraftExternalGateAdviceIncomplete {
		return true, fmt.Errorf("第 %d 章外判未形成完整的新哈希结论，已停止流水线", chapter)
	}
	return true, nil
}

// pipelinePreflightManagedDraftCausal invalidates stale prose before any
// external-review code can inspect it. The body/plan epoch proof applies to
// both ordinary next chapters and rewrites; rewrites additionally prove their
// mutable causal inputs. The quarantine is lossless and deliberately keeps
// reviews/drafts/NN_full_rerender_required.json in place, so registered
// detector/mode obligations survive and attach to the eventual replacement.
func pipelinePreflightManagedDraftCausal(st *store.Store, chapter int, isRewrite bool) (pipelinePendingDraftPreflight, error) {
	result := pipelinePendingDraftPreflight{}
	if st == nil || chapter <= 0 {
		return result, fmt.Errorf("invalid chapter %d", chapter)
	}
	draftRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter)))
	draftRaw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(draftRel)))
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	result.HasDraft = true

	var causalReasons []string
	if len(bytes.TrimSpace(draftRaw)) == 0 {
		// An empty file is not a judgeable candidate either. Quarantine it through
		// the same fail-closed path instead of letting Inspector fall back to the
		// committed final body.
		causalReasons = append(causalReasons, "current draft is empty")
	}
	invalidatePlan := false
	invalidateWorld := false
	if escalation := tools.InspectRenderOnlyReplanEscalation(st, chapter); escalation.Required {
		invalidatePlan = true
		invalidateWorld = true
		causalReasons = append(causalReasons, escalation.Reason)
	}
	if isRewrite {
		if !tools.RenderOnlyReviewAllowsPlanReuse(st, chapter) {
			if err := tools.ValidateReusableCausalPlanForRerender(st, chapter); err != nil {
				invalidatePlan = true
				causalReasons = append(causalReasons, err.Error())
				worldRequired, worldReady, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
				if worldRequired && !worldReady {
					invalidateWorld = true
					if len(gaps) > 0 {
						causalReasons = append(causalReasons, "world simulation gaps: "+strings.Join(gaps, "；"))
					}
				}
			}
		}
	}

	// A current causal-plan checkpoint is mandatory for every managed draft.
	// This also detects a newer finalized world-simulation epoch that the plan
	// has not consumed yet, including ordinary next-chapter recovery.
	if _, err := tools.CurrentChapterPlanCausalCheckpoint(st, chapter); err != nil {
		invalidatePlan = true
		causalReasons = append(causalReasons, "current causal plan checkpoint invalid: "+err.Error())
		worldRequired, worldReady, gaps := tools.ChapterWorldSimulationStatus(st, chapter)
		if worldRequired && !worldReady {
			invalidateWorld = true
			if len(gaps) > 0 {
				causalReasons = append(causalReasons, "world simulation gaps: "+strings.Join(gaps, "；"))
			}
		}
	}

	// Pipeline-authored prose must be newer than the finalized plan and any
	// explicit rerender request. Exact bytes plus a draft checkpoint are not
	// enough when those bytes were produced in an older causal epoch.
	bodyCheckpoint, bodyErr := tools.CurrentChapterBodyCheckpoint(st, chapter)
	planCheckpoint, planErr := tools.CurrentChapterPlanCausalCheckpoint(st, chapter)
	if bodyErr != nil {
		causalReasons = append(causalReasons, "current draft checkpoint invalid: "+bodyErr.Error())
	}
	if planErr != nil {
		invalidatePlan = true
		causalReasons = append(causalReasons, "current plan checkpoint invalid: "+planErr.Error())
	}
	if bodyErr == nil && planErr == nil {
		boundary := planCheckpoint.Seq
		if request := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "rerender-request"); request != nil && request.Seq > boundary {
			boundary = request.Seq
		}
		if bodyCheckpoint.Seq <= boundary {
			causalReasons = append(causalReasons, fmt.Sprintf("draft checkpoint seq=%d is not newer than causal boundary seq=%d", bodyCheckpoint.Seq, boundary))
		}
	}
	if len(causalReasons) == 0 {
		return result, nil
	}

	manifestRel, err := pipelineQuarantineStaleCausalCandidate(
		st,
		chapter,
		draftRaw,
		strings.Join(causalReasons, "；"),
		invalidatePlan,
		invalidateWorld,
	)
	if err != nil {
		return result, err
	}
	result.Invalidated = true
	result.ManifestRel = manifestRel
	return result, nil
}

// pipelinePreflightManagedDraftStatic runs deterministic checks over the exact
// retained payload before DeepSeek or a named-platform handoff. A body failure
// quarantines only prose/parts so the current plan remains reusable by Drafter;
// named detector obligations live under reviews/ and are never moved.
func pipelinePreflightManagedDraftStatic(st *store.Store, chapter int) (pipelinePendingDraftPreflight, error) {
	result := pipelinePendingDraftPreflight{HasDraft: true}
	if st == nil || chapter <= 0 {
		return result, fmt.Errorf("invalid chapter %d", chapter)
	}
	draftRel := filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter)))
	draftRaw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(draftRel)))
	if err != nil {
		if os.IsNotExist(err) {
			result.HasDraft = false
			return result, nil
		}
		return result, err
	}
	content := string(draftRaw)

	var reasons []string
	anchors, err := tools.InspectDraftHardFactAnchorsForExternalJudge(st, chapter, content)
	if err != nil {
		return result, err
	}
	if !anchors.Passed {
		missing, marshalErr := json.Marshal(anchors.Missing)
		if marshalErr != nil {
			return result, marshalErr
		}
		reasons = append(reasons, "missing current hard-fact anchors: "+string(missing))
	}

	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return result, err
	}
	if plan == nil || strings.TrimSpace(plan.Title) == "" {
		return result, fmt.Errorf("第 %d 章缺少当前正式 plan/title", chapter)
	}
	heading := pipelineFirstChapterHeading(content)
	if heading == "" || pipelineNormalizeChapterTitle(heading) != pipelineNormalizeChapterTitle(plan.Title) {
		reasons = append(reasons, fmt.Sprintf("chapter title mismatch: body=%q plan=%q", heading, plan.Title))
	}

	wordReason, err := pipelineManagedDraftWordRangeReason(st, chapter, content)
	if err != nil {
		return result, err
	}
	if wordReason != "" {
		reasons = append(reasons, wordReason)
	}
	if len(reasons) == 0 {
		return result, nil
	}

	manifestRel, err := pipelineQuarantineStaleCausalCandidate(st, chapter, draftRaw, strings.Join(reasons, "；"), false, false)
	if err != nil {
		return result, err
	}
	result.Invalidated = true
	result.ManifestRel = manifestRel
	result.Reason = strings.Join(reasons, "；")
	return result, nil
}

func pipelineManagedDraftWordRangeReason(st *store.Store, chapter int, content string) (string, error) {
	snapshot, err := st.UserRules.Load()
	if err != nil {
		return "", err
	}
	actualWords := utf8.RuneCountInString(content)
	effectiveMin := 0
	effectiveMax := 0
	if snapshot != nil && snapshot.Structured.ChapterWords != nil {
		rule := snapshot.Structured.ChapterWords
		effectiveMin = rule.Min
		effectiveMax = rule.Max
	}
	dynamicBounds, err := tools.InspectSealedShortChapterWordBounds(st, chapter)
	if err != nil {
		return "", err
	}
	wordReasonPrefix := "chapter word count out of range"
	if dynamicBounds.Active {
		effectiveMin = dynamicBounds.Min
		effectiveMax = dynamicBounds.Max
		wordReasonPrefix = fmt.Sprintf(
			"sealed short cumulative word count out of range (prior_accepted_chapters=%d prior_accepted=%d book=%d-%d absolute_chapter=%d-%d)",
			dynamicBounds.PriorAcceptedChapters,
			dynamicBounds.PriorAcceptedRunes,
			dynamicBounds.BookMin,
			dynamicBounds.BookMax,
			dynamicBounds.ChapterMin,
			dynamicBounds.ChapterMax,
		)
	}
	if (effectiveMin > 0 && actualWords < effectiveMin) ||
		(effectiveMax > 0 && actualWords > effectiveMax) {
		return fmt.Sprintf("%s: actual=%d required=%d-%d", wordReasonPrefix, actualWords, effectiveMin, effectiveMax), nil
	}
	return "", nil
}

func pipelineFirstChapterHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return ""
}

func pipelineNormalizeChapterTitle(title string) string {
	title = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(title), "#"))
	if strings.HasPrefix(title, "第") {
		if index := strings.Index(title, "章"); index >= 0 {
			title = strings.TrimSpace(title[index+len("章"):])
		}
	}
	title = strings.TrimLeft(title, " ：:.-")
	return strings.ToLower(strings.Join(strings.Fields(title), ""))
}

func pipelineQuarantineStaleCausalCandidate(st *store.Store, chapter int, draftRaw []byte, reason string, invalidatePlan, invalidateWorld bool) (string, error) {
	if st == nil || chapter <= 0 {
		return "", fmt.Errorf("invalid chapter %d", chapter)
	}
	bodySHA := reviewreport.BodySHA256(string(draftRaw))
	shortSHA := bodySHA
	if len(shortSHA) > 12 {
		shortSHA = shortSHA[:12]
	}
	epoch := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102T150405.000000000Z"), shortSHA)
	quarantineRootRel := filepath.ToSlash(filepath.Join("meta", "quarantine", "causal_preflight", fmt.Sprintf("ch%03d", chapter), epoch))
	quarantineRoot := filepath.Join(st.Dir(), filepath.FromSlash(quarantineRootRel))

	rels := []string{
		filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter))),
		filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.parts", chapter))),
	}
	if invalidatePlan {
		rels = append(rels,
			filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.plan.json", chapter))),
			filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.plan.partial.json", chapter))),
			filepath.ToSlash(filepath.Join("drafts", fmt.Sprintf("%02d.plan_consistency.json", chapter))),
		)
	}
	if invalidateWorld {
		rels = append(rels,
			filepath.ToSlash(filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.json", chapter))),
			filepath.ToSlash(filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.md", chapter))),
			filepath.ToSlash(filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.partial.json", chapter))),
		)
	}

	manifest := pipelineCausalQuarantineManifest{
		Version:          1,
		Chapter:          chapter,
		DraftBodySHA256:  bodySHA,
		Reason:           strings.TrimSpace(reason),
		PlanInvalidated:  invalidatePlan,
		WorldInvalidated: invalidateWorld,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	for _, rel := range rels {
		source := filepath.Join(st.Dir(), filepath.FromSlash(rel))
		info, statErr := os.Stat(source)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return "", fmt.Errorf("检查待隔离 artifact %s: %w", rel, statErr)
		}
		targetRel := filepath.ToSlash(filepath.Join(quarantineRootRel, rel))
		target := filepath.Join(st.Dir(), filepath.FromSlash(targetRel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		entry := pipelineCausalQuarantineEntry{Source: rel, Quarantine: targetRel}
		if !info.IsDir() {
			raw, readErr := os.ReadFile(source)
			if readErr != nil {
				return "", readErr
			}
			sum := sha256.Sum256(raw)
			entry.SHA256 = hex.EncodeToString(sum[:])
		}
		if err := os.Rename(source, target); err != nil {
			return "", fmt.Errorf("隔离 stale artifact %s: %w", rel, err)
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	if err := os.MkdirAll(quarantineRoot, 0o755); err != nil {
		return "", err
	}
	manifestRaw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestRel := filepath.ToSlash(filepath.Join(quarantineRootRel, "manifest.json"))
	if err := os.WriteFile(filepath.Join(st.Dir(), filepath.FromSlash(manifestRel)), manifestRaw, 0o644); err != nil {
		return "", err
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(chapter), "causal-candidate-quarantined", manifestRel); err != nil {
		return "", fmt.Errorf("记录 stale causal candidate quarantine: %w", err)
	}
	return manifestRel, nil
}

func pipelineDraftNeedsExternalJudgeForChapter(outputDir string, chapter int, inspection tools.DraftExternalGateInspection) bool {
	return pipelineDraftNeedsExternalJudgeForChapterWithStore(store.NewStore(outputDir), chapter, inspection)
}

func pipelineDraftNeedsExternalJudgeForChapterWithStore(st *store.Store, chapter int, inspection tools.DraftExternalGateInspection) bool {
	if tools.ExplicitRerenderRequestActive(st, chapter) {
		return false
	}
	return pipelineDraftNeedsExternalJudge(inspection)
}

func pipelineDraftNeedsExternalJudge(inspection tools.DraftExternalGateInspection) bool {
	if inspection.LocalSoftEditPending || inspection.RequiresRegisteredRetest {
		return false
	}
	if inspection.Status == tools.DraftExternalGateRejudgePending || inspection.Status == tools.DraftExternalGateAdviceIncomplete {
		return true
	}
	// A newly rendered draft has no marker or judge artifact yet. Treat that as
	// pending instead of letting the finalizer read, edit, or commit an unjudged
	// hash. A blocking artifact with a same-hash requirement remains authorized
	// for its single full rerender and is intentionally not caught here.
	return inspection.Status == tools.DraftExternalGateNotRequired &&
		inspection.CurrentBodySHA256 != "" && !inspection.ArtifactExists && inspection.Requirement == nil
}

func latestPipelineChapterCommitSeq(outputDir string, chapter int) int64 {
	if chapter <= 0 {
		return 0
	}
	cp := store.NewStore(outputDir).Checkpoints.LatestByStep(domain.ChapterScope(chapter), "commit")
	if cp == nil {
		return 0
	}
	return cp.Seq
}

func pipelineHasWritingProgress(prog *domain.Progress) bool {
	if prog == nil {
		return false
	}
	return prog.Phase == domain.PhaseWriting ||
		prog.CurrentChapter > 0 ||
		prog.InProgressChapter > 0 ||
		len(prog.CompletedChapters) > 0 ||
		len(prog.PendingRewrites) > 0
}

func pipelineNeedsFreshWritingSession(outputDir string, prog *domain.Progress) (bool, error) {
	if !pipelineHasWritingProgress(prog) {
		return true, nil
	}
	if prog == nil || prog.Phase != domain.PhaseWriting {
		return false, nil
	}
	if prog.TotalWordCount != 0 || len(prog.CompletedChapters) > 0 || len(prog.PendingRewrites) > 0 {
		return false, nil
	}
	if prog.CurrentChapter != 1 && prog.InProgressChapter != 1 {
		return false, nil
	}
	st := store.NewStore(outputDir)
	if partial, err := st.Drafts.LoadChapterPlanPartial(1); err == nil && partial != nil {
		if _, migrateErr := tools.MigrateLegacyPlanStageToChapterSimulation(st, 1, partial); migrateErr != nil {
			return false, fmt.Errorf("迁移第 1 章全角色推演失败: %w", migrateErr)
		}
		partial, err = st.Drafts.LoadChapterPlanPartial(1)
		if err != nil {
			return false, fmt.Errorf("重载第 1 章 staged plan 失败: %w", err)
		}
		if issues := tools.ChapterPlanIdentityIssues(st, 1, partial); len(issues) > 0 {
			repairable := true
			for _, issue := range issues {
				if !strings.Contains(issue, "visible_in_chapter") {
					repairable = false
					break
				}
			}
			if repairable {
				return false, nil
			}
			fmt.Fprintf(os.Stderr, "[pipeline:write] 清理不合格第 1 章 staged plan：%s\n", strings.Join(issues, "；"))
			return true, nil
		}
	}
	for _, rel := range []string{
		filepath.Join("drafts", "01.plan.partial.json"),
		filepath.Join("drafts", "01.plan.json"),
		filepath.Join("drafts", "01.plan_consistency.json"),
		filepath.Join("drafts", "01.draft.md"),
		filepath.Join("chapters", "01.md"),
	} {
		_, err := os.Stat(filepath.Join(outputDir, rel))
		if err == nil {
			return false, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("检查第 1 章写作产物失败: %w", err)
		}
	}
	return true, nil
}

func pipelinePrepareFreshWritingSession(outputDir string, chapter int) error {
	if chapter <= 0 {
		chapter = 1
	}
	st := store.NewStore(outputDir)
	if err := pipelineClearStalePipelineSteer(st); err != nil {
		return err
	}
	if err := st.Runtime.Reset(); err != nil {
		return fmt.Errorf("重置 runtime 队列失败: %w", err)
	}
	if err := st.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("重置 checkpoints 失败: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(outputDir, "meta", "sessions")); err != nil {
		return fmt.Errorf("清理旧会话失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "meta", "sessions", "agents"), 0o755); err != nil {
		return fmt.Errorf("重建会话目录失败: %w", err)
	}
	for _, rel := range []string{
		filepath.Join("drafts", fmt.Sprintf("%02d.plan.partial.json", chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.plan.json", chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.plan_consistency.json", chapter)),
		filepath.Join("drafts", fmt.Sprintf("%02d.draft.md", chapter)),
		filepath.Join("chapters", fmt.Sprintf("%02d.md", chapter)),
	} {
		if err := os.Remove(filepath.Join(outputDir, rel)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("清理旧第 %d 章产物失败: %w", chapter, err)
		}
	}
	if err := st.Progress.StartChapter(chapter); err != nil {
		return fmt.Errorf("启动第 %d 章写作状态失败: %w", chapter, err)
	}
	return nil
}

func pipelineClearStalePipelineSteer(st *store.Store) error {
	meta, _ := st.RunMeta.Load()
	if meta == nil {
		return nil
	}
	if isPipelineInternalRepairSteer(meta.PendingSteer) {
		if err := st.RunMeta.ClearPendingSteer(); err != nil {
			return fmt.Errorf("清理旧 staged plan 修复指令失败: %w", err)
		}
	}
	return nil
}

func pipelineFinalizeStagedPlans(outputDir string, writeTo int) error {
	st := store.NewStore(outputDir)
	matches, err := filepath.Glob(filepath.Join(outputDir, "drafts", "*.plan.partial.json"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return pipelineClearStalePipelineSteer(st)
	}
	sort.Strings(matches)
	tool := tools.NewPlanDetailsTool(st)
	hadFailure := false
	for _, path := range matches {
		chapter, ok := chapterFromPlanPartialPath(path)
		if !ok || (writeTo > 0 && chapter > writeTo) {
			continue
		}
		if partial, loadErr := st.Drafts.LoadChapterPlanPartial(chapter); loadErr != nil {
			return loadErr
		} else if _, migrateErr := tools.MigrateLegacyPlanStageToChapterSimulation(st, chapter, partial); migrateErr != nil {
			return fmt.Errorf("迁移第 %d 章全角色推演失败: %w", chapter, migrateErr)
		}
		args, _ := json.Marshal(map[string]any{"chapter": chapter, "finalize": true})
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			hadFailure = true
			fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章 staged plan 尚未可收口：%v\n", chapter, err)
			if setErr := pipelineQueueStagedPlanRepair(st, chapter, err); setErr != nil {
				return setErr
			}
			continue
		}
		var result struct {
			Planned bool `json:"planned"`
		}
		_ = json.Unmarshal(raw, &result)
		if result.Planned {
			fmt.Fprintf(os.Stderr, "[pipeline:write] 第 %d 章 staged plan 已收口为正式计划\n", chapter)
		}
	}
	if !hadFailure {
		return pipelineClearStalePipelineSteer(st)
	}
	return nil
}

func pipelineQueueStagedPlanRepair(st *store.Store, chapter int, cause error) error {
	meta, _ := st.RunMeta.Load()
	if meta != nil && strings.TrimSpace(meta.PendingSteer) != "" && !isPipelineInternalRepairSteer(meta.PendingSteer) {
		return nil
	}
	causeText := cause.Error()
	msg := pipelineStagedPlanRepairSteer(chapter, causeText)
	label := "staged plan"
	if pipelineFailureNeedsWorldSimulation(causeText) {
		msg = pipelineWorldSimulationRepairSteer(chapter, causeText)
		label = "world simulation"
	}
	if err := st.RunMeta.SetPendingSteer(msg); err != nil {
		return fmt.Errorf("写入 staged plan 修复指令失败: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[pipeline:write] 已排队第 %d 章 %s 修复指令\n", chapter, label)
	return nil
}

func isPipelineInternalRepairSteer(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "Pipeline staged-plan repair") ||
		strings.HasPrefix(text, "Pipeline world-simulation repair")
}

func pipelineFailureNeedsWorldSimulation(cause string) bool {
	if strings.Contains(cause, "全角色世界推演") || strings.Contains(cause, "单世界全角色推演") {
		return true
	}
	return strings.Contains(cause, "chapter_world_simulation") &&
		(strings.Contains(cause, "invalid") || strings.Contains(cause, "未完成") || strings.Contains(cause, "missing"))
}

func pipelineWorldSimulationRepairSteer(chapter int, cause string) string {
	return fmt.Sprintf("Pipeline world-simulation repair：第%d章的全角色世界推演尚未完成或已因 rewrite brief/hash 更新而失效。当前回合不是 plan 修复回合：先调用 novel_context(chapter=%d) 一次，然后只允许调用 simulate_chapter_world；严格读取工具返回的 gaps，每批只补 gaps 中最多8名角色，复用现有 meta/chapter_simulations/%03d.partial.json，不重发已完成角色。返工章必须逐条补齐 rewrite_fact_coverage，并提交完整 protagonist_projection；直到工具明确返回 simulated=true 才算完成。simulated=true 之前严禁调用 plan_structure、plan_details、draft_chapter、read_chapter 或 craft_recall，plan_details 在此阶段必然被拒绝。模拟 finalize 后工具会自动作废绑定旧 simulation ID 的 POV plan partial；请结束本回合，让 Router 下一轮重新创建 plan_structure。缺口摘要：%s",
		chapter, chapter, chapter, truncateForPipelineSteer(cause, 6000))
}

func pipelineStagedPlanRepairSteer(chapter int, cause string) string {
	return fmt.Sprintf("Pipeline staged-plan repair：第%d章已有 drafts/%02d.plan.partial.json，但收口为正式计划失败。请立即派 writer 修复，不写正文，不调用 craft_recall/read_chapter。先调用 novel_context(chapter=%d) 一次读取紧凑状态并严格执行 next_step。返工章的 rewrite_source 已直接注入当前终稿、正文 hash、完整 brief 和 preserve_facts：若 chapter_world_simulation 未完成或 invalid，分批调用 simulate_chapter_world，每批最多8名角色，让全部实名角色各自提交目标、压力、可选项、决定、决定理由、行动和蝴蝶效应，并用 rewrite_fact_coverage 逐条覆盖 preserve_facts 后 finalize；模拟更新或 structure_source_status=stale 时必须重新 plan_structure，不能沿用旧骨架。之后再调用 plan_details。POV plan 最小分组：batch1=world_simulation_id+protagonist_decision+project_promise+chapter_function+context_sources+initial_state+environment_state+causal_beats+decision_points+outcome_shift（initial_state 只覆盖主角；context_sources 必须含 rewrite_source 和 rewrite_brief 精确令牌），batch2=voice_logic+literary_rendering_plan+dialogue_scene_blueprints+emotional_logic+anti_ai_execution_plan+reader_entertainment_plan（literary_rendering_plan 只选本章有功能的镜头并保留 source_refs，不做九项清单；显式要求热梗时同时补 trend_language_plan），batch3=reader_reward_plan+reader_retention_plan+ending_consequence_contract（第一章长篇项目同时补 longform_opening）；返工章同时补 review_refinement，并将全部 preserve_facts 原样写入 preserve_constraints。最后调用 plan_details(chapter=%d, finalize=true)。缺项摘要：%s",
		chapter, chapter, chapter, chapter, truncateForPipelineSteer(cause, 6000))
}

func truncateForPipelineSteer(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 || len([]rune(s)) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + "..."
}

func chapterFromPlanPartialPath(path string) (int, bool) {
	name := filepath.Base(path)
	const suffix = ".plan.partial.json"
	if !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, suffix))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
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
		s = normalizePipelineStageName(s)
		if s == "" {
			continue
		}
		if !knownPipelineStages[s] {
			return nil, fmt.Errorf("未知阶段 %q（可用：cocreate / architect / outline-all / zero-init / preplan / project-all / seal / promote / plan / render / write / review / rewrite / finalize / deliver）", s)
		}
		stages = append(stages, s)
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("--stages 为空")
	}
	outlineIndex := -1
	for i, stage := range stages {
		if stage != "outline-all" {
			continue
		}
		if outlineIndex >= 0 {
			return nil, fmt.Errorf("outline-all 只能在同一流水线阶段图中出现一次")
		}
		outlineIndex = i
	}
	if outlineIndex >= 0 {
		for i, stage := range stages {
			if stage == "architect" && i > outlineIndex {
				return nil, fmt.Errorf("architect 必须先于 outline-all")
			}
			switch stage {
			case "zero-init", "preplan", "project-all", "seal", "promote", "plan", "render", "write", "review", "rewrite", "deliver":
				if i < outlineIndex {
					return nil, fmt.Errorf("阶段 %s 不能先于 outline-all", stage)
				}
			}
		}
	}
	return stages, nil
}

func normalizePipelineStageName(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "zeroinit", "zero_init":
		return "zero-init"
	case "batch-plan", "batch_plan", "pre-plan":
		return "preplan"
	case "projectall", "project_all", "all-plan", "all_plan":
		return "project-all"
	case "outlineall", "outline_all", "full-outline", "full_outline":
		return "outline-all"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
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

// loadOrInitPipelineState 读取已有状态；--restart、阶段列表、显式创作指令、
// 模型/prompt 协议指纹或运行范围变化时重置，避免旧产物被当成新输入的完成证据。
func loadOrInitPipelineState(
	path string,
	stages []string,
	prompt, inputDigest, runIdentity string,
	restart bool,
) (*domain.PipelineState, error) {
	fresh := &domain.PipelineState{
		Stages:      stages,
		Prompt:      prompt,
		InputDigest: inputDigest,
		RunIdentity: runIdentity,
	}
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
		next := &domain.PipelineState{
			Stages:      stages,
			Prompt:      prev.Prompt,
			InputDigest: inputDigest,
			RunIdentity: runIdentity,
		}
		if prompt != "" {
			next.Prompt = prompt
		}
		return next, nil
	}
	// 命令行显式给出不同 prompt 是新的 pipeline 输入，必须失效旧完成图。
	if prompt != "" && prompt != prev.Prompt {
		fmt.Fprintln(os.Stderr, "[pipeline] 创作指令已变化，重置阶段证据")
		return fresh, nil
	}
	if prev.InputDigest != "" && inputDigest != "" && prev.InputDigest != inputDigest {
		fmt.Fprintln(os.Stderr, "[pipeline] 模型或 prompt 协议指纹已变化，重置阶段证据")
		if prompt == "" {
			fresh.Prompt = prev.Prompt
		}
		return fresh, nil
	}
	if prev.RunIdentity != "" && runIdentity != "" && prev.RunIdentity != runIdentity {
		fmt.Fprintln(os.Stderr, "[pipeline] --from/--to/--budget 等运行范围已变化，重置阶段证据")
		if prompt == "" {
			fresh.Prompt = prev.Prompt
		}
		return fresh, nil
	}
	// 旧 schema 首次升级时保留已验证产物并建立后续比较基线。
	prev.InputDigest = inputDigest
	prev.RunIdentity = runIdentity
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
	// 唯一临时文件 + fsync + rename，避免并发/崩溃留下半份状态。
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pipeline-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func pipelineRunInputDigest(cfg bootstrap.Config, bundle assets.Bundle) string {
	brainstormSHA := ""
	brainstormPath := filepath.Clean(filepath.Join(cfg.OutputDir, "..", "..", "brainstorm.md"))
	if body, err := os.ReadFile(brainstormPath); err == nil {
		sum := sha256.Sum256(body)
		brainstormSHA = hex.EncodeToString(sum[:])
	}
	payload, _ := json.Marshal(struct {
		Schema          string
		Provider        string
		Model           string
		ReasoningEffort string
		Style           string
		Roles           map[string]bootstrap.RoleConfig
		Prompts         assets.Prompts
		References      tools.References
		Styles          map[string]string
		BrainstormSHA   string
	}{
		Schema:          "pipeline-input-v3-20260716",
		Provider:        cfg.Provider,
		Model:           cfg.ModelName,
		ReasoningEffort: cfg.ReasoningEffort,
		Style:           cfg.Style,
		Roles:           cfg.Roles,
		Prompts:         bundle.Prompts,
		References:      bundle.References,
		Styles:          bundle.Styles,
		BrainstormSHA:   brainstormSHA,
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pipelineProjectAllInputDigest is intentionally narrower than the legacy
// whole-pipeline digest. Editor/reviewer/drafter changes do not alter a world
// simulation or POV plan and therefore must not invalidate an expensive sealed
// full-book projection.
func pipelineProjectAllInputDigest(cfg bootstrap.Config, bundle assets.Bundle) string {
	writer := resolvedPipelineRoleConfig(cfg, "writer")
	contextWindow, _ := cfg.ResolveContextWindow(writer.Model)
	payload, _ := json.Marshal(struct {
		Schema          string
		Provider        string
		Model           string
		ReasoningEffort string
		ContextWindow   int
		Role            bootstrap.RoleConfig
		Style           string
		PlannerPrompt   string
		AgentProtocol   string
		References      tools.References
		Styles          map[string]string
		Embedding       struct {
			Enabled   bool   `json:"enabled"`
			LocalGGUF string `json:"local_gguf"`
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			BaseURL   string `json:"base_url"`
		} `json:"embedding"`
		SeedContract string
	}{
		Schema:          "project-all-input-v1-20260716",
		Provider:        cfg.Provider,
		Model:           cfg.ModelName,
		ReasoningEffort: cfg.ReasoningEffort,
		ContextWindow:   contextWindow,
		Role:            writer,
		Style:           cfg.Style,
		PlannerPrompt:   bundle.Prompts.Planner,
		AgentProtocol:   agents.ProjectAllPlanningProtocolDigest(bundle.Prompts.Planner),
		References:      bundle.References,
		Styles:          bundle.Styles,
		Embedding: struct {
			Enabled   bool   `json:"enabled"`
			LocalGGUF string `json:"local_gguf"`
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			BaseURL   string `json:"base_url"`
		}{
			Enabled:   cfg.RAG.Embedding.Enabled,
			LocalGGUF: cfg.RAG.Embedding.LocalGGUF,
			Provider:  cfg.RAG.Embedding.Provider,
			Model:     cfg.RAG.Embedding.Model,
			BaseURL:   cfg.RAG.Embedding.BaseURL,
		},
		SeedContract: pipelineProjectAllSeedContract,
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pipelineRenderInputDigest binds every model/prompt/protocol that can change
// prose bytes before the candidate is committed. Post-write Editor review is
// separately exact-body bound and does not belong to this digest.
func pipelineRenderInputDigest(cfg bootstrap.Config, bundle assets.Bundle) string {
	drafter := resolvedPipelineRoleConfig(cfg, "drafter")
	coordinator := resolvedPipelineRoleConfig(cfg, "coordinator")
	reviewer := resolvedPipelineRoleConfig(cfg, "reviewer")
	contextWindow, _ := cfg.ResolveContextWindow(drafter.Model)
	coordinatorContextWindow, _ := cfg.ResolveContextWindow(coordinator.Model)
	selectedStyle := bundle.Styles[cfg.Style]
	payload, _ := json.Marshal(struct {
		Schema                   string
		Provider                 string
		Model                    string
		ReasoningEffort          string
		ContextWindow            int
		Role                     bootstrap.RoleConfig
		CoordinatorRole          bootstrap.RoleConfig
		CoordinatorContextWindow int
		ReviewerRole             bootstrap.RoleConfig
		Style                    string
		StyleBody                string
		DrafterPrompt            string
		CoordinatorPrompt        string
		SamplingProtocol         string
		StrictPrimaryModels      bool
		RenderToolProtocol       string
	}{
		Schema:                   "sealed-render-input-v3-20260720",
		Provider:                 cfg.Provider,
		Model:                    cfg.ModelName,
		ReasoningEffort:          cfg.ReasoningEffort,
		ContextWindow:            contextWindow,
		Role:                     drafter,
		CoordinatorRole:          coordinator,
		CoordinatorContextWindow: coordinatorContextWindow,
		ReviewerRole:             reviewer,
		Style:                    cfg.Style,
		StyleBody:                selectedStyle,
		DrafterPrompt:            bundle.Prompts.Drafter,
		CoordinatorPrompt:        bundle.Prompts.Coordinator,
		SamplingProtocol:         writersampler.ProtocolDigest(),
		StrictPrimaryModels:      true,
		RenderToolProtocol:       "frozen-render-tools.v3:no-planner,no-live-rag,no-web;draft,read,check,commit;server-owned-hidden-delta;anti_ai_render_contract-v1-prospective",
	})
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func resolvedPipelineRoleConfig(cfg bootstrap.Config, role string) bootstrap.RoleConfig {
	if configured, ok := cfg.Roles[role]; ok {
		return configured
	}
	if role == "drafter" {
		if writer, ok := cfg.Roles["writer"]; ok {
			return writer
		}
	}
	return bootstrap.RoleConfig{
		Provider:        cfg.Provider,
		Model:           cfg.ModelName,
		ReasoningEffort: cfg.ReasoningEffort,
	}
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
