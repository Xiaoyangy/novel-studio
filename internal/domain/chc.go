package domain

import "fmt"

// CHCAbilities 角色能力偏科矩阵（CHC 认知能力分类，9 项，全部 [0,1]）。
// 用途：让角色各有偏科（谋士推理强记忆强、莽将处理速度快知识面窄），防止全员全能同质化。
type CHCAbilities struct {
	Knowledge             float64 `json:"knowledge"`              // 晶体智力/知识面
	FluidReasoning        float64 `json:"fluid_reasoning"`        // 即时推理
	VisualProcessing      float64 `json:"visual_processing"`      // 视觉处理
	WorkingMemory         float64 `json:"working_memory"`         // 工作记忆
	LongTermMemory        float64 `json:"long_term_memory"`       // 长期记忆
	ProcessingSpeed       float64 `json:"processing_speed"`       // 处理速度
	QuantitativeKnowledge float64 `json:"quantitative_knowledge"` // 数量知识
	ReadingWriting        float64 `json:"reading_writing"`        // 读写能力
	AuditoryProcessing    float64 `json:"auditory_processing"`    // 听觉处理
}

// Validate 校验九项都在 [0,1]。
func (c CHCAbilities) Validate() error {
	dims := map[string]float64{
		"knowledge":              c.Knowledge,
		"fluid_reasoning":        c.FluidReasoning,
		"visual_processing":      c.VisualProcessing,
		"working_memory":         c.WorkingMemory,
		"long_term_memory":       c.LongTermMemory,
		"processing_speed":       c.ProcessingSpeed,
		"quantitative_knowledge": c.QuantitativeKnowledge,
		"reading_writing":        c.ReadingWriting,
		"auditory_processing":    c.AuditoryProcessing,
	}
	for name, v := range dims {
		if v < 0 || v > 1 {
			return fmt.Errorf("abilities.%s 必须在 [0,1]，实际 %v", name, v)
		}
	}
	return nil
}
