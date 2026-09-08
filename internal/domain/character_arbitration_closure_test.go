package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A disagreement can have a completely determined unsuccessful outcome. In
// particular, resolution of a location conflict is not permission to rewrite
// either participant's destination, make them meet, or grant document access.
func arbitrationMissedMeetingFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f, _ := newSelfChronologyFixture(t)
	f.receipt.Round = 2 // The sole character revision has already been used.
	f.receipt.StoryTime.EndDay = 2.0 / 1440
	f.proposals[0].Decision = "携原件到柜台，另一人实际到场时出示并核读"
	f.proposals[0].IntendedAction = "携本人记录前往柜台；仅在乙到场后进行本次共同查阅。"
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{
		{TaskID: "carry-document", Kind: "carry", Action: "携本人记录到柜台", ResourceIDs: []string{selfDocumentTestID}, KnowledgeRefs: []string{"known-ca_a"}},
		{TaskID: "present-document", Kind: "work", Action: "乙实际到达柜台时出示记录并共同查阅", KnowledgeRefs: []string{"known-ca_a"}},
	}
	f.proposals[1].Decision = "前往固定记录夹，实际获准出示后核读"
	f.proposals[1].IntendedAction = "前往固定记录夹；仅在甲在那里实际出示和许可后核读。"
	f.proposals[1].SelfTasks = []CharacterSelfTaskV2{
		{TaskID: "approach-clip", Kind: "work", Action: "安全前往固定记录夹", KnowledgeRefs: []string{"known-ca_b"}},
		{TaskID: "review-document", Kind: "work", Action: "甲在固定记录夹实际出示和许可后核读", KnowledgeRefs: []string{"known-ca_b"}},
	}
	f.proposals[1].ResourceReads = []ResourceReadRequestV2{{IncomingDeliveryFrom: "甲"}}
	for i := range f.proposals {
		f.observations[i].Round = 2
		f.observations[i].ConflictFeedback = []string{"本轮当面出示地点不一致，请提交本人的一次修订。"}
		f.proposals[i].Round = 2
		f.receipt.Resolutions[i].Decision = f.proposals[i].Decision
		f.receipt.Resolutions[i].IntendedAction = f.proposals[i].IntendedAction
		f.receipt.Resolutions[i].Outcome = "partial"
		f.receipt.Resolutions[i].CompletionState = "in_progress"
		f.receipt.Resolutions[i].ImmediateResult = "实际到达本人选择的地点，但双方未会合，共同查阅没有发生。"
		f.receipt.Resolutions[i].StateAfter = "地点分歧已裁定为本轮展示与核读受阻；没有新增文档事实或阅读权限。"
		f.receipt.Resolutions[i].ConflictIDs = []string{"presentation-location"}
	}
	f.receipt.Resolutions[0].PostState.Location = "柜台"
	f.receipt.Resolutions[1].PostState.Location = "固定记录夹"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{
		{TaskID: "carry-document", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(2)},
		{TaskID: "present-document", Status: "blocked"},
	}
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{
		{TaskID: "approach-clip", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(2)},
		{TaskID: "review-document", Status: "blocked"},
	}
	f.receipt.Conflicts = []WorldArbitrationConflict{{ID: "presentation-location", Kind: "location", AffectedAgentIDs: []string{"ca_a", "ca_b"}, Resolved: true,
		Feedback: "双方目的地仍不同；已经裁定为各自抵达、展示和核读受阻，不是人物达成一致。"}}
	f.receipt.ProtagonistProjection.ChosenDecision = f.proposals[0].Decision
	rebindPhysicalTestStimulus(t, &f)
	return f
}

func TestFinalArbitrationMayCloseMissedMeetingAsPartialWithoutInventingRead(t *testing.T) {
	f := arbitrationMissedMeetingFixture(t)
	input, _ := json.Marshal(struct {
		Stimulus  WorldStimulusPacket
		Proposals []CharacterDecisionProposal
	}{f.stimulus, f.proposals})
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatalf("final round confused a determined blocked subtask with an unresolved world outcome: %v", err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Finalized || !r.Conflicts[0].Resolved || r.HardContractStatus != "feasible" || len(r.ResourceDeliveries) != 0 || after.Actors[0].Location != "柜台" || after.Actors[1].Location != "固定记录夹" {
		t.Fatal("closure invented a meeting or delivery, or did not preserve the actual separate destinations")
	}
	for i, actor := range after.Actors {
		if r.Resolutions[i].Decision != f.proposals[i].Decision || r.Resolutions[i].IntendedAction != f.proposals[i].IntendedAction || r.Resolutions[i].Outcome != "partial" || len(actor.ReceivedFacts) != 0 {
			t.Fatal("arbitration rewrote an intent or converted a failed read request into received knowledge")
		}
		active, blocked := 0, 0
		for _, fact := range actor.SelfExperiences {
			switch fact.Status {
			case "completed":
				active++
				if fact.StartDay == nil || fact.EndDay == nil {
					t.Fatal("actual movement lacks its execution interval")
				}
			case "blocked":
				blocked++
				if fact.StartDay != nil || fact.EndDay != nil {
					t.Fatal("unperformed reading acquired fictitious execution time")
				}
			}
		}
		if active != 1 || blocked != 1 {
			t.Fatalf("lost actual movement or the blocked task: %+v", actor.SelfExperiences)
		}
		private, err := CharacterPrivateOutcomeV2(f.proposals[i], r.Resolutions[i], after, r)
		if err != nil || strings.Contains(private, "秘密余额11.8") {
			t.Fatalf("unperformed read leaked its document content: %v", err)
		}
	}
	unchanged, _ := json.Marshal(struct {
		Stimulus  WorldStimulusPacket
		Proposals []CharacterDecisionProposal
	}{f.stimulus, f.proposals})
	if !bytes.Equal(input, unchanged) {
		t.Fatal("closure changed the source state or the original revised proposals")
	}
	raw, _ := json.Marshal(r)
	var restored WorldArbitrationReceipt
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	replayed, err := FinalizeWorldArbitrationReceipt(restored, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || replayed.Digest != r.Digest {
		t.Fatalf("closed partial result cannot verify after reload: %v", err)
	}
	got, _ := json.Marshal(replayed)
	if !bytes.Equal(raw, got) {
		t.Fatal("reloading a closed partial outcome changed its evidence bytes")
	}

	for _, tc := range []struct {
		name string
		edit func(*physicalProtocolFixture)
		want string
	}{
		{"still unresolved", func(f *physicalProtocolFixture) { f.receipt.Conflicts[0].Resolved = false }, "unresolved conflicts"},
		{"third revision forbidden", func(f *physicalProtocolFixture) { f.receipt.Finalized = false; f.receipt.Conflicts[0].Resolved = false }, "exhausted revision rounds"},
		{"intent cannot change", func(f *physicalProtocolFixture) { f.receipt.Resolutions[1].IntendedAction = "改去柜台" }, "rewrote intent"},
		{"whole actor blocked cannot move", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].Outcome = "blocked" }, "blocked actor"},
		{"unexecuted read has no interval", func(f *physicalProtocolFixture) { f.receipt.Resolutions[1].SelfExecutions[1].StartDay = selfTestDay(0) }, "unexecuted self task"},
		{"failed read cannot manufacture document knowledge", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{{Kind: "document_statement", Text: "未被本人读取的秘密余额11.8。", SourceType: "resource_read", SourceID: "hidden-number", SourceProposalDigest: f.proposals[1].Digest, ResourceID: selfDocumentTestID, Chapter: 1}}
		}, "authorized document read"},
		{"hard conflict cannot finalize", func(f *physicalProtocolFixture) {
			f.receipt.HardContractStatus = "infeasible"
			f.receipt.HardContractConflicts = []string{"硬义务已不可实现"}
		}, "infeasible arbitration cannot finalize"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := arbitrationMissedMeetingFixture(t)
			tc.edit(&changed)
			if _, err := finalizePhysicalFixture(changed); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("closure weakened its original guard: %v (want %q)", err, tc.want)
			}
		})
	}
}
