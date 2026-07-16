package tools

import (
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestRenderRequiredOutcomesDoesNotSampleHardBeatsByCharacterVisibility(t *testing.T) {
	want := []string{
		"开场暴雨压住院门",
		"沈知遥独自收起旧伞",
		"贺骁完成登记核验",
		"顾岚当众改口，站到主角这边",
		"章末铁门彻底落锁",
	}
	plan := domain.ChapterPlan{
		Contract: domain.ChapterContract{RequiredBeats: want},
		CausalSimulation: domain.ChapterCausalSimulation{OffscreenStage: []domain.CharacterStageRecord{
			{Character: "沈知遥", VisibleInChapter: false},
			{Character: "贺骁", VisibleInChapter: false},
			{Character: "顾岚", VisibleInChapter: true},
		}},
	}

	got := RenderRequiredOutcomes(plan)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hard outcomes were sampled by character visibility: got %#v want %#v", got, want)
	}
}
