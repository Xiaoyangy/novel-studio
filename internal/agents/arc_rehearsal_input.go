package agents

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// BuildArcRehearsalInput only reads authored foundation and accepted state.
// It never reads preplan projections, draft plans or its own rehearsal files.
func BuildArcRehearsalInput(st *store.Store, binding domain.ArcRehearsalInput) (domain.ArcRehearsalInput, error) {
	input := domain.ArcRehearsalInput{Version: domain.ArcRehearsalVersion, ArcID: binding.ArcID, ArcFirstChapter: binding.ArcFirstChapter, ArcLastChapter: binding.ArcLastChapter, BaseCanonChapter: binding.BaseCanonChapter, BaseCanonRoot: binding.BaseCanonRoot, SourceRoot: binding.SourceRoot, SourceFiles: map[string]string{}}
	if st == nil {
		return input, fmt.Errorf("rehearsal requires a source Store")
	}
	protocol, err := ArcRehearsalProtocolDigest()
	if err != nil {
		return input, err
	}
	input.ProtocolDigest = protocol
	read := func(rel string, out any) error {
		raw, err := os.ReadFile(filepath.Join(st.Dir(), rel))
		if err != nil {
			return fmt.Errorf("read rehearsal source %s: %w", rel, err)
		}
		input.SourceFiles[rel] = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode rehearsal source %s: %w", rel, err)
		}
		return nil
	}
	var characters []domain.Character
	var outline []domain.OutlineEntry
	var compass domain.StoryCompass
	var userRules rules.Snapshot
	for _, source := range []struct {
		path string
		out  any
	}{{"characters.json", &characters}, {"outline.json", &outline}, {"world_rules.json", &input.WorldRules}, {"world_codex.json", &input.WorldCodex}, {"book_world.json", &input.BookWorld}, {"meta/compass.json", &compass}, {"meta/user_rules.json", &userRules}} {
		if err := read(source.path, source.out); err != nil {
			return input, err
		}
	}
	if successor, err := st.CharacterAgents.LoadCurrentSuccessorPlan(); err != nil {
		return input, err
	} else if successor != nil && successor.BaseCanonChapter == input.BaseCanonChapter && successor.ArcFirstChapter == input.ArcFirstChapter && successor.ArcLastChapter == input.ArcLastChapter {
		if successor.AcceptedCanonRoot != input.BaseCanonRoot {
			return input, fmt.Errorf("rehearsal successor has a foreign accepted canon root")
		}
		if err := read("meta/character_agents/successors/current.json", nil); err != nil {
			return input, err
		}
		if err := read(filepath.ToSlash(filepath.Join("meta/character_agents/successors", successor.ParentGenerationID, strings.TrimPrefix(successor.Digest, "sha256:")+".json")), nil); err != nil {
			return input, err
		}
		for i := range outline {
			for _, revised := range successor.RevisedChapters {
				if revised.Chapter == outline[i].Chapter {
					outline[i] = revised
				}
			}
		}
	}
	for _, entry := range outline {
		if entry.Chapter >= input.ArcFirstChapter && entry.Chapter <= input.ArcLastChapter {
			input.Outline = append(input.Outline, entry)
		}
	}
	sort.Slice(input.Outline, func(i, j int) bool { return input.Outline[i].Chapter < input.Outline[j].Chapter })
	input.HardContracts = compactAgentStrings(append([]string{compass.EndingDirection}, compass.NonNegotiables...))
	if userRules.Status != rules.StatusReady {
		return input, fmt.Errorf("arc rehearsal requires ready normalized user rules")
	}
	input.UserRules, _ = json.Marshal(map[string]any{"structured": userRules.Structured, "preferences": userRules.Preferences})
	if words := userRules.Structured.ChapterWords; words != nil {
		input.HardContracts = append(input.HardContracts, fmt.Sprintf("每章正文%d—%d字（meta/user_rules.json:structured.chapter_words）", words.Min, words.Max))
	}
	registry := domain.CharacterAgentRegistry{Version: domain.CharacterAgentRegistryVersion}
	if input.BaseCanonChapter > 0 {
		stored, err := st.CharacterAgents.LoadRegistry()
		if err != nil {
			return input, err
		}
		if stored == nil {
			return input, fmt.Errorf("accepted rehearsal baseline lacks its character registry")
		}
		registry = *stored
	} else {
		for _, character := range characters {
			if character.InitialState == nil {
				continue
			}
			updated, _, err := registry.UpsertCharacter(character.Name, character.Aliases, character.Tier, 0, "")
			if err != nil {
				return input, err
			}
			registry = updated
		}
	}
	currentChoices := map[string]domain.CharacterDecisionProposal{}
	if input.BaseCanonChapter == 0 {
		state, err := domain.BuildWorldPhysicalStateFromInitialV2(characters, registry)
		if err != nil {
			return input, err
		}
		input.WorldState = &state
	} else {
		bundle, err := st.LoadAcceptedCharacterAgentBundle(input.BaseCanonChapter)
		if err != nil {
			return input, err
		}
		input.WorldState = bundle.ChapterWorldSimulation.PhysicalState
		if bundle.CharacterActivationEvidence != nil {
			for _, cycle := range bundle.CharacterActivationEvidence.Cycles {
				for _, p := range domain.LatestCharacterCycleProposals(cycle.Evidence) {
					currentChoices[p.AgentID] = p
				}
			}
		} else if bundle.CharacterAgentEvidence != nil {
			for _, p := range domain.LatestCharacterCycleProposals(*bundle.CharacterAgentEvidence) {
				currentChoices[p.AgentID] = p
			}
		}
		input.AcceptedEvidence = map[int]string{}
		for chapter := input.ArcFirstChapter; chapter <= input.BaseCanonChapter; chapter++ {
			var summary domain.ChapterSummary
			if err := read(fmt.Sprintf("summaries/%02d.json", chapter), &summary); err != nil {
				return input, err
			}
			bodyPath := fmt.Sprintf("chapters/%02d.md", chapter)
			if err := read(bodyPath, nil); err != nil {
				return input, err
			}
			input.AcceptedSummaries = append(input.AcceptedSummaries, summary)
			input.AcceptedEvidence[chapter] = input.SourceFiles[bodyPath]
		}
	}
	for _, character := range characters {
		if character.Tier == "decorative" || character.InitialState == nil {
			continue
		}
		record, ok := registry.Resolve(character.Name)
		if !ok {
			return input, fmt.Errorf("rehearsal character lacks current identity: %s", character.Name)
		}
		var current *domain.CharacterPhysicalStateV2
		for i := range input.WorldState.Actors {
			if input.WorldState.Actors[i].AgentID == record.AgentID {
				current = &input.WorldState.Actors[i]
			}
		}
		if current == nil {
			return input, fmt.Errorf("rehearsal character lacks accepted physical state: %s", character.Name)
		}
		views, err := domain.BuildCharacterResourceViewsV2(*input.WorldState, record.AgentID)
		if err != nil {
			return input, err
		}
		o := domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, GenerationID: "arc_rehearsal", Chapter: input.BaseCanonChapter + 1, Round: 1, AgentID: record.AgentID, Character: record.Character, Location: current.Location, CurrentGoal: character.InitialState.CurrentGoal, Pressure: character.InitialState.Pressure, StimulusDigest: input.SourceRoot, ResourceViews: views}
		o.KnownFacts = []domain.CharacterAgentFact{newCharacterAgentFact("self_profile", "身份："+character.Name+"；岗位："+character.Role, "characters.json", "private")}
		if input.BaseCanonChapter == 0 {
			for _, fact := range character.InitialState.KnownFacts {
				o.KnownFacts = append(o.KnownFacts, newCharacterAgentFact("initial_known", fact, "characters.json#initial_state", "private"))
			}
		} else {
			var memory domain.CharacterAgentMemory
			if err := read("meta/character_agents/memory/"+record.AgentID+".json", &memory); err != nil {
				return input, err
			}
			valid, err := domain.FinalizeCharacterAgentMemory(memory)
			if err != nil || valid.MemoryRoot != memory.MemoryRoot || memory.State != "canonical" || memory.LastAcceptedChapter > input.BaseCanonChapter {
				return input, fmt.Errorf("rehearsal lacks verified accepted memory for %s", character.Name)
			}
			o.Memory, o.MemoryRoot = memory.Facts, memory.MemoryRoot
			if p, ok := currentChoices[record.AgentID]; ok {
				o.CurrentGoal, o.Pressure = p.CurrentGoal, p.Pressure
			}
		}
		for _, rule := range input.WorldRules {
			if view := strings.TrimSpace(rule.CharacterView); view != "" && domain.WorldRuleVisibility(rule) != "secret" {
				o.PublicRules = append(o.PublicRules, newCharacterAgentFact("world_rule_view", view, "world_rules.json", domain.WorldRuleVisibility(rule)))
			}
		}
		if input.WorldCodex != nil {
			for _, mechanism := range input.WorldCodex.Mechanisms {
				if visible, ok := characterFacingMechanism(mechanism); ok {
					o.PublicMechanisms = append(o.PublicMechanisms, visible)
				}
			}
		}
		o, err = domain.FinalizeCharacterObservationPacket(o)
		if err != nil {
			return input, err
		}
		input.CharacterObservations = append(input.CharacterObservations, o)
	}
	sort.Slice(input.CharacterObservations, func(i, j int) bool {
		return input.CharacterObservations[i].AgentID < input.CharacterObservations[j].AgentID
	})
	return domain.FinalizeArcRehearsalInput(input)
}
