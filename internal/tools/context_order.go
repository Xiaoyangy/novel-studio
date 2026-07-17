package tools

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Lost-in-the-Middle 治理：LLM 对输入开头/结尾注意力最高、中段断崖下降（U 形注意力，
// 2026 年生产模型仍存在）。Go 对 map 按 key 字典序序列化，注入文本的物理位置此前完全失控。
// marshalOrderedContext 只控制顶层 key 的序列化顺序，不改 key 名、不改内容：
// 关键信息（进度/本章契约/角色状态）放开头，参考资料落中段，衔接与告警收尾。
// 未登记的 key 自动落中段（字典序），新增容器无需登记即向后兼容。

// contextCriticalFirst 开头段：Writer 必须首先读到的"本章要做什么、角色是谁"。
var contextCriticalFirst = []string{
	"_reading_guide",
	"_loading_summary",
	"progress_status",
	"active_chapter_task",
	"chapter_pipeline_instruction",
	"project_all_state",
	"project_all_state_policy",
	"planning_context_access_receipt",
	"chapter_world_simulation",
	"formal_plan_receipt",
	"render_packet",
	"simulation_authority_receipt",
	"simulation_characters",
	"simulation_character_authority_policy",
	"simulation_character_authority",
	"rewrite_craft_pack",
	"rewrite_craft_status",
	"working_memory",
	"rewrite_brief",
	"rewrite_source",
	"planning_memory",
	"characters",
	"character_snapshots",
	"character_continuity",
	"premise_structure",
	"premise_sections",
	"premise",
}

// contextCriticalLast 结尾段：写作前最后应停留的衔接事实与硬性提醒。
var contextCriticalLast = []string{
	"previous_tail",
	"resource_audit",
	"_warnings",
	"_trimmed",
}

// marshalOrderedContext 按 critical-first → 中段（字典序）→ critical-last 序列化。
func marshalOrderedContext(result map[string]any) (json.RawMessage, error) {
	listed := make(map[string]bool, len(contextCriticalFirst)+len(contextCriticalLast))
	for _, k := range contextCriticalFirst {
		listed[k] = true
	}
	for _, k := range contextCriticalLast {
		listed[k] = true
	}

	var middle []string
	for k := range result {
		if !listed[k] {
			middle = append(middle, k)
		}
	}
	sort.Strings(middle)

	ordered := make([]string, 0, len(result))
	for _, k := range contextCriticalFirst {
		if _, ok := result[k]; ok {
			ordered = append(ordered, k)
		}
	}
	ordered = append(ordered, middle...)
	for _, k := range contextCriticalLast {
		if _, ok := result[k]; ok {
			ordered = append(ordered, k)
		}
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range ordered {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valJSON, err := json.Marshal(result[k])
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// contextReadingGuide 一句话阅读指引，放在开头段首位。
const contextReadingGuide = "阅读顺序指引：开头是进度与本章契约（必须遵循），中段是参考资料（按需查证），结尾是上一章结尾衔接与告警（写作前最后确认）"
