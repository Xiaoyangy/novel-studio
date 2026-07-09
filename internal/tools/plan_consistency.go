package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// checkChapterPlanConsistency 在 plan 收尾时对计划做一致性检查——计划是正文的唯一
// 范围依据，收尾前必须先与本书既定事实（角色、契约自洽、卷弧结构）对齐。
//
// 返回 (hard, warn)：
//   - hard：定义级矛盾，必须挡下 finalize 让 planner 修正（如契约自我矛盾）。
//   - warn：疑点，随 finalize 结果透出并落盘，供 drafter 在正文阶段核对，不阻断。
//
// 语义级矛盾（违反世界硬规则等）交给 planner 自身在 novel_context 下判断；这里只做
// 机器能确定的检查，避免误伤合法计划。
func checkChapterPlanConsistency(s *store.Store, plan domain.ChapterPlan) (hard []string, warn []string) {
	// 0) 章节标题必须继承 outline：plan.title 是章节名，不是书名，也不能另起标题。
	// 标题漂移会直接让 Writer 误判本章入口，必须在计划收口时挡下。
	if entry, err := s.Outline.GetChapterOutline(plan.Chapter); err == nil && entry != nil {
		want := strings.TrimSpace(entry.Title)
		got := strings.TrimSpace(plan.Title)
		if want != "" && !chapterTitleEquivalent(got, want) {
			hard = append(hard, fmt.Sprintf("章节标题与大纲不一致：第 %d 章 plan.title=%q，outline.title=%q；plan.title 必须使用大纲章节名，不能写成书名或另起章名", plan.Chapter, got, want))
		}
	}

	// 1) 契约自我矛盾（hard）：同一推进项既要求又禁止，计划无法自洽。
	for _, fm := range plan.Contract.ForbiddenMoves {
		fn := normalizeBeat(fm)
		if fn == "" {
			continue
		}
		for _, rb := range plan.Contract.RequiredBeats {
			if normalizeBeat(rb) == fn {
				hard = append(hard, fmt.Sprintf("契约自我矛盾：同一推进项既在 required_beats 又在 forbidden_moves —— %q", strings.TrimSpace(fm)))
			}
		}
	}

	// 2) cast 与角色档一致性（warn）：计划里推演到的角色若不在 characters.json，
	//    可能是计划外新角色或笔误——正文引入新角色应是有意为之，让 planner/drafter 确认。
	known := knownCharacterNames(s)
	if len(known) > 0 {
		seen := map[string]bool{}
		for _, name := range chapterPlanCast(plan) {
			key := strings.TrimSpace(name)
			if key == "" || seen[key] || known[key] {
				continue
			}
			seen[key] = true
			warn = append(warn, fmt.Sprintf("角色 %q 出现在本章推演但不在角色档 —— 确认是有意引入的新角色（正文需交代来历）还是笔误/别名不一致", key))
		}
	}

	// 3) 卷弧结构对齐（warn）：本章号超出已规划的总章数，说明计划可能越界。
	if total := plannedChapterTotal(s); total > 0 && plan.Chapter > total {
		warn = append(warn, fmt.Sprintf("本章号 %d 超出已规划总章数 %d —— 确认是否需要先扩展卷弧大纲，正文不应写入大纲未覆盖的情节", plan.Chapter, total))
	}

	return hard, warn
}

// normalizeBeat 归一化推进项文本用于比对（去空白、转小写）。
func normalizeBeat(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func chapterTitleEquivalent(a, b string) bool {
	return normalizeChapterTitleText(a) == normalizeChapterTitleText(b)
}

func normalizeChapterTitleText(s string) string {
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#"))
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "第") {
		if idx := strings.Index(s, "章"); idx >= 0 {
			s = strings.TrimSpace(s[idx+len("章"):])
		}
	}
	s = strings.TrimLeft(s, " ：:.-")
	s = strings.Join(strings.Fields(s), "")
	return strings.ToLower(s)
}

// chapterPlanCast 从推演层收集本章涉及的角色名（各含 Character 字段的子结构）。
func chapterPlanCast(plan domain.ChapterPlan) []string {
	sim := plan.CausalSimulation
	var out []string
	for _, v := range sim.VoiceLogic {
		out = append(out, v.Character)
	}
	for _, v := range sim.InitialState {
		out = append(out, v.Character)
	}
	for _, v := range sim.OffscreenStage {
		out = append(out, v.Character)
	}
	for _, v := range sim.EmotionalLogic {
		out = append(out, v.Character)
	}
	return out
}

// knownCharacterNames 返回角色档里的全部正名与别名集合。
func knownCharacterNames(s *store.Store) map[string]bool {
	chars, err := s.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	known := make(map[string]bool, len(chars)*2)
	for _, c := range chars {
		if n := strings.TrimSpace(c.Name); n != "" {
			known[n] = true
		}
		for _, a := range c.Aliases {
			if a = strings.TrimSpace(a); a != "" {
				known[a] = true
			}
		}
	}
	return known
}

// plannedChapterTotal 从分层大纲/线性大纲推断已规划的总章数（0=无法确定）。
func plannedChapterTotal(s *store.Store) int {
	if vols, err := s.Outline.LoadLayeredOutline(); err == nil && len(vols) > 0 {
		total := 0
		for _, v := range vols {
			for _, arc := range v.Arcs {
				if n := len(arc.Chapters); n > 0 {
					total += n // 已展开的弧按实际章数
				} else {
					total += arc.EstimatedChapters // 骨架弧按预估
				}
			}
		}
		if total > 0 {
			return total
		}
	}
	if entries, err := s.Outline.LoadOutline(); err == nil && len(entries) > 0 {
		return len(entries)
	}
	return 0
}
