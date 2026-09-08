package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestArbiterSelfHistoryModelViewIsBoundedAndNeverResigned(t *testing.T) {
	actor := domain.CharacterPhysicalStateV2{AgentID: "ca_history", Character: "角色", Location: "现场", Resources: []domain.CharacterResourceHoldingV2{}}
	target := 1440.0 / 1024
	for i := 0; i < 40; i++ {
		start, end := float64(i)/1024, float64(i+1)/1024
		experience := domain.CharacterSelfExperienceV2{Chapter: i + 1, TaskID: fmt.Sprintf("work_%02d", i), Kind: "work", Action: fmt.Sprintf("history_action_%02d", i), Status: "completed", StartDay: &start, EndDay: &end, ProgressTarget: &target, ProgressUnit: "minute", SourceProposalDigest: "sha256:" + strings.Repeat("a", 64)}
		experience.ID = domain.CharacterSelfExperienceIDV2(actor.AgentID, experience)
		actor.SelfExperiences = append(actor.SelfExperiences, experience)
		actor.TaskProgress = append(actor.TaskProgress, domain.CharacterTaskProgressV2{TaskID: experience.TaskID, Action: experience.Action, Unit: "minute", Target: &target, Completed: target, State: "completed", AsOfChapter: i + 1, SourceExperienceID: experience.ID})
	}
	state, err := domain.PrepareCharacterSelfExperienceStateV2(domain.WorldPhysicalStateV2{Version: domain.WorldPhysicalStateV2Version, Actors: []domain.CharacterPhysicalStateV2{actor}})
	if err != nil {
		t.Fatal(err)
	}
	mirror, _ := json.Marshal(state)
	stimulus, err := domain.FinalizeWorldStimulusPacket(domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, GenerationID: "pg2_self_history", Chapter: 41, TimeWindow: "当前时段", PhysicalState: &state, Sources: []string{domain.CharacterSelfExperiencePolicyV2}, CurrentEvents: []domain.CharacterAgentFact{{ID: "physical-mirror", Kind: "projected_state", Text: "world的" + domain.WorldPhysicalStateV2Field + "=" + string(mirror)}, {ID: "ordinary", Kind: "projected_state", Text: "允许的普通当前事件"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(stimulus)
	view, err := characterArbiterStimulusModelView(stimulus)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	if _, exists := object["digest"]; exists || object["source_stimulus_digest"] != stimulus.Digest || object["version"] != "world-stimulus-model-view.v1" {
		t.Fatal("bounded view impersonated original signed stimulus")
	}
	shown := object["physical_state"].(map[string]any)["actors"].([]any)[0].(map[string]any)
	if len(shown["self_experiences"].([]any)) > domain.CharacterSelfObservationExperienceLimitV2 || len(shown["task_progress"].([]any)) != 0 {
		t.Fatal("arbiter still receives unbounded owner history")
	}
	if strings.Contains(string(raw), "history_action_00") || strings.Contains(string(raw), "physical-mirror") || !strings.Contains(string(raw), "允许的普通当前事件") {
		t.Fatal("full encoded history bypassed bounded view or unrelated event was removed")
	}
	after, _ := json.Marshal(stimulus)
	if string(before) != string(after) {
		t.Fatal("model view altered durable stimulus")
	}
}
