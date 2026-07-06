package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func chapterWorldDeltaJSON(chapter int) string {
	return filepath.Join("meta", "chapter_world_deltas", fmt.Sprintf("%03d.json", chapter))
}

func chapterWorldDeltaMD(chapter int) string {
	return filepath.Join("meta", "chapter_world_deltas", fmt.Sprintf("%03d.md", chapter))
}

func (s *Store) SaveChapterWorldDelta(delta domain.ChapterWorldDelta) error {
	if delta.Chapter <= 0 {
		return nil
	}
	if delta.Version == 0 {
		delta.Version = 1
	}
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(chapterWorldDeltaJSON(delta.Chapter), delta); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(chapterWorldDeltaMD(delta.Chapter), renderChapterWorldDelta(delta))
}

func (s *Store) LoadChapterWorldDelta(chapter int) (*domain.ChapterWorldDelta, error) {
	if chapter <= 0 {
		return nil, nil
	}
	var delta domain.ChapterWorldDelta
	if err := s.Progress.io.ReadJSON(chapterWorldDeltaJSON(chapter), &delta); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &delta, nil
}

func (s *Store) LoadRecentChapterWorldDeltas(current, window int) ([]domain.ChapterWorldDelta, error) {
	if window <= 0 {
		window = 5
	}
	start := current - window
	if start < 1 {
		start = 1
	}
	var out []domain.ChapterWorldDelta
	var firstErr error
	for ch := start; ch < current; ch++ {
		delta, err := s.LoadChapterWorldDelta(ch)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if delta != nil {
			out = append(out, *delta)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Chapter < out[j].Chapter })
	return out, firstErr
}

func renderChapterWorldDelta(delta domain.ChapterWorldDelta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%03d章 全角色与世界推进汇总\n\n", delta.Chapter)
	if delta.GenerationID != "" {
		fmt.Fprintf(&b, "- generation_id：%s\n", delta.GenerationID)
	}
	fmt.Fprintf(&b, "- rewrite：%t\n", delta.Rewrite)
	if delta.GeneratedAt != "" {
		fmt.Fprintf(&b, "- generated_at：%s\n", delta.GeneratedAt)
	}
	if delta.Summary != "" {
		fmt.Fprintf(&b, "- 摘要：%s\n", delta.Summary)
	}
	b.WriteString("\n## 角色推进\n\n")
	for _, item := range delta.CharacterDeltas {
		fmt.Fprintf(&b, "### %s\n\n", item.Character)
		if item.Location != "" {
			fmt.Fprintf(&b, "- 位置：%s\n", item.Location)
		}
		if item.Status != "" {
			fmt.Fprintf(&b, "- 状态：%s\n", item.Status)
		}
		fmt.Fprintf(&b, "- 正文可见：%t\n", item.VisibleInChapter)
		if item.CurrentAction != "" {
			fmt.Fprintf(&b, "- 行动：%s\n", item.CurrentAction)
		}
		if item.Decision != "" {
			fmt.Fprintf(&b, "- 决策：%s\n", item.Decision)
		}
		if item.MistakeOrMisbelief != "" {
			fmt.Fprintf(&b, "- 误判/错误：%s\n", item.MistakeOrMisbelief)
		}
		if item.KnowledgeBoundary != "" {
			fmt.Fprintf(&b, "- 信息边界：%s\n", item.KnowledgeBoundary)
		}
		if item.PersonalityDelta != "" {
			fmt.Fprintf(&b, "- 性格/判断变化：%s\n", item.PersonalityDelta)
		}
		if item.DeathState != "" {
			fmt.Fprintf(&b, "- 死亡/失踪/异化：%s\n", item.DeathState)
		}
		if item.WorldImpact != "" {
			fmt.Fprintf(&b, "- 对世界推进的影响：%s\n", item.WorldImpact)
		}
		if item.ProtagonistNotice != "" {
			fmt.Fprintf(&b, "- 传回主角：%s\n", item.ProtagonistNotice)
		}
		if item.NextPotential != "" {
			fmt.Fprintf(&b, "- 后续潜势：%s\n", item.NextPotential)
		}
		if item.TimelineConsistency != "" {
			fmt.Fprintf(&b, "- 时间线一致性：%s\n", item.TimelineConsistency)
		}
		b.WriteString("\n")
	}
	if len(delta.WorldDeltas) > 0 {
		b.WriteString("## 世界推进\n\n")
		for _, item := range delta.WorldDeltas {
			label := strings.TrimSpace(item.Kind)
			if item.Entity != "" {
				label += "/" + item.Entity
			}
			if label == "" {
				label = "world"
			}
			fmt.Fprintf(&b, "- **%s**：%s", label, item.Change)
			if item.Evidence != "" {
				fmt.Fprintf(&b, "；证据=%s", item.Evidence)
			}
			fmt.Fprintf(&b, "；主角已知=%t\n", item.VisibleToProtagonist)
		}
	}
	if len(delta.Sources) > 0 {
		fmt.Fprintf(&b, "\n## 来源\n\n- %s\n", strings.Join(delta.Sources, "\n- "))
	}
	return b.String()
}
