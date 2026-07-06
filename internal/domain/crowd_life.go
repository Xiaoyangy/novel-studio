package domain

import "fmt"

// ScheduleEntry NPC 日程中的一段活动。
type ScheduleEntry struct {
	TimeOfDay    string `json:"time_of_day"` // "辰时" / "morning"
	Location     string `json:"location"`
	Activity     string `json:"activity"` // "开店" / "扫街" / "买菜"
	Interactable bool   `json:"interactable,omitempty"`
}

// NPCSchedule 一个 NPC 的自洽生活循环：有自己的日程、目标和关系，
// 不是只为主角配戏的工具人（参照《刺客信条：奥德赛》Crowd Life）。
type NPCSchedule struct {
	NPCID         string          `json:"npc_id"`
	DailySchedule []ScheduleEntry `json:"daily_schedule,omitempty"`
	Goals         []string        `json:"goals,omitempty"`
	Relationships []string        `json:"relationships,omitempty"`
}

// CrowdLifeEcosystem NPC 生态。落盘 meta/crowd_life.json。
// 注意与 meta/crowd_role_policy.json（捧场/凑数角色的使用策略）是不同层：
// policy 管"怎么用群演"，本结构管"群演自己怎么活"。
type CrowdLifeEcosystem struct {
	NPCs []NPCSchedule `json:"npcs"`
}

// Validate 校验 NPC 必填字段。
func (c CrowdLifeEcosystem) Validate() error {
	for i, n := range c.NPCs {
		if n.NPCID == "" {
			return fmt.Errorf("crowd_life.npcs[%d] 缺少 npc_id", i)
		}
	}
	return nil
}
