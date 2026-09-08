package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const selfDocumentTestID = "res_0000000000000004"

func selfTestDay(minute float64) *float64 { return physicalTestNumber(minute / 1440) }

func newSelfExperienceFixture(t *testing.T) physicalProtocolFixture {
	t.Helper()
	f := newPhysicalProtocolFixture(t)
	state := f.stimulus.PhysicalState
	state.Resources[1].Name = "作者知道工具的真实来历"
	state.Resources[1].ReadableFacts = nil
	state.Resources = append(state.Resources, WorldResourceBalanceV2{ResourceID: selfDocumentTestID, Name: "作者保密的原始记录", ReadableFacts: []ResourceReadableFactV2{{ID: "hidden-number", Text: "未被本人读取的秘密余额11.8。"}}})
	state.Actors[0].Resources[1].PerceivedName = "机修棚内本人正在整理的工具"
	state.Actors[0].Resources[1].PerceivedLabel = "常规手工具（不具万能开锁能力）"
	state.Actors[0].Resources = append(state.Actors[0].Resources, CharacterResourceHoldingV2{ResourceID: selfDocumentTestID, PerceivedName: "留在开局记录夹中的文件", PerceivedLabel: "本人记录原件（保留原页）", Access: "exclusive", Perception: ResourcePerceptionV2{Kind: "unknown"}})
	f.stimulus.Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2}
	f.receipt.StoryTime = &StoryTimeChapterSchedule{Chapter: 1, StartDay: 0, EndDay: 5.0 / 1440}
	f.proposals[0].SelfTasks = []CharacterSelfTaskV2{
		{TaskID: "leave-record", Kind: "place", Action: "将本人记录留在当前安全记录夹", ResourceIDs: []string{selfDocumentTestID}, KnowledgeRefs: []string{"known-ca_a"}},
		{TaskID: "carry-tools", Kind: "carry", Action: "携带常规手工具前往渡船", ResourceIDs: []string{physicalPaperTestID}, KnowledgeRefs: []string{"known-ca_a"}},
		{TaskID: "inspection", Kind: "work", Action: "执行本班机务安全检查", ProgressTarget: physicalTestNumber(35), ProgressUnit: "minute", ResourceIDs: []string{physicalPaperTestID}, KnowledgeRefs: []string{"known-ca_a"}},
	}
	f.proposals[1].SelfTasks = []CharacterSelfTaskV2{{TaskID: "wait", Kind: "work", Action: "等待安全作业条件", KnowledgeRefs: []string{"known-ca_b"}}}
	for i := range f.observations {
		f.observations[i].Sources = []string{CharacterSourceRefPolicyV2, CharacterSelfExperiencePolicyV2}
	}
	f.observations[0].KnownFacts[0].Text = "本班检查需要本人累计35分钟有效工时，工具和本人记录均由我持管。"
	rebindPhysicalTestStimulus(t, &f)
	for i, actor := range f.stimulus.PhysicalState.Actors {
		copy := actor
		f.receipt.Resolutions[i].PostState = &copy
	}
	f.receipt.Resolutions[0].PostState.Location = "停泊的渡船"
	f.receipt.Resolutions[0].Outcome, f.receipt.Resolutions[0].CompletionState = "partial", "in_progress"
	f.receipt.Resolutions[0].SelfExecutions = []CharacterSelfExecutionV2{
		{TaskID: "leave-record", Status: "completed", StartDay: selfTestDay(0), EndDay: selfTestDay(.25)},
		{TaskID: "carry-tools", Status: "completed", StartDay: selfTestDay(1), EndDay: selfTestDay(4)},
		{TaskID: "inspection", Status: "in_progress", StartDay: selfTestDay(4), EndDay: selfTestDay(5)},
	}
	f.receipt.Resolutions[1].Outcome, f.receipt.Resolutions[1].CompletionState = "blocked", "blocked"
	f.receipt.Resolutions[1].SelfExecutions = []CharacterSelfExecutionV2{{TaskID: "wait", Status: "blocked"}}
	settlePhysicalFuel(&f)
	return f
}

func TestSelfExperienceV2DerivesExecutedWorkAndPlacementWithoutWorldTruth(t *testing.T) {
	f := newSelfExperienceFixture(t)
	before, _ := json.Marshal(f.stimulus)
	receipt, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ApplyArbitrationPhysicalStateV2(receipt, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	actor := after.Actors[0]
	if len(actor.SelfExperiences) != 3 || len(actor.TaskProgress) != 1 || !physicalAmountsCloseV2(actor.TaskProgress[0].Completed, 1) || *actor.TaskProgress[0].Target != 35 || actor.TaskProgress[0].State != "in_progress" {
		t.Fatalf("real C1 work was not derived as 1/35, in progress: %+v", actor.TaskProgress)
	}
	for _, holding := range actor.Resources {
		if holding.ResourceID == physicalPaperTestID && (holding.KnownPlacement == nil || holding.KnownPlacement.Kind != "with_actor" || holding.KnownPlacement.Location != "停泊的渡船") {
			t.Fatal("explicit completed transport did not establish the actual tool placement")
		}
		if holding.ResourceID == selfDocumentTestID && (holding.KnownPlacement == nil || holding.KnownPlacement.Kind != "stored" || holding.KnownPlacement.Location != "仓库") {
			t.Fatal("explicit completed placement was guessed from the final destination")
		}
	}
	if !samePhysicalValueV2(receipt.Resolutions[0].PostState, &actor) {
		t.Fatal("receipt did not contain its complete host-derived post state")
	}
	private, err := CharacterPrivateOutcomeV2(f.proposals[0], receipt.Resolutions[0], after)
	if err != nil || !strings.Contains(private, "累计有效工时1分钟") || !strings.Contains(private, "尚余34分钟") || !strings.Contains(private, "实际携带至停泊的渡船") {
		t.Fatalf("owner memory lost its executed progress/transport: %s (%v)", private, err)
	}
	for _, secret := range []string{"秘密余额", "11.8", "作者知道", "机修棚内本人正在整理", "秘密真值只属于裁判"} {
		if strings.Contains(private, secret) {
			t.Fatalf("private experience exposed omniscient data or stale dynamic label: %s", secret)
		}
	}
	other, err := CharacterPrivateOutcomeV2(f.proposals[1], receipt.Resolutions[1], after)
	if err != nil || strings.Contains(other, "机务安全检查") || strings.Contains(other, "尚余34") {
		t.Fatal("one owner's work progress leaked into the other owner's private memory")
	}
	unchanged, _ := json.Marshal(f.stimulus)
	if !bytes.Equal(before, unchanged) {
		t.Fatal("host derivation mutated the immutable prior stimulus")
	}
	raw, _ := json.Marshal(receipt)
	var restored WorldArbitrationReceipt
	_ = json.Unmarshal(raw, &restored)
	replayed, err := FinalizeWorldArbitrationReceipt(restored, f.stimulus, f.activation, f.proposals, 1)
	if err != nil || replayed.Digest != receipt.Digest {
		t.Fatalf("derived receipt could not verify after disk restart: %v", err)
	}
	reencoded, _ := json.Marshal(replayed)
	if !bytes.Equal(raw, reencoded) {
		t.Fatal("receipt replay changed derived experience bytes")
	}
}

func TestSelfExperienceV2NextOwnerObservationCarriesProgressAndProtectsDefinitions(t *testing.T) {
	f := newSelfExperienceFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	nextStimulus := f.stimulus
	nextStimulus.Chapter, nextStimulus.PhysicalState = 2, &state
	nextStimulus, err = FinalizeWorldStimulusPacket(nextStimulus)
	if err != nil {
		t.Fatal(err)
	}
	o := f.observations[0]
	o.Chapter, o.StimulusDigest, o.Location = 2, nextStimulus.Digest, state.Actors[0].Location
	o.ResourceViews, err = BuildCharacterResourceViewsV2(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o.SelfExperiences, o.TaskProgress, err = BuildCharacterSelfObservationV2(state, o.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	o, err = FinalizeCharacterObservationPacket(o)
	if err != nil || ValidateCharacterResourceViewsAgainstStimulusV2(nextStimulus, o) != nil {
		t.Fatalf("C2 cannot reconstruct its exact C1 experiences: %v", err)
	}
	p := f.proposals[0]
	p.Chapter, p.ObservationDigest, p.Location = 2, o.Digest, o.Location
	p.SelfTasks = p.SelfTasks[2:]
	if _, err := FinalizeCharacterDecisionProposal(p, o); err != nil {
		t.Fatalf("continuing the same work task was rejected: %v", err)
	}
	p.SelfTasks[0].ProgressTarget = physicalTestNumber(1)
	if _, err := FinalizeCharacterDecisionProposal(p, o); err == nil {
		t.Fatal("C2 silently changed the same task's original target")
	}
	for _, corrupt := range []func(*CharacterObservationPacket){
		func(o *CharacterObservationPacket) { o.SelfExperiences[0].Action = "作者秘密经历" },
		func(o *CharacterObservationPacket) { o.TaskProgress[0].Completed = 35 },
	} {
		raw, _ := json.Marshal(o)
		var changed CharacterObservationPacket
		_ = json.Unmarshal(raw, &changed)
		corrupt(&changed)
		finalized, err := FinalizeCharacterObservationPacket(changed)
		if err == nil && ValidateCharacterResourceViewsAgainstStimulusV2(nextStimulus, finalized) == nil {
			t.Fatal("forged or future owner observation passed the complete host-state binding")
		}
	}
}

func TestSelfExperienceV2BoundsHistoryWithoutDroppingOlderUnfinishedTasks(t *testing.T) {
	f := newSelfExperienceFixture(t)
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	actor := &state.Actors[0]
	for chapter := 2; chapter <= 110; chapter++ {
		status := "completed"
		var target *float64
		if chapter > 100 {
			status, target = "in_progress", physicalTestNumber(100)
		}
		experience := CharacterSelfExperienceV2{Chapter: chapter, TaskID: "work-" + fmt.Sprint(chapter), Kind: "work", Action: "完成有记录的例行工作", Status: status, StartDay: selfTestDay(float64(chapter * 10)), EndDay: selfTestDay(float64(chapter*10 + 1)), ProgressUnit: "minute", ProgressTarget: target, SourceProposalDigest: f.proposals[0].Digest}
		experience.ID = CharacterSelfExperienceIDV2(actor.AgentID, experience)
		actor.SelfExperiences = append(actor.SelfExperiences, experience)
	}
	actor.TaskProgress, err = deriveCharacterTaskProgressV2(actor.SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	state, err = FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(state)
	experiences, progress, err := BuildCharacterSelfObservationV2(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 11 || len(experiences) > CharacterSelfObservationExperienceLimitV2 || len(experiences) >= len(state.Actors[0].SelfExperiences) {
		t.Fatalf("history projection did not preserve all unfinished tasks while bounding old detail: experiences=%d tasks=%d", len(experiences), len(progress))
	}
	foundOld := false
	for _, task := range progress {
		if task.TaskID == "inspection" {
			foundOld = physicalAmountsCloseV2(task.Completed, 1) && task.AsOfChapter == 1
		}
	}
	if !foundOld {
		t.Fatal("recent tasks pushed out the older still-unfinished 35-minute inspection")
	}
	private, err := CharacterPrivateOutcomeV2(f.proposals[0], r.Resolutions[0], state)
	if err != nil || strings.Contains(private, "完成有记录的例行工作") || strings.Contains(private, "work-100") {
		t.Fatal("closed historical task summaries reentered per-chapter private memory")
	}
	observation := f.observations[0]
	observation.Chapter, observation.SelfExperiences, observation.TaskProgress = 111, experiences, progress
	observation.ResourceViews, err = BuildCharacterResourceViewsV2(state, "ca_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeCharacterObservationPacket(observation); err != nil {
		t.Fatalf("bounded summaries no longer validate: %v", err)
	}
	allowed := observation.AllowedFactIDs()
	if _, exists := allowed[experiences[0].ID]; !exists {
		t.Fatal("owner cannot cite its visible self-experience proof")
	}
	if _, exists := allowed["inspection"]; exists {
		t.Fatal("task identifier was mistaken for independently known fact evidence")
	}
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("bounded projection deleted canonical execution history")
	}
	// Required unfinished work is never silently truncated to fit a fixed
	// count. A caller must focus context or resolve workload explicitly.
	for chapter := 111; chapter <= 140; chapter++ {
		experience := CharacterSelfExperienceV2{Chapter: chapter, TaskID: "work-" + fmt.Sprint(chapter), Kind: "work", Action: "仍须处理的工作", Status: "in_progress", StartDay: selfTestDay(float64(chapter * 10)), EndDay: selfTestDay(float64(chapter*10 + 1)), ProgressUnit: "minute", ProgressTarget: physicalTestNumber(100), SourceProposalDigest: f.proposals[0].Digest}
		experience.ID = CharacterSelfExperienceIDV2("ca_a", experience)
		state.Actors[0].SelfExperiences = append(state.Actors[0].SelfExperiences, experience)
	}
	state.Actors[0].TaskProgress, err = deriveCharacterTaskProgressV2(state.Actors[0].SelfExperiences)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildCharacterSelfObservationV2(state, "ca_a"); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("over-budget active tasks were silently dropped: %v", err)
	}
}

func TestSelfExperienceV2DoesNotInferTransportFromActorDestination(t *testing.T) {
	f := newSelfExperienceFixture(t)
	f.receipt.Resolutions[0].SelfExecutions[1].Status = "in_progress"
	r, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ApplyArbitrationPhysicalStateV2(r, f.stimulus, f.proposals...)
	if err != nil {
		t.Fatal(err)
	}
	if state.Actors[0].Location != "停泊的渡船" {
		t.Fatal("test did not exercise an actor that really moved")
	}
	for _, h := range state.Actors[0].Resources {
		if h.ResourceID == physicalPaperTestID && h.KnownPlacement != nil {
			t.Fatal("actor arrival was mistaken for completed tool transport")
		}
	}
}

func TestSelfExperienceV2RejectsRawOrEncodedPrivateHistoryInProse(t *testing.T) {
	private := map[string]any{"self_experiences": []any{map[string]any{"action": "另一个角色的私下工作"}}}
	encoded, _ := json.Marshal(private)
	for _, value := range []any{private, string(encoded), "mirror=" + string(encoded)} {
		if !planningSelfTruthV2(value, 0) {
			t.Fatal("private typed history was not detected")
		}
	}
	payload := map[string]any{"text": "这个角色实际用了1分钟，尚余34分钟。", "mirror": string(encoded)}
	planningV2DeleteJSONKeys(payload, map[string]struct{}{"self_experiences": {}})
	_, _ = stripPlanningEncodedSelfTruthV2(payload)
	if _, exists := payload["mirror"]; exists || payload["text"] == nil {
		t.Fatal("prose boundary did not distinguish encoded private records from lawful numeric prose")
	}
}

func TestSelfExperienceV2RejectsUnexecutedForeignOverlappingOrInventedResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*physicalProtocolFixture)
	}{
		{"foreign_task", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].SelfExecutions[2].TaskID = "wait" }},
		{"outside_story_window", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].SelfExecutions[2].EndDay = selfTestDay(6) }},
		{"blocked_with_interval", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].SelfExecutions[2].Status = "blocked" }},
		{"premature_target_completion", func(f *physicalProtocolFixture) { f.receipt.Resolutions[0].SelfExecutions[2].Status = "completed" }},
		{"provisional_execution", func(f *physicalProtocolFixture) { f.receipt.Finalized = false }},
		{"missing_explicit_task_status", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions = f.receipt.Resolutions[0].SelfExecutions[:2]
		}},
		{"inferred_midpoint_place", func(f *physicalProtocolFixture) {
			f.receipt.Resolutions[0].SelfExecutions[0].StartDay, f.receipt.Resolutions[0].SelfExecutions[0].EndDay = selfTestDay(4), selfTestDay(4.5)
		}},
		{"shared_read_is_not_transport", func(f *physicalProtocolFixture) {
			f.proposals[0].SelfTasks[1].ResourceIDs = []string{physicalFuelTestID}
			rebindPhysicalTestProposals(t, f)
		}},
		{"overlapping_own_work", func(f *physicalProtocolFixture) {
			f.proposals[0].SelfTasks = append(f.proposals[0].SelfTasks, CharacterSelfTaskV2{TaskID: "other-work", Kind: "work", Action: "同时处理另一项工作", KnowledgeRefs: []string{"known-ca_a"}})
			rebindPhysicalTestProposals(t, f)
			f.receipt.Resolutions[0].SelfExecutions = append(f.receipt.Resolutions[0].SelfExecutions, CharacterSelfExecutionV2{TaskID: "other-work", Status: "in_progress", StartDay: selfTestDay(4), EndDay: selfTestDay(5)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSelfExperienceFixture(t)
			tc.edit(&f)
			if _, err := finalizePhysicalFixture(f); err == nil {
				t.Fatal("invalid self execution became owner knowledge")
			}
		})
	}
}

func TestSelfExperienceV2StaticLabelsKeepFixedConstraintsAndLegacyBytes(t *testing.T) {
	f := newPhysicalProtocolFixture(t)
	before, _ := json.Marshal(f.stimulus)
	old, err := finalizePhysicalFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(old)
	var restored WorldArbitrationReceipt
	_ = json.Unmarshal(raw, &restored)
	replayed, err := FinalizeWorldArbitrationReceipt(restored, f.stimulus, f.activation, f.proposals, 1)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(replayed)
	if !bytes.Equal(raw, again) || bytes.Contains(raw, []byte("self_experiences")) || bytes.Contains(raw, []byte("self_tasks")) {
		t.Fatal("legacy untagged v2 receipt bytes or digest changed")
	}
	f.stimulus.Sources = append(f.stimulus.Sources, CharacterSelfExperiencePolicyV2)
	prepared, err := FinalizeWorldStimulusPacket(f.stimulus)
	if err != nil {
		t.Fatal(err)
	}
	views, err := BuildCharacterResourceViewsV2(*prepared.PhysicalState, "ca_a")
	if err != nil || views[0].Name != UnidentifiedResourceNameV2 {
		t.Fatal("missing static label fell back to a dynamic/author name")
	}
	original := *prepared.PhysicalState
	original.Actors[0].Resources[0].PerceivedLabel = "共用燃油（仅限既定用途，不代表任意支配权）"
	views, err = BuildCharacterResourceViewsV2(original, "ca_a")
	if err != nil || !strings.Contains(views[0].Name, "不代表任意支配权") {
		t.Fatal("static resource label lost its fixed access/use constraints")
	}
	original.Actors[0].Resources[0].PerceivedLabel = strings.Repeat("字", 161)
	if _, err := FinalizeWorldPhysicalStateV2(original); err == nil {
		t.Fatal("unbounded static label accepted")
	}
	unchanged := f.stimulus
	unchanged.Sources = nil
	encoded, _ := json.Marshal(unchanged)
	if !bytes.Equal(before, encoded) {
		t.Fatal("new label preparation mutated an old author source state")
	}
}
