package domain

import "testing"

func TestReactivationIgnoresEnvelopeChangesAndDoesNotDropOlderTasks(t *testing.T) {
	before := CharacterObservationPacket{AgentID: "ca_a", Character: "甲", Chapter: 1, GenerationID: "pg2_a", Location: "船上", KnownFacts: []CharacterAgentFact{{ID: "known", Kind: "known", Text: "已知事实"}}, TaskProgress: []CharacterTaskProgressV2{{TaskID: "old_inspection", Completed: 4, State: "in_progress"}}}
	current := before
	current.Digest = "new digest"
	current.StimulusDigest = "new stimulus"
	current.GeneratedAt = "later"
	current.TimeWindow = "current time moved"
	current.MemoryRoot = "new wrapper hash"
	current.Sources = []string{"new source envelope"}
	if reasons, err := CharacterReactivationReasons(before, current); err != nil || len(reasons) != 0 {
		t.Fatalf("metadata caused model call: %v %v", reasons, err)
	}
	current.TaskProgress = []CharacterTaskProgressV2{{TaskID: "old_inspection", Completed: 11, State: "in_progress"}}
	if reasons, err := CharacterReactivationReasons(before, current); err != nil || len(reasons) != 1 || reasons[0] != "self_execution_feedback" {
		t.Fatalf("real old-task progress did not trigger: %v %v", reasons, err)
	}
	current = before
	current.KnownFacts = append(append([]CharacterAgentFact(nil), before.KnownFacts...), CharacterAgentFact{ID: "new", Kind: "statement", Text: "对方实际送达的说法，尚未核验"})
	if reasons, err := CharacterReactivationReasons(before, current); err != nil || len(reasons) != 1 || reasons[0] != "received_information" {
		t.Fatalf("new delivered information did not trigger: %v %v", reasons, err)
	}
}

func TestReactivationRejectsForeignIdentity(t *testing.T) {
	before := CharacterObservationPacket{AgentID: "ca_a", Character: "甲", Chapter: 1, GenerationID: "pg2_a"}
	for _, edit := range []func(*CharacterObservationPacket){func(v *CharacterObservationPacket) { v.AgentID = "ca_b" }, func(v *CharacterObservationPacket) { v.Character = "乙" }, func(v *CharacterObservationPacket) { v.Chapter = 2 }, func(v *CharacterObservationPacket) { v.GenerationID = "pg2_b" }} {
		current := before
		edit(&current)
		if _, err := CharacterReactivationReasons(before, current); err == nil {
			t.Fatal("foreign identity compared as same actor")
		}
	}
}

func TestReactivationIgnoresEmptyListJSONRoundTrips(t *testing.T) {
	before := CharacterObservationPacket{AgentID: "ca_a", Character: "甲", Chapter: 1, GenerationID: "pg2_a"}
	after := before
	after.ResourceViews = []CharacterResourceViewV2{}
	after.Relationships, after.Commitments = []string{}, []string{}
	after.SelfExperiences = []CharacterSelfExperienceV2{}
	after.TaskProgress = []CharacterTaskProgressV2{}
	after.PublicMechanisms = []CodexMechanism{}
	if reasons, err := CharacterReactivationReasons(before, after); err != nil || len(reasons) != 0 {
		t.Fatalf("empty-list serialization woke an actor: %v %v", reasons, err)
	}
}
