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
	wordScaleRangeRE    = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*[-–—~～至到]\s*(\d+(?:\.\d+)?)\s*(万)?\s*(?:字|words?)`)
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
	if m := wordScaleRangeRE.FindStringSubmatch(value); len(m) == 4 {
		minValue, _ := strconv.ParseFloat(m[1], 64)
		maxValue, _ := strconv.ParseFloat(m[2], 64)
		factor := 1.0
		if m[3] != "" {
			factor = 10000
		}
		target.MinWords = int(minValue*factor + 0.5)
		target.MaxWords = int(maxValue*factor + 0.5)
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
	payoffEvidence string
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
							payoffEvidence: normalizeContractText(chapter.CoreEvent + "\n" + strings.Join(chapter.Scenes, "\n")),
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
		resolutionEvidence := normalizeContractText(c.ref.PlannedResolution)
		if resolutionEvidence == "" || !strings.Contains(c.payoffEvidence, resolutionEvidence) {
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
