package agents

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestArcRehearsalTransportDoesNotExpandSmallInputs(t *testing.T) {
	for _, raw := range []string{`{"a":1}`, `{"a":"` + strings.Repeat("x", 48) + `","b":"` + strings.Repeat("x", 48) + `"}`} {
		got, err := compactArcRehearsalJSON([]byte(raw))
		if err != nil || !bytes.Equal(got, []byte(raw)) {
			t.Fatalf("non-beneficial encoding should preserve the original packet: err=%v bytes=%d want=%d", err, len(got), len(raw))
		}
	}
}

func TestArcRehearsalTransportRoundTripPreservesAllSourceValues(t *testing.T) {
	shared := map[string]any{"rule": strings.Repeat("读取必须实际发生；不要补造秘密或过去事件。", 20), "resource_amount_authorized": json.Number("9007199254740993"), "unknown": nil, "nested_literal": map[string]any{"$v": "v0001"}}
	original := map[string]any{"actors": []any{shared, shared, shared}, "literal": map[string]any{arcRehearsalSharedValueKey: "not-a-reference"}, "goal": "原始目标", "k1": "existing key must not be overwritten"}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := compactArcRehearsalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := compactArcRehearsalJSON(raw)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatal("transport is not deterministic")
	}
	if len(encoded) >= len(raw) {
		t.Fatalf("repeated fixture did not shrink: %d >= %d", len(encoded), len(raw))
	}
	var view struct {
		Fields  map[string]string `json:"field_names"`
		Values  map[string]any    `json:"shared_values"`
		Payload any               `json:"payload"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&view); err != nil {
		t.Fatal(err)
	}
	var expand func(any) any
	expand = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			if len(value) == 1 {
				if id, ok := value[arcRehearsalSharedValueKey].(string); ok {
					literal, exists := view.Values[id]
					if !exists {
						t.Fatalf("missing value %s", id)
					}
					return literal
				}
			}
			out := map[string]any{}
			for key, child := range value {
				out[key] = expand(child)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				out[i] = expand(child)
			}
			return out
		default:
			return value
		}
	}
	var restoreKeys func(any) any
	restoreKeys = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			out := map[string]any{}
			for key, child := range value {
				if original := view.Fields[key]; original != "" {
					key = original
				}
				out[key] = restoreKeys(child)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, child := range value {
				out[i] = restoreKeys(child)
			}
			return out
		default:
			return value
		}
	}
	if !reflect.DeepEqual(original, restoreKeys(expand(view.Payload))) {
		t.Fatal("lossless transport altered source values, precision, or literal marker data")
	}
}

func TestArcRehearsalModelPayloadDoesNotMutateCanonicalInputOrDraft(t *testing.T) {
	input := domain.ArcRehearsalInput{HardContracts: []string{strings.Repeat("条件", 100)}, SourceFiles: map[string]string{"source": "unchanged"}}
	draft := &domain.ArcRehearsalDraft{Body: domain.ArcRehearsalBody{Summary: input.HardContracts[0], UnresolvedItems: append([]string(nil), input.HardContracts...)}}
	before, err := json.Marshal([]any{input, draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArcRehearsalModelPayload(input, draft); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal([]any{input, draft})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("transport changed canonical input or draft")
	}
}
