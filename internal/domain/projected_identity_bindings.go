package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateProjectedIdentityBindingsV2 checks mechanically provable compiler
// identities. Free-text actions/permission/timing are separately grounded
// against arbitration; this does not infer them from prose or erase text.
// Historical bundles without the new policy retain their original verifier.
func ValidateProjectedIdentityBindingsV2(bundle ProjectedChapterBundle) error {
	sim, pov, formal := bundle.ChapterWorldSimulation, bundle.POVPlan, bundle.FormalWorldSimulation
	if !HasPlanGroundingPolicy(sim) {
		return nil
	}
	protagonist := strings.TrimSpace(sim.ProtagonistProjection.Protagonist)
	actors := map[string]CharacterWorldDecision{}
	for _, actor := range sim.CharacterDecisions {
		name := strings.TrimSpace(actor.Character)
		if name == "" {
			return fmt.Errorf("projection identity: simulation has an unnamed actor")
		}
		if _, duplicate := actors[name]; duplicate {
			return fmt.Errorf("projection identity: duplicate simulation actor")
		}
		actors[name] = actor
	}
	povActor, exists := actors[protagonist]
	if !exists || pov.POVCharacterID != protagonist {
		return fmt.Errorf("projection identity: POV character does not match the simulated protagonist")
	}
	povLocation := strings.TrimSpace(povActor.Location)
	if sim.PhysicalState != nil {
		if povActor.PostState == nil || povActor.PostState.Character != protagonist {
			return fmt.Errorf("projection identity: POV has no matching physical post-state")
		}
		povLocation = strings.TrimSpace(povActor.PostState.Location)
	}
	if povLocation == "" {
		return fmt.Errorf("projection identity: protagonist location is unknown")
	}
	seen := map[string]bool{}
	for _, motivation := range pov.Motivations {
		actor, exists := actors[motivation.CharacterID]
		if !exists || seen[motivation.CharacterID] || motivation.Choice != strings.TrimSpace(actor.Decision) {
			return fmt.Errorf("projection identity: actor motivation choice differs from that actor's arbitrated decision")
		}
		seen[motivation.CharacterID] = true
	}
	if len(seen) != len(actors) {
		return fmt.Errorf("projection identity: actor motivations do not cover the simulation")
	}
	seen = map[string]bool{}
	for _, actor := range formal.Actors {
		if _, exists := actors[actor.CharacterID]; !exists || seen[actor.CharacterID] {
			return fmt.Errorf("projection identity: formal actor identity is unknown or duplicated")
		}
		seen[actor.CharacterID] = true
	}
	if len(seen) != len(actors) {
		return fmt.Errorf("projection identity: formal actor coverage differs from simulation")
	}
	initialOwners := map[string]string{}
	initialLocations := map[string]string{}
	for _, fact := range formal.InitialConditions {
		if fact.ID == "" || initialOwners[fact.ID] != "" {
			return fmt.Errorf("projection identity: formal initial fact identity is empty or duplicated")
		}
		actor, exists := actors[fact.Subject]
		if !exists {
			return fmt.Errorf("projection identity: formal initial fact has an unknown actor")
		}
		initialOwners[fact.ID] = fact.Subject
		if fact.Field != "location" {
			continue
		}
		if _, duplicate := initialLocations[fact.Subject]; duplicate {
			return fmt.Errorf("projection identity: actor has duplicate initial locations")
		}
		location := actor.Location
		if sim.PhysicalState != nil {
			location = ""
			stimulus := bundle.CharacterOpeningStimulus()
			if stimulus == nil || stimulus.PhysicalState == nil || actor.PostState == nil {
				return fmt.Errorf("projection identity: initial physical source is missing")
			}
			for _, before := range stimulus.PhysicalState.Actors {
				if before.AgentID == actor.PostState.AgentID && before.Character == fact.Subject {
					location = before.Location
				}
			}
		}
		if location == "" || fact.Value != strings.TrimSpace(location) {
			return fmt.Errorf("projection identity: initial location differs from its actor's exact pre-state")
		}
		initialLocations[fact.Subject] = fact.Value
	}
	if len(initialLocations) != len(actors) {
		return fmt.Errorf("projection identity: initial actor location coverage is incomplete")
	}
	seen = map[string]bool{}
	for _, step := range formal.CausalSteps {
		actor, exists := actors[step.ActorID]
		if !exists || seen[step.ActorID] || step.Decision != strings.TrimSpace(actor.Decision) {
			return fmt.Errorf("projection identity: formal causal choice differs from its actor")
		}
		seen[step.ActorID] = true
		for _, cause := range step.CauseIDs {
			if initialOwners[cause] != step.ActorID {
				return fmt.Errorf("projection identity: formal causal origin belongs to another actor")
			}
		}
	}
	if len(seen) != len(actors) {
		return fmt.Errorf("projection identity: causal actor coverage differs from simulation")
	}
	expectedTime := strings.TrimSpace(sim.TimeWindow)
	if formal.TimeAdvance != expectedTime || pov.TimeAdvance != expectedTime {
		return fmt.Errorf("projection identity: formal/POV time differs from actual simulation time")
	}
	// Dialogue blueprints carry explicit anchors. Only absent anchors use the
	// protagonist's own current location; no other actor can fill that gap.
	explicitAnchors := map[string]string{}
	for _, scene := range bundle.ChapterPlan.CausalSimulation.DialogueBlueprints {
		id := strings.TrimSpace(scene.SceneID)
		if id == "" {
			hash, err := DeterministicPlanningHash([]string{"scene", strconv.Itoa(bundle.Chapter), scene.LocationAnchor})
			if err != nil {
				return err
			}
			id = "v2:" + strings.TrimPrefix(hash, PlanningV2DigestPrefix)[:20]
		}
		explicitAnchors[id] = strings.TrimSpace(scene.LocationAnchor)
	}
	for _, scene := range pov.Scenes {
		location := explicitAnchors[scene.SceneID]
		if location == "" {
			location = povLocation
		}
		if scene.Location != location {
			return fmt.Errorf("projection identity: default POV scene borrowed a non-POV location")
		}
		if scene.Time != expectedTime {
			return fmt.Errorf("projection identity: POV scene time differs from actual simulation window")
		}
		for _, name := range scene.PresentActors {
			if _, exists := actors[name]; !exists {
				return fmt.Errorf("projection identity: POV scene contains an unknown named actor")
			}
		}
	}
	return nil
}
