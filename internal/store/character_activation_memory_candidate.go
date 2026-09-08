package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

type activationMemoryFactGroup struct {
	Character string
	Facts     []domain.CharacterAgentMemoryFact
}

func activationMemoryFactGroups(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2) (map[string]activationMemoryFactGroup, error) {
	if bundle.CharacterActivationEvidence == nil {
		return nil, fmt.Errorf("activation memory requires full chapter evidence")
	}
	if err := domain.ValidateCharacterActivationSimulation(bundle.ChapterWorldSimulation, *bundle.CharacterActivationEvidence); err != nil {
		return nil, err
	}
	groups := map[string]activationMemoryFactGroup{}
	add := func(id, character, kind, source, text string, refs []string) {
		sum := sha256.Sum256([]byte(kind + "\x00" + outcome.ReceiptDigest + "\x00" + source + "\x00" + id))
		group := groups[id]
		group.Character = character
		group.Facts = append(group.Facts, domain.CharacterAgentMemoryFact{ID: "mem_" + hex.EncodeToString(sum[:8]), Chapter: bundle.Chapter, Kind: kind, Text: text, SourceDigest: outcome.ReceiptDigest, KnowledgeRefs: append([]string(nil), refs...), Accepted: true})
		groups[id] = group
	}
	var steps []domain.VerifiedCharacterActivationStep
	for _, cycle := range bundle.CharacterActivationEvidence.Cycles {
		if cycle.Version == domain.CharacterActivationCycleV2Version || cycle.Version == domain.CharacterActivationCycleV3Version {
			var err error
			steps, err = domain.VerifiedStepsForCharacterActivationChapter(*bundle.CharacterActivationEvidence)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	for i, cycle := range bundle.CharacterActivationEvidence.Cycles {
		receipt := cycle.Evidence.Arbitrations[len(cycle.Evidence.Arbitrations)-1]
		var proposals []domain.CharacterDecisionProposal
		var state domain.WorldPhysicalStateV2
		var err error
		if len(steps) > 0 {
			proposals, state = steps[i].EffectiveProposals(), steps[i].AfterState()
		} else {
			proposals = domain.LatestCharacterCycleProposals(cycle.Evidence)
			state, err = domain.ApplyArbitrationPhysicalStateV2(receipt, cycle.Evidence.Stimulus, proposals...)
		}
		if err != nil {
			return nil, err
		}
		for _, resolution := range receipt.Resolutions {
			for _, proposal := range proposals {
				if proposal.AgentID != resolution.AgentID {
					continue
				}
				text, err := domain.CharacterActivationPrivateOutcome(proposal, resolution, state, receipt)
				if err != nil {
					return nil, err
				}
				add(proposal.AgentID, proposal.Character, "accepted_cycle_decision_outcome", cycle.Digest, text, proposal.KnowledgeRefs)
			}
		}
		for _, reception := range receipt.PassiveReceptions {
			for _, actor := range state.Actors {
				if actor.AgentID != reception.ToAgentID {
					continue
				}
				for _, fact := range actor.ReceivedFacts {
					if fact.SourceType != "communication" || fact.FromAgentID != reception.FromAgentID || fact.SourceProposalDigest != reception.SourceProposalDigest || fact.SourceID != reception.CommunicationID {
						continue
					}
					text := "实际收到的" + fact.Kind + "（未自动核实真实性）：" + fact.Text
					if fact.ReceivedAtDay != nil {
						text = fmt.Sprintf("故事开始后%.12g分钟，%s", *fact.ReceivedAtDay*1440, text)
					}
					add(actor.AgentID, actor.Character, "accepted_received_communication", fact.ID, text, []string{fact.ID})
				}
			}
		}
	}
	return groups, nil
}

func deriveActivationMemoryPublicationFiles(bundle domain.ProjectedChapterBundle, outcome domain.ActualOutcomeReceiptV2, paths []string, before map[string]CharacterMemoryPublicationFile) ([]CharacterMemoryPublicationFile, error) {
	groups, err := activationMemoryFactGroups(bundle, outcome)
	if err != nil {
		return nil, err
	}
	sourceRegistry := bundle.CharacterActivationEvidence.Inputs[0].Registry
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
		if root == "" || registry.RegistryRoot != root {
			return nil, fmt.Errorf("activation canonical registry before root mismatch")
		}
	} else {
		registry = sourceRegistry
		registry.Entries = cloneCharacterAgentRecords(sourceRegistry.Entries)
	}
	known := map[string]bool{}
	for i := range registry.Entries {
		entry := &registry.Entries[i]
		known[entry.AgentID] = true
		if !registryFile.BeforeExists {
			if entry.Status != domain.CharacterAgentRetired {
				entry.Status = domain.CharacterAgentSleeping
			}
			entry.UpdatedAt = outcome.AcceptedAt
		}
	}
	for _, entry := range cloneCharacterAgentRecords(sourceRegistry.Entries) {
		if known[entry.AgentID] {
			continue
		}
		entry.Status, entry.UpdatedAt = domain.CharacterAgentSleeping, outcome.AcceptedAt
		registry.Entries = append(registry.Entries, entry)
		registryChanged = true
	}
	after := map[string][]byte{}
	for id, group := range groups {
		path := characterAgentMemoryPath(id)
		file := before[path]
		memory := domain.CharacterAgentMemory{AgentID: id, Character: group.Character, State: "canonical"}
		if file.BeforeExists {
			if err := decodeCharacterMemoryCandidateJSON(file.Before, &memory); err != nil {
				return nil, err
			}
			root := memory.MemoryRoot
			memory, err = domain.FinalizeCharacterAgentMemory(memory)
			if err != nil {
				return nil, err
			}
			if memory.AgentID != id || memory.State != "canonical" || memory.GenerationID != "" || root == "" || memory.MemoryRoot != root || memory.LastAcceptedChapter > bundle.Chapter {
				return nil, fmt.Errorf("activation canonical memory before identity/root mismatch")
			}
		}
		probe, err := domain.FinalizeCharacterAgentMemory(domain.CharacterAgentMemory{AgentID: id, Character: group.Character, State: "canonical", Facts: group.Facts})
		if err != nil {
			return nil, err
		}
		old := map[string]domain.CharacterAgentMemoryFact{}
		for _, fact := range memory.Facts {
			old[fact.ID] = fact
		}
		changed := false
		for _, fact := range probe.Facts {
			if existing, exists := old[fact.ID]; exists {
				if !reflect.DeepEqual(existing, fact) {
					return nil, fmt.Errorf("activation accepted memory fact has conflicting content")
				}
				continue
			}
			memory.Facts = append(memory.Facts, fact)
			changed = true
		}
		if !changed {
			after[path] = append([]byte(nil), file.Before...)
			continue
		}
		memory.LastAcceptedChapter, memory.UpdatedAt = max(memory.LastAcceptedChapter, bundle.Chapter), outcome.AcceptedAt
		memory, err = domain.FinalizeCharacterAgentMemory(memory)
		if err != nil {
			return nil, err
		}
		after[path], err = json.MarshalIndent(memory, "", "  ")
		if err != nil {
			return nil, err
		}
		for i := range registry.Entries {
			if registry.Entries[i].AgentID == id {
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
			return nil, fmt.Errorf("activation memory publication produced an empty file")
		}
		file.AfterSHA256 = characterMemoryPublicationSHA(file.After)
		result = append(result, file)
	}
	return result, nil
}
