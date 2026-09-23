package main

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestInitialWorldTickRetryCapturesActualRejectionBeforeReset(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "许珩"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, CoreEvent: "许珩保留四十八小时保护窗口。", Hook: "次日上午前必须提交。"}}); err != nil {
		t.Fatal(err)
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	base := pipelineInitialWorldTickAttemptOptions(st.Dir(), contract.Block)
	if base.InitialWorldTickRetryFeedback != "" || base.Prompt != contract.Block {
		t.Fatal("empty baseline received invented rejection")
	}
	if _, err := st.WorldSim.AppendWorldEvents([]domain.WorldEvent{{TickID: "v1-a1", Chapter: 0, Actors: []string{"许珩"}, Summary: "许珩待办。", VisibilityChapter: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WorldSim.SaveTick(domain.WorldTick{TickID: "v1-a1", Volume: 1, Arc: 1, EventCount: 1}); err != nil {
		t.Fatal(err)
	}
	opts := pipelineInitialWorldTickAttemptOptions(st.Dir(), contract.Block)
	for _, needle := range []string{`时间锚点 "四十八小时"`, `时间锚点 "次日上午"`} {
		if !strings.Contains(opts.Prompt, needle) || !strings.Contains(opts.InitialWorldTickRetryFeedback, needle) {
			t.Fatalf("actual rejection not passed to both host and coordinator: %q", needle)
		}
	}
	if err := pipelineResetInvalidInitialWorldTick(st.Dir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(opts.InitialWorldTickRetryFeedback, `时间锚点 "四十八小时"`) {
		t.Fatal("reset lost captured rejection")
	}
	if err := tools.ValidateInitialWorldTickDispatchTask(opts.Prompt, contract); err != nil {
		t.Fatalf("retry altered exact dispatch: %v", err)
	}
}
