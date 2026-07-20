package aigc

import (
	"encoding/json"
	"fmt"
	"strings"
)

var proseRenderPacketSectionNames = [...]string{
	"working_memory",
	"episodic_memory",
	"reference_pack",
	"selected_memory",
}

const (
	// ProseRenderCompatibilityProtocolVersion identifies the deterministic
	// prospective anti-AI contract supplied to historical frozen v11 packets.
	// The compatibility overlay is model/provider independent and is applied
	// only to an in-memory prose-facing copy; sealed payload bytes stay immutable.
	ProseRenderCompatibilityProtocolVersion = "anti_ai_render_contract-v1-prospective"
	ProseRenderCompatibilityPacketVersion   = 11
	// ProseRenderUsagePolicyV1 is the only natural-language serialization of
	// the typed v1 before-draft execution mode. Validators compare it exactly;
	// substring matches would incorrectly admit negations such as “不在首稿前执行”.
	ProseRenderUsagePolicyV1 ProseRenderUsagePolicy = "首稿前执行；章级优先。"
)

// ProseRenderUsagePolicy gives the prospective execution mode a typed,
// versioned identity instead of treating arbitrary prose as authorization.
type ProseRenderUsagePolicy string

// ValidateProseRenderContractV11 validates the complete prospective contract
// shape consumed before prose generation. A canonical usage_policy alone is
// not sufficient authorization: all causal/rhythm/dialogue/review baselines
// must be present, typed and non-empty.
func ValidateProseRenderContractV11(value any) error {
	contract, ok := value.(map[string]any)
	if !ok || contract == nil {
		return fmt.Errorf("anti_ai_render_contract must be an object")
	}
	for _, field := range []string{"risk_signals", "counter_moves", "review_checks"} {
		if err := validateProseRenderStringList(field, contract[field]); err != nil {
			return err
		}
	}
	for _, field := range []string{
		"sentence_rhythm_policy",
		"object_response_budget",
		"dialogue_function_plan",
	} {
		text, ok := contract[field].(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("anti_ai_render_contract.%s must be a non-empty string", field)
		}
	}
	usage, ok := contract["usage_policy"].(string)
	if !ok || usage != string(ProseRenderUsagePolicyV1) {
		return fmt.Errorf(
			"anti_ai_render_contract.usage_policy=%q, want exact typed policy %q",
			usage,
			ProseRenderUsagePolicyV1,
		)
	}
	if protocol, present := contract["compatibility_protocol"]; present {
		text, ok := protocol.(string)
		if !ok || text != ProseRenderCompatibilityProtocolVersion {
			return fmt.Errorf(
				"anti_ai_render_contract.compatibility_protocol=%v, want %q",
				protocol,
				ProseRenderCompatibilityProtocolVersion,
			)
		}
	}
	return nil
}

func validateProseRenderStringList(field string, value any) error {
	count := 0
	switch values := value.(type) {
	case []string:
		for i, item := range values {
			if strings.TrimSpace(item) == "" {
				return fmt.Errorf("anti_ai_render_contract.%s[%d] must be a non-empty string", field, i)
			}
			count++
		}
	case []any:
		for i, item := range values {
			text, ok := item.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("anti_ai_render_contract.%s[%d] must be a non-empty string", field, i)
			}
			count++
		}
	default:
		return fmt.Errorf("anti_ai_render_contract.%s must be a non-empty string array", field)
	}
	if count == 0 {
		return fmt.Errorf("anti_ai_render_contract.%s must be a non-empty string array", field)
	}
	return nil
}

// ProseRenderCompatibilityResult records what the compatibility protocol did
// to a prose-facing context copy. It is deliberately explicit so callers can
// audit the exact protocol version instead of silently relying on renderer
// prompt behavior.
type ProseRenderCompatibilityResult struct {
	ProtocolVersion          string
	EligiblePackets          int
	ContractsInjected        int
	TimingSafeguardsInjected int
}

// Applied reports whether the dynamic copy received any compatibility field.
func (r ProseRenderCompatibilityResult) Applied() bool {
	return r.ContractsInjected > 0 || r.TimingSafeguardsInjected > 0
}

// SupportsProseRenderCompatibility reports whether a frozen packet version is
// eligible for the historical compatibility protocol. Future packet versions
// must carry their own prospective contract and are never silently relaxed.
func SupportsProseRenderCompatibility(packetVersion int) bool {
	return packetVersion == ProseRenderCompatibilityPacketVersion
}

// FindUniqueProseRenderPacket locates the sole prose packet using the same
// container vocabulary at seal preflight and immediately before provider
// dispatch. Keeping this list centralized prevents a context from passing one
// boundary only to fail at the next because its packet lives in a different
// supported section.
func FindUniqueProseRenderPacket(payload map[string]any) (map[string]any, string, error) {
	if payload == nil {
		return nil, "render_packet", fmt.Errorf("render context must be an object")
	}
	type locatedPacket struct {
		packet map[string]any
		path   string
	}
	located := make([]locatedPacket, 0, 1)
	inspect := func(container map[string]any, path string) error {
		value, present := container["render_packet"]
		if !present {
			return nil
		}
		packet, ok := value.(map[string]any)
		if !ok || packet == nil {
			return fmt.Errorf("%s must be an object", path)
		}
		located = append(located, locatedPacket{packet: packet, path: path})
		return nil
	}
	if err := inspect(payload, "render_packet"); err != nil {
		return nil, "render_packet", err
	}
	for _, sectionName := range proseRenderPacketSectionNames {
		section, ok := payload[sectionName].(map[string]any)
		if !ok || section == nil {
			continue
		}
		path := sectionName + ".render_packet"
		if err := inspect(section, path); err != nil {
			return nil, path, err
		}
	}
	switch len(located) {
	case 0:
		return nil, "render_packet", fmt.Errorf("render_packet is missing")
	case 1:
		return located[0].packet, located[0].path, nil
	default:
		paths := make([]string, 0, len(located))
		for _, item := range located {
			paths = append(paths, item.path)
		}
		return nil, "render_packet", fmt.Errorf(
			"render_packet must appear exactly once; found %s",
			strings.Join(paths, ", "),
		)
	}
}

// ValidateProseRenderPacketV11 is the provider-facing packet validator. Until
// another protocol is explicitly implemented, future versions fail closed
// instead of inheriting v11 semantics by accident.
func ValidateProseRenderPacketV11(packet map[string]any, expectedChapter int) error {
	if packet == nil {
		return fmt.Errorf("render_packet must be an object")
	}
	version, ok := ProseRenderPacketVersion(packet)
	if !ok || version != ProseRenderCompatibilityPacketVersion {
		return fmt.Errorf(
			"render_packet.version=%v, want exact %d",
			packet["version"],
			ProseRenderCompatibilityPacketVersion,
		)
	}
	if expectedChapter > 0 {
		if err := ValidateProseRenderPacketChapter(packet, expectedChapter); err != nil {
			return err
		}
	}
	return ValidateProseRenderContractV11(packet["anti_ai_render_contract"])
}

// ValidateProseRenderPacketChapter binds a prose packet to the actionable
// chapter using the same exact integer rules as provider-side validation.
func ValidateProseRenderPacketChapter(packet map[string]any, expectedChapter int) error {
	if packet == nil || expectedChapter <= 0 {
		return fmt.Errorf("render_packet chapter validation requires an exact expected chapter")
	}
	chapter, ok := proseRenderExactJSONInt(packet["chapter"])
	if !ok || chapter != expectedChapter {
		return fmt.Errorf("render_packet.chapter=%v, want %d", packet["chapter"], expectedChapter)
	}
	return nil
}

// ApplyProseRenderCompatibilityContracts upgrades only a private, mutable
// prose-facing context copy. An omitted v11 contract receives the stable
// prospective default before model dispatch. Existing chapter-specific
// contracts and safeguards always win; malformed values and non-v11 packets
// are left untouched so validation can fail closed.
func ApplyProseRenderCompatibilityContracts(selected map[string]any) ProseRenderCompatibilityResult {
	result := ProseRenderCompatibilityResult{ProtocolVersion: ProseRenderCompatibilityProtocolVersion}
	if selected == nil {
		return result
	}

	containers := []map[string]any{selected}
	for _, sectionName := range proseRenderPacketSectionNames {
		if section, ok := selected[sectionName].(map[string]any); ok {
			containers = append(containers, section)
		}
	}
	for _, container := range containers {
		packet, ok := container["render_packet"].(map[string]any)
		packetVersion, versionOK := ProseRenderPacketVersion(packet)
		if !ok || !versionOK || !SupportsProseRenderCompatibility(packetVersion) {
			continue
		}
		result.EligiblePackets++

		contractValue, contractPresent := packet["anti_ai_render_contract"]
		contract, contractObject := contractValue.(map[string]any)
		switch {
		case !contractPresent:
			contract = defaultProseRenderCompatibilityContract()
			packet["anti_ai_render_contract"] = contract
			result.ContractsInjected++
		case !contractObject || ValidateProseRenderContractV11(contract) != nil:
			// A malformed chapter contract is not a legacy omission. Preserve it
			// so preflight can reject the invalid frozen packet.
			continue
		}

		safeguardsValue, safeguardsPresent := packet["event_timing_safeguards"]
		safeguards, safeguardsObject := safeguardsValue.(map[string]any)
		if !safeguardsPresent || safeguardsValue == nil || (safeguardsObject && len(safeguards) == 0) {
			packet["event_timing_safeguards"] = map[string]any{
				"object_response_budget": contract["object_response_budget"],
				"dialogue_function_plan": contract["dialogue_function_plan"],
			}
			result.TimingSafeguardsInjected++
		}
	}
	return result
}

func defaultProseRenderCompatibilityContract() map[string]any {
	return map[string]any{
		"compatibility_protocol": ProseRenderCompatibilityProtocolVersion,
		"risk_signals": []string{
			"证据、规则或流程按计划顺序逐项播报成台账",
			"人物轮流补齐信息形成对白传送带",
			"相邻段落只更换对象却重复说明或验证功能",
			"用密集微动作、动作标签、零散心理句或界面提示代替人物因果",
		},
		"counter_moves": []string{
			"刺激先改变POV的注意、判断或误判，再落成选择与可见后果",
			"同一人物因果现场内合并硬事实，不改变选择的信息压缩或离屏",
			"删掉无需开口的人和只解释规则的话轮，让回应改变选择、权力或关系",
			"主视角过薄时，让独有误判、克制或旧经验真实改变后续做法并留下余波",
		},
		"sentence_rhythm_policy": "句段随观察、犹疑、冲突、决断和余波自然换挡，不按固定间隔机械轮换。",
		"object_response_budget": "物件、屏幕和消息只在改变判断、选择、关系或安全后果时回应，不用等距弹窗反复解释。",
		"dialogue_function_plan": "对白只承担眼前冲突或关系位移，不让人物轮流补齐背景和规则。",
		"review_checks": []string{
			"相邻段落没有只换名词却重复同一功能",
			"核心信息经人物判断、选择和后果落地，而非旁白或对白复述",
			"没有用微动作、动作标签或界面提示代替主观因果",
			"不改变选择与后果的流程已压缩或离屏",
		},
		"usage_policy": string(ProseRenderUsagePolicyV1),
	}
}

// ProseRenderPacketVersion parses a packet version without accepting
// fractional, overflowing, stringly typed or otherwise ambiguous values.
func ProseRenderPacketVersion(packet map[string]any) (int, bool) {
	if packet == nil {
		return 0, false
	}
	return proseRenderExactJSONInt(packet["version"])
}

func proseRenderExactJSONInt(value any) (int, bool) {
	switch version := value.(type) {
	case float64:
		parsed := int(version)
		return parsed, float64(parsed) == version
	case float32:
		parsed := int(version)
		return parsed, float32(parsed) == version
	case int:
		return version, true
	case int8:
		return int(version), true
	case int16:
		return int(version), true
	case int32:
		return int(version), true
	case int64:
		parsed := int(version)
		return parsed, int64(parsed) == version
	case uint:
		parsed := int(version)
		return parsed, parsed >= 0 && uint(parsed) == version
	case uint8:
		return int(version), true
	case uint16:
		return int(version), true
	case uint32:
		parsed := int(version)
		return parsed, parsed >= 0 && uint32(parsed) == version
	case uint64:
		parsed := int(version)
		return parsed, parsed >= 0 && uint64(parsed) == version
	case json.Number:
		parsed, err := version.Int64()
		converted := int(parsed)
		return converted, err == nil && int64(converted) == parsed
	}
	return 0, false
}
