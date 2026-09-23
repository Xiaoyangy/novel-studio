package modelinput

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const PlanningProjectionModelViewPolicyV1 = "planning-observable-effects-text-shared.v1"

const planningProjectionModelViewHelpV1 = "这是已验证主角投影的无损文字表示，不增加事实或权限。items逐项对应原observable_effects，顺序和条目身份不变。每项普通字符串原样使用；数组项按顺序将字符串原样保留、整数替换为shared_text从0开始的对应字符串，再直接拼接，不增删空格、标点或换行。字典字符串不递归解释，字符串中的数字、@ref、$v均为原文。共享文字不合并事件，旧周期状态不能当成当前状态。source_binding与projection_digest绑定原始来源，其他projection字段保持原义。"

// PlanningProjectionSourceBinding identifies the already-verified source. This
// transport is not an authority receipt and never authenticates source data.
type PlanningProjectionSourceBinding struct {
	SimulationID     string `json:"simulation_id"`
	SimulationDigest string `json:"simulation_digest"`
}

type planningEffectsEnvelopeV1 struct {
	Encoding         string                          `json:"encoding"`
	SourceBinding    PlanningProjectionSourceBinding `json:"source_binding"`
	ProjectionDigest string                          `json:"projection_digest"`
	Instructions     string                          `json:"instructions"`
	SharedText       []string                        `json:"shared_text"`
	Items            []any                           `json:"items"`
}

func planningProjectionDigest(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }

func validPlanningProjectionSource(binding PlanningProjectionSourceBinding) bool {
	digest := strings.TrimPrefix(binding.SimulationDigest, "sha256:")
	if binding.SimulationID == "" || len(digest) != 64 || binding.SimulationDigest != "sha256:"+digest {
		return false
	}
	for _, r := range digest {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func planningObservableEffectStrings(raw []byte) ([]string, error) {
	var items []any
	if err := decodeCharacterMemoryJSONV1(raw, &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, nil
	}
	effects := make([]string, len(items))
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("observable effect %d must be a string", i)
		}
		effects[i] = text
	}
	return effects, nil
}

// EncodePlanningProjectionModelViewV1 changes only observable_effects. The
// caller must first reject the ordinary planning payload on its old budget and
// verify the independent multi-cycle source. Plain fallbacks are exact bytes.
func EncodePlanningProjectionModelViewV1(raw []byte, binding PlanningProjectionSourceBinding) ([]byte, bool, error) {
	if !validPlanningProjectionSource(binding) {
		return nil, false, fmt.Errorf("invalid planning projection source binding")
	}
	var projection map[string]json.RawMessage
	if err := decodeCharacterMemoryJSONV1(raw, &projection); err != nil {
		return nil, false, err
	}
	if projection == nil || len(projection["observable_effects"]) == 0 {
		return nil, false, fmt.Errorf("missing observable effects")
	}
	effects, err := planningObservableEffectStrings(projection["observable_effects"])
	if err != nil {
		return nil, false, err
	}
	if len(effects) == 0 {
		return raw, false, nil
	}
	canonical, err := json.Marshal(projection)
	if err != nil {
		return nil, false, err
	}
	counts := map[string]int{}
	var order []string
	for _, text := range effects {
		for _, part := range characterMemorySegmentsV1(text) {
			if counts[part] == 0 {
				order = append(order, part)
			}
			counts[part]++
		}
	}
	dictionary := []string{}
	indices := map[string]int{}
	for _, part := range order {
		literal, _ := json.Marshal(part)
		refBytes := len(strconv.Itoa(len(dictionary)))
		if counts[part] > 1 && counts[part]*(len(literal)-refBytes) > len(literal)+1 {
			indices[part] = len(dictionary)
			dictionary = append(dictionary, part)
		}
	}
	if len(dictionary) == 0 {
		return raw, false, nil
	}
	items := make([]any, len(effects))
	for i, text := range effects {
		var parts []any
		var literal strings.Builder
		used := false
		flush := func() {
			if literal.Len() > 0 {
				parts = append(parts, literal.String())
				literal.Reset()
			}
		}
		for _, part := range characterMemorySegmentsV1(text) {
			if index, exists := indices[part]; exists {
				flush()
				parts = append(parts, index)
				used = true
			} else {
				literal.WriteString(part)
			}
		}
		flush()
		if used {
			items[i] = parts
		} else {
			items[i] = text
		}
	}
	envelope := planningEffectsEnvelopeV1{PlanningProjectionModelViewPolicyV1, binding, planningProjectionDigest(canonical), planningProjectionModelViewHelpV1, dictionary, items}
	projection["observable_effects"], err = json.Marshal(envelope)
	if err != nil {
		return nil, false, err
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		return nil, false, err
	}
	if len(encoded) >= len(raw) || utf8.RuneCount(encoded) >= utf8.RuneCount(raw) {
		return raw, false, nil
	}
	return encoded, true, nil
}

// DecodePlanningProjectionModelViewV1 reconstructs the exact original strings
// and verifies the projection digest, including every unchanged field. A caller
// supplies the expected verified source binding; the envelope cannot select it.
func DecodePlanningProjectionModelViewV1(raw []byte, expected PlanningProjectionSourceBinding) ([]byte, error) {
	if !validPlanningProjectionSource(expected) {
		return nil, fmt.Errorf("invalid expected planning projection source")
	}
	var projection map[string]json.RawMessage
	if err := decodeCharacterMemoryJSONV1(raw, &projection); err != nil {
		return nil, err
	}
	if projection == nil || len(projection["observable_effects"]) == 0 {
		return nil, fmt.Errorf("missing observable effects")
	}
	if _, err := planningObservableEffectStrings(projection["observable_effects"]); err == nil {
		return raw, nil
	}
	var envelope planningEffectsEnvelopeV1
	if err := decodeCharacterMemoryJSONV1(projection["observable_effects"], &envelope); err != nil {
		return nil, err
	}
	if envelope.Encoding != PlanningProjectionModelViewPolicyV1 || envelope.SourceBinding != expected || envelope.Instructions != planningProjectionModelViewHelpV1 || len(envelope.SharedText) == 0 || len(envelope.Items) == 0 {
		return nil, fmt.Errorf("invalid planning effects envelope or source binding")
	}
	effects := make([]string, len(envelope.Items))
	total := 0
	for i, item := range envelope.Items {
		var parts []string
		switch item := item.(type) {
		case string:
			parts = []string{item}
		case []any:
			if len(item) == 0 {
				return nil, fmt.Errorf("empty encoded effect %d", i)
			}
			for _, part := range item {
				switch part := part.(type) {
				case string:
					parts = append(parts, part)
				case json.Number:
					index, err := strconv.Atoi(string(part))
					if err != nil || index < 0 || index >= len(envelope.SharedText) || strconv.Itoa(index) != string(part) {
						return nil, fmt.Errorf("invalid effect %d dictionary index", i)
					}
					parts = append(parts, envelope.SharedText[index])
				default:
					return nil, fmt.Errorf("invalid effect %d part", i)
				}
			}
		default:
			return nil, fmt.Errorf("invalid effect %d type", i)
		}
		for _, part := range parts {
			if len(part) > characterMemoryViewMaxBytes-total {
				return nil, fmt.Errorf("expanded planning effects exceed decoding size limit")
			}
			total += len(part)
		}
		effects[i] = strings.Join(parts, "")
	}
	var err error
	projection["observable_effects"], err = json.Marshal(effects)
	if err != nil {
		return nil, err
	}
	decoded, err := json.Marshal(projection)
	if err != nil {
		return nil, err
	}
	if len(decoded) > characterMemoryViewMaxBytes {
		return nil, fmt.Errorf("expanded planning projection exceeds decoding size limit")
	}
	if planningProjectionDigest(decoded) != envelope.ProjectionDigest {
		return nil, fmt.Errorf("planning projection digest mismatch")
	}
	return decoded, nil
}
