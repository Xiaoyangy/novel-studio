package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const surfaceToolBag = "res_7d683751779f4393"
const surfaceToolMechanism = "M_CUSTODY"

func surfaceToolFixture(t *testing.T) *artifactFlowFixture {
	return newArtifactFlowFixture(t, func(input *domain.CharacterActivationInputSet) {
		input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterSurfaceInspectionPolicyV1)
		input.Stimulus.PhysicalState.Resources = append(input.Stimulus.PhysicalState.Resources, domain.WorldResourceBalanceV2{
			ResourceID: surfaceToolBag, Name: "既有F07袋与外清单", InspectableSurfaces: []string{"container_exterior", "seal_exterior"},
			ReadableFacts: []domain.ResourceReadableFactV2{{ID: "manifest-old", Text: "PRIVATE_OLD_MANIFEST_CONTENT_NOT_A_CURRENT_INSPECTION"}},
		})
		for i := range input.Stimulus.PhysicalState.Actors {
			actor := &input.Stimulus.PhysicalState.Actors[i]
			if actor.AgentID == input.Observations[0].AgentID {
				actor.Resources = append(actor.Resources, domain.CharacterResourceHoldingV2{
					ResourceID: surfaceToolBag, PerceivedName: "F07", PerceivedLabel: "本人保管的F07袋", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{input.Observations[0].KnownFacts[0].ID},
				})
			}
		}
		mechanism := domain.CodexMechanism{ID: surfaceToolMechanism, Name: "当前外表面检查", Visibility: "formal", CharacterView: &domain.CharacterMechanismView{Name: "当前外表面检查"}}
		input.Stimulus.Mechanisms = append(input.Stimulus.Mechanisms, mechanism)
		input.Observations[0].PublicMechanisms = append(input.Observations[0].PublicMechanisms, mechanism)
	})
}

func surfaceToolInspectionArgs(o domain.CharacterObservationPacket) map[string]any {
	args := artifactFlowSubmitArgs(o, "inspect_old_seal")
	args["mechanism_refs"] = []string{surfaceToolMechanism}
	task := args["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.Action, task.ResourceIDs = "本人检查既有封条外表面，不读取袋内或外清单", []string{surfaceToolBag}
	task.ObservationRequests = []domain.CharacterOperationalObservationRequestV1{{RequestID: "seal_now", ResourceID: surfaceToolBag, Surface: "seal_exterior", Purpose: "记录当时封条外观，不认证封存历史或开启许可", MechanismRef: surfaceToolMechanism, KnowledgeRefs: task.KnowledgeRefs}}
	args["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	return args
}

func TestSurfaceInspectionRealToolsBindCurrentOwnerThenWrittenArtifact(t *testing.T) {
	f := surfaceToolFixture(t)
	view := f.prepare(t)
	o := f.observation(t, f.author)
	beforeObservation, _ := json.Marshal(o)
	if strings.Contains(string(beforeObservation), "PRIVATE_OLD_MANIFEST_CONTENT") || len(o.OperationalObservations) != 0 {
		t.Fatal("capability declaration revealed old document contents or invented current observation")
	}
	f.submit(t, view, f.author, surfaceToolInspectionArgs(o))
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "peer_own_work"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	proposal := artifactFlowProposal(t, scope, f.author)
	args := artifactFlowArbiterArgs(scope)
	resolution := artifactFlowResolution(t, args, f.author)
	resolution["mechanism_refs"] = []string{surfaceToolMechanism}
	executions := resolution["self_executions"].([]domain.CharacterSelfExecutionV2)
	observed := *executions[0].EndDay
	executions[0].ObservationResults = []domain.CharacterOperationalObservationResultV1{{RequestID: "seal_now", Surface: "seal_exterior", Result: "no_visible_damage", ObservedAtDay: &observed}}
	resolution["self_executions"] = executions
	resolver, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	for _, change := range []string{"wrong_surface", "old_available", "missing_surface", "future_time", "unexecuted"} {
		t.Run(change, func(t *testing.T) {
			bad := artifactFlowCopy(t, args)
			// Preserve typed fixture helpers after the detached JSON copy.
			var receipt struct {
				Resolutions []struct {
					AgentID        string                            `json:"agent_id"`
					SelfExecutions []domain.CharacterSelfExecutionV2 `json:"self_executions"`
				} `json:"resolutions"`
			}
			raw, _ := json.Marshal(bad)
			artifactFlowMust(t, json.Unmarshal(raw, &receipt))
			for _, r := range receipt.Resolutions {
				if r.AgentID != f.author {
					continue
				}
				e := r.SelfExecutions[0]
				switch change {
				case "wrong_surface":
					e.ObservationResults[0].Surface = "container_exterior"
				case "old_available":
					e.ObservationResults[0].Result = "available"
				case "missing_surface":
					e.ObservationResults[0].Surface = ""
				case "future_time":
					e.ObservationResults[0].ObservedAtDay = artifactFlowNumber(observed + 1.0/1440)
				case "unexecuted":
					e.Status, e.StartDay, e.EndDay = "not_started", nil, nil
				}
				for _, item := range bad["resolutions"].([]any) {
					row := item.(map[string]any)
					if row["agent_id"] == f.author {
						row["self_executions"] = []domain.CharacterSelfExecutionV2{e}
					}
				}
			}
			f.rejectArbitration(t, resolver, bad)
		})
	}
	step := f.commit(t, view, resolver, args)
	owner := f.observation(t, f.author)
	if len(owner.OperationalObservations) != 1 {
		t.Fatal("next owner input lacks exact committed surface observation")
	}
	inspection := owner.OperationalObservations[0]
	if inspection.Surface != "seal_exterior" || inspection.Result != "no_visible_damage" || inspection.ResourceID != surfaceToolBag || inspection.SourceProposalDigest != proposal.Digest || inspection.SourceExperienceID == "" || inspection.ObservedAtDay == nil || *inspection.ObservedAtDay != observed {
		t.Fatal("next observation lost source, owner, facet or actual time")
	}
	if _, known := owner.AllowedFactIDs()[inspection.ID]; !known {
		t.Fatal("actual surface observation cannot be cited")
	}
	peer := f.observation(t, f.peer)
	if len(peer.OperationalObservations) != 0 {
		t.Fatal("surface fact reached a different owner without delivery")
	}
	if _, known := peer.AllowedFactIDs()[inspection.ID]; known {
		t.Fatal("peer can cite unreceived surface observation")
	}
	bag := artifactFlowResource(t, step.AfterState(), surfaceToolBag)
	if len(bag.ReadableFacts) != 1 || bag.ReadableFacts[0].ID != "manifest-old" {
		t.Fatal("inspection removed original readable content")
	}
	for _, actor := range step.AfterState().Actors {
		if len(actor.ReceivedFacts) != 0 {
			t.Fatal("surface inspection fabricated document reading")
		}
	}

	// The next real submission cites the derived observation ID, not the old
	// manifest or purpose text. A real artifact is formed from the actual claim.
	view = f.prepare(t)
	write := artifactFlowSubmitArgs(owner, "record_actual_surface")
	write["knowledge_refs"] = []string{inspection.ID}
	task := write["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.KnowledgeRefs, task.ResourceIDs = []string{inspection.ID}, []string{artifactFlowPaperID}
	task.OutputRequests = []domain.CharacterWorkOutputRequestV1{{OutputKey: "surface_record", Label: "本人当时封条外观记录", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: artifactFlowPaperID, Amount: 1}}, Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "observed_seal", Text: "本人本次检查未见封条外表面破损；不证明历史未开封、内容或许可。", EpistemicKind: "self_statement", SourceRefs: []string{inspection.ID}}}}}
	write["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	f.submit(t, view, f.author, write)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(peer, "peer_next_own_work"))
	scope, err = view.Sources(1)
	artifactFlowMust(t, err)
	written := artifactFlowProposal(t, scope, f.author)
	if !reflect.DeepEqual(written.KnowledgeRefs, []string{inspection.ID}) || written.SelfTasks[0].OutputRequests[0].Claims[0].SourceRefs[0] != inspection.ID {
		t.Fatal("real written intent did not bind the derived surface ID")
	}
	args = artifactFlowArbiterArgs(scope)
	resolution = artifactFlowResolution(t, args, f.author)
	executions = resolution["self_executions"].([]domain.CharacterSelfExecutionV2)
	end := *executions[0].EndDay
	executions[0].OutputResults = []domain.CharacterWorkOutputResultV1{{OutputKey: "surface_record", Status: "created", AtDay: end, ClaimIDs: []string{"observed_seal"}, Complete: true}}
	resolution["self_executions"] = executions
	args["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: artifactFlowPaperID, Before: artifactFlowNumber(5), Delta: artifactFlowNumber(-1), After: artifactFlowNumber(4), EvidenceRefs: []string{written.Digest}}}
	resolver, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	second := f.commit(t, view, resolver, args)
	count := 0
	for _, resource := range second.AfterState().Resources {
		if resource.Artifact != nil && resource.Artifact.OutputKey == "surface_record" {
			count++
			if resource.Artifact.OriginProposalDigest != written.Digest || resource.Artifact.Claims[0].SourceRefs[0] != inspection.ID {
				t.Fatal("artifact lost its actual owner observation lineage")
			}
		}
	}
	if count != 1 || *artifactFlowResource(t, second.AfterState(), artifactFlowPaperID).ActualAmount != 4 {
		t.Fatal("surface record or its once-only material accounting is missing")
	}
}

func TestSurfaceInspectionConcurrentExactSubmissionsRemainImmutable(t *testing.T) {
	f := surfaceToolFixture(t)
	f.prepare(t)
	o := f.observation(t, f.author)
	raw, _ := json.Marshal(surfaceToolInspectionArgs(o))
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := store.NewStore(f.st.Dir())
			current, err := local.LoadCharacterArbitrationV3(f.session.GenerationID, f.session.Chapter)
			if err == nil {
				var tool *SubmitCharacterDecisionTool
				tool, err = NewSubmitCharacterActivationV3DecisionTool(local, f.session, o, current)
				if err == nil {
					_, err = tool.Execute(context.Background(), raw)
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		artifactFlowMust(t, err)
	}
	proofs, err := f.st.CharacterAgents.ForActivationCycle(f.session)
	artifactFlowMust(t, err)
	proposal, err := proofs.LoadProposal(f.session.GenerationID, f.session.Chapter, 1, o.AgentID)
	artifactFlowMust(t, err)
	if proposal == nil || proposal.SelfTasks[0].ObservationRequests[0].Surface != "seal_exterior" {
		t.Fatal("concurrent identical surface proposals lost identity")
	}
}

func TestSurfaceInspectionRequestsRejectForeignOrUnboundTargetsWithoutWrites(t *testing.T) {
	f := surfaceToolFixture(t)
	view := f.prepare(t)
	for _, change := range []string{"undeclared_facet", "foreign_resource", "missing_task_resource", "peer_owner"} {
		t.Run(change, func(t *testing.T) {
			owner := f.author
			if change == "peer_owner" {
				owner = f.peer
			}
			args := surfaceToolInspectionArgs(f.observation(t, owner))
			task := args["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
			switch change {
			case "undeclared_facet":
				task.ObservationRequests[0].Surface = "document_contents"
			case "foreign_resource":
				task.ObservationRequests[0].ResourceID = "res_9999999999999999"
				task.ResourceIDs = []string{"res_9999999999999999"}
			case "missing_task_resource":
				task.ResourceIDs = nil
			}
			args["self_tasks"] = []domain.CharacterSelfTaskV2{task}
			f.rejectSubmit(t, view, owner, args)
		})
	}
	// A saved old-generation observation is not upgraded by a new request.
	st, session, input, old := operationalPreflightFixture(t, true)
	observation := input.Observations[0]
	proposal := operationalPreflightProposal(t, observation)
	proposal.SelfTasks[0].ObservationRequests[0].Surface = "seal_exterior"
	tool, err := NewSubmitCharacterActivationV3DecisionTool(st, session, observation, old)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, st.Dir())
	_, err = tool.Execute(context.Background(), operationalPreflightArgs(t, proposal))
	if err == nil || !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("unmarked old observation accepted surface request or changed state")
	}
}
