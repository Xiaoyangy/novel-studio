package domain_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func roundSourcePrefix(t *testing.T, p2 bool) (domain.VerifiedCharacterActivationPrefix, domain.CharacterActivationInputSet, domain.CharacterActivationCycle) {
	t.Helper()
	origin, _, _, _ := workBatchFixture(t, false)
	origin, input := prefixInputForOrigin(t, origin)
	if p2 {
		e := continuationCopy(origin.Evidence)
		owner := e.Proposals[0].AgentID
		r1 := continuationCopy(e.Arbitrations[0])
		r1.Finalized = false
		r1.StoryTime.EndDay = r1.StoryTime.StartDay
		r1.Conflicts = []domain.WorldArbitrationConflict{{ID: "format-1", Kind: "time", AffectedAgentIDs: []string{owner}, Feedback: "明确本次检查与后续授权的边界"}}
		for i := range r1.Resolutions {
			r1.Resolutions[i].SelfExecutions = nil
			for _, a := range input.Stimulus.PhysicalState.Actors {
				if a.AgentID == r1.Resolutions[i].AgentID {
					copy := continuationCopy(a)
					r1.Resolutions[i].PostState = &copy
				}
			}
		}
		var err error
		r1, err = domain.FinalizeWorldArbitrationReceipt(r1, e.Stimulus, e.Activation, e.Proposals, 1)
		continuationMust(t, err)
		var o2 domain.CharacterObservationPacket
		for _, o := range e.Observations {
			if o.AgentID == owner {
				o2 = continuationCopy(o)
			}
		}
		o2.Round = 2
		o2.ConflictFeedback = []string{"time：明确本次检查与后续授权的边界"}
		o2, err = domain.FinalizeCharacterObservationPacket(o2)
		continuationMust(t, err)
		p2 := continuationCopy(e.Proposals[0])
		p2.Round, p2.ObservationDigest = 2, o2.Digest
		p2.DecisionReason = "先完成本段，并明确授权余下有限检查"
		p2, err = domain.FinalizeCharacterDecisionProposal(p2, o2)
		continuationMust(t, err)
		r2 := continuationCopy(e.Arbitrations[0])
		r2.Round = 2
		r2.ProposalDigests = nil
		latest := append([]domain.CharacterDecisionProposal(nil), e.Proposals...)
		latest[0] = p2
		for i, p := range latest {
			r2.ProposalDigests = append(r2.ProposalDigests, p.Digest)
			r2.Resolutions[i].ProposalDigest = p.Digest
			for _, a := range input.Stimulus.PhysicalState.Actors {
				if a.AgentID == p.AgentID {
					copy := continuationCopy(a)
					r2.Resolutions[i].PostState = &copy
				}
			}
		}
		r2, err = domain.FinalizeWorldArbitrationReceipt(r2, e.Stimulus, e.Activation, latest, 1)
		continuationMust(t, err)
		e.Observations = append(e.Observations, o2)
		e.Proposals = append(e.Proposals, p2)
		e.Arbitrations = []domain.WorldArbitrationReceipt{r1, r2}
		e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
		continuationMust(t, err)
		origin.Evidence = e
		origin, err = domain.FinalizeCharacterActivationCycle(origin)
		continuationMust(t, err)
	}
	prefix := verifiedPrefixForCycle(t, origin)
	_, prefix, err := domain.VerifyCharacterActivationStep(prefix, input, origin)
	continuationMust(t, err)
	prefix, err = domain.ApplyVerifiedCharacterActivationReadiness(prefix, testutil.CycleReadiness(t, origin, "continue"))
	continuationMust(t, err)
	return prefix, roundSourceNextInput(t, prefix), origin
}

func roundSourceNextInput(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix) domain.CharacterActivationInputSet {
	t.Helper()
	steps := prefix.Steps()
	last := steps[len(steps)-1]
	session := prefix.Session()
	input := last.Input()
	state := last.AfterState()
	input.Stimulus.PhysicalState = &state
	input.Stimulus.StoryClock.CurrentDay = session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	continuationMust(t, err)
	input.Stimulus.StoryClock = &clock
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(steps)+1, session.ChapterContextDigest, last.GlobalRoot())
	continuationMust(t, err)
	input.Stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.StimulusDigest = input.Stimulus.Digest
		o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: len(steps) + 1, ChapterContextDigest: session.ChapterContextDigest, PreviousCycleDigest: last.GlobalRoot(), CurrentDay: session.CurrentDay}
		for _, p := range last.EffectiveProposals() {
			if p.AgentID == o.AgentID {
				o.CurrentGoal, o.Pressure = p.CurrentGoal, p.Pressure
			}
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(state, o.AgentID)
		continuationMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(state, o.AgentID)
		continuationMust(t, err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		continuationMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID {
				input.Activation.Entries[j].State = domain.CharacterAgentActive
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func roundSourceScope(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix, input domain.CharacterActivationInputSet) domain.VerifiedCharacterArbitrationSourcesV1 {
	t.Helper()
	var admissions []domain.CharacterWorkContinuationReceiptV1
	for _, o := range input.Observations {
		if o.Character == "丙" {
			continue
		}
		eligible, err := domain.EvaluateCharacterWorkContinuationForRoundV1(prefix, input, o.AgentID)
		continuationMust(t, err)
		if !eligible.Eligible {
			t.Fatalf("source unexpectedly woke: %+v", eligible)
		}
		admissions = append(admissions, *eligible.Receipt)
	}
	scope, err := domain.ResolveCharacterArbitrationRoundV1(prefix, input, admissions, []domain.CharacterDecisionProposal{workBatchFreshProposal(t, input)})
	continuationMust(t, err)
	return scope
}

func roundSourceReceipt(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, minutes float64) domain.WorldArbitrationReceipt {
	t.Helper()
	input := scope.Input()
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+minutes/1440
	r := domain.WorldArbitrationReceipt{Version: domain.WorldArbitrationReceiptV2Version, GenerationID: input.Stimulus.GenerationID, Chapter: input.Stimulus.Chapter, Round: scope.CurrentRound(), StoryTime: &domain.StoryTimeChapterSchedule{Chapter: input.Stimulus.Chapter, StartDay: start, EndDay: end}, StimulusDigest: input.Stimulus.Digest, ActivationDigest: input.Activation.Digest, Finalized: true, HardContractStatus: "feasible"}
	for i, p := range scope.EffectiveProposals() {
		var post domain.CharacterPhysicalStateV2
		for _, a := range input.Stimulus.PhysicalState.Actors {
			if a.AgentID == p.AgentID {
				post = continuationCopy(a)
			}
		}
		status := "in_progress"
		if p.Character == "丙" {
			status = "completed"
		}
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		r.Resolutions = append(r.Resolutions, domain.CharacterDecisionResolution{AgentID: p.AgentID, Character: p.Character, ProposalDigest: p.Digest, Decision: p.Decision, IntendedAction: p.IntendedAction, ActionOrder: i + 1, Outcome: "success", CompletionState: status, ImmediateResult: "本段已执行", StateAfter: "保留实际进度", PostState: &post, SelfExecutions: []domain.CharacterSelfExecutionV2{{TaskID: p.SelfTasks[0].TaskID, Status: status, StartDay: &start, EndDay: &end}}, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "本段结束", TransmissionPath: "实际行动", ArrivalChapter: input.Stimulus.Chapter, ProtagonistImpact: "下一次选择有据"}}})
	}
	return r
}

func roundSourceRejected(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, affected []string) domain.VerifiedCharacterArbitrationRoundV1 {
	t.Helper()
	r := roundSourceReceipt(t, scope, 0)
	r.Finalized = false
	r.Conflicts = []domain.WorldArbitrationConflict{{ID: "current-conflict", Kind: "time", AffectedAgentIDs: affected, Feedback: "明确本次实际执行顺序，尚未发生本段动作"}}
	for i := range r.Resolutions {
		r.Resolutions[i].SelfExecutions = nil
	}
	result, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	continuationMust(t, err)
	return result
}

func roundSourceRevision(t *testing.T, previous domain.VerifiedCharacterArbitrationRoundV1, affected []string) ([]domain.CharacterObservationPacket, []domain.CharacterDecisionProposal) {
	t.Helper()
	var observations []domain.CharacterObservationPacket
	var proposals []domain.CharacterDecisionProposal
	for _, id := range affected {
		var o domain.CharacterObservationPacket
		var p domain.CharacterDecisionProposal
		for _, base := range previous.Sources().Input().Observations {
			if base.AgentID == id {
				o = base
			}
		}
		for _, base := range previous.Sources().EffectiveProposals() {
			if base.AgentID == id {
				p = base
			}
		}
		o.Round = 2
		o.ConflictFeedback = []string{"time：明确本次实际执行顺序，尚未发生本段动作"}
		o.GeneratedAt = "2026-09-07T01:00:00Z"
		var err error
		o, err = domain.FinalizeCharacterObservationPacket(o)
		continuationMust(t, err)
		p.Round, p.ObservationDigest = 2, o.Digest
		p.DecisionReason = "根据本人的冲突反馈重新选择明确执行顺序"
		p, err = domain.FinalizeCharacterDecisionProposal(p, o)
		continuationMust(t, err)
		observations = append(observations, o)
		proposals = append(proposals, p)
	}
	return observations, proposals
}

func TestArbitrationRoundSourcesContinueActualP2AtNextCycleR1(t *testing.T) {
	prefix, input, origin := roundSourcePrefix(t, true)
	continuationMust(t, domain.ValidateCharacterActivationCycle(origin))
	if len(origin.Evidence.Arbitrations) != 2 || origin.Evidence.Arbitrations[0].Finalized || origin.Evidence.Arbitrations[1].Round != 2 {
		t.Fatal("P2 origin is not a real complete R1/R2 source cycle")
	}
	raw, _ := json.Marshal(origin)
	scope := roundSourceScope(t, prefix, input)
	found := false
	for _, source := range scope.Sources() {
		if source.Kind == "continuation" && source.Origin.Round == 2 {
			found = true
			if source.Origin.Cycle != 1 || scope.Coordinate().Cycle != 2 || scope.CurrentRound() != 1 {
				t.Fatal("origin and current round coordinates conflated")
			}
			ledger, _ := prefix.ContinuationLedger(source.AgentID)
			legacy, err := domain.EvaluateCharacterWorkContinuationV1(ledger, input, prefix.ContinuationBoundaries()...)
			continuationMust(t, err)
			if legacy.Eligible {
				t.Fatal("old entry point silently acquired new P2 support")
			}
		}
	}
	if !found {
		t.Fatal("fixture lost its actual P2 grant")
	}
	r := roundSourceReceipt(t, scope, 1)
	if _, err := domain.FinalizeWorldArbitrationReceipt(continuationCopy(r), input.Stimulus, input.Activation, scope.EffectiveProposals(), 1); err == nil {
		t.Fatal("legacy same-cycle finalizer accepted old P2 as current R1")
	}
	result, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	continuationMust(t, err)
	if result.Receipt().Round != 1 || result.AfterState().Actors[0].AgentID == "" {
		t.Fatal("new source-aware finalizer changed current round or lost physical state")
	}
	after, _ := json.Marshal(prefix.Steps()[0].Cycle())
	if string(raw) != string(after) {
		t.Fatal("origin proposal/observation/cycle was re-signed")
	}
	for _, p := range scope.EffectiveProposals() {
		if p.Round == 2 {
			var original domain.CharacterDecisionProposal
			for _, candidate := range origin.Evidence.Proposals {
				if candidate.Digest == p.Digest {
					original = candidate
				}
			}
			got, _ := json.Marshal(p)
			want, _ := json.Marshal(original)
			if string(got) != string(want) {
				t.Fatal("P2 was relabeled or copied into a fake current proposal")
			}
		}
	}
}

func TestArbitrationRoundOldSingleRoundReceiptsAndEligibilityRemainByteIdentical(t *testing.T) {
	prefix, input, _ := roundSourcePrefix(t, false)
	scope := roundSourceScope(t, prefix, input)
	if scope.SessionDigest() != prefix.Session().Digest {
		t.Fatal("source capability lost its exact prefix cursor")
	}
	for _, admission := range scope.Continuations() {
		ledger, ok := prefix.ContinuationLedger(admission.AgentID)
		if !ok {
			t.Fatal("missing original ledger")
		}
		legacy, err := domain.EvaluateCharacterWorkContinuationV1(ledger, input, prefix.ContinuationBoundaries()...)
		continuationMust(t, err)
		newJSON, _ := json.Marshal(admission)
		oldJSON, _ := json.Marshal(legacy.Receipt)
		if !legacy.Eligible || string(newJSON) != string(oldJSON) {
			t.Fatal("R1 source-aware eligibility changed legacy grant bytes")
		}
	}
	r := roundSourceReceipt(t, scope, 1)
	old, err := domain.FinalizeWorldArbitrationReceipt(continuationCopy(r), input.Stimulus, input.Activation, scope.EffectiveProposals(), 1)
	continuationMust(t, err)
	next, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	continuationMust(t, err)
	before, _ := json.Marshal(old)
	after, _ := json.Marshal(next.Receipt())
	if string(before) != string(after) {
		t.Fatal("shared kernel changed an existing single-round receipt/digest")
	}
}

func TestArbitrationRoundRevisionChangesOnlyAffectedOwnerAndSettlesOnce(t *testing.T) {
	for _, mode := range []string{"fresh", "one-continuation", "all-continuations"} {
		t.Run(mode, func(t *testing.T) {
			prefix, input, _ := roundSourcePrefix(t, true)
			scope := roundSourceScope(t, prefix, input)
			var affected []string
			for _, source := range scope.Sources() {
				if (mode == "fresh" && source.Kind == "fresh") || (mode != "fresh" && source.Kind == "continuation" && (mode == "all-continuations" || len(affected) == 0)) {
					affected = append(affected, source.AgentID)
				}
			}
			previous := roundSourceRejected(t, scope, affected)
			if !reflect.DeepEqual(previous.AfterState(), *input.Stimulus.PhysicalState) {
				t.Fatal("R1 rejection advanced world state")
			}
			observations, proposals := roundSourceRevision(t, previous, affected)
			revision, err := domain.ResolveCharacterArbitrationRevisionV1(previous, observations, proposals)
			continuationMust(t, err)
			if revision.CurrentRound() != 2 || len(revision.AdmissionContinuations()) != 2 || len(revision.PreviousArbitrations()) != 1 || revision.Input().Digest != input.Digest {
				t.Fatal("revision discarded original frozen admission/input/rejection")
			}
			for _, old := range scope.Sources() {
				changed := false
				for _, id := range affected {
					changed = changed || id == old.AgentID
				}
				if changed {
					continue
				}
				for _, now := range revision.Sources() {
					if now.AgentID == old.AgentID && now != old {
						t.Fatal("unaffected source was replaced")
					}
				}
			}
			r := roundSourceReceipt(t, revision, 1)
			before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
			r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: continuationNumber(-1), After: continuationNumber(before - 1), EvidenceRefs: []string{revision.EffectiveProposals()[0].Digest}}}
			result, err := domain.FinalizeCharacterArbitrationRoundV1(revision, r)
			continuationMust(t, err)
			if *result.AfterState().Resources[0].ActualAmount != before-1 {
				t.Fatal("shared resource was settled per owner")
			}
			if mode == "all-continuations" && len(revision.Continuations()) != 0 {
				t.Fatal("revised owners retained stale execution authority")
			}
			if _, err := domain.ResolveCharacterArbitrationRevisionV1(result, observations, proposals); err == nil {
				t.Fatal("final R2 permitted a third decision round")
			}
		})
	}
}

func TestArbitrationRoundNonfinalAndHardCannotCommitSideEffects(t *testing.T) {
	prefix, input, _ := roundSourcePrefix(t, true)
	scope := roundSourceScope(t, prefix, input)
	for _, hard := range []bool{false, true} {
		for _, mutation := range []string{"none", "time", "settlement", "delivery", "knowledge", "self-execution", "location"} {
			t.Run(fmt.Sprintf("hard=%v/%s", hard, mutation), func(t *testing.T) {
				r := roundSourceReceipt(t, scope, 0)
				r.Finalized = false
				if hard {
					r.HardContractStatus = "infeasible"
					r.HardContractConflicts = []string{"明确硬合同已不能同时满足"}
				} else {
					r.Conflicts = []domain.WorldArbitrationConflict{{ID: "c", Kind: "rule", AffectedAgentIDs: []string{r.Resolutions[0].AgentID}, Feedback: "约束尚待修订"}}
				}
				for i := range r.Resolutions {
					r.Resolutions[i].SelfExecutions = nil
				}
				switch mutation {
				case "time":
					r.StoryTime.EndDay += 1.0 / 1440
				case "settlement":
					r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID}}
				case "delivery":
					r.ResourceDeliveries = []domain.ResourceDeliveryV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID}}
				case "knowledge":
					r.Resolutions[0].PostState.ReceivedFacts = append(r.Resolutions[0].PostState.ReceivedFacts, domain.CharacterReceivedFactV2{Text: "invented knowledge"})
				case "self-execution":
					r.Resolutions[0].SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "not_started"}}
				case "location":
					r.Resolutions[0].PostState.Location = "another place"
				}
				result, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
				if mutation != "none" {
					if err == nil {
						t.Fatal("nonfinal receipt acquired real side effects")
					}
					return
				}
				continuationMust(t, err)
				if !reflect.DeepEqual(result.AfterState(), *input.Stimulus.PhysicalState) {
					t.Fatal("diagnostic advanced state")
				}
				if hard {
					if _, err := domain.ResolveCharacterArbitrationRevisionV1(result, nil, nil); err == nil {
						t.Fatal("hard infeasible permitted owner redecisions")
					}
				}
			})
		}
	}
}

func TestArbitrationRoundRejectsOtherwiseValidProvisionalResourceEffect(t *testing.T) {
	prefix, input, _ := roundSourcePrefix(t, false)
	scope := roundSourceScope(t, prefix, input)
	r := roundSourceReceipt(t, scope, 0)
	r.Finalized = false
	r.Conflicts = []domain.WorldArbitrationConflict{{ID: "pending", Kind: "time", AffectedAgentIDs: []string{r.Resolutions[0].AgentID}, Feedback: "本周期尚待修订"}}
	for i := range r.Resolutions {
		r.Resolutions[i].SelfExecutions = nil
	}
	before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
	r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: continuationNumber(-1), After: continuationNumber(before - 1), EvidenceRefs: []string{scope.EffectiveProposals()[0].Digest}}}
	// This passes the historical mechanical receipt contract; rejection below
	// specifically proves the NEW no-side-effects round boundary, not bad JSON.
	_, err := domain.FinalizeWorldArbitrationReceipt(continuationCopy(r), input.Stimulus, input.Activation, scope.EffectiveProposals(), 1)
	continuationMust(t, err)
	if _, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r); err == nil || !strings.Contains(err.Error(), "nonfinal/hard arbitration cannot advance") {
		t.Fatalf("provisional debit was not blocked at new round boundary: %v", err)
	}
}

func TestArbitrationRoundRejectsForgedCoordinatesSourcesAndRevisionKnowledge(t *testing.T) {
	prefix, input, _ := roundSourcePrefix(t, true)
	scope := roundSourceScope(t, prefix, input)
	var zero domain.VerifiedCharacterArbitrationSourcesV1
	_ = json.Unmarshal([]byte(`{"verified":true,"coordinate":{"round":1}}`), &zero)
	if _, err := domain.FinalizeCharacterArbitrationRoundV1(zero, roundSourceReceipt(t, scope, 1)); err == nil {
		t.Fatal("JSON minted source authority")
	}
	var prior domain.VerifiedCharacterArbitrationRoundV1
	_ = json.Unmarshal([]byte(`{"verified":true}`), &prior)
	if _, err := domain.ResolveCharacterArbitrationRevisionV1(prior, nil, nil); err == nil {
		t.Fatal("JSON minted revision authority")
	}
	for _, mode := range []string{"missing", "duplicate", "wrong-grant", "future-fresh", "foreign-input"} {
		t.Run(mode, func(t *testing.T) {
			admissions, fresh, current := scope.AdmissionContinuations(), scope.SubmittedProposals(), continuationCopy(input)
			switch mode {
			case "missing":
				fresh = nil
			case "duplicate":
				admissions = append(admissions, admissions[0])
			case "wrong-grant":
				admissions[0].AuthorizedRemaining += 20
				admissions[0].Digest, _ = domain.ComputeCharacterWorkContinuationReceiptV1Digest(admissions[0])
			case "future-fresh":
				fresh[0].Round = 2
			case "foreign-input":
				current.Stimulus.GenerationID = "pg2_foreign"
			}
			if _, err := domain.ResolveCharacterArbitrationRoundV1(prefix, current, admissions, fresh); err == nil {
				t.Fatal("forged source set accepted")
			}
		})
	}
	affected := []string{scope.Sources()[0].AgentID}
	rejected := roundSourceRejected(t, scope, affected)
	for _, mode := range []string{"extra-owner", "knowledge", "feedback", "location", "source-digest"} {
		t.Run(mode, func(t *testing.T) {
			observations, proposals := roundSourceRevision(t, rejected, affected)
			switch mode {
			case "extra-owner":
				observations = append(observations, observations[0])
				proposals = append(proposals, proposals[0])
			case "knowledge":
				observations[0].KnownFacts = append(observations[0].KnownFacts, domain.CharacterAgentFact{ID: "injected", Kind: "known", Text: "foreign secret"})
			case "feedback":
				observations[0].ConflictFeedback = []string{"not the actual rejection"}
			case "location":
				observations[0].Location = "another place"
			case "source-digest":
				observations[0].StimulusDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if mode != "extra-owner" {
				var err error
				observations[0], err = domain.FinalizeCharacterObservationPacket(observations[0])
				if err == nil {
					proposals[0].ObservationDigest = observations[0].Digest
					proposals[0], _ = domain.FinalizeCharacterDecisionProposal(proposals[0], observations[0])
				}
			}
			if _, err := domain.ResolveCharacterArbitrationRevisionV1(rejected, observations, proposals); err == nil {
				t.Fatal("revision re-signed a changed knowledge/source baseline")
			}
		})
	}
	r := roundSourceReceipt(t, scope, 10)
	if _, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r); err == nil {
		t.Fatal("continued work exceeded its finite allowance")
	}
}

func TestArbitrationRoundGettersAreDetachedAndConcurrent(t *testing.T) {
	prefix, input, _ := roundSourcePrefix(t, true)
	scope := roundSourceScope(t, prefix, input)
	r := roundSourceReceipt(t, scope, 1)
	result, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
	continuationMust(t, err)
	want, _ := json.Marshal(result.Receipt())
	original, _ := json.Marshal(prefix.Steps()[0].Cycle())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := scope.EffectiveProposals()
			p[0].KnowledgeRefs[0] = "changed"
			o := scope.Observations()
			o[0].KnownFacts[0].Text = "changed"
			in := scope.Input()
			in.Stimulus.PhysicalState.Actors[0].Location = "changed"
			after := result.AfterState()
			after.Actors[0].Location = "changed"
			admissions := scope.AdmissionContinuations()
			admissions[0].AuthorizedRemaining = 999
			copy := result.Sources()
			parts := copy.PreviousArbitrations()
			_ = parts
			got, err := domain.FinalizeCharacterArbitrationRoundV1(scope, r)
			if err == nil {
				raw, _ := json.Marshal(got.Receipt())
				if string(raw) != string(want) {
					err = fmt.Errorf("concurrent finalization changed result")
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		continuationMust(t, err)
	}
	after, _ := json.Marshal(prefix.Steps()[0].Cycle())
	if string(original) != string(after) {
		t.Fatal("getter/finalizer mutated original verified history")
	}
}
