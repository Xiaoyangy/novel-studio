package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// WorldStore 管理时间线、伏笔、人物关系、状态变化、世界规则、风格规则、审阅和交接。
type WorldStore struct{ io *IO }

func NewWorldStore(io *IO) *WorldStore { return &WorldStore{io: io} }

// ── 时间线 ──

// SaveTimeline 全量写入 timeline.json + timeline.md（原子写入）。
func (s *WorldStore) SaveTimeline(events []domain.TimelineEvent) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("timeline.json", events); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("timeline.md", renderTimeline(events))
	})
}

// LoadTimeline 读取时间线。
func (s *WorldStore) LoadTimeline() ([]domain.TimelineEvent, error) {
	var events []domain.TimelineEvent
	if err := s.io.ReadJSON("timeline.json", &events); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return events, nil
}

// AppendTimelineEvents 追加时间线事件。同一事件重复提交时按稳定 key 去重，保证
// commit_chapter 崩溃后重跑不会污染时间线。
func (s *WorldStore) AppendTimelineEvents(newEvents []domain.TimelineEvent) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.TimelineEvent
		if err := s.io.ReadJSONUnlocked("timeline.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		seen := make(map[string]struct{}, len(existing)+len(newEvents))
		for _, e := range existing {
			seen[timelineEventKey(e)] = struct{}{}
		}
		all := existing
		for _, e := range newEvents {
			key := timelineEventKey(e)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, e)
		}
		if err := s.io.WriteJSONUnlocked("timeline.json", all); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("timeline.md", renderTimeline(all))
	})
}

// ReplaceTimelineEventsForChapter replaces all timeline events for one chapter.
// Rewrite commits use this to keep chapter-local world state aligned with the
// rewritten body instead of leaving stale events beside the new ones.
func (s *WorldStore) ReplaceTimelineEventsForChapter(chapter int, newEvents []domain.TimelineEvent) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.TimelineEvent
		if err := s.io.ReadJSONUnlocked("timeline.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		filtered := existing[:0]
		for _, event := range existing {
			if event.Chapter != chapter {
				filtered = append(filtered, event)
			}
		}
		for _, event := range newEvents {
			event.Chapter = chapter
			filtered = append(filtered, event)
		}
		if err := s.io.WriteJSONUnlocked("timeline.json", filtered); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("timeline.md", renderTimeline(filtered))
	})
}

// LoadRecentTimeline 返回最近 window 章内的时间线事件。
func (s *WorldStore) LoadRecentTimeline(current, window int) ([]domain.TimelineEvent, error) {
	all, err := s.LoadTimeline()
	if err != nil {
		return nil, err
	}
	minCh := max(current-window, 1)
	var filtered []domain.TimelineEvent
	for _, e := range all {
		if e.Chapter >= minCh {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ── 伏笔 ──

// SaveForeshadowLedger 全量写入 foreshadow_ledger.json + foreshadow_ledger.md（原子写入）。
func (s *WorldStore) SaveForeshadowLedger(entries []domain.ForeshadowEntry) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("foreshadow_ledger.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("foreshadow_ledger.md", renderForeshadow(entries))
	})
}

// LoadForeshadowLedger 读取伏笔账本。
func (s *WorldStore) LoadForeshadowLedger() ([]domain.ForeshadowEntry, error) {
	var entries []domain.ForeshadowEntry
	if err := s.io.ReadJSON("foreshadow_ledger.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// UpdateForeshadow 批量应用伏笔增量操作。
func (s *WorldStore) UpdateForeshadow(chapter int, updates []domain.ForeshadowUpdate) error {
	return s.io.WithWriteLock(func() error {
		var entries []domain.ForeshadowEntry
		if err := s.io.ReadJSONUnlocked("foreshadow_ledger.json", &entries); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		idx := make(map[string]int, len(entries))
		for i, e := range entries {
			idx[e.ID] = i
		}
		for _, u := range updates {
			switch u.Action {
			case "plant":
				if i, ok := idx[u.ID]; ok {
					if entries[i].Description == "" {
						entries[i].Description = u.Description
					}
					if entries[i].PlantedAt == 0 {
						entries[i].PlantedAt = chapter
					}
					if entries[i].Status == "" {
						entries[i].Status = "planted"
					}
					continue
				}
				idx[u.ID] = len(entries)
				entries = append(entries, domain.ForeshadowEntry{
					ID:          u.ID,
					Description: u.Description,
					PlantedAt:   chapter,
					Status:      "planted",
				})
			case "advance":
				if i, ok := idx[u.ID]; ok {
					entries[i].Status = "advanced"
				}
			case "resolve":
				if i, ok := idx[u.ID]; ok {
					entries[i].Status = "resolved"
					entries[i].ResolvedAt = chapter
				}
			}
		}
		if err := s.io.WriteJSONUnlocked("foreshadow_ledger.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("foreshadow_ledger.md", renderForeshadow(entries))
	})
}

// LoadActiveForeshadow 返回未回收的伏笔条目。
func (s *WorldStore) LoadActiveForeshadow() ([]domain.ForeshadowEntry, error) {
	all, err := s.LoadForeshadowLedger()
	if err != nil {
		return nil, err
	}
	var active []domain.ForeshadowEntry
	for _, e := range all {
		if e.Status != "resolved" {
			active = append(active, e)
		}
	}
	return active, nil
}

// ── 人物关系 ──

// SaveRelationships 全量写入 relationship_state.json + relationship_state.md（原子写入）。
func (s *WorldStore) SaveRelationships(entries []domain.RelationshipEntry) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("relationship_state.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("relationship_state.md", renderRelationships(entries))
	})
}

// LoadRelationships 读取人物关系状态。
func (s *WorldStore) LoadRelationships() ([]domain.RelationshipEntry, error) {
	var entries []domain.RelationshipEntry
	if err := s.io.ReadJSON("relationship_state.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// UpdateRelationships 合并关系变化。
func (s *WorldStore) UpdateRelationships(changes []domain.RelationshipEntry) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.RelationshipEntry
		if err := s.io.ReadJSONUnlocked("relationship_state.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		idx := make(map[string]int, len(existing))
		for i, e := range existing {
			idx[pairKey(e.CharacterA, e.CharacterB)] = i
		}
		for _, c := range changes {
			key := pairKey(c.CharacterA, c.CharacterB)
			if i, ok := idx[key]; ok {
				existing[i].Relation = c.Relation
				existing[i].Chapter = c.Chapter
			} else {
				idx[key] = len(existing)
				existing = append(existing, c)
			}
		}
		if err := s.io.WriteJSONUnlocked("relationship_state.json", existing); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("relationship_state.md", renderRelationships(existing))
	})
}

// ── 状态变化 ──

// AppendStateChanges 追加角色状态变化。同一状态变化重复提交时按稳定 key 去重。
func (s *WorldStore) AppendStateChanges(changes []domain.StateChange) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.StateChange
		if err := s.io.ReadJSONUnlocked("meta/state_changes.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		seen := make(map[string]struct{}, len(existing)+len(changes))
		for _, c := range existing {
			seen[stateChangeKey(c)] = struct{}{}
		}
		all := existing
		for _, c := range changes {
			key := stateChangeKey(c)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, c)
		}
		// Task 073：同 fact_key 旧记录自动补 superseded_by 链（合法演化 vs 矛盾的依据）。
		all = domain.LinkSupersededChain(all)
		return s.io.WriteJSONUnlocked("meta/state_changes.json", all)
	})
}

// ReplaceStateChangesForChapter replaces all state changes for one chapter.
func (s *WorldStore) ReplaceStateChangesForChapter(chapter int, changes []domain.StateChange) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.StateChange
		if err := s.io.ReadJSONUnlocked("meta/state_changes.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		filtered := existing[:0]
		for _, change := range existing {
			if change.Chapter != chapter {
				filtered = append(filtered, change)
			}
		}
		for _, change := range changes {
			change.Chapter = chapter
			filtered = append(filtered, change)
		}
		return s.io.WriteJSONUnlocked("meta/state_changes.json", filtered)
	})
}

// LoadStateChanges 读取全部状态变化记录。
func (s *WorldStore) LoadStateChanges() ([]domain.StateChange, error) {
	var changes []domain.StateChange
	if err := s.io.ReadJSON("meta/state_changes.json", &changes); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return changes, nil
}

// ── 世界规则 ──

// SaveWorldRules 全量写入 world_rules.json + world_rules.md（原子写入）。
func (s *WorldStore) SaveWorldRules(rules []domain.WorldRule) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("world_rules.json", rules); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("world_rules.md", renderWorldRules(rules))
	})
}

// LoadWorldRules 读取世界规则。
func (s *WorldStore) LoadWorldRules() ([]domain.WorldRule, error) {
	var rules []domain.WorldRule
	if err := s.io.ReadJSON("world_rules.json", &rules); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return rules, nil
}

// ── 本书世界 ──

// SaveBookWorld 保存地图、地点和势力图谱。
func (s *WorldStore) SaveBookWorld(world domain.BookWorld) error {
	if world.Version == 0 {
		world.Version = 1
	}
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("book_world.json", world); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("book_world.md", renderBookWorld(world))
	})
}

// LoadBookWorld 读取本书世界资产。
func (s *WorldStore) LoadBookWorld() (*domain.BookWorld, error) {
	var world domain.BookWorld
	if err := s.io.ReadJSON("book_world.json", &world); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &world, nil
}

// ── 风格规则 ──

// SaveStyleRules 保存写作风格规则。
func (s *WorldStore) SaveStyleRules(rules domain.WritingStyleRules) error {
	return s.io.WriteJSON("meta/style_rules.json", rules)
}

// LoadStyleRules 读取写作风格规则。
func (s *WorldStore) LoadStyleRules() (*domain.WritingStyleRules, error) {
	var rules domain.WritingStyleRules
	if err := s.io.ReadJSON("meta/style_rules.json", &rules); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rules, nil
}

// ── 审阅 ──

// SaveReview 保存审阅结果。
func (s *WorldStore) SaveReview(r domain.ReviewEntry) error {
	if r.Scope == "chapter" && strings.TrimSpace(r.BodySHA256) == "" {
		if body, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", r.Chapter)); err == nil && len(body) > 0 {
			r.BodySHA256 = chapterBodySHA256(body)
		}
	}
	if r.Scope == "global" && strings.TrimSpace(r.BookBodySHA256) == "" {
		// Keep the low-level store permissive for imported historical reviews, but
		// bind every normal global review when the complete manuscript is present.
		if digest, err := s.CurrentBookBodySHA256(r.Chapter); err == nil {
			r.BookBodySHA256 = digest
		}
	}
	return s.io.WriteJSON(reviewPath(r.Chapter, r.Scope), r)
}

// HasArcReview 检查指定章节（弧末章）是否已保存 scope=arc 的评审。
// 读失败按"未保存"处理，让 Router 倾向于重派而不是跳过。
func (s *WorldStore) HasArcReview(chapter int) bool {
	var rv domain.ReviewEntry
	err := s.io.ReadJSON(reviewPath(chapter, "arc"), &rv)
	return err == nil && rv.Scope == "arc"
}

// HasAcceptedChapterReview 检查指定章节是否已有通过的章级审阅。
// 读失败按"未通过"处理，让 Router 倾向于重派 editor，而不是跳过审阅。
func (s *WorldStore) HasAcceptedChapterReview(chapter int) bool {
	rv, err := s.LoadReview(chapter)
	if err != nil || rv == nil || rv.Scope != "chapter" || rv.Verdict != "accept" || strings.TrimSpace(rv.BodySHA256) == "" {
		return false
	}
	body, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", chapter))
	return err == nil && len(body) > 0 && rv.BodySHA256 == chapterBodySHA256(body)
}

func chapterBodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// HasAcceptedChapterReviews 检查一组已完成章节是否都已有通过的章级审阅。
func (s *WorldStore) HasAcceptedChapterReviews(chapters []int) bool {
	for _, ch := range chapters {
		if !s.HasAcceptedChapterReview(ch) {
			return false
		}
	}
	return true
}

// FirstUnacceptedChapterReview 返回第一章尚未通过章级审阅的已完成章节。
// 返回 0 表示全部已通过。
func (s *WorldStore) FirstUnacceptedChapterReview(chapters []int) int {
	for _, ch := range chapters {
		if !s.HasAcceptedChapterReview(ch) {
			return ch
		}
	}
	return 0
}

// CurrentBookBodySHA256 binds the exact ordered bytes of chapters 1..chapter.
// Length framing prevents two different chapter boundaries from producing the
// same concatenated byte stream.
func (s *WorldStore) CurrentBookBodySHA256(chapter int) (string, error) {
	if chapter <= 0 {
		return "", fmt.Errorf("book body hash requires a positive last chapter")
	}
	h := sha256.New()
	for ch := 1; ch <= chapter; ch++ {
		body, err := s.io.ReadFile(fmt.Sprintf("chapters/%02d.md", ch))
		if err != nil {
			return "", fmt.Errorf("read chapter %d for book body hash: %w", ch, err)
		}
		if len(body) == 0 {
			return "", fmt.Errorf("chapter %d is empty while hashing book body", ch)
		}
		_, _ = fmt.Fprintf(h, "%08d:%016d\n", ch, len(body))
		_, _ = h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HasCurrentGlobalReview checks that the review binds the exact current bytes
// of every chapter in its 1..chapter review range.
func (s *WorldStore) HasCurrentGlobalReview(chapter int) bool {
	var rv domain.ReviewEntry
	err := s.io.ReadJSON(reviewPath(chapter, "global"), &rv)
	if err != nil || rv.Scope != "global" || strings.TrimSpace(rv.BookBodySHA256) == "" {
		return false
	}
	current, err := s.CurrentBookBodySHA256(chapter)
	return err == nil && current == rv.BookBodySHA256
}

// HasAcceptedGlobalReview 检查指定章节锚点的全局/全文审阅是否已通过且仍绑定当前全文。
func (s *WorldStore) HasAcceptedGlobalReview(chapter int) bool {
	var rv domain.ReviewEntry
	err := s.io.ReadJSON(reviewPath(chapter, "global"), &rv)
	return err == nil && rv.Scope == "global" && rv.Verdict == "accept" &&
		s.HasCurrentGlobalReview(chapter)
}

// ClearChapterReview invalidates every current-version review artifact for a
// chapter. Review history and external detector history are retained, but no
// stale current report may survive a body rewrite and look deliverable.
func (s *WorldStore) ClearChapterReview(chapter int) error {
	for _, rel := range []string{
		fmt.Sprintf("reviews/%02d.json", chapter),
		fmt.Sprintf("reviews/%02d.md", chapter),
		fmt.Sprintf("reviews/%02d_ai_gate.json", chapter),
		fmt.Sprintf("reviews/%02d_ai_voice_redflags.json", chapter),
		fmt.Sprintf("reviews/%02d_deepseek_ai_judge.json", chapter),
		fmt.Sprintf("reviews/%02d_deepseek_ai_judge.md", chapter),
	} {
		if err := s.io.RemoveFile(rel); err != nil {
			return err
		}
	}
	return nil
}

// ClearGlobalReview 删除指定章节锚点的全局/全文审阅结果。
// 任意章节终稿变更后，最终全文审阅都需要重新生成。
func (s *WorldStore) ClearGlobalReview(chapter int) error {
	if chapter <= 0 {
		return nil
	}
	return s.io.RemoveFile(reviewPath(chapter, "global"))
}

// LoadReview 读取章节审阅结果。
func (s *WorldStore) LoadReview(chapter int) (*domain.ReviewEntry, error) {
	var r domain.ReviewEntry
	if err := s.io.ReadJSON(reviewPath(chapter, "chapter"), &r); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func reviewPath(chapter int, scope string) string {
	switch scope {
	case "arc":
		return fmt.Sprintf("reviews/%02d-arc.json", chapter)
	case "global":
		return fmt.Sprintf("reviews/%02d-global.json", chapter)
	default:
		return fmt.Sprintf("reviews/%02d.json", chapter)
	}
}

// LoadLastReview 读取最近一次全局审阅。
func (s *WorldStore) LoadLastReview(fromChapter int) (*domain.ReviewEntry, error) {
	for ch := fromChapter; ch >= 1; ch-- {
		var r domain.ReviewEntry
		if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d-global.json", ch), &r); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		return &r, nil
	}
	return nil, nil
}

// ── render helpers ──

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func timelineEventKey(e domain.TimelineEvent) string {
	chars := append([]string(nil), e.Characters...)
	slices.Sort(chars)
	return fmt.Sprintf("%d|%s|%s|%s", e.Chapter, e.Time, e.Event, strings.Join(chars, ","))
}

func stateChangeKey(c domain.StateChange) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s", c.Chapter, c.Entity, c.Field, c.OldValue, c.NewValue)
}

func renderTimeline(events []domain.TimelineEvent) string {
	var b strings.Builder
	b.WriteString("# 时间线\n\n")
	for _, e := range events {
		chars := ""
		if len(e.Characters) > 0 {
			chars = "（" + strings.Join(e.Characters, "、") + "）"
		}
		fmt.Fprintf(&b, "- **第 %d 章 [%s]**：%s%s\n", e.Chapter, e.Time, e.Event, chars)
	}
	return b.String()
}

func renderForeshadow(entries []domain.ForeshadowEntry) string {
	var b strings.Builder
	b.WriteString("# 伏笔账本\n\n")
	for _, e := range entries {
		status := e.Status
		if e.ResolvedAt > 0 {
			status = fmt.Sprintf("已回收（第 %d 章）", e.ResolvedAt)
		}
		fmt.Fprintf(&b, "- **[%s]** %s — 埋设于第 %d 章，状态：%s\n",
			e.ID, e.Description, e.PlantedAt, status)
	}
	return b.String()
}

func renderRelationships(entries []domain.RelationshipEntry) string {
	var b strings.Builder
	b.WriteString("# 人物关系\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- **%s ↔ %s**：%s（第 %d 章）\n",
			e.CharacterA, e.CharacterB, e.Relation, e.Chapter)
	}
	return b.String()
}

func renderWorldRules(rules []domain.WorldRule) string {
	grouped := make(map[string][]domain.WorldRule)
	var order []string
	for _, r := range rules {
		cat := r.Category
		if cat == "" {
			cat = "other"
		}
		if _, exists := grouped[cat]; !exists {
			order = append(order, cat)
		}
		grouped[cat] = append(grouped[cat], r)
	}

	var b strings.Builder
	b.WriteString("# 世界观规则\n\n")
	for _, cat := range order {
		fmt.Fprintf(&b, "## %s\n\n", cat)
		for _, r := range grouped[cat] {
			fmt.Fprintf(&b, "- **规则**：%s\n", r.Rule)
			if r.Boundary != "" {
				fmt.Fprintf(&b, "  - 边界：%s\n", r.Boundary)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderBookWorld(world domain.BookWorld) string {
	var b strings.Builder
	b.WriteString("# 本书世界\n\n")
	if world.Name != "" {
		fmt.Fprintf(&b, "- 名称：%s\n", world.Name)
	}
	if world.Summary != "" {
		fmt.Fprintf(&b, "- 摘要：%s\n", world.Summary)
	}
	if len(world.MapNotes) > 0 {
		b.WriteString("\n## 地图说明\n\n")
		for _, note := range world.MapNotes {
			fmt.Fprintf(&b, "- %s\n", note)
		}
	}
	if len(world.Places) > 0 {
		b.WriteString("\n## 地点\n\n")
		for _, p := range world.Places {
			fmt.Fprintf(&b, "- **%s**", p.Name)
			if p.Kind != "" {
				fmt.Fprintf(&b, "（%s）", p.Kind)
			}
			if p.Description != "" {
				fmt.Fprintf(&b, "：%s", p.Description)
			}
			b.WriteString("\n")
		}
	}
	if len(world.Routes) > 0 {
		b.WriteString("\n## 路线\n\n")
		for _, r := range world.Routes {
			fmt.Fprintf(&b, "- **%s → %s**", r.From, r.To)
			if r.Description != "" {
				fmt.Fprintf(&b, "：%s", r.Description)
			}
			if r.Risk != "" {
				fmt.Fprintf(&b, "（风险：%s）", r.Risk)
			}
			b.WriteString("\n")
		}
	}
	if len(world.Factions) > 0 {
		b.WriteString("\n## 势力\n\n")
		for _, f := range world.Factions {
			fmt.Fprintf(&b, "- **%s**", f.Name)
			if f.Goal != "" {
				fmt.Fprintf(&b, "：%s", f.Goal)
			}
			b.WriteString("\n")
			if len(f.Aliases) > 0 {
				fmt.Fprintf(&b, "  - 别名：%s\n", strings.Join(f.Aliases, "、"))
			}
			if f.Clock != nil {
				fmt.Fprintf(&b, "  - 进度钟：%d/%d；后果：%s\n", f.Clock.Progress, f.Clock.Segments, f.Clock.Consequence)
			}
			for _, rel := range f.Relations {
				fmt.Fprintf(&b, "  - %s → %s：%s\n", rel.Kind, rel.Target, rel.Note)
			}
		}
	}
	return b.String()
}

// ArchiveReviewHistory 把上一轮章级审阅归档到 reviews/{NN}.history.jsonl（append-only），
// 复审覆盖 reviews/{NN}.json 前调用——审核历史零丢失，供 Editor 回归验证与 diag 追溯。
func (s *WorldStore) ArchiveReviewHistory(prev domain.ReviewEntry) error {
	data, err := json.Marshal(prev)
	if err != nil {
		return err
	}
	return s.io.AppendLine(fmt.Sprintf("reviews/%02d.history.jsonl", prev.Chapter), append(data, '\n'))
}

// LoadReviewHistory 读取某章的历史审阅（按轮次先后），文件缺失返回空。损坏行跳过。
func (s *WorldStore) LoadReviewHistory(chapter int) []domain.ReviewEntry {
	data, err := s.io.ReadFile(fmt.Sprintf("reviews/%02d.history.jsonl", chapter))
	if err != nil {
		return nil
	}
	var out []domain.ReviewEntry
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r domain.ReviewEntry
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			out = append(out, r)
		}
	}
	return out
}
