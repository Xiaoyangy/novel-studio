package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	worldFoundationJSON = "meta/world_foundation.json"
	worldFoundationMD   = "meta/world_foundation.md"
)

func (s *Store) SaveWorldFoundation(f domain.WorldFoundation) error {
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(worldFoundationJSON, f); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(worldFoundationMD, renderWorldFoundation(f))
}

func (s *Store) LoadWorldFoundation() (*domain.WorldFoundation, error) {
	var f domain.WorldFoundation
	if err := s.Progress.io.ReadJSON(worldFoundationJSON, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func characterDossierDir(name string) string {
	return filepath.Join("meta", "characters", safeDossierName(name))
}

func characterDossierJSON(name string) string {
	return filepath.Join(characterDossierDir(name), "dossier.json")
}

func characterDossierMD(name string) string {
	return filepath.Join(characterDossierDir(name), "dossier.md")
}

func (s *Store) SaveCharacterDossier(d domain.CharacterDossier) error {
	d.Character = strings.TrimSpace(d.Character)
	if d.Character == "" {
		return nil
	}
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()
	if err := s.Progress.io.WriteJSONUnlocked(characterDossierJSON(d.Character), d); err != nil {
		return err
	}
	return s.Progress.io.WriteMarkdownUnlocked(characterDossierMD(d.Character), renderCharacterDossier(d))
}

func (s *Store) LoadCharacterDossier(name string) (*domain.CharacterDossier, error) {
	var d domain.CharacterDossier
	if err := s.Progress.io.ReadJSON(characterDossierJSON(name), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) LoadAllCharacterDossiers() ([]domain.CharacterDossier, error) {
	root := s.Progress.io.path(filepath.Join("meta", "characters"))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []domain.CharacterDossier
	var firstErr error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rel := filepath.Join("meta", "characters", entry.Name(), "dossier.json")
		var d domain.CharacterDossier
		if err := s.Progress.io.ReadJSON(rel, &d); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if strings.TrimSpace(d.Character) != "" {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Character < out[j].Character
	})
	return out, firstErr
}

func renderWorldFoundation(f domain.WorldFoundation) string {
	var b strings.Builder
	b.WriteString("# 世界基础规则与开局时间线\n\n")
	if f.Project != "" {
		fmt.Fprintf(&b, "- 项目：%s\n", f.Project)
	}
	if f.GeneratedAt != "" {
		fmt.Fprintf(&b, "- 生成时间：%s\n", f.GeneratedAt)
	}
	b.WriteString("- 用途：正文开始前已经成立的世界铁律、开局时间、过去时间线和地点基线。角色未获得改变规则的能力/凭证前，后续推演必须服从这里的边界。\n\n")
	b.WriteString("## 故事开始\n\n")
	if f.StoryStart.AbsoluteTime != "" {
		fmt.Fprintf(&b, "- 绝对时间：%s\n", f.StoryStart.AbsoluteTime)
	}
	if f.StoryStart.StoryClock != "" {
		fmt.Fprintf(&b, "- 故事时钟：%s\n", f.StoryStart.StoryClock)
	}
	if f.StoryStart.Location != "" {
		fmt.Fprintf(&b, "- 起点位置：%s\n", f.StoryStart.Location)
	}
	if f.StoryStart.Description != "" {
		fmt.Fprintf(&b, "- 起点说明：%s\n", f.StoryStart.Description)
	}
	if f.KnowledgePolicy != "" {
		fmt.Fprintf(&b, "- 信息边界：%s\n", f.KnowledgePolicy)
	}
	b.WriteString("\n## 世界铁律\n\n")
	for _, law := range f.IronLaws {
		fmt.Fprintf(&b, "### %s\n\n", law.Name)
		if law.ID != "" {
			fmt.Fprintf(&b, "- ID：%s\n", law.ID)
		}
		fmt.Fprintf(&b, "- 规则：%s\n", law.Rule)
		fmt.Fprintf(&b, "- 边界：%s\n", law.Boundary)
		if law.Evidence != "" {
			fmt.Fprintf(&b, "- 证据：%s\n", law.Evidence)
		}
		if len(law.AppliesTo) > 0 {
			fmt.Fprintf(&b, "- 适用对象：%s\n", strings.Join(law.AppliesTo, "、"))
		}
		b.WriteString("\n")
	}
	if len(f.RuleChangeConditions) > 0 {
		b.WriteString("## 规则改变条件\n\n")
		for _, item := range f.RuleChangeConditions {
			fmt.Fprintf(&b, "- `%s`：允许条件=%s；所需证据=%s；更新目标=%s\n",
				item.RuleID, strings.Join(item.AllowedBy, "、"), item.ProofNeeded, strings.Join(item.UpdateTargets, "、"))
		}
		b.WriteString("\n")
	}
	if len(f.PastTimeline) > 0 {
		b.WriteString("## 故事开始前时间线\n\n")
		for _, ev := range f.PastTimeline {
			fmt.Fprintf(&b, "### %s\n\n", ev.Time)
			fmt.Fprintf(&b, "- 事件：%s\n", ev.Event)
			if len(ev.Locations) > 0 {
				fmt.Fprintf(&b, "- 地点：%s\n", strings.Join(ev.Locations, "、"))
			}
			if len(ev.Participants) > 0 {
				fmt.Fprintf(&b, "- 参与者：%s\n", strings.Join(ev.Participants, "、"))
			}
			if len(ev.Consequences) > 0 {
				fmt.Fprintf(&b, "- 后果：%s\n", strings.Join(ev.Consequences, "；"))
			}
			fmt.Fprintf(&b, "- 主角是否已知：%t\n", ev.ProtagonistKnows)
			if ev.Source != "" {
				fmt.Fprintf(&b, "- 来源：%s\n", ev.Source)
			}
			b.WriteString("\n")
		}
	}
	if len(f.CityBaseline) > 0 {
		b.WriteString("## 地点开局基线\n\n")
		for _, loc := range f.CityBaseline {
			fmt.Fprintf(&b, "### %s\n\n", loc.Name)
			if loc.ID != "" {
				fmt.Fprintf(&b, "- ID：%s\n", loc.ID)
			}
			if loc.StatusAtStart != "" {
				fmt.Fprintf(&b, "- 开局状态：%s\n", loc.StatusAtStart)
			}
			if len(loc.OpenQuestions) > 0 {
				fmt.Fprintf(&b, "- 未解问题：%s\n", strings.Join(loc.OpenQuestions, "；"))
			}
			if loc.UpdatePolicy != "" {
				fmt.Fprintf(&b, "- 更新规则：%s\n", loc.UpdatePolicy)
			}
			b.WriteString("\n")
		}
	}
	if len(f.Sources) > 0 {
		fmt.Fprintf(&b, "## 来源\n\n- %s\n", strings.Join(f.Sources, "\n- "))
	}
	return b.String()
}

func renderCharacterDossier(d domain.CharacterDossier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 角色独立档案\n\n", d.Character)
	if d.Role != "" {
		fmt.Fprintf(&b, "- 定位：%s\n", d.Role)
	}
	if d.Tier != "" {
		fmt.Fprintf(&b, "- 层级：%s\n", d.Tier)
	}
	if len(d.Aliases) > 0 {
		fmt.Fprintf(&b, "- 别名：%s\n", strings.Join(d.Aliases, "、"))
	}
	if d.GeneratedAt != "" {
		fmt.Fprintf(&b, "- 生成时间：%s\n", d.GeneratedAt)
	}
	b.WriteString("- 用途：独立存储该角色在主角视角之外的生活、工作、过去经历、资源、通信边界和关系来源，支持 RAG 在任意章节推演该角色自己的时间线。\n\n")
	b.WriteString("## 基础信息\n\n")
	if d.Profile.Description != "" {
		fmt.Fprintf(&b, "- 描述：%s\n", d.Profile.Description)
	}
	if d.Profile.Backstory != "" {
		fmt.Fprintf(&b, "- 背景：%s\n", d.Profile.Backstory)
	}
	if len(d.Profile.Traits) > 0 {
		fmt.Fprintf(&b, "- 性格/习惯：%s\n", strings.Join(d.Profile.Traits, "、"))
	}
	if d.Profile.Arc != "" {
		fmt.Fprintf(&b, "- 长期弧线：%s\n", d.Profile.Arc)
	}
	if len(d.Profile.Desires) > 0 {
		fmt.Fprintf(&b, "- 欲望：%s\n", strings.Join(d.Profile.Desires, "、"))
	}
	if len(d.Profile.Fears) > 0 {
		fmt.Fprintf(&b, "- 恐惧：%s\n", strings.Join(d.Profile.Fears, "、"))
	}
	if len(d.Profile.Boundaries) > 0 {
		fmt.Fprintf(&b, "- 底线：%s\n", strings.Join(d.Profile.Boundaries, "、"))
	}
	b.WriteString("\n## 生活与工作锚点\n\n")
	for _, anchor := range d.LifeAnchors {
		fmt.Fprintf(&b, "- %s：%s", anchor.Kind, anchor.Place)
		if anchor.Schedule != "" {
			fmt.Fprintf(&b, "；时间=%s", anchor.Schedule)
		}
		if anchor.Obligation != "" {
			fmt.Fprintf(&b, "；义务=%s", anchor.Obligation)
		}
		if anchor.TravelNotes != "" {
			fmt.Fprintf(&b, "；交通=%s", anchor.TravelNotes)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## 故事开始前经历\n\n")
	for _, ev := range d.PreStoryTimeline {
		fmt.Fprintf(&b, "- %s：%s", ev.Time, ev.Event)
		if ev.Location != "" {
			fmt.Fprintf(&b, "（%s）", ev.Location)
		}
		if len(ev.PeopleMet) > 0 {
			fmt.Fprintf(&b, "；结识=%s", strings.Join(ev.PeopleMet, "、"))
		}
		if ev.Relationship != "" {
			fmt.Fprintf(&b, "；关系=%s", ev.Relationship)
		}
		if ev.Consequence != "" {
			fmt.Fprintf(&b, "；后果=%s", ev.Consequence)
		}
		fmt.Fprintf(&b, "；主角已知=%t\n", ev.KnownToProtagonist)
	}
	b.WriteString("\n## 资源\n\n")
	for _, r := range d.Resources {
		fmt.Fprintf(&b, "- `%s` %s：kind=%s；status=%s；risk=%s；evidence=%s\n", r.ID, r.Name, r.Kind, r.Status, r.Risk, r.Evidence)
	}
	b.WriteString("\n## 关系与相识来源\n\n")
	for _, rel := range d.Relationships {
		fmt.Fprintf(&b, "- %s：相识=%s；当前关系=%s；债/信任=%s；主角已知=%t\n", rel.Other, rel.HowMet, rel.CurrentTie, rel.DebtOrTrust, rel.KnownToProtagonist)
	}
	b.WriteString("\n## 通信与信息边界\n\n")
	fmt.Fprintf(&b, "- 能否主动联系主角：%t\n", d.CommunicationBoundary.CanContactProtagonist)
	if len(d.CommunicationBoundary.Channels) > 0 {
		fmt.Fprintf(&b, "- 渠道：%s\n", strings.Join(d.CommunicationBoundary.Channels, "、"))
	}
	if d.CommunicationBoundary.Delay != "" {
		fmt.Fprintf(&b, "- 延迟：%s\n", d.CommunicationBoundary.Delay)
	}
	if len(d.CommunicationBoundary.FailureModes) > 0 {
		fmt.Fprintf(&b, "- 失败方式：%s\n", strings.Join(d.CommunicationBoundary.FailureModes, "、"))
	}
	if d.CommunicationBoundary.InfoAllowed != "" {
		fmt.Fprintf(&b, "- 可传递信息：%s\n", d.CommunicationBoundary.InfoAllowed)
	}
	if d.KnowledgeBoundary != "" {
		fmt.Fprintf(&b, "- 知识边界：%s\n", d.KnowledgeBoundary)
	}
	if d.DecisionModel != "" {
		fmt.Fprintf(&b, "- 决策模型：%s\n", d.DecisionModel)
	}
	b.WriteString("\n## 故事开始时状态\n\n")
	if d.CurrentAtStoryStart.Time != "" {
		fmt.Fprintf(&b, "- 时间：%s\n", d.CurrentAtStoryStart.Time)
	}
	if d.CurrentAtStoryStart.Location != "" {
		fmt.Fprintf(&b, "- 位置：%s\n", d.CurrentAtStoryStart.Location)
	}
	if d.CurrentAtStoryStart.Status != "" {
		fmt.Fprintf(&b, "- 状态：%s\n", d.CurrentAtStoryStart.Status)
	}
	if d.CurrentAtStoryStart.CurrentAction != "" {
		fmt.Fprintf(&b, "- 当前行动：%s\n", d.CurrentAtStoryStart.CurrentAction)
	}
	if d.CurrentAtStoryStart.Pressure != "" {
		fmt.Fprintf(&b, "- 压力：%s\n", d.CurrentAtStoryStart.Pressure)
	}
	if d.CurrentAtStoryStart.NextIndependentMove != "" {
		fmt.Fprintf(&b, "- 下一步独立行动：%s\n", d.CurrentAtStoryStart.NextIndependentMove)
	}
	if len(d.RAGHints) > 0 {
		fmt.Fprintf(&b, "\n## RAG 提示\n\n- %s\n", strings.Join(d.RAGHints, "\n- "))
	}
	if len(d.Sources) > 0 {
		fmt.Fprintf(&b, "\n## 来源\n\n- %s\n", strings.Join(d.Sources, "\n- "))
	}
	return b.String()
}

func safeDossierName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if name == "" {
		return "unknown"
	}
	return name
}
