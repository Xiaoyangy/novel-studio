package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	StoryTimeContractVersion = 1
	StoryDaysPerYear         = 365.2425

	StoryTimeSourceExplicit              = "explicit"
	StoryTimeSourceOutlineAll            = "outline_all"
	StoryTimeSourceCompassEstimatedScale = "compass_estimated_scale"
	StoryTimeSourceFallbackNominal       = "fallback_nominal"
)

// StoryTimeArcSchedule reserves a numeric story-day window for one arc. The
// schedule is optional: callers must leave it empty instead of inventing
// uniform pacing when the Architect has not supplied a defensible time budget.
type StoryTimeArcSchedule struct {
	Volume       int     `json:"volume"`
	Arc          int     `json:"arc"`
	StartChapter int     `json:"start_chapter"`
	EndChapter   int     `json:"end_chapter"`
	StartDay     float64 `json:"start_day"`
	EndDay       float64 `json:"end_day"`
}

// StoryTimeChapterSchedule is the most precise chapter-to-story-day mapping.
// EndDay is used for world-event coordinates because the historical fallback
// also maps chapter N to N*days_per_chapter (the end of that nominal window).
type StoryTimeChapterSchedule struct {
	Chapter  int     `json:"chapter"`
	StartDay float64 `json:"start_day"`
	EndDay   float64 `json:"end_day"`
}

// StoryTimeContract freezes the quantitative time promise that prose planning
// must honor. CoreDigest covers the immutable target/duration/nominal fields;
// ScheduleDigest separately seals the ordered optional arc/chapter schedules.
// Keeping both makes an intentional schedule refinement possible only through
// FinalizeStoryTimeContract, while direct edits to either half fail validation.
type StoryTimeContract struct {
	Version               int                        `json:"version"`
	Source                string                     `json:"source"`
	TargetChapters        int                        `json:"target_chapters"`
	DurationDaysMin       float64                    `json:"duration_days_min"`
	DurationDaysMax       float64                    `json:"duration_days_max"`
	NominalDaysPerChapter float64                    `json:"nominal_days_per_chapter"`
	SourceEstimatedScale  string                     `json:"source_estimated_scale,omitempty"`
	ArcSchedule           []StoryTimeArcSchedule     `json:"arc_schedule,omitempty"`
	ChapterSchedule       []StoryTimeChapterSchedule `json:"chapter_schedule,omitempty"`
	CoreDigest            string                     `json:"core_digest"`
	ScheduleDigest        string                     `json:"schedule_digest"`
}

type ParsedStoryScale struct {
	ChapterMin       int
	ChapterMax       int
	DurationYearsMin float64
	DurationYearsMax float64
}

var (
	storyChapterRangeRE    = regexp.MustCompile(`([0-9]+)\s*(?:-|—|–|~|～|至|到)\s*([0-9]+)\s*章`)
	storyChapterSingleRE   = regexp.MustCompile(`([0-9]+)\s*章`)
	storyYearRangeRE       = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*年?\s*(?:-|—|–|~|～|至|到)\s*([0-9]+(?:\.[0-9]+)?)\s*年`)
	storyYearSingleRE      = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*年`)
	storyArabicHalfYearRE  = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*年半`)
	storyChineseHalfYearRE = regexp.MustCompile(`([零〇一二三四五六七八九十两]+)\s*年半`)
	storyChineseYearRE     = regexp.MustCompile(`([零〇一二三四五六七八九十两]+)\s*年`)
)

// ParseStoryScale extracts only explicit chapter/year contracts. Word-count
// and volume ranges are deliberately ignored. It supports both "3.5-4年" and
// the common Chinese form "3年半到4年".
func ParseStoryScale(raw string) ParsedStoryScale {
	normalized := normalizeStoryYearExpressions(strings.TrimSpace(raw))
	var out ParsedStoryScale
	if match := storyChapterRangeRE.FindStringSubmatch(normalized); len(match) == 3 {
		out.ChapterMin, _ = strconv.Atoi(match[1])
		out.ChapterMax, _ = strconv.Atoi(match[2])
	} else if match := storyChapterSingleRE.FindStringSubmatch(normalized); len(match) == 2 {
		out.ChapterMin, _ = strconv.Atoi(match[1])
		out.ChapterMax = out.ChapterMin
	}
	if out.ChapterMin > out.ChapterMax {
		out.ChapterMin, out.ChapterMax = out.ChapterMax, out.ChapterMin
	}
	if match := storyYearRangeRE.FindStringSubmatch(normalized); len(match) == 3 {
		out.DurationYearsMin, _ = strconv.ParseFloat(match[1], 64)
		out.DurationYearsMax, _ = strconv.ParseFloat(match[2], 64)
	} else if match := storyYearSingleRE.FindStringSubmatch(normalized); len(match) == 2 {
		out.DurationYearsMin, _ = strconv.ParseFloat(match[1], 64)
		out.DurationYearsMax = out.DurationYearsMin
	}
	if out.DurationYearsMin > out.DurationYearsMax {
		out.DurationYearsMin, out.DurationYearsMax = out.DurationYearsMax, out.DurationYearsMin
	}
	return out
}

func normalizeStoryYearExpressions(raw string) string {
	raw = storyArabicHalfYearRE.ReplaceAllStringFunc(raw, func(value string) string {
		match := storyArabicHalfYearRE.FindStringSubmatch(value)
		if len(match) != 2 {
			return value
		}
		n, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return value
		}
		return strconv.FormatFloat(n+0.5, 'f', -1, 64) + "年"
	})
	raw = storyChineseHalfYearRE.ReplaceAllStringFunc(raw, func(value string) string {
		match := storyChineseHalfYearRE.FindStringSubmatch(value)
		if len(match) != 2 {
			return value
		}
		n, ok := parseSmallChineseNumber(match[1])
		if !ok {
			return value
		}
		return strconv.FormatFloat(float64(n)+0.5, 'f', -1, 64) + "年"
	})
	return storyChineseYearRE.ReplaceAllStringFunc(raw, func(value string) string {
		match := storyChineseYearRE.FindStringSubmatch(value)
		if len(match) != 2 {
			return value
		}
		n, ok := parseSmallChineseNumber(match[1])
		if !ok {
			return value
		}
		return strconv.Itoa(n) + "年"
	})
}

func parseSmallChineseNumber(raw string) (int, bool) {
	digit := func(r rune) (int, bool) {
		switch r {
		case '零', '〇':
			return 0, true
		case '一':
			return 1, true
		case '二', '两':
			return 2, true
		case '三':
			return 3, true
		case '四':
			return 4, true
		case '五':
			return 5, true
		case '六':
			return 6, true
		case '七':
			return 7, true
		case '八':
			return 8, true
		case '九':
			return 9, true
		default:
			return 0, false
		}
	}
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) == 0 {
		return 0, false
	}
	if len(runes) == 1 {
		if runes[0] == '十' {
			return 10, true
		}
		return digit(runes[0])
	}
	for i, r := range runes {
		if r != '十' {
			continue
		}
		tens := 1
		if i > 0 {
			var ok bool
			tens, ok = digit(runes[i-1])
			if !ok {
				return 0, false
			}
		}
		ones := 0
		if i+1 < len(runes) {
			var ok bool
			ones, ok = digit(runes[i+1])
			if !ok {
				return 0, false
			}
		}
		return tens*10 + ones, true
	}
	return 0, false
}

// DeriveStoryTimeContract migrates a natural-language compass contract into a
// deterministic numeric core. targetChapters should be the finalized
// outline-all total. If it is zero, an explicit chapter range midpoint is used.
func DeriveStoryTimeContract(estimatedScale string, targetChapters int) (StoryTimeContract, error) {
	parsed := ParseStoryScale(estimatedScale)
	if targetChapters <= 0 && parsed.ChapterMin > 0 {
		targetChapters = int(math.Round(float64(parsed.ChapterMin+parsed.ChapterMax) / 2))
	}
	if targetChapters <= 0 {
		return StoryTimeContract{}, fmt.Errorf("story time contract requires a positive target_chapters")
	}
	if parsed.ChapterMin > 0 && (targetChapters < parsed.ChapterMin || targetChapters > parsed.ChapterMax) {
		return StoryTimeContract{}, fmt.Errorf(
			"target_chapters=%d is outside compass chapter range %d-%d",
			targetChapters,
			parsed.ChapterMin,
			parsed.ChapterMax,
		)
	}
	contract := StoryTimeContract{
		Version:              StoryTimeContractVersion,
		TargetChapters:       targetChapters,
		SourceEstimatedScale: strings.TrimSpace(estimatedScale),
	}
	if parsed.DurationYearsMin > 0 && parsed.DurationYearsMax > 0 {
		contract.Source = StoryTimeSourceCompassEstimatedScale
		contract.DurationDaysMin = parsed.DurationYearsMin * StoryDaysPerYear
		contract.DurationDaysMax = parsed.DurationYearsMax * StoryDaysPerYear
		contract.NominalDaysPerChapter = ((contract.DurationDaysMin + contract.DurationDaysMax) / 2) / float64(targetChapters)
	} else {
		contract.Source = StoryTimeSourceFallbackNominal
		contract.NominalDaysPerChapter = 2
		contract.DurationDaysMin = float64(targetChapters) * contract.NominalDaysPerChapter
		contract.DurationDaysMax = contract.DurationDaysMin
	}
	return FinalizeStoryTimeContract(contract)
}

func FinalizeStoryTimeContract(contract StoryTimeContract) (StoryTimeContract, error) {
	if contract.Version == 0 {
		contract.Version = StoryTimeContractVersion
	}
	digest, err := ComputeStoryTimeContractCoreDigest(contract)
	if err != nil {
		return StoryTimeContract{}, err
	}
	contract.CoreDigest = digest
	scheduleDigest, err := ComputeStoryTimeContractScheduleDigest(contract)
	if err != nil {
		return StoryTimeContract{}, err
	}
	contract.ScheduleDigest = scheduleDigest
	if err := contract.Validate(); err != nil {
		return StoryTimeContract{}, err
	}
	return contract, nil
}

// ComputeStoryTimeContractScheduleDigest seals the ordered schedule as written.
// Order is semantically significant because validation and lookup both consume
// schedules in ascending chapter order; it is therefore not sorted here.
func ComputeStoryTimeContractScheduleDigest(contract StoryTimeContract) (string, error) {
	payload := struct {
		ArcSchedule     []StoryTimeArcSchedule     `json:"arc_schedule"`
		ChapterSchedule []StoryTimeChapterSchedule `json:"chapter_schedule"`
	}{
		ArcSchedule:     contract.ArcSchedule,
		ChapterSchedule: contract.ChapterSchedule,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ComputeStoryTimeContractCoreDigest(contract StoryTimeContract) (string, error) {
	payload := struct {
		Version               int     `json:"version"`
		Source                string  `json:"source"`
		TargetChapters        int     `json:"target_chapters"`
		DurationDaysMin       float64 `json:"duration_days_min"`
		DurationDaysMax       float64 `json:"duration_days_max"`
		NominalDaysPerChapter float64 `json:"nominal_days_per_chapter"`
		SourceEstimatedScale  string  `json:"source_estimated_scale,omitempty"`
	}{
		Version:               contract.Version,
		Source:                strings.TrimSpace(contract.Source),
		TargetChapters:        contract.TargetChapters,
		DurationDaysMin:       contract.DurationDaysMin,
		DurationDaysMax:       contract.DurationDaysMax,
		NominalDaysPerChapter: contract.NominalDaysPerChapter,
		SourceEstimatedScale:  strings.TrimSpace(contract.SourceEstimatedScale),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (contract StoryTimeContract) Validate() error {
	if contract.Version != StoryTimeContractVersion {
		return fmt.Errorf("story time contract version=%d, want %d", contract.Version, StoryTimeContractVersion)
	}
	if strings.TrimSpace(contract.Source) == "" || contract.TargetChapters <= 0 {
		return fmt.Errorf("story time contract requires source and positive target_chapters")
	}
	switch contract.Source {
	case StoryTimeSourceExplicit, StoryTimeSourceOutlineAll, StoryTimeSourceCompassEstimatedScale, StoryTimeSourceFallbackNominal:
	default:
		return fmt.Errorf("story time contract source=%q is unsupported", contract.Source)
	}
	if math.IsNaN(contract.DurationDaysMin) || math.IsInf(contract.DurationDaysMin, 0) ||
		math.IsNaN(contract.DurationDaysMax) || math.IsInf(contract.DurationDaysMax, 0) ||
		math.IsNaN(contract.NominalDaysPerChapter) || math.IsInf(contract.NominalDaysPerChapter, 0) ||
		contract.DurationDaysMin <= 0 || contract.DurationDaysMax < contract.DurationDaysMin || contract.NominalDaysPerChapter <= 0 {
		return fmt.Errorf("story time contract has invalid duration or nominal chapter time")
	}
	minNominal := contract.DurationDaysMin / float64(contract.TargetChapters)
	maxNominal := contract.DurationDaysMax / float64(contract.TargetChapters)
	if contract.NominalDaysPerChapter < minNominal-1e-9 || contract.NominalDaysPerChapter > maxNominal+1e-9 {
		return fmt.Errorf(
			"nominal_days_per_chapter %.6f is outside derived range %.6f-%.6f",
			contract.NominalDaysPerChapter,
			minNominal,
			maxNominal,
		)
	}
	want, err := ComputeStoryTimeContractCoreDigest(contract)
	if err != nil {
		return err
	}
	if contract.CoreDigest != want {
		return fmt.Errorf("story time contract core_digest mismatch")
	}
	if err := validateStoryTimeChapterSchedule(contract.TargetChapters, contract.DurationDaysMin, contract.DurationDaysMax, contract.ChapterSchedule); err != nil {
		return err
	}
	if err := validateStoryTimeArcSchedule(contract.TargetChapters, contract.DurationDaysMin, contract.DurationDaysMax, contract.ArcSchedule); err != nil {
		return err
	}
	wantSchedule, err := ComputeStoryTimeContractScheduleDigest(contract)
	if err != nil {
		return err
	}
	if contract.ScheduleDigest != wantSchedule {
		return fmt.Errorf("story time contract schedule_digest mismatch")
	}
	return nil
}

func validateStoryTimeChapterSchedule(target int, durationMin, durationMax float64, schedule []StoryTimeChapterSchedule) error {
	previousChapter := 0
	previousEnd := 0.0
	for i, item := range schedule {
		if item.Chapter <= previousChapter || item.Chapter > target || item.StartDay < 0 ||
			item.EndDay < item.StartDay || item.EndDay > durationMax+1e-9 {
			return fmt.Errorf("story time chapter schedule[%d] is invalid", i)
		}
		if item.Chapter == target && (item.EndDay < durationMin-1e-9 || item.EndDay > durationMax+1e-9) {
			return fmt.Errorf("story time chapter schedule target endpoint is outside contract duration")
		}
		if i > 0 && item.StartDay < previousEnd-1e-9 {
			return fmt.Errorf("story time chapter schedule is not monotonic at chapter %d", item.Chapter)
		}
		previousChapter = item.Chapter
		previousEnd = item.EndDay
	}
	return nil
}

func validateStoryTimeArcSchedule(target int, durationMin, durationMax float64, schedule []StoryTimeArcSchedule) error {
	previousEndChapter := 0
	previousEndDay := 0.0
	for i, item := range schedule {
		if item.Volume <= 0 || item.Arc <= 0 || item.StartChapter <= previousEndChapter ||
			item.EndChapter < item.StartChapter || item.EndChapter > target ||
			item.StartDay < 0 || item.EndDay < item.StartDay || item.EndDay > durationMax+1e-9 {
			return fmt.Errorf("story time arc schedule[%d] is invalid", i)
		}
		if item.EndChapter == target && (item.EndDay < durationMin-1e-9 || item.EndDay > durationMax+1e-9) {
			return fmt.Errorf("story time arc schedule target endpoint is outside contract duration")
		}
		if i > 0 && item.StartDay < previousEndDay-1e-9 {
			return fmt.Errorf("story time arc schedule is not monotonic at volume=%d arc=%d", item.Volume, item.Arc)
		}
		previousEndChapter = item.EndChapter
		previousEndDay = item.EndDay
	}
	return nil
}

// StoryDayForChapter resolves the most authoritative available numeric time.
// Exact chapter windows win, then a declared arc window is interpolated. When
// neither exists, the frozen nominal rate is the explicit fallback.
func (contract StoryTimeContract) StoryDayForChapter(chapter int) float64 {
	if chapter <= 0 {
		return 0
	}
	for _, item := range contract.ChapterSchedule {
		if item.Chapter == chapter {
			return item.EndDay
		}
	}
	for _, item := range contract.ArcSchedule {
		if chapter < item.StartChapter || chapter > item.EndChapter {
			continue
		}
		span := item.EndChapter - item.StartChapter + 1
		position := chapter - item.StartChapter + 1
		if span <= 0 {
			break
		}
		return item.StartDay + (item.EndDay-item.StartDay)*float64(position)/float64(span)
	}
	return float64(chapter) * contract.NominalDaysPerChapter
}
