package agents

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func selectionMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func selectionCopy[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	selectionMust(t, err)
	var copy T
	selectionMust(t, json.Unmarshal(raw, &copy))
	return copy
}

func selectionNumber(value float64) *float64 { return &value }

// Extend the existing complete-cycle fixture, then use production finalizers
// for every source. No caller-supplied cumulative progress or fake eligibility
// result is used. Each owner actually worked one minute in this common cycle.
func selectionFixture(t *testing.T, count int, edit func(int, *domain.CharacterDecisionProposal)) (domain.CharacterActivationCycle, domain.CharacterActivationInputSet, map[string]domain.CharacterWorkContinuationLedgerV1) {
	t.Helper()
	cycle := testutil.CharacterCycle(t, 1, "", nil, 0)
	e := cycle.Evidence
	baseObservation, baseProposal := e.Observations[0], e.Proposals[0]
	state := *e.Stimulus.PhysicalState
	for i := 1; i < count; i++ {
		registry, actor, err := e.Registry.UpsertCharacter(fmt.Sprintf("角色%02d", i), nil, "important", 1, "")
		selectionMust(t, err)
		e.Registry = registry
		state.Actors = append(state.Actors, domain.CharacterPhysicalStateV2{AgentID: actor.AgentID, Character: actor.Character, Location: "船上", Resources: []domain.CharacterResourceHoldingV2{}})
	}
	var err error
	e.Registry, err = domain.FinalizeCharacterAgentRegistry(e.Registry)
	selectionMust(t, err)
	state, err = domain.PrepareCharacterSelfExperienceStateV2(state)
	selectionMust(t, err)
	e.Stimulus.PhysicalState = &state
	e.Stimulus.Sources = append(e.Stimulus.Sources, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1)
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	selectionMust(t, err)
	e.Activation.RegistryRoot = e.Registry.RegistryRoot
	e.Activation.Entries, e.Observations, e.Proposals, e.MemoryRoots = nil, nil, nil, nil
	var memories []domain.CharacterAgentMemory
	var resolutions []domain.CharacterDecisionResolution
	for i, actor := range state.Actors {
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: actor.AgentID, Character: actor.Character, State: "projected", GenerationID: cycle.GenerationID})
		selectionMust(t, err)
		memories = append(memories, memory)
		o := selectionCopy(t, baseObservation)
		o.AgentID, o.Character, o.MemoryRoot, o.StimulusDigest = actor.AgentID, actor.Character, memory.MemoryRoot, e.Stimulus.Digest
		o.Sources = []string{domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
		o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: 1, ChapterContextDigest: cycle.ChapterContextDigest}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(state, actor.AgentID)
		selectionMust(t, err)
		o, err = domain.FinalizeCharacterObservationPacket(o)
		selectionMust(t, err)
		p := selectionCopy(t, baseProposal)
		p.AgentID, p.Character, p.ObservationDigest = actor.AgentID, actor.Character, o.Digest
		p.IntendedAction = "进行常规检查"
		p.SelfTasks = []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "常规检查", ProgressTarget: selectionNumber(5), ProgressUnit: "minute", KnowledgeRefs: p.KnowledgeRefs}}
		p.WorkContinuations = []domain.CharacterWorkContinuationAuthorizationV1{{TaskID: "inspection", UntilTarget: true}}
		if edit != nil {
			edit(i, &p)
		}
		p, err = domain.FinalizeCharacterDecisionProposal(p, o)
		selectionMust(t, err)
		e.Observations, e.Proposals, e.MemoryRoots = append(e.Observations, o), append(e.Proposals, p), append(e.MemoryRoots, memory.MemoryRoot)
		e.Activation.Entries = append(e.Activation.Entries, domain.CharacterAgentActivationEntry{AgentID: actor.AgentID, Character: actor.Character, Tier: o.Tier, State: domain.CharacterAgentActive, Reasons: []string{"task_progress"}, ObservationDigest: o.Digest})
		post := actor
		resolutions = append(resolutions, domain.CharacterDecisionResolution{AgentID: p.AgentID, Character: p.Character, ProposalDigest: p.Digest, Decision: p.Decision, IntendedAction: p.IntendedAction, ActionOrder: i + 1, Outcome: "success", CompletionState: "in_progress", ImmediateResult: "本轮常规检查仍在进行", StateAfter: "检查工作尚未结束", PostState: &post, SelfExecutions: []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: selectionNumber(0), EndDay: selectionNumber(1.0 / 1440)}}, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "本轮工作时间已经消耗", TransmissionPath: "本人操作", ArrivalChapter: 1, ProtagonistImpact: "后续工作仍需时间"}}})
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	selectionMust(t, err)
	r := e.Arbitrations[0]
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", e.Stimulus.Digest, e.Activation.Digest
	r.ProposalDigests, r.ResourceSettlements, r.ProtagonistProjection = nil, nil, domain.ProtagonistDecisionProjection{}
	r.Resolutions = resolutions
	for _, p := range e.Proposals {
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
	}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, e.Proposals, 1)
	selectionMust(t, err)
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	selectionMust(t, err)
	initial, err := domain.FinalizeCharacterActivationInputSet(domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: e.Stimulus, Activation: e.Activation, Observations: e.Observations, Memories: memories})
	selectionMust(t, err)
	cycle.Evidence, cycle.InputSetDigest = e, initial.Digest
	cycle, err = domain.FinalizeCharacterActivationCycle(cycle)
	selectionMust(t, err)
	selectionMust(t, domain.ValidateCharacterActivationInputsForCycle(initial, cycle))
	ledgers := make(map[string]domain.CharacterWorkContinuationLedgerV1, len(e.Proposals))
	for _, p := range e.Proposals {
		ledger, err := domain.NewCharacterWorkContinuationLedgerV1(cycle, p.Digest, "inspection")
		selectionMust(t, err)
		ledgers[p.AgentID] = ledger
	}
	physical, err := domain.ApplyArbitrationPhysicalStateV2(r, e.Stimulus, e.Proposals...)
	selectionMust(t, err)
	input := selectionCopy(t, initial)
	input.Stimulus.PhysicalState = &physical
	clock := *input.Stimulus.StoryClock
	clock.CurrentDay = cycle.EndDay
	clock, err = domain.FinalizeStoryClockContext(clock)
	selectionMust(t, err)
	input.Stimulus.StoryClock = &clock
	token, err := domain.CharacterActivationCycleSourceToken(cycle.GenerationID, cycle.Chapter, 2, cycle.ChapterContextDigest, cycle.Digest)
	selectionMust(t, err)
	input.Stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	for i := range input.Observations {
		input.Observations[i].CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: 2, ChapterContextDigest: cycle.ChapterContextDigest, PreviousCycleDigest: cycle.Digest, CurrentDay: cycle.EndDay}
		input.Observations[i].SelfExperiences, input.Observations[i].TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, input.Observations[i].AgentID)
		selectionMust(t, err)
	}
	return cycle, selectionFinalizeInput(t, input), ledgers
}

func selectionFinalizeInput(t *testing.T, input domain.CharacterActivationInputSet) domain.CharacterActivationInputSet {
	t.Helper()
	var err error
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	selectionMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.StimulusDigest = input.Stimulus.Digest
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		selectionMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID {
				input.Activation.Entries[j].ObservationDigest = ""
				if input.Activation.Entries[j].State == domain.CharacterAgentActive {
					input.Activation.Entries[j].ObservationDigest = o.Digest
				}
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	selectionMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	selectionMust(t, err)
	return input
}

func TestSelectWorkContinuationsPartitionsMoreThanEightActorsWithoutChangingSources(t *testing.T) {
	origin, input, ledgers := selectionFixture(t, 12, nil)
	ids := make([]string, 0, len(ledgers))
	for id := range ledgers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	delete(ledgers, ids[2]) // Absence is a fresh decision, never inferred authority.
	delete(ledgers, ids[7])
	for i := range input.Activation.Entries {
		if input.Activation.Entries[i].AgentID == ids[11] {
			input.Activation.Entries[i].State = domain.CharacterAgentSleeping
			input.Activation.Entries[i].Reasons = nil
		}
	}
	input = selectionFinalizeInput(t, input)
	before, err := json.Marshal([]any{origin, input, ledgers})
	selectionMust(t, err)
	selection, err := SelectCharacterWorkContinuations(input, ledgers)
	selectionMust(t, err)
	if !reflect.DeepEqual(selection.FreshAgentIDs, []string{ids[2], ids[7]}) || len(selection.Continuations) != 9 {
		t.Fatalf("wrong partition: %+v", selection)
	}
	seen := map[string]bool{}
	for _, id := range selection.FreshAgentIDs {
		seen[id] = true
		if !reflect.DeepEqual(selection.WakeReasons[id], []string{"no_continuation_authorization"}) {
			t.Fatal("missing authorization was invented")
		}
	}
	last := ""
	for _, receipt := range selection.Continuations {
		if receipt.AgentID <= last || seen[receipt.AgentID] || receipt.AgentID == ids[11] {
			t.Fatal("unsorted, duplicate or sleeping continuation")
		}
		last, seen[receipt.AgentID] = receipt.AgentID, true
		if receipt.EffectiveCompleted != 1 || receipt.AuthorizedRemaining != 4 || receipt.InputSetDigest != input.Digest || receipt.OriginCycleDigest != origin.Digest {
			t.Fatal("selection changed actual work allowance/source binding")
		}
		selectionMust(t, domain.ValidateCharacterWorkContinuationReceiptV1(receipt, ledgers[receipt.AgentID], input))
	}
	if len(seen) != 11 {
		t.Fatal("active actor omitted")
	}
	replayed, err := SelectCharacterWorkContinuations(input, ledgers)
	selectionMust(t, err)
	if !reflect.DeepEqual(selection, replayed) {
		t.Fatal("same frozen inputs produced a different dispatch")
	}
	after, err := json.Marshal([]any{origin, input, ledgers})
	selectionMust(t, err)
	if string(before) != string(after) {
		t.Fatal("dispatch mutated original proposal/activation/input/ledger")
	}
}

func TestSelectWorkContinuationsWakesFromDomainEvidenceWithoutInventingAuthority(t *testing.T) {
	for _, reason := range []string{"owner_information_changed", "task_completed", "authorization_exhausted"} {
		t.Run(reason, func(t *testing.T) {
			_, input, ledgers := selectionFixture(t, 1, func(_ int, p *domain.CharacterDecisionProposal) {
				if reason == "task_completed" {
					p.SelfTasks[0].ProgressTarget = selectionNumber(1)
				}
				if reason == "authorization_exhausted" {
					p.WorkContinuations[0].UntilTarget = false
					p.WorkContinuations[0].MaxEffectiveMinutes = selectionNumber(1)
				}
			})
			id := input.Observations[0].AgentID
			if reason == "owner_information_changed" {
				input.Observations[0].KnownFacts = append(input.Observations[0].KnownFacts, domain.CharacterAgentFact{ID: "actual-new-information", Kind: "known", Text: "收到暂停检查的通知"})
				input = selectionFinalizeInput(t, input)
			}
			selection, err := SelectCharacterWorkContinuations(input, ledgers)
			selectionMust(t, err)
			if !reflect.DeepEqual(selection.FreshAgentIDs, []string{id}) || len(selection.Continuations) != 0 || !slices.Contains(selection.WakeReasons[id], reason) {
				t.Fatalf("domain wake reason lost: %+v", selection)
			}
		})
	}
}

func TestSelectWorkContinuationsRejectsBadInputsAndUnknownOrMismatchedLedgers(t *testing.T) {
	_, input, ledgers := selectionFixture(t, 2, nil)
	ids := []string{input.Observations[0].AgentID, input.Observations[1].AgentID}
	for _, name := range []string{"unknown_owner", "wrong_owner", "bad_ledger", "bad_input", "foreign_generation", "unverified_boundary", "sleeping_bad_ledger"} {
		t.Run(name, func(t *testing.T) {
			current, sources := selectionCopy(t, input), selectionCopy(t, ledgers)
			var boundaries []domain.VerifiedCharacterWorkContinuationBoundaryV1
			switch name {
			case "unknown_owner":
				sources["unknown-owner"] = sources[ids[0]]
			case "wrong_owner":
				sources[ids[1]] = sources[ids[0]]
			case "bad_ledger", "sleeping_bad_ledger":
				ledger := sources[ids[0]]
				ledger.Digest = "sha256:" + strings.Repeat("f", 64)
				sources[ids[0]] = ledger
				if name == "sleeping_bad_ledger" {
					current.Activation.Entries[0].State = domain.CharacterAgentSleeping
					current.Activation.Entries[0].Reasons = nil
					current = selectionFinalizeInput(t, current)
				}
			case "bad_input":
				current.Digest = "sha256:" + strings.Repeat("f", 64)
			case "foreign_generation":
				ledger := sources[ids[0]]
				ledger.Origin.GenerationID = "pg2_foreign"
				sources[ids[0]] = ledger
			case "unverified_boundary":
				boundaries = []domain.VerifiedCharacterWorkContinuationBoundaryV1{{}}
			}
			result, err := SelectCharacterWorkContinuations(current, sources, boundaries...)
			if err == nil || !reflect.DeepEqual(result, CharacterWorkContinuationSelection{}) {
				t.Fatalf("bad proof did not fail closed with zero dispatch: %+v %v", result, err)
			}
		})
	}
}

func TestSelectWorkContinuationsWithoutLedgerPreservesFreshLegacyDispatch(t *testing.T) {
	input := testutil.CharacterActivationInputs(t)
	selection, err := SelectCharacterWorkContinuations(input, nil)
	selectionMust(t, err)
	id := input.Activation.Entries[0].AgentID
	if !reflect.DeepEqual(selection.FreshAgentIDs, []string{id}) || len(selection.Continuations) != 0 || !reflect.DeepEqual(selection.WakeReasons[id], []string{"no_continuation_authorization"}) {
		t.Fatalf("legacy input acquired authority or lost its fresh decision: %+v", selection)
	}
}

func TestSelectWorkContinuationsUsesVerifiedGlobalBoundaryForPriorExecution(t *testing.T) {
	origin, input, ledgers := selectionFixture(t, 1, nil)
	id := input.Observations[0].AgentID
	selection, err := SelectCharacterWorkContinuations(input, ledgers)
	selectionMust(t, err)
	proposal := origin.Evidence.Proposals[0]
	r := selectionCopy(t, origin.Evidence.Arbitrations[0])
	r.Digest, r.StimulusDigest, r.ActivationDigest = "", input.Stimulus.Digest, input.Activation.Digest
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}
	r.Resolutions[0].PostState = &input.Stimulus.PhysicalState.Actors[0]
	r.Resolutions[0].SelfExecutions = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "in_progress", StartDay: &start, EndDay: &end}}
	r, err = domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, []domain.CharacterDecisionProposal{proposal}, 1)
	selectionMust(t, err)
	ledger, err := domain.AppendCharacterWorkContinuationExecutionV1(ledgers[id], input, selection.Continuations[0], r, nil)
	selectionMust(t, err)
	// This test's entire world has one owner and one continuation entry. The
	// test-only adapter verifies that complete ledger through the real domain
	// kernel; production must supply its own complete multi-owner verifier.
	raw, err := json.Marshal(map[string]any{"complete_single_owner_test_ledger": ledger})
	selectionMust(t, err)
	boundary, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
		var proof struct {
			Ledger domain.CharacterWorkContinuationLedgerV1 `json:"complete_single_owner_test_ledger"`
		}
		if err := json.Unmarshal(raw, &proof); err != nil {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, err
		}
		if err := domain.ValidateCharacterWorkContinuationLedgerV1(proof.Ledger); err != nil {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, err
		}
		if len(proof.Ledger.Origin.Evidence.Activation.Entries) != 1 || len(proof.Ledger.Entries) != 1 {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, fmt.Errorf("incomplete single-owner test proof")
		}
		entry := proof.Ledger.Entries[0]
		root, err := domain.ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
		return domain.CharacterWorkContinuationGlobalBindingV1{GlobalRoot: root, GenerationID: origin.GenerationID, Chapter: 1, Cycle: 2, InputSetDigest: entry.Input.Digest, ArbitrationDigest: entry.Arbitration.Digest, AfterPhysicalRoot: entry.AfterPhysicalRoot, ContinuationEntryDigests: []string{entry.EntryDigest}}, err
	})
	selectionMust(t, err)
	physical, err := domain.ApplyArbitrationPhysicalStateV2(r, input.Stimulus, proposal)
	selectionMust(t, err)
	next := selectionCopy(t, input)
	next.Stimulus.PhysicalState = &physical
	next.Stimulus.StoryClock.CurrentDay = end
	clock, err := domain.FinalizeStoryClockContext(*next.Stimulus.StoryClock)
	selectionMust(t, err)
	next.Stimulus.StoryClock = &clock
	token, err := domain.CharacterActivationCycleSourceToken(origin.GenerationID, 1, 3, origin.ChapterContextDigest, boundary.GlobalRoot())
	selectionMust(t, err)
	next.Stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	next.Observations[0].CycleContext.Index = 3
	next.Observations[0].CycleContext.CurrentDay = end
	next.Observations[0].CycleContext.PreviousCycleDigest = boundary.GlobalRoot()
	next.Observations[0].SelfExperiences, next.Observations[0].TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, id)
	selectionMust(t, err)
	next = selectionFinalizeInput(t, next)
	ledgers[id] = ledger
	if result, err := SelectCharacterWorkContinuations(next, ledgers); err == nil || !reflect.DeepEqual(result, CharacterWorkContinuationSelection{}) {
		t.Fatal("an entry digest or missing global boundary authorized dispatch")
	}
	result, err := SelectCharacterWorkContinuations(next, ledgers, boundary)
	selectionMust(t, err)
	if len(result.FreshAgentIDs) != 0 || len(result.Continuations) != 1 || result.Continuations[0].Cycle != 3 || result.Continuations[0].EffectiveCompleted != 2 || result.Continuations[0].AuthorizedRemaining != 3 {
		t.Fatalf("verified boundary was not forwarded to the real eligibility kernel: %+v", result)
	}
}
