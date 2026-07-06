package domain

import "fmt"

// agendaStatuses 日程状态枚举。
var agendaStatuses = map[string]bool{
	"active":    true, // 正常推进中
	"blocked":   true, // 被某条件卡住（BlockedBy 说明）
	"dormant":   true, // 暂时蛰伏（角色退场期）
	"completed": true, // 目标达成，待分配新目标
}

// AgendaStep 日程中的一步。
type AgendaStep struct {
	Description string `json:"description"`
	EtaChapters int    `json:"eta_chapters,omitempty"` // 预计耗时（章）
	Done        bool   `json:"done,omitempty"`
}

// CharacterAgenda 一个离屏角色的自主日程：goal → steps → 推进位置。
// 让配角"自己有事做"的运行态载体，由 Architect 在每次世界 tick 时推进 1-2 步。
// 与 CharacterDynamicsProfile 的关系：dynamics 是画像派生（角色是什么样的决策系统），
// agenda 是离屏运行态（此刻在忙什么、进行到哪一步）。
type CharacterAgenda struct {
	Name                string       `json:"name"`
	Tier                string       `json:"tier,omitempty"` // 模拟层：supporting / background
	CurrentGoal         string       `json:"current_goal"`
	Motivation          string       `json:"motivation,omitempty"` // 与 psych/资源/关系的锚点
	Steps               []AgendaStep `json:"steps,omitempty"`
	Status              string       `json:"status,omitempty"` // active/blocked/dormant/completed
	BlockedBy           string       `json:"blocked_by,omitempty"`
	LastAdvancedChapter int          `json:"last_advanced_chapter,omitempty"`
}

// Validate 校验日程必填字段与状态枚举。
func (a CharacterAgenda) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("agenda 缺少 name")
	}
	if a.CurrentGoal == "" {
		return fmt.Errorf("agenda %q 缺少 current_goal", a.Name)
	}
	if a.Status != "" && !agendaStatuses[a.Status] {
		return fmt.Errorf("agenda %q 的 status 非法: %s", a.Name, a.Status)
	}
	return nil
}

// OffscreenAgendaLedger 全部离屏角色的日程账本。落盘 meta/offscreen_agenda.json。
type OffscreenAgendaLedger struct {
	Agendas []CharacterAgenda `json:"agendas"`
}

// Upsert 按 name 更新或追加一条日程，返回新账本（值语义，便于测试）。
func (l OffscreenAgendaLedger) Upsert(a CharacterAgenda) OffscreenAgendaLedger {
	for i, existing := range l.Agendas {
		if existing.Name == a.Name {
			l.Agendas[i] = a
			return l
		}
	}
	l.Agendas = append(l.Agendas, a)
	return l
}

// Stale 返回停滞日程：active 状态但距上次推进超过 threshold 章。
func (l OffscreenAgendaLedger) Stale(current, threshold int) []CharacterAgenda {
	var out []CharacterAgenda
	for _, a := range l.Agendas {
		if (a.Status == "" || a.Status == "active") && current-a.LastAdvancedChapter > threshold {
			out = append(out, a)
		}
	}
	return out
}
