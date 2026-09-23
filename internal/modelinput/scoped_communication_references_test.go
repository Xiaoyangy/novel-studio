package modelinput

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScopedCommunicationReplyReferencePreservesOldCodecAndExactFact(t *testing.T) {
	fact := "recv_" + strings.Repeat("a", 64)
	base, err := NewScopedArtifactReferenceCodecV1(KindCharacterObservation, map[string]any{"known_facts": []any{map[string]any{"id": fact, "kind": "received_information", "text": "对方实际说过的内容"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(base.ModelView())
	codec, err := WithScopedCommunicationReferencesV1(base)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"communications":[{"reply_to_received_fact_id":"` + codec.Alias(fact) + `","text":"我的原回复"}]}`)
	if base.ValidateModelArguments(raw, base.Binding()) == nil {
		t.Fatal("old codec acquired the new reply field")
	}
	if err := codec.ValidateModelArguments(raw, codec.Binding()); err != nil {
		t.Fatal(err)
	}
	expanded, err := codec.ExpandArguments(raw, codec.Binding())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(expanded), `"reply_to_received_fact_id":"`+fact+`"`) {
		t.Fatal("reply did not restore exact owned fact id")
	}
	after, _ := json.Marshal(base.ModelView())
	if string(before) != string(after) {
		t.Fatal("decorating mutated legacy view")
	}
}

func TestScopedCommunicationReplyRejectsLiteralNestedAliases(t *testing.T) {
	fact := "recv_" + strings.Repeat("a", 64)
	base, err := NewScopedArtifactReferenceCodecV1(KindCharacterObservation, map[string]any{"known_facts": []any{map[string]any{"id": fact, "kind": "received_information", "text": "原话"}}})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := WithScopedCommunicationReferencesV1(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"claims":{"communications":[{"reply_to_received_fact_id":"` + codec.Alias(fact) + `"}]}}`,
		`{"communications":[{"reply_to_received_fact_id":"@ref999999"}]}`,
		`{"communications":[{"text":"` + codec.Alias(fact) + `"}]}`,
	} {
		if err := codec.ValidateModelArguments(json.RawMessage(raw), codec.Binding()); err == nil {
			t.Fatalf("untyped or foreign reply alias accepted: %s", raw)
		}
	}
}
