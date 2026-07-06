package domain

import "fmt"

// PropFunction 一件出场物件的功能登记（细节经济）：
// 出现 1 次→推动情节；2 次→伏笔回扣；3+ 次→必有象征意义；出现但不做事→候选删除。
type PropFunction struct {
	Name            string `json:"name"`
	AppearanceCount int    `json:"appearance_count,omitempty"`
	Function        string `json:"function,omitempty"`         // 信息载体 / 伏笔 / 氛围营造
	NarrativePayoff string `json:"narrative_payoff,omitempty"` // "第 28 章回收"
	IsDecorative    bool   `json:"is_decorative,omitempty"`    // 纯装饰（候选删除）
}

// SceneInventory 某章的物件清单。落盘 meta/scene_inventory/{NN}.json（可选工件）。
type SceneInventory struct {
	Chapter       int            `json:"chapter"`
	Objects       []PropFunction `json:"objects,omitempty"`
	FunctionRatio float64        `json:"function_ratio,omitempty"` // 有功能物件占比
}

// Validate 校验物件必填字段。
func (s SceneInventory) Validate() error {
	for i, o := range s.Objects {
		if o.Name == "" {
			return fmt.Errorf("scene_inventory.objects[%d] 缺少 name", i)
		}
	}
	return nil
}

// ComputeFunctionRatio 计算有功能物件占比（非装饰物 / 总数）；无物件时返回 0。
func (s SceneInventory) ComputeFunctionRatio() float64 {
	if len(s.Objects) == 0 {
		return 0
	}
	functional := 0
	for _, o := range s.Objects {
		if !o.IsDecorative {
			functional++
		}
	}
	return float64(functional) / float64(len(s.Objects))
}
