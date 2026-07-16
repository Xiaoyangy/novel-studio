package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The quantity contract is intentionally narrow. It only recognizes explicit
// result targets (for example, "扩到十家") and explicit hard caps (for example,
// "维持五摊上限"). Vague quantities, alternatives and future hooks remain the
// planner's responsibility instead of becoming brittle numeric lint rules.
const chapterQuantityNumberPattern = `(?:[0-9０-９]+|[零〇一二两三四五六七八九十百千万]+)`
const chapterQuantityUnitPattern = `(?:摊位|摊主|摊|商户|家|订单|笔|单|份(?:交易|订单|餐食)?)`

var chapterQuantityTargetPattern = regexp.MustCompile(
	`(?:扩到|扩至|增至|达到|累计到|累计至|增加到|增加至|扩为|完成|形成|做到|达成)\s*第?\s*(` +
		chapterQuantityNumberPattern + `)\s*个?\s*(` + chapterQuantityUnitPattern + `)`,
)

var chapterQuantityTargetNegationPrefixPattern = regexp.MustCompile(
	`(?:不(?:再|会|能|应|该|可|允许|接受|考虑|选择|决定|打算|准备|计划|同意|支持|要求|需要)?|暂不(?:再|考虑|选择|决定|打算|准备|计划)?|不是|并非|未|尚未|还未|拒绝|否决|避免|禁止|无需|无须|别|莫)\s*$`,
)

var chapterQuantityTargetAlternativeSuffixPattern = regexp.MustCompile(
	`^\s*(?:(?:，|,)?\s*(?:或者|或是|或\s*(?:(?:保持|维持|保留|只|仅|暂|不|继续|改|缩|留|退|扩|增|减|降|取消)))|(?:，|,)?\s*否则)`,
)

var chapterQuantityTargetAlternativeQuantitySuffixPattern = regexp.MustCompile(
	`^\s*(?:，|,)?\s*(?:或者|或是|或)\s*(` + chapterQuantityNumberPattern + `)\s*个?\s*(` + chapterQuantityUnitPattern + `)`,
)

var chapterQuantityTargetAlternativeAnnotationSuffixPattern = regexp.MustCompile(
	`^\s*(?:[（(]\s*(?:备选|可选|候选)|(?:作为|列为|仅作|只作|用作|用于)\s*(?:一个)?\s*(?:备选|可选|候选))`,
)

var chapterQuantityTargetAlternativePrefixPattern = regexp.MustCompile(`(?:或者|或是|或)\s*$`)

var chapterQuantityTargetAlternativeContextPrefixPattern = regexp.MustCompile(
	`(?:(?:要么|否则)(?:就|则|只能|只好|选择|考虑|决定|改为|改成)?|` +
		`(?:作为|列为|仅作|只作|用作|用于)?(?:备选|可选|候选)(?:方案|选项|路径|目标)?(?:是|为|选择|考虑|决定)?)\s*$`,
)

var chapterQuantityTargetTentativePrefixPattern = regexp.MustCompile(
	`(?:可能|也许|或许|计划|考虑|尝试|争取)(?:会|要|先|再|继续|仍然|仍|本章|当天|当晚)?\s*$`,
)

var chapterQuantityTargetConditionalPrefixPattern = regexp.MustCompile(
	`(?:(?:若[^，,；;。.!！？?\n\r]{0,24}(?:则|就|才))|` +
		`(?:(?:如果|假如|倘若)[^，,；;。.!！？?\n\r]{0,24})|` +
		`(?:视(?:情况|客流|现场|结果|反馈|条件)[^，,；;。.!！？?\n\r]{0,16}(?:决定)?是否))\s*$`,
)

var chapterQuantityTargetCompletedCancellationSuffixPattern = regexp.MustCompile(
	`^\s*(?:的)?\s*(?:(?:原有|原先|此前|旧的|原|旧)\s*)?(?:计划|方案|目标|提议|安排|设想|要求)?\s*` +
		`(?:，|,)?\s*(?:已经取消|已取消|已经被取消|已被取消|已经否决|已否决|已经被否决|已被否决|已经作废|已作废|不再适用)`,
)

var chapterQuantityCapPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:(?:维持|保持)\s*(?:在)?|控制在\s*)(?:原有|原)?\s*(` + chapterQuantityNumberPattern + `)\s*个?\s*(` + chapterQuantityUnitPattern + `)\s*(?:的)?\s*(?:上限|封顶|以内|范围)`),
	regexp.MustCompile(`(?:最多|不超过|仅限|只限|上限(?:为|是)?)\s*(` + chapterQuantityNumberPattern + `)\s*个?\s*(` + chapterQuantityUnitPattern + `)`),
}

var chapterQuantityCapNegationPrefixPattern = regexp.MustCompile(
	`(?:不再|不必|不需|不需要|不用|不会|不能|不打算|不准备|不考虑|不同意|不选择|不应|不该|不可|不是|` +
		`并不|而非|并非|无需|无须|取消|打破|放弃|拒绝|避免|禁止|否决|废除|撤销|作废)` +
		`(?:原先|原有|此前|旧的|继续|仍然|仍|再)?\s*$`,
)

var chapterQuantityCapCompletedSuffixPattern = regexp.MustCompile(
	`^\s*(?:的)?\s*(?:，|,)?\s*(?:不再适用|已经取消|已取消|已经被取消|已被取消|已经作废|已作废|已经被打破|已被打破)`,
)

var chapterQuantityCapTentativePrefixPattern = regexp.MustCompile(
	`(?:可能|也许|或许|未必|不一定|考虑|打算|准备|计划)(?:会|要|先|再|继续|仍然|仍|本章|当天|当晚)?\s*$`,
)

var chapterQuantityCapAlternativeExpansionSuffixPattern = regexp.MustCompile(
	`^\s*(?:，|,)?\s*(?:或者|或是|或)\s*(?:直接\s*)?(?:扩围|扩大|扩到|扩至|增至|增加|取消|放弃|打破|不再维持|不维持|改为|改成|转为)`,
)

type chapterQuantityText struct {
	Source string
	Text   string
}

type chapterQuantityClaim struct {
	Entity string
	Count  int
	Source string
	Text   string
	Offset int
}

type chapterQuantityConflict struct {
	Entity chapterQuantityClaim
	Target chapterQuantityClaim
	Cap    chapterQuantityClaim
}

func chapterWorldSimulationQuantityGaps(s *store.Store, sim domain.ChapterWorldSimulation) []string {
	targets := chapterQuantityTargetClaims(chapterQuantityContractTargets(s, sim.Chapter))
	caps := chapterQuantityCapClaims(chapterWorldSimulationCapTexts(s, sim))
	return formatChapterQuantityConflicts(sim.Chapter, chapterQuantityConflicts(targets, caps))
}

// ValidateChapterQuantityResultContract prevents a finalized POV plan from
// binding an explicit result target to a smaller hard cap. It is exported so
// the Host router and render-only reuse path can reject legacy plans created by
// older binaries before dispatching an expensive Drafter run.
func ValidateChapterQuantityResultContract(s *store.Store, plan domain.ChapterPlan) error {
	if s == nil || plan.Chapter <= 0 {
		return fmt.Errorf("invalid chapter quantity contract for chapter %d: %w", plan.Chapter, errs.ErrToolPrecondition)
	}
	targetTexts := chapterQuantityContractTargets(s, plan.Chapter)
	targetTexts = append(targetTexts, chapterPlanQuantityTargetTexts(plan)...)
	conflicts := chapterQuantityConflicts(
		chapterQuantityTargetClaims(targetTexts),
		chapterQuantityCapClaims(chapterPlanQuantityCapTexts(plan)),
	)
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %w", strings.Join(formatChapterQuantityConflicts(plan.Chapter, conflicts), "；"), errs.ErrToolPrecondition)
}

func chapterQuantityContractTargets(s *store.Store, chapter int) []chapterQuantityText {
	var out []chapterQuantityText
	if s == nil || chapter <= 0 {
		return out
	}
	if entry, err := s.Outline.GetChapterOutline(chapter); err == nil && entry != nil {
		out = appendQuantityText(out, "outline.core_event", entry.CoreEvent)
		for i, scene := range entry.Scenes {
			out = appendQuantityText(out, fmt.Sprintf("outline.scenes[%d]", i), scene)
		}
		// Deliberately exclude entry.Hook: it normally describes a future
		// chapter and must not become the current chapter's result contract.
	}
	if brief, err := s.Drafts.LoadRewriteBrief(chapter); err == nil && strings.TrimSpace(brief) != "" {
		for _, heading := range []string{"合同漏项", "必须修正", "验收条件"} {
			for i, item := range rewriteBriefTopLevelBullets(brief, heading) {
				out = appendQuantityText(out, fmt.Sprintf("rewrite_brief.%s[%d]", heading, i), item)
			}
		}
	}
	return out
}

func chapterWorldSimulationCapTexts(s *store.Store, sim domain.ChapterWorldSimulation) []chapterQuantityText {
	var out []chapterQuantityText
	projection := sim.ProtagonistProjection
	out = appendQuantityText(out, "world_simulation.protagonist_projection.chosen_decision", projection.ChosenDecision)
	protagonist := inferCommitProtagonist(s)
	for _, decision := range sim.CharacterDecisions {
		if strings.TrimSpace(decision.Character) != protagonist {
			continue
		}
		out = appendQuantityText(out, "world_simulation.protagonist.decision", decision.Decision)
	}
	return out
}

func chapterPlanQuantityTargetTexts(plan domain.ChapterPlan) []chapterQuantityText {
	var out []chapterQuantityText
	out = appendQuantityText(out, "plan.goal", plan.Goal)
	for i, item := range plan.Contract.RequiredBeats {
		out = appendQuantityText(out, fmt.Sprintf("plan.required_beats[%d]", i), item)
	}
	for i, item := range plan.Contract.PayoffPoints {
		out = appendQuantityText(out, fmt.Sprintf("plan.payoff_points[%d]", i), item)
	}
	for i, item := range plan.CausalSimulation.OutcomeShift {
		out = appendQuantityText(out, fmt.Sprintf("plan.outcome_shift[%d]", i), item)
	}
	reward := plan.CausalSimulation.ReaderRewardPlan
	out = appendQuantityText(out, "plan.reader_reward.small_win", reward.FirstChapterSmallWin)
	out = appendQuantityText(out, "plan.reader_reward.payoff_visibility", reward.PayoffVisibility)
	return out
}

func chapterPlanQuantityCapTexts(plan domain.ChapterPlan) []chapterQuantityText {
	var out []chapterQuantityText
	out = appendQuantityText(out, "plan.protagonist_decision", plan.CausalSimulation.ProtagonistDecision)
	return out
}

func appendQuantityText(items []chapterQuantityText, source, value string) []chapterQuantityText {
	value = strings.TrimSpace(value)
	if value == "" {
		return items
	}
	return append(items, chapterQuantityText{Source: source, Text: value})
}

func chapterQuantityTargetClaims(items []chapterQuantityText) []chapterQuantityClaim {
	claims := chapterQuantityClaims(items, []*regexp.Regexp{chapterQuantityTargetPattern})
	filtered := claims[:0]
	for _, claim := range claims {
		if chapterQuantityTargetIsNonBinding(claim, items) {
			continue
		}
		filtered = append(filtered, claim)
	}
	return filtered
}

func chapterQuantityTargetIsNonBinding(claim chapterQuantityClaim, items []chapterQuantityText) bool {
	for _, item := range items {
		if item.Source != claim.Source || claim.Offset < 0 || claim.Offset+len(claim.Text) > len(item.Text) ||
			item.Text[claim.Offset:claim.Offset+len(claim.Text)] != claim.Text {
			continue
		}
		before := item.Text[:claim.Offset]
		after := item.Text[claim.Offset+len(claim.Text):]
		clausePrefix := chapterQuantityClausePrefix(before)

		// Explicit future windows are not a current-chapter result contract,
		// even when the future clause itself uses strong wording such as
		// "下一章必须扩到十家".
		if chapterQuantityTargetHasFuturePrefix(clausePrefix) {
			return true
		}
		if chapterQuantityTargetTentativePrefixPattern.MatchString(clausePrefix) ||
			chapterQuantityTargetConditionalPrefixPattern.MatchString(clausePrefix) {
			return true
		}

		compactPrefix := compactChapterQuantityPrefix(clausePrefix)
		// Necessity and double negation are not target cancellation.
		forcedPositive := chapterQuantityPrefixHasDoubleNegation(compactPrefix) ||
			strings.HasSuffix(compactPrefix, "不得不再") ||
			strings.HasSuffix(compactPrefix, "不能不再")
		if !forcedPositive && chapterQuantityTargetNegationPrefixPattern.MatchString(clausePrefix) {
			return true
		}

		// Alternatives and fallbacks are planning branches, not guaranteed
		// result quantities. Keep this local to the target's sentence so an
		// unrelated "或" elsewhere cannot erase a hard target.
		sentenceSuffix := chapterQuantitySentenceSuffix(after)
		if chapterQuantityTargetCompletedCancellationSuffixPattern.MatchString(sentenceSuffix) ||
			chapterQuantityTargetAlternativeSuffixPattern.MatchString(sentenceSuffix) ||
			chapterQuantityTargetHasAlternativeQuantitySuffix(sentenceSuffix, claim.Entity) ||
			chapterQuantityTargetAlternativeAnnotationSuffixPattern.MatchString(sentenceSuffix) ||
			chapterQuantityTargetHasAlternativePrefix(clausePrefix) {
			return true
		}
	}
	return false
}

func chapterQuantityClausePrefix(text string) string {
	if index := strings.LastIndexAny(text, "，,；;。.!！？?\n\r"); index >= 0 {
		return text[index+1:]
	}
	return text
}

func chapterQuantitySentenceSuffix(text string) string {
	if index := strings.IndexAny(text, "；;。.!！？?\n\r"); index >= 0 {
		return text[:index]
	}
	return text
}

func chapterQuantityTargetHasFuturePrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	lastFuture := -1
	for _, marker := range []string{"下一章", "下章", "明天", "次日", "以后", "后续", "未来", "下一批"} {
		if index := strings.LastIndex(prefix, marker); index > lastFuture {
			lastFuture = index
		}
	}
	if lastFuture < 0 {
		return false
	}
	// An explicit current window after a future-context mention governs the
	// target: "为下一章蓄客，本章必须扩到十家" is still a chapter-ten
	// contract. Conversely, "本章五家、下一章扩到十家" remains future.
	lastCurrent := -1
	for _, marker := range []string{"本章", "当章", "当天", "当晚", "当前"} {
		if index := strings.LastIndex(prefix, marker); index > lastCurrent {
			lastCurrent = index
		}
	}
	return lastCurrent <= lastFuture
}

func chapterQuantityTargetHasAlternativePrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	// Only a marker governing this predicate is non-binding. Merely mentioning
	// a rejected backup earlier in the sentence must not erase a later hard
	// target (for example, "排除五家备选方案，本章必须扩到十家").
	return chapterQuantityTargetAlternativeContextPrefixPattern.MatchString(prefix) ||
		chapterQuantityTargetAlternativePrefixPattern.MatchString(prefix)
}

func chapterQuantityTargetHasAlternativeQuantitySuffix(suffix, entity string) bool {
	match := chapterQuantityTargetAlternativeQuantitySuffixPattern.FindStringSubmatch(suffix)
	return len(match) == 3 && normalizeChapterQuantityEntity(match[2]) == entity
}

func chapterQuantityCapClaims(items []chapterQuantityText) []chapterQuantityClaim {
	claims := chapterQuantityClaims(items, chapterQuantityCapPatterns)
	// A decision may explicitly describe a transition: "先维持五摊上限，
	// 随后扩到十家". A later, higher target in the same decision makes the
	// earlier cap an opening state rather than the terminal result. The reverse
	// order ("先扩到十家，最后维持五家上限") remains a hard conflict.
	laterTargets := chapterQuantityTargetClaims(items)
	filtered := claims[:0]
	for _, claim := range claims {
		if chapterQuantityCapIsNonBinding(claim, items) {
			continue
		}
		transitional := false
		for _, target := range laterTargets {
			if target.Source == claim.Source && target.Entity == claim.Entity &&
				target.Count > claim.Count && target.Offset > claim.Offset &&
				chapterQuantityLaterTargetIsCurrentTransition(claim, target, items) {
				transitional = true
				break
			}
		}
		if !transitional {
			filtered = append(filtered, claim)
		}
	}
	return filtered
}

func chapterQuantityLaterTargetIsCurrentTransition(cap, target chapterQuantityClaim, items []chapterQuantityText) bool {
	for _, item := range items {
		if item.Source != cap.Source || target.Source != cap.Source {
			continue
		}
		start := cap.Offset + len(cap.Text)
		if start < 0 || start > target.Offset || target.Offset > len(item.Text) {
			return false
		}
		bridge := item.Text[start:target.Offset]
		for _, marker := range []string{"下一章", "下章", "明天", "次日", "以后", "后续", "未来", "下一批", "待", "准备", "计划"} {
			if strings.Contains(bridge, marker) {
				return false
			}
		}
		for _, marker := range []string{"随后", "然后", "再", "当晚", "本章", "当天", "继而", "进而", "最终", "接着", "立即", "马上", "而是"} {
			if strings.Contains(bridge, marker) {
				return true
			}
		}
		return false
	}
	return false
}

func chapterQuantityCapIsNonBinding(claim chapterQuantityClaim, items []chapterQuantityText) bool {
	for _, item := range items {
		if item.Source != claim.Source || claim.Offset < 0 || claim.Offset > len(item.Text) {
			continue
		}
		// Prefix negation is clause-local and must terminate immediately before
		// the cap predicate. This keeps "禁止临时加摊，最终维持五摊上限"
		// active while accepting "不考虑维持五摊上限" as a rejection.
		before := []rune(chapterQuantityClausePrefix(item.Text[:claim.Offset]))
		if len(before) > 14 {
			before = before[len(before)-14:]
		}
		afterStart := claim.Offset + len(claim.Text)
		if afterStart > len(item.Text) {
			afterStart = len(item.Text)
		}
		prefix, suffix := string(before), item.Text[afterStart:]
		// Negation must govern the cap predicate itself. A broad window check
		// would misread "拒绝临时加摊，最终维持五摊上限" as rejecting
		// the cap, even though the cap is the terminal decision.
		compactPrefix := compactChapterQuantityPrefix(prefix)
		if strings.HasSuffix(compactPrefix, "不") &&
			!chapterQuantityPrefixHasDoubleNegation(compactPrefix) {
			return true
		}
		if chapterQuantityCapNegationPrefixPattern.MatchString(prefix) {
			return true
		}
		if chapterQuantityCapTentativePrefixPattern.MatchString(prefix) {
			return true
		}
		// Passive "被打破" is only a completed cancellation with 已/已经.
		// Risk or future language such as "会被打破" keeps the cap active.
		if chapterQuantityCapCompletedSuffixPattern.MatchString(suffix) ||
			chapterQuantityCapAlternativeExpansionSuffixPattern.MatchString(suffix) {
			return true
		}
	}
	return false
}

func compactChapterQuantityPrefix(prefix string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, prefix)
}

func chapterQuantityPrefixHasDoubleNegation(compactPrefix string) bool {
	for _, suffix := range []string{
		"不得不", "不能不", "不会不", "不可能不", "不应不", "不该不", "不可不",
		"不是不", "并非不", "无法不", "未必不", "不一定不",
	} {
		if strings.HasSuffix(compactPrefix, suffix) {
			return true
		}
	}
	return false
}

func chapterQuantityClaims(items []chapterQuantityText, patterns []*regexp.Regexp) []chapterQuantityClaim {
	var out []chapterQuantityClaim
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, pattern := range patterns {
			for _, match := range pattern.FindAllStringSubmatchIndex(item.Text, -1) {
				if len(match) < 6 || match[2] < 0 || match[3] < 0 || match[4] < 0 || match[5] < 0 {
					continue
				}
				count, ok := parseChapterQuantityNumber(item.Text[match[2]:match[3]])
				entity := normalizeChapterQuantityEntity(item.Text[match[4]:match[5]])
				if !ok || count <= 0 || entity == "" {
					continue
				}
				key := fmt.Sprintf("%s\x00%d\x00%s\x00%d", entity, count, item.Source, match[0])
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, chapterQuantityClaim{
					Entity: entity, Count: count, Source: item.Source,
					Text: item.Text[match[0]:match[1]], Offset: match[0],
				})
			}
		}
	}
	return out
}

func chapterQuantityConflicts(targets, caps []chapterQuantityClaim) []chapterQuantityConflict {
	targetByEntity := map[string]chapterQuantityClaim{}
	for _, claim := range targets {
		current, ok := targetByEntity[claim.Entity]
		if !ok || claim.Count > current.Count || (claim.Count == current.Count && claim.Source < current.Source) {
			targetByEntity[claim.Entity] = claim
		}
	}
	capByEntity := map[string]chapterQuantityClaim{}
	for _, claim := range caps {
		current, ok := capByEntity[claim.Entity]
		if !ok || claim.Count < current.Count || (claim.Count == current.Count && claim.Source < current.Source) {
			capByEntity[claim.Entity] = claim
		}
	}
	var entities []string
	for entity := range targetByEntity {
		entities = append(entities, entity)
	}
	sort.Strings(entities)
	var out []chapterQuantityConflict
	for _, entity := range entities {
		target := targetByEntity[entity]
		cap, ok := capByEntity[entity]
		if !ok || cap.Count >= target.Count {
			continue
		}
		out = append(out, chapterQuantityConflict{Entity: target, Target: target, Cap: cap})
	}
	return out
}

func formatChapterQuantityConflicts(chapter int, conflicts []chapterQuantityConflict) []string {
	labels := map[string]string{"stall": "摊位/商户", "transaction": "交易/订单"}
	out := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		label := labels[conflict.Entity.Entity]
		if label == "" {
			label = conflict.Entity.Entity
		}
		out = append(out, fmt.Sprintf(
			"chapter quantity result conflict: 第 %d 章%s目标至少 %d（%s: %s），但当前决策硬上限为 %d（%s: %s）",
			chapter, label, conflict.Target.Count, conflict.Target.Source, conflict.Target.Text,
			conflict.Cap.Count, conflict.Cap.Source, conflict.Cap.Text,
		))
	}
	return out
}

func normalizeChapterQuantityEntity(unit string) string {
	unit = strings.TrimSpace(unit)
	switch {
	case strings.Contains(unit, "摊"), strings.Contains(unit, "商户"), unit == "家":
		return "stall"
	case strings.Contains(unit, "笔"), strings.Contains(unit, "单"), strings.Contains(unit, "份"):
		return "transaction"
	default:
		return ""
	}
}

func parseChapterQuantityNumber(raw string) (int, bool) {
	raw = strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return '0' + (r - '０')
		}
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	if n, err := strconv.Atoi(raw); err == nil {
		return n, n > 0
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, section, digit := 0, 0, 0
	seen := false
	for _, r := range raw {
		if value, ok := digits[r]; ok {
			digit = value
			seen = true
			continue
		}
		if unit, ok := units[r]; ok {
			if digit == 0 {
				digit = 1
			}
			section += digit * unit
			digit = 0
			seen = true
			continue
		}
		if r == '万' {
			section += digit
			if section == 0 {
				section = 1
			}
			total += section * 10000
			section, digit = 0, 0
			seen = true
			continue
		}
		return 0, false
	}
	n := total + section + digit
	return n, seen && n > 0
}
