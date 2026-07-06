package domain

import "fmt"

// MoralFoundations 道德基础理论（Haidt / Graham）六模块评分，全部 [0,1]。
// 注意文化差异：东亚传统派 vs 西方自由派在 authority / sanctity 上几乎相反。
type MoralFoundations struct {
	HarmCare            float64  `json:"harm_care"`            // 伤害/关怀：见人被虐待即触发
	FairnessCheating    float64  `json:"fairness_cheating"`    // 公平/欺骗
	LoyaltyBetrayal     float64  `json:"loyalty_betrayal"`     // 忠诚/背叛
	AuthoritySubversion float64  `json:"authority_subversion"` // 权威/颠覆
	SanctityDegradation float64  `json:"sanctity_degradation"` // 圣洁/堕落
	LibertyOppression   float64  `json:"liberty_oppression"`   // 自由/压迫
	PrimaryMorality     string   `json:"primary_morality,omitempty"`
	TabooZones          []string `json:"taboo_zones,omitempty"` // 触动即触发强烈情绪的禁区
}

// Validate 校验六项都在 [0,1]。
func (m MoralFoundations) Validate() error {
	dims := map[string]float64{
		"harm_care":            m.HarmCare,
		"fairness_cheating":    m.FairnessCheating,
		"loyalty_betrayal":     m.LoyaltyBetrayal,
		"authority_subversion": m.AuthoritySubversion,
		"sanctity_degradation": m.SanctityDegradation,
		"liberty_oppression":   m.LibertyOppression,
	}
	for name, v := range dims {
		if v < 0 || v > 1 {
			return fmt.Errorf("moral_foundations.%s 必须在 [0,1]，实际 %v", name, v)
		}
	}
	return nil
}
