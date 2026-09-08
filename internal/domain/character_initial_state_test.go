package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCharacterInitialStateLegacyJSONIsUnchanged(t *testing.T) {
	const raw = `{"name":"值班员","role":"主角","description":"熟悉当地记录","arc":"未来可能承担新的责任","traits":["谨慎"]}`
	var character Character
	if err := json.Unmarshal([]byte(raw), &character); err != nil {
		t.Fatal(err)
	}
	if character.InitialState != nil || ValidateCharacterInitialState(character) != nil {
		t.Fatal("legacy character acquired an invented initial state")
	}
	encoded, err := json.Marshal(character)
	if err != nil || string(encoded) != raw {
		t.Fatalf("legacy JSON changed: %s err=%v", encoded, err)
	}
}

func TestCharacterInitialStateValidatesOnlyExplicitOpeningFacts(t *testing.T) {
	character := Character{Name: "机修员", Arc: "未来会与主角合作", InitialState: &CharacterInitialState{
		Location: "机修棚", CurrentGoal: "完成当班机务检查", Pressure: "检查工时不能压缩",
		KnownFacts: []string{"当班检查尚未完成", "自己持有原始接收记录"},
		Resources:  []string{}, Relationships: []string{}, Commitments: []string{},
	}}
	before, _ := json.Marshal(character)
	if err := ValidateCharacterInitialState(character); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(character)
	if string(before) != string(after) || strings.Contains(string(after), `"time"`) || strings.Contains(string(after), `"current_action"`) {
		t.Fatalf("validation invented unknown opening values: %s", after)
	}
}

func TestCharacterInitialStateRejectsMissingFieldsAndDuplicateKnowledge(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		mutate      func(*CharacterInitialState)
	}{
		{"location", "location", func(s *CharacterInitialState) { s.Location = " \n" }},
		{"goal", "current_goal", func(s *CharacterInitialState) { s.CurrentGoal = "" }},
		{"pressure", "pressure", func(s *CharacterInitialState) { s.Pressure = "\t" }},
		{"missing facts", "known_facts", func(s *CharacterInitialState) { s.KnownFacts = nil }},
		{"blank fact", "known_facts[1]", func(s *CharacterInitialState) { s.KnownFacts = []string{"持有钥匙", " "} }},
		{"duplicate fact", "duplicates", func(s *CharacterInitialState) { s.KnownFacts = []string{"持有钥匙", " 持有钥匙 "} }},
		{"case/spacing duplicate", "duplicates", func(s *CharacterInitialState) { s.KnownFacts = []string{"Record J17 exists", "record\tJ17 exists"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &CharacterInitialState{Location: "值班室", CurrentGoal: "核对交接材料", Pressure: "截止时间临近", KnownFacts: []string{"持有钥匙"}}
			tc.mutate(state)
			err := ValidateCharacterInitialState(Character{Name: "林澄", InitialState: state})
			if err == nil || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), "林澄") {
				t.Fatalf("missing field-specific error %q: %v", tc.field, err)
			}
		})
	}
}
