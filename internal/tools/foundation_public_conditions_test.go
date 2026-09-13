package tools

import (
	"strings"
	"testing"
)

// This guards the creation/repair instructions, not completeness of arbitrary
// authored prose. Runtime still exposes only explicitly authorized views.
func TestFoundationPublicOperationGuidanceReachesCreationAndRepair(t *testing.T) {
	tool := NewSaveFoundationTool(nil)
	description := tool.Schema()["properties"].(map[string]any)["content"].(map[string]any)["description"].(string)
	for _, help := range []string{description, foundationShapeHint("world_rules"), foundationShapeHint("world_codex")} {
		if strings.Count(help, publicCharacterOperationHint) != 1 {
			t.Fatal("public operation contract is missing or duplicated")
		}
		for _, required := range []string{"必要工时", "数值门槛", "costs/timing/preconditions", "未观测实例数量或秘密", "不得为了补齐视图新造固定工时", "counterfactual_tests"} {
			if !strings.Contains(help, required) {
				t.Fatalf("missing public/private execution boundary %q", required)
			}
		}
	}
}
