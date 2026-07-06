package domain

import "fmt"

// EmotionVector 结构化情绪坐标（Barrett 情绪建构理论：情绪由 valence×arousal 构造）。
// 与既有两层并存、各司其职：
//   - CharacterDynamicsProfile.EmotionalState(string)：快照标签（保留不动）
//   - CharacterEmotionAppraisal：情绪因果链 trigger→visible/suppressed→coping（保留不动）
//   - EmotionVector：连续坐标，供跨章情绪弧线的定量对比（新增）
type EmotionVector struct {
	Valence      float64 `json:"valence"`                 // [-1,1] 负面 → 正面
	Arousal      float64 `json:"arousal"`                 // [-1,1] 平静 → 激动
	Intensity    float64 `json:"intensity"`               // [0,1] 情绪强度
	PrimaryLabel string  `json:"primary_label,omitempty"` // 主标签，可由 DeriveLabel 推导
	Granularity  float64 `json:"granularity,omitempty"`   // [0,1] 情绪粒度（分辨自身情绪的精细度）
}

// Validate 校验各维度取值范围。
func (e EmotionVector) Validate() error {
	if e.Valence < -1 || e.Valence > 1 {
		return fmt.Errorf("emotion_vector.valence 必须在 [-1,1]，实际 %v", e.Valence)
	}
	if e.Arousal < -1 || e.Arousal > 1 {
		return fmt.Errorf("emotion_vector.arousal 必须在 [-1,1]，实际 %v", e.Arousal)
	}
	if e.Intensity < 0 || e.Intensity > 1 {
		return fmt.Errorf("emotion_vector.intensity 必须在 [0,1]，实际 %v", e.Intensity)
	}
	if e.Granularity < 0 || e.Granularity > 1 {
		return fmt.Errorf("emotion_vector.granularity 必须在 [0,1]，实际 %v", e.Granularity)
	}
	return nil
}

// DeriveLabel 由象限 + 强度推导主标签（+,+ 兴奋/狂喜；+,- 满足/平和；-,+ 愤怒/恐惧；-,- 低落/抑郁）。
func (e EmotionVector) DeriveLabel() string {
	strong := e.Intensity >= 0.6
	switch {
	case e.Valence >= 0 && e.Arousal >= 0:
		if strong {
			return "狂喜"
		}
		return "兴奋"
	case e.Valence >= 0 && e.Arousal < 0:
		if strong {
			return "满足"
		}
		return "平和"
	case e.Valence < 0 && e.Arousal >= 0:
		if strong {
			return "愤怒"
		}
		return "焦躁"
	default:
		if strong {
			return "抑郁"
		}
		return "低落"
	}
}
