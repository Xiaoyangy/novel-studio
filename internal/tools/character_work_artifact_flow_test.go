package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

const artifactFlowPaperID = "res_0000000000000071"

var artifactFlowProtocol = "sha256:" + strings.Repeat("d", 64)
var artifactFlowPolicies = []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3, domain.CharacterWorkArtifactPolicyV1}

func artifactFlowMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func artifactFlowCopy[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	artifactFlowMust(t, err)
	var result T
	artifactFlowMust(t, json.Unmarshal(raw, &result))
	return result
}
func artifactFlowNumber(value float64) *float64 { return &value }

type artifactFlowFixture struct {
	st           *store.Store
	context      domain.CharacterReadinessContext
	input        domain.CharacterActivationInputSet
	session      domain.CharacterActivationSession
	author, peer string
}

func newArtifactFlowFixture(t *testing.T, prepare ...func(*domain.CharacterActivationInputSet)) *artifactFlowFixture {
	t.Helper()
	_, input := testutil.CharacterActivationV3Inputs(t)
	author := input.Observations[0].AgentID
	registry, record, err := input.Registry.UpsertCharacter("乙", nil, "core", 1, "")
	artifactFlowMust(t, err)
	input.Registry, err = domain.FinalizeCharacterAgentRegistry(registry)
	artifactFlowMust(t, err)
	physical := artifactFlowCopy(t, *input.Stimulus.PhysicalState)
	physical.Resources = append(physical.Resources, domain.WorldResourceBalanceV2{ResourceID: artifactFlowPaperID, Name: "纸张原料", Unit: "张", ActualAmount: artifactFlowNumber(5)})
	physical.Actors[0].Location = "柜台"
	physical.Actors[0].Resources = append(physical.Actors[0].Resources, domain.CharacterResourceHoldingV2{ResourceID: artifactFlowPaperID, PerceivedName: "纸张", PerceivedLabel: "纸张原料", PerceivedUnit: "张", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: artifactFlowNumber(5), EvidenceRefs: []string{"known-paper"}}, EvidenceRefs: []string{"known-paper"}})
	physical.Actors = append(physical.Actors, domain.CharacterPhysicalStateV2{AgentID: record.AgentID, Character: record.Character, Location: "柜台", Resources: []domain.CharacterResourceHoldingV2{}})
	physical, err = domain.PrepareCharacterSelfChronologyStateV1(physical)
	artifactFlowMust(t, err)
	input.Stimulus.PhysicalState = &physical
	peerObservation := artifactFlowCopy(t, input.Observations[0])
	peerObservation.AgentID, peerObservation.Character = record.AgentID, record.Character
	peerObservation.Location = "柜台"
	peerObservation.KnownFacts = []domain.CharacterAgentFact{{ID: "peer-known", Kind: "known", Text: "我可在柜台选择查看实际交来的文书"}}
	input.Observations = append(input.Observations, peerObservation)
	input.Observations[0].KnownFacts = []domain.CharacterAgentFact{{ID: "author-known", Kind: "known", Text: "我可以记录本人的声明并分配手边纸张"}, {ID: "author-private", Kind: "known", Text: "PRIVATE_AUTHOR_UNWRITTEN"}}
	peerMemory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: record.AgentID, Character: record.Character, GenerationID: input.Stimulus.GenerationID, State: "projected"})
	artifactFlowMust(t, err)
	input.Memories = append(input.Memories, peerMemory)
	input.Activation.RegistryRoot = input.Registry.RegistryRoot
	input.Activation.Entries = append(input.Activation.Entries, domain.CharacterAgentActivationEntry{AgentID: record.AgentID, Character: record.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"fixture_peer"}})
	for _, configure := range prepare {
		configure(&input)
	}
	physical, err = domain.FinalizeWorldPhysicalStateV2(*input.Stimulus.PhysicalState)
	artifactFlowMust(t, err)
	input.Stimulus.PhysicalState = &physical
	readiness, err := domain.FinalizeCharacterReadinessContext(domain.CharacterReadinessContext{GenerationID: input.Stimulus.GenerationID, Chapter: 1, POVCharacter: "甲", ArcLastChapter: 1, BookLastChapter: 2, TargetWords: 2250, SoftOutline: domain.OutlineEntry{Chapter: 1, Title: "文书工作", CoreEvent: "独立决定与实际产物"}, HardContracts: []string{"只使用真实发生的产物和实际读签结果"}})
	artifactFlowMust(t, err)
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, 1, readiness.Digest, physical, input.Stimulus.StoryClock.CurrentDay, 8)
	artifactFlowMust(t, err)
	f := &artifactFlowFixture{st: store.NewStore(t.TempDir()), context: readiness, input: input, session: session, author: author, peer: record.AgentID}
	f.input = artifactFlowRebind(t, f.input, session)
	artifactFlowMust(t, f.st.Init())
	artifactFlowMust(t, f.st.SaveCharacterReadinessContext(readiness))
	artifactFlowMust(t, f.st.CreateCharacterActivationSession(session))
	return f
}

// Rebuild the observation only from the committed physical state and existing
// owner memories. No future claim, access, signature or post-state is injected.
func artifactFlowRebind(t *testing.T, input domain.CharacterActivationInputSet, session domain.CharacterActivationSession) domain.CharacterActivationInputSet {
	t.Helper()
	input = artifactFlowCopy(t, input)
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(session.CycleDigests)+1, session.ChapterContextDigest, previous)
	artifactFlowMust(t, err)
	policies := append([]string(nil), artifactFlowPolicies...)
	if domain.HasCharacterSurfaceInspectionPolicyV1(input.Stimulus.Sources) {
		policies = append(policies, domain.CharacterOperationalAvailabilityPolicyV1, domain.CharacterSurfaceInspectionPolicyV1)
	}
	if domain.HasCharacterIncomingMaterialReadPolicyV1(input.Stimulus.Sources) {
		policies = append(policies, domain.CharacterIncomingMaterialReadPolicyV1)
	}
	input.Stimulus.Sources = append(append([]string(nil), policies...), token)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	artifactFlowMust(t, err)
	input.Stimulus.StoryClock.CurrentDay = session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	artifactFlowMust(t, err)
	input.Stimulus.StoryClock = &clock
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	artifactFlowMust(t, err)
	cycleContext, err := domain.NewCharacterObservationCycleContext(session)
	artifactFlowMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.Round = 1
		o.ConflictFeedback = nil
		o.Sources = append([]string(nil), policies...)
		o.CycleContext = cycleContext
		o.StimulusDigest = input.Stimulus.Digest
		for _, actor := range input.Stimulus.PhysicalState.Actors {
			if actor.AgentID == o.AgentID {
				o.Location = actor.Location
			}
		}
		o.ResourceViews, err = domain.BuildCharacterResourceViewsForSourcesV2(*input.Stimulus.PhysicalState, o.AgentID, policies)
		artifactFlowMust(t, err)
		o.ArtifactViews, err = domain.BuildCharacterArtifactViewsV1(*input.Stimulus.PhysicalState, o.AgentID)
		artifactFlowMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(*input.Stimulus.PhysicalState, o.AgentID)
		artifactFlowMust(t, err)
		if domain.HasCharacterOperationalAvailabilityPolicyV1(policies) {
			o.OperationalObservations, err = domain.BuildCharacterOperationalObservationsV1(*input.Stimulus.PhysicalState, o.AgentID)
			artifactFlowMust(t, err)
		}
		for _, memory := range input.Memories {
			if memory.AgentID == o.AgentID {
				o.MemoryRoot = memory.MemoryRoot
				o.Memory = artifactFlowCopy(t, memory.Facts)
				for j := range o.Memory {
					o.Memory[j].SourceDigest = domain.CharacterSourceRefV2(o.AgentID, o.Memory[j].SourceDigest)
					o.Memory[j].KnowledgeRefs = domain.CharacterSourceRefsV2(o.AgentID, o.Memory[j].KnowledgeRefs)
				}
			}
		}
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		artifactFlowMust(t, err)
		for j := range input.Activation.Entries {
			entry := &input.Activation.Entries[j]
			if entry.AgentID == o.AgentID {
				entry.State = domain.CharacterAgentActive
				entry.ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	artifactFlowMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	artifactFlowMust(t, err)
	return input
}

func (f *artifactFlowFixture) observation(t *testing.T, owner string) domain.CharacterObservationPacket {
	t.Helper()
	for _, o := range f.input.Observations {
		if o.AgentID == owner {
			return o
		}
	}
	t.Fatal("missing owner observation")
	return domain.CharacterObservationPacket{}
}
func (f *artifactFlowFixture) prepare(t *testing.T) *store.CharacterArbitrationV3 {
	t.Helper()
	proofs, err := f.st.CharacterAgents.ForActivationCycle(f.session)
	artifactFlowMust(t, err)
	artifactFlowMust(t, proofs.PublishActivationInputs(f.input))
	view, err := f.st.PrepareCharacterArbitrationV3(f.session, nil, artifactFlowProtocol)
	artifactFlowMust(t, err)
	return view
}

func artifactFlowSubmitArgs(o domain.CharacterObservationPacket, taskID string) map[string]any {
	ref := o.KnownFacts[0].ID
	return map[string]any{"location": o.Location, "current_goal": o.CurrentGoal, "pressure": o.Pressure, "available_options": []string{"执行本步", "等待"}, "decision": "执行本步", "decision_reason": "本人根据已知条件选择", "intended_action": "完成本人明确声明的本步工序", "action_duration": "一分钟", "knowledge_refs": []string{ref}, "self_tasks": []domain.CharacterSelfTaskV2{{TaskID: taskID, Kind: "work", Action: "本人本步文书工序", ProgressTarget: artifactFlowNumber(1), ProgressUnit: "minute", KnowledgeRefs: []string{ref}}}}
}

func (f *artifactFlowFixture) submit(t *testing.T, view *store.CharacterArbitrationV3, owner string, args map[string]any) {
	t.Helper()
	tool, err := NewSubmitCharacterActivationV3DecisionTool(f.st, f.session, f.observation(t, owner), view)
	artifactFlowMust(t, err)
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
}

func (f *artifactFlowFixture) rejectSubmit(t *testing.T, view *store.CharacterArbitrationV3, owner string, args map[string]any) {
	t.Helper()
	tool, err := NewSubmitCharacterActivationV3DecisionTool(f.st, f.session, f.observation(t, owner), view)
	artifactFlowMust(t, err)
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, f.st.Dir())
	if _, err := tool.Execute(context.Background(), raw); err == nil {
		t.Fatal("unauthorized artifact intent passed real Submit")
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, f.st.Dir())) {
		t.Fatal("rejected artifact intent wrote proof/partial/state")
	}
}

func artifactFlowArbiterArgs(scope domain.VerifiedCharacterArbitrationSourcesV1) map[string]any {
	input := scope.Input()
	start, end := input.Stimulus.StoryClock.CurrentDay, input.Stimulus.StoryClock.CurrentDay+1.0/1440
	resolutions := []map[string]any{}
	for i, p := range scope.EffectiveProposals() {
		resolutions = append(resolutions, map[string]any{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction, "action_order": i + 1, "outcome": "success", "completion_state": "completed", "immediate_result": "完成本人实际声明的本步工序", "state_after": "AUTHOR_PRIVATE_STATE_NOT_KNOWLEDGE", "post_state": map[string]any{"location": p.Location, "resource_updates": []any{}}, "self_executions": []domain.CharacterSelfExecutionV2{{TaskID: p.SelfTasks[0].TaskID, Status: "completed", StartDay: &start, EndDay: &end}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "本步结果影响之后的独立选择", TransmissionPath: "本人实际工序", ArrivalChapter: p.Chapter, ProtagonistImpact: "之后可以依据真实结果选择"}}})
	}
	return map[string]any{"time_window": input.Stimulus.TimeWindow, "story_time": domain.StoryTimeChapterSchedule{Chapter: input.Stimulus.Chapter, StartDay: start, EndDay: end}, "resolutions": resolutions, "hard_contract_status": "feasible", "finalized": true, "resource_settlements": []domain.ResourceSettlementV2{}}
}

func artifactFlowResolution(t *testing.T, args map[string]any, owner string) map[string]any {
	t.Helper()
	for _, r := range args["resolutions"].([]map[string]any) {
		if r["agent_id"] == owner {
			return r
		}
	}
	t.Fatal("missing resolution")
	return nil
}
func artifactFlowProposal(t *testing.T, scope domain.VerifiedCharacterArbitrationSourcesV1, owner string) domain.CharacterDecisionProposal {
	t.Helper()
	for _, p := range scope.EffectiveProposals() {
		if p.AgentID == owner {
			return p
		}
	}
	t.Fatal("missing source proposal")
	return domain.CharacterDecisionProposal{}
}

func (f *artifactFlowFixture) rejectArbitration(t *testing.T, tool *ResolveChapterWorldTool, args map[string]any) {
	t.Helper()
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, f.st.Dir())
	if _, err := tool.Execute(context.Background(), raw); err == nil {
		t.Fatal("invalid artifact result passed real Arbiter")
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, f.st.Dir())) {
		t.Fatal("failed artifact kernel wrote receipt/stock/checkpoint")
	}
}

func (f *artifactFlowFixture) commit(t *testing.T, view *store.CharacterArbitrationV3, tool *ResolveChapterWorldTool, args map[string]any) domain.VerifiedCharacterActivationStep {
	t.Helper()
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, f.st.Dir())
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, f.st.Dir())) {
		t.Fatal("identical paid-result replay changed artifact/stock")
	}
	cycle, err := view.FinalizeCycle(nil)
	artifactFlowMust(t, err)
	if cycle.Version != domain.CharacterActivationCycleV3Version {
		t.Fatal("artifact flow downgraded cycle")
	}
	_, err = f.st.AppendVerifiedCharacterActivationCycle(f.session.Digest, cycle)
	artifactFlowMust(t, err)
	prefix, err := f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, f.session.Chapter)
	artifactFlowMust(t, err)
	review, err := domain.NewCharacterReadinessReviewInputFromSteps(f.context, prefix.Session(), prefix.Steps(), artifactFlowProtocol)
	artifactFlowMust(t, err)
	verdict := testutil.ReadyVerdict(review)
	verdict.Decision = "continue"
	receipt, err := domain.FinalizeCharacterReadinessReview(review, verdict)
	artifactFlowMust(t, err)
	codec, err := domain.NewCharacterReadinessModelCodecV1(review)
	artifactFlowMust(t, err)
	binding := codec.Binding()
	artifactFlowMust(t, f.st.SaveVerifiedCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: review, Receipt: receipt, ModelView: &binding}))
	_, err = f.st.ApplyVerifiedCharacterChapterReadiness(prefix.Session().Digest, receipt)
	artifactFlowMust(t, err)
	f.st = store.NewStore(f.st.Dir()) // Real process-local cache/authority is discarded.
	prefix, err = f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, f.session.Chapter)
	artifactFlowMust(t, err)
	steps := prefix.Steps()
	step := steps[len(steps)-1]
	after := step.AfterState()
	next := step.Input()
	next.Stimulus.PhysicalState = &after
	r := step.Cycle().Evidence.Arbitrations[len(step.Cycle().Evidence.Arbitrations)-1]
	for _, p := range step.EffectiveProposals() {
		for _, resolution := range r.Resolutions {
			if resolution.AgentID != p.AgentID {
				continue
			}
			text, err := domain.CharacterActivationPrivateOutcome(p, resolution, after, r)
			artifactFlowMust(t, err)
			for i, memory := range next.Memories {
				if memory.AgentID != p.AgentID {
					continue
				}
				memory.Facts = append(memory.Facts, domain.CharacterAgentMemoryFact{ID: fmt.Sprintf("actual-%d-%s", cycle.Index, p.AgentID), Chapter: p.Chapter, Kind: "projected_decision", Text: text, SourceDigest: r.Digest, KnowledgeRefs: p.KnowledgeRefs})
				memory, err = domain.FinalizeCharacterAgentMemory(memory)
				artifactFlowMust(t, err)
				next.Memories[i] = memory
			}
		}
	}
	f.session = prefix.Session()
	f.input = artifactFlowRebind(t, next, f.session)
	return step
}

func artifactFlowResource(t *testing.T, state domain.WorldPhysicalStateV2, id string) domain.WorldResourceBalanceV2 {
	t.Helper()
	for _, r := range state.Resources {
		if r.ResourceID == id {
			return r
		}
	}
	t.Fatal("expected artifact/stock absent")
	return domain.WorldResourceBalanceV2{}
}

func TestWorkArtifactFlowRealToolsCreateGrantReadSignAndVersionUpdate(t *testing.T) {
	f := newArtifactFlowFixture(t)
	view := f.prepare(t)
	authorArgs := artifactFlowSubmitArgs(f.observation(t, f.author), "compose_record")
	writerTask := authorArgs["self_tasks"].([]domain.CharacterSelfTaskV2)
	writerTask[0].ResourceIDs = []string{artifactFlowPaperID}
	writerTask[0].OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "record", Label: "有限工作记录", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: artifactFlowPaperID, Amount: 1}}, Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "statement", Text: "这是本人的有限声明，并非独立世界证据。", EpistemicKind: "self_statement", SourceRefs: authorArgs["knowledge_refs"].([]string)}}}}
	authorArgs["self_tasks"] = writerTask
	f.submit(t, view, f.author, authorArgs)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "wait_create"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	args := artifactFlowArbiterArgs(scope)
	p := artifactFlowProposal(t, scope, f.author)
	end := args["story_time"].(domain.StoryTimeChapterSchedule).EndDay
	resolution := artifactFlowResolution(t, args, f.author)
	executions := resolution["self_executions"].([]domain.CharacterSelfExecutionV2)
	executions[0].OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: "record", Status: "created", AtDay: end, ClaimIDs: []string{"statement"}, Complete: true}}
	resolution["self_executions"] = executions
	settlement := domain.ResourceSettlementV2{ResourceID: artifactFlowPaperID, Before: artifactFlowNumber(5), Delta: artifactFlowNumber(-1), After: artifactFlowNumber(4), EvidenceRefs: []string{p.Digest}}
	args["resource_settlements"] = []domain.ResourceSettlementV2{settlement}
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	bad := artifactFlowCopy(t, args)
	bad["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: artifactFlowPaperID, Before: artifactFlowNumber(5), Delta: artifactFlowNumber(-2), After: artifactFlowNumber(3), EvidenceRefs: []string{p.Digest}}}
	f.rejectArbitration(t, tool, bad)
	bad = artifactFlowCopy(t, args)
	bad["resource_settlements"] = []domain.ResourceSettlementV2{settlement, settlement}
	f.rejectArbitration(t, tool, bad)
	step := f.commit(t, view, tool, args)
	id := domain.CharacterWorkArtifactResourceIDV1(f.session.GenerationID, f.author, "compose_record", "record")
	artifact := artifactFlowResource(t, step.AfterState(), id).Artifact
	if artifact == nil || artifact.Revision != 1 || artifact.Placement.Location != "柜台" || artifact.Placement.CustodianAgentID != f.author || *artifactFlowResource(t, step.AfterState(), artifactFlowPaperID).ActualAmount != 4 {
		t.Fatal("actual creation/custody/one-time material settlement missing")
	}
	version := artifact.VersionDigest
	for _, original := range step.EffectiveProposals() {
		if original.AgentID == f.author && (original.SelfTasks[0].OutputRequests[0].ResourceID != "" || len(original.SelfTasks[0].OutputRequests[0].Claims[0].LineageRoots) != 0) {
			t.Fatal("Host materialization rewrote the original choice with derived identity/lineage")
		}
	}
	authorObs := f.observation(t, f.author)
	if len(authorObs.ArtifactViews) != 1 || authorObs.ArtifactViews[0].ResourceID != id || authorObs.ArtifactViews[0].Placement == nil || authorObs.ArtifactViews[0].Placement.Location != "柜台" || len(authorObs.ArtifactViews[0].Claims) != 1 {
		t.Fatal("restart forgot author's real artifact")
	}
	peerObs := f.observation(t, f.peer)
	if len(peerObs.ArtifactViews) != 0 {
		t.Fatal("peer learned artifact before actual access")
	}
	// This test deliberately drives each subsequent choice through fresh
	// Submit + V3 arbitration, never inserts a desired physical post-state.
	artifactFlowContinueThroughAccessReadSignUpdate(t, f, id, version)
	if len(f.session.CycleDigests) != 5 {
		t.Fatal("real Store prefix did not retain all five actual cycles")
	}
	if simulation, err := f.st.LoadChapterWorldSimulation(1); err != nil || simulation != nil {
		t.Fatalf("cycle tool incorrectly published an accepted chapter/simulation: %v", err)
	}
}

func artifactFlowContinueThroughAccessReadSignUpdate(t *testing.T, f *artifactFlowFixture, id, version string) {
	t.Helper()
	// First establish an independently chosen, actually adjudicated shared
	// access grant; no recipient receives the document contents in this cycle.
	view := f.prepare(t)
	bad := artifactFlowSubmitArgs(f.observation(t, f.peer), "unauthorized_read")
	bad["artifact_reads"] = []map[string]any{{"task_id": "unauthorized_read", "resource_id": id, "version_digest": version, "claim_ids": []string{}}}
	bad["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = []string{id}
	f.rejectSubmit(t, view, f.peer, bad)
	authorArgs := artifactFlowSubmitArgs(f.observation(t, f.author), "show_record")
	authorArgs["artifact_access"] = []domain.CharacterArtifactAccessIntentV1{{ResourceID: id, VersionDigest: version, ToCharacter: "乙", Access: "shared"}}
	f.submit(t, view, f.author, authorArgs)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "receive_access"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	args := artifactFlowArbiterArgs(scope)
	author := artifactFlowProposal(t, scope, f.author)
	artifactFlowResolution(t, args, f.author)["post_state"] = map[string]any{"location": "柜台", "resource_updates": []map[string]any{{"resource_id": id, "access": "shared", "evidence_refs": []string{author.Digest}}}}
	artifactFlowResolution(t, args, f.peer)["post_state"] = map[string]any{"location": "柜台", "resource_updates": []map[string]any{{"resource_id": id, "access": "shared", "perception": domain.ResourcePerceptionV2{Kind: "unknown", AsOfChapter: 1}, "evidence_refs": []string{author.Digest}}}}
	args["resource_deliveries"] = []domain.ResourceDeliveryV2{{ResourceID: id, ArtifactVersionDigest: version, FromAgentID: f.author, ToAgentID: f.peer, SourceProposalDigest: author.Digest, Access: "shared", ReceivedFields: []string{}, EvidenceRefs: []string{author.Digest}}}
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	f.commit(t, view, tool, args)
	peer := f.observation(t, f.peer)
	if len(peer.ArtifactViews) != 1 || peer.ArtifactViews[0].KnowledgeKind != "unread" || len(peer.ArtifactViews[0].Claims) != 0 {
		t.Fatal("delivery became reading")
	}
	// The unread actor cannot sign. Only explicit reading can yield claim facts.
	view = f.prepare(t)
	bad = artifactFlowSubmitArgs(f.observation(t, f.peer), "unread_sign")
	bad["artifact_signs"] = []map[string]any{{"task_id": "unread_sign", "resource_id": id, "version_digest": version, "claim_ids": []string{"statement"}, "scope": "仅签这一条"}}
	bad["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = []string{id}
	f.rejectSubmit(t, view, f.peer, bad)
	readerArgs := artifactFlowSubmitArgs(f.observation(t, f.peer), "read_record")
	readerArgs["artifact_reads"] = []map[string]any{{"task_id": "read_record", "resource_id": id, "version_digest": version, "claim_ids": []string{}}}
	readerArgs["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = []string{id}
	f.submit(t, view, f.peer, readerArgs)
	f.submit(t, view, f.author, artifactFlowSubmitArgs(f.observation(t, f.author), "wait_read"))
	scope, err = view.Sources(1)
	artifactFlowMust(t, err)
	args = artifactFlowArbiterArgs(scope)
	end := args["story_time"].(domain.StoryTimeChapterSchedule).EndDay
	artifactFlowResolution(t, args, f.peer)["artifact_read_results"] = []domain.CharacterArtifactReadResultV1{{ResourceID: id, VersionDigest: version, ClaimIDs: []string{"statement"}, AtDay: end}}
	tool, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	f.commit(t, view, tool, args)
	peer = f.observation(t, f.peer)
	if len(peer.ArtifactViews) != 1 || peer.ArtifactViews[0].KnowledgeKind != "read" || len(peer.ArtifactViews[0].Claims) != 1 || !strings.Contains(peer.ArtifactViews[0].Claims[0].Text, "本人") || len(peer.ArtifactViews[0].Signatures) != 0 {
		t.Fatal("real read did not preserve exact version or invented a signature")
	}
	peerJSON, _ := json.Marshal(peer)
	if strings.Contains(string(peerJSON), "PRIVATE_AUTHOR_UNWRITTEN") || strings.Contains(string(peerJSON), "AUTHOR_PRIVATE_STATE_NOT_KNOWLEDGE") {
		t.Fatal("peer observation/private memory leaked author context")
	}
	// Signing is a later independent choice, not fabricated as part of reading.
	view = f.prepare(t)
	signerArgs := artifactFlowSubmitArgs(f.observation(t, f.peer), "sign_record")
	signerArgs["artifact_signs"] = []map[string]any{{"task_id": "sign_record", "resource_id": id, "version_digest": version, "claim_ids": []string{"statement"}, "scope": "仅确认本人读到的这条有限声明"}}
	signerArgs["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = []string{id}
	f.submit(t, view, f.peer, signerArgs)
	f.submit(t, view, f.author, artifactFlowSubmitArgs(f.observation(t, f.author), "wait_signature"))
	scope, err = view.Sources(1)
	artifactFlowMust(t, err)
	args = artifactFlowArbiterArgs(scope)
	end = args["story_time"].(domain.StoryTimeChapterSchedule).EndDay
	artifactFlowResolution(t, args, f.peer)["artifact_signatures"] = []domain.CharacterArtifactSignatureResultV1{{ResourceID: id, VersionDigest: version, ClaimIDs: []string{"statement"}, Scope: "仅确认本人读到的这条有限声明", AtDay: end}}
	tool, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	f.commit(t, view, tool, args)
	peer = f.observation(t, f.peer)
	if len(peer.ArtifactViews) != 1 || len(peer.ArtifactViews[0].Signatures) != 1 || peer.ArtifactViews[0].Signatures[0].SignerAgentID != f.peer || peer.ArtifactViews[0].Signatures[0].VersionDigest != version {
		t.Fatal("owner forgot actual signature after restart")
	}
	peerMemoryJSON, _ := json.Marshal(peer.Memory)
	if !strings.Contains(string(peerMemoryJSON), "仅确认本人读到的这条有限声明") {
		t.Fatal("private outcome/memory forgot the actual owner signature scope")
	}
	authorMemoryJSON, _ := json.Marshal(f.observation(t, f.author).Memory)
	if strings.Contains(string(authorMemoryJSON), "仅确认本人读到的这条有限声明") {
		t.Fatal("global signature became another actor's unobserved private memory")
	}
	// Updating consumes no new paper and does not rebind the peer's old read or signature.
	view = f.prepare(t)
	update := artifactFlowSubmitArgs(f.observation(t, f.author), "amend_record")
	tasks := update["self_tasks"].([]domain.CharacterSelfTaskV2)
	tasks[0].ResourceIDs = []string{id}
	tasks[0].OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "record", ResourceID: id, ExpectedVersionDigest: version, Label: "有限工作记录", Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "statement", Text: "新版补充仍然只是本人有限声明。", EpistemicKind: "self_statement", SourceRefs: update["knowledge_refs"].([]string)}}}}
	update["self_tasks"] = tasks
	bad = artifactFlowCopy(t, update)
	badOutputs := artifactFlowCopy(t, tasks)
	badOutputs[0].OutputRequests[0].MaterialInputs = []domain.CharacterWorkMaterialInputV1{{ResourceID: artifactFlowPaperID, Amount: 1}}
	bad["self_tasks"] = badOutputs
	f.rejectSubmit(t, view, f.author, bad)
	f.submit(t, view, f.author, update)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "wait_amend"))
	scope, err = view.Sources(1)
	artifactFlowMust(t, err)
	args = artifactFlowArbiterArgs(scope)
	end = args["story_time"].(domain.StoryTimeChapterSchedule).EndDay
	resolution := artifactFlowResolution(t, args, f.author)
	executions := resolution["self_executions"].([]domain.CharacterSelfExecutionV2)
	executions[0].OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: "record", Status: "updated", AtDay: end, ClaimIDs: []string{"statement"}, Complete: true}}
	resolution["self_executions"] = executions
	tool, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	step := f.commit(t, view, tool, args)
	a := artifactFlowResource(t, step.AfterState(), id).Artifact
	if a.Revision != 2 || a.PreviousVersionDigest != version || a.VersionDigest == version || len(a.Signatures) != 1 || a.Signatures[0].VersionDigest != version || *artifactFlowResource(t, step.AfterState(), artifactFlowPaperID).ActualAmount != 4 {
		t.Fatal("update lost identity/CAS/signature version or recharged material")
	}
	peer = f.observation(t, f.peer)
	oldFound, newFound := false, false
	for _, view := range peer.ArtifactViews {
		if view.VersionDigest == version {
			oldFound = len(view.Signatures) == 1 && len(view.Claims) == 1
		}
		if view.VersionDigest == a.VersionDigest {
			newFound = view.KnowledgeKind == "unread" && len(view.Claims) == 0 && len(view.Signatures) == 0
		}
	}
	if !oldFound || !newFound {
		t.Fatal("new content inherited old read/signature or old knowledge vanished")
	}
	peerJSON, _ = json.Marshal(peer)
	if strings.Contains(string(peerJSON), "新版补充仍然只是本人有限声明") {
		t.Fatal("new author text leaked through peer memory after update")
	}
}
