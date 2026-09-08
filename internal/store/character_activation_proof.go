package store

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// A view is immutable and may be shared by the four character workers. Only
// chapter proof paths are scoped; book identity, accepted memory and accounting
// keep their existing locations. Never swap Store.CharacterAgents on a shared
// Store or manufacture chapter/generation identifiers to run another cycle.
type characterActivationProofScope struct {
	generation, source, physicalRoot string
	chapter, index                   int
	day                              float64
}

func (s *CharacterAgentStore) ForActivationCycle(session domain.CharacterActivationSession) (*CharacterAgentStore, error) {
	if s == nil || s.io == nil || s.cycle != nil {
		return nil, fmt.Errorf("activation proof view requires an unscoped character store")
	}
	if err := domain.ValidateCharacterActivationSession(session); err != nil {
		return nil, err
	}
	index := len(session.CycleDigests) + 1
	if session.Phase != "collecting" || index > session.MaxCycles {
		return nil, fmt.Errorf("activation session is not collecting a permitted cycle")
	}
	previous := ""
	if index > 1 {
		previous = session.CycleDigests[index-2]
	}
	source, err := domain.CharacterActivationCycleSourceToken(session.GenerationID, session.Chapter, index, session.ChapterContextDigest, previous)
	if err != nil {
		return nil, err
	}
	if _, err := characterActivationSessionDir(session.GenerationID, session.Chapter); err != nil {
		return nil, err
	}
	return &CharacterAgentStore{io: s.io, cycle: &characterActivationProofScope{
		generation: session.GenerationID, chapter: session.Chapter, index: index,
		source: source, physicalRoot: session.CurrentPhysicalRoot, day: session.CurrentDay,
	}}, nil
}

func (s *CharacterAgentStore) ActivationCycleIndex() int {
	if s == nil || s.cycle == nil {
		return 0
	}
	return s.cycle.index
}

func (s *CharacterAgentStore) ActivationCycleSourceToken() string {
	if s == nil || s.cycle == nil {
		return ""
	}
	return s.cycle.source
}

// Existing proof callers supply a validated legacy chapter path. Reject any
// foreign chapter/generation instead of falling back to legacy evidence: that
// fallback would make a missing cycle proposal look already paid and complete.
func (s *CharacterAgentStore) proofPath(path string) (string, error) {
	if s.cycle == nil {
		return path, nil
	}
	base := characterAgentChapterDir(s.cycle.generation, s.cycle.chapter)
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("character proof is outside the bound activation cycle")
	}
	root, err := characterActivationSessionDir(s.cycle.generation, s.cycle.chapter)
	if err != nil {
		return "", err
	}
	path = filepath.Join(root, "work", fmt.Sprintf("%06d", s.cycle.index), "proof", rel)
	if err := validateCharacterMemoryPublicationPath(s.io, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *CharacterAgentStore) readProof(path string, value any) error {
	path, err := s.proofPath(path)
	if err != nil {
		return err
	}
	return s.io.ReadJSON(path, value)
}

func (s *CharacterAgentStore) writeProof(path string, value any) error {
	path, err := s.proofPath(path)
	if err != nil {
		return err
	}
	return s.writeImmutable(path, value)
}

func (s *CharacterAgentStore) validateCycleStimulus(packet domain.WorldStimulusPacket) error {
	if s.cycle == nil {
		return nil
	}
	if packet.GenerationID != s.cycle.generation || packet.Chapter != s.cycle.chapter || packet.Version != domain.WorldStimulusPacketV2Version || packet.PhysicalState == nil || packet.StoryClock == nil {
		return fmt.Errorf("cycle stimulus lacks its bound identity/physical state/clock")
	}
	found := false
	for _, source := range packet.Sources {
		if !strings.HasPrefix(source, domain.CharacterActivationCycleSourcePrefix) {
			continue
		}
		if source != s.cycle.source {
			return fmt.Errorf("stimulus contains a foreign activation cycle source")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("stimulus lacks the bound activation cycle source")
	}
	root, err := domain.CharacterPhysicalRootForCycle(*packet.PhysicalState)
	if err != nil {
		return err
	}
	if root != s.cycle.physicalRoot || packet.StoryClock.CurrentDay != s.cycle.day {
		return fmt.Errorf("cycle stimulus differs from the session's actual pre-state/clock")
	}
	return nil
}

func (s *CharacterAgentStore) validateCycleObservation(packet domain.CharacterObservationPacket) error {
	if s.cycle == nil {
		return nil
	}
	if packet.Round < 1 || packet.Round > 2 {
		return fmt.Errorf("cycle permits only an initial proposal and one revision")
	}
	stimulus, err := s.LoadStimulus(packet.GenerationID, packet.Chapter)
	if err != nil {
		return err
	}
	if stimulus == nil || packet.StimulusDigest != stimulus.Digest {
		return fmt.Errorf("cycle observation lacks its persisted stimulus")
	}
	return domain.ValidateCharacterResourceViewsAgainstStimulusV2(*stimulus, packet)
}

func (s *CharacterAgentStore) validateCycleActivation(activation domain.CharacterAgentActivation) error {
	if s.cycle == nil {
		return nil
	}
	registry, err := s.LoadRegistrySnapshot(activation.GenerationID, activation.Chapter)
	if err != nil {
		return err
	}
	if registry == nil || registry.RegistryRoot != activation.RegistryRoot {
		return fmt.Errorf("cycle activation lacks its persisted registry snapshot")
	}
	for _, entry := range activation.Entries {
		if entry.State != domain.CharacterAgentActive {
			continue
		}
		observation, err := s.LoadObservation(activation.GenerationID, activation.Chapter, 1, entry.AgentID)
		if err != nil {
			return err
		}
		if observation == nil || observation.Digest != entry.ObservationDigest {
			return fmt.Errorf("cycle activation lacks its persisted observation for %s", entry.AgentID)
		}
	}
	return nil
}

func (s *CharacterAgentStore) validateCycleArbitrationInputs(receipt domain.WorldArbitrationReceipt, stimulus domain.WorldStimulusPacket, activation domain.CharacterAgentActivation, proposals []domain.CharacterDecisionProposal) error {
	if s.cycle == nil {
		return nil
	}
	if receipt.Round < 1 || receipt.Round > 2 {
		return fmt.Errorf("cycle arbitration exceeds its single revision")
	}
	storedStimulus, err := s.LoadStimulus(receipt.GenerationID, receipt.Chapter)
	if err != nil {
		return err
	}
	storedActivation, err := s.LoadActivation(receipt.GenerationID, receipt.Chapter)
	if err != nil {
		return err
	}
	if storedStimulus == nil || storedStimulus.Digest != stimulus.Digest || storedActivation == nil || storedActivation.Digest != activation.Digest {
		return fmt.Errorf("cycle arbitration lacks its persisted stimulus/activation")
	}
	for _, proposal := range proposals {
		stored, err := s.LoadProposal(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID)
		if err != nil {
			return err
		}
		if stored == nil || stored.Digest != proposal.Digest {
			return fmt.Errorf("cycle arbitration lacks its persisted proposal for %s", proposal.AgentID)
		}
	}
	return nil
}
