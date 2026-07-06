package domain

import (
	"fmt"
	"strings"
)

// BigFive 大五人格（Costa & McCrae 五因素模型）定量评分，全部取值 [0,1]。
// 用途：novel_context 注入行为化指引；commit_chapter 做确定性软 lint（warning only）。
type BigFive struct {
	Openness          float64 `json:"openness"`          // 开放性：想象/审美/求新 vs 务实/传统
	Conscientiousness float64 `json:"conscientiousness"` // 尽责性：自律/谨慎 vs 随性/冲动
	Extraversion      float64 `json:"extraversion"`      // 外向性：社交/活力 vs 内向/独处
	Agreeableness     float64 `json:"agreeableness"`     // 宜人性：利他/合作 vs 怀疑/强硬
	Neuroticism       float64 `json:"neuroticism"`       // 神经质：焦虑/情绪化 vs 冷静/坚韧
	Profile           string  `json:"profile,omitempty"` // 由分数推导的简档文本
}

// 高/低分阈值：>=0.6 视为高，<=0.4 视为低，中间不标注。
const (
	bigFiveHighThreshold = 0.6
	bigFiveLowThreshold  = 0.4
)

// Validate 校验五个维度都在 [0,1]。
func (b BigFive) Validate() error {
	dims := map[string]float64{
		"openness":          b.Openness,
		"conscientiousness": b.Conscientiousness,
		"extraversion":      b.Extraversion,
		"agreeableness":     b.Agreeableness,
		"neuroticism":       b.Neuroticism,
	}
	for name, v := range dims {
		if v < 0 || v > 1 {
			return fmt.Errorf("big_five.%s 必须在 [0,1]，实际 %v", name, v)
		}
	}
	return nil
}

// GenerateProfile 由分数推导一行简档，如 "高O 高N 低A"。中间分不出现在简档里。
func (b BigFive) GenerateProfile() string {
	type dim struct {
		letter string
		value  float64
	}
	var parts []string
	for _, d := range []dim{
		{"O", b.Openness},
		{"C", b.Conscientiousness},
		{"E", b.Extraversion},
		{"A", b.Agreeableness},
		{"N", b.Neuroticism},
	} {
		switch {
		case d.value >= bigFiveHighThreshold:
			parts = append(parts, "高"+d.letter)
		case d.value <= bigFiveLowThreshold:
			parts = append(parts, "低"+d.letter)
		}
	}
	if len(parts) == 0 {
		return "五维均衡"
	}
	return strings.Join(parts, " ")
}

// ExpectedEmotionRange 由神经质分数推导"情绪表现强度"的期望区间，
// 供 commit_chapter 与 Writer 自报值做确定性对比（见 personality lint）。
func (b BigFive) ExpectedEmotionRange() (low, high float64) {
	center := 0.2 + 0.6*b.Neuroticism // N=0 → 0.2，N=1 → 0.8
	return clamp01(center - 0.3), clamp01(center + 0.3)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
