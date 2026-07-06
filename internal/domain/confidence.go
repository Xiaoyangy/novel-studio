package domain

import "fmt"

// ConfidenceReport Writer 对本章产出的自报置信度。研究共识：LLM 自报分数系统性
// 过度自信（普遍饱和 0.8-1.0），因此本报告**只做观测信号**（低分 = "Writer 自己都说
// 没把握"的线索，透传给 Editor 与 diag），绝不进入任何自动化控制流。
// 自然语言疑点（Doubts）比数值更有用，prompt 指引里优先要求写疑点。
type ConfidenceReport struct {
	Overall  float64            `json:"overall"`             // [0,1]
	PerField map[string]float64 `json:"per_field,omitempty"` // draft / plan.goal 等
	Doubts   []string           `json:"doubts,omitempty"`    // 哪里没把握、为什么
}

// ConfidenceWarnThreshold 低于该值时产出一条 warning 级 Violation（仅观测语义）。
const ConfidenceWarnThreshold = 0.7

// Validate 校验取值范围。
func (c ConfidenceReport) Validate() error {
	if c.Overall < 0 || c.Overall > 1 {
		return fmt.Errorf("confidence.overall 必须在 [0,1]，实际 %v", c.Overall)
	}
	for field, v := range c.PerField {
		if v < 0 || v > 1 {
			return fmt.Errorf("confidence.per_field.%s 必须在 [0,1]，实际 %v", field, v)
		}
	}
	return nil
}

// CharacterExpressionCheck Writer 对本章某主要角色情绪表现强度的自评，
// 供与 BigFive 推导的期望区间做确定性对比（personality 软 lint）。
type CharacterExpressionCheck struct {
	Name             string  `json:"name"`
	EmotionIntensity float64 `json:"emotion_intensity"` // [0,1]
}
