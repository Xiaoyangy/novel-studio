package domain

import "fmt"

// cosmologyCategories 公理类别枚举。
// 原 5 类是玄幻/科幻经典分类（physics/causality/existence/fate/knowledge）；
// 2026-07-06 扩展 3 类以覆盖"经济/形而上/认识论"维度的写作公理；项目资产曾写入
// economy/metaphysics/epistemology，而 schema 未列举，导致 Validate() 静默放过数据-枚举错配。
var cosmologyCategories = map[string]bool{
	"physics":      true, // 物理法则（灵气衰减/能量守恒）
	"causality":    true, // 因果律（杀一人背一因果）
	"existence":    true, // 存在层（凡间/灵界/轮回）
	"fate":         true, // 命运结构（自由意志 vs 注定）
	"knowledge":    true, // 知识垄断（谁知道什么）
	"economy":      true, // 经济/债务/审计层（交易、抵押、账单、利息、产权）
	"metaphysics":  true, // 形而上层（命运、不可灭规则、神祇级约束）
	"epistemology": true, // 知识论层（信息差、证据链、认知边界、谁知道什么）
}

// CosmologyAxiom 世界第一性原理的一条公理。玄幻/科幻最常崩在这里：
// "魔法的代价"没立稳，200 章后角色突然违反设定。
// 所有"显规则"（WorldRule）都应从公理推导而来，用 DerivedWorldRules 记录推导关系。
type CosmologyAxiom struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Rule              string   `json:"rule"`
	AppliesTo         string   `json:"applies_to,omitempty"` // all_characters / cultivators_only
	Category          string   `json:"category"`             // physics/causality/existence/fate/knowledge/economy/metaphysics/epistemology
	Note              string   `json:"note,omitempty"`
	DerivedWorldRules []string `json:"derived_world_rules,omitempty"` // 由此推导的显规则描述
}

// ExistenceLayer 一个存在层及其规则。
type ExistenceLayer struct {
	Name  string `json:"name"` // "凡间" / "灵界" / "冥界"
	Rules string `json:"rules,omitempty"`
}

// Cosmology 元背景宇宙观。落盘 meta/cosmology.json（可选工件），
// 与扁平 WorldRule 通过 DerivedWorldRules 互相引用，不塞进同一结构。
type Cosmology struct {
	Axioms            []CosmologyAxiom `json:"axioms"`
	ExistenceLayers   []ExistenceLayer `json:"existence_layers,omitempty"`
	FateStructure     string           `json:"fate_structure,omitempty"` // "个人意志可改命，但代价是失去更多"
	KnowledgeMonopoly string           `json:"knowledge_monopoly,omitempty"`
}

// Validate 校验公理必填字段与类别枚举。
func (c Cosmology) Validate() error {
	for i, a := range c.Axioms {
		if a.Name == "" || a.Rule == "" {
			return fmt.Errorf("cosmology.axioms[%d] 缺少 name/rule", i)
		}
		if !cosmologyCategories[a.Category] {
			return fmt.Errorf("cosmology.axioms[%d].category 非法: %s", i, a.Category)
		}
	}
	return nil
}
