package domain

import "fmt"

// CulturalFootnote 一条跨文化脚注：跨文化改编（影视/海外出版）时保留文化负载。
// 三类：文化专有项（春节=团圆+红包）/ 行为隐喻（跪拜=臣服 vs 鞠躬=礼貌）/
// 历史典故（楚汉相争=强弱逆转隐喻）。
type CulturalFootnote struct {
	Term         string   `json:"term"`              // "上元灯会"
	Literal      string   `json:"literal,omitempty"` // "First full moon festival"
	CulturalLoad string   `json:"cultural_load"`     // "元宵节，全民放灯"
	NarrativeUse []string `json:"narrative_use,omitempty"`
	Preservation string   `json:"preservation,omitempty"` // "翻译时建议保留'灯'的意象"
	Region       string   `json:"region,omitempty"`       // "中国" / "东亚"
}

// CulturalFootnotes 全书脚注集。落盘 meta/cultural_footnotes.json（可选工件）。
type CulturalFootnotes struct {
	Footnotes []CulturalFootnote `json:"footnotes"`
}

// Validate 校验必填字段。
func (c CulturalFootnotes) Validate() error {
	for i, f := range c.Footnotes {
		if f.Term == "" || f.CulturalLoad == "" {
			return fmt.Errorf("cultural_footnotes[%d] 缺少 term/cultural_load", i)
		}
	}
	return nil
}
