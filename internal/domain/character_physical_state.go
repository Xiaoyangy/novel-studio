package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
)

const (
	WorldPhysicalStateV2Version      = "world-physical-state.v2"
	WorldPhysicalStateV2Field        = "physical_state_v2"
	WorldResourceActualAmountV2Field = "actual_amount_v2"
	UnidentifiedResourceNameV2       = "未识别资源"
)

type WorldResourceBalanceV2 struct {
	Artifact            *CharacterWorkArtifactV1 `json:"artifact,omitempty"`
	ResourceID          string                   `json:"resource_id"`
	Name                string                   `json:"name"`
	Unit                string                   `json:"unit"`
	ActualAmount        *float64                 `json:"actual_amount"`
	ReadableFacts       []ResourceReadableFactV2 `json:"readable_facts,omitempty"`
	InspectableSurfaces []string                 `json:"inspectable_surfaces,omitempty"`
	AccessRequiresAny   []string                 `json:"access_requires_any,omitempty"`
}

type ResourcePerceptionV2 struct {
	Kind          string   `json:"kind"`
	Amount        *float64 `json:"amount,omitempty"`
	EstimateMin   *float64 `json:"estimate_min,omitempty"`
	EstimateMax   *float64 `json:"estimate_max,omitempty"`
	AsOfChapter   int      `json:"as_of_chapter"`
	ObservedAtDay *float64 `json:"observed_at_day,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
}

type CharacterResourceHoldingV2 struct {
	ResourceID     string                        `json:"resource_id"`
	PerceivedName  string                        `json:"perceived_name,omitempty"`
	PerceivedLabel string                        `json:"perceived_label,omitempty"`
	PerceivedUnit  string                        `json:"perceived_unit,omitempty"`
	Access         string                        `json:"access"`
	Perception     ResourcePerceptionV2          `json:"perception"`
	EvidenceRefs   []string                      `json:"evidence_refs,omitempty"`
	KnownPlacement *CharacterResourcePlacementV2 `json:"known_placement,omitempty"`
}

type CharacterPhysicalStateV2 struct {
	ArtifactKnowledge       []CharacterArtifactKnowledgeV1      `json:"artifact_knowledge,omitempty"`
	SelfChronologyBaseline  *CharacterSelfChronologyBaselineV1  `json:"self_chronology_baseline,omitempty"`
	OperationalObservations []CharacterOperationalObservationV1 `json:"operational_observations,omitempty"`
	AgentID                 string                              `json:"agent_id"`
	Character               string                              `json:"character"`
	Location                string                              `json:"location"`
	Resources               []CharacterResourceHoldingV2        `json:"resources"`
	ReceivedFacts           []CharacterReceivedFactV2           `json:"received_facts,omitempty"`
	SelfExperiences         []CharacterSelfExperienceV2         `json:"self_experiences,omitempty"`
	TaskProgress            []CharacterTaskProgressV2           `json:"task_progress,omitempty"`
}

type WorldPhysicalStateV2 struct {
	Version   string                     `json:"version"`
	Resources []WorldResourceBalanceV2   `json:"resources"`
	Actors    []CharacterPhysicalStateV2 `json:"actors"`
}

// This is deliberately a separate type: observations cannot serialize actual
// world balances by copying an arbiter-only catalog entry.
type CharacterResourceViewV2 struct {
	InspectableSurfaces []string                      `json:"inspectable_surfaces,omitempty"`
	ResourceID          string                        `json:"resource_id"`
	Name                string                        `json:"name"`
	Unit                string                        `json:"unit"`
	Access              string                        `json:"access"`
	Perception          ResourcePerceptionV2          `json:"perception"`
	EvidenceRefs        []string                      `json:"evidence_refs,omitempty"`
	KnownPlacement      *CharacterResourcePlacementV2 `json:"known_placement,omitempty"`
}

type InitialCharacterResourceV2 struct {
	InspectableSurfaces []string                 `json:"inspectable_surfaces,omitempty"`
	ResourceID          string                   `json:"resource_id"`
	PerceivedName       string                   `json:"perceived_name,omitempty"`
	PerceivedLabel      string                   `json:"perceived_label,omitempty"`
	PerceivedUnit       string                   `json:"perceived_unit,omitempty"`
	Name                string                   `json:"name"`
	Unit                string                   `json:"unit"`
	ActualAmount        *float64                 `json:"actual_amount"`
	ReadableFacts       []ResourceReadableFactV2 `json:"readable_facts,omitempty"`
	AccessRequiresAny   []string                 `json:"access_requires_any,omitempty"`
	Access              string                   `json:"access"`
	Perception          ResourcePerceptionV2     `json:"perception"`
	EvidenceRefs        []string                 `json:"evidence_refs,omitempty"`
}

type ResourceSettlementV2 struct {
	ResourceID   string   `json:"resource_id"`
	Before       *float64 `json:"before"`
	Delta        *float64 `json:"delta"`
	After        *float64 `json:"after"`
	EvidenceRefs []string `json:"evidence_refs"`
	StartDay     *float64 `json:"start_day,omitempty"`
	EndDay       *float64 `json:"end_day,omitempty"`
}

type ResourceDeliveryV2 struct {
	ArtifactVersionDigest string   `json:"artifact_version_digest,omitempty"`
	ResourceID            string   `json:"resource_id"`
	FromAgentID           string   `json:"from_agent_id"`
	ToAgentID             string   `json:"to_agent_id"`
	SourceProposalDigest  string   `json:"source_proposal_digest"`
	ReceivedFields        []string `json:"received_fields"`
	Access                string   `json:"access"`
	EvidenceRefs          []string `json:"evidence_refs"`
}

type ResourceEstimateV2 struct {
	ResourceID   string   `json:"resource_id"`
	EstimateMin  *float64 `json:"estimate_min"`
	EstimateMax  *float64 `json:"estimate_max"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type ResourceMeasurementV2 struct {
	ResourceID   string `json:"resource_id"`
	MechanismRef string `json:"mechanism_ref"`
	TaskID       string `json:"task_id,omitempty"`
}

type ResourceReportV2 struct {
	ResourceID    string   `json:"resource_id"`
	PerceivedName string   `json:"perceived_name,omitempty"`
	PerceivedUnit string   `json:"perceived_unit,omitempty"`
	ToCharacter   string   `json:"to_character"`
	Amount        *float64 `json:"amount"`
	EvidenceRefs  []string `json:"evidence_refs"`
}

func ValidateWorldPhysicalStateV2(state WorldPhysicalStateV2) error {
	if state.Version != WorldPhysicalStateV2Version {
		return fmt.Errorf("physical state v2: unsupported version %q", state.Version)
	}
	resources := make(map[string]WorldResourceBalanceV2, len(state.Resources))
	for _, balance := range state.Resources {
		if err := validateWorldResourceBalanceV2(balance); err != nil {
			return err
		}
		if _, duplicate := resources[balance.ResourceID]; duplicate {
			return fmt.Errorf("physical state v2: duplicate world resource %q", balance.ResourceID)
		}
		resources[balance.ResourceID] = balance
	}
	actors, names := map[string]bool{}, map[string]bool{}
	exclusive, shared := map[string]string{}, map[string]int{}
	for _, actor := range state.Actors {
		if err := validateCharacterArtifactKnowledgeStateV1(actor, resources); err != nil {
			return err
		}
		if err := validateCharacterPhysicalStateV2(actor, resources); err != nil {
			return err
		}
		if err := validateCharacterReceivedFactsShapeV2(actor); err != nil {
			return err
		}
		if err := validateCharacterSelfStateV2(actor, resources); err != nil {
			return err
		}
		if err := validateCharacterOperationalStateV1(actor, resources); err != nil {
			return err
		}
		name := strings.ToLower(strings.Join(strings.Fields(actor.Character), " "))
		if actors[actor.AgentID] || names[name] {
			return fmt.Errorf("physical state v2: duplicate actor %q", actor.AgentID)
		}
		actors[actor.AgentID], names[name] = true, true
		for _, holding := range actor.Resources {
			switch holding.Access {
			case "exclusive":
				if exclusive[holding.ResourceID] != "" {
					return fmt.Errorf("physical state v2: exclusive resource %q cloned between actors", holding.ResourceID)
				}
				exclusive[holding.ResourceID] = actor.AgentID
			case "shared":
				shared[holding.ResourceID]++
			}
		}
	}
	for id := range exclusive {
		if shared[id] > 0 {
			return fmt.Errorf("physical state v2: resource %q is both exclusive and shared", id)
		}
	}
	return nil
}

func validateWorldResourceBalanceV2(balance WorldResourceBalanceV2) error {
	if err := ValidateInspectableSurfacesV1(balance.InspectableSurfaces); err != nil {
		return err
	}
	if balance.Artifact != nil {
		if err := validateCharacterWorkArtifactV1(balance); err != nil {
			return err
		}
	}
	if !physicalResourceIDV2(balance.ResourceID) || strings.TrimSpace(balance.Name) == "" {
		return fmt.Errorf("physical state v2: resource requires a stable id and name")
	}
	if balance.ActualAmount != nil && (strings.TrimSpace(balance.Unit) == "" || !physicalAmountV2(balance.ActualAmount)) {
		return fmt.Errorf("physical state v2: resource %q actual amount requires a unit and finite nonnegative value", balance.ResourceID)
	}
	seen := map[string]bool{}
	for _, fact := range balance.ReadableFacts {
		if !physicalIdentityV2(fact.ID) || strings.TrimSpace(fact.Text) == "" || seen[fact.ID] {
			return fmt.Errorf("physical resource has invalid/duplicate readable fact")
		}
		seen[fact.ID] = true
	}
	return nil
}

func validateCharacterPhysicalStateV2(actor CharacterPhysicalStateV2, catalog map[string]WorldResourceBalanceV2) error {
	if !physicalIdentityV2(actor.AgentID) || strings.TrimSpace(actor.Character) == "" || strings.TrimSpace(actor.Location) == "" {
		return fmt.Errorf("physical state v2: actor id, character and actual location are required")
	}
	if actor.Resources == nil {
		return fmt.Errorf("physical state v2: actor %s resources must be an explicit list (empty is allowed)", actor.AgentID)
	}
	seen := map[string]bool{}
	for _, holding := range actor.Resources {
		_, exists := catalog[holding.ResourceID]
		if !exists || seen[holding.ResourceID] {
			return fmt.Errorf("physical state v2: actor %s has unknown/duplicate resource %q", actor.AgentID, holding.ResourceID)
		}
		seen[holding.ResourceID] = true
		if err := validateResourceHoldingV2(holding); err != nil {
			return err
		}
	}
	return nil
}

func validateResourceHoldingV2(holding CharacterResourceHoldingV2) error {
	if err := validateCharacterPerceivedLabelV2(holding.PerceivedLabel); err != nil {
		return err
	}
	if !physicalResourceIDV2(holding.ResourceID) {
		return fmt.Errorf("physical state v2: resource holding id is required")
	}
	switch holding.Access {
	case "exclusive", "shared", "none":
	default:
		return fmt.Errorf("physical state v2: invalid resource access %q", holding.Access)
	}
	if perceptionHasNumberV2(holding.Perception) && strings.TrimSpace(holding.PerceivedUnit) == "" {
		return fmt.Errorf("numeric resource perception requires an explicitly perceived unit")
	}
	return validateResourcePerceptionV2(holding.Perception)
}

func validateResourcePerceptionV2(p ResourcePerceptionV2) error {
	if p.ObservedAtDay != nil && !physicalAmountV2(p.ObservedAtDay) {
		return fmt.Errorf("resource perception observed_at_day must be finite and nonnegative")
	}
	if p.AsOfChapter < 0 {
		return fmt.Errorf("resource perception has a negative as_of_chapter")
	}
	for _, value := range []*float64{p.Amount, p.EstimateMin, p.EstimateMax} {
		if value != nil && !physicalAmountV2(value) {
			return fmt.Errorf("resource perception requires finite nonnegative numbers")
		}
	}
	switch p.Kind {
	case "unaware", "unknown":
		if perceptionHasNumberV2(p) {
			return fmt.Errorf("resource perception %q must not invent an amount", p.Kind)
		}
	case "last_observed", "reported":
		if p.Amount == nil || p.EstimateMin != nil || p.EstimateMax != nil || len(normalizeV2Strings(p.EvidenceRefs)) == 0 {
			return fmt.Errorf("resource perception %q requires an evidenced amount without estimate bounds", p.Kind)
		}
	case "estimated":
		if (p.EstimateMin == nil) != (p.EstimateMax == nil) || (p.Amount == nil && p.EstimateMin == nil) || len(normalizeV2Strings(p.EvidenceRefs)) == 0 {
			return fmt.Errorf("estimated resource perception requires an evidenced point or complete range")
		}
		if p.EstimateMin != nil && (*p.EstimateMin > *p.EstimateMax || (p.Amount != nil && (*p.Amount < *p.EstimateMin || *p.Amount > *p.EstimateMax))) {
			return fmt.Errorf("resource estimate has inconsistent bounds")
		}
	default:
		return fmt.Errorf("invalid resource perception kind %q", p.Kind)
	}
	return nil
}

func FinalizeWorldPhysicalStateV2(state WorldPhysicalStateV2) (WorldPhysicalStateV2, error) {
	if state.Version == "" {
		state.Version = WorldPhysicalStateV2Version
	}
	state = assignCharacterReceivedFactIDsV2(state)
	if err := ValidateWorldPhysicalStateV2(state); err != nil {
		return state, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return state, err
	}
	var out WorldPhysicalStateV2
	if err := json.Unmarshal(raw, &out); err != nil {
		return state, err
	}
	if out.Resources == nil {
		out.Resources = []WorldResourceBalanceV2{}
	}
	if out.Actors == nil {
		out.Actors = []CharacterPhysicalStateV2{}
	}
	sort.Slice(out.Resources, func(i, j int) bool { return out.Resources[i].ResourceID < out.Resources[j].ResourceID })
	sort.Slice(out.Actors, func(i, j int) bool { return out.Actors[i].AgentID < out.Actors[j].AgentID })
	for i := range out.Actors {
		actor := &out.Actors[i]
		sort.Slice(actor.OperationalObservations, func(i, j int) bool { return actor.OperationalObservations[i].ID < actor.OperationalObservations[j].ID })
		sortCharacterSelfStateV2(actor)
		actor.Location = strings.TrimSpace(actor.Location)
		sort.Slice(actor.ReceivedFacts, func(i, j int) bool { return actor.ReceivedFacts[i].ID < actor.ReceivedFacts[j].ID })
		sort.Slice(actor.Resources, func(i, j int) bool { return actor.Resources[i].ResourceID < actor.Resources[j].ResourceID })
		for j := range actor.Resources {
			actor.Resources[j].PerceivedName = strings.TrimSpace(actor.Resources[j].PerceivedName)
			actor.Resources[j].PerceivedLabel = strings.TrimSpace(actor.Resources[j].PerceivedLabel)
			actor.Resources[j].PerceivedUnit = strings.TrimSpace(actor.Resources[j].PerceivedUnit)
			if actor.Resources[j].Perception.Kind != "unaware" && actor.Resources[j].PerceivedName == "" {
				actor.Resources[j].PerceivedName = UnidentifiedResourceNameV2
			}
			actor.Resources[j].EvidenceRefs = normalizeV2Strings(actor.Resources[j].EvidenceRefs)
			actor.Resources[j].Perception.EvidenceRefs = normalizeV2Strings(actor.Resources[j].Perception.EvidenceRefs)
		}
	}
	return out, nil
}

func EncodeWorldPhysicalStateV2(state WorldPhysicalStateV2) (string, error) {
	finalized, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(finalized)
	return string(raw), err
}

func DecodeWorldPhysicalStateV2(raw string) (WorldPhysicalStateV2, error) {
	var state WorldPhysicalStateV2
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return state, fmt.Errorf("physical state v2: trailing JSON")
	}
	if err := ValidateWorldPhysicalStateV2(state); err != nil {
		return state, err
	}
	return FinalizeWorldPhysicalStateV2(state)
}

func ComputeWorldPhysicalStateV2Digest(state WorldPhysicalStateV2) (string, error) {
	finalized, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return "", err
	}
	return characterAgentDigest(finalized)
}

func BuildWorldPhysicalStateFromInitialV2(characters []Character, registry CharacterAgentRegistry) (WorldPhysicalStateV2, error) {
	state := WorldPhysicalStateV2{Version: WorldPhysicalStateV2Version, Resources: []WorldResourceBalanceV2{}, Actors: []CharacterPhysicalStateV2{}}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, character := range characters {
		if character.InitialState == nil {
			continue
		}
		record, exists := registry.Resolve(character.Name)
		if !exists {
			return state, fmt.Errorf("physical initial state has no registered actor %q", character.Name)
		}
		actor := CharacterPhysicalStateV2{AgentID: record.AgentID, Character: record.Character, Location: character.InitialState.Location, Resources: []CharacterResourceHoldingV2{}}
		for _, initial := range character.InitialState.ResourceBalances {
			balance := WorldResourceBalanceV2{ResourceID: initial.ResourceID, Name: initial.Name, Unit: initial.Unit, ActualAmount: initial.ActualAmount, ReadableFacts: initial.ReadableFacts, AccessRequiresAny: initial.AccessRequiresAny, InspectableSurfaces: initial.InspectableSurfaces}
			if previous, ok := catalog[balance.ResourceID]; ok {
				if previous.Name != balance.Name || previous.Unit != balance.Unit || !samePhysicalNumberV2(previous.ActualAmount, balance.ActualAmount) || !samePhysicalValueV2(previous.ReadableFacts, balance.ReadableFacts) || !samePhysicalValueV2(normalizeV2Strings(previous.AccessRequiresAny), normalizeV2Strings(balance.AccessRequiresAny)) || !samePhysicalValueV2(normalizeV2Strings(previous.InspectableSurfaces), normalizeV2Strings(balance.InspectableSurfaces)) {
					return state, fmt.Errorf("physical initial state conflicts on shared resource %q name/unit/actual amount", balance.ResourceID)
				}
			} else {
				catalog[balance.ResourceID] = balance
				state.Resources = append(state.Resources, balance)
			}
			actor.Resources = append(actor.Resources, CharacterResourceHoldingV2{ResourceID: initial.ResourceID, PerceivedName: initial.PerceivedName, PerceivedLabel: initial.PerceivedLabel, PerceivedUnit: initial.PerceivedUnit, Access: initial.Access, Perception: initial.Perception, EvidenceRefs: initial.EvidenceRefs})
		}
		state.Actors = append(state.Actors, actor)
	}
	return FinalizeWorldPhysicalStateV2(state)
}

func BuildCharacterResourceViewsV2(state WorldPhysicalStateV2, agentID string) ([]CharacterResourceViewV2, error) {
	return buildCharacterResourceViewsV2(state, agentID, true)
}

func buildCharacterResourceViewsV2(state WorldPhysicalStateV2, agentID string, opaqueRefs bool) ([]CharacterResourceViewV2, error) {
	state, err := FinalizeWorldPhysicalStateV2(state)
	if err != nil {
		return nil, err
	}
	catalog := map[string]WorldResourceBalanceV2{}
	for _, resource := range state.Resources {
		catalog[resource.ResourceID] = resource
	}
	for _, actor := range state.Actors {
		if actor.AgentID != agentID {
			continue
		}
		views := []CharacterResourceViewV2{}
		for _, holding := range actor.Resources {
			if holding.Perception.Kind == "unaware" {
				continue
			}
			resource := catalog[holding.ResourceID]
			if opaqueRefs {
				holding.EvidenceRefs = CharacterSourceRefsV2(agentID, holding.EvidenceRefs)
				holding.Perception.EvidenceRefs = CharacterSourceRefsV2(agentID, holding.Perception.EvidenceRefs)
			}
			name := holding.PerceivedName
			if holding.PerceivedLabel != "" {
				name = holding.PerceivedLabel
			}
			views = append(views, CharacterResourceViewV2{ResourceID: resource.ResourceID, Name: name, Unit: holding.PerceivedUnit, Access: holding.Access, Perception: holding.Perception, EvidenceRefs: holding.EvidenceRefs, KnownPlacement: holding.KnownPlacement})
		}
		return views, nil
	}
	return nil, fmt.Errorf("physical state has no actor %q", agentID)
}

func FormatCharacterResourceViewsV2(views []CharacterResourceViewV2) []string {
	var out []string
	for _, view := range views {
		p := view.Perception
		if p.Kind == "unaware" {
			continue
		}
		name := view.Name
		if name == "" {
			name = UnidentifiedResourceNameV2
		}
		text := name + "（" + view.ResourceID + "；access=" + view.Access + "）：数量未知"
		if view.Unit == "" {
			text = name + "（" + view.ResourceID + "；access=" + view.Access + "）：信息未确认"
		}
		switch p.Kind {
		case "last_observed", "reported":
			if p.Amount != nil {
				label := "最后观测"
				if p.Kind == "reported" {
					label = "未核实报告"
				}
				text = fmt.Sprintf("%s（%s；access=%s）：%s %g %s（第%d章）", name, view.ResourceID, view.Access, label, *p.Amount, view.Unit, p.AsOfChapter)
			}
		case "estimated":
			if p.EstimateMin != nil && p.EstimateMax != nil {
				text = fmt.Sprintf("%s（%s；access=%s）：估计 %g–%g %s（第%d章）", name, view.ResourceID, view.Access, *p.EstimateMin, *p.EstimateMax, view.Unit, p.AsOfChapter)
			} else if p.Amount != nil {
				text = fmt.Sprintf("%s（%s；access=%s）：估计 %g %s（第%d章）", name, view.ResourceID, view.Access, *p.Amount, view.Unit, p.AsOfChapter)
			}
		}
		if placement := view.KnownPlacement; placement != nil {
			if placement.Kind == "with_actor" {
				text += fmt.Sprintf("；本人已实际携带至%s（第%d章）", placement.Location, placement.AsOfChapter)
			} else {
				text += fmt.Sprintf("；本人已实际放置于%s（第%d章）", placement.Location, placement.AsOfChapter)
			}
		}
		out = append(out, text)
	}
	return out
}

func ValidateCharacterResourceViewsV2(views []CharacterResourceViewV2, chapter int) error {
	seen := map[string]bool{}
	for _, view := range views {
		if seen[view.ResourceID] || view.Perception.Kind == "unaware" || strings.TrimSpace(view.Name) == "" || view.Perception.AsOfChapter > chapter {
			return fmt.Errorf("invalid/duplicate or future/unaware resource observation %q", view.ResourceID)
		}
		seen[view.ResourceID] = true
		if err := validateResourceHoldingV2(CharacterResourceHoldingV2{ResourceID: view.ResourceID, PerceivedName: view.Name, PerceivedUnit: view.Unit, Access: view.Access, Perception: view.Perception}); err != nil {
			return err
		}
		if view.Unit == "" && perceptionHasNumberV2(view.Perception) {
			return fmt.Errorf("qualitative resource observation cannot include an amount")
		}
	}
	return nil
}

var physicalResourceIDPatternV2 = regexp.MustCompile(`^res_[0-9a-f]{16,64}$`)

func physicalResourceIDV2(value string) bool { return physicalResourceIDPatternV2.MatchString(value) }
func physicalIdentityV2(value string) bool   { return value != "" && strings.TrimSpace(value) == value }
func physicalAmountV2(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0
}
func perceptionHasNumberV2(p ResourcePerceptionV2) bool {
	return p.Amount != nil || p.EstimateMin != nil || p.EstimateMax != nil
}
func samePhysicalNumberV2(a, b *float64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func samePhysicalValueV2(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}
func physicalNumberCopyV2(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
