package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// requiredDossierCharacterNames 计算章级覆盖门禁（offscreen stage / arc tests /
// emotional logic 等）必须覆盖的角色集合（Task 055）：
//
//	核心主线角色 ∪ 已出场角色 ∪ 本章大纲点名角色
//
// dossier 是可检索资产，不等于每章硬覆盖名单；未出场且未点名的角色不强制，
// 避免逼 agent 为没上场的人编全套心理/视觉/关系矩阵。
func requiredDossierCharacterNames(s *store.Store, chapter int) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	chars, err := s.Characters.Load()
	if err != nil {
		chars = nil
	}

	// 已出场：continuity 台账的 first_seen_chapter 有值即视为出过场。
	appeared := map[string]bool{}
	if ledger, err := s.LoadCharacterContinuityLedger(); err == nil && ledger != nil {
		for _, entry := range ledger.Entries {
			if entry.FirstSeenChapter > 0 {
				appeared[strings.TrimSpace(entry.Name)] = true
			}
		}
	}
	// 本章点名：当前章 outline 文本与角色名/别名匹配。
	mentioned := map[string]bool{}
	if entry, err := s.Outline.GetChapterOutline(chapter); err == nil && entry != nil {
		text := entry.Title + " " + entry.CoreEvent + " " + strings.Join(entry.Scenes, " ")
		for _, name := range matchOutlineCharacters(text, chars) {
			mentioned[strings.TrimSpace(name)] = true
		}
	}

	charByName := make(map[string]domain.Character, len(chars))
	for _, c := range chars {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			continue
		}
		charByName[name] = c
		if characterRequiresChapterCoverage(c) || appeared[name] || mentioned[name] {
			add(name)
		}
	}
	if dossiers, err := s.LoadAllCharacterDossiers(); err == nil {
		for _, dossier := range dossiers {
			name := strings.TrimSpace(dossier.Character)
			if name == "" {
				continue
			}
			if c, ok := charByName[name]; ok {
				if characterRequiresChapterCoverage(c) || appeared[name] || mentioned[name] {
					add(name)
				}
				continue
			}
			if dossierRequiresChapterCoverage(dossier) || appeared[name] || mentioned[name] {
				add(name)
			}
		}
	}
	return names
}

func characterRequiresChapterCoverage(c domain.Character) bool {
	if strings.TrimSpace(c.Tier) == "core" {
		return true
	}
	return roleRequiresChapterCoverage(c.Role)
}

func dossierRequiresChapterCoverage(d domain.CharacterDossier) bool {
	if strings.TrimSpace(d.Tier) == "core" {
		return true
	}
	return roleRequiresChapterCoverage(d.Role)
}

func roleRequiresChapterCoverage(role string) bool {
	role = strings.TrimSpace(role)
	if role == "" || strings.Contains(role, "主角团") {
		return false
	}
	return role == "主角" || role == "男主" || role == "女主" ||
		strings.Contains(role, "男主") || strings.Contains(role, "女主")
}

func missingStageCoverage(required []string, records []domain.CharacterStageRecord) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		name := strings.TrimSpace(record.Character)
		if name != "" {
			present[name] = struct{}{}
		}
	}
	var missing []string
	for _, name := range required {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func missingInitialStateCoverage(required []string, records []domain.CharacterSimulationState) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		name := strings.TrimSpace(record.Character)
		if name != "" {
			present[name] = struct{}{}
		}
	}
	return missingNames(required, present)
}

func missingArcTestCoverage(required []string, records []domain.CharacterArcTest) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		name := strings.TrimSpace(record.Character)
		if name != "" {
			present[name] = struct{}{}
		}
	}
	return missingNames(required, present)
}

func missingEmotionalLogicCoverage(required []string, records []domain.CharacterEmotionalLogic) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		name := strings.TrimSpace(record.Character)
		if name != "" {
			present[name] = struct{}{}
		}
	}
	return missingNames(required, present)
}

func missingVisualDesignCoverage(required []string, records []domain.CharacterVisualDesign) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		name := strings.TrimSpace(record.Character)
		if name != "" {
			present[name] = struct{}{}
		}
	}
	return missingNames(required, present)
}

func missingRelationshipArcCoverage(required []string, records []domain.RelationshipEmotionArc) []string {
	if len(required) == 0 {
		return nil
	}
	present := map[string]struct{}{}
	for _, record := range records {
		for _, name := range record.Pair {
			name = strings.TrimSpace(name)
			if name != "" && name != "none" {
				present[name] = struct{}{}
			}
		}
	}
	return missingNames(required, present)
}

func missingNames(required []string, present map[string]struct{}) []string {
	var missing []string
	for _, name := range required {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func formatMissingCharacterCoverage(field string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("%s missing characters: %s", field, strings.Join(names, "、"))
}
