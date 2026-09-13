package tools

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const incomingWrittenClaim = "WRITTEN_STATEMENT_ONLY_NOT_INDEPENDENT_TRUTH"
const incomingVisibleClaim = "派生文档声明（self_statement；不自动等于世界真相）：" + incomingWrittenClaim

// Create the predecessor artifacts through actual Submit/Arbiter/Store, not by
// inserting a desired post-state. Incoming reading begins only in cycle two.
func incomingReadFixture(t *testing.T, enabled bool, count int) (*artifactFlowFixture, []string, []string) {
	t.Helper()
	f := newArtifactFlowFixture(t, func(input *domain.CharacterActivationInputSet) {
		if enabled {
			input.Stimulus.Sources = append(input.Stimulus.Sources, domain.CharacterIncomingMaterialReadPolicyV1)
		}
	})
	view := f.prepare(t)
	args := artifactFlowSubmitArgs(f.observation(t, f.author), "create_before_delivery")
	task := args["self_tasks"].([]domain.CharacterSelfTaskV2)[0]
	task.ResourceIDs = []string{artifactFlowPaperID}
	var ids []string
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("record_%d", i)
		task.OutputRequests = append(task.OutputRequests, domain.CharacterWorkOutputRequestV1{OutputKey: key, Label: "可在实际出示后读取的记录", MaterialInputs: []domain.CharacterWorkMaterialInputV1{{ResourceID: artifactFlowPaperID, Amount: 1}}, Claims: []domain.CharacterWorkArtifactClaimV1{{ClaimID: "statement", Text: incomingWrittenClaim, EpistemicKind: "self_statement", SourceRefs: args["knowledge_refs"].([]string)}}})
		ids = append(ids, domain.CharacterWorkArtifactResourceIDV1(f.session.GenerationID, f.author, task.TaskID, key))
	}
	args["self_tasks"] = []domain.CharacterSelfTaskV2{task}
	f.submit(t, view, f.author, args)
	f.submit(t, view, f.peer, artifactFlowSubmitArgs(f.observation(t, f.peer), "wait_for_creation"))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	raw := artifactFlowArbiterArgs(scope)
	end := raw["story_time"].(domain.StoryTimeChapterSchedule).EndDay
	author := artifactFlowResolution(t, raw, f.author)
	executions := author["self_executions"].([]domain.CharacterSelfExecutionV2)
	for i := 0; i < count; i++ {
		executions[0].OutputResults = append(executions[0].OutputResults, domain.CharacterWorkOutputResultV1{OutputKey: fmt.Sprintf("record_%d", i), Status: "created", AtDay: end, ClaimIDs: []string{"statement"}, Complete: true})
	}
	author["self_executions"] = executions
	p := artifactFlowProposal(t, scope, f.author)
	raw["resource_settlements"] = []domain.ResourceSettlementV2{{ResourceID: artifactFlowPaperID, Before: artifactFlowNumber(5), Delta: artifactFlowNumber(-float64(count)), After: artifactFlowNumber(5 - float64(count)), EvidenceRefs: []string{p.Digest}}}
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	step := f.commit(t, view, tool, raw)
	versions := make([]string, len(ids))
	for i, id := range ids {
		versions[i] = artifactFlowResource(t, step.AfterState(), id).Artifact.VersionDigest
	}
	peer := f.observation(t, f.peer)
	if len(peer.ArtifactViews) != 0 || strings.Contains(incomingObservationJSON(t, peer), incomingWrittenClaim) {
		t.Fatal("predecessor artifact leaked before actual grant/read")
	}
	return f, ids, versions
}

func incomingObservationJSON(t *testing.T, o domain.CharacterObservationPacket) string {
	t.Helper()
	raw, err := json.Marshal(o)
	artifactFlowMust(t, err)
	return string(raw)
}

func incomingReadArgs(o domain.CharacterObservationPacket, sender string, enabled bool) map[string]any {
	args := artifactFlowSubmitArgs(o, "read_given_record")
	args["decision"] = "读取对方实际交来且有权限的文书"
	args["intended_action"] = "若对方本轮实际交来唯一可读文书，则投入本人实际工时读取；未送达则不声称读过"
	tasks := args["self_tasks"].([]domain.CharacterSelfTaskV2)
	tasks[0].Action = "在实际获准且文书已到场后读取该份文书"
	args["self_tasks"] = tasks
	request := domain.ResourceReadRequestV2{IncomingDeliveryFrom: sender}
	if enabled {
		request.TaskID = tasks[0].TaskID
	}
	args["resource_reads"] = []domain.ResourceReadRequestV2{request}
	return args
}

func incomingGrantAndRead(t *testing.T, f *artifactFlowFixture, ids, versions []string, enabled bool, requestSender string) (*store.CharacterArbitrationV3, *ResolveChapterWorldTool, map[string]any) {
	t.Helper()
	view := f.prepare(t)
	authorArgs := artifactFlowSubmitArgs(f.observation(t, f.author), "show_existing_records")
	authorArgs["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = append([]string(nil), ids...)
	var grants []domain.CharacterArtifactAccessIntentV1
	for i, id := range ids {
		grants = append(grants, domain.CharacterArtifactAccessIntentV1{ResourceID: id, VersionDigest: versions[i], ToCharacter: "乙", Access: "shared"})
	}
	authorArgs["artifact_access"] = grants
	f.submit(t, view, f.author, authorArgs)
	f.submit(t, view, f.peer, incomingReadArgs(f.observation(t, f.peer), requestSender, enabled))
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	args := artifactFlowArbiterArgs(scope)
	clock := args["story_time"].(domain.StoryTimeChapterSchedule)
	arrival := clock.StartDay + 1.0/1440
	readerEnd := arrival + 1.0/1440
	var sent, received []map[string]any
	var deliveries []domain.ResourceDeliveryV2
	sender := artifactFlowProposal(t, scope, f.author)
	for i, id := range ids {
		sent = append(sent, map[string]any{"resource_id": id, "access": "shared", "evidence_refs": []string{sender.Digest}})
		received = append(received, map[string]any{"resource_id": id, "access": "shared", "perception": domain.ResourcePerceptionV2{Kind: "unknown", AsOfChapter: 1}, "evidence_refs": []string{sender.Digest}})
		delivery := domain.ResourceDeliveryV2{ResourceID: id, ArtifactVersionDigest: versions[i], FromAgentID: f.author, ToAgentID: f.peer, SourceProposalDigest: sender.Digest, Access: "shared", ReceivedFields: []string{}, EvidenceRefs: []string{sender.Digest}}
		if enabled {
			delivery.DeliveredAtDay = &arrival
		}
		deliveries = append(deliveries, delivery)
	}
	args["resource_deliveries"] = deliveries
	artifactFlowResolution(t, args, f.author)["post_state"] = map[string]any{"location": "柜台", "resource_updates": sent}
	peer := artifactFlowResolution(t, args, f.peer)
	peer["post_state"] = map[string]any{"location": "柜台", "resource_updates": received}
	if enabled {
		clock.EndDay = readerEnd
		args["story_time"] = clock
		peer["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "read_given_record", Status: "completed", StartDay: &arrival, EndDay: &readerEnd}}
		peer["artifact_read_results"] = []domain.CharacterArtifactReadResultV1{{ResourceID: ids[0], VersionDigest: versions[0], ClaimIDs: []string{"statement"}, AtDay: readerEnd}}
	} else {
		// Old protocol cannot turn a legacy incoming resource read into a
		// version-specific artifact read: retain the chosen but unexecuted task.
		peer["outcome"], peer["completion_state"] = "partial", "in_progress"
		peer["immediate_result"] = "本轮只实际取得共享访问，原读取意图尚未执行"
		peer["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "read_given_record", Status: "not_started"}}
	}
	tool, err := NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
	artifactFlowMust(t, err)
	return view, tool, args
}

func TestIncomingArtifactReadRealToolsEliminateOneDecisionCycle(t *testing.T) {
	var finishDays []float64
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("incoming_policy=%v", enabled), func(t *testing.T) {
			f, ids, versions := incomingReadFixture(t, enabled, 1)
			startCycles := len(f.session.CycleDigests)
			view, tool, args := incomingGrantAndRead(t, f, ids, versions, enabled, "甲")
			step := f.commit(t, view, tool, args)
			peer := f.observation(t, f.peer)
			if !enabled {
				if len(peer.ArtifactViews) != 1 || peer.ArtifactViews[0].KnowledgeKind != "unread" || len(peer.ArtifactViews[0].Claims) != 0 {
					t.Fatal("old access-only grant acquired unread content")
				}
				view = f.prepare(t)
				reader := incomingReadArgs(peer, "甲", false)
				delete(reader, "resource_reads")
				reader["self_tasks"].([]domain.CharacterSelfTaskV2)[0].ResourceIDs = []string{ids[0]}
				reader["artifact_reads"] = []domain.CharacterArtifactReadVersionV1{{TaskID: "read_given_record", ResourceID: ids[0], VersionDigest: versions[0]}}
				f.submit(t, view, f.peer, reader)
				wait := artifactFlowSubmitArgs(f.observation(t, f.author), "author_waits_for_read")
				f.submit(t, view, f.author, wait)
				scope, err := view.Sources(1)
				artifactFlowMust(t, err)
				args = artifactFlowArbiterArgs(scope)
				end := args["story_time"].(domain.StoryTimeChapterSchedule).EndDay
				artifactFlowResolution(t, args, f.peer)["artifact_read_results"] = []domain.CharacterArtifactReadResultV1{{ResourceID: ids[0], VersionDigest: versions[0], ClaimIDs: []string{"statement"}, AtDay: end}}
				// Waiting is not extra effective work and incurs no made-up task completion.
				author := artifactFlowResolution(t, args, f.author)
				author["outcome"], author["completion_state"] = "partial", "in_progress"
				author["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "author_waits_for_read", Status: "not_started"}}
				tool, err = NewResolveCharacterArbitrationV3Tool(f.st, f.session, view, 1, artifactFlowProtocol)
				artifactFlowMust(t, err)
				step = f.commit(t, view, tool, args)
				peer = f.observation(t, f.peer)
			}
			if len(peer.ArtifactViews) != 1 || peer.ArtifactViews[0].KnowledgeKind != "read" || len(peer.ArtifactViews[0].Claims) != 1 || peer.ArtifactViews[0].Claims[0].Text != incomingVisibleClaim || peer.ArtifactViews[0].Claims[0].EpistemicKind != "self_statement" || peer.ArtifactViews[0].VersionDigest != versions[0] {
				t.Fatalf("actual version-specific reading not retained after Store reload: %+v", peer.ArtifactViews)
			}
			encoded := incomingObservationJSON(t, peer)
			if strings.Contains(encoded, "PRIVATE_AUTHOR_UNWRITTEN") || strings.Contains(encoded, "AUTHOR_PRIVATE_STATE_NOT_KNOWLEDGE") || len(peer.ArtifactViews[0].Signatures) != 0 {
				t.Fatal("incoming reading leaked private context or invented signing")
			}
			used := len(f.session.CycleDigests) - startCycles
			want := 2
			if enabled {
				want = 1
			}
			if used != want {
				t.Fatalf("decision/arbitration/readiness cycles=%d want=%d", used, want)
			}
			prefix, err := store.NewStore(f.st.Dir()).LoadVerifiedCharacterActivationPrefix(f.session.GenerationID, f.session.Chapter)
			artifactFlowMust(t, err)
			readerDecisions := 0
			for _, proof := range prefix.Steps()[startCycles:] {
				for _, p := range proof.EffectiveProposals() {
					if p.AgentID == f.peer {
						readerDecisions++
					}
				}
			}
			if readerDecisions != want || len(f.session.ReadinessDigests)-startCycles != want {
				t.Fatal("saved evidence did not demonstrate the removed reader decision/assessment round")
			}
			t.Logf("persisted reader decisions=%d arbitration/readiness cycles=%d; actual elapsed day=%g", readerDecisions, used, f.session.CurrentDay)
			if *artifactFlowResource(t, step.AfterState(), artifactFlowPaperID).ActualAmount != 4 {
				t.Fatal("reading consumed or duplicated creation materials")
			}
			finishDays = append(finishDays, f.session.CurrentDay)
		})
	}
	if len(finishDays) != 2 || finishDays[0] != finishDays[1] {
		t.Fatalf("saved one orchestration round by changing actual elapsed time: %v", finishDays)
	}
}

func incomingCopyArgs(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	copy := artifactFlowCopy(t, args)
	copy["resolutions"] = artifactFlowCopy(t, args["resolutions"].([]map[string]any))
	return copy
}

func TestIncomingArtifactReadRejectsUnfulfilledOrMismatchedDeliveryWithoutWrites(t *testing.T) {
	f, ids, versions := incomingReadFixture(t, true, 1)
	view, tool, args := incomingGrantAndRead(t, f, ids, versions, true, "甲")
	scope, err := view.Sources(1)
	artifactFlowMust(t, err)
	originalProposals := scope.EffectiveProposals()
	clock := args["story_time"].(domain.StoryTimeChapterSchedule)
	for _, mode := range []string{"no-delivery", "wrong-sender", "late-delivery", "no-time", "wrong-version", "missing-delivery-time", "no-grant", "no-reader-work", "unrelated-sender-work"} {
		t.Run(mode, func(t *testing.T) {
			bad := incomingCopyArgs(t, args)
			deliveries := artifactFlowCopy(t, args["resource_deliveries"].([]domain.ResourceDeliveryV2))
			peer := artifactFlowResolution(t, bad, f.peer)
			switch mode {
			case "no-delivery":
				deliveries = nil
				peer["post_state"] = map[string]any{"location": "柜台", "resource_updates": []any{}}
				artifactFlowResolution(t, bad, f.author)["post_state"] = map[string]any{"location": "柜台", "resource_updates": []any{}}
			case "wrong-sender":
				deliveries[0].FromAgentID = f.peer
			case "late-delivery":
				deliveries[0].DeliveredAtDay = artifactFlowNumber(clock.EndDay)
			case "no-time":
				peer["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "read_given_record", Status: "completed"}}
			case "no-reader-work":
				peer["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "read_given_record", Status: "not_started"}}
			case "wrong-version":
				peer["artifact_read_results"] = []domain.CharacterArtifactReadResultV1{{ResourceID: ids[0], VersionDigest: "sha256:" + strings.Repeat("f", 64), ClaimIDs: []string{"statement"}, AtDay: clock.EndDay}}
			case "missing-delivery-time":
				deliveries[0].DeliveredAtDay = nil
			case "no-grant":
				deliveries[0].Access = "none"
			case "unrelated-sender-work":
				artifactFlowResolution(t, bad, f.author)["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "show_existing_records", Status: "not_started"}}
			}
			bad["resource_deliveries"] = deliveries
			f.rejectArbitration(t, tool, bad)
		})
	}
	// All rejected attempts leave the exact original request available; a
	// subsequent valid submit uses no replacement character decision.
	step := f.commit(t, view, tool, args)
	if !reflect.DeepEqual(originalProposals, step.EffectiveProposals()) {
		t.Fatal("corrected arbitration replaced the original authorized character proposals")
	}
	peer := f.observation(t, f.peer)
	if len(peer.ArtifactViews) != 1 || peer.ArtifactViews[0].KnowledgeKind != "read" {
		t.Fatal("original authorized request did not succeed after rejected candidates")
	}
}

func TestIncomingArtifactReadRejectsAmbiguityAndOldPolicyNewFields(t *testing.T) {
	t.Run("two-actual-incoming-artifacts", func(t *testing.T) {
		f, ids, versions := incomingReadFixture(t, true, 2)
		_, tool, args := incomingGrantAndRead(t, f, ids, versions, true, "甲")
		f.rejectArbitration(t, tool, args)
	})
	t.Run("different-declared-sender", func(t *testing.T) {
		f, ids, versions := incomingReadFixture(t, true, 1)
		_, tool, args := incomingGrantAndRead(t, f, ids, versions, true, "没有交付的另一人")
		f.rejectArbitration(t, tool, args)
	})
	t.Run("old-policy", func(t *testing.T) {
		f, ids, versions := incomingReadFixture(t, false, 1)
		view := f.prepare(t)
		f.rejectSubmit(t, view, f.peer, incomingReadArgs(f.observation(t, f.peer), "甲", true))
		_, tool, args := incomingGrantAndRead(t, f, ids, versions, false, "甲")
		deliveries := args["resource_deliveries"].([]domain.ResourceDeliveryV2)
		deliveries[0].DeliveredAtDay = artifactFlowNumber(args["story_time"].(domain.StoryTimeChapterSchedule).EndDay)
		args["resource_deliveries"] = deliveries
		f.rejectArbitration(t, tool, args)
	})
	t.Run("new-policy-does-not-upgrade-unbound-old-intent", func(t *testing.T) {
		f, ids, versions := incomingReadFixture(t, true, 1)
		_, tool, args := incomingGrantAndRead(t, f, ids, versions, false, "甲")
		clock := args["story_time"].(domain.StoryTimeChapterSchedule)
		arrival := clock.EndDay
		clock.EndDay += 1.0 / 1440
		args["story_time"] = clock
		deliveries := args["resource_deliveries"].([]domain.ResourceDeliveryV2)
		deliveries[0].DeliveredAtDay = &arrival
		args["resource_deliveries"] = deliveries
		peer := artifactFlowResolution(t, args, f.peer)
		peer["outcome"], peer["completion_state"] = "success", "completed"
		peer["self_executions"] = []domain.CharacterSelfExecutionV2{{TaskID: "read_given_record", Status: "completed", StartDay: &arrival, EndDay: &clock.EndDay}}
		peer["artifact_read_results"] = []domain.CharacterArtifactReadResultV1{{ResourceID: ids[0], VersionDigest: versions[0], ClaimIDs: []string{"statement"}, AtDay: clock.EndDay}}
		f.rejectArbitration(t, tool, args)
	})
}
