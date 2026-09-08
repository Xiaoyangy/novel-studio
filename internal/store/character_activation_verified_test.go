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

func verifiedStoreMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func verifiedStoreNumber(n float64) *float64 { return &n }
func verifiedStoreCopy[T any](v T) T {
	raw, _ := json.Marshal(v)
	var copy T
	_ = json.Unmarshal(raw, &copy)
	return copy
}

// Complete paid-review fixtures are built through the production domain APIs,
// not through a persisted verified badge or a fake global-root callback.
func verifiedStoreOrigin(t *testing.T) (domain.CharacterReadinessContext, domain.CharacterActivationInputSet, domain.CharacterActivationCycle) {
	t.Helper()
	context, _, _, _ := testutil.CharacterReadiness(t, false)
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0, context.Digest)
	e := cycle.Evidence
	physical, err := domain.PrepareCharacterSelfExperienceStateV2(*e.Stimulus.PhysicalState)
	verifiedStoreMust(t, err)
	physical.Actors[0].Resources[0].Access = "shared"
	for _, name := range []string{"乙", "丙"} {
		var record domain.CharacterAgentRecord
		e.Registry, record, err = e.Registry.UpsertCharacter(name, nil, "important", 1, "")
		verifiedStoreMust(t, err)
		actor := verifiedStoreCopy(physical.Actors[0])
		actor.AgentID, actor.Character = record.AgentID, name
		if name == "丙" {
			actor.Resources = []domain.CharacterResourceHoldingV2{}
		}
		physical.Actors = append(physical.Actors, actor)
	}
	e.Registry, err = domain.FinalizeCharacterAgentRegistry(e.Registry)
	verifiedStoreMust(t, err)
	physical, err = domain.FinalizeWorldPhysicalStateV2(physical)
	verifiedStoreMust(t, err)
	e.Stimulus.PhysicalState = &physical
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1)
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	verifiedStoreMust(t, err)
	baseObservation, baseProposal := e.Observations[0], e.Proposals[0]
	e.Observations, e.Proposals, e.MemoryRoots = nil, nil, nil
	e.Activation.RegistryRoot, e.Activation.Entries = e.Registry.RegistryRoot, nil
	input := domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus}
	for _, actor := range physical.Actors {
		o := verifiedStoreCopy(baseObservation)
		o.AgentID, o.Character, o.StimulusDigest = actor.AgentID, actor.Character, e.Stimulus.Digest
		record, _ := e.Registry.Resolve(actor.Character)
		o.Tier = record.Tier
		o.Sources = []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
		o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: 1, ChapterContextDigest: context.Digest}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, actor.AgentID)
		verifiedStoreMust(t, err)
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: actor.AgentID, Character: actor.Character, GenerationID: e.GenerationID, State: "projected"})
		verifiedStoreMust(t, err)
		o.MemoryRoot = memory.MemoryRoot
		o, err = domain.FinalizeCharacterObservationPacket(o)
		verifiedStoreMust(t, err)
		input.Observations, input.Memories = append(input.Observations, o), append(input.Memories, memory)
		entry := domain.CharacterAgentActivationEntry{AgentID: actor.AgentID, Character: actor.Character, Tier: o.Tier, State: domain.CharacterAgentActive, Reasons: []string{"initial_work"}, ObservationDigest: o.Digest}
		if actor.Character == "丙" {
			entry.State, entry.ObservationDigest = domain.CharacterAgentSleeping, ""
		} else {
			p := verifiedStoreCopy(baseProposal)
			p.AgentID, p.Character, p.ObservationDigest = actor.AgentID, actor.Character, o.Digest
			p.SelfTasks = []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "常规检查", ProgressTarget: verifiedStoreNumber(5), ProgressUnit: "minute", KnowledgeRefs: p.KnowledgeRefs}}
			p.WorkContinuations = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: true}}
			p, err = domain.FinalizeCharacterDecisionProposal(p, o)
			verifiedStoreMust(t, err)
			e.Proposals = append(e.Proposals, p)
			e.MemoryRoots = append(e.MemoryRoots, o.MemoryRoot)
		}
		e.Activation.Entries = append(e.Activation.Entries, entry)
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	verifiedStoreMust(t, err)
	input.Activation = e.Activation
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	verifiedStoreMust(t, err)
	e.Observations = input.Observations
	r := verifiedStoreArbitration(t, e.Arbitrations[0], input, e.Proposals, false)
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	verifiedStoreMust(t, err)
	cycle.Evidence, cycle.InputSetDigest = e, input.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	verifiedStoreMust(t, err)
	return context, input, cycle
}

func verifiedStoreArbitration(t *testing.T, template domain.WorldArbitrationReceipt, input domain.CharacterActivationInputSet, proposals []domain.CharacterDecisionProposal, consume bool) domain.WorldArbitrationReceipt {
	t.Helper()
	r := verifiedStoreCopy(template)
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	r.ProposalDigests, r.Resolutions, r.ResourceSettlements = nil, nil, nil
	r.ProtagonistProjection = domain.ProtagonistDecisionProjection{}
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}
	for i, p := range proposals {
		var post domain.CharacterPhysicalStateV2
		for _, actor := range input.Stimulus.PhysicalState.Actors {
			if actor.AgentID == p.AgentID {
				post = verifiedStoreCopy(actor)
			}
		}
		status := "in_progress"
		if p.Character == "丙" {
			status = "completed"
		}
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		r.Resolutions = append(r.Resolutions, domain.CharacterDecisionResolution{AgentID: p.AgentID, Character: p.Character, ProposalDigest: p.Digest, Decision: p.Decision, IntendedAction: p.IntendedAction, ActionOrder: i + 1, Outcome: "success", CompletionState: status, ImmediateResult: "本人本段已执行", StateAfter: "按本段累计", PostState: &post, SelfExecutions: []domain.CharacterSelfExecutionV2{{TaskID: p.SelfTasks[0].TaskID, Status: status, StartDay: &start, EndDay: &end}}, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "时间推进", TransmissionPath: "实际工作", ArrivalChapter: 1, ProtagonistImpact: "尚有余下工作"}}})
	}
	if consume {
		before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
		r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: verifiedStoreNumber(-1), After: verifiedStoreNumber(before - 1), EvidenceRefs: []string{proposals[0].Digest, proposals[1].Digest}}}
	}
	r, err := domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, proposals, 1)
	verifiedStoreMust(t, err)
	return r
}

func verifiedStoreNext(t *testing.T, prefix domain.VerifiedCharacterActivationPrefix) (domain.CharacterActivationInputSet, domain.CharacterActivationCycle) {
	t.Helper()
	steps, session := prefix.Steps(), prefix.Session()
	origin, last := steps[0].Cycle(), steps[len(steps)-1]
	input := last.Input()
	physical := last.AfterState()
	input.Stimulus.PhysicalState = &physical
	input.Stimulus.StoryClock.CurrentDay = session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	verifiedStoreMust(t, err)
	input.Stimulus.StoryClock = &clock
	index := len(steps) + 1
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, index, session.ChapterContextDigest, last.GlobalRoot())
	verifiedStoreMust(t, err)
	input.Stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	verifiedStoreMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.StimulusDigest = input.Stimulus.Digest
		o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: index, ChapterContextDigest: session.ChapterContextDigest, PreviousCycleDigest: last.GlobalRoot(), CurrentDay: session.CurrentDay}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
		verifiedStoreMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, o.AgentID)
		verifiedStoreMust(t, err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		verifiedStoreMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID {
				input.Activation.Entries[j].State = domain.CharacterAgentActive
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	verifiedStoreMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	verifiedStoreMust(t, err)
	var fresh domain.CharacterDecisionProposal
	for _, o := range input.Observations {
		if o.Character == "丙" {
			fresh = domain.CharacterDecisionProposal{GenerationID: o.GenerationID, Chapter: o.Chapter, Round: 1, AgentID: o.AgentID, Character: o.Character, ObservationDigest: o.Digest, Location: o.Location, CurrentGoal: o.CurrentGoal, Pressure: o.Pressure, AvailableOptions: []string{"观察", "等待"}, Decision: "观察", DecisionReason: "现场核对", IntendedAction: "本人观察现场", ActionDuration: "一分钟", KnowledgeRefs: []string{o.KnownFacts[0].ID}, SelfTasks: []domain.CharacterSelfTaskV2{{TaskID: fmt.Sprintf("fresh_%d", index), Kind: "work", Action: "观察现场", ProgressTarget: verifiedStoreNumber(1), ProgressUnit: "minute", KnowledgeRefs: []string{o.KnownFacts[0].ID}}}}
			fresh, err = domain.FinalizeCharacterDecisionProposal(fresh, o)
			verifiedStoreMust(t, err)
		}
	}
	proposals := append(append([]domain.CharacterDecisionProposal(nil), origin.Evidence.Proposals...), fresh)
	r := verifiedStoreArbitration(t, origin.Evidence.Arbitrations[0], input, proposals, index == 2)
	cycle := domain.CharacterActivationCycle{Version: domain.CharacterActivationCycleV2Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Index: index, PreviousDigest: last.GlobalRoot(), ChapterContextDigest: session.ChapterContextDigest, InputSetDigest: input.Digest, Evidence: domain.CharacterAgentEvidenceBundle{Version: domain.CharacterContinuationEvidenceV2Version, GenerationID: session.GenerationID, Chapter: session.Chapter, Registry: input.Registry, Stimulus: input.Stimulus, Activation: input.Activation, Observations: input.Observations, Proposals: []domain.CharacterDecisionProposal{fresh}, Arbitrations: []domain.WorldArbitrationReceipt{r}, ProtocolDigest: origin.Evidence.ProtocolDigest}}
	for _, o := range input.Observations {
		cycle.Evidence.MemoryRoots = append(cycle.Evidence.MemoryRoots, o.MemoryRoot)
	}
	for _, p := range origin.Evidence.Proposals {
		ledger, ok := prefix.ContinuationLedger(p.AgentID)
		if !ok {
			t.Fatal("lost source ledger")
		}
		eligible, err := domain.EvaluateCharacterWorkContinuationV1(ledger, input, prefix.ContinuationBoundaries()...)
		verifiedStoreMust(t, err)
		if !eligible.Eligible {
			t.Fatalf("fixture unexpectedly woke: %+v", eligible)
		}
		cycle.WorkContinuations = append(cycle.WorkContinuations, *eligible.Receipt)
	}
	cycle, err = domain.FinalizeCharacterContinuationCycleV2(prefix, input, cycle)
	verifiedStoreMust(t, err)
	return input, cycle
}

func verifiedStoreAudit(t *testing.T, context domain.CharacterReadinessContext, prefix domain.VerifiedCharacterActivationPrefix, decision string) domain.CharacterReadinessReviewAudit {
	t.Helper()
	input, err := domain.NewCharacterReadinessReviewInputFromSteps(context, prefix.Session(), prefix.Steps(), "sha256:"+strings.Repeat("e", 64))
	verifiedStoreMust(t, err)
	verdict := testutil.ReadyVerdict(input)
	verdict.Decision = decision
	receipt, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	verifiedStoreMust(t, err)
	audit := domain.CharacterReadinessReviewAudit{Input: input, Receipt: receipt}
	verifiedStoreMust(t, domain.ValidateCharacterReadinessReviewAudit(audit))
	return audit
}

func verifiedStoreSetup(t *testing.T) (*Store, domain.CharacterReadinessContext, domain.CharacterActivationInputSet, domain.CharacterActivationCycle, domain.CharacterActivationSession) {
	t.Helper()
	st := NewStore(t.TempDir())
	verifiedStoreMust(t, st.Init())
	context, input, cycle := verifiedStoreOrigin(t)
	session, err := domain.NewCharacterActivationSession(cycle.GenerationID, cycle.Chapter, context.Digest, *input.Stimulus.PhysicalState, 0, 8)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.SaveCharacterReadinessContext(context))
	verifiedStoreMust(t, st.CreateCharacterActivationSession(session))
	view, err := st.CharacterAgents.ForActivationCycle(session)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, view.PublishActivationInputs(input))
	verifiedStoreProofs(t, view, cycle)
	return st, context, input, cycle, session
}

func verifiedStoreProofs(t *testing.T, view *CharacterAgentStore, cycle domain.CharacterActivationCycle) {
	t.Helper()
	for _, proposal := range cycle.Evidence.Proposals {
		var observation domain.CharacterObservationPacket
		for _, o := range cycle.Evidence.Observations {
			if o.AgentID == proposal.AgentID && o.Digest == proposal.ObservationDigest {
				observation = o
			}
		}
		verifiedStoreMust(t, view.SaveProposal(proposal, observation))
	}
	for _, receipt := range cycle.Evidence.Arbitrations {
		if cycle.Version != domain.CharacterActivationCycleV2Version {
			verifiedStoreMust(t, view.SaveArbitration(receipt, cycle.Evidence.Stimulus, cycle.Evidence.Activation, cycle.Evidence.Proposals, 1))
			continue
		}
		st := NewStore(view.io.dir)
		prefix, err := st.LoadVerifiedCharacterActivationPrefix(cycle.GenerationID, cycle.Chapter)
		verifiedStoreMust(t, err)
		arbiter, err := st.NewCharacterContinuationArbitration(prefix.Session(), cycle.WorkContinuations, cycle.Evidence.ProtocolDigest)
		verifiedStoreMust(t, err)
		verifiedStoreMust(t, arbiter.SaveArbitration(receipt))
		loaded, err := arbiter.LoadArbitration()
		verifiedStoreMust(t, err)
		actual, err := arbiter.FinalizeCycle(*loaded)
		verifiedStoreMust(t, err)
		if !jsonValuesEqual(actual, cycle) {
			t.Fatal("store arbitration view changed the source-aware domain cycle")
		}
	}
}

func verifiedStoreContinue(t *testing.T, st *Store, context domain.CharacterReadinessContext, cycle domain.CharacterActivationCycle, session domain.CharacterActivationSession) domain.VerifiedCharacterActivationPrefix {
	t.Helper()
	_, err := st.AppendVerifiedCharacterActivationCycle(session.Digest, cycle)
	verifiedStoreMust(t, err)
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
	verifiedStoreMust(t, err)
	audit := verifiedStoreAudit(t, context, *prefix, "continue")
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	_, err = st.ApplyVerifiedCharacterChapterReadiness(prefix.Session().Digest, audit.Receipt)
	verifiedStoreMust(t, err)
	prefix, err = st.LoadVerifiedCharacterActivationPrefix(session.GenerationID, session.Chapter)
	verifiedStoreMust(t, err)
	return *prefix
}

func TestVerifiedActivationStoreMixedContinuationRestartsFlatSourceChain(t *testing.T) {
	st, context, _, origin, initial := verifiedStoreSetup(t)
	prefix := verifiedStoreContinue(t, st, context, origin, initial)
	originalJSON, _ := json.Marshal(origin.Evidence.Proposals)
	for index := 2; index <= 3; index++ {
		input, cycle := verifiedStoreNext(t, prefix)
		view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
		verifiedStoreMust(t, err)
		verifiedStoreMust(t, view.PublishActivationInputs(input))
		verifiedStoreProofs(t, view, cycle)
		old := prefix.Session()
		// Use the old public append too: explicit v2 dispatch must resolve the
		// true predecessor rather than accepting this source-less envelope.
		_, err = st.AppendCharacterActivationCycle(old.Digest, cycle)
		verifiedStoreMust(t, err)
		loaded, err := NewStore(st.dir).LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
		verifiedStoreMust(t, err)
		if len(loaded.Steps()) != index {
			t.Fatal("flat restart lost a cycle")
		}
		step := loaded.Steps()[index-1]
		if len(step.EffectiveProposals()) != 3 || len(step.Cycle().Evidence.Proposals) != 1 || len(step.Cycle().WorkContinuations) != 2 {
			t.Fatal("fresh/continued source sets conflated")
		}
		if got := *step.AfterState().Resources[0].ActualAmount; got != 11 {
			t.Fatalf("shared balance settled multiple times: %g", got)
		}
		for _, p := range origin.Evidence.Proposals {
			l, ok := loaded.ContinuationLedger(p.AgentID)
			if !ok || len(l.Entries) != index-1 {
				t.Fatal("missing authenticated owner ledger")
			}
		}
		decision := "continue"
		if index == 3 {
			decision = "ready_for_plan"
		}
		audit := verifiedStoreAudit(t, context, *loaded, decision)
		verifiedStoreMust(t, st.SaveCharacterReadinessReviewAudit(audit))
		_, err = st.ApplyCharacterChapterReadiness(loaded.Session().Digest, audit.Receipt)
		verifiedStoreMust(t, err)
		loaded, err = st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
		verifiedStoreMust(t, err)
		prefix = *loaded
		public, err := st.LoadCharacterActivationCycle(initial.GenerationID, 1, index)
		verifiedStoreMust(t, err)
		if public.Digest != cycle.Digest {
			t.Fatal("legacy public load lost global root")
		}
	}
	got, _ := json.Marshal(prefix.Steps()[0].Cycle().Evidence.Proposals)
	if string(got) != string(originalJSON) {
		t.Fatal("source proposals were rewritten")
	}
	chapter, err := st.CollectCharacterActivationChapterEvidence(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, domain.ValidateCharacterActivationChapterEvidence(*chapter))
	loadedChapter, err := NewStore(st.dir).LoadCharacterActivationChapterEvidence(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	if loadedChapter.Digest != chapter.Digest || len(chapter.Cycles) != 3 {
		t.Fatal("ready chapter lost its complete flat execution evidence")
	}
	before, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	for _, cycle := range chapter.Cycles {
		actual, err := st.CharacterAgents.LoadActivationInputsForCycle(cycle)
		verifiedStoreMust(t, err)
		if actual.Digest != cycle.InputSetDigest {
			t.Fatal("history input was reconstructed instead of read exactly")
		}
	}
	_, err = st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	after, err := DirectoryContentRoot(st.dir)
	verifiedStoreMust(t, err)
	if before != after {
		t.Fatal("verified read/history verification changed the workspace")
	}
}

func TestVerifiedActivationStoreRecoversCycleAndReadinessBeforeCursor(t *testing.T) {
	st, context, _, origin, initial := verifiedStoreSetup(t)
	prefix := verifiedStoreContinue(t, st, context, origin, initial)
	input, cycle := verifiedStoreNext(t, prefix)
	view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, view.PublishActivationInputs(input))
	verifiedStoreProofs(t, view, cycle)
	root, err := characterActivationSessionDir(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, st.writeCharacterActivationJSON(activationCyclePath(root, 2), cycle, true))
	loaded, err := st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	if loaded.Session().Digest != prefix.Session().Digest {
		t.Fatal("read-only load advanced an orphan cycle")
	}
	if _, err := st.LoadCharacterActivationCycle(initial.GenerationID, 1, 2); err == nil {
		t.Fatal("uncommitted v2 cycle acquired source authority")
	}
	recovered, err := NewStore(st.dir).RecoverCharacterActivationSession(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	if recovered.Phase != "assessing" || len(recovered.CycleDigests) != 2 {
		t.Fatal("durable cycle was not recovered")
	}
	loaded, err = st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	audit := verifiedStoreAudit(t, context, *loaded, "continue")
	verifiedStoreMust(t, st.SaveVerifiedCharacterReadinessReviewAudit(audit))
	verifiedStoreMust(t, st.writeCharacterActivationJSON(activationReadinessPath(root, 2), audit.Receipt, true))
	recovered, err = NewStore(st.dir).RecoverVerifiedCharacterActivationSession(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	if recovered.Phase != "collecting" || len(recovered.ReadinessDigests) != 2 {
		t.Fatal("durable paid assessment was not recovered")
	}
	before, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
	verifiedStoreMust(t, err)
	_, err = st.AppendVerifiedCharacterActivationCycle("old CAS is allowed only for an identical committed retry", cycle)
	verifiedStoreMust(t, err)
	_, err = st.ApplyVerifiedCharacterChapterReadiness("old CAS", audit.Receipt)
	verifiedStoreMust(t, err)
	_, err = st.RecoverVerifiedCharacterActivationSession(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	after, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
	verifiedStoreMust(t, err)
	if string(before) != string(after) {
		t.Fatal("recovery/retry incremented or resigned the cursor")
	}
	// The proof namespace only has the actually fresh independent proposal.
	for _, observation := range input.Observations {
		if observation.AgentID == cycle.Evidence.Proposals[0].AgentID {
			verifiedStoreMust(t, view.SaveProposal(cycle.Evidence.Proposals[0], observation))
		}
	}
	if err := view.SaveArbitration(cycle.Evidence.Arbitrations[0], input.Stimulus, input.Activation, cycle.Evidence.Proposals, 1); err == nil {
		t.Fatal("bare proof store accepted old original proposals as current fresh submissions")
	}
}

func TestVerifiedActivationStoreConcurrentCASIsImmutable(t *testing.T) {
	st, context, _, origin, initial := verifiedStoreSetup(t)
	prefix := verifiedStoreContinue(t, st, context, origin, initial)
	input, cycle := verifiedStoreNext(t, prefix)
	view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
	verifiedStoreMust(t, err)
	verifiedStoreMust(t, view.PublishActivationInputs(input))
	verifiedStoreProofs(t, view, cycle)
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewStore(st.dir).AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		verifiedStoreMust(t, err)
	}
	loaded, err := st.LoadVerifiedCharacterActivationPrefix(initial.GenerationID, 1)
	verifiedStoreMust(t, err)
	if len(loaded.Steps()) != 2 {
		t.Fatal("concurrent append duplicated a step")
	}
	forged := verifiedStoreCopy(cycle)
	forged.Evidence.Arbitrations[0].Resolutions[0].StateAfter = "changed without updating the claimed digest"
	if _, err := st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, forged); err == nil {
		t.Fatal("same-root different-content retry accepted")
	}
}

func TestVerifiedActivationStoreFailsClosedOnMissingOrAlteredSources(t *testing.T) {
	for _, name := range []string{"missing_baseline", "changed_baseline", "missing_input", "foreign_input", "missing_audit", "resigned_audit", "bare_v2", "missing_fresh_proposal", "changed_fresh_proposal", "missing_arbitration", "changed_arbitration"} {
		t.Run(name, func(t *testing.T) {
			st, context, _, origin, initial := verifiedStoreSetup(t)
			prefix := verifiedStoreContinue(t, st, context, origin, initial)
			input, cycle := verifiedStoreNext(t, prefix)
			view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
			verifiedStoreMust(t, err)
			verifiedStoreMust(t, view.PublishActivationInputs(input))
			verifiedStoreProofs(t, view, cycle)
			root, err := characterActivationSessionDir(initial.GenerationID, 1)
			verifiedStoreMust(t, err)
			switch name {
			case "missing_baseline":
				verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(activationBaselinePath(root))))
			case "changed_baseline":
				changed := initial
				changed.MaxCycles++
				// A well-formed different empty baseline cannot match the existing cursor.
				changed, err = domain.NewCharacterActivationSession(initial.GenerationID, 1, initial.ChapterContextDigest, *origin.Evidence.Stimulus.PhysicalState, 0, changed.MaxCycles)
				verifiedStoreMust(t, err)
				verifiedStoreMust(t, st.CharacterAgents.io.WriteJSON(activationBaselinePath(root), changed))
			case "missing_input":
				verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(activationFrozenInputPath(root, 2))))
			case "foreign_input":
				verifiedStoreMust(t, st.CharacterAgents.io.WriteJSON(activationFrozenInputPath(root, 2), prefix.Steps()[0].Input()))
			case "missing_audit":
				verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(characterReadinessAuditPath(root, 1))))
			case "resigned_audit":
				var audit domain.CharacterReadinessReviewAudit
				verifiedStoreMust(t, st.readCharacterActivationJSON(characterReadinessAuditPath(root, 1), &audit))
				audit.Input.Trace.Cycles[0].Actions[0].ImmediateResult = "invented successful progress"
				verdict := testutil.ReadyVerdict(audit.Input)
				verdict.Decision = "continue"
				audit.Receipt, err = domain.FinalizeCharacterReadinessReview(audit.Input, verdict)
				verifiedStoreMust(t, err)
				verifiedStoreMust(t, st.CharacterAgents.io.WriteJSON(characterReadinessAuditPath(root, 1), audit))
			case "bare_v2":
				verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(activationCyclePath(root, 1))))
			case "missing_fresh_proposal", "changed_fresh_proposal":
				proposal := cycle.Evidence.Proposals[0]
				path, err := view.proofPath(characterAgentProposalPath(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID))
				verifiedStoreMust(t, err)
				if name == "missing_fresh_proposal" {
					verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(path)))
				} else {
					proposal.DecisionReason = "unsaved altered intent"
					verifiedStoreMust(t, st.CharacterAgents.io.WriteJSON(path, proposal))
				}
			case "missing_arbitration", "changed_arbitration":
				receipt := cycle.Evidence.Arbitrations[0]
				path, err := view.proofPath(characterAgentArbitrationPath(receipt.GenerationID, receipt.Chapter, receipt.Round))
				verifiedStoreMust(t, err)
				if name == "missing_arbitration" {
					verifiedStoreMust(t, os.Remove(st.CharacterAgents.io.path(path)))
				} else {
					receipt.Resolutions[0].StateAfter = "changed actual result"
					verifiedStoreMust(t, st.CharacterAgents.io.WriteJSON(path, receipt))
				}
			}
			before, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
			verifiedStoreMust(t, err)
			if _, err := st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle); err == nil {
				t.Fatal("unverified source admitted")
			}
			if _, err := os.Stat(st.CharacterAgents.io.path(activationCyclePath(root, 2))); !os.IsNotExist(err) {
				t.Fatal("failed verification wrote a cycle")
			}
			after, err := os.ReadFile(st.CharacterAgents.io.path(filepath.Join(root, "session.json")))
			verifiedStoreMust(t, err)
			if string(before) != string(after) {
				t.Fatal("failed verification changed cursor")
			}
		})
	}
}

func TestVerifiedActivationStoreAbsentAndUnsafeSourcesAreReadOnly(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		st := NewStore(t.TempDir())
		before, err := DirectoryContentRoot(st.dir)
		verifiedStoreMust(t, err)
		prefix, err := st.LoadVerifiedCharacterActivationPrefix("pg2_missing", 1)
		verifiedStoreMust(t, err)
		if prefix != nil {
			t.Fatal("absent history created authority")
		}
		after, err := DirectoryContentRoot(st.dir)
		verifiedStoreMust(t, err)
		if before != after {
			t.Fatal("absent read created directories or lock")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		st, context, _, origin, initial := verifiedStoreSetup(t)
		prefix := verifiedStoreContinue(t, st, context, origin, initial)
		input, cycle := verifiedStoreNext(t, prefix)
		view, err := st.CharacterAgents.ForActivationCycle(prefix.Session())
		verifiedStoreMust(t, err)
		verifiedStoreMust(t, view.PublishActivationInputs(input))
		verifiedStoreProofs(t, view, cycle)
		root, err := characterActivationSessionDir(initial.GenerationID, 1)
		verifiedStoreMust(t, err)
		path := st.CharacterAgents.io.path(activationFrozenInputPath(root, 2))
		external := filepath.Join(t.TempDir(), "input.json")
		raw, err := os.ReadFile(path)
		verifiedStoreMust(t, err)
		verifiedStoreMust(t, os.WriteFile(external, raw, 0600))
		verifiedStoreMust(t, os.Remove(path))
		verifiedStoreMust(t, os.Symlink(external, path))
		if _, err := st.AppendVerifiedCharacterActivationCycle(prefix.Session().Digest, cycle); err == nil {
			t.Fatal("symlink input crossed the evidence boundary")
		}
		after, err := os.ReadFile(external)
		verifiedStoreMust(t, err)
		if string(after) != string(raw) {
			t.Fatal("rejected source changed external file")
		}
	})
}
