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

func TestNarrativeHumanAnchorAllowsFinalCapForStrongScenes(t *testing.T) {
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
	if !boolFromAny(anchor["final_cap_allowed"]) {
		t.Fatalf("expected strong narrative anchor to allow final cap: %+v", anchor)
	}

	stats.Repeated12Extra = 1
	anchor = humanAnchorStats(body, stats, []float64{8, 22, 10, 18, 9, 24, 11, 17}, map[string]float64{}, map[string]any{})
	if boolFromAny(anchor["final_cap_allowed"]) {
		t.Fatalf("expected repeated text to disable final cap: %+v", anchor)
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
	if floatFromAny(staged["score"]) >= floatFromAny(natural["score"]) {
		t.Fatalf("stuffed score %.2f should stay below natural %.2f", floatFromAny(staged["score"]), floatFromAny(natural["score"]))
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
