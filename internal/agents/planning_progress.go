package agents

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"regexp"
)

const (
	PlanningContextBound         = "context_bound"
	PlanningProposalCommitted    = "proposal_committed"
	PlanningArbitrationCommitted = "arbitration_committed"
	PlanningCycleCommitted       = "cycle_committed"
	PlanningReadinessCommitted   = "readiness_committed"
	PlanningBundleCommitted      = "bundle_committed"
)

// DurablePlanningProgress is a host-only observation, not model input or a
// business receipt. It carries no actor names, choices, messages or text.
// ContextBound sets the target only; it is never business progress.
type DurablePlanningProgress struct {
	GenerationID   string
	Chapter        int
	Cycle          int
	Round          int
	Kind           string
	ArtifactDigest string
}

var planningProgressGeneration = regexp.MustCompile(`^pg2_[A-Za-z0-9_-]{1,120}$`)
var planningProgressDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (event DurablePlanningProgress) EventID() (string, error) {
	if !planningProgressGeneration.MatchString(event.GenerationID) || event.Chapter <= 0 || event.Cycle < 0 || event.Cycle > 64 || event.Round < 0 || event.Round > 2 || !planningProgressDigest.MatchString(event.ArtifactDigest) {
		return "", fmt.Errorf("invalid durable planning progress identity")
	}
	switch event.Kind {
	case PlanningContextBound, PlanningProposalCommitted, PlanningArbitrationCommitted, PlanningCycleCommitted, PlanningReadinessCommitted, PlanningBundleCommitted:
	default:
		return "", fmt.Errorf("unsupported durable planning progress event")
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fmt.Sprintf("planning-progress.v1\x00%s\x00%d\x00%d\x00%d\x00%s\x00%s", event.GenerationID, event.Chapter, event.Cycle, event.Round, event.Kind, event.ArtifactDigest)))), nil
}

// Observability must never fail a paid operation, cancel a model or alter retry
// policy. A failed callback reports only a fixed message and an error digest.
func reportDurablePlanningProgress(ctx context.Context, event DurablePlanningProgress) {
	hook := projectedAccounting(ctx).DurableProgress
	if hook == nil {
		return
	}
	_, err := event.EventID()
	if err == nil {
		err = hook(event)
	}
	if err != nil {
		slog.Warn("durable planning progress observer unavailable", "diagnostic", fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(err.Error()))))
	}
}
