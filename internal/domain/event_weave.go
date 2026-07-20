package domain

import "fmt"

// Task 078：事件编织层（StoryWriter 事件-章解耦思路）。事件池独立于章节存在，
// 编织表声明"哪一章推进哪些事件、哪些线索静默"——plan 与编织表冲突时要求说明
// 改排理由（软约束），线索连续静默超限产 warning。落盘 meta/event_weave.json（可选）。

// WeaveEvent 事件池中的一个事件。
type WeaveEvent struct {
	ID           string   `json:"id"`     // "ev-001"
	Thread       string   `json:"thread"` // 所属线索：主线/家庭线/事业线…
	Summary      string   `json:"summary"`
	Participants []string `json:"participants,omitempty"`
	Prereq       []string `json:"prereq,omitempty"`      // 前置事件 id
	WindowFrom   int      `json:"window_from,omitempty"` // 可安排章区间
	WindowTo     int      `json:"window_to,omitempty"`
	Status       string   `json:"status,omitempty"` // planned / advanced / done / dropped
}

// WeaveRow 编织表一行：章号 → 本章推进的事件与静默线索。
type WeaveRow struct {
	Chapter       int      `json:"chapter"`
	AdvanceEvents []string `json:"advance_events,omitempty"` // 本章应推进的事件 id
	SilentThreads []string `json:"silent_threads,omitempty"` // 本章有意静默的线索
}

// EventWeave 事件池 + 编织表。
type EventWeave struct {
	Events []WeaveEvent `json:"events"`
	Rows   []WeaveRow   `json:"rows,omitempty"`
}

// Validate 校验事件必填与引用存在性。
func (w EventWeave) Validate() error {
	ids := map[string]bool{}
	for i, e := range w.Events {
		if e.ID == "" || e.Summary == "" || e.Thread == "" {
			return fmt.Errorf("event_weave.events[%d] 缺少 id/summary/thread", i)
		}
		if ids[e.ID] {
			return fmt.Errorf("event_weave 事件 id 重复: %s", e.ID)
		}
		ids[e.ID] = true
	}
	for _, r := range w.Rows {
		for _, id := range r.AdvanceEvents {
			if !ids[id] {
				return fmt.Errorf("编织表第%d章引用不存在的事件: %s", r.Chapter, id)
			}
		}
	}
	return nil
}

// RowFor 返回某章的编织行；无排期返回 nil。
func (w EventWeave) RowFor(chapter int) *WeaveRow {
	for i := range w.Rows {
		if w.Rows[i].Chapter == chapter {
			return &w.Rows[i]
		}
	}
	return nil
}

// PlanWeaveConflicts 对比"本章计划涉及的事件 id"与编织表，返回冲突描述
// （推进了未排期事件 / 跳过了已排期事件）。planned 为空且无编织行时零冲突。
func (w EventWeave) PlanWeaveConflicts(chapter int, plannedEventIDs []string) []string {
	row := w.RowFor(chapter)
	if row == nil && len(plannedEventIDs) == 0 {
		return nil
	}
	scheduled := map[string]bool{}
	if row != nil {
		for _, id := range row.AdvanceEvents {
			scheduled[id] = true
		}
	}
	planned := map[string]bool{}
	var conflicts []string
	for _, id := range plannedEventIDs {
		planned[id] = true
		if row != nil && !scheduled[id] {
			conflicts = append(conflicts, fmt.Sprintf("本章计划推进事件 %s，但编织表第%d章未排期它——plan 里必须说明改排理由", id, chapter))
		}
	}
	if row != nil {
		for _, id := range row.AdvanceEvents {
			if !planned[id] {
				conflicts = append(conflicts, fmt.Sprintf("编织表第%d章排期了事件 %s，本章计划未推进——plan 里必须说明顺延理由", chapter, id))
			}
		}
	}
	return conflicts
}

// SilentThreadOverruns 检测连续静默超过 threshold 章的线索（无 dropped/done 标记）。
// lastAdvanced 由调用方从编织表/事件状态推导：线索 → 最近推进章。
func (w EventWeave) SilentThreadOverruns(current, threshold int) []string {
	lastAdvanced := map[string]int{}
	threadAlive := map[string]bool{}
	for _, e := range w.Events {
		if e.Status == "done" || e.Status == "dropped" {
			continue
		}
		threadAlive[e.Thread] = true
	}
	for _, r := range w.Rows {
		for _, id := range r.AdvanceEvents {
			for _, e := range w.Events {
				if e.ID == id && r.Chapter > lastAdvanced[e.Thread] {
					lastAdvanced[e.Thread] = r.Chapter
				}
			}
		}
	}
	var out []string
	for thread := range threadAlive {
		if current-lastAdvanced[thread] > threshold {
			out = append(out, fmt.Sprintf("线索 %q 已连续 %d 章未推进（>阈值 %d）且无休眠标记", thread, current-lastAdvanced[thread], threshold))
		}
	}
	return out
}
