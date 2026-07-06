package domain

import "fmt"

// SocialMood 社会情绪快照：氛围级信息不直接进主线剧情，但没有它场景就缺底色
// （"国破山河在"、天宝三载上元夜的狂欢与掩耳盗铃）。落盘 meta/social_mood.json。
type SocialMood struct {
	Mood         string  `json:"mood"`      // "民怨沸腾" / "歌舞升平" / "惶惶不可终日"
	Intensity    float64 `json:"intensity"` // [0,1]
	Rumors       []Rumor `json:"rumors,omitempty"`
	SeasonalMood string  `json:"seasonal_mood,omitempty"` // "春困" / "夏躁"
	Chapter      int     `json:"chapter,omitempty"`       // 快照章号
}

// Rumor 街头巷尾流传的一条谣言。
type Rumor struct {
	Text          string  `json:"text"`
	Credibility   float64 `json:"credibility"` // [0,1]
	SpreadRate    float64 `json:"spread_rate"` // [0,1]
	SourceFaction string  `json:"source_faction,omitempty"`
}

// Validate 校验情绪与谣言的取值。
func (s SocialMood) Validate() error {
	if s.Mood == "" {
		return fmt.Errorf("social_mood.mood 不能为空")
	}
	if s.Intensity < 0 || s.Intensity > 1 {
		return fmt.Errorf("social_mood.intensity 必须在 [0,1]，实际 %v", s.Intensity)
	}
	for i, r := range s.Rumors {
		if r.Text == "" {
			return fmt.Errorf("social_mood.rumors[%d].text 不能为空", i)
		}
		if r.Credibility < 0 || r.Credibility > 1 || r.SpreadRate < 0 || r.SpreadRate > 1 {
			return fmt.Errorf("social_mood.rumors[%d] 数值必须在 [0,1]", i)
		}
	}
	return nil
}
