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

func TestDialogueMicroPeriodChainSignalsNarrativeDynamics(t *testing.T) {
	body := `沈知遥说：“断电。先把线断了。”

老丁还在看线，她又指向翘边：“这里。护套没盖住线头。”

马玉芬问完，她只回了一句：“都挪。推车会慢，孩子不会。”`
	stats := dialogueMicroPeriodChain(body)
	if stats.HitTurns != 3 {
		t.Fatalf("micro-period turns=%d, want 3 (%v)", stats.HitTurns, stats.Examples)
	}
	dimension := scoreNarrativeDynamics(body, Stats{Hanzi: len(hanzi(body))})
	if got := intFromAny(dimension.Stats["dialogue_micro_period_chain_turns"]); got != 3 {
		t.Fatalf("narrative stats turns=%d, want 3: %+v", got, dimension)
	}
	found := false
	for _, signal := range dimension.Signals {
		if signal.Name == "dialogue_micro_period_chain" {
			found = true
			if signal.Score < 45 {
				t.Fatalf("micro-period signal must independently support whole-text review: %+v", signal)
			}
		}
	}
	if !found {
		t.Fatalf("narrative dynamics missing dialogue_micro_period_chain: %+v", dimension)
	}
}

func TestDialogueMicroPeriodChainBlocksNarrativeHumanAnchor(t *testing.T) {
	stats := Stats{
		Hanzi:               1800,
		SentenceCV:          0.68,
		ParagraphCV:         0.62,
		DialogueRatio:       0.22,
		ConcreteDensityPerK: 12,
		ActionDensityPerK:   12,
		SensoryDensityPerK:  6,
		AbstractDensityPerK: 2,
		ClicheTotalPerK:     2,
	}
	body := `“断电。先把线断了。”她挡住孩子。

“这里。护套没盖住线头。”她指向水桶。

“都挪。推车会慢，孩子不会。”她没再退。`
	anchor := humanAnchorStats(body, stats, []float64{8, 24, 11, 19, 7, 28, 13, 17}, map[string]float64{}, map[string]any{})
	if boolFromAny(anchor["eligible"]) {
		t.Fatalf("three micro-period dialogue turns must block the narrative human anchor: %+v", anchor)
	}
	if !strings.Contains(strings.Join(stringSliceFromAny(anchor["blockers"]), "\n"), "至少三个对白话轮") {
		t.Fatalf("human-anchor blocker missing exact dialogue evidence: %+v", anchor)
	}
	metrics, _ := anchor["metrics"].(map[string]any)
	if got := intFromAny(metrics["dialogue_micro_period_chain_turns"]); got != 3 {
		t.Fatalf("human-anchor metrics turns=%d, want 3: %+v", got, metrics)
	}
}

func TestDialogueMicroPeriodChainExclusionsAndAllowlist(t *testing.T) {
	exempt := []string{
		"好", "好的", "好吧", "行", "行吧", "可以", "知道", "知道了", "明白", "明白了",
		"是", "是的", "是啊", "对", "对的", "对啊", "没错", "不是", "不是的", "不用", "不用了",
		"没事", "没事了", "谢谢", "谢了", "抱歉", "对不起", "嗯", "嗯嗯", "嗯哼", "哦", "噢", "啊", "哎", "唉", "喂",
	}
	if len(dialogueMicroPeriodExempt) != len(exempt) {
		t.Fatalf("dialogue short-answer allowlist size=%d, want %d", len(dialogueMicroPeriodExempt), len(exempt))
	}
	for _, answer := range exempt {
		if !dialogueMicroPeriodExempt[answer] {
			t.Fatalf("short-answer allowlist missing %q", answer)
		}
		turn := "“" + answer + "。后面这句话正常说完。”\n"
		if got := dialogueMicroPeriodChain(strings.Repeat(turn, 3)).HitTurns; got != 0 {
			t.Fatalf("short answer %q produced %d micro-period hits", answer, got)
		}
	}

	body := `“断哪根？这根新线？”

“老丁，晃眼！锅都看不清了。”

“我……再想想。”

【“断电。先把线断了。”“这里。护套没盖住线头。”“都挪。推车会慢。”】`
	if got := dialogueMicroPeriodChain(body).HitTurns; got != 0 {
		t.Fatalf("questions, exclamations, ellipses and system text produced %d hits", got)
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
				{Index: 1, Proportion: 1, AIGCPercent: 82, Category: "疑似AI", WholeTextHardGate: true},
			},
		},
	}
	if got := EffectiveGatePercent(report); got != 82 {
		t.Fatalf("whole-text single segment risk should override human anchor cap, got %.2f", got)
	}
}

func TestWholeTextRawCurveConsensusWithNarrativeRiskCreatesHardGate(t *testing.T) {
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
			"weak_lm_uniformity":           {Score: 27.88, Stats: map[string]any{"human_anchor_adjusted_from": 96.0}},
			"local_entropy_uniformity":     {Score: 23.12, Stats: map[string]any{"human_anchor_adjusted_from": 90.0}},
			"layout_humanizer_fingerprint": {Score: 0},
			"narrative_dynamics":           {Score: 55},
		}},
	}

	score, evidence, hardGate := segmentAIGCProxy(report, 2935, 1)
	if score < 76 {
		t.Fatalf("raw consensus plus narrative risk should establish a whole-text floor, got %.2f (%v)", score, evidence)
	}
	if !hardGate {
		t.Fatalf("raw consensus plus narrative risk did not mark a hard gate: %v", evidence)
	}
	if !strings.Contains(strings.Join(evidence, "\n"), "独立叙事或结构风险") {
		t.Fatalf("expected independent-support evidence, got %v", evidence)
	}
}

func TestWholeTextRawCurveConsensusWithoutIndependentSupportStaysSoft(t *testing.T) {
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
				"segment_cap": 35.0,
			},
		},
		Dimensions: map[string]Dimension{
			"burstiness":                  {Score: 0},
			"structure_fingerprint":       {Score: 0},
			"cross_paragraph_consistency": {Score: 0},
			"perplexity_proxy": {Stats: map[string]any{
				"ttr":                0.72,
				"normalized_entropy": 0.97,
			}},
		},
		LatestDetectorProxy: DetectorProxy{Components: map[string]Dimension{
			"probability_curvature_proxy":  {Score: 4, Stats: map[string]any{"human_anchor_adjusted_from": 100.0}},
			"weak_lm_uniformity":           {Score: 3, Stats: map[string]any{"human_anchor_adjusted_from": 96.0}},
			"local_entropy_uniformity":     {Score: 2, Stats: map[string]any{"human_anchor_adjusted_from": 90.0}},
			"layout_humanizer_fingerprint": {Score: 0},
			"narrative_dynamics":           {Score: 0},
		}},
	}

	score, evidence, hardGate := segmentAIGCProxy(report, 2935, 1)
	if score >= 50 {
		t.Fatalf("raw consensus without independent support must stay soft, got %.2f (%v)", score, evidence)
	}
	if hardGate {
		t.Fatalf("raw consensus without independent support marked a hard gate: %v", evidence)
	}
	if !strings.Contains(strings.Join(evidence, "\n"), "缺少独立叙事或结构风险") {
		t.Fatalf("expected unsupported-consensus diagnostic, got %v", evidence)
	}
}

func TestWholeTextCandidateBLowWeakCurveDoesNotCreateHardGate(t *testing.T) {
	report := Report{
		Stats: Stats{
			DialogueRatio: 0.33, ActionDensityPerK: 25,
			HumanAnchor: map[string]any{
				"eligible": true, "strength": "strong", "anchor_type": "narrative_scene",
				"blockers": []string{}, "segment_cap": 35.0,
			},
		},
		Dimensions: map[string]Dimension{
			"burstiness": {Score: 0}, "structure_fingerprint": {Score: 0}, "cross_paragraph_consistency": {Score: 0},
			"perplexity_proxy": {Stats: map[string]any{"ttr": 0.72, "normalized_entropy": 0.97}},
		},
		LatestDetectorProxy: DetectorProxy{Components: map[string]Dimension{
			"probability_curvature_proxy":  {Score: 4, Stats: map[string]any{"human_anchor_adjusted_from": 98.0}},
			"weak_lm_uniformity":           {Score: 3, Stats: map[string]any{"human_anchor_adjusted_from": 82.0}},
			"local_entropy_uniformity":     {Score: 2, Stats: map[string]any{"human_anchor_adjusted_from": 90.0}},
			"layout_humanizer_fingerprint": {Score: 0},
			"narrative_dynamics":           {Score: 0},
		}},
	}

	_, _, hardGate := segmentAIGCProxy(report, 2935, 1)
	if hardGate {
		t.Fatal("candidate-B raw 98/82/90 shape must not create a hard gate")
	}
}

func TestSingleSegmentRiskFloorRequiresWholeTextHardGate(t *testing.T) {
	proxy := ZhuqueSegmentProxy{
		Enabled:                 true,
		SuspectedAIRatioPercent: 100,
		MaxSegmentPercent:       95,
		Segments: []ZhuqueSegment{{
			Index: 1, Proportion: 1, AIGCPercent: 95,
		}},
	}
	if got := zhuqueSegmentRiskFloor(proxy, 100); got != 0 {
		t.Fatalf("non-hard single segment risk floor = %.2f, want 0", got)
	}
	report := Report{ZhuqueSegmentProxy: proxy}
	if got, ok := wholeTextSingleSegmentRisk(report); ok || got != 0 {
		t.Fatalf("non-hard max segment bypassed the gate: %.2f, %v", got, ok)
	}

	proxy.Segments[0].WholeTextHardGate = true
	if got := zhuqueSegmentRiskFloor(proxy, 100); got != 95 {
		t.Fatalf("verified hard floor = %.2f, want 95", got)
	}
}

func TestEffectiveGatePercentPreservesStoredHardGateOverExternalPass(t *testing.T) {
	capValue := 2.7
	report := Report{
		AIGCPercent:          2.0,
		WholeTextSegmentGate: 79.07,
		HumanAnchorFinalCap:  &capValue,
	}
	if got := EffectiveGatePercent(report); got != 79.07 {
		t.Fatalf("stored whole-text floor must override external/human cap, got %.2f", got)
	}
	if _, ok := HumanAnchorFinalCap(report); ok {
		t.Fatal("whole-text composite floor must invalidate a stored human-anchor cap")
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
			"local_entropy_uniformity":     {Score: 24, Stats: map[string]any{"human_anchor_adjusted_from": 50.0}},
			"layout_humanizer_fingerprint": {Score: 0},
		}},
	}

	score, evidence, _ := segmentAIGCProxy(report, 2800, 1)
	if score < 80 {
		t.Fatalf("two independent high-risk signals should override the anchor, got %.2f (%v)", score, evidence)
	}
}

func TestWholeTextBorderlineInterioritySignalStaysAdvisory(t *testing.T) {
	if wholeTextIndependentRiskSupport(46, 0, 0, 0) {
		t.Fatal("a lone borderline POV/interiority heuristic must not create a whole-text hard floor")
	}
	if !wholeTextIndependentRiskSupport(55, 0, 0, 0) {
		t.Fatal("compound narrative risk must still support a whole-text hard floor")
	}
	if !wholeTextIndependentRiskSupport(0, 65, 0, 0) {
		t.Fatal("strong structural evidence must still support a whole-text hard floor")
	}
	if wholeTextIndependentRiskSupport(0, 0, 38, 30) {
		t.Fatal("two mid-level scores derived from the same paragraph-variance feature must not form independent hard support")
	}
	if !wholeTextIndependentRiskSupport(0, 0, 38, 45) {
		t.Fatal("burstiness plus strong cross-paragraph evidence must still support a whole-text hard floor")
	}
}
