package tools

import (
	"bytes"
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

const countFlowBatch = "res_0000000000000082"
const countFlowMechanism = "M_COUNT_BATCH"

func countFlowFixture(t *testing.T, known bool) *artifactFlowFixture {
	t.Helper()
	f := newArtifactFlowFixture(t, func(input *domain.CharacterActivationInputSet) {
		var actual *float64
		if known {
			actual = artifactFlowNumber(2)
		}
		// One resource identifies a declared physical batch containing TWO
		// original documents. Neither ID cardinality nor a manifest supplies 2.
		input.Stimulus.PhysicalState.Resources = append(input.Stimulus.PhysicalState.Resources,
			domain.WorldResourceBalanceV2{ResourceID: countFlowBatch, Name: "明确的原件批次（不代表整袋内容）", Unit: "件", ActualAmount: actual,
				ReadableFacts: []domain.ResourceReadableFactV2{{ID: "old_manifest", Text: "外清单旧记载一件；这只是文档陈述。"}}})
		var owner *domain.CharacterPhysicalStateV2
		for i := range input.Stimulus.PhysicalState.Actors {
			if input.Stimulus.PhysicalState.Actors[i].AgentID == input.Observations[0].AgentID {
				owner = &input.Stimulus.PhysicalState.Actors[i]
			}
		}
		if owner == nil {
			t.Fatal("fixture owner is absent")
		}
		owner.Resources = append(owner.Resources, domain.CharacterResourceHoldingV2{ResourceID: countFlowBatch,
			PerceivedName: "原件批次", PerceivedLabel: "原件批次", PerceivedUnit: "件", Access: "exclusive",
			Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"author-known"}})
		mechanism := domain.CodexMechanism{ID: countFlowMechanism, Name: "实际逐件清点已可接触的指定批次", Visibility: "formal",
			CharacterView: &domain.CharacterMechanismView{Name: "实际逐件清点已可接触的指定批次"}}
		input.Stimulus.Mechanisms = append(input.Stimulus.Mechanisms, mechanism)
		input.Observations[0].PublicMechanisms = append(input.Observations[0].PublicMechanisms, mechanism)
	})
	f.input = countFlowRebind(t, f.input, f.session)
	return f
}

// The old artifact fixture intentionally freezes its own policy inventory.
// Keep that helper and its golden tests untouched: this test-local rebind uses
// the same domain builders with the already-existing timed-measurement policy.
func countFlowRebind(t *testing.T, input domain.CharacterActivationInputSet, session domain.CharacterActivationSession) domain.CharacterActivationInputSet {
	t.Helper()
	input = artifactFlowCopy(t, input)
	previous := ""
	if len(session.CycleDigests) > 0 {
		previous = session.CycleDigests[len(session.CycleDigests)-1]
	}
	token, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, len(session.CycleDigests)+1, session.ChapterContextDigest, previous)
	artifactFlowMust(t, err)
	policies := append(append([]string(nil), artifactFlowPolicies...), domain.CharacterResourceObservationTimePolicyV1)
	input.Stimulus.Sources = append(append([]string(nil), policies...), token)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	artifactFlowMust(t, err)
	input.Stimulus.StoryClock.CurrentDay = session.CurrentDay
	clock, err := domain.FinalizeStoryClockContext(*input.Stimulus.StoryClock)
	artifactFlowMust(t, err)
	input.Stimulus.StoryClock = &clock
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	artifactFlowMust(t, err)
	cc, err := domain.NewCharacterObservationCycleContext(session)
	artifactFlowMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.Round, o.ConflictFeedback = 1, nil
		o.Sources, o.CycleContext, o.StimulusDigest = append([]string(nil), policies...), cc, input.Stimulus.Digest
		o.ResourceViews, err = domain.BuildCharacterResourceViewsForSourcesV2(*input.Stimulus.PhysicalState, o.AgentID, policies)
		artifactFlowMust(t, err)
		o.ArtifactViews, err = domain.BuildCharacterArtifactViewsV1(*input.Stimulus.PhysicalState, o.AgentID)
		artifactFlowMust(t, err)
		o.SelfExperiences, o.TaskProgress, err = domain.BuildCharacterSelfObservationForSourcesV2(*input.Stimulus.PhysicalState, o.AgentID, policies)
		artifactFlowMust(t, err)
		for _, memory := range input.Memories {
			if memory.AgentID != o.AgentID {
				continue
			}
			o.MemoryRoot, o.Memory = memory.MemoryRoot, artifactFlowCopy(t, memory.Facts)
			for j := range o.Memory {
				o.Memory[j].SourceDigest = domain.CharacterSourceRefV2(o.AgentID, o.Memory[j].SourceDigest)
				o.Memory[j].KnowledgeRefs = domain.CharacterSourceRefsV2(o.AgentID, o.Memory[j].KnowledgeRefs)
			}
		}
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		artifactFlowMust(t, err)
		for j := range input.Activation.Entries {
			if input.Activation.Entries[j].AgentID == o.AgentID {
				input.Activation.Entries[j].ObservationDigest = o.Digest
			}
		}
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	artifactFlowMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	artifactFlowMust(t, err)
	return input
}

func countFlowCountArgs(o domain.CharacterObservationPacket, requested bool) map[string]any {
	args := artifactFlowSubmitArgs(o, "count_batch")
	args["intended_action"] = "在柜台实际清点这一已可接触原件批次，不据外清单预认件数或整袋范围"
	args["mechanism_refs"] = []string{countFlowMechanism}
	task := args["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.Action, task.ResourceIDs = "逐件清点指定原件批次，不核读内部文字", []string{countFlowBatch}
	args["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	if requested {
		args["resource_measurements"] = []domain.ResourceMeasurementV2{{ResourceID: countFlowBatch, MechanismRef: countFlowMechanism, TaskID: task.TaskID}}
	}
	return args
}

func countFlowPrepare(t *testing.T, known, requested bool) (*artifactFlowFixture, *store.CharacterArbitrationV3, *ResolveChapterWorldTool, map[string]any, domain.CharacterDecisionProposal) {
	t.Helper()
	f := countFlowFixture(t, known)
	view := f.prepare(t)
	f.submit(t, view, f.author, countFlowCountArgs(f.observation(t, f.author), requested))
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "peer_wait"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	proposal := artifactFlowProposal(t, scope, f.author)
	args := artifactFlowArbiterArgs(scope)
	resolution := artifactFlowResolution(t, args, f.author)
	resolution["mechanism_refs"] = []string{countFlowMechanism}
	// This contradictory free text must never override the typed real 2.
	resolution["immediate_result"] = "清点一件（仅自由文字，不是宿主数量来源）"
	resolution["post_state"] = map[string]any{"location": proposal.Location, "resource_updates": []map[string]any{{"resource_id": countFlowBatch,
		"perception": domain.ResourcePerceptionV2{Kind: "last_observed", Amount: artifactFlowNumber(2), AsOfChapter: 1,
			ObservedAtDay: artifactFlowNumber(args["story_time"].(domain.StoryTimeChapterSchedule).EndDay), EvidenceRefs: []string{proposal.Digest}}}}}
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	return f, view, tool, args, proposal
}

func countFlowCommit(t *testing.T, f *artifactFlowFixture, view *store.CharacterArbitrationV3, tool *ResolveChapterWorldTool, args map[string]any) domain.VerifiedCharacterActivationStep {
	t.Helper()
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, f.st.Dir())
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, f.st.Dir())) {
		t.Fatal("replaying the result changed files")
	}
	cycle, err := view.FinalizeCycle(nil)
	artifactFlowMust(t, err)
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
	f.st = store.NewStore(f.st.Dir())
	prefix, err = f.st.LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, f.session.Chapter)
	artifactFlowMust(t, err)
	step, ok := prefix.Step(len(prefix.Session().CycleDigests) - 1)
	if !ok {
		t.Fatal("persisted cycle missing")
	}
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
				memory.Facts = append(memory.Facts, domain.CharacterAgentMemoryFact{ID: fmt.Sprintf("actual-%d-%s", cycle.Index, p.AgentID), Chapter: 1, Kind: "projected_decision", Text: text, SourceDigest: r.Digest, KnowledgeRefs: p.KnowledgeRefs})
				memory, err = domain.FinalizeCharacterAgentMemory(memory)
				artifactFlowMust(t, err)
				next.Memories[i] = memory
			}
		}
	}
	f.session = prefix.Session()
	f.input = countFlowRebind(t, next, f.session)
	return step
}

func TestCountObservationRealToolsPersistReloadAndWriteLimitedOwnerRecord(t *testing.T) {
	f, view, tool, args, proposal := countFlowPrepare(t, true, true)
	before := f.observation(t, f.author)
	for _, resource := range before.ResourceViews {
		if resource.ResourceID == countFlowBatch && (resource.Unit != "件" || resource.Perception.Kind != "unknown" || resource.Perception.Amount != nil) {
			t.Fatal("initial owner learned undisclosed actual count")
		}
	}
	beforeBytes, _ := json.Marshal(before)
	step := countFlowCommit(t, f, view, tool, args)
	batch := artifactFlowResource(t, step.AfterState(), countFlowBatch)
	if batch.Unit != "件" || batch.ActualAmount == nil || *batch.ActualAmount != 2 || len(batch.ReadableFacts) != 1 {
		t.Fatal("measurement changed the real batch or its separate document content")
	}
	owner := f.observation(t, f.author)
	var measured domain.ResourcePerceptionV2
	for _, resource := range owner.ResourceViews {
		if resource.ResourceID == countFlowBatch {
			measured = resource.Perception
		}
	}
	ref := domain.CharacterSourceRefV2(f.author, proposal.Digest)
	if measured.Kind != "last_observed" || measured.Amount == nil || *measured.Amount != 2 || measured.ObservedAtDay == nil || *measured.ObservedAtDay != 1.0/1440 || !containsString(measured.EvidenceRefs, ref) {
		t.Fatal("next owner lost actual count/time/source")
	}
	if _, ok := owner.AllowedFactIDs()[ref]; !ok {
		t.Fatal("actual measured source is not owner-citable")
	}
	if _, ok := f.observation(t, f.peer).AllowedFactIDs()[ref]; ok {
		t.Fatal("unreceived physical count leaked to the peer")
	}
	var memory string
	for _, fact := range owner.Memory {
		memory += fact.Text
	}
	if !strings.Contains(memory, "最后观测 2 件") || strings.Contains(memory, "清点一件") || strings.Contains(memory, "外清单旧记载一件") {
		t.Fatal("memory used free text/manifest instead of typed measured count")
	}
	if afterBytes, _ := json.Marshal(before); !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("measurement rewrote original observation")
	}
	for _, actor := range step.AfterState().Actors {
		if len(actor.ReceivedFacts) != 0 {
			t.Fatal("counting fabricated a document reading")
		}
	}
	view = f.prepare(t)
	write := artifactFlowSubmitArgs(owner, "write_count_record")
	write["knowledge_refs"] = []string{owner.KnownFacts[0].ID, ref}
	task := write["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.KnowledgeRefs = write["knowledge_refs"].([]string)
	task.ResourceIDs = []string{artifactFlowPaperID}
	task.OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "count_note", Label: "本人批次清点记录", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: artifactFlowPaperID, Amount: 1}},
		Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "count_seen", Text: "本人于T+1分钟实际清点指定原件批次为2件；不证明整袋只有这些材料，也不证明文书内容真实。", EpistemicKind: "self_statement", SourceRefs: []string{ref}}}}}
	write["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	f.submit(t, view, f.author, write)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "wait_record"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	args = artifactFlowArbiterArgs(scope)
	r := artifactFlowResolution(t, args, f.author)
	executions := r["self_executions"].([]domain.CharacterSelfExecutionV2)
	executions[0].OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: "count_note", Status: "created", AtDay: *executions[0].EndDay, ClaimIDs: []string{"count_seen"}, Complete: true}}
	r["self_executions"] = executions
	writer := artifactFlowProposal(t, scope, f.author)
	args["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: artifactFlowPaperID, Before: artifactFlowNumber(5), Delta: artifactFlowNumber(-1), After: artifactFlowNumber(4), EvidenceRefs: []string{writer.Digest}, StartDay: executions[0].StartDay, EndDay: executions[0].EndDay}}
	tool, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	step = countFlowCommit(t, f, view, tool, args)
	id := domain.CharacterWorkArtifactResourceIDV1(f.session.GenerationID, f.author, "write_count_record", "count_note")
	artifact := artifactFlowResource(t, step.AfterState(), id).Artifact
	if artifact == nil || len(artifact.Claims) != 1 || artifact.Claims[0].EpistemicKind != "self_statement" || !containsString(artifact.Claims[0].SourceRefs, ref) {
		t.Fatal("persisted record lost its actual measured source or limited epistemic category")
	}
	if *artifactFlowResource(t, step.AfterState(), countFlowBatch).ActualAmount != 2 || *artifactFlowResource(t, step.AfterState(), artifactFlowPaperID).ActualAmount != 4 {
		t.Fatal("count/write changed batch stock or charged paper twice")
	}
}

func TestCountObservationRejectsUnknownWrongUnexecutedTimeAndFreeTextWithoutWrites(t *testing.T) {
	for _, mode := range []string{"unknown_actual", "wrong_count", "no_work", "wrong_time", "free_text_without_measurement"} {
		t.Run(mode, func(t *testing.T) {
			f, _, tool, args, _ := countFlowPrepare(t, mode != "unknown_actual", mode != "free_text_without_measurement")
			r := artifactFlowResolution(t, args, f.author)
			perception := r["post_state"].(map[string]any)["resource_updates"].([]map[string]any)[0]["perception"].(domain.ResourcePerceptionV2)
			switch mode {
			case "wrong_count", "free_text_without_measurement":
				perception.Amount = artifactFlowNumber(1)
			case "no_work":
				r["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "count_batch", Status: "not_started"}}
			case "wrong_time":
				perception.ObservedAtDay = artifactFlowNumber(.5 / 1440)
			}
			r["post_state"].(map[string]any)["resource_updates"].([]map[string]any)[0]["perception"] = perception
			f.rejectArbitration(t, tool, args)
		})
	}
}
