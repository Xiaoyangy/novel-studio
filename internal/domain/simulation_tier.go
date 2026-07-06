package domain

// SimulationTier LOD 模拟分层：不可能给每个角色每次 tick 一次全量 LLM 推演，
// 三层深度对应三种成本（参照 Dwarf Fortress 传奇抽象层 / APS 原型 agent）：
//   - protagonist_circle：主角圈，正文全推演（Writer 章级负责）
//   - supporting：在册配角，弧级日程推演（Architect tick 时批量）
//   - background：背景群体，只更新 social_mood/势力状态（表驱动，零独立推演）
type SimulationTier string

const (
	TierProtagonistCircle SimulationTier = "protagonist_circle"
	TierSupporting        SimulationTier = "supporting"
	TierBackground        SimulationTier = "background"
)

// TierAssignment 一个角色的模拟层指派。
type TierAssignment struct {
	Name   string         `json:"name"`
	Tier   SimulationTier `json:"tier"`
	Reason string         `json:"reason,omitempty"`
	Since  int            `json:"since_chapter,omitempty"`
}

// SimulationCast 模拟分层名单。落盘 meta/simulation_tiers.json（可选工件，
// 缺失时 tick 由 Architect 按 Character.Tier 自行判断）。
type SimulationCast struct {
	Assignments []TierAssignment `json:"assignments"`
}

// TierOf 查角色所在层。
func (c SimulationCast) TierOf(name string) (SimulationTier, bool) {
	for _, a := range c.Assignments {
		if a.Name == name {
			return a.Tier, true
		}
	}
	return "", false
}

// Upsert 按 name 更新或追加指派，返回新名单。
func (c SimulationCast) Upsert(a TierAssignment) SimulationCast {
	for i, existing := range c.Assignments {
		if existing.Name == a.Name {
			c.Assignments[i] = a
			return c
		}
	}
	c.Assignments = append(c.Assignments, a)
	return c
}

// SuggestSimulationTier 确定性分层建议：按角色卡 Tier 打底，近期高频出场上调一层
// （对齐 diag 的配角升格信号语义：recentAppearances 为最近一弧出场章数，≥3 视为高频）。
func SuggestSimulationTier(char Character, recentAppearances int) SimulationTier {
	base := TierBackground
	switch char.Tier {
	case "core":
		base = TierProtagonistCircle
	case "important", "": // 未标注默认 important（与 Character.Tier 注释一致）
		base = TierSupporting
	}
	if recentAppearances >= 3 {
		switch base {
		case TierBackground:
			return TierSupporting
		case TierSupporting:
			return TierProtagonistCircle
		}
	}
	return base
}
