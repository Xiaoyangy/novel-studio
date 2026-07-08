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

func TestEffectiveGatePercentUsesHumanAnchorFinalCap(t *testing.T) {
	capValue := 4.8
	report := Report{
		AIGCPercent:         80,
		BlendedAIGCPercent:  4.8,
		SegmentRiskFloor:    80,
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
