package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func characterStageJSON(chapter int) string {
	return filepath.Join("meta", "character_stage", fmt.Sprintf("%03d.json", chapter))
}

func characterStageMD(chapter int) string {
	return filepath.Join("meta", "character_stage", fmt.Sprintf("%03d.md", chapter))
}

func sideCharacterJourneyJSON(chapter int) string {
	return filepath.Join("meta", "side_character_journeys", fmt.Sprintf("%03d.json", chapter))
}

func sideCharacterJourneyMD(chapter int) string {
	return filepath.Join("meta", "side_character_journeys", fmt.Sprintf("%03d.md", chapter))
}

func (s *Store) SaveCharacterStageRecords(chapter int, records []domain.CharacterStageRecord) error {
	if chapter <= 0 || len(records) == 0 {
		return nil
	}
	out := make([]domain.CharacterStageRecord, 0, len(records))
	for _, r := range records {
		if strings.TrimSpace(r.Character) == "" {
			continue
		}
		r.Chapter = chapter
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(characterStageJSON(chapter), out); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(characterStageMD(chapter), renderCharacterStageRecords(chapter, out))
}

func (s *Store) SaveSideCharacterJourneys(chapter int, records []domain.CharacterStageRecord) error {
	if chapter <= 0 || len(records) == 0 {
		return nil
	}
	protagonist := strings.TrimSpace(s.inferProtagonist())
	out := make([]domain.CharacterStageRecord, 0, len(records))
	for _, r := range records {
		r.Character = strings.TrimSpace(r.Character)
		if r.Character == "" || (protagonist != "" && r.Character == protagonist) {
			continue
		}
		r.Chapter = chapter
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(sideCharacterJourneyJSON(chapter), out); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(sideCharacterJourneyMD(chapter), renderSideCharacterJourneys(chapter, protagonist, out))
}

func (s *Store) LoadCharacterStageRecords(chapter int) ([]domain.CharacterStageRecord, error) {
	if chapter <= 0 {
		return nil, nil
	}
	var records []domain.CharacterStageRecord
	if err := s.Progress.io.ReadJSON(characterStageJSON(chapter), &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) LoadRecentCharacterStageRecords(current, window int) ([]domain.CharacterStageRecord, error) {
	if window <= 0 {
		window = 5
	}
	start := current - window
	if start < 1 {
		start = 1
	}
	var out []domain.CharacterStageRecord
	var firstErr error
	for ch := start; ch < current; ch++ {
		records, err := s.LoadCharacterStageRecords(ch)
		if err != nil {
			if !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, records...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Chapter != out[j].Chapter {
			return out[i].Chapter < out[j].Chapter
		}
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Character < out[j].Character
	})
	return out, firstErr
}

func (s *Store) LoadSideCharacterJourneys(chapter int) ([]domain.CharacterStageRecord, error) {
	if chapter <= 0 {
		return nil, nil
	}
	var records []domain.CharacterStageRecord
	if err := s.Progress.io.ReadJSON(sideCharacterJourneyJSON(chapter), &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *Store) LoadRecentSideCharacterJourneys(current, window int) ([]domain.CharacterStageRecord, error) {
	if window <= 0 {
		window = 5
	}
	start := current - window
	if start < 1 {
		start = 1
	}
	var out []domain.CharacterStageRecord
	var firstErr error
	for ch := start; ch < current; ch++ {
		records, err := s.LoadSideCharacterJourneys(ch)
		if err != nil {
			if !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, records...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Chapter != out[j].Chapter {
			return out[i].Chapter < out[j].Chapter
		}
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Character < out[j].Character
	})
	return out, firstErr
}

func renderCharacterStageRecords(chapter int, records []domain.CharacterStageRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%03d章 角色现场台账\n\n", chapter)
	b.WriteString("用途：记录正文内外角色在同一时间线中的环境、行动、误判、决策和后续潜势。正文只展示主视角可见行动；本台账用于防止角色后续突然出现或全知全能。\n\n")
	for _, r := range records {
		fmt.Fprintf(&b, "## %s\n\n", r.Character)
		if r.Time != "" {
			fmt.Fprintf(&b, "- 时间：%s\n", r.Time)
		}
		fmt.Fprintf(&b, "- 地点：%s\n", r.Location)
		if r.Status != "" {
			fmt.Fprintf(&b, "- 状态：%s\n", r.Status)
		}
		fmt.Fprintf(&b, "- 环境：%s\n", r.Environment)
		fmt.Fprintf(&b, "- 当前行动：%s\n", r.CurrentAction)
		fmt.Fprintf(&b, "- 压力：%s\n", r.Pressure)
		fmt.Fprintf(&b, "- 决策：%s\n", r.Decision)
		if r.DecisionReason != "" {
			fmt.Fprintf(&b, "- 决策理由：%s\n", r.DecisionReason)
		}
		if len(r.ButterflyEffects) > 0 {
			fmt.Fprintf(&b, "- 蝴蝶效应：%s\n", strings.Join(r.ButterflyEffects, "；"))
		}
		if r.MistakeOrMisbelief != "" {
			fmt.Fprintf(&b, "- 误判/错误：%s\n", r.MistakeOrMisbelief)
		}
		fmt.Fprintf(&b, "- 知识边界：%s\n", r.KnowledgeBoundary)
		fmt.Fprintf(&b, "- 正文可见：%t\n", r.VisibleInChapter)
		if r.Evidence != "" {
			fmt.Fprintf(&b, "- 证据：%s\n", r.Evidence)
		}
		if r.Transport != "" || r.TravelTime != "" || r.MeetingConstraint != "" {
			fmt.Fprintf(&b, "- 移动/见面限制：%s\n", movementSummary(r))
		}
		if r.PersonalityDelta != "" {
			fmt.Fprintf(&b, "- 性格变化：%s\n", r.PersonalityDelta)
		}
		if r.DeathState != "" {
			fmt.Fprintf(&b, "- 死亡/失踪/异化状态：%s\n", r.DeathState)
		}
		if r.ProtagonistNotice != "" {
			fmt.Fprintf(&b, "- 传回主角计划：%s\n", r.ProtagonistNotice)
		}
		fmt.Fprintf(&b, "- 时间线一致性：%s\n", r.TimelineConsistency)
		if r.NextPotential != "" {
			fmt.Fprintf(&b, "- 后续潜势：%s\n", r.NextPotential)
		}
		if len(r.Tags) > 0 {
			fmt.Fprintf(&b, "- 标签：%s\n", strings.Join(r.Tags, "、"))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderSideCharacterJourneys(chapter int, protagonist string, records []domain.CharacterStageRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%03d章 配角动态日志\n\n", chapter)
	if protagonist != "" {
		fmt.Fprintf(&b, "- 主角：%s（本目录自动排除主角，只记录主角视角之外的人物经历）\n", protagonist)
	}
	b.WriteString("- 用途：沉淀配角所在场景、行动时间线、移动限制、性格变化、死亡/失踪状态和何时传回主角。\n")
	b.WriteString("- 规则：配角不能随主角随叫随到；没有交通、凭证、附身或快速移动能力时，按现实耗时移动。\n\n")
	for _, r := range records {
		fmt.Fprintf(&b, "## %s\n\n", r.Character)
		if r.Time != "" {
			fmt.Fprintf(&b, "- 时间：%s\n", r.Time)
		}
		fmt.Fprintf(&b, "- 地点：%s\n", r.Location)
		if r.Status != "" {
			fmt.Fprintf(&b, "- 当前状态：%s\n", r.Status)
		}
		fmt.Fprintf(&b, "- 所在场景/环境：%s\n", r.Environment)
		fmt.Fprintf(&b, "- 本章经历/行动：%s\n", r.CurrentAction)
		fmt.Fprintf(&b, "- 压力：%s\n", r.Pressure)
		fmt.Fprintf(&b, "- 按性格做出的决策：%s\n", r.Decision)
		if r.DecisionReason != "" {
			fmt.Fprintf(&b, "- 决策理由：%s\n", r.DecisionReason)
		}
		if len(r.ButterflyEffects) > 0 {
			fmt.Fprintf(&b, "- 蝴蝶效应：%s\n", strings.Join(r.ButterflyEffects, "；"))
		}
		if r.MistakeOrMisbelief != "" {
			fmt.Fprintf(&b, "- 误判/错误：%s\n", r.MistakeOrMisbelief)
		}
		fmt.Fprintf(&b, "- 知识边界：%s\n", r.KnowledgeBoundary)
		fmt.Fprintf(&b, "- 正文可见：%t\n", r.VisibleInChapter)
		if r.Evidence != "" {
			fmt.Fprintf(&b, "- 证据：%s\n", r.Evidence)
		}
		fmt.Fprintf(&b, "- 移动/见面限制：%s\n", movementSummary(r))
		if r.PersonalityDelta != "" {
			fmt.Fprintf(&b, "- 性格变化：%s\n", r.PersonalityDelta)
		}
		if r.DeathState != "" {
			fmt.Fprintf(&b, "- 死亡/失踪/异化状态：%s\n", r.DeathState)
		}
		if r.ProtagonistNotice != "" {
			fmt.Fprintf(&b, "- 传回主角计划：%s\n", r.ProtagonistNotice)
		}
		fmt.Fprintf(&b, "- 时间线一致性：%s\n", r.TimelineConsistency)
		if r.NextPotential != "" {
			fmt.Fprintf(&b, "- 后续推进项：%s\n", r.NextPotential)
		}
		if len(r.Tags) > 0 {
			fmt.Fprintf(&b, "- 标签：%s\n", strings.Join(r.Tags, "、"))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func movementSummary(r domain.CharacterStageRecord) string {
	var parts []string
	if r.Transport != "" {
		parts = append(parts, "交通/移动方式："+r.Transport)
	}
	if r.TravelTime != "" {
		parts = append(parts, "耗时："+r.TravelTime)
	}
	if r.MeetingConstraint != "" {
		parts = append(parts, "见面限制："+r.MeetingConstraint)
	}
	if len(parts) == 0 {
		return "未声明；后续不得默认随叫随到，需按 book_world 路线补足交通与耗时"
	}
	return strings.Join(parts, "；")
}
