package diag

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// GhostCharacter 检测 core/important 角色长期未出现。
func GhostCharacter(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Characters) == 0 || len(snap.Summaries) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 5 {
		return nil
	}

	// 计算每个角色最后出现的章节号
	lastSeen := make(map[string]int)
	for ch, s := range snap.Summaries {
		for _, name := range s.Characters {
			if ch > lastSeen[name] {
				lastSeen[name] = ch
			}
		}
	}

	threshold := completed / 3
	if threshold < 5 {
		threshold = 5
	}
	latest := snap.LatestCompleted()

	var ghosts []string
	for _, c := range snap.Characters {
		if c.Tier != "core" && c.Tier != "important" {
			continue
		}
		seen, ok := lastSeen[c.Name]
		if !ok {
			// 也检查别名
			for _, alias := range c.Aliases {
				if s, exists := lastSeen[alias]; exists && s > seen {
					seen = s
					ok = true
				}
			}
		}
		gap := latest - seen
		if !ok {
			ghosts = append(ghosts, fmt.Sprintf("%s(从未出现在摘要中)", c.Name))
		} else if gap > threshold {
			ghosts = append(ghosts, fmt.Sprintf("%s(最后出现ch%d,已缺席%d章)", c.Name, seen, gap))
		}
	}
	if len(ghosts) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "GhostCharacter",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.characters",
		Title:      fmt.Sprintf("角色消失: %d 个核心角色长期缺席", len(ghosts)),
		Evidence:   strings.Join(ghosts, "; "),
		Suggestion: "Writer 可能丢失了该角色的追踪。考虑直接在输入框提交干预指令重新引入该角色，或在 characters.json 中降级其 tier。",
	}}
}

// CastBriefRoleMissing 检测反复出场但缺少一句话定位的配角。
func CastBriefRoleMissing(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.CastLedger) == 0 {
		return nil
	}
	var missing []domain.CastEntry
	for _, e := range activeCastEntries(snap.CastLedger) {
		if strings.TrimSpace(e.BriefRole) != "" {
			continue
		}
		if castAppearanceCount(e) < 2 {
			continue
		}
		missing = append(missing, e)
	}
	if len(missing) == 0 {
		return nil
	}
	sortCastEntries(missing)
	severity := SevInfo
	if len(missing) >= 3 {
		severity = SevWarning
	}
	return []Finding{{
		Rule:       "CastBriefRoleMissing",
		Category:   CatContext,
		Severity:   severity,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.cast_ledger",
		Title:      fmt.Sprintf("配角定位缺失: %d 个反复出场配角没有 brief_role", len(missing)),
		Evidence:   formatCastEntries(missing, 8),
		Suggestion: "Writer 可能没有稳定填写 cast_intros。检查 writer.md 的配角连续性段，或在后续章节让 Writer 补足这些角色的一句话定位。",
	}}
}

// CastBloat 检测配角名册相对已完成章节数明显膨胀。
func CastBloat(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.CastLedger) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 10 {
		return nil
	}
	active := activeCastEntries(snap.CastLedger)
	count := len(active)
	threshold := int(float64(completed) * ThresholdCastBloatRatio)
	if threshold < 8 {
		threshold = 8
	}
	if count <= threshold {
		return nil
	}
	ratio := float64(count) / float64(completed)
	severity := SevInfo
	if ratio > 1.2 {
		severity = SevWarning
	}
	sortCastEntries(active)
	return []Finding{{
		Rule:       "CastBloat",
		Category:   CatContext,
		Severity:   severity,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "context.cast_ledger",
		Title:      fmt.Sprintf("配角名册膨胀: %d 个活跃配角 / %d 章", count, completed),
		Evidence:   fmt.Sprintf("ratio=%.2f, threshold=%d, top=[%s]", ratio, threshold, formatCastEntries(active, 8)),
		Suggestion: "如果不是群像密集章节，可能是 Writer 把过场群众写进 cast_intros 或角色命名漂移。检查最近章节的 characters/cast_intros，必要时合并同人多名或停止记录一次性群众。",
	}}
}

// CastPromotionCandidate 提示高频配角应考虑升格为核心角色档案。
func CastPromotionCandidate(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.CastLedger) == 0 {
		return nil
	}
	var candidates []domain.CastEntry
	for _, e := range activeCastEntries(snap.CastLedger) {
		if castAppearanceCount(e) >= 5 {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sortCastEntries(candidates)
	return []Finding{{
		Rule:       "CastPromotionCandidate",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.characters",
		Title:      fmt.Sprintf("配角升格候选: %d 个配角已高频出场", len(candidates)),
		Evidence:   formatCastEntries(candidates, 8),
		Suggestion: "高频配角继续只靠 recent_cast 召回会变脆。考虑把这些角色补入 characters.json，或在下一次规划/弧摘要中明确其人物弧线与关系功能。",
	}}
}

// TimelineGaps 检测已完成章节缺少时间线事件。
func TimelineGaps(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.CompletedChapters) == 0 {
		return nil
	}
	if len(snap.Timeline) == 0 && snap.CompletedCount() > 0 {
		return []Finding{{
			Rule:       "TimelineGaps",
			Category:   CatContext,
			Severity:   SevInfo,
			Confidence: ConfMedium,
			AutoLevel:  AutoNone,
			Target:     "context.timeline",
			Title:      "时间线为空",
			Evidence:   fmt.Sprintf("completed=%d, timeline_events=0", snap.CompletedCount()),
			Suggestion: "commit_chapter 的时间线提取可能未生效。检查 Writer 输出是否包含 timeline 字段。",
		}}
	}

	// 建立章节→事件映射
	chaptersWithEvents := make(map[int]bool)
	for _, e := range snap.Timeline {
		chaptersWithEvents[e.Chapter] = true
	}

	var missing []int
	for _, ch := range snap.Progress.CompletedChapters {
		if !chaptersWithEvents[ch] {
			missing = append(missing, ch)
		}
	}
	// 允许少量缺失（某些过渡章可能确实无重大事件）
	if len(missing) == 0 || float64(len(missing))/float64(snap.CompletedCount()) < ThresholdTimelineGapRate {
		return nil
	}
	return []Finding{{
		Rule:       "TimelineGaps",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.timeline",
		Title:      fmt.Sprintf("时间线缺口: %d 章无事件记录", len(missing)),
		Evidence:   fmt.Sprintf("missing=[%s]", intsToStr(missing)),
		Suggestion: "commit_chapter 的时间线提取可能部分失效。检查 Writer 输出的 timeline 字段格式。",
	}}
}

func activeCastEntries(entries []domain.CastEntry) []domain.CastEntry {
	active := make([]domain.CastEntry, 0, len(entries))
	for _, e := range entries {
		if e.Promoted || strings.TrimSpace(e.Name) == "" {
			continue
		}
		active = append(active, e)
	}
	return active
}

func castAppearanceCount(e domain.CastEntry) int {
	if len(e.AppearanceChapters) > e.AppearanceCount {
		return len(e.AppearanceChapters)
	}
	return e.AppearanceCount
}

func castLastSeen(e domain.CastEntry) int {
	latest := e.LastSeenChapter
	for _, ch := range e.AppearanceChapters {
		if ch > latest {
			latest = ch
		}
	}
	return latest
}

func sortCastEntries(entries []domain.CastEntry) {
	sort.Slice(entries, func(i, j int) bool {
		ci, cj := castAppearanceCount(entries[i]), castAppearanceCount(entries[j])
		if ci != cj {
			return ci > cj
		}
		li, lj := castLastSeen(entries[i]), castLastSeen(entries[j])
		if li != lj {
			return li > lj
		}
		return entries[i].Name < entries[j].Name
	})
}

func formatCastEntries(entries []domain.CastEntry, limit int) string {
	if limit <= 0 || limit > len(entries) {
		limit = len(entries)
	}
	parts := make([]string, 0, limit+1)
	for i := 0; i < limit; i++ {
		e := entries[i]
		part := fmt.Sprintf("%s(出场%d次,最后ch%d", e.Name, castAppearanceCount(e), castLastSeen(e))
		if e.BriefRole != "" {
			part += "," + e.BriefRole
		}
		part += ")"
		parts = append(parts, part)
	}
	if len(entries) > limit {
		parts = append(parts, fmt.Sprintf("另%d个", len(entries)-limit))
	}
	return strings.Join(parts, "; ")
}

// RelationshipStagnation 检测关系数据停止更新。
func RelationshipStagnation(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Relationships) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 6 {
		return nil
	}

	// 找到关系数据的最新章节
	latestRelCh := 0
	for _, r := range snap.Relationships {
		if r.Chapter > latestRelCh {
			latestRelCh = r.Chapter
		}
	}

	// 如果最新关系数据在前 1/3，判定为停滞
	cutoff := snap.LatestCompleted() - completed/3
	if latestRelCh >= cutoff {
		return nil
	}
	return []Finding{{
		Rule:       "RelationshipStagnation",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "context.relationships",
		Title:      fmt.Sprintf("关系数据停滞: 最新更新在第 %d 章", latestRelCh),
		Evidence:   fmt.Sprintf("relationship_entries=%d, latest_update=ch%d, latest_completed=ch%d", len(snap.Relationships), latestRelCh, snap.LatestCompleted()),
		Suggestion: "commit_chapter 的关系更新可能停止工作，或故事关系确实无变化。检查 Writer 输出的 relationships 字段。",
	}}
}
