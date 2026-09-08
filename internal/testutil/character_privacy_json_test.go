package testutil

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCharacterPrivacyJSONExcludesOnlyTopLevelHostTimestamp(t *testing.T) {
	for _, container := range []struct {
		name, timestamp string
		view            func(testing.TB, json.RawMessage) json.RawMessage
	}{
		{"observation", "generated_at", CharacterObservationPrivacyJSON},
		{"memory", "updated_at", CharacterMemoryPrivacyJSON},
	} {
		t.Run(container.name, func(t *testing.T) {
			const stamp = "2026-09-08T06:10:11.889498374Z"
			const digest = "sha256:original-source-digest"
			original, err := json.Marshal(map[string]any{
				container.timestamp: stamp,
				"digest":            digest,
				"facts":             []any{map[string]any{"text": "已经收到原始记录", "amount": 12}},
			})
			if err != nil {
				t.Fatal(err)
			}
			before := string(original)
			if !strings.Contains(before, "11.8") {
				t.Fatal("fixture does not reproduce the CI timestamp collision")
			}
			view := container.view(t, original)
			if strings.Contains(string(view), "11.8") {
				t.Fatal("Host timestamp was mistaken for the author's actual balance")
			}
			if string(original) != before {
				t.Fatal("privacy view modified its source JSON or original digest")
			}
			var wanted, got map[string]json.RawMessage
			if err := json.Unmarshal(original, &wanted); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(view, &got); err != nil {
				t.Fatal(err)
			}
			delete(wanted, container.timestamp)
			if !reflect.DeepEqual(got, wanted) {
				t.Fatal("privacy view changed fields other than the Host timestamp")
			}
			for _, leak := range []struct {
				name, needle string
				mutate       func(map[string]any)
			}{
				{"numeric fact", "11.8", func(fields map[string]any) {
					fields["facts"].([]any)[0].(map[string]any)["amount"] = 11.8
				}},
				{"visible fact text", "11.8", func(fields map[string]any) {
					fields["facts"].([]any)[0].(map[string]any)["text"] = "实际余量11.8升"
				}},
				{"visible goal text", "11.8", func(fields map[string]any) { fields["current_goal"] = "根据11.8升作决定" }},
				{"timestamp-looking fact", "11.8", func(fields map[string]any) {
					fields["facts"].([]any)[0].(map[string]any)["text"] = stamp
				}},
				{"nested same-named timestamp", "11.8", func(fields map[string]any) {
					fields["facts"].([]any)[0].(map[string]any)[container.timestamp] = stamp
				}},
				{"malformed timestamp text", "作者秘密泵量", func(fields map[string]any) {
					fields[container.timestamp] = "作者秘密泵量11.8"
				}},
				{"numeric timestamp", "11.8", func(fields map[string]any) { fields[container.timestamp] = 11.8 }},
				{"object timestamp", "actual_amount", func(fields map[string]any) {
					fields[container.timestamp] = map[string]any{"actual_amount": 11.8}
				}},
				{"secret resource name", "作者秘密泵量", func(fields map[string]any) { fields["name"] = "作者秘密泵量" }},
				{"actual balance key", "actual_amount", func(fields map[string]any) { fields["actual_amount"] = 12 }},
				{"nested actual balance key", "actual_amount", func(fields map[string]any) {
					fields["facts"].([]any)[0].(map[string]any)["actual_amount"] = 12
				}},
			} {
				t.Run(leak.name, func(t *testing.T) {
					var fields map[string]any
					if err := json.Unmarshal(original, &fields); err != nil {
						t.Fatal(err)
					}
					leak.mutate(fields)
					raw, err := json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(container.view(t, raw)), leak.needle) {
						t.Fatal("privacy view hid actual author truth or a nested fact field")
					}
				})
			}
		})
	}
}
