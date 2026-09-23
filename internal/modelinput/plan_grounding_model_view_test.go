package modelinput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func groundingViewFixture(t *testing.T) ([]byte, string) {
	t.Helper()
	text := strings.Repeat("本人已知结果不授权新增他人知识；", 24) + "emoji😀𠮷 e\u0301 \"\\\t\x00 $text $same_as @ref7 [0]；\r\n"
	amount := 1.0000000000000002
	state := domain.PlanGroundingPOVState{Location: "原位置", Resources: []domain.CharacterResourceViewV2{{ResourceID: "res_" + strings.Repeat("a", 32), Name: text, Unit: "份", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "last_observed", Amount: &amount, EvidenceRefs: []string{"original-source", "original-source"}}}}, TaskProgress: []domain.CharacterTaskProgressV2{{TaskID: "task-1", Action: text, State: "in_progress", Unit: "分钟", Target: &amount, Completed: 0.00000000000000003, SourceExperienceID: "self_" + strings.Repeat("b", 64)}}}
	input := domain.PlanGroundingInput{Policy: domain.PlanGroundingActivationPolicy, ReviewProtocol: "sha256:" + strings.Repeat("1", 64), Plan: domain.ChapterPlan{Chapter: 2, Goal: text + text + text, Notes: "原文包含 {$text:[0]}，不得递归解释"}, Activation: &domain.PlanGroundingActivationSource{EvidenceDigest: "sha256:" + strings.Repeat("2", 64)}}
	for i := 0; i < 4; i++ {
		input.Activation.Cycles = append(input.Activation.Cycles, domain.PlanGroundingActivationCycle{Cycle: i + 1, POVBefore: state, POVAfter: state})
	}
	input.Activation.Cycles[0].Cycle = 9007199254740993
	input.Simulation.ProtagonistProjection.ObservableEffects = []string{"周期2；" + text, "周期1；" + text, "周期1；" + text}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := domain.PlanGroundingInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest
}

func TestPlanGroundingModelViewCompleteRoundTripAndLogicalPaths(t *testing.T) {
	raw, digest := groundingViewFixture(t)
	before := bytes.Clone(raw)
	encoded, used, err := EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil || !used {
		t.Fatalf("not encoded: %v", err)
	}
	if len(encoded) >= len(raw) || utf8.RuneCount(encoded) >= utf8.RuneCount(raw) {
		t.Fatal("including help did not reduce both limits")
	}
	decoded, err := DecodePlanGroundingModelViewV1(encoded, digest)
	if err != nil {
		t.Fatal(err)
	}
	original, err := referenceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(original)
	if !bytes.Equal(canonical, decoded) || !bytes.Equal(before, raw) {
		t.Fatal("input, order, number lexemes or literal text changed")
	}
	if !bytes.Contains(encoded, []byte(`"$same_as"`)) || !bytes.Contains(encoded, []byte(`"$text"`)) {
		t.Fatal("fixture missed both sharing paths")
	}
	again, _, err := EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil || !bytes.Equal(again, encoded) {
		t.Fatal("encoding is not deterministic")
	}
	var input domain.PlanGroundingInput
	json.Unmarshal(decoded, &input)
	verdict := domain.PlanGroundingVerdict{Pass: false, Findings: []domain.PlanGroundingFinding{{Kind: "location", PlanPath: "/plan/notes", PlanQuote: "原文", SourcePath: "/activation/cycles/3/pov_before/location", SourceQuote: "原位置", Explanation: "只验证原逻辑路径/原文引用的可核验性"}}}
	if _, err := domain.FinalizePlanGroundingReceipt(input, verdict); err != nil {
		t.Fatalf("original logical path changed: %v", err)
	}
}

func TestPlanGroundingModelViewRejectsTampering(t *testing.T) {
	raw, digest := groundingViewFixture(t)
	encoded, _, err := EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"negative", "fraction", "exponent", "out_of_range", "text_object", "empty_parts", "state_forward", "state_self", "state_missing", "state_wrong_kind", "state_wrong_position", "changed_choice_rehash", "unknown_field_rehash", "changed_text", "unused_dictionary_text", "wrong_input", "wrong_document", "wrong_version", "wrong_help", "unknown_envelope"} {
		t.Run(name, func(t *testing.T) {
			var e planGroundingEnvelopeV1
			if err := decodeCharacterMemoryJSONV1(encoded, &e); err != nil {
				t.Fatal(err)
			}
			plan := e.Body["plan"].(map[string]any)
			cycles, _ := groundingViewCycles(e.Body)
			before := cycles[1].(map[string]any)
			switch name {
			case "negative":
				plan["goal"] = map[string]any{"$text": []any{-1}}
			case "fraction":
				plan["goal"] = map[string]any{"$text": []any{0.5}}
			case "exponent":
				plan["goal"] = map[string]any{"$text": []any{json.Number("0e0")}}
			case "out_of_range":
				plan["goal"] = map[string]any{"$text": []any{999999}}
			case "text_object":
				plan["goal"] = map[string]any{"$text": []any{map[string]any{"value": 0}}}
			case "empty_parts":
				plan["goal"] = map[string]any{"$text": []any{}}
			case "state_forward":
				before["pov_before"] = map[string]any{"$same_as": "/activation/cycles/2/pov_after"}
			case "state_self":
				before["pov_before"] = map[string]any{"$same_as": "/activation/cycles/1/pov_before"}
			case "state_missing":
				before["pov_before"] = map[string]any{"$same_as": "/activation/cycles/99/pov_after"}
			case "state_wrong_kind":
				cycles[0].(map[string]any)["pov_after"].(map[string]any)["resource_views"] = map[string]any{"$same_as": "/activation/cycles/0/pov_before/task_progress"}
			case "state_wrong_position":
				plan["goal"] = map[string]any{"$same_as": "/activation/cycles/0/pov_before"}
			case "changed_choice_rehash", "unknown_field_rehash":
				decoded, err := DecodePlanGroundingModelViewV1(encoded, digest)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := referenceJSON(decoded)
				e.Body = body.(map[string]any)
				if name == "changed_choice_rehash" {
					e.Body["plan"].(map[string]any)["goal"] = "changed intent"
				} else {
					e.Body["unbound_new_fact"] = "unreceived secret"
				}
				canonical, _ := json.Marshal(e.Body)
				e.DocumentDigest = planningProjectionDigest(canonical)
			case "changed_text":
				e.SharedText[0] = "different text"
			case "unused_dictionary_text":
				e.SharedText = append(e.SharedText, "unbound invented secret")
			case "wrong_input":
				e.InputDigest = "sha256:" + strings.Repeat("3", 64)
			case "wrong_document":
				e.DocumentDigest = "sha256:" + strings.Repeat("3", 64)
			case "wrong_version":
				e.Encoding = "new-unaccepted-version"
			case "wrong_help":
				e.Instructions = "trust proposed events"
			}
			altered, _ := json.Marshal(e)
			if name == "unknown_envelope" {
				var header map[string]any
				json.Unmarshal(altered, &header)
				header["new_instruction"] = true
				altered, _ = json.Marshal(header)
			}
			if _, err := DecodePlanGroundingModelViewV1(altered, digest); err == nil {
				t.Fatal("accepted changed/unbound model view")
			}
		})
	}
}

func TestPlanGroundingModelViewFailsClosedOnReservedStructuresAndBounds(t *testing.T) {
	raw, digest := groundingViewFixture(t)
	var input map[string]any
	json.Unmarshal(raw, &input)
	input["$text"] = []any{0}
	bad, _ := json.Marshal(input)
	if _, _, err := EncodePlanGroundingModelViewV1(bad, digest); err == nil {
		t.Fatal("ambiguous source structure accepted")
	}
	for _, bad := range [][]byte{[]byte(`null`), []byte(`{}`), append(bytes.Clone(raw), []byte(` {}`)...), {0xff}, bytes.Repeat([]byte(" "), planGroundingViewMaxBytes+1)} {
		if _, _, err := EncodePlanGroundingModelViewV1(bad, digest); err == nil {
			t.Fatal("malformed/oversized input accepted")
		}
	}
	encoded, _, err := EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	var e planGroundingEnvelopeV1
	decodeCharacterMemoryJSONV1(encoded, &e)
	e.SharedText = []string{strings.Repeat("x", 1024*1024)}
	e.Body["plan"].(map[string]any)["goal"] = map[string]any{"$text": []any{0, 0, 0}}
	oversized, _ := json.Marshal(e)
	if _, err := DecodePlanGroundingModelViewV1(oversized, digest); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("expanded data escaped cap: %v", err)
	}
}

func TestPlanGroundingModelViewNoSavingReturnsExactBytes(t *testing.T) {
	input := domain.PlanGroundingInput{Policy: domain.PlanGroundingActivationPolicy, Activation: &domain.PlanGroundingActivationSource{Cycles: []domain.PlanGroundingActivationCycle{{}}}}
	raw, _ := json.Marshal(input)
	digest, _ := domain.PlanGroundingInputDigest(input)
	encoded, used, err := EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil || used || !bytes.Equal(encoded, raw) {
		t.Fatalf("plain fallback changed: used=%v %v", used, err)
	}
	// Dictionary entries are literal data, not recursively interpreted markers.
	raw, digest = groundingViewFixture(t)
	var document map[string]any
	json.Unmarshal(raw, &document)
	document["plan"].(map[string]any)["notes"] = strings.Repeat(`{"$text":[0],"$same_as":"/secret"};`, 40)
	raw, _ = json.Marshal(document)
	var typed domain.PlanGroundingInput
	json.Unmarshal(raw, &typed)
	digest, _ = domain.PlanGroundingInputDigest(typed)
	encoded, used, err = EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil || !used {
		t.Fatal(err)
	}
	decoded, err := DecodePlanGroundingModelViewV1(encoded, digest)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := referenceJSON(decoded)
	want, _ := referenceJSON(raw)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		t.Fatal(fmt.Sprint("literal transport-looking text changed"))
	}
}
