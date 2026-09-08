package store

import "github.com/chenhongyang/novel-studio/internal/domain"

// A sealed registry belongs to its immutable evidence bundle. In particular,
// copying the outer struct does not detach Entries or the alias slices.
func cloneCharacterAgentRecords(entries []domain.CharacterAgentRecord) []domain.CharacterAgentRecord {
	if entries == nil {
		return nil
	}
	cloned := append([]domain.CharacterAgentRecord(nil), entries...)
	for i := range cloned {
		if entries[i].Aliases != nil {
			cloned[i].Aliases = append([]string{}, entries[i].Aliases...)
		}
	}
	return cloned
}
