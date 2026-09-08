package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func planGroundingAuditPath(inputDigest string) (string, error) {
	if !characterAgentDigestPattern.MatchString(inputDigest) {
		return "", fmt.Errorf("invalid grounding input digest")
	}
	return filepath.Join("meta", "planning", "grounding", strings.TrimPrefix(inputDigest, "sha256:")+".json"), nil
}

func (s *Store) SavePlanGroundingAudit(audit domain.PlanGroundingAudit) error {
	if err := domain.ValidatePlanGroundingAudit(audit); err != nil {
		return err
	}
	path, err := planGroundingAuditPath(audit.Receipt.InputDigest)
	if err != nil {
		return err
	}
	return s.CharacterAgents.writeImmutable(path, audit)
}

func (s *Store) LoadPlanGroundingAudit(inputDigest string) (*domain.PlanGroundingAudit, error) {
	path, err := planGroundingAuditPath(inputDigest)
	if err != nil {
		return nil, err
	}
	var audit domain.PlanGroundingAudit
	if err := s.CharacterAgents.io.ReadJSON(path, &audit); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if audit.Receipt.InputDigest != inputDigest {
		return nil, fmt.Errorf("grounding audit input path binding mismatch")
	}
	if err := domain.ValidatePlanGroundingAudit(audit); err != nil {
		return nil, err
	}
	return &audit, nil
}
