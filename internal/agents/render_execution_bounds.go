package agents

import (
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Render execution is a small, frozen state machine: one coordinator route,
// one chapter-scoped prose worker, and a durable tool boundary.  It must not
// inherit the much larger interactive-writing limits, because every failed
// request can otherwise wait for the provider timeout and retry seven times.
const (
	renderCoordinatorMaxTurns    = 12
	renderDrafterMaxTurns        = 4
	renderFinalizerMaxTurns      = 6
	renderModelMaxRetries        = 1
	renderCoordinatorCallTimeout = 2 * time.Minute
	// One Drafter Generate call may include the control decision, full prose,
	// and the single word-contract correction. Live C5 calls finished near four
	// minutes but the old four-minute total envelope also included the preceding
	// context/control turn, causing a completed prose request to be cancelled and
	// retried from zero. Five minutes remains a hard bound while giving prose
	// roughly 4m45s and is faster in the observed tail than a second 4m attempt.
	renderDrafterCallTimeout = 5 * time.Minute
)

type agentExecutionBounds struct {
	Render                 bool
	CoordinatorTurns       int
	DrafterTurns           int
	FinalizerTurns         int
	ModelMaxRetries        int
	CoordinatorCallTimeout time.Duration
	DrafterCallTimeout     time.Duration
}

func currentAgentExecutionBounds(st *store.Store) agentExecutionBounds {
	bounds := agentExecutionBounds{
		CoordinatorTurns: 100_000,
		ModelMaxRetries:  subagentMaxRetries,
	}
	if st == nil {
		return bounds
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil || lock.Mode != domain.PipelineExecutionRender {
		return bounds
	}
	bounds.Render = true
	bounds.CoordinatorTurns = renderCoordinatorMaxTurns
	bounds.DrafterTurns = renderDrafterMaxTurns
	bounds.FinalizerTurns = renderFinalizerMaxTurns
	bounds.ModelMaxRetries = renderModelMaxRetries
	bounds.CoordinatorCallTimeout = renderCoordinatorCallTimeout
	bounds.DrafterCallTimeout = renderDrafterCallTimeout
	return bounds
}
