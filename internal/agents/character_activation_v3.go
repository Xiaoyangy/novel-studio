package agents

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func runCharacterActivationCycleV3(ctx context.Context, cfg bootstrap.Config, st *store.Store, models *bootstrap.ModelSet, session domain.CharacterActivationSession, inputs characterAgentChapterInputs, frozen *domain.CharacterActivationInputSet) (domain.CharacterActivationCycle, error) {
	var empty domain.CharacterActivationCycle
	if frozen == nil {
		return empty, fmt.Errorf("v3 requires immutable current inputs")
	}
	policy := characterActivationPolicyForStimulus(frozen.Stimulus)
	if !domain.CharacterActivationUsesRoundSources(policy) {
		return empty, fmt.Errorf("source-bound arbitration requires v3/v4 execution")
	}
	if err := validateCharacterActivationInputPolicy(*frozen, session, policy); err != nil {
		return empty, err
	}
	view, err := st.LoadCharacterArbitrationV3(session.GenerationID, session.Chapter)
	if err != nil {
		return empty, err
	}
	if view == nil {
		prefix, err := st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
		if err != nil {
			return empty, err
		}
		if prefix == nil || !sameCharacterCycleValue(prefix.Session(), session) {
			return empty, fmt.Errorf("v3 selection lacks exact verified prefix")
		}
		var continuations []domain.CharacterWorkContinuationReceiptV1
		for _, id := range activeCharacterAgentIDs(frozen.Activation) {
			eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(*prefix, *frozen, id)
			if err != nil {
				return empty, err
			}
			if eligible.Eligible {
				if eligible.Receipt == nil {
					return empty, fmt.Errorf("v3 eligibility lost receipt")
				}
				continuations = append(continuations, *eligible.Receipt)
			}
		}
		view, err = st.PrepareCharacterArbitrationV3(session, continuations, characterActivationProtocolForPolicy(policy))
		if err != nil {
			return empty, err
		}
	}
	if !sameCharacterCycleValue(view.Input(), *frozen) {
		return empty, fmt.Errorf("v3 admission differs from frozen current input")
	}
	continued := map[string]bool{}
	for _, c := range view.Continuations() {
		continued[c.AgentID] = true
	}
	var freshIDs []string
	for _, id := range activeCharacterAgentIDs(frozen.Activation) {
		if !continued[id] {
			freshIDs = append(freshIDs, id)
		}
	}
	if _, err := runCharacterProposalRound(ctx, cfg, st, models, inputs.Observations, freshIDs, 1, &session); err != nil {
		return empty, err
	}
	inputs.ArbitrationV3, inputs.ArbitrationRoundV3 = view, 1
	sources, err := view.Sources(1)
	if err != nil {
		return empty, err
	}
	receipt, err := runWorldArbitration(ctx, cfg, st, models, inputs, sources.EffectiveProposals())
	if err != nil {
		return empty, err
	}
	if !receipt.Finalized && receipt.HardContractStatus != "infeasible" {
		if cfg.CharacterAgents.MaxRevisionRounds < 1 {
			return empty, fmt.Errorf("v3 R1 requires a revision but revisions are disabled")
		}
		proofs, err := st.CharacterAgents.ForActivationCycle(session)
		if err != nil {
			return empty, err
		}
		revisions := map[string]domain.CharacterObservationPacket{}
		var ids []string
		for id, base := range inputs.Observations {
			feedback := domain.CharacterArbitrationFeedbackForOwnerV1(*receipt, id, frozen.Stimulus.Sources)
			if len(feedback) == 0 {
				continue
			}
			base.Round, base.ConflictFeedback, base.Digest = 2, feedback, ""
			base.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
			stored, err := proofs.LoadObservation(session.GenerationID, session.Chapter, 2, id)
			if err != nil {
				return empty, err
			}
			if stored != nil {
				base.GeneratedAt = stored.GeneratedAt
			}
			base, err = domain.FinalizeCharacterObservationPacket(base)
			if err != nil {
				return empty, err
			}
			if stored != nil && !sameCharacterCycleValue(base, *stored) {
				return empty, fmt.Errorf("v3 stored O2 differs from frozen owner-safe feedback")
			}
			if err := view.SaveObservation(base); err != nil {
				return empty, err
			}
			revisions[id], ids = base, append(ids, id)
		}
		if len(ids) == 0 {
			return empty, fmt.Errorf("v3 nonfinal R1 has no affected owner")
		}
		sort.Strings(ids)
		if _, err := runCharacterProposalRound(ctx, cfg, st, models, revisions, ids, 2, &session); err != nil {
			return empty, err
		}
		sources, err = view.Sources(2)
		if err != nil {
			return empty, err
		}
		inputs.ArbitrationRoundV3 = 2
		if _, err := runWorldArbitration(ctx, cfg, st, models, inputs, sources.EffectiveProposals()); err != nil {
			return empty, err
		}
	}
	// Usage is already durable in the book's single accounting WAL. This step
	// does not synthesize calls for continuers or turn callbacks into paid work.
	return view.FinalizeCycle(nil)
}

func validateCharacterDispatchV3(st *store.Store, session *domain.CharacterActivationSession, view *store.CharacterArbitrationV3, o domain.CharacterObservationPacket) error {
	if session == nil || view == nil || !view.BelongsTo(st) {
		return fmt.Errorf("v3 character dispatch lacks immutable Store admission")
	}
	input := view.Input()
	if input.Stimulus.Digest != o.StimulusDigest || input.Stimulus.GenerationID != session.GenerationID || input.Stimulus.Chapter != session.Chapter {
		return fmt.Errorf("v3 dispatch changed source identity")
	}
	active := false
	for _, e := range input.Activation.Entries {
		if e.AgentID == o.AgentID && e.State == domain.CharacterAgentActive {
			active = true
		}
	}
	if !active {
		return fmt.Errorf("v3 cannot dispatch an inactive owner")
	}
	if o.Round == 1 {
		for _, c := range view.Continuations() {
			if c.AgentID == o.AgentID {
				return fmt.Errorf("v3 cannot requery an admitted continuer")
			}
		}
		return nil
	}
	if o.Round != 2 {
		return fmt.Errorf("v3 character round outside bounded revision")
	}
	r1, err := view.LoadArbitration(1)
	if err != nil {
		return err
	}
	if r1 == nil || r1.Finalized || r1.HardContractStatus != "feasible" || len(domain.CharacterArbitrationFeedbackForOwnerV1(*r1, o.AgentID, input.Stimulus.Sources)) == 0 {
		return fmt.Errorf("v3 revision cannot dispatch an unaffected owner")
	}
	return nil
}
