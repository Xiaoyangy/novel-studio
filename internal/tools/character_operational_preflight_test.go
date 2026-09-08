package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

const operationalPreflightResource = "res_0000000000000074"
const operationalPreflightDevice = "res_0000000000000075"

func operationalPreflightFixture(t *testing.T, document bool, withPeer ...bool) (*store.Store, domain.CharacterActivationSession, domain.CharacterActivationInputSet, *store.CharacterArbitrationV3) {
	t.Helper()
	_, input := testutil.CharacterActivationV3Inputs(t)
	physical := artifactFlowCopy(t, *input.Stimulus.PhysicalState)
	resource := domain.WorldResourceBalanceV2{ResourceID: operationalPreflightResource, Name: "PRIVATE_CATALOG_NAME"}
	if document {
		resource.ReadableFacts = []domain.ResourceReadableFactV2{{ID: "private-document-fact", Text: "PRIVATE_DOCUMENT_CONTENT"}}
	}
	physical.Resources = append(physical.Resources, resource)
	physical.Actors[0].Resources = append(physical.Actors[0].Resources, domain.CharacterResourceHoldingV2{ResourceID: operationalPreflightResource, PerceivedName: "本人持有物", PerceivedLabel: "本人持有物", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{input.Observations[0].KnownFacts[0].ID}})
	if len(withPeer) > 0 && withPeer[0] {
		registry, record, err := input.Registry.UpsertCharacter("乙", nil, "core", 1, "")
		artifactFlowMust(t, err)
		input.Registry, err = domain.FinalizeCharacterAgentRegistry(registry)
		artifactFlowMust(t, err)
		physical.Resources = append(physical.Resources, domain.WorldResourceBalanceV2{ResourceID: operationalPreflightDevice, Name: "同伴设备"})
		physical.Actors = append(physical.Actors, domain.CharacterPhysicalStateV2{AgentID: record.AgentID, Character: record.Character, Location: physical.Actors[0].Location,
			Resources: []domain.CharacterResourceHoldingV2{{ResourceID: operationalPreflightDevice, PerceivedName: "设备", PerceivedLabel: "设备", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{input.Observations[0].KnownFacts[0].ID}}}})
		physical, err = domain.PrepareCharacterSelfChronologyStateV1(physical)
		artifactFlowMust(t, err)
		o := artifactFlowCopy(t, input.Observations[0])
		o.AgentID, o.Character = record.AgentID, record.Character
		memory, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: record.AgentID, Character: record.Character, GenerationID: input.Stimulus.GenerationID, State: "projected"})
		artifactFlowMust(t, err)
		o.MemoryRoot = memory.MemoryRoot
		input.Memories = append(input.Memories, memory)
		input.Observations = append(input.Observations, o)
		input.Activation.RegistryRoot = input.Registry.RegistryRoot
		input.Activation.Entries = append(input.Activation.Entries, domain.CharacterAgentActivationEntry{AgentID: record.AgentID, Character: record.Character, Tier: "core", State: domain.CharacterAgentActive, Reasons: []string{"fixture"}})
	}
	var err error
	physical, err = domain.FinalizeWorldPhysicalStateV2(physical)
	artifactFlowMust(t, err)
	input.Stimulus.PhysicalState = &physical
	input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterOperationalAvailabilityPolicyV1)
	input.Stimulus.Mechanisms = append(input.Stimulus.Mechanisms, domain.CodexMechanism{ID: "local-check", Name: "本人局部检查", Visibility: "formal"})
	session, err := domain.NewCharacterActivationSession(input.Stimulus.GenerationID, input.Stimulus.Chapter, input.Observations[0].CycleContext.ChapterContextDigest, physical, input.Stimulus.StoryClock.CurrentDay, 8)
	artifactFlowMust(t, err)
	input.Stimulus.SelfEvaluationContext, err = domain.NewCharacterSelfEvaluationContextV1(session)
	artifactFlowMust(t, err)
	input.Stimulus, err = domain.FinalizeWorldStimulusPacket(input.Stimulus)
	artifactFlowMust(t, err)
	for i := range input.Observations {
		o := &input.Observations[i]
		o.StimulusDigest = input.Stimulus.Digest
		o.Sources = append(o.Sources, domain.CharacterOperationalAvailabilityPolicyV1)
		o.PublicMechanisms = input.Stimulus.Mechanisms
		o.ResourceViews, err = domain.BuildCharacterResourceViewsV2(physical, o.AgentID)
		artifactFlowMust(t, err)
		*o, err = domain.FinalizeCharacterObservationPacket(*o)
		artifactFlowMust(t, err)
		input.Activation.Entries[i].ObservationDigest = o.Digest
	}
	input.Activation, err = domain.FinalizeCharacterAgentActivation(input.Activation)
	artifactFlowMust(t, err)
	input, err = domain.FinalizeCharacterActivationInputSet(input)
	artifactFlowMust(t, err)
	st := store.NewStore(t.TempDir())
	artifactFlowMust(t, st.Init())
	artifactFlowMust(t, st.CreateCharacterActivationSession(session))
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	artifactFlowMust(t, err)
	artifactFlowMust(t, proofs.PublishActivationInputs(input))
	view, err := st.PrepareCharacterArbitrationV3(session, nil, artifactFlowProtocol)
	artifactFlowMust(t, err)
	return st, session, input, view
}

func operationalPreflightProposal(t *testing.T, o domain.CharacterObservationPacket) domain.CharacterDecisionProposal {
	t.Helper()
	resourceID := operationalPreflightResource
	for _, view := range o.ResourceViews {
		if view.ResourceID == operationalPreflightDevice {
			resourceID = operationalPreflightDevice
		}
	}
	p := domain.CharacterDecisionProposal{GenerationID: o.GenerationID, Chapter: o.Chapter, Round: o.Round, AgentID: o.AgentID, Character: o.Character, ObservationDigest: o.Digest,
		Location: o.Location, CurrentGoal: o.CurrentGoal, Pressure: o.Pressure, AvailableOptions: []string{"检查", "等待"}, Decision: "本人检查", DecisionReason: "本人选择", IntendedAction: "仅检查所持物是否适于安全操作，不读取内容", ActionDuration: "一分钟",
		KnowledgeRefs: []string{o.KnownFacts[0].ID}, MechanismRefs: []string{"local-check"}, SubmittedAt: "2026-09-08T00:00:00Z",
		SelfTasks: []domain.CharacterSelfTaskV2{{TaskID: "inspection", Kind: "work", Action: "本人检查所持物", ResourceIDs: []string{resourceID}, ProgressTarget: artifactFlowNumber(1), ProgressUnit: "minute", KnowledgeRefs: []string{o.KnownFacts[0].ID},
			ObservationRequests: []domain.CharacterOperationalObservationRequestV1{{RequestID: "safe-use", ResourceID: resourceID, Purpose: "检查本人所持物是否适合安全操作，不读内容", MechanismRef: "local-check", KnowledgeRefs: []string{o.KnownFacts[0].ID}}}}}}
	p, err := domain.FinalizeCharacterDecisionProposal(p, o)
	artifactFlowMust(t, err)
	return p
}

func operationalPreflightArgs(t *testing.T, p domain.CharacterDecisionProposal) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"location": p.Location, "current_goal": p.CurrentGoal, "pressure": p.Pressure, "available_options": p.AvailableOptions, "decision": p.Decision,
		"decision_reason": p.DecisionReason, "intended_action": p.IntendedAction, "action_duration": p.ActionDuration, "knowledge_refs": p.KnowledgeRefs, "mechanism_refs": p.MechanismRefs, "self_tasks": p.SelfTasks})
	artifactFlowMust(t, err)
	return raw
}

func TestOperationalPreflightRejectsHiddenIneligibilityBeforeSubmitWithoutDisclosure(t *testing.T) {
	st, session, input, view := operationalPreflightFixture(t, true)
	p := operationalPreflightProposal(t, input.Observations[0]) // Original view-only contract remains unchanged.
	tool, err := NewSubmitCharacterActivationV3DecisionTool(st, session, input.Observations[0], view)
	artifactFlowMust(t, err)
	before := arbitrationReferenceFiles(t, st.Dir())
	_, err = tool.Execute(context.Background(), operationalPreflightArgs(t, p))
	if err == nil || !strings.Contains(err.Error(), "safe-use") || !strings.Contains(err.Error(), operationalPreflightResource) {
		t.Fatalf("unsupported owner request was not rejected precisely: %v", err)
	}
	for _, secret := range []string{"PRIVATE_DOCUMENT_CONTENT", "private-document-fact", "PRIVATE_CATALOG_NAME"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("preflight exposed a private catalog property or document content")
		}
	}
	if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
		t.Fatal("rejected submission wrote a proposal or modified frozen sources")
	}
}

func TestOperationalPreflightSavedProposalCanRequestZeroEffectR1ThenOwnerP2(t *testing.T) {
	st, session, input, view := operationalPreflightFixture(t, true)
	old := operationalPreflightProposal(t, input.Observations[0])
	// Simulate a proposal already durably admitted by the former view-only
	// submission tool. Never rewrite it to make the new validator accept it.
	artifactFlowMust(t, view.SaveProposal(old))
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	artifactFlowMust(t, err)
	originalBytes, err := json.Marshal(old)
	artifactFlowMust(t, err)
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	var args map[string]any
	artifactFlowMust(t, json.Unmarshal(v3ArbitrationToolArgs(scope, true), &args))
	args["conflicts"] = []domain.WorldArbitrationConflict{{ID: "unsupported-owner-request", Kind: "rule", AffectedAgentIDs: []string{old.AgentID}, Feedback: "该操作性请求不适用于此接口；本人可改选任务，尚未执行。"}}
	tool, err := NewResolveCharacterArbitrationV3Tool(st, session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	raw, err := json.Marshal(args)
	artifactFlowMust(t, err)
	_, err = tool.Execute(context.Background(), raw)
	artifactFlowMust(t, err)
	r1, err := view.LoadArbitration(1)
	artifactFlowMust(t, err)
	if r1 == nil || r1.Finalized || r1.StoryTime.StartDay != r1.StoryTime.EndDay {
		t.Fatal("old unsupported request did not produce a genuine no-op revision")
	}
	after, err := domain.ApplyArbitrationPhysicalStateV2(*r1, input.Stimulus, old)
	artifactFlowMust(t, err)
	if !sameActivationToolValue(after, *input.Stimulus.PhysicalState) {
		t.Fatal("technical rejection became a physical outcome")
	}
	stored, err := proofs.LoadProposal(old.GenerationID, old.Chapter, old.Round, old.AgentID)
	artifactFlowMust(t, err)
	storedBytes, err := json.Marshal(stored)
	artifactFlowMust(t, err)
	if string(storedBytes) != string(originalBytes) || stored.Digest != old.Digest {
		t.Fatal("no-op R1 changed the original proposal")
	}
	revision := artifactFlowCopy(t, input.Observations[0])
	revision.Round = 2
	revision.ConflictFeedback = domain.CharacterArbitrationFeedbackForOwnerV1(*r1, old.AgentID, input.Stimulus.Sources)
	revision, err = domain.FinalizeCharacterObservationPacket(revision)
	artifactFlowMust(t, err)
	artifactFlowMust(t, view.SaveObservation(revision))
	p2 := operationalPreflightProposal(t, revision)
	p2.SelfTasks[0].ObservationRequests = nil
	p2.Decision, p2.IntendedAction = "本人保护原件", "保护本人所持物，不声明操作性结果或读取"
	writer, err := NewSubmitCharacterActivationV3DecisionTool(st, session, revision, view)
	artifactFlowMust(t, err)
	_, err = writer.Execute(context.Background(), operationalPreflightArgs(t, p2))
	artifactFlowMust(t, err)
	scope, err = view.Sources(2)
	artifactFlowMust(t, err)
	resolver, err := NewResolveCharacterArbitrationV3Tool(st, session, view, 2, artifactFlowProtocol)
	artifactFlowMust(t, err)
	_, err = resolver.Execute(context.Background(), v3ArbitrationToolArgs(scope, false))
	artifactFlowMust(t, err)
	if r2, err := view.LoadArbitration(2); err != nil || r2 == nil || !r2.Finalized {
		t.Fatalf("owner-authored P2 did not close through the real tool: %v", err)
	}
}

func TestOperationalPreflightZeroEffectRevisionCannotHideExecutionOrKnowledge(t *testing.T) {
	for _, change := range []string{"time", "settlement", "delivery", "contact", "self_execution", "location", "received_fact", "operational_result", "finalized", "final_available", "final_unavailable", "final_inconclusive", "wrong_owner_conflict"} {
		t.Run(change, func(t *testing.T) {
			st, session, input, view := operationalPreflightFixture(t, true)
			p := operationalPreflightProposal(t, input.Observations[0])
			artifactFlowMust(t, view.SaveProposal(p))
			scope, err := view.Sources(1)
			artifactFlowMust(t, err)
			var args map[string]any
			artifactFlowMust(t, json.Unmarshal(v3ArbitrationToolArgs(scope, true), &args))
			args["conflicts"] = []domain.WorldArbitrationConflict{{ID: "unsupported", Kind: "rule", AffectedAgentIDs: []string{p.AgentID}, Feedback: "接口不适用，尚未执行"}}
			resolution := args["resolutions"].([]any)[0].(map[string]any)
			post := resolution["post_state"].(map[string]any)
			start := input.Stimulus.StoryClock.CurrentDay
			end := start + 1.0/1440
			switch change {
			case "time":
				args["story_time"].(map[string]any)["end_day"] = end
			case "settlement":
				args["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: operationalPreflightResource, EvidenceRefs: []string{p.Digest}}}
			case "delivery":
				args["resource_deliveries"] = []domain.ResourceDeliveryV2{{ResourceID: operationalPreflightResource, FromAgentID: p.AgentID, ToAgentID: p.AgentID, SourceProposalDigest: p.Digest, Access: "shared", EvidenceRefs: []string{p.Digest}}}
			case "contact":
				args["passive_receptions"] = []domain.CharacterPassiveReceptionV2{{FromAgentID: p.AgentID, ToAgentID: p.AgentID, SourceProposalDigest: p.Digest, CommunicationID: "not-sent"}}
			case "self_execution":
				resolution["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "not_started"}}
			case "location":
				post["location"] = "别处"
			case "received_fact":
				post["received_facts"] = []domain.CharacterReceivedFactV2{{Kind: "document_statement", Text: "PRIVATE_DOCUMENT_CONTENT", SourceType: "resource_read", SourceID: "private-document-fact", SourceProposalDigest: p.Digest, ResourceID: operationalPreflightResource, Chapter: p.Chapter}}
			case "operational_result":
				resolution["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "completed", StartDay: &start, EndDay: &end, ObservationResults: []domain.CharacterOperationalObservationResultV1{{RequestID: "safe-use", Result: "inconclusive", ObservedAtDay: &end}}}}
			case "finalized":
				args["finalized"], args["conflicts"] = true, nil
				resolution["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "blocked"}}
			case "final_available", "final_unavailable", "final_inconclusive":
				args["finalized"], args["conflicts"] = true, nil
				args["story_time"].(map[string]any)["end_day"] = end
				resolution["mechanism_refs"] = []string{"local-check"}
				resolution["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "completed", StartDay: &start, EndDay: &end, ObservationResults: []domain.CharacterOperationalObservationResultV1{{RequestID: "safe-use", Result: strings.TrimPrefix(change, "final_"), ObservedAtDay: &end}}}}
			case "wrong_owner_conflict":
				args["conflicts"] = []domain.WorldArbitrationConflict{{ID: "unrelated", Kind: "time", AffectedAgentIDs: []string{p.AgentID}, Feedback: "另一时间问题"}}
			}
			tool, err := NewResolveCharacterArbitrationV3Tool(st, session, view, 1, artifactFlowProtocol)
			artifactFlowMust(t, err)
			before := arbitrationReferenceFiles(t, st.Dir())
			raw, err := json.Marshal(args)
			artifactFlowMust(t, err)
			if _, err := tool.Execute(context.Background(), raw); err == nil {
				t.Fatal("unsupported operational request used the no-op branch to publish another effect")
			}
			if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
				t.Fatal("rejected revision mutated durable sources")
			}
		})
	}
}

func TestOperationalPreflightRevisionRejectsOtherOwnersEffectsRegardlessOfOrder(t *testing.T) {
	for _, change := range []string{"device_observation", "received_communication", "artifact_read", "artifact_signature"} {
		for _, reverse := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "/owner-first", true: "/peer-first"}[reverse], func(t *testing.T) {
				st, session, input, view := operationalPreflightFixture(t, true, true)
				var owner, peer domain.CharacterObservationPacket
				for _, observation := range input.Observations {
					for _, resource := range observation.ResourceViews {
						if resource.ResourceID == operationalPreflightResource {
							owner = observation
						} else if resource.ResourceID == operationalPreflightDevice {
							peer = observation
						}
					}
				}
				p := operationalPreflightProposal(t, owner)
				p.Communications = []domain.CharacterCommunicationV2{{ID: "own-message", ToCharacter: peer.Character, Kind: "statement", Text: "本人已知内容", KnowledgeRefs: []string{owner.KnownFacts[0].ID}}}
				p, err := domain.FinalizeCharacterDecisionProposal(p, owner)
				artifactFlowMust(t, err)
				artifactFlowMust(t, view.SaveProposal(p))
				q := operationalPreflightProposal(t, peer)
				q.SelfTasks[0].ResourceIDs = []string{operationalPreflightDevice}
				q.SelfTasks[0].ObservationRequests[0].ResourceID = operationalPreflightDevice
				q, err = domain.FinalizeCharacterDecisionProposal(q, peer)
				artifactFlowMust(t, err)
				writer, err := NewSubmitCharacterActivationV3DecisionTool(st, session, peer, view)
				artifactFlowMust(t, err)
				_, err = writer.Execute(context.Background(), operationalPreflightArgs(t, q))
				artifactFlowMust(t, err)
				scope, err := view.Sources(1)
				artifactFlowMust(t, err)
				var args map[string]any
				artifactFlowMust(t, json.Unmarshal(v3ArbitrationToolArgs(scope, true), &args))
				args["conflicts"] = []domain.WorldArbitrationConflict{{ID: "unsupported", Kind: "rule", AffectedAgentIDs: []string{p.AgentID}, Feedback: "接口不适用，尚未执行"}}
				template := args["resolutions"].([]any)[0].(map[string]any)
				var resolutions []any
				var peerResolution map[string]any
				for i, proposal := range scope.EffectiveProposals() {
					r := artifactFlowCopy(t, template)
					r["agent_id"], r["character"], r["proposal_digest"] = proposal.AgentID, proposal.Character, proposal.Digest
					r["decision"], r["intended_action"], r["action_order"] = proposal.Decision, proposal.IntendedAction, i+1
					r["mechanism_refs"] = []string{"local-check"}
					if proposal.AgentID == peer.AgentID {
						peerResolution, q = r, proposal
					}
					resolutions = append(resolutions, r)
				}
				start, end := session.CurrentDay, session.CurrentDay+1.0/1440
				switch change {
				case "device_observation":
					peerResolution["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "inspection", Status: "completed", StartDay: &start, EndDay: &end, ObservationResults: []domain.CharacterOperationalObservationResultV1{{RequestID: "safe-use", Result: "available", ObservedAtDay: &end}}}}
				case "received_communication":
					peerResolution["post_state"].(map[string]any)["received_facts"] = []domain.CharacterReceivedFactV2{{Kind: "statement", Text: "本人已知内容", SourceType: "communication", SourceID: "own-message", SourceProposalDigest: p.Digest, FromAgentID: p.AgentID, Chapter: p.Chapter}}
				case "artifact_read":
					peerResolution["artifact_read_results"] = []map[string]any{{"resource_id": operationalPreflightDevice}}
				case "artifact_signature":
					peerResolution["artifact_signatures"] = []map[string]any{{"resource_id": operationalPreflightDevice}}
				}
				if reverse {
					resolutions[0], resolutions[1] = resolutions[1], resolutions[0]
				}
				args["resolutions"] = resolutions
				resolver, err := NewResolveCharacterArbitrationV3Tool(st, session, view, 1, artifactFlowProtocol)
				artifactFlowMust(t, err)
				before := arbitrationReferenceFiles(t, st.Dir())
				raw, err := json.Marshal(args)
				artifactFlowMust(t, err)
				if _, err := resolver.Execute(context.Background(), raw); err == nil {
					t.Fatal("another owner's effect slipped through the no-op revision")
				}
				if !reflect.DeepEqual(before, arbitrationReferenceFiles(t, st.Dir())) {
					t.Fatal("rejected mixed-owner revision changed durable evidence")
				}
			})
		}
	}
}
