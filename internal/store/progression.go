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
	chapterProgressJSON = "meta/chapter_progress.json"
	chapterProgressMD   = "meta/chapter_progress.md"
)

// LoadChapterProgressLedger reads the durable chapter-progress ledger.
func (s *Store) LoadChapterProgressLedger() (*domain.ChapterProgressLedger, error) {
	var ledger domain.ChapterProgressLedger
	if err := s.Progress.io.ReadJSON(chapterProgressJSON, &ledger); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ledger, nil
}

// RefreshChapterProgressLedger rebuilds the chapter-progress ledger from
// already committed facts. It is deterministic and safe to replay after each
// accepted chapter review or as a one-shot backfill for older runs.
func (s *Store) RefreshChapterProgressLedger(acceptedChapter int, acceptedReview *domain.ReviewEntry) (*domain.ChapterProgressLedger, error) {
	progress, err := s.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return nil, fmt.Errorf("missing progress")
	}

	protagonist := s.inferProtagonist()
	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)

	outlineByChapter, positionByChapter, err := s.chapterOutlineMaps()
	if err != nil {
		return nil, err
	}
	timeline, err := s.World.LoadTimeline()
	if err != nil {
		return nil, fmt.Errorf("load timeline: %w", err)
	}
	stateChanges, err := s.World.LoadStateChanges()
	if err != nil {
		return nil, fmt.Errorf("load state changes: %w", err)
	}
	relationships, err := s.World.LoadRelationships()
	if err != nil {
		return nil, fmt.Errorf("load relationships: %w", err)
	}
	resources, err := s.resourcesByChapter()
	if err != nil {
		return nil, err
	}

	now := time.Now().Format(time.RFC3339)
	ledger := &domain.ChapterProgressLedger{
		Version:           1,
		NovelName:         progress.NovelName,
		GeneratedAt:       now,
		Protagonist:       protagonist,
		CompletedChapters: completed,
		TotalChapters:     progress.TotalChapters,
		CurrentChapter:    progress.CurrentChapter,
		CurrentVolume:     progress.CurrentVolume,
		CurrentArc:        progress.CurrentArc,
	}
	characterContinuity, err := s.refreshCharacterContinuityLedger(progress, outlineByChapter, positionByChapter, completed, now)
	if err != nil {
		return nil, fmt.Errorf("refresh character continuity: %w", err)
	}

	for _, ch := range completed {
		summary, err := s.Summaries.LoadSummary(ch)
		if err != nil {
			return nil, fmt.Errorf("load summary ch%d: %w", ch, err)
		}
		entry := domain.ChapterProgressEntry{
			Chapter:             ch,
			Position:            positionByChapter[ch],
			ReviewStatus:        "review_missing",
			TimelineEvents:      timelineForChapter(timeline, ch),
			StateChanges:        stateChangesForChapter(stateChanges, ch),
			ResourceChanges:     resources[ch],
			RelationshipChanges: relationshipsForChapter(relationships, ch),
			UpdatedAt:           now,
		}
		if outline, ok := outlineByChapter[ch]; ok {
			entry.Title = outline.Title
			entry.OutlineCoreEvent = outline.CoreEvent
			entry.OutlineHook = outline.Hook
		}
		if summary != nil {
			entry.Summary = summary.Summary
			entry.KeyEvents = append([]string(nil), summary.KeyEvents...)
		}
		for _, change := range entry.StateChanges {
			if protagonist != "" && change.Entity == protagonist {
				entry.ProtagonistChanges = append(entry.ProtagonistChanges, change)
			}
		}
		if len(entry.ProtagonistChanges) == 0 {
			entry.ProtagonistChanges = derivedProtagonistChanges(protagonist, entry)
		}
		review := acceptedReview
		if ch != acceptedChapter || review == nil || review.Scope != "chapter" {
			review, err = s.World.LoadReview(ch)
			if err != nil {
				return nil, fmt.Errorf("load review ch%d: %w", ch, err)
			}
		}
		if review != nil {
			entry.ReviewStatus = review.Verdict
			entry.ReviewSummary = review.Summary
		}
		ledger.Entries = append(ledger.Entries, entry)
	}

	ledger.NextPlan = s.buildNextChapterProgressPlan(progress, outlineByChapter, positionByChapter, protagonist, stateChanges, timeline, characterContinuity)
	if err := s.writeChapterProgressLedger(ledger); err != nil {
		return nil, err
	}
	projectLedger, err := s.RefreshProjectProgressLedger(ledger, characterContinuity)
	if err != nil {
		return nil, fmt.Errorf("refresh project progress: %w", err)
	}
	if _, err := s.RefreshEvolutionReport(ledger, projectLedger); err != nil {
		return nil, fmt.Errorf("refresh evolution report: %w", err)
	}
	return ledger, nil
}

func (s *Store) writeChapterProgressLedger(ledger *domain.ChapterProgressLedger) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(chapterProgressJSON, ledger); err != nil {
			return err
		}
		return s.Progress.io.WriteMarkdownUnlocked(chapterProgressMD, renderChapterProgressLedger(ledger))
	})
}

func (s *Store) inferProtagonist() string {
	chars, err := s.Characters.Load()
	if err == nil {
		for _, c := range chars {
			if c.Tier == "core" || strings.Contains(c.Role, "主角") {
				return c.Name
			}
		}
		if len(chars) > 0 {
			return chars[0].Name
		}
	}
	changes, err := s.World.LoadStateChanges()
	if err != nil || len(changes) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, c := range changes {
		if c.Entity != "" {
			counts[c.Entity]++
		}
	}
	best := ""
	for name, n := range counts {
		if best == "" || n > counts[best] {
			best = name
		}
	}
	return best
}

func (s *Store) chapterOutlineMaps() (map[int]domain.OutlineEntry, map[int]domain.ChapterPosition, error) {
	outlineByChapter := map[int]domain.OutlineEntry{}
	positionByChapter := map[int]domain.ChapterPosition{}

	flat, err := s.Outline.LoadOutline()
	if err != nil {
		return nil, nil, fmt.Errorf("load outline: %w", err)
	}
	for _, e := range flat {
		outlineByChapter[e.Chapter] = e
	}

	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, nil, fmt.Errorf("load layered outline: %w", err)
	}
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for i, e := range a.Chapters {
				globalChapter := ch + i
				e.Chapter = globalChapter
				outlineByChapter[globalChapter] = e
				positionByChapter[globalChapter] = domain.ChapterPosition{
					Volume:      v.Index,
					VolumeTitle: v.Title,
					Arc:         a.Index,
					ArcTitle:    a.Title,
					ArcGoal:     a.Goal,
				}
			}
			// 尚未展开的骨架弧同样占用稳定的全局章号区间。
			// 否则后方已展开弧会在前方骨架展开后整体漂移。
			ch += a.ChapterSpan()
		}
	}
	return outlineByChapter, positionByChapter, nil
}

func (s *Store) resourcesByChapter() (map[int][]domain.ResourceClaim, error) {
	out := map[int][]domain.ResourceClaim{}
	ledger, err := s.ResourceLedger.Load()
	if err != nil {
		return nil, fmt.Errorf("load resource ledger: %w", err)
	}
	if ledger == nil {
		return out, nil
	}
	for _, claim := range ledger.Claims {
		if claim.Chapter > 0 {
			out[claim.Chapter] = append(out[claim.Chapter], claim)
		}
	}
	return out, nil
}

func (s *Store) buildNextChapterProgressPlan(
	progress *domain.Progress,
	outlineByChapter map[int]domain.OutlineEntry,
	positionByChapter map[int]domain.ChapterPosition,
	protagonist string,
	allStateChanges []domain.StateChange,
	timeline []domain.TimelineEvent,
	characterContinuity *domain.CharacterContinuityLedger,
) *domain.NextChapterProgressPlan {
	next := progress.NextChapter()
	if next <= 0 || (progress.TotalChapters > 0 && next > progress.TotalChapters) {
		return nil
	}
	outline, ok := outlineByChapter[next]
	if !ok {
		return &domain.NextChapterProgressPlan{
			Chapter: next,
			PlanningInstructions: []string{
				"下一章不在当前 outline.json/layered_outline.json 中；先让 architect_long expand_arc 或 append_volume，再写正文。",
				"人物回归规划仍可作为后续扩弧参考，但人物是否回归不作为章级审核要素。",
			},
		}
	}
	plan := &domain.NextChapterProgressPlan{
		Chapter:                  next,
		Title:                    outline.Title,
		Position:                 positionByChapter[next],
		CoreEvent:                outline.CoreEvent,
		Hook:                     outline.Hook,
		RequiredBeats:            appendRequiredBeats(outline),
		RecentProtagonistChanges: recentProtagonistChanges(allStateChanges, protagonist, next, 5),
		RecentTimeline:           recentTimelineBefore(timeline, next, 5),
		PlanningInstructions: []string{
			"写第 N 章前先用本 next_plan 生成或核对 drafts/NN.plan.json，不能只沿用旧章提示词。",
			"本章必须让 core_event 至少产生一个可入账事实：时间线、人物状态、资源账本、关系或伏笔推进。",
			"如果正文偏离当前大纲，commit_chapter.feedback 必须写明偏离和后续大纲调整建议。",
			"章节 commit 后仍需 save_review(scope=chapter, verdict=accept)；accept 会刷新本进度台账并重算下一章计划。",
			"人物续用参考只提示可回归/可露脸/需保留状态的人物；不合适本章就沉淀到后续，不能硬塞为审核要素。",
		},
	}
	for _, c := range plan.RecentProtagonistChanges {
		plan.ContinuityInputs = append(plan.ContinuityInputs,
			fmt.Sprintf("%s：%s -> %s", c.Field, c.OldValue, c.NewValue))
	}
	if active, err := s.World.LoadActiveForeshadow(); err == nil {
		if len(active) > 8 {
			active = active[:8]
		}
		plan.ActiveForeshadow = active
		for _, f := range active {
			plan.ContinuityInputs = append(plan.ContinuityInputs, "未回收伏笔："+f.ID)
		}
	}
	if ledger, err := s.ResourceLedger.Load(); err == nil && ledger != nil {
		for _, claim := range ledger.Claims {
			if len(plan.ResourceFocus) >= 8 {
				break
			}
			if claim.Status == "pending" || claim.Chapter >= max(1, next-3) {
				plan.ResourceFocus = append(plan.ResourceFocus, claim)
				plan.ContinuityInputs = append(plan.ContinuityInputs, "资源账本："+claim.Name+"("+claim.Status+")")
			}
		}
	}
	if characterContinuity != nil {
		plan.CharacterContinuity = append([]domain.CharacterHint(nil), characterContinuity.NextChapterFocus...)
		for _, hint := range plan.CharacterContinuity {
			label := "人物续用参考：" + hint.Name + "(" + hint.UsageType + ")"
			if hint.Suggestion != "" {
				label += "：" + compactProgressText(hint.Suggestion, 120)
			}
			plan.ContinuityInputs = append(plan.ContinuityInputs, label)
		}
	}
	return plan
}

func appendRequiredBeats(outline domain.OutlineEntry) []string {
	var beats []string
	if strings.TrimSpace(outline.CoreEvent) != "" {
		beats = append(beats, outline.CoreEvent)
	}
	beats = append(beats, outline.Scenes...)
	return beats
}

func timelineForChapter(events []domain.TimelineEvent, chapter int) []domain.TimelineEvent {
	var out []domain.TimelineEvent
	for _, e := range events {
		if e.Chapter == chapter {
			out = append(out, e)
		}
	}
	return out
}

func stateChangesForChapter(changes []domain.StateChange, chapter int) []domain.StateChange {
	var out []domain.StateChange
	for _, c := range changes {
		if c.Chapter == chapter {
			out = append(out, c)
		}
	}
	return out
}

func relationshipsForChapter(relationships []domain.RelationshipEntry, chapter int) []domain.RelationshipEntry {
	var out []domain.RelationshipEntry
	for _, r := range relationships {
		if r.Chapter == chapter {
			out = append(out, r)
		}
	}
	return out
}

func derivedProtagonistChanges(protagonist string, entry domain.ChapterProgressEntry) []domain.StateChange {
	if protagonist == "" {
		return nil
	}
	if len(entry.ResourceChanges) > 0 {
		return []domain.StateChange{{
			Chapter:  entry.Chapter,
			Entity:   protagonist,
			Field:    "本章资源/权限推进（派生）",
			NewValue: "本章入账或更新：" + compactProgressText(renderResourceInline(entry.ResourceChanges), 160),
			Reason:   "chapter_progress 根据资源账本回填，供续写时追踪主角阶段变化",
		}}
	}
	if text := firstNonEmpty(entry.Summary, strings.Join(entry.KeyEvents, "；"), entry.OutlineCoreEvent); text != "" {
		return []domain.StateChange{{
			Chapter:  entry.Chapter,
			Entity:   protagonist,
			Field:    "本章行动/风险推进（派生）",
			NewValue: compactProgressText(text, 160),
			Reason:   "chapter_progress 根据章节摘要回填，供续写时追踪主角阶段变化",
		}}
	}
	return nil
}

func recentProtagonistChanges(changes []domain.StateChange, protagonist string, beforeChapter, limit int) []domain.StateChange {
	if protagonist == "" || limit <= 0 {
		return nil
	}
	var out []domain.StateChange
	for i := len(changes) - 1; i >= 0 && len(out) < limit; i-- {
		c := changes[i]
		if c.Chapter < beforeChapter && c.Entity == protagonist {
			out = append(out, c)
		}
	}
	reverseStateChanges(out)
	return out
}

func recentTimelineBefore(events []domain.TimelineEvent, beforeChapter, limit int) []domain.TimelineEvent {
	if limit <= 0 {
		return nil
	}
	var out []domain.TimelineEvent
	for i := len(events) - 1; i >= 0 && len(out) < limit; i-- {
		if events[i].Chapter < beforeChapter {
			out = append(out, events[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func reverseStateChanges(changes []domain.StateChange) {
	for i, j := 0, len(changes)-1; i < j; i, j = i+1, j-1 {
		changes[i], changes[j] = changes[j], changes[i]
	}
}

func renderChapterProgressLedger(ledger *domain.ChapterProgressLedger) string {
	var b strings.Builder
	b.WriteString("# 章节推进与人物变化台账\n\n")
	if ledger.NovelName != "" {
		fmt.Fprintf(&b, "- 书名：%s\n", ledger.NovelName)
	}
	if ledger.Protagonist != "" {
		fmt.Fprintf(&b, "- 主角追踪对象：%s\n", ledger.Protagonist)
	}
	fmt.Fprintf(&b, "- 已完成章节：%d/%d\n", len(ledger.CompletedChapters), ledger.TotalChapters)
	if ledger.CurrentChapter > 0 {
		fmt.Fprintf(&b, "- 当前工程章节：第 %d 章\n", ledger.CurrentChapter)
	}
	if ledger.CurrentVolume > 0 || ledger.CurrentArc > 0 {
		fmt.Fprintf(&b, "- 当前卷弧：V%d A%d\n", ledger.CurrentVolume, ledger.CurrentArc)
	}
	fmt.Fprintf(&b, "- 生成时间：%s\n\n", ledger.GeneratedAt)

	b.WriteString("## 每章推进表\n\n")
	b.WriteString("| 章 | 位置 | 审阅 | 主线推进 | 主角变化 |\n")
	b.WriteString("|---:|---|---|---|---|\n")
	for _, e := range ledger.Entries {
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n",
			e.Chapter,
			escapeTable(positionLabel(e.Position)),
			escapeTable(e.ReviewStatus),
			escapeTable(firstNonEmpty(e.Summary, strings.Join(e.KeyEvents, "；"), e.OutlineCoreEvent, "缺结构化摘要")),
			escapeTable(renderStateChangeInline(e.ProtagonistChanges, "无结构化主角变化记录")),
		)
	}
	b.WriteString("\n")

	for _, e := range ledger.Entries {
		fmt.Fprintf(&b, "## 第 %d 章", e.Chapter)
		if e.Title != "" {
			fmt.Fprintf(&b, "：%s", e.Title)
		}
		b.WriteString("\n\n")
		if e.Summary != "" {
			fmt.Fprintf(&b, "- 摘要：%s\n", e.Summary)
		}
		if e.OutlineCoreEvent != "" {
			fmt.Fprintf(&b, "- 大纲核心事件：%s\n", e.OutlineCoreEvent)
		}
		if len(e.KeyEvents) > 0 {
			fmt.Fprintf(&b, "- 关键事件：%s\n", strings.Join(e.KeyEvents, "；"))
		}
		fmt.Fprintf(&b, "- 主角变化：%s\n", renderStateChangeInline(e.ProtagonistChanges, "无结构化主角变化记录"))
		if len(e.ResourceChanges) > 0 {
			fmt.Fprintf(&b, "- 资源变化：%s\n", renderResourceInline(e.ResourceChanges))
		}
		if e.ReviewSummary != "" {
			fmt.Fprintf(&b, "- 审阅摘要：%s\n", e.ReviewSummary)
		}
		b.WriteString("\n")
	}

	if ledger.NextPlan != nil {
		b.WriteString("## 下一章动态计划\n\n")
		p := ledger.NextPlan
		fmt.Fprintf(&b, "- 目标章节：第 %d 章", p.Chapter)
		if p.Title != "" {
			fmt.Fprintf(&b, "《%s》", p.Title)
		}
		b.WriteString("\n")
		if label := positionLabel(p.Position); label != "" {
			fmt.Fprintf(&b, "- 位置：%s\n", label)
		}
		if p.CoreEvent != "" {
			fmt.Fprintf(&b, "- 核心事件：%s\n", p.CoreEvent)
		}
		if p.Hook != "" {
			fmt.Fprintf(&b, "- 章尾钩子目标：%s\n", p.Hook)
		}
		if len(p.RequiredBeats) > 0 {
			fmt.Fprintf(&b, "- 必须推进：%s\n", strings.Join(p.RequiredBeats, "；"))
		}
		if len(p.ContinuityInputs) > 0 {
			fmt.Fprintf(&b, "- 连续性输入：%s\n", strings.Join(p.ContinuityInputs, "；"))
		}
		if len(p.CharacterContinuity) > 0 {
			b.WriteString("- 人物续用参考（非审核项）：\n")
			for _, item := range p.CharacterContinuity {
				fmt.Fprintf(&b, "  - %s [%s]：%s", item.Name, item.UsageType, item.Suggestion)
				if item.Evidence != "" {
					fmt.Fprintf(&b, "（证据：%s）", item.Evidence)
				}
				b.WriteString("\n")
			}
		}
		if len(p.PlanningInstructions) > 0 {
			b.WriteString("- 写作指令：\n")
			for _, item := range p.PlanningInstructions {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func positionLabel(pos domain.ChapterPosition) string {
	var parts []string
	if pos.Volume > 0 {
		label := fmt.Sprintf("V%d", pos.Volume)
		if pos.VolumeTitle != "" {
			label += " " + pos.VolumeTitle
		}
		parts = append(parts, label)
	}
	if pos.Arc > 0 {
		label := fmt.Sprintf("A%d", pos.Arc)
		if pos.ArcTitle != "" {
			label += " " + pos.ArcTitle
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " / ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func renderStateChangeInline(changes []domain.StateChange, empty string) string {
	if len(changes) == 0 {
		return empty
	}
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.OldValue != "" {
			parts = append(parts, fmt.Sprintf("%s：%s -> %s", c.Field, c.OldValue, c.NewValue))
		} else {
			parts = append(parts, fmt.Sprintf("%s：%s", c.Field, c.NewValue))
		}
	}
	return strings.Join(parts, "；")
}

func renderResourceInline(claims []domain.ResourceClaim) string {
	parts := make([]string, 0, len(claims))
	for _, c := range claims {
		label := c.Name
		if c.Status != "" {
			label += "(" + c.Status + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "；")
}

func compactProgressText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

func escapeTable(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}
