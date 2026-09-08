package domain_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func verifiedPrefixForCycle(t *testing.T, cycle domain.CharacterActivationCycle) domain.VerifiedCharacterActivationPrefix {
	t.Helper()
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, *cycle.Evidence.Stimulus.PhysicalState, cycle.StartDay, 4)
	continuationMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	continuationMust(t, err)
	return prefix
}

func TestVerifiedActivationPrefixFreshStepKeepsOriginalHashAndCopiesGetters(t *testing.T) {
	evidence := testutil.CharacterActivationChapter(t)
	cycle, input := evidence.Cycles[0], evidence.Inputs[0]
	rawCycle, _ := json.Marshal(cycle)
	rawInput, _ := json.Marshal(input)
	prefix := verifiedPrefixForCycle(t, cycle)
	before := prefix.Session()
	step, next, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	continuationMust(t, err)
	if step.GlobalRoot() != cycle.Digest || next.Session().Phase != "assessing" || !reflect.DeepEqual(before, prefix.Session()) {
		t.Fatal("fresh verification changed historical root, source prefix or readiness state")
	}
	wanted, err := domain.ApplyArbitrationPhysicalStateV2(cycle.Evidence.Arbitrations[0], input.Stimulus, cycle.Evidence.Proposals...)
	continuationMust(t, err)
	if !reflect.DeepEqual(step.AfterState(), wanted) || !reflect.DeepEqual(step.EffectiveProposals(), domain.LatestCharacterCycleProposals(cycle.Evidence)) {
		t.Fatal("verified step did not retain its actual legacy proposals/state")
	}
	returnedInput := step.Input()
	returnedInput.Observations[0].KnownFacts[0].Text = "mutated getter"
	returnedCycle := step.Cycle()
	returnedCycle.Evidence.Proposals[0].Decision = "mutated getter"
	returnedAfter := step.AfterState()
	returnedAfter.Actors[0].Location = "mutated getter"
	returnedProposals := step.EffectiveProposals()
	returnedProposals[0].KnowledgeRefs[0] = "mutated getter"
	returnedSession := next.Session()
	returnedSession.CycleDigests[0] = "mutated getter"
	returnedSteps := next.Steps()
	returnedSteps[0] = domain.VerifiedCharacterActivationStep{}
	if step.Input().Observations[0].KnownFacts[0].Text == "mutated getter" || step.Cycle().Evidence.Proposals[0].Decision == "mutated getter" || step.AfterState().Actors[0].Location == "mutated getter" || step.EffectiveProposals()[0].KnowledgeRefs[0] == "mutated getter" || next.Session().CycleDigests[0] != cycle.Digest || next.Steps()[0].GlobalRoot() != cycle.Digest {
		t.Fatal("getter exposed writable verified source state")
	}
	ready, err := domain.ApplyVerifiedCharacterActivationReadiness(next, evidence.Reviews[0].Receipt)
	continuationMust(t, err)
	if ready.Session().Phase != "ready" || next.Session().Phase != "assessing" {
		t.Fatal("readiness transition mutated a prefix or skipped its receipt")
	}
	if _, _, err := domain.VerifyCharacterActivationStep(ready, input, cycle); err == nil {
		t.Fatal("ready prefix executed another cycle")
	}
	if _, err := domain.NewVerifiedCharacterActivationPrefix(ready.Session()); err == nil {
		t.Fatal("advanced self-signed session replaced flat evidence replay")
	}
	unchangedCycle, _ := json.Marshal(cycle)
	unchangedInput, _ := json.Marshal(input)
	if !bytes.Equal(rawCycle, unchangedCycle) || !bytes.Equal(rawInput, unchangedInput) {
		t.Fatal("verification changed caller-owned historical evidence")
	}
}

func TestVerifiedActivationPrefixRejectsZeroJSONForeignAndUntypedSources(t *testing.T) {
	evidence := testutil.CharacterActivationChapter(t)
	cycle, input := evidence.Cycles[0], evidence.Inputs[0]
	var forged domain.VerifiedCharacterActivationPrefix
	_ = json.Unmarshal([]byte(`{"verified":true,"session":{"phase":"collecting"},"ledgers":{"fake":{}}}`), &forged)
	if forged.Session().Digest != "" || len(forged.Steps()) != 0 || len(forged.ContinuationBoundaries()) != 0 {
		t.Fatal("JSON manufactured verified prefix authority")
	}
	if _, ok := forged.ContinuationLedger("fake"); ok {
		t.Fatal("JSON manufactured a continuation ledger")
	}
	if _, _, err := domain.VerifyCharacterActivationStep(forged, input, cycle); err == nil {
		t.Fatal("zero prefix accepted a complete-looking cycle")
	}
	if _, err := domain.ApplyVerifiedCharacterActivationReadiness(forged, evidence.Reviews[0].Receipt); err == nil {
		t.Fatal("zero prefix accepted readiness")
	}
	var fakeStep domain.VerifiedCharacterActivationStep
	_ = json.Unmarshal([]byte(`{"verified":true,"cycle":{"digest":"sha256:fake"}}`), &fakeStep)
	if fakeStep.GlobalRoot() != "" || len(fakeStep.EffectiveProposals()) != 0 || fakeStep.AfterState().Version != "" {
		t.Fatal("JSON manufactured a verified step")
	}
	prefix := verifiedPrefixForCycle(t, cycle)
	for name, edit := range map[string]func(*domain.CharacterActivationInputSet){
		"legacy":             func(i *domain.CharacterActivationInputSet) { i.Stimulus.Version = domain.WorldStimulusPacketVersion },
		"nil physical":       func(i *domain.CharacterActivationInputSet) { i.Stimulus.PhysicalState = nil },
		"nil clock":          func(i *domain.CharacterActivationInputSet) { i.Stimulus.StoryClock = nil },
		"foreign generation": func(i *domain.CharacterActivationInputSet) { i.Stimulus.GenerationID = "pg2_foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := continuationCopy(input)
			edit(&changed)
			before := prefix.Session()
			step, after, err := domain.VerifyCharacterActivationStep(prefix, changed, cycle)
			if err == nil || step.GlobalRoot() != "" || !reflect.DeepEqual(before, after.Session()) {
				t.Fatal("invalid input advanced source authority")
			}
		})
	}
	foreign, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, *input.Stimulus.PhysicalState, cycle.StartDay+1, 4)
	continuationMust(t, err)
	foreignPrefix, err := domain.NewVerifiedCharacterActivationPrefix(foreign)
	continuationMust(t, err)
	if _, _, err := domain.VerifyCharacterActivationStep(foreignPrefix, input, cycle); err == nil {
		t.Fatal("internally valid input bypassed the prefix actual clock")
	}
	unknown := cycle
	unknown.Version = "character-activation-cycle.unknown"
	if _, _, err := domain.VerifyCharacterActivationStep(prefix, input, unknown); err == nil {
		t.Fatal("unknown cycle version was routed through the legacy validator")
	}
}

func prefixInputForOrigin(t *testing.T, cycle domain.CharacterActivationCycle) (domain.CharacterActivationCycle, domain.CharacterActivationInputSet) {
	t.Helper()
	e := cycle.Evidence
	var memories []domain.CharacterAgentMemory
	for _, o := range e.Observations {
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, State: "projected", GenerationID: o.GenerationID})
		continuationMust(t, err)
		memories = append(memories, memory)
	}
	input, err := domain.FinalizeCharacterActivationInputSet(domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus, Activation: e.Activation, Observations: e.Observations, Memories: memories})
	continuationMust(t, err)
	cycle.InputSetDigest = input.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	continuationMust(t, err)
	return cycle, input
}

func TestVerifiedActivationPrefixFreshChoiceReplacesPriorContinuationAuthority(t *testing.T) {
	first, input := prefixInputForOrigin(t, continuationOriginFixture(t, nil, nil))
	prefix := verifiedPrefixForCycle(t, first)
	_, next, err := domain.VerifyCharacterActivationStep(prefix, input, first)
	continuationMust(t, err)
	owner := first.Evidence.Proposals[0].AgentID
	ledger, ok := next.ContinuationLedger(owner)
	if !ok || ledger.Origin.Digest != first.Digest || ledger.ProposalDigest != first.Evidence.Proposals[0].Digest {
		t.Fatal("fresh explicit single-task authority was not registered")
	}
	ledger.Origin.Evidence.Proposals[0].Decision = "mutated getter"
	ledger, ok = next.ContinuationLedger(owner)
	if !ok || ledger.Origin.Evidence.Proposals[0].Decision == "mutated getter" {
		t.Fatal("ledger getter exposed original signed authority")
	}
	if _, ok := next.ContinuationLedger("foreign"); ok {
		t.Fatal("unknown owner received a ledger")
	}
	next, err = domain.ApplyVerifiedCharacterActivationReadiness(next, testutil.CycleReadiness(t, first, "continue"))
	continuationMust(t, err)
	current := continuationNextInput(t, ledger)
	p := continuationCopy(first.Evidence.Proposals[0])
	p.WorkContinuations = nil // This actual fresh choice revokes the standing grant.
	p.ObservationDigest = current.Observations[0].Digest
	p, err = domain.FinalizeCharacterDecisionProposal(p, current.Observations[0])
	continuationMust(t, err)
	r := continuationArbitration(t, ledger, current, 1, "in_progress")
	r.Digest, r.ProposalDigests = "", []string{p.Digest}
	r.Resolutions[0].ProposalDigest = p.Digest
	r.Resolutions[0].PostState = &current.Stimulus.PhysicalState.Actors[0]
	r, err = domain.FinalizeWorldArbitrationReceipt(r, current.Stimulus, current.Activation, []domain.CharacterDecisionProposal{p}, 1)
	continuationMust(t, err)
	e, err := domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{GenerationID: first.GenerationID, Chapter: first.Chapter, Registry: current.Registry,
		Stimulus: current.Stimulus, Activation: current.Activation, Observations: current.Observations, Proposals: []domain.CharacterDecisionProposal{p}, Arbitrations: []domain.WorldArbitrationReceipt{r},
		MemoryRoots: []string{current.Observations[0].MemoryRoot}, ProtocolDigest: first.Evidence.ProtocolDigest})
	continuationMust(t, err)
	second, err := domain.FinalizeCharacterActivationCycle(domain.CharacterActivationCycle{GenerationID: first.GenerationID, Chapter: first.Chapter, Index: 2, PreviousDigest: first.Digest,
		ChapterContextDigest: first.ChapterContextDigest, InputSetDigest: current.Digest, Evidence: e})
	continuationMust(t, err)
	_, latest, err := domain.VerifyCharacterActivationStep(next, current, second)
	continuationMust(t, err)
	if _, ok := latest.ContinuationLedger(owner); ok {
		t.Fatal("a fresh ungranted choice retained old standing authority")
	}
	if _, ok := next.ContinuationLedger(owner); !ok {
		t.Fatal("later prefix mutated its immutable predecessor ledger")
	}
	if len(latest.Steps()) != 2 || latest.Session().CycleDigests[1] != second.Digest {
		t.Fatal("flat two-step prefix did not advance exactly once")
	}
	raw, err := json.Marshal(latest)
	continuationMust(t, err)
	if string(raw) != "{}" || strings.Contains(string(raw), first.Digest) {
		t.Fatal("verified runtime authority recursively serialized historical sources")
	}
}

func TestVerifiedActivationPrefixBindsChronologyToActualSessionNotSelfSignedContext(t *testing.T) {
	cycle, input := prefixInputForOrigin(t, continuationOriginFixture(t, nil, nil))
	prepared, err := domain.PrepareCharacterSelfChronologyStateV1(*input.Stimulus.PhysicalState)
	continuationMust(t, err)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, prepared, cycle.StartDay, 4)
	continuationMust(t, err)
	otherSession, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, cycle.ChapterContextDigest, prepared, cycle.StartDay, 5)
	continuationMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	continuationMust(t, err)
	input.Stimulus.PhysicalState = &prepared
	input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterSelfChronologyPolicyV1)
	bind := func(contextSession domain.CharacterActivationSession) {
		input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(contextSession)
		continuationMust(t, err)
		input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
		continuationMust(t, err)
		observation := input.Observations[0]
		observation.StimulusDigest = input.Stimulus.Digest
		observation.Sources = []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1, domain.CharacterSelfChronologyPolicyV1}
		observation.ResourceViews, err = domain.BuildCharacterResourceViewsV2(prepared, observation.AgentID)
		continuationMust(t, err)
		observation, err = domain.FinalizeCharacterObservationPacket(observation)
		continuationMust(t, err)
		input.Observations[0] = observation
		input.Activation.Entries[0].ObservationDigest = observation.Digest
		input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
		continuationMust(t, err)
		input, err = domain.FinalizeCharacterActivationInputSet(input)
		continuationMust(t, err)
	}
	bind(otherSession) // Valid context, exact state/time, but not THIS actual session.
	if _, after, err := domain.VerifyCharacterActivationStep(prefix, input, cycle); err == nil || !strings.Contains(err.Error(), "current host session") || after.Session().Digest != prefix.Session().Digest {
		t.Fatalf("foreign session context escaped the prefix guard: %v", err)
	}
	bind(session)
	p := cycle.Evidence.Proposals[0]
	p.ObservationDigest = input.Observations[0].Digest
	p, err = domain.FinalizeCharacterDecisionProposal(p, input.Observations[0])
	continuationMust(t, err)
	r := cycle.Evidence.Arbitrations[0]
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	r.ProposalDigests = []string{p.Digest}
	r.Resolutions[0].ProposalDigest, r.Resolutions[0].PostState = p.Digest, &prepared.Actors[0]
	r, err = domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, []domain.CharacterDecisionProposal{p}, 1)
	continuationMust(t, err)
	cycle.Evidence, err = domain.FinalizeCharacterAgentEvidenceBundle(domain.CharacterAgentEvidenceBundle{GenerationID: cycle.GenerationID, Chapter: cycle.Chapter, Registry: input.Registry,
		Stimulus: input.Stimulus, Activation: input.Activation, Observations: input.Observations, Proposals: []domain.CharacterDecisionProposal{p}, Arbitrations: []domain.WorldArbitrationReceipt{r},
		MemoryRoots: []string{input.Observations[0].MemoryRoot}, ProtocolDigest: cycle.Evidence.ProtocolDigest})
	continuationMust(t, err)
	cycle.InputSetDigest = input.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	continuationMust(t, err)
	step, _, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
	continuationMust(t, err)
	if step.GlobalRoot() != cycle.Digest || step.AfterState().Actors[0].SelfExperiences[0].Evaluation == nil {
		t.Fatal("valid fresh chronological step lost its exact evaluation source")
	}
}
