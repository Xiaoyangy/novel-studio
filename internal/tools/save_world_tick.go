package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore/schema"
)

// SaveWorldTickTool 世界推演（离屏世界 tick）的唯一持久化入口，Architect 在弧边界
// 以 Game Master 身份裁决镜头外世界的变化后调用。
//
// 合宪定位：工具只做事实管道 + 确定性护栏（warning 事实随返回透出，绝不阻塞）；
// GM 裁决规则在 Description 与 architect prompt 里（复杂度进模型）。
type SaveWorldTickTool struct {
	store *store.Store
}

// NewSaveWorldTickTool 创建世界推演工具。
func NewSaveWorldTickTool(store *store.Store) *SaveWorldTickTool {
	return &SaveWorldTickTool{store: store}
}

func (t *SaveWorldTickTool) Name() string { return "save_world_tick" }

func (t *SaveWorldTickTool) Description() string {
	return "保存一次世界推演（离屏世界 tick）的结果：镜头外事件、角色日程推进、社会情绪与分层名单更新。" +
		"你此刻是 Game Master：只推演镜头外的世界（绝不复写、不改动已发布正文的事实）；" +
		"每条事件必须按 physics_axioms.info_propagation（如有）推算 visibility_chapter（该消息最早可能传到主角处的章号）与 visibility_path（谣言/信使/亲见/官报）；" +
		"supporting 层每个在册角色按其 offscreen_agenda 推进 1-2 步并回写 agenda_updates；" +
		"background 层不逐角色推演，只用 social_mood 更新群体情绪与谣言；" +
		"与既有事实（timeline/relationship_state/resource_ledger/info_graph/已发布章节）冲突时，一律以既有事实为准，调整你的推演而不是账本；" +
		"将来才会浮出（visibility 远期）或有回收价值的事件标 foreshadow_candidate=true，供下一弧规划埋线。" +
		"参数：{volume, arc, through_chapter, events[], agenda_updates[], social_mood?, tier_updates[]}。"
}

func (t *SaveWorldTickTool) Label() string { return "世界推演" }

// 写工具（更新世界事实层），禁止并发。
func (t *SaveWorldTickTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveWorldTickTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveWorldTickTool) Schema() map[string]any {
	eventSchema := schema.Object(
		schema.Property("chapter", schema.Int("事件发生时点（故事内章号坐标）")).Required(),
		schema.Property("actors", schema.Array("参与角色/势力名", schema.String(""))).Required(),
		schema.Property("summary", schema.String("一句话事实（发生了什么）")).Required(),
		schema.Property("consequence", schema.String("对世界状态的影响")),
		schema.Property("location", schema.String("地点")),
		schema.Property("visibility_chapter", schema.Int("最早可能进入主角感知的章号（按信息传播推算，不得早于 chapter）")).Required(),
		schema.Property("visibility_path", schema.String("传播路径：谣言/信使/亲见/官报等")),
		schema.Property("foreshadow_candidate", schema.Bool("是否有埋线回收价值")),
		schema.Property("tier", schema.Enum("产生该事件的模拟层", "supporting", "background")),
	)
	agendaSchema := schema.Object(
		schema.Property("name", schema.String("角色名")).Required(),
		schema.Property("current_goal", schema.String("当前目标")).Required(),
		schema.Property("motivation", schema.String("动机锚点（与画像/资源/关系挂钩）")),
		schema.Property("steps", schema.Array("目标分解步骤", schema.Object(
			schema.Property("description", schema.String("这一步做什么")).Required(),
			schema.Property("eta_chapters", schema.Int("预计耗时（章）")),
			schema.Property("done", schema.Bool("是否已完成")),
		))),
		schema.Property("status", schema.Enum("日程状态", "active", "blocked", "dormant", "completed")),
		schema.Property("blocked_by", schema.String("受阻原因（status=blocked 时）")),
		schema.Property("last_advanced_chapter", schema.Int("最近一次推进对应的章号")),
	)
	tierSchema := schema.Object(
		schema.Property("name", schema.String("角色名")).Required(),
		schema.Property("tier", schema.Enum("模拟层", "protagonist_circle", "supporting", "background")).Required(),
		schema.Property("reason", schema.String("升降级理由")),
	)
	return schema.Object(
		schema.Property("volume", schema.Int("本次 tick 所在卷")),
		schema.Property("arc", schema.Int("本次 tick 所在弧")),
		schema.Property("through_chapter", schema.Int("推演覆盖到的章号（通常为刚结束的弧的末章）")).Required(),
		schema.Property("events", schema.Array("镜头外事件（世界这段时间发生了什么）", eventSchema)),
		schema.Property("agenda_updates", schema.Array("角色日程推进（supporting 层逐角色 1-2 步）", agendaSchema)),
		schema.Property("social_mood", schema.Object(
			schema.Property("mood", schema.String("当前社会情绪")).Required(),
			schema.Property("intensity", schema.Number("强度 0-1")).Required(),
			schema.Property("rumors", schema.Array("街头谣言", schema.Object(
				schema.Property("text", schema.String("谣言内容")).Required(),
				schema.Property("credibility", schema.Number("可信度 0-1")).Required(),
				schema.Property("spread_rate", schema.Number("传播速度 0-1")).Required(),
				schema.Property("source_faction", schema.String("源头势力")),
			))),
		)),
		schema.Property("tier_updates", schema.Array("模拟分层升降级", tierSchema)),
		schema.Property("faction_clock_updates", schema.Array("势力进度钟拨动（Blades 式：关注哪个拨哪个，被忽略的下次补拨；走满的钟必须转化为镜头外事件）", schema.Object(
			schema.Property("target", schema.String("势力名或 ID")).Required(),
			schema.Property("ticks", schema.Int("本次拨动段数（通常每弧 1-2 段，重大变故可更多）")).Required(),
			schema.Property("note", schema.String("拨钟理由")),
		))),
	)
}

func (t *SaveWorldTickTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume         int                      `json:"volume"`
		Arc            int                      `json:"arc"`
		ThroughChapter int                      `json:"through_chapter"`
		Events         []domain.WorldEvent      `json:"events"`
		AgendaUpdates  []domain.CharacterAgenda `json:"agenda_updates"`
		SocialMood     *domain.SocialMood       `json:"social_mood"`
		TierUpdates    []domain.TierAssignment  `json:"tier_updates"`
		ClockUpdates   []struct {
			Target string `json:"target"`
			Ticks  int    `json:"ticks"`
			Note   string `json:"note"`
		} `json:"faction_clock_updates"`
	}
	if err := unmarshalToolArgs(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.ThroughChapter <= 0 {
		return nil, fmt.Errorf("through_chapter must be > 0: %w", errs.ErrToolArgs)
	}

	var warnings []string

	// 护栏 1：through_chapter 不应倒退于上次 tick。
	if prev, err := t.store.WorldSim.LoadTick(); err == nil && prev != nil && a.ThroughChapter < prev.ThroughChapter {
		warnings = append(warnings, fmt.Sprintf("through_chapter(%d) 倒退于上次 tick(%d)，请确认不是漏读了游标", a.ThroughChapter, prev.ThroughChapter))
	}

	// 护栏 2：actor 应是已知角色/势力（characters.json + cast_ledger + book_world factions）。
	known := t.knownActorSet()
	for _, e := range a.Events {
		for _, actor := range e.Actors {
			if actor == "" {
				continue
			}
			if _, ok := known[actor]; !ok && len(known) > 0 {
				warnings = append(warnings, fmt.Sprintf("事件 %q 的 actor %q 不在角色册/势力册中（如是新角色请先经 cast_intros/characters 入册）", compactProgressTextTool(e.Summary), actor))
			}
		}
	}

	// 护栏 3：visibility 不得早于事件发生——自动修正并记警告（信息不能早于事件）。
	for i := range a.Events {
		if a.Events[i].VisibilityChapter < a.Events[i].Chapter {
			warnings = append(warnings, fmt.Sprintf("事件 %q 的 visibility_chapter(%d) 早于发生章(%d)，已自动修正", compactProgressTextTool(a.Events[i].Summary), a.Events[i].VisibilityChapter, a.Events[i].Chapter))
			a.Events[i].VisibilityChapter = a.Events[i].Chapter
		}
	}

	tickID := fmt.Sprintf("v%d-a%d", a.Volume, a.Arc)
	for i := range a.Events {
		if a.Events[i].TickID == "" {
			a.Events[i].TickID = tickID
		}
	}

	saved, err := t.store.WorldSim.AppendWorldEvents(a.Events)
	if err != nil {
		return nil, fmt.Errorf("append world events: %w: %w", errs.ErrStoreWrite, err)
	}

	// 日程账本 upsert。
	if len(a.AgendaUpdates) > 0 {
		ledger, err := t.store.WorldSim.LoadAgendaLedger()
		if err != nil {
			return nil, fmt.Errorf("load agenda ledger: %w: %w", errs.ErrStoreWrite, err)
		}
		for _, agenda := range a.AgendaUpdates {
			if agenda.LastAdvancedChapter == 0 {
				agenda.LastAdvancedChapter = a.ThroughChapter
			}
			if err := agenda.Validate(); err != nil {
				warnings = append(warnings, fmt.Sprintf("日程 %q 非法已跳过: %v", agenda.Name, err))
				continue
			}
			ledger = ledger.Upsert(agenda)
		}
		if err := t.store.WorldSim.SaveAgendaLedger(ledger); err != nil {
			return nil, fmt.Errorf("save agenda ledger: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 可选：社会情绪与分层名单。
	if a.SocialMood != nil {
		a.SocialMood.Chapter = a.ThroughChapter
		if err := a.SocialMood.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("social_mood 非法已跳过: %v", err))
		} else if err := t.store.Methodology.SaveSocialMood(*a.SocialMood); err != nil {
			return nil, fmt.Errorf("save social mood: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.TierUpdates) > 0 {
		cast, err := t.store.WorldSim.LoadSimulationCast()
		if err != nil {
			return nil, fmt.Errorf("load simulation cast: %w: %w", errs.ErrStoreWrite, err)
		}
		for _, upd := range a.TierUpdates {
			if upd.Since == 0 {
				upd.Since = a.ThroughChapter
			}
			cast = cast.Upsert(upd)
		}
		if err := t.store.WorldSim.SaveSimulationCast(cast); err != nil {
			return nil, fmt.Errorf("save simulation cast: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// 势力进度钟拨动：按 name/ID 匹配 book_world 势力，走满的钟随事实透出，
	// 提醒 GM 把 Consequence 转化为镜头外事件。
	var clocksCompleted []string
	if len(a.ClockUpdates) > 0 {
		world, err := t.store.World.LoadBookWorld()
		if err != nil || world == nil {
			warnings = append(warnings, "book_world 不可读，势力钟拨动已跳过")
		} else {
			changed := false
			for _, upd := range a.ClockUpdates {
				matched := false
				for i := range world.Factions {
					f := &world.Factions[i]
					if f.Name != upd.Target && f.ID != upd.Target {
						continue
					}
					matched = true
					if f.Clock == nil {
						warnings = append(warnings, fmt.Sprintf("势力 %q 未设进度钟（应在 book_world 里先建 clock），本次拨动跳过", upd.Target))
						break
					}
					if f.Clock.Tick(upd.Ticks) {
						clocksCompleted = append(clocksCompleted, fmt.Sprintf("%s: %s", f.Name, f.Clock.Consequence))
					}
					changed = true
					break
				}
				if !matched {
					warnings = append(warnings, fmt.Sprintf("势力钟目标 %q 不在 book_world 势力册中，已跳过", upd.Target))
				}
			}
			if changed {
				if err := t.store.World.SaveBookWorld(*world); err != nil {
					return nil, fmt.Errorf("save book world clocks: %w: %w", errs.ErrStoreWrite, err)
				}
			}
		}
	}

	if err := t.store.WorldSim.SaveTick(domain.WorldTick{
		TickID:         tickID,
		Volume:         a.Volume,
		Arc:            a.Arc,
		ThroughChapter: a.ThroughChapter,
		EventCount:     len(saved),
	}); err != nil {
		return nil, fmt.Errorf("save world tick: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint：崩溃恢复时可见"世界推演已做到哪"，弧边界重启不重复 tick。
	if _, err := t.store.Checkpoints.AppendArtifact(domain.ArcScope(a.Volume, a.Arc), "world_tick", "meta/world_tick.json"); err != nil {
		warnings = append(warnings, fmt.Sprintf("world_tick checkpoint 追加失败（不影响本次落盘）: %v", err))
	}

	result := map[string]any{
		"saved":        true,
		"tick_id":      tickID,
		"saved_events": len(saved),
		"agenda_count": len(a.AgendaUpdates),
	}
	if len(clocksCompleted) > 0 {
		result["clocks_completed"] = clocksCompleted
		result["clocks_completed_usage"] = "这些势力钟已走满：其 consequence 必须在本次或下次 tick 转化为镜头外事件，并考虑重置/换新钟"
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return json.Marshal(result)
}

// knownActorSet 汇总已知角色与势力名（characters + cast_ledger + book_world factions）。
// 任一来源加载失败只影响护栏（返回空集时跳过检查），不影响写入。
func (t *SaveWorldTickTool) knownActorSet() map[string]struct{} {
	known := make(map[string]struct{})
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			if c.Name != "" {
				known[c.Name] = struct{}{}
			}
			for _, alias := range c.Aliases {
				if alias != "" {
					known[alias] = struct{}{}
				}
			}
		}
	}
	if entries, err := t.store.Cast.Load(); err == nil {
		for _, e := range entries {
			if e.Name != "" {
				known[e.Name] = struct{}{}
			}
		}
	}
	if world, err := t.store.World.LoadBookWorld(); err == nil && world != nil {
		for _, f := range world.Factions {
			if f.Name != "" {
				known[f.Name] = struct{}{}
			}
			if f.ID != "" {
				known[f.ID] = struct{}{}
			}
		}
	}
	return known
}

// compactProgressTextTool 事件摘要截断（警告文案用）。
func compactProgressTextTool(s string) string {
	runes := []rune(s)
	if len(runes) <= 30 {
		return s
	}
	return string(runes[:30]) + "…"
}
