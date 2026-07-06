package domain

import (
	"bytes"
	"encoding/json"
)

// CharacterPsychProfile 聚合角色的定量心理画像。全部子维度可选：
// 缺失时消费方（novel_context 注入、commit_chapter 软 lint）一律跳过，
// 老数据（无 psych 字段）读写零影响。
//
// 设计约定：作者源挂在 Character.Psych（characters.json，Architect 落笔），
// 派生的 CharacterDynamicsProfile.Psych 只做透传，不二次加工。
type CharacterPsychProfile struct {
	BigFive          *BigFive              `json:"big_five,omitempty"`          // 大五人格 OCEAN
	Attachment       *Attachment           `json:"attachment,omitempty"`        // 依恋类型
	EmotionVector    *EmotionVector        `json:"emotion_vector,omitempty"`    // 情绪 valence×arousal 坐标
	CognitiveBiases  *CognitiveBiasProfile `json:"cognitive_biases,omitempty"`  // 认知偏差画像
	Values           *ValuesProfile        `json:"values,omitempty"`            // Schwartz 价值观
	MoralFoundations *MoralFoundations     `json:"moral_foundations,omitempty"` // 道德基础 MFT
	Abilities        *CHCAbilities         `json:"abilities,omitempty"`         // CHC 能力偏科矩阵
	DNA              *CharacterDNA         `json:"dna,omitempty"`               // 显性/隐性/突变三维分组
	// DegradedDims 记录解码时因形状不符被丢弃的子维度键名（见 UnmarshalJSON）。
	// 持久化在 characters.json 里留痕，readiness/diag 的 psych 缺失 warning 会自然覆盖。
	DegradedDims []string `json:"degraded_dims,omitempty"`
}

// UnmarshalJSON 按子维度独立容错解码。LLM（尤其 MiniMax）常把子维度写成
// 数组或字符串等非约定形状；psych 全维度可选、无门禁，约定的降级路径就是
// "当缺失跳过"。因此形状不符的子维度单独丢弃并记入 DegradedDims，
// 不让一个软性画像字段毁掉整份 characters.json 的保存。
func (p *CharacterPsychProfile) UnmarshalJSON(data []byte) error {
	*p = CharacterPsychProfile{}
	if isJSONNull(data) {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// psych 本体不是对象：整体按缺失降级。
		p.DegradedDims = []string{"psych"}
		return nil
	}
	decodePsychDim(raw, "big_five", &p.BigFive, &p.DegradedDims)
	decodePsychDim(raw, "attachment", &p.Attachment, &p.DegradedDims)
	decodePsychDim(raw, "emotion_vector", &p.EmotionVector, &p.DegradedDims)
	decodePsychDim(raw, "cognitive_biases", &p.CognitiveBiases, &p.DegradedDims)
	decodePsychDim(raw, "values", &p.Values, &p.DegradedDims)
	decodePsychDim(raw, "moral_foundations", &p.MoralFoundations, &p.DegradedDims)
	decodePsychDim(raw, "abilities", &p.Abilities, &p.DegradedDims)
	decodePsychDim(raw, "dna", &p.DNA, &p.DegradedDims)
	return nil
}

func decodePsychDim[T any](raw map[string]json.RawMessage, key string, dst **T, degraded *[]string) {
	v, ok := raw[key]
	if !ok || isJSONNull(v) {
		return
	}
	var out T
	if err := json.Unmarshal(v, &out); err != nil {
		*degraded = append(*degraded, key)
		return
	}
	*dst = &out
}

func isJSONNull(data []byte) bool {
	return len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}

// Validate 逐个校验非空子维度；任一失败即返回。
func (p *CharacterPsychProfile) Validate() error {
	if p == nil {
		return nil
	}
	if p.BigFive != nil {
		if err := p.BigFive.Validate(); err != nil {
			return err
		}
	}
	if p.Attachment != nil {
		if err := p.Attachment.Style.Validate(); err != nil {
			return err
		}
	}
	if p.EmotionVector != nil {
		if err := p.EmotionVector.Validate(); err != nil {
			return err
		}
	}
	if p.CognitiveBiases != nil {
		if err := p.CognitiveBiases.Validate(); err != nil {
			return err
		}
	}
	if p.Values != nil {
		if err := p.Values.Values.Validate(); err != nil {
			return err
		}
	}
	if p.MoralFoundations != nil {
		if err := p.MoralFoundations.Validate(); err != nil {
			return err
		}
	}
	if p.Abilities != nil {
		if err := p.Abilities.Validate(); err != nil {
			return err
		}
	}
	return nil
}
