package store

import (
	"crypto/subtle"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const planningContextAccessReceiptDir = "meta/runtime/planning_context_access"

func planningContextAccessReceiptPath(
	phase domain.PlanningContextAccessPhase,
) (string, error) {
	switch phase {
	case domain.PlanningContextAccessSimulate, domain.PlanningContextAccessPlan:
		return filepath.Join(planningContextAccessReceiptDir, string(phase)+".json"), nil
	default:
		return "", fmt.Errorf("unsupported planning context access phase")
	}
}

func (s *RuntimeStore) SavePlanningContextAccessReceipt(
	receipt domain.PlanningContextAccessReceipt,
) error {
	if err := domain.ValidatePlanningContextAccessReceipt(receipt); err != nil {
		return err
	}
	if !receipt.ConsumedAt.IsZero() {
		return fmt.Errorf("cannot issue an already consumed planning context access receipt")
	}
	path, err := planningContextAccessReceiptPath(receipt.Phase)
	if err != nil {
		return err
	}
	return s.io.WriteJSON(path, receipt)
}

func (s *RuntimeStore) LoadPlanningContextAccessReceipt(
	phase domain.PlanningContextAccessPhase,
) (*domain.PlanningContextAccessReceipt, error) {
	path, err := planningContextAccessReceiptPath(phase)
	if err != nil {
		return nil, err
	}
	var receipt domain.PlanningContextAccessReceipt
	if err := s.io.ReadJSON(path, &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidatePlanningContextAccessReceipt(receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// ConsumePlanningContextAccessReceipt atomically marks the exact loaded
// receipt as spent. The raw source token is never persisted and is never
// interpolated into an error.
func (s *RuntimeStore) ConsumePlanningContextAccessReceipt(
	expected domain.PlanningContextAccessReceipt,
	sourceToken string,
	consumedAt time.Time,
) error {
	if err := domain.ValidatePlanningContextAccessReceipt(expected); err != nil {
		return err
	}
	path, err := planningContextAccessReceiptPath(expected.Phase)
	if err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		var current domain.PlanningContextAccessReceipt
		if err := s.io.ReadJSONUnlocked(path, &current); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("planning context access receipt is missing")
			}
			return err
		}
		if err := domain.ValidatePlanningContextAccessReceipt(current); err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(
			[]byte(current.ReceiptDigest),
			[]byte(expected.ReceiptDigest),
		) != 1 {
			return fmt.Errorf("planning context access receipt changed before consumption")
		}
		if !current.ConsumedAt.IsZero() {
			return fmt.Errorf("planning context access receipt was already consumed")
		}
		consumedAt = consumedAt.UTC()
		if consumedAt.Before(current.IssuedAt) || !consumedAt.Before(current.ExpiresAt) {
			return fmt.Errorf("planning context access receipt is expired")
		}
		tokenSHA, hashErr := domain.PlanningContextAccessTokenSHA256(sourceToken)
		if hashErr != nil || subtle.ConstantTimeCompare(
			[]byte(tokenSHA),
			[]byte(current.TokenSHA256),
		) != 1 {
			return fmt.Errorf("planning context access receipt token does not match")
		}
		current.ConsumedAt = consumedAt
		return s.io.WriteJSONUnlocked(path, current)
	})
}
