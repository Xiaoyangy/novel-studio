package domain

import "fmt"

// RitualEvent 一个周期性仪式事件：节庆/祭祀/纪念日/神秘仪式。
// 每个重要场景的"日期"影响全民行为（春节回归、上元夜游、国耻日情绪、渡劫）。
type RitualEvent struct {
	Name           string   `json:"name"`
	Date           string   `json:"date"`                      // "正月十五"
	Type           string   `json:"type"`                      // festive / sacrificial / memorial / lifecycle / mystical
	BehaviorChange string   `json:"behavior_change,omitempty"` // "全民夜游、放松宵禁"
	NarrativeUse   []string `json:"narrative_use,omitempty"`   // ["主角偶遇", "反派阴谋"]
}

// LifecycleEvent 生命周期仪式（成年礼/婚礼/葬礼等角色转折点）。
type LifecycleEvent struct {
	Name   string `json:"name"` // "及笄"
	Age    int    `json:"age,omitempty"`
	Gender string `json:"gender,omitempty"`
}

// RitualCalendar 人类社会的时间结构。落盘 meta/ritual_calendar.json（可选工件）。
type RitualCalendar struct {
	Annual    []RitualEvent    `json:"annual,omitempty"`
	Lifecycle []LifecycleEvent `json:"lifecycle,omitempty"`
}

// Validate 校验事件必填字段。
func (c RitualCalendar) Validate() error {
	for i, e := range c.Annual {
		if e.Name == "" || e.Date == "" {
			return fmt.Errorf("ritual_calendar.annual[%d] 缺少 name/date", i)
		}
	}
	for i, e := range c.Lifecycle {
		if e.Name == "" {
			return fmt.Errorf("ritual_calendar.lifecycle[%d] 缺少 name", i)
		}
	}
	return nil
}
