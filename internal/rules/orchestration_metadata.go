package rules

import (
	"regexp"
	"strings"
)

// OrchestrationMetadataLeakRule identifies machine-only planning, retrieval and
// checkpoint fields that must never be rendered as fiction. The detector is
// intentionally key-based: ordinary story words such as “系统” remain valid,
// while exact snake_case orchestration keys are deterministic evidence of a
// context-to-prose leak.
const OrchestrationMetadataLeakRule = "orchestration_metadata_leak"

var orchestrationMetadataKeyRe = regexp.MustCompile(`(?i)(^|[^a-z0-9_])(` +
	`evaluated_body_sha256|candidate_body_sha256|rewrite_body_sha256|` +
	`chapter_world_simulation|world_simulation_id|craft_recall_receipt|` +
	`chapter_plan_details|literary_render_contract|` +
	`simulation_id|render_packet|body_sha256|rewrite_source|plan_details|` +
	`source_refs|source_ref|receipt_id|checkpoint_id|checkpoint_seq|` +
	`checkpoint|sha256` +
	`)([^a-z0-9_]|$)`)

// OrchestrationMetadataLeaks returns one error-level violation per leaked key,
// preserving first-seen order and counting repeats. A raw hash is only treated
// as metadata when its sha256 label is present; arbitrary numbers and ordinary
// fictional system panels are deliberately left alone.
func OrchestrationMetadataLeaks(text string) []Violation {
	counts := make(map[string]int)
	var order []string
	for _, match := range orchestrationMetadataKeyRe.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[2]))
		if key == "" {
			continue
		}
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
	}
	violations := make([]Violation, 0, len(order))
	for _, key := range order {
		violations = append(violations, Violation{
			Rule:     OrchestrationMetadataLeakRule,
			Target:   key,
			Limit:    "小说正文不得包含内部规划、RAG、哈希或 checkpoint 元数据键",
			Actual:   counts[key],
			Severity: SeverityError,
		})
	}
	return violations
}
