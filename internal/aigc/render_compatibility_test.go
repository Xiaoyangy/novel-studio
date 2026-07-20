package aigc

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplyProseRenderCompatibilityContractsInjectsOnlyMissingV11Contract(t *testing.T) {
	context := map[string]any{
		"working_memory": map[string]any{
			"render_packet": map[string]any{"version": float64(11), "chapter": float64(9)},
		},
	}
	result := ApplyProseRenderCompatibilityContracts(context)
	if result.ProtocolVersion != ProseRenderCompatibilityProtocolVersion ||
		result.EligiblePackets != 1 || result.ContractsInjected != 1 ||
		result.TimingSafeguardsInjected != 1 || !result.Applied() {
		t.Fatalf("unexpected compatibility result: %+v", result)
	}
	packet := context["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	contract := packet["anti_ai_render_contract"].(map[string]any)
	if contract["compatibility_protocol"] != ProseRenderCompatibilityProtocolVersion ||
		contract["usage_policy"] != string(ProseRenderUsagePolicyV1) {
		t.Fatalf("v11 compatibility contract missing protocol/policy: %#v", contract)
	}
	if _, ok := packet["event_timing_safeguards"].(map[string]any); !ok {
		t.Fatalf("v11 compatibility timing safeguards missing: %#v", packet)
	}
}

func TestApplyProseRenderCompatibilityContractsPreservesChapterSpecificContract(t *testing.T) {
	chapterContract := map[string]any{
		"risk_signals":           []string{"本章专属风险"},
		"counter_moves":          []string{"本章专属修法"},
		"sentence_rhythm_policy": "本章节奏",
		"object_response_budget": "本章物件预算",
		"dialogue_function_plan": "本章对白计划",
		"review_checks":          []string{"本章专属复核"},
		"usage_policy":           string(ProseRenderUsagePolicyV1),
	}
	chapterSafeguards := map[string]any{"object_response_budget": "章级 timing"}
	context := map[string]any{"render_packet": map[string]any{
		"version":                 11,
		"anti_ai_render_contract": chapterContract,
		"event_timing_safeguards": chapterSafeguards,
	}}
	want, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	result := ApplyProseRenderCompatibilityContracts(context)
	got, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractsInjected != 0 || result.TimingSafeguardsInjected != 0 || result.Applied() {
		t.Fatalf("chapter contract was treated as legacy omission: %+v", result)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chapter-specific contract was overwritten:\nwant=%s\n got=%s", want, got)
	}
}

func TestApplyProseRenderCompatibilityContractsDoesNotRelaxMalformedOrNonV11Packets(t *testing.T) {
	for name, packet := range map[string]map[string]any{
		"malformed contract": {"version": 11, "anti_ai_render_contract": "invalid"},
		"empty contract":     {"version": 11, "anti_ai_render_contract": map[string]any{}},
		"usage only": {"version": 11, "anti_ai_render_contract": map[string]any{
			"usage_policy": string(ProseRenderUsagePolicyV1),
		}},
		"old version":        {"version": 10},
		"future version":     {"version": 12},
		"invalid version":    {"version": "11"},
		"fractional version": {"version": 11.5},
		"overflow version":   {"version": ^uint64(0)},
	} {
		t.Run(name, func(t *testing.T) {
			context := map[string]any{"render_packet": packet}
			before, err := json.Marshal(context)
			if err != nil {
				t.Fatal(err)
			}
			result := ApplyProseRenderCompatibilityContracts(context)
			after, err := json.Marshal(context)
			if err != nil {
				t.Fatal(err)
			}
			if result.Applied() || string(after) != string(before) {
				t.Fatalf("invalid/non-v11 packet was relaxed: result=%+v before=%s after=%s", result, before, after)
			}
		})
	}
}

func TestValidateProseRenderContractV11RequiresCompleteTypedShape(t *testing.T) {
	valid := defaultProseRenderCompatibilityContract()
	if err := ValidateProseRenderContractV11(valid); err != nil {
		t.Fatalf("default compatibility contract is invalid: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"usage only": func(contract map[string]any) {
			for key := range contract {
				delete(contract, key)
			}
			contract["usage_policy"] = string(ProseRenderUsagePolicyV1)
		},
		"empty risk list": func(contract map[string]any) {
			contract["risk_signals"] = []string{}
		},
		"wrong counter type": func(contract map[string]any) {
			contract["counter_moves"] = "not-a-list"
		},
		"missing rhythm": func(contract map[string]any) {
			delete(contract, "sentence_rhythm_policy")
		},
		"negated usage": func(contract map[string]any) {
			contract["usage_policy"] = "不在首稿前执行；章级优先。"
		},
		"unknown protocol": func(contract map[string]any) {
			contract["compatibility_protocol"] = "anti_ai_render_contract-v2"
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract := defaultProseRenderCompatibilityContract()
			mutate(contract)
			if err := ValidateProseRenderContractV11(contract); err == nil {
				t.Fatalf("invalid contract shape passed: %#v", contract)
			}
		})
	}
}

func TestFindUniqueProseRenderPacketUsesOneSharedContainerVocabulary(t *testing.T) {
	for _, location := range []string{
		"root",
		"working_memory",
		"episodic_memory",
		"reference_pack",
		"selected_memory",
	} {
		t.Run(location, func(t *testing.T) {
			packet := map[string]any{"version": 11}
			payload := map[string]any{}
			wantPath := "render_packet"
			if location == "root" {
				payload["render_packet"] = packet
			} else {
				payload[location] = map[string]any{"render_packet": packet}
				wantPath = location + ".render_packet"
			}
			got, path, err := FindUniqueProseRenderPacket(payload)
			if err != nil || !reflect.DeepEqual(got, packet) || path != wantPath {
				t.Fatalf("packet=%#v path=%q err=%v", got, path, err)
			}
		})
	}

	duplicate := map[string]any{
		"render_packet": map[string]any{"version": 11},
		"selected_memory": map[string]any{
			"render_packet": map[string]any{"version": 11},
		},
	}
	if _, _, err := FindUniqueProseRenderPacket(duplicate); err == nil {
		t.Fatal("duplicate packets passed unique packet lookup")
	}
}

func TestValidateProseRenderPacketV11RejectsFutureVersion(t *testing.T) {
	packet := map[string]any{
		"version":                 12,
		"chapter":                 3,
		"anti_ai_render_contract": defaultProseRenderCompatibilityContract(),
	}
	if err := ValidateProseRenderPacketV11(packet, 3); err == nil {
		t.Fatal("future packet inherited v11 semantics")
	}
	packet["version"] = 11
	if err := ValidateProseRenderPacketV11(packet, 3); err != nil {
		t.Fatalf("valid exact v11 packet rejected: %v", err)
	}
}
