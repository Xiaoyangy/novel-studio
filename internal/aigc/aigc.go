// Package aigc implements a deterministic local AIGC ratio scorer.
//
// It mirrors the local Python checker used by short_story_service and exposes
// four Zhuque-inspired dimensions: burstiness, perplexity proxy, structural
// fingerprint, and cross-paragraph consistency. It is a writing-quality signal,
// not a factual authorship verdict.
package aigc

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const Engine = "codex-local-aigc-v3"

var dimensionWeights = map[string]float64{
	"burstiness":                  0.30,
	"perplexity_proxy":            0.25,
	"structure_fingerprint":       0.25,
	"cross_paragraph_consistency": 0.20,
}

var finalBlendWeights = map[string]float64{
	"zhuque_four_dimensions": 0.10,
	"latest_detector_proxy":  0.85,
	"legacy_heuristic":       0.05,
}

var latestProxyWeights = map[string]float64{
	"probability_curvature_proxy":  0.28,
	"weak_lm_uniformity":           0.20,
	"local_entropy_uniformity":     0.17,
	"stylometry_readability":       0.07,
	"semantic_smoothing":           0.05,
	"semantic_perplexity":          0.08,
	"layout_humanizer_fingerprint": 0.05,
	"content_integrity":            0.10,
	"zhuque_segment_proxy":         0.00,
}

type Report struct {
	Engine                 string               `json:"engine"`
	LexiconVersion         string               `json:"lexicon_version,omitempty"`
	RealLM                 *RealLMResult        `json:"real_lm,omitempty"`
	AIGCValue              float64              `json:"aigc_value"`
	AIGCPercent            float64              `json:"aigc_percent"`
	AIRatioPercent         float64              `json:"ai_ratio_percent"`
	BlendedAIGCPercent     float64              `json:"blended_aigc_percent"`
	SegmentRiskFloor       float64              `json:"segment_risk_floor_percent"`
	WholeTextSegmentGate   float64              `json:"whole_text_single_segment_gate_percent,omitempty"`
	ContentIntegrityFloor  float64              `json:"content_integrity_floor_percent"`
	RiskLabel              string               `json:"risk_label"`
	Confidence             string               `json:"confidence"`
	ZhuqueCompositePercent float64              `json:"zhuque_composite_percent"`
	LatestDetectorProxy    DetectorProxy        `json:"latest_detector_proxy"`
	ZhuqueSegmentProxy     ZhuqueSegmentProxy   `json:"zhuque_segment_proxy"`
	LegacyHeuristicPercent float64              `json:"legacy_heuristic_percent"`
	FinalBlendWeights      map[string]float64   `json:"final_blend_weights"`
	HumanAnchorFinalCap    *float64             `json:"human_anchor_final_cap_percent,omitempty"`
	Dimensions             map[string]Dimension `json:"dimensions"`
	Stats                  Stats                `json:"stats"`
}

type DetectorProxy struct {
	CompositePercent float64              `json:"composite_percent"`
	Weights          map[string]float64   `json:"weights"`
	Note             string               `json:"note,omitempty"`
	Components       map[string]Dimension `json:"components"`
}

type Dimension struct {
	Name    string         `json:"name"`
	Score   float64        `json:"score"`
	Weight  float64        `json:"weight"`
	Stats   map[string]any `json:"stats,omitempty"`
	Signals []Signal       `json:"signals,omitempty"`
}

type Signal struct {
	Name     string  `json:"name"`
	Score    float64 `json:"score"`
	Evidence string  `json:"evidence"`
}

type ZhuqueSegmentProxy struct {
	Enabled                 bool            `json:"enabled"`
	Segments                []ZhuqueSegment `json:"segments"`
	SuspectedAIRatioPercent float64         `json:"suspected_ai_ratio_percent"`
	HumanRatioPercent       float64         `json:"human_ratio_percent"`
	AIFeatureRatioPercent   float64         `json:"ai_feature_ratio_percent"`
	MaxSegmentPercent       float64         `json:"max_segment_percent"`
	MaxSegmentIndex         int             `json:"max_segment_index"`
	RiskFloorPercent        float64         `json:"risk_floor_percent"`
	Note                    string          `json:"note,omitempty"`
}

type ZhuqueSegment struct {
	Index               int      `json:"index"`
	Start               int      `json:"start"`
	End                 int      `json:"end"`
	CharCount           int      `json:"char_count"`
	Proportion          float64  `json:"proportion"`
	AIGCPercent         float64  `json:"aigc_percent"`
	Category            string   `json:"category"`
	Evidence            []string `json:"evidence"`
	LocalAIGCPercent    float64  `json:"local_aigc_percent"`
	WeakLMScore         float64  `json:"weak_lm_score"`
	ConcreteDensityPerK float64  `json:"concrete_density_per_k"`
}

type Stats struct {
	Hanzi                  int            `json:"hanzi"`
	Sentences              int            `json:"sentences"`
	AvgSentenceLen         float64        `json:"avg_sentence_len"`
	SentenceStd            float64        `json:"sentence_std"`
	SentenceCV             float64        `json:"sentence_cv"`
	ParagraphCV            float64        `json:"paragraph_cv"`
	ShortSentenceRatio     float64        `json:"short_sentence_ratio"`
	DialogueRatio          float64        `json:"dialogue_ratio"`
	ClicheTotalPerK        float64        `json:"cliche_total_per_k"`
	ConcreteDensityPerK    float64        `json:"concrete_density_per_k"`
	ActionDensityPerK      float64        `json:"action_density_per_k"`
	SensoryDensityPerK     float64        `json:"sensory_density_per_k"`
	EmotionDensityPerK     float64        `json:"emotion_density_per_k"`
	AbstractDensityPerK    float64        `json:"abstract_density_per_k"`
	Repeated12Extra        int            `json:"repeated_12_extra"`
	TransitionDensityPerK  float64        `json:"transition_density_per_k"`
	SummaryDensityPerK     float64        `json:"summary_density_per_k"`
	CrossParagraphFeatures int            `json:"cross_paragraph_features"`
	AvgHanziPerParagraph   float64        `json:"avg_hanzi_per_paragraph"`
	MedianHanziPerPara     float64        `json:"median_hanzi_per_paragraph"`
	ShortParagraphRatio    float64        `json:"short_paragraph_ratio"`
	VeryShortParaRatio     float64        `json:"very_short_paragraph_ratio"`
	SingleSentenceParaRate float64        `json:"single_sentence_paragraph_ratio"`
	BracketLineRatio       float64        `json:"bracket_line_ratio"`
	DetectorNoise          map[string]any `json:"detector_noise,omitempty"`
	HumanAnchor            map[string]any `json:"human_anchor,omitempty"`
}

var cliches = map[string][]string{
	"时间锚点": {"那一刻", "那一瞬", "就在这时", "刹那间", "一瞬间", "顷刻间"},
	"微表情":  {"嘴角", "眼眸", "眼角", "眼底", "眸子", "勾起", "扬起一抹", "抿了抿"},
	"情绪命名": {"百感交集", "五味杂陈", "一种说不出", "复杂的情绪", "莫名的", "难以言喻", "心如刀绞", "痛不欲生"},
	"金句升华": {"原来", "或许", "也许就是", "所谓", "有些", "这就是", "人生就是", "真正的选择", "真正的答案", "最终的选择", "最终答案"},
	"陈词意象": {"月光如水", "时间仿佛凝固", "命运的齿轮", "如潮水般", "似乎", "仿佛", "宛如", "犹如"},
	"解释归纳": {"这让他意识到", "这让她意识到", "终于明白", "不再是", "而是", "不仅仅是", "更是", "这意味着", "换句话说"},
	"平滑转场": {"然而", "与此同时", "紧接着", "随后", "片刻后", "很快", "没过多久"},
	"工程泄漏": {"本章", "细纲", "大纲", "设定", "爽点", "情绪爆发", "关系升级", "伏笔回收", "节奏点"},
}

var (
	sentenceSplitRe     = regexp.MustCompile(`[。！？!?；;\n]+`)
	paragraphSplitRe    = regexp.MustCompile(`\n\s*\n+`)
	orderedMarkerRe     = regexp.MustCompile(`(?:首先|其次|再次|最后|总之|综上|换句话说|第一[，、点:]|第二[，、点:]|第三[，、点:])`)
	nonCJKRe            = regexp.MustCompile(`[^一-龥]`)
	soundNoiseRe        = regexp.MustCompile(`(?:[嗒咯叩沙咔啪滴哒哗啦停]{1,10}[，、,。；;]?){6,}`)
	cjkRunRe            = regexp.MustCompile(`[\x{4e00}-\x{9fff}]{24,}`)
	rareTermSoupRe      = regexp.MustCompile(`(?:魑魅魍魉|饕餮|螭吻|赑屃|狴犴|蒲牢|睚眦|狻猊|椒图|囚牛|貔貅|獬豸|鸱吻|蚣蝮|趴蝮){4,}`)
	asciiWordRe         = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_-]{1,}`)
	transitionMarkers   = []string{"然而", "与此同时", "随后", "紧接着", "片刻后", "很快", "没过多久", "于是", "因此"}
	summaryMarkers      = []string{"这让他意识到", "这让她意识到", "终于明白", "不仅仅是", "更是", "这意味着"}
	concreteHintMarkers = []string{"微信", "手机", "电梯", "地铁", "外卖", "快递", "钥匙", "门禁", "发票", "截图", "合同", "医院", "药", "咖啡", "便利店", "停车场", "工牌", "雨伞", "纸巾", "房间", "圆桌", "桌子", "餐桌", "椅子", "座钟", "钟表", "面具", "墙", "地板", "天花板", "白纸", "纸", "笔", "血迹", "尸体", "雨", "雨水", "泥", "门", "家门", "灯", "灯光", "剧院", "舞台", "射灯", "诊所", "口袋", "衣服", "戏袍", "黑发", "眼睛"}
	actionMarkers       = []string{"拿", "放", "推", "拉", "拽", "摁", "敲", "砸", "递", "接", "翻", "撕", "扔", "踢", "踩", "躲", "退", "停", "看", "笑", "哭", "骂", "问", "答", "咳", "抖"}
	sensoryMarkers      = []string{"冷", "热", "烫", "疼", "痒", "酸", "涩", "苦", "甜", "腥", "臭", "响", "哑", "湿", "黏", "硬", "软", "亮", "暗", "刺", "闷", "吵"}
	emotionMarkers      = []string{"紧张", "愤怒", "悲伤", "难过", "委屈", "害怕", "恐惧", "震惊", "惊讶", "复杂", "痛苦", "绝望", "崩溃", "开心", "喜悦", "温柔", "释然", "怅然", "茫然"}
	abstractMarkers     = []string{"意义", "命运", "人生", "灵魂", "内心", "情绪", "感觉", "关系", "成长", "救赎", "羁绊", "真相", "现实", "未来", "过去", "世界", "规则", "答案"}
	semanticActionMarks = []string{"拿", "放", "推", "拉", "拽", "摁", "敲", "砸", "递", "接", "翻", "撕", "扔", "踢", "踩", "躲", "退", "停", "看", "笑", "哭", "骂", "问", "咳", "抖", "走", "站", "坐", "伸", "按", "写", "签", "扫", "盯", "转", "开", "关", "锁", "摸", "擦"}
	semanticRuleMarkers = []string{"规则", "账单", "合同", "收据", "凭证", "审核", "交易", "权利", "债务", "代价", "边界", "标的", "名单", "门牌", "价签", "账户", "回执"}
	technicalMarkers    = []string{"系统", "算法", "模型", "实验", "数据", "检测", "识别", "跟踪", "控制", "优化", "实时", "计算", "阈值", "方差", "协方差", "序列", "图像", "像素", "轨迹", "预测", "摄像机", "相机", "目标", "运动", "干扰", "环境", "信噪比", "闭环", "响应", "延迟", "数据库", "平台", "处理", "分析", "PTZ", "SNR", "PC", "database", "tracking", "model", "control", "algorithm", "camera", "image", "sequence"}
	grammarChars        = "的一是不了在有和人这那中为上个我你他她它以要时来用们到于就对成会可也能下过说得着里把被给但并而或及其都还只又先再才没无"
	rareSoupChars       = "魑魅魍魉饕餮螭赑屃狴犴蒲牢睚眦狻猊椒图囚牛貔貅獬豸鸱吻蚣蝮趴"
)

func Analyze(text string) Report {
	return analyze(text, true)
}

// SingleDetectionSegmentMaxHanzi 是"读者会一次性整章丢进检测器"的字数上限。多数小说章节
// 2000-4000 字，读者复制整章检测时全章就是一个检测片段，看到的是 segment_risk_floor。
const SingleDetectionSegmentMaxHanzi = 5000

// EffectiveGatePercent 返回用于门禁判定的 AIGC 百分比：
//   - 内容完整性风险（脏码/绕检噪声）在场 → 直接用 AIGCPercent。
//   - 短章被朱雀式代理判为整章单段高风险 → 按 segment_risk_floor 判定；
//     这模拟读者把整章合成一段提交检测的场景，human anchor 不能覆盖它。
//   - 强人工叙事锚点已触发 final cap → 使用 human_anchor_final_cap_percent 对应的
//     AIGCPercent；报告仍保留原始 segment_risk_floor 供复核。
//   - 短章（≤5000 字，单检测片段）默认用 AIGCPercent（含 segment_risk_floor 真高），
//     不允许被多片段 blended 平均稀释；因为读者就是整章丢进检测器看这个分。
//   - 长章（>5000 字，能切成多个检测片段）且各 composite 都低 → 允许用 blended 降权，
//     避免长文里个别长尾片段把整章误判高。
//
// commit 机械门禁与章级审阅门禁共用此口径，保证两处判定一致。
func EffectiveGatePercent(report Report) float64 {
	if report.ContentIntegrityFloor > 0 {
		return report.AIGCPercent
	}
	if risk, ok := wholeTextSingleSegmentRisk(report); ok {
		return math.Max(report.AIGCPercent, risk)
	}
	if capValue, ok := HumanAnchorFinalCap(report); ok {
		if report.AIGCPercent > 0 && report.AIGCPercent < capValue {
			return report.AIGCPercent
		}
		return capValue
	}
	singleDetectionSegment := report.Stats.Hanzi <= SingleDetectionSegmentMaxHanzi
	if !singleDetectionSegment &&
		report.SegmentRiskFloor >= 70 &&
		report.BlendedAIGCPercent > 0 &&
		report.BlendedAIGCPercent < 25 &&
		report.LatestDetectorProxy.CompositePercent < 25 &&
		report.ZhuqueCompositePercent < 35 {
		return report.BlendedAIGCPercent
	}
	return report.AIGCPercent
}

func wholeTextSingleSegmentRisk(report Report) (float64, bool) {
	proxy := report.ZhuqueSegmentProxy
	if !proxy.Enabled || len(proxy.Segments) != 1 {
		return 0, false
	}
	segment := proxy.Segments[0]
	if segment.Proportion >= 0.95 && proxy.RiskFloorPercent >= 50 {
		return proxy.RiskFloorPercent, true
	}
	if segment.Proportion >= 0.95 && proxy.MaxSegmentPercent >= 50 {
		return proxy.MaxSegmentPercent, true
	}
	return 0, false
}

func HumanAnchorFinalCap(report Report) (float64, bool) {
	if report.ContentIntegrityFloor > 0 || report.HumanAnchorFinalCap == nil {
		return 0, false
	}
	capValue := *report.HumanAnchorFinalCap
	return capValue, capValue > 0
}

func analyze(text string, includeSegments bool) Report {
	body := stripMarkdownTitles(text)
	chars := hanzi(body)
	sents := splitSentences(body)
	sentLens := make([]float64, 0, len(sents))
	for _, sent := range sents {
		if n := len(hanzi(sent)); n > 0 {
			sentLens = append(sentLens, float64(n))
		}
	}
	paras := paragraphs(text)
	fragment := paragraphFragmentationStats(paras)
	paraLens := make([]float64, 0, len(paras))
	for _, para := range paras {
		if n := len(hanzi(para)); n > 0 {
			paraLens = append(paraLens, float64(n))
		}
	}

	nHanzi := len(chars)
	perK, totalCliche := clicheDensities(body, nHanzi)
	rep12Extra := repeatedExtra(chars, 12)
	concreteDensity := density(concreteCount(body), nHanzi)
	actionDensity := density(countAll(body, actionMarkers), nHanzi)
	sensoryDensity := density(countAll(body, sensoryMarkers), nHanzi)
	emotionDensity := density(countAll(body, emotionMarkers), nHanzi)
	abstractDensity := density(countAll(body, abstractMarkers), nHanzi)
	shortRatio := ratio(countWhere(sentLens, func(v float64) bool { return v <= 8 }), len(sentLens))
	dialogueRatio := dialogueRatio(body)
	transitionDensity := density(countAll(body, transitionMarkers), nHanzi)
	summaryDensity := density(countAll(body, summaryMarkers), nHanzi)
	noiseStats := detectorNoiseStats(body)

	stats := Stats{
		Hanzi:                  nHanzi,
		Sentences:              len(sentLens),
		AvgSentenceLen:         round1(mean(sentLens)),
		SentenceStd:            round3(stddev(sentLens)),
		SentenceCV:             round3(cv(sentLens)),
		ParagraphCV:            round3(cv(paraLens)),
		ShortSentenceRatio:     round3(shortRatio),
		DialogueRatio:          round3(dialogueRatio),
		ClicheTotalPerK:        totalCliche,
		ConcreteDensityPerK:    concreteDensity,
		ActionDensityPerK:      actionDensity,
		SensoryDensityPerK:     sensoryDensity,
		EmotionDensityPerK:     emotionDensity,
		AbstractDensityPerK:    abstractDensity,
		Repeated12Extra:        rep12Extra,
		TransitionDensityPerK:  transitionDensity,
		SummaryDensityPerK:     summaryDensity,
		CrossParagraphFeatures: len(paras),
		AvgHanziPerParagraph:   fragment["avg_hanzi_per_paragraph"],
		MedianHanziPerPara:     fragment["median_hanzi_per_paragraph"],
		ShortParagraphRatio:    fragment["short_paragraph_ratio"],
		VeryShortParaRatio:     fragment["very_short_paragraph_ratio"],
		SingleSentenceParaRate: fragment["single_sentence_paragraph_ratio"],
		BracketLineRatio:       fragment["bracket_line_ratio"],
		DetectorNoise:          noiseStats,
	}
	stats.HumanAnchor = humanAnchorStats(body, stats, sentLens, perK, noiseStats)

	dims := map[string]Dimension{
		"burstiness":                  scoreBurstiness(sentLens, paraLens, shortRatio, fragment),
		"perplexity_proxy":            scorePerplexityProxy(chars, totalCliche, perK, concreteDensity, rep12Extra),
		"structure_fingerprint":       scoreStructureFingerprint(body, paras, perK, transitionDensity, summaryDensity, fragment),
		"cross_paragraph_consistency": scoreCrossParagraphConsistency(paras),
	}
	zhuqueComposite := 0.0
	for key, dim := range dims {
		zhuqueComposite += dim.Score * dimensionWeights[key]
	}
	legacy := legacyHeuristicPercent(stats, perK)
	segmentProxy := emptyZhuqueSegmentProxy()
	if includeSegments {
		segmentProxy = zhuqueSegmentProxy(text)
	}
	latest := latestDetectorProxy(body, chars, sents, sentLens, perK, concreteDensity, stats, segmentProxy)
	blended := round2(
		zhuqueComposite*finalBlendWeights["zhuque_four_dimensions"] +
			latest.CompositePercent*finalBlendWeights["latest_detector_proxy"] +
			legacy*finalBlendWeights["legacy_heuristic"],
	)
	contentFloor := 0.0
	if intFromAny(noiseStats["semantic_noise_runs"]) > 0 {
		contentFloor = 82.0
		if floatFromAny(noiseStats["semantic_noise_ratio"]) >= 0.015 || intFromAny(noiseStats["semantic_noise_hanzi"]) >= 90 {
			contentFloor = 100.0
		}
	}
	var humanAnchorFinalCap *float64
	if contentFloor == 0 &&
		boolFromAny(stats.HumanAnchor["eligible"]) &&
		stringFromAny(stats.HumanAnchor["strength"]) == "strong" &&
		boolFromAny(stats.HumanAnchor["final_cap_allowed"]) &&
		floatFromAny(stats.HumanAnchor["score"]) >= 90 &&
		len(stringSliceFromAny(stats.HumanAnchor["blockers"])) == 0 &&
		totalCliche <= 7 &&
		rep12Extra == 0 {
		capValue := 4.8
		humanAnchorFinalCap = &capValue
		if blended > capValue {
			blended = capValue
		}
	}
	final := math.Max(math.Max(blended, segmentProxy.RiskFloorPercent), contentFloor)
	wholeTextGate, wholeTextHighRisk := wholeTextSingleSegmentRisk(Report{
		AIGCPercent:        final,
		SegmentRiskFloor:   segmentProxy.RiskFloorPercent,
		ZhuqueSegmentProxy: segmentProxy,
	})
	if humanAnchorFinalCap != nil && contentFloor == 0 && !wholeTextHighRisk {
		final = math.Max(blended, contentFloor)
	}
	return Report{
		Engine:                 Engine,
		LexiconVersion:         LexiconVersion(),
		RealLM:                 realLMResult(text),
		AIGCValue:              round4(final / 100),
		AIGCPercent:            final,
		AIRatioPercent:         final,
		BlendedAIGCPercent:     blended,
		SegmentRiskFloor:       segmentProxy.RiskFloorPercent,
		WholeTextSegmentGate:   wholeTextGate,
		ContentIntegrityFloor:  contentFloor,
		RiskLabel:              labelFor(final),
		Confidence:             confidenceFor(nHanzi, dims),
		ZhuqueCompositePercent: round2(zhuqueComposite),
		LatestDetectorProxy:    latest,
		ZhuqueSegmentProxy:     segmentProxy,
		LegacyHeuristicPercent: legacy,
		FinalBlendWeights:      finalBlendWeights,
		HumanAnchorFinalCap:    humanAnchorFinalCap,
		Dimensions:             dims,
		Stats:                  stats,
	}
}

func scoreBurstiness(sentLens, paraLens []float64, shortRatio float64, fragment map[string]float64) Dimension {
	signals := []Signal{}
	sentenceStd := stddev(sentLens)
	sentenceCV := cv(sentLens)
	paragraphCV := cv(paraLens)
	if len(sentLens) >= 8 {
		switch {
		case sentenceStd < 1.5:
			signals = append(signals, sig("sentence_std_lt_1_5", 100, "句长标准差 < 1.5，突发性高风险"))
		case sentenceStd < 3:
			signals = append(signals, sig("sentence_std_low", 65, "句长标准差偏低"))
		case sentenceCV < 0.45:
			signals = append(signals, sig("sentence_cv_low", 55, "句长 CV 偏低"))
		case sentenceCV < 0.65:
			signals = append(signals, sig("sentence_cv_mid", 18, "句长 CV 略低"))
		}
	}
	if len(paraLens) >= 6 {
		if paragraphCV < 0.38 {
			signals = append(signals, sig("paragraph_cv_low", 55, "段长 CV 过低"))
		} else if paragraphCV < 0.55 {
			signals = append(signals, sig("paragraph_cv_mid", 30, "段长 CV 偏低"))
		}
	}
	if len(sentLens) >= 20 && shortRatio < 0.08 {
		signals = append(signals, sig("short_sentence_ratio_low", 35, "短句比例偏低"))
	}
	if len(sentLens) >= 80 &&
		shortRatio >= 0.40 &&
		fragment["median_hanzi_per_paragraph"] <= 12 &&
		fragment["single_sentence_paragraph_ratio"] >= 0.62 {
		signals = append(signals, sig("over_staccato_humanizer", 70, "短句比例很高且大量单句短段，呈现刻意打碎的 humanizer/staccato 痕迹"))
	}
	return dim("突发性", "burstiness", map[string]any{
		"sentence_std": round3(sentenceStd), "sentence_cv": round3(sentenceCV), "paragraph_cv": round3(paragraphCV), "short_sentence_ratio": round3(shortRatio),
	}, signals)
}

func scorePerplexityProxy(chars []rune, totalCliche float64, perK map[string]float64, concreteDensity float64, rep12Extra int) Dimension {
	signals := []Signal{}
	entropyRatio := normalizedEntropy(chars)
	ttr := ratio(len(uniqueRunes(chars)), len(chars))
	if len(chars) >= 300 {
		if entropyRatio < 0.72 {
			signals = append(signals, sig("entropy_low", 62, "归一化字熵偏低，用词可预测"))
		} else if entropyRatio < 0.78 {
			signals = append(signals, sig("entropy_mid", 36, "归一化字熵略低"))
		}
		if ttr < ttrLowFloor(len(chars)) {
			signals = append(signals, sig("ttr_low", 48, "用字多样度偏低"))
		}
	}
	if totalCliche >= 8 {
		signals = append(signals, sig("cliche_total_high", 72, "套路措辞密度强烈偏高"))
	} else if totalCliche >= 4 {
		signals = append(signals, sig("cliche_total_mid", 45, "套路措辞密度偏高"))
	}
	if perK["情绪命名"] >= 1 || perK["陈词意象"] >= 1 {
		signals = append(signals, sig("safe_emotion_imagery", 40, "情绪命名/陈词意象偏高"))
	}
	if concreteDensity < 0.4 && len(chars) >= 800 {
		signals = append(signals, sig("concrete_low", 35, "具体物/数字密度偏低"))
	}
	if rep12Extra >= 3 {
		signals = append(signals, sig("long_ngram_repeat", math.Min(65, float64(rep12Extra)/12*100), "12 字级重复偏高"))
	}
	return dim("困惑度代理", "perplexity_proxy", map[string]any{
		"normalized_entropy": round3(entropyRatio), "ttr": round3(ttr), "cliche_total_per_k": totalCliche, "concrete_density_per_k": concreteDensity, "repeated_12_extra": rep12Extra,
	}, signals)
}

func ttrLowFloor(charCount int) float64 {
	switch {
	case charCount >= 3200:
		return 0.22
	case charCount >= 2800:
		return 0.23
	case charCount >= 2400:
		return 0.24
	case charCount >= 1400:
		return 0.26
	default:
		return 0.28
	}
}

func scoreStructureFingerprint(body string, paras []string, perK map[string]float64, transitionDensity, summaryDensity float64, fragment map[string]float64) Dimension {
	signals := []Signal{}
	markerCount := len(orderedMarkerRe.FindAllString(body, -1))
	repeatedStarts := repeatedParagraphStarts(paras)
	sentenceCounts := make([]float64, 0, len(paras))
	for _, para := range paras {
		sentenceCounts = append(sentenceCounts, float64(len(splitSentences(para))))
	}
	sentenceCountCV := cv(sentenceCounts)
	if markerCount >= 2 {
		signals = append(signals, sig("ordered_markers", math.Min(100, float64(markerCount)/4*100), "首先/其次/最后等结构标记偏多"))
	}
	if transitionDensity >= 2 {
		signals = append(signals, sig("transition_density_high", 65, "机械转场密度偏高"))
	} else if transitionDensity >= 1 {
		signals = append(signals, sig("transition_density_mid", 35, "机械转场密度略高"))
	}
	if summaryDensity >= 1 {
		signals = append(signals, sig("summary_density", 62, "解释归纳标记偏高"))
	} else if perK["解释归纳"] >= 0.8 {
		signals = append(signals, sig("summary_category", 48, "解释归纳类密度偏高"))
	}
	if repeatedStarts >= 3 {
		signals = append(signals, sig("paragraph_start_repeat", 42, "段首重复偏多"))
	}
	if len(sentenceCounts) >= 8 && sentenceCountCV < 0.35 {
		signals = append(signals, sig("paragraph_sentence_shape", 35, "每段句数过于同构"))
	}
	if fragment["paragraph_count"] >= 70 &&
		fragment["median_hanzi_per_paragraph"] <= 12 &&
		fragment["single_sentence_paragraph_ratio"] >= 0.62 {
		signals = append(signals, sig("fragmented_single_sentence_paragraphs", 86, "单句短段密度过高，像后期反检测式碎段"))
	} else if fragment["paragraph_count"] >= 40 && fragment["single_sentence_paragraph_ratio"] >= 0.45 {
		// 中档：大量单句成段（一句话独立成段当节奏）但未到极端碎段——检测器偏爱的
		// "戏剧性一行"招牌，需在审阅里surface以驱动重写把多数并进连贯段落。
		signals = append(signals, sig("single_sentence_paragraphs_elevated", 44, "单句成段占比偏高，戏剧性一行段用得过多，节奏偏碎"))
	}
	if fragment["very_short_paragraph_ratio"] >= 0.28 && fragment["paragraph_count"] >= 70 {
		signals = append(signals, sig("very_short_paragraph_overuse", 58, "6字以内短段占比过高"))
	}
	if fragment["bracket_line_ratio"] >= 0.10 && fragment["paragraph_count"] >= 60 {
		signals = append(signals, sig("contract_block_density", 48, "独立条款/账单块占比偏高，过度依赖格式化规则块"))
	}
	return dim("结构指纹", "structure_fingerprint", map[string]any{
		"ordered_marker_count": markerCount, "transition_density_per_k": transitionDensity, "summary_density_per_k": summaryDensity, "repeated_paragraph_starts": repeatedStarts, "paragraph_sentence_count_cv": round3(sentenceCountCV),
		"paragraph_fragmentation": fragment,
	}, signals)
}

func scoreCrossParagraphConsistency(paras []string) Dimension {
	rows := paragraphRows(paras)
	if len(rows) < 6 {
		return Dimension{Name: "跨段一致性", Score: 0, Weight: dimensionWeights["cross_paragraph_consistency"], Stats: map[string]any{"paragraph_feature_count": len(rows)}}
	}
	signals := []Signal{}
	lengthCV := cv(rowValues(rows, "hanzi"))
	avgSentenceCV := cv(rowValues(rows, "avg_sentence_len"))
	sentenceCVStd := stddev(rowValues(rows, "sentence_cv"))
	commaStd := stddev(rowValues(rows, "comma_period_ratio"))
	similarityAvg, similarityStd, similarityHighRatio := paragraphSimilarityStats(rows)
	if lengthCV < 0.35 {
		signals = append(signals, sig("paragraph_length_uniform", 58, "段落长度过于一致"))
	} else if lengthCV < 0.55 {
		signals = append(signals, sig("paragraph_length_mid", 30, "段落长度变化偏弱"))
	}
	if avgSentenceCV < 0.28 {
		signals = append(signals, sig("avg_sentence_uniform", 45, "各段平均句长过于一致"))
	}
	if sentenceCVStd < 0.12 {
		signals = append(signals, sig("sentence_rhythm_uniform", 38, "各段节奏变化少"))
	}
	if commaStd < 0.45 {
		signals = append(signals, sig("punctuation_uniform", 26, "标点习惯跨段过稳"))
	}
	if similarityHighRatio >= 0.65 && similarityStd <= 0.08 {
		signals = append(signals, sig("paragraph_vector_uniform", 46, "相邻段落风格向量高相似，段落功能过于同质"))
	} else if similarityAvg >= 0.94 && similarityStd <= 0.06 {
		signals = append(signals, sig("paragraph_vector_mid", 28, "相邻段落风格向量变化偏弱"))
	}
	return dim("跨段一致性", "cross_paragraph_consistency", map[string]any{
		"paragraph_feature_count": len(rows), "paragraph_length_cv": round3(lengthCV), "avg_sentence_len_cv_across_paragraphs": round3(avgSentenceCV), "sentence_cv_std_across_paragraphs": round3(sentenceCVStd), "comma_ratio_std": round3(commaStd), "adjacent_similarity_avg": round3(similarityAvg), "adjacent_similarity_std": round3(similarityStd), "adjacent_similarity_high_ratio": round3(similarityHighRatio),
	}, signals)
}

func latestDetectorProxy(body string, chars []rune, sents []string, sentLens []float64, perK map[string]float64, concreteDensity float64, stats Stats, segmentProxy ZhuqueSegmentProxy) DetectorProxy {
	curveBody := normalizeDetectorCurveText(body)
	surprisal := localSurprisalStats(splitSentences(curveBody))
	entropy := windowEntropyStats(hanzi(curveBody))
	components := map[string]Dimension{
		"probability_curvature_proxy":  scoreProbabilityCurvatureProxy(surprisal, entropy),
		"weak_lm_uniformity":           scoreWeakLMUniformity(surprisal),
		"local_entropy_uniformity":     scoreLocalEntropyUniformity(entropy),
		"stylometry_readability":       scoreStylometryReadability(sentLens, stats),
		"semantic_smoothing":           scoreSemanticSmoothing(stats, perK, concreteDensity),
		"semantic_perplexity":          scoreSemanticPerplexity(body, stats),
		"layout_humanizer_fingerprint": scoreLayoutHumanizerFingerprint(stats),
		"content_integrity":            scoreContentIntegrity(stats.DetectorNoise),
		"zhuque_segment_proxy":         scoreZhuqueSegmentProxy(segmentProxy),
	}
	components = applyHumanAnchorCalibration(components, stats.HumanAnchor)
	composite := 0.0
	for key, component := range components {
		composite += component.Score * latestProxyWeights[key]
	}
	return DetectorProxy{
		CompositePercent: round2(composite),
		Weights:          latestProxyWeights,
		Note:             "近年检测器常把句级分类、弱模型概率/熵、概率曲率、风格计量、语义平滑、语意困惑度和句段级布局指纹融合；本地用可复算代理特征近似，不调用外部模型。拟声词/重复声响和无语义脏码会先中和后再计算曲线，且脏码长串会进入内容完整性风险。若文本同时命中高质量人工样本锚点，会对曲线类特征做误判降权，但不覆盖内容完整性、真重复和空泛概括风险。",
		Components:       components,
	}
}

func emptyZhuqueSegmentProxy() ZhuqueSegmentProxy {
	return ZhuqueSegmentProxy{}
}

func zhuqueLikeSegmentBounds(total int) [][2]int {
	if total <= 0 {
		return nil
	}
	if total < 800 {
		return [][2]int{{0, total}}
	}
	if total >= 1800 && total <= 3600 {
		return [][2]int{{0, total}}
	}
	bounds := [][2]int{}
	start := 0
	window := 640
	for start < total {
		end := minInt(total, start+window)
		if total-end < 360 && len(bounds) > 0 {
			bounds[len(bounds)-1][1] = total
			break
		}
		bounds = append(bounds, [2]int{start, end})
		start = end
	}
	return bounds
}

func zhuqueSegmentProxy(raw string) ZhuqueSegmentProxy {
	body := stripMarkdownTitles(raw)
	visible := make([]rune, 0, len(body))
	for _, r := range body {
		if !unicode.IsSpace(r) {
			visible = append(visible, r)
		}
	}
	bounds := zhuqueLikeSegmentBounds(len(visible))
	proxy := ZhuqueSegmentProxy{Enabled: true, Note: "按朱雀报告校准的本地分片代理：输出疑似AI占比和最高风险片段，用于防止全文平均稀释长尾高风险。"}
	if len(bounds) == 0 {
		return proxy
	}
	suspectedChars := 0
	aiChars := 0
	for index, bound := range bounds {
		start, end := bound[0], bound[1]
		if end <= start {
			continue
		}
		chunk := string(visible[start:end])
		report := analyze(chunk, false)
		proportion := ratio(end-start, len(visible))
		score, evidence := segmentAIGCProxy(report, end-start, proportion)
		category := "人工特征"
		if score >= 99 {
			category = "AI特征"
			aiChars += end - start
		} else if score >= 50 {
			category = "疑似AI"
			suspectedChars += end - start
		}
		if score > proxy.MaxSegmentPercent {
			proxy.MaxSegmentPercent = score
			proxy.MaxSegmentIndex = index + 1
		}
		weakScore := 0.0
		if dim, ok := report.LatestDetectorProxy.Components["weak_lm_uniformity"]; ok {
			weakScore = dim.Score
		}
		proxy.Segments = append(proxy.Segments, ZhuqueSegment{
			Index:               index + 1,
			Start:               start,
			End:                 end,
			CharCount:           end - start,
			Proportion:          round4(proportion),
			AIGCPercent:         score,
			Category:            category,
			Evidence:            evidence,
			LocalAIGCPercent:    report.AIGCPercent,
			WeakLMScore:         weakScore,
			ConcreteDensityPerK: report.Stats.ConcreteDensityPerK,
		})
	}
	total := len(visible)
	proxy.SuspectedAIRatioPercent = round2(float64(suspectedChars) / float64(maxInt(total, 1)) * 100)
	proxy.AIFeatureRatioPercent = round2(float64(aiChars) / float64(maxInt(total, 1)) * 100)
	proxy.HumanRatioPercent = round2(100 - proxy.SuspectedAIRatioPercent - proxy.AIFeatureRatioPercent)
	riskRatio := round2(proxy.SuspectedAIRatioPercent + proxy.AIFeatureRatioPercent)
	if len(proxy.Segments) == 1 && riskRatio >= 99 && proxy.MaxSegmentPercent >= 50 {
		proxy.RiskFloorPercent = proxy.MaxSegmentPercent
	} else if proxy.MaxSegmentPercent >= 80 && riskRatio >= 60 {
		proxy.RiskFloorPercent = round2(riskRatio * 0.90)
	} else if proxy.MaxSegmentPercent >= 60 && riskRatio >= 35 {
		proxy.RiskFloorPercent = round2(riskRatio * 0.70)
	}
	return proxy
}

func rawDetectorScore(item Dimension) float64 {
	raw := item.Score
	if item.Stats != nil {
		raw = math.Max(raw, floatFromAny(item.Stats["human_anchor_adjusted_from"]))
	}
	return raw
}

func segmentAIGCProxy(report Report, charCount int, proportion float64) (float64, []string) {
	latest := report.LatestDetectorProxy.Components
	weak := latest["weak_lm_uniformity"].Score
	probabilityCurve := latest["probability_curvature_proxy"].Score
	entropy := latest["local_entropy_uniformity"].Score
	rawWeak := rawDetectorScore(latest["weak_lm_uniformity"])
	rawProbabilityCurve := rawDetectorScore(latest["probability_curvature_proxy"])
	rawEntropy := rawDetectorScore(latest["local_entropy_uniformity"])
	layout := latest["layout_humanizer_fingerprint"].Score
	concrete := report.Stats.ConcreteDensityPerK
	action := report.Stats.ActionDensityPerK
	ttr := 1.0
	normalizedEntropy := 1.0
	if dim, ok := report.Dimensions["perplexity_proxy"]; ok && dim.Stats != nil {
		if value, ok := dim.Stats["ttr"].(float64); ok {
			ttr = value
		}
		if value, ok := dim.Stats["normalized_entropy"].(float64); ok {
			normalizedEntropy = value
		}
	}
	score := math.Max(math.Max(probabilityCurve, entropy), layout)
	evidence := []string{}
	if weak >= 90 && concrete < 45 && charCount <= 700 {
		score = math.Max(score, 62)
		evidence = append(evidence, "短片段弱模型曲线过稳且具体密度偏低")
	} else if weak >= 90 && concrete >= 45 && action >= 18 && charCount <= 700 {
		score = math.Max(score, 42)
		evidence = append(evidence, "弱模型曲线过稳，但具体物/动作密度较高，按人工锚点降权")
	}
	if weak >= 80 && proportion >= 0.50 && concrete < 45 && ttr <= 0.42 {
		score = math.Max(score, 86)
		evidence = append(evidence, "长尾片段占比高，弱模型曲线过稳，TTR和具体密度偏低")
	} else if weak >= 80 && concrete < 45 {
		score = math.Max(score, weak*0.65)
		evidence = append(evidence, "弱模型曲线过稳且具体密度偏低")
	}
	if normalizedEntropy < 0.93 && weak >= 70 {
		score = math.Max(score, 72)
		evidence = append(evidence, "片段归一化熵偏低，且弱模型曲线过稳")
	}
	anchor := report.Stats.HumanAnchor
	anchorType := stringFromAny(anchor["anchor_type"])
	blockers := stringSliceFromAny(anchor["blockers"])
	narrativeLike := anchorType == "narrative_scene" || report.Stats.DialogueRatio >= 0.10 || report.Stats.ActionDensityPerK+report.Stats.SensoryDensityPerK >= 15
	if charCount >= 1800 && proportion >= 0.95 && narrativeLike && anchorType != "technical_expository" {
		rawCurve := math.Max(math.Max(rawProbabilityCurve, rawEntropy), rawWeak)
		currentCurve := math.Max(math.Max(probabilityCurve, entropy), weak)
		dimScore := func(key string) float64 {
			if dim, ok := report.Dimensions[key]; ok {
				return dim.Score
			}
			return 0
		}
		burstiness := dimScore("burstiness")
		structure := dimScore("structure_fingerprint")
		crossParagraph := dimScore("cross_paragraph_consistency")
		strongAnchor := boolFromAny(anchor["eligible"]) &&
			stringFromAny(anchor["strength"]) == "strong" &&
			len(blockers) == 0
		multiSignalSupport := currentCurve >= 60 ||
			report.ZhuqueCompositePercent >= 38 ||
			(burstiness >= 35 && (structure >= 45 || crossParagraph >= 30)) ||
			(structure >= 65 && concrete < 18 && report.Stats.DialogueRatio < 0.35) ||
			(!strongAnchor && rawCurve >= 90)
		if rawCurve >= 80 && multiSignalSupport {
			externalLikeScore := math.Min(86, math.Max(76, 62+rawCurve*0.20))
			if externalLikeScore > score {
				score = externalLikeScore
				evidence = append(evidence, "整章单段疑似朱雀形态：曲线原始高值偏高且存在当前曲线/结构节奏复合支撑")
			}
		} else if rawCurve >= 80 {
			evidence = append(evidence, "整章单段原始曲线偏高，但当前曲线、人味锚点和结构节奏未形成复合高风险")
		}
	}
	lengthOnlyBlocker := len(blockers) > 0
	for _, blocker := range blockers {
		if !strings.HasPrefix(blocker, "文本长度不足") {
			lengthOnlyBlocker = false
			break
		}
	}
	anchorScore := floatFromAny(anchor["score"])
	segmentAnchorEligible := boolFromAny(anchor["eligible"]) || (lengthOnlyBlocker && charCount >= 420 && anchorScore >= 52)
	if segmentAnchorEligible {
		capScore := 100.0
		if boolFromAny(anchor["eligible"]) {
			capScore = floatFromAny(anchor["segment_cap"])
			if capScore == 0 {
				capScore = 100
			}
		} else if anchorScore >= 72 {
			capScore = 34
		} else {
			capScore = 48
		}
		if score > capScore {
			score = capScore
			evidence = append(evidence, "片段具备高质人工锚点，按正样本校准降权")
		}
	}
	return round2(score), evidence
}

func scoreZhuqueSegmentProxy(proxy ZhuqueSegmentProxy) Dimension {
	signals := []Signal{}
	riskRatio := round2(proxy.SuspectedAIRatioPercent + proxy.AIFeatureRatioPercent)
	if riskRatio >= 60 && proxy.MaxSegmentPercent >= 80 {
		signals = append(signals, sig("zhuque_like_suspected_span_ratio_high", riskRatio, "疑似/AI特征片段占比和最高风险片段偏高"))
	} else if riskRatio >= 35 || proxy.MaxSegmentPercent >= 70 {
		signals = append(signals, sig("zhuque_like_suspected_span_ratio_mid", math.Max(riskRatio, proxy.MaxSegmentPercent), "出现中高风险分片"))
	}
	return dim("朱雀式分片代理", "zhuque_segment_proxy", map[string]any{
		"suspected_ai_ratio_percent": proxy.SuspectedAIRatioPercent,
		"human_ratio_percent":        proxy.HumanRatioPercent,
		"ai_feature_ratio_percent":   proxy.AIFeatureRatioPercent,
		"max_segment_percent":        proxy.MaxSegmentPercent,
		"max_segment_index":          proxy.MaxSegmentIndex,
		"risk_floor_percent":         proxy.RiskFloorPercent,
		"segments":                   proxy.Segments,
	}, signals)
}

func scoreProbabilityCurvatureProxy(surprisal, entropy map[string]any) Dimension {
	signals := []Signal{}
	sentenceCount := surprisal["sentence_count"].(int)
	windowCount := entropy["window_count"].(int)
	bigramCV := surprisal["bigram_surprisal_cv"].(float64)
	unigramCV := surprisal["unigram_surprisal_cv"].(float64)
	entropyStd := entropy["normalized_entropy_std"].(float64)
	entropyCV := entropy["normalized_entropy_cv"].(float64)
	ttrCV := entropy["ttr_cv"].(float64)
	if sentenceCount >= 80 &&
		windowCount >= 10 &&
		bigramCV > 0 &&
		bigramCV < 0.032 &&
		unigramCV < 0.055 &&
		entropyStd < 0.010 &&
		entropyCV < 0.012 {
		signals = append(signals, sig("fast_detectgpt_curve_proxy_high", 98, "句级概率曲线与滑窗熵曲线同时过平"))
	} else if sentenceCount >= 60 &&
		windowCount >= 8 &&
		bigramCV > 0 &&
		bigramCV < 0.040 &&
		entropyStd < 0.014 &&
		ttrCV <= 0.080 {
		signals = append(signals, sig("detectgpt_curve_proxy_mid_high", 84, "弱模型惊讶度曲线和局部多样度曲线同步偏平"))
	}
	if sentenceCount >= 80 && bigramCV > 0 && bigramCV < 0.030 && entropyStd < 0.008 {
		signals = append(signals, sig("sentence_classifier_consensus_flat", 88, "大量句子的局部概率形态高度一致，符合句级分类器高一致性风险"))
	}
	return dim("概率曲率/句级分类代理", "probability_curvature_proxy", map[string]any{
		"sentence_count":         sentenceCount,
		"window_count":           windowCount,
		"bigram_surprisal_cv":    bigramCV,
		"unigram_surprisal_cv":   unigramCV,
		"normalized_entropy_std": entropyStd,
		"normalized_entropy_cv":  entropyCV,
		"ttr_cv":                 ttrCV,
	}, signals)
}

func scoreWeakLMUniformity(stats map[string]any) Dimension {
	signals := []Signal{}
	count := stats["sentence_count"].(int)
	bigramCV := stats["bigram_surprisal_cv"].(float64)
	bigramAvg := stats["bigram_surprisal_avg"].(float64)
	unigramCV := stats["unigram_surprisal_cv"].(float64)
	if count >= 20 {
		if bigramCV > 0 && bigramCV < 0.030 && unigramCV < 0.055 {
			signals = append(signals, sig("bigram_unigram_curve_too_flat", 96, "句级 bigram/unigram 惊讶度曲线异常平滑"))
		} else if bigramCV > 0 && bigramCV < 0.035 && unigramCV < 0.060 {
			signals = append(signals, sig("bigram_curve_flat_high", 82, "句级 bigram 惊讶度跨句概率曲线过于稳定"))
		} else if bigramCV > 0 && bigramCV < 0.055 && (bigramAvg < 7.25 || unigramCV < 0.045) {
			signals = append(signals, sig("bigram_surprisal_cv_low", 58, "句级 bigram 惊讶度过稳且均值偏低"))
		} else if bigramCV < 0.070 && (bigramAvg < 7.45 || unigramCV < 0.050) {
			signals = append(signals, sig("bigram_surprisal_cv_mid", 35, "句级 bigram 惊讶度曲线偏平且均值偏低"))
		}
		if unigramCV > 0 && unigramCV < 0.040 {
			signals = append(signals, sig("unigram_surprisal_cv_low", 30, "句级 unigram 惊讶度过稳"))
		}
	}
	return dim("弱语言模型一致性", "weak_lm_uniformity", stats, signals)
}

func scoreLocalEntropyUniformity(stats map[string]any) Dimension {
	signals := []Signal{}
	count := stats["window_count"].(int)
	entropyStd := stats["normalized_entropy_std"].(float64)
	entropyAvg := stats["normalized_entropy_avg"].(float64)
	entropyCV := stats["normalized_entropy_cv"].(float64)
	ttrAvg := stats["ttr_avg"].(float64)
	ttrCV := stats["ttr_cv"].(float64)
	if count >= 4 {
		lowDiversity := entropyAvg < 0.955 || ttrAvg < 0.620
		if count >= 8 && entropyStd < 0.010 && entropyCV < 0.012 && ttrCV < 0.070 {
			signals = append(signals, sig("window_entropy_signature_flat", 90, "滑窗熵和局部随机性曲线异常稳定"))
		} else if count >= 8 && entropyStd < 0.016 && ttrCV <= 0.080 {
			signals = append(signals, sig("window_entropy_signature_mid", 68, "滑窗熵波动偏低"))
		} else if entropyStd < 0.018 && lowDiversity {
			signals = append(signals, sig("window_entropy_std_low", 50, "滑窗熵过稳且局部多样度偏低"))
		} else if entropyStd < 0.028 && lowDiversity {
			signals = append(signals, sig("window_entropy_std_mid", 30, "滑窗熵变化偏小且多样度不足"))
		}
		if ttrCV < 0.080 && ttrAvg < 0.620 {
			signals = append(signals, sig("window_ttr_cv_low", 35, "滑窗 TTR 跨段过稳"))
		}
	}
	return dim("局部熵/TTR波动", "local_entropy_uniformity", stats, signals)
}

func scoreStylometryReadability(sentLens []float64, stats Stats) Dimension {
	signals := []Signal{}
	punctuationKinds := 0
	if stats.Sentences > 0 {
		punctuationKinds = 2
	}
	if len(sentLens) >= 20 {
		m := mean(sentLens)
		mid := 0
		long := 0
		shortBand := 0
		midShortBand := 0
		midLongBand := 0
		longBand := 0
		for _, length := range sentLens {
			if length >= m*0.65 && length <= m*1.35 {
				mid++
			}
			if length >= m*1.8 {
				long++
			}
			switch {
			case length <= 8:
				shortBand++
			case length <= 16:
				midShortBand++
			case length <= 28:
				midLongBand++
			default:
				longBand++
			}
		}
		midRatio := ratio(mid, len(sentLens))
		longRatio := ratio(long, len(sentLens))
		bins := []float64{
			ratio(shortBand, len(sentLens)),
			ratio(midShortBand, len(sentLens)),
			ratio(midLongBand, len(sentLens)),
			ratio(longBand, len(sentLens)),
		}
		minBin := bins[0]
		maxBin := bins[0]
		for _, value := range bins[1:] {
			if value < minBin {
				minBin = value
			}
			if value > maxBin {
				maxBin = value
			}
		}
		sentenceCV := cv(sentLens)
		shortRatio := ratio(shortBand, len(sentLens))
		if len(sentLens) >= 80 &&
			minBin >= 0.18 &&
			maxBin <= 0.32 &&
			sentenceCV >= 0.55 &&
			sentenceCV <= 0.75 &&
			shortRatio >= 0.18 &&
			shortRatio <= 0.32 &&
			stats.DialogueRatio >= 0.16 &&
			stats.DialogueRatio <= 0.35 {
			signals = append(signals, sig("over_balanced_sentence_band_distribution", 45, "短/中/长句分布过于均衡"))
		}
		if midRatio >= 0.72 && longRatio < 0.06 {
			signals = append(signals, sig("sentence_length_centered", 45, "中位句长带占比高，极长/极短句不足"))
		} else if midRatio >= 0.62 {
			signals = append(signals, sig("sentence_length_centered_mid", 25, "句式分布偏集中"))
		}
	}
	if stats.Hanzi >= 1200 && stats.DialogueRatio < 0.08 && punctuationKinds <= 2 {
		signals = append(signals, sig("punctuation_palette_thin", 28, "标点种类少且对话比例低"))
	}
	return dim("风格计量/可读性", "stylometry_readability", map[string]any{
		"punctuation_kinds": punctuationKinds, "dialogue_ratio": stats.DialogueRatio,
	}, signals)
}

func scoreSemanticSmoothing(stats Stats, perK map[string]float64, concreteDensity float64) Dimension {
	signals := []Signal{}
	sceneDensity := concreteDensity + stats.ActionDensityPerK + stats.SensoryDensityPerK
	if stats.Hanzi >= 800 && stats.AbstractDensityPerK >= 5 && sceneDensity < 8 {
		signals = append(signals, sig("abstract_high_scene_low", 52, "抽象词偏高，场景动作感官密度偏低"))
	} else if stats.Hanzi >= 800 && stats.AbstractDensityPerK >= 3.5 && sceneDensity < 10 {
		signals = append(signals, sig("abstract_scene_mid", 32, "抽象概括偏高且场景锚点偏少"))
	}
	if stats.EmotionDensityPerK >= 2 && stats.ActionDensityPerK < 8 {
		signals = append(signals, sig("emotion_named_action_low", 42, "情绪命名偏高且动作密度偏低"))
	}
	if perK["解释归纳"] >= 0.8 && sceneDensity < 12 {
		signals = append(signals, sig("summary_without_scene", 40, "解释归纳腔偏高但具体动作/物件承载不足"))
	}
	return dim("语义平滑/概括腔", "semantic_smoothing", map[string]any{
		"abstract_density_per_k": stats.AbstractDensityPerK, "action_density_per_k": stats.ActionDensityPerK, "sensory_density_per_k": stats.SensoryDensityPerK, "emotion_density_per_k": stats.EmotionDensityPerK, "scene_density_per_k": round2(sceneDensity),
	}, signals)
}

func scoreSemanticPerplexity(body string, stats Stats) Dimension {
	profiles := semanticSentenceProfiles(splitSentences(body))
	stat := semanticPerplexityStats(profiles)
	signals := []Signal{}
	sentenceCount := intFromAny(stat["sentence_count"])
	signatureKinds := intFromAny(stat["signature_kinds"])
	dominantRatio := floatFromAny(stat["dominant_signature_ratio"])
	maxRun := intFromAny(stat["same_signature_run_max"])
	sceneRatio := floatFromAny(stat["scene_signature_ratio"])
	abstractOnlyRatio := floatFromAny(stat["abstract_only_ratio"])
	turnRatio := floatFromAny(stat["semantic_turn_ratio"])
	avgRoles := floatFromAny(stat["avg_role_count"])
	if sentenceCount >= 12 {
		if signatureKinds <= 2 && dominantRatio >= 0.55 {
			signals = append(signals, sig("semantic_signature_low", 64, "语义角色签名种类过少，句子功能过于好猜"))
		} else if signatureKinds <= 3 && dominantRatio >= 0.62 {
			signals = append(signals, sig("semantic_signature_mid", 46, "主导语义功能占比偏高"))
		}
		if maxRun >= 5 {
			signals = append(signals, sig("semantic_run_flat", 58, "连续多句承担同一语义功能"))
		} else if maxRun >= 4 {
			signals = append(signals, sig("semantic_run_mid", 38, "同类语义句连续出现"))
		}
		if turnRatio < 0.35 {
			signals = append(signals, sig("semantic_turn_low", 44, "句间语义转折/功能切换偏少"))
		}
	}
	if sentenceCount >= 10 && abstractOnlyRatio >= 0.30 && sceneRatio < 0.45 {
		signals = append(signals, sig("abstract_branching_low", 56, "抽象判断句比例高，动作/物件/感官/对话分支不足"))
	}
	if sentenceCount >= 10 && avgRoles < 1.25 && sceneRatio < 0.35 {
		signals = append(signals, sig("semantic_branching_thin", 42, "单句语义承载维度偏薄"))
	}
	stat["hanzi"] = stats.Hanzi
	return dim("语意困惑度", "semantic_perplexity", stat, signals)
}

func scoreContentIntegrity(noise map[string]any) Dimension {
	signals := []Signal{}
	semanticRuns := intFromAny(noise["semantic_noise_runs"])
	semanticHanzi := intFromAny(noise["semantic_noise_hanzi"])
	semanticRatio := floatFromAny(noise["semantic_noise_ratio"])
	if semanticRuns > 0 {
		score := 82.0
		if semanticRatio >= 0.015 || semanticHanzi >= 90 {
			score = 100.0
		}
		signals = append(signals, sig(
			"semantic_noise_char_soup",
			score,
			"出现无语义长串/脏码，疑似为绕检噪声而非剧情内容",
		))
	} else if soundRatio := floatFromAny(noise["sound_noise_ratio"]); soundRatio >= 0.03 {
		signals = append(signals, sig(
			"sound_noise_overuse",
			42,
			"密集拟声/重复声响占比偏高，需确认是否服务剧情而非曲线扰动",
		))
	}
	return dim("内容完整性/绕检噪声", "content_integrity", noise, signals)
}

func technicalExpositoryAnchorStats(body string, stats Stats, sentLens []float64, noise map[string]any) map[string]any {
	techDensity := density(countAll(body, technicalMarkers), stats.Hanzi)
	asciiDensity := round2(float64(len(asciiWordRe.FindAllString(body, -1))) / float64(maxInt(stats.Hanzi, 1)) * 1000)
	paragraphCount := stats.CrossParagraphFeatures
	blockers := []string{}
	if stats.Hanzi < 320 {
		blockers = append(blockers, "技术说明文人工锚点长度不足 320 汉字")
	}
	if intFromAny(noise["semantic_noise_runs"]) > 0 {
		blockers = append(blockers, "存在无语义脏码/字符汤")
	}
	if floatFromAny(noise["sound_noise_ratio"]) >= 0.03 {
		blockers = append(blockers, "密集拟声/重复声响占比过高")
	}
	if stats.Repeated12Extra >= 20 {
		blockers = append(blockers, "12字级重复过高")
	}
	if stats.DialogueRatio > 0.08 {
		blockers = append(blockers, "存在明显对话声口，不属于技术说明文锚点")
	}
	if stats.ClicheTotalPerK > 4 {
		blockers = append(blockers, "套路/解释归纳密度偏高，不适合技术说明文人工锚点")
	}
	if techDensity < 18 && asciiDensity < 45 {
		blockers = append(blockers, "领域术语或英文摘要密度不足")
	}
	if stats.AvgSentenceLen < 22 {
		blockers = append(blockers, "说明文句长承载不足")
	}

	score := 0
	credits := []map[string]any{}
	add := func(points int, name, evidence string) {
		score += points
		credits = append(credits, map[string]any{"name": name, "points": points, "evidence": evidence})
	}
	if techDensity >= 45 {
		add(26, "technical_terms_high", "领域术语密度高")
	} else if techDensity >= 26 {
		add(20, "technical_terms_mid", "领域术语密度较高")
	} else if techDensity >= 18 {
		add(14, "technical_terms_present", "存在领域术语")
	}
	if asciiDensity >= 90 {
		add(20, "bilingual_abstract_shape", "英文/缩写词密度高")
	} else if asciiDensity >= 45 {
		add(12, "ascii_terms_present", "存在英文/缩写词")
	}
	if stats.AvgSentenceLen >= 34 && stats.SentenceCV >= 0.18 && stats.SentenceCV <= 0.62 {
		add(18, "expository_long_sentence_control", "说明文长句承载稳定")
	} else if stats.AvgSentenceLen >= 26 {
		add(12, "expository_sentence_load", "说明文句长有承载")
	}
	if paragraphCount <= 4 {
		add(10, "abstract_paragraph_form", "段落形态接近摘要/说明文")
	}
	if stats.DialogueRatio == 0 {
		add(8, "no_dialogue_expository", "无对话声口")
	}
	if stats.ClicheTotalPerK <= 1 {
		add(8, "cliche_absent", "套路措辞密度低")
	}
	if score > 100 {
		score = 100
	}
	eligible := len(blockers) == 0 && score >= 52
	strength := "none"
	if eligible && score >= 72 {
		strength = "strong"
	} else if eligible {
		strength = "moderate"
	}
	if len(credits) > 8 {
		credits = credits[:8]
	}
	curveFactor, curveCap, styleFactor, styleCap, segmentCap := 1.0, 100.0, 1.0, 100.0, 100.0
	if eligible {
		curveFactor, curveCap, styleFactor, styleCap, segmentCap = 0.05, 5, 0.15, 12, 18
	}
	return map[string]any{
		"score":             round2(float64(score)),
		"eligible":          eligible,
		"strength":          strength,
		"anchor_type":       "technical_expository",
		"final_cap_allowed": eligible && score >= 52,
		"blockers":          blockers,
		"credits":           credits,
		"curve_factor":      curveFactor,
		"curve_cap":         curveCap,
		"style_factor":      styleFactor,
		"style_cap":         styleCap,
		"segment_cap":       segmentCap,
		"metrics": map[string]any{
			"anchor_type":                  "technical_expository",
			"technical_term_density_per_k": techDensity,
			"ascii_word_density_per_k":     asciiDensity,
			"avg_sentence_len":             stats.AvgSentenceLen,
			"sentence_cv":                  stats.SentenceCV,
			"paragraph_count":              paragraphCount,
			"dialogue_ratio":               stats.DialogueRatio,
		},
	}
}

func humanAnchorStats(body string, stats Stats, sentLens []float64, perK map[string]float64, noise map[string]any) map[string]any {
	technicalAnchor := technicalExpositoryAnchorStats(body, stats, sentLens, noise)
	if boolFromAny(technicalAnchor["eligible"]) {
		return technicalAnchor
	}
	sceneDensity := round2(stats.ConcreteDensityPerK + stats.ActionDensityPerK + stats.SensoryDensityPerK)
	short12 := round3(ratio(countWhere(sentLens, func(v float64) bool { return v <= 12 }), len(sentLens)))
	quoteDensity := density(countAll(body, []string{"“", "”", "「", "」", "『", "』"}), stats.Hanzi)
	punctKinds := punctuationKindCount(body)
	blockers := []string{}
	if stats.Hanzi < 800 {
		blockers = append(blockers, "文本长度不足 800 汉字，人工锚点置信度低")
	}
	if intFromAny(noise["semantic_noise_runs"]) > 0 {
		blockers = append(blockers, "存在无语义脏码/字符汤")
	}
	if floatFromAny(noise["sound_noise_ratio"]) >= 0.03 {
		blockers = append(blockers, "密集拟声/重复声响占比过高")
	}
	if perK["工程泄漏"] > 0 {
		blockers = append(blockers, "存在写作工程词泄漏")
	}
	if stats.Repeated12Extra >= 20 {
		blockers = append(blockers, "12字级重复过高")
	}
	if stats.ClicheTotalPerK >= 10 && sceneDensity < 18 {
		blockers = append(blockers, "套路密度高且场景锚点不足")
	}
	if stats.CrossParagraphFeatures >= 70 &&
		stats.SingleSentenceParaRate >= 0.70 &&
		stats.ShortParagraphRatio >= 0.62 &&
		stats.BracketLineRatio >= 0.08 {
		blockers = append(blockers, "短段与条款块同时密集，疑似人味化后处理")
	}

	score := 0
	credits := []map[string]any{}
	add := func(points int, name, evidence string) {
		score += points
		credits = append(credits, map[string]any{"name": name, "points": points, "evidence": evidence})
	}

	if stats.SentenceCV >= 0.72 {
		add(18, "sentence_cv_high", "句长 CV 较高，长短句自然拉开")
	} else if stats.SentenceCV >= 0.60 {
		add(14, "sentence_cv_mid_high", "句长 CV 有起伏")
	} else if stats.SentenceCV >= 0.52 {
		add(8, "sentence_cv_usable", "句长未呈均质平滑")
	}
	if stats.ParagraphCV >= 0.70 {
		add(16, "paragraph_cv_high", "段长 CV 较高，段落疏密明显")
	} else if stats.ParagraphCV >= 0.55 {
		add(12, "paragraph_cv_mid_high", "段落功能有差异")
	} else if stats.ParagraphCV >= 0.45 {
		add(6, "paragraph_cv_usable", "段落没有过度整齐")
	}
	if short12 >= 0.10 && short12 <= 0.50 {
		add(12, "short_sentence_mix", "12字以内短句占比处于自然断气区间")
	} else if short12 >= 0.06 && short12 <= 0.58 {
		add(6, "short_sentence_present", "存在短句断气")
	}
	if stats.DialogueRatio >= 0.25 || quoteDensity >= 45 {
		add(14, "dialogue_voice_high", "对话/引号密度较高")
	} else if stats.DialogueRatio >= 0.12 || quoteDensity >= 18 {
		add(10, "dialogue_voice_present", "存在人物声口")
	}
	if sceneDensity >= 28 {
		add(20, "scene_density_high", "物件/动作/感官密度高")
	} else if sceneDensity >= 20 {
		add(15, "scene_density_mid_high", "物件/动作/感官密度较高")
	} else if sceneDensity >= 14 {
		add(9, "scene_density_present", "存在场景锚点")
	}
	if stats.ActionDensityPerK >= 10 && stats.SensoryDensityPerK >= 5 {
		add(12, "action_sensory_chain", "动作和感官共同承载场景")
	} else if stats.ActionDensityPerK >= 8 {
		add(6, "action_chain", "动作链存在")
	}
	if stats.AbstractDensityPerK <= 4.5 && sceneDensity >= 18 {
		add(8, "abstract_under_scene", "抽象词低于场景锚点")
	}
	if stats.ConcreteDensityPerK >= 6 {
		add(8, "concrete_objects", "具体物/数字密度较高")
	} else if stats.ConcreteDensityPerK >= 2 {
		add(4, "concrete_objects_present", "存在具体物/数字")
	}
	if punctKinds >= 8 {
		add(8, "punctuation_palette", "标点层次较多")
	}
	if stats.ClicheTotalPerK <= 4 {
		add(8, "cliche_low", "套路措辞密度低")
	} else if stats.ClicheTotalPerK <= 7 {
		add(4, "cliche_controlled", "套路措辞密度可控")
	}

	if score > 100 {
		score = 100
	}
	eligible := len(blockers) == 0 && score >= 52
	strength := "none"
	curveFactor := 1.0
	curveCap := 100.0
	styleFactor := 1.0
	styleCap := 100.0
	segmentCap := 100.0
	if eligible && score >= 72 {
		strength = "strong"
		if score >= 90 && sceneDensity >= 28 && stats.ClicheTotalPerK <= 4 {
			curveFactor = 0.34
			curveCap = 36
			styleFactor = 0.55
			styleCap = 50
			segmentCap = 88
		} else if score >= 82 && sceneDensity >= 22 {
			curveFactor = 0.42
			curveCap = 45
			styleFactor = 0.60
			styleCap = 55
			segmentCap = 78
		} else {
			curveFactor = 0.55
			curveCap = 58
			styleFactor = 0.70
			styleCap = 65
			segmentCap = 70
		}
	} else if eligible {
		strength = "moderate"
		curveFactor = 0.70
		curveCap = 70
		styleFactor = 0.75
		styleCap = 72
		segmentCap = 70
	}
	if len(credits) > 8 {
		credits = credits[:8]
	}
	finalCapAllowed := eligible &&
		strength == "strong" &&
		score >= 90 &&
		sceneDensity >= 28 &&
		stats.DialogueRatio >= 0.25 &&
		stats.ClicheTotalPerK <= 4 &&
		stats.AbstractDensityPerK <= 4.5 &&
		stats.Repeated12Extra == 0
	return map[string]any{
		"score":             round2(float64(score)),
		"eligible":          eligible,
		"strength":          strength,
		"anchor_type":       "narrative_scene",
		"final_cap_allowed": finalCapAllowed,
		"blockers":          blockers,
		"credits":           credits,
		"curve_factor":      curveFactor,
		"curve_cap":         curveCap,
		"style_factor":      styleFactor,
		"style_cap":         styleCap,
		"segment_cap":       segmentCap,
		"metrics": map[string]any{
			"sentence_cv":             stats.SentenceCV,
			"paragraph_cv":            stats.ParagraphCV,
			"short_sentence_ratio_12": short12,
			"dialogue_ratio":          stats.DialogueRatio,
			"quote_density_per_k":     quoteDensity,
			"punctuation_kinds":       punctKinds,
			"concrete_density_per_k":  stats.ConcreteDensityPerK,
			"action_density_per_k":    stats.ActionDensityPerK,
			"sensory_density_per_k":   stats.SensoryDensityPerK,
			"emotion_density_per_k":   stats.EmotionDensityPerK,
			"abstract_density_per_k":  stats.AbstractDensityPerK,
			"scene_density_per_k":     sceneDensity,
		},
	}
}

func applyHumanAnchorCalibration(components map[string]Dimension, anchor map[string]any) map[string]Dimension {
	if !boolFromAny(anchor["eligible"]) {
		return components
	}
	factor := floatFromAny(anchor["curve_factor"])
	if factor == 0 {
		factor = 1
	}
	capScore := floatFromAny(anchor["curve_cap"])
	if capScore == 0 {
		capScore = 100
	}
	styleFactor := floatFromAny(anchor["style_factor"])
	if styleFactor == 0 {
		styleFactor = 1
	}
	styleCap := floatFromAny(anchor["style_cap"])
	if styleCap == 0 {
		styleCap = 100
	}
	for _, key := range []string{"probability_curvature_proxy", "weak_lm_uniformity", "local_entropy_uniformity", "zhuque_segment_proxy", "stylometry_readability"} {
		item, ok := components[key]
		if !ok {
			continue
		}
		original := item.Score
		adjusted := math.Min(original*factor, capScore)
		if key == "stylometry_readability" {
			adjusted = math.Min(original*styleFactor, styleCap)
		}
		if adjusted < original {
			item.Score = round2(adjusted)
			if item.Stats == nil {
				item.Stats = map[string]any{}
			}
			item.Stats["human_anchor_adjusted_from"] = round2(original)
			item.Signals = append(item.Signals, sig(
				"high_quality_human_anchor_calibration",
				0,
				"高质人工样本锚点启用，曲线类误判降权",
			))
			components[key] = item
		}
	}
	return components
}

func scoreLayoutHumanizerFingerprint(stats Stats) Dimension {
	signals := []Signal{}
	if stats.CrossParagraphFeatures >= 70 &&
		stats.SingleSentenceParaRate >= 0.62 &&
		stats.ShortParagraphRatio >= 0.55 {
		signals = append(signals, sig("single_sentence_short_paragraph_grid", 78, "单句段和12字以内短段同时密集，呈现句段级分类器容易识别的网格化断行"))
	}
	if stats.CrossParagraphFeatures >= 60 &&
		stats.MedianHanziPerPara <= 10 &&
		stats.AvgHanziPerParagraph <= 24 {
		signals = append(signals, sig("microparagraph_median_low", 60, "段落中位字数和平均字数偏低，整章信息点被拆得过碎"))
	}
	if stats.CrossParagraphFeatures >= 60 && stats.BracketLineRatio >= 0.10 {
		signals = append(signals, sig("formatted_clause_blocks", 48, "独立条款/账单块占比偏高"))
	}
	if stats.CrossParagraphFeatures >= 70 &&
		stats.BracketLineRatio >= 0.08 &&
		stats.ShortParagraphRatio >= 0.50 &&
		stats.VeryShortParaRatio >= 0.24 {
		signals = append(signals, sig("fragmented_rule_card_pattern", 72, "短段、极短段和规则卡片同时密集，像后期 humanizer 打碎的文本"))
	}
	return dim("布局/碎段人味化指纹", "layout_humanizer_fingerprint", map[string]any{
		"paragraph_count":                 stats.CrossParagraphFeatures,
		"avg_hanzi_per_paragraph":         stats.AvgHanziPerParagraph,
		"median_hanzi_per_paragraph":      stats.MedianHanziPerPara,
		"short_paragraph_ratio":           stats.ShortParagraphRatio,
		"very_short_paragraph_ratio":      stats.VeryShortParaRatio,
		"single_sentence_paragraph_ratio": stats.SingleSentenceParaRate,
		"bracket_line_ratio":              stats.BracketLineRatio,
	}, signals)
}

func dim(name, key string, stats map[string]any, signals []Signal) Dimension {
	score := 0.0
	for _, s := range signals {
		if s.Score > score {
			score = s.Score
		}
	}
	if len(signals) > 1 {
		score = math.Min(100, score+float64(len(signals)-1)*6)
	}
	weight := dimensionWeights[key]
	if weight == 0 {
		weight = latestProxyWeights[key]
	}
	return Dimension{Name: name, Score: round2(score), Weight: weight, Stats: stats, Signals: signals}
}

func sig(name string, score float64, evidence string) Signal {
	return Signal{Name: name, Score: round2(score), Evidence: evidence}
}

func legacyHeuristicPercent(stats Stats, perK map[string]float64) float64 {
	score := 0.0
	if stats.SentenceCV < 0.42 {
		score += 22
	} else if stats.SentenceCV < 0.55 {
		score += 14
	} else if stats.SentenceCV < 0.70 {
		score += 6
	}
	score += math.Min(24, stats.ClicheTotalPerK/12*24)
	if perK["解释归纳"] >= 0.8 {
		score += 10
	}
	if perK["平滑转场"] >= 1.2 {
		score += 7
	}
	if perK["工程泄漏"] > 0 {
		score += 30
	}
	if stats.Repeated12Extra >= 3 {
		score += 7
	}
	if stats.ConcreteDensityPerK < 0.4 && stats.Hanzi >= 800 {
		score += 6
	}
	positive := score
	if stats.DialogueRatio >= 0.25 {
		score -= 5
	}
	if stats.ShortSentenceRatio >= 0.20 {
		score -= 4
	}
	if stats.ConcreteDensityPerK >= 1.5 {
		score -= 4
	}
	if stats.ParagraphCV >= 0.85 {
		score -= 3
	}
	if positive > 0 && score < positive*0.35 {
		score = positive * 0.35
	}
	return round2(clamp(score, 0, 100))
}

// plainChapterTitleRe 匹配正常小说格式的纯文本章节标题首行（无 # 号），如「第一章 欠费单」/
// 「第 12 章」，用于在 AIGC 分析时剔除标题、只统计正文。
var plainChapterTitleRe = regexp.MustCompile(`^第[0-9零一二三四五六七八九十百千]+章([ 　].*)?$`)

func stripMarkdownTitles(text string) string {
	var lines []string
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// 正常小说格式：首个非空行若是「第N章 …」纯文本标题，也剔除。
		if i < 3 && plainChapterTitleRe.MatchString(trimmed) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func normalizeDetectorCurveText(text string) string {
	text = soundNoiseRe.ReplaceAllString(text, "声响。")
	for _, item := range semanticNoiseMatches(text) {
		if raw, ok := item["text"].(string); ok && raw != "" {
			text = strings.ReplaceAll(text, raw, "无效乱码。")
		}
	}
	return text
}

func detectorNoiseStats(text string) map[string]any {
	soundMatches := soundNoiseRe.FindAllString(text, -1)
	soundHanzi := 0
	for _, match := range soundMatches {
		soundHanzi += len(hanzi(match))
	}
	semanticMatches := semanticNoiseMatches(text)
	semanticHanzi := 0
	examples := make([]map[string]any, 0, minInt(len(semanticMatches), 3))
	for i, item := range semanticMatches {
		n := intFromAny(item["hanzi"])
		semanticHanzi += n
		if i < 3 {
			examples = append(examples, map[string]any{
				"text":            truncateRunes(item["text"].(string), 80),
				"hanzi":           n,
				"unique_ratio":    item["unique_ratio"],
				"grammar_ratio":   item["grammar_ratio"],
				"top_char_ratio":  item["top_char_ratio"],
				"rare_char_ratio": item["rare_char_ratio"],
				"noise_type":      item["noise_type"],
			})
		}
	}
	total := math.Max(float64(len(hanzi(text))), 1)
	return map[string]any{
		"sound_noise_runs":        len(soundMatches),
		"sound_noise_hanzi":       soundHanzi,
		"sound_noise_ratio":       round4(float64(soundHanzi) / total),
		"semantic_noise_runs":     len(semanticMatches),
		"semantic_noise_hanzi":    semanticHanzi,
		"semantic_noise_ratio":    round4(float64(semanticHanzi) / total),
		"semantic_noise_examples": examples,
	}
}

func semanticNoiseMatches(text string) []map[string]any {
	rows := []map[string]any{}
	usedSpans := [][2]int{}
	for _, span := range rareTermSoupRe.FindAllStringIndex(text, -1) {
		run := text[span[0]:span[1]]
		chars := hanzi(run)
		if len(chars) == 0 {
			continue
		}
		counts := map[rune]int{}
		top := 0
		for _, r := range chars {
			counts[r]++
			if counts[r] > top {
				top = counts[r]
			}
		}
		usedSpans = append(usedSpans, [2]int{span[0], span[1]})
		rows = append(rows, map[string]any{
			"text":            run,
			"hanzi":           len(chars),
			"unique_ratio":    round3(float64(len(counts)) / float64(len(chars))),
			"grammar_ratio":   0.0,
			"top_char_ratio":  round3(float64(top) / float64(len(chars))),
			"rare_char_ratio": 1.0,
			"noise_type":      "rare_term_soup",
		})
	}
	for _, span := range cjkRunRe.FindAllStringIndex(text, -1) {
		contained := false
		for _, used := range usedSpans {
			if span[0] >= used[0] && span[1] <= used[1] {
				contained = true
				break
			}
		}
		if contained {
			continue
		}
		run := text[span[0]:span[1]]
		chars := hanzi(run)
		if len(chars) == 0 {
			continue
		}
		counts := map[rune]int{}
		grammar := 0
		rare := 0
		top := 0
		for _, r := range chars {
			counts[r]++
			if counts[r] > top {
				top = counts[r]
			}
			if strings.ContainsRune(grammarChars, r) {
				grammar++
			}
			if strings.ContainsRune(rareSoupChars, r) {
				rare++
			}
		}
		uniqueRatio := float64(len(counts)) / float64(len(chars))
		grammarRatio := float64(grammar) / float64(len(chars))
		topRatio := float64(top) / float64(len(chars))
		rareRatio := float64(rare) / float64(len(chars))
		repetitiveSoup := uniqueRatio <= 0.24 && grammarRatio <= 0.10 && (len(chars) >= 90 || topRatio >= 0.09)
		rareSoup := rareRatio >= 0.42 && grammarRatio <= 0.12 && len(chars) >= 18
		if repetitiveSoup || rareSoup {
			noiseType := "repetitive_char_soup"
			if rareSoup {
				noiseType = "rare_char_soup"
			}
			rows = append(rows, map[string]any{
				"text":            run,
				"hanzi":           len(chars),
				"unique_ratio":    round3(uniqueRatio),
				"grammar_ratio":   round3(grammarRatio),
				"top_char_ratio":  round3(topRatio),
				"rare_char_ratio": round3(rareRatio),
				"noise_type":      noiseType,
			})
		}
	}
	return rows
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	default:
		return false
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func stringSliceFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func punctuationKindCount(text string) int {
	kinds := 0
	for _, mark := range []string{"，", "。", "？", "！", "：", "；", "、", "“", "”", "「", "」", "——", "……"} {
		if strings.Contains(text, mark) {
			kinds++
		}
	}
	return kinds
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func hanzi(text string) []rune {
	var out []rune
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			out = append(out, r)
		}
	}
	return out
}

func splitSentences(text string) []string {
	parts := sentenceSplitRe.Split(text, -1)
	var out []string
	for _, p := range parts {
		if len(hanzi(p)) > 0 {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return out
}

func paragraphs(text string) []string {
	parts := paragraphSplitRe.Split(text, -1)
	var out []string
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

func paragraphFragmentationStats(paras []string) map[string]float64 {
	lengths := make([]float64, 0, len(paras))
	singleSentence := 0
	bracketLines := 0
	shortParagraphs := 0
	veryShortParagraphs := 0
	for _, para := range paras {
		n := len(hanzi(para))
		if n <= 0 {
			continue
		}
		lengths = append(lengths, float64(n))
		if n <= 12 {
			shortParagraphs++
		}
		if n <= 6 {
			veryShortParagraphs++
		}
		if len(splitSentences(para)) == 1 {
			singleSentence++
		}
		trimmed := strings.TrimSpace(para)
		if strings.HasPrefix(trimmed, "【") && strings.HasSuffix(trimmed, "】") {
			bracketLines++
		}
	}
	count := len(lengths)
	if count == 0 {
		return map[string]float64{
			"paragraph_count":                 0,
			"avg_hanzi_per_paragraph":         0,
			"median_hanzi_per_paragraph":      0,
			"short_paragraph_ratio":           0,
			"very_short_paragraph_ratio":      0,
			"single_sentence_paragraph_ratio": 0,
			"bracket_line_ratio":              0,
		}
	}
	return map[string]float64{
		"paragraph_count":                 float64(count),
		"avg_hanzi_per_paragraph":         round2(mean(lengths)),
		"median_hanzi_per_paragraph":      round2(median(lengths)),
		"short_paragraph_ratio":           round3(ratio(shortParagraphs, count)),
		"very_short_paragraph_ratio":      round3(ratio(veryShortParagraphs, count)),
		"single_sentence_paragraph_ratio": round3(ratio(singleSentence, count)),
		"bracket_line_ratio":              round3(ratio(bracketLines, count)),
	}
}

type semanticSentenceProfile struct {
	signature    string
	scene        bool
	abstractOnly bool
	roleCount    int
}

func semanticSentenceProfiles(sents []string) []semanticSentenceProfile {
	out := make([]semanticSentenceProfile, 0, len(sents))
	for _, sent := range sents {
		if len(hanzi(sent)) < 4 {
			continue
		}
		out = append(out, semanticSentenceProfileFor(sent))
	}
	return out
}

func semanticSentenceProfileFor(sent string) semanticSentenceProfile {
	roles := []string{}
	addRole := func(ok bool, name string) {
		if ok {
			roles = append(roles, name)
		}
	}
	hasObject := concreteCount(sent) > 0
	hasAction := countAll(sent, semanticActionMarks) > 0
	hasSensory := countAll(sent, sensoryMarkers) > 0
	hasDialogue := strings.ContainsAny(sent, "“”「」") || countAll(sent, []string{"说", "问", "回答", "答道", "喊", "骂"}) > 0
	hasRule := countAll(sent, semanticRuleMarkers) > 0
	hasAbstract := countAll(sent, abstractMarkers) > 0 || countAll(sent, summaryMarkers) > 0
	hasEmotion := countAll(sent, emotionMarkers) > 0
	addRole(hasObject, "object")
	addRole(hasAction, "action")
	addRole(hasSensory, "sensory")
	addRole(hasDialogue, "dialogue")
	addRole(hasRule, "rule")
	addRole(hasAbstract, "abstract")
	addRole(hasEmotion, "emotion")
	if len(roles) == 0 {
		roles = append(roles, "plain")
	}
	scene := hasObject || hasAction || hasSensory || hasDialogue || hasRule
	return semanticSentenceProfile{
		signature:    strings.Join(roles, "+"),
		scene:        scene,
		abstractOnly: !scene && (hasAbstract || hasEmotion),
		roleCount:    len(roles),
	}
}

func semanticPerplexityStats(profiles []semanticSentenceProfile) map[string]any {
	if len(profiles) == 0 {
		return map[string]any{
			"sentence_count":           0,
			"signature_kinds":          0,
			"dominant_signature":       "",
			"dominant_signature_ratio": 0.0,
			"same_signature_run_max":   0,
			"scene_signature_ratio":    0.0,
			"abstract_only_ratio":      0.0,
			"semantic_turn_ratio":      0.0,
			"avg_role_count":           0.0,
		}
	}
	counts := map[string]int{}
	sceneCount := 0
	abstractOnlyCount := 0
	roleCounts := make([]float64, 0, len(profiles))
	turns := 0
	maxRun := 1
	currentRun := 1
	for i, profile := range profiles {
		counts[profile.signature]++
		if profile.scene {
			sceneCount++
		}
		if profile.abstractOnly {
			abstractOnlyCount++
		}
		roleCounts = append(roleCounts, float64(profile.roleCount))
		if i == 0 {
			continue
		}
		if profile.signature != profiles[i-1].signature {
			turns++
			currentRun = 1
		} else {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
		}
	}
	dominantSignature := ""
	dominantCount := 0
	for signature, count := range counts {
		if count > dominantCount {
			dominantSignature = signature
			dominantCount = count
		}
	}
	n := len(profiles)
	return map[string]any{
		"sentence_count":           n,
		"signature_kinds":          len(counts),
		"dominant_signature":       dominantSignature,
		"dominant_signature_ratio": round3(ratio(dominantCount, n)),
		"same_signature_run_max":   maxRun,
		"scene_signature_ratio":    round3(ratio(sceneCount, n)),
		"abstract_only_ratio":      round3(ratio(abstractOnlyCount, n)),
		"semantic_turn_ratio":      round3(ratio(turns, maxInt(n-1, 1))),
		"avg_role_count":           round3(mean(roleCounts)),
	}
}

func clicheDensities(text string, n int) (map[string]float64, float64) {
	perK := map[string]float64{}
	total := 0.0
	for name, words := range cliches {
		count := countAll(text, words)
		d := density(count, n)
		perK[name] = d
		total += d
	}
	return perK, round2(total)
}

func countAll(text string, needles []string) int {
	total := 0
	for _, needle := range needles {
		total += strings.Count(text, needle)
	}
	return total
}

func concreteCount(text string) int {
	count := countAll(text, concreteHintMarkers)
	for _, r := range text {
		if unicode.IsDigit(r) {
			count++
		}
	}
	return count
}

func repeatedExtra(chars []rune, n int) int {
	if len(chars) < n {
		return 0
	}
	counts := map[string]int{}
	for i := 0; i+n <= len(chars); i++ {
		counts[string(chars[i:i+n])]++
	}
	extra := 0
	for _, c := range counts {
		if c >= 2 {
			extra += c - 1
		}
	}
	return extra
}

func repeatedParagraphStarts(paras []string) int {
	counts := map[string]int{}
	for _, para := range paras {
		clean := nonCJKRe.ReplaceAllString(para, "")
		rs := []rune(clean)
		if len(rs) >= 4 {
			counts[string(rs[:4])]++
		}
	}
	extra := 0
	for _, c := range counts {
		if c >= 2 {
			extra += c - 1
		}
	}
	return extra
}

func paragraphRows(paras []string) []map[string]float64 {
	rows := make([]map[string]float64, 0, len(paras))
	for _, para := range paras {
		pChars := hanzi(para)
		if len(pChars) == 0 {
			continue
		}
		sents := splitSentences(para)
		lens := make([]float64, 0, len(sents))
		for _, sent := range sents {
			lens = append(lens, float64(len(hanzi(sent))))
		}
		rows = append(rows, map[string]float64{
			"hanzi":              float64(len(pChars)),
			"avg_sentence_len":   mean(lens),
			"sentence_cv":        cv(lens),
			"comma_period_ratio": float64(strings.Count(para, "，")) / math.Max(1, float64(strings.Count(para, "。"))),
			"dialogue":           boolFloat(strings.Contains(para, "“") || strings.Contains(para, "\"")),
			"cliche_total_per_k": density(countAll(para, flattenCliches()), len(pChars)),
			"concrete_density":   density(concreteCount(para), len(pChars)),
			"action_density":     density(countAll(para, actionMarkers), len(pChars)),
			"sensory_density":    density(countAll(para, sensoryMarkers), len(pChars)),
			"emotion_density":    density(countAll(para, emotionMarkers), len(pChars)),
			"abstract_density":   density(countAll(para, abstractMarkers), len(pChars)),
		})
	}
	return rows
}

func flattenCliches() []string {
	var out []string
	for _, words := range cliches {
		out = append(out, words...)
	}
	return out
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func paragraphSimilarityStats(rows []map[string]float64) (float64, float64, float64) {
	if len(rows) < 2 {
		return 0, 0, 0
	}
	vectors := make([][]float64, 0, len(rows))
	for _, row := range rows {
		vectors = append(vectors, []float64{
			math.Min(row["hanzi"]/260, 2),
			math.Min(row["avg_sentence_len"]/28, 2),
			math.Min(row["sentence_cv"], 1.8),
			math.Min(row["comma_period_ratio"]/3, 2),
			row["dialogue"],
			math.Min(row["cliche_total_per_k"]/6, 2),
			math.Min(row["concrete_density"]/5, 2),
			math.Min(row["action_density"]/24, 2),
			math.Min(row["sensory_density"]/12, 2),
			math.Min(row["emotion_density"]/8, 2),
			math.Min(row["abstract_density"]/8, 2),
		})
	}
	sims := make([]float64, 0, len(vectors)-1)
	high := 0
	for i := 1; i < len(vectors); i++ {
		sim := cosine(vectors[i-1], vectors[i])
		sims = append(sims, sim)
		if sim >= 0.965 {
			high++
		}
	}
	return mean(sims), stddev(sims), ratio(high, len(sims))
}

func cosine(left, right []float64) float64 {
	dot := 0.0
	leftNorm := 0.0
	rightNorm := 0.0
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	denom := math.Sqrt(leftNorm) * math.Sqrt(rightNorm)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

func localSurprisalStats(sents []string) map[string]any {
	var sentChars [][]rune
	var all []rune
	for _, sent := range sents {
		chars := hanzi(sent)
		if len(chars) < 4 {
			continue
		}
		sentChars = append(sentChars, chars)
		all = append(all, chars...)
	}
	if len(all) < 200 || len(sentChars) < 8 {
		return map[string]any{"sentence_count": len(sentChars), "unigram_surprisal_avg": 0.0, "unigram_surprisal_cv": 0.0, "bigram_surprisal_avg": 0.0, "bigram_surprisal_cv": 0.0}
	}
	unigrams := map[rune]int{}
	bigrams := map[string]int{}
	prev := map[rune]int{}
	for _, r := range all {
		unigrams[r]++
	}
	for _, chars := range sentChars {
		for i := 0; i+1 < len(chars); i++ {
			bigrams[string([]rune{chars[i], chars[i+1]})]++
			prev[chars[i]]++
		}
	}
	vocab := float64(len(unigrams))
	total := float64(len(all))
	var uniVals []float64
	var biVals []float64
	for _, chars := range sentChars {
		uni := 0.0
		for _, r := range chars {
			uni += -math.Log2((float64(unigrams[r]) + 1) / (total + vocab))
		}
		uniVals = append(uniVals, uni/float64(len(chars)))
		if len(chars) >= 2 {
			bi := 0.0
			for i := 0; i+1 < len(chars); i++ {
				key := string([]rune{chars[i], chars[i+1]})
				bi += -math.Log2((float64(bigrams[key]) + 1) / (float64(prev[chars[i]]) + vocab))
			}
			biVals = append(biVals, bi/float64(len(chars)-1))
		}
	}
	return map[string]any{
		"sentence_count":        len(sentChars),
		"unigram_surprisal_avg": round3(mean(uniVals)),
		"unigram_surprisal_cv":  round3(cv(uniVals)),
		"bigram_surprisal_avg":  round3(mean(biVals)),
		"bigram_surprisal_cv":   round3(cv(biVals)),
	}
}

func windowEntropyStats(chars []rune) map[string]any {
	const window = 180
	const step = 90
	if len(chars) < window*2 {
		return map[string]any{"window_count": 0, "normalized_entropy_avg": 0.0, "normalized_entropy_std": 0.0, "normalized_entropy_cv": 0.0, "ttr_avg": 0.0, "ttr_cv": 0.0}
	}
	var entropyVals []float64
	var ttrVals []float64
	for start := 0; start+window <= len(chars); start += step {
		chunk := chars[start : start+window]
		entropyVals = append(entropyVals, normalizedEntropy(chunk))
		ttrVals = append(ttrVals, ratio(len(uniqueRunes(chunk)), window))
	}
	return map[string]any{
		"window_count":           len(entropyVals),
		"normalized_entropy_avg": round3(mean(entropyVals)),
		"normalized_entropy_std": round3(stddev(entropyVals)),
		"normalized_entropy_cv":  round3(cv(entropyVals)),
		"ttr_avg":                round3(mean(ttrVals)),
		"ttr_cv":                 round3(cv(ttrVals)),
	}
}

func rowValues(rows []map[string]float64, key string) []float64 {
	values := make([]float64, 0, len(rows))
	for _, row := range rows {
		values = append(values, row[key])
	}
	return values
}

func dialogueRatio(text string) float64 {
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return 0
	}
	dialogue := 0
	for _, line := range lines {
		if strings.Contains(line, "“") || strings.Contains(line, "\"") {
			dialogue++
		}
	}
	lineRatio := ratio(dialogue, len(lines))
	quoteRatio := quotedHanziRatio(text)
	if len(lines) <= 2 && quoteRatio > 0 {
		return round3(math.Min(lineRatio, quoteRatio*1.15))
	}
	if quoteRatio > 0 {
		return round3(math.Min(lineRatio, math.Max(quoteRatio*1.15, 0.12)))
	}
	return round3(lineRatio)
}

func quotedHanziRatio(text string) float64 {
	total := len(hanzi(text))
	if total == 0 {
		return 0
	}
	quoted := 0
	inQuote := false
	for _, r := range text {
		switch r {
		case '“', '「', '『':
			inQuote = true
			continue
		case '"':
			inQuote = !inQuote
			continue
		case '”', '」', '』':
			inQuote = false
			continue
		}
		if inQuote && r >= '\u4e00' && r <= '\u9fff' {
			quoted++
		}
	}
	return math.Min(1, float64(quoted)/float64(total))
}

func normalizedEntropy(chars []rune) float64 {
	if len(chars) == 0 {
		return 0
	}
	counts := map[rune]int{}
	for _, r := range chars {
		counts[r]++
	}
	entropy := 0.0
	for _, c := range counts {
		p := float64(c) / float64(len(chars))
		entropy -= p * math.Log2(p)
	}
	maxEntropy := math.Log2(math.Max(2, float64(len(counts))))
	return entropy / maxEntropy
}

func uniqueRunes(chars []rune) map[rune]struct{} {
	out := make(map[rune]struct{})
	for _, r := range chars {
		out[r] = struct{}{}
	}
	return out
}

func countWhere(values []float64, fn func(float64) bool) int {
	n := 0
	for _, v := range values {
		if fn(v) {
			n++
		}
	}
	return n
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func stddev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	m := mean(values)
	sum := 0.0
	for _, v := range values {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)))
}

func cv(values []float64) float64 {
	m := mean(values)
	if m == 0 {
		return 0
	}
	return stddev(values) / m
}

func density(count int, n int) float64 {
	if n <= 0 {
		return 0
	}
	return round2(float64(count) / float64(n) * 1000)
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp(v, low, high float64) float64 {
	return math.Min(high, math.Max(low, v))
}

func labelFor(percent float64) string {
	switch {
	case percent >= 80:
		return "极高"
	case percent >= 60:
		return "高"
	case percent >= 35:
		return "中"
	case percent >= 15:
		return "低-中"
	default:
		return "低"
	}
}

func confidenceFor(n int, dims map[string]Dimension) string {
	if n < 300 {
		return "低"
	}
	active := 0
	for _, dim := range dims {
		if dim.Score >= 20 {
			active++
		}
	}
	if n >= 1200 && active >= 3 {
		return "高"
	}
	return "中"
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
