package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const characterAgentRoot = "meta/character_agents"

var characterAgentPathComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var characterAgentDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// CharacterAgentStore owns durable identities/canonical memories and the
// generation-scoped evidence produced by autonomous character decisions.
type CharacterAgentStore struct {
	io    *IO
	cycle *characterActivationProofScope
}

func NewCharacterAgentStore(io *IO) *CharacterAgentStore { return &CharacterAgentStore{io: io} }

func validateCharacterAgentPathComponent(label, value string) error {
	value = strings.TrimSpace(value)
	if !characterAgentPathComponent.MatchString(value) || value == "." || value == ".." {
		return fmt.Errorf("invalid %s %q", label, value)
	}
	return nil
}

func characterAgentRegistryPath() string {
	return filepath.Join(characterAgentRoot, "registry.json")
}

func characterAgentMemoryPath(agentID string) string {
	return filepath.Join(characterAgentRoot, "memory", agentID+".json")
}

func projectedCharacterAgentMemoryPath(generationID, agentID string) string {
	return filepath.Join(characterAgentRoot, "projected", generationID, "memory", agentID+".json")
}

func characterAgentChapterDir(generationID string, chapter int) string {
	return filepath.Join(characterAgentRoot, "projected", generationID, "chapters", fmt.Sprintf("%06d", chapter))
}

func characterAgentObservationPath(generationID string, chapter, round int, agentID string) string {
	return filepath.Join(characterAgentChapterDir(generationID, chapter), "observations", fmt.Sprintf("round-%02d", round), agentID+".json")
}

func characterAgentProposalPath(generationID string, chapter, round int, agentID string) string {
	return filepath.Join(characterAgentChapterDir(generationID, chapter), "proposals", fmt.Sprintf("round-%02d", round), agentID+".json")
}

func characterAgentArbitrationPath(generationID string, chapter, round int) string {
	return filepath.Join(characterAgentChapterDir(generationID, chapter), fmt.Sprintf("arbitration-round-%02d.json", round))
}

func characterAgentRegistrySnapshotPath(generationID string, chapter int) string {
	return filepath.Join(characterAgentChapterDir(generationID, chapter), "registry.json")
}

func characterAgentSuccessorPlanPath(parentGenerationID, digest string) string {
	return filepath.Join(characterAgentRoot, "successors", parentGenerationID, strings.TrimPrefix(digest, "sha256:")+".json")
}

func characterAgentCurrentSuccessorPath() string {
	return filepath.Join(characterAgentRoot, "successors", "current.json")
}

func (s *CharacterAgentStore) LoadRegistry() (*domain.CharacterAgentRegistry, error) {
	var registry domain.CharacterAgentRegistry
	if err := s.io.ReadJSON(characterAgentRegistryPath(), &registry); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		return nil, err
	}
	if finalized.RegistryRoot != registry.RegistryRoot {
		return nil, fmt.Errorf("character agent registry root mismatch")
	}
	return &registry, nil
}

func (s *CharacterAgentStore) SaveRegistry(registry domain.CharacterAgentRegistry) error {
	finalized, err := domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		return err
	}
	return s.io.WriteJSON(characterAgentRegistryPath(), finalized)
}

func (s *CharacterAgentStore) SaveRegistrySnapshot(generationID string, chapter int, registry domain.CharacterAgentRegistry) error {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return err
	}
	if chapter <= 0 {
		return fmt.Errorf("character agent registry snapshot chapter must be > 0")
	}
	finalized, err := domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil {
		return err
	}
	path := characterAgentRegistrySnapshotPath(generationID, chapter)
	return s.writeProof(path, &finalized)
}

func (s *CharacterAgentStore) LoadRegistrySnapshot(generationID string, chapter int) (*domain.CharacterAgentRegistry, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	var registry domain.CharacterAgentRegistry
	if err := s.readProof(characterAgentRegistrySnapshotPath(generationID, chapter), &registry); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterAgentRegistry(registry)
	if err != nil || finalized.RegistryRoot != registry.RegistryRoot {
		return nil, fmt.Errorf("invalid character-agent registry snapshot: %w", err)
	}
	return &registry, nil
}

// SaveSuccessorPlan stores an immutable Architect result and optionally makes
// it the active input for the next project-all identity. The mutable pointer
// contains only the content digest; the plan itself remains content-addressed.
func (s *CharacterAgentStore) SaveSuccessorPlan(plan domain.CharacterAgentSuccessorPlan, activate bool) error {
	if err := validateCharacterAgentPathComponent("parent_generation_id", plan.ParentGenerationID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil {
		return err
	}
	if err := s.writeImmutable(characterAgentSuccessorPlanPath(finalized.ParentGenerationID, finalized.Digest), &finalized); err != nil {
		return err
	}
	if !activate {
		return nil
	}
	pointer := struct {
		Version            string `json:"version"`
		ParentGenerationID string `json:"parent_generation_id"`
		PlanDigest         string `json:"plan_digest"`
	}{domain.CharacterAgentSuccessorPlanVersion, finalized.ParentGenerationID, finalized.Digest}
	return s.io.WriteJSON(characterAgentCurrentSuccessorPath(), pointer)
}

func (s *CharacterAgentStore) LoadSuccessorPlan(parentGenerationID, digest string) (*domain.CharacterAgentSuccessorPlan, error) {
	if err := validateCharacterAgentPathComponent("parent_generation_id", parentGenerationID); err != nil {
		return nil, err
	}
	if !characterAgentDigestPattern.MatchString(strings.TrimSpace(digest)) {
		return nil, fmt.Errorf("invalid character-agent successor digest %q", digest)
	}
	var plan domain.CharacterAgentSuccessorPlan
	if err := s.io.ReadJSON(characterAgentSuccessorPlanPath(parentGenerationID, digest), &plan); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterAgentSuccessorPlan(plan)
	if err != nil || finalized.Digest != plan.Digest || plan.Digest != digest || plan.ParentGenerationID != parentGenerationID {
		return nil, fmt.Errorf("invalid character-agent successor plan: %w", err)
	}
	return &plan, nil
}

func (s *CharacterAgentStore) LoadCurrentSuccessorPlan() (*domain.CharacterAgentSuccessorPlan, error) {
	var pointer struct {
		Version            string `json:"version"`
		ParentGenerationID string `json:"parent_generation_id"`
		PlanDigest         string `json:"plan_digest"`
	}
	if err := s.io.ReadJSON(characterAgentCurrentSuccessorPath(), &pointer); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if pointer.Version != domain.CharacterAgentSuccessorPlanVersion {
		return nil, fmt.Errorf("invalid current character-agent successor pointer")
	}
	return s.LoadSuccessorPlan(pointer.ParentGenerationID, pointer.PlanDigest)
}

func (s *CharacterAgentStore) LoadCanonicalMemory(agentID string) (*domain.CharacterAgentMemory, error) {
	return s.loadMemory(characterAgentMemoryPath(agentID), agentID, "canonical", "")
}

func (s *CharacterAgentStore) SaveCanonicalMemory(memory domain.CharacterAgentMemory) error {
	if err := validateCharacterAgentPathComponent("agent_id", memory.AgentID); err != nil {
		return err
	}
	memory.State = "canonical"
	memory.GenerationID = ""
	finalized, err := domain.FinalizeCharacterAgentMemory(memory)
	if err != nil {
		return err
	}
	return s.io.WriteJSON(characterAgentMemoryPath(memory.AgentID), finalized)
}

func (s *CharacterAgentStore) LoadProjectedMemory(generationID, agentID string) (*domain.CharacterAgentMemory, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return s.loadMemory(projectedCharacterAgentMemoryPath(generationID, agentID), agentID, "projected", generationID)
}

func (s *CharacterAgentStore) SaveProjectedMemory(memory domain.CharacterAgentMemory) error {
	if err := validateCharacterAgentPathComponent("generation_id", memory.GenerationID); err != nil {
		return err
	}
	if err := validateCharacterAgentPathComponent("agent_id", memory.AgentID); err != nil {
		return err
	}
	memory.State = "projected"
	finalized, err := domain.FinalizeCharacterAgentMemory(memory)
	if err != nil {
		return err
	}
	return s.io.WriteJSON(projectedCharacterAgentMemoryPath(memory.GenerationID, memory.AgentID), finalized)
}

func (s *CharacterAgentStore) loadMemory(path, agentID, state, generationID string) (*domain.CharacterAgentMemory, error) {
	if err := validateCharacterAgentPathComponent("agent_id", agentID); err != nil {
		return nil, err
	}
	var memory domain.CharacterAgentMemory
	if err := s.io.ReadJSON(path, &memory); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterAgentMemory(memory)
	if err != nil {
		return nil, err
	}
	if finalized.MemoryRoot != memory.MemoryRoot || memory.AgentID != agentID || memory.State != state || memory.GenerationID != generationID {
		return nil, fmt.Errorf("character memory identity/root mismatch for %s", agentID)
	}
	return &memory, nil
}

func (s *CharacterAgentStore) SaveStimulus(packet domain.WorldStimulusPacket) error {
	if err := validateCharacterAgentPathComponent("generation_id", packet.GenerationID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeWorldStimulusPacket(packet)
	if err != nil {
		return err
	}
	if err := s.validateCycleStimulus(finalized); err != nil {
		return err
	}
	path := filepath.Join(characterAgentChapterDir(packet.GenerationID, packet.Chapter), "stimulus.json")
	return s.writeProof(path, &finalized)
}

func (s *CharacterAgentStore) LoadStimulus(generationID string, chapter int) (*domain.WorldStimulusPacket, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	var packet domain.WorldStimulusPacket
	path := filepath.Join(characterAgentChapterDir(generationID, chapter), "stimulus.json")
	if err := s.readProof(path, &packet); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeWorldStimulusPacket(packet)
	if err != nil {
		return nil, fmt.Errorf("invalid world stimulus: %w", err)
	}
	if finalized.Digest != packet.Digest || packet.GenerationID != generationID || packet.Chapter != chapter {
		return nil, fmt.Errorf("world stimulus identity/digest mismatch for %s chapter %d", generationID, chapter)
	}
	if err := s.validateCycleStimulus(packet); err != nil {
		return nil, err
	}
	return &packet, nil
}

func (s *CharacterAgentStore) SaveActivation(activation domain.CharacterAgentActivation) error {
	if err := validateCharacterAgentPathComponent("generation_id", activation.GenerationID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeCharacterAgentActivation(activation)
	if err != nil {
		return err
	}
	if err := s.validateCycleActivation(finalized); err != nil {
		return err
	}
	path := filepath.Join(characterAgentChapterDir(activation.GenerationID, activation.Chapter), "activation.json")
	return s.writeProof(path, &finalized)
}

func (s *CharacterAgentStore) LoadActivation(generationID string, chapter int) (*domain.CharacterAgentActivation, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	var activation domain.CharacterAgentActivation
	path := filepath.Join(characterAgentChapterDir(generationID, chapter), "activation.json")
	if err := s.readProof(path, &activation); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterAgentActivation(activation)
	if err != nil {
		return nil, fmt.Errorf("invalid character activation: %w", err)
	}
	if finalized.Digest != activation.Digest || activation.GenerationID != generationID || activation.Chapter != chapter {
		return nil, fmt.Errorf("character activation identity/digest mismatch for %s chapter %d", generationID, chapter)
	}
	if err := s.validateCycleActivation(activation); err != nil {
		return nil, err
	}
	return &activation, nil
}

func (s *CharacterAgentStore) SaveObservation(packet domain.CharacterObservationPacket) error {
	if err := validateCharacterAgentPathComponent("generation_id", packet.GenerationID); err != nil {
		return err
	}
	if err := validateCharacterAgentPathComponent("agent_id", packet.AgentID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeCharacterObservationPacket(packet)
	if err != nil {
		return err
	}
	if err := s.validateCycleObservation(finalized); err != nil {
		return err
	}
	return s.writeProof(characterAgentObservationPath(packet.GenerationID, packet.Chapter, packet.Round, packet.AgentID), &finalized)
}

func (s *CharacterAgentStore) LoadObservation(generationID string, chapter, round int, agentID string) (*domain.CharacterObservationPacket, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	if err := validateCharacterAgentPathComponent("agent_id", agentID); err != nil {
		return nil, err
	}
	var packet domain.CharacterObservationPacket
	if err := s.readProof(characterAgentObservationPath(generationID, chapter, round, agentID), &packet); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterObservationPacket(packet)
	if err != nil {
		return nil, fmt.Errorf("invalid character observation: %w", err)
	}
	if finalized.Digest != packet.Digest || packet.GenerationID != generationID || packet.Chapter != chapter || packet.Round != round || packet.AgentID != agentID {
		return nil, fmt.Errorf("character observation identity/digest mismatch for %s chapter %d round %d agent %s", generationID, chapter, round, agentID)
	}
	if err := s.validateCycleObservation(packet); err != nil {
		return nil, err
	}
	return &packet, nil
}

func (s *CharacterAgentStore) SaveProposal(proposal domain.CharacterDecisionProposal, observation domain.CharacterObservationPacket) error {
	if err := validateCharacterAgentPathComponent("generation_id", proposal.GenerationID); err != nil {
		return err
	}
	if err := validateCharacterAgentPathComponent("agent_id", proposal.AgentID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeCharacterDecisionProposal(proposal, observation)
	if err != nil {
		return err
	}
	if s.cycle != nil {
		stored, err := s.LoadObservation(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID)
		if err != nil {
			return err
		}
		if stored == nil || stored.Digest != observation.Digest {
			return fmt.Errorf("cycle proposal lacks its persisted observation")
		}
	}
	return s.writeProof(characterAgentProposalPath(proposal.GenerationID, proposal.Chapter, proposal.Round, proposal.AgentID), &finalized)
}

func (s *CharacterAgentStore) LoadProposal(generationID string, chapter, round int, agentID string) (*domain.CharacterDecisionProposal, error) {
	observation, err := s.LoadObservation(generationID, chapter, round, agentID)
	if err != nil || observation == nil {
		return nil, err
	}
	var proposal domain.CharacterDecisionProposal
	if err := s.readProof(characterAgentProposalPath(generationID, chapter, round, agentID), &proposal); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	finalized, err := domain.FinalizeCharacterDecisionProposal(proposal, *observation)
	if err != nil || finalized.Digest != proposal.Digest {
		return nil, fmt.Errorf("invalid character proposal: %w", err)
	}
	return &proposal, nil
}

func (s *CharacterAgentStore) LoadProposals(generationID string, chapter, round int, agentIDs []string) ([]domain.CharacterDecisionProposal, error) {
	out := make([]domain.CharacterDecisionProposal, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		proposal, err := s.LoadProposal(generationID, chapter, round, agentID)
		if err != nil {
			return nil, err
		}
		if proposal != nil {
			out = append(out, *proposal)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (s *CharacterAgentStore) LoadLatestProposals(generationID string, chapter, throughRound int, agentIDs []string) ([]domain.CharacterDecisionProposal, error) {
	out := make([]domain.CharacterDecisionProposal, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		var latest *domain.CharacterDecisionProposal
		for round := 1; round <= throughRound; round++ {
			proposal, err := s.LoadProposal(generationID, chapter, round, agentID)
			if err != nil {
				return nil, err
			}
			if proposal != nil {
				latest = proposal
			}
		}
		if latest != nil {
			out = append(out, *latest)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (s *CharacterAgentStore) SaveArbitration(receipt domain.WorldArbitrationReceipt, stimulus domain.WorldStimulusPacket, activation domain.CharacterAgentActivation, proposals []domain.CharacterDecisionProposal, maxRevisionRounds int) error {
	if err := validateCharacterAgentPathComponent("generation_id", receipt.GenerationID); err != nil {
		return err
	}
	finalized, err := domain.FinalizeWorldArbitrationReceipt(receipt, stimulus, activation, proposals, maxRevisionRounds)
	if err != nil {
		return err
	}
	if err := s.validateCycleArbitrationInputs(finalized, stimulus, activation, proposals); err != nil {
		return err
	}
	return s.writeProof(characterAgentArbitrationPath(receipt.GenerationID, receipt.Chapter, receipt.Round), &finalized)
}

func (s *CharacterAgentStore) LoadArbitration(generationID string, chapter, round int) (*domain.WorldArbitrationReceipt, error) {
	if err := validateCharacterAgentPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	var receipt domain.WorldArbitrationReceipt
	if err := s.readProof(characterAgentArbitrationPath(generationID, chapter, round), &receipt); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if receipt.GenerationID != generationID || receipt.Chapter != chapter || receipt.Round != round {
		return nil, fmt.Errorf("world arbitration identity mismatch for %s chapter %d round %d", generationID, chapter, round)
	}
	stimulus, err := s.LoadStimulus(generationID, chapter)
	if err != nil || stimulus == nil {
		return nil, fmt.Errorf("load arbitration stimulus: %w", err)
	}
	activation, err := s.LoadActivation(generationID, chapter)
	if err != nil || activation == nil {
		return nil, fmt.Errorf("load arbitration activation: %w", err)
	}
	ids := make([]string, 0)
	for _, entry := range activation.Entries {
		if entry.State == domain.CharacterAgentActive {
			ids = append(ids, entry.AgentID)
		}
	}
	proposals, err := s.LoadLatestProposals(generationID, chapter, round, ids)
	if err != nil {
		return nil, err
	}
	finalized, err := domain.FinalizeWorldArbitrationReceipt(receipt, *stimulus, *activation, proposals, max(1, round-1))
	if err != nil || finalized.Digest != receipt.Digest {
		return nil, fmt.Errorf("invalid world arbitration receipt: %w", err)
	}
	return &receipt, nil
}

func (s *CharacterAgentStore) AppendUsage(usage domain.CharacterAgentUsage) error {
	if err := validateCharacterAgentPathComponent("agent_id", usage.AgentID); err != nil {
		return err
	}
	raw, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	return s.io.AppendLine(filepath.Join(characterAgentRoot, "usage.jsonl"), append(raw, '\n'))
}

func (s *CharacterAgentStore) LoadUsage() ([]domain.CharacterAgentUsage, error) {
	raw, err := s.io.ReadFile(filepath.Join(characterAgentRoot, "usage.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]domain.CharacterAgentUsage, 0, len(lines))
	for lineNo, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var usage domain.CharacterAgentUsage
		if err := json.Unmarshal([]byte(line), &usage); err != nil {
			return nil, fmt.Errorf("character-agent usage line %d: %w", lineNo+1, err)
		}
		out = append(out, usage)
	}
	return out, nil
}

// EnsureCharacterAgentCanon performs the deterministic v1 migration used at a
// new planning-generation boundary. It never asks a model to invent history:
// stable identities come from accepted character records, while initial
// canonical memory contains only accepted continuity/self-knowledge facts.
func (s *Store) EnsureCharacterAgentCanon(baseChapter int) error {
	characters, err := s.Characters.Load()
	if err != nil {
		return err
	}
	dossiers, err := s.LoadAllCharacterDossiers()
	if err != nil {
		return err
	}
	continuity, err := s.LoadCharacterContinuityLedger()
	if err != nil {
		return err
	}
	if len(characters) == 0 && len(dossiers) == 0 {
		return nil
	}
	registry, err := s.CharacterAgents.LoadRegistry()
	if err != nil {
		return err
	}
	if registry == nil {
		registry = &domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
		if progress, loadErr := s.Progress.Load(); loadErr == nil && progress != nil {
			registry.Project = progress.NovelName
		}
	}
	type seed struct {
		name    string
		aliases []string
		tier    string
		role    string
		facts   []string
		chapter int
	}
	seeds := make(map[string]seed)
	add := func(item seed) {
		item.name = strings.TrimSpace(item.name)
		item.tier = strings.TrimSpace(item.tier)
		protagonist := characterAgentSeedIsProtagonist(item.role)
		if item.name == "" || (!protagonist && characterAgentSeedIsCrowdOrDecorative(item.name, item.role, item.tier)) {
			return
		}
		if protagonist {
			item.tier = "core"
		} else if item.tier == "" {
			item.tier = "important"
		}
		key := strings.ToLower(strings.Join(strings.Fields(item.name), " "))
		if existing, ok := seeds[key]; ok {
			existing.aliases = append(existing.aliases, item.aliases...)
			existing.facts = append(existing.facts, item.facts...)
			if item.tier == "core" {
				existing.tier = item.tier
			}
			if item.chapter > existing.chapter {
				existing.chapter = item.chapter
			}
			seeds[key] = existing
			return
		}
		seeds[key] = item
	}
	for _, character := range characters {
		var facts []string
		if character.InitialState != nil {
			facts = append(facts, character.InitialState.KnownFacts...)
		}
		add(seed{name: character.Name, aliases: character.Aliases, tier: character.Tier, role: character.Role, facts: facts})
	}
	for _, dossier := range dossiers {
		// KnowledgeBoundary is an author-facing restriction and may name the
		// very secret this character must not know. It is not positive memory.
		add(seed{name: dossier.Character, aliases: dossier.Aliases, tier: dossier.Tier, role: dossier.Role, facts: dossier.KnownFactsAtStoryStart})
	}
	if continuity != nil {
		for _, entry := range continuity.Entries {
			facts := append([]string(nil), entry.CurrentFacts...)
			facts = append(facts, entry.Dynamics.KnowledgeLedger.KnownFacts...)
			add(seed{name: entry.Name, aliases: entry.Aliases, tier: entry.Tier, facts: facts, chapter: entry.LastSeenChapter})
		}
	}
	keys := make([]string, 0, len(seeds))
	for key := range seeds {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changed := false
	stamp := fmt.Sprintf("migration:chapter-%06d", max(baseChapter, 0))
	for _, key := range keys {
		item := seeds[key]
		record, exists := registry.Resolve(item.name)
		if exists && !strings.EqualFold(strings.TrimSpace(record.Character), strings.TrimSpace(item.name)) && characterAgentIdentityContains(record.Aliases, item.name) && !characterAgentIdentityContains(item.aliases, record.Character) {
			// A continuity/dossier entry may still use the old canonical name after
			// characters.json renamed it. Treat that old spelling as an alias; do
			// not oscillate the registry canonical name back on every migration.
			item.aliases = append(item.aliases, item.name)
			item.name = record.Character
		}
		if !exists || characterAgentRegistrySeedChanged(record, item.name, item.aliases, item.tier) {
			updated, next, upsertErr := registry.UpsertCharacter(item.name, item.aliases, item.tier, max(baseChapter+1, 1), stamp)
			if upsertErr != nil {
				return upsertErr
			}
			registry = &updated
			record = next
			changed = true
		}
		memory, loadErr := s.CharacterAgents.LoadCanonicalMemory(record.AgentID)
		if loadErr != nil {
			return loadErr
		}
		if memory != nil {
			continue
		}
		memoryFacts := make([]domain.CharacterAgentMemoryFact, 0, len(item.facts))
		seenFacts := map[string]struct{}{}
		for _, fact := range item.facts {
			fact = strings.TrimSpace(fact)
			if fact == "" {
				continue
			}
			if _, duplicate := seenFacts[fact]; duplicate {
				continue
			}
			seenFacts[fact] = struct{}{}
			chapter := item.chapter
			if chapter <= 0 {
				chapter = max(baseChapter, 1)
			}
			digest := sha256.Sum256([]byte("character-agent-memory-migration.v1\x00" + record.AgentID + "\x00" + fact))
			memoryFacts = append(memoryFacts, domain.CharacterAgentMemoryFact{
				ID: "mem_" + hex.EncodeToString(digest[:8]), Chapter: chapter, Kind: "accepted_continuity",
				Text: fact, SourceDigest: "sha256:" + hex.EncodeToString(digest[:]), Accepted: true,
			})
		}
		if err := s.CharacterAgents.SaveCanonicalMemory(domain.CharacterAgentMemory{
			Version: domain.CharacterAgentMemoryVersion, AgentID: record.AgentID, Character: record.Character,
			State: "canonical", LastAcceptedChapter: max(baseChapter, item.chapter), Facts: memoryFacts, UpdatedAt: stamp,
		}); err != nil {
			return err
		}
	}
	if changed || registry.RegistryRoot == "" {
		return s.CharacterAgents.SaveRegistry(*registry)
	}
	return nil
}

func characterAgentSeedIsProtagonist(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	return strings.Contains(role, "主角") || strings.Contains(role, "protagonist") || strings.Contains(role, "lead")
}

func characterAgentSeedIsCrowdOrDecorative(name, role, tier string) bool {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "decorative" || tier == "secondary" || domain.IsCrowdRoleLabel(name) || domain.IsCrowdRoleLabel(role) {
		return true
	}
	for _, marker := range []string{"群众", "群演", "路人", "背景角色", "装饰性角色"} {
		if strings.Contains(role, marker) {
			return true
		}
	}
	return false
}

func characterAgentRegistrySeedChanged(record domain.CharacterAgentRecord, name string, aliases []string, tier string) bool {
	if strings.TrimSpace(record.Character) != strings.TrimSpace(name) || strings.TrimSpace(record.Tier) != strings.TrimSpace(tier) {
		return true
	}
	wanted := normalizeCharacterAgentIdentitySet(aliases, name)
	current := normalizeCharacterAgentIdentitySet(record.Aliases, record.Character)
	return strings.Join(wanted, "\x00") != strings.Join(current, "\x00")
}

func characterAgentIdentityContains(values []string, wanted string) bool {
	wanted = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(wanted)), " "))
	for _, value := range values {
		if strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " ")) == wanted {
			return true
		}
	}
	return false
}

func normalizeCharacterAgentIdentitySet(values []string, canonical string) []string {
	seen := map[string]struct{}{}
	canonical = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(canonical)), " "))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
		if key == "" || key == canonical {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// PromoteAcceptedCharacterAgentMemory advances canonical memory only after an
// immutable actual-outcome receipt proves the chapter body was accepted. It
// derives each fact from that character's own final proposal and actual
// arbitration result; projected memory files and abandoned generations are
// never copied into canon.
func (s *Store) PromoteAcceptedCharacterAgentMemory(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) error {
	if bundle.CharacterActivationEvidence != nil {
		return fmt.Errorf("multi-cycle accepted memory must use the prepared immutable publication workflow")
	}
	if bundle.ChapterWorldSimulation.Version < 2 {
		return nil
	}
	if bundle.CharacterAgentEvidence == nil {
		return fmt.Errorf("simulation v2 has no sealed character-agent evidence")
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return err
	}
	if err := domain.ValidateActualOutcomeReceiptV2(outcome); err != nil {
		return err
	}
	if !outcome.ProjectionMatch || outcome.GenerationID != bundle.GenerationID || outcome.Chapter != bundle.Chapter || outcome.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
		return fmt.Errorf("accepted outcome does not match character-agent bundle")
	}
	evidence := bundle.CharacterAgentEvidence
	finalArbitration := evidence.Arbitrations[len(evidence.Arbitrations)-1]
	latest := make(map[string]domain.CharacterDecisionProposal)
	for _, proposal := range evidence.Proposals {
		if current, ok := latest[proposal.AgentID]; !ok || proposal.Round > current.Round {
			latest[proposal.AgentID] = proposal
		}
	}
	registry, err := s.CharacterAgents.LoadRegistry()
	if err != nil {
		return fmt.Errorf("load canonical character-agent registry: %w", err)
	}
	registryChanged := false
	if registry == nil {
		clone := evidence.Registry
		clone.Entries = cloneCharacterAgentRecords(evidence.Registry.Entries)
		clone.RegistryRoot = ""
		for i := range clone.Entries {
			if clone.Entries[i].Status != domain.CharacterAgentRetired {
				clone.Entries[i].Status = domain.CharacterAgentSleeping
			}
			clone.Entries[i].UpdatedAt = outcome.AcceptedAt
		}
		registry = &clone
		registryChanged = true
	} else {
		known := make(map[string]struct{}, len(registry.Entries))
		for _, entry := range registry.Entries {
			known[entry.AgentID] = struct{}{}
		}
		for _, entry := range evidence.Registry.Entries {
			if _, exists := known[entry.AgentID]; exists {
				continue
			}
			entry.Status = domain.CharacterAgentSleeping
			entry.UpdatedAt = outcome.AcceptedAt
			registry.Entries = append(registry.Entries, entry)
			registryChanged = true
		}
	}
	for _, resolution := range finalArbitration.Resolutions {
		proposal, ok := latest[resolution.AgentID]
		if !ok {
			return fmt.Errorf("accepted arbitration lacks proposal for %s", resolution.AgentID)
		}
		memory, loadErr := s.CharacterAgents.LoadCanonicalMemory(resolution.AgentID)
		if loadErr != nil {
			return loadErr
		}
		if memory == nil {
			memory = &domain.CharacterAgentMemory{
				Version: domain.CharacterAgentMemoryVersion, AgentID: resolution.AgentID,
				Character: resolution.Character, State: "canonical",
			}
		}
		factSum := sha256.Sum256([]byte("accepted-character-decision.v1\x00" + outcome.ReceiptDigest + "\x00" + resolution.AgentID))
		factID := "mem_" + hex.EncodeToString(factSum[:8])
		alreadyPresent := false
		for _, fact := range memory.Facts {
			if fact.ID == factID {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent {
			memoryText := strings.TrimSpace("决定：" + proposal.Decision + "；行动：" + proposal.IntendedAction + "；实际结果：" + resolution.ImmediateResult + "；后态：" + resolution.StateAfter)
			if finalArbitration.Version == domain.WorldArbitrationReceiptV2Version {
				if bundle.ChapterWorldSimulation.PhysicalState == nil {
					return fmt.Errorf("accepted v2 character memory lacks physical state")
				}
				var memoryErr error
				memoryText, memoryErr = domain.CharacterPrivateOutcomeV2(proposal, resolution, *bundle.ChapterWorldSimulation.PhysicalState, finalArbitration)
				if memoryErr != nil {
					return memoryErr
				}
			}
			memory.Facts = append(memory.Facts, domain.CharacterAgentMemoryFact{
				ID: factID, Chapter: bundle.Chapter, Kind: "accepted_decision_outcome",
				Text:         memoryText,
				SourceDigest: outcome.ReceiptDigest, KnowledgeRefs: append([]string(nil), proposal.KnowledgeRefs...), Accepted: true,
			})
			memory.LastAcceptedChapter = max(memory.LastAcceptedChapter, bundle.Chapter)
			memory.UpdatedAt = outcome.AcceptedAt
			if err := s.CharacterAgents.SaveCanonicalMemory(*memory); err != nil {
				return err
			}
			for i := range registry.Entries {
				if registry.Entries[i].AgentID == resolution.AgentID {
					registry.Entries[i].MemoryVersion++
					registry.Entries[i].UpdatedAt = outcome.AcceptedAt
					registryChanged = true
					break
				}
			}
		}
	}
	if registryChanged {
		return s.CharacterAgents.SaveRegistry(*registry)
	}
	return nil
}

// PromoteReviewedCharacterAgentMemory is the ordinary interactive-pipeline
// promotion path. It runs only after a chapter review is formally accepted and
// copies the refreshed continuity facts actually derived from the accepted
// body. It never copies projected decisions or projected-memory files.
func (s *Store) PromoteReviewedCharacterAgentMemory(chapter int, acceptedAt string) error {
	simulation, err := s.LoadChapterWorldSimulation(chapter)
	if err != nil || simulation == nil || simulation.Version < 2 || simulation.CharacterAgentProtocol == nil {
		return err
	}
	type memoryTarget struct{ AgentID, Character string }
	var targets []memoryTarget
	var snapshot *domain.CharacterAgentRegistry
	if simulation.CharacterActivation != nil {
		if !s.World.HasAcceptedChapterReview(chapter) {
			return fmt.Errorf("activation memory promotion requires an accepted body review")
		}
		evidence, err := s.LoadCharacterActivationChapterEvidence(simulation.GenerationID, chapter)
		if err != nil {
			return err
		}
		if evidence == nil {
			return fmt.Errorf("reviewed activation chapter lacks complete evidence")
		}
		if err := domain.ValidateCharacterActivationSimulation(*simulation, *evidence); err != nil {
			return err
		}
		snapshot = &evidence.Inputs[0].Registry
		ids := map[string]bool{}
		for _, cycle := range evidence.Cycles {
			last := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
			for _, resolution := range last.Resolutions {
				ids[resolution.AgentID] = true
			}
			for _, reception := range last.PassiveReceptions {
				ids[reception.ToAgentID] = true
			}
		}
		for _, entry := range snapshot.Entries {
			if ids[entry.AgentID] {
				targets = append(targets, memoryTarget{entry.AgentID, entry.Character})
			}
		}
	} else {
		activation, err := s.CharacterAgents.LoadActivation(simulation.GenerationID, chapter)
		if err != nil || activation == nil || activation.Digest != simulation.CharacterAgentProtocol.ActivationDigest {
			return fmt.Errorf("load accepted character activation: %w", err)
		}
		snapshot, err = s.CharacterAgents.LoadRegistrySnapshot(simulation.GenerationID, chapter)
		if err != nil || snapshot == nil {
			return fmt.Errorf("load accepted character registry snapshot: %w", err)
		}
		for _, entry := range activation.Entries {
			if entry.State == domain.CharacterAgentActive {
				targets = append(targets, memoryTarget{entry.AgentID, entry.Character})
			}
		}
	}
	registry, err := s.CharacterAgents.LoadRegistry()
	if err != nil {
		return err
	}
	if registry == nil {
		clone := *snapshot
		clone.Entries = cloneCharacterAgentRecords(snapshot.Entries)
		clone.RegistryRoot = ""
		registry = &clone
	}
	knownRegistry := make(map[string]struct{}, len(registry.Entries))
	for _, entry := range registry.Entries {
		knownRegistry[entry.AgentID] = struct{}{}
	}
	for _, entry := range snapshot.Entries {
		if _, exists := knownRegistry[entry.AgentID]; !exists {
			registry.Entries = append(registry.Entries, entry)
		}
	}
	continuity, err := s.LoadCharacterContinuityLedger()
	if err != nil {
		return err
	}
	factsByName := make(map[string][]string)
	if continuity != nil {
		for _, entry := range continuity.Entries {
			factsByName[strings.ToLower(strings.Join(strings.Fields(entry.Name), " "))] = append([]string(nil), entry.CurrentFacts...)
		}
	}
	sourceRaw, _ := s.Progress.io.ReadFile(fmt.Sprintf("reviews/%02d.json", chapter))
	sourceSum := sha256.Sum256(append(append([]byte(nil), sourceRaw...), []byte(simulation.SimulationID)...))
	sourceDigest := "sha256:" + hex.EncodeToString(sourceSum[:])
	registryChanged := false
	for _, active := range targets {
		memory, loadErr := s.CharacterAgents.LoadCanonicalMemory(active.AgentID)
		if loadErr != nil {
			return loadErr
		}
		if memory == nil {
			memory = &domain.CharacterAgentMemory{Version: domain.CharacterAgentMemoryVersion, AgentID: active.AgentID, Character: active.Character, State: "canonical"}
		}
		added := false
		for _, factText := range factsByName[strings.ToLower(strings.Join(strings.Fields(active.Character), " "))] {
			factText = strings.TrimSpace(factText)
			if factText == "" {
				continue
			}
			factSum := sha256.Sum256([]byte(fmt.Sprintf("accepted-review.v1\x00%s\x00%d\x00%s", active.AgentID, chapter, factText)))
			factID := "mem_" + hex.EncodeToString(factSum[:8])
			duplicate := false
			for _, existing := range memory.Facts {
				if existing.ID == factID {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			memory.Facts = append(memory.Facts, domain.CharacterAgentMemoryFact{ID: factID, Chapter: chapter, Kind: "accepted_continuity", Text: factText, SourceDigest: sourceDigest, Accepted: true})
			added = true
		}
		memory.LastAcceptedChapter = max(memory.LastAcceptedChapter, chapter)
		memory.UpdatedAt = acceptedAt
		if err := s.CharacterAgents.SaveCanonicalMemory(*memory); err != nil {
			return err
		}
		for i := range registry.Entries {
			if registry.Entries[i].AgentID == active.AgentID {
				registry.Entries[i].Status = domain.CharacterAgentSleeping
				registry.Entries[i].LastActivatedChapter = max(registry.Entries[i].LastActivatedChapter, chapter)
				registry.Entries[i].UpdatedAt = acceptedAt
				if added {
					registry.Entries[i].MemoryVersion++
				}
				registryChanged = true
				break
			}
		}
	}
	if registryChanged {
		return s.CharacterAgents.SaveRegistry(*registry)
	}
	return nil
}

func (s *CharacterAgentStore) writeImmutable(path string, value any) error {
	incoming, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return s.io.WithWriteLock(func() error {
		verifyExisting := func() error {
			info, err := os.Lstat(s.io.path(path))
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("immutable character-agent artifact is not a regular file at %s", path)
			}
			raw, err := s.io.ReadFileUnlocked(path)
			if err != nil {
				return err
			}
			// A matching stored digest is only a claim; compare the actual payload
			// so retries cannot silently accept damaged evidence with an old hash.
			if !sameJSON(raw, incoming) {
				return fmt.Errorf("immutable character-agent artifact already exists at %s", path)
			}
			return nil
		}
		if err := verifyExisting(); !os.IsNotExist(err) {
			return err
		}
		// IO locks are instance-local. Publish with no-replace semantics so two
		// Store instances/processes can never overwrite each other's decisions.
		if err := s.io.writeFileNoReplaceUnlocked(path, incoming); err != nil {
			if os.IsExist(err) {
				return verifyExisting()
			}
			return err
		}
		return nil
	})
}
