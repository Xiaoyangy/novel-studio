package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Task 074：commit 前确定性对账器（ConStory-Checker 证据锚定 + FACTTRACK 有效期语义）。
// 全部 warning/info 级 rules.Violation，绝不阻塞；每条带原文短引或台账键作 Evidence，
// 让 Editor/LLM 复核而不是从零自查。研究依据（Lost in Stories）：一致性错误集中在
// 事实与时间维度——这层机器先把可机检的部分筛出来。

// consistencyReconcile 跑五类对账：存亡 / 位置 / 资源数额 / 时间顺序 / 别名混用。
func consistencyReconcile(s *store.Store, chapter int, content string, resourceUpdates []domain.ResourceClaim) []rules.Violation {
	var violations []rules.Violation
	warn := func(rule, evidence string, actual any) {
		violations = append(violations, rules.Violation{
			Rule: rule, Target: evidence, Actual: actual, Severity: rules.SeverityWarning,
		})
	}

	chars, _ := s.Characters.Load()
	changes, _ := s.World.LoadStateChanges()
	latest := domain.LatestFactValues(changes)

	// --- 1. 存亡对账：status/injury 链尾为死亡/失踪的角色在本章出现且无复活/回忆标记 ---
	deadMarkers := []string{"死亡", "身亡", "已死", "殒命", "失踪"}
	reviveMarkers := []string{"复活", "回忆", "闪回", "梦中", "回想", "灵体", "残影", "遗言", "生前"}
	for _, c := range chars {
		key := c.Name + ":status"
		fact, ok := latest[key]
		if !ok || fact.Chapter >= chapter {
			continue
		}
		isDead := false
		for _, m := range deadMarkers {
			if strings.Contains(fact.NewValue, m) {
				isDead = true
				break
			}
		}
		if !isDead {
			continue
		}
		if idx := indexCharacterMention(content, c); idx >= 0 {
			ctxSnippet := snippetAround(content, idx, 24)
			revived := false
			for _, m := range reviveMarkers {
				if strings.Contains(ctxSnippet, m) || strings.Contains(content, c.Name+m) {
					revived = true
					break
				}
			}
			if !revived {
				warn("consistency_death",
					fmt.Sprintf("台账 %s=%q(第%d章) 但本章出现 %q（无复活/回忆标记）", key, fact.NewValue, fact.Chapter, ctxSnippet),
					c.Name)
			}
		}
	}

	// --- 2. 位置对账：location 链尾 vs 本章正文地点，且路线 travel_days 不允许当章往返 ---
	world, _ := s.World.LoadBookWorld()
	cal, _ := s.WorldSim.LoadStoryCalendar()
	daysPerChapter := 1.0
	if cal != nil && cal.DaysPerChapter > 0 {
		daysPerChapter = cal.DaysPerChapter
	}
	if world != nil {
		placeName := map[string]string{}
		for _, p := range world.Places {
			placeName[p.ID] = p.Name
		}
		travel := map[string]float64{}
		for _, r := range world.Routes {
			if r.TravelDays > 0 {
				travel[r.From+"|"+r.To] = r.TravelDays
				travel[r.To+"|"+r.From] = r.TravelDays
			}
		}
		for _, c := range chars {
			fact, ok := latest[c.Name+":location"]
			if !ok || fact.Chapter >= chapter || fact.NewValue == "" {
				continue
			}
			if indexCharacterMention(content, c) < 0 {
				continue
			}
			for id, name := range placeName {
				if name == "" || strings.Contains(fact.NewValue, name) || !strings.Contains(content, name) {
					continue
				}
				// 正文出现了与台账不同的地点：若路线时长超过本章覆盖天数则告警。
				if td, ok := travel[locationID(world, fact.NewValue)+"|"+id]; ok && td > daysPerChapter {
					warn("consistency_location",
						fmt.Sprintf("台账 %s:location=%q(第%d章)，本章出现地点 %q，路线需 %.1f 天 > 本章约 %.1f 天", c.Name, fact.NewValue, fact.Chapter, name, td, daysPerChapter),
						c.Name)
					break
				}
			}
		}
	}

	// --- 3. 资源数额对账：本章更新后余额为负 ---
	for _, claim := range resourceUpdates {
		if strings.Contains(claim.Name, "-") || claim.Status != "booked" {
			continue
		}
		if v, ok := parseLeadingNumber(claim.Name); ok && v < 0 {
			warn("consistency_resource",
				fmt.Sprintf("资源 %q 入账后为负值（owner=%s）", claim.Name, claim.Owner), claim.Name)
		}
	}

	// --- 4. 时间顺序对账：本章 timeline 事件的故事内日期倒流（无闪回标记） ---
	timeline, _ := s.World.LoadTimeline()
	var lastTime string
	for _, ev := range timeline {
		if ev.Chapter > chapter {
			continue
		}
		if ev.Chapter == chapter && lastTime != "" && ev.Time != "" && ev.Time < lastTime &&
			!strings.Contains(ev.Event, "闪回") && !strings.Contains(ev.Event, "回忆") {
			warn("consistency_time_order",
				fmt.Sprintf("第%d章时间线 %q 早于前序 %q 且无闪回标记（事件：%s）", chapter, ev.Time, lastTime, compactProgressTextTool(ev.Event)),
				ev.Time)
		}
		if ev.Time != "" {
			lastTime = ev.Time
		}
	}

	// --- 5. 别名混用：同一段落里同角色两个称呼交替且无引入语（info 级） ---
	for _, c := range chars {
		if len(c.Aliases) == 0 {
			continue
		}
		for _, para := range strings.Split(content, "\n") {
			if !strings.Contains(para, c.Name) {
				continue
			}
			for _, alias := range c.Aliases {
				if alias != "" && strings.Contains(para, alias) &&
					!strings.Contains(para, "又叫") && !strings.Contains(para, "也就是") && !strings.Contains(para, "人称") {
					violations = append(violations, rules.Violation{
						Rule:     "consistency_alias",
						Target:   fmt.Sprintf("同段落 %q 与 %q 交替指称（段首：%s）", c.Name, alias, compactProgressTextTool(para)),
						Actual:   c.Name,
						Severity: rules.SeverityWarning,
					})
					break
				}
			}
		}
	}
	return violations
}

// indexCharacterMention 返回角色名/别名在正文中的首个下标；未出现返回 -1。
func indexCharacterMention(content string, c domain.Character) int {
	if i := strings.Index(content, c.Name); i >= 0 {
		return i
	}
	for _, a := range c.Aliases {
		if a != "" {
			if i := strings.Index(content, a); i >= 0 {
				return i
			}
		}
	}
	return -1
}

// snippetAround 取下标附近的原文短引（证据锚定）。
func snippetAround(content string, idx, span int) string {
	runes := []rune(content)
	// 把字节下标近似换算为 rune 下标
	r := len([]rune(content[:idx]))
	lo, hi := r-span, r+span
	if lo < 0 {
		lo = 0
	}
	if hi > len(runes) {
		hi = len(runes)
	}
	return strings.ReplaceAll(string(runes[lo:hi]), "\n", " ")
}

// locationID 把台账里的地点值映射回 place ID（名字包含匹配）。
func locationID(world *domain.BookWorld, value string) string {
	for _, p := range world.Places {
		if p.Name != "" && strings.Contains(value, p.Name) {
			return p.ID
		}
	}
	return ""
}

// parseLeadingNumber 解析字符串开头的带符号数字。
func parseLeadingNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	var v float64
	n, err := fmt.Sscanf(s, "%f", &v)
	return v, err == nil && n == 1
}
