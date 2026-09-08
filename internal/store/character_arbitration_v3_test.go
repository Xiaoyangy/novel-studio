package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

var arbitrationV3Policies = []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3}
var arbitrationV3Protocol = "sha256:" + strings.Repeat("e", 64)

func arbitrationV3Rebind(t *testing.T, input domain.CharacterActivationInputSet, session domain.CharacterActivationSession, all bool) domain.CharacterActivationInputSet {
	t.Helper()
	input = verifiedStoreCopy(input)
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(session.CycleDigests)+1, session.ChapterContextDigest, previous)
	verifiedStoreMust(t, err)
	input.Stimulus.Sources = append(append([]string(nil), arbitrationV3Policies...), token)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	verifiedStoreMust(t, err)
	input.Stimulus.StoryClock.CurrentDay = session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	verifiedStoreMust(t, err)
	input.Stimulus.StoryClock = &clock
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	verifiedStoreMust(t, err)
	cc, err := domain.NewCharacterObservationCycleContext(session)
	verifiedStoreMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.Round, o.ConflictFeedback = 1, nil
		o.Sources, o.CycleContext, o.StimulusDigest = append([]string(nil), arbitrationV3Policies...), cc, input.Stimulus.Digest
		for j := range o.KnownFacts {
			if o.KnownFacts[j].Source != "" {
				o.KnownFacts[j].Source = domain.CharacterSourceRefV2(o.AgentID, o.KnownFacts[j].Source)
			}
		}
		for j := range o.PerceivedEvents {
			if o.PerceivedEvents[j].Source != "" {
				o.PerceivedEvents[j].Source = domain.CharacterSourceRefV2(o.AgentID, o.PerceivedEvents[j].Source)
			}
		}
		for j := range o.PublicRules {
			if o.PublicRules[j].Source != "" {
				o.PublicRules[j].Source = domain.CharacterSourceRefV2(o.AgentID, o.PublicRules[j].Source)
			}
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(*input.Stimulus.PhysicalState, o.AgentID)
		verifiedStoreMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(*input.Stimulus.PhysicalState, o.AgentID)
		verifiedStoreMust(t, err)
		for _, m := range input.Memories {
			if m.AgentID == o.AgentID {
				o.MemoryRoot, o.Memory = m.MemoryRoot, verifiedStoreCopy(m.Facts)
			}
		}
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		verifiedStoreMust(t, err)
		for j := range input.Activation.Entries {
			e := &input.Activation.Entries[j]
			if e.AgentID != o.AgentID {
				continue
			}
			if all {
				e.State = domain.CharacterAgentActive
			}
			if e.State == domain.CharacterAgentActive {
				e.ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	verifiedStoreMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	verifiedStoreMust(t, err)
	return input
}

func arbitrationV3Setup(t *testing.T) (*Store, domain.CharacterReadinessContext, domain.VerifiedCharacterActivationPrefix, domain.CharacterActivationInputSet) {
	t.Helper()
	context, input, _ := verifiedStoreOrigin(t)
	physical, err := domain.PrepareCharacterSelfChronologyStateV1(*input.Stimulus.PhysicalState)
	verifiedStoreMust(t, err)
	input.Stimulus.PhysicalState = &physical
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, context.Digest, physical, 0, 8)
	verifiedStoreMust(t, err)
	input = arbitrationV3Rebind(t, input, session, false)
	st := NewStore(t.TempDir())
	verifiedStoreMust(t, st.Init())
	verifiedStoreMust(t, st.SaveCharacterReadinessContext(context))
	verifiedStoreMust(t, st.CreateCharacterActivationSession(session))
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proofs.PublishActivationInputs(input))
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
	verifiedStoreMust(t, err)
	return st, context, *prefix, input
}

func arbitrationV3Fresh(t *testing.T, o domain.CharacterObservationPacket) domain.CharacterDecisionProposal {
	t.Helper()
	p := domain.CharacterDecisionProposal{GenerationID: o.GenerationID, Chapter: o.Chapter, Round: o.Round, AgentID: o.AgentID, Character: o.Character, ObservationDigest: o.Digest, Location: o.Location, CurrentGoal: o.CurrentGoal, Pressure: o.Pressure, AvailableOptions: []string{"检查", "等待"}, Decision: "检查", DecisionReason: "本人选择", IntendedAction: "原地常规检查", ActionDuration: "本段有效一分钟", KnowledgeRefs: []string{o.KnownFacts[0].ID}, SelfTasks: []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "常规检查", ProgressTarget: verifiedStoreNumber(5), ProgressUnit: "minute", KnowledgeRefs: []string{o.KnownFacts[0].ID}}}, WorkContinuations: []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: true}}}
	if o.Character == "丙" {
		p.SelfTasks[0].TaskID = fmt.Sprintf("fresh-%d", o.CycleContext.Index)
		p.SelfTasks[0].ProgressTarget = verifiedStoreNumber(1)
		p.WorkContinuations = nil
	}
	p, err := domain.FinalizeCharacterDecisionProposal(p, o)
	verifiedStoreMust(t, err)
	return p
}

func arbitrationV3Prepare(t *testing.T, st *Store, prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet) *CharacterArbitrationV3 {
	t.Helper()
	proofs, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, proofs.PublishActivationInputs(input))
	var continuations []domain.CharacterWorkContinuationReceiptV1
	var fresh []domain.CharacterDecisionProposal
	for _, e := range input.Activation.Entries {
		if e.State != domain.CharacterAgentActive {
			continue
		}
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, e.AgentID)
		verifiedStoreMust(t, err)
		if eligible.Eligible {
			continuations = append(continuations, *eligible.Receipt)
			continue
		}
		for _, o := range input.Observations {
			if o.AgentID == e.AgentID {
				fresh = append(fresh, arbitrationV3Fresh(t, o))
			}
		}
	}
	v, err := st.PrepareCharacterArbitrationV3(prefix.Session(), continuations, arbitrationV3Protocol)
	verifiedStoreMust(t, err)
	if len(fresh) > 0 {
		if _, err := v.Sources(1); err == nil {
			t.Fatal("missing fresh proposal appeared complete")
		}
	}
	for _, p := range fresh {
		verifiedStoreMust(t, v.SaveProposal(p))
	}
	return v
}

func arbitrationV3Receipt(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, affected []string, hard, consume bool) domain.WorldArbitrationReceipt {
	t.Helper()
	input := scope.Input()
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	if len(affected) > 0 || hard {
		end = start
	}
	r := domain.WorldArbitrationReceipt{Version: domain.WorldArbitrationReceiptV2Version, GenerationID: input.Stimulus.GenerationID, Chapter: input.Stimulus.Chapter, Round: scope.CurrentRound(), StoryTime: &domain.StoryTimeChapterSchedule{Chapter: input.Stimulus.Chapter, StartDay: start, EndDay: end}, StimulusDigest: input.Stimulus.Digest, ActivationDigest: input.Activation.Digest, Finalized: len(affected) == 0 && !hard, HardContractStatus: "feasible"}
	if hard {
		r.HardContractStatus = "infeasible"
		r.HardContractConflicts = []string{"明确硬合同不能共同满足"}
	}
	if len(affected) > 0 {
		r.Conflicts = []domain.WorldArbitrationConflict{{ID: "actual-conflict", Kind: "time", AffectedAgentIDs: affected, Feedback: "请明确本人本段顺序，本段尚未执行"}}
	}
	for i, p := range scope.EffectiveProposals() {
		var post domain.CharacterPhysicalStateV2
		for _, a := range input.Stimulus.PhysicalState.Actors {
			if a.AgentID == p.AgentID {
				post = verifiedStoreCopy(a)
			}
		}
		status := "in_progress"
		if p.Character == "丙" {
			status = "completed"
		}
		resolution := domain.CharacterDecisionResolution{AgentID: p.AgentID, Character: p.Character, ProposalDigest: p.Digest, Decision: p.Decision, IntendedAction: p.IntendedAction, ActionOrder: i + 1, Outcome: "success", CompletionState: status, ImmediateResult: "本人结果", StateAfter: "AUTHOR_PRIVATE_SECRET", PostState: &post, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "本段结束", TransmissionPath: "实际行动", ArrivalChapter: 1, ProtagonistImpact: "后续有据"}}}
		if r.Finalized {
			resolution.SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: p.SelfTasks[0].TaskID, Status: status, StartDay: &start, EndDay: &end}}
		}
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		r.Resolutions = append(r.Resolutions, resolution)
	}
	if consume {
		before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
		r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: verifiedStoreNumber(-1), After: verifiedStoreNumber(before - 1), EvidenceRefs: r.ProposalDigests}}
	}
	verified, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	verifiedStoreMust(t, err)
	return verified.Receipt()
}

func arbitrationV3Revisions(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, r1 domain.WorldArbitrationReceipt, ids []string) ([]domain.CharacterObservationPacket, []domain.CharacterDecisionProposal) {
	t.Helper()
	var observations []domain.CharacterObservationPacket
	var proposals []domain.CharacterDecisionProposal
	for _, id := range ids {
		var o domain.CharacterObservationPacket
		for _, old := range scope.Input().Observations {
			if old.AgentID == id {
				o = old
			}
		}
		o.Round, o.GeneratedAt = 2, "2026-09-07T12:00:00Z"
		o.ConflictFeedback = []string{"time：" + r1.Conflicts[0].Feedback}
		var err error
		o, err = domain.FinalizeCharacterObservationPacket(o)
		verifiedStoreMust(t, err)
		p := arbitrationV3Fresh(t, o)
		p.DecisionReason = "本人根据冲突反馈重新选择"
		p, err = domain.FinalizeCharacterDecisionProposal(p, o)
		verifiedStoreMust(t, err)
		observations, proposals = append(observations, o), append(proposals, p)
	}
	return observations, proposals
}

func arbitrationV3Next(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix) domain.CharacterActivationInputSet {
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
			verifiedStoreMust(t, err)
			for i, m := range input.Memories {
				if m.AgentID != p.AgentID {
					continue
				}
				m.Facts = append(m.Facts, domain.CharacterAgentMemoryFact{ID: "actual-" + r.Digest, Chapter: 1, Kind: "projected_decision", Text: text, SourceDigest: r.Digest})
				m, err = domain.FinalizeCharacterAgentMemory(m)
				verifiedStoreMust(t, err)
				input.Memories[i] = m
			}
		}
	}
	return arbitrationV3Rebind(t, input, prefix.Session(), true)
}

func arbitrationV3Audit(t *testing.T, context domain.CharacterReadinessContext, prefix domain.VerifiedCharacterActivationPrefix, decision string) domain.CharacterReadinessReviewAudit {
	t.Helper()
	input, err := domain.NewCharacterReadinessReviewInputFromSteps(context, prefix.Session(), prefix.Steps(), arbitrationV3Protocol)
	verifiedStoreMust(t, err)
	verdict := testutil.ReadyVerdict(input)
	verdict.Decision = decision
	if decision == "hard_conflict" {
		for i := range verdict.ContractChecks {
			verdict.ContractChecks[i].Status = "impossible"
		}
	}
	receipt, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	verifiedStoreMust(t, err)
	codec, err := domain.NewCharacterReadinessModelCodecV1(input)
	verifiedStoreMust(t, err)
	binding := codec.Binding()
	return domain.CharacterReadinessReviewAudit{Input: input, Receipt: receipt, ModelView: &binding}
}

func arbitrationV3Assess(t *testing.T, st *Store, context domain.CharacterReadinessContext, cycle domain.CharacterActivationCycle, before domain.CharacterActivationSession, decision string) domain.VerifiedCharacterActivationPrefix {
	t.Helper()
	_, err := st.AppendCharacterActivationCycle(before.Digest, cycle)
	verifiedStoreMust(t, err)
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(before.GenerationID, before.Chapter)
	verifiedStoreMust(t, err)
	audit := arbitrationV3Audit(t, context, *prefix, decision)
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	_, err = st.ApplyVerifiedCharacterChapterReadiness(prefix.Session().Digest, audit.Receipt)
	verifiedStoreMust(t, err)
	prefix, err = st.LoadVerifiedCharacterActivationPrefix(before.GenerationID, before.Chapter)
	verifiedStoreMust(t, err)
	return *prefix
}

func TestArbitrationV3StoreFreshMixedRevisionAndOriginalP2Restart(t *testing.T) {
	st, context, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	sources, err := v.Sources(1)
	verifiedStoreMust(t, err)
	r := arbitrationV3Receipt(t, sources, nil, false, false)
	verifiedStoreMust(t, v.SaveArbitration(r))
	c1, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	prefix = arbitrationV3Assess(t, st, context, c1, prefix.Session(), "continue")
	input = arbitrationV3Next(t, prefix)
	v = arbitrationV3Prepare(t, st, prefix, input)
	if len(v.Continuations()) != 2 {
		t.Fatal("expected two original grant continuers")
	}
	sources, err = v.Sources(1)
	verifiedStoreMust(t, err)
	owner := v.Continuations()[0].AgentID
	r1 := arbitrationV3Receipt(t, sources, []string{owner}, false, false)
	before := prefix.Session()
	verifiedStoreMust(t, v.SaveArbitration(r1))
	if _, err := v.FinalizeCycle(nil); err == nil {
		t.Fatal("provisional R1 published a cycle")
	}
	current, err := st.LoadCharacterActivationSession(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(*current, before) {
		t.Fatal("saving R1 advanced the session")
	}
	st = NewStore(st.CharacterAgents.io.dir)
	v, err = st.LoadCharacterArbitrationV3(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	loaded, err := v.LoadArbitration(1)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(loaded, &r1) {
		t.Fatal("restart lost original R1")
	}
	obs, proposals := arbitrationV3Revisions(t, sources, r1, []string{owner})
	verifiedStoreMust(t, v.SaveObservation(obs[0]))
	st = NewStore(st.CharacterAgents.io.dir)
	v, err = st.LoadCharacterArbitrationV3(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	if _, err := v.Sources(2); err == nil {
		t.Fatal("missing P2 was treated as old P1")
	}
	verifiedStoreMust(t, v.SaveProposal(proposals[0]))
	s2, err := v.Sources(2)
	verifiedStoreMust(t, err)
	if len(s2.Continuations()) != 1 {
		t.Fatal("unaffected continuer was reasked or affected one not replaced")
	}
	r2 := arbitrationV3Receipt(t, s2, nil, false, true)
	verifiedStoreMust(t, v.SaveArbitration(r2))
	c2, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	if len(c2.Evidence.Arbitrations) != 2 || len(c2.ContinuationEntryDigests) != 1 {
		t.Fatal("wrong complete R1/R2 evidence")
	}
	prefix = arbitrationV3Assess(t, st, context, c2, before, "continue")
	st = NewStore(st.CharacterAgents.io.dir)
	loadedPrefix, err := st.LoadVerifiedCharacterActivationPrefix(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	prefix = *loadedPrefix
	input = arbitrationV3Next(t, prefix)
	v = arbitrationV3Prepare(t, st, prefix, input)
	s3, err := v.Sources(1)
	verifiedStoreMust(t, err)
	matched := false
	for _, p := range s3.EffectiveProposals() {
		if p.AgentID == owner {
			matched = jsonValuesEqual(p, proposals[0]) && p.Round == 2
		}
	}
	if !matched {
		t.Fatal("next-cycle R1 did not preserve actual original P2")
	}
	verifiedStoreMust(t, v.SaveArbitration(arbitrationV3Receipt(t, s3, nil, false, false)))
	c3, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	_, err = st.AppendCharacterActivationCycle(prefix.Session().Digest, c3)
	verifiedStoreMust(t, err)
	loadedPrefix, err = NewStore(st.CharacterAgents.io.dir).LoadVerifiedCharacterActivationPrefix(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	if len(loadedPrefix.Steps()) != 3 {
		t.Fatal("flat restore lost steps")
	}
	legacyLoad, err := st.LoadCharacterActivationCycle(before.GenerationID, 1, 3)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(*legacyLoad, c3) {
		t.Fatal("normal loader did not dispatch V3")
	}
	_, err = st.CharacterAgents.LoadActivationInputsForCycle(c3)
	verifiedStoreMust(t, err)
	audit := arbitrationV3Audit(t, context, *loadedPrefix, "ready_for_plan")
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	_, err = st.ApplyVerifiedCharacterChapterReadiness(loadedPrefix.Session().Digest, audit.Receipt)
	verifiedStoreMust(t, err)
	evidence, err := st.CollectCharacterActivationChapterEvidence(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	if len(evidence.Cycles) != 3 || len(evidence.Inputs) != 3 || len(evidence.Reviews) != 3 {
		t.Fatal("chapter collector lost flat source evidence")
	}
	verifiedStoreMust(t, domain.ValidateCharacterActivationChapterEvidence(*evidence))
	loadedChapter, err := st.LoadVerifiedCharacterActivationChapter(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	if !jsonValuesEqual(loadedChapter.Evidence(), *evidence) {
		t.Fatal("local chapter read lost exact flat proof")
	}
	// A self-contained envelope cannot hide corruption in its actual local
	// paid source files when the source-aware Store reader is used.
	chapterRoot, err := characterActivationSessionDir(before.GenerationID, 1)
	verifiedStoreMust(t, err)
	p2Path := filepath.Join(chapterRoot, "work", "000002", "proof", "proposals", "round-02", owner+".json")
	forgedP2 := proposals[0]
	forgedP2.DecisionReason = "篡改已落原始提案"
	raw, err := json.Marshal(forgedP2)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, os.WriteFile(st.CharacterAgents.io.path(p2Path), raw, 0600))
	beforeRoot, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if _, err := st.LoadVerifiedCharacterActivationChapter(before.GenerationID, 1); err == nil {
		t.Fatal("chapter loader trusted envelope over damaged local original")
	}
	afterRoot, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if beforeRoot != afterRoot {
		t.Fatal("failed read repaired or changed evidence")
	}
}

func TestArbitrationV3StoreAllContinuersReviseAndPartialR2Recovers(t *testing.T) {
	st, context, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	scope, err := v.Sources(1)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, v.SaveArbitration(arbitrationV3Receipt(t, scope, nil, false, false)))
	c1, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	prefix = arbitrationV3Assess(t, st, context, c1, prefix.Session(), "continue")
	input = arbitrationV3Next(t, prefix)
	v = arbitrationV3Prepare(t, st, prefix, input)
	scope, err = v.Sources(1)
	verifiedStoreMust(t, err)
	ids := []string{v.Continuations()[0].AgentID, v.Continuations()[1].AgentID}
	r1 := arbitrationV3Receipt(t, scope, ids, false, false)
	verifiedStoreMust(t, v.SaveArbitration(r1))
	obs, ps := arbitrationV3Revisions(t, scope, r1, ids)
	for _, o := range obs {
		verifiedStoreMust(t, v.SaveObservation(o))
	}
	verifiedStoreMust(t, v.SaveProposal(ps[0]))
	st = NewStore(st.dir)
	v, err = st.LoadCharacterArbitrationV3(prefix.Session().GenerationID, 1)
	verifiedStoreMust(t, err)
	if _, err := v.Sources(2); err == nil {
		t.Fatal("partial multi-owner R2 was treated as complete")
	}
	verifiedStoreMust(t, v.SaveProposal(ps[1]))
	s2, err := v.Sources(2)
	verifiedStoreMust(t, err)
	if len(s2.Continuations()) != 0 {
		t.Fatal("superseded continuation still executes")
	}
	verifiedStoreMust(t, v.SaveArbitration(arbitrationV3Receipt(t, s2, nil, false, false)))
	c2, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	if len(c2.ContinuationEntryDigests) != 0 {
		t.Fatal("all-fresh R2 fabricated work entries")
	}
	prefix = arbitrationV3Assess(t, st, context, c2, prefix.Session(), "continue")
	input = arbitrationV3Next(t, prefix)
	v = arbitrationV3Prepare(t, st, prefix, input)
	s3, err := v.Sources(1)
	verifiedStoreMust(t, err)
	for _, source := range s3.Sources() {
		if source.Kind == "continuation" && source.Origin.Round != 2 {
			t.Fatal("new grants lost their real R2 origin")
		}
	}
}

func TestArbitrationV3StoreHardR1R2NeverAdvanceAndAreRecoverable(t *testing.T) {
	for _, round := range []int{1, 2} {
		t.Run(fmt.Sprintf("round%d", round), func(t *testing.T) {
			st, context, prefix, input := arbitrationV3Setup(t)
			v := arbitrationV3Prepare(t, st, prefix, input)
			scope, err := v.Sources(1)
			verifiedStoreMust(t, err)
			if round == 2 {
				id := scope.EffectiveProposals()[0].AgentID
				r1 := arbitrationV3Receipt(t, scope, []string{id}, false, false)
				verifiedStoreMust(t, v.SaveArbitration(r1))
				obs, ps := arbitrationV3Revisions(t, scope, r1, []string{id})
				verifiedStoreMust(t, v.SaveObservation(obs[0]))
				verifiedStoreMust(t, v.SaveProposal(ps[0]))
				scope, err = v.Sources(2)
				verifiedStoreMust(t, err)
			}
			r := arbitrationV3Receipt(t, scope, nil, true, false)
			verifiedStoreMust(t, v.SaveArbitration(r))
			st = NewStore(st.dir)
			v, err = st.LoadCharacterArbitrationV3(prefix.Session().GenerationID, 1)
			verifiedStoreMust(t, err)
			c, err := v.FinalizeCycle(nil)
			verifiedStoreMust(t, err)
			if c.StartDay != c.EndDay || c.BeforePhysicalRoot != c.AfterPhysicalRoot || len(c.ContinuationEntryDigests) != 0 {
				t.Fatal("hard result advanced actual state")
			}
			prefix = arbitrationV3Assess(t, st, context, c, prefix.Session(), "hard_conflict")
			if prefix.Session().PendingHardConflict || prefix.Session().Phase != "hard_conflict" {
				t.Fatalf("hard stop not retained: %s", prefix.Session().Phase)
			}
			if _, err := st.PrepareCharacterArbitrationV3(prefix.Session(), nil, arbitrationV3Protocol); err == nil {
				t.Fatal("hard stop reopened model work")
			}
		})
	}
}

func TestArbitrationV3StoreCycleAndReadinessBeforeCursorRecoveryIsIdempotent(t *testing.T) {
	st, context, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	scope, err := v.Sources(1)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, v.SaveArbitration(arbitrationV3Receipt(t, scope, nil, false, false)))
	cycle, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	root, err := characterActivationSessionDir(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.writeCharacterActivationJSON(activationCyclePath(root, 1), cycle, true))
	loaded, err := st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	if loaded.Session().Digest != prefix.Session().Digest {
		t.Fatal("read-only load accepted orphan")
	}
	if _, err := st.LoadCharacterActivationCycle(cycle.GenerationID, 1, 1); err == nil {
		t.Fatal("bare orphan became verified")
	}
	recovered, err := NewStore(st.dir).RecoverCharacterActivationSession(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	if recovered.Phase != "assessing" || len(recovered.CycleDigests) != 1 {
		t.Fatal("orphan cycle not recovered")
	}
	loaded, err = st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	audit := arbitrationV3Audit(t, context, *loaded, "continue")
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	verifiedStoreMust(t, st.writeCharacterActivationJSON(activationReadinessPath(root, 1), audit.Receipt, true))
	recovered, err = NewStore(st.dir).RecoverCharacterActivationSession(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	if recovered.Phase != "collecting" || len(recovered.ReadinessDigests) != 1 {
		t.Fatal("orphan readiness not recovered")
	}
	before, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
	verifiedStoreMust(t, err)
	_, err = st.AppendVerifiedCharacterActivationCycle("old identical retry", cycle)
	verifiedStoreMust(t, err)
	_, err = st.RecoverVerifiedCharacterActivationSession(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	after, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
	verifiedStoreMust(t, err)
	if string(before) != string(after) {
		t.Fatal("recovery changed cursor again")
	}
}

func TestArbitrationV3StoreConcurrentPaidProofsAndCycleCAS(t *testing.T) {
	st, context, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	scope, err := v.Sources(1)
	verifiedStoreMust(t, err)
	r := arbitrationV3Receipt(t, scope, nil, false, false)
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := NewStore(st.dir)
			view, err := local.PrepareCharacterArbitrationV3(prefix.Session(), nil, arbitrationV3Protocol)
			if err == nil {
				err = view.SaveArbitration(r)
			}
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		verifiedStoreMust(t, err)
	}
	cycle, err := v.FinalizeCycle(nil)
	verifiedStoreMust(t, err)
	errors = make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewStore(st.dir).AppendCharacterActivationCycle(prefix.Session().Digest, cycle)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		verifiedStoreMust(t, err)
	}
	loaded, err := st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, 1)
	verifiedStoreMust(t, err)
	if len(loaded.Steps()) != 1 {
		t.Fatal("CAS duplicated a cycle")
	}
	if err := v.SaveArbitration(r); err == nil {
		t.Fatal("stale view wrote past committed cycle")
	}
	forged := verifiedStoreCopy(cycle)
	forged.Evidence.Arbitrations[0].Resolutions[0].StateAfter = "changed"
	if _, err := st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, forged); err == nil {
		t.Fatal("same root accepted changed payload")
	}
	if !v.BelongsTo(NewStore(st.dir)) || v.BelongsTo(NewStore(t.TempDir())) {
		t.Fatal("view store identity check failed")
	}
	copied := NewStore(t.TempDir())
	verifiedStoreMust(t, copied.Init())
	verifiedStoreMust(t, copied.SaveCharacterReadinessContext(context))
	verifiedStoreMust(t, copied.CreateCharacterActivationSession(prefix.Session()))
	copiedView := arbitrationV3Prepare(t, copied, prefix, input)
	if copiedView.Input().Digest != v.Input().Digest || copiedView.BelongsTo(st) {
		t.Fatal("identical copied source hashes authorized a different workspace")
	}
	var forgedView CharacterArbitrationV3
	raw, _ := json.Marshal(v)
	verifiedStoreMust(t, json.Unmarshal(raw, &forgedView))
	if forgedView.BelongsTo(st) {
		t.Fatal("JSON minted store authority")
	}
}

func TestArbitrationV3StoreRejectsUnauthorizedRevisionAndProvisionalEffectsWithoutWrites(t *testing.T) {
	st, _, prefix, input := arbitrationV3Setup(t)
	v := arbitrationV3Prepare(t, st, prefix, input)
	scope, err := v.Sources(1)
	verifiedStoreMust(t, err)
	owner := scope.EffectiveProposals()[0].AgentID
	other := scope.EffectiveProposals()[1].AgentID
	r1 := arbitrationV3Receipt(t, scope, []string{owner}, false, false)
	bad := verifiedStoreCopy(r1)
	bad.StoryTime.EndDay += 1.0 / 1440
	before, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if err := v.SaveArbitration(bad); err == nil {
		t.Fatal("provisional clock advance accepted")
	}
	bad = verifiedStoreCopy(r1)
	bad.Resolutions[0].SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: verifiedStoreNumber(0), EndDay: verifiedStoreNumber(1.0 / 1440)}}
	if err := v.SaveArbitration(bad); err == nil {
		t.Fatal("provisional self execution accepted")
	}
	bad = verifiedStoreCopy(r1)
	bad.Resolutions[0].PostState.Location = "未经执行的目的地"
	if err := v.SaveArbitration(bad); err == nil {
		t.Fatal("provisional post state advance accepted")
	}
	after, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("invalid provisional receipts wrote files")
	}
	verifiedStoreMust(t, v.SaveArbitration(r1))
	before, err = DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	foreignObs, _ := arbitrationV3Revisions(t, scope, r1, []string{other})
	if err := v.SaveObservation(foreignObs[0]); err == nil {
		t.Fatal("unaffected owner was permitted a revision")
	}
	obs, ps := arbitrationV3Revisions(t, scope, r1, []string{owner})
	forged := verifiedStoreCopy(obs[0])
	forged.KnownFacts[0].Text = "从别人的未执行意图补知识"
	forged, err = domain.FinalizeCharacterObservationPacket(forged)
	verifiedStoreMust(t, err)
	if err := v.SaveObservation(forged); err == nil {
		t.Fatal("resigned changed frozen knowledge accepted")
	}
	if err := v.SaveProposal(ps[0]); err == nil {
		t.Fatal("P2 without durable O2 accepted")
	}
	after, err = DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("unauthorized revision wrote files")
	}
	verifiedStoreMust(t, v.SaveObservation(obs[0]))
	verifiedStoreMust(t, v.SaveProposal(ps[0]))
	before, err = DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	changed := ps[0]
	changed.DecisionReason = "另一独立选择"
	changed, err = domain.FinalizeCharacterDecisionProposal(changed, obs[0])
	verifiedStoreMust(t, err)
	if err := v.SaveProposal(changed); err == nil {
		t.Fatal("immutable paid P2 overwritten")
	}
	after, err = DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("P2 conflict changed files")
	}
}

func TestArbitrationV3StoreAuthenticatesEveryProofOnAppendLoadAndOrphanRecovery(t *testing.T) {
	mutations := []string{"missing_admission", "changed_frozen_registry", "missing_observation", "changed_proposal", "changed_receipt", "foreign_owner", "extra_round"}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			st, _, prefix, input := arbitrationV3Setup(t)
			v := arbitrationV3Prepare(t, st, prefix, input)
			scope, err := v.Sources(1)
			verifiedStoreMust(t, err)
			r := arbitrationV3Receipt(t, scope, nil, false, false)
			verifiedStoreMust(t, v.SaveArbitration(r))
			cycle, err := v.FinalizeCycle(nil)
			verifiedStoreMust(t, err)
			gen, ch := cycle.GenerationID, cycle.Chapter
			path := func(legacy string) string {
				relative, err := v.proofs.proofPath(legacy)
				verifiedStoreMust(t, err)
				return v.proofs.io.path(relative)
			}
			write := func(target string, value any) {
				raw, err := json.Marshal(value)
				verifiedStoreMust(t, err)
				verifiedStoreMust(t, os.WriteFile(target, raw, 0600))
			}
			switch mutation {
			case "missing_admission":
				verifiedStoreMust(t, os.Remove(path(v.admissionPath())))
			case "changed_frozen_registry":
				forged := input.Registry
				forged.Entries = verifiedStoreCopy(forged.Entries)
				forged.Entries[0].Character = "伪造身份"
				write(path(characterAgentRegistrySnapshotPath(gen, ch)), forged)
			case "missing_observation":
				verifiedStoreMust(t, os.Remove(path(characterAgentObservationPath(gen, ch, 1, scope.EffectiveProposals()[0].AgentID))))
			case "changed_proposal":
				p := scope.EffectiveProposals()[0]
				p.DecisionReason = "不同选择"
				for _, o := range input.Observations {
					if o.AgentID == p.AgentID {
						p, err = domain.FinalizeCharacterDecisionProposal(p, o)
						verifiedStoreMust(t, err)
					}
				}
				write(path(characterAgentProposalPath(gen, ch, 1, p.AgentID)), p)
			case "changed_receipt":
				forged := verifiedStoreCopy(r)
				forged.Resolutions[0].StateAfter = "changed"
				write(path(characterAgentArbitrationPath(gen, ch, 1)), forged)
			case "foreign_owner":
				write(path(characterAgentProposalPath(gen, ch, 1, "ca_foreign")), scope.EffectiveProposals()[0])
			case "extra_round":
				write(path(characterAgentArbitrationPath(gen, ch, 3)), r)
			}
			before, err := DirectoryContentRoot(st.dir)
			verifiedStoreMust(t, err)
			if _, err := st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle); err == nil {
				t.Fatal("tampered actual proofs accepted")
			}
			loaded, err := st.LoadCharacterArbitrationV3(gen, ch)
			if err == nil && loaded != nil {
				t.Fatal("reload trusted altered source")
			}
			if mutation == "missing_admission" {
				if _, err := st.PrepareCharacterArbitrationV3(prefix.Session(), nil, arbitrationV3Protocol); err == nil {
					t.Fatal("lost admission was recreated after paid proof")
				}
			}
			after, err := DirectoryContentRoot(st.dir)
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("failed source validation wrote data")
			}
			root, err := characterActivationSessionDir(gen, ch)
			verifiedStoreMust(t, err)
			verifiedStoreMust(t, st.writeCharacterActivationJSON(activationCyclePath(root, 1), cycle, true))
			before, err = DirectoryContentRoot(st.dir)
			verifiedStoreMust(t, err)
			if _, err := NewStore(st.dir).RecoverCharacterActivationSession(gen, ch); err == nil {
				t.Fatal("orphan recovery trusted altered source")
			}
			after, err = DirectoryContentRoot(st.dir)
			verifiedStoreMust(t, err)
			if before != after {
				t.Fatal("failed orphan recovery changed cursor")
			}
		})
	}
}
