package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func (s *Store) PrepareAcceptedCharacterMemoryCandidate(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) (*CharacterMemoryPublicationCandidate, error) {
	if err := validateCharacterMemoryCandidateSource(bundle, outcome); err != nil {
		return nil, err
	}
	p := s.ProjectedV2()
	if err := validateCharacterMemoryPublicationPath(p.io, projectedWriteLockFile); err != nil {
		return nil, err
	}
	return withProjectedReadResult(p, func() (*CharacterMemoryPublicationCandidate, error) {
		var before []CharacterMemoryPublicationFile
		existing, err := p.loadCharacterMemoryPublicationUnlocked(outcome.ReceiptDigest)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.BundleDigest != bundle.BundleDigest || existing.GenerationID != bundle.GenerationID || existing.Chapter != bundle.Chapter || !jsonValuesEqual(existing.Outcome, outcome) {
				return nil, fmt.Errorf("prepared character memory candidate belongs to another exact outcome/bundle")
			}
			before = existing.Files
		} else {
			paths, err := characterMemoryCandidatePaths(bundle)
			if err != nil {
				return nil, err
			}
			for _, path := range paths {
				raw, exists, err := readCharacterMemoryPublicationFile(p.io, path)
				if err != nil {
					return nil, err
				}
				file := CharacterMemoryPublicationFile{Path: path, BeforeExists: exists, Before: raw}
				if exists {
					file.BeforeSHA256 = characterMemoryPublicationSHA(raw)
				}
				before = append(before, file)
			}
		}
		files, err := deriveCharacterMemoryPublicationFiles(bundle, outcome, before)
		if err != nil {
			return nil, err
		}
		if existing != nil && !reflect.DeepEqual(files, existing.Files) {
			return nil, fmt.Errorf("prepared character memory after-state differs from deterministic accepted derivation")
		}
		return &CharacterMemoryPublicationCandidate{Version: characterMemoryPublicationVersion, GenerationID: bundle.GenerationID, Chapter: bundle.Chapter, BundleDigest: bundle.BundleDigest, OutcomeReceiptDigest: outcome.ReceiptDigest, OutcomeActualCanonRoot: outcome.ActualCanonRoot, Outcome: outcome, Files: files}, nil
	})
}

func validateCharacterMemoryCandidateSource(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) error {
	if bundle.ChapterWorldSimulation.Version < 2 || !bundle.HasCharacterEvidence() {
		return fmt.Errorf("character memory publication requires sealed independent character evidence")
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return err
	}
	if err := domain.ValidateActualOutcomeReceiptV2(outcome); err != nil {
		return err
	}
	if !outcome.ProjectionMatch || outcome.GenerationID != bundle.GenerationID || outcome.Chapter != bundle.Chapter || outcome.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot || outcome.ActualPostStateRoot != bundle.ProjectedPostStateRoot {
		return fmt.Errorf("character memory candidate outcome does not match sealed chapter")
	}
	actual, err := domain.ComputeProjectedDeltaV2Digest(outcome.ActualDelta)
	if err != nil {
		return err
	}
	projected, err := domain.ComputeProjectedDeltaV2Digest(bundle.ProjectedDelta)
	if err != nil {
		return err
	}
	if actual != projected {
		return fmt.Errorf("character memory candidate actual delta differs from sealed projection")
	}
	return nil
}

func characterMemoryCandidatePaths(bundle domain.ProjectedChapterBundle) ([]string, error) {
	if bundle.CharacterActivationEvidence != nil {
		if err := domain.ValidateCharacterActivationSimulation(bundle.ChapterWorldSimulation, *bundle.CharacterActivationEvidence); err != nil {
			return nil, err
		}
		ids := map[string]bool{}
		for _, cycle := range bundle.CharacterActivationEvidence.Cycles {
			last := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
			for _, resolution := range last.Resolutions {
				ids[resolution.AgentID] = true
			}
			for _, reception := range last.PassiveReceptions {
				ids[reception.ToAgentID] = true
			}
		}
		paths := []string{characterAgentRegistryPath()}
		for id := range ids {
			if err := validateCharacterAgentPathComponent("agent_id", id); err != nil {
				return nil, err
			}
			paths = append(paths, characterAgentMemoryPath(id))
		}
		sort.Strings(paths)
		return paths, nil
	}
	if bundle.CharacterAgentEvidence == nil || len(bundle.CharacterAgentEvidence.Arbitrations) == 0 {
		return nil, fmt.Errorf("character memory candidate lacks final arbitration")
	}
	last := bundle.CharacterAgentEvidence.Arbitrations[len(bundle.CharacterAgentEvidence.Arbitrations)-1]
	paths := []string{characterAgentRegistryPath()}
	seen := map[string]bool{paths[0]: true}
	for _, resolution := range last.Resolutions {
		if err := validateCharacterAgentPathComponent("agent_id", resolution.AgentID); err != nil {
			return nil, err
		}
		path := characterAgentMemoryPath(resolution.AgentID)
		if seen[path] {
			return nil, fmt.Errorf("duplicate accepted character memory target")
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// This pure derivation reads only immutable before snapshots and accepted
// evidence. Load/Inspect can re-run it to reject a re-signed invented after.
func deriveCharacterMemoryPublicationFiles(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2, beforeFiles []CharacterMemoryPublicationFile) ([]CharacterMemoryPublicationFile, error) {
	if err := validateCharacterMemoryCandidateSource(bundle, outcome); err != nil {
		return nil, err
	}
	paths, err := characterMemoryCandidatePaths(bundle)
	if err != nil {
		return nil, err
	}
	if len(paths) != len(beforeFiles) {
		return nil, fmt.Errorf("character memory candidate must snapshot registry and every resolved actor")
	}
	before := make(map[string]CharacterMemoryPublicationFile, len(beforeFiles))
	for _, file := range beforeFiles {
		if _, duplicate := before[file.Path]; duplicate {
			return nil, fmt.Errorf("duplicate character memory before snapshot")
		}
		if file.BeforeExists {
			if len(file.Before) == 0 || file.BeforeSHA256 != characterMemoryPublicationSHA(file.Before) {
				return nil, fmt.Errorf("character memory before snapshot digest mismatch")
			}
		} else if len(file.Before) > 0 || file.BeforeSHA256 != "" {
			return nil, fmt.Errorf("absent character memory before snapshot contains bytes")
		}
		before[file.Path] = CharacterMemoryPublicationFile{Path: file.Path, BeforeExists: file.BeforeExists, Before: append([]byte(nil), file.Before...), BeforeSHA256: file.BeforeSHA256}
	}
	for _, path := range paths {
		if _, ok := before[path]; !ok {
			return nil, fmt.Errorf("character memory candidate is missing required target %s", path)
		}
	}
	if bundle.CharacterActivationEvidence != nil {
		return deriveActivationMemoryPublicationFiles(bundle, outcome, paths, before)
	}
	evidence := bundle.CharacterAgentEvidence
	final := evidence.Arbitrations[len(evidence.Arbitrations)-1]
	latest := map[string]domain.CharacterDecisionProposal{}
	for _, proposal := range evidence.Proposals {
		if current, ok := latest[proposal.AgentID]; !ok || proposal.Round > current.Round {
			latest[proposal.AgentID] = proposal
		}
	}
	registryFile := before[characterAgentRegistryPath()]
	var registry domain.CharacterAgentRegistry
	registryChanged := !registryFile.BeforeExists
	if registryFile.BeforeExists {
		if err := decodeCharacterMemoryCandidateJSON(registryFile.Before, &registry); err != nil {
			return nil, err
		}
		root := registry.RegistryRoot
		registry, err = domain.FinalizeCharacterAgentRegistry(registry)
		if err != nil {
			return nil, err
		}
		if root == "" || root != registry.RegistryRoot {
			return nil, fmt.Errorf("canonical registry before snapshot has invalid root")
		}
	} else {
		registry = evidence.Registry
		registry.Entries = cloneCharacterAgentRecords(evidence.Registry.Entries)
		registry.RegistryRoot = ""
		for i := range registry.Entries {
			if registry.Entries[i].Status != domain.CharacterAgentRetired {
				registry.Entries[i].Status = domain.CharacterAgentSleeping
			}
			registry.Entries[i].UpdatedAt = outcome.AcceptedAt
		}
	}
	known := map[string]bool{}
	for _, entry := range registry.Entries {
		known[entry.AgentID] = true
	}
	for _, entry := range cloneCharacterAgentRecords(evidence.Registry.Entries) {
		if known[entry.AgentID] {
			continue
		}
		entry.Status = domain.CharacterAgentSleeping
		entry.UpdatedAt = outcome.AcceptedAt
		registry.Entries = append(registry.Entries, entry)
		known[entry.AgentID] = true
		registryChanged = true
	}
	after := map[string][]byte{}
	for _, resolution := range final.Resolutions {
		proposal, ok := latest[resolution.AgentID]
		if !ok {
			return nil, fmt.Errorf("accepted arbitration has no final actor proposal")
		}
		file := before[characterAgentMemoryPath(resolution.AgentID)]
		memory := domain.CharacterAgentMemory{Version: domain.CharacterAgentMemoryVersion, AgentID: resolution.AgentID, Character: resolution.Character, State: "canonical"}
		if file.BeforeExists {
			if err := decodeCharacterMemoryCandidateJSON(file.Before, &memory); err != nil {
				return nil, err
			}
			root := memory.MemoryRoot
			memory, err = domain.FinalizeCharacterAgentMemory(memory)
			if err != nil {
				return nil, err
			}
			if root == "" || root != memory.MemoryRoot || memory.AgentID != resolution.AgentID || memory.State != "canonical" || memory.GenerationID != "" || memory.LastAcceptedChapter > outcome.Chapter {
				return nil, fmt.Errorf("canonical memory before snapshot has invalid identity/root/chapter")
			}
		}
		text := strings.TrimSpace("决定：" + proposal.Decision + "；行动：" + proposal.IntendedAction + "；实际结果：" + resolution.ImmediateResult + "；后态：" + resolution.StateAfter)
		if final.Version == domain.WorldArbitrationReceiptV2Version {
			if bundle.ChapterWorldSimulation.PhysicalState == nil {
				return nil, fmt.Errorf("accepted physical memory lacks world state")
			}
			text, err = domain.CharacterPrivateOutcomeV2(proposal, resolution, *bundle.ChapterWorldSimulation.PhysicalState, final)
			if err != nil {
				return nil, err
			}
		}
		sum := sha256.Sum256([]byte("accepted-character-decision.v1\x00" + outcome.ReceiptDigest + "\x00" + resolution.AgentID))
		fact := domain.CharacterAgentMemoryFact{ID: "mem_" + hex.EncodeToString(sum[:8]), Chapter: bundle.Chapter, Kind: "accepted_decision_outcome", Text: text, SourceDigest: outcome.ReceiptDigest, KnowledgeRefs: append([]string(nil), proposal.KnowledgeRefs...), Accepted: true}
		probe, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: resolution.AgentID, Character: resolution.Character, State: "canonical", Facts: []domain.CharacterAgentMemoryFact{fact}})
		if err != nil {
			return nil, err
		}
		fact = probe.Facts[0]
		present := false
		for _, existing := range memory.Facts {
			if existing.ID != fact.ID {
				continue
			}
			if !reflect.DeepEqual(existing, fact) {
				return nil, fmt.Errorf("accepted character fact id already contains different outcome content")
			}
			present = true
		}
		if present {
			after[file.Path] = append([]byte(nil), file.Before...)
			continue
		}
		memory.Facts = append(memory.Facts, fact)
		memory.LastAcceptedChapter = max(memory.LastAcceptedChapter, bundle.Chapter)
		memory.UpdatedAt = outcome.AcceptedAt
		memory, err = domain.FinalizeCharacterAgentMemory(memory)
		if err != nil {
			return nil, err
		}
		after[file.Path], err = json.MarshalIndent(memory, "", "  ")
		if err != nil {
			return nil, err
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
	if registryChanged {
		registry, err = domain.FinalizeCharacterAgentRegistry(registry)
		if err != nil {
			return nil, err
		}
		after[registryFile.Path], err = json.MarshalIndent(registry, "", "  ")
		if err != nil {
			return nil, err
		}
	} else {
		after[registryFile.Path] = append([]byte(nil), registryFile.Before...)
	}
	result := make([]CharacterMemoryPublicationFile, 0, len(paths))
	for _, path := range paths {
		file := before[path]
		file.After = after[path]
		if len(file.After) == 0 {
			return nil, fmt.Errorf("character memory derivation produced an empty target")
		}
		file.AfterSHA256 = characterMemoryPublicationSHA(file.After)
		result = append(result, file)
	}
	return result, nil
}

func decodeCharacterMemoryCandidateJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("character memory snapshot has trailing JSON")
	}
	return nil
}
