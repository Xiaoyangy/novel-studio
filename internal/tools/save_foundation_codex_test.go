package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func codexTestStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	return s
}

func validWorldCodexContent() map[string]any {
	sections := make([]map[string]any, 0, len(domain.RequiredCodexSections))
	for _, sec := range domain.RequiredCodexSections {
		sections = append(sections, map[string]any{
			"key":     sec.Key,
			"title":   sec.Title,
			"content": "测试设定：" + sec.Title,
			"rules":   []string{"测试规则"},
		})
	}
	return map[string]any{
		"ability_tiers": []map[string]any{
			{"order": 1, "name": "夜租新客", "magnitude": "只能被动接单", "limits": "无法议价", "promotion": "完成首笔有效交易"},
			{"order": 2, "name": "持卡人", "magnitude": "可发起小额契约", "limits": "大额须审计", "promotion": "通过阴司银行初审"},
		},
		"skill_domains":        []map[string]any{{"name": "契约拆解", "description": "读出条款漏洞", "tier_binding": "持卡人"}},
		"races":                []map[string]any{{"name": "人类", "description": "阳间常驻族群", "constraints": []string{"午夜后受夜租约束"}}},
		"weapon_categories":    []map[string]any{{"name": "现代器械", "description": "枪械刀具", "constraints": []string{"对诡异无效"}}},
		"equipment_categories": []map[string]any{{"name": "契约资产", "description": "黑卡/欠条/产权凭证", "grades": []string{"临时", "正式", "审计确权"}}},
		"immutability_policy":  "修订必须提供 change_reason 与 change_evidence，版本自增。",
		"sections":             sections,
	}
}

func TestSaveFoundationWorldCodexLifecycle(t *testing.T) {
	s := codexTestStore(t)
	tool := NewSaveFoundationTool(s)

	// 1) 缺维度必须打回
	incomplete := validWorldCodexContent()
	incomplete["sections"] = []map[string]any{}
	args, _ := json.Marshal(map[string]any{"type": "world_codex", "content": incomplete})
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "sections") {
		t.Fatalf("expected missing sections error, got %v", err)
	}

	// 2) 完整法典保存成功 v1
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": validWorldCodexContent()})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("save world_codex: %v", err)
	}
	codex, err := s.LoadWorldCodex()
	if err != nil || codex == nil || codex.Version != 1 {
		t.Fatalf("expected v1 codex, got %+v err=%v", codex, err)
	}

	// 3) 无修订理由的覆盖必须被拒（不可随意更改）
	args, _ = json.Marshal(map[string]any{"type": "world_codex", "content": validWorldCodexContent()})
	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "change_reason") {
		t.Fatalf("expected immutability rejection, got %v", err)
	}

	// 4) 带理由+证据的修订：版本自增并留 change_log
	updated := validWorldCodexContent()
	updated["ability_tiers"] = append(updated["ability_tiers"].([]map[string]any), map[string]any{
		"order": 3, "name": "资产经理", "magnitude": "可批量确权", "limits": "受审计冻结", "promotion": "资产经理资格预审通过",
	})
	args, _ = json.Marshal(map[string]any{
		"type": "world_codex", "content": updated,
		"change_reason": "第二卷需要开放资产经理层级", "change_evidence": "layered_outline 卷2 阶段目标",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("revise world_codex: %v", err)
	}
	codex, _ = s.LoadWorldCodex()
	if codex.Version != 2 || len(codex.ChangeLog) != 1 {
		t.Fatalf("expected v2 with change log, got v%d log=%d", codex.Version, len(codex.ChangeLog))
	}

	// 5) volume_codex：上限必须引用全局分级名
	badVC, _ := json.Marshal(map[string]any{"type": "volume_codex", "volume": 1, "content": map[string]any{
		"volume": 1, "tier_ceiling": "不存在的等级", "protagonist_ceiling": "持卡人",
	}})
	if _, err := tool.Execute(context.Background(), badVC); err == nil || !strings.Contains(err.Error(), "ability_tiers") {
		t.Fatalf("expected tier reference rejection, got %v", err)
	}
	goodVC, _ := json.Marshal(map[string]any{"type": "volume_codex", "volume": 1, "content": map[string]any{
		"volume": 1, "volume_title": "夜租初临", "tier_ceiling": "持卡人", "protagonist_ceiling": "持卡人",
		"allowed_skill_domains": []string{"契约拆解"}, "forbidden_in_volume": []string{"资产经理权限"},
	}})
	if _, err := tool.Execute(context.Background(), goodVC); err != nil {
		t.Fatalf("save volume_codex: %v", err)
	}
	vc, err := s.LoadVolumeCodex(1)
	if err != nil || vc == nil || vc.TierCeiling != "持卡人" {
		t.Fatalf("expected volume codex saved, got %+v err=%v", vc, err)
	}
}
