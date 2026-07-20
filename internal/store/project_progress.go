package store

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	projectProgressJSON = "meta/project_progress.json"
	projectProgressMD   = "meta/project_progress.md"
)

// LoadProjectProgressLedger reads the project-level planning dashboard.
func (s *Store) LoadProjectProgressLedger() (*domain.ProjectProgressLedger, error) {
	var ledger domain.ProjectProgressLedger
	if err := s.Progress.io.ReadJSON(projectProgressJSON, &ledger); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &ledger, nil
}

// RefreshProjectProgressLedger rebuilds the project-level planning dashboard.
// It is deterministic and intentionally conservative: it does not rewrite the
// outline, but it records the concrete issues that should drive the next
// architect or writer step.
func (s *Store) RefreshProjectProgressLedger(
	chapterLedger *domain.ChapterProgressLedger,
	characterLedger *domain.CharacterContinuityLedger,
) (*domain.ProjectProgressLedger, error) {
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
	if characterLedger == nil {
		characterLedger, _ = s.LoadCharacterContinuityLedger()
	}
	premise, _ := s.Outline.LoadPremise()
	deliveryChapters := inferDeliveryChapters(premise)
	compass, _ := s.Outline.LoadCompass()
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, fmt.Errorf("load layered outline: %w", err)
	}
	outlineByChapter, positionByChapter, err := s.chapterOutlineMaps()
	if err != nil {
		return nil, err
	}
	resources, err := s.ResourceLedger.Load()
	if err != nil {
		return nil, fmt.Errorf("load resource ledger: %w", err)
	}
	foreshadow, err := s.World.LoadForeshadowLedger()
	if err != nil {
		return nil, fmt.Errorf("load foreshadow ledger: %w", err)
	}
	relationships, err := s.World.LoadRelationships()
	if err != nil {
		return nil, fmt.Errorf("load relationships: %w", err)
	}

	completed := append([]int(nil), progress.CompletedChapters...)
	sort.Ints(completed)
	latest := maxCompletedChapter(completed)
	ledger := &domain.ProjectProgressLedger{
		Version:             1,
		NovelName:           progress.NovelName,
		GeneratedAt:         time.Now().Format(time.RFC3339),
		CurrentChapter:      progress.CurrentChapter,
		CompletedChapters:   completed,
		TotalChapters:       progress.TotalChapters,
		DeliveryChapters:    deliveryChapters,
		CurrentVolume:       progress.CurrentVolume,
		CurrentArc:          progress.CurrentArc,
		ScopeWarnings:       buildScopeWarnings(progress, deliveryChapters, compass, latest),
		OutlineStatus:       buildOutlineStatus(layered, completed, progress.CurrentChapter),
		PromiseEntries:      buildPromiseEntries(chapterLedger, progress),
		ProtagonistArc:      buildProtagonistArc(chapterLedger, progress, outlineByChapter, positionByChapter),
		ResourceHygiene:     buildResourceHygiene(resources, latest),
		ForeshadowPlan:      buildForeshadowPlan(foreshadow, progress.CurrentChapter),
		RelationshipTension: buildRelationshipTension(relationships),
		AssetOperations:     buildAssetOperations(resources),
	}
	ledger.HookAnalysis = buildHookAnalysis(ledger.PromiseEntries)
	ledger.NextChapterActions = buildNextProjectActions(ledger, chapterLedger, characterLedger)
	if err := s.writeProjectProgressLedger(ledger); err != nil {
		return nil, err
	}
	return ledger, nil
}

func (s *Store) writeProjectProgressLedger(ledger *domain.ProjectProgressLedger) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(projectProgressJSON, ledger); err != nil {
			return err
		}
		return s.Progress.io.WriteMarkdownUnlocked(projectProgressMD, renderProjectProgressLedger(ledger))
	})
}

func inferDeliveryChapters(premise string) int {
	re := regexp.MustCompile(`(?:约|规划约|前)?\s*([0-9]{1,4})\s*章`)
	match := re.FindStringSubmatch(premise)
	if len(match) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

func buildScopeWarnings(progress *domain.Progress, deliveryChapters int, compass *domain.StoryCompass, latest int) []domain.ProjectPlanningWarning {
	var warnings []domain.ProjectPlanningWarning
	if deliveryChapters > 0 && progress.TotalChapters > 0 && deliveryChapters != progress.TotalChapters {
		warnings = append(warnings, domain.ProjectPlanningWarning{
			Code:       "chapter_scope_mismatch",
			Severity:   "high",
			Message:    fmt.Sprintf("当前交付线约 %d 章，但 progress.total_chapters=%d。", deliveryChapters, progress.TotalChapters),
			Suggestion: "明确 total_chapters 是本轮交付章数还是全书粗纲章数；必要时新增 delivery_chapters/global_outline_chapters 区分。",
		})
	}
	if compass != nil {
		var text string
		text += compass.EstimatedScale + "\n"
		text += strings.Join(compass.OpenThreads, "\n")
		if latest > 10 && (strings.Contains(text, "8-10") || strings.Contains(text, "重生成") || strings.Contains(text, "只重生成")) {
			warnings = append(warnings, domain.ProjectPlanningWarning{
				Code:       "stale_compass_thread",
				Severity:   "high",
				Message:    "compass.open_threads 仍含早期重生成任务口径，但当前已写过第 10 章。",
				Suggestion: "刷新 compass：删除重生成限定，改写为当前章节、当前弧目标、后续开放长线。",
			})
		}
		if latest >= 10 && compass.LastUpdated == 0 {
			warnings = append(warnings, domain.ProjectPlanningWarning{
				Code:       "compass_missing_last_updated",
				Severity:   "medium",
				Message:    "compass.last_updated 为空，无法判断指南针是否随章节推进更新。",
				Suggestion: "下一次 architect_long/update_compass 时写入最新已完成章数。",
			})
		} else if compass.LastUpdated > 0 && latest-compass.LastUpdated > 10 {
			warnings = append(warnings, domain.ProjectPlanningWarning{
				Code:       "compass_drift",
				Severity:   "medium",
				Message:    fmt.Sprintf("compass 已落后最新章节 %d 章。", latest-compass.LastUpdated),
				Suggestion: "弧末或每 10 章刷新一次指南针，清理已完成/已失效长线。",
			})
		}
	}
	return warnings
}

func buildOutlineStatus(layered []domain.VolumeOutline, completed []int, currentChapter int) []domain.OutlineArcStatus {
	if len(layered) == 0 {
		return nil
	}
	completedSet := map[int]bool{}
	for _, ch := range completed {
		completedSet[ch] = true
	}
	var out []domain.OutlineArcStatus
	cursor := 1
	for _, v := range layered {
		for _, a := range v.Arcs {
			total := len(a.Chapters)
			if total == 0 {
				total = a.EstimatedChapters
			}
			status := domain.OutlineArcStatus{
				Volume:            v.Index,
				VolumeTitle:       v.Title,
				Arc:               a.Index,
				ArcTitle:          a.Title,
				Goal:              a.Goal,
				Expanded:          a.IsExpanded(),
				StartChapter:      cursor,
				TotalChapters:     total,
				EstimatedChapters: a.EstimatedChapters,
				Status:            "planned",
			}
			if total > 0 {
				status.EndChapter = cursor + total - 1
				for ch := status.StartChapter; ch <= status.EndChapter; ch++ {
					if completedSet[ch] {
						status.CompletedChapters++
					}
				}
				if status.CompletedChapters >= total {
					status.Status = "complete"
				} else if currentChapter >= status.StartChapter && currentChapter <= status.EndChapter {
					status.Status = "current"
				} else if !a.IsExpanded() {
					status.Status = "skeleton"
				}
			} else if !a.IsExpanded() {
				status.Status = "skeleton"
			}
			out = append(out, status)
			cursor += max(total, 0)
		}
	}
	return out
}

func buildPromiseEntries(chapterLedger *domain.ChapterProgressLedger, progress *domain.Progress) []domain.ChapterPromiseEntry {
	if chapterLedger == nil {
		return nil
	}
	var out []domain.ChapterPromiseEntry
	for _, e := range chapterLedger.Entries {
		entry := domain.ChapterPromiseEntry{
			Chapter:               e.Chapter,
			Title:                 e.Title,
			Position:              e.Position,
			Summary:               firstNonEmpty(e.Summary, strings.Join(e.KeyEvents, "；"), e.OutlineCoreEvent),
			HookType:              hookTypeForChapter(progress, e.Chapter),
			HookShape:             classifyHookShape(firstNonEmpty(e.OutlineHook, e.Summary, strings.Join(e.KeyEvents, "；"))),
			HasAssetOrRiskChange:  len(e.ResourceChanges) > 0 || containsAny(joinProgressEntryText(e), []string{"审计", "账单", "旧债", "风险", "担保", "亏空"}),
			HasRelationshipChange: len(e.RelationshipChanges) > 0,
			HasTimelineEvent:      len(e.TimelineEvents) > 0,
			HasForeshadowSignal:   containsAny(joinProgressEntryText(e), []string{"伏笔", "线索", "显字", "名片", "请柬", "预告", "倒计时"}),
		}
		entry.PromiseSignals = promiseSignalsForEntry(e)
		entry.MissingSignals = missingPromiseSignals(entry)
		out = append(out, entry)
	}
	return out
}

func buildProtagonistArc(
	chapterLedger *domain.ChapterProgressLedger,
	progress *domain.Progress,
	outlineByChapter map[int]domain.OutlineEntry,
	positionByChapter map[int]domain.ChapterPosition,
) []domain.ProtagonistArcEntry {
	if progress == nil {
		return nil
	}
	total := progress.TotalChapters
	if total <= 0 {
		for ch := range outlineByChapter {
			if ch > total {
				total = ch
			}
		}
	}
	if total <= 0 && chapterLedger != nil {
		for _, ch := range chapterLedger.CompletedChapters {
			if ch > total {
				total = ch
			}
		}
	}
	if total <= 0 {
		return nil
	}

	actual := map[int]domain.ChapterProgressEntry{}
	if chapterLedger != nil {
		for _, e := range chapterLedger.Entries {
			actual[e.Chapter] = e
		}
	}

	out := make([]domain.ProtagonistArcEntry, 0, total)
	for ch := 1; ch <= total; ch++ {
		if e, ok := actual[ch]; ok {
			item := domain.ProtagonistArcEntry{
				Chapter:      ch,
				Title:        e.Title,
				Position:     e.Position,
				Source:       "actual",
				Change:       protagonistActualChange(e),
				Driver:       firstNonEmpty(e.OutlineCoreEvent, e.Summary, strings.Join(e.KeyEvents, "；")),
				Result:       protagonistActualResult(e),
				NextPressure: e.OutlineHook,
			}
			out = append(out, item)
			continue
		}

		outline, ok := outlineByChapter[ch]
		if !ok {
			out = append(out, domain.ProtagonistArcEntry{
				Chapter: ch,
				Source:  "missing_outline",
				Change:  "缺少章级大纲，写作前先补本章主角目标、阻力、失败代价和新增信息。",
			})
			continue
		}
		out = append(out, domain.ProtagonistArcEntry{
			Chapter:      ch,
			Title:        outline.Title,
			Position:     positionByChapter[ch],
			Source:       "planned",
			Change:       protagonistPlannedChange(outline),
			Driver:       firstNonEmpty(outline.CoreEvent, strings.Join(outline.Scenes, "；")),
			Result:       protagonistPlannedResult(outline),
			NextPressure: outline.Hook,
		})
	}
	return out
}

func protagonistActualChange(e domain.ChapterProgressEntry) string {
	if len(e.ProtagonistChanges) > 0 {
		return renderStateChangeInline(e.ProtagonistChanges, "")
	}
	if len(e.ResourceChanges) > 0 {
		return "资源/权限推进：" + renderResourceInline(e.ResourceChanges)
	}
	return compactProgressText(firstNonEmpty(e.Summary, strings.Join(e.KeyEvents, "；"), e.OutlineCoreEvent), 180)
}

func protagonistActualResult(e domain.ChapterProgressEntry) string {
	var parts []string
	if len(e.ResourceChanges) > 0 {
		parts = append(parts, "资源入账")
	}
	if len(e.RelationshipChanges) > 0 {
		parts = append(parts, "关系改变")
	}
	if len(e.TimelineEvents) > 0 {
		parts = append(parts, "时间线推进")
	}
	if e.ReviewStatus == "accept" {
		parts = append(parts, "审阅通过")
	}
	if len(parts) == 0 {
		return "已写章节，保留为后续事实"
	}
	return strings.Join(parts, "、")
}

func protagonistPlannedChange(outline domain.OutlineEntry) string {
	text := firstNonEmpty(outline.CoreEvent, strings.Join(outline.Scenes, "；"))
	if text == "" {
		return "计划让主角完成本章核心推进，写作前需补充具体行动与代价。"
	}
	return "计划推进：" + compactProgressText(text, 180)
}

func protagonistPlannedResult(outline domain.OutlineEntry) string {
	text := outline.CoreEvent + "；" + outline.Hook + "；" + strings.Join(outline.Scenes, "；")
	switch {
	case containsAny(text, []string{"买下", "取得", "包下", "成立", "获得", "成交", "经营权", "牌照"}):
		return "把本章胜利沉淀为资产、权限或组织能力"
	case containsAny(text, []string{"救醒", "救援", "营救", "学生", "伤员", "患者", "被困者"}):
		return "推进救援线，同时制造新的账单或担保压力"
	case containsAny(text, []string{"背叛", "清退", "反杀", "清算"}):
		return "处理背叛并形成可复用的规则样本"
	case containsAny(text, []string{"确认", "核验", "发现", "查", "录音", "名单"}):
		return "补齐可见事实或获得下一步行动信息"
	default:
		return "让核心事件产生可入账变化"
	}
}

func hookTypeForChapter(progress *domain.Progress, chapter int) string {
	if progress == nil || chapter <= 0 || chapter > len(progress.HookHistory) {
		return ""
	}
	return progress.HookHistory[chapter-1]
}

func promiseSignalsForEntry(e domain.ChapterProgressEntry) []string {
	text := joinProgressEntryText(e)
	var signals []string
	add := func(label string, words []string) {
		if containsAny(text, words) {
			signals = append(signals, label)
		}
	}
	add("权利凭证", []string{"欠费单", "价签", "病历", "同意书", "保单", "收据", "门牌", "账本", "小票", "挂号", "探视牌", "合同", "牌照", "欠条", "名片", "钥匙", "票据", "回执"})
	add("错误代价", []string{"失败", "吞", "割", "削", "伤", "抵押", "扣", "损", "死亡", "反噬", "越权", "伪造", "旧债", "亏空"})
	add("交易条款", []string{"条款", "确认", "交易", "购买", "支付", "债权", "产权", "租", "担保", "清算", "合同", "权限", "权"})
	add("资产沉淀", []string{"取得", "获得", "成为", "建立", "入账", "确认权", "经营", "资产", "客户", "观察位"})
	add("追责升级", []string{"审计", "账单", "催债", "旧债", "抽查", "追责", "问责", "核查", "复核"})
	if len(e.RelationshipChanges) > 0 {
		signals = append(signals, "关系推进")
	}
	if len(e.TimelineEvents) > 0 {
		signals = append(signals, "时间线推进")
	}
	return appendUniqueString(nil, signals...)
}

func missingPromiseSignals(e domain.ChapterPromiseEntry) []string {
	var missing []string
	if len(e.PromiseSignals) < 2 {
		missing = append(missing, "本章承诺信号少于2类，后续复盘需确认是否真实推进故事变化")
	}
	if !e.HasAssetOrRiskChange {
		missing = append(missing, "缺少资产/风险/账单沉淀")
	}
	if !e.HasRelationshipChange {
		missing = append(missing, "缺少结构化关系推进")
	}
	return missing
}

func joinProgressEntryText(e domain.ChapterProgressEntry) string {
	var parts []string
	parts = append(parts, e.Summary, e.OutlineCoreEvent, e.OutlineHook, strings.Join(e.KeyEvents, "；"))
	for _, r := range e.ResourceChanges {
		parts = append(parts, r.Name, r.Kind, r.Risk, r.Evidence)
	}
	for _, s := range e.StateChanges {
		parts = append(parts, s.Field, s.OldValue, s.NewValue, s.Reason)
	}
	return strings.Join(parts, "；")
}

func classifyHookShape(text string) string {
	switch {
	case containsAny(text, []string{"倒计时", "午夜前", "五小时", "期限"}):
		return "倒计时"
	case containsAny(text, []string{"来电", "电话", "手机", "短信", "录音"}):
		return "通信/来电"
	case containsAny(text, []string{"门", "入口", "开门", "门缝"}):
		return "新门/入口"
	case containsAny(text, []string{"显字", "浮出", "写着", "显示"}):
		return "纸面/屏幕显字"
	case containsAny(text, []string{"账单", "欠费", "收据", "小票", "欠条"}):
		return "账单/票据"
	case containsAny(text, []string{"背叛", "伪造", "抢", "泄露"}):
		return "背叛/内鬼"
	case containsAny(text, []string{"出现", "现身", "递出"}):
		return "人物/执行者现身"
	default:
		return "未分类"
	}
}

func buildHookAnalysis(entries []domain.ChapterPromiseEntry) domain.HookAnalysis {
	a := domain.HookAnalysis{HookTypeCounts: map[string]int{}}
	start := max(0, len(entries)-8)
	for _, e := range entries[start:] {
		if e.HookType != "" {
			a.RecentHookTypes = append(a.RecentHookTypes, e.HookType)
			a.HookTypeCounts[e.HookType]++
		}
		if e.HookShape != "" {
			a.RecentShapes = append(a.RecentShapes, e.HookShape)
		}
	}
	if repeated := repeatedInTail(a.RecentShapes, 5, 3); repeated != "" {
		a.Warnings = append(a.Warnings, "最近5章钩子形态中 "+repeated+" 出现3次以上，下一章应换承载物和情绪功能。")
	}
	if repeated := repeatedInTail(a.RecentHookTypes, 5, 3); repeated != "" {
		a.Warnings = append(a.Warnings, "最近5章 hook_type 中 "+repeated+" 出现3次以上，下一章应调整章尾推进功能。")
	}
	return a
}

func buildResourceHygiene(ledger *domain.ResourceLedger, latest int) domain.ResourceHygieneReport {
	if ledger == nil {
		return domain.ResourceHygieneReport{}
	}
	var report domain.ResourceHygieneReport
	var booked []domain.ResourceClaim
	var pending []domain.ResourceClaim
	for _, c := range ledger.Claims {
		switch c.Status {
		case "pending":
			pending = append(pending, c)
		case "booked", "spent":
			booked = append(booked, c)
		}
	}
	report.PendingCount = len(pending)
	for _, p := range pending {
		if p.Chapter > 0 && latest-p.Chapter >= 5 {
			report.StalePending = append(report.StalePending, p)
			if len(report.StalePending) >= 12 {
				break
			}
		}
	}
	for _, p := range pending {
		pn := normalizeResourceName(p.Name)
		if pn == "" {
			continue
		}
		for _, b := range booked {
			bn := normalizeResourceName(b.Name)
			if bn == "" {
				continue
			}
			if p.Owner == b.Owner && likelySameResourceName(pn, bn) {
				report.DuplicateLikely = append(report.DuplicateLikely, fmt.Sprintf("%s pending 可能已被 %s booked 覆盖", p.Name, b.Name))
				break
			}
		}
		if len(report.DuplicateLikely) >= 12 {
			break
		}
	}
	if len(report.StalePending) > 0 {
		report.Actions = append(report.Actions, "清理超过5章未更新的 pending：确认成交、保留残余风险或标记 rejected/spent。")
	}
	if len(report.DuplicateLikely) > 0 {
		report.Actions = append(report.Actions, "合并同一资源的 pending/booked 状态，避免后续上下文把已解决事项写回未解决。")
	}
	return report
}

func buildForeshadowPlan(entries []domain.ForeshadowEntry, currentChapter int) []domain.ForeshadowPlanningEntry {
	var out []domain.ForeshadowPlanningEntry
	for _, f := range entries {
		if f.Status == "resolved" {
			continue
		}
		age := 0
		if f.PlantedAt > 0 && currentChapter > 0 {
			age = currentChapter - f.PlantedAt
		}
		priority := foreshadowPriority(f, age)
		deadline := f.PlantedAt + foreshadowWindow(priority)
		if f.PlantedAt == 0 {
			deadline = currentChapter + foreshadowWindow(priority)
		}
		item := domain.ForeshadowPlanningEntry{
			ID:                       f.ID,
			Description:              compactProgressText(f.Description, 180),
			Status:                   f.Status,
			PlantedAt:                f.PlantedAt,
			AgeChapters:              age,
			Priority:                 priority,
			PayoffType:               foreshadowPayoffType(f),
			SuggestedDeadlineChapter: deadline,
			Action:                   foreshadowAction(f.Status, age, deadline, currentChapter),
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := foreshadowPriorityRank(out[i].Priority), foreshadowPriorityRank(out[j].Priority)
		if ri != rj {
			return ri < rj
		}
		return out[i].AgeChapters > out[j].AgeChapters
	})
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

func buildRelationshipTension(entries []domain.RelationshipEntry) []domain.RelationshipTensionEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Chapter > entries[j].Chapter
	})
	seen := map[string]bool{}
	var out []domain.RelationshipTensionEntry
	for _, r := range entries {
		pair := pairLabel(r.CharacterA, r.CharacterB)
		if pair == "" || seen[pair] {
			continue
		}
		seen[pair] = true
		out = append(out, domain.RelationshipTensionEntry{
			Pair:            pair,
			Chapter:         r.Chapter,
			CurrentRelation: compactProgressText(r.Relation, 160),
			NextNeed:        relationshipNextNeed(r.Relation),
			AvoidRepeat:     relationshipAvoidRepeat(r.Relation),
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func buildAssetOperations(ledger *domain.ResourceLedger) []domain.AssetOperationEntry {
	if ledger == nil {
		return nil
	}
	claims := append([]domain.ResourceClaim(nil), ledger.Claims...)
	sort.SliceStable(claims, func(i, j int) bool {
		return claims[i].Chapter > claims[j].Chapter
	})
	var out []domain.AssetOperationEntry
	seen := map[string]bool{}
	for _, c := range claims {
		if c.Name == "" || seen[resourceClaimKey(c)] {
			continue
		}
		if !resourceNeedsOperations(c) {
			continue
		}
		seen[resourceClaimKey(c)] = true
		out = append(out, domain.AssetOperationEntry{
			Name:          c.Name,
			Owner:         c.Owner,
			Kind:          c.Kind,
			Status:        c.Status,
			LastChapter:   c.Chapter,
			CurrentRisk:   compactProgressText(c.Risk, 140),
			OperationNeed: assetOperationNeed(c),
			NextTrigger:   assetNextTrigger(c),
		})
		if len(out) >= 14 {
			break
		}
	}
	return out
}

func buildNextProjectActions(
	ledger *domain.ProjectProgressLedger,
	chapterLedger *domain.ChapterProgressLedger,
	characterLedger *domain.CharacterContinuityLedger,
) []string {
	var actions []string
	for _, w := range ledger.ScopeWarnings {
		if w.Severity == "high" {
			actions = append(actions, w.Suggestion)
		}
	}
	if len(ledger.ResourceHygiene.Actions) > 0 {
		actions = append(actions, ledger.ResourceHygiene.Actions...)
	}
	if len(ledger.HookAnalysis.Warnings) > 0 {
		actions = append(actions, ledger.HookAnalysis.Warnings...)
	}
	for _, arc := range ledger.OutlineStatus {
		if arc.Status == "skeleton" && arc.StartChapter > 0 && arc.StartChapter <= ledger.CurrentChapter+5 {
			actions = append(actions, fmt.Sprintf("第%d章前先展开 V%dA%d《%s》的章级大纲。", arc.StartChapter, arc.Volume, arc.Arc, arc.ArcTitle))
			break
		}
	}
	if chapterLedger != nil && chapterLedger.NextPlan != nil {
		p := chapterLedger.NextPlan
		actions = append(actions, fmt.Sprintf("写第%d章前同时核对 chapter_progress.next_plan 与 project_progress.next_chapter_actions，确保本章产生可入账变化。", p.Chapter))
	}
	if characterLedger != nil && len(characterLedger.NextChapterFocus) > 0 {
		actions = append(actions, "下一章人物可参考 character_continuity.next_chapter_focus，但仍以大纲核心事件和项目级压力为先。")
	}
	return appendUniqueString(nil, actions...)
}

func renderProjectProgressLedger(ledger *domain.ProjectProgressLedger) string {
	var b strings.Builder
	b.WriteString("# 项目级推进仪表盘\n\n")
	if ledger.NovelName != "" {
		fmt.Fprintf(&b, "- 书名：%s\n", ledger.NovelName)
	}
	fmt.Fprintf(&b, "- 已完成章节：%d/%d\n", len(ledger.CompletedChapters), ledger.TotalChapters)
	if ledger.DeliveryChapters > 0 {
		fmt.Fprintf(&b, "- 当前交付线：约 %d 章\n", ledger.DeliveryChapters)
	}
	if ledger.CurrentChapter > 0 {
		fmt.Fprintf(&b, "- 当前工程章节：第 %d 章\n", ledger.CurrentChapter)
	}
	if ledger.CurrentVolume > 0 || ledger.CurrentArc > 0 {
		fmt.Fprintf(&b, "- 当前卷弧：V%d A%d\n", ledger.CurrentVolume, ledger.CurrentArc)
	}
	fmt.Fprintf(&b, "- 生成时间：%s\n\n", ledger.GeneratedAt)

	if len(ledger.ScopeWarnings) > 0 {
		b.WriteString("## 口径风险\n\n")
		for _, w := range ledger.ScopeWarnings {
			fmt.Fprintf(&b, "- **%s** [%s]：%s", w.Code, w.Severity, w.Message)
			if w.Suggestion != "" {
				fmt.Fprintf(&b, " 建议：%s", w.Suggestion)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if len(ledger.NextChapterActions) > 0 {
		b.WriteString("## 下一步项目动作\n\n")
		for _, a := range ledger.NextChapterActions {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		b.WriteString("\n")
	}

	if len(ledger.OutlineStatus) > 0 {
		b.WriteString("## 卷弧推进\n\n")
		b.WriteString("| 卷弧 | 状态 | 章节 | 已完成 | 目标 |\n")
		b.WriteString("|---|---|---:|---:|---|\n")
		for _, a := range ledger.OutlineStatus {
			chapterRange := ""
			if a.StartChapter > 0 && a.EndChapter > 0 {
				chapterRange = fmt.Sprintf("%d-%d", a.StartChapter, a.EndChapter)
			} else if a.EstimatedChapters > 0 {
				chapterRange = fmt.Sprintf("预估%d章", a.EstimatedChapters)
			}
			fmt.Fprintf(&b, "| V%dA%d %s | %s | %s | %d/%d | %s |\n",
				a.Volume, a.Arc, escapeTable(a.ArcTitle), a.Status, chapterRange, a.CompletedChapters, a.TotalChapters, escapeTable(compactProgressText(a.Goal, 90)))
		}
		b.WriteString("\n")
	}

	if len(ledger.ProtagonistArc) > 0 {
		b.WriteString("## 主角变化路线图\n\n")
		b.WriteString("| 章 | 状态 | 位置 | 章节 | 主角变化 | 结果/压力 |\n")
		b.WriteString("|---:|---|---|---|---|---|\n")
		for _, e := range ledger.ProtagonistArc {
			result := e.Result
			if e.NextPressure != "" {
				if result != "" {
					result += "；"
				}
				result += "下一压力：" + e.NextPressure
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %s |\n",
				e.Chapter,
				escapeTable(protagonistArcSourceLabel(e.Source)),
				escapeTable(positionLabel(e.Position)),
				escapeTable(e.Title),
				escapeTable(compactProgressText(e.Change, 120)),
				escapeTable(compactProgressText(result, 120)),
			)
		}
		b.WriteString("\n")
	}

	if len(ledger.PromiseEntries) > 0 {
		b.WriteString("## 逐章承诺兑现\n\n")
		b.WriteString("| 章 | 信号 | 钩子形态 | 缺口 |\n")
		b.WriteString("|---:|---|---|---|\n")
		for _, e := range ledger.PromiseEntries {
			gaps := "无"
			if len(e.MissingSignals) > 0 {
				gaps = strings.Join(e.MissingSignals, "；")
			}
			fmt.Fprintf(&b, "| %d | %s | %s | %s |\n",
				e.Chapter,
				escapeTable(strings.Join(e.PromiseSignals, "、")),
				escapeTable(e.HookShape),
				escapeTable(gaps),
			)
		}
		b.WriteString("\n")
	}

	if len(ledger.HookAnalysis.Warnings) > 0 || len(ledger.HookAnalysis.RecentShapes) > 0 {
		b.WriteString("## 钩子节奏\n\n")
		if len(ledger.HookAnalysis.RecentShapes) > 0 {
			fmt.Fprintf(&b, "- 近期形态：%s\n", strings.Join(ledger.HookAnalysis.RecentShapes, " / "))
		}
		for _, w := range ledger.HookAnalysis.Warnings {
			fmt.Fprintf(&b, "- 风险：%s\n", w)
		}
		b.WriteString("\n")
	}

	if ledger.ResourceHygiene.PendingCount > 0 || len(ledger.ResourceHygiene.Actions) > 0 {
		b.WriteString("## 资源清账\n\n")
		fmt.Fprintf(&b, "- pending 总数：%d\n", ledger.ResourceHygiene.PendingCount)
		for _, item := range ledger.ResourceHygiene.StalePending {
			fmt.Fprintf(&b, "- 过期 pending：第%d章 %s（%s）\n", item.Chapter, item.Name, item.Owner)
		}
		for _, item := range ledger.ResourceHygiene.DuplicateLikely {
			fmt.Fprintf(&b, "- 疑似重复：%s\n", item)
		}
		b.WriteString("\n")
	}

	if len(ledger.ForeshadowPlan) > 0 {
		b.WriteString("## 伏笔优先级\n\n")
		b.WriteString("| ID | 优先级 | 状态 | 建议截止章 | 动作 |\n")
		b.WriteString("|---|---|---|---:|---|\n")
		for _, f := range ledger.ForeshadowPlan {
			fmt.Fprintf(&b, "| %s | %s | %s | %d | %s |\n", escapeTable(f.ID), f.Priority, f.Status, f.SuggestedDeadlineChapter, escapeTable(f.Action))
		}
		b.WriteString("\n")
	}

	if len(ledger.RelationshipTension) > 0 {
		b.WriteString("## 关系张力债\n\n")
		for _, r := range ledger.RelationshipTension {
			fmt.Fprintf(&b, "- **%s**（第%d章）：%s；下一步：%s；避免：%s\n", r.Pair, r.Chapter, r.CurrentRelation, r.NextNeed, r.AvoidRepeat)
		}
		b.WriteString("\n")
	}

	if len(ledger.AssetOperations) > 0 {
		b.WriteString("## 资产运营提醒\n\n")
		for _, a := range ledger.AssetOperations {
			fmt.Fprintf(&b, "- **%s**", a.Name)
			if a.Owner != "" {
				fmt.Fprintf(&b, "（%s）", a.Owner)
			}
			fmt.Fprintf(&b, "：%s；下一触发：%s\n", a.OperationNeed, a.NextTrigger)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func protagonistArcSourceLabel(source string) string {
	switch source {
	case "actual":
		return "已沉淀"
	case "planned":
		return "计划"
	case "missing_outline":
		return "缺章纲"
	default:
		return source
	}
}

func containsAny(text string, words []string) bool {
	for _, w := range words {
		if w != "" && strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func repeatedInTail(items []string, window, threshold int) string {
	if len(items) == 0 || threshold <= 1 {
		return ""
	}
	if len(items) > window {
		items = items[len(items)-window:]
	}
	counts := map[string]int{}
	for _, item := range items {
		if item == "" || item == "未分类" {
			continue
		}
		counts[item]++
		if counts[item] >= threshold {
			return item
		}
	}
	return ""
}

func normalizeResourceName(name string) string {
	replacer := strings.NewReplacer(
		"临时", "",
		"有限", "",
		"主要", "",
		"线索", "",
		"入口", "",
		"待办", "",
		"十五分钟", "",
		"一次", "",
		"部分", "",
		"主要抵押", "抵押",
		"名字", "姓名",
		"凭证", "",
		"证据", "",
		"记录", "",
		"状态", "",
		"样本", "",
		"有效性", "",
	)
	return strings.TrimSpace(replacer.Replace(name))
}

func likelySameResourceName(a, b string) bool {
	if a == b {
		return true
	}
	ar, br := []rune(a), []rune(b)
	if len(ar) < 6 || len(br) < 6 {
		return false
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	// One resource often mutates from "临时X权" to "有限X权". After
	// normalization those should match exactly or by containment; shared
	// location prefixes alone are intentionally ignored to avoid merging
	// different rights under the same scene.
	return false
}

func foreshadowPriority(f domain.ForeshadowEntry, age int) string {
	if age >= 12 {
		return "high"
	}
	if age >= 6 || f.Status == "advanced" {
		return "medium"
	}
	return "low"
}

func foreshadowPriorityRank(priority string) int {
	switch priority {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

func foreshadowWindow(priority string) int {
	switch priority {
	case "high":
		return 8
	case "medium":
		return 14
	default:
		return 22
	}
}

func foreshadowPayoffType(f domain.ForeshadowEntry) string {
	text := f.ID + f.Description
	switch {
	case containsAny(text, []string{"资产", "产权", "牌照", "经营", "权限"}):
		return "资产"
	case containsAny(text, []string{"反杀", "背叛", "伪造", "清算"}):
		return "反杀"
	case containsAny(text, []string{"亲人", "父亲", "母亲", "兄长", "姐姐", "妹妹", "弟弟", "朋友", "同伴", "恋人", "关系", "情感", "承诺", "信任"}):
		return "情感/关系"
	case containsAny(text, []string{"反派", "敌手", "对手", "追杀", "威胁", "组织", "势力"}):
		return "反派升级"
	default:
		return "解谜"
	}
}

func foreshadowAction(status string, age, deadline, current int) string {
	if current > 0 && deadline > 0 && current >= deadline {
		return "已接近或超过建议截止章，下一次相关章节必须推进或回收。"
	}
	if status == "planted" && age >= 4 {
		return "从 planted 推进到 advanced，给读者一次可见反馈。"
	}
	if status == "advanced" {
		return "规划明确回收方式，避免只反复提醒不兑现。"
	}
	return "保留，等待相邻场景自然触发。"
}

func pairLabel(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" && b == "" {
		return ""
	}
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " / " + b
}

func relationshipNextNeed(relation string) string {
	switch {
	case containsAny(relation, []string{"怀疑", "质疑", "防备", "冲突", "拒绝"}):
		return "用一次行动或交易改变信任状态，不能只继续互相质问。"
	case containsAny(relation, []string{"合作", "协作", "担保", "同盟"}):
		return "让合作产生代价、边界或新分工。"
	case containsAny(relation, []string{"债", "欠", "抵押", "风险"}):
		return "明确债务如何影响下一次选择。"
	default:
		return "下一次同场必须带来新信息、新选择或新责任。"
	}
}

func relationshipAvoidRepeat(relation string) string {
	switch {
	case containsAny(relation, []string{"质疑", "怀疑"}):
		return "避免重复质疑-解释循环。"
	case containsAny(relation, []string{"后勤", "登记", "解释"}):
		return "避免只让角色承担说明书功能。"
	case containsAny(relation, []string{"账", "账本", "提示"}):
		return "避免只通过账本提示推进。"
	default:
		return "避免与上次出场承担同一功能。"
	}
}

func resourceNeedsOperations(c domain.ResourceClaim) bool {
	if c.Status == "pending" {
		return true
	}
	return containsAny(c.Kind, []string{"asset", "permission", "guarantee", "claim", "debt", "status", "system", "insurance"}) ||
		containsAny(c.Name, []string{"便利店", "七楼", "安全屋", "观察位", "医院", "鬼市", "客户", "账", "债", "权", "担保"})
}

func assetOperationNeed(c domain.ResourceClaim) string {
	if c.Status == "pending" {
		return "确认成交/保留残余风险/标记废弃，避免后续写成既成事实。"
	}
	switch {
	case containsAny(c.Kind, []string{"asset", "permission", "system"}):
		return "补充容量、权利边界、负债、日常消耗和审计暴露。"
	case containsAny(c.Kind, []string{"guarantee"}):
		return "明确担保人、追索对象、失效条件和谁承担损耗。"
	case containsAny(c.Kind, []string{"debt", "claim"}):
		return "明确债权优先级、回收截止和失败后果。"
	default:
		return "在下一次相关场景中说明可解决问题与不能解决问题。"
	}
}

func assetNextTrigger(c domain.ResourceClaim) string {
	if c.Risk != "" {
		return compactProgressText(c.Risk, 120)
	}
	if c.Status == "pending" {
		return "下次相关章节做状态清账。"
	}
	return "下一次使用该资源时必须留下新收益或新账单。"
}
