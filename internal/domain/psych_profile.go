package domain

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
