package domain

import (
	"strings"
	"testing"
)

func testRenderCapacity(targets ...int) ChapterRenderCapacity {
	scenes := make([]ChapterRenderSceneUnit, 0, len(targets))
	total := 0
	for i, target := range targets {
		total += target
		scenes = append(scenes, ChapterRenderSceneUnit{
			SceneID:          "scene-" + string(rune('a'+i)),
			TargetRunes:      target,
			POVObjective:     "林澈要在现场拿到可核验的选择权",
			ActiveOpposition: "对方用截止时间和合同条款逼他立刻答应",
			Turn:             "一张盖错章的回执暴露了对方不能公开的条件",
			ExitConsequence:  "林澈带走回执，对方只能改到下一场重新谈",
			ConcreteActionBeats: []string{
				"林澈把合同翻到盖章页",
				"沈知遥用指甲压住错误日期",
				"对方伸手抽回文件却被回执夹住",
			},
		})
	}
	return ChapterRenderCapacity{
		TotalTargetRunes:  total,
		SceneUnits:        scenes,
		AntiPaddingPolicy: "重复手续一笔带过；字数只用于加深选择、阻力、人物反应和可见后果。",
	}
}

func TestChapterRenderCapacityValidatesNaturalBudget(t *testing.T) {
	capacity := testRenderCapacity(700, 700, 700)
	if err := capacity.Validate(2000, 3300); err != nil {
		t.Fatalf("valid 2100-rune capacity rejected: %v", err)
	}
}

func TestChapterRenderCapacityRejectsThinOrSkewedScenes(t *testing.T) {
	for name, test := range map[string]struct {
		capacity ChapterRenderCapacity
		needle   string
	}{
		"too few scenes": {testRenderCapacity(1000, 1000), "scene_units=2"},
		"tiny scene":     {testRenderCapacity(299, 900, 900), "target_runes=299"},
		"bloated scene":  {testRenderCapacity(1401, 400, 400), "target_runes=1401"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test.capacity.Validate(2000, 3300); err == nil || !strings.Contains(err.Error(), test.needle) {
				t.Fatalf("invalid capacity accepted or wrong error: %v", err)
			}
		})
	}
}

func TestChapterRenderCapacityRejectsFalseAggregateAndPadding(t *testing.T) {
	capacity := testRenderCapacity(700, 700, 700)
	capacity.TotalTargetRunes++
	if err := capacity.Validate(2000, 3300); err == nil || !strings.Contains(err.Error(), "does not equal") {
		t.Fatalf("false total accepted: %v", err)
	}

	capacity = testRenderCapacity(600, 600, 600)
	if err := capacity.Validate(2000, 3300); err == nil || !strings.Contains(err.Error(), "outside user_rules.chapter_words") {
		t.Fatalf("under-budget capacity accepted: %v", err)
	}

	capacity = testRenderCapacity(700, 700, 700)
	capacity.SceneUnits[0].ConcreteActionBeats = []string{"看", "看", "看", "又看"}
	if err := capacity.Validate(2000, 3300); err == nil || !strings.Contains(err.Error(), "concrete unique beats") {
		t.Fatalf("non-concrete/repeated action beats accepted: %v", err)
	}
}
