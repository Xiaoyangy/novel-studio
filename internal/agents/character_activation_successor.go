package agents

import (
	"context"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

type characterActivationSuccessorCause struct {
	Readiness      domain.CharacterChapterReadiness
	Conflicts      []string
	NonNegotiables []string
}

func characterActivationHardContractFailure(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, boundary ProjectedArcBoundary, failure CharacterActivationChapterConflictError) (*domain.ChapterWorldSimulation, *domain.Checkpoint, error) {
	session, err := st.LoadCharacterActivationSession(failure.GenerationID, failure.Chapter)
	if err != nil {
		return nil, nil, err
	}
	if session == nil || session.Phase != "hard_conflict" {
		return nil, nil, fmt.Errorf("activation successor lacks a completed hard-conflict session")
	}
	index := len(session.CycleDigests)
	audit, err := st.LoadCharacterReadinessReviewAudit(failure.GenerationID, failure.Chapter, index)
	if err != nil {
		return nil, nil, err
	}
	if audit == nil || audit.Receipt.Digest != failure.ReadinessDigest || audit.Receipt.Decision != "hard_conflict" {
		return nil, nil, fmt.Errorf("activation successor lacks its exact hard-conflict audit")
	}
	cycle, err := st.LoadCharacterActivationCycle(failure.GenerationID, failure.Chapter, index)
	if err != nil {
		return nil, nil, err
	}
	if cycle == nil || cycle.Digest != audit.Receipt.CycleDigest {
		return nil, nil, fmt.Errorf("activation successor has a different world cycle")
	}
	receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
	cause := &characterActivationSuccessorCause{Readiness: audit.Receipt, NonNegotiables: append([]string(nil), audit.Input.Context.HardContracts...)}
	contracts := map[string]string{}
	for _, requirement := range audit.Input.Requirements {
		contracts[requirement.ID] = requirement.Contract
		cause.NonNegotiables = append(cause.NonNegotiables, requirement.Contract)
	}
	for _, conflict := range audit.Receipt.UnresolvedHardContracts {
		if text := contracts[conflict]; text != "" {
			cause.Conflicts = append(cause.Conflicts, text)
		} else {
			cause.Conflicts = append(cause.Conflicts, conflict)
		}
	}
	inputs := characterAgentChapterInputs{Stimulus: cycle.Evidence.Stimulus, Activation: cycle.Evidence.Activation, Sources: cycle.Evidence.Stimulus.Sources}
	plan, err := runCharacterAgentSuccessorArchitectForCause(ctx, cfg, st, models, boundary, inputs, domain.LatestCharacterCycleProposals(cycle.Evidence), receipt, cause)
	if err != nil {
		return nil, nil, err
	}
	return nil, nil, &CharacterAgentHardContractConflictError{GenerationID: failure.GenerationID, Chapter: failure.Chapter, ArbitrationDigest: receipt.Digest, Conflicts: cause.Conflicts, SuccessorPlan: *plan}
}
