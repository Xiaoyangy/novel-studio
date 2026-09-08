package tools

import (
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// The model cannot opt into this mode. The host supplies a validated session
// and all inputs must already exist in that cycle's proof namespace before a
// model is dispatched. Tool schemas and actor identity remain unchanged.
func NewSubmitCharacterActivationDecisionTool(st *store.Store, session domain.CharacterActivationSession, observation domain.CharacterObservationPacket) (*SubmitCharacterDecisionTool, error) {
	if st == nil {
		return nil, fmt.Errorf("character activation store is unavailable")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return nil, err
	}
	stored, err := proofs.LoadObservation(observation.GenerationID, observation.Chapter, observation.Round, observation.AgentID)
	if err != nil {
		return nil, err
	}
	verified, err := domain.FinalizeCharacterObservationPacket(observation)
	if err != nil || stored == nil || stored.Digest != observation.Digest || verified.Digest != observation.Digest {
		return nil, fmt.Errorf("character activation tool lacks its exact persisted observation")
	}
	t := NewSubmitCharacterDecisionTool(st, observation)
	t.proofs = proofs
	return t, nil
}

func NewResolveCharacterActivationTool(st *store.Store, session domain.CharacterActivationSession, stimulus domain.WorldStimulusPacket, activation domain.CharacterAgentActivation, proposals []domain.CharacterDecisionProposal, protocolDigest string, sources []string, maxRevisionRounds int) (*ResolveChapterWorldTool, error) {
	if st == nil || maxRevisionRounds < 0 || maxRevisionRounds > 1 {
		return nil, fmt.Errorf("character activation requires a store and at most one revision")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return nil, err
	}
	storedStimulus, err := proofs.LoadStimulus(stimulus.GenerationID, stimulus.Chapter)
	if err != nil {
		return nil, err
	}
	storedActivation, err := proofs.LoadActivation(activation.GenerationID, activation.Chapter)
	if err != nil {
		return nil, err
	}
	if storedStimulus == nil || storedActivation == nil || !sameActivationToolValue(storedStimulus, stimulus) || !sameActivationToolValue(storedActivation, activation) {
		return nil, fmt.Errorf("activation arbiter lacks its exact persisted stimulus/activation")
	}
	seen := map[string]bool{}
	for _, proposal := range proposals {
		if seen[proposal.AgentID] {
			return nil, fmt.Errorf("activation arbiter has duplicate actor proposals")
		}
		seen[proposal.AgentID] = true
		stored, err := proofs.LoadProposal(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID)
		if err != nil {
			return nil, err
		}
		if stored == nil || !sameActivationToolValue(stored, proposal) {
			return nil, fmt.Errorf("activation arbiter lacks its exact persisted proposal")
		}
	}
	for _, entry := range activation.Entries {
		if entry.State == domain.CharacterAgentActive {
			if !seen[entry.AgentID] {
				return nil, fmt.Errorf("activation arbiter is missing an active actor proposal")
			}
			delete(seen, entry.AgentID)
		}
	}
	if len(seen) != 0 || len(proposals) == 0 {
		return nil, fmt.Errorf("activation arbiter proposal set differs from active actors")
	}
	t := NewResolveChapterWorldTool(st, stimulus, activation, proposals, protocolDigest, sources, maxRevisionRounds)
	t.cycleProofs = proofs
	return t, nil
}

func sameActivationToolValue(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && string(left) == string(right)
}

// The source-aware store view resolves old authorization at its ORIGINAL
// location. No synthetic proposal is inserted into the current proof paths.
func NewResolveCharacterContinuationTool(st *store.Store, session domain.CharacterActivationSession, view *store.CharacterContinuationArbitration, protocol string) (*ResolveChapterWorldTool, error) {
	if st == nil || view == nil {
		return nil, fmt.Errorf("continuation arbiter requires its verified store source view")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		return nil, err
	}
	input := view.Input()
	if input.Stimulus.GenerationID != session.GenerationID || input.Stimulus.Chapter != session.Chapter {
		return nil, fmt.Errorf("continuation arbiter source scope differs from its session")
	}
	t := NewResolveChapterWorldTool(st, input.Stimulus, input.Activation, view.EffectiveProposals(), protocol, input.Stimulus.Sources, 1)
	t.cycleProofs, t.continuationProof = proofs, view
	return t, nil
}

// The one-shot schema remains byte-for-byte unchanged. Only the host-bound
// cycle contract can omit chapter POV synthesis; Execute still rejects cycle
// packets sent through the one-shot constructor.
func (t *ResolveChapterWorldTool) characterActivationArbitrationSchema(result map[string]any) map[string]any {
	if !domain.HasCharacterActivationCycleContract(t.stimulus) {
		return result
	}
	required := result["required"].([]string)
	filtered := make([]string, 0, len(required)-1)
	for _, name := range required {
		if name != "protagonist_projection" {
			filtered = append(filtered, name)
		}
	}
	result["required"] = filtered
	projection := result["properties"].(map[string]any)["protagonist_projection"].(map[string]any)
	projection["description"] = "章内单周期应省略此字段；宿主在完整周期链结束后从真正 POV 的实际选择汇总整章。不得唤醒休眠主角、虚构其提案或把活跃配角改称全书主角。若提供，仍须完整绑定本轮真实提案。"
	return result
}

// This boundary intentionally does not save a ChapterWorldSimulation, consume
// its one-shot planning receipt, delete chapter partials, advance checkpoints,
// publish memory or activate an Architect successor. The chapter controller
// owns those effects only after the whole cycle chain has been assessed.
func (t *ResolveChapterWorldTool) persistActivationArbitration(receipt domain.WorldArbitrationReceipt) (json.RawMessage, error) {
	if receipt.Finalized {
		var physical domain.WorldPhysicalStateV2
		var err error
		if t.roundSourcesV3 != nil {
			verified, verifyErr := domain.FinalizeCharacterArbitrationRoundV1(*t.roundSourcesV3, receipt)
			err = verifyErr
			if err == nil {
				physical = verified.AfterState()
			}
		} else {
			physical, err = domain.ApplyArbitrationPhysicalStateV2(receipt, t.stimulus, t.proposals...)
		}
		if err != nil {
			return nil, err
		}
		decisions, err := receipt.CharacterDecisions(t.proposals, physical)
		if err != nil {
			return nil, err
		}
		if err := validateIncomingSimulationSemanticInvariants(t.store, receipt.Chapter, decisions, receipt.ProtagonistProjection, nil); err != nil {
			return nil, fmt.Errorf("resolved activation decisions are invalid: %w", err)
		}
	}
	save := func(value domain.WorldArbitrationReceipt) error {
		return t.cycleProofs.SaveArbitration(value, t.stimulus, t.activation, t.proposals, t.maxRevisionRounds)
	}
	load := func() (*domain.WorldArbitrationReceipt, error) {
		return t.cycleProofs.LoadArbitration(receipt.GenerationID, receipt.Chapter, receipt.Round)
	}
	if t.continuationProof != nil {
		save, load = t.continuationProof.SaveArbitration, t.continuationProof.LoadArbitration
	}
	if t.arbitrationV3 != nil {
		save = t.arbitrationV3.SaveArbitration
		load = func() (*domain.WorldArbitrationReceipt, error) {
			return t.arbitrationV3.LoadArbitration(receipt.Round)
		}
	}
	if err := save(receipt); err != nil {
		original, loadErr := load()
		if loadErr != nil {
			return nil, loadErr
		}
		if original == nil {
			return nil, err
		}
		receipt.GeneratedAt = original.GeneratedAt
		retry, finalizeErr := t.finalizeBoundArbitration(receipt)
		if finalizeErr != nil || retry.Digest != original.Digest {
			return nil, err
		}
		receipt = *original
	}
	var affected []string
	for _, conflict := range receipt.Conflicts {
		if !conflict.Resolved {
			affected = append(affected, conflict.AffectedAgentIDs...)
		}
	}
	return json.Marshal(map[string]any{
		"resolved": receipt.Finalized, "cycle_closed": receipt.Finalized || receipt.HardContractStatus == "infeasible",
		"chapter": receipt.Chapter, "cycle": t.cycleProofs.ActivationCycleIndex(), "round": receipt.Round,
		"arbitration_digest": receipt.Digest, "affected_agent_ids": compactStrings(affected),
	})
}
