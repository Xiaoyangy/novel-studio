package domain

import (
	"testing"
)

// TestCosmologyCategories_OriginalFivePresent 回归：原 5 类不能丢。
func TestCosmologyCategories_OriginalFivePresent(t *testing.T) {
	for _, c := range []string{"physics", "causality", "existence", "fate", "knowledge"} {
		if !cosmologyCategories[c] {
			t.Errorf("原 5 类枚举 %q 缺失", c)
		}
	}
}

// TestCosmologyCategories_ExtensionThreePresent 2026-07-06 扩展的 3 类到位。
func TestCosmologyCategories_ExtensionThreePresent(t *testing.T) {
	for _, c := range []string{"economy", "metaphysics", "epistemology"} {
		if !cosmologyCategories[c] {
			t.Errorf("2026-07-06 扩展的 3 类枚举 %q 缺失", c)
		}
	}
}

// TestCosmologyCategories_UnknownStringIsFalse 拒绝无关键字符串。
func TestCosmologyCategories_UnknownStringIsFalse(t *testing.T) {
	for _, bad := range []string{"", "nonsense", "magic", "physics-extended", "social"} {
		if cosmologyCategories[bad] {
			t.Errorf("%q 不应在 cosmologyCategories 内合法", bad)
		}
	}
}

// TestCosmologyValidate_LegalMixedCategories 通过：原 5 类 + 扩展 3 类混合应全过。
func TestCosmologyValidate_LegalMixedCategories(t *testing.T) {
	c := Cosmology{
		Axioms: []CosmologyAxiom{
			{ID: "ax-01", Name: "物理公理", Rule: "...", Category: "physics"},
			{ID: "ax-02", Name: "因果律", Rule: "...", Category: "causality"},
			{ID: "ax-03", Name: "存在层", Rule: "...", Category: "existence"},
			{ID: "ax-04", Name: "命运结构", Rule: "...", Category: "fate"},
			{ID: "ax-05", Name: "知识垄断", Rule: "...", Category: "knowledge"},
			{ID: "ax-06", Name: "经济规律", Rule: "...", Category: "economy"},
			{ID: "ax-07", Name: "形而上", Rule: "...", Category: "metaphysics"},
			{ID: "ax-08", Name: "认识论", Rule: "...", Category: "epistemology"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate 应通过但报: %v", err)
	}
}

// TestCosmologyValidate_RejectsUnknownCategory 拒绝未在 enum 内的字符串。
func TestCosmologyValidate_RejectsUnknownCategory(t *testing.T) {
	c := Cosmology{
		Axioms: []CosmologyAxiom{
			{ID: "x", Name: "bad", Rule: "x", Category: "nonsense"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate 应报错 (category 不在 enum)")
	}
}

// TestCosmologyValidate_RejectsEmptyNameOrRule 拒绝缺 name / rule 字段。
func TestCosmologyValidate_RejectsEmptyNameOrRule(t *testing.T) {
	c := Cosmology{
		Axioms: []CosmologyAxiom{
			{ID: "x", Category: "physics"}, // 缺 name + rule
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate 应报错 (缺 name/rule)")
	}
}

// TestCosmologyValidate_AcceptsRealGhostCityData 回归：data/runs/鬼城/meta/cosmology.json
// 实际写入的 6 axiom 必须 Validate 通过——避免 schema 扩展后再回退。
func TestCosmologyValidate_AcceptsRealGhostCityData(t *testing.T) {
	c := Cosmology{
		Axioms: []CosmologyAxiom{
			{ID: "ax-01", Name: "夜间法域", Rule: "rule", Category: "physics"},
			{ID: "ax-02", Name: "夜租可收项", Rule: "rule", Category: "economy"},
			{ID: "ax-03", Name: "交易内容先行", Rule: "rule", Category: "economy"},
			{ID: "ax-04", Name: "规则不可毁灭", Rule: "rule", Category: "metaphysics"},
			{ID: "ax-05", Name: "账单与审计足迹", Rule: "rule", Category: "economy"},
			{ID: "ax-06", Name: "证据链认知识边界", Rule: "rule", Category: "epistemology"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("鬼城 cosmology.json 6 axiom Validate 应通过: %v", err)
	}
}

// TestCosmologyEmpty 边界：空 axioms 应 Valid（仅校验字段）。
func TestCosmologyEmpty(t *testing.T) {
	c := Cosmology{}
	if err := c.Validate(); err != nil {
		t.Fatalf("空 Cosmology 应 Valid: %v", err)
	}
}
