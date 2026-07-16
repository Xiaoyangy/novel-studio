package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	DraftHardFactAnchorAmount      = "amount"
	DraftHardFactAnchorDistance    = "distance"
	DraftHardFactAnchorEntityCount = "entity_count"
	DraftHardFactAnchorLiteral     = "literal"
)

// DraftHardFactAnchor is a deliberately narrow prose invariant extracted from
// the current plan. It represents only exact real-world amounts, distances,
// supported entity counts and the explicit 少糖 marker. It is not a general
// semantic contract and must not be extended to writing-recipe counts such as
// "两条主观链" or "一次回应".
type DraftHardFactAnchor struct {
	Kind    string `json:"kind"`
	Value   int64  `json:"value,omitempty"`
	Unit    string `json:"unit,omitempty"`
	Ordinal bool   `json:"ordinal,omitempty"`
	Literal string `json:"literal,omitempty"`
	Source  string `json:"source,omitempty"`
}

type DraftHardFactAnchorResult struct {
	Passed  bool                  `json:"passed"`
	Missing []DraftHardFactAnchor `json:"missing,omitempty"`
	Anchors []DraftHardFactAnchor `json:"anchors,omitempty"`
}

const draftHardFactNumberPattern = `(?:[0-9０-９][0-9０-９,，]*(?:万|亿)?|[零〇○一二两三四五六七八九十百千万亿]+)`

var (
	draftHardFactAmountPattern = regexp.MustCompile(`(?:人民币[[:space:]]*)?(` + draftHardFactNumberPattern + `)[[:space:]]*(块钱|元|块)`)
	// Chinese prose often writes “一百万到账” or “一百万专项经营额度”
	// without repeating 元. Treat that as money only when the number is bound
	// directly to an unambiguous monetary cue; a naked “一百万” must not satisfy
	// an amount anchor.
	draftHardFactContextAmountPattern = regexp.MustCompile(`(` + draftHardFactNumberPattern + `)[[:space:]]*(?:的[[:space:]]*)?(?:青山县专项经营额度|专项经营额度|经营专项额度|经营专用额度|专项额度|经营额度|可用额度|授信额度|到账|入账)`)
	draftHardFactDistancePattern      = regexp.MustCompile(`(` + draftHardFactNumberPattern + `)[[:space:]]*(公里|千米)`)
	draftHardFactCountPattern         = regexp.MustCompile(`(第)?(` + draftHardFactNumberPattern + `)[[:space:]]*(?:个)?(张桌子|张桌|桌子|摊位|碗|摊|套|桌)`)
)

// InspectDraftHardFactAnchors checks exact prose anchors from the current,
// write-eligible plan. A stale plan is an input error, not a prose failure: its
// obsolete facts must never become the baseline for a new candidate.
func InspectDraftHardFactAnchors(st *store.Store, chapter int, content string) (DraftHardFactAnchorResult, error) {
	result := DraftHardFactAnchorResult{}
	guard, err := validateCurrentChapterRenderPlan(st, chapter)
	if err != nil {
		return result, fmt.Errorf("第 %d 章 hard-fact anchor 检查前 plan freshness 复验失败: %w", chapter, err)
	}
	if guard.Plan == nil {
		// Legacy/imported non-pipeline projects may intentionally have no formal
		// plan. They have no anchor contract to enforce; pipeline-managed missing
		// plans were already rejected by validateCurrentChapterRenderPlan above.
		result.Passed = true
		return result, nil
	}

	sources := append([]string(nil), guard.Plan.CausalSimulation.ReviewRefinement.PreserveConstraints...)
	sources = append(sources, RenderRequiredOutcomes(*guard.Plan)...)
	result.Anchors = extractDraftHardFactAnchors(sources)
	actual := extractDraftHardFactAnchors([]string{content})
	actualKeys := make(map[string]struct{}, len(actual))
	for _, anchor := range actual {
		actualKeys[draftHardFactAnchorKey(anchor)] = struct{}{}
	}
	for _, anchor := range result.Anchors {
		if _, ok := actualKeys[draftHardFactAnchorKey(anchor)]; !ok {
			result.Missing = append(result.Missing, anchor)
		}
	}
	result.Passed = len(result.Missing) == 0
	return result, nil
}

func requireDraftHardFactAnchors(st *store.Store, chapter int, content string) error {
	inspection, err := InspectDraftHardFactAnchors(st, chapter, content)
	if err != nil {
		return err
	}
	if inspection.Passed {
		return nil
	}
	details := make([]string, 0, len(inspection.Missing))
	for _, anchor := range inspection.Missing {
		details = append(details, draftHardFactAnchorMissingDetail(anchor))
	}
	return fmt.Errorf("第 %d 章正文缺少当前 plan 的 hard-fact anchors：%s: %w",
		chapter, strings.Join(details, "；"), errs.ErrToolPrecondition)
}

func draftHardFactAnchorViolation(anchor DraftHardFactAnchor) string {
	return "hard_fact_anchor: missing " + draftHardFactAnchorMissingDetail(anchor) + "；当前不可 commit_chapter"
}

func draftHardFactAnchorMissingDetail(anchor DraftHardFactAnchor) string {
	value := strconv.FormatInt(anchor.Value, 10)
	unit := anchor.Unit
	if anchor.Kind == DraftHardFactAnchorLiteral {
		value = anchor.Literal
		unit = "literal"
	}
	ordinal := ""
	if anchor.Ordinal {
		ordinal = " ordinal=true"
	}
	return fmt.Sprintf("kind=%s value=%s unit=%s%s source=%q",
		anchor.Kind, value, unit, ordinal, draftHardFactSourceSummary(anchor.Source))
}

func draftHardFactSourceSummary(source string) string {
	source = strings.Join(strings.Fields(source), " ")
	runes := []rune(source)
	const limit = 72
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return source
}

func extractDraftHardFactAnchors(texts []string) []DraftHardFactAnchor {
	seen := make(map[string]struct{})
	var anchors []DraftHardFactAnchor
	add := func(anchor DraftHardFactAnchor) {
		key := draftHardFactAnchorKey(anchor)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		anchors = append(anchors, anchor)
	}

	for _, source := range texts {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		for _, clause := range draftHardFactClauses(source) {
			for _, match := range draftHardFactAmountPattern.FindAllStringSubmatchIndex(clause, -1) {
				if draftHardFactNegativePrefix(clause[:match[0]]) {
					continue
				}
				number, ok := parseDraftHardFactInteger(clause[match[2]:match[3]])
				if !ok {
					continue
				}
				unit := clause[match[4]:match[5]]
				if unit == "块" && !draftHardFactBareBlockIsMoney(clause[:match[0]], clause[match[1]:]) {
					continue
				}
				add(DraftHardFactAnchor{Kind: DraftHardFactAnchorAmount, Value: number, Unit: "元", Source: source})
			}
			for _, match := range draftHardFactContextAmountPattern.FindAllStringSubmatchIndex(clause, -1) {
				if draftHardFactNegativePrefix(clause[:match[0]]) {
					continue
				}
				number, ok := parseDraftHardFactInteger(clause[match[2]:match[3]])
				if !ok {
					continue
				}
				add(DraftHardFactAnchor{Kind: DraftHardFactAnchorAmount, Value: number, Unit: "元", Source: source})
			}
			for _, match := range draftHardFactDistancePattern.FindAllStringSubmatchIndex(clause, -1) {
				if draftHardFactNegativePrefix(clause[:match[0]]) {
					continue
				}
				number, ok := parseDraftHardFactInteger(clause[match[2]:match[3]])
				if !ok {
					continue
				}
				add(DraftHardFactAnchor{Kind: DraftHardFactAnchorDistance, Value: number, Unit: "公里", Source: source})
			}
			for _, match := range draftHardFactCountPattern.FindAllStringSubmatchIndex(clause, -1) {
				if draftHardFactNegativePrefix(clause[:match[0]]) {
					continue
				}
				number, ok := parseDraftHardFactInteger(clause[match[4]:match[5]])
				if !ok {
					continue
				}
				unit := normalizeDraftHardFactEntityUnit(clause[match[6]:match[7]])
				add(DraftHardFactAnchor{
					Kind: DraftHardFactAnchorEntityCount, Value: number, Unit: unit,
					Ordinal: match[2] >= 0, Source: source,
				})
			}
			if index := strings.Index(clause, "少糖"); index >= 0 && !draftHardFactNegativePrefix(clause[:index]) {
				add(DraftHardFactAnchor{Kind: DraftHardFactAnchorLiteral, Literal: "少糖", Source: source})
			}
		}
	}
	return anchors
}

func draftHardFactClauses(text string) []string {
	runes := []rune(text)
	clauses := make([]string, 0, 8)
	var current strings.Builder
	flush := func() {
		if clause := strings.TrimSpace(current.String()); clause != "" {
			clauses = append(clauses, clause)
		}
		current.Reset()
	}
	for index, r := range runes {
		if (r == ',' || r == '，') && index > 0 && index+1 < len(runes) &&
			isDraftHardFactArabicDigit(runes[index-1]) && isDraftHardFactArabicDigit(runes[index+1]) {
			// Thousands separators are part of the number, not clause
			// punctuation: 1,000,000元 must stay one exact amount.
			current.WriteRune(r)
			continue
		}
		switch r {
		case '，', ',', '。', '；', ';', '！', '!', '？', '?', '\n', '\r':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return clauses
}

func isDraftHardFactArabicDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= '０' && r <= '９')
}

func draftHardFactNegativePrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	runes := []rune(prefix)
	if len(runes) > 16 {
		prefix = string(runes[len(runes)-16:])
	}
	for _, marker := range []string{
		"不得", "不能", "不可", "禁止", "严禁", "不准", "不许", "不要", "切勿",
		"不点", "不买", "不付", "不收", "不用", "无需", "无须",
		"没有", "并非", "不是", "尚未", "未有", "未出现", "未增加",
		"未付", "未走", "未到", "未收", "未买", "未点", "未花",
		"全程无", "并无", "毫无", "绝无", "不存在", "没",
	} {
		if strings.Contains(prefix, marker) {
			return true
		}
	}
	return false
}

func draftHardFactBareBlockIsMoney(prefix, suffix string) bool {
	if suffix == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	if unicode.IsSpace(r) || unicode.IsPunct(r) {
		return true
	}
	context := prefix + suffix
	for _, cue := range []string{
		"合计", "共计", "总计", "一共", "付款", "付了", "支付", "收款", "收了", "售价", "价格", "报价", "要价",
		"油费", "人工", "工钱", "材料费", "五金费", "取货款", "费用", "金额", "成本", "块的工钱", "块的油费",
	} {
		if strings.Contains(context, cue) {
			return true
		}
	}
	return false
}

func normalizeDraftHardFactEntityUnit(unit string) string {
	switch unit {
	case "摊", "摊位":
		return "摊位"
	case "张桌", "张桌子", "桌", "桌子":
		return "桌"
	default:
		return unit
	}
}

func draftHardFactAnchorKey(anchor DraftHardFactAnchor) string {
	if anchor.Kind == DraftHardFactAnchorLiteral {
		return anchor.Kind + ":" + anchor.Literal
	}
	return fmt.Sprintf("%s:%d:%s:%t", anchor.Kind, anchor.Value, anchor.Unit, anchor.Ordinal)
}

func parseDraftHardFactInteger(raw string) (int64, bool) {
	raw = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(raw, ",", ""), "，", ""))
	if raw == "" {
		return 0, false
	}
	arabicRaw := raw
	var arabicMultiplier int64 = 1
	if strings.HasSuffix(arabicRaw, "万") {
		arabicRaw = strings.TrimSuffix(arabicRaw, "万")
		arabicMultiplier = 10_000
	} else if strings.HasSuffix(arabicRaw, "亿") {
		arabicRaw = strings.TrimSuffix(arabicRaw, "亿")
		arabicMultiplier = 100_000_000
	}
	var ascii strings.Builder
	arabic := true
	for _, r := range arabicRaw {
		switch {
		case r >= '0' && r <= '9':
			ascii.WriteRune(r)
		case r >= '０' && r <= '９':
			ascii.WriteRune('0' + (r - '０'))
		default:
			arabic = false
		}
	}
	if arabic && ascii.Len() > 0 {
		value, err := strconv.ParseInt(ascii.String(), 10, 64)
		if err != nil || value > 9_223_372_036_854_775_807/arabicMultiplier {
			return 0, false
		}
		return value * arabicMultiplier, true
	}

	digits := map[rune]int64{
		'零': 0, '〇': 0, '○': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
		'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	}
	containsUnit := strings.ContainsAny(raw, "十百千万亿")
	if !containsUnit {
		var value int64
		for _, r := range raw {
			digit, ok := digits[r]
			if !ok {
				return 0, false
			}
			value = value*10 + digit
		}
		return value, true
	}

	var total, section, number int64
	for _, r := range raw {
		if digit, ok := digits[r]; ok {
			number = digit
			continue
		}
		switch r {
		case '十', '百', '千':
			unit := map[rune]int64{'十': 10, '百': 100, '千': 1000}[r]
			if number == 0 {
				number = 1
			}
			section += number * unit
			number = 0
		case '万':
			section += number
			total += section * 10_000
			section, number = 0, 0
		case '亿':
			section += number
			total += section * 100_000_000
			section, number = 0, 0
		default:
			return 0, false
		}
	}
	return total + section + number, true
}
