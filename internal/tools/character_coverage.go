package tools

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// requiredDossierCharacterNames 是全角色模拟和提交回填的覆盖名单。
//
// 单世界模型要求 characters.json 中每个实名角色都持续做自己的选择；dossier
// 中尚未同步回角色册的人也不能静默丢失。chapter 参数保留给现有调用签名。
func requiredDossierCharacterNames(s *store.Store, chapter int) []string {
	_ = chapter
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
	if chars, err := s.Characters.Load(); err == nil {
		for _, character := range chars {
			add(character.Name)
		}
	}
	if dossiers, err := s.LoadAllCharacterDossiers(); err == nil {
		for _, dossier := range dossiers {
			add(dossier.Character)
		}
	}
	return names
}

// chapterOutlineCharacterNames 是主视角正文的可见角色投影，不参与削减世界模拟。
func chapterOutlineCharacterNames(s *store.Store, chapter int) []string {
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
	add(inferCommitProtagonist(s))
	chars, err := s.Characters.Load()
	if err != nil {
		return names
	}
	entry, err := s.Outline.GetChapterOutline(chapter)
	if err != nil || entry == nil {
		return names
	}
	outlineText := entry.Title + "\n" + entry.CoreEvent + "\n" + entry.Hook + "\n" + strings.Join(entry.Scenes, "\n")
	mentioned := map[string]bool{}
	for _, name := range matchOutlineCharacters(outlineText, chars) {
		mentioned[strings.TrimSpace(name)] = true
	}
	for _, character := range chars {
		name := strings.TrimSpace(character.Name)
		if mentioned[name] || outlineRoleAuthorizesCharacter(outlineText, strings.TrimSpace(character.Role)) {
			add(name)
		}
	}
	if source, body, _, loadErr := loadChapterRewriteSource(s, chapter); loadErr == nil && source != nil {
		for _, name := range rewriteVisibleCharacterNames(s, body, source.PreserveFacts) {
			add(name)
		}
	}
	return names
}

func rewriteVisibleCharacterNames(s *store.Store, body string, preserveFacts []string) []string {
	chars, err := s.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	text := body + "\n" + strings.Join(preserveFacts, "\n")
	return compactStrings(matchOutlineCharacters(text, chars))
}
func outlineRoleAuthorizesCharacter(outlineText, role string) bool {
	switch {
	case (strings.Contains(outlineText, "父母") || strings.Contains(outlineText, "爸妈")) &&
		(strings.Contains(role, "父亲") || strings.Contains(role, "母亲")):
		return true
	case (strings.Contains(outlineText, "父亲") || strings.Contains(outlineText, "爸爸")) && strings.Contains(role, "父亲"):
		return true
	case (strings.Contains(outlineText, "母亲") || strings.Contains(outlineText, "妈妈")) && strings.Contains(role, "母亲"):
		return true
	case strings.Contains(outlineText, "女主") && strings.Contains(role, "女主"):
		return true
	case (strings.Contains(outlineText, "朋友") || strings.Contains(outlineText, "兄弟") || strings.Contains(outlineText, "闺蜜")) &&
		(strings.Contains(role, "朋友") || strings.Contains(role, "主角团") || strings.Contains(role, "闺蜜")):
		return true
	case strings.Contains(outlineText, "亲戚") && strings.Contains(role, "亲戚"):
		return true
	case strings.Contains(outlineText, "反派") && strings.Contains(role, "反派"):
		return true
	default:
		return false
	}
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
