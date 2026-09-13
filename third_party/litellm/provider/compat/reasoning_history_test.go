package compat

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/voocel/litellm"
)

func splitHistorySpec() Spec {
	return Spec{
		Response: ResponseSpec{ReasoningFields: []string{"reasoning_content", "reasoning_details"}},
		Request:  RequestSpec{ReasoningHistoryField: "reasoning_content", ReasoningHistoryDetailsField: "reasoning_details"},
	}
}

func TestSplitReasoningHistoryKeepsOpaqueArrayAndIndependentText(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.text","text":"synthetic detail","index":9007199254740993,"signature":"synthetic-opaque-state","future":{"raw":1.2300}}]`)
	before := append(json.RawMessage(nil), extra...)
	block := litellm.ReasoningBlock{Text: "synthetic canonical text", Extra: extra}
	converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(block)}, splitHistorySpec())
	if err != nil {
		t.Fatal(err)
	}
	if converted[0]["reasoning_content"] != block.Text {
		t.Fatal("separate reasoning text was lost behind Extra")
	}
	raw, err := json.Marshal(converted[0]["reasoning_details"])
	if err != nil {
		t.Fatal(err)
	}
	var expected bytes.Buffer
	if err := json.Compact(&expected, before); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, expected.Bytes()) {
		t.Fatalf("opaque structured details changed: %s", raw)
	}
	if !bytes.Equal(extra, before) {
		t.Fatal("request encoding mutated original provider state")
	}
	returned := converted[0]["reasoning_details"].(json.RawMessage)
	returned[1] = ' '
	if !bytes.Equal(extra, before) {
		t.Fatal("request details alias source Extra")
	}
}

func TestSplitReasoningHistoryDoesNotGuessArraysFromTextOrJSONStringExtra(t *testing.T) {
	text := `[{"type":"synthetic","text":"still plain text"}]`
	converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: text})}, splitHistorySpec())
	if err != nil {
		t.Fatal(err)
	}
	if converted[0]["reasoning_content"] != text {
		t.Fatal("JSON-looking text changed")
	}
	if _, exists := converted[0]["reasoning_details"]; exists {
		t.Fatal("plain text became structured provider state")
	}
	quoted, _ := json.Marshal(text)
	for _, invalid := range []json.RawMessage{quoted, json.RawMessage(`{"text":"not an array"}`), json.RawMessage(`null`), json.RawMessage(`true`), json.RawMessage(`123`), json.RawMessage(`[`)} {
		if _, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "not a fallback", Extra: invalid})}, splitHistorySpec()); err == nil || !strings.Contains(err.Error(), "JSON array") {
			t.Fatalf("invalid structured Extra was accepted: %s, err=%v", invalid, err)
		}
	}
}

func TestSplitReasoningHistoryDoesNotDuplicateMirroredText(t *testing.T) {
	extra := json.RawMessage(`[{"type":"reasoning.text","text":"fixture A","signature":"opaque"},{"type":"reasoning.text","text":" fixture B"}]`)
	converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "fixture A\n\n fixture B", Extra: extra})}, splitHistorySpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := converted[0]["reasoning_content"]; exists {
		t.Fatal("mirrored text increased request tokens unnecessarily")
	}
	if string(converted[0]["reasoning_details"].(json.RawMessage)) != string(extra) {
		t.Fatal("deduplication changed opaque details")
	}
}

func TestSplitReasoningHistoryPreservesEveryBlockAndEmptyArray(t *testing.T) {
	converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(
		litellm.ReasoningBlock{Text: "synthetic A", Extra: json.RawMessage(`[{"id":"a","unknown":{"n":1}}]`)},
		litellm.TextBlock{Text: "interleaved content"},
		litellm.ReasoningBlock{Text: " synthetic B", Extra: json.RawMessage(`[{"id":"b","unknown":{"n":2}}]`)},
	)}, splitHistorySpec())
	if err != nil {
		t.Fatal(err)
	}
	if converted[0]["reasoning_content"] != "synthetic A synthetic B" {
		t.Fatal("a reasoning block was overwritten")
	}
	raw, _ := json.Marshal(converted[0]["reasoning_details"])
	if string(raw) != `[{"id":"a","unknown":{"n":1}},{"id":"b","unknown":{"n":2}}]` {
		t.Fatalf("details order/state changed: %s", raw)
	}
	converted, err = convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "synthetic", Extra: json.RawMessage(`[]`)})}, splitHistorySpec())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(converted[0]["reasoning_details"])
	if string(raw) != `[]` || converted[0]["reasoning_content"] != "synthetic" {
		t.Fatal("empty array was replaced by text or null")
	}
}

func TestReasoningHistoryDefaultEncodingRemainsExtraFirst(t *testing.T) {
	legacy := Spec{Response: ResponseSpec{ReasoningFields: []string{"legacy_reasoning"}}}
	for _, tc := range []struct {
		block litellm.ReasoningBlock
		want  any
	}{
		{litellm.ReasoningBlock{Text: "synthetic text"}, "synthetic text"},
		{litellm.ReasoningBlock{Text: "not independently emitted", Extra: json.RawMessage(`{"opaque":"synthetic"}`)}, map[string]any{"opaque": "synthetic"}},
		{litellm.ReasoningBlock{Text: "not independently emitted", Extra: json.RawMessage(`["synthetic"]`)}, []any{"synthetic"}},
	} {
		converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(tc.block)}, legacy)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(converted[0], map[string]any{"role": "assistant", "legacy_reasoning": tc.want}) {
			t.Fatalf("default provider history changed: %#v", converted[0])
		}
	}
	legacy.Request.ReasoningHistoryField = "text_history"
	converted, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "synthetic text"})}, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if converted[0]["text_history"] != "synthetic text" {
		t.Fatal("explicit text field was ignored")
	}
}

func TestSplitReasoningHistoryRejectsUnsupportedStateAndCollidingFields(t *testing.T) {
	for _, block := range []litellm.ReasoningBlock{{Text: "synthetic", Signature: "foreign-signature"}, {Extra: json.RawMessage(`[]`), Redacted: []byte("foreign-state")}} {
		if _, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(block)}, splitHistorySpec()); err == nil {
			t.Fatal("unrepresentable provider state was silently dropped")
		}
	}
	spec := splitHistorySpec()
	spec.Request.ReasoningHistoryField = "reasoning_details"
	if _, err := convertMessagesWithSpec([]litellm.Message{litellm.Assistant(litellm.ReasoningBlock{Text: "synthetic"})}, spec); err == nil {
		t.Fatal("text could overwrite the array field")
	}
}
