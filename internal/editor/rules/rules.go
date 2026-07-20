package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	FigurativeDensityLimit = 0.25
	DialogueRatioLimit     = 0.12
)

var aphorismPatterns = []struct {
	rule string
	re   *regexp.Regexp
}{
	{rule: "aphorism_reject_i_dont_want_x_or_y", re: regexp.MustCompile(`我不要[^。！？!?；;\n]{0,30}[，,、]?也不要[^。！？!?；;\n]{0,30}`)},
	{rule: "aphorism_i_never", re: regexp.MustCompile(`我从未[^。！？!?；;\n]{1,30}`)},
	{rule: "aphorism_i_will_declare", re: regexp.MustCompile(`(?:^|[“"\n])我要[^。！？!?；;\n]{1,30}`)},
	{rule: "aphorism_two_words_summary", re: regexp.MustCompile(`[^。！？!?；;\n]{1,16}两个字[，,]?原来也会[^。！？!?；;\n]{0,30}`)},
	{rule: "aphorism_i_no_longer_need_you", re: regexp.MustCompile(`我已经不需要你`)},
	{rule: "aphorism_broken_people", re: regexp.MustCompile(`残缺的人[，,]?(?:才|就|会)[^。！？!?；;\n]{0,30}`)},
}

// "我从未"本身不是格言。庭审、合同争议和现场对质里，人物经常需要否认一个
// 可核验事实，例如“我从未持有该账户”或“我从未进入过仓库”。只有把这类事实
// 陈述当成可摘抄宣言，规则才应该介入；下面两组词把两者分开，避免对白一律豁免。
var (
	neverFactualPredicateRe = regexp.MustCompile(`^(?:持有|保管|控制|占有|管理|接触|收到|发送|转交|转让|转账|付款|借款|借用|购买|出售|使用|登录|登记|开立|授权|签署|签过|提交|删除|修改|下载|上传|拍摄|直播|下单|点过|配送|递送|拨打|联系|进入|离开|到过|去过|来过|见过|见到|认识|参与|担任|雇佣|指使|威胁|伤害|杀害|藏匿|拿过|碰过|收过|给过|说过|承诺|同意|承认|举报|报案|查过|看过|听过|等过)`)
	neverFactualObjectRe    = regexp.MustCompile(`(?:账户|账号|银行卡|资产|款项|现金|合同|协议|文件|证件|签名|电话|号码|手机|电脑|订单|外卖|直播|录音|录像|照片|现场|仓库|房间|车辆|门禁|钥匙|凶器|药物|包裹|货物|转账|付款)`)
	neverAbstractCueRe      = regexp.MustCompile(`(?:如此|这样|这般|真正|命运|真相|黑暗|光明|自由|救赎|孤独|完整|残缺|世界|人心|灵魂|意义|清醒|勇敢|恐惧|希望|绝望|幸福|永恒|人生)`)
)

var (
	figurativeRe = regexp.MustCompile(`(像|好像|仿佛|宛如|如同|似|倒像)`)
	quoteRe      = regexp.MustCompile(`[“「][^”」]{1,240}[”」]`)
	// A genuine waver has an impulse followed by a deliberate override. Neither
	// a bare hesitation word nor an ordinary reflex (for example “下意识抬手，
	// 随后停住”) earns credit on its own.
	waverImpulseRe  = regexp.MustCompile(`(?:第一反应|本想|原本想|一度想|想要|想去|想说|想答应|想拒绝|冲动|犹豫|迟疑|差点|险些|已经准备|正准备|刚要|正要|几乎[^。！？!?；;\n]{0,16}(?:作出|做出)?决定|先(?:说|答|承诺|拒绝|答应))`)
	waverOverrideRe = regexp.MustCompile(`(?:强迫自己|又改口|改了主意|转而|反而|话到嘴边[^。！？!?；;\n]{0,12}(?:咽回|吞回|没说|没有说)|(?:却|但|随后|最终|还是)[^。！？!?；;\n]{0,20}(?:停住|收回|忍住|压住|放弃|拒绝|改口|改成|重新|再看|退出|关掉|删掉|没有(?:去|答应|开门|下车)|不再|不肯|不愿)|(?:收回|忍住|压住|放弃|拒绝|改口|重新(?:检查|核对|考虑)|退出(?:导航|路线)|没有(?:去|答应|开门|下车)|不再(?:碰|继续)|没说完))`)
	conflictRe      = regexp.MustCompile(`(质问|反问|逼问|对峙|拦住|证据|签字|审问|威胁|赔偿|交代|摊牌)`)
	atmosphereRe    = regexp.MustCompile(`(雨|雾|风|冷|潮|灰尘|霉味|灯|影|钟声|走廊|窗|门缝|古堡|墓园|月光)`)
	hookRe          = regexp.MustCompile(`(下一秒|下一刻|下一章|第三响|谁|还没|没有结束|露出|钥匙|名单|照片|来信|遗书|信封|短信|血|契约|吗[？?]|[？！]{1,2})`)

	numberedLadderRe       = regexp.MustCompile(`(?s)(第一|首先|一是|一、|1[.、）\)]).{0,80}?(第二|其次|二是|二、|2[.、）\)]).{0,80}?(第三|最后|三是|三、|3[.、）\)])`)
	openingGoldRe          = regexp.MustCompile(`(命运|黑暗|自由|勇敢|恐惧|孤独|残缺|真相|世界|人心|从来|真正|原来|不是[^。！？!?；;\n]{0,20}而是|才会|终究|总会)`)
	actionSensoryRe        = regexp.MustCompile(`(说|问|答|喊|推|拉|握|攥|摸|碰|退|走|站|坐|听|闻|嗅|疼|冷|热|雨|风|门|灯|血|气味|脚步|钟声|指尖|手套|杯|纸|钥匙)`)
	purposeAnswerRe        = regexp.MustCompile(`我(?:从一开始|一开始|本来|原本|早就)[^。！？!?；;\n”」]{0,18}(?:为|为了|冲着|奔着)[^。！？!?；;\n”」]{0,20}(?:来|来的)|我就是[^。！？!?；;\n”」]{0,20}来的`)
	hesitationRe           = regexp.MustCompile(`(?:沉默|迟疑|犹豫|改口|话到嘴边|没答|没有回答|答非所问|反问|只说了?半句|没说完|说到一半|隔了[^。！？!?；;\n]{0,8}(?:拍|秒)|半晌|一会儿)`)
	endingQuestionRe       = regexp.MustCompile(`[？?]\s*$`)
	endingGoldRe           = regexp.MustCompile(`(?:命运|人生|世界|人心|救赎|自由|勇敢|孤独|残缺|意义|真正|所谓|原来|终究|从来|难道|谁又能|又有谁|还算|才是|最终的答案|真正的答案|最终的选择|真正的选择)[^。！？!?；;\n]{0,48}[？?]\s*$`)
	pendingArrivalHookRe   = regexp.MustCompile(`第[一二三四五六七八九十百0-9]+(?:张|个|家|辆|封|份|位|道|次|批)?[^。！？!?；;\n]{0,24}(?:已经|正|还)?(?:停|摆|站|等|堵|落|送|到|出现)在(?:眼前|门外|桥头|路边)`)
	endingSentenceRe       = regexp.MustCompile(`[^。！？!?；;\n]+[。！？!?]?`)
	catalogRunRe           = regexp.MustCompile(`[\p{Han}A-Za-z0-9“”《》]{2,18}(?:[、，,；;][\p{Han}A-Za-z0-9“”《》]{2,18}){5,}`)
	catalogSplitRe         = regexp.MustCompile(`[、，,；;]+`)
	firstPersonQuoteLeadRe = regexp.MustCompile(`(?:^|[\n，,。！？!?；;])\s*我(?:说|问|答|喊|道|低声(?:说|问|答|喊|道)?)\s*[：:,，]?\s*$`)
	firstPersonQuoteTailRe = regexp.MustCompile(`^\s*我(?:说|问|答|喊|道|低声(?:说|问|答|喊|道)?)(?:\s*[，,。！？!?；;：:]|\s*$)`)
)

const (
	dialogueRatioNearMissTolerance          = 0.005
	minimumSupportingDialogueTurns          = 6
	minimumSupportingDialogueParagraphRatio = 0.18
)

// AnalyzeChapter 运行确定性 AI 腔规则引擎。
func AnalyzeChapter(chapter int, text string, history []domain.ChapterAIVoiceMetrics) domain.AIVoiceAnalysis {
	bodyText := stripMarkdownTitle(text)
	paragraphs := splitParagraphs(bodyText)
	sentences := splitSentences(bodyText)
	figurativeCount := countFigurativeSentences(sentences)
	dialogue := dialogueStats(paragraphs)
	totalChars := utf8.RuneCountInString(bodyText)
	if totalChars <= 0 {
		totalChars = 1
	}
	hits := aphorismHits(paragraphs)
	patternFlags := textPatternRedFlags(paragraphs)
	metrics := domain.ChapterAIVoiceMetrics{
		Chapter:                          chapter,
		FigurativeCount:                  figurativeCount,
		FigurativeDensity:                ratio(figurativeCount, len(paragraphs)),
		DialogueChars:                    dialogue.DialogueChars,
		SupportingDialogue:               dialogue.SupportingChars,
		DialogueRatio:                    float64(dialogue.SupportingChars) / float64(totalChars),
		SupportingDialogueTurns:          dialogue.SupportingTurns,
		SupportingDialogueParagraphs:     dialogue.SupportingParagraphs,
		SupportingDialogueParagraphRatio: ratio(dialogue.SupportingParagraphs, len(paragraphs)),
		ParagraphCount:                   len(paragraphs),
		SentenceCount:                    len(sentences),
		ChapterFunction:                  chapterFunction(bodyText, dialogue.DialogueChars, dialogue.SupportingChars, totalChars),
		AphorismHits:                     hits,
		ProtagonistWaver:                 protagonistWaver(paragraphs),
		EndingHookUsed:                   endingHookUsed(paragraphs),
		GeneratedAt:                      time.Now().Format(time.RFC3339),
	}
	metrics.AIVoiceScore = round4(clamp(aiVoiceScore(metrics, history)+textPatternRisk(patternFlags), 0, 1))
	metrics.ReaderExperienceScore = ReaderExperienceScore(bodyText, metrics)
	metrics.AIVoiceScoreHistory = append(metrics.AIVoiceScoreHistory, domain.AIVoiceScorePoint{
		Round:  metrics.RevisionRound,
		Source: "rules",
		Score:  metrics.AIVoiceScore,
		At:     metrics.GeneratedAt,
	})

	analysis := domain.AIVoiceAnalysis{
		Chapter:     chapter,
		Metrics:     metrics,
		GeneratedAt: metrics.GeneratedAt,
	}
	analysis.RedFlags = append(redFlags(metrics, history), patternFlags...)
	analysis.RedFlags = append(analysis.RedFlags, dialogueInfoDumpFlags(text)...)
	analysis.RedFlags = append(analysis.RedFlags, clinicalRegisterFlags(bodyText)...)
	analysis.Label = labelFor(metrics, analysis.RedFlags)
	analysis.Summary = summaryFor(metrics, analysis.RedFlags)
	return analysis
}

// RoughnessScore 越高表示越不像平滑模板文。
func RoughnessScore(metrics domain.ChapterAIVoiceMetrics) float64 {
	score := 1.0
	score -= math.Max(0, metrics.FigurativeDensity-FigurativeDensityLimit) * 1.6
	if metrics.DialogueRatio >= 0.08 && metrics.DialogueRatio <= 0.35 {
		score += 0.10
	} else if metrics.DialogueRatio > 0.45 {
		score -= math.Min((metrics.DialogueRatio-0.45)*0.8, 0.20)
	}
	if metrics.ProtagonistWaver {
		score += 0.18
	} else {
		score -= 0.18
	}
	score -= float64(len(metrics.AphorismHits)) * 0.25
	if metrics.ChapterFunction == "互动" || metrics.ChapterFunction == "留白" {
		score += 0.08
	}
	if metrics.EndingHookUsed {
		score -= 0.05
	}
	score -= math.Max(0, metrics.AIVoiceScore-0.25) * 0.4
	return round4(clamp(score, 0, 1.5))
}

// CandidateFromText 供 Writer 三采样评分使用。
func CandidateFromText(index, chapter int, text string) domain.SamplingCandidate {
	analysis := AnalyzeChapter(chapter, text, nil)
	sum := sha256.Sum256([]byte(text))
	roughness := round4(clamp(RoughnessScore(analysis.Metrics)-redFlagRoughnessPenalty(analysis.RedFlags), 0, 1.5))
	readability := analysis.Metrics.ReaderExperienceScore
	return domain.SamplingCandidate{
		Index:             index,
		ContentHash:       hex.EncodeToString(sum[:8]),
		RoughnessScore:    roughness,
		ReadabilityScore:  readability,
		SelectionScore:    SelectionScore(readability, roughness),
		FigurativeDensity: analysis.Metrics.FigurativeDensity,
		DialogueRatio:     analysis.Metrics.DialogueRatio,
		AphorismHitCount:  len(analysis.Metrics.AphorismHits),
		ProtagonistWaver:  analysis.Metrics.ProtagonistWaver,
		ChapterFunction:   analysis.Metrics.ChapterFunction,
		AIVoiceScore:      analysis.Metrics.AIVoiceScore,
	}
}

func splitParagraphs(text string) []string {
	text = stripMarkdownTitle(text)
	raw := regexp.MustCompile(`\n\s*\n+`).Split(text, -1)
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 && strings.TrimSpace(text) != "" {
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func splitSentences(text string) []string {
	text = stripMarkdownTitle(text)
	raw := regexp.MustCompile(`[。！？!?；;\n]+`).Split(text, -1)
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out = append(out, s)
	}
	return out
}

func protagonistWaver(paragraphs []string) bool {
	for i, paragraph := range paragraphs {
		impulses := waverImpulseRe.FindAllStringIndex(paragraph, -1)
		if len(impulses) == 0 {
			continue
		}
		// In one paragraph, enforce textual order instead of accepting an
		// override that was narrated before the impulse.
		for _, override := range waverOverrideRe.FindAllStringIndex(paragraph, -1) {
			for _, impulse := range impulses {
				if override[0] >= impulse[1] {
					return true
				}
			}
		}
		// A decision can resolve after one intervening beat, but not after an
		// arbitrary later event that merely happens to contain reversal words.
		for j := i + 1; j < len(paragraphs) && j <= i+2; j++ {
			if waverOverrideRe.MatchString(paragraphs[j]) {
				return true
			}
		}
	}
	return false
}

var plainChapterTitleRe = regexp.MustCompile(`^第[ 　]*[0-9零一二三四五六七八九十百千]+[ 　]*章(?:[ 　]+.*)?$`)

// stripMarkdownTitle returns narrative body text for style analysis. The raw
// chapter remains untouched for the independent plan/title consistency gate;
// this function only prevents either supported heading format from becoming
// paragraph 1 of the prose analysis.
func stripMarkdownTitle(text string) string {
	var lines []string
	firstNonEmptySeen := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if !firstNonEmptySeen {
				firstNonEmptySeen = true
			}
			continue
		}
		if !firstNonEmptySeen && trimmed != "" {
			firstNonEmptySeen = true
			if plainChapterTitleRe.MatchString(trimmed) {
				continue
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func countFigurativeSentences(sentences []string) int {
	count := 0
	for _, sentence := range sentences {
		if figurativeRe.MatchString(sentence) {
			count++
		}
	}
	return count
}

type dialogueStatsResult struct {
	DialogueChars        int
	SupportingChars      int
	SupportingTurns      int
	SupportingParagraphs int
}

func dialogueStats(paragraphs []string) dialogueStatsResult {
	var stats dialogueStatsResult
	for _, paragraph := range paragraphs {
		paragraphHasSupportingDialogue := false
		for _, loc := range quoteRe.FindAllStringIndex(paragraph, -1) {
			span := paragraph[loc[0]:loc[1]]
			size := utf8.RuneCountInString(span)
			stats.DialogueChars += size
			if firstPersonQuoteLeadRe.MatchString(paragraph[:loc[0]]) ||
				firstPersonQuoteTailRe.MatchString(paragraph[loc[1]:]) {
				continue
			}
			stats.SupportingChars += size
			stats.SupportingTurns++
			paragraphHasSupportingDialogue = true
		}
		if paragraphHasSupportingDialogue {
			stats.SupportingParagraphs++
		}
	}
	return stats
}

// infoDumpDigitRe 匹配对白里的数字串（房号/编号/日期），用于识别"罗列清单"的信息倾倒。
var infoDumpDigitRe = regexp.MustCompile(`[0-9]{2,}`)

// dialogueInfoDumpFlags 检测信息倾倒式对白：单段引号内一口气罗列清单（姓名+房号+背景、
// 来龙去脉）。真人在慌乱里说话是断的、有隐瞒、被追问才挤出下一条——"一口气念完一串结构化
// 信息"是检测器与读者都反感的 AI 招牌。命中给 warning，驱动重写把长独白拆成被追问/打断逼出。
func dialogueInfoDumpFlags(text string) []domain.AIVoiceRedFlag {
	var worst string
	worstScore := 0
	for _, loc := range quoteRe.FindAllStringIndex(text, -1) {
		span := text[loc[0]:loc[1]]
		n := utf8.RuneCountInString(span)
		if n < 60 {
			continue
		}
		seps := strings.Count(span, "，") + strings.Count(span, "、")
		digitGroups := len(infoDumpDigitRe.FindAllString(span, -1))
		// 长独白 + 多枚举分句，或 长独白 + 多个编号（房号/清单）→ 判为信息倾倒。
		if (n >= 70 && seps >= 3) || (n >= 60 && digitGroups >= 2) {
			score := n + seps*10 + digitGroups*15
			if score > worstScore {
				worstScore = score
				worst = span
			}
		}
	}
	if worst == "" {
		return nil
	}
	ex := []rune(worst)
	if len(ex) > 32 {
		ex = ex[:32]
	}
	return []domain.AIVoiceRedFlag{{
		Rule:       "dialogue_info_dump",
		Severity:   "warning",
		Suggestion: "信息倾倒式对白（一口气罗列清单/姓名+房号+背景）：拆成被对方追问、打断、质疑后一句一句逼出来，或把信息落到动作物件（推名单、指门、掏湿钱）上。示例段：「" + string(ex) + "…」",
	}}
}

func aphorismHits(paragraphs []string) []domain.AphorismHit {
	var hits []domain.AphorismHit
	for pIdx, p := range paragraphs {
		sentences := splitSentences(p)
		for sIdx, sentence := range sentences {
			for _, pat := range aphorismPatterns {
				match := strings.TrimSpace(pat.re.FindString(sentence))
				if match == "" {
					continue
				}
				if pat.rule == "aphorism_i_never" && isFactualNeverDenial(match) {
					continue
				}
				hits = append(hits, domain.AphorismHit{
					Rule:      pat.rule,
					Paragraph: pIdx + 1,
					Sentence:  sIdx + 1,
					Text:      truncateRunes(match, 80),
				})
			}
		}
	}
	return hits
}

func isFactualNeverDenial(match string) bool {
	remainder := strings.TrimSpace(strings.TrimPrefix(match, "我从未"))
	if remainder == "" || neverAbstractCueRe.MatchString(remainder) {
		return false
	}
	return neverFactualPredicateRe.MatchString(remainder) || neverFactualObjectRe.MatchString(remainder)
}

func textPatternRedFlags(paragraphs []string) []domain.AIVoiceRedFlag {
	var flags []domain.AIVoiceRedFlag
	if flag := openingSingleSentenceGoldFlag(paragraphs); flag != nil {
		flags = append(flags, *flag)
	}
	if flag := endingAphorismQuestionFlag(paragraphs); flag != nil {
		flags = append(flags, *flag)
	}
	flags = append(flags, numberedLadderFlags(paragraphs)...)
	flags = append(flags, instantPurposeAnswerFlags(paragraphs)...)
	flags = append(flags, catalogStuffingFlags(paragraphs)...)
	return flags
}

func openingSingleSentenceGoldFlag(paragraphs []string) *domain.AIVoiceRedFlag {
	if len(paragraphs) == 0 {
		return nil
	}
	sentences := splitSentences(paragraphs[0])
	if len(sentences) != 1 {
		return nil
	}
	sentence := strings.TrimSpace(sentences[0])
	if sentence == "" || utf8.RuneCountInString(sentence) > 70 {
		return nil
	}
	if !openingGoldRe.MatchString(sentence) || actionSensoryRe.MatchString(sentence) {
		return nil
	}
	return &domain.AIVoiceRedFlag{
		Rule:        "opening_single_sentence_aphorism",
		Severity:    "error",
		Paragraph:   1,
		Sentence:    1,
		Evidence:    truncateRunes(sentence, 80),
		Suggestion:  "开篇第一段改成动作、感官、物件或环境异常，不要先抛主题判断。",
		Replacement: "先写角色摸到/听见/闻到的异常，再让判断在后文浮出来。",
	}
}

func endingAphorismQuestionFlag(paragraphs []string) *domain.AIVoiceRedFlag {
	if len(paragraphs) == 0 {
		return nil
	}
	sentence := lastSentenceWithPunctuation(paragraphs[len(paragraphs)-1])
	if sentence == "" || !endingQuestionRe.MatchString(sentence) || !endingGoldRe.MatchString(sentence) {
		return nil
	}
	return &domain.AIVoiceRedFlag{
		Rule:        "ending_aphorism_question",
		Severity:    "warning",
		Paragraph:   len(paragraphs),
		Sentence:    sentenceIndexContaining(paragraphs[len(paragraphs)-1], strings.TrimRight(sentence, "？?")),
		Evidence:    truncateRunes(sentence, 80),
		Suggestion:  "章末不要用主题金句加问号当钩子，改成具体动作、物件变化、新事实或未完成选择。",
		Replacement: "让钩子落在现场：门响、账单变字、某人停手，或角色必须立刻做选择。",
	}
}

func numberedLadderFlags(paragraphs []string) []domain.AIVoiceRedFlag {
	var flags []domain.AIVoiceRedFlag
	for i, p := range paragraphs {
		match := strings.TrimSpace(numberedLadderRe.FindString(p))
		if match == "" {
			continue
		}
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:        "numbered_ladder_statement",
			Severity:    "error",
			Paragraph:   i + 1,
			Sentence:    sentenceIndexContaining(p, match),
			Evidence:    truncateRunes(match, 100),
			Suggestion:  "删掉 1-2-3 阶梯式规则陈述，改成角色逐个接触文件、物件或后果。",
			Replacement: "把三条规则拆进动作：一张纸、一句打断、一次代价，而不是顺序列清单。",
		})
	}
	return flags
}

func instantPurposeAnswerFlags(paragraphs []string) []domain.AIVoiceRedFlag {
	var flags []domain.AIVoiceRedFlag
	for i, p := range paragraphs {
		for _, loc := range quoteRe.FindAllStringIndex(p, -1) {
			quote := strings.Trim(p[loc[0]:loc[1]], "“”「」")
			if !purposeAnswerRe.MatchString(quote) {
				continue
			}
			start := max(0, loc[0]-80)
			end := min(len(p), loc[1]+80)
			if hesitationRe.MatchString(p[start:end]) {
				continue
			}
			flags = append(flags, domain.AIVoiceRedFlag{
				Rule:        "instant_purpose_answer_without_beat",
				Severity:    "error",
				Paragraph:   i + 1,
				Sentence:    sentenceIndexContaining(p, quote),
				Evidence:    truncateRunes(quote, 80),
				Suggestion:  "主角回答来意不能秒答成宣言；让他改口、反问、只答半句、答非所问或明确拒答，再由对方追问。不要用摸物件/停手动作充当通行证。",
				Replacement: "她先答了另一件事。对方追问后，她才把真正目的说出半句。",
			})
		}
	}
	return flags
}

func catalogStuffingFlags(paragraphs []string) []domain.AIVoiceRedFlag {
	var flags []domain.AIVoiceRedFlag
	type hit struct {
		paragraph int
		count     int
		evidence  string
	}
	var hits []hit
	for i, p := range paragraphs {
		bestCount := 0
		bestEvidence := ""
		for _, run := range catalogRunRe.FindAllString(p, -1) {
			count := len(catalogItems(run))
			if count > bestCount {
				bestCount = count
				bestEvidence = run
			}
		}
		if bestCount < 8 {
			continue
		}
		severity := "warning"
		if bestCount >= 10 {
			severity = "error"
		}
		hits = append(hits, hit{paragraph: i + 1, count: bestCount, evidence: bestEvidence})
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:        "catalog_stuffing",
			Severity:    severity,
			Paragraph:   i + 1,
			Evidence:    truncateRunes(bestEvidence, 120),
			Actual:      float64(bestCount),
			Limit:       7,
			Suggestion:  "删掉检测投机式长清单，只保留 3-5 个会入账、触发规则、制造冲突或后文回收的物件。",
			Replacement: "把长串名词改成少量功能物件 + 一个可见动作/规则后果；其余用“其余暂不入档/来源待核”一笔带过。",
		})
	}
	if len(hits) >= 2 {
		var parts []string
		for _, h := range hits[:min(len(hits), 4)] {
			parts = append(parts, fmt.Sprintf("第%d段%d项", h.paragraph, h.count))
		}
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:       "catalog_stuffing_run",
			Severity:   "error",
			Paragraph:  hits[0].paragraph,
			Evidence:   strings.Join(parts, "；"),
			Actual:     float64(len(hits)),
			Limit:      1,
			Suggestion: "连续多段清单会形成“为抬 TTR 堆词”的 AI 痕迹；必须改成交易、误判、对话打断或规则代价。",
		})
	}
	return flags
}

func catalogItems(run string) []string {
	raw := catalogSplitRe.Split(run, -1)
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.Trim(item, " \t\r\n“”「」《》：:。！？!?")
		if utf8.RuneCountInString(item) < 2 {
			continue
		}
		items = append(items, item)
	}
	return items
}

func redFlagRoughnessPenalty(flags []domain.AIVoiceRedFlag) float64 {
	penalty := 0.0
	for _, flag := range flags {
		switch flag.Rule {
		case "catalog_stuffing", "catalog_stuffing_run":
			if flag.Severity == "error" {
				penalty += 0.45
			} else {
				penalty += 0.25
			}
		default:
			if flag.Severity == "error" {
				penalty += 0.18
			}
		}
	}
	return math.Min(0.70, penalty)
}

func lastSentenceWithPunctuation(text string) string {
	matches := endingSentenceRe.FindAllString(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		sentence := strings.TrimSpace(matches[i])
		if sentence == "" {
			continue
		}
		return sentence
	}
	return ""
}

func textPatternRisk(flags []domain.AIVoiceRedFlag) float64 {
	risk := 0.0
	for _, flag := range flags {
		if flag.Severity == "error" {
			risk += 0.12
		} else {
			risk += 0.06
		}
	}
	return math.Min(0.30, risk)
}

func sentenceIndexContaining(paragraph, evidence string) int {
	if evidence == "" {
		return 0
	}
	sentences := splitSentences(paragraph)
	for i, sentence := range sentences {
		if strings.Contains(sentence, evidence) || strings.Contains(evidence, sentence) {
			return i + 1
		}
	}
	return 0
}

func chapterFunction(text string, dialogueChars, supportingDialogue, totalChars int) string {
	dialogueRatio := float64(supportingDialogue) / float64(max(totalChars, 1))
	dialogueLimit := dialogueRatioLimitForSize(totalChars, 0, 0)
	switch {
	case dialogueRatio >= dialogueLimit:
		return "互动"
	case conflictRe.MatchString(text):
		return "对质"
	case atmosphereRe.MatchString(text) && dialogueRatio < 0.18:
		return "氛围"
	default:
		return "留白"
	}
}

func endingHookUsed(paragraphs []string) bool {
	if len(paragraphs) == 0 {
		return false
	}
	start := max(0, len(paragraphs)-2)
	tail := strings.Join(paragraphs[start:], "\n")
	if hookRe.MatchString(tail) || pendingArrivalHookRe.MatchString(tail) {
		return true
	}
	last := paragraphs[len(paragraphs)-1]
	return utf8.RuneCountInString(last) <= 35 && strings.ContainsAny(last, "？！?")
}

func aiVoiceScore(metrics domain.ChapterAIVoiceMetrics, history []domain.ChapterAIVoiceMetrics) float64 {
	score := 0.08
	if metrics.FigurativeDensity > FigurativeDensityLimit {
		score += math.Min(0.30, (metrics.FigurativeDensity-FigurativeDensityLimit)*1.25)
	}
	dialogueLimit := dialogueRatioLimitForMetrics(metrics)
	if metrics.DialogueRatio < dialogueLimit && supportingDialogueInteractionSparse(metrics) {
		score += math.Min(0.24, (dialogueLimit-metrics.DialogueRatio)*0.95)
	}
	score += math.Min(0.30, float64(len(metrics.AphorismHits))*0.12)
	if !metrics.ProtagonistWaver {
		score += 0.12
	}
	if metrics.EndingHookUsed {
		score += 0.04
	}
	recentHistory := recentDistinctChapterMetrics(history, metrics.Chapter, 4)
	if len(recentHistory) >= 2 {
		recentHooks := 0
		for _, h := range recentHistory {
			if h.EndingHookUsed {
				recentHooks++
			}
		}
		if recentHooks >= 3 && metrics.EndingHookUsed {
			score += 0.08
		}
	}
	return round4(clamp(score, 0, 1))
}

func redFlags(metrics domain.ChapterAIVoiceMetrics, history []domain.ChapterAIVoiceMetrics) []domain.AIVoiceRedFlag {
	var flags []domain.AIVoiceRedFlag
	for _, hit := range metrics.AphorismHits {
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:        hit.Rule,
			Severity:    "error",
			Paragraph:   hit.Paragraph,
			Sentence:    hit.Sentence,
			Evidence:    hit.Text,
			Suggestion:  "删掉宣言式总结，改成角色被迫做一个小动作或说一句带遮掩的短话。",
			Replacement: "把判断藏到动作里：停顿、改口、避开视线，或让对方先误解一句。",
		})
	}
	if metrics.FigurativeDensity > FigurativeDensityLimit {
		severity := "warning"
		if metrics.FigurativeDensity >= 0.40 {
			severity = "error"
		}
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:       "figurative_density",
			Severity:   severity,
			Actual:     metrics.FigurativeDensity,
			Limit:      FigurativeDensityLimit,
			Suggestion: "保留最有剧情功能的一处比喻，其余改成物件、动作、环境后果。",
		})
	}
	dialogueLimit := dialogueRatioLimitForMetrics(metrics)
	if metrics.DialogueRatio < dialogueLimit && supportingDialogueInteractionSparse(metrics) {
		if !dialogueRatioNearMiss(metrics.DialogueRatio, dialogueLimit) {
			severity := "warning"
			if metrics.DialogueRatio < 0.04 {
				severity = "error"
			}
			flags = append(flags, domain.AIVoiceRedFlag{
				Rule:       "supporting_dialogue_ratio",
				Severity:   severity,
				Actual:     metrics.DialogueRatio,
				Limit:      dialogueLimit,
				Suggestion: "补一组配角主动误解、打断或拒绝的对话，让信息从冲突里出来。",
			})
		}
	}
	if !metrics.ProtagonistWaver {
		flags = append(flags, domain.AIVoiceRedFlag{
			Rule:       "protagonist_waver_missing",
			Severity:   "warning",
			Suggestion: "增加至少一处真实动摇：判断错误、动作迟疑、话说半句吞回去，随后付出小代价。",
		})
	}
	recentHistory := recentDistinctChapterMetrics(history, metrics.Chapter, 4)
	if len(recentHistory) > 0 {
		recentHooks := 0
		recentSameFunction := 0
		for _, h := range recentHistory {
			if h.EndingHookUsed {
				recentHooks++
			}
			if h.ChapterFunction == metrics.ChapterFunction {
				recentSameFunction++
			}
		}
		if metrics.EndingHookUsed && recentHooks >= 3 {
			flags = append(flags, domain.AIVoiceRedFlag{
				Rule:       "ending_hook_uniformity",
				Severity:   "warning",
				Suggestion: "本章章末改成动作余波或沉默留白，不再追加新谜面。",
			})
		}
		if recentSameFunction >= 2 {
			flags = append(flags, domain.AIVoiceRedFlag{
				Rule:       domain.AIVoiceChapterFunctionRepetitionRule,
				Severity:   "info",
				Suggestion: "若本章已有独立事件和结果，只记录为下一章换型建议，不返工本章；只有重复结构已损害本章阅读体验时，才按当前章原文证据修改。",
			})
		}
	}
	return flags
}

// recentDistinctChapterMetrics prevents repeated review/rewrite snapshots of
// one chapter from masquerading as several consecutive chapters. Only the
// latest metric for each completed chapter before the current chapter may
// influence cross-chapter repetition rules.
func recentDistinctChapterMetrics(history []domain.ChapterAIVoiceMetrics, currentChapter, limit int) []domain.ChapterAIVoiceMetrics {
	if currentChapter <= 0 || limit <= 0 || len(history) == 0 {
		return nil
	}
	seen := make(map[int]bool, limit)
	out := make([]domain.ChapterAIVoiceMetrics, 0, limit)
	for i := len(history) - 1; i >= 0 && len(out) < limit; i-- {
		metric := history[i]
		if metric.Chapter <= 0 || metric.Chapter >= currentChapter || seen[metric.Chapter] {
			continue
		}
		seen[metric.Chapter] = true
		out = append(out, metric)
	}
	return out
}

func dialogueRatioNearMiss(actual, limit float64) bool {
	return limit > 0 && actual > 0 && actual >= limit-dialogueRatioNearMissTolerance
}

// A terse confrontation can carry several real exchanges while using very few
// quoted characters. A low character ratio is therefore actionable only when
// dialogue topology independently shows sparse interaction as well.
func supportingDialogueInteractionSparse(metrics domain.ChapterAIVoiceMetrics) bool {
	return metrics.SupportingDialogueTurns < minimumSupportingDialogueTurns ||
		metrics.SupportingDialogueParagraphRatio < minimumSupportingDialogueParagraphRatio
}

func dialogueRatioLimitForMetrics(metrics domain.ChapterAIVoiceMetrics) float64 {
	return dialogueRatioLimitForSize(0, metrics.SentenceCount, metrics.ParagraphCount)
}

func dialogueRatioLimitForSize(totalChars, sentenceCount, paragraphCount int) float64 {
	if totalChars >= 3600 || sentenceCount >= 180 || paragraphCount >= 55 {
		return 0.10
	}
	return DialogueRatioLimit
}

func labelFor(metrics domain.ChapterAIVoiceMetrics, flags []domain.AIVoiceRedFlag) string {
	hasError := false
	hasActionableFlag := false
	for _, flag := range flags {
		if domain.IsAdvisoryAIVoiceFlag(flag) {
			continue
		}
		hasActionableFlag = true
		if flag.Severity == "error" {
			hasError = true
			break
		}
	}
	switch {
	case hasError || metrics.AIVoiceScore >= 0.55:
		return "❌ 需返工"
	case hasActionableFlag || metrics.AIVoiceScore >= 0.25:
		return "⚠️ 需打磨"
	default:
		return "✅ 可通过"
	}
}

func summaryFor(metrics domain.ChapterAIVoiceMetrics, flags []domain.AIVoiceRedFlag) string {
	actionable := 0
	advisory := 0
	for _, flag := range flags {
		if domain.IsAdvisoryAIVoiceFlag(flag) {
			advisory++
			continue
		}
		actionable++
	}
	if actionable == 0 {
		if advisory > 0 {
			return fmt.Sprintf("规则引擎未发现硬性 AI 腔红旗；记录 %d 项非阻断跨章规划建议。", advisory)
		}
		return "规则引擎未发现硬性 AI 腔红旗。"
	}
	return fmt.Sprintf("命中 %d 项红旗；比喻密度 %.2f，对话占比 %.2f，格言命中 %d。", actionable, metrics.FigurativeDensity, metrics.DialogueRatio, len(metrics.AphorismHits))
}

func ratio(n, d int) float64 {
	if d <= 0 {
		return 0
	}
	return round4(float64(n) / float64(d))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit]) + "..."
}
