package domain

// PhysicsAxioms 物理一致性公理：百万字长篇最容易塌的层
// （"昨天飞鸽传书今早京城就知道了"）。落盘 meta/physics_axioms.json（可选工件），
// 经 novel_context 注入做写前预防；不做写后抽取式 lint。
type PhysicsAxioms struct {
	DistanceSpeed     map[string]SpeedRule `json:"distance_speed,omitempty"`     // 出行方式 → 速度
	InfoPropagation   map[string]float64   `json:"info_propagation,omitempty"`   // 传播方式 → 天数
	CurrencyReference map[string]float64   `json:"currency_reference,omitempty"` // 物品 → 价格
	AbilityCeiling    []string             `json:"ability_ceiling,omitempty"`    // 境界/等级序列（由低到高）
	SeasonCalendar    map[string]string    `json:"season_calendar,omitempty"`    // 月份 → 物候
	MaterialDecay     map[string]int       `json:"material_decay,omitempty"`     // 物品 → 保质天数
	Era               string               `json:"era,omitempty"`                // "古代" / "现代"
	Notes             []string             `json:"notes,omitempty"`
}

// SpeedRule 一种出行/传递方式的速度约束。
type SpeedRule struct {
	Unit  string  `json:"unit"` // "里/天"
	Speed float64 `json:"speed"`
	Note  string  `json:"note,omitempty"`
}

// IsEmpty 全部为空时为真，消费方据此跳过注入。
func (p PhysicsAxioms) IsEmpty() bool {
	return len(p.DistanceSpeed) == 0 && len(p.InfoPropagation) == 0 &&
		len(p.CurrencyReference) == 0 && len(p.AbilityCeiling) == 0 &&
		len(p.SeasonCalendar) == 0 && len(p.MaterialDecay) == 0 && len(p.Notes) == 0
}
