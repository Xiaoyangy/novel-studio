package main

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestInitialWorldTickPromptPinsReadOnlyChapterOneWorldContext(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Arcs: []domain.ArcOutline{{Index: 1, Chapters: []domain.OutlineEntry{{Chapter: 1, CoreEvent: "本人在第一章依据可见来源自主决定是否读取原件", Hook: "对方保留是否回应的选择"}}}}}}); err != nil {
		t.Fatal(err)
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := pipelineInitialWorldTickPrompt(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`novel_context(chapter=1, profile="world_simulation")`, "原样转交", "只读上下文定位", "不得提前执行第1章", contract.Block} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("INITIAL_TICK_CONTEXT_SCOPE_MISSING: host prompt lacks %q", want)
		}
	}
	if err := tools.ValidateInitialWorldTickDispatchTask(prompt, contract); err != nil {
		t.Fatal("existing exact dispatch block changed", err)
	}
}
