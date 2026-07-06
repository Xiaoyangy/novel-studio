package domain

import "fmt"

// WorldEvent 一条镜头外（离屏）世界事件：世界模拟范式下，世界先动、正文只是
// 主角视角的采样。事件由 Architect 在弧边界的世界 tick 中裁决产生（GM 模式），
// append-only 落盘 meta/world_events.jsonl。
//
// 可见性即信息传播：VisibilityChapter 是事件最早可能进入主角感知的章号
// （按 physics_axioms.info_propagation 推算），novel_context 只把"已越过地平线"
// 的事件注入 Writer——正文永远只写主角能感知到的世界。
type WorldEvent struct {
	ID                  string   `json:"id"`                // "we-000123"，store 自动分配
	TickID              string   `json:"tick_id,omitempty"` // 所属 tick，如 "v2-a1"
	Chapter             int      `json:"chapter"`           // 事件发生时点（故事内章号坐标）
	Location            string   `json:"location,omitempty"`
	Actors              []string `json:"actors"`                    // 参与角色/势力
	Summary             string   `json:"summary"`                   // 一句话事实
	Consequence         string   `json:"consequence,omitempty"`     // 对世界状态的影响
	VisibilityChapter   int      `json:"visibility_chapter"`        // 最早进入主角感知的章号
	VisibilityPath      string   `json:"visibility_path,omitempty"` // 谣言/信使/亲见/官报
	ForeshadowCandidate bool     `json:"foreshadow_candidate,omitempty"`
	Tier                string   `json:"tier,omitempty"` // supporting / background
}

// Validate 校验事件必填字段与因果次序（信息不能早于事件发生）。
func (e WorldEvent) Validate() error {
	if e.Summary == "" {
		return fmt.Errorf("world_event 缺少 summary")
	}
	if len(e.Actors) == 0 {
		return fmt.Errorf("world_event %q 缺少 actors", e.Summary)
	}
	if e.Chapter <= 0 {
		return fmt.Errorf("world_event %q 的 chapter 必须 > 0", e.Summary)
	}
	if e.VisibilityChapter < e.Chapter {
		return fmt.Errorf("world_event %q 的 visibility_chapter(%d) 不能早于事件发生章(%d)", e.Summary, e.VisibilityChapter, e.Chapter)
	}
	return nil
}

// WorldTick 世界推演游标：记录离屏世界已推演覆盖到的位置。落盘 meta/world_tick.json。
type WorldTick struct {
	TickID         string `json:"tick_id"` // "v2-a1"
	Volume         int    `json:"volume,omitempty"`
	Arc            int    `json:"arc,omitempty"`
	ThroughChapter int    `json:"through_chapter"` // 推演已覆盖到的章
	EventCount     int    `json:"event_count,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

// NominateForeshadowCandidates 确定性伏笔提名：模拟范式的独有红利——离屏事件里
// "将来才会撞上主角"的，天然是伏笔素材。命中两类：
//   - 显式标记 foreshadow_candidate 的事件
//   - visibility_chapter > current+lead 的远期事件（还没浮出 = 天然埋线窗口）
//
// 提名只做注入素材，不自动写 foreshadow_ledger（账本仍由 commit_chapter 单一入口维护）。
func NominateForeshadowCandidates(events []WorldEvent, current, lead int) []WorldEvent {
	var out []WorldEvent
	for _, e := range events {
		if e.ForeshadowCandidate || e.VisibilityChapter > current+lead {
			out = append(out, e)
		}
	}
	return out
}
