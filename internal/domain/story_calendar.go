package domain

// StoryCalendar 故事内时间轴基线：世界模拟的"时钟"。没有它，世界 tick 的
// "这段时间发生了什么"和 visibility_chapter（消息几章后传到主角）都无从换算。
// 落盘 meta/story_calendar.json；zero-init 生成默认骨架，Architect 初始规划时校准。
type StoryCalendar struct {
	Era            string   `json:"era,omitempty"`              // "古代/架空王朝" / "现代"
	StartDate      string   `json:"start_date,omitempty"`       // 第一章开场的故事内日期，如 "天启三年三月初七"
	SeasonAtStart  string   `json:"season_at_start,omitempty"`  // 开场季节
	DaysPerChapter float64  `json:"days_per_chapter,omitempty"` // 一章平均覆盖的故事内天数（可为小数）
	Notes          []string `json:"notes,omitempty"`            // 换算备注（如"战斗弧时间密度加倍"）
}

// IsEmpty 未填写任何字段时为真。
func (c StoryCalendar) IsEmpty() bool {
	return c.Era == "" && c.StartDate == "" && c.SeasonAtStart == "" &&
		c.DaysPerChapter == 0 && len(c.Notes) == 0
}

// EstimateElapsedDays 估算 n 章覆盖的故事内天数；密度未配置时按 1 章 ≈ 1 天。
func (c StoryCalendar) EstimateElapsedDays(chapters int) float64 {
	if chapters <= 0 {
		return 0
	}
	perChapter := c.DaysPerChapter
	if perChapter <= 0 {
		perChapter = 1
	}
	return float64(chapters) * perChapter
}
