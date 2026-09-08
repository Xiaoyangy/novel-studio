package domain_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func workBatchFixture(t *testing.T, limited bool) (domain.CharacterActivationCycle, []domain.CharacterWorkContinuationLedgerV1, domain.CharacterActivationInputSet, domain.CharacterDecisionProposal) {
	t.Helper()
	origin := continuationOriginFixture(t, nil, nil)
	e := continuationCopy(origin.Evidence)
	baseActor := e.Stimulus.PhysicalState.Actors[0]
	baseActor.Resources[0].Access = "shared"
	e.Stimulus.PhysicalState.Actors = []domain.CharacterPhysicalStateV2{baseActor}
	baseObservation := e.Observations[0]
	baseProposal := e.Proposals[0]
	registry := e.Registry
	for _, name := range []string{"乙", "丙"} {
		var record domain.CharacterAgentRecord
		var err error
		registry, record, err = registry.UpsertCharacter(name, nil, "important", 1, "")
		continuationMust(t, err)
		actor := continuationCopy(baseActor)
		actor.AgentID, actor.Character = record.AgentID, name
		if name == "丙" {
			actor.Resources = []domain.CharacterResourceHoldingV2{}
		}
		e.Stimulus.PhysicalState.Actors = append(e.Stimulus.PhysicalState.Actors, actor)
	}
	var err error
	e.Registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	continuationMust(t, err)
	physical, err := domain.FinalizeWorldPhysicalStateV2(*e.Stimulus.PhysicalState)
	continuationMust(t, err)
	e.Stimulus.PhysicalState = &physical
	e.Stimulus, err = domain.FinalizeWorldStimulusPacket(e.Stimulus)
	continuationMust(t, err)
	e.Observations = nil
	e.Proposals = nil
	e.MemoryRoots = nil
	e.Activation.RegistryRoot = e.Registry.RegistryRoot
	e.Activation.Entries = nil
	r := continuationCopy(e.Arbitrations[0])
	r.Digest = ""
	r.StimulusDigest = e.Stimulus.Digest
	r.ProposalDigests = nil
	r.Resolutions = nil
	for _, actor := range physical.Actors {
		o := continuationCopy(baseObservation)
		o.AgentID, o.Character = actor.AgentID, actor.Character
		if record, ok := e.Registry.Resolve(actor.Character); ok {
			o.Tier = record.Tier
		}
		o.StimulusDigest = e.Stimulus.Digest
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, actor.AgentID)
		continuationMust(t, err)
		m, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, State: "projected", GenerationID: o.GenerationID})
		continuationMust(t, err)
		o.MemoryRoot = m.MemoryRoot
		o, err = domain.FinalizeCharacterObservationPacket(o)
		continuationMust(t, err)
		e.Observations = append(e.Observations, o)
		a := domain.CharacterAgentActivationEntry{AgentID: actor.AgentID, Character: actor.Character, Tier: o.Tier, State: domain.CharacterAgentActive, Reasons: []string{"initial_work"}, ObservationDigest: o.Digest}
		if actor.Character == "丙" {
			a.State = domain.CharacterAgentSleeping
			a.ObservationDigest = ""
			e.Activation.Entries = append(e.Activation.Entries, a)
			continue
		}
		e.MemoryRoots = append(e.MemoryRoots, o.MemoryRoot)
		e.Activation.Entries = append(e.Activation.Entries, a)
		p := continuationCopy(baseProposal)
		p.AgentID, p.Character, p.ObservationDigest = actor.AgentID, actor.Character, o.Digest
		if limited && actor.Character == "乙" {
			p.WorkContinuations[0].UntilTarget = false
			p.WorkContinuations[0].MaxEffectiveMinutes = continuationNumber(1.5)
		}
		p, err = domain.FinalizeCharacterDecisionProposal(p, o)
		continuationMust(t, err)
		e.Proposals = append(e.Proposals, p)
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		resolution := continuationCopy(origin.Evidence.Arbitrations[0].Resolutions[0])
		resolution.AgentID, resolution.Character, resolution.ProposalDigest = actor.AgentID, actor.Character, p.Digest
		resolution.PostState = &actor
		resolution.ActionOrder = len(r.Resolutions) + 1
		r.Resolutions = append(r.Resolutions, resolution)
	}
	e.Activation, err = domain.FinalizeCharacterAgentActivation(e.Activation)
	continuationMust(t, err)
	r.ActivationDigest = e.Activation.Digest
	r, err = domain.FinalizeWorldArbitrationReceipt(r, e.Stimulus, e.Activation, e.Proposals, 1)
	continuationMust(t, err)
	e.Arbitrations = []domain.WorldArbitrationReceipt{r}
	e, err = domain.FinalizeCharacterAgentEvidenceBundle(e)
	continuationMust(t, err)
	origin.Evidence = e
	origin, err = domain.FinalizeCharacterActivationCycle(origin)
	continuationMust(t, err)
	var ledgers []domain.CharacterWorkContinuationLedgerV1
	for _, p := range e.Proposals {
		l, err := domain.NewCharacterWorkContinuationLedgerV1(origin, p.Digest, "inspection")
		continuationMust(t, err)
		ledgers = append(ledgers, l)
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(r, e.Stimulus, e.Proposals...)
	continuationMust(t, err)
	input := workBatchNextInput(t, origin, after, r.StoryTime.EndDay, 2, origin.Digest)
	fresh := workBatchFreshProposal(t, input)
	return origin, ledgers, input, fresh
}

func workBatchNextInput(t *testing.T, origin domain.CharacterActivationCycle, physical domain.WorldPhysicalStateV2, day float64, index int, previous string) domain.CharacterActivationInputSet {
	t.Helper()
	e := origin.Evidence
	input := domain.CharacterActivationInputSet{Registry: e.Registry, Stimulus: continuationCopy(e.Stimulus), Activation: continuationCopy(e.Activation)}
	input.Stimulus.PhysicalState = &physical
	clock := *input.Stimulus.StoryClock
	clock.CurrentDay = day
	var err error
	clock, err = domain.FinalizeStoryClockContext(clock)
	continuationMust(t, err)
	input.Stimulus.StoryClock = &clock
	token, err := domain.CharacterActivationCycleSourceToken(origin.GenerationID, origin.Chapter, index, origin.ChapterContextDigest, previous)
	continuationMust(t, err)
	input.Stimulus.Sources = []string{token, domain.CharacterSelfExperiencePolicyV2, domain.CharacterWorkContinuationPolicyV1}
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	continuationMust(t, err)
	for _, old := range e.Observations {
		o := continuationCopy(old)
		o.StimulusDigest = input.Stimulus.Digest
		o.CycleContext = &domain.CharacterObservationCycleContext{Version: domain.CharacterObservationCyclePolicy, Index: index, ChapterContextDigest: origin.ChapterContextDigest, PreviousCycleDigest: previous, CurrentDay: day}
		for _, p := range e.Proposals {
			if p.AgentID == o.AgentID {
				o.CurrentGoal, o.Pressure = p.CurrentGoal, p.Pressure
			}
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
		continuationMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(physical, o.AgentID)
		continuationMust(t, err)
		m, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: o.AgentID, Character: o.Character, State: "projected", GenerationID: o.GenerationID})
		continuationMust(t, err)
		o.Memory = nil
		o.MemoryRoot = m.MemoryRoot
		o, err = domain.FinalizeCharacterObservationPacket(o)
		continuationMust(t, err)
		input.Observations = append(input.Observations, o)
		input.Memories = append(input.Memories, m)
		for i := range input.Activation.Entries {
			a := &input.Activation.Entries[i]
			if a.AgentID == o.AgentID {
				a.State = domain.CharacterAgentActive
				a.Reasons = []string{"current_execution"}
				a.ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	continuationMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	continuationMust(t, err)
	return input
}

func workBatchFreshProposal(t *testing.T, input domain.CharacterActivationInputSet) domain.CharacterDecisionProposal {
	t.Helper()
	var o domain.CharacterObservationPacket
	for _, candidate := range input.Observations {
		if candidate.Character == "丙" {
			o = candidate
		}
	}
	p := domain.CharacterDecisionProposal{GenerationID: o.GenerationID, Chapter: o.Chapter, Round: 1, AgentID: o.AgentID, Character: o.Character, ObservationDigest: o.Digest, Location: o.Location, CurrentGoal: o.CurrentGoal, Pressure: o.Pressure, AvailableOptions: []string{"观察", "等待"}, Decision: "观察", DecisionReason: "核对现场", IntendedAction: "完成本人现场观察", ActionDuration: "一分钟", KnowledgeRefs: []string{o.KnownFacts[0].ID}, SelfTasks: []domain.CharacterSelfTaskV2{{TaskID: fmt.Sprintf("fresh_%d", o.CycleContext.Index), Kind: "work", Action: "观察现场", ProgressTarget: continuationNumber(1), ProgressUnit: "minute", KnowledgeRefs: []string{o.KnownFacts[0].ID}}}}
	p, err := domain.FinalizeCharacterDecisionProposal(p, o)
	continuationMust(t, err)
	return p
}

func workBatchItems(t *testing.T, ledgers []domain.CharacterWorkContinuationLedgerV1, input domain.CharacterActivationInputSet, boundaries []domain.VerifiedCharacterWorkContinuationBoundaryV1) []domain.CharacterWorkContinuationBatchItemV1 {
	t.Helper()
	var items []domain.CharacterWorkContinuationBatchItemV1
	for _, l := range ledgers {
		r, err := domain.EvaluateCharacterWorkContinuationV1(l, input, boundaries...)
		continuationMust(t, err)
		if !r.Eligible {
			t.Fatalf("batch owner woke unexpectedly: %+v", r)
		}
		items = append(items, domain.CharacterWorkContinuationBatchItemV1{Ledger: l, Receipt: *r.Receipt})
	}
	return items
}

func workBatchArbitration(t *testing.T, origin domain.CharacterActivationCycle, input domain.CharacterActivationInputSet, fresh domain.CharacterDecisionProposal, minutes float64, settle bool) domain.WorldArbitrationReceipt {
	t.Helper()
	proposals := append(append([]domain.CharacterDecisionProposal(nil), origin.Evidence.Proposals...), fresh)
	r := continuationCopy(origin.Evidence.Arbitrations[0])
	r.Digest = ""
	r.StimulusDigest = input.Stimulus.Digest
	r.ActivationDigest = input.Activation.Digest
	r.ProposalDigests = nil
	r.Resolutions = nil
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+minutes/1440
	r.StoryTime = &domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}
	for i, p := range proposals {
		var post domain.CharacterPhysicalStateV2
		for _, a := range input.Stimulus.PhysicalState.Actors {
			if a.AgentID == p.AgentID {
				post = a
			}
		}
		r.ProposalDigests = append(r.ProposalDigests, p.Digest)
		state := "in_progress"
		if p.Character == "丙" && minutes >= 1 {
			state = "completed"
		}
		r.Resolutions = append(r.Resolutions, domain.CharacterDecisionResolution{AgentID: p.AgentID, Character: p.Character, ProposalDigest: p.Digest, Decision: p.Decision, IntendedAction: p.IntendedAction, ActionOrder: i + 1, Outcome: "success", CompletionState: state, ImmediateResult: "已执行本段", StateAfter: "状态按实际累计", PostState: &post, SelfExecutions: []domain.CharacterSelfExecutionV2{{TaskID: p.SelfTasks[0].TaskID, Status: state, StartDay: &start, EndDay: &end}}, ButterflyEffects: []domain.DecisionButterflyEffect{{Effect: "时间已推进", TransmissionPath: "实际作业", ArrivalChapter: 1, ProtagonistImpact: "余下工作继续"}}})
	}
	if settle {
		before := *input.Stimulus.PhysicalState.Resources[0].ActualAmount
		delta := -1.0
		after := before + delta
		r.ResourceSettlements = []domain.ResourceSettlementV2{{ResourceID: input.Stimulus.PhysicalState.Resources[0].ResourceID, Before: &before, Delta: &delta, After: &after, EvidenceRefs: []string{proposals[0].Digest, proposals[1].Digest}}}
	}
	r, err := domain.FinalizeWorldArbitrationReceipt(r, input.Stimulus, input.Activation, proposals, 1)
	continuationMust(t, err)
	return r
}

func TestWorkContinuationBatchTwoContinuersAndFreshShareOneWorld(t *testing.T) {
	origin, ledgers, input, fresh := workBatchFixture(t, false)
	items := workBatchItems(t, ledgers, input, nil)
	r := workBatchArbitration(t, origin, input, fresh, 1, true)
	before, _ := json.Marshal(items)
	result, err := domain.AppendCharacterWorkContinuationBatchV1(input, items, []domain.CharacterDecisionProposal{fresh}, r, nil)
	continuationMust(t, err)
	if len(result.Owners) != 2 || *result.PhysicalState.Resources[0].ActualAmount != 11 {
		t.Fatal("shared resource was counted per continuer rather than once")
	}
	for _, owner := range result.Owners {
		if len(owner.Entry.PeerContinuations) != 1 || owner.Entry.AfterPhysicalRoot != result.AfterPhysicalRoot || owner.Entry.ContinuationSourcesRoot != result.ContinuationSourcesRoot {
			t.Fatal("owner entry lost batch identity")
		}
		if domain.ValidateCharacterWorkContinuationLedgerV1(owner.Ledger) == nil {
			t.Fatal("uncommitted peer source was accepted as globally authenticated")
		}
	}
	after, _ := json.Marshal(items)
	if string(before) != string(after) {
		t.Fatal("batch mutated original inputs/ledgers")
	}
	reverse := []domain.CharacterWorkContinuationBatchItemV1{items[1], items[0]}
	replayed, err := domain.AppendCharacterWorkContinuationBatchV1(input, reverse, []domain.CharacterDecisionProposal{fresh}, r, nil)
	continuationMust(t, err)
	if !reflect.DeepEqual(result, replayed) {
		t.Fatal("batch depends on caller owner ordering")
	}
}

func TestWorkContinuationBatchRejectsOverlapMissingOwnerAndAuthorityEscalation(t *testing.T) {
	origin, ledgers, input, fresh := workBatchFixture(t, false)
	items := workBatchItems(t, ledgers, input, nil)
	r := workBatchArbitration(t, origin, input, fresh, 1, false)
	for name, call := range map[string]func() error{
		"missing continuing owner": func() error {
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, items[:1], []domain.CharacterDecisionProposal{fresh}, r, nil)
			return e
		},
		"missing fresh owner": func() error {
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, items, nil, r, nil)
			return e
		},
		"duplicate continuing owner": func() error {
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, append(items, items[0]), []domain.CharacterDecisionProposal{fresh}, r, nil)
			return e
		},
		"fresh and continuation overlap": func() error {
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, items, append([]domain.CharacterDecisionProposal{fresh}, origin.Evidence.Proposals[0]), r, nil)
			return e
		},
		"changed grant receipt": func() error {
			bad := continuationCopy(items)
			bad[1].Receipt.AuthorizedRemaining = 500
			bad[1].Receipt.Digest, _ = domain.ComputeCharacterWorkContinuationReceiptV1Digest(bad[1].Receipt)
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, bad, []domain.CharacterDecisionProposal{fresh}, r, nil)
			return e
		},
		"changed original intent": func() error {
			bad := continuationCopy(items)
			bad[1].Ledger.Origin.Evidence.Proposals[0].Decision = "改写"
			_, e := domain.AppendCharacterWorkContinuationBatchV1(input, bad, []domain.CharacterDecisionProposal{fresh}, r, nil)
			return e
		},
	} {
		t.Run(name, func(t *testing.T) {
			if call() == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
	origin, ledgers, input, fresh = workBatchFixture(t, true)
	items = workBatchItems(t, ledgers, input, nil)
	r = workBatchArbitration(t, origin, input, fresh, 1, false)
	result, err := domain.AppendCharacterWorkContinuationBatchV1(input, items, []domain.CharacterDecisionProposal{fresh}, r, nil)
	if err == nil || len(result.Owners) != 0 {
		t.Fatal("one over-budget owner partially advanced the batch")
	}
}

func TestWorkContinuationBatchRejectsResourceConflict(t *testing.T) {
	origin, ledgers, input, fresh := workBatchFixture(t, false)
	items := workBatchItems(t, ledgers, input, nil)
	r := workBatchArbitration(t, origin, input, fresh, 1, true)
	r.ResourceSettlements = append(r.ResourceSettlements, r.ResourceSettlements[0])
	r.Digest, _ = domain.ComputeWorldArbitrationReceiptDigest(r)
	if _, err := domain.AppendCharacterWorkContinuationBatchV1(input, items, []domain.CharacterDecisionProposal{fresh}, r, nil); err == nil {
		t.Fatal("two continuers settled the same resource twice")
	}
	r = workBatchArbitration(t, origin, input, fresh, 1, false)
	for i := range r.Resolutions {
		if r.Resolutions[i].Character != "丙" {
			r.Resolutions[i].PostState.Resources[0].Access = "exclusive"
		}
	}
	r.Digest, _ = domain.ComputeWorldArbitrationReceiptDigest(r)
	if _, err := domain.AppendCharacterWorkContinuationBatchV1(input, items, []domain.CharacterDecisionProposal{fresh}, r, nil); err == nil {
		t.Fatal("two continuers cloned exclusive resource access")
	}
}

func TestWorkContinuationBatchCommittedPeersRemainReplayableWithoutNestedLedgers(t *testing.T) {
	origin, ledgers, input, fresh := workBatchFixture(t, false)
	items := workBatchItems(t, ledgers, input, nil)
	r := workBatchArbitration(t, origin, input, fresh, 1, true)
	result, err := domain.AppendCharacterWorkContinuationBatchV1(input, items, []domain.CharacterDecisionProposal{fresh}, r, nil)
	continuationMust(t, err)
	proof := struct {
		Input       domain.CharacterActivationInputSet
		Items       []domain.CharacterWorkContinuationBatchItemV1
		Fresh       []domain.CharacterDecisionProposal
		Arbitration domain.WorldArbitrationReceipt
		Result      domain.CharacterWorkContinuationBatchResultV1
	}{input, items, []domain.CharacterDecisionProposal{fresh}, r, result}
	raw, err := json.Marshal(proof)
	continuationMust(t, err)
	verifier := func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
		var decoded = proof
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, err
		}
		actual, err := domain.AppendCharacterWorkContinuationBatchV1(decoded.Input, decoded.Items, decoded.Fresh, decoded.Arbitration, nil)
		if err != nil {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, err
		}
		if !reflect.DeepEqual(actual, decoded.Result) {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, fmt.Errorf("global batch result mismatch")
		}
		root, err := domain.ComputeCharacterWorkContinuationGlobalProofRootV1(raw)
		if err != nil {
			return domain.CharacterWorkContinuationGlobalBindingV1{}, err
		}
		var members []string
		for _, o := range actual.Owners {
			members = append(members, o.Entry.EntryDigest)
		}
		return domain.CharacterWorkContinuationGlobalBindingV1{GlobalRoot: root, GenerationID: input.Stimulus.GenerationID, Chapter: 1, Cycle: 2, InputSetDigest: input.Digest, ArbitrationDigest: r.Digest, AfterPhysicalRoot: actual.AfterPhysicalRoot, ContinuationEntryDigests: members}, nil
	}
	boundary, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, verifier)
	continuationMust(t, err)
	var nextLedgers []domain.CharacterWorkContinuationLedgerV1
	for _, o := range result.Owners {
		continuationMust(t, domain.ValidateCharacterWorkContinuationLedgerV1(o.Ledger, boundary))
		nextLedgers = append(nextLedgers, o.Ledger)
	}
	next := workBatchNextInput(t, origin, result.PhysicalState, r.StoryTime.EndDay, 3, boundary.GlobalRoot())
	nextFresh := workBatchFreshProposal(t, next)
	nextItems := workBatchItems(t, nextLedgers, next, []domain.VerifiedCharacterWorkContinuationBoundaryV1{boundary})
	nextR := workBatchArbitration(t, origin, next, nextFresh, 1, true)
	second, err := domain.AppendCharacterWorkContinuationBatchV1(next, nextItems, []domain.CharacterDecisionProposal{nextFresh}, nextR, []domain.VerifiedCharacterWorkContinuationBoundaryV1{boundary})
	continuationMust(t, err)
	if *second.PhysicalState.Resources[0].ActualAmount != 10 {
		t.Fatal("multi-segment shared accounting drift")
	}
	badBoundary, err := domain.VerifyCharacterWorkContinuationBoundaryV1(raw, func(raw json.RawMessage) (domain.CharacterWorkContinuationGlobalBindingV1, error) {
		b, e := verifier(raw)
		sort.Strings(b.ContinuationEntryDigests)
		b.ContinuationEntryDigests = b.ContinuationEntryDigests[:1]
		return b, e
	})
	continuationMust(t, err)
	for _, o := range result.Owners {
		if domain.ValidateCharacterWorkContinuationLedgerV1(o.Ledger, badBoundary) == nil {
			t.Fatal("global boundary missing a peer entry authenticated the batch")
		}
	}
	tampered := continuationCopy(result.Owners[0].Ledger)
	tampered.Entries[0].PeerContinuations[0].Receipt.AuthorizedRemaining = 500
	tampered.Entries[0].PeerContinuations[0].Receipt.Digest, _ = domain.ComputeCharacterWorkContinuationReceiptV1Digest(tampered.Entries[0].PeerContinuations[0].Receipt)
	tampered.Digest, _ = domain.ComputeCharacterWorkContinuationLedgerV1Digest(tampered)
	if domain.ValidateCharacterWorkContinuationLedgerV1(tampered, boundary) == nil {
		t.Fatal("forged peer budget escaped global source binding")
	}
	encoded, _ := json.Marshal(result.Owners[0].Entry.PeerContinuations)
	if strings.Contains(string(encoded), `"entries"`) {
		t.Fatal("peer source recursively copied another ledger")
	}
}
