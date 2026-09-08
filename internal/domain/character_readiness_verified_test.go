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

func mixedReadinessOrigin(t *testing.T, context domain.CharacterReadinessContext) (domain.CharacterActivationCycle, domain.CharacterActivationInputSet) {
	t.Helper()
	origin, _, _, _ := workBatchFixture(t, false)
	origin.ChapterContextDigest = context.Digest
	e := origin.Evidence
	token, err := domain.CharacterActivationCycleSourceToken(origin.GenerationID, origin.Chapter, 1, context.Digest, "")
	continuationMust(t, err)
	for i, source := range e.Stimulus.Sources {
		if strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
			e.Stimulus.Sources[i] = token
		}
	}
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	continuationMust(t, err)
	observations := map[string]domain.CharacterObservationPacket{}
	for i, observation := range e.Observations {
		observation.StimulusDigest = e.Stimulus.Digest
		observation.CycleContext.ChapterContextDigest = context.Digest
		observation, err = domain.FinalizeCharacterObservationPacket(observation)
		continuationMust(t, err)
		e.Observations[i], observations[observation.AgentID] = observation, observation
	}
	for i := range e.Activation.Entries {
		entry := &e.Activation.Entries[i]
		if entry.State == domain.CharacterAgentActive {
			entry.ObservationDigest = observations[entry.AgentID].Digest
		}
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	continuationMust(t, err)
	proposals := map[string]domain.CharacterDecisionProposal{}
	r := e.Arbitrations[0]
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", e.Stimulus.Digest, e.Activation.Digest
	r.ProposalDigests = nil
	for i, proposal := range e.Proposals {
		proposal.ObservationDigest = observations[proposal.AgentID].Digest
		proposal, err = domain.FinalizeCharacterDecisionProposal(proposal, observations[proposal.AgentID])
		continuationMust(t, err)
		e.Proposals[i], proposals[proposal.AgentID] = proposal, proposal
		r.ProposalDigests = append(r.ProposalDigests, proposal.Digest)
	}
	for i := range r.Resolutions {
		resolution := &r.Resolutions[i]
		resolution.ProposalDigest = proposals[resolution.AgentID].Digest
		for _, before := range e.Stimulus.PhysicalState.Actors {
			if before.AgentID == resolution.AgentID {
				copy := before
				resolution.PostState = &copy
			}
		}
	}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, e.Proposals, 1)
	continuationMust(t, err)
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	continuationMust(t, err)
	origin.Evidence = e
	origin, err = domain.FinalizeCharacterActivationCycle(origin)
	continuationMust(t, err)
	return prefixInputForOrigin(t, origin)
}

func mixedReadinessChapterFixture(t *testing.T) (domain.CharacterActivationChapterEvidence, []domain.VerifiedCharacterActivationStep) {
	t.Helper()
	context, err := domain.FinalizeCharacterReadinessContext(domain.CharacterReadinessContext{GenerationID: "pg2_cycle_fixture", Chapter: 1, POVCharacter: "甲", ArcLastChapter: 3, BookLastChapter: 3,
		SoftOutline: domain.OutlineEntry{Chapter: 1}, HardContracts: []string{"保留实际选择与已完成工时"}})
	continuationMust(t, err)
	origin, input := mixedReadinessOrigin(t, context)
	session, err := domain.NewCharacterActivationSession(origin.GenerationID, origin.Chapter, context.Digest, *input.Stimulus.PhysicalState, origin.StartDay, 8)
	continuationMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	continuationMust(t, err)
	chapter := domain.CharacterActivationChapterEvidence{Context: context, ProtocolDigest: origin.Evidence.ProtocolDigest}
	for index := 1; index <= 3; index++ {
		cycle := origin
		if index > 1 {
			var draft domain.CharacterActivationCycle
			input, draft = verifiedWorkDraft(t, prefix, origin)
			cycle, err = domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
			continuationMust(t, err)
		}
		_, pending, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
		continuationMust(t, err)
		reviewInput, err := domain.NewCharacterReadinessReviewInputFromSteps(context, pending.Session(), pending.Steps(), origin.Evidence.ProtocolDigest)
		continuationMust(t, err)
		verdict := testutil.ReadyVerdict(reviewInput)
		if index < 3 {
			verdict.Decision = "continue"
		}
		receipt, err := domain.FinalizeCharacterReadinessReview(reviewInput, verdict)
		continuationMust(t, err)
		codec, err := domain.NewCharacterReadinessModelCodecV1(reviewInput)
		continuationMust(t, err)
		binding := codec.Binding()
		audit := domain.CharacterReadinessReviewAudit{Input: reviewInput, Receipt: receipt, ModelView: &binding}
		continuationMust(t, domain.ValidateCharacterReadinessReviewAudit(audit))
		chapter.Cycles = append(chapter.Cycles, cycle)
		chapter.Inputs = append(chapter.Inputs, input)
		chapter.Reviews = append(chapter.Reviews, audit)
		prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(pending, receipt)
		continuationMust(t, err)
	}
	chapter.Session = prefix.Session()
	chapter, err = domain.FinalizeCharacterActivationChapterEvidence(chapter)
	continuationMust(t, err)
	return chapter, prefix.Steps()
}

func TestReadinessFromVerifiedStepsPreservesLegacyInputAndRejectsJSONAuthority(t *testing.T) {
	chapter := testutil.CharacterActivationChapter(t)
	verifiedChapterSteps, err := domain.VerifiedStepsForCharacterActivationChapter(chapter)
	continuationMust(t, err)
	if len(verifiedChapterSteps) != 1 || verifiedChapterSteps[0].GlobalRoot() != chapter.Cycles[0].Digest {
		t.Fatal("public chapter source resolver changed a legacy cycle")
	}
	prefix := verifiedPrefixForCycle(t, chapter.Cycles[0])
	step, pending, err := domain.VerifyCharacterActivationStep(prefix, chapter.Inputs[0], chapter.Cycles[0])
	continuationMust(t, err)
	oldTrace, err := domain.BuildCharacterReadinessTrace(chapter.Cycles)
	continuationMust(t, err)
	newTrace, err := domain.BuildCharacterReadinessTraceFromSteps([]domain.VerifiedCharacterActivationStep{step})
	continuationMust(t, err)
	if !reflect.DeepEqual(oldTrace, newTrace) {
		t.Fatal("verified consumer changed the legacy compact trace")
	}
	oldInput, err := domain.NewCharacterReadinessReviewInput(chapter.Context, pending.Session(), chapter.Cycles, chapter.ProtocolDigest)
	continuationMust(t, err)
	newInput, err := domain.NewCharacterReadinessReviewInputFromSteps(chapter.Context, pending.Session(), pending.Steps(), chapter.ProtocolDigest)
	continuationMust(t, err)
	oldBytes, _ := json.Marshal(oldInput)
	newBytes, _ := json.Marshal(newInput)
	if !bytes.Equal(oldBytes, newBytes) {
		t.Fatal("fresh-only readiness input/hash changed")
	}
	var fromJSON []domain.VerifiedCharacterActivationStep
	_ = json.Unmarshal([]byte(`[{"verified":true,"cycle":{"index":1}}]`), &fromJSON)
	if _, err := domain.BuildCharacterReadinessTraceFromSteps(fromJSON); err == nil {
		t.Fatal("JSON claimed verified step authority")
	}
	if _, err := domain.NewCharacterReadinessReviewInputFromSteps(chapter.Context, pending.Session(), fromJSON, chapter.ProtocolDigest); err == nil {
		t.Fatal("readiness accepted a JSON-created source step")
	}
	newTrace.Cycles[0].StoryTime.EndDay = 100
	newTrace.Actors[0].Resources[0].Name = "mutated"
	newInput.Context.HardContracts[0] = "mutated"
	again, err := domain.BuildCharacterReadinessTraceFromSteps(pending.Steps())
	continuationMust(t, err)
	if !reflect.DeepEqual(again, oldTrace) || chapter.Context.HardContracts[0] == "mutated" {
		t.Fatal("readiness consumer exposed mutable verified source data")
	}
}

func TestMixedReadinessChapterRebuildsFlatVerifiedSourcesAndOriginalAudits(t *testing.T) {
	chapter, steps := mixedReadinessChapterFixture(t)
	resolved, err := domain.VerifiedStepsForCharacterActivationChapter(chapter)
	continuationMust(t, err)
	if len(resolved) != len(steps) {
		t.Fatal("public flat chapter resolver lost verified steps")
	}
	for i := range steps {
		if resolved[i].GlobalRoot() != steps[i].GlobalRoot() || !reflect.DeepEqual(resolved[i].EffectiveProposals(), steps[i].EffectiveProposals()) || !reflect.DeepEqual(resolved[i].AfterState(), steps[i].AfterState()) {
			t.Fatal("public source resolver changed effective intent or actual state")
		}
	}
	trace, err := domain.BuildCharacterReadinessTraceFromSteps(steps)
	continuationMust(t, err)
	if len(trace.Cycles) != 3 || len(trace.Cycles[1].Actions) != 3 || len(chapter.Cycles[1].Evidence.Proposals) != 1 {
		t.Fatal("readiness lost continued original choices or invented fresh proposals")
	}
	for _, p := range chapter.Cycles[0].Evidence.Proposals {
		found := false
		for _, action := range trace.Cycles[2].Actions {
			if action.AgentID == p.AgentID && action.ProposalDigest == p.Digest && action.Decision == p.Decision && action.IntendedAction == p.IntendedAction {
				found = true
			}
		}
		if !found {
			t.Fatal("continued intent was copied/resigned as a fresh choice")
		}
	}
	if _, err := domain.BuildCharacterReadinessTrace(chapter.Cycles); err == nil {
		t.Fatal("legacy standalone cycle reader was loosened to accept mixed sources")
	}
	raw, _ := json.Marshal(chapter)
	var restored domain.CharacterActivationChapterEvidence
	continuationMust(t, json.Unmarshal(raw, &restored))
	continuationMust(t, domain.ValidateCharacterActivationChapterEvidence(restored))
	verified, err := domain.FinalizeCharacterActivationChapterEvidence(restored)
	continuationMust(t, err)
	after, _ := json.Marshal(verified)
	if !bytes.Equal(raw, after) || verified.Digest != chapter.Digest || strings.Contains(string(raw), `"ledger":`) {
		t.Fatal("flat chapter replay changed hashes or embedded recursive source ledgers")
	}
	for name, change := range map[string]func(*domain.CharacterActivationChapterEvidence){
		"missing input": func(c *domain.CharacterActivationChapterEvidence) { c.Inputs = c.Inputs[:2] },
		"missing audit": func(c *domain.CharacterActivationChapterEvidence) { c.Reviews = c.Reviews[:2] },
		"wrong pending session": func(c *domain.CharacterActivationChapterEvidence) {
			c.Reviews[1].Input.SessionDigest = c.Reviews[0].Input.SessionDigest
		},
		"wrong actual state": func(c *domain.CharacterActivationChapterEvidence) {
			c.Reviews[1].Input.Trace.Actors[0].Location = "elsewhere"
		},
		"model view from another call": func(c *domain.CharacterActivationChapterEvidence) { c.Reviews[1].ModelView = c.Reviews[0].ModelView },
		"different cycle input":        func(c *domain.CharacterActivationChapterEvidence) { c.Inputs[1] = c.Inputs[0] },
		"self-consistent forged trace": func(c *domain.CharacterActivationChapterEvidence) {
			audit := &c.Reviews[1]
			audit.Input.Trace.Actors[0].Location = "invented but self-signed location"
			var err error
			audit.Receipt, err = domain.FinalizeCharacterReadinessReview(audit.Input, domain.CharacterReadinessVerdict{Decision: audit.Receipt.Decision, Reason: audit.Receipt.Reason,
				EvidenceRefs: audit.Receipt.EvidenceRefs, ContractChecks: audit.Receipt.ContractChecks})
			continuationMust(t, err)
			codec, err := domain.NewCharacterReadinessModelCodecV1(audit.Input)
			continuationMust(t, err)
			binding := codec.Binding()
			audit.ModelView = &binding
			// This passes its own audit hashes. Only replay against the actual
			// verified source step can reject the invented world result.
			continuationMust(t, domain.ValidateCharacterReadinessReviewAudit(*audit))
		},
	} {
		t.Run(name, func(t *testing.T) {
			var changed domain.CharacterActivationChapterEvidence
			_ = json.Unmarshal(raw, &changed)
			change(&changed)
			if _, err := domain.FinalizeCharacterActivationChapterEvidence(changed); err == nil {
				t.Fatal("mixed source/audit corruption was accepted")
			}
			if _, err := domain.VerifiedStepsForCharacterActivationChapter(changed); err == nil {
				t.Fatal("public chapter resolver trusted a corrupted source/audit")
			}
		})
	}
	wrongDigest := continuationCopy(chapter)
	wrongDigest.Digest = "sha256:" + strings.Repeat("f", 64)
	if _, err := domain.VerifiedStepsForCharacterActivationChapter(wrongDigest); err == nil {
		t.Fatal("public chapter resolver trusted a caller-supplied evidence digest")
	}
	if _, err := domain.BuildCharacterReadinessTraceFromSteps([]domain.VerifiedCharacterActivationStep{steps[0], steps[2]}); err == nil {
		t.Fatal("verified but noncontiguous steps were accepted")
	}
}
