package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
)

func TestSaveWorldCodexRejectsUnknownAuthorFields(t *testing.T) {
	st := codexTestStore(t)
	content := validWorldCodexContent()
	content["equipment"] = map[string]any{"evidence_reader": "只能离线读取封存凭证"}
	content["evidence_contract"] = map[string]any{"prior_sequence": []string{"Q_open", "Q_verify", "Q_seal"}}
	args, err := json.Marshal(map[string]any{"type": "world_codex", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); !errors.Is(err, errs.ErrToolArgs) || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "sections[].content/rules") {
		t.Fatalf("unknown author mechanism fields did not return actionable schema error: %v", err)
	}
	if codex, err := st.LoadWorldCodex(); err != nil || codex != nil {
		t.Fatalf("rejected fields published a codex: %+v %v", codex, err)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), worldCodexDraftRel)); !os.IsNotExist(err) {
		t.Fatalf("rejected fields modified the draft: %v", err)
	}
}

func TestWorldCodexUnknownFieldsCannotBypassRepairFallback(t *testing.T) {
	for _, content := range []string{
		`{"evidence_contract":{"prior_sequence":["Q_open","Q_seal"]},}`,
		`{"sections":{"law_order":{"content":"核验必须有原件","evidence_contract":{"prior_sequence":["Q_open"]}}}}`,
		`{"mechanisms":[{"id":"check","actor_scope":"持证人","character_view":{"name":"公开核验","unsupported_secret":"隐藏行动顺序"}}]}`,
	} {
		var codex domain.WorldCodex
		err := decodeFoundationJSON("world_codex", normalizeWorldCodexContent(content), &codex)
		if !errors.Is(err, errs.ErrToolArgs) || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("unknown field bypassed decoder fallback: %s: %v", content, err)
		}
	}
}

func TestWorldCodexRejectedPartialPreservesAcceptedDraft(t *testing.T) {
	st := codexTestStore(t)
	tool := NewSaveFoundationTool(st)
	complete := validWorldCodexContent()
	sections := complete["sections"]
	delete(complete, "sections")
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": complete})
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "已合并暂存") {
		t.Fatalf("partial was not staged: %v", err)
	}
	draftPath := filepath.Join(st.Dir(), worldCodexDraftRel)
	before, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{"sections": sections, "evidence_contract": map[string]any{"prior_sequence": []string{"Q_open", "Q_verify", "Q_seal"}}}
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": bad})
	if _, err := tool.Execute(context.Background(), args); !errors.Is(err, errs.ErrToolArgs) {
		t.Fatalf("expected schema rejection, got %v", err)
	}
	after, err := os.ReadFile(draftPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected partial changed accepted draft")
	}
	// Preserve the previously unsupported domain information in an existing
	// section, then submit only the missing block rather than the full codex.
	for _, section := range sections.([]map[string]any) {
		if section["key"] == "law_order" {
			section["content"] = "证据流程顺序固定为 Q_open → Q_verify → Q_seal；读证设备只能离线核验封存凭证。"
		}
	}
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": map[string]any{"sections": sections}})
	saved, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("supported partial recovery failed: %v", err)
	}
	var receipt struct {
		Version      int `json:"version"`
		AbilityTiers int `json:"ability_tiers"`
	}
	if err := json.Unmarshal(saved, &receipt); err != nil || receipt.Version != 1 || receipt.AbilityTiers != 2 {
		t.Fatalf("partial save reported unmerged state: %s %v", saved, err)
	}
	codex, err := st.LoadWorldCodex()
	if err != nil || codex == nil || codex.CharacterViewVersion != domain.CurrentWorldCharacterViewVersion {
		t.Fatalf("missing explicit protocol after merge: %+v %v", codex, err)
	}
	if len(codex.AbilityTiers) != 2 || len(codex.Mechanisms) != 1 {
		t.Fatal("partial recovery lost previous blocks")
	}
	preserved := false
	for _, section := range codex.Sections {
		if section.Key == "law_order" {
			preserved = strings.Contains(section.Content, "Q_verify")
		}
	}
	if !preserved {
		t.Fatal("domain information was lost during schema repair")
	}
}

func TestNewWorldCodexCannotOmitCharacterViewProtocol(t *testing.T) {
	st := codexTestStore(t)
	tool := NewSaveFoundationTool(st)
	content := validWorldCodexContent()
	mechanisms := content["mechanisms"].([]map[string]any)
	view := mechanisms[0]["character_view"]
	delete(mechanisms[0], "character_view")
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": content})
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "character_view") {
		t.Fatalf("missing view was accepted by omitting protocol: %v", err)
	}
	draft := tool.loadWorldCodexDraft()
	if draft == nil || draft.CharacterViewVersion != domain.CurrentWorldCharacterViewVersion {
		t.Fatalf("new draft protocol not pinned: %+v", draft)
	}
	mechanisms[0]["character_view"] = view
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": map[string]any{"mechanisms": mechanisms}})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("view-only block repair failed: %v", err)
	}
	codex, err := st.LoadWorldCodex()
	if err != nil || codex == nil || codex.CharacterViewVersion != domain.CurrentWorldCharacterViewVersion {
		t.Fatalf("missing pinned protocol: %+v %v", codex, err)
	}
}

func TestHistoricalWorldCodexRemainsReadableUntilExplicitViewUpgrade(t *testing.T) {
	st := codexTestStore(t)
	content := validWorldCodexContent()
	delete(content["mechanisms"].([]map[string]any)[0], "character_view")
	raw, _ := json.Marshal(content)
	var historical domain.WorldCodex
	if err := json.Unmarshal(raw, &historical); err != nil {
		t.Fatal(err)
	}
	historical.Version = 1
	if err := st.SaveWorldCodex(historical); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "world_codex.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadWorldCodex()
	if err != nil || loaded.CharacterViewVersion != 0 {
		t.Fatalf("historical load upgraded data: %+v %v", loaded, err)
	}
	content["character_view_version"] = domain.CurrentWorldCharacterViewVersion
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": content, "change_reason": "升级角色视图", "change_evidence": "用户要求独立角色决策"})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "character_view") {
		t.Fatalf("explicit upgrade accepted missing view: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed upgrade rewrote historical codex")
	}
}
