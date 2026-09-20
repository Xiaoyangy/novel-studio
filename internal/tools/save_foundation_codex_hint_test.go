package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorldCodexCategoryHintCanPassActualSaveContract(t *testing.T) {
	// The seam is the real tool's corrective feedback, not a separately
	// maintained example. No model or production source is involved.
	_, hintErr := NewSaveFoundationTool(codexTestStore(t)).Execute(t.Context(), json.RawMessage(`{"type":"world_codex","content":{"unsupported_example":true}}`))
	if hintErr == nil {
		t.Fatal("expected corrective schema feedback")
	}
	for _, field := range []string{"weapon_categories", "equipment_categories"} {
		t.Run(field, func(t *testing.T) {
			_, suffix, ok := strings.Cut(hintErr.Error(), `"`+field+`":`)
			if !ok {
				t.Fatalf("tool feedback omitted %s", field)
			}
			var examples []map[string]json.RawMessage
			// Decode the first JSON array in the hint. The rest of the help is
			// prose; do not invent another format or parser for that prose.
			if err := json.NewDecoder(strings.NewReader(suffix)).Decode(&examples); err != nil {
				t.Fatalf("%s example is not usable JSON: %v", field, err)
			}
			if len(examples) != 1 {
				t.Fatalf("want one complete %s example", field)
			}
			for _, required := range []string{"name", "description", "grades"} {
				if len(examples[0][required]) == 0 {
					t.Fatalf("%s hint omits required %s", field, required)
				}
			}
			// Fill only illustrative values, never add a missing constraints
			// field. Preserve the example's exact field inventory for validation.
			for key, value := range examples[0] {
				examples[0][key] = json.RawMessage(strings.ReplaceAll(string(value), "…", "仅可用于明确的现场用途"))
			}
			if _, ok := examples[0]["tier_binding"]; ok {
				examples[0]["tier_binding"] = json.RawMessage(`"不绑定能力分级"`)
			}
			content := validWorldCodexContent()
			content[field] = examples
			args, err := json.Marshal(map[string]any{"type": "world_codex", "content": content})
			if err != nil {
				t.Fatal(err)
			}
			st := codexTestStore(t)
			if _, err := NewSaveFoundationTool(st).Execute(t.Context(), args); err != nil {
				t.Fatalf("advertised %s shape fails the real decoder/coherence contract: %v", field, err)
			}
			saved, err := st.LoadWorldCodex()
			if err != nil || saved == nil {
				t.Fatalf("saved codex missing: %v", err)
			}
			categories := saved.WeaponCategories
			if field == "equipment_categories" {
				categories = saved.EquipmentCategories
			}
			if len(categories) != 1 || len(categories[0].Constraints) == 0 {
				t.Fatal("hint lost real limits/failure boundaries on save")
			}
		})
	}
}

func TestWorldCodexCategoryRequiredFieldsStillRejectOmissions(t *testing.T) {
	for _, field := range []string{"weapon_categories", "equipment_categories"} {
		for _, required := range []string{"name", "description", "grades", "constraints"} {
			t.Run(field+"/"+required, func(t *testing.T) {
				content := validWorldCodexContent()
				items := content[field].([]map[string]any)
				delete(items[0], required)
				args, err := json.Marshal(map[string]any{"type": "world_codex", "content": content})
				if err != nil {
					t.Fatal(err)
				}
				st := codexTestStore(t)
				if _, err := NewSaveFoundationTool(st).Execute(t.Context(), args); err == nil || !strings.Contains(err.Error(), field) {
					t.Fatalf("missing %s.%s was not rejected by the real contract: %v", field, required, err)
				}
				if saved, err := st.LoadWorldCodex(); err != nil || saved != nil {
					t.Fatalf("invalid category became published codex: %+v %v", saved, err)
				}
			})
		}
	}
}
