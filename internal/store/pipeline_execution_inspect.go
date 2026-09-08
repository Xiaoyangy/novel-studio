package store

import (
	"fmt"
	"os"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// InspectPipelineExecution validates the current lease without creating a
// guard file or removing stale metadata. It is for preflight checks that must
// fail without writes; execution paths still use Load/Acquire to transact.
func (s *RuntimeStore) InspectPipelineExecution() (*domain.PipelineExecutionLock, error) {
	var lock domain.PipelineExecutionLock
	if err := s.io.ReadJSON(pipelineExecutionPath, &lock); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect pipeline execution lock: %w", err)
	}
	if err := validateStoredPipelineExecution(lock); err != nil {
		return nil, err
	}
	if !lock.ActiveAt(time.Now().UTC()) || !pipelineExecutionOwnerProcessAlive(lock) {
		return nil, nil
	}
	return &lock, nil
}
