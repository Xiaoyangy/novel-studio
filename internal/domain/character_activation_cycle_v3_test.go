package domain_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

var v3FixturePolicies = []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3}

func v3Fixture(t *testing.T) (domain.CharacterReadinessContext, domain.VerifiedCharacterActivationPrefix, domain.CharacterActivationInputSet) {
	t.Helper()
	template, _, _, _ := workBatchFixture(t, false)
	_, input := prefixInputForOrigin(t, template)
	context, err := domain.FinalizeCharacterReadinessContext(domain.CharacterReadinessContext{GenerationID: input.Stimulus.GenerationID, Chapter: 1, POVCharacter: "甲", ArcLastChapter: 1, BookLastChapter: 2, TargetWords: 2250, SoftOutline: domain.OutlineEntry{Chapter: 1, Title: "现场检查"}, HardContracts: []string{"保留实际选择与已发生状态"}})
	continuationMust(t, err)
	physical, err := domain.PrepareCharacterSelfChronologyStateV1(*input.Stimulus.PhysicalState)
	continuationMust(t, err)
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, context.Digest, physical, 0, 8)
	continuationMust(t, err)
	prefix, err := domain.NewVerifiedCharacterActivationPrefix(session)
	continuationMust(t, err)
	input.Stimulus.PhysicalState = &physical
	input = v3RebindInput(t, input, session, false)
	return context, prefix, input
}

func v3RebindInput(t *testing.T, input domain.CharacterActivationInputSet, session domain.CharacterActivationSession, activateAll bool) domain.CharacterActivationInputSet {
	t.Helper()
	input = continuationCopy(input)
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(session.CycleDigests)+1, session.ChapterContextDigest, previous)
	continuationMust(t, err)
	input.Stimulus.Sources = append(append([]string(nil), v3FixturePolicies...), token)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	continuationMust(t, err)
	clock := *input.Stimulus.StoryClock
	clock.CurrentDay = session.CurrentDay
	clock, err = domain.FinalizeStoryClockContext(clock)
	continuationMust(t, err)
	input.Stimulus.StoryClock = &clock
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	cycleContext, err := domain.NewCharacterObservationCycleContext(session)
	continuationMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.Round = 1
		o.ConflictFeedback = nil
		o.Sources = append([]string(nil), v3FixturePolicies...)
		o.StimulusDigest = input.Stimulus.Digest
		o.CycleContext = cycleContext
		for _, a := range input.Stimulus.PhysicalState.Actors {
			if a.AgentID == o.AgentID {
				o.Location = a.Location
			}
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(*input.Stimulus.PhysicalState, o.AgentID)
		continuationMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(*input.Stimulus.PhysicalState, o.AgentID)
		continuationMust(t, err)
		for _, m := range input.Memories {
			if m.AgentID == o.AgentID {
				o.MemoryRoot = m.MemoryRoot
				o.Memory = continuationCopy(m.Facts)
			}
		}
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		continuationMust(t, err)
		for j := range input.Activation.Entries {
			a := &input.Activation.Entries[j]
			if a.AgentID == o.AgentID {
				if activateAll {
					a.State = domain.CharacterAgentActive
				}
				if a.State == domain.CharacterAgentActive {
					a.ObservationDigest = o.Digest
				}
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func v3NextInput(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix) domain.CharacterActivationInputSet {
	t.Helper()
	steps := prefix.Steps()
	last := steps[len(steps)-1]
	input := last.Input()
	after := last.AfterState()
	input.Stimulus.PhysicalState = &after
	r := last.Cycle().Evidence.Arbitrations[len(last.Cycle().Evidence.Arbitrations)-1]
	for _, p := range last.EffectiveProposals() {
		for i := range input.Observations {
			if input.Observations[i].AgentID == p.AgentID {
				input.Observations[i].CurrentGoal, input.Observations[i].Pressure = p.CurrentGoal, p.Pressure
			}
		}
		for _, resolution := range r.Resolutions {
			if resolution.AgentID != p.AgentID {
				continue
			}
			text, err := domain.CharacterActivationPrivateOutcome(p, resolution, after, r)
			continuationMust(t, err)
			if strings.Contains(text, "AUTHOR_PRIVATE_SECRET") {
				t.Fatal("world-side text entered owner memory")
			}
			for i, m := range input.Memories {
				if m.AgentID == p.AgentID {
					m.Facts = append(m.Facts, domain.CharacterAgentMemoryFact{ID: "actual-" + r.Digest, Chapter: 1, Kind: "projected_decision", Text: text, SourceDigest: r.Digest})
					m, err = domain.FinalizeCharacterAgentMemory(m)
					continuationMust(t, err)
					input.Memories[i] = m
				}
			}
		}
	}
	return v3RebindInput(t, input, prefix.Session(), true)
}

func v3Fresh(t *testing.T, o domain.CharacterObservationPacket) domain.CharacterDecisionProposal {
	t.Helper()
	p := domain.CharacterDecisionProposal{GenerationID: o.GenerationID, Chapter: o.Chapter, Round: 1, AgentID: o.AgentID, Character: o.Character, ObservationDigest: o.Digest, Location: o.Location, CurrentGoal: o.CurrentGoal, Pressure: o.Pressure, AvailableOptions: []string{"继续检查", "暂停"}, Decision: "继续检查", DecisionReason: "本岗条件允许", IntendedAction: "在原地执行常规检查", ActionDuration: "只计实际有效分钟", KnowledgeRefs: []string{o.KnownFacts[0].ID}, SelfTasks: []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "常规检查", ProgressTarget: continuationNumber(5), ProgressUnit: "minute", KnowledgeRefs: []string{o.KnownFacts[0].ID}}}, WorkContinuations: []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: true}}}
	if o.Character == "丙" {
		p.SelfTasks[0].TaskID = "fresh-" + o.CycleContext.PreviousCycleDigest
		p.SelfTasks[0].ProgressTarget = continuationNumber(1)
		p.WorkContinuations = nil
	}
	p, err := domain.FinalizeCharacterDecisionProposal(p, o)
	continuationMust(t, err)
	return p
}

func v3Scope(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet) domain.VerifiedCharacterArbitrationSourcesV1 {
	t.Helper()
	var admissions []domain.CharacterWorkContinuationReceiptV1
	var fresh []domain.CharacterDecisionProposal
	for _, entry := range input.Activation.Entries {
		if entry.State != domain.CharacterAgentActive {
			continue
		}
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, entry.AgentID)
		continuationMust(t, err)
		if eligible.Eligible {
			admissions = append(admissions, *eligible.Receipt)
		} else {
			for _, o := range input.Observations {
				if o.AgentID == entry.AgentID {
					fresh = append(fresh, v3Fresh(t, o))
				}
			}
		}
	}
	scope, err := domain.ResolveCharacterArbitrationRoundV1(prefix, input, admissions, fresh)
	continuationMust(t, err)
	return scope
}

func v3Draft(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet, affected []string, hardRound int) domain.CharacterActivationCycle {
	t.Helper()
	first := v3Scope(t, prefix, input)
	scope := first
	var rounds []domain.VerifiedCharacterArbitrationRoundV1
	hard := func(s domain.VerifiedCharacterArbitrationSourcesV1) domain.VerifiedCharacterArbitrationRoundV1 {
		r := roundSourceReceipt(t, s, 0)
		r.Finalized = false
		r.HardContractStatus = "infeasible"
		r.HardContractConflicts = []string{"当前明确硬合同不可共同满足"}
		for i := range r.Resolutions {
			r.Resolutions[i].SelfExecutions = nil
		}
		value, err := domain.FinalizeCharacterArbitrationRoundV1(s, r)
		continuationMust(t, err)
		return value
	}
	if hardRound == 1 {
		rounds = append(rounds, hard(scope))
	} else {
		if len(affected) > 0 {
			rejected := roundSourceRejected(t, scope, affected)
			rounds = append(rounds, rejected)
			observations, proposals := roundSourceRevision(t, rejected, affected)
			var err error
			scope, err = domain.ResolveCharacterArbitrationRevisionV1(rejected, observations, proposals)
			continuationMust(t, err)
		}
		if hardRound == 2 {
			rounds = append(rounds, hard(scope))
		} else {
			r := roundSourceReceipt(t, scope, 1)
			for i := range r.Resolutions {
				r.Resolutions[i].StateAfter = "AUTHOR_PRIVATE_SECRET"
			}
			value, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
			continuationMust(t, err)
			rounds = append(rounds, value)
		}
	}
	session := prefix.Session()
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	cycle := domain.CharacterActivationCycle{Version: domain.CharacterActivationCycleV3Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Index: len(prefix.Steps()) + 1, PreviousDigest: previous, ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: input.Digest, WorkContinuations: first.AdmissionContinuations(), Evidence: domain.CharacterAgentEvidenceBundle{Version: domain.CharacterActivationRoundEvidenceV3Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation, Observations: scope.Observations(), Proposals: scope.SubmittedProposals(), ProtocolDigest: "sha256:" + strings.Repeat("e", 64)}}
	for _, o := range first.Observations() {
		cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, o.MemoryRoot)
	}
	for _, round := range rounds {
		cycle.Evidence.Arbitrations = append(cycle.Evidence.Arbitrations, round.Receipt())
		n := round.Sources().CurrentRound()
		cycle.Evidence.Usage = append(cycle.Evidence.Usage, domain.CharacterAgentUsage{GenerationID: session.GenerationID, Chapter: session.Chapter, Cycle: cycle.Index, Round: n, AgentID: "world_arbiter", Role: "world_arbiter", Input: 1, Output: 1, Attempts: 1, Status: "success", CostSource: "unknown"})
		for _, p := range round.Sources().SubmittedProposals() {
			if p.Round == n {
				cycle.Evidence.Usage = append(cycle.Evidence.Usage, domain.CharacterAgentUsage{GenerationID: session.GenerationID, Chapter: session.Chapter, Cycle: cycle.Index, Round: n, AgentID: p.AgentID, Role: "character", Input: 1, Output: 1, Attempts: 1, Status: "success", CostSource: "unknown"})
			}
		}
	}
	return cycle
}

func v3Assess(t *testing.T, context domain.CharacterReadinessContext, prefix domain.VerifiedCharacterActivationPrefix, decision string) (domain.VerifiedCharacterActivationPrefix, domain.CharacterReadinessReviewAudit) {
	t.Helper()
	input, err := domain.NewCharacterReadinessReviewInputFromSteps(context, prefix.Session(), prefix.Steps(), "sha256:"+strings.Repeat("e", 64))
	continuationMust(t, err)
	verdict := testutil.ReadyVerdict(input)
	verdict.Decision = decision
	if decision == "hard_conflict" {
		for i := range verdict.ContractChecks {
			verdict.ContractChecks[i].Status = "impossible"
		}
	}
	receipt, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	continuationMust(t, err)
	codec, err := domain.NewCharacterReadinessModelCodecV1(input)
	continuationMust(t, err)
	binding := codec.Binding()
	audit := domain.CharacterReadinessReviewAudit{Input: input, Receipt: receipt, ModelView: &binding}
	continuationMust(t, domain.ValidateCharacterReadinessReviewAudit(audit))
	next, err := domain.ApplyVerifiedCharacterActivationReadiness(prefix, receipt)
	continuationMust(t, err)
	return next, audit
}

func TestActivationV3MixedR2GrantContinuesOriginalP2AndRestartsFlat(t *testing.T) {
	context, prefix, input := v3Fixture(t)
	baseline := prefix.Session()
	chapter := domain.CharacterActivationChapterEvidence{Context: context, ProtocolDigest: "sha256:" + strings.Repeat("e", 64)}
	var p2 domain.CharacterDecisionProposal
	for index := 1; index <= 3; index++ {
		var affected []string
		if index == 2 {
			scope := v3Scope(t, prefix, input)
			if len(scope.Continuations()) != 2 {
				t.Fatal("two independent grants were not continued")
			}
			affected = []string{scope.Continuations()[0].AgentID}
		}
		draft := v3Draft(t, prefix, input, affected, 0)
		cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, draft)
		continuationMust(t, err)
		step, pending, err := domain.VerifyCharacterActivationStep(prefix, input, cycle)
		continuationMust(t, err)
		if domain.ValidateCharacterActivationCycle(cycle) == nil || domain.ValidateCharacterAgentEvidenceBundle(cycle.Evidence) == nil {
			t.Fatal("old source-less validator accepted explicit V3")
		}
		if cycle.RoundSourceDigests[len(cycle.RoundSourceDigests)-1] != step.ArbitrationRounds()[len(step.ArbitrationRounds())-1].Sources().Digest() {
			t.Fatal("global root did not bind actual verified round sources")
		}
		if index == 1 && len(cycle.ContinuationEntryDigests) != 0 {
			t.Fatal("all-fresh genesis invented a work membership entry")
		}
		if index == 2 {
			if len(cycle.Evidence.Arbitrations) != 2 || len(cycle.WorkContinuations) != 2 || len(cycle.ContinuationEntryDigests) != 1 {
				t.Fatal("mixed revision did not retain admissions and only actual surviving continuation entry")
			}
			for _, p := range step.EffectiveProposals() {
				if p.Round == 2 {
					p2 = p
				}
			}
			ledger, ok := pending.ContinuationLedger(p2.AgentID)
			if !ok || ledger.Version != domain.CharacterWorkContinuationLedgerV2Version || ledger.ProposalDigest != p2.Digest || ledger.Origin.Digest != cycle.Digest {
				t.Fatal("R2 final grant did not derive from the real complete global step")
			}
			if domain.ValidateCharacterWorkContinuationLedgerV1(ledger) == nil {
				t.Fatal("legacy ledger verifier silently accepted V2 ledger")
			}
		}
		if index == 3 {
			found := false
			for _, p := range step.EffectiveProposals() {
				if p.AgentID == p2.AgentID {
					left, _ := json.Marshal(p)
					right, _ := json.Marshal(p2)
					found = string(left) == string(right)
				}
			}
			if !found || step.Cycle().Evidence.Arbitrations[0].Round != 1 {
				t.Fatal("original P2 was relabeled or not reused at next cycle R1")
			}
			ledger, _ := pending.ContinuationLedger(p2.AgentID)
			if len(ledger.Entries) != 1 || ledger.Entries[0].RoundSourceDigest != cycle.RoundSourceDigests[0] {
				t.Fatal("new P2 execution was not bound to actual current round")
			}
		}
		decision := "continue"
		if index == 3 {
			decision = "ready_for_plan"
		}
		next, audit := v3Assess(t, context, pending, decision)
		chapter.Cycles = append(chapter.Cycles, cycle)
		chapter.Inputs = append(chapter.Inputs, input)
		chapter.Reviews = append(chapter.Reviews, audit)
		prefix = next
		if index < 3 {
			input = v3NextInput(t, prefix)
		}
	}
	chapter.Session = prefix.Session()
	var err error
	chapter, err = domain.FinalizeCharacterActivationChapterEvidence(chapter)
	continuationMust(t, err)
	verified, err := domain.VerifyCharacterActivationChapter(chapter)
	continuationMust(t, err)
	sim, err := verified.BuildSimulation("", nil)
	continuationMust(t, err)
	continuationMust(t, verified.ValidateSimulation(sim))
	if !containsV3(sim.Sources, domain.CharacterActivationCyclePolicyV3) || containsV3(sim.Sources, domain.CharacterActivationCyclePolicyV2) {
		t.Fatal("simulation mislabeled V3 source as old V2 policy")
	}
	for _, trace := range sim.CharacterDecisionTrace {
		if trace.AgentID == p2.AgentID && trace.Cycle == 2 && trace.Continued {
			t.Fatal("R2 new choice was mislabeled as continued merely due to R1 admission")
		}
		if trace.AgentID == p2.AgentID && trace.Cycle == 3 && !trace.Continued {
			t.Fatal("actual next-cycle P2 continuation was lost")
		}
	}
	raw, err := json.Marshal(chapter)
	continuationMust(t, err)
	if strings.Contains(string(raw), `"verified":`) || strings.Contains(string(raw), `"ledger":`) {
		t.Fatal("flat evidence serialized runtime authority/recursive ledgers")
	}
	var decoded domain.CharacterActivationChapterEvidence
	continuationMust(t, json.Unmarshal(raw, &decoded))
	restored, err := domain.NewVerifiedCharacterActivationPrefix(baseline)
	continuationMust(t, err)
	for i, cycle := range decoded.Cycles {
		_, restored, err = domain.VerifyCharacterActivationStepV3(restored, decoded.Inputs[i], cycle)
		continuationMust(t, err)
		restored, err = domain.ApplyVerifiedCharacterActivationReadiness(restored, decoded.Reviews[i].Receipt)
		continuationMust(t, err)
	}
	if restored.Session().Digest != prefix.Session().Digest {
		t.Fatal("flat restart changed final cursor")
	}
	for _, actor := range restored.Steps()[2].AfterState().Actors {
		if actor.Character == "甲" || actor.Character == "乙" {
			if len(actor.TaskProgress) == 0 || math.Abs(actor.TaskProgress[0].Completed-3) > 1e-9 {
				t.Fatal("actual effective minutes were lost across revision")
			}
		}
	}
}

func containsV3(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestActivationV3AllContinuersMayReviseToFreshWithoutFakeEntries(t *testing.T) {
	context, prefix, input := v3Fixture(t)
	first, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 0))
	continuationMust(t, err)
	_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, first)
	continuationMust(t, err)
	prefix, _ = v3Assess(t, context, pending, "continue")
	input = v3NextInput(t, prefix)
	scope := v3Scope(t, prefix, input)
	var affected []string
	for _, r := range scope.Continuations() {
		affected = append(affected, r.AgentID)
	}
	cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, affected, 0))
	continuationMust(t, err)
	step, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
	continuationMust(t, err)
	if len(cycle.WorkContinuations) != 2 || len(cycle.ContinuationEntryDigests) != 0 || len(step.ArbitrationRounds()[1].Sources().Continuations()) != 0 {
		t.Fatal("all-to-fresh needed fabricated work entries")
	}
	if len(pending.ContinuationBoundaries()) != 0 {
		t.Fatal("V3 was certified using the old nonempty work-entry badge")
	}
	prefix, _ = v3Assess(t, context, pending, "continue")
	nextInput := v3NextInput(t, prefix)
	for _, id := range affected {
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, nextInput, id)
		continuationMust(t, err)
		if !eligible.Eligible {
			t.Fatalf("true R2 grant was not eligible after all-fresh global commit: %+v", eligible)
		}
	}
}

func TestActivationV3FreshR2GenesisAndZeroWorkHardGenesis(t *testing.T) {
	t.Run("fresh R2 grant becomes next R1 continuation", func(t *testing.T) {
		context, prefix, input := v3Fixture(t)
		scope := v3Scope(t, prefix, input)
		owner := scope.EffectiveProposals()[0].AgentID
		cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, []string{owner}, 0))
		continuationMust(t, err)
		if len(cycle.WorkContinuations) != 0 || len(cycle.ContinuationEntryDigests) != 0 || len(cycle.Evidence.Arbitrations) != 2 {
			t.Fatal("all-fresh R1/R2 genesis invented continuation authority")
		}
		step, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
		continuationMust(t, err)
		var original domain.CharacterDecisionProposal
		for _, p := range step.EffectiveProposals() {
			if p.AgentID == owner {
				original = p
			}
		}
		if original.Round != 2 {
			t.Fatal("actual P2 was not the effective genesis grant")
		}
		prefix, _ = v3Assess(t, context, pending, "continue")
		nextInput := v3NextInput(t, prefix)
		nextCycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, nextInput, v3Draft(t, prefix, nextInput, nil, 0))
		continuationMust(t, err)
		nextStep, _, err := domain.VerifyCharacterActivationStepV3(prefix, nextInput, nextCycle)
		continuationMust(t, err)
		if nextCycle.Evidence.Arbitrations[0].Round != 1 || len(nextCycle.ContinuationEntryDigests) != 2 {
			t.Fatal("next R1 did not execute both real origin grants")
		}
		for _, p := range nextStep.EffectiveProposals() {
			if p.AgentID == owner {
				left, _ := json.Marshal(p)
				right, _ := json.Marshal(original)
				if string(left) != string(right) {
					t.Fatal("origin P2 was relabeled for R1")
				}
			}
		}
	})
	t.Run("hard genesis", func(t *testing.T) {
		context, prefix, input := v3Fixture(t)
		baseline := prefix.Session()
		cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 1))
		continuationMust(t, err)
		_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
		continuationMust(t, err)
		if cycle.StartDay != cycle.EndDay || cycle.AfterPhysicalRoot != baseline.InitialPhysicalRoot || len(cycle.ContinuationEntryDigests) != 0 || len(pending.Steps()) != 1 {
			t.Fatal("zero-work hard genesis lost full global proof")
		}
		for _, entry := range input.Activation.Entries {
			if _, ok := pending.ContinuationLedger(entry.AgentID); ok {
				t.Fatal("unexecuted hard genesis minted a grant ledger")
			}
		}
		stopped, audit := v3Assess(t, context, pending, "hard_conflict")
		initial, err := domain.NewVerifiedCharacterActivationPrefix(baseline)
		continuationMust(t, err)
		_, restored, err := domain.VerifyCharacterActivationStepV3(initial, input, cycle)
		continuationMust(t, err)
		restored, err = domain.ApplyVerifiedCharacterActivationReadiness(restored, audit.Receipt)
		continuationMust(t, err)
		if restored.Session().Digest != stopped.Session().Digest {
			t.Fatal("hard genesis flat restart changed global identity")
		}
	})
}

// The field order and tags below are the complete pre-V3 cycle wire shape.
// It deliberately has no round_source_digests member.
type legacyActivationCycleWire struct {
	Version                  string                                      `json:"version"`
	GenerationID             string                                      `json:"generation_id"`
	Chapter                  int                                         `json:"chapter"`
	Index                    int                                         `json:"index"`
	PreviousDigest           string                                      `json:"previous_digest"`
	ChapterContextDigest     string                                      `json:"chapter_context_digest"`
	InputSetDigest           string                                      `json:"input_set_digest,omitempty"`
	BeforePhysicalRoot       string                                      `json:"before_physical_root"`
	AfterPhysicalRoot        string                                      `json:"after_physical_root"`
	StartDay                 float64                                     `json:"start_day"`
	EndDay                   float64                                     `json:"end_day"`
	Evidence                 domain.CharacterAgentEvidenceBundle         `json:"evidence"`
	WorkContinuations        []domain.CharacterWorkContinuationReceiptV1 `json:"work_continuations,omitempty"`
	ContinuationEntryDigests []string                                    `json:"continuation_entry_digests,omitempty"`
	Digest                   string                                      `json:"digest"`
}

func TestActivationV3KeepsLegacyV1V2WireAndGoldenDigestFormula(t *testing.T) {
	legacy := testutil.CharacterCycle(t, 1, "", nil, 0)
	prefix, origin := verifiedWorkPrefixFixture(t)
	input, draft := verifiedWorkDraft(t, prefix, origin)
	v2, err := domain.FinalizeCharacterContinuationCycleV2(prefix, input, draft)
	continuationMust(t, err)
	for _, cycle := range []domain.CharacterActivationCycle{legacy, v2} {
		old := legacyActivationCycleWire{cycle.Version, cycle.GenerationID, cycle.Chapter, cycle.Index, cycle.PreviousDigest, cycle.ChapterContextDigest, cycle.InputSetDigest, cycle.BeforePhysicalRoot, cycle.AfterPhysicalRoot, cycle.StartDay, cycle.EndDay, cycle.Evidence, cycle.WorkContinuations, cycle.ContinuationEntryDigests, cycle.Digest}
		want, _ := json.Marshal(old)
		got, _ := json.Marshal(cycle)
		if string(want) != string(got) || strings.Contains(string(got), "round_source_digest") {
			t.Fatal("V3 added bytes to old cycle wire")
		}
		old.Digest = ""
		var hash string
		if cycle.Version == domain.CharacterActivationCycleVersion {
			raw, err := domain.DeterministicPlanningHash(old)
			continuationMust(t, err)
			hash = "sha256:" + raw
		} else {
			raw, _ := json.Marshal(old)
			hash, err = domain.ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
			continuationMust(t, err)
		}
		if hash != cycle.Digest {
			t.Fatal("legacy golden digest algorithm changed")
		}
	}
	_, next, err := domain.VerifyCharacterActivationStep(prefix, input, v2)
	continuationMust(t, err)
	for _, admission := range v2.WorkContinuations {
		ledger, _ := next.ContinuationLedger(admission.AgentID)
		if ledger.Version != domain.CharacterWorkContinuationPolicyV1 {
			t.Fatal("V3 upgraded an old ledger")
		}
		for _, entry := range ledger.Entries {
			raw, _ := json.Marshal(entry)
			if strings.Contains(string(raw), "round_source_digest") {
				t.Fatal("V3 added source fields to an old work entry")
			}
		}
	}
}

func TestActivationV3HardR1AndR2AreRealZeroAdvanceGlobalStops(t *testing.T) {
	for _, round := range []int{1, 2} {
		t.Run(string(rune('0'+round)), func(t *testing.T) {
			context, prefix, input := v3Fixture(t)
			first, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 0))
			continuationMust(t, err)
			_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, first)
			continuationMust(t, err)
			prefix, _ = v3Assess(t, context, pending, "continue")
			input = v3NextInput(t, prefix)
			before := prefix.Session()
			var affected []string
			if round == 2 {
				affected = []string{v3Scope(t, prefix, input).Continuations()[0].AgentID}
			}
			cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, affected, round))
			continuationMust(t, err)
			step, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
			continuationMust(t, err)
			if !pending.Session().PendingHardConflict || cycle.StartDay != cycle.EndDay || cycle.BeforePhysicalRoot != cycle.AfterPhysicalRoot || len(cycle.ContinuationEntryDigests) != 0 || !reflect.DeepEqual(step.AfterState(), *input.Stimulus.PhysicalState) {
				t.Fatal("hard stop fabricated advancement or lost full global identity")
			}
			for _, r := range cycle.WorkContinuations {
				old, _ := prefix.ContinuationLedger(r.AgentID)
				after, _ := pending.ContinuationLedger(r.AgentID)
				if !reflect.DeepEqual(old, after) {
					t.Fatal("hard diagnosis advanced/replaced continuation ledger")
				}
			}
			if _, err := domain.ApplyVerifiedCharacterActivationReadiness(pending, testutil.CycleReadiness(t, cycle, "continue")); err == nil {
				t.Fatal("readiness overrode hard infeasibility")
			}
			stopped, _ := v3Assess(t, context, pending, "hard_conflict")
			if stopped.Session().Phase != "hard_conflict" || stopped.Session().CurrentDay != before.CurrentDay {
				t.Fatal("hard receipt did not close the exact unchanged state")
			}
			if _, _, err := domain.VerifyCharacterActivationStepV3(stopped, input, cycle); err == nil {
				t.Fatal("hard-stopped chapter continued")
			}
		})
	}
}

func TestActivationV3MixedRevisionSharesOneResourceSettlement(t *testing.T) {
	context, prefix, input := v3Fixture(t)
	first, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 0))
	continuationMust(t, err)
	_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, first)
	continuationMust(t, err)
	prefix, _ = v3Assess(t, context, pending, "continue")
	input = v3NextInput(t, prefix)
	scope := v3Scope(t, prefix, input)
	var freshOwner string
	for _, source := range scope.Sources() {
		if source.Kind == "fresh" {
			freshOwner = source.AgentID
		}
	}
	draft := v3Draft(t, prefix, input, []string{freshOwner}, 0)
	r1, err := domain.FinalizeCharacterArbitrationRoundV1(scope, draft.Evidence.Arbitrations[0])
	continuationMust(t, err)
	var observations []domain.CharacterObservationPacket
	var proposals []domain.CharacterDecisionProposal
	for _, o := range draft.Evidence.Observations {
		if o.Round == 2 {
			observations = append(observations, o)
		}
	}
	for _, p := range draft.Evidence.Proposals {
		if p.Round == 2 {
			proposals = append(proposals, p)
		}
	}
	r2Scope, err := domain.ResolveCharacterArbitrationRevisionV1(r1, observations, proposals)
	continuationMust(t, err)
	r2 := continuationCopy(draft.Evidence.Arbitrations[1])
	for i := range r2.Resolutions {
		for _, a := range input.Stimulus.PhysicalState.Actors {
			if a.AgentID == r2.Resolutions[i].AgentID {
				copy := continuationCopy(a)
				r2.Resolutions[i].PostState = &copy
			}
		}
	}
	before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
	r2.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: continuationNumber(-1), After: continuationNumber(before - 1), EvidenceRefs: []string{r2Scope.EffectiveProposals()[0].Digest}}}
	checked, err := domain.FinalizeCharacterArbitrationRoundV1(r2Scope, r2)
	continuationMust(t, err)
	draft.Evidence.Arbitrations[1] = checked.Receipt()
	cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, draft)
	continuationMust(t, err)
	step, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
	continuationMust(t, err)
	if len(cycle.ContinuationEntryDigests) != 2 || *step.AfterState().Resources[0].ActualAmount != before-1 {
		t.Fatal("shared settlement was counted per continuing actor")
	}
	for _, admission := range cycle.WorkContinuations {
		ledger, _ := pending.ContinuationLedger(admission.AgentID)
		entry := ledger.Entries[len(ledger.Entries)-1]
		if entry.AfterPhysicalRoot != cycle.AfterPhysicalRoot || len(entry.Arbitration.ResourceSettlements) != 1 {
			t.Fatal("owner entry lost the common actual resource result")
		}
	}
	prefix, _ = v3Assess(t, context, pending, "continue")
	nextInput := v3NextInput(t, prefix)
	if *nextInput.Stimulus.PhysicalState.Resources[0].ActualAmount != before-1 {
		t.Fatal("rebuilding next input debited the already settled resource again")
	}
	bad := continuationCopy(r2)
	bad.ResourceSettlements = append(bad.ResourceSettlements, bad.ResourceSettlements[0])
	if _, err := domain.FinalizeCharacterArbitrationRoundV1(r2Scope, bad); err == nil {
		t.Fatal("two owners independently settled the same global resource")
	}
}

func TestActivationV3RejectsResignedSourceMembershipUsageAndOldPolicy(t *testing.T) {
	context, prefix, input := v3Fixture(t)
	first, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, nil, 0))
	continuationMust(t, err)
	_, pending, err := domain.VerifyCharacterActivationStepV3(prefix, input, first)
	continuationMust(t, err)
	prefix, _ = v3Assess(t, context, pending, "continue")
	input = v3NextInput(t, prefix)
	owner := v3Scope(t, prefix, input).Continuations()[0].AgentID
	cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, v3Draft(t, prefix, input, []string{owner}, 0))
	continuationMust(t, err)
	for name, mutate := range map[string]func(*domain.CharacterActivationCycle){
		"round root":     func(c *domain.CharacterActivationCycle) { c.RoundSourceDigests[1] = c.RoundSourceDigests[0] },
		"member root":    func(c *domain.CharacterActivationCycle) { c.ContinuationEntryDigests = nil },
		"drop R1":        func(c *domain.CharacterActivationCycle) { c.Evidence.Arbitrations = c.Evidence.Arbitrations[1:] },
		"foreign prefix": func(c *domain.CharacterActivationCycle) { c.PreviousDigest = projectedV3BadDigest() },
		"rewrite original admission": func(c *domain.CharacterActivationCycle) {
			c.WorkContinuations[0].OriginProposalDigest = projectedV3BadDigest()
			c.WorkContinuations[0].Digest, _ = domain.ComputeCharacterWorkContinuationReceiptV1Digest(c.WorkContinuations[0])
		},
		"invent continuer call": func(c *domain.CharacterActivationCycle) {
			c.Evidence.Usage = append(c.Evidence.Usage, domain.CharacterAgentUsage{GenerationID: c.GenerationID, Chapter: c.Chapter, Cycle: c.Index, Round: 1, Role: "character", AgentID: owner})
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := continuationCopy(cycle)
			mutate(&bad)
			bad.Digest, err = domain.CharacterActivationCycleRootV3(bad)
			continuationMust(t, err)
			before := prefix.Session()
			if _, _, err := domain.VerifyCharacterActivationStepV3(prefix, input, bad); err == nil {
				t.Fatal("resigned partial/foreign evidence acquired global authority")
			}
			if !reflect.DeepEqual(before, prefix.Session()) {
				t.Fatal("failed verifier changed prefix")
			}
		})
	}
	oldPrefix, oldInput, _ := roundSourcePrefix(t, false)
	if _, err := domain.FinalizeCharacterActivationCycleV3(oldPrefix, oldInput, cycle); err == nil {
		t.Fatal("V3 silently upgraded old input/session history")
	}
	downgraded := continuationCopy(cycle)
	downgraded.Version = domain.CharacterActivationCycleVersion
	if _, _, err := domain.VerifyCharacterActivationStep(prefix, input, downgraded); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("V3 prefix silently reentered legacy dispatch: %v", err)
	}
	var forged domain.VerifiedCharacterActivationStep
	_ = json.Unmarshal([]byte(`{"verified":true,"rounds":[{}]}`), &forged)
	if len(forged.ArbitrationRounds()) != 0 {
		t.Fatal("JSON forged verified round capabilities")
	}
}

func projectedV3BadDigest() string { return "sha256:" + strings.Repeat("f", 64) }

func TestActivationV3GettersAndRepeatedFinalizationAreRaceSafe(t *testing.T) {
	_, prefix, input := v3Fixture(t)
	draft := v3Draft(t, prefix, input, nil, 0)
	cycle, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, draft)
	continuationMust(t, err)
	step, _, err := domain.VerifyCharacterActivationStepV3(prefix, input, cycle)
	continuationMust(t, err)
	before, _ := json.Marshal(cycle)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rounds := step.ArbitrationRounds()
			r := rounds[0].Receipt()
			r.Resolutions[0].Decision = "changed"
			p := rounds[0].Sources().EffectiveProposals()
			p[0].KnowledgeRefs[0] = "changed"
			state := step.AfterState()
			state.Actors[0].Location = "changed"
			got, err := domain.FinalizeCharacterActivationCycleV3(prefix, input, draft)
			if err == nil && got.Digest != cycle.Digest {
				err = fmt.Errorf("concurrent root drift")
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		continuationMust(t, err)
	}
	after, _ := json.Marshal(step.Cycle())
	if string(before) != string(after) {
		t.Fatal("getter aliased verified cycle")
	}
}
