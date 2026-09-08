package domain

import (
	"fmt"
	"slices"
	"strings"
)

const CharacterSurfaceInspectionPolicyV1 = "character-surface-inspection:receipt.v1"
const CharacterSurfaceContainerExteriorV1 = "container_exterior"
const CharacterSurfaceSealExteriorV1 = "seal_exterior"

func HasCharacterSurfaceInspectionPolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterSurfaceInspectionPolicyV1)
}

func IsCharacterInspectableSurfaceV1(surface string) bool {
	return surface == CharacterSurfaceContainerExteriorV1 || surface == CharacterSurfaceSealExteriorV1
}

// These are inspectable physical faces, never pre-populated current values.
func ValidateInspectableSurfacesV1(surfaces []string) error {
	if len(surfaces) > 2 {
		return fmt.Errorf("inspectable_surfaces permits only the two explicit exterior facets")
	}
	seen := map[string]bool{}
	for _, surface := range surfaces {
		if !IsCharacterInspectableSurfaceV1(surface) || seen[surface] {
			return fmt.Errorf("inspectable_surfaces requires unique container_exterior/seal_exterior definitions, not current states or hidden contents")
		}
		seen[surface] = true
	}
	return nil
}

func HasInspectableSurfaceV1(resource WorldResourceBalanceV2, surface string) bool {
	return IsCharacterInspectableSurfaceV1(surface) && slices.Contains(resource.InspectableSurfaces, surface)
}

func surfaceInspectionResultV1(result string) bool {
	return result == "no_visible_damage" || result == "visible_damage" || result == "indeterminate"
}

func validOperationalResultForSurfaceV1(surface, result string) bool {
	if surface == "" {
		return operationalResultV1(result)
	}
	return IsCharacterInspectableSurfaceV1(surface) && surfaceInspectionResultV1(result)
}

func operationalRequestResourceV1(resource WorldResourceBalanceV2, surface string) bool {
	if surface == "" {
		return operationalResourceV1(resource)
	}
	return HasInspectableSurfaceV1(resource, surface)
}

// The original builder deliberately remains unchanged for frozen producers.
func BuildCharacterResourceViewsForSourcesV2(state WorldPhysicalStateV2, agentID string, sources []string) ([]CharacterResourceViewV2, error) {
	views, err := BuildCharacterResourceViewsV2(state, agentID)
	if err != nil || !HasCharacterSurfaceInspectionPolicyV1(sources) {
		return views, err
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range state.Resources {
		catalog[resource.ResourceID] = resource
	}
	for i := range views {
		if views[i].Access != "none" && views[i].Perception.Kind != "unaware" {
			views[i].InspectableSurfaces = append([]string(nil), catalog[views[i].ResourceID].InspectableSurfaces...)
		}
	}
	return views, nil
}

func validateSurfaceInspectionStimulusV1(stimulus WorldStimulusPacket) error {
	policy := HasCharacterSurfaceInspectionPolicyV1(stimulus.Sources)
	if policy && (stimulus.Version != WorldStimulusPacketV2Version || !HasCharacterOperationalAvailabilityPolicyV1(stimulus.Sources) || !HasCharacterSelfExperiencePolicyV2(stimulus.Sources)) {
		return fmt.Errorf("surface inspections require explicit v2 operational/self-experience policies")
	}
	if stimulus.PhysicalState != nil && !policy {
		for _, actor := range stimulus.PhysicalState.Actors {
			for _, observation := range actor.OperationalObservations {
				if observation.Surface != "" {
					return fmt.Errorf("surface observation history requires its frozen surface policy")
				}
			}
		}
	}
	return nil
}

func validateSurfaceInspectionObservationV1(observation CharacterObservationPacket) error {
	policy := HasCharacterSurfaceInspectionPolicyV1(observation.Sources)
	for _, view := range observation.ResourceViews {
		if len(view.InspectableSurfaces) > 0 && (!policy || view.Access == "none" || view.Perception.Kind == "unaware") {
			return fmt.Errorf("surface capabilities require an enabled, known accessible owner view")
		}
		if err := ValidateInspectableSurfacesV1(view.InspectableSurfaces); err != nil {
			return err
		}
	}
	for _, result := range observation.OperationalObservations {
		if result.Surface != "" && !policy {
			return fmt.Errorf("owner surface observations require the explicit surface policy")
		}
	}
	return nil
}

func formatSurfaceInspectionV1(observation CharacterOperationalObservationV1) string {
	face := map[string]string{CharacterSurfaceContainerExteriorV1: "容器外表", CharacterSurfaceSealExteriorV1: "封条外表"}[observation.Surface]
	result := map[string]string{"no_visible_damage": "当时外观未见明显破损", "visible_damage": "当时外观可见破损或断开", "indeterminate": "当时无法判定外观状况"}[observation.Result]
	return fmt.Sprintf("本人表面检查（%s；%s；第%d章T+%.6g分钟；%s）：%s；仅为该次实际检查的有限外观结果，不证明从未开启、历史篡改、内部内容、数量或任何许可；申请用途仍是本人意图：%s", observation.ResourceLabel, face, observation.Chapter, *observation.ObservedAtDay*1440, observation.ID, result, strings.TrimSpace(observation.Purpose))
}
