package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

const artifactPaperStockTestV1 = "res_0000000000000005"

func artifactFlowFixtureV1(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f, _ := newSelfChronologyFixture(t)
	state := continuationCloneV1(*f.stimulus.PhysicalState)
	state.Resources = append(state.Resources, WorldResourceBalanceV2{ResourceID: artifactPaperStockTestV1, Name: "纸张库存", Unit: "张", ActualAmount: physicalTestNumber(5)})
	state.Actors[0].Resources = append(state.Actors[0].Resources, CharacterResourceHoldingV2{ResourceID: artifactPaperStockTestV1, PerceivedName: "纸张", PerceivedLabel: "纸张", PerceivedUnit: "张", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "estimated", EstimateMin: physicalTestNumber(5), EstimateMax: physicalTestNumber(5), EvidenceRefs: []string{"paper-source"}}, EvidenceRefs: []string{"paper-source"}})
	state.Actors[1].Location = state.Actors[0].Location
	for i := range state.Actors {
		state.Actors[i].SelfChronologyBaseline = nil
	}
	var err error
	state, err = PrepareCharacterSelfChronologyStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	f.stimulus.PhysicalState = &state
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterWorkArtifactPolicyV1, CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1)
	for i := range f.observations {
		f.observations[i].Sources = append(f.observations[i].Sources, CharacterWorkArtifactPolicyV1, CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1)
	}
	artifactFlowAdvanceV1(t, &f, state, 1, 0)
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{{TaskID: "write-note", Kind: "work", Action: "写下本人有限确认与待核声明", ResourceIDs: []string{artifactPaperStockTestV1}, ProgressUnit: "minute", ProgressTarget: physicalTestNumber(2), KnowledgeRefs: []string{"known-ca_a"}, OutputRequests: []CharacterWorkOutputRequestV1{{OutputKey: "note", Label: "有来源的更正附件", MaterialInputs: []CharacterWorkMaterialInputV1{{ResourceID: artifactPaperStockTestV1, Amount: 1}}, Claims: []CharacterWorkArtifactClaimV1{{ClaimID: "c1", Text: "本人仅记录已经看到的内容。", EpistemicKind: "self_statement"}, {ClaimID: "c2", Text: "最终责任仍待核验。", EpistemicKind: "pending"}}}}}}
	f.receipt.Resolutions[0].Outcome = "partial"
	f.receipt.Resolutions[0].CompletionState = "in_progress"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "write-note", Status: "in_progress", StartDay: selfTestDay(0), EndDay: selfTestDay(1), OutputResults: []CharacterWorkOutputResultV1{{OutputKey: "note", Status: "created", AtDay: 1.0 / 1440, ClaimIDs: []string{"c1"}}}}}
	f.receipt.ResourceSettlements = []ResourceSettlementV2{{ResourceID: artifactPaperStockTestV1, Before: physicalTestNumber(5), Delta: physicalTestNumber(-1), After: physicalTestNumber(4)}}
	artifactFlowRebindV1(t, &f)
	f.receipt.ResourceSettlements[0].EvidenceRefs = []string{f.proposals[0].Digest}
	return f
}

func artifactFlowAdvanceV1(t *testing.T, f *physicalProtocolFixture, state WorldPhysicalStateV2, cycle int, startMinute float64) {
	t.Helper()
	var err error
	f.stimulus.PhysicalState = &state
	f.stimulus.StoryClock.CurrentDay = startMinute / 1440
	f.stimulus.StoryClock.Digest = ""
	*f.stimulus.StoryClock, err = FinalizeStoryClockContext(*f.stimulus.StoryClock)
	if err != nil {
		t.Fatal(err)
	}
	ctx := *f.stimulus.SelfEvaluationContext
	ctx.Cycle = cycle
	ctx.CurrentDay = startMinute / 1440
	ctx.BeforePhysicalRoot, err = CharacterPhysicalRootForCycle(state)
	if err != nil {
		t.Fatal(err)
	}
	ctx.PreviousCycleDigest = ""
	if cycle > 1 {
		ctx.PreviousCycleDigest = "sha256:" + strings.Repeat("d", 64)
	}
	ctx.Digest, _ = selfEvaluationContextDigestV1(ctx)
	f.stimulus.SelfEvaluationContext = &ctx
	token, err := CharacterActivationCycleSourceToken(f.stimulus.GenerationID, f.stimulus.Chapter, cycle, ctx.ChapterContextDigest, ctx.PreviousCycleDigest)
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, source := range f.stimulus.Sources {
		if !strings.HasPrefix(source, CharacterActivationCycleSourcePrefix) {
			sources = append(sources, source)
		}
	}
	f.stimulus.Sources = append(sources, token)
	f.stimulus, err = FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.StimulusDigest = f.stimulus.Digest
	f.receipt.Digest = ""
	f.receipt.ResourceSettlements = nil
	f.receipt.ResourceDeliveries = nil
	f.receipt.Conflicts = nil
	f.receipt.Finalized = true
	f.receipt.StoryTime = &StoryTimeChapterSchedule{Chapter: f.stimulus.Chapter, StartDay: startMinute / 1440, EndDay: (startMinute + 1) / 1440}
	for i, actor := range state.Actors {
		o := &f.observations[i]
		o.Location = actor.Location
		o.CycleContext = &CharacterObservationCycleContext{Version: CharacterObservationCyclePolicy, Index: cycle, ChapterContextDigest: ctx.ChapterContextDigest, PreviousCycleDigest: ctx.PreviousCycleDigest, CurrentDay: ctx.CurrentDay}
		o.StimulusDigest = f.stimulus.Digest
		o.ResourceViews, err = BuildCharacterResourceViewsV2(state, actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		o.ArtifactViews, err = BuildCharacterArtifactViewsV1(state, actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		o.SelfExperiences, o.TaskProgress, err = BuildCharacterSelfObservationV2(state, actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		*o, err = FinalizeCharacterObservationPacket(*o)
		if err != nil {
			t.Fatal(err)
		}
		p := &f.proposals[i]
		p.ObservationDigest = o.Digest
		p.Location = o.Location
		p.ArtifactReads = nil
		p.ArtifactSigns = nil
		p.ArtifactAccess = nil
		p.ResourceReads = nil
		p.ResourceReports = nil
		p.SelfTasks = []CharacterSelfTaskV2{{TaskID: "wait", Kind: "work", Action: "等待", KnowledgeRefs: []string{"known-" + actor.AgentID}}}
		f.activation.Entries[i].ObservationDigest = o.Digest
		r := &f.receipt.Resolutions[i]
		r.PostState = &actor
		r.Outcome = "blocked"
		r.CompletionState = "blocked"
		r.ArtifactReadResults = nil
		r.ArtifactSignatures = nil
		r.SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "blocked"}}
	}
	f.activation, err = FinalizeCharacterAgentActivation(f.activation)
	if err != nil {
		t.Fatal(err)
	}
	f.receipt.ActivationDigest = f.activation.Digest
}

func artifactFlowRebindV1(t *testing.T, f *physicalProtocolFixture) {
	t.Helper()
	rebindPhysicalTestProposals(t, f)
}

func artifactFlowApplyV1(t *testing.T, f physicalProtocolFixture, prior map[string]string) (WorldArbitrationReceipt, WorldPhysicalStateV2) {
	t.Helper()
	r, err := finalizeWorldArbitrationReceiptWithPriorSources(f.receipt, f.stimulus, f.activation, f.proposals, 1, prior)
	if err != nil {
		t.Fatal(err)
	}
	state, err := applyArbitrationPhysicalStateWithArtifactSourcesV1(r, f.stimulus, f.proposals, prior)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	var restored WorldArbitrationReceipt
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	checked, err := finalizeWorldArbitrationReceiptWithPriorSources(restored, f.stimulus, f.activation, f.proposals, 1, prior)
	if err != nil || checked.Digest != r.Digest {
		t.Fatalf("artifact receipt replay: %v", err)
	}
	return r, state
}

func TestWorkArtifactV1PartialContinuationGrantReadSignAndReplay(t *testing.T) {
	f := artifactFlowFixtureV1(t)
	if _, err := FinalizeWorldArbitrationReceipt(f.receipt, f.stimulus, f.activation, f.proposals, 1); err == nil {
		t.Fatal("old entry accepted artifact semantics")
	}
	_, state := artifactFlowApplyV1(t, f, map[string]string{})
	id := CharacterWorkArtifactResourceIDV1(f.stimulus.GenerationID, "ca_a", "write-note", "note")
	row, ok := artifactStateResourceV1(state, id)
	if !ok || row.Artifact.Status != "draft" || len(row.Artifact.Claims) != 1 || row.Artifact.Placement.Location != "仓库" {
		t.Fatal("actual partial output did not become one located object")
	}
	original := continuationCloneV1(f.proposals[0])
	originalRaw, _ := json.Marshal(original)
	artifactFlowAdvanceV1(t, &f, state, 2, 1)
	f.proposals[0] = original
	f.receipt.Resolutions[0].Outcome = "success"
	f.receipt.Resolutions[0].CompletionState = "completed"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "write-note", Status: "completed", StartDay: selfTestDay(1), EndDay: selfTestDay(2), OutputResults: []CharacterWorkOutputResultV1{{OutputKey: "note", Status: "updated", AtDay: 2.0 / 1440, ClaimIDs: []string{"c2"}, Complete: true}}}}
	p, err := FinalizeCharacterDecisionProposal(f.proposals[1], f.observations[1])
	if err != nil {
		t.Fatal(err)
	}
	f.proposals[1] = p
	f.receipt.ProposalDigests = []string{original.Digest, p.Digest}
	f.receipt.Resolutions[0].ProposalDigest = original.Digest
	f.receipt.Resolutions[1].ProposalDigest = p.Digest
	if _, err := ApplyArbitrationPhysicalStateV2(f.receipt, f.stimulus, f.proposals...); err == nil {
		t.Fatal("ordinary helper followed an old proposal's output CAS without verified source")
	}
	_, state = artifactFlowApplyV1(t, f, map[string]string{"ca_a": original.Digest})
	afterRaw, _ := json.Marshal(f.proposals[0])
	if string(originalRaw) != string(afterRaw) {
		t.Fatal("continuation rewrote original output intent")
	}
	row, _ = artifactStateResourceV1(state, id)
	version := row.Artifact.VersionDigest
	stock, _ := artifactStateResourceV1(state, artifactPaperStockTestV1)
	if *stock.ActualAmount != 4 || row.Artifact.Revision != 2 || row.Artifact.Status != "complete" || len(row.Artifact.Claims) != 2 {
		t.Fatal("continuation created another artifact or charged materials twice")
	}
	artifactFlowAdvanceV1(t, &f, state, 3, 2)
	f.proposals[0].ArtifactAccess = []CharacterArtifactAccessIntentV1{{ResourceID: id, VersionDigest: version, ToCharacter: "乙", Access: "shared"}}
	f.receipt.Resolutions[0].Outcome = "success"
	f.receipt.Resolutions[0].CompletionState = "completed"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "completed", StartDay: selfTestDay(2), EndDay: selfTestDay(3)}}
	artifactFlowRebindV1(t, &f)
	for i := range f.receipt.Resolutions[0].PostState.Resources {
		if f.receipt.Resolutions[0].PostState.Resources[i].ResourceID == id {
			f.receipt.Resolutions[0].PostState.Resources[i].Access = "shared"
			f.receipt.Resolutions[0].PostState.Resources[i].EvidenceRefs = []string{f.proposals[0].Digest}
		}
	}
	f.receipt.Resolutions[1].PostState.Resources = append(f.receipt.Resolutions[1].PostState.Resources, CharacterResourceHoldingV2{ResourceID: id, Access: "shared", PerceivedName: UnidentifiedResourceNameV2, PerceivedLabel: UnidentifiedResourceNameV2, Perception: ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{f.proposals[0].Digest}})
	f.receipt.ResourceDeliveries = []ResourceDeliveryV2{{ResourceID: id, ArtifactVersionDigest: version, FromAgentID: "ca_a", ToAgentID: "ca_b", SourceProposalDigest: f.proposals[0].Digest, Access: "shared", EvidenceRefs: []string{f.proposals[0].Digest}}}
	_, state = artifactFlowApplyV1(t, f, map[string]string{})
	views, err := BuildCharacterArtifactViewsV1(state, "ca_b")
	if err != nil || len(views) != 1 || views[0].KnowledgeKind != "unread" || len(views[0].Claims) != 0 {
		t.Fatal("access granted unrequested artifact content")
	}
	artifactFlowAdvanceV1(t, &f, state, 4, 3)
	f.proposals[1].SelfTasks[0].ResourceIDs = []string{id}
	f.proposals[1].ArtifactReads = []CharacterArtifactReadVersionV1{{TaskID: "wait", ResourceID: id, VersionDigest: version}}
	f.receipt.Resolutions[1].Outcome = "success"
	f.receipt.Resolutions[1].CompletionState = "completed"
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "completed", StartDay: selfTestDay(3), EndDay: selfTestDay(4)}}
	f.receipt.Resolutions[1].ArtifactReadResults = []CharacterArtifactReadResultV1{{ResourceID: id, VersionDigest: version, ClaimIDs: []string{"c1", "c2"}, AtDay: 4.0 / 1440}}
	artifactFlowRebindV1(t, &f)
	_, state = artifactFlowApplyV1(t, f, map[string]string{})
	artifactFlowAdvanceV1(t, &f, state, 5, 4)
	f.proposals[1].SelfTasks[0].ResourceIDs = []string{id}
	f.proposals[1].ArtifactSigns = []CharacterArtifactSignIntentV1{{TaskID: "wait", ResourceID: id, VersionDigest: version, ClaimIDs: []string{"c1"}, Scope: "只确认实际读到此声明，不替代独立核验"}}
	f.receipt.Resolutions[1].Outcome = "success"
	f.receipt.Resolutions[1].CompletionState = "completed"
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "completed", StartDay: selfTestDay(4), EndDay: selfTestDay(5)}}
	f.receipt.Resolutions[1].ArtifactSignatures = []CharacterArtifactSignatureResultV1{{ResourceID: id, VersionDigest: version, ClaimIDs: []string{"c1"}, Scope: f.proposals[1].ArtifactSigns[0].Scope, AtDay: 5.0 / 1440}}
	artifactFlowRebindV1(t, &f)
	r, state := artifactFlowApplyV1(t, f, map[string]string{})
	row, _ = artifactStateResourceV1(state, id)
	if row.Artifact.VersionDigest != version || len(row.Artifact.Signatures) != 1 || row.Artifact.Signatures[0].SignerAgentID != "ca_b" {
		t.Fatal("signing changed content version or signed as another actor")
	}
	text, err := CharacterPrivateOutcomeV2(f.proposals[1], r.Resolutions[1], state, r)
	if err != nil || !strings.Contains(text, "本人实际签认") || !strings.Contains(text, f.proposals[1].ArtifactSigns[0].Scope) {
		t.Fatalf("actual signed result missing from private memory: %v", err)
	}
	if _, err := ArtifactIndependentSourcesV1(state, []string{id}); err == nil {
		t.Fatal("self/pending artifact became independent factual evidence")
	}
	// A fresh, explicit CAS update may continue over later verified rounds
	// without changing its old request or allocating another sheet. Old
	// signatures remain historical and cannot attest to the new content.
	oldSignature := row.Artifact.Signatures[0].SignatureDigest
	artifactFlowAdvanceV1(t, &f, state, 6, 5)
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{{TaskID: "edit-note", Kind: "work", Action: "按新的明确范围修订同一份附件", ResourceIDs: []string{id}, ProgressTarget: physicalTestNumber(2), ProgressUnit: "minute", KnowledgeRefs: []string{"known-ca_a"}, OutputRequests: []CharacterWorkOutputRequestV1{{OutputKey: "edit", ResourceID: id, ExpectedVersionDigest: version, Label: row.Name, Claims: []CharacterWorkArtifactClaimV1{{ClaimID: "c1", Text: "本人仅确认自己的记录范围，不代他人证明。", EpistemicKind: "self_statement"}, {ClaimID: "c2", Text: "新增待核事项，仍不作最终结论。", EpistemicKind: "pending"}}}}}}
	f.receipt.Resolutions[0].Outcome = "partial"
	f.receipt.Resolutions[0].CompletionState = "in_progress"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "edit-note", Status: "in_progress", StartDay: selfTestDay(5), EndDay: selfTestDay(6), OutputResults: []CharacterWorkOutputResultV1{{OutputKey: "edit", Status: "updated", AtDay: 6.0 / 1440, ClaimIDs: []string{"c1"}}}}}
	artifactFlowRebindV1(t, &f)
	_, state = artifactFlowApplyV1(t, f, map[string]string{})
	editOriginal := continuationCloneV1(f.proposals[0])
	editRaw, _ := json.Marshal(editOriginal)
	artifactFlowAdvanceV1(t, &f, state, 7, 6)
	f.proposals[0] = editOriginal
	f.receipt.Resolutions[0].Outcome = "success"
	f.receipt.Resolutions[0].CompletionState = "completed"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "edit-note", Status: "completed", StartDay: selfTestDay(6), EndDay: selfTestDay(7), OutputResults: []CharacterWorkOutputResultV1{{OutputKey: "edit", Status: "updated", AtDay: 7.0 / 1440, ClaimIDs: []string{"c2"}, Complete: true}}}}
	p, err = FinalizeCharacterDecisionProposal(f.proposals[1], f.observations[1])
	if err != nil {
		t.Fatal(err)
	}
	f.proposals[1] = p
	f.receipt.ProposalDigests = []string{editOriginal.Digest, p.Digest}
	f.receipt.Resolutions[0].ProposalDigest = editOriginal.Digest
	f.receipt.Resolutions[1].ProposalDigest = p.Digest
	_, state = artifactFlowApplyV1(t, f, map[string]string{"ca_a": editOriginal.Digest})
	gotEdit, _ := json.Marshal(f.proposals[0])
	if string(gotEdit) != string(editRaw) {
		t.Fatal("verified update continuation rewrote original CAS")
	}
	row, _ = artifactStateResourceV1(state, id)
	stock, _ = artifactStateResourceV1(state, artifactPaperStockTestV1)
	if row.Artifact.Revision != 4 || row.Artifact.Status != "complete" || *stock.ActualAmount != 4 || row.Artifact.Signatures[0].SignatureDigest != oldSignature || row.Artifact.Signatures[0].VersionDigest == row.Artifact.VersionDigest {
		t.Fatal("update cloned resources/debit or inherited a prior signature as current")
	}
	bViews, err := BuildCharacterArtifactViewsV1(state, "ca_b")
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range bViews {
		if view.VersionDigest == row.Artifact.VersionDigest && (len(view.Claims) > 0 || len(view.Signatures) > 0) {
			t.Fatal("old reader acquired unseen revised content/signatures")
		}
	}
	// Sharing content did not surrender physical custody. Only that actual
	// custodian may carry the object; a reader's shared access is not transport.
	artifactFlowAdvanceV1(t, &f, state, 8, 7)
	f.proposals[1].SelfTasks = []CharacterSelfTaskV2{{TaskID: "take-note", Kind: "carry", Action: "携走共享查阅的附件", ResourceIDs: []string{id}, KnowledgeRefs: []string{"known-ca_b"}}}
	f.receipt.Resolutions[1].Outcome = "success"
	f.receipt.Resolutions[1].CompletionState = "completed"
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "take-note", Status: "completed", StartDay: selfTestDay(7), EndDay: selfTestDay(8)}}
	artifactFlowRebindV1(t, &f)
	if _, err := finalizeWorldArbitrationReceiptWithPriorSources(f.receipt, f.stimulus, f.activation, f.proposals, 1, map[string]string{}); err == nil {
		t.Fatal("reader carried another custodian's artifact")
	}
	artifactFlowAdvanceV1(t, &f, state, 8, 7)
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{{TaskID: "carry-note", Kind: "carry", Action: "携本人持管的附件到办公室", ResourceIDs: []string{id}, KnowledgeRefs: []string{"known-ca_a"}}}
	f.receipt.Resolutions[0].Outcome = "success"
	f.receipt.Resolutions[0].CompletionState = "completed"
	f.receipt.Resolutions[0].PostState.Location = "办公室"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "carry-note", Status: "completed", StartDay: selfTestDay(7), EndDay: selfTestDay(8)}}
	artifactFlowRebindV1(t, &f)
	contentVersion := row.Artifact.VersionDigest
	_, state = artifactFlowApplyV1(t, f, map[string]string{})
	row, _ = artifactStateResourceV1(state, id)
	if row.Artifact.VersionDigest != contentVersion || row.Artifact.Placement.Location != "办公室" || row.Artifact.Placement.CustodianAgentID != "ca_a" {
		t.Fatal("explicit carry lost custody/location or changed content version")
	}
}

func TestWorkArtifactV1AuthorCompletesBeforeParallelCycleEnd(t *testing.T) {
	f := artifactFlowFixtureV1(t)
	f.receipt.StoryTime.EndDay = 5.0 / 1440
	f.receipt.Resolutions[0].Outcome = "success"
	f.receipt.Resolutions[0].CompletionState = "completed"
	e := &f.receipt.Resolutions[0].SelfExecutions[0]
	e.Status = "completed"
	e.EndDay = selfTestDay(2)
	e.OutputResults[0].AtDay = 2.0 / 1440
	e.OutputResults[0].ClaimIDs = []string{"c1", "c2"}
	e.OutputResults[0].Complete = true
	f.receipt.Resolutions[1].Outcome = "success"
	f.receipt.Resolutions[1].CompletionState = "completed"
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(5)}}
	r, state := artifactFlowApplyV1(t, f, map[string]string{})
	for _, resource := range state.Resources {
		if resource.Artifact != nil && resource.Artifact.CreatedAtDay != 2.0/1440 {
			t.Fatal("author's actual creation time was delayed to unrelated global end")
		}
	}
	if *r.Resolutions[0].SelfExecutions[0].EndDay != 2.0/1440 || r.StoryTime.EndDay != 5.0/1440 {
		t.Fatal("parallel closure changed actual worker durations")
	}
}

func TestWorkArtifactV1NoMaterialNoOutputAndTamperedClaimsFail(t *testing.T) {
	for _, kind := range []string{"missing material debit", "missing output", "unknown claim", "created while moving"} {
		t.Run(kind, func(t *testing.T) {
			f := artifactFlowFixtureV1(t)
			switch kind {
			case "missing material debit":
				f.receipt.ResourceSettlements = nil
			case "missing output":
				f.receipt.Resolutions[0].SelfExecutions[0].OutputResults = nil
			case "unknown claim":
				f.receipt.Resolutions[0].SelfExecutions[0].OutputResults[0].ClaimIDs = []string{"invented"}
			case "created while moving":
				f.receipt.Resolutions[0].PostState.Location = "他处"
			}
			if _, err := finalizeWorldArbitrationReceiptWithPriorSources(f.receipt, f.stimulus, f.activation, f.proposals, 1, map[string]string{}); err == nil {
				t.Fatal("invalid artifact result accepted")
			}
		})
	}
}

func TestWorkArtifactV1ReadAndSignTimeMustBelongToDeclaredTask(t *testing.T) {
	const id = "res_0000000000000006"
	p := CharacterDecisionProposal{SelfTasks: []CharacterSelfTaskV2{{TaskID: "wait", Kind: "work"}, {TaskID: "read", Kind: "work", ResourceIDs: []string{id}}}}
	r := WorldArbitrationReceipt{Finalized: true, StoryTime: &StoryTimeChapterSchedule{StartDay: 0, EndDay: 5.0 / 1440}}
	resolution := CharacterDecisionResolution{Outcome: "success", CompletionState: "completed", PostState: &CharacterPhysicalStateV2{Location: "A"}, SelfExecutions: []CharacterSelfExecutionV2{{TaskID: "wait", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(2)}, {TaskID: "read", Status: "completed", StartDay: selfTestDay(3), EndDay: selfTestDay(4)}}}
	old := CharacterPhysicalStateV2{Location: "A"}
	if artifactBoundTaskTimeV1(r, resolution, p, old, "read", id, 1.0/1440) {
		t.Fatal("a waiting interval became document reading")
	}
	if !artifactBoundTaskTimeV1(r, resolution, p, old, "read", id, 3.5/1440) {
		t.Fatal("real task-local read was delayed to unrelated global cycle end")
	}
	resolution.PostState.Location = "B"
	if artifactBoundTaskTimeV1(r, resolution, p, old, "read", id, 3.5/1440) {
		t.Fatal("unbound movement manufactured a reading location")
	}
}

func TestWorkArtifactV1UnseenCapabilityDoesNotWakeAndCannotCarrySignature(t *testing.T) {
	p := CharacterObservationPacket{AgentID: "ca_one", Character: "甲", GenerationID: "pg2_same", Chapter: 1, ArtifactViews: []CharacterArtifactViewV1{{ResourceID: "res_0000000000000006", VersionDigest: "sha256:" + strings.Repeat("a", 64), KnowledgeKind: "unread"}}}
	q := continuationCloneV1(p)
	q.ArtifactViews[0].VersionDigest = "sha256:" + strings.Repeat("b", 64)
	reasons, err := CharacterReactivationReasons(p, q)
	if err != nil || len(reasons) > 0 {
		t.Fatalf("opaque capability drift became new observed information: %v %v", reasons, err)
	}
	q.Sources = []string{CharacterWorkArtifactPolicyV1, CharacterActivationCyclePolicyV3, CharacterArbitrationRoundSourcesPolicyV1, CharacterSelfExperiencePolicyV2, CharacterSelfChronologyPolicyV1}
	q.Version = CharacterObservationV2Version
	q.ArtifactViews[0].Signatures = []CharacterArtifactSignatureReceiptV1{{Signer: "未感知签名"}}
	if err := validateWorkArtifactObservationV1(q); err == nil {
		t.Fatal("unread metadata leaked a signature")
	}
}
