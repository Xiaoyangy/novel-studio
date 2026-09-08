package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func characterExecutionProofs(st *store.Store, sessions ...*domain.CharacterActivationSession) (*store.CharacterAgentStore, *domain.CharacterActivationSession, error) {
	if st == nil || st.CharacterAgents == nil || len(sessions) > 1 {
		return nil, nil, fmt.Errorf("character execution has invalid store/session binding")
	}
	if len(sessions) == 0 || sessions[0] == nil {
		return st.CharacterAgents, nil, nil
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(*sessions[0])
	return proofs, sessions[0], err
}

func characterCyclePromptCacheKey(base string, proofs *store.CharacterAgentStore) string {
	if proofs.ActivationCycleIndex() == 0 {
		return base
	}
	return agentPromptCacheKey(base, proofs.ActivationCycleSourceToken())
}

// Both historical single-chapter execution and a new activation cycle use the
// same real character worker pool and arbiter. The caller, not this protocol,
// decides whether a hard conflict needs an Architect successor or assessment.
func runCharacterAgentDecisionProtocol(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, inputs characterAgentChapterInputs) ([]domain.CharacterDecisionProposal, *domain.WorldArbitrationReceipt, error) {
	if models == nil {
		return nil, nil, fmt.Errorf("character decision protocol requires models")
	}
	proofs, _, err := characterExecutionProofs(st, inputs.CycleSession)
	if err != nil {
		return nil, nil, err
	}
	activeIDs := activeCharacterAgentIDs(inputs.Activation)
	if len(activeIDs) == 0 {
		return nil, nil, fmt.Errorf("character-agent activation has no active character")
	}
	freshIDs := activeIDs
	if inputs.ContinuationSelection != nil {
		freshIDs = inputs.ContinuationSelection.FreshAgentIDs
	}
	proposals, err := runCharacterProposalRound(ctx, cfg, st, models, inputs.Observations, freshIDs, 1, inputs.CycleSession)
	if err != nil {
		return nil, nil, err
	}
	if inputs.ContinuationSelection != nil && len(inputs.ContinuationSelection.Continuations) > 0 {
		inputs.ContinuationProof, err = st.NewCharacterContinuationArbitration(*inputs.CycleSession, inputs.ContinuationSelection.Continuations, characterActivationProtocolForStimulus(inputs.Stimulus))
		if err != nil {
			return nil, nil, err
		}
		proposals = inputs.ContinuationProof.EffectiveProposals()
	}
	receipt, err := runWorldArbitration(ctx, cfg, st, models, inputs, proposals)
	if err != nil {
		return nil, nil, err
	}
	if receipt.Finalized || receipt.HardContractStatus == "infeasible" {
		return proposals, receipt, nil
	}
	if cfg.CharacterAgents.MaxRevisionRounds < 1 {
		return nil, nil, fmt.Errorf("world arbitration requires a revision but revisions are disabled")
	}
	feedback := arbitrationFeedbackByAgent(*receipt)
	revisions := make(map[string]domain.CharacterObservationPacket, len(feedback))
	revisionIDs := make([]string, 0, len(feedback))
	for agentID, items := range feedback {
		base, ok := inputs.Observations[agentID]
		if !ok {
			return nil, nil, fmt.Errorf("arbiter requested unknown character revision %s", agentID)
		}
		base.Round, base.ConflictFeedback, base.Digest = 2, items, ""
		stored, err := proofs.LoadObservation(base.GenerationID, base.Chapter, 2, agentID)
		if err != nil {
			return nil, nil, err
		}
		base.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if stored != nil {
			base.GeneratedAt = stored.GeneratedAt
		}
		revised, err := domain.FinalizeCharacterObservationPacket(base)
		if err != nil {
			return nil, nil, err
		}
		if stored != nil && revised.Digest != stored.Digest {
			return nil, nil, fmt.Errorf("persisted character revision differs from its original observation and minimal conflict feedback")
		}
		if err := proofs.SaveObservation(revised); err != nil {
			return nil, nil, err
		}
		revisions[agentID] = revised
		revisionIDs = append(revisionIDs, agentID)
	}
	if len(revisionIDs) == 0 {
		return nil, nil, fmt.Errorf("non-final arbitration supplied no affected character")
	}
	if _, err := runCharacterProposalRound(ctx, cfg, st, models, revisions, revisionIDs, 2, inputs.CycleSession); err != nil {
		return nil, nil, err
	}
	proposals, err = proofs.LoadLatestProposals(inputs.Stimulus.GenerationID, inputs.Stimulus.Chapter, 2, activeIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(proposals) != len(activeIDs) {
		return nil, nil, fmt.Errorf("revised character proposal set is incomplete")
	}
	receipt, err = runWorldArbitration(ctx, cfg, st, models, inputs, proposals)
	if err != nil {
		return nil, nil, err
	}
	if !receipt.Finalized && receipt.HardContractStatus != "infeasible" {
		return nil, nil, fmt.Errorf("world arbitration did not close after its single revision")
	}
	return proposals, receipt, nil
}

// This is the actual single-cycle adapter for ChapterActivationDriver. Input
// construction, readiness and whole-chapter publication stay separate: no
// partial cycle becomes accepted memory or a ChapterWorldSimulation here.
func runCharacterActivationCycle(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, session domain.CharacterActivationSession, inputs characterAgentChapterInputs) (domain.CharacterActivationCycle, error) {
	var empty domain.CharacterActivationCycle
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if st == nil || st.CharacterAgents == nil || models == nil || cfg.CharacterAgents.MaxRevisionRounds < 0 || cfg.CharacterAgents.MaxRevisionRounds > 1 {
		return empty, fmt.Errorf("activation cycle requires a store, models and at most one conflict revision")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return empty, err
	}
	stimulus, err := proofs.LoadStimulus(session.GenerationID, session.Chapter)
	if err != nil {
		return empty, err
	}
	activation, err := proofs.LoadActivation(session.GenerationID, session.Chapter)
	if err != nil {
		return empty, err
	}
	if stimulus == nil || activation == nil || !sameCharacterCycleValue(*stimulus, inputs.Stimulus) || !sameCharacterCycleValue(*activation, inputs.Activation) {
		return empty, fmt.Errorf("activation cycle inputs are not bound to their persisted session evidence")
	}
	inputs.CycleSession = &session
	inputSnapshot, err := proofs.LoadActivationInputs()
	if err != nil {
		return empty, err
	}
	inputDigest := ""
	if inputSnapshot != nil {
		inputDigest = inputSnapshot.Digest
	}
	if hasCharacterActivationPolicyV3(inputs.Stimulus.Sources) {
		return runCharacterActivationCycleV3(ctx,cfg,st,models,session,inputs,inputSnapshot)
	}
	if err := prepareCharacterWorkContinuationSelection(st, &inputs, inputSnapshot, session); err != nil {
		return empty, err
	}
	_, receipt, err := runCharacterAgentDecisionProtocol(ctx, cfg, st, models, inputs)
	if err != nil {
		return empty, err
	}
	if inputs.ContinuationSelection != nil && len(inputs.ContinuationSelection.Continuations) > 0 {
		view, err := st.NewCharacterContinuationArbitration(session, inputs.ContinuationSelection.Continuations, characterActivationProtocolForStimulus(inputs.Stimulus))
		if err != nil {
			return empty, err
		}
		return view.FinalizeCycle(*receipt)
	}
	registry, err := proofs.LoadRegistrySnapshot(session.GenerationID, session.Chapter)
	if err != nil {
		return empty, err
	}
	if registry == nil {
		return empty, fmt.Errorf("activation cycle lost its registry snapshot")
	}
	evidence := domain.CharacterAgentEvidenceBundle{
		GenerationID: session.GenerationID, Chapter: session.Chapter, Registry: *registry,
		Stimulus: *stimulus, Activation: *activation,
		ProtocolDigest: characterActivationProtocolForStimulus(inputs.Stimulus),
	}
	for round := 1; round <= receipt.Round; round++ {
		for _, agentID := range activeCharacterAgentIDs(*activation) {
			observation, err := proofs.LoadObservation(session.GenerationID, session.Chapter, round, agentID)
			if err != nil {
				return empty, err
			}
			if observation == nil {
				continue
			} // Only affected actors revise.
			proposal, err := proofs.LoadProposal(session.GenerationID, session.Chapter, round, agentID)
			if err != nil {
				return empty, err
			}
			if proposal == nil {
				return empty, fmt.Errorf("activation cycle has an observation without its proposal")
			}
			evidence.Observations = append(evidence.Observations, *observation)
			evidence.Proposals = append(evidence.Proposals, *proposal)
			if observation.MemoryRoot != "" {
				evidence.MemoryRoots = append(evidence.MemoryRoots, observation.MemoryRoot)
			}
		}
		arbitration, err := proofs.LoadArbitration(session.GenerationID, session.Chapter, round)
		if err != nil {
			return empty, err
		}
		if arbitration == nil {
			return empty, fmt.Errorf("activation cycle is missing an arbitration round")
		}
		evidence.Arbitrations = append(evidence.Arbitrations, *arbitration)
	}
	finalize := domain.FinalizeCharacterAgentEvidenceBundle
	if receipt.HardContractStatus == "infeasible" {
		finalize = domain.FinalizeCharacterHardConflictEvidenceBundle
	}
	evidence, err = finalize(evidence)
	if err != nil {
		return empty, err
	}
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	return domain.FinalizeCharacterActivationCycle(domain.CharacterActivationCycle{
		GenerationID: session.GenerationID, Chapter: session.Chapter, Index: proofs.ActivationCycleIndex(),
		PreviousDigest: previous, ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: inputDigest, Evidence: evidence,
	})
}

func sameCharacterCycleValue(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}
