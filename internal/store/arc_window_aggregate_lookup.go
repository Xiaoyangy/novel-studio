package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// LoadVerifiedWindowedArcCompletionForGenerationV1 is a read-only recovery
// lookup. Absence returns nil; ambiguity is an error, never a newest-wins rule.
func (s *Store) LoadVerifiedWindowedArcCompletionForGenerationV1(finalGenerationID string) (*domain.ArcWindowAggregateCompletionV1, error) {
	if err := validateArcCycleGenerationID(finalGenerationID); err != nil {
		return nil, err
	}
	arc := s.ArcCycle()
	lookup := func() (string, error) {
		return withArcCycleReadResult(arc, func() (string, error) {
			dir := filepath.Join(arcWindowAggregateDirV1, finalGenerationID)
			entries, err := os.ReadDir(arc.io.path(dir))
			if os.IsNotExist(err) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			if len(entries) == 0 {
				return "", nil
			}
			if len(entries) != 1 {
				return "", fmt.Errorf("generation %s requires exactly one window aggregate completion, got %d", finalGenerationID, len(entries))
			}
			if err := validateArcCycleRegularJSONEntry(entries[0], dir); err != nil {
				return "", err
			}
			digest := strings.TrimSuffix(entries[0].Name(), ".json")
			return digest, validateArcCycleDigest("aggregate_digest", digest)
		})
	}
	digest, err := lookup()
	if err != nil || digest == "" {
		return nil, err
	}
	// Release ArcCycle before the verified loader acquires Projected -> ArcCycle.
	receipt, err := s.LoadVerifiedWindowedArcCompletionV1(finalGenerationID, digest)
	if err != nil {
		return nil, err
	}
	confirmed, err := lookup()
	if err != nil {
		return nil, err
	}
	if confirmed != digest {
		return nil, fmt.Errorf("window aggregate completion changed during recovery lookup")
	}
	return receipt, nil
}
