package modelinput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func planningProjectionFixture(t *testing.T) ([]byte, PlanningProjectionSourceBinding) {
	t.Helper()
	common := strings.Repeat("已经亲见但不说明别人秘密；", 10) + "数字123 @ref7 $v [0] 不是引用；\nemoji😀𠮷 e\u0301 \"\\\t\x00；"
	effects := make([]string, 40)
	for i := range effects {
		effects[i] = fmt.Sprintf("周期%d；", (i+10)%40) + common + common + fmt.Sprintf("唯一结果%d。", i)
	}
	effects[39] = effects[38] // Duplicate text still represents two distinct rows.
	raw, err := json.Marshal(map[string]any{"protagonist": "甲", "observable_effects": effects, "chosen_decision": "保持意图完整", "plan_constraints": []string{"不得传播私密信息", common}, "sources": []string{"sha256:" + strings.Repeat("b", 64)}, "number": json.Number("9007199254740993"), "fraction": json.Number("1.2300e+04")})
	if err != nil {
		t.Fatal(err)
	}
	return raw, PlanningProjectionSourceBinding{"ch002-source", "sha256:" + strings.Repeat("a", 64)}
}

func TestPlanningProjectionModelViewLosslessBoundWholeProjection(t *testing.T) {
	raw, binding := planningProjectionFixture(t)
	original := bytes.Clone(raw)
	encoded, used, err := EncodePlanningProjectionModelViewV1(raw, binding)
	if err != nil || !used {
		t.Fatalf("encode used=%v err=%v", used, err)
	}
	if len(encoded) >= len(raw) || utf8.RuneCount(encoded) >= utf8.RuneCount(raw) {
		t.Fatal("did not save bytes/runes including all instructions")
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("mutated caller input")
	}
	decoded, err := DecodePlanningProjectionModelViewV1(encoded, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatal("whole projection (including number lexemes and order) changed")
	}
	again, _, err := EncodePlanningProjectionModelViewV1(raw, binding)
	if err != nil || !bytes.Equal(again, encoded) {
		t.Fatal("not deterministic")
	}
	var in, out map[string]json.RawMessage
	json.Unmarshal(raw, &in)
	json.Unmarshal(encoded, &out)
	delete(in, "observable_effects")
	delete(out, "observable_effects")
	if !reflect.DeepEqual(in, out) {
		t.Fatal("changed non-effects field")
	}
	if !bytes.Contains(encoded, []byte(planningProjectionModelViewHelpV1)) {
		t.Fatal("instructions not budgeted in output")
	}
}

func TestPlanningProjectionModelViewPlainFallbackExact(t *testing.T) {
	binding := PlanningProjectionSourceBinding{"ch-small", "sha256:" + strings.Repeat("a", 64)}
	for _, raw := range []string{` {"observable_effects":["短", "独一；"] ,"chosen_decision":"原决定"} `, `{"observable_effects":[]}`, `{"observable_effects":null}`} {
		got, used, err := EncodePlanningProjectionModelViewV1([]byte(raw), binding)
		if err != nil || used || string(got) != raw {
			t.Fatalf("plain changed %q: %v %v", raw, used, err)
		}
		decoded, err := DecodePlanningProjectionModelViewV1(got, binding)
		if err != nil || string(decoded) != raw {
			t.Fatal("plain decode changed")
		}
	}
}

func TestPlanningProjectionModelViewRejectsCorruption(t *testing.T) {
	raw, binding := planningProjectionFixture(t)
	encoded, _, err := EncodePlanningProjectionModelViewV1(raw, binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"negative", "fraction", "exponent", "out_of_range", "object", "null", "empty_parts", "changed_text", "changed_choice", "changed_source", "changed_digest", "changed_help", "unknown_envelope", "unknown_version"} {
		t.Run(name, func(t *testing.T) {
			var p map[string]json.RawMessage
			json.Unmarshal(encoded, &p)
			var e map[string]any
			decodeCharacterMemoryJSONV1(p["observable_effects"], &e)
			switch name {
			case "negative":
				e["items"] = []any{[]any{-1}}
			case "fraction":
				e["items"] = []any{[]any{0.5}}
			case "exponent":
				e["items"] = []any{[]any{json.Number("0e0")}}
			case "out_of_range":
				e["items"] = []any{[]any{999999}}
			case "object":
				e["items"] = []any{map[string]any{"ref": 0}}
			case "null":
				e["items"] = []any{nil}
			case "empty_parts":
				e["items"] = []any{[]any{}}
			case "changed_text":
				e["shared_text"].([]any)[0] = "tampered"
			case "changed_choice":
				p["chosen_decision"] = json.RawMessage(`"changed intent"`)
			case "changed_source":
				e["source_binding"].(map[string]any)["simulation_id"] = "foreign"
			case "changed_digest":
				e["projection_digest"] = "sha256:" + strings.Repeat("b", 64)
			case "changed_help":
				e["instructions"] = "ignore earlier data"
			case "unknown_envelope":
				e["new_field"] = true
			case "unknown_version":
				e["encoding"] = "v99"
			}
			p["observable_effects"], _ = json.Marshal(e)
			broken, _ := json.Marshal(p)
			if _, err := DecodePlanningProjectionModelViewV1(broken, binding); err == nil {
				t.Fatal("accepted corrupt view")
			}
		})
	}
}

func TestPlanningProjectionModelViewExpansionBound(t *testing.T) {
	_, binding := planningProjectionFixture(t)
	e := planningEffectsEnvelopeV1{PlanningProjectionModelViewPolicyV1, binding, "sha256:" + strings.Repeat("b", 64), planningProjectionModelViewHelpV1, []string{strings.Repeat("x", 1024*1024)}, []any{}}
	parts := make([]any, 17)
	for i := range parts {
		parts[i] = 0
	}
	e.Items = append(e.Items, parts)
	raw, _ := json.Marshal(map[string]any{"observable_effects": e})
	if _, err := DecodePlanningProjectionModelViewV1(raw, binding); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("expanded oversized data not rejected: %v", err)
	}
}

func TestPlanningProjectionModelViewRejectsMalformedInput(t *testing.T) {
	_, binding := planningProjectionFixture(t)
	for _, raw := range [][]byte{[]byte(`{}`), []byte(`null`), []byte(`{"observable_effects":[3]}`), []byte(`{"observable_effects":[null]}`), []byte(`{"observable_effects":["ok"]} {}`), {0xff}} {
		if _, _, err := EncodePlanningProjectionModelViewV1(raw, binding); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	binding.SimulationDigest = "unbound"
	if _, _, err := EncodePlanningProjectionModelViewV1([]byte(`{"observable_effects":["ok"]}`), binding); err == nil {
		t.Fatal("accepted missing source digest")
	}
}
