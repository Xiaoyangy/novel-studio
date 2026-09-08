package store

import (
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Cross-instance locking prevents two independent receipts from losing one
// another in a load/append/save race. Identical retries do not rewrite files.
func (s *Store) AppendProjectedCharacterMemoryFact(generationID, agentID string, fact domain.CharacterAgentMemoryFact, updatedAt string) error {
	return s.withCharacterActivationWrite(func() error {
		memory, err := s.CharacterAgents.LoadProjectedMemory(generationID, agentID)
		if err != nil {
			return err
		}
		if memory == nil {
			return fmt.Errorf("projected memory does not exist for %s", agentID)
		}
		updated, changed, err := domain.AppendProjectedCharacterMemoryFact(*memory, fact, updatedAt)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return s.CharacterAgents.SaveProjectedMemory(updated)
	})
}
