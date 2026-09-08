package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func verifiedWorkPrefixFixture(t *testing.T) (domain.VerifiedCharacterActivationPrefix, domain.CharacterActivationCycle) {
	t.Helper()
	origin, _, _, _ := workBatchFixture(t, false)
	e := origin.Evidence
	input := domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus, Activation: e.Activation, Observations: e.Observations}
	for _, observation := range e.Observations {
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: observation.AgentID, Character: observation.Character, GenerationID: observation.GenerationID, State: "projected"})
		continuationMust(t, err)
		input.Memories = append(input.Memories, memory)
	}
	input, err := domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	origin.InputSetDigest = input.Digest
	origin, err = domain.FinalizeCharacterActivationCycle(origin)
	continuationMust(t, err)
	session, err := domain.NewCharacterActivationSession(origin.GenerationID, origin.Chapter, origin.ChapterContextDigest, *e.Stimulus.PhysicalState, origin.StartDay, 8)
	continuationMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	continuationMust(t, err)
	_, prefix, err = domain.VerifyCharacterActivationStep(prefix, input, origin)
	continuationMust(t, err)
	prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, testutil.CycleReadiness(t, origin, "continue"))
	continuationMust(t, err)
	return prefix, origin
}

func verifiedWorkDraft(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, origin domain.CharacterActivationCycle) (domain.CharacterActivationInputSet, domain.CharacterActivationCycle) {
	t.Helper()
	steps := prefix.Steps()
	last := steps[len(steps)-1]
	session := prefix.Session()
	input := workBatchNextInput(t, origin, last.AfterState(), session.CurrentDay, len(steps)+1, last.GlobalRoot())
	fresh := workBatchFreshProposal(t, input)
	arbitration := workBatchArbitration(t, origin, input, fresh, 1, false)
	cycle := domain.CharacterActivationCycle{
		Version: domain.CharacterActivationCycleV2Version, GenerationID: origin.GenerationID, Chapter: origin.Chapter,
		Index: len(steps) + 1, PreviousDigest: last.GlobalRoot(), ChapterContextDigest: origin.ChapterContextDigest,
		InputSetDigest: input.Digest,
		Evidence: domain.CharacterAgentEvidenceBundle{
			Version: domain.CharacterContinuationEvidenceV2Version, GenerationID: origin.GenerationID, Chapter: origin.Chapter,
			Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation,
			Observations: input.Observations, Proposals: []domain.CharacterDecisionProposal{fresh},
			Arbitrations: []domain.WorldArbitrationReceipt{arbitration}, ProtocolDigest: origin.Evidence.ProtocolDigest,
		},
	}
	for _, observation := range input.Observations {
		cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, observation.MemoryRoot)
	}
	for _, proposal := range origin.Evidence.Proposals {
		ledger, ok := prefix.ContinuationLedger(proposal.AgentID)
		if !ok {
			t.Fatal("verified prefix lost the original independent grant")
		}
		eligible, err := domain.EvaluateCharacterWorkContinuationV1(ledger, input, prefix.ContinuationBoundaries()...)
		continuationMust(t, err)
		if !eligible.Eligible {
			t.Fatalf("verified source unexpectedly woke: %+v", eligible)
		}
		cycle.WorkContinuations = append(cycle.WorkContinuations, *eligible.Receipt)
	}
	return input, cycle
}

func TestVerifiedContinuationGlobalCycleReplaysMultipleOwnersWithoutSyntheticProposals(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	initial, err := domain.NewCharacterActivationSession(origin.GenerationID, origin.Chapter, origin.ChapterContextDigest, *origin.Evidence.Stimulus.PhysicalState, origin.StartDay, 8)
	continuationMust(t, err)
	original, _ := json.Marshal(origin.Evidence.Proposals)
	for index := 2; index <= 3; index++ {
		input, draft := verifiedWorkDraft(t, prefix, origin)
		cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
		continuationMust(t, err)
		step, next, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
		continuationMust(t, err)
		if len(step.EffectiveProposals()) != 3 || len(cycle.Evidence.Proposals) != 1 || len(cycle.WorkContinuations) != 2 || len(cycle.ContinuationEntryDigests) != 2 {
			t.Fatal("fresh proposals and unchanged source intent were conflated")
		}
		if domain.ValidateCharacterActivationCycle(cycle) == nil || domain.ValidateCharacterAgentEvidenceBundle(cycle.Evidence) == nil {
			t.Fatal("fresh-only validators accepted a source-less continuation envelope")
		}
		root, err := domain.CharacterContinuationCycleRootV2(cycle)
		continuationMust(t, err)
		if root != step.GlobalRoot() || root != next.ContinuationBoundaries()[index-2].GlobalRoot() {
			t.Fatal("cycle/predecessor/global boundary use different root formulas")
		}
		for _, proposal := range origin.Evidence.Proposals {
			found := false
			for _, actual := range step.EffectiveProposals() {
				if actual.AgentID == proposal.AgentID {
					actualBytes, _ := json.Marshal(actual)
					originalBytes, _ := json.Marshal(proposal)
					found = string(actualBytes) == string(originalBytes)
				}
			}
			if !found {
				t.Fatal("continuation rewrote or resigned an original proposal")
			}
			ledger, ok := next.ContinuationLedger(proposal.AgentID)
			if !ok || len(ledger.Entries) != index-1 {
				t.Fatal("global prefix did not retain the exact per-owner execution chain")
			}
			continuationMust(t, domain.ValidateCharacterWorkContinuationLedgerV1(ledger, next.ContinuationBoundaries()...))
		}
		prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(next, testutil.CycleReadiness(t, cycle, "continue"))
		continuationMust(t, err)
	}
	after, _ := json.Marshal(origin.Evidence.Proposals)
	if string(original) != string(after) {
		t.Fatal("source fixture was changed by verification")
	}
	// Restart authority comes from flat source artifacts, never a serialized
	// verified badge or recursively embedded ledgers/prefixes.
	var archive struct {
		Inputs []domain.CharacterActivationInputSet
		Cycles []domain.CharacterActivationCycle
	}
	for _, step := range prefix.Steps() {
		archive.Inputs = append(archive.Inputs, step.Input())
		archive.Cycles = append(archive.Cycles, step.Cycle())
	}
	raw, err := json.Marshal(archive)
	continuationMust(t, err)
	if strings.Contains(string(raw), `"ledger":`) || strings.Contains(string(raw), `"origin":`) {
		t.Fatal("flat persistence recursively duplicated source ledgers")
	}
	continuationMust(t, json.Unmarshal(raw, &archive))
	restored, err := domain.NewVerifiedCharacterActivationPrefix(initial)
	continuationMust(t, err)
	for i, cycle := range archive.Cycles {
		_, restored, err = domain.VerifyCharacterActivationStep(restored, archive.Inputs[i], cycle)
		continuationMust(t, err)
		restored, err = domain.ApplyVerifiedCharacterActivationReadiness(restored, testutil.CycleReadiness(t, cycle, "continue"))
		continuationMust(t, err)
	}
	if restored.Session().Digest != prefix.Session().Digest || restored.Steps()[2].GlobalRoot() != prefix.Steps()[2].GlobalRoot() {
		t.Fatal("flat restart changed the global state or root")
	}
}

func TestVerifiedContinuationMixedFreshGrantBecomesARealNextCycleSource(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	p := draft.Evidence.Proposals[0]
	p.SelfTasks[0].ProgressTarget = continuationNumber(3)
	p.WorkContinuations = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: p.SelfTasks[0].TaskID, UntilTarget: true}}
	var observation domain.CharacterObservationPacket
	for _, o := range input.Observations {
		if o.AgentID == p.AgentID {
			observation = o
		}
	}
	old := p.Digest
	p, err := domain.FinalizeCharacterDecisionProposal(p, observation)
	continuationMust(t, err)
	r := draft.Evidence.Arbitrations[0]
	for i := range r.ProposalDigests {
		if r.ProposalDigests[i] == old {
			r.ProposalDigests[i] = p.Digest
		}
	}
	for i := range r.Resolutions {
		// A changed test proposal must be evaluated from the immutable prestate,
		// not an already-derived poststate of the previous proposal identity.
		for _, actor := range input.Stimulus.PhysicalState.Actors {
			if actor.AgentID == r.Resolutions[i].AgentID {
				post := continuationCopy(actor)
				r.Resolutions[i].PostState = &post
			}
		}
		if r.Resolutions[i].AgentID == p.AgentID {
			r.Resolutions[i].ProposalDigest = p.Digest
			r.Resolutions[i].CompletionState = "in_progress"
			r.Resolutions[i].SelfExecutions[0].Status = "in_progress"
		}
	}
	effective := append(append([]domain.CharacterDecisionProposal(nil), origin.Evidence.Proposals...), p)
	r, err = domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, effective, 1)
	continuationMust(t, err)
	draft.Evidence.Proposals, draft.Evidence.Arbitrations = []domain.CharacterDecisionProposal{p}, []domain.WorldArbitrationReceipt{r}
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	step, prefix, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	continuationMust(t, err)
	prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, testutil.CycleReadiness(t, cycle, "continue"))
	continuationMust(t, err)
	ledger, ok := prefix.ContinuationLedger(p.AgentID)
	if !ok || ledger.Origin.Digest != cycle.Digest || ledger.ProposalDigest != p.Digest {
		t.Fatal("fresh mixed-cycle grant did not bind its actual global origin")
	}
	if domain.ValidateCharacterWorkContinuationLedgerV1(ledger) == nil {
		t.Fatal("mixed-cycle origin was accepted without a global source verifier")
	}
	continuationMust(t, domain.ValidateCharacterWorkContinuationLedgerV1(ledger, prefix.ContinuationBoundaries()...))
	nextInput := workBatchNextInput(t, origin, step.AfterState(), prefix.Session().CurrentDay, 3, cycle.Digest)
	eligible, err := domain.EvaluateCharacterWorkContinuationV1(ledger, nextInput, prefix.ContinuationBoundaries()...)
	continuationMust(t, err)
	if !eligible.Eligible || eligible.Receipt.EffectiveCompleted != 1 || eligible.Receipt.AuthorizedRemaining != 2 {
		t.Fatalf("mixed-origin continuation lost its real progress/grant: %+v", eligible)
	}
}

func TestVerifiedContinuationGlobalCycleRejectsResignedPartialOrForeignAuthority(t *testing.T) {
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	cycle, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	for name, mutate := range map[string]func(*domain.CharacterActivationCycle){
		"allowance":           func(c *domain.CharacterActivationCycle) { c.WorkContinuations[0].AuthorizedRemaining += 100 },
		"missing_member":      func(c *domain.CharacterActivationCycle) { c.ContinuationEntryDigests = c.ContinuationEntryDigests[:1] },
		"foreign_predecessor": func(c *domain.CharacterActivationCycle) { c.PreviousDigest = "sha256:" + strings.Repeat("f", 64) },
		"fresh_and_continued": func(c *domain.CharacterActivationCycle) {
			c.Evidence.Proposals = append(c.Evidence.Proposals, origin.Evidence.Proposals[0])
		},
		"invented_model_call": func(c *domain.CharacterActivationCycle) {
			c.Evidence.Usage = append(c.Evidence.Usage, domain.CharacterAgentUsage{Role: "character", AgentID: c.WorkContinuations[0].AgentID, GenerationID: c.GenerationID, Chapter: c.Chapter, Round: 1})
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := continuationCopy(cycle)
			mutate(&changed)
			changed.Digest, err = domain.CharacterContinuationCycleRootV2(changed)
			continuationMust(t, err)
			if _, _, err := domain.VerifyCharacterActivationStep(prefix, input, changed); err == nil {
				t.Fatal("resigning incomplete or unauthorized evidence made it valid")
			}
		})
	}
	var zero domain.VerifiedCharacterActivationPrefix
	if _, _, err := domain.VerifyCharacterActivationStep(zero, input, cycle); err == nil {
		t.Fatal("JSON/zero prefix supplied source authority")
	}
}
