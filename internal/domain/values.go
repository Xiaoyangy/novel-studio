package domain

import "fmt"

// SchwartzValues Schwartz 10 项普世价值观评分，全部 [0,1]。
// 应用：道德困境中角色的选择取决于价值观优先级
// （"先救师父还是先救苍生"取决于 Benevolence vs SelfDirection 的具体值）。
type SchwartzValues struct {
	SelfDirection float64 `json:"self_direction"` // 自主
	Stimulation   float64 `json:"stimulation"`    // 刺激
	Hedonism      float64 `json:"hedonism"`       // 享乐
	Achievement   float64 `json:"achievement"`    // 成就
	Power         float64 `json:"power"`          // 权力
	Security      float64 `json:"security"`       // 安全
	Tradition     float64 `json:"tradition"`      // 传统
	Conformity    float64 `json:"conformity"`     // 遵从
	Benevolence   float64 `json:"benevolence"`    // 仁爱
	Universalism  float64 `json:"universalism"`   // 普世主义
}

// Validate 校验 10 项都在 [0,1]。
func (v SchwartzValues) Validate() error {
	dims := map[string]float64{
		"self_direction": v.SelfDirection,
		"stimulation":    v.Stimulation,
		"hedonism":       v.Hedonism,
		"achievement":    v.Achievement,
		"power":          v.Power,
		"security":       v.Security,
		"tradition":      v.Tradition,
		"conformity":     v.Conformity,
		"benevolence":    v.Benevolence,
		"universalism":   v.Universalism,
	}
	for name, val := range dims {
		if val < 0 || val > 1 {
			return fmt.Errorf("values.%s 必须在 [0,1]，实际 %v", name, val)
		}
	}
	return nil
}

// ValueConflict 两项价值观之间的内在冲突（角色丰富度 = 张力没解决）。
type ValueConflict struct {
	Between   string  `json:"between"`   // 价值观 A
	And       string  `json:"and"`       // 价值观 B
	Intensity float64 `json:"intensity"` // [0,1]
}

// ValuesProfile 角色价值观画像。
type ValuesProfile struct {
	Values        SchwartzValues  `json:"values"`
	Conflicts     []ValueConflict `json:"conflicts,omitempty"`
	PrimaryDriver string          `json:"primary_driver,omitempty"` // 如 "benevolence + universalism"
}
