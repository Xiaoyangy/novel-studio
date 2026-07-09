package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// 世界推演（离屏 tick）卡点的单一事实源。两个卡点：
//   - 第 1 章写作前：初始 world_tick 必须已生成（离屏世界有信息流，不是空基线）；
//   - 弧/卷边界 expand_arc/append_volume 前：world_tick 必须已推进到弧末。
// 未启用世界推演（无 tick 工件）的短篇/简单项目一律零影响。

// worldSimEnabled 报告项目是否启用了世界推演（zero-init 会写 world_tick.json 基线）。
func worldSimEnabled(st *store.Store) bool {
	tick, err := st.WorldSim.LoadTick()
	return err == nil && tick != nil
}

// worldTickSubstantive 报告离屏世界是否已有实质信息流（≥1 条镜头外事件）。
// 空的 v0-a0 基线（0 事件）不算——那只是占位，世界还没"活起来"。
func worldTickSubstantive(st *store.Store) bool {
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil || strings.TrimSpace(tick.TickID) == "" || tick.TickID == "v0-a0" || tick.EventCount <= 0 {
		return false
	}
	events, err := st.WorldSim.LoadWorldEvents()
	return err == nil && len(events) > 0
}

// InitialWorldTickQualityIssues returns blocking issues for the opening world
// tick. These are not style warnings: if the tick references unknown actors,
// downstream planning can consume a different project/genre without noticing.
func InitialWorldTickQualityIssues(st *store.Store) []string {
	tick, err := st.WorldSim.LoadTick()
	if err != nil {
		return []string{fmt.Sprintf("world_tick 不可读: %v", err)}
	}
	if tick == nil || strings.TrimSpace(tick.TickID) == "" || tick.TickID == "v0-a0" || tick.EventCount <= 0 {
		return []string{"world_tick 仍是空基线或缺少事件计数"}
	}
	events, err := st.WorldSim.LoadWorldEvents()
	if err != nil {
		return []string{fmt.Sprintf("world_events 不可读: %v", err)}
	}
	if len(events) == 0 {
		return []string{"world_events 为空"}
	}
	known := worldTickKnownActorSet(st)
	if len(known) == 0 {
		return nil
	}
	var issues []string
	for _, event := range events {
		for _, actor := range event.Actors {
			actor = strings.TrimSpace(actor)
			if actor == "" {
				continue
			}
			if _, ok := known[actor]; !ok {
				issues = append(issues, fmt.Sprintf("world_tick 事件 %q 的 actor %q 不在角色册/势力册/别名中", compactWorldTickIssue(event.Summary), actor))
			}
		}
	}
	return issues
}

func worldTickKnownActorSet(st *store.Store) map[string]struct{} {
	known := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			known[value] = struct{}{}
		}
	}
	if chars, err := st.Characters.Load(); err == nil {
		for _, c := range chars {
			add(c.Name)
			for _, alias := range c.Aliases {
				add(alias)
			}
		}
	}
	if entries, err := st.Cast.Load(); err == nil {
		for _, e := range entries {
			add(e.Name)
		}
	}
	if world, err := st.World.LoadBookWorld(); err == nil && world != nil {
		for _, faction := range world.Factions {
			add(faction.ID)
			add(faction.Name)
			for _, alias := range faction.Aliases {
				add(alias)
			}
		}
	}
	return known
}

func compactWorldTickIssue(s string) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= 30 {
		return string(runes)
	}
	return string(runes[:30]) + "..."
}

// EnsureInitialWorldTickForChapterOne 第 1 章写作前的初始 world_tick 硬卡点。
// 仅长篇（分层）项目：启用世界推演但离屏世界还没有任何事件时拒绝——离屏世界的
// 信息流是下游推演所需，必须在第 1 章推演/渲染前由 Architect 先跑一次开局
// save_world_tick 建立。短篇（扁平）与第 1 章已写完（重写路径）时不拦。
func EnsureInitialWorldTickForChapterOne(st *store.Store) error {
	if !ChapterOnePendingFirstWrite(st) || !worldSimEnabled(st) {
		return nil
	}
	if !worldSimRequiresInitialTick(st) {
		return nil // 短篇/扁平项目不强制离屏世界推演
	}
	if worldTickSubstantive(st) {
		if issues := InitialWorldTickQualityIssues(st); len(issues) == 0 {
			return nil
		} else {
			return fmt.Errorf("第 1 章写作前的 world_tick 不合格：%s", strings.Join(issues, "；"))
		}
	}
	return fmt.Errorf("第 1 章写作前，离屏世界推演游标必须已生成：请派 architect_long 先跑一次开局 save_world_tick，" +
		"按各离屏角色 agenda 与势力钟推进出开局前的镜头外事件（每条带 visibility_chapter/visibility_path），" +
		"让世界在第 1 章之前已经在自转，之后再推演/渲染第 1 章")
}

func worldSimRequiresInitialTick(st *store.Store) bool {
	progress, err := st.Progress.Load()
	if err == nil && progress != nil {
		if progress.Layered {
			return true
		}
		if progress.TotalChapters > 30 {
			return true
		}
	}
	layered, err := st.Outline.LoadLayeredOutline()
	return err == nil && len(layered) > 0
}

// EnsureWorldTickCurrent 弧/卷边界的 world_tick 硬卡点：world_tick 落后已完成正文时拒绝。
// 展开下一弧/追加新卷前，镜头外世界必须已推进到刚结束的弧末——否则下一弧规划
// 消费不到离屏事件与伏笔素材。
func EnsureWorldTickCurrent(st *store.Store) error {
	if !worldSimEnabled(st) {
		return nil
	}
	tick, err := st.WorldSim.LoadTick()
	if err != nil || tick == nil {
		return nil
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return nil
	}
	latest := progress.LatestCompleted()
	if latest <= tick.ThroughChapter {
		return nil
	}
	return fmt.Errorf("镜头外世界推演落后正文：world_tick 只推进到第 %d 章，正文已完成到第 %d 章。"+
		"展开下一弧/追加新卷前必须先调 save_world_tick 把世界推进到弧末——"+
		"推进各离屏 agenda、产生镜头外事件、拨势力钟，并按 story_calendar 每章天数推算事件 visibility_chapter，再 expand_arc/append_volume",
		tick.ThroughChapter, latest)
}
