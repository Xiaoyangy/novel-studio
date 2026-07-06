package domain

// ResourceKind 稀缺资源规范枚举（9 类 + other）。
// ResourceClaim.Kind 历史上是自由文本（asset/skill/item/place/debt 等），
// 本枚举只做归一化与分组展示，不强制：未知值归 other，不报错。
type ResourceKind string

const (
	ResourceCurrency  ResourceKind = "currency"  // 货币/财物
	ResourceCredit    ResourceKind = "credit"    // 信用/名声
	ResourceRelations ResourceKind = "relations" // 关系/人情
	ResourceKnowledge ResourceKind = "knowledge" // 知识/信息
	ResourceAbility   ResourceKind = "ability"   // 能力/修为
	ResourceTime      ResourceKind = "time"      // 时间
	ResourceHealth    ResourceKind = "health"    // 健康/寿命
	ResourceEmotion   ResourceKind = "emotion"   // 情感
	ResourceSecret    ResourceKind = "secret"    // 秘密
	ResourceOther     ResourceKind = "other"     // 未归类
)

// AllResourceKinds 全部规范枚举（不含 other）。
var AllResourceKinds = []ResourceKind{
	ResourceCurrency, ResourceCredit, ResourceRelations, ResourceKnowledge,
	ResourceAbility, ResourceTime, ResourceHealth, ResourceEmotion, ResourceSecret,
}

// legacyResourceKindMap 既有自由文本用法 → 规范枚举的映射。
var legacyResourceKindMap = map[string]ResourceKind{
	"asset": ResourceCurrency,
	"item":  ResourceCurrency,
	"skill": ResourceAbility,
	"place": ResourceRelations, // 据点/场所本质是关系与庇护资产
	"debt":  ResourceCredit,
}

// NormalizeResourceKind 把自由文本归一到规范枚举；命中规范值原样返回，
// 命中历史用法按映射转换，其余（含空串）归 other。
func NormalizeResourceKind(raw string) ResourceKind {
	k := ResourceKind(raw)
	for _, known := range AllResourceKinds {
		if k == known {
			return known
		}
	}
	if mapped, ok := legacyResourceKindMap[raw]; ok {
		return mapped
	}
	return ResourceOther
}
