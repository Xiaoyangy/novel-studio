package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type pipelineSealedStoryClockEvidence struct {
	BodySHA256     string  `json:"body_sha256"`
	Chapter        int     `json:"chapter"`
	StartDay       float64 `json:"start_day"`
	EndDay         float64 `json:"end_day"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	Kind           string  `json:"kind"`
	StartRune      int     `json:"start_rune"`
	EndRune        int     `json:"end_rune"`
	StartText      string  `json:"start_text"`
	EndText        string  `json:"end_text"`
}

type pipelineStoryClockAnchor struct {
	Kind, Source, Text string
	Seconds            float64
	StartRune, EndRune int
}

var pipelineStoryClockSentenceRE = regexp.MustCompile(`[^。！？!?\n]+[。！？!?\n]?`)
var pipelineStoryClockColonRE = regexp.MustCompile(`([0-9]{1,3})[:：]([0-9]{2})(?:[:：]([0-9]{2}(?:\.[0-9]+)?))?`)
var pipelineStoryClockChineseRE = regexp.MustCompile(`([0-9零〇一二两三四五六七八九十]+)[点时](?:([0-9零〇一二两三四五六七八九十]+)分?)?(?:([0-9零〇一二两三四五六七八九十]+)秒)?`)
var pipelineStoryClockNumberRE = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?|[零〇一二两三四五六七八九十百千]+)(天|小时|分钟|分|秒钟|秒)`)
var pipelineStoryClockSpanRE = regexp.MustCompile(`从([^，,。；;\n]{2,36}?)到([^，,。；;\n]{2,36})[，,]?(?:已经|一共|总共|整整|恰好|共|竟然|足足)*(?:过去了?|耗时|用了)([^。！？!?\n]+)`)
var pipelineStoryClockPlainSpanRE = regexp.MustCompile(`从([^，,。；;\n]{2,36}?)到([^，,。；;\n]{2,36}?)[，,]?(?:一共|总共|共)(?:用了|耗时)?([^。！？!?\n]+)`)
var pipelineStoryClockRemainingRE = regexp.MustCompile(`(?:距(?:离)?|离)([^，,。；;：:\n]{2,18}?)(?:还剩|只剩|仅剩|剩余)[：: ]*([^，,。；;！？!?\n]+)`)
var pipelineStoryClockDaysRE = regexp.MustCompile(`([0-9零〇一二两三四五六七八九十百]+)天(?:之后|以后|过去|后)`)
var pipelineStoryClockOrdinalDayRE = regexp.MustCompile(`第([0-9零〇一二两三四五六七八九十百]+)天`)

func pipelineSealedStoryClockMutation(category string, mutation domain.StateMutationV2) bool {
	return category == "timeline" && strings.TrimSpace(mutation.Subject) == "world" && strings.TrimSpace(mutation.Field) == "story_day"
}

func pipelineStoryClockBodySHA(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pipelineStoryClockNumberEqual(text string, want float64) bool {
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	return err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) && value == want
}

// Derive only elapsed time from the exact body. The already accepted/adjudicated
// start coordinate is the origin, never evidence that the chapter was rendered.
// One beginning/end pair spans the chapter; concurrent action durations are
// deliberately not accumulated.
func derivePipelineSealedStoryClockEvidence(bundle *domain.ProjectedChapterBundle, body string) (*pipelineSealedStoryClockEvidence, error) {
	if bundle == nil || bundle.ChapterWorldSimulation.StoryTime == nil {
		return nil, nil
	}
	storyTime := bundle.ChapterWorldSimulation.StoryTime
	if _, _, err := domain.CharacterSimulationStoryTimeSource(bundle.ChapterWorldSimulation, bundle.CharacterAgentEvidence, bundle.CharacterActivationEvidence); err != nil {
		return nil, err
	}
	if err := domain.ValidateStoryTimeForClock(bundle.Chapter, storyTime, nil); err != nil {
		return nil, err
	}
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("story_clock requires an exact nonempty UTF-8 chapter body")
	}
	wanted := (storyTime.EndDay - storyTime.StartDay) * 86400
	count := utf8.RuneCountInString(body)
	finish := func(kind string, start, end pipelineStoryClockAnchor, elapsed float64) (*pipelineSealedStoryClockEvidence, error) {
		if math.Abs(elapsed-wanted) > 1e-6 {
			return nil, fmt.Errorf("story_clock 正文首尾实际经过 %.12g 秒，裁决为 %.12g 秒；请核对整段时间，不能按并行动作相加", elapsed, wanted)
		}
		return &pipelineSealedStoryClockEvidence{
			BodySHA256: pipelineStoryClockBodySHA(body), Chapter: bundle.Chapter,
			StartDay: storyTime.StartDay, EndDay: storyTime.EndDay, ElapsedSeconds: elapsed, Kind: kind,
			StartRune: start.StartRune, EndRune: end.EndRune, StartText: start.Text, EndText: end.Text,
		}, nil
	}
	anchors := collectPipelineStoryClockAnchors(body)
	var pairError error
	var proved *pipelineSealedStoryClockEvidence
	groups := make(map[string][]pipelineStoryClockAnchor)
	var groupOrder []string
	for _, anchor := range anchors {
		key := anchor.Kind
		if anchor.Kind != "wall_clock" {
			if anchor.Source == "" {
				continue
			}
			key += ":" + anchor.Source
		}
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], anchor)
	}
	for _, key := range groupOrder {
		group := groups[key]
		if len(group) < 2 {
			continue
		}
		first, last := group[0], group[len(group)-1]
		kind := first.Kind
		if first.StartRune > count/3 || last.EndRune < count*2/3 || first.EndRune > last.StartRune {
			continue
		}
		elapsed := 0.0
		valid := true
		calendarDay := 1.0
		if marker := pipelineStoryClockOrdinalDayRE.FindStringSubmatch(first.Text); len(marker) == 2 {
			calendarDay, _ = parsePipelineStoryClockNumber(marker[1])
		}
		for index := 1; index < len(group); index++ {
			step := group[index].Seconds - group[index-1].Seconds
			if kind == "countdown" {
				step = -step
				if group[index].Source != first.Source {
					valid = false
				}
			}
			if kind == "wall_clock" {
				between := string([]rune(body)[group[index-1].EndRune:group[index].EndRune])
				if days, explicit := pipelineStoryClockDaysBetween(between, calendarDay); explicit {
					step += days * 86400
					calendarDay += days
				} else if step < 0 {
					// A rollover requires an actual date-boundary statement;
					// reversed readings alone never invent a missing day.
					if !pipelineStoryClockActualBoundary(between, []string{"午夜", "零点"}) {
						valid = false
					}
					step += 86400
					calendarDay++
				}
			}
			if step < 0 {
				valid = false
			}
			elapsed += step
		}
		if !valid {
			pairError = fmt.Errorf("story_clock 钟表读数不连续或跨日信息不足；请核对现实顺序与跨日交代")
			continue
		}
		evidence, err := finish(kind, first, last, elapsed)
		if err == nil {
			proved = evidence
		} else {
			pairError = err
		}
	}
	spans := append(pipelineStoryClockSpanRE.FindAllStringSubmatchIndex(body, -1), pipelineStoryClockPlainSpanRE.FindAllStringSubmatchIndex(body, -1)...)
	for _, indexes := range spans {
		text := body[indexes[0]:indexes[1]]
		if pipelineStoryClockNonActual(pipelineStoryClockSentenceAt(body, indexes[0])) {
			continue
		}
		startText := strings.TrimSpace(body[indexes[2]:indexes[3]])
		endText := strings.TrimSpace(body[indexes[4]:indexes[5]])
		duration := strings.TrimSpace(body[indexes[6]:indexes[7]])
		elapsed, ok := parsePipelineStoryClockDuration(duration)
		if !ok {
			continue
		}
		startByte := strings.Index(body[:indexes[0]], startText)
		endRune := utf8.RuneCountInString(body[:indexes[1]])
		if startByte < 0 || utf8.RuneCountInString(body[:startByte]) > count/3 || endRune < count*2/3 {
			continue
		}
		startSentence := pipelineStoryClockSentenceAt(body, startByte)
		if pipelineStoryClockNonActual(startSentence) || pipelineSealedSegmentNegatesExpected(startSentence, startText) {
			continue
		}
		start := pipelineStoryClockAnchor{Text: startText, StartRune: utf8.RuneCountInString(body[:startByte])}
		end := pipelineStoryClockAnchor{Text: endText + "；" + text, EndRune: endRune}
		evidence, err := finish("bounded_elapsed_span", start, end, elapsed)
		if err == nil {
			proved = evidence
		} else {
			pairError = err
		}
	}
	if pairError != nil {
		return nil, pairError
	}
	if proved != nil {
		return proved, nil
	}
	return nil, fmt.Errorf("story_clock 尚不能从正文证实本章实际经过 %.12g 秒；请在开场与收束自然交代现实钟表读数、同一具名截止点的剩余时间，或用正文开场/收束动作界定明确持续时间；计划、回忆和‘过了一会儿’不能作为已发生时间", wanted)
}

func collectPipelineStoryClockAnchors(body string) []pipelineStoryClockAnchor {
	var anchors []pipelineStoryClockAnchor
	for _, indexes := range pipelineStoryClockSentenceRE.FindAllStringIndex(body, -1) {
		sentence := body[indexes[0]:indexes[1]]
		if pipelineStoryClockNonActual(sentence) {
			continue
		}
		for _, match := range pipelineStoryClockRemainingRE.FindAllStringSubmatchIndex(sentence, -1) {
			duration := sentence[match[4]:match[5]]
			if seconds, ok := parsePipelineStoryClockDuration(duration); ok {
				anchors = append(anchors, pipelineStoryClockAnchor{
					Kind: "countdown", Source: strings.TrimSpace(sentence[match[2]:match[3]]), Seconds: seconds, Text: sentence,
					StartRune: utf8.RuneCountInString(body[:indexes[0]+match[0]]), EndRune: utf8.RuneCountInString(body[:indexes[0]+match[1]]),
				})
			}
		}
		kind, source := "wall_clock", ""
		if index := strings.Index(sentence, "倒计时"); index >= 0 {
			kind = "countdown"
			prefix := strings.TrimSpace(sentence[:index])
			for _, boundary := range []string{"，", ",", "：", ":", "“", "\""} {
				if at := strings.LastIndex(prefix, boundary); at >= 0 {
					prefix = strings.TrimSpace(prefix[at+len(boundary):])
				}
			}
			if prefix != "" {
				source = prefix
			}
		} else if pipelineSealedContainsAny(sentence, []string{"秒表", "计时器"}) {
			kind = "elapsed_clock"
			for _, name := range []string{"秒表", "计时器"} {
				if index := strings.Index(sentence, name); index >= 0 {
					source = strings.TrimSpace(sentence[:index+len(name)])
					break
				}
			}
		}
		if !pipelineSealedContainsAny(sentence, []string{"显示", "亮着", "跳到", "走到", "指向", "还剩", "剩余", "看", "瞥", "扫"}) ||
			!pipelineSealedContainsAny(sentence, []string{"手表", "腕表", "挂钟", "时钟", "墙上的钟", "钟指", "钟显", "钟面", "表盘", "看表", "看钟", "瞥表", "秒表", "手机", "屏幕", "屏", "倒计时", "计时器"}) {
			continue
		}
		for _, match := range pipelineStoryClockColonRE.FindAllStringSubmatchIndex(sentence, -1) {
			first, _ := strconv.ParseFloat(sentence[match[2]:match[3]], 64)
			second, _ := strconv.ParseFloat(sentence[match[4]:match[5]], 64)
			third, hasSeconds := 0.0, match[6] >= 0
			if hasSeconds {
				third, _ = strconv.ParseFloat(sentence[match[6]:match[7]], 64)
			}
			if second >= 60 || third >= 60 || (kind == "wall_clock" && first >= 24) {
				continue
			}
			seconds := first*3600 + second*60 + third
			if kind != "wall_clock" && !hasSeconds {
				seconds = first*60 + second
			}
			startByte, endByte := indexes[0]+match[0], indexes[0]+match[1]
			anchors = append(anchors, pipelineStoryClockAnchor{Kind: kind, Source: source, Seconds: seconds,
				Text: sentence, StartRune: utf8.RuneCountInString(body[:startByte]), EndRune: utf8.RuneCountInString(body[:endByte])})
		}
		if kind == "wall_clock" {
			for _, match := range pipelineStoryClockChineseRE.FindAllStringSubmatchIndex(sentence, -1) {
				hours, ok := parsePipelineStoryClockNumber(sentence[match[2]:match[3]])
				minutes, seconds := 0.0, 0.0
				if match[4] >= 0 {
					minutes, _ = parsePipelineStoryClockNumber(sentence[match[4]:match[5]])
				}
				if match[6] >= 0 {
					seconds, _ = parsePipelineStoryClockNumber(sentence[match[6]:match[7]])
				}
				if !ok || hours >= 24 || minutes >= 60 || seconds >= 60 {
					continue
				}
				if strings.HasPrefix(sentence[match[1]:], "半") {
					minutes = 30
				} else if strings.HasPrefix(sentence[match[1]:], "秒") || strings.HasPrefix(sentence[match[1]:], "刻") {
					continue
				}
				startByte, endByte := indexes[0]+match[0], indexes[0]+match[1]
				anchors = append(anchors, pipelineStoryClockAnchor{Kind: kind, Seconds: hours*3600 + minutes*60 + seconds,
					Text: sentence, StartRune: utf8.RuneCountInString(body[:startByte]), EndRune: utf8.RuneCountInString(body[:endByte])})
			}
		}
		if kind == "wall_clock" || pipelineStoryClockColonRE.MatchString(sentence) {
			continue
		}
		for _, marker := range []string{"显示", "还剩", "剩余", "跳到", "走到"} {
			at := strings.LastIndex(sentence, marker)
			if at < 0 {
				continue
			}
			duration := strings.Trim(sentence[at+len(marker):], " ：:，,。！？!?\n\r\t“”\"")
			if seconds, ok := parsePipelineStoryClockDuration(duration); ok {
				anchors = append(anchors, pipelineStoryClockAnchor{Kind: kind, Source: source, Seconds: seconds,
					Text: sentence, StartRune: utf8.RuneCountInString(body[:indexes[0]+at]), EndRune: utf8.RuneCountInString(body[:indexes[1]])})
				break
			}
		}
	}
	// Different supported formats may occur in one sentence. Preserve actual
	// body order rather than the order in which format parsers ran.
	sort.SliceStable(anchors, func(i, j int) bool { return anchors[i].StartRune < anchors[j].StartRune })
	return anchors
}

func pipelineStoryClockDaysBetween(text string, currentDay float64) (float64, bool) {
	startDay, explicit := currentDay, false
	for _, sentence := range pipelineStoryClockSentenceRE.FindAllString(text, -1) {
		if pipelineStoryClockNonActual(sentence) {
			continue
		}
		if match := pipelineStoryClockDaysRE.FindStringSubmatch(sentence); len(match) == 2 {
			if days, ok := parsePipelineStoryClockNumber(match[1]); ok {
				currentDay += days
				explicit = true
			}
		} else if matches := pipelineStoryClockOrdinalDayRE.FindAllStringSubmatch(sentence, -1); len(matches) > 0 {
			if day, ok := parsePipelineStoryClockNumber(matches[len(matches)-1][1]); ok && day > 0 {
				currentDay = day
				explicit = true
			}
		} else if pipelineSealedContainsAny(sentence, []string{"次日", "翌日"}) {
			currentDay++
			explicit = true
		}
	}
	return currentDay - startDay, explicit
}

func pipelineStoryClockActualBoundary(text string, markers []string) bool {
	for _, sentence := range pipelineStoryClockSentenceRE.FindAllString(text, -1) {
		if !pipelineStoryClockNonActual(sentence) && pipelineSealedContainsAny(sentence, markers) {
			return true
		}
	}
	return false
}

func pipelineStoryClockSentenceAt(body string, byteOffset int) string {
	for _, indexes := range pipelineStoryClockSentenceRE.FindAllStringIndex(body, -1) {
		if indexes[0] <= byteOffset && byteOffset < indexes[1] {
			return body[indexes[0]:indexes[1]]
		}
	}
	return ""
}

func pipelineStoryClockNonActual(text string) bool {
	return pipelineSealedContainsAny(text, []string{
		"如果", "假如", "假设", "计划", "预计", "预定", "约定", "应该", "将会", "将要", "明天", "昨天", "昨晚", "回忆", "记得",
		"大约", "差不多", "好像", "仿佛", "可能", "估计", "似乎",
		"照片", "录像", "录屏", "截图", "旧影像", "没有显示", "尚未走到", "还没走到", "没有过去", "未曾过去", "不是现在", "坏了", "停住了",
		"纸上", "记录中", "旧记录", "记录里的", "日程", "排班表", "快了", "慢了", "拨快", "拨慢", "校时", "调整时间", "重置", "重新计时",
	})
}

func parsePipelineStoryClockDuration(text string) (float64, bool) {
	text = strings.Trim(text, " 。；;，,\n\r\t“”\"")
	matches := pipelineStoryClockNumberRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return 0, false
	}
	seconds, previous, lastScale := 0.0, 0, math.Inf(1)
	for _, match := range matches {
		if strings.Trim(text[previous:match[0]], " 又零\t") != "" {
			return 0, false
		}
		value, ok := parsePipelineStoryClockNumber(text[match[2]:match[3]])
		if !ok {
			return 0, false
		}
		scale := map[string]float64{"天": 86400, "小时": 3600, "分钟": 60, "分": 60, "秒钟": 1, "秒": 1}[text[match[4]:match[5]]]
		if scale >= lastScale {
			return 0, false
		}
		seconds += value * scale
		lastScale, previous = scale, match[1]
	}
	return seconds, strings.TrimSpace(text[previous:]) == "" && !math.IsInf(seconds, 0) && !math.IsNaN(seconds)
}

func parsePipelineStoryClockNumber(text string) (float64, bool) {
	if value, err := strconv.ParseFloat(text, 64); err == nil {
		return value, value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
	}
	total, digit := 0, 0
	for _, r := range text {
		if unit, ok := map[rune]int{'十': 10, '百': 100, '千': 1000}[r]; ok {
			if digit == 0 {
				digit = 1
			}
			total += digit * unit
			digit = 0
		} else if value, ok := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}[r]; ok {
			digit = value
		} else {
			return 0, false
		}
	}
	return float64(total + digit), text != ""
}

func validatePipelineSealedStoryClockMatch(st *store.Store, bundle *domain.ProjectedChapterBundle, commit *domain.Checkpoint, bodySHA string, match *pipelineSealedActualDeltaMatch) error {
	if bundle == nil || bundle.ChapterWorldSimulation.StoryTime == nil {
		return nil
	}
	if err := domain.ValidateProjectedChapterBundle(*bundle); err != nil {
		return err
	}
	if match == nil || match.StoryClockEvidence == nil {
		return fmt.Errorf("story_clock acceptance requires independently located exact-body time evidence")
	}
	bodyPath := filepath.Join("chapters", fmt.Sprintf("%02d.md", bundle.Chapter))
	if commit == nil || commit.Step != "commit" || !commit.Scope.Matches(domain.ChapterScope(bundle.Chapter)) ||
		filepath.ToSlash(commit.Artifact) != filepath.ToSlash(bodyPath) || commit.Digest != bodySHA {
		return fmt.Errorf("story_clock acceptance commit is not bound to exact chapter body")
	}
	body, err := os.ReadFile(filepath.Join(st.Dir(), bodyPath))
	if err != nil {
		return fmt.Errorf("story_clock acceptance body: %w", err)
	}
	if pipelineStoryClockBodySHA(string(body)) != bodySHA {
		return fmt.Errorf("story_clock acceptance exact body hash differs from outcome")
	}
	verified, err := derivePipelineSealedStoryClockEvidence(bundle, string(body))
	if err != nil {
		return err
	}
	if *verified != *match.StoryClockEvidence {
		return fmt.Errorf("story_clock acceptance evidence differs from independently reread exact body")
	}
	return nil
}

func pipelineOutcomeHasStoryClock(outcome *domain.ActualOutcomeReceiptV2) bool {
	if outcome != nil {
		for _, mutation := range outcome.ActualDelta.Timeline {
			if pipelineSealedStoryClockMutation("timeline", mutation) {
				return true
			}
		}
	}
	return false
}

// Replay at acceptance/arc unlock/canon promotion. Receipt hashes establish
// provenance, but never substitute for rechecking the exact durable prose.
func validatePipelineAcceptedStoryClock(st *store.Store, outcome *domain.ActualOutcomeReceiptV2) error {
	if !pipelineOutcomeHasStoryClock(outcome) {
		return nil
	}
	bundle, err := pipelineProjectAllBundleForChapter(st.ProjectedV2(), outcome.GenerationID, outcome.Chapter)
	if err != nil {
		return fmt.Errorf("accepted story_clock bundle: %w", err)
	}
	if bundle.ChapterWorldSimulation.StoryTime == nil {
		return fmt.Errorf("accepted story_clock has no adjudicated chapter time")
	}
	body, err := os.ReadFile(filepath.Join(st.Dir(), "chapters", fmt.Sprintf("%02d.md", outcome.Chapter)))
	if err != nil {
		return fmt.Errorf("accepted story_clock exact body: %w", err)
	}
	if pipelineStoryClockBodySHA(string(body)) != outcome.ChapterBodySHA256 {
		return fmt.Errorf("accepted story_clock exact body hash differs from outcome")
	}
	evidence, err := derivePipelineSealedStoryClockEvidence(bundle, string(body))
	if err != nil {
		return err
	}
	count := 0
	for _, mutation := range outcome.ActualDelta.Timeline {
		if !pipelineSealedStoryClockMutation("timeline", mutation) {
			continue
		}
		count++
		if mutation.Operation != "advance" || mutation.Object != "" || !pipelineStoryClockNumberEqual(mutation.Before, evidence.StartDay) || !pipelineStoryClockNumberEqual(mutation.After, evidence.EndDay) {
			return fmt.Errorf("accepted story_clock actual delta differs from exact-body/adjudicated time")
		}
	}
	if count != 1 {
		return fmt.Errorf("accepted story_clock requires exactly one actual world/story_day mutation")
	}
	return nil
}
