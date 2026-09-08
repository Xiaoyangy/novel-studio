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
		"schema_version": domain.CurrentWorldCodexSchemaVersion,
		"ability_tiers": []map[string]any{
			{"order": 1, "name": "夜租新客", "magnitude": "只能被动接单", "limits": "无法议价", "promotion": "完成首笔有效交易", "cost": "每次核验消耗一小时申诉窗口"},
			{"order": 2, "name": "持卡人", "magnitude": "可发起小额契约", "limits": "大额须审计", "promotion": "通过阴司银行初审", "cost": "冻结等额信用额度"},
		},
		"skill_domains":        []map[string]any{{"name": "契约拆解", "description": "读出条款漏洞", "tier_binding": "持卡人", "constraints": []string{"必须取得原始凭证"}}},
		"races":                []map[string]any{{"name": "人类", "description": "阳间常驻族群", "constraints": []string{"午夜后受夜租约束"}}},
		"weapon_categories":    []map[string]any{{"name": "现代器械", "description": "枪械刀具", "grades": []string{"普通"}, "constraints": []string{"对诡异无效"}}},
		"equipment_categories": []map[string]any{{"name": "契约资产", "description": "黑卡/欠条/产权凭证", "grades": []string{"临时", "正式", "审计确权"}, "constraints": []string{"权属未确认时不能结算"}}},
		"immutability_policy":  "修订必须提供 change_reason 与 change_evidence，版本自增。",
		"sections":             sections,
		"mechanisms": []map[string]any{{
			"id": "contract-settlement", "name": "契约结算", "visibility": "formal", "section_refs": []string{"mechanism_structure", "economy_currency"},
			"actor_scope": []string{"持有原始凭证者"}, "trigger": "持有人发起结算", "preconditions": []string{"凭证有效且权属已确认"},
			"inputs": []string{"有效凭证", "信用额度"}, "costs": []string{"冻结等额信用额度"}, "effects": []string{"债务状态进入已结算"},
			"failure_modes": []string{"凭证无效时驳回并留下审计记录"}, "observability": []string{"当事人收到回执，旁观者只能看到公开状态"}, "timing": "提交后一个工作日",
			"character_view": map[string]any{
				"name": "凭证结算程序", "actor_scope": []string{"持有效原始凭证者"}, "trigger": "提交结算申请",
				"preconditions": []string{"出示权属明确的原始凭证"}, "inputs": []string{"原始凭证"}, "costs": []string{"占用核验时间"},
				"effects": []string{"核验通过后更新结算状态"}, "failure_modes": []string{"凭证不全则退回"},
				"observability": []string{"当事人可查看核验回执"}, "timing": "提交后一个工作日",
			},
		}},
		"counterfactual_tests": []map[string]any{{
			"id": "contract-without-proof", "given": []string{"申请人没有原始凭证"}, "action": "申请结算", "expected_outcome": "驳回并留下审计记录",
			"forbidden_outcome": "因主角急需而口头免除债务", "mechanism_refs": []string{"contract-settlement"},
		}},
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
		"order": 3, "name": "资产经理", "magnitude": "可批量确权", "limits": "受审计冻结", "promotion": "资产经理资格预审通过", "cost": "每次批量确权触发公开审计",
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
	checkpoint := s.Checkpoints.Latest(domain.GlobalScope())
	if checkpoint == nil || checkpoint.Step != "volume_codex" || checkpoint.Artifact != "meta/volume_codex/v01.json" || checkpoint.Digest == "" {
		t.Fatalf("expected content-bound volume codex checkpoint, got %+v", checkpoint)
	}
}
