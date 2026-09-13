package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type incomingBindingFixtureV1 struct {
	before, after WorldPhysicalStateV2
	stimulus      WorldStimulusPacket
	receipt       WorldArbitrationReceipt
	proposals     map[string]CharacterDecisionProposal
	result        CharacterArtifactReadResultV1
}

func incomingBindingFixture(t *testing.T) incomingBindingFixtureV1 {
	t.Helper()
	before, index := artifactShapeFixtureV1(t)
	before.Actors[1].Location = "柜台"
	before.Actors = append(before.Actors, CharacterPhysicalStateV2{AgentID: "ca_idle", Character: "丙", Location: "柜台"})
	after := artifactShapeCopyV1(t, before)
	resource := before.Resources[index]
	after.Actors[1].Resources[0].Access = "shared"
	sender := CharacterDecisionProposal{AgentID: "ca_author", Character: "甲", Location: "柜台", Digest: "sha256:" + strings.Repeat("b", 64), SelfTasks: []CharacterSelfTaskV2{{TaskID: "grant", Kind: "work", ResourceIDs: []string{resource.ResourceID}}}, ArtifactAccess: []CharacterArtifactAccessIntentV1{{ResourceID: resource.ResourceID, VersionDigest: resource.Artifact.VersionDigest, ToCharacter: "乙", Access: "shared"}}}
	reader := CharacterDecisionProposal{AgentID: "ca_reader", Character: "乙", Location: "柜台", Digest: "sha256:" + strings.Repeat("c", 64), SelfTasks: []CharacterSelfTaskV2{{TaskID: "read", Kind: "work"}}, ResourceReads: []ResourceReadRequestV2{{IncomingDeliveryFrom: "甲", TaskID: "read"}}}
	grant := CharacterDecisionResolution{AgentID: sender.AgentID, Character: sender.Character, ProposalDigest: sender.Digest, Outcome: "success", CompletionState: "completed", PostState: &after.Actors[0], SelfExecutions: []CharacterSelfExecutionV2{{TaskID: "grant", Status: "completed", StartDay: physicalTestNumber(.1), EndDay: physicalTestNumber(.2)}}}
	read := CharacterDecisionResolution{AgentID: reader.AgentID, Character: reader.Character, ProposalDigest: reader.Digest, Outcome: "success", CompletionState: "completed", PostState: &after.Actors[1], SelfExecutions: []CharacterSelfExecutionV2{{TaskID: "read", Status: "completed", StartDay: physicalTestNumber(.2), EndDay: physicalTestNumber(.3)}}}
	return incomingBindingFixtureV1{before: before, after: after, stimulus: WorldStimulusPacket{Sources: []string{CharacterIncomingMaterialReadPolicyV1}},
		receipt:   WorldArbitrationReceipt{Finalized: true, StoryTime: &StoryTimeChapterSchedule{StartDay: .1, EndDay: .3}, Resolutions: []CharacterDecisionResolution{grant, read}, ResourceDeliveries: []ResourceDeliveryV2{{ResourceID: resource.ResourceID, ArtifactVersionDigest: resource.Artifact.VersionDigest, DeliveredAtDay: physicalTestNumber(.2), FromAgentID: sender.AgentID, ToAgentID: reader.AgentID, SourceProposalDigest: sender.Digest, Access: "shared"}}},
		proposals: map[string]CharacterDecisionProposal{sender.AgentID: sender, reader.AgentID: reader}, result: CharacterArtifactReadResultV1{ResourceID: resource.ResourceID, VersionDigest: resource.Artifact.VersionDigest, ClaimIDs: []string{"original_field"}, AtDay: .3}}
}

func (f incomingBindingFixtureV1) bind() (bool, error) {
	return bindIncomingMaterialArtifactReadV1(f.receipt, f.stimulus, f.before, f.after, f.proposals, f.proposals["ca_reader"], f.receipt.Resolutions[1], f.result)
}

func TestIncomingMaterialReadIndependentConditionsAndReadableSelection(t *testing.T) {
	for _, other := range []string{"丙", "不存在的发送者", "重名者"} {
		t.Run(other, func(t *testing.T) {
			f := incomingBindingFixture(t)
			if other == "重名者" {
				f.before.Actors = append(f.before.Actors, CharacterPhysicalStateV2{AgentID: "ca_x", Character: other}, CharacterPhysicalStateV2{AgentID: "ca_y", Character: other})
			}
			p := f.proposals["ca_reader"]
			p.ResourceReads = append([]ResourceReadRequestV2{{IncomingDeliveryFrom: other, TaskID: "read"}}, p.ResourceReads...)
			f.proposals[p.AgentID] = p
			if bound, err := f.bind(); err != nil || !bound {
				t.Fatalf("untriggered condition blocked real delivery: %v", err)
			}
		})
	}
	f := incomingBindingFixture(t)
	// A pen is not a second readable material. A second real document is.
	pen := WorldResourceBalanceV2{ResourceID: "res_0000000000000077", Name: "pen"}
	f.before.Resources = append(f.before.Resources, pen)
	f.receipt.ResourceDeliveries = append(f.receipt.ResourceDeliveries, ResourceDeliveryV2{ResourceID: pen.ResourceID, FromAgentID: "ca_author", ToAgentID: "ca_reader", Access: "shared"})
	if bound, err := f.bind(); err != nil || !bound {
		t.Fatalf("non-readable pen made incoming material ambiguous: %v", err)
	}
	f.before.Resources[len(f.before.Resources)-1].ReadableFacts = []ResourceReadableFactV2{{ID: "existing-note", Text: "synthetic readable record"}}
	if bound, err := f.bind(); err == nil || bound {
		t.Fatal("second readable document was arbitrarily ignored")
	}
}

func TestIncomingMaterialReadRequiresProvenPositionAtDelivery(t *testing.T) {
	f := incomingBindingFixture(t)
	f.before.Actors[1].Location = "门口"
	p := f.proposals["ca_reader"]
	p.Location = "门口"
	p.SelfTasks = append(p.SelfTasks, CharacterSelfTaskV2{TaskID: "arrive", Kind: "carry", ResourceIDs: []string{artifactShapeOtherID}})
	f.proposals[p.AgentID] = p
	f.receipt.Resolutions[1].SelfExecutions = append(f.receipt.Resolutions[1].SelfExecutions, CharacterSelfExecutionV2{TaskID: "arrive", Status: "completed", StartDay: physicalTestNumber(.1), EndDay: physicalTestNumber(.15)})
	if bound, err := f.bind(); err != nil || !bound {
		t.Fatalf("explicit prior arrival could not support incoming reading: %v", err)
	}
	for _, change := range []string{"no_route", "in_transit", "late_read_start", "unexecuted_read", "no_grant_work", "grant_spans_movement", "changed_version"} {
		t.Run(change, func(t *testing.T) {
			c := incomingBindingFixture(t)
			switch change {
			case "no_route":
				c.before.Actors[1].Location = "门口"
				p := c.proposals["ca_reader"]
				p.Location = "门口"
				c.proposals[p.AgentID] = p
			case "in_transit":
				c = f
				c.receipt = artifactShapeCopyV1(t, f.receipt)
				c.receipt.ResourceDeliveries[0].DeliveredAtDay = physicalTestNumber(.12)
			case "late_read_start":
				c.receipt.ResourceDeliveries[0].DeliveredAtDay = physicalTestNumber(.21)
			case "unexecuted_read":
				c.receipt.Resolutions[1].SelfExecutions[0].Status = "not_started"
			case "no_grant_work":
				c.receipt.Resolutions[0].SelfExecutions = nil
			case "grant_spans_movement":
				c.before.Actors[0].Location = "门口"
				c.before.Resources[3].Artifact.Placement.Location = "门口"
				p := c.proposals["ca_author"]
				p.Location = "门口"
				p.SelfTasks = append(p.SelfTasks, CharacterSelfTaskV2{TaskID: "move", Kind: "carry", ResourceIDs: []string{c.result.ResourceID}})
				c.proposals[p.AgentID] = p
				c.receipt.Resolutions[0].SelfExecutions = append(c.receipt.Resolutions[0].SelfExecutions, CharacterSelfExecutionV2{TaskID: "move", Status: "completed", StartDay: physicalTestNumber(.12), EndDay: physicalTestNumber(.15)})
			case "changed_version":
				c.after.Resources[3].Artifact.VersionDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if bound, err := c.bind(); err == nil || bound {
				t.Fatal("unproven actual delivery/read boundary was accepted")
			}
		})
	}
}

func incomingFoundationFixture(t *testing.T, timed bool) physicalProtocolFixture {
	t.Helper()
	f := artifactFlowFixtureV1(t)
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterIncomingMaterialReadPolicyV1)
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterIncomingMaterialReadPolicyV1)
	}
	// The reused work fixture intentionally makes this object unreadable. Give
	// this synthetic foundation document its actual source before rebinding O.
	state := continuationCloneV1(*f.stimulus.PhysicalState)
	for i := range state.Resources {
		if state.Resources[i].ResourceID == physicalPaperTestID {
			state.Resources[i].ReadableFacts = []ResourceReadableFactV2{{ID: "synthetic-registration", Text: "合成登记纸记载一次交接。"}}
		}
	}
	artifactFlowAdvanceV1(t, &f, state, 1, 0)
	f.receipt.ResourceSettlements = nil
	f.receipt.StoryTime.EndDay = 2.0 / 1440
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{{TaskID: "grant_document", Kind: "work", Action: "实际出示登记纸", ResourceIDs: []string{physicalPaperTestID}, KnowledgeRefs: []string{"known-ca_a"}}}
	var paperName string
	for _, view := range f.observations[0].ResourceViews {
		if view.ResourceID == physicalPaperTestID {
			paperName = view.Name
		}
	}
	f.proposals[0].ResourceReports = []ResourceReportV2{{ResourceID: physicalPaperTestID, ToCharacter: "乙", PerceivedName: paperName, EvidenceRefs: []string{"known-ca_a"}}}
	f.proposals[1].SelfTasks = []CharacterSelfTaskV2{{TaskID: "read_document", Kind: "work", Action: "读取实际送来的材料", KnowledgeRefs: []string{"known-ca_b"}}}
	request := ResourceReadRequestV2{IncomingDeliveryFrom: "甲"}
	if timed {
		request.TaskID = "read_document"
	}
	f.proposals[1].ResourceReads = []ResourceReadRequestV2{request}
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "grant_document", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(1)}}
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "read_document", Status: "completed", StartDay: selfTestDay(1), EndDay: selfTestDay(2)}}
	for i := range f.receipt.Resolutions {
		f.receipt.Resolutions[i].Outcome = "success"
		f.receipt.Resolutions[i].CompletionState = "completed"
	}
	rebindPhysicalTestStimulus(t, &f)
	for i := range f.receipt.Resolutions[0].PostState.Resources {
		if f.receipt.Resolutions[0].PostState.Resources[i].ResourceID == physicalPaperTestID {
			f.receipt.Resolutions[0].PostState.Resources[i].Access = "shared"
		}
	}
	for i := range f.receipt.Resolutions[1].PostState.Resources {
		if f.receipt.Resolutions[1].PostState.Resources[i].ResourceID == physicalPaperTestID {
			f.receipt.Resolutions[1].PostState.Resources[i].Access = "shared"
			f.receipt.Resolutions[1].PostState.Resources[i].EvidenceRefs = []string{f.proposals[0].Digest}
		}
	}
	delivery := ResourceDeliveryV2{ResourceID: physicalPaperTestID, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, Access: "shared", EvidenceRefs: []string{f.proposals[0].Digest}}
	if timed {
		delivery.DeliveredAtDay = selfTestDay(1)
	}
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{delivery}
	var readable ResourceReadableFactV2
	for _, resource := range f.stimulus.PhysicalState.Resources {
		if resource.ResourceID == physicalPaperTestID && len(resource.ReadableFacts) > 0 {
			readable = resource.ReadableFacts[0]
		}
	}
	if readable.ID == "" {
		t.Fatal("existing fixture lacks its real foundation document")
	}
	fact := CharacterReceivedFactV2{Kind: "document_statement", Text: readable.Text, SourceType: "resource_read", SourceID: readable.ID, SourceProposalDigest: f.proposals[1].Digest, ResourceID: physicalPaperTestID, Chapter: 1}
	fact.ID = CharacterReceivedFactIDV2("ca_b", fact)
	f.receipt.Resolutions[1].PostState.ReceivedFacts = []CharacterReceivedFactV2{fact}
	return f
}

func TestIncomingMaterialReadFoundationUsesDeclaredWorkWithoutChangingLegacy(t *testing.T) {
	for _, timed := range []bool{false, true} {
		f := incomingFoundationFixture(t, timed)
		r, state := artifactFlowApplyV1(t, f, map[string]string{})
		if len(state.Actors[1].ReceivedFacts) != 1 || state.Actors[1].ReceivedFacts[0].SourceProposalDigest != f.proposals[1].Digest || r.StoryTime.EndDay != 2.0/1440 {
			t.Fatal("foundation source or actual work interval changed")
		}
	}
	for _, change := range []string{"missing_delivery_time", "read_before_delivery", "read_not_executed", "missing_source_report"} {
		t.Run(change, func(t *testing.T) {
			f := incomingFoundationFixture(t, true)
			switch change {
			case "missing_delivery_time":
				f.receipt.ResourceDeliveries[0].DeliveredAtDay = nil
			case "read_before_delivery":
				f.receipt.Resolutions[1].SelfExecutions[0].StartDay = selfTestDay(.5)
			case "read_not_executed":
				f.receipt.Resolutions[1].SelfExecutions[0] = CharacterSelfExecutionV2{TaskID: "read_document", Status: "not_started"}
			case "missing_source_report":
				f.proposals[0].ResourceReports = nil
				artifactFlowRebindV1(t, &f)
				f.receipt.ResourceDeliveries[0].SourceProposalDigest = f.proposals[0].Digest
				f.receipt.ResourceDeliveries[0].EvidenceRefs = []string{f.proposals[0].Digest}
				for i := range f.receipt.Resolutions[1].PostState.Resources {
					if f.receipt.Resolutions[1].PostState.Resources[i].ResourceID == physicalPaperTestID {
						f.receipt.Resolutions[1].PostState.Resources[i].EvidenceRefs = []string{f.proposals[0].Digest}
					}
				}
			}
			want := map[string]string{"missing_delivery_time": "actual delivery/read time", "read_before_delivery": "starting after delivery", "read_not_executed": "declared actual work", "missing_source_report": "original sender's exact grant/report"}[change]
			if _, err := finalizeWorldArbitrationReceiptWithPriorSources(f.receipt, f.stimulus, f.activation, f.proposals, 1, map[string]string{}); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("task_id foundation variant rejected for the wrong reason or passed: want %q, got %v", want, err)
			}
		})
	}
}

func TestIncomingMaterialReadNilFieldsKeepHistoricalEncoding(t *testing.T) {
	read := ResourceReadRequestV2{IncomingDeliveryFrom: "甲"}
	raw, _ := json.Marshal(read)
	if string(raw) != `{"incoming_delivery_from":"甲"}` {
		t.Fatalf("old read encoding changed: %s", raw)
	}
	delivery := ResourceDeliveryV2{ResourceID: "r", FromAgentID: "a", ToAgentID: "b", SourceProposalDigest: "p", ReceivedFields: []string{}, Access: "shared", EvidenceRefs: []string{"p"}}
	raw, _ = json.Marshal(delivery)
	if bytes.Contains(raw, []byte("delivered_at_day")) {
		t.Fatal("nil timestamp changed old receipt bytes")
	}
	p := CharacterDecisionProposal{Character: "乙", ResourceReads: []ResourceReadRequestV2{{IncomingDeliveryFrom: "甲", TaskID: "read"}}, SelfTasks: []CharacterSelfTaskV2{{TaskID: "read", Kind: "work"}}}
	if err := validateIncomingMaterialReadIntentV1(p, CharacterObservationPacket{}); err == nil {
		t.Fatal("old producer accepted new task_id")
	}
	if err := validateIncomingMaterialReadReceiptFieldsV1(WorldArbitrationReceipt{Finalized: true, StoryTime: &StoryTimeChapterSchedule{EndDay: 1}, ResourceDeliveries: []ResourceDeliveryV2{{DeliveredAtDay: physicalTestNumber(0)}}}, WorldStimulusPacket{}); err == nil {
		t.Fatal("old producer accepted timed delivery")
	}
}
