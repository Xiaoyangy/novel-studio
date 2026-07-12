package aigc

import (
	"strings"
	"testing"
)

func TestAnalyzeIncludesSemanticPerplexityComponent(t *testing.T) {
	text := strings.Repeat("这意味着他已经失去选择。某种程度上，这不仅仅是失败。真正的答案藏在命运背后。复杂的情绪在内心堆积。", 4)

	report := Analyze(text)
	component, ok := report.LatestDetectorProxy.Components["semantic_perplexity"]
	if !ok {
		t.Fatalf("expected semantic_perplexity component, got %+v", report.LatestDetectorProxy.Components)
	}
	if component.Weight <= 0 {
		t.Fatalf("expected component weight, got %+v", component)
	}
	if component.Score < 50 {
		t.Fatalf("expected semantic_perplexity risk score, got %+v", component)
	}
	if len(component.Signals) == 0 {
		t.Fatalf("expected semantic_perplexity signals, got %+v", component)
	}
}

func TestTTRLowFloorRelaxesForLongChapters(t *testing.T) {
	if got := ttrLowFloor(500); got != 0.28 {
		t.Fatalf("short chapter floor = %.2f, want 0.28", got)
	}
	if got := ttrLowFloor(3300); got >= 0.28 {
		t.Fatalf("long chapter floor = %.2f, want below short floor", got)
	}

	chars := make([]rune, 0, 4000)
	for i := 0; i < 1000; i++ {
		chars = append(chars, rune(0x4e00+i))
	}
	chars = append(chars, chars...)
	chars = append(chars, chars...)
	dimension := scorePerplexityProxy(chars, 0, map[string]float64{}, 50, 0)
	for _, signal := range dimension.Signals {
		if signal.Name == "ttr_low" {
			t.Fatalf("unexpected ttr_low for long varied chapter: %+v", dimension)
		}
	}
}

func TestOrderedMarkersIgnoreOrdinaryNarrativeUses(t *testing.T) {
	body := "第一碗豆腐脑刚端出去，他收到了第一笔转账。最后一条回复没发出去，想了想又把最后那句删掉。"
	if got := len(orderedMarkerRe.FindAllString(body, -1)); got != 0 {
		t.Fatalf("ordinary ordinal or temporal wording counted as outline markers: %d", got)
	}
}

func TestOrderedMarkersKeepExplicitOutlineSequence(t *testing.T) {
	body := "首先，把材料列出来。其次，逐项解释。最后，给出结论。"
	if got := len(orderedMarkerRe.FindAllString(body, -1)); got != 3 {
		t.Fatalf("explicit outline markers = %d, want 3", got)
	}
}

func TestStructureFingerprintAllowsVariedMobileSingleSentenceParagraphs(t *testing.T) {
	body := `河风从桥洞里穿过来。

摊前那张卷边的价目纸被吹得啪啪作响，马玉芬抬头看了一眼，没有腾出手去压。

林澈等到带孩子的女人转身，才沿着坡口走下去。

灯坏了。

价却不能跟着看不见。

老丁搬来支架、护套和新牌，车斗里还留着五金店的灰。

第一次送电，白光正照进翻滚的锅里，连热气都显得扎眼。

“往下压。”

灯头低下去以后，孩子隔着两步念出了价钱。

女人牵着他回来，要了两碗，其中一碗少糖。

普通的收款声响过两次，沈知遥才从坡口走进灯下。

她没看票，先看那截斜出去的线。`
	paras := paragraphs(body)
	fragment := paragraphFragmentationStats(paras)
	dimension := scoreStructureFingerprint(body, paras, map[string]float64{}, 0, 0, fragment)
	for _, signal := range dimension.Signals {
		if signal.Name == "single_sentence_paragraphs_elevated" || signal.Name == "fragmented_single_sentence_paragraphs" || signal.Name == "paragraph_sentence_shape" {
			t.Fatalf("varied mobile paragraphs should not be treated as a fragmentation template: %+v", dimension)
		}
	}
}

func TestZhuqueWholeTextSegmentPreservesParagraphLayout(t *testing.T) {
	body := "第一段有现场动作。\n\n第二段有不同长度的对白：“先等等。”\n\n第三段继续推进。"
	visible := make([]rune, 0, len(body))
	for _, r := range body {
		if !strings.ContainsRune(" \t\r\n", r) {
			visible = append(visible, r)
		}
	}
	bounds := [][2]int{{0, len(visible)}}
	got := zhuqueSegmentChunk(body, visible, bounds, 0)
	if got != body {
		t.Fatalf("whole-text segment lost paragraph layout:\n%q", got)
	}
}

func TestNarrativeHumanAnchorOnlySoftCalibratesStrongScenes(t *testing.T) {
	stats := Stats{
		Hanzi:               1500,
		SentenceCV:          0.75,
		ParagraphCV:         0.75,
		DialogueRatio:       0.30,
		ConcreteDensityPerK: 30,
		ActionDensityPerK:   12,
		SensoryDensityPerK:  6,
		AbstractDensityPerK: 1,
		ClicheTotalPerK:     1,
	}
	body := "门铃响了。“别开门。”他把欠费单压住，回头问：“你刚才听见什么？”她说：“有人在念我的房号。”"
	anchor := humanAnchorStats(body, stats, []float64{8, 22, 10, 18, 9, 24, 11, 17}, map[string]float64{}, map[string]any{})
	if !boolFromAny(anchor["eligible"]) {
		t.Fatalf("expected strong narrative anchor to remain eligible for soft calibration: %+v", anchor)
	}
	if boolFromAny(anchor["final_cap_allowed"]) {
		t.Fatalf("narrative anchor must never allow a final cap: %+v", anchor)
	}

	stats.Repeated12Extra = 1
	anchor = humanAnchorStats(body, stats, []float64{8, 22, 10, 18, 9, 24, 11, 17}, map[string]float64{}, map[string]any{})
	if boolFromAny(anchor["final_cap_allowed"]) {
		t.Fatalf("repetition must not restore a narrative final cap: %+v", anchor)
	}
}

func TestHumanAnchorDoesNotRewardDialogueAndActionStuffing(t *testing.T) {
	base := Stats{
		Hanzi:               1800,
		SentenceCV:          0.68,
		ParagraphCV:         0.62,
		DialogueRatio:       0.22,
		ConcreteDensityPerK: 12,
		ActionDensityPerK:   8,
		SensoryDensityPerK:  5,
		AbstractDensityPerK: 2,
		ClicheTotalPerK:     2,
	}
	stuffed := base
	stuffed.DialogueRatio = 0.58
	stuffed.ActionDensityPerK = 32
	sentences := []float64{8, 24, 11, 19, 7, 28, 13, 17}
	natural := humanAnchorStats("桌边有人争执，窗外雨声压住半句话。", base, sentences, map[string]float64{}, map[string]any{})
	staged := humanAnchorStats("“问。”他抬眼。“答。”她停手。", stuffed, sentences, map[string]float64{}, map[string]any{})
	if !boolFromAny(natural["eligible"]) {
		t.Fatalf("natural scene should remain anchor eligible: %+v", natural)
	}
	if boolFromAny(staged["eligible"]) || boolFromAny(staged["final_cap_allowed"]) {
		t.Fatalf("unsupported dialogue/action stuffing must not earn a human cap: %+v", staged)
	}
	if stringFromAny(staged["anchor_type"]) != "narrative_scene" {
		t.Fatalf("novel prose must not switch to technical anchor: %+v", staged)
	}
}

func TestEffectiveGatePercentUsesHumanAnchorFinalCap(t *testing.T) {
	capValue := 4.8
	report := Report{
		AIGCPercent:         80,
		BlendedAIGCPercent:  4.8,
		Stats:               Stats{Hanzi: 3000},
		HumanAnchorFinalCap: &capValue,
	}
	if got := EffectiveGatePercent(report); got != 4.8 {
		t.Fatalf("EffectiveGatePercent() = %.2f, want 4.80", got)
	}

	report.ContentIntegrityFloor = 82
	if got := EffectiveGatePercent(report); got != 80 {
		t.Fatalf("content integrity should bypass cap, got %.2f", got)
	}
}

func TestEffectiveGatePercentUsesWholeTextSegmentRiskOverHumanAnchorCap(t *testing.T) {
	capValue := 4.8
	report := Report{
		AIGCPercent:         4.8,
		BlendedAIGCPercent:  4.8,
		SegmentRiskFloor:    82,
		Stats:               Stats{Hanzi: 3000},
		HumanAnchorFinalCap: &capValue,
		ZhuqueSegmentProxy: ZhuqueSegmentProxy{
			Enabled:                 true,
			SuspectedAIRatioPercent: 100,
			MaxSegmentPercent:       82,
			RiskFloorPercent:        82,
			Segments: []ZhuqueSegment{
				{Index: 1, Proportion: 1, AIGCPercent: 82, Category: "疑似AI"},
			},
		},
	}
	if got := EffectiveGatePercent(report); got != 82 {
		t.Fatalf("whole-text single segment risk should override human anchor cap, got %.2f", got)
	}
}

func TestWholeTextRawCurveConsensusNeedsIndependentSupport(t *testing.T) {
	report := Report{
		Stats: Stats{
			DialogueRatio:       0.33,
			ConcreteDensityPerK: 17,
			ActionDensityPerK:   25,
			HumanAnchor: map[string]any{
				"eligible":    true,
				"strength":    "strong",
				"anchor_type": "narrative_scene",
				"blockers":    []string{},
				"segment_cap": 88.0,
			},
		},
		Dimensions: map[string]Dimension{
			"burstiness":                  {Score: 0},
			"structure_fingerprint":       {Score: 0},
			"cross_paragraph_consistency": {Score: 0},
			"perplexity_proxy": {Stats: map[string]any{
				"ttr":                0.25,
				"normalized_entropy": 0.90,
			}},
		},
		LatestDetectorProxy: DetectorProxy{Components: map[string]Dimension{
			"probability_curvature_proxy":  {Score: 33.32, Stats: map[string]any{"human_anchor_adjusted_from": 98.0}},
			"weak_lm_uniformity":           {Score: 27.88, Stats: map[string]any{"human_anchor_adjusted_from": 82.0}},
			"local_entropy_uniformity":     {Score: 23.12, Stats: map[string]any{"human_anchor_adjusted_from": 68.0}},
			"layout_humanizer_fingerprint": {Score: 0},
		}},
	}

	score, evidence := segmentAIGCProxy(report, 2935, 1)
	if score >= 50 {
		t.Fatalf("raw three-curve consensus alone should not override a strong human baseline, got %.2f (%v)", score, evidence)
	}
	if !strings.Contains(strings.Join(evidence, "\n"), "未形成独立复合高风险") {
		t.Fatalf("expected unsupported raw-curve evidence, got %v", evidence)
	}
}

func TestWholeTextStrongAnchorCanBeOverriddenByTwoIndependentSignals(t *testing.T) {
	report := Report{
		Stats: Stats{
			DialogueRatio:       0.20,
			ConcreteDensityPerK: 12,
			ActionDensityPerK:   18,
			HumanAnchor: map[string]any{
				"eligible":    true,
				"strength":    "strong",
				"anchor_type": "narrative_scene",
				"blockers":    []string{},
				"segment_cap": 88.0,
			},
		},
		ZhuqueCompositePercent: 60,
		Dimensions: map[string]Dimension{
			"burstiness":                  {Score: 0},
			"structure_fingerprint":       {Score: 0},
			"cross_paragraph_consistency": {Score: 0},
			"perplexity_proxy": {Stats: map[string]any{
				"ttr":                0.25,
				"normalized_entropy": 0.90,
			}},
		},
		LatestDetectorProxy: DetectorProxy{Components: map[string]Dimension{
			"probability_curvature_proxy":  {Score: 70, Stats: map[string]any{"human_anchor_adjusted_from": 98.0}},
			"weak_lm_uniformity":           {Score: 32, Stats: map[string]any{"human_anchor_adjusted_from": 82.0}},
			"local_entropy_uniformity":     {Score: 24, Stats: map[string]any{"human_anchor_adjusted_from": 68.0}},
			"layout_humanizer_fingerprint": {Score: 0},
		}},
	}

	score, evidence := segmentAIGCProxy(report, 2800, 1)
	if score < 80 {
		t.Fatalf("two independent high-risk signals should override the anchor, got %.2f (%v)", score, evidence)
	}
}
