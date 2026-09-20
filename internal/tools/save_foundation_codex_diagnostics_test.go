package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestWorldCodexUnknownFieldDiagnosticNamesExactLayer(t *testing.T) {
	content := validWorldCodexContent()
	content["mechanisms"].([]map[string]any)[0]["description"] = "这一层不支持的说明"
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": content})
	_, err := NewSaveFoundationTool(codexTestStore(t)).Execute(t.Context(), args)
	if err == nil || !strings.Contains(err.Error(), "mechanisms[0].description") || !strings.Contains(err.Error(), "本层允许字段") {
		t.Fatalf("UNKNOWN_CODEX_FIELD_LOST_LAYER: real tool must identify the invalid layer, not imply all description fields are forbidden: %v", err)
	}
}

func TestWorldCodexUnknownDiagnosticsRespectObjectLayerAndRepair(t *testing.T) {
	for _, tc := range []struct{ raw, path, allowed string }{
		{`{"description":"bad"}`, "$.description", "ability_tiers"},
		{`{"mechanisms":[{"description":"bad"}]}`, "$.mechanisms[0].description", "trigger"},
		{`{"mechanisms":[{"character_view":{"description":"bad"}}]}`, "$.mechanisms[0].character_view.description", "timing"},
		{`{"sections":{"law_order":{"description":"bad"}}}`, "$.sections[0].description", "content"},
		{`{"mechanisms":[{}, {"description":"bad"}],}`, "$.mechanisms[1].description", "actor_scope"},
		{`{"mechanisms":[{"actor_scope":"持证人","character_view":{"description":"bad"}}]}`, "$.mechanisms[0].character_view.description", "observability"},
	} {
		t.Run(tc.path+tc.allowed, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": tc.raw})
			st := codexTestStore(t)
			_, err := NewSaveFoundationTool(st).Execute(t.Context(), args)
			if !errors.Is(err, errs.ErrToolArgs) || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("wrong strict-error diagnostic: %v", err)
			}
			_, allowed, ok := strings.Cut(err.Error(), "本层允许字段：")
			allowed, _, _ = strings.Cut(allowed, "。")
			if !ok || !strings.Contains(allowed, tc.allowed) || strings.Contains(allowed, "description") {
				t.Fatalf("wrong containing-layer field inventory: %s", allowed)
			}
			if draft := NewSaveFoundationTool(st).loadWorldCodexDraft(); draft != nil {
				t.Fatal("unknown field was staged")
			}
		})
	}
	// All four legitimately declared description fields survive the same tool.
	st := codexTestStore(t)
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": validWorldCodexContent()})
	if _, err := NewSaveFoundationTool(st).Execute(t.Context(), args); err != nil {
		t.Fatal(err)
	}
	saved, err := st.LoadWorldCodex()
	if err != nil || saved == nil || saved.Races[0].Description != "阳间常驻族群" || saved.WeaponCategories[0].Description != "枪械刀具" || saved.SkillDomains[0].Description != "读出条款漏洞" || saved.EquipmentCategories[0].Description != "黑卡/欠条/产权凭证" {
		t.Fatal("legal descriptions changed", err)
	}
}

func TestWorldCodexUnknownDiagnosticFollowsDecoderOrderNotMapOrder(t *testing.T) {
	for _, raw := range []string{
		`{"mechanisms":[{"character_view":{"description":"first"},"description":"later"}]}`,
		`{"mechanisms":[{"name":"valid"}],"mechanisms":[{"character_view":{"description":"first"}}],"description":"later"}`,
	} {
		var codex domain.WorldCodex
		err := decodeFoundationJSON("world_codex", raw, &codex)
		if err == nil || !strings.Contains(err.Error(), "$.mechanisms[0].character_view.description") {
			t.Fatalf("diagnostic mislocated the decoder's first unknown field: %v", err)
		}
	}
}

func TestWorldCodexOperationFailureWithoutReadableDraftDoesNotInventState(t *testing.T) {
	for _, kind := range []string{"absent", "corrupt", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			st := codexTestStore(t)
			path := filepath.Join(st.Dir(), worldCodexDraftRel)
			want := "当前没有已暂存草稿"
			switch kind {
			case "corrupt":
				if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
					t.Fatal(err)
				}
				want = "草稿文件无法解析"
			case "unreadable":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				want = "草稿状态读取失败"
			}
			content := validWorldCodexContent()
			delete(content["equipment_categories"].([]map[string]any)[0], "grades")
			args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": content})
			_, err := NewSaveFoundationTool(st).Execute(t.Context(), args)
			if !errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "本次提交未保存、未合并草稿") || !strings.Contains(err.Error(), want) {
				t.Fatalf("incorrect persistence claim: %v", err)
			}
			if strings.Contains(err.Error(), "skill_domains=0") {
				t.Fatal("uncertain/absent draft was represented as a decoded empty draft")
			}
		})
	}
}

func TestWorldCodexOperationFailureReportsUnchangedDraft(t *testing.T) {
	st := codexTestStore(t)
	tool := NewSaveFoundationTool(st)
	seed := validWorldCodexContent()
	skills, weapons := seed["skill_domains"], seed["weapon_categories"]
	delete(seed, "skill_domains")
	delete(seed, "weapon_categories")
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": seed})
	if _, err := tool.Execute(t.Context(), args); err == nil || !strings.Contains(err.Error(), "已合并暂存") {
		t.Fatalf("fixture did not stage original partial: %v", err)
	}
	path := filepath.Join(st.Dir(), worldCodexDraftRel)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	equipment := validWorldCodexContent()["equipment_categories"].([]map[string]any)
	delete(equipment[0], "grades")
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": map[string]any{"skill_domains": skills, "weapon_categories": weapons, "equipment_categories": equipment}})
	_, err = tool.Execute(t.Context(), args)
	if err == nil || !strings.Contains(err.Error(), "操作合同") {
		t.Fatalf("fixture did not reach real operational validation: %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatal("failed operational submission changed actual draft", readErr)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(before))
	for _, want := range []string{"本次提交未保存、未合并草稿", digest, "skill_domains=0", "weapon_categories=0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("REJECTED_CODEX_PARTIAL_LOOKS_STAGED: diagnostic lacks %q: %v", want, err)
		}
	}
	// Reproduce the real repair trap: fixing only grades cannot recover the
	// skills/weapons from the earlier rejected submission, which never staged.
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": map[string]any{"equipment_categories": validWorldCodexContent()["equipment_categories"]}})
	if _, err := tool.Execute(t.Context(), args); err == nil || !strings.Contains(err.Error(), "skill_domains") || !strings.Contains(err.Error(), "weapon_categories") || !strings.Contains(err.Error(), "已合并暂存") {
		t.Fatalf("grade-only repair pretended rejected blocks had been retained: %v", err)
	}
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": map[string]any{"skill_domains": skills, "weapon_categories": weapons}})
	if _, err := tool.Execute(t.Context(), args); err != nil {
		t.Fatalf("explicitly resubmitted missing blocks failed: %v", err)
	}
	saved, err := st.LoadWorldCodex()
	if err != nil || saved == nil || len(saved.SkillDomains) != 1 || len(saved.WeaponCategories) != 1 || len(saved.EquipmentCategories[0].Grades) != 3 {
		t.Fatal("recovery changed accepted codex content", err)
	}
}
