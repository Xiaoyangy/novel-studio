package store

import (
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// SaveWorldCodex 全量写入 world_codex.json + world_codex.md（原子写入）。
func (s *Store) SaveWorldCodex(codex domain.WorldCodex) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked("world_codex.json", codex); err != nil {
			return err
		}
		return s.Progress.io.WriteFileUnlocked("world_codex.md", []byte(renderWorldCodexMarkdown(codex)))
	})
}

// LoadWorldCodex 读取世界法典；不存在时返回 nil。
func (s *Store) LoadWorldCodex() (*domain.WorldCodex, error) {
	var codex domain.WorldCodex
	if err := s.Progress.io.ReadJSON("world_codex.json", &codex); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &codex, nil
}

// SaveVolumeCodex 写入卷级上限 meta/volume_codex/vNN.json + md。
func (s *Store) SaveVolumeCodex(codex domain.VolumeCodex) error {
	return s.Progress.io.WithWriteLock(func() error {
		if err := s.Progress.io.WriteJSONUnlocked(volumeCodexPath(codex.Volume, "json"), codex); err != nil {
			return err
		}
		return s.Progress.io.WriteFileUnlocked(volumeCodexPath(codex.Volume, "md"), []byte(renderVolumeCodexMarkdown(codex)))
	})
}

// LoadVolumeCodex 读取某卷的上限；不存在时返回 nil。
func (s *Store) LoadVolumeCodex(volume int) (*domain.VolumeCodex, error) {
	var codex domain.VolumeCodex
	if err := s.Progress.io.ReadJSON(volumeCodexPath(volume, "json"), &codex); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &codex, nil
}

func volumeCodexPath(volume int, ext string) string {
	return fmt.Sprintf("meta/volume_codex/v%02d.%s", volume, ext)
}

func renderWorldCodexMarkdown(codex domain.WorldCodex) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 世界法典（v%d）\n\n", codex.Version)
	if codex.NovelName != "" {
		fmt.Fprintf(&b, "- 作品：%s\n", codex.NovelName)
	}
	if codex.GeneratedAt != "" {
		fmt.Fprintf(&b, "- 生成时间：%s\n", codex.GeneratedAt)
	}
	if codex.ImmutabilityPolicy != "" {
		fmt.Fprintf(&b, "- 修订政策：%s\n", codex.ImmutabilityPolicy)
	}
	b.WriteString("\n## 能力分级\n\n")
	for _, tier := range codex.AbilityTiers {
		fmt.Fprintf(&b, "### %d. %s\n\n", tier.Order, tier.Name)
		if len(tier.Aliases) > 0 {
			fmt.Fprintf(&b, "- 别称：%s\n", strings.Join(tier.Aliases, "、"))
		}
		fmt.Fprintf(&b, "- 量级：%s\n- 边界：%s\n- 晋升：%s\n", tier.Magnitude, tier.Limits, tier.Promotion)
		if tier.Cost != "" {
			fmt.Fprintf(&b, "- 代价：%s\n", tier.Cost)
		}
		if tier.Rarity != "" {
			fmt.Fprintf(&b, "- 稀有度：%s\n", tier.Rarity)
		}
		if len(tier.Samples) > 0 {
			fmt.Fprintf(&b, "- 样本：%s\n", strings.Join(tier.Samples, "、"))
		}
		b.WriteString("\n")
	}
	b.WriteString("## 技能范畴\n\n")
	for _, d := range codex.SkillDomains {
		fmt.Fprintf(&b, "- **%s**：%s", d.Name, d.Description)
		if d.TierBinding != "" {
			fmt.Fprintf(&b, "（分级绑定：%s）", d.TierBinding)
		}
		b.WriteString("\n")
		for _, c := range d.Constraints {
			fmt.Fprintf(&b, "  - 约束：%s\n", c)
		}
	}
	b.WriteString("\n## 种族设定\n\n")
	for _, r := range codex.Races {
		fmt.Fprintf(&b, "- **%s**：%s", r.Name, r.Description)
		if r.Habitat != "" {
			fmt.Fprintf(&b, "（分布：%s）", r.Habitat)
		}
		b.WriteString("\n")
		if len(r.Traits) > 0 {
			fmt.Fprintf(&b, "  - 特质：%s\n", strings.Join(r.Traits, "；"))
		}
		if r.Relations != "" {
			fmt.Fprintf(&b, "  - 关系：%s\n", r.Relations)
		}
		for _, c := range r.Constraints {
			fmt.Fprintf(&b, "  - 约束：%s\n", c)
		}
	}
	writeGraded := func(title string, list []domain.CodexGradedCategory) {
		fmt.Fprintf(&b, "\n## %s\n\n", title)
		for _, g := range list {
			fmt.Fprintf(&b, "- **%s**：%s", g.Name, g.Description)
			if len(g.Grades) > 0 {
				fmt.Fprintf(&b, "（品级：%s）", strings.Join(g.Grades, " → "))
			}
			if g.TierBinding != "" {
				fmt.Fprintf(&b, "（使用门槛：%s）", g.TierBinding)
			}
			b.WriteString("\n")
			for _, c := range g.Constraints {
				fmt.Fprintf(&b, "  - 约束：%s\n", c)
			}
		}
	}
	writeGraded("武器范畴", codex.WeaponCategories)
	writeGraded("装备范畴", codex.EquipmentCategories)

	b.WriteString("\n## 世界维度\n")
	for _, sec := range codex.Sections {
		title := sec.Title
		if title == "" {
			title = sec.Key
		}
		fmt.Fprintf(&b, "\n### %s\n\n", title)
		if sec.NotApplicable {
			fmt.Fprintf(&b, "- 本题材不适用：%s\n", sec.Reason)
			continue
		}
		if sec.Content != "" {
			b.WriteString(sec.Content)
			b.WriteString("\n")
		}
		for _, rule := range sec.Rules {
			fmt.Fprintf(&b, "- 规则：%s\n", rule)
		}
	}
	if len(codex.ChangeLog) > 0 {
		b.WriteString("\n## 修订记录\n\n")
		for _, ch := range codex.ChangeLog {
			fmt.Fprintf(&b, "- v%d @ %s：%s（依据：%s；改动：%s）\n",
				ch.Version, ch.At, ch.Reason, ch.Evidence, strings.Join(ch.Fields, "、"))
		}
	}
	return b.String()
}

func renderVolumeCodexMarkdown(codex domain.VolumeCodex) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 第%d卷 上限法典\n\n", codex.Volume)
	if codex.VolumeTitle != "" {
		fmt.Fprintf(&b, "- 卷名：%s\n", codex.VolumeTitle)
	}
	fmt.Fprintf(&b, "- 世界侧能力上限：%s\n- 主角上限：%s\n", codex.TierCeiling, codex.ProtagonistCeiling)
	if len(codex.AllowedSkillDomains) > 0 {
		fmt.Fprintf(&b, "- 可出现技能门类：%s\n", strings.Join(codex.AllowedSkillDomains, "、"))
	}
	if codex.WeaponGradeCeiling != "" {
		fmt.Fprintf(&b, "- 武器品级上限：%s\n", codex.WeaponGradeCeiling)
	}
	if codex.EquipGradeCeiling != "" {
		fmt.Fprintf(&b, "- 装备品级上限：%s\n", codex.EquipGradeCeiling)
	}
	if len(codex.NewRaces) > 0 {
		fmt.Fprintf(&b, "- 本卷新登场种族：%s\n", strings.Join(codex.NewRaces, "、"))
	}
	if len(codex.NewMechanisms) > 0 {
		fmt.Fprintf(&b, "- 本卷解锁机制：%s\n", strings.Join(codex.NewMechanisms, "、"))
	}
	if len(codex.ForbiddenInVolume) > 0 {
		fmt.Fprintf(&b, "- 本卷禁止出现：%s\n", strings.Join(codex.ForbiddenInVolume, "、"))
	}
	if codex.Evidence != "" {
		fmt.Fprintf(&b, "- 依据：%s\n", codex.Evidence)
	}
	if codex.GeneratedAt != "" {
		fmt.Fprintf(&b, "- 生成时间：%s\n", codex.GeneratedAt)
	}
	return b.String()
}
