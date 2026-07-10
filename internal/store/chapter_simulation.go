package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func chapterSimulationJSON(chapter int) string {
	return filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.json", chapter))
}

func chapterSimulationMD(chapter int) string {
	return filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.md", chapter))
}

func chapterSimulationPartialJSON(chapter int) string {
	return filepath.Join("meta", "chapter_simulations", fmt.Sprintf("%03d.partial.json", chapter))
}

func (s *Store) SaveChapterWorldSimulation(sim domain.ChapterWorldSimulation) error {
	if sim.Chapter <= 0 {
		return fmt.Errorf("chapter simulation chapter must be > 0")
	}
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(chapterSimulationJSON(sim.Chapter), sim); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(chapterSimulationMD(sim.Chapter), renderChapterWorldSimulation(sim))
}

func (s *Store) LoadChapterWorldSimulation(chapter int) (*domain.ChapterWorldSimulation, error) {
	var sim domain.ChapterWorldSimulation
	if err := s.Progress.io.ReadJSON(chapterSimulationJSON(chapter), &sim); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sim, nil
}

func (s *Store) SaveChapterWorldSimulationPartial(sim domain.ChapterWorldSimulation) error {
	if sim.Chapter <= 0 {
		return fmt.Errorf("chapter simulation chapter must be > 0")
	}
	return s.Progress.io.WriteJSON(chapterSimulationPartialJSON(sim.Chapter), sim)
}

func (s *Store) LoadChapterWorldSimulationPartial(chapter int) (*domain.ChapterWorldSimulation, error) {
	var sim domain.ChapterWorldSimulation
	if err := s.Progress.io.ReadJSON(chapterSimulationPartialJSON(chapter), &sim); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sim, nil
}

func (s *Store) DeleteChapterWorldSimulationPartial(chapter int) error {
	return s.Progress.io.RemoveFile(chapterSimulationPartialJSON(chapter))
}

func renderChapterWorldSimulation(sim domain.ChapterWorldSimulation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%03d章 全角色世界推演\n\n", sim.Chapter)
	fmt.Fprintf(&b, "- 模拟 ID：%s\n- 基础 tick：%s\n- 时间窗口：%s\n\n", sim.SimulationID, sim.BaseTickID, sim.TimeWindow)
	for _, decision := range sim.CharacterDecisions {
		fmt.Fprintf(&b, "## %s\n\n", decision.Character)
		fmt.Fprintf(&b, "- 地点：%s\n- 目标：%s\n- 压力：%s\n- 决策：%s\n- 理由：%s\n- 行动：%s\n- 现实耗时：%s\n- 完成度：%s\n- 结果：%s\n", decision.Location, decision.CurrentGoal, decision.Pressure, decision.Decision, decision.DecisionReason, decision.Action, decision.ActionDuration, decision.CompletionState, decision.ImmediateResult)
		for _, effect := range decision.ButterflyEffects {
			fmt.Fprintf(&b, "- 蝴蝶效应：%s；路径=%s；抵达章=%d；对主角=%s\n", effect.Effect, effect.TransmissionPath, effect.ArrivalChapter, effect.ProtagonistImpact)
		}
		b.WriteString("\n")
	}
	p := sim.ProtagonistProjection
	b.WriteString("## 主视角投影\n\n")
	fmt.Fprintf(&b, "- 主角：%s\n- 选择：%s\n- 理由：%s\n", p.Protagonist, p.ChosenDecision, p.DecisionReason)
	if len(p.ObservableEffects) > 0 {
		fmt.Fprintf(&b, "- 可见影响：%s\n", strings.Join(p.ObservableEffects, "；"))
	}
	if len(p.HiddenPressures) > 0 {
		fmt.Fprintf(&b, "- 隐藏压力：%s\n", strings.Join(p.HiddenPressures, "；"))
	}
	if len(p.CausalChain) > 0 {
		fmt.Fprintf(&b, "- 因果链：%s\n", strings.Join(p.CausalChain, " -> "))
	}
	return b.String()
}
