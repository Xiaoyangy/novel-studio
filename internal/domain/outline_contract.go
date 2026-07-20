package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// BookScaleRange is the mechanically enforceable part of compass.estimated_scale.
// The free-form word-count and story-time hints remain prompt context; volumes and
// chapters are the stable structural bounds used by outline-all.
type BookScaleRange struct {
	MinVolumes  int `json:"min_volumes"`
	MaxVolumes  int `json:"max_volumes"`
	MinChapters int `json:"min_chapters"`
	MaxChapters int `json:"max_chapters"`
}

type BookScaleTarget struct {
	Range                 BookScaleRange `json:"range"`
	TargetVolumes         int            `json:"target_volumes"`
	TargetChapters        int            `json:"target_chapters"`
	MinWords              int            `json:"min_words,omitempty"`
	MaxWords              int            `json:"max_words,omitempty"`
	TargetWords           int            `json:"target_words,omitempty"`
	TargetWordsPerChapter int            `json:"target_words_per_chapter,omitempty"`
	StoryTimeHint         string         `json:"story_time_hint,omitempty"`
}

const (
	// OutlineAllMinArcChapters and OutlineAllMaxArcChapters keep every
	// reservation small enough for one reliable expand_arc response while
	// retaining enough space for a real causal movement.
	OutlineAllMinArcChapters = 8
	OutlineAllMaxArcChapters = 16
)

func FormatOutlineAllArcSpans(spans []int) string {
	parts := make([]string, len(spans))
	for i, span := range spans {
		parts[i] = strconv.Itoa(span)
	}
	return strings.Join(parts, ",")
}

// RecommendedOutlineAllArcSpans deterministically balances a volume across
// the smallest number of arcs that can each be expanded in one bounded model
// response. Earlier arcs receive the remainder one chapter at a time.
func RecommendedOutlineAllArcSpans(total int) ([]int, error) {
	if total < OutlineAllMinArcChapters {
		return nil, fmt.Errorf("outline-all volume span %d is below the minimum expandable arc span %d", total, OutlineAllMinArcChapters)
	}
	arcCount := (total + OutlineAllMaxArcChapters - 1) / OutlineAllMaxArcChapters
	base := total / arcCount
	remainder := total % arcCount
	if base < OutlineAllMinArcChapters || base > OutlineAllMaxArcChapters {
		return nil, fmt.Errorf("outline-all volume span %d cannot be partitioned into %d-%d chapter arcs", total, OutlineAllMinArcChapters, OutlineAllMaxArcChapters)
	}
	spans := make([]int, arcCount)
	for i := range spans {
		spans[i] = base
		if i < remainder {
			spans[i]++
		}
		if spans[i] < OutlineAllMinArcChapters || spans[i] > OutlineAllMaxArcChapters {
			return nil, fmt.Errorf("outline-all deterministic arc span %d is outside %d-%d", spans[i], OutlineAllMinArcChapters, OutlineAllMaxArcChapters)
		}
	}
	return spans, nil
}

// OutlineAllArcSpanIssues validates both reservation and expanded arcs. It is
// intentionally separate from the generic outline quality contract so legacy
// short fiction can still use shorter arcs outside outline-all.
func OutlineAllArcSpanIssues(volumes []VolumeOutline) []OutlineContractIssue {
	var issues []OutlineContractIssue
	for _, volume := range volumes {
		if volume.Index <= 0 {
			continue
		}
		for _, arc := range volume.Arcs {
			span := arc.ChapterSpan()
			if span < OutlineAllMinArcChapters || span > OutlineAllMaxArcChapters {
				issues = append(issues, OutlineContractIssue{
					Code: "arc_span_out_of_bounds", Volume: volume.Index, Arc: arc.Index,
					Message: fmt.Sprintf("arc span=%d; outline-all requires %d-%d chapters", span, OutlineAllMinArcChapters, OutlineAllMaxArcChapters),
				})
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Volume != issues[j].Volume {
			return issues[i].Volume < issues[j].Volume
		}
		return issues[i].Arc < issues[j].Arc
	})
	return issues
}

var (
	volumeScaleRangeRE  = regexp.MustCompile(`(?i)(\d+)\s*[-–—~～至到]\s*(\d+)\s*(?:卷|volumes?)`)
	chapterScaleRangeRE = regexp.MustCompile(`(?i)(\d+)\s*[-–—~～至到]\s*(\d+)\s*(?:章|chapters?)`)
	wordScaleRangeRE    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(万)?\s*[-–—~～至到]\s*(\d+(?:\.\d+)?)\s*(万)?\s*(?:字|words?)`)
	storyTimeRangeRE    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*[-–—~～至到]\s*(\d+(?:\.\d+)?)\s*(?:年|years?)`)
)

// ParseBookScaleRange extracts volume and chapter bounds without tying the
// pipeline to a title, genre, or a particular Chinese wording around them.
func ParseBookScaleRange(value string) (BookScaleRange, error) {
	parse := func(re *regexp.Regexp, label string) (int, int, error) {
		m := re.FindStringSubmatch(value)
		if len(m) != 3 {
			return 0, 0, fmt.Errorf("estimated_scale missing %s range", label)
		}
		min, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, 0, err
		}
		max, err := strconv.Atoi(m[2])
		if err != nil {
			return 0, 0, err
		}
		if min <= 0 || max < min {
			return 0, 0, fmt.Errorf("estimated_scale has invalid %s range %d-%d", label, min, max)
		}
		return min, max, nil
	}
	minVolumes, maxVolumes, err := parse(volumeScaleRangeRE, "volume")
	if err != nil {
		return BookScaleRange{}, err
	}
	minChapters, maxChapters, err := parse(chapterScaleRangeRE, "chapter")
	if err != nil {
		return BookScaleRange{}, err
	}
	return BookScaleRange{
		MinVolumes: minVolumes, MaxVolumes: maxVolumes,
		MinChapters: minChapters, MaxChapters: maxChapters,
	}, nil
}

func (r BookScaleRange) Validate() error {
	if r.MinVolumes <= 0 || r.MaxVolumes < r.MinVolumes ||
		r.MinChapters <= 0 || r.MaxChapters < r.MinChapters {
		return fmt.Errorf("invalid book scale range: %+v", r)
	}
	return nil
}

// ResolveBookScaleTarget freezes a deterministic midpoint target. Existing
// reservations may raise the target within the declared maximum, but the model
// never chooses or changes it. When estimated_scale also declares a word range,
// the midpoint must imply a plausible long-form per-chapter budget.
func ResolveBookScaleTarget(value string, currentVolumes, currentChapters int) (BookScaleTarget, error) {
	rangeValue, err := ParseBookScaleRange(value)
	if err != nil {
		return BookScaleTarget{}, err
	}
	target := BookScaleTarget{
		Range:          rangeValue,
		TargetVolumes:  (rangeValue.MinVolumes + rangeValue.MaxVolumes + 1) / 2,
		TargetChapters: (rangeValue.MinChapters + rangeValue.MaxChapters + 1) / 2,
		StoryTimeHint:  strings.TrimSpace(storyTimeRangeRE.FindString(value)),
	}
	if currentVolumes > rangeValue.MaxVolumes || currentChapters > rangeValue.MaxChapters {
		return BookScaleTarget{}, fmt.Errorf(
			"existing outline exceeds estimated_scale maximum: volumes=%d/%d chapters=%d/%d",
			currentVolumes, rangeValue.MaxVolumes, currentChapters, rangeValue.MaxChapters,
		)
	}
	if currentVolumes > target.TargetVolumes {
		target.TargetVolumes = currentVolumes
	}
	if currentChapters > target.TargetChapters {
		target.TargetChapters = currentChapters
	}
	if target.TargetChapters < target.TargetVolumes*OutlineAllMinArcChapters {
		return BookScaleTarget{}, fmt.Errorf(
			"estimated scale target %d chapters cannot allocate at least one %d-chapter arc to %d volumes",
			target.TargetChapters, OutlineAllMinArcChapters, target.TargetVolumes,
		)
	}
	if m := wordScaleRangeRE.FindStringSubmatch(value); len(m) == 5 {
		minValue, _ := strconv.ParseFloat(m[1], 64)
		maxValue, _ := strconv.ParseFloat(m[3], 64)
		minFactor, maxFactor := 1.0, 1.0
		if m[2] != "" {
			minFactor = 10000
		}
		if m[4] != "" {
			maxFactor = 10000
			// Chinese commonly elides the first unit in ranges such as
			// “2.8—3万字”. Preserve that accepted spelling while also
			// supporting the fully explicit “2.8万—3万字”.
			if m[2] == "" && minValue < 1000 {
				minFactor = maxFactor
			}
		}
		target.MinWords = int(minValue*minFactor + 0.5)
		target.MaxWords = int(maxValue*maxFactor + 0.5)
		if target.MinWords <= 0 || target.MaxWords < target.MinWords {
			return BookScaleTarget{}, fmt.Errorf("estimated_scale has invalid word range")
		}
		target.TargetWords = (target.MinWords + target.MaxWords + 1) / 2
		target.TargetWordsPerChapter = (target.TargetWords + target.TargetChapters/2) / target.TargetChapters
		if target.TargetWordsPerChapter < 1500 || target.TargetWordsPerChapter > 5000 {
			return BookScaleTarget{}, fmt.Errorf(
				"estimated word range is incompatible with target chapters: midpoint implies %d words/chapter",
				target.TargetWordsPerChapter,
			)
		}
	}
	return target, nil
}

// RealVolumeCount ignores only legacy compatibility shells. A positive-index
// volume with no arcs remains visible to validation instead of inflating scale.
func RealVolumeCount(volumes []VolumeOutline) int {
	n := 0
	for _, volume := range volumes {
		if volume.Index > 0 && len(volume.Arcs) > 0 {
			n++
		}
	}
	return n
}

type OutlineContractIssue struct {
	Code    string `json:"code"`
	Volume  int    `json:"volume"`
	Arc     int    `json:"arc"`
	Chapter int    `json:"chapter,omitempty"`
	Message string `json:"message"`
}

var outlinePlaceholderFragments = []string{
	"tbd", "todo", "placeholder", "\u5f85\u7ec6\u5316", "\u5f85\u5c55\u5f00", "\u5f85\u8865\u5145", "\u5360\u4f4d",
	"\u627f\u63a5\u4e0a\u7ae0", "\u7ee7\u7eed\u63a8\u8fdb", "\u540e\u7eed\u63a8\u8fdb", "\u6839\u636e\u5267\u60c5", "\u5236\u9020\u60ac\u5ff5",
	"\u672c\u7ae0\u56f4\u7ed5", "\u8fdb\u4e00\u6b65\u63a8\u8fdb", "\u4e3a\u540e\u7eed\u57cb\u4e0b\u4f0f\u7b14",
}

var outlinePlaceholderShellFragments = []string{
	"\u6b64\u5904\u5360\u4f4d", "\u4e34\u65f6\u5360\u4f4d", "\u6682\u65f6\u5360\u4f4d", "\u5148\u884c\u5360\u4f4d",
	"\u4ec5\u4f5c\u5360\u4f4d", "\u53ea\u4f5c\u5360\u4f4d", "\u4ec5\u7528\u4e8e\u5360\u4f4d", "\u53ea\u7528\u4e8e\u5360\u4f4d", "\u7528\u6765\u5360\u4f4d", "\u7528\u4e8e\u5360\u4f4d", "\u4f5c\u4e3a\u5360\u4f4d",
	"\u5360\u4f4d\u5185\u5bb9", "\u5360\u4f4d\u6587\u672c", "\u5360\u4f4d\u5b57\u6bb5", "\u5360\u4f4d\u7ae0\u8282", "\u5360\u4f4d\u573a\u666f", "\u5360\u4f4d\u63cf\u8ff0", "\u5360\u4f4d\u5f85",
}

const outlineCoreEventMinMeaningfulRunes = 18
const outlineAmbiguousPlaceholderMaxMeaningfulRunes = 12

// OutlineChapterContractIssues distinguishes a merely expanded arc from one
// that is concrete enough to feed all-book simulation and later rendering.
func OutlineChapterContractIssues(volumes []VolumeOutline) []OutlineContractIssue {
	var issues []OutlineContractIssue
	titleOwner := make(map[string]string)
	coreOwner := make(map[string]int)
	hookOwner := make(map[string]int)
	globalChapter := 1
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			start := globalChapter
			span := arc.ChapterSpan()
			globalChapter += span
			if !arc.IsExpanded() {
				issues = append(issues, OutlineContractIssue{
					Code: "arc_unexpanded", Volume: volume.Index, Arc: arc.Index,
					Message: "arc still has only a chapter reservation",
				})
				continue
			}
			for i, chapter := range arc.Chapters {
				chapterNo := start + i
				location := fmt.Sprintf("V%dA%dC%d", volume.Index, arc.Index, chapterNo)
				titleKey := normalizeContractText(chapter.Title)
				switch {
				case meaningfulRuneCount(chapter.Title) < 2:
					issues = append(issues, contractIssue("title_too_thin", volume.Index, arc.Index, chapterNo, "chapter title is empty or too thin"))
				case containsOutlinePlaceholder(chapter.Title):
					issues = append(issues, contractIssue("title_placeholder", volume.Index, arc.Index, chapterNo, "chapter title contains placeholder language"))
				case titleOwner[titleKey] != "":
					issues = append(issues, contractIssue("duplicate_title", volume.Index, arc.Index, chapterNo, "chapter title duplicates "+titleOwner[titleKey]))
				default:
					titleOwner[titleKey] = location
				}

				coreKey := normalizeContractText(chapter.CoreEvent)
				if meaningfulRuneCount(chapter.CoreEvent) < outlineCoreEventMinMeaningfulRunes || containsOutlinePlaceholder(chapter.CoreEvent) {
					issues = append(issues, contractIssue(
						"core_event_not_concrete",
						volume.Index,
						arc.Index,
						chapterNo,
						outlineCoreEventRepairFeedback(chapter.CoreEvent),
					))
				}
				if previous := coreOwner[coreKey]; coreKey != "" && previous > 0 {
					issues = append(issues, contractIssue("duplicate_core_event", volume.Index, arc.Index, chapterNo, fmt.Sprintf("core_event duplicates earlier chapter %d", previous)))
				} else if coreKey != "" {
					coreOwner[coreKey] = chapterNo
				}

				hookKey := normalizeContractText(chapter.Hook)
				if meaningfulRuneCount(chapter.Hook) < 10 || containsOutlinePlaceholder(chapter.Hook) {
					issues = append(issues, contractIssue("hook_not_actionable", volume.Index, arc.Index, chapterNo, "hook needs a specific unresolved consequence or next action"))
				}
				if previous := hookOwner[hookKey]; hookKey != "" && previous > 0 {
					issues = append(issues, contractIssue("duplicate_hook", volume.Index, arc.Index, chapterNo, fmt.Sprintf("hook duplicates earlier chapter %d", previous)))
				} else if hookKey != "" {
					hookOwner[hookKey] = chapterNo
				}

				readableScenes := 0
				seenScenes := make(map[string]struct{}, len(chapter.Scenes))
				for _, scene := range chapter.Scenes {
					if sceneLooksLikeJSONShell(scene) {
						issues = append(issues, contractIssue("scene_json_shell", volume.Index, arc.Index, chapterNo, "scene is serialized JSON instead of readable scene text"))
						continue
					}
					if meaningfulRuneCount(scene) >= 10 && !containsOutlinePlaceholder(scene) {
						key := normalizeContractText(scene)
						if _, duplicate := seenScenes[key]; duplicate {
							issues = append(issues, contractIssue("duplicate_scene", volume.Index, arc.Index, chapterNo, "chapter repeats the same scene contract"))
							continue
						}
						seenScenes[key] = struct{}{}
						readableScenes++
					}
				}
				if readableScenes < 3 {
					issues = append(issues, contractIssue("scenes_too_thin", volume.Index, arc.Index, chapterNo, fmt.Sprintf("chapter has %d readable scenes; need at least 3", readableScenes)))
				}
			}
		}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Volume != issues[j].Volume {
			return issues[i].Volume < issues[j].Volume
		}
		if issues[i].Arc != issues[j].Arc {
			return issues[i].Arc < issues[j].Arc
		}
		if issues[i].Chapter != issues[j].Chapter {
			return issues[i].Chapter < issues[j].Chapter
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

func contractIssue(code string, volume, arc, chapter int, message string) OutlineContractIssue {
	return OutlineContractIssue{Code: code, Volume: volume, Arc: arc, Chapter: chapter, Message: message}
}

func ArcChapterContractReady(volumes []VolumeOutline, volume, arc int) bool {
	for _, issue := range OutlineChapterContractIssues(volumes) {
		if issue.Volume == volume && issue.Arc == arc {
			return false
		}
	}
	return true
}

func containsOutlinePlaceholder(value string) bool {
	return outlinePlaceholderFragment(value) != ""
}

func outlinePlaceholderFragment(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, fragment := range outlinePlaceholderFragments {
		if !strings.Contains(lower, fragment) {
			continue
		}
		// “占位” is also ordinary domain language: a merchant can split orders
		// to occupy a warehouse slot, reserve a seat, or hold inventory space.
		// Treating every substring occurrence as a meta placeholder rejects long,
		// concrete events. It remains fail-closed for short shells and explicit
		// wrapper phrases that describe placeholder content rather than a story
		// action. The other fragments are unambiguously meta-writing language.
		if fragment == "\u5360\u4f4d" && !outlineAmbiguousPlaceholderIsShell(lower, value) {
			continue
		}
		return fragment
	}
	return ""
}

func outlineAmbiguousPlaceholderIsShell(lower, original string) bool {
	if meaningfulRuneCount(original) <= outlineAmbiguousPlaceholderMaxMeaningfulRunes {
		return true
	}
	for _, wrapper := range outlinePlaceholderShellFragments {
		if strings.Contains(lower, wrapper) {
			return true
		}
	}
	return false
}

func outlineCoreEventRepairFeedback(value string) string {
	meaningfulRunes := meaningfulRuneCount(value)
	placeholder := outlinePlaceholderFragment(value)
	placeholderSignal := "none"
	if placeholder != "" {
		placeholderSignal = strconv.Quote(placeholder)
	}
	return fmt.Sprintf(
		"field=core_event parser_signals={meaningful_runes:%d, minimum:%d, punctuation_and_spaces_count:false, placeholder_fragment:%s}; "+
			"repair: replace only this chapter's core_event with one complete sentence that has at least %d meaningful letters/digits, contains a concrete actor + specific obstacle + chosen visible action + observable state change, and removes the reported placeholder fragment; "+
			"fill every bracket with actual story facts and do not submit the bracket labels: [actor] because [specific obstacle] cannot [immediate goal], chooses [visible action], changing [ledger/relationship/resource/timeline] from [before] to [after]; "+
			"passing example: 林澈因供货商临时涨价无法按期开市，改用备用名单重排摊位，使开市时间恢复并留下新核销记录",
		meaningfulRunes,
		outlineCoreEventMinMeaningfulRunes,
		placeholderSignal,
		outlineCoreEventMinMeaningfulRunes,
	)
}

func meaningfulRuneCount(value string) int {
	n := 0
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			n++
		}
	}
	return n
}

func normalizeContractText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var outlineContractClauseSplitRE = regexp.MustCompile(`[，,。；;！？!?\r\n]+`)

var outlineContractHypotheticalMarkers = []string{
	"计划以后", "计划日后", "计划未来", "计划下一", "打算以后", "打算日后", "打算未来", "打算下一",
	"准备以后", "准备日后", "准备未来", "准备下一", "未来再", "日后再", "以后再", "等以后", "等日后",
	"如果", "假如", "若是", "若要", "若能", "仅是假设", "只是假设", "只是设想", "仅是设想",
	"方案写着", "草案写着", "回执写着", "纸面写着", "文件写着", "设想是", "考虑将", "考虑把",
	"声称将", "宣称将", "拟于", "尚待", "仍待", "有待",
}

var outlineContractNegatedOutcomeMarkers = []string{
	"并未", "未能", "更未", "不曾", "尚未", "还未", "还没", "无法", "没有", "否决", "落空", "取消", "放弃",
}

var outlineContractAlwaysNonRealizedMarkers = []string{
	"并未做到", "没有做到", "未能做到", "并未实现", "没有实现", "未能实现", "并未落实", "没有落实", "未能落实",
	"并未完成", "没有完成", "未能完成", "并未兑现", "没有兑现", "未能兑现", "最终落空", "后来取消", "当场否决",
	"只是纸面", "仅在纸面",
}

var outlineContractExactFrameMarkers = []string{
	"并非", "不是", "并未", "未曾", "没有", "未能", "尚未", "还未", "还没", "无法",
	"原文如下", "会议纪要", "方案写着", "草案写着", "回执写着", "纸面写着", "文件写着", "公告栏展示", "展示一段文字",
	"有人提议", "曾提议", "会上提出", "提议", "建议", "计划", "打算", "准备", "考虑", "如果", "假如", "若是", "若要", "若能",
	"下个月", "下周", "明天", "未来", "以后", "日后", "届时", "将在", "将于", "拟于", "声称", "宣称",
	"待审", "待定", "待批", "待表决", "尚处讨论", "仍在讨论", "仅供讨论", "只供讨论", "没有采纳", "未被采纳", "并未采纳", "未采纳",
	"否决", "取消", "落空", "未发生", "没有发生", "未曾发生", "并未发生", "未实现", "未落实", "未兑现", "不作数", "作废",
	"仅是假设", "只是假设", "只是方案", "仅是方案", "只是一份方案", "只是草案", "仅是草案", "仅为记录", "只作记录", "只做记录",
}

var outlineContractNegativeIntentMarkers = []string{
	"不再", "不能", "不得", "不许", "不知", "不公开", "不泄露", "不抢", "不返场", "不依赖", "不牺牲", "不接受", "不允许",
	"未返", "未满", "从未", "尚未", "未公开", "未知", "未接触", "未泄露", "未暴露", "未牺牲", "未发生", "未完成", "未能",
	"无冷战", "无分离", "无人", "无须", "无需", "无法", "无权", "无因", "无损", "无条件",
	"拒绝", "保密", "守密", "秘密", "隐藏", "隐瞒", "禁止", "停止", "退出", "取消", "否决", "放弃",
}

// OutlineContractResolutionRealized is the single deterministic proof
// predicate shared by arc mutation validation, repair selection, and final
// outline validation. It accepts a concrete paraphrase only when evidence
// covers the actor/action/outcome breadth of planned_resolution. Clauses that
// explicitly describe a future plan, rejected quotation, or negated outcome
// do not contribute evidence, so lexical copying cannot prove realization.
func OutlineContractResolutionRealized(chapter OutlineEntry, ref StoryContractRef) bool {
	resolution := normalizeContractText(ref.PlannedResolution)
	if resolution == "" {
		return false
	}
	negativeIntent := containsAnyContractMarker(resolution, outlineContractNegativeIntentMarkers)
	evidenceBigrams := make(map[string]struct{})
	fields := append([]string{chapter.CoreEvent}, chapter.Scenes...)
	for _, field := range fields {
		normalizedField := normalizeContractText(field)
		if normalizedField == "" {
			continue
		}
		if strings.Contains(normalizedField, resolution) {
			if outlineContractExactResolutionIsAsserted(normalizedField, resolution) {
				return true
			}
			// A framed exact copy contributes no fallback n-grams. Otherwise
			// removing only its first clause would let the remaining copied
			// clauses satisfy the semantic coverage threshold.
			continue
		}
		for _, clause := range outlineContractClauseSplitRE.Split(field, -1) {
			normalizedClause := normalizeContractText(clause)
			if normalizedClause == "" || containsAnyContractMarker(normalizedClause, outlineContractHypotheticalMarkers) {
				continue
			}
			if containsAnyContractMarker(normalizedClause, outlineContractAlwaysNonRealizedMarkers) {
				continue
			}
			if !negativeIntent && containsAnyContractMarker(normalizedClause, outlineContractNegatedOutcomeMarkers) {
				continue
			}
			if strings.Contains(normalizedClause, resolution) {
				return true
			}
			for gram := range outlineContractBigrams(normalizedClause) {
				evidenceBigrams[gram] = struct{}{}
			}
		}
	}
	resolutionBigrams := outlineContractBigrams(resolution)
	if len(resolutionBigrams) == 0 || len(evidenceBigrams) == 0 {
		return false
	}
	hits := outlineContractBigramHits(resolutionBigrams, evidenceBigrams)
	minimumHits := 10
	if len(resolutionBigrams) < 24 {
		minimumHits = 6
	}
	if hits < minimumHits || hits*4 < len(resolutionBigrams) {
		return false
	}
	resolutionRunes := []rune(resolution)
	for segment := 0; segment < 3; segment++ {
		start := len(resolutionRunes) * segment / 3
		end := len(resolutionRunes) * (segment + 1) / 3
		segmentBigrams := outlineContractBigrams(string(resolutionRunes[start:end]))
		minimumSegmentHits := (len(segmentBigrams) + 4) / 5
		if minimumSegmentHits < 1 {
			minimumSegmentHits = 1
		}
		if minimumSegmentHits > 3 {
			minimumSegmentHits = 3
		}
		if outlineContractBigramHits(segmentBigrams, evidenceBigrams) < minimumSegmentHits {
			return false
		}
	}
	return true
}

func containsAnyContractMarker(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func outlineContractTextPrefix(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func outlineContractTextSuffix(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
}

func outlineContractExactResolutionIsAsserted(field, resolution string) bool {
	searchFrom := 0
	for searchFrom <= len(field)-len(resolution) {
		relative := strings.Index(field[searchFrom:], resolution)
		if relative < 0 {
			break
		}
		start := searchFrom + relative
		end := start + len(resolution)
		outside := outlineContractTextSuffix(field[:start], 32) + outlineContractTextPrefix(field[end:], 32)
		if !containsAnyContractMarker(outside, outlineContractExactFrameMarkers) {
			return true
		}
		searchFrom = end
	}
	return false
}

func outlineContractBigrams(value string) map[string]struct{} {
	runes := []rune(value)
	grams := make(map[string]struct{}, max(0, len(runes)-1))
	for i := 0; i+1 < len(runes); i++ {
		grams[string(runes[i:i+2])] = struct{}{}
	}
	return grams
}

func outlineContractBigramHits(want, evidence map[string]struct{}) int {
	hits := 0
	for gram := range want {
		if _, ok := evidence[gram]; ok {
			hits++
		}
	}
	return hits
}

// OutlineArcContractPayoffIssues validates the complete arc-local payoff
// receipt, including its immutable ref, exact global chapter, uniqueness, and
// concrete planned_resolution evidence. Callers must use the same globalStart
// convention as FlattenOutline (reserved arcs included in the cursor).
func OutlineArcContractPayoffIssues(arc ArcOutline, globalStart int) []string {
	want := make(map[string]StoryContractRef, len(arc.ContractRefs))
	parentCounts := make(map[string]int, len(arc.ContractRefs))
	for _, ref := range arc.ContractRefs {
		want[ref.ID] = ref
		parentCounts[ref.ID]++
	}
	seen := make(map[string]int, len(want))
	var issues []string
	for offset, chapter := range arc.Chapters {
		chapterNo := globalStart + offset
		for _, ref := range chapter.ContractRefs {
			expected, ok := want[ref.ID]
			switch {
			case !ok:
				issues = append(issues, fmt.Sprintf("unknown_contract_ref=%q@chapter-%d", ref.ID, chapterNo))
				continue
			case ref != expected:
				issues = append(issues, fmt.Sprintf("contract_ref_drift=%q@chapter-%d", ref.ID, chapterNo))
				continue
			case ref.PlannedPayoffChapter != chapterNo:
				issues = append(issues, fmt.Sprintf("misplaced_contract_ref=%q@chapter-%d want-%d", ref.ID, chapterNo, ref.PlannedPayoffChapter))
			}
			seen[ref.ID]++
			if !OutlineContractResolutionRealized(chapter, ref) {
				issues = append(issues, fmt.Sprintf(
					"planned_resolution_evidence_missing=%q@chapter-%d; concretely realize this actor, action, and terminal state in core_event/scenes (a negated, quoted-only, or future plan does not count): %q",
					ref.ID, chapterNo, ref.PlannedResolution,
				))
			}
		}
	}
	for id := range want {
		if parentCounts[id] != 1 {
			issues = append(issues, fmt.Sprintf("arc_contract_ref=%q count=%d want-1", id, parentCounts[id]))
		}
		if seen[id] != 1 {
			issues = append(issues, fmt.Sprintf("contract_ref=%q payoff_count=%d want-1", id, seen[id]))
		}
	}
	sort.Strings(issues)
	return issues
}

func sceneLooksLikeJSONShell(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if (strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]")) ||
		(strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")) {
		var decoded any
		return json.Unmarshal([]byte(value), &decoded) == nil
	}
	return false
}

const (
	StoryContractEnding        = "ending"
	StoryContractNonNegotiable = "non_negotiable"
	StoryContractOpenThread    = "open_thread"
)

// BuildStoryContractRegistry converts compass prose into stable, source-bound
// identifiers. The identifiers are copied into structural contract_refs; the
// prose fields remain free to describe the story naturally.
func BuildStoryContractRegistry(compass StoryCompass) []StoryContractRef {
	var refs []StoryContractRef
	appendRef := func(kind string, index int, source string) {
		source = strings.TrimSpace(source)
		if source == "" {
			return
		}
		sum := sha256.Sum256([]byte(source))
		digest := fmt.Sprintf("sha256:%x", sum[:])
		refs = append(refs, StoryContractRef{
			ID:           fmt.Sprintf("%s-%02d-%x", kind, index, sum[:6]),
			Kind:         kind,
			SourceDigest: digest,
		})
	}
	appendRef(StoryContractEnding, 0, compass.EndingDirection)
	for i, source := range compass.NonNegotiables {
		appendRef(StoryContractNonNegotiable, i, source)
	}
	for i, source := range compass.OpenThreads {
		appendRef(StoryContractOpenThread, i, source)
	}
	return refs
}

// StoryContractSkeletonIssues validates the arc-level payoff map while some
// arcs may still be reservations. final=true additionally requires every
// compass contract to be assigned exactly once.
func StoryContractSkeletonIssues(volumes []VolumeOutline, compass StoryCompass, final bool) []string {
	expected := make(map[string]StoryContractRef)
	for _, ref := range BuildStoryContractRegistry(compass) {
		expected[ref.ID] = ref
	}
	counts := make(map[string]int)
	resolutionOwner := make(map[string]string)
	finalVolume, finalArc, finalChapter := finalOutlinePosition(volumes)
	cursor := 1
	var issues []string
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			start, end := cursor, cursor+arc.ChapterSpan()-1
			for _, ref := range arc.ContractRefs {
				want, ok := expected[ref.ID]
				where := fmt.Sprintf("V%dA%d", volume.Index, arc.Index)
				if !ok {
					issues = append(issues, "unknown_contract_ref@"+where+":"+ref.ID)
					continue
				}
				counts[ref.ID]++
				if ref.Kind != want.Kind || ref.SourceDigest != want.SourceDigest ||
					ref.PlannedPayoffChapter < start || ref.PlannedPayoffChapter > end {
					issues = append(issues, "invalid_contract_ref@"+where+":"+ref.ID)
				}
				resolutionKey := normalizeContractText(ref.PlannedResolution)
				if meaningfulRuneCount(ref.PlannedResolution) < 18 || containsOutlinePlaceholder(ref.PlannedResolution) {
					issues = append(issues, "contract_resolution_not_concrete@"+where+":"+ref.ID)
				} else if owner := resolutionOwner[resolutionKey]; owner != "" && owner != ref.ID {
					issues = append(issues, "duplicate_contract_resolution@"+where+":"+ref.ID+":"+owner)
				} else {
					resolutionOwner[resolutionKey] = ref.ID
				}
				if (want.Kind == StoryContractEnding || want.Kind == StoryContractNonNegotiable) &&
					(volume.Index != finalVolume || arc.Index != finalArc || ref.PlannedPayoffChapter != finalChapter) {
					issues = append(issues, ref.ID+" must_bind_final_arc_and_chapter")
				}
			}
			cursor += arc.ChapterSpan()
		}
	}
	for id := range expected {
		if counts[id] > 1 {
			issues = append(issues, fmt.Sprintf("%s arc_payoff_count=%d", id, counts[id]))
		} else if final && counts[id] != 1 {
			issues = append(issues, fmt.Sprintf("%s arc_payoff_count=%d", id, counts[id]))
		}
	}
	sort.Strings(issues)
	return issues
}

type outlineContractPlacement struct {
	ref            StoryContractRef
	volume         int
	arc            int
	chapter        int
	arcStart       int
	arcEnd         int
	isFinalArc     bool
	isFinalChapter bool
	payoffChapter  OutlineEntry
}

// MissingCompassCoverage validates references, source digests and unique
// payoff positions. Ending/non-negotiable contracts must resolve in the final
// arc and final chapter; every open thread gets exactly one arc and one chapter
// payoff at the same planned chapter.
func MissingCompassCoverage(volumes []VolumeOutline, compass StoryCompass) []string {
	expected := make(map[string]StoryContractRef)
	for _, ref := range BuildStoryContractRegistry(compass) {
		expected[ref.ID] = ref
	}
	arcRefs := make(map[string][]outlineContractPlacement)
	chapterRefs := make(map[string][]outlineContractPlacement)
	finalVolume, finalArc, finalChapter := finalOutlinePosition(volumes)
	cursor := 1
	var invalid []string
	resolutionOwner := make(map[string]string)
	validateRef := func(ref StoryContractRef, where string) bool {
		want, ok := expected[ref.ID]
		if !ok {
			invalid = append(invalid, "unknown_contract_ref@"+where+":"+ref.ID)
			return false
		}
		resolutionKey := normalizeContractText(ref.PlannedResolution)
		if ref.Kind != want.Kind || ref.SourceDigest != want.SourceDigest || ref.PlannedPayoffChapter <= 0 ||
			meaningfulRuneCount(ref.PlannedResolution) < 18 || containsOutlinePlaceholder(ref.PlannedResolution) {
			invalid = append(invalid, "invalid_contract_ref@"+where+":"+ref.ID)
			return false
		}
		if owner := resolutionOwner[resolutionKey]; owner != "" && owner != ref.ID {
			invalid = append(invalid, "duplicate_contract_resolution@"+where+":"+ref.ID+":"+owner)
			return false
		}
		resolutionOwner[resolutionKey] = ref.ID
		return true
	}
	for _, volume := range volumes {
		for _, arc := range volume.Arcs {
			start := cursor
			end := start + arc.ChapterSpan() - 1
			for _, issue := range OutlineArcContractPayoffIssues(arc, start) {
				invalid = append(invalid, fmt.Sprintf("V%dA%d %s", volume.Index, arc.Index, issue))
			}
			for _, ref := range arc.ContractRefs {
				where := fmt.Sprintf("V%dA%d", volume.Index, arc.Index)
				if validateRef(ref, where) {
					arcRefs[ref.ID] = append(arcRefs[ref.ID], outlineContractPlacement{
						ref: ref, volume: volume.Index, arc: arc.Index,
						arcStart: start, arcEnd: end,
						isFinalArc: volume.Index == finalVolume && arc.Index == finalArc,
					})
				}
			}
			for i, chapter := range arc.Chapters {
				chapterNo := start + i
				for _, ref := range chapter.ContractRefs {
					where := fmt.Sprintf("chapter-%d", chapterNo)
					if validateRef(ref, where) {
						chapterRefs[ref.ID] = append(chapterRefs[ref.ID], outlineContractPlacement{
							ref: ref, volume: volume.Index, arc: arc.Index, chapter: chapterNo,
							arcStart: start, arcEnd: end,
							isFinalArc:     volume.Index == finalVolume && arc.Index == finalArc,
							isFinalChapter: chapterNo == finalChapter,
							payoffChapter:  chapter,
						})
					}
				}
			}
			cursor += arc.ChapterSpan()
		}
	}
	missing := append([]string(nil), invalid...)
	for id, want := range expected {
		arcs := arcRefs[id]
		chapters := chapterRefs[id]
		if len(arcs) != 1 {
			missing = append(missing, fmt.Sprintf("%s arc_payoff_count=%d", id, len(arcs)))
			continue
		}
		if len(chapters) != 1 {
			missing = append(missing, fmt.Sprintf("%s chapter_payoff_count=%d", id, len(chapters)))
			continue
		}
		a, c := arcs[0], chapters[0]
		if a.ref.PlannedPayoffChapter != c.ref.PlannedPayoffChapter ||
			c.chapter != c.ref.PlannedPayoffChapter ||
			c.chapter < a.arcStart || c.chapter > a.arcEnd ||
			a.volume != c.volume || a.arc != c.arc {
			missing = append(missing, id+" payoff_binding_mismatch")
		}
		if !OutlineContractResolutionRealized(c.payoffChapter, c.ref) {
			missing = append(missing, id+" planned_resolution_not_realized_in_core_event_or_scenes")
		}
		if (want.Kind == StoryContractEnding || want.Kind == StoryContractNonNegotiable) &&
			(!a.isFinalArc || !c.isFinalChapter) {
			missing = append(missing, id+" must_payoff_at_final_chapter")
		}
	}
	sort.Strings(missing)
	return missing
}

func finalOutlinePosition(volumes []VolumeOutline) (volume, arc, chapter int) {
	cursor := 1
	for _, item := range volumes {
		for _, candidate := range item.Arcs {
			span := candidate.ChapterSpan()
			if span > 0 {
				volume, arc, chapter = item.Index, candidate.Index, cursor+span-1
			}
			cursor += span
		}
	}
	return volume, arc, chapter
}
