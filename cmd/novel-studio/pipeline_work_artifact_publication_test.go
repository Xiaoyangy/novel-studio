package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const publicationArtifactPaperID = "res_0000000000000081"
const publicationArtifactBody = "本人记录眼前情况，未核事项保持待核。"
const publicationArtifactSignScope = "仅签认本人写下的这条有限声明"

func publicationArtifactMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func publicationArtifactCopy[T any](t *testing.T, value T) T {
	t.Helper()
	raw, err := json.Marshal(value)
	publicationArtifactMust(t, err)
	var out T
	publicationArtifactMust(t, json.Unmarshal(raw, &out))
	return out
}

type publicationArtifactFixture struct {
	producer   string
	st         *store.Store
	generation domain.PlanningGenerationV2
	registry   domain.ObligationRegistryV2
	input      domain.CharacterActivationInputSet
	session    domain.CharacterActivationSession
	readiness  domain.CharacterReadinessContext
	owner      string
}

func newPublicationArtifactFixture(t *testing.T) *publicationArtifactFixture {
	t.Helper()
	_, input := testutil.CharacterActivationV3Inputs(t)
	owner := input.Observations[0].AgentID
	characters := []domain.Character{{Name: "甲", Role: "protagonist", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "柜台", CurrentGoal: "写下有限声明再自主签认", Pressure: "时间有限", KnownFacts: []string{"本人可以选择记录自己的声明"}, ResourceBalances: []domain.InitialCharacterResourceV2{{ResourceID: publicationArtifactPaperID, Name: "纸张原料", PerceivedName: "纸张", PerceivedLabel: "纸张原料", Unit: "张", PerceivedUnit: "张", ActualAmount: projectAllPhysicalNumber(2), Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: projectAllPhysicalNumber(2), EvidenceRefs: []string{"paper-source"}}, EvidenceRefs: []string{"paper-source"}}}}}}
	st := store.NewStore(t.TempDir())
	publicationArtifactMust(t, st.Init())
	publicationArtifactMust(t, st.Characters.Save(characters))
	publicationArtifactMust(t, st.CharacterAgents.SaveRegistry(input.Registry))
	publicationArtifactMust(t, st.Progress.Init("产物接受事务测试", 1))
	physical, err := domain.BuildWorldPhysicalStateFromInitialV2(characters, input.Registry)
	publicationArtifactMust(t, err)
	physical, err = domain.PrepareCharacterSelfChronologyStateV1(physical)
	publicationArtifactMust(t, err)
	g, obligations := projectAllCmdTestGenerationAndRegistry(t, 1)
	g.CharacterAgentProtocol, g.CharacterActivationPolicy, g.MaxCharacterActivationCycles = domain.CharacterAgentDecisionProtocolV2Version, domain.CharacterActivationCyclePolicyV3, 4
	g.PlanGroundingPolicy = domain.PlanGroundingPolicyV1
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	publicationArtifactMust(t, err)
	input.Stimulus.GenerationID = g.GenerationID
	input.Stimulus.PhysicalState = &physical
	input.Activation.GenerationID = g.GenerationID
	for i := range input.Memories {
		input.Memories[i].GenerationID = g.GenerationID
		input.Memories[i], err = domain.FinalizeCharacterAgentMemory(input.Memories[i])
		publicationArtifactMust(t, err)
	}
	input.Observations[0].GenerationID = g.GenerationID
	input.Observations[0].Location = "柜台"
	input.Observations[0].CurrentGoal = characters[0].InitialState.CurrentGoal
	input.Observations[0].Pressure = characters[0].InitialState.Pressure
	input.Observations[0].KnownFacts = []domain.CharacterAgentFact{{ID: "known-write", Kind: "known", Text: characters[0].InitialState.KnownFacts[0]}}
	readiness, err := domain.FinalizeCharacterReadinessContext(domain.CharacterReadinessContext{GenerationID: g.GenerationID, Chapter: 1, POVCharacter: "甲", ArcLastChapter: 1, BookLastChapter: 1, TargetWords: 2250, SoftOutline: domain.OutlineEntry{Chapter: 1, Title: "有限声明", CoreEvent: "本人记录并独立签认"}, HardContracts: []string{"不得把声明或签认升级成世界真相"}})
	publicationArtifactMust(t, err)
	session, err := domain.NewCharacterActivationSession(g.GenerationID, 1, readiness.Digest, physical, 0, 4)
	publicationArtifactMust(t, err)
	f := &publicationArtifactFixture{st: st, generation: g, registry: obligations, input: input, session: session, readiness: readiness, owner: owner}
	f.rebind(t)
	publicationArtifactMust(t, st.SaveCharacterReadinessContext(readiness))
	publicationArtifactMust(t, st.CreateCharacterActivationSession(session))
	_, foundation, err := store.CaptureProjectAllFoundationSnapshot(st.Dir())
	publicationArtifactMust(t, err)
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, GenerationID: g.GenerationID, BaseCanonChapter: g.BaseCanonChapter, BaseCanonRoot: g.BaseCanonRoot, BaseStateRoot: g.BaseStateRoot, StableOutlineRoot: g.StableOutlineRoot, PlanningDependencyRoot: g.PlanningDependencyRoot, RandomSeedContractRoot: g.RandomSeedContractRoot, FoundationSnapshotRoot: foundation, RAGSnapshotRoot: projectAllCmdTestDigest("artifact-fixture-rag"), CapturedAt: "2026-07-17T00:00:00Z"}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.ProjectedV2().CreateBuildingGeneration(g, source, obligations))
	return f
}

func (f *publicationArtifactFixture) rebind(t *testing.T) {
	t.Helper()
	input := &f.input
	policies := []string{domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, domain.CharacterSelfChronologyPolicyV1, domain.CharacterWorkContinuationPolicyV1, domain.CharacterArbitrationRoundSourcesPolicyV1, domain.CharacterActivationCyclePolicyV3, domain.CharacterWorkArtifactPolicyV1, domain.CharacterRevisionFeedbackPolicyV1, domain.PlanGroundingPolicyV1}
	if f.producer != "" {
		policies = append(policies[:len(policies)-1], domain.CharacterResourceObservationTimePolicyV1, domain.PlanGroundingPolicyV1)
	}
	previous := ""
	if len(f.session.CycleDigests) > 0 {
		previous = f.session.CycleDigests[len(f.session.CycleDigests)-1]
	}
	token, err := domain.CharacterActivationCycleSourceToken(f.session.GenerationID, 1, len(f.session.CycleDigests)+1, f.session.ChapterContextDigest, previous)
	publicationArtifactMust(t, err)
	input.Stimulus.Sources = append(policies, token)
	if f.producer != "" {
		input.Stimulus.Sources = append(input.Stimulus.Sources, "character-agent-protocol:"+f.producer)
	}
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(f.session)
	publicationArtifactMust(t, err)
	input.Stimulus.StoryClock.CurrentDay = f.session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	publicationArtifactMust(t, err)
	input.Stimulus.StoryClock = &clock
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	publicationArtifactMust(t, err)
	o := &input.Observations[0]
	o.Sources = append([]string(nil), policies[:len(policies)-1]...)
	o.Round = 1
	o.StimulusDigest = input.Stimulus.Digest
	o.CycleContext, err = domain.NewCharacterObservationCycleContext(f.session)
	publicationArtifactMust(t, err)
	o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(*input.Stimulus.PhysicalState, o.AgentID)
	publicationArtifactMust(t, err)
	o.ArtifactViews, err = domain.BuildCharacterArtifactViewsV1(*input.Stimulus.PhysicalState, o.AgentID)
	publicationArtifactMust(t, err)
	o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationV2(*input.Stimulus.PhysicalState, o.AgentID)
	publicationArtifactMust(t, err)
	o.MemoryRoot = input.Memories[0].MemoryRoot
	o.Memory = publicationArtifactCopy(t, input.Memories[0].Facts)
	for i := range o.Memory {
		o.Memory[i].SourceDigest = domain.CharacterSourceRefV2(o.AgentID, o.Memory[i].SourceDigest)
		o.Memory[i].KnowledgeRefs = domain.CharacterSourceRefsV2(o.AgentID, o.Memory[i].KnowledgeRefs)
	}
	*o, err = domain.FinalizeCharacterObservationPacket(*o)
	publicationArtifactMust(t, err)
	input.Activation.Entries[0].ObservationDigest = o.Digest
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	publicationArtifactMust(t, err)
	*input, err = domain.FinalizeCharacterActivationInputSet(*input)
	publicationArtifactMust(t, err)
}

func (f *publicationArtifactFixture) cycle(t *testing.T, sign bool) {
	t.Helper()
	producer := f.producer
	if producer == "" {
		producer = projectAllCmdTestDigest("artifact-protocol")
	}
	proofs, err := f.st.CharacterAgents.ForActivationCycle(f.session)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, proofs.PublishActivationInputs(f.input))
	view, err := f.st.PrepareCharacterArbitrationV3(f.session, nil, producer)
	publicationArtifactMust(t, err)
	o := f.input.Observations[0]
	taskID := "write"
	task := domain.CharacterSelfTaskV2{TaskID: taskID, Kind: "work", Action: "完成本人声明工序", ResourceIDs: []string{publicationArtifactPaperID}, ProgressTarget: projectAllPhysicalNumber(1), ProgressUnit: "minute", KnowledgeRefs: []string{"known-write"}}
	args := map[string]any{"location": o.Location, "current_goal": o.CurrentGoal, "pressure": o.Pressure, "available_options": []string{"执行本人决定", "等待"}, "decision": "执行本人决定", "decision_reason": "本人选择承担有限声明", "intended_action": "完成本人明确选择的一段工序", "action_duration": "一分钟", "knowledge_refs": []string{"known-write"}}
	id, version := "", ""
	if !sign {
		task.OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "record", Label: "有限声明", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: publicationArtifactPaperID, Amount: 1}}, Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "claim", Text: publicationArtifactBody, EpistemicKind: "self_statement", SourceRefs: []string{"known-write"}}}}}
	} else {
		taskID = "sign"
		task.TaskID = taskID
		id, version = o.ArtifactViews[0].ResourceID, o.ArtifactViews[0].VersionDigest
		task.ResourceIDs = []string{id}
		args["artifact_signs"] = []domain.CharacterArtifactSignIntentV1{{TaskID: taskID, ResourceID: id, VersionDigest: version, ClaimIDs: []string{"claim"}, Scope: publicationArtifactSignScope}}
	}
	args["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	raw, err := json.Marshal(args)
	publicationArtifactMust(t, err)
	submit, err := tools.NewSubmitCharacterActivationV3DecisionTool(f.st, f.session, o, view)
	publicationArtifactMust(t, err)
	_, err = submit.Execute(context.Background(), raw)
	publicationArtifactMust(t, err)
	scope, err := view.Sources(1)
	publicationArtifactMust(t, err)
	p := scope.EffectiveProposals()[0]
	start, end := f.session.CurrentDay, f.session.CurrentDay+1.0/1440
	execution := domain.CharacterSelfExecutionV2{TaskID: taskID, Status: "completed", StartDay: &start, EndDay: &end}
	resolution := map[string]any{"agent_id": p.AgentID, "character": p.Character, "proposal_digest": p.Digest, "decision": p.Decision, "intended_action": p.IntendedAction, "action_order": 1, "outcome": "success", "completion_state": "completed", "immediate_result": "本人工序确已完成", "state_after": "PRIVATE_AUTHOR_WORLD_TEXT", "post_state": map[string]any{"location": o.Location, "resource_updates": []any{}}, "butterfly_effects": []domain.DecisionButterflyEffect{{Effect: "本人保留这份有限声明", TransmissionPath: "本人实际操作", ArrivalChapter: 1, ProtagonistImpact: "之后可依据本人产物选择"}}}
	arbArgs := map[string]any{"time_window": f.input.Stimulus.TimeWindow, "story_time": domain.StoryTimeChapterSchedule{Chapter: 1, StartDay: start, EndDay: end}, "resolutions": []map[string]any{resolution}, "hard_contract_status": "feasible", "finalized": true}
	if !sign {
		execution.OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: "record", Status: "created", AtDay: end, ClaimIDs: []string{"claim"}, Complete: true}}
		arbArgs["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: publicationArtifactPaperID, Before: projectAllPhysicalNumber(2), Delta: projectAllPhysicalNumber(-1), After: projectAllPhysicalNumber(1), EvidenceRefs: []string{p.Digest}}}
		if f.producer != "" {
			settlements := arbArgs["resource_settlements"].([]domain.ResourceSettlementV2)
			settlements[0].StartDay, settlements[0].EndDay = &start, &start
		}
	} else {
		resolution["artifact_signatures"] = []domain.CharacterArtifactSignatureResultV1{{ResourceID: id, VersionDigest: version, ClaimIDs: []string{"claim"}, Scope: publicationArtifactSignScope, AtDay: end}}
	}
	resolution["self_executions"] = []domain.CharacterSelfExecutionV2{execution}
	raw, err = json.Marshal(arbArgs)
	publicationArtifactMust(t, err)
	tool, err := tools.NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, producer)
	publicationArtifactMust(t, err)
	_, err = tool.Execute(context.Background(), raw)
	publicationArtifactMust(t, err)
	cycle, err := view.FinalizeCycle(nil)
	publicationArtifactMust(t, err)
	_, err = f.st.AppendVerifiedCharacterActivationCycle(f.session.Digest, cycle)
	publicationArtifactMust(t, err)
	prefix, err := f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, 1)
	publicationArtifactMust(t, err)
	review, err := domain.NewCharacterReadinessReviewInputFromSteps(f.readiness, prefix.Session(), prefix.Steps(), projectAllCmdTestDigest("artifact-review"))
	publicationArtifactMust(t, err)
	verdict := testutil.ReadyVerdict(review)
	if !sign {
		verdict.Decision = "continue"
	}
	ready, err := domain.FinalizeCharacterReadinessReview(review, verdict)
	publicationArtifactMust(t, err)
	codec, err := domain.NewCharacterReadinessModelCodecV1(review)
	publicationArtifactMust(t, err)
	binding := codec.Binding()
	publicationArtifactMust(t, f.st.SaveVerifiedCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: review, Receipt: ready, ModelView: &binding}))
	_, err = f.st.ApplyVerifiedCharacterChapterReadiness(prefix.Session().Digest, ready)
	publicationArtifactMust(t, err)
	f.st = store.NewStore(f.st.Dir())
	prefix, err = f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, 1)
	publicationArtifactMust(t, err)
	f.session = prefix.Session()
	steps := prefix.Steps()
	step := steps[len(steps)-1]
	after := step.AfterState()
	f.input = step.Input()
	f.input.Stimulus.PhysicalState = &after
	if !sign {
		r := step.Cycle().Evidence.Arbitrations[0]
		text, err := domain.CharacterActivationPrivateOutcome(p, r.Resolutions[0], after, r)
		publicationArtifactMust(t, err)
		m := f.input.Memories[0]
		m.Facts = append(m.Facts, domain.CharacterAgentMemoryFact{ID: "actual-creation", Chapter: 1, Kind: "projected_decision", Text: text, SourceDigest: r.Digest})
		m, err = domain.FinalizeCharacterAgentMemory(m)
		publicationArtifactMust(t, err)
		f.input.Memories[0] = m
		f.rebind(t)
	}
}

func TestWorkArtifactV3PublicationBundlePromotionAndAcceptedMemory(t *testing.T) {
	f := newPublicationArtifactFixture(t)
	canonicalPaths := []string{filepath.Join("meta", "character_agents", "registry.json"), filepath.Join("meta", "character_agents", "memory", f.owner+".json")}
	canonicalBefore := map[string][]byte{}
	for _, path := range canonicalPaths {
		raw, err := os.ReadFile(filepath.Join(f.st.Dir(), path))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		canonicalBefore[path] = raw
	}
	assertNoCanon := func() {
		t.Helper()
		for _, path := range canonicalPaths {
			raw, err := os.ReadFile(filepath.Join(f.st.Dir(), path))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, canonicalBefore[path]) {
				t.Fatalf("unaccepted artifact changed exact canonical registry/memory file %s", path)
			}
		}
		memory, err := f.st.CharacterAgents.LoadCanonicalMemory(f.owner)
		publicationArtifactMust(t, err)
		if memory != nil && len(memory.Facts) > 0 {
			t.Fatal("unaccepted artifact changed formal character memory")
		}
	}
	assertNoCanon()
	f.cycle(t, false)
	f.cycle(t, true)
	assertNoCanon()
	evidence, err := f.st.CollectCharacterActivationChapterEvidence(f.session.GenerationID, 1)
	publicationArtifactMust(t, err)
	planning, err := domain.DeriveProjectedPlanningContextV2(f.generation, nil, f.registry, 1)
	publicationArtifactMust(t, err)
	token, err := domain.ProjectedPlanningContextSourceTokenV2(planning.ContextDigest)
	publicationArtifactMust(t, err)
	simulation, _, err := tools.PublishCharacterActivationSimulation(context.Background(), f.st, f.session.GenerationID, 1, []string{token})
	publicationArtifactMust(t, err)
	assertNoCanon()
	artifacts, outline := projectAllCmdTestArtifacts(t, f.session.GenerationID, 1)
	raw, _ := json.Marshal(artifacts.Plan)
	publicationArtifactMust(t, json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "主角", "甲")), artifacts.Plan))
	artifacts.WorldSimulation, artifacts.CharacterActivationEvidence = simulation, evidence
	plan := artifacts.Plan
	plan.Title, plan.Goal, plan.Conflict, plan.Hook = "有限声明", "甲记录并自主签认有限声明", "声明不能代替真相", "本人留下有边界的签认"
	plan.Contract.RequiredBeats = []string{"甲实际写下有限声明，之后独立签认这一版本"}
	plan.Contract.ForbiddenMoves = []string{"不得把签认升级为世界真相"}
	plan.Contract.ContinuityChecks = []string{"纸张只分配一次，签名只绑定确切版本"}
	plan.Contract.PayoffPoints = []string{"甲承担本人的声明范围"}
	plan.CausalSimulation.WorldSimulationID, plan.CausalSimulation.ProtagonistDecision = simulation.SimulationID, simulation.ProtagonistProjection.ChosenDecision
	plan.CausalSimulation.CausalBeats = []domain.CausalSimulationBeat{{Cause: "本人愿意留下有边界的记录", CharacterChoice: "甲记录并独立签认", WorldResponse: "材料分配形成实际文书与版本签名", StoryResult: "有限声明留存"}}
	plan.CausalSimulation.OutcomeShift = []string{"甲保留有版本绑定的本人签认"}
	for i := range plan.CausalSimulation.RenderCapacity.SceneUnits {
		unit := &plan.CausalSimulation.RenderCapacity.SceneUnits[i]
		unit.POVObjective = "完成本人工序"
		unit.ActiveOpposition = "避免超出所知范围"
		unit.Turn = "本人决定落为真实结果"
		unit.ExitConsequence = "本人保留明确结果"
		unit.ConcreteActionBeats = []string{"甲查看手边纸张", "甲完成本人声明工序", "甲确认有限范围"}
	}
	artifacts.PlanningContextDigest = planning.ContextDigest
	plan.CausalSimulation.ArcTransition = domain.ArcChapterTransitionContract{OutgoingConsequenceID: "artifact-end", OutgoingConsequenceText: "本人承担有限声明"}
	fact, err := domain.NewRAGFactReceipt(1, "artifact fixture", []string{"fixture"}, "no_material_v1", "", nil)
	publicationArtifactMust(t, err)
	artifacts.RAGFactReceipt = &fact
	craft := projectAllCmdTestNoMaterialCraftReceipt(t, f.session.GenerationID, 1, planning.ContextDigest)
	artifacts.CraftRecallReceipt = &craft
	plan.CausalSimulation.ContextSources = []string{token, fact.SourceToken(), domain.CraftRecallReceiptSourceTokenV2(craft)}
	grounding, err := domain.NewActivationPlanGroundingInput(*plan, *simulation, *evidence, projectAllCmdTestDigest("explicit-structural-fixture-review"))
	publicationArtifactMust(t, err)
	passed, err := domain.FinalizePlanGroundingReceipt(grounding, domain.PlanGroundingVerdict{Pass: true})
	publicationArtifactMust(t, err)
	plan.GroundingReview = &passed // Structural fixture; no claim of a model quality judgment.
	outline.Title, outline.CoreEvent, outline.Hook = plan.Title, plan.Goal, plan.Hook
	genesis, err := domain.DeriveProjectedChainGenesisV2(f.generation)
	publicationArtifactMust(t, err)
	bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(f.generation, outline, genesis, f.generation.BaseStateRoot, artifacts, f.registry)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, domain.ValidateProjectedChapterBundle(bundle))
	publicationArtifactMust(t, domain.ValidateGenerationCharacterProtocolV2(f.generation, bundle))
	publicationArtifactMust(t, f.st.ProjectedV2().SaveObligationRegistry(f.generation.GenerationID, nextRegistry))
	publicationArtifactMust(t, f.st.ProjectedV2().SaveProjectedChapterBundle(bundle))
	_, err = f.st.ProjectedV2().SealGeneration(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	active, cursor, err := f.st.ProjectedV2().ActivateSealedGeneration(f.generation.GenerationID, nil)
	publicationArtifactMust(t, err)
	promotion := projectAllCmdTestPromotion(t, bundle)
	_, err = f.st.ProjectedV2().Promote(*cursor, promotion)
	publicationArtifactMust(t, err)
	assertNoCanon()
	sealed, err := f.st.ProjectedV2().LoadSealedGeneration(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	current, err := f.st.ProjectedV2().LoadRealizationCursor()
	publicationArtifactMust(t, err)
	binding := &pipelineSealedRenderBinding{Active: *active, Cursor: *current, Generation: *sealed, Bundle: bundle, Promotion: promotion}
	body := "甲开始记录。\n\n甲把本人有限声明写在分配的一张纸上，随后独立签认这一版本。这并不保证未核事项已经为真。\n\n甲停笔收好。从开始记录到停笔收好，一共用了两分钟。"
	projectAllCmdTestWriteFile(t, filepath.Join(f.st.Dir(), "chapters/01.md"), body)
	bodySHA, err := pipelineRequiredFileSHA(f.st.Dir(), "chapters/01.md")
	publicationArtifactMust(t, err)
	commit, err := f.st.Checkpoints.Append(domain.ChapterScope(1), "commit", "chapters/01.md", bodySHA)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, f.st.Progress.MarkChapterComplete(1, len([]rune(body)), "choice", "main"))
	clock, err := derivePipelineSealedStoryClockEvidence(&bundle, body)
	publicationArtifactMust(t, err)
	match := &pipelineSealedActualDeltaMatch{ActualDelta: bundle.ProjectedDelta, ProjectionMatch: true, Complete: true, ObligationsSatisfied: bundle.ObligationsConsumed, StoryClockEvidence: clock}
	progress, err := f.st.Progress.Load()
	publicationArtifactMust(t, err)
	canon, err := pipelineProjectAllLiveCanonRoot(f.st.Dir(), progress)
	publicationArtifactMust(t, err)
	for _, kind := range []string{"text", "version", "signature"} {
		t.Run("tamper_"+kind, func(t *testing.T) {
			bad := publicationArtifactCopy(t, bundle)
			var artifact *domain.CharacterWorkArtifactV1
			for i := range bad.ChapterWorldSimulation.PhysicalState.Resources {
				if value := bad.ChapterWorldSimulation.PhysicalState.Resources[i].Artifact; value != nil {
					artifact = value
					break
				}
			}
			if artifact == nil || len(artifact.Signatures) == 0 {
				t.Fatal("fixture lacks actual signed artifact")
			}
			switch kind {
			case "text":
				artifact.Claims[0].Text = "伪造未发生的文书正文"
			case "version":
				artifact.VersionDigest = projectAllCmdTestDigest("foreign-version")
			case "signature":
				artifact.Signatures[0].Scope = "伪造签认范围"
			}
			bad.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bad)
			publicationArtifactMust(t, err)
			if err := domain.ValidateProjectedChapterBundle(bad); err == nil {
				t.Fatal("outer hash masked artifact tampering")
			}
			badBinding := *binding
			badBinding.Bundle = bad
			before, err := store.DirectoryContentRoot(f.st.Dir())
			publicationArtifactMust(t, err)
			if _, err := acceptPipelineSealedRenderOutcome(f.st, &badBinding, commit, bodySHA, canon, match); err == nil {
				t.Fatal("accepted artifact tampering")
			}
			after, err := store.DirectoryContentRoot(f.st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("rejected acceptance wrote canon/outcome")
			}
			assertNoCanon()
		})
	}
	assertNoCanon()
	outcome, err := acceptPipelineSealedRenderOutcome(f.st, binding, commit, bodySHA, canon, match)
	publicationArtifactMust(t, err)
	memory, err := f.st.CharacterAgents.LoadCanonicalMemory(f.owner)
	publicationArtifactMust(t, err)
	if memory == nil || memory.LastAcceptedChapter != 1 {
		t.Fatal("accepted artifact failed to publish owner memory")
	}
	memoryJSON, _ := json.Marshal(memory)
	if !strings.Contains(string(memoryJSON), publicationArtifactBody) || !strings.Contains(string(memoryJSON), publicationArtifactSignScope) || strings.Contains(string(memoryJSON), "PRIVATE_AUTHOR_WORLD_TEXT") {
		t.Fatal("accepted artifact memory lost actual claim/signature or leaked author prose")
	}
	before, err := store.DirectoryContentRoot(f.st.Dir())
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, applyPipelineAcceptedCharacterMemoryPublication(store.NewStore(f.st.Dir()), bundle, *outcome))
	after, err := store.DirectoryContentRoot(f.st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("accepted artifact memory replay was not idempotent")
	}
}
