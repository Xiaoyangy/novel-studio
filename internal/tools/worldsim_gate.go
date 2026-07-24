package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// 世界推演（离屏 tick）卡点的单一事实源。两个卡点：
//   - 第 1 章写作前：初始 world_tick 必须已生成（离屏世界有信息流，不是空基线）；
//   - 弧/卷边界 expand_arc/append_volume 前：world_tick 必须已推进到弧末。
// 未启用世界推演（无 tick 工件）的短篇/简单项目一律零影响。

// worldSimEnabled 报告项目是否启用了世界推演（zero-init 会写 world_tick.json 基线）。
func worldSimEnabled(st *store.Store) bool {
	tick, err := st.WorldSim.LoadTick()
	return err == nil && tick != nil
}

// worldTickSubstantive 报告离屏世界是否已有实质信息流（≥1 条镜头外事件）。
// 空的 v0-a0 基线（0 事件）不算——那只是占位，世界还没"活起来"。
func worldTickSubstantive(st *store.Store) bool {
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil || strings.TrimSpace(tick.TickID) == "" || tick.TickID == "v0-a0" || tick.EventCount <= 0 {
		return false
	}
	events, err := st.WorldSim.LoadWorldEvents()
	return err == nil && len(events) > 0
}

// InitialWorldTickQualityIssues returns blocking issues for the opening world
// tick. These are not style warnings: if the tick references unknown actors,
// downstream planning can consume a different project/genre without noticing.
func InitialWorldTickQualityIssues(st *store.Store) []string {
	if st == nil {
		return []string{"world_tick 校验缺少项目存储"}
	}
	tick, err := st.WorldSim.LoadTick()
	if err != nil {
		return []string{fmt.Sprintf("world_tick 不可读: %v", err)}
	}
	if tick == nil || strings.TrimSpace(tick.TickID) == "" || tick.TickID == "v0-a0" || tick.EventCount <= 0 {
		return []string{"world_tick 仍是空基线或缺少事件计数"}
	}
	var issues []string
	for _, warning := range tick.Warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			issues = append(issues, fmt.Sprintf("initial world_tick 留有未解决 warning: %s", warning))
		}
	}
	if tick.ThroughChapter != 0 {
		issues = append(issues, fmt.Sprintf("开篇 world_tick.through_chapter=%d；初始推演必须严格停在第0章", tick.ThroughChapter))
	}
	if tick.Volume > 0 && tick.Arc > 0 {
		expectedTickID := fmt.Sprintf("v%d-a%d", tick.Volume, tick.Arc)
		if tick.TickID != expectedTickID {
			issues = append(issues, fmt.Sprintf("world_tick.tick_id=%q 与 volume/arc 推导值 %q 不一致", tick.TickID, expectedTickID))
		}
	}
	issues = append(issues, worldTickGenerationIssues(st, *tick)...)

	events, err := st.WorldSim.LoadWorldEvents()
	if err != nil {
		return append(issues, fmt.Sprintf("world_events 不可读: %v", err))
	}
	if len(events) == 0 {
		return append(issues, "world_events 为空")
	}
	currentTickEvents := 0
	hasChapterZeroEvent := false
	for _, event := range events {
		if strings.TrimSpace(event.TickID) == strings.TrimSpace(tick.TickID) {
			currentTickEvents++
			if event.Chapter == 0 {
				hasChapterZeroEvent = true
			}
		}
	}
	if currentTickEvents != tick.EventCount {
		issues = append(issues, fmt.Sprintf(
			"world_tick.event_count=%d，但事件账本中属于当前 tick %q 的事件为 %d 条",
			tick.EventCount, tick.TickID, currentTickEvents,
		))
	}
	if !hasChapterZeroEvent {
		issues = append(issues, fmt.Sprintf("当前 tick %q 至少需要一条 chapter=0 的开篇前事件", tick.TickID))
	}

	known, knownIssues := worldTickKnownActorSetWithIssues(st)
	issues = append(issues, knownIssues...)
	characterIdentities := worldTickCharacterGenderIdentityIndex(st)
	firstVisible, boundaryIssues := worldTickCharacterFirstVisibleChaptersWithIssues(st)
	issues = append(issues, boundaryIssues...)
	issues = append(issues, worldTickChapterOneTimeAnchorIssues(st, events)...)
	for _, event := range events {
		reported := map[string]bool{}
		if strings.TrimSpace(event.TickID) != strings.TrimSpace(tick.TickID) {
			issues = append(issues, fmt.Sprintf(
				"world_tick 事件 %q 的 tick_id=%q，与当前 tick %q 不一致",
				compactWorldTickIssue(event.Summary), event.TickID, tick.TickID,
			))
		}
		if err := event.Validate(); err != nil {
			issues = append(issues, fmt.Sprintf("world_tick 事件 %q 结构非法: %v", compactWorldTickIssue(event.Summary), err))
		}
		if event.Chapter > tick.ThroughChapter {
			issues = append(issues, fmt.Sprintf(
				"world_tick 事件 %q 发生于第%d章，超出游标 through_chapter=%d",
				compactWorldTickIssue(event.Summary), event.Chapter, tick.ThroughChapter,
			))
		}
		if event.VisibilityChapter < 1 {
			issues = append(issues, fmt.Sprintf("world_tick 事件 %q 的 visibility_chapter=%d；开篇事件最早只能从第1章进入主角视野", compactWorldTickIssue(event.Summary), event.VisibilityChapter))
		}
		if len(event.Actors) == 0 {
			issues = append(issues, fmt.Sprintf("world_tick 事件 %q 缺少 actor；初始推演不能产生无归属事件", compactWorldTickIssue(event.Summary)))
		}
		for _, actor := range event.Actors {
			actor = strings.TrimSpace(actor)
			if actor == "" {
				issues = append(issues, fmt.Sprintf("world_tick 事件 %q 含空 actor", compactWorldTickIssue(event.Summary)))
				continue
			}
			if _, ok := known[actor]; !ok {
				issues = append(issues, fmt.Sprintf("world_tick 事件 %q 的 actor %q 不在角色册/势力册/别名中", compactWorldTickIssue(event.Summary), actor))
				continue
			}
			if boundary, ok := firstVisible[actor]; ok {
				issues = append(issues, worldTickVisibilityBoundaryIssue(event, boundary, reported)...)
			}
		}
		for _, field := range worldTickVisibleEventTextFields(event) {
			for alias, boundary := range firstVisible {
				if utf8.RuneCountInString(alias) < 2 || !strings.Contains(field.Text, alias) {
					continue
				}
				issues = append(issues, worldTickVisibilityBoundaryIssue(event, boundary, reported)...)
			}
		}
		issues = append(issues, worldTickFemaleActorPronounIssues(event, characterIdentities)...)
	}

	artifactTexts, textIssues := worldTickScannableArtifactTexts(st, events)
	issues = append(issues, textIssues...)
	forbidden, forbiddenIssues := worldTickExplicitForbiddenTerms(st)
	issues = append(issues, forbiddenIssues...)
	issues = append(issues, worldTickForbiddenContaminationIssues(artifactTexts, forbidden)...)
	return issues
}

var worldTickExplicitDurationRE = regexp.MustCompile(`(?:[0-9０-９]+|[〇零一二三四五六七八九十百千万两]+)(?:个)?(?:小时|日|天|周|个月|月|年)`)

var worldTickRelativeTimeAnchors = []string{
	"次日上午", "次日下午", "次日清晨", "次日夜里", "次日", "翌日上午", "翌日下午", "翌日",
	"当日上午", "当日下午", "当日清晨", "当日夜里", "当日", "当天", "当晚", "今夜", "今晚", "明早", "明日", "明天",
}

// worldTickChapterOneTimeAnchorIssues keeps explicit chapter-one clocks from
// disappearing during the Coordinator -> Architect handoff. The initial tick
// may leave the action pending, but it cannot silently replace a sealed
// four-hour/next-morning deadline with an unbounded setup. Projects without an
// explicit time expression are unaffected.
func worldTickChapterOneTimeAnchorIssues(st *store.Store, events []domain.WorldEvent) []string {
	outline, _ := worldTickAuthoritativeOutlineWithIssues(st)
	if len(outline) == 0 {
		return nil
	}
	chapterOne := outline[0]
	source := strings.TrimSpace(chapterOne.CoreEvent + "\n" + chapterOne.Hook)
	anchors := worldTickExtractChapterOneTimeAnchors(source)
	if len(anchors) == 0 {
		return nil
	}
	var visible strings.Builder
	for _, event := range events {
		if visible.Len() > 0 {
			visible.WriteByte('\n')
		}
		for _, field := range worldTickVisibleEventTextFields(event) {
			visible.WriteString(field.Text)
			visible.WriteByte('\n')
		}
	}
	corpus := visible.String()
	var issues []string
	for _, anchor := range anchors {
		if !strings.Contains(corpus, anchor) {
			issues = append(issues, fmt.Sprintf(
				"initial world_tick 未保留第1章显式时间锚点 %q；必须以 pending/条件式事件承接，不能在转交中丢失或改写",
				anchor,
			))
		}
	}
	return issues
}

func worldTickExtractChapterOneTimeAnchors(text string) []string {
	seen := map[string]struct{}{}
	var anchors []string
	for _, anchor := range worldTickExplicitDurationRE.FindAllString(text, -1) {
		anchor = strings.TrimSpace(anchor)
		if anchor == "" {
			continue
		}
		if _, ok := seen[anchor]; ok {
			continue
		}
		seen[anchor] = struct{}{}
		anchors = append(anchors, anchor)
	}
	for _, anchor := range worldTickRelativeTimeAnchors {
		if !strings.Contains(text, anchor) {
			continue
		}
		covered := false
		for _, existing := range anchors {
			if strings.Contains(existing, anchor) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		seen[anchor] = struct{}{}
		anchors = append(anchors, anchor)
	}
	return anchors
}

func worldTickGenerationIssues(st *store.Store, tick domain.WorldTick) []string {
	progress, err := st.Progress.Load()
	if err != nil {
		return []string{fmt.Sprintf("progress 不可读，无法绑定 world_tick generation: %v", err)}
	}
	if progress == nil || strings.TrimSpace(progress.GenerationID) == "" {
		if strings.TrimSpace(tick.GenerationID) != "" {
			return []string{fmt.Sprintf(
				"world_tick.generation_id=%q，但 progress 没有活动 generation；拒绝消费来源不明的推演游标",
				tick.GenerationID,
			)}
		}
		return nil
	}
	activeGeneration := strings.TrimSpace(progress.GenerationID)
	var issues []string
	if strings.TrimSpace(tick.GenerationID) != activeGeneration {
		issues = append(issues, fmt.Sprintf(
			"world_tick.generation_id=%q 与活动 generation %q 不一致",
			tick.GenerationID, activeGeneration,
		))
	}
	policy, err := st.LoadSimulationRestartPolicy()
	if err != nil {
		return append(issues, fmt.Sprintf("simulation_restart_policy 不可读，无法绑定 world_tick generation %q: %v", activeGeneration, err))
	}
	if policy == nil {
		return append(issues, fmt.Sprintf("活动 generation=%q，但缺少 simulation_restart_policy，world_tick 无法证明属于当前推演", activeGeneration))
	}
	if !policy.Active {
		issues = append(issues, fmt.Sprintf("活动 generation=%q，但 simulation_restart_policy 未激活", activeGeneration))
	}
	if strings.TrimSpace(policy.GenerationID) != activeGeneration {
		issues = append(issues, fmt.Sprintf(
			"world_tick generation 边界不一致：progress=%q，simulation_restart_policy=%q",
			activeGeneration, policy.GenerationID,
		))
	}
	policyTime, policyErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(policy.GeneratedAt))
	tickTime, tickErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(tick.UpdatedAt))
	if policyErr != nil {
		issues = append(issues, fmt.Sprintf("simulation_restart_policy.generated_at=%q 不可解析，无法验证 world_tick generation 边界", policy.GeneratedAt))
	}
	if tickErr != nil {
		issues = append(issues, fmt.Sprintf("world_tick.updated_at=%q 不可解析，无法验证其属于活动 generation %q", tick.UpdatedAt, activeGeneration))
	}
	if policyErr == nil && tickErr == nil && tickTime.Before(policyTime) {
		issues = append(issues, fmt.Sprintf(
			"world_tick.updated_at=%s 早于活动 generation %q 的重启边界 %s",
			tick.UpdatedAt, activeGeneration, policy.GeneratedAt,
		))
	}
	return issues
}

type worldTickTextField struct {
	Source string
	Text   string
}

func worldTickVisibleEventTextFields(event domain.WorldEvent) []worldTickTextField {
	return worldTickNonEmptyTextFields(
		worldTickTextField{Source: "world_event.location", Text: event.Location},
		worldTickTextField{Source: "world_event.summary", Text: event.Summary},
		worldTickTextField{Source: "world_event.consequence", Text: event.Consequence},
		worldTickTextField{Source: "world_event.visibility_path", Text: event.VisibilityPath},
	)
}

func worldTickNonEmptyTextFields(fields ...worldTickTextField) []worldTickTextField {
	out := fields[:0]
	for _, field := range fields {
		field.Text = strings.TrimSpace(field.Text)
		if field.Text != "" {
			out = append(out, field)
		}
	}
	return out
}

type worldTickCharacterBoundary struct {
	Character string
	Chapter   int
}

const worldTickCharacterPronounLookbackRunes = 48

type worldTickCharacterIdentitySurface struct {
	Canonical string
	Surface   []rune
}

type worldTickCharacterGenderIdentities struct {
	BySurface      map[string]string
	Surfaces       []worldTickCharacterIdentitySurface
	ExplicitFemale map[string]struct{}
}

// worldTickCharacterGenderIdentityIndex derives gender only from explicit,
// character-local evidence. Role labels such as "女性..."/"女主" are direct
// evidence. For generic role labels, two sentence-leading "她" references and
// no sentence-leading "他" reference are required, so a single mention of some
// other woman in a character card cannot silently assign gender.
func worldTickCharacterGenderIdentityIndex(st *store.Store) worldTickCharacterGenderIdentities {
	index := worldTickCharacterGenderIdentities{
		BySurface:      map[string]string{},
		ExplicitFemale: map[string]struct{}{},
	}
	if st == nil {
		return index
	}
	characters, err := st.Characters.Load()
	if err != nil {
		return index
	}
	ambiguous := map[string]struct{}{}
	for _, character := range characters {
		canonical := strings.TrimSpace(character.Name)
		if canonical == "" {
			continue
		}
		if worldTickCharacterExplicitlyFemale(character) {
			index.ExplicitFemale[canonical] = struct{}{}
		}
		for _, surface := range append([]string{canonical}, character.Aliases...) {
			surface = strings.TrimSpace(surface)
			if utf8.RuneCountInString(surface) < 2 {
				continue
			}
			if existing, ok := index.BySurface[surface]; ok && existing != canonical {
				delete(index.BySurface, surface)
				ambiguous[surface] = struct{}{}
				continue
			}
			if _, collision := ambiguous[surface]; !collision {
				index.BySurface[surface] = canonical
			}
		}
	}
	for surface, canonical := range index.BySurface {
		index.Surfaces = append(index.Surfaces, worldTickCharacterIdentitySurface{
			Canonical: canonical,
			Surface:   []rune(surface),
		})
	}
	sort.Slice(index.Surfaces, func(i, j int) bool {
		if len(index.Surfaces[i].Surface) != len(index.Surfaces[j].Surface) {
			return len(index.Surfaces[i].Surface) > len(index.Surfaces[j].Surface)
		}
		return index.Surfaces[i].Canonical < index.Surfaces[j].Canonical
	})
	return index
}

func worldTickCharacterExplicitlyFemale(character domain.Character) bool {
	role := strings.ToLower(strings.TrimSpace(character.Role))
	for _, segment := range strings.FieldsFunc(role, func(r rune) bool {
		return strings.ContainsRune("／/、，,；;|：:", r)
	}) {
		segment = strings.TrimSpace(segment)
		if strings.HasPrefix(segment, "女性") && !strings.Contains(segment, "的") {
			return true
		}
		switch segment {
		case "女主", "女主角", "女配", "女配角", "female lead", "female protagonist":
			return true
		}
	}
	profile := strings.TrimSpace(character.Description + "\n" + character.Arc)
	// Free-standing phrases such as "他的客户是一位女性" describe somebody
	// else and therefore cannot establish this character's gender. Accept an
	// explicit declaration only when it is bound to the canonical name.
	for _, marker := range []string{
		character.Name + "性别为女",
		character.Name + "性别：女",
		character.Name + "性别:女",
		character.Name + "明确为女性",
		character.Name + "是女性",
		character.Name + "是一名女性",
		character.Name + "是一位女性",
	} {
		if strings.Contains(profile, marker) {
			return true
		}
	}
	femaleSubjects := worldTickSentenceLeadingPronounCount(profile, "她")
	maleSubjects := worldTickSentenceLeadingPronounCount(profile, "他")
	return femaleSubjects >= 2 && maleSubjects == 0
}

func worldTickSentenceLeadingPronounCount(text, pronoun string) int {
	count := 0
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。！？!?；;\n\r", r)
	}) {
		sentence = strings.TrimLeft(strings.TrimSpace(sentence), "　 　\t‘’“”\"'（）()【】[]—-：:")
		if strings.HasPrefix(sentence, pronoun) {
			count++
		}
	}
	return count
}

func worldTickFemaleActorPronounIssues(event domain.WorldEvent, identities worldTickCharacterGenderIdentities) []string {
	if len(identities.ExplicitFemale) == 0 || len(identities.Surfaces) == 0 {
		return nil
	}
	femaleActors := map[string]struct{}{}
	for _, actor := range event.Actors {
		canonical, ok := identities.BySurface[strings.TrimSpace(actor)]
		if !ok {
			continue
		}
		if _, female := identities.ExplicitFemale[canonical]; female {
			femaleActors[canonical] = struct{}{}
		}
	}
	if len(femaleActors) == 0 {
		return nil
	}

	var issues []string
	for _, field := range worldTickVisibleEventTextFields(event) {
		runes := []rune(field.Text)
		reported := map[string]struct{}{}
		for i := range runes {
			if !worldTickIsLikelySelfActionMalePronoun(runes, i) {
				continue
			}
			canonical, mentionEnd, ok := worldTickNearestCharacterBefore(runes, i, identities.Surfaces)
			if !ok {
				continue
			}
			if _, isFemaleActor := femaleActors[canonical]; !isFemaleActor {
				continue
			}
			if worldTickHasCloserUnnamedPersonReferent(runes, mentionEnd, i) {
				continue
			}
			if _, duplicate := reported[canonical]; duplicate {
				continue
			}
			reported[canonical] = struct{}{}
			issues = append(issues, fmt.Sprintf(
				"world_tick 事件 %q 的 %s 在角色 %q 的近邻子句中使用男性代词“他”；角色册明确其为女性，请改用“她”或角色实名",
				compactWorldTickIssue(event.Summary), field.Source, canonical,
			))
		}
	}
	return issues
}

func worldTickNearestCharacterBefore(text []rune, pronounIndex int, surfaces []worldTickCharacterIdentitySurface) (string, int, bool) {
	start := pronounIndex - worldTickCharacterPronounLookbackRunes
	if start < 0 {
		start = 0
	}
	for i := pronounIndex - 1; i >= start; i-- {
		if strings.ContainsRune("。！？!?；;\n\r", text[i]) {
			start = i + 1
			break
		}
	}
	bestCanonical := ""
	bestEnd := -1
	bestLength := -1
	for _, identity := range surfaces {
		length := len(identity.Surface)
		for i := start; i+length <= pronounIndex; i++ {
			matched := true
			for j := range identity.Surface {
				if text[i+j] != identity.Surface[j] {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			end := i + length
			if end > bestEnd || (end == bestEnd && length > bestLength) {
				bestCanonical = identity.Canonical
				bestEnd = end
				bestLength = length
			}
		}
	}
	return bestCanonical, bestEnd, bestCanonical != ""
}

func worldTickIsLikelySelfActionMalePronoun(text []rune, index int) bool {
	if !worldTickIsStandaloneMalePronoun(text, index) {
		return false
	}
	tail := string(text[index+1:])
	for _, prefix := range []string{
		"可", "会", "将", "已", "已经", "仍", "还", "随后", "随即", "便", "就", "再", "才", "正", "正在",
		"必须", "不能", "不得", "需要", "应", "应该", "未", "没有", "不", "先", "又", "也", "只",
	} {
		if strings.HasPrefix(tail, prefix) {
			return true
		}
	}
	return false
}

func worldTickHasCloserUnnamedPersonReferent(text []rune, mentionEnd, pronounIndex int) bool {
	if mentionEnd < 0 || mentionEnd >= pronounIndex || pronounIndex > len(text) {
		return false
	}
	between := text[mentionEnd:pronounIndex]
	for _, role := range []string{
		"值班员", "复核员", "审核员", "管理员", "工作人员", "经办人", "负责人", "主管", "主任",
		"律师", "医生", "警员", "职员", "工人", "员工", "同事", "助理", "秘书", "门卫", "保安", "司机", "记者", "编辑", "老师", "顾问", "代表", "人员", "会计",
	} {
		roleRunes := []rune(role)
		for i := 0; i+len(roleRunes) <= len(between); i++ {
			matched := true
			for j := range roleRunes {
				if between[i+j] != roleRunes[j] {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			next := i + len(roleRunes)
			for next < len(between) && strings.ContainsRune(" \t\r\n　", between[next]) {
				next++
			}
			if next >= len(between) || between[next] != '的' {
				return true
			}
		}
	}
	// Chinese occupational nouns productively end in 员/师/官/者/士/生/手;
	// a finite title list would miss ordinary variants such as 档案员 or 审计师.
	// Treat such a noun as the nearer referent only when it is not itself inside
	// a longer occupational suffix and is not a genitive modifier (人员的风险告知).
	for i, r := range between {
		if !strings.ContainsRune("员师官者士生手", r) {
			continue
		}
		next := i + 1
		for next < len(between) && strings.ContainsRune(" \t\r\n　", between[next]) {
			next++
		}
		if next < len(between) && (between[next] == '的' || strings.ContainsRune("员师官者士生手", between[next])) {
			continue
		}
		return true
	}
	return false
}

func worldTickIsStandaloneMalePronoun(text []rune, index int) bool {
	if index < 0 || index >= len(text) || text[index] != '他' {
		return false
	}
	if index > 0 && strings.ContainsRune("其吉利排维", text[index-1]) {
		return false
	}
	if index+1 < len(text) && strings.ContainsRune("人们者日年处乡方国校物事项种类般杀律", text[index+1]) {
		return false
	}
	return true
}

func worldTickVisibilityBoundaryIssue(event domain.WorldEvent, boundary worldTickCharacterBoundary, reported map[string]bool) []string {
	if reported[boundary.Character] {
		return nil
	}
	if boundary.Chapter <= 0 {
		reported[boundary.Character] = true
		return []string{fmt.Sprintf(
			"world_tick 事件 %q 让角色 %q 进入可见信息，但当前大纲尚未安排其首次可见章节",
			compactWorldTickIssue(event.Summary), boundary.Character,
		)}
	}
	if event.VisibilityChapter >= boundary.Chapter {
		return nil
	}
	reported[boundary.Character] = true
	return []string{fmt.Sprintf(
		"world_tick 事件 %q 让角色 %q 在第%d章可见，早于大纲首次可见第%d章",
		compactWorldTickIssue(event.Summary), boundary.Character, event.VisibilityChapter, boundary.Chapter,
	)}
}

func worldTickCharacterFirstVisibleChapters(st *store.Store) map[string]worldTickCharacterBoundary {
	boundaries, _ := worldTickCharacterFirstVisibleChaptersWithIssues(st)
	return boundaries
}

func worldTickCharacterFirstVisibleChaptersWithIssues(st *store.Store) (map[string]worldTickCharacterBoundary, []string) {
	boundaries := map[string]worldTickCharacterBoundary{}
	chars, err := st.Characters.Load()
	if err != nil {
		return boundaries, []string{fmt.Sprintf("characters 不可读，无法校验 world_tick 角色首登场边界: %v", err)}
	}
	if len(chars) == 0 {
		return boundaries, nil
	}
	outline, outlineIssues := worldTickAuthoritativeOutlineWithIssues(st)
	primary := worldTickPrimaryProtagonistName(chars)
	for _, c := range chars {
		names := append([]string{strings.TrimSpace(c.Name)}, c.Aliases...)
		first := 0
		for i, entry := range outline {
			if worldTickOutlineEntryUsesCharacter(entry, names) {
				first = entry.Chapter
				if first <= 0 {
					first = i + 1
				}
				break
			}
		}
		if first == 0 && strings.TrimSpace(c.Name) == primary {
			first = 1
		}
		boundary := worldTickCharacterBoundary{Character: strings.TrimSpace(c.Name), Chapter: first}
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				if old, exists := boundaries[name]; !exists || old.Chapter <= 0 || (first > 0 && first < old.Chapter) {
					boundaries[name] = boundary
				}
			}
		}
	}
	return boundaries, outlineIssues
}

func worldTickAuthoritativeOutline(st *store.Store) []domain.OutlineEntry {
	outline, _ := worldTickAuthoritativeOutlineWithIssues(st)
	return outline
}

func worldTickAuthoritativeOutlineWithIssues(st *store.Store) ([]domain.OutlineEntry, []string) {
	if st == nil {
		return nil, []string{"缺少项目存储，无法读取角色首登场大纲"}
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, []string{fmt.Sprintf("layered_outline 不可读；拒绝退回可能过期的 flat outline: %v", err)}
	}
	if len(layered) > 0 {
		return domain.FlattenOutline(layered), nil
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		return nil, []string{fmt.Sprintf("outline 不可读，无法校验角色首登场边界: %v", err)}
	}
	return outline, nil
}

func worldTickPrimaryProtagonistName(chars []domain.Character) string {
	for _, c := range chars {
		role := strings.ToLower(strings.TrimSpace(c.Role))
		if role == "主角" || role == "第一主角" || role == "protagonist" {
			return strings.TrimSpace(c.Name)
		}
	}
	for _, c := range chars {
		role := strings.ToLower(strings.TrimSpace(c.Role))
		if role == "男主" || role == "男主角" || role == "女主" || role == "女主角" || role == "male lead" || role == "female lead" {
			return strings.TrimSpace(c.Name)
		}
	}
	return ""
}

func worldTickOutlineEntryUsesCharacter(entry domain.OutlineEntry, names []string) bool {
	texts := append([]string{entry.Title, entry.CoreEvent, entry.Hook}, entry.Scenes...)
	for _, text := range texts {
		for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
			return strings.ContainsRune("。；！？!?\n", r)
		}) {
			matched := false
			for _, name := range names {
				name = strings.TrimSpace(name)
				if name != "" && strings.Contains(clause, name) {
					matched = true
					break
				}
			}
			if matched && !worldTickFutureAuthorOnlyClause(clause) {
				return true
			}
		}
	}
	return false
}

func worldTickFutureAuthorOnlyClause(clause string) bool {
	return worldTickContainsAny(clause, "后续", "将来", "日后", "未来", "下一章") &&
		worldTickContainsAny(clause, "入场", "登场", "出场", "回归", "承接", "消费", "交给", "铺垫", "安排")
}

func worldTickContainsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func worldTickKnownActorSet(st *store.Store) map[string]struct{} {
	known, _ := worldTickKnownActorSetWithIssues(st)
	return known
}

func worldTickKnownActorSetWithIssues(st *store.Store) (map[string]struct{}, []string) {
	known := map[string]struct{}{}
	var issues []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			known[value] = struct{}{}
		}
	}
	if chars, err := st.Characters.Load(); err == nil {
		for _, c := range chars {
			add(c.Name)
			for _, alias := range c.Aliases {
				add(alias)
			}
		}
	} else {
		issues = append(issues, fmt.Sprintf("characters 不可读，world_tick actor 白名单不完整: %v", err))
	}
	if entries, err := st.Cast.Load(); err == nil {
		for _, e := range entries {
			add(e.Name)
			for _, alias := range e.Aliases {
				add(alias)
			}
		}
	} else {
		issues = append(issues, fmt.Sprintf("cast_ledger 不可读，world_tick actor 白名单不完整: %v", err))
	}
	if world, err := st.World.LoadBookWorld(); err == nil && world != nil {
		for _, faction := range world.Factions {
			add(faction.ID)
			add(faction.Name)
			for _, alias := range faction.Aliases {
				add(alias)
			}
		}
	} else if err != nil {
		issues = append(issues, fmt.Sprintf("book_world 不可读，world_tick actor 白名单不完整: %v", err))
	}
	return known, issues
}

func worldTickScannableArtifactTexts(st *store.Store, events []domain.WorldEvent) ([]worldTickTextField, []string) {
	var fields []worldTickTextField
	for i, event := range events {
		label := strings.TrimSpace(event.ID)
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		for actorIndex, actor := range event.Actors {
			fields = append(fields, worldTickTextField{
				Source: fmt.Sprintf("world_event[%s].actors[%d]", label, actorIndex),
				Text:   actor,
			})
		}
		for _, field := range worldTickVisibleEventTextFields(event) {
			field.Source = fmt.Sprintf("world_event[%s].%s", label, strings.TrimPrefix(field.Source, "world_event."))
			fields = append(fields, field)
		}
	}

	var issues []string
	ledger, err := st.WorldSim.LoadAgendaLedger()
	if err != nil {
		issues = append(issues, fmt.Sprintf("offscreen_agenda 不可读，无法执行初始 tick 污染扫描: %v", err))
	} else {
		for i, agenda := range ledger.Agendas {
			prefix := fmt.Sprintf("offscreen_agenda[%d:%s]", i, strings.TrimSpace(agenda.Name))
			fields = append(fields, worldTickNonEmptyTextFields(
				worldTickTextField{Source: prefix + ".name", Text: agenda.Name},
				worldTickTextField{Source: prefix + ".current_goal", Text: agenda.CurrentGoal},
				worldTickTextField{Source: prefix + ".motivation", Text: agenda.Motivation},
				worldTickTextField{Source: prefix + ".blocked_by", Text: agenda.BlockedBy},
			)...)
			for stepIndex, step := range agenda.Steps {
				fields = append(fields, worldTickTextField{
					Source: fmt.Sprintf("%s.steps[%d].description", prefix, stepIndex),
					Text:   step.Description,
				})
			}
		}
	}

	mood, err := st.Methodology.LoadSocialMood()
	if err != nil {
		issues = append(issues, fmt.Sprintf("social_mood 不可读，无法执行初始 tick 污染扫描: %v", err))
	} else if mood != nil {
		fields = append(fields, worldTickNonEmptyTextFields(
			worldTickTextField{Source: "social_mood.mood", Text: mood.Mood},
			worldTickTextField{Source: "social_mood.seasonal_mood", Text: mood.SeasonalMood},
		)...)
		for i, rumor := range mood.Rumors {
			fields = append(fields, worldTickNonEmptyTextFields(
				worldTickTextField{Source: fmt.Sprintf("social_mood.rumors[%d].text", i), Text: rumor.Text},
				worldTickTextField{Source: fmt.Sprintf("social_mood.rumors[%d].source_faction", i), Text: rumor.SourceFaction},
			)...)
		}
	}

	cast, err := st.WorldSim.LoadSimulationCast()
	if err != nil {
		issues = append(issues, fmt.Sprintf("simulation_tiers 不可读，无法执行初始 tick 污染扫描: %v", err))
	} else {
		for i, assignment := range cast.Assignments {
			fields = append(fields, worldTickNonEmptyTextFields(
				worldTickTextField{Source: fmt.Sprintf("simulation_tiers[%d].name", i), Text: assignment.Name},
				worldTickTextField{Source: fmt.Sprintf("simulation_tiers[%d].reason", i), Text: assignment.Reason},
			)...)
		}
	}

	world, err := st.World.LoadBookWorld()
	if err != nil {
		issues = append(issues, fmt.Sprintf("book_world 不可读，无法执行势力钟污染扫描: %v", err))
	} else if world != nil {
		for i, faction := range world.Factions {
			prefix := fmt.Sprintf("book_world.factions[%d:%s]", i, strings.TrimSpace(faction.Name))
			fields = append(fields, worldTickNonEmptyTextFields(
				worldTickTextField{Source: prefix + ".name", Text: faction.Name},
				worldTickTextField{Source: prefix + ".goal", Text: faction.Goal},
				worldTickTextField{Source: prefix + ".internal_tension", Text: faction.InternalTension},
			)...)
			for resourceIndex, resource := range faction.Resources {
				fields = append(fields, worldTickTextField{
					Source: fmt.Sprintf("%s.resources[%d]", prefix, resourceIndex),
					Text:   resource,
				})
			}
			for valueIndex, value := range faction.CoreValues {
				fields = append(fields, worldTickTextField{
					Source: fmt.Sprintf("%s.core_values[%d]", prefix, valueIndex),
					Text:   value,
				})
			}
			if faction.Clock != nil {
				fields = append(fields, worldTickNonEmptyTextFields(
					worldTickTextField{Source: prefix + ".clock.consequence", Text: faction.Clock.Consequence},
					worldTickTextField{Source: prefix + ".clock.pace", Text: faction.Clock.Pace},
				)...)
			}
		}
	}

	return worldTickNonEmptyTextFields(fields...), issues
}

type worldTickForbiddenTerm struct {
	Term   string
	Source string
}

func worldTickExplicitForbiddenTerms(st *store.Store) ([]worldTickForbiddenTerm, []string) {
	var inputs []worldTickTextField
	var direct []worldTickForbiddenTerm
	var issues []string
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		issues = append(issues, fmt.Sprintf("premise 不可读，无法派生项目禁题材: %v", err))
	} else if strings.TrimSpace(premise) != "" {
		inputs = append(inputs, worldTickTextField{Source: "premise", Text: premise})
	}
	snapshot, err := st.UserRules.Load()
	if err != nil {
		issues = append(issues, fmt.Sprintf("user_rules 不可读，无法派生项目禁题材: %v", err))
	} else if snapshot != nil {
		inputs = append(inputs,
			worldTickTextField{Source: "user_rules.preferences", Text: snapshot.Preferences},
			worldTickTextField{Source: "user_rules.genre", Text: snapshot.Structured.Genre},
			worldTickTextField{Source: "user_rules.uncertain", Text: strings.Join(snapshot.Uncertain, "\n")},
		)
		for _, phrase := range snapshot.Structured.ForbiddenPhrases {
			phrase = strings.TrimSpace(phrase)
			if utf8.RuneCountInString(phrase) >= 2 {
				direct = append(direct, worldTickForbiddenTerm{Term: phrase, Source: "user_rules.forbidden_phrases"})
			}
		}
	}

	byTerm := map[string]worldTickForbiddenTerm{}
	for _, term := range direct {
		worldTickRememberForbiddenTerm(byTerm, term)
	}
	for _, input := range inputs {
		for _, term := range worldTickExtractExplicitNegativeTopics(input.Text) {
			worldTickRememberForbiddenTerm(byTerm, worldTickForbiddenTerm{Term: term, Source: input.Source})
		}
	}
	terms := make([]worldTickForbiddenTerm, 0, len(byTerm))
	for _, term := range byTerm {
		terms = append(terms, term)
	}
	sort.Slice(terms, func(i, j int) bool {
		if terms[i].Term == terms[j].Term {
			return terms[i].Source < terms[j].Source
		}
		return terms[i].Term < terms[j].Term
	})
	return terms, issues
}

func worldTickRememberForbiddenTerm(terms map[string]worldTickForbiddenTerm, term worldTickForbiddenTerm) {
	term.Term = strings.TrimSpace(term.Term)
	if term.Term == "" {
		return
	}
	key := strings.ToLower(term.Term)
	if existing, ok := terms[key]; ok {
		if !strings.Contains(existing.Source, term.Source) {
			existing.Source += ", " + term.Source
			terms[key] = existing
		}
		return
	}
	terms[key] = term
}

var worldTickNegativeTopicMarkers = []string{
	"明确禁止出现", "不允许出现", "严禁出现", "不得出现", "不能出现", "不可出现", "不要出现", "禁止出现", "避免出现", "杜绝出现",
	"明确禁止写", "严禁写入", "严禁写", "不要写", "禁止写", "避免写", "不引入", "不涉及", "不包含", "不采用", "不使用", "不含", "不写",
	"杜绝", "排除", "禁止", "避免", "不要", "不是", "没有",
}

func worldTickExtractExplicitNegativeTopics(text string) []string {
	seen := map[string]struct{}{}
	var topics []string
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool {
		return strings.ContainsRune("。；;！？!?\n\r", r)
	}) {
		occurrences := worldTickNegativeMarkerOccurrences(clause)
		for i, occurrence := range occurrences {
			end := len(clause)
			if i+1 < len(occurrences) {
				end = occurrences[i+1].Index
			}
			tail := clause[occurrence.Index+len(occurrence.Marker) : end]
			// A semantic method boundary is not a lexical topic prohibition. For
			// example, "禁止用单份材料……解决作者、权属、补偿和债务"
			// forbids treating one source as dispositive; it does not forbid the
			// story from mentioning the protected subjects after "解决". Flattening
			// that sentence at list separators would turn core canon into false
			// forbidden terms.
			if worldTickNegativeTailIsContextualMethodRule(tail) {
				continue
			}
			for _, candidate := range worldTickSplitNegativeTopicTail(tail) {
				candidate = worldTickCleanNegativeTopic(candidate)
				if !worldTickUsefulForbiddenTopic(candidate) {
					continue
				}
				key := strings.ToLower(candidate)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				topics = append(topics, candidate)
			}
		}
	}
	return topics
}

func worldTickNegativeTailIsContextualMethodRule(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return false
	}
	for _, prefix := range []string{"把", "让"} {
		if strings.HasPrefix(tail, prefix) {
			return true
		}
	}
	methodPrefix := false
	for _, prefix := range []string{"用", "以", "通过", "依靠", "凭借", "将"} {
		if strings.HasPrefix(tail, prefix) {
			methodPrefix = true
			break
		}
	}
	if !methodPrefix {
		return false
	}
	return worldTickContainsAny(tail,
		"解决", "认定", "决定", "裁定", "修复", "完成", "证明", "替代", "换取", "交换",
		"覆盖", "改写", "处理", "推进", "达成", "消除", "洗清", "抹平", "纠正", "等同", "成为",
	)
}

type worldTickNegativeMarkerOccurrence struct {
	Index  int
	Marker string
}

func worldTickNegativeMarkerOccurrences(clause string) []worldTickNegativeMarkerOccurrence {
	var out []worldTickNegativeMarkerOccurrence
	for cursor := 0; cursor < len(clause); {
		best := worldTickNegativeMarkerOccurrence{Index: -1}
		for _, marker := range worldTickNegativeTopicMarkers {
			idx := strings.Index(clause[cursor:], marker)
			if idx < 0 {
				continue
			}
			idx += cursor
			if !worldTickNegativeMarkerHasProjectScope(clause, idx) {
				continue
			}
			if best.Index < 0 || idx < best.Index || (idx == best.Index && len(marker) > len(best.Marker)) {
				best = worldTickNegativeMarkerOccurrence{Index: idx, Marker: marker}
			}
		}
		if best.Index < 0 {
			break
		}
		out = append(out, best)
		cursor = best.Index + len(best.Marker)
	}
	return out
}

func worldTickNegativeMarkerHasProjectScope(clause string, index int) bool {
	prefix := strings.TrimSpace(clause[:index])
	if prefix == "" {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(prefix)
	if strings.ContainsRune("，,:：、/／（([{【", last) {
		return true
	}
	for _, subject := range []string{
		"本书", "作品", "故事", "剧情", "正文", "题材", "项目", "设定", "世界观",
		"世界信息流", "信息流", "叙事层", "主线", "内容",
	} {
		if strings.HasSuffix(prefix, subject) {
			return true
		}
	}
	return false
}

func worldTickSplitNegativeTopicTail(tail string) []string {
	replacer := strings.NewReplacer(
		"或者", "\n", "以及", "\n", "还有", "\n",
		"、", "\n", "，", "\n", ",", "\n", "/", "\n", "／", "\n", "|", "\n",
		"或", "\n", "和", "\n", "与", "\n", "及", "\n",
	)
	return strings.Split(replacer.Replace(tail), "\n")
}

func worldTickCleanNegativeTopic(topic string) string {
	topic = strings.Trim(strings.TrimSpace(topic), " \t：:。；;！!？?，,、/／|“”\"‘’'（）()[]【】《》")
	for _, prefix := range []string{"任何", "一切", "相关的", "相关", "写入", "写成", "写", "出现", "引入", "涉及", "包含", "采用", "使用", "带有"} {
		if strings.HasPrefix(topic, prefix) {
			topic = strings.TrimSpace(strings.TrimPrefix(topic, prefix))
			break
		}
	}
	if idx := strings.Index(topic, "等"); idx > 0 {
		topic = topic[:idx]
	}
	for {
		before := topic
		for _, suffix := range []string{"等偏离题材的元素", "偏离题材的元素", "题材元素", "类元素", "式表达", "类内容", "元素", "题材", "内容", "设定", "桥段", "情节", "描写", "表达", "风格", "路线"} {
			if strings.HasSuffix(topic, suffix) {
				topic = strings.TrimSpace(strings.TrimSuffix(topic, suffix))
				break
			}
		}
		if topic == before {
			break
		}
	}
	return strings.Trim(strings.TrimSpace(topic), " \t：:。；;！!？?，,、/／|“”\"‘’'（）()[]【】《》")
}

func worldTickUsefulForbiddenTopic(topic string) bool {
	count := utf8.RuneCountInString(topic)
	if count < 2 || count > 24 {
		return false
	}
	for _, prefix := range []string{"把", "让", "因此", "所以", "但", "而", "同时", "整体", "保持", "采用", "改为", "重点", "这些", "该", "是否"} {
		if strings.HasPrefix(topic, prefix) {
			return false
		}
	}
	return !worldTickContainsAny(topic, "字面短语", "提升为", "structured", "字段不", "规则不")
}

func worldTickForbiddenContaminationIssues(fields []worldTickTextField, forbidden []worldTickForbiddenTerm) []string {
	if len(fields) == 0 || len(forbidden) == 0 {
		return nil
	}
	reported := map[string]bool{}
	var issues []string
	for _, field := range fields {
		text := strings.ToLower(strings.TrimSpace(field.Text))
		if text == "" {
			continue
		}
		for _, term := range forbidden {
			if !strings.Contains(text, strings.ToLower(term.Term)) {
				continue
			}
			key := field.Source + "\x00" + strings.ToLower(term.Term)
			if reported[key] {
				continue
			}
			reported[key] = true
			issues = append(issues, fmt.Sprintf(
				"初始 world_tick 工件 %s 命中项目明确禁题材/禁语 %q（来源: %s）: %q",
				field.Source, term.Term, term.Source, compactWorldTickIssue(field.Text),
			))
		}
	}
	return issues
}

func compactWorldTickIssue(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= 30 {
		return string(runes)
	}
	return string(runes[:30]) + "..."
}

// EnsureInitialWorldTickForChapterOne 第 1 章写作前的初始 world_tick 硬卡点。
// 仅长篇（分层）项目：启用世界推演但离屏世界还没有任何事件时拒绝——离屏世界的
// 信息流是下游推演所需，必须在第 1 章推演/渲染前由 Architect 先跑一次开局
// save_world_tick 建立。短篇（扁平）与第 1 章已写完（重写路径）时不拦。
func EnsureInitialWorldTickForChapterOne(st *store.Store) error {
	if !ChapterOnePendingFirstWrite(st) || !worldSimEnabled(st) {
		return nil
	}
	if !worldSimRequiresInitialTick(st) {
		return nil // 短篇/扁平项目不强制离屏世界推演
	}
	if worldTickSubstantive(st) {
		if issues := InitialWorldTickQualityIssues(st); len(issues) == 0 {
			return nil
		} else {
			return fmt.Errorf("第 1 章写作前的 world_tick 不合格：%s", strings.Join(issues, "；"))
		}
	}
	return fmt.Errorf("第 1 章写作前，离屏世界推演游标必须已生成：请派 architect_long 先跑一次开局 save_world_tick，" +
		"按各离屏角色 agenda 与势力钟推进出开局前的镜头外事件（每条带 visibility_chapter/visibility_path），" +
		"让世界在第 1 章之前已经在自转，之后再推演/渲染第 1 章")
}

func worldSimRequiresInitialTick(st *store.Store) bool {
	progress, err := st.Progress.Load()
	if err == nil && progress != nil {
		if progress.Layered {
			return true
		}
		if progress.TotalChapters > 30 {
			return true
		}
	}
	layered, err := st.Outline.LoadLayeredOutline()
	return err == nil && len(layered) > 0
}

// EnsureWorldTickCurrent 弧/卷边界的 world_tick 硬卡点：world_tick 落后已完成正文时拒绝。
// 展开下一弧/追加新卷前，镜头外世界必须已推进到刚结束的弧末——否则下一弧规划
// 消费不到离屏事件与伏笔素材。
func EnsureWorldTickCurrent(st *store.Store) error {
	if !worldSimEnabled(st) {
		return nil
	}
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil
	}
	latest := progress.LatestCompleted()
	if latest <= tick.ThroughChapter {
		return nil
	}
	return fmt.Errorf("镜头外世界推演落后正文：world_tick 只推进到第 %d 章，正文已完成到第 %d 章。"+
		"展开下一弧/追加新卷前必须先调 save_world_tick 把世界推进到弧末——"+
		"推进各离屏 agenda、产生镜头外事件、拨势力钟，并按 story_calendar 每章天数推算事件 visibility_chapter，再 expand_arc/append_volume",
		tick.ThroughChapter, latest)
}
