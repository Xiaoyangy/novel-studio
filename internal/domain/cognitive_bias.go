package domain

import (
	"fmt"
	"slices"
)

// BiasType 认知偏差类型（Kahneman / Tversky 常见 15 种）。
type BiasType string

const (
	BiasLossAversion       BiasType = "loss_aversion"      // 损失厌恶：失去的痛感 > 得到的快感
	BiasAnchoring          BiasType = "anchoring"          // 锚定：第一印象过度影响后续判断
	BiasSunkCost           BiasType = "sunk_cost"          // 沉没成本：已投入的舍不得放弃
	BiasStatusQuo          BiasType = "status_quo"         // 现状偏见：维持现状的偏好
	BiasConfirmation       BiasType = "confirmation_bias"  // 确认偏差：只找支持自己观点的信息
	BiasSelfServing        BiasType = "self_serving"       // 自利偏差：成功归自己、失败归外部
	BiasHindsight          BiasType = "hindsight_bias"     // 后见之明："我早知道会这样"
	BiasOverconfidence     BiasType = "overconfidence"     // 过度自信：高估自己判断的准确性
	BiasFraming            BiasType = "framing"            // 框架效应：同一件事换种说法反应不同
	BiasEndowment          BiasType = "endowment"          // 禀赋效应：自己拥有的估值更高
	BiasDunningKruger      BiasType = "dunning_kruger"     // 达克效应：能力差的反而高估自己
	BiasGroupPolarization  BiasType = "group_polarization" // 群体极化：讨论后观点更极端
	BiasGamblerFallacy     BiasType = "gambler_fallacy"    // 赌徒谬误："连输 5 次该赢了"
	BiasRepresentativeness BiasType = "representativeness" // 代表性启发：用典型性做判断
	BiasAvailability       BiasType = "availability"       // 可得性启发：用容易想到的例子估概率
)

// AllBiasTypes 全部合法偏差类型（测试与校验用）。
var AllBiasTypes = []BiasType{
	BiasLossAversion, BiasAnchoring, BiasSunkCost, BiasStatusQuo,
	BiasConfirmation, BiasSelfServing, BiasHindsight, BiasOverconfidence,
	BiasFraming, BiasEndowment, BiasDunningKruger, BiasGroupPolarization,
	BiasGamblerFallacy, BiasRepresentativeness, BiasAvailability,
}

// Validate 校验偏差类型枚举。
func (b BiasType) Validate() error {
	if slices.Contains(AllBiasTypes, b) {
		return nil
	}
	return fmt.Errorf("非法认知偏差类型: %s", b)
}

// BiasActivation 一条偏差在该角色身上的激活情况。
type BiasActivation struct {
	Type      BiasType `json:"type"`
	Intensity float64  `json:"intensity"`            // [0,1]
	AppliedTo string   `json:"applied_to,omitempty"` // 典型触发场景描述
}

// CognitiveBiasProfile 角色认知偏差画像。不要求覆盖全部 15 种，写主导的 2-4 种即可。
type CognitiveBiasProfile struct {
	Biases       []BiasActivation `json:"biases,omitempty"`
	BlindSpots   []string         `json:"blind_spots,omitempty"`   // 认知盲区
	InverseNotes string           `json:"inverse_notes,omitempty"` // 角色反直觉的地方
}

// Validate 逐条校验偏差类型与强度范围。
func (p CognitiveBiasProfile) Validate() error {
	for _, b := range p.Biases {
		if err := b.Type.Validate(); err != nil {
			return err
		}
		if b.Intensity < 0 || b.Intensity > 1 {
			return fmt.Errorf("cognitive_biases.%s.intensity 必须在 [0,1]，实际 %v", b.Type, b.Intensity)
		}
	}
	return nil
}
