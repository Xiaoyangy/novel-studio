package store

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

// CanonicalSerialStyleMemoryStopwords returns the exact entity-name set used
// by the serial style-memory compiler. Keeping this derivation in store lets
// both the compiler and the one-shot prose-permit gate read the same
// authoritative character/cast projection without importing one another.
func (s *Store) CanonicalSerialStyleMemoryStopwords() ([]string, error) {
	if s == nil {
		return []string{}, nil
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, err
	}
	cast, err := s.Cast.RecentActive(50)
	if err != nil {
		return nil, err
	}
	bookWorld, err := s.World.LoadBookWorld()
	if err != nil {
		return nil, err
	}
	worldCodex, err := s.LoadWorldCodex()
	if err != nil {
		return nil, err
	}
	words := make([]string, 0, len(characters)*2+len(cast)*2)
	for _, character := range characters {
		words = append(words, character.Name)
		words = append(words, character.Aliases...)
	}
	for _, entry := range cast {
		words = append(words, entry.Name)
		words = append(words, entry.Aliases...)
	}
	if bookWorld != nil {
		words = append(words, bookWorld.Name)
		for _, place := range bookWorld.Places {
			words = append(words, place.Name)
		}
		for _, faction := range bookWorld.Factions {
			words = append(words, faction.Name)
			words = append(words, faction.Aliases...)
		}
	}
	appendWorldCodexStopwords(&words, worldCodex)
	return stylestat.CanonicalStopwords(words), nil
}

// appendWorldCodexStopwords deliberately projects only structured proper-name
// fields. Descriptions, rules, constraints, bindings, samples, and other prose
// must not suppress ordinary phrases from serial style-memory statistics.
func appendWorldCodexStopwords(words *[]string, codex *domain.WorldCodex) {
	if codex == nil {
		return
	}
	*words = append(*words, codex.NovelName)
	for _, tier := range codex.AbilityTiers {
		*words = append(*words, tier.Name)
		*words = append(*words, tier.Aliases...)
	}
	for _, entry := range codex.SkillDomains {
		*words = append(*words, entry.Name)
	}
	for _, race := range codex.Races {
		*words = append(*words, race.Name)
	}
	for _, category := range codex.WeaponCategories {
		*words = append(*words, category.Name)
		*words = append(*words, category.Grades...)
	}
	for _, category := range codex.EquipmentCategories {
		*words = append(*words, category.Name)
		*words = append(*words, category.Grades...)
	}
}
