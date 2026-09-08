package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// Durable Host publication intent survives replacement of the ephemeral access
// receipt on resume. It carries no model reasoning or mutable projected state.
type CharacterActivationPublication struct {
	Version        string                              `json:"version"`
	GenerationID   string                              `json:"generation_id"`
	Chapter        int                                 `json:"chapter"`
	EvidenceDigest string                              `json:"evidence_digest"`
	SimulationID   string                              `json:"simulation_id"`
	BaseTickID     string                              `json:"base_tick_id,omitempty"`
	Sources        []string                            `json:"sources"`
	Access         domain.PlanningContextAccessReceipt `json:"consumed_access"`
}

func (s *Store) validateCharacterActivationPublication(value CharacterActivationPublication) error {
	if value.Version != "character-activation-publication.v1" {
		return fmt.Errorf("invalid activation publication version")
	}
	evidence, err := s.LoadCharacterActivationChapterEvidence(value.GenerationID, value.Chapter)
	if err != nil {
		return err
	}
	if evidence == nil || evidence.Digest != value.EvidenceDigest || evidence.Context.ProjectionContextDigest == "" {
		return fmt.Errorf("activation publication lacks its exact projected chapter evidence")
	}
	access := value.Access
	if err := domain.ValidatePlanningContextAccessReceipt(access); err != nil {
		return err
	}
	if access.ConsumedAt.IsZero() || access.GenerationID != value.GenerationID || access.Chapter != value.Chapter || access.Phase != domain.PlanningContextAccessSimulate || access.PlanningContextDigest != evidence.Context.ProjectionContextDigest {
		return fmt.Errorf("activation publication lacks a consumed exact-context access receipt")
	}
	simulation, err := domain.BuildCharacterActivationSimulation(*evidence, value.BaseTickID, value.Sources)
	if err != nil {
		return err
	}
	if simulation.SimulationID != value.SimulationID {
		return fmt.Errorf("activation publication differs from its immutable simulation")
	}
	return nil
}

func (s *Store) SaveCharacterActivationPublication(value CharacterActivationPublication) error {
	if err := s.validateCharacterActivationPublication(value); err != nil {
		return err
	}
	// Only the still-present, actually consumed Host receipt may issue a new
	// intent. Reading an existing intent later deliberately does not require it.
	current, err := s.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessSimulate)
	if err != nil {
		return err
	}
	if current == nil || current.ReceiptDigest != value.Access.ReceiptDigest || !current.ConsumedAt.Equal(value.Access.ConsumedAt) {
		return fmt.Errorf("activation publication access receipt changed before durable intent")
	}
	root, err := characterActivationSessionDir(value.GenerationID, value.Chapter)
	if err != nil {
		return err
	}
	return s.withCharacterActivationWrite(func() error {
		return s.writeCharacterActivationJSON(filepath.Join(root, "publication.json"), value, true)
	})
}

func (s *Store) LoadCharacterActivationPublication(generation string, chapter int) (*CharacterActivationPublication, error) {
	root, err := characterActivationSessionDir(generation, chapter)
	if err != nil {
		return nil, err
	}
	var value CharacterActivationPublication
	if err := s.readCharacterActivationJSON(filepath.Join(root, "publication.json"), &value); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if value.GenerationID != generation || value.Chapter != chapter {
		return nil, fmt.Errorf("activation publication path mismatch")
	}
	if err := s.validateCharacterActivationPublication(value); err != nil {
		return nil, err
	}
	return &value, nil
}
