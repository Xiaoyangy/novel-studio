package store

import (
	"os"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const writingPipelineModePath = "meta/planning/writing_mode.json"

func (s *Store) SaveWritingPipelineMode(
	receipt domain.WritingPipelineModeReceipt,
) error {
	if err := domain.ValidateWritingPipelineModeReceipt(receipt); err != nil {
		return err
	}
	return s.Progress.io.WriteJSON(writingPipelineModePath, receipt)
}

func (s *Store) LoadWritingPipelineMode() (*domain.WritingPipelineModeReceipt, error) {
	var receipt domain.WritingPipelineModeReceipt
	if err := s.Progress.io.ReadJSON(writingPipelineModePath, &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateWritingPipelineModeReceipt(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}
