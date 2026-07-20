package agents

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCurrentAgentExecutionBoundsAreTightOnlyDuringFrozenRender(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	ordinary := currentAgentExecutionBounds(st)
	if ordinary.Render || ordinary.CoordinatorTurns != 100_000 ||
		ordinary.ModelMaxRetries != subagentMaxRetries || ordinary.DrafterTurns != 0 || ordinary.FinalizerTurns != 0 {
		t.Fatalf("ordinary execution limits changed: %+v", ordinary)
	}

	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 5,
		PlanDigest:    "sha256:frozen-plan",
		Owner:         "render-bounds-test",
	}); err != nil {
		t.Fatal(err)
	}
	render := currentAgentExecutionBounds(st)
	if !render.Render || render.CoordinatorTurns != renderCoordinatorMaxTurns ||
		render.DrafterTurns != renderDrafterMaxTurns || render.FinalizerTurns != renderFinalizerMaxTurns ||
		render.ModelMaxRetries != renderModelMaxRetries ||
		render.CoordinatorCallTimeout != renderCoordinatorCallTimeout ||
		render.DrafterCallTimeout != renderDrafterCallTimeout {
		t.Fatalf("frozen render limits not applied: %+v", render)
	}
}
