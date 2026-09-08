package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	CharacterAgentRegistryVersion           = "character-agent-registry.v1"
	CharacterAgentActivationVersion         = "character-agent-activation.v1"
	WorldStimulusPacketVersion              = "world-stimulus-packet.v1"
	CharacterObservationVersion             = "character-observation-packet.v1"
	CharacterDecisionProposalVersion        = "character-decision-proposal.v1"
	WorldArbitrationReceiptVersion          = "world-arbitration-receipt.v1"
	CharacterAgentMemoryVersion             = "character-agent-memory.v1"
	CharacterAgentEvidenceVersion           = "character-agent-evidence.v1"
	CharacterHardConflictEvidenceVersion    = "character-hard-conflict-evidence.v1"
	CharacterAgentSuccessorPlanVersion      = "character-agent-successor-plan.v1"
	CharacterAgentDecisionProtocolVersion   = "character-agent-protocol.v1"
	CharacterAgentDecisionProtocolV2Version = "character-agent-protocol.v2"
	WorldStimulusPacketV2Version            = "world-stimulus-packet.v2"
	CharacterObservationV2Version           = "character-observation-packet.v2"
	WorldArbitrationReceiptV2Version        = "world-arbitration-receipt.v2"

	CharacterAgentActive   = "active"
	CharacterAgentSleeping = "sleeping"
	CharacterAgentRetired  = "retired"
)

// CharacterAgentRecord is the durable identity of one autonomous story
// character. AgentID never changes when the canonical display name changes;
// aliases are used to reconnect a renamed character to the same identity.
type CharacterAgentRecord struct {
	AgentID                string   `json:"agent_id"`
	AgentName              string   `json:"agent_name"`
	Character              string   `json:"character"`
	Aliases                []string `json:"aliases,omitempty"`
	Tier                   string   `json:"tier"`
	Status                 string   `json:"status"`
	FirstRegisteredChapter int      `json:"first_registered_chapter,omitempty"`
	LastActivatedChapter   int      `json:"last_activated_chapter,omitempty"`
	MemoryVersion          int      `json:"memory_version"`
	CreatedAt              string   `json:"created_at,omitempty"`
	UpdatedAt              string   `json:"updated_at,omitempty"`
}

// CharacterAgentSuccessorPlan is the Architect-authored, generation-local
// replacement for soft chapter slots after an independently chosen action
// makes a hard contract impossible in the current projection. The host binds
// every immutable constraint; the Architect can change only the soft outline
// fields of the listed chapters.
type CharacterAgentSuccessorPlan struct {
	Version               string         `json:"version"`
	ParentGenerationID    string         `json:"parent_generation_id"`
	BaseCanonChapter      int            `json:"base_canon_chapter"`
	TriggerChapter        int            `json:"trigger_chapter"`
	ArcFirstChapter       int            `json:"arc_first_chapter"`
	ArcLastChapter        int            `json:"arc_last_chapter"`
	BookLastChapter       int            `json:"book_last_chapter"`
	ArbitrationDigest     string         `json:"arbitration_digest"`
	ReadinessDigest       string         `json:"readiness_digest,omitempty"`
	AcceptedCanonRoot     string         `json:"accepted_canon_root"`
	EndingDirection       string         `json:"ending_direction"`
	NonNegotiables        []string       `json:"non_negotiables"`
	HardContractConflicts []string       `json:"hard_contract_conflicts"`
	RevisedChapters       []OutlineEntry `json:"revised_chapters"`
	ArchitectSummary      string         `json:"architect_summary"`
	CreatedAt             string         `json:"created_at,omitempty"`
	Digest                string         `json:"digest"`
}

func ComputeCharacterAgentSuccessorPlanDigest(plan CharacterAgentSuccessorPlan) (string, error) {
	plan.Digest = ""
	return characterAgentDigest(plan)
}

func FinalizeCharacterAgentSuccessorPlan(plan CharacterAgentSuccessorPlan) (CharacterAgentSuccessorPlan, error) {
	if plan.ReadinessDigest != "" {
		if err := validatePlanningV2Digest("successor readiness source", plan.ReadinessDigest); err != nil {
			return plan, err
		}
	}
	if plan.Version == "" {
		plan.Version = CharacterAgentSuccessorPlanVersion
	}
	if plan.Version != CharacterAgentSuccessorPlanVersion || strings.TrimSpace(plan.ParentGenerationID) == "" ||
		plan.BaseCanonChapter < 0 || plan.ArcFirstChapter != plan.BaseCanonChapter+1 ||
		plan.TriggerChapter < plan.ArcFirstChapter || plan.TriggerChapter > plan.ArcLastChapter ||
		plan.BookLastChapter < plan.ArcLastChapter || strings.TrimSpace(plan.ArbitrationDigest) == "" ||
		strings.TrimSpace(plan.AcceptedCanonRoot) == "" || strings.TrimSpace(plan.EndingDirection) == "" ||
		len(plan.NonNegotiables) == 0 || len(plan.HardContractConflicts) == 0 ||
		strings.TrimSpace(plan.ArchitectSummary) == "" {
		return plan, fmt.Errorf("character-agent successor plan identity/constraints are incomplete")
	}
	plan.NonNegotiables = normalizeV2Strings(plan.NonNegotiables)
	plan.HardContractConflicts = normalizeV2Strings(plan.HardContractConflicts)
	if len(plan.NonNegotiables) == 0 || len(plan.HardContractConflicts) == 0 {
		return plan, fmt.Errorf("character-agent successor plan has empty hard constraints")
	}
	if len(plan.RevisedChapters) != plan.ArcLastChapter-plan.TriggerChapter+1 {
		return plan, fmt.Errorf("character-agent successor plan must replace every soft slot from chapter %d through %d", plan.TriggerChapter, plan.ArcLastChapter)
	}
	for i := range plan.RevisedChapters {
		chapter := &plan.RevisedChapters[i]
		want := plan.TriggerChapter + i
		if chapter.Chapter != want || strings.TrimSpace(chapter.Title) == "" || strings.TrimSpace(chapter.CoreEvent) == "" ||
			strings.TrimSpace(chapter.Hook) == "" || len(normalizeV2Strings(chapter.Scenes)) == 0 {
			return plan, fmt.Errorf("character-agent successor plan chapter %d is incomplete or out of order", want)
		}
		chapter.Title = strings.TrimSpace(chapter.Title)
		chapter.CoreEvent = strings.TrimSpace(chapter.CoreEvent)
		chapter.Hook = strings.TrimSpace(chapter.Hook)
		chapter.Scenes = normalizeV2Strings(chapter.Scenes)
	}
	digest, err := ComputeCharacterAgentSuccessorPlanDigest(plan)
	if err != nil {
		return plan, err
	}
	plan.Digest = digest
	return plan, nil
}

type CharacterAgentRegistry struct {
	Version      string                 `json:"version"`
	Project      string                 `json:"project,omitempty"`
	Entries      []CharacterAgentRecord `json:"entries"`
	RegistryRoot string                 `json:"registry_root"`
}

// StableCharacterAgentID derives the initial ID. Registry reconciliation,
// rather than re-derivation, preserves the ID across later renames.
func StableCharacterAgentID(name string) (string, error) {
	name = normalizeCharacterIdentity(name)
	if name == "" {
		return "", fmt.Errorf("character name is required")
	}
	sum := sha256.Sum256([]byte("character-agent.v1\x00" + name))
	return "ca_" + hex.EncodeToString(sum[:8]), nil
}

func CharacterAgentName(agentID string) string {
	return "character_" + strings.TrimSpace(agentID)
}

func normalizeCharacterIdentity(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func normalizeCharacterAliases(values []string, canonical string) []string {
	seen := map[string]struct{}{normalizeCharacterIdentity(canonical): {}}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := normalizeCharacterIdentity(value)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		return normalizeCharacterIdentity(out[i]) < normalizeCharacterIdentity(out[j])
	})
	return out
}

func (r CharacterAgentRegistry) Resolve(name string) (CharacterAgentRecord, bool) {
	key := normalizeCharacterIdentity(name)
	if key == "" {
		return CharacterAgentRecord{}, false
	}
	for _, entry := range r.Entries {
		if normalizeCharacterIdentity(entry.Character) == key {
			return entry, true
		}
		for _, alias := range entry.Aliases {
			if normalizeCharacterIdentity(alias) == key {
				return entry, true
			}
		}
	}
	return CharacterAgentRecord{}, false
}

// UpsertCharacter keeps an existing identity when either the name or any
// alias overlaps. A renamed canonical name is retained as an alias.
func (r CharacterAgentRegistry) UpsertCharacter(name string, aliases []string, tier string, chapter int, now string) (CharacterAgentRegistry, CharacterAgentRecord, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return r, CharacterAgentRecord{}, fmt.Errorf("character name is required")
	}
	keys := map[string]struct{}{normalizeCharacterIdentity(name): {}}
	for _, alias := range aliases {
		if key := normalizeCharacterIdentity(alias); key != "" {
			keys[key] = struct{}{}
		}
	}
	match := -1
	for i, entry := range r.Entries {
		candidates := append([]string{entry.Character}, entry.Aliases...)
		for _, candidate := range candidates {
			if _, ok := keys[normalizeCharacterIdentity(candidate)]; ok {
				match = i
				break
			}
		}
		if match >= 0 {
			break
		}
	}
	if match < 0 {
		id, err := StableCharacterAgentID(name)
		if err != nil {
			return r, CharacterAgentRecord{}, err
		}
		entry := CharacterAgentRecord{
			AgentID:                id,
			AgentName:              CharacterAgentName(id),
			Character:              name,
			Aliases:                normalizeCharacterAliases(aliases, name),
			Tier:                   strings.TrimSpace(tier),
			Status:                 CharacterAgentSleeping,
			FirstRegisteredChapter: chapter,
			MemoryVersion:          1,
			CreatedAt:              now,
			UpdatedAt:              now,
		}
		r.Entries = append(r.Entries, entry)
		r = normalizeCharacterAgentRegistry(r)
		resolved, _ := r.Resolve(name)
		return r, resolved, nil
	}
	entry := r.Entries[match]
	if normalizeCharacterIdentity(entry.Character) != normalizeCharacterIdentity(name) {
		aliases = append(aliases, entry.Character)
	}
	entry.Character = name
	entry.Aliases = normalizeCharacterAliases(append(entry.Aliases, aliases...), name)
	if strings.TrimSpace(tier) != "" {
		entry.Tier = strings.TrimSpace(tier)
	}
	if entry.Status == "" {
		entry.Status = CharacterAgentSleeping
	}
	if entry.MemoryVersion <= 0 {
		entry.MemoryVersion = 1
	}
	entry.UpdatedAt = now
	r.Entries[match] = entry
	r = normalizeCharacterAgentRegistry(r)
	resolved, _ := r.Resolve(name)
	return r, resolved, nil
}

func normalizeCharacterAgentRegistry(r CharacterAgentRegistry) CharacterAgentRegistry {
	if r.Version == "" {
		r.Version = CharacterAgentRegistryVersion
	}
	for i := range r.Entries {
		r.Entries[i].Character = strings.TrimSpace(r.Entries[i].Character)
		if r.Entries[i].AgentName == "" {
			r.Entries[i].AgentName = CharacterAgentName(r.Entries[i].AgentID)
		}
		r.Entries[i].Aliases = normalizeCharacterAliases(r.Entries[i].Aliases, r.Entries[i].Character)
	}
	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].AgentID < r.Entries[j].AgentID })
	return r
}

func ComputeCharacterAgentRegistryRoot(r CharacterAgentRegistry) (string, error) {
	r = normalizeCharacterAgentRegistry(r)
	r.RegistryRoot = ""
	return characterAgentDigest(r)
}

func FinalizeCharacterAgentRegistry(r CharacterAgentRegistry) (CharacterAgentRegistry, error) {
	r = normalizeCharacterAgentRegistry(r)
	seenIDs := map[string]struct{}{}
	seenNames := map[string]string{}
	for _, entry := range r.Entries {
		if strings.TrimSpace(entry.AgentID) == "" || strings.TrimSpace(entry.Character) == "" {
			return r, fmt.Errorf("character agent identity is incomplete")
		}
		if entry.AgentName != CharacterAgentName(entry.AgentID) {
			return r, fmt.Errorf("character agent %s has invalid stable agent name %q", entry.AgentID, entry.AgentName)
		}
		if _, ok := seenIDs[entry.AgentID]; ok {
			return r, fmt.Errorf("duplicate character agent id %q", entry.AgentID)
		}
		seenIDs[entry.AgentID] = struct{}{}
		for _, identity := range append([]string{entry.Character}, entry.Aliases...) {
			key := normalizeCharacterIdentity(identity)
			if owner, ok := seenNames[key]; ok && owner != entry.AgentID {
				return r, fmt.Errorf("character identity %q belongs to both %s and %s", identity, owner, entry.AgentID)
			}
			seenNames[key] = entry.AgentID
		}
		switch entry.Status {
		case CharacterAgentActive, CharacterAgentSleeping, CharacterAgentRetired:
		default:
			return r, fmt.Errorf("character agent %s has invalid status %q", entry.AgentID, entry.Status)
		}
	}
	root, err := ComputeCharacterAgentRegistryRoot(r)
	if err != nil {
		return r, err
	}
	r.RegistryRoot = root
	return r, nil
}

type CharacterAgentActivationEntry struct {
	AgentID           string   `json:"agent_id"`
	Character         string   `json:"character"`
	Tier              string   `json:"tier"`
	State             string   `json:"state"` // active / sleeping
	Reasons           []string `json:"reasons,omitempty"`
	ObservationDigest string   `json:"observation_digest,omitempty"`
}

type CharacterAgentActivation struct {
	Version      string                          `json:"version"`
	GenerationID string                          `json:"generation_id"`
	Chapter      int                             `json:"chapter"`
	RegistryRoot string                          `json:"registry_root"`
	Entries      []CharacterAgentActivationEntry `json:"entries"`
	GeneratedAt  string                          `json:"generated_at,omitempty"`
	Digest       string                          `json:"digest"`
}

func ComputeCharacterAgentActivationDigest(a CharacterAgentActivation) (string, error) {
	a.Digest = ""
	return characterAgentDigest(a)
}

func FinalizeCharacterAgentActivation(a CharacterAgentActivation) (CharacterAgentActivation, error) {
	if a.Version == "" {
		a.Version = CharacterAgentActivationVersion
	}
	if a.Version != CharacterAgentActivationVersion || strings.TrimSpace(a.GenerationID) == "" || a.Chapter <= 0 {
		return a, fmt.Errorf("character activation identity is incomplete")
	}
	seen := map[string]struct{}{}
	for i := range a.Entries {
		entry := &a.Entries[i]
		entry.Reasons = normalizeV2Strings(entry.Reasons)
		if entry.AgentID == "" || entry.Character == "" {
			return a, fmt.Errorf("character activation entry is incomplete")
		}
		if _, ok := seen[entry.AgentID]; ok {
			return a, fmt.Errorf("duplicate activation for %s", entry.AgentID)
		}
		seen[entry.AgentID] = struct{}{}
		if entry.State != CharacterAgentActive && entry.State != CharacterAgentSleeping {
			return a, fmt.Errorf("invalid activation state %q", entry.State)
		}
		if entry.State == CharacterAgentActive && len(entry.Reasons) == 0 {
			return a, fmt.Errorf("active character %s has no activation reason", entry.AgentID)
		}
		if entry.State == CharacterAgentSleeping && entry.ObservationDigest != "" {
			return a, fmt.Errorf("sleeping character %s cannot bind an observation", entry.AgentID)
		}
	}
	sort.Slice(a.Entries, func(i, j int) bool { return a.Entries[i].AgentID < a.Entries[j].AgentID })
	digest, err := ComputeCharacterAgentActivationDigest(a)
	if err != nil {
		return a, err
	}
	a.Digest = digest
	return a, nil
}

type CharacterAgentFact struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Source     string `json:"source,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// WorldOperationalState is the compact, arbitration-facing projection of
// BookWorld. It keeps only topology, movement, faction pressure and finite
// resources; visual prose and other encyclopedic fields stay out of every
// character-agent round to avoid repeated tokens.
type WorldOperationalState struct {
	Version  int                       `json:"version,omitempty"`
	Name     string                    `json:"name,omitempty"`
	Places   []WorldOperationalPlace   `json:"places,omitempty"`
	Routes   []WorldRoute              `json:"routes,omitempty"`
	Factions []WorldOperationalFaction `json:"factions,omitempty"`
}

type WorldOperationalPlace struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Rules    []string `json:"rules,omitempty"`
	Factions []string `json:"factions,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

type WorldOperationalFaction struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Aliases         []string          `json:"aliases,omitempty"`
	Goal            string            `json:"goal,omitempty"`
	Resources       []string          `json:"resources,omitempty"`
	Relations       []FactionRelation `json:"relations,omitempty"`
	Stance          string            `json:"stance,omitempty"`
	InternalTension string            `json:"internal_tension,omitempty"`
	Clock           *FactionClock     `json:"clock,omitempty"`
}

type WorldStimulusPacket struct {
	SelfEvaluationContext *CharacterSelfEvaluationContextV1 `json:"self_evaluation_context,omitempty"`
	Version               string                            `json:"version"`
	GenerationID          string                            `json:"generation_id"`
	Chapter               int                               `json:"chapter"`
	TimeWindow            string                            `json:"time_window"`
	StoryClock            *StoryClockContext                `json:"story_clock,omitempty"`
	PhysicalState         *WorldPhysicalStateV2             `json:"physical_state,omitempty"`
	PublicFacts           []CharacterAgentFact              `json:"public_facts,omitempty"`
	CurrentEvents         []CharacterAgentFact              `json:"current_events,omitempty"`
	OperationalWorld      *WorldOperationalState            `json:"operational_world,omitempty"`
	Mechanisms            []CodexMechanism                  `json:"mechanisms,omitempty"`
	CounterfactualTests   []CodexCounterfactualProbe        `json:"counterfactual_tests,omitempty"`
	WorldCoherenceDigest  string                            `json:"world_coherence_digest,omitempty"`
	HardContracts         []string                          `json:"hard_contracts,omitempty"`
	SoftGuidance          []string                          `json:"soft_guidance,omitempty"`
	Sources               []string                          `json:"sources,omitempty"`
	GeneratedAt           string                            `json:"generated_at,omitempty"`
	Digest                string                            `json:"digest"`
}

func ComputeWorldStimulusPacketDigest(p WorldStimulusPacket) (string, error) {
	p.Digest = ""
	return characterAgentDigest(p)
}

func FinalizeWorldStimulusPacket(p WorldStimulusPacket) (WorldStimulusPacket, error) {
	if err := validateSurfaceInspectionStimulusV1(p); err != nil {
		return p, err
	}
	if err := validateWorkArtifactStimulusV1(p); err != nil {
		return p, err
	}
	if p.Version == "" {
		p.Version = WorldStimulusPacketVersion
	}
	if (p.Version != WorldStimulusPacketVersion && p.Version != WorldStimulusPacketV2Version) || p.Chapter <= 0 || strings.TrimSpace(p.GenerationID) == "" {
		return p, fmt.Errorf("world stimulus identity is incomplete")
	}
	if p.Version == WorldStimulusPacketV2Version {
		if p.PhysicalState == nil {
			return p, fmt.Errorf("v2 world stimulus requires physical_state")
		}
		state, err := FinalizeWorldPhysicalStateV2(*p.PhysicalState)
		if err != nil {
			return p, err
		}
		if HasCharacterSelfExperiencePolicyV2(p.Sources) {
			state, err = PrepareCharacterSelfExperienceStateV2(state)
			if err != nil {
				return p, err
			}
		}
		p.PhysicalState = &state
		knownMechanisms := map[string]bool{}
		for _, mechanism := range p.Mechanisms {
			knownMechanisms[mechanism.ID] = true
		}
		for _, resource := range state.Resources {
			for _, ref := range resource.AccessRequiresAny {
				if ref == "" || !knownMechanisms[ref] {
					return p, fmt.Errorf("resource access requires an unknown world mechanism %q", ref)
				}
			}
		}
	} else if p.PhysicalState != nil {
		return p, fmt.Errorf("physical_state requires v2 world stimulus")
	}
	if p.StoryClock != nil {
		if err := ValidateStoryClockContext(*p.StoryClock); err != nil {
			return p, fmt.Errorf("world stimulus: %w", err)
		}
	}
	p.HardContracts = normalizeV2Strings(p.HardContracts)
	p.SoftGuidance = normalizeV2Strings(p.SoftGuidance)
	p.Sources = normalizeV2Strings(p.Sources)
	if err := validateSelfChronologyStimulusV1(p); err != nil {
		return p, err
	}
	digest, err := ComputeWorldStimulusPacketDigest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

type CharacterObservationPacket struct {
	ArtifactViews           []CharacterArtifactViewV1           `json:"artifact_views,omitempty"`
	OperationalObservations []CharacterOperationalObservationV1 `json:"operational_observations,omitempty"`
	CycleContext            *CharacterObservationCycleContext   `json:"cycle_context,omitempty"`
	Version                 string                              `json:"version"`
	GenerationID            string                              `json:"generation_id"`
	Chapter                 int                                 `json:"chapter"`
	Round                   int                                 `json:"round"`
	AgentID                 string                              `json:"agent_id"`
	Character               string                              `json:"character"`
	Tier                    string                              `json:"tier"`
	TimeWindow              string                              `json:"time_window"`
	Location                string                              `json:"location,omitempty"`
	CurrentGoal             string                              `json:"current_goal"`
	Pressure                string                              `json:"pressure"`
	Resources               []string                            `json:"resources,omitempty"`
	ResourceViews           []CharacterResourceViewV2           `json:"resource_views,omitempty"`
	SelfExperiences         []CharacterSelfExperienceV2         `json:"self_experiences,omitempty"`
	TaskProgress            []CharacterTaskProgressV2           `json:"task_progress,omitempty"`
	Relationships           []string                            `json:"relationships,omitempty"`
	Commitments             []string                            `json:"commitments,omitempty"`
	KnownFacts              []CharacterAgentFact                `json:"known_facts,omitempty"`
	PerceivedEvents         []CharacterAgentFact                `json:"perceived_events,omitempty"`
	PublicRules             []CharacterAgentFact                `json:"public_rules,omitempty"`
	PublicMechanisms        []CodexMechanism                    `json:"public_mechanisms,omitempty"`
	Memory                  []CharacterAgentMemoryFact          `json:"memory,omitempty"`
	ConflictFeedback        []string                            `json:"conflict_feedback,omitempty"`
	StimulusDigest          string                              `json:"stimulus_digest"`
	MemoryRoot              string                              `json:"memory_root,omitempty"`
	Sources                 []string                            `json:"sources,omitempty"`
	GeneratedAt             string                              `json:"generated_at,omitempty"`
	Digest                  string                              `json:"digest"`
}

func (p CharacterObservationPacket) AllowedFactIDs() map[string]struct{} {
	out := make(map[string]struct{}, len(p.KnownFacts)+len(p.PerceivedEvents)+len(p.PublicRules)+len(p.Memory))
	for _, group := range [][]CharacterAgentFact{p.KnownFacts, p.PerceivedEvents, p.PublicRules} {
		for _, fact := range group {
			if fact.ID != "" {
				out[fact.ID] = struct{}{}
			}
		}
	}
	for _, fact := range p.Memory {
		if fact.ID != "" {
			out[fact.ID] = struct{}{}
		}
	}
	if p.Version == CharacterObservationV2Version {
		if HasCharacterWorkArtifactPolicyV1(p.Sources) {
			for _, view := range p.ArtifactViews {
				for _, claim := range view.Claims {
					out[claim.ID] = struct{}{}
				}
				for _, signature := range view.Signatures {
					out[signature.SignatureDigest] = struct{}{}
				}
			}
		}
		if HasCharacterOperationalAvailabilityPolicyV1(p.Sources) {
			for _, observation := range p.OperationalObservations {
				out[observation.ID] = struct{}{}
			}
		}
		if HasCharacterSelfExperiencePolicyV2(p.Sources) {
			for _, experience := range p.SelfExperiences {
				out[experience.ID] = struct{}{}
			}
		}
		for _, view := range p.ResourceViews {
			for _, ref := range append(append([]string(nil), view.EvidenceRefs...), view.Perception.EvidenceRefs...) {
				if ref != "" {
					out[ref] = struct{}{}
				}
			}
			// Only the opt-in, host-source-bound completion view can reference a
			// completed row's original source without repeating its full event.
			// This lookup is not authentication; frozen-input validation is.
			for _, task := range p.TaskProgress {
				if validCompactSelfCompletionV1(task, p) {
					out[task.SourceExperienceID] = struct{}{}
				}
			}
		}
	}
	return out
}

func (p CharacterObservationPacket) AllowedMechanismIDs() map[string]struct{} {
	out := make(map[string]struct{}, len(p.PublicMechanisms))
	for _, mechanism := range p.PublicMechanisms {
		if id := strings.TrimSpace(mechanism.ID); id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func ComputeCharacterObservationDigest(p CharacterObservationPacket) (string, error) {
	p.Digest = ""
	return characterAgentDigest(p)
}

func FinalizeCharacterObservationPacket(p CharacterObservationPacket) (CharacterObservationPacket, error) {
	if err := validateWorkArtifactObservationV1(p); err != nil {
		return p, err
	}
	if err := validateCharacterOperationalObservationPacketV1(p); err != nil {
		return p, err
	}
	if err := validateCharacterObservationCycleContext(p); err != nil {
		return p, err
	}
	if p.Version == "" {
		p.Version = CharacterObservationVersion
	}
	if (p.Version != CharacterObservationVersion && p.Version != CharacterObservationV2Version) || p.GenerationID == "" || p.Chapter <= 0 || p.Round <= 0 || p.AgentID == "" || p.Character == "" {
		return p, fmt.Errorf("character observation identity is incomplete")
	}
	if p.Version == CharacterObservationV2Version {
		if strings.TrimSpace(p.Location) == "" {
			return p, fmt.Errorf("v2 character observation requires actual current location")
		}
		if err := ValidateCharacterResourceViewsV2(p.ResourceViews, p.Chapter); err != nil {
			return p, err
		}
		if err := validateCharacterObservationSourceRefsV2(p); err != nil {
			return p, err
		}
		if err := validateCharacterSelfObservationV2(p); err != nil {
			return p, err
		}
	} else {
		if len(p.ResourceViews) > 0 {
			return p, fmt.Errorf("resource_views require v2 character observation")
		}
		if HasCharacterSelfChronologyPolicyV1(p.Sources) {
			return p, fmt.Errorf("self chronology requires v2 character observation")
		}
		for _, fact := range p.SelfExperiences {
			if fact.Evaluation != nil {
				return p, fmt.Errorf("self evaluation cannot be embedded in a legacy observation")
			}
		}
		for _, task := range p.TaskProgress {
			if task.LatestAttemptStatus != "" {
				return p, fmt.Errorf("attempt status cannot be embedded in a legacy observation")
			}
		}
	}
	if p.Round > 1 && len(p.ConflictFeedback) == 0 {
		return p, fmt.Errorf("revision observation must contain conflict feedback")
	}
	p.Resources = normalizeV2Strings(p.Resources)
	p.Relationships = normalizeV2Strings(p.Relationships)
	p.Commitments = normalizeV2Strings(p.Commitments)
	p.ConflictFeedback = normalizeV2Strings(p.ConflictFeedback)
	p.Sources = normalizeV2Strings(p.Sources)
	for _, mechanism := range p.PublicMechanisms {
		if CodexMechanismVisibility(mechanism) == "secret" {
			return p, fmt.Errorf("character observation exposes secret mechanism %q", mechanism.ID)
		}
	}
	digest, err := ComputeCharacterObservationDigest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

type CharacterDecisionProposal struct {
	ArtifactAccess       []CharacterArtifactAccessIntentV1          `json:"artifact_access,omitempty"`
	ArtifactReads        []CharacterArtifactReadVersionV1           `json:"artifact_reads,omitempty"`
	ArtifactSigns        []CharacterArtifactSignIntentV1            `json:"artifact_signs,omitempty"`
	Version              string                                     `json:"version"`
	SelfTasks            []CharacterSelfTaskV2                      `json:"self_tasks,omitempty"`
	WorkContinuations    []CharacterWorkContinuationAuthorizationV1 `json:"work_continuations,omitempty"`
	GenerationID         string                                     `json:"generation_id"`
	Chapter              int                                        `json:"chapter"`
	Round                int                                        `json:"round"`
	AgentID              string                                     `json:"agent_id"`
	Character            string                                     `json:"character"`
	ObservationDigest    string                                     `json:"observation_digest"`
	Time                 string                                     `json:"time,omitempty"`
	Location             string                                     `json:"location"`
	CurrentGoal          string                                     `json:"current_goal"`
	Pressure             string                                     `json:"pressure"`
	Resources            []string                                   `json:"resources,omitempty"`
	AvailableOptions     []string                                   `json:"available_options"`
	RejectedOptions      []string                                   `json:"rejected_options,omitempty"`
	Decision             string                                     `json:"decision"`
	DecisionReason       string                                     `json:"decision_reason"`
	IntendedAction       string                                     `json:"intended_action"`
	ActionDuration       string                                     `json:"action_duration"`
	KnowledgeRefs        []string                                   `json:"knowledge_refs"`
	MechanismRefs        []string                                   `json:"mechanism_refs,omitempty"`
	ResourceClaims       []string                                   `json:"resource_claims,omitempty"`
	ResourceEstimates    []ResourceEstimateV2                       `json:"resource_estimates,omitempty"`
	ResourceMeasurements []ResourceMeasurementV2                    `json:"resource_measurements,omitempty"`
	ResourceReports      []ResourceReportV2                         `json:"resource_reports,omitempty"`
	Communications       []CharacterCommunicationV2                 `json:"communications,omitempty"`
	ResourceReads        []ResourceReadRequestV2                    `json:"resource_reads,omitempty"`
	Constraints          []string                                   `json:"constraints,omitempty"`
	Contingencies        []string                                   `json:"contingencies,omitempty"`
	ExpectedConsequences []string                                   `json:"expected_consequences,omitempty"`
	SubmittedAt          string                                     `json:"submitted_at,omitempty"`
	Digest               string                                     `json:"digest"`
}

func ComputeCharacterDecisionProposalDigest(p CharacterDecisionProposal) (string, error) {
	p.Digest = ""
	return characterAgentDigest(p)
}

func FinalizeCharacterDecisionProposal(p CharacterDecisionProposal, observation CharacterObservationPacket) (CharacterDecisionProposal, error) {
	if p.Version == "" {
		p.Version = CharacterDecisionProposalVersion
	}
	if p.Version != CharacterDecisionProposalVersion || p.GenerationID != observation.GenerationID || p.Chapter != observation.Chapter || p.Round != observation.Round || p.AgentID != observation.AgentID || p.Character != observation.Character || p.ObservationDigest != observation.Digest {
		return p, fmt.Errorf("character proposal is not bound to its observation")
	}
	if err := ValidateCharacterArtifactIntentV1(p, observation); err != nil {
		return p, err
	}
	if err := ValidateCharacterResourceIntentV2(p, observation); err != nil {
		return p, err
	}
	if err := ValidateCharacterKnowledgeIntentV2(p, observation); err != nil {
		return p, err
	}
	if err := ValidateCharacterSelfTaskIntentV2(p, observation); err != nil {
		return p, err
	}
	if err := ValidateCharacterWorkContinuationIntentV1(p, observation); err != nil {
		return p, err
	}
	if strings.TrimSpace(p.Location) == "" || strings.TrimSpace(p.CurrentGoal) == "" || strings.TrimSpace(p.Pressure) == "" || len(p.AvailableOptions) < 2 || strings.TrimSpace(p.Decision) == "" || strings.TrimSpace(p.DecisionReason) == "" || strings.TrimSpace(p.IntendedAction) == "" || strings.TrimSpace(p.ActionDuration) == "" || len(p.KnowledgeRefs) == 0 {
		return p, fmt.Errorf("character proposal is incomplete")
	}
	p.Resources = normalizeV2Strings(p.Resources)
	p.AvailableOptions = normalizeV2Strings(p.AvailableOptions)
	p.RejectedOptions = normalizeV2Strings(p.RejectedOptions)
	p.KnowledgeRefs = normalizeV2Strings(p.KnowledgeRefs)
	p.MechanismRefs = normalizeV2Strings(p.MechanismRefs)
	p.ResourceClaims = normalizeV2Strings(p.ResourceClaims)
	p.Constraints = normalizeV2Strings(p.Constraints)
	p.Contingencies = normalizeV2Strings(p.Contingencies)
	p.ExpectedConsequences = normalizeV2Strings(p.ExpectedConsequences)
	allowed := observation.AllowedFactIDs()
	for _, ref := range p.KnowledgeRefs {
		if _, ok := allowed[ref]; !ok {
			return p, fmt.Errorf("proposal references unavailable knowledge %q", ref)
		}
	}
	allowedMechanisms := observation.AllowedMechanismIDs()
	for _, ref := range p.MechanismRefs {
		if _, ok := allowedMechanisms[ref]; !ok {
			return p, fmt.Errorf("proposal references unavailable mechanism %q", ref)
		}
	}
	digest, err := ComputeCharacterDecisionProposalDigest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

type WorldArbitrationConflict struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"` // time / location / resource / rule / knowledge / collision
	AffectedAgentIDs []string `json:"affected_agent_ids"`
	Feedback         string   `json:"feedback"`
	Resolved         bool     `json:"resolved"`
}

type CharacterDecisionResolution struct {
	ArtifactReadResults []CharacterArtifactReadResultV1      `json:"artifact_read_results,omitempty"`
	ArtifactSignatures  []CharacterArtifactSignatureResultV1 `json:"artifact_signatures,omitempty"`
	SelfExecutions      []CharacterSelfExecutionV2           `json:"self_executions,omitempty"`
	AgentID             string                               `json:"agent_id"`
	Character           string                               `json:"character"`
	ProposalDigest      string                               `json:"proposal_digest"`
	Decision            string                               `json:"decision"`
	IntendedAction      string                               `json:"intended_action"`
	ActionOrder         int                                  `json:"action_order"`
	Outcome             string                               `json:"outcome"` // success / partial / blocked
	CompletionState     string                               `json:"completion_state"`
	ImmediateResult     string                               `json:"immediate_result"`
	StateAfter          string                               `json:"state_after"`
	PostState           *CharacterPhysicalStateV2            `json:"post_state,omitempty"`
	MechanismRefs       []string                             `json:"mechanism_refs,omitempty"`
	VisibleToPOV        bool                                 `json:"visible_to_pov,omitempty"`
	ButterflyEffects    []DecisionButterflyEffect            `json:"butterfly_effects"`
	ConflictIDs         []string                             `json:"conflict_ids,omitempty"`
}

type WorldArbitrationReceipt struct {
	Version               string                        `json:"version"`
	GenerationID          string                        `json:"generation_id"`
	Chapter               int                           `json:"chapter"`
	Round                 int                           `json:"round"`
	StoryTime             *StoryTimeChapterSchedule     `json:"story_time,omitempty"`
	StimulusDigest        string                        `json:"stimulus_digest"`
	ActivationDigest      string                        `json:"activation_digest"`
	ProposalDigests       []string                      `json:"proposal_digests"`
	Resolutions           []CharacterDecisionResolution `json:"resolutions"`
	ResourceSettlements   []ResourceSettlementV2        `json:"resource_settlements,omitempty"`
	ResourceDeliveries    []ResourceDeliveryV2          `json:"resource_deliveries,omitempty"`
	PassiveReceptions     []CharacterPassiveReceptionV2 `json:"passive_receptions,omitempty"`
	Conflicts             []WorldArbitrationConflict    `json:"conflicts,omitempty"`
	HardContractStatus    string                        `json:"hard_contract_status"` // feasible / infeasible
	HardContractConflicts []string                      `json:"hard_contract_conflicts,omitempty"`
	ProtagonistProjection ProtagonistDecisionProjection `json:"protagonist_projection"`
	Finalized             bool                          `json:"finalized"`
	GeneratedAt           string                        `json:"generated_at,omitempty"`
	Digest                string                        `json:"digest"`
}

func ComputeWorldArbitrationReceiptDigest(r WorldArbitrationReceipt) (string, error) {
	r.Digest = ""
	return characterAgentDigest(r)
}

// This feedback copies only the bound proposal's identity and mismatched
// intent fields, never the rejected candidate or either side's reasoning.
func characterArbitrationIntentRewriteError(proposal CharacterDecisionProposal, resolution CharacterDecisionResolution) error {
	const maxExpectedJSONBytes = 1536
	var fields []string
	add := func(field, expected string) {
		encoded, _ := json.Marshal(expected)
		if len(encoded) <= maxExpectedJSONBytes {
			fields = append(fields, fmt.Sprintf("%s must exactly match original proposal.%s; expected_json=%s", field, field, encoded))
			return
		}
		fields = append(fields, fmt.Sprintf("%s must exactly match original proposal.%s; original value omitted (utf8_bytes=%d, sha256:%x); copy that complete proposal field, not a shortened value", field, field, len(expected), sha256.Sum256([]byte(expected))))
	}
	if resolution.Decision != proposal.Decision {
		add("decision", proposal.Decision)
	}
	if resolution.IntendedAction != proposal.IntendedAction {
		add("intended_action", proposal.IntendedAction)
	}
	agentID := proposal.AgentID
	if len(agentID) > 128 {
		agentID = fmt.Sprintf("<agent utf8_bytes=%d sha256:%x>", len(agentID), sha256.Sum256([]byte(agentID)))
	}
	encodedCharacter, _ := json.Marshal(proposal.Character)
	characterLabel := string(encodedCharacter)
	if len(encodedCharacter) > 256 {
		characterLabel = fmt.Sprintf("<name utf8_bytes=%d sha256:%x>", len(proposal.Character), sha256.Sum256([]byte(proposal.Character)))
	}
	return fmt.Errorf("arbiter rewrote intent for %s: character=%s; %s", agentID, characterLabel, strings.Join(fields, "; "))
}

func FinalizeWorldArbitrationReceipt(r WorldArbitrationReceipt, stimulus WorldStimulusPacket, activation CharacterAgentActivation, proposals []CharacterDecisionProposal, maxRevisionRounds int) (WorldArbitrationReceipt, error) {
	if HasCharacterWorkArtifactPolicyV1(stimulus.Sources) {
		return r, fmt.Errorf("work artifacts require the verified v3 arbitration round entry")
	}
	return finalizeWorldArbitrationReceiptWithPriorSources(r, stimulus, activation, proposals, maxRevisionRounds, nil)
}

// priorSources is private Host-verified authority, not a model-provided round
// override. The legacy public entry point always passes nil and keeps its exact
// same-cycle round rule and serialization.
func finalizeWorldArbitrationReceiptWithPriorSources(r WorldArbitrationReceipt, stimulus WorldStimulusPacket, activation CharacterAgentActivation, proposals []CharacterDecisionProposal, maxRevisionRounds int, priorSources map[string]string) (WorldArbitrationReceipt, error) {
	if r.Version == "" {
		r.Version = WorldArbitrationReceiptVersion
	}
	if (r.Version != WorldArbitrationReceiptVersion && r.Version != WorldArbitrationReceiptV2Version) || r.GenerationID != stimulus.GenerationID || r.Chapter != stimulus.Chapter || r.StimulusDigest != stimulus.Digest || r.ActivationDigest != activation.Digest || r.Round <= 0 || r.Round > maxRevisionRounds+1 {
		return r, fmt.Errorf("world arbitration identity is incomplete")
	}
	if (r.Version == WorldArbitrationReceiptV2Version) != (stimulus.Version == WorldStimulusPacketV2Version) {
		return r, fmt.Errorf("world arbitration/stimulus protocol versions do not match")
	}
	if len(r.PassiveReceptions) > 0 {
		sleeping := map[string]bool{}
		for _, entry := range activation.Entries {
			sleeping[entry.AgentID] = entry.State == CharacterAgentSleeping
		}
		for _, reception := range r.PassiveReceptions {
			if !sleeping[reception.ToAgentID] {
				return r, fmt.Errorf("passive reception recipient is not in this cycle's registered sleeping baseline")
			}
		}
	}
	if r.Version == WorldArbitrationReceiptVersion {
		if len(r.ResourceSettlements)+len(r.ResourceDeliveries)+len(r.PassiveReceptions) > 0 {
			return r, fmt.Errorf("resource_settlements require v2 arbitration")
		}
		for _, resolution := range r.Resolutions {
			if resolution.PostState != nil || len(resolution.SelfExecutions) > 0 {
				return r, fmt.Errorf("post_state requires v2 arbitration")
			}
		}
	}
	if err := ValidateStoryTimeForClock(r.Chapter, r.StoryTime, stimulus.StoryClock); err != nil {
		return r, fmt.Errorf("world arbitration: %w", err)
	}
	if stimulus.StoryClock != nil {
		// Accept harmless input rounding, but persist the exact host coordinate
		// so tolerance never accumulates into drift across chapters.
		if r.Digest != "" && r.StoryTime.StartDay != stimulus.StoryClock.CurrentDay {
			return r, fmt.Errorf("world arbitration: stored story_time start_day differs from host current_day")
		}
		storyTime := *r.StoryTime
		storyTime.StartDay = stimulus.StoryClock.CurrentDay
		r.StoryTime = &storyTime
	}
	byAgent := make(map[string]CharacterDecisionProposal, len(proposals))
	wantedDigests := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		priorAllowed := priorSources != nil && proposal.Digest != "" && priorSources[proposal.AgentID] == proposal.Digest
		if proposal.Round <= 0 || (proposal.Round > r.Round && !priorAllowed) {
			return r, fmt.Errorf("proposal %s belongs to future/invalid round %d, arbitration round %d", proposal.AgentID, proposal.Round, r.Round)
		}
		byAgent[proposal.AgentID] = proposal
		wantedDigests = append(wantedDigests, proposal.Digest)
	}
	wantedDigests = normalizeV2Strings(wantedDigests)
	r.ProposalDigests = normalizeV2Strings(r.ProposalDigests)
	if !sameV2Strings(r.ProposalDigests, wantedDigests) {
		return r, fmt.Errorf("world arbitration proposal digest set mismatch")
	}
	seen := map[string]struct{}{}
	actionOrders := map[int]string{}
	knownMechanisms := make(map[string]struct{}, len(stimulus.Mechanisms))
	for _, mechanism := range stimulus.Mechanisms {
		if id := strings.TrimSpace(mechanism.ID); id != "" {
			knownMechanisms[id] = struct{}{}
		}
	}
	for i := range r.Resolutions {
		resolution := &r.Resolutions[i]
		resolution.MechanismRefs = normalizeV2Strings(resolution.MechanismRefs)
		proposal, ok := byAgent[resolution.AgentID]
		if !ok || resolution.Character != proposal.Character || resolution.ProposalDigest != proposal.Digest {
			return r, fmt.Errorf("resolution for %s is not bound to a proposal", resolution.AgentID)
		}
		if resolution.Decision != proposal.Decision || resolution.IntendedAction != proposal.IntendedAction {
			return r, characterArbitrationIntentRewriteError(proposal, *resolution)
		}
		for _, ref := range resolution.MechanismRefs {
			if _, ok := knownMechanisms[ref]; !ok {
				return r, fmt.Errorf("resolution for %s references unknown mechanism %q", resolution.AgentID, ref)
			}
		}
		if _, duplicate := seen[resolution.AgentID]; duplicate {
			return r, fmt.Errorf("duplicate resolution for %s", resolution.AgentID)
		}
		seen[resolution.AgentID] = struct{}{}
		if resolution.ActionOrder <= 0 || strings.TrimSpace(resolution.ImmediateResult) == "" || strings.TrimSpace(resolution.StateAfter) == "" || len(resolution.ButterflyEffects) == 0 {
			return r, fmt.Errorf("resolution for %s is incomplete", resolution.AgentID)
		}
		if owner, duplicate := actionOrders[resolution.ActionOrder]; duplicate {
			return r, fmt.Errorf("action order %d belongs to both %s and %s", resolution.ActionOrder, owner, resolution.AgentID)
		}
		actionOrders[resolution.ActionOrder] = resolution.AgentID
		for _, effect := range resolution.ButterflyEffects {
			if strings.TrimSpace(effect.Effect) == "" || strings.TrimSpace(effect.TransmissionPath) == "" || effect.ArrivalChapter < r.Chapter || strings.TrimSpace(effect.ProtagonistImpact) == "" {
				return r, fmt.Errorf("resolution for %s has incomplete butterfly effect", resolution.AgentID)
			}
		}
		switch resolution.Outcome {
		case "success", "partial", "blocked":
		default:
			return r, fmt.Errorf("resolution for %s has invalid outcome %q", resolution.AgentID, resolution.Outcome)
		}
	}
	if len(seen) != len(byAgent) {
		return r, fmt.Errorf("world arbitration does not resolve every active proposal")
	}
	r.HardContractConflicts = normalizeV2Strings(r.HardContractConflicts)
	switch r.HardContractStatus {
	case "feasible":
		if len(r.HardContractConflicts) != 0 {
			return r, fmt.Errorf("feasible arbitration cannot list hard-contract conflicts")
		}
	case "infeasible":
		if len(r.HardContractConflicts) == 0 {
			return r, fmt.Errorf("infeasible arbitration must identify hard-contract conflicts")
		}
		if r.Finalized {
			return r, fmt.Errorf("hard-contract-infeasible arbitration cannot finalize")
		}
	default:
		return r, fmt.Errorf("world arbitration has invalid hard_contract_status %q", r.HardContractStatus)
	}
	unresolved := false
	conflictIDs := make(map[string]struct{}, len(r.Conflicts))
	for i := range r.Conflicts {
		conflict := &r.Conflicts[i]
		conflict.AffectedAgentIDs = normalizeV2Strings(conflict.AffectedAgentIDs)
		if conflict.ID == "" || conflict.Feedback == "" || len(conflict.AffectedAgentIDs) == 0 {
			return r, fmt.Errorf("world arbitration conflict[%d] is incomplete", i)
		}
		if _, duplicate := conflictIDs[conflict.ID]; duplicate {
			return r, fmt.Errorf("duplicate world arbitration conflict %s", conflict.ID)
		}
		conflictIDs[conflict.ID] = struct{}{}
		for _, agentID := range conflict.AffectedAgentIDs {
			if _, exists := byAgent[agentID]; !exists {
				return r, fmt.Errorf("world arbitration conflict %s affects unknown agent %s", conflict.ID, agentID)
			}
		}
		switch conflict.Kind {
		case "time", "location", "resource", "rule", "knowledge", "collision":
		default:
			return r, fmt.Errorf("world arbitration conflict %s has invalid kind %q", conflict.ID, conflict.Kind)
		}
		if !conflict.Resolved {
			unresolved = true
		}
	}
	for _, resolution := range r.Resolutions {
		for _, conflictID := range resolution.ConflictIDs {
			if _, exists := conflictIDs[conflictID]; !exists {
				return r, fmt.Errorf("resolution for %s references unknown conflict %s", resolution.AgentID, conflictID)
			}
		}
	}
	if r.Finalized && unresolved {
		return r, fmt.Errorf("final arbitration contains unresolved conflicts")
	}
	if !r.Finalized && !unresolved && r.HardContractStatus != "infeasible" {
		return r, fmt.Errorf("non-final arbitration must identify an unresolved conflict")
	}
	if !r.Finalized && r.Round > maxRevisionRounds && r.HardContractStatus != "infeasible" {
		return r, fmt.Errorf("world arbitration exhausted revision rounds (round=%d, max_revision_rounds=%d): 裁决闭合不要求所有角色意图成功或人物达成共识。若能依据原意图、实际时空和资源给出确定后果，可裁 partial/blocked，并仅将已经裁定后果的冲突标为 resolved；不能再请求角色修订、改变其意图、伪造会合/交付/读取，或只翻转 finalized/resolved 而不提供完整真实后态。若角色实际移动或执行了任务，整体不能标 blocked，应保留真实执行并把未发生的子任务分别标为 blocked/not_started、不给执行时间。真正没有任何执行时才可整体 blocked；硬合同是否不可实现仍按事实判断", r.Round, maxRevisionRounds)
	}
	projection := r.ProtagonistProjection
	var protagonistProposal *CharacterDecisionProposal
	for i := range proposals {
		if proposals[i].Character == projection.Protagonist {
			proposal := proposals[i]
			protagonistProposal = &proposal
			break
		}
	}
	// A cycle arbitrates only active actors; the chapter host builds its POV
	// projection from real choices across the complete cycle chain. Never
	// invent a sleeping POV proposal to satisfy the one-shot contract.
	omittedCycleProjection := HasCharacterActivationCycleContract(stimulus) && emptyCharacterCycleProjection(projection)
	if !omittedCycleProjection && (protagonistProposal == nil || projection.ChosenDecision != protagonistProposal.Decision || len(projection.AvailableOptions) < 2 || strings.TrimSpace(projection.DecisionReason) == "" || len(projection.PlanConstraints) == 0 || len(projection.CausalChain) == 0) {
		return r, fmt.Errorf("world arbitration protagonist projection is not bound to the protagonist proposal")
	}
	if r.Version == WorldArbitrationReceiptV2Version {
		if HasCharacterSelfChronologyPolicyV1(stimulus.Sources) {
			for i := range r.Resolutions {
				var err error
				r.Resolutions[i].SelfExecutions, err = canonicalSelfExecutionsV1(r.Resolutions[i].SelfExecutions)
				if err != nil {
					return r, err
				}
			}
		}
		physical, err := applyArbitrationPhysicalStateWithArtifactSourcesV1(r, stimulus, proposals, priorSources)
		if err != nil {
			return r, err
		}
		for i := range r.Resolutions {
			for _, actor := range physical.Actors {
				if actor.AgentID == r.Resolutions[i].AgentID {
					copy := actor
					r.Resolutions[i].PostState = &copy
					break
				}
			}
		}
		if HasCharacterResourceObservationTimePolicyV1(stimulus.Sources) {
			r.ResourceSettlements = canonicalResourceObservationSettlementsV1(r.ResourceSettlements)
		}
		for i := range r.ResourceSettlements {
			r.ResourceSettlements[i].EvidenceRefs = normalizeV2Strings(r.ResourceSettlements[i].EvidenceRefs)
		}
	}
	digest, err := ComputeWorldArbitrationReceiptDigest(r)
	if err != nil {
		return r, err
	}
	r.Digest = digest
	return r, nil
}

func (r WorldArbitrationReceipt) CharacterDecisions(proposals []CharacterDecisionProposal, physical ...WorldPhysicalStateV2) ([]CharacterWorldDecision, error) {
	if r.Version == WorldArbitrationReceiptV2Version {
		if len(physical) != 1 {
			return nil, fmt.Errorf("v2 character decisions require the complete applied physical state")
		}
		if err := ValidateWorldPhysicalStateV2(physical[0]); err != nil {
			return nil, err
		}
	}
	byDigest := make(map[string]CharacterDecisionProposal, len(proposals))
	for _, proposal := range proposals {
		byDigest[proposal.Digest] = proposal
	}
	out := make([]CharacterWorldDecision, 0, len(r.Resolutions))
	for _, resolution := range r.Resolutions {
		proposal, ok := byDigest[resolution.ProposalDigest]
		if !ok {
			return nil, fmt.Errorf("missing proposal %s", resolution.ProposalDigest)
		}
		location := proposal.Location
		resources := append([]string(nil), proposal.Resources...)
		var postState *CharacterPhysicalStateV2
		if r.Version == WorldArbitrationReceiptV2Version {
			if resolution.PostState == nil {
				return nil, fmt.Errorf("v2 character decision lacks post_state")
			}
			views, err := BuildCharacterResourceViewsV2(physical[0], resolution.AgentID)
			if err != nil {
				return nil, err
			}
			resources = FormatCharacterResourceViewsV2(views)
			for _, actor := range physical[0].Actors {
				if actor.AgentID == resolution.AgentID {
					if !samePhysicalValueV2(actor, *resolution.PostState) {
						return nil, fmt.Errorf("v2 physical actor differs from arbitration post_state")
					}
					copy := actor
					postState = &copy
					break
				}
			}
			location = resolution.PostState.Location
		}
		out = append(out, CharacterWorldDecision{
			Character:         proposal.Character,
			Time:              proposal.Time,
			Location:          location,
			CurrentGoal:       proposal.CurrentGoal,
			Pressure:          proposal.Pressure,
			Resources:         resources,
			KnowledgeBoundary: strings.Join(proposal.KnowledgeRefs, ","),
			MechanismRefs:     normalizeV2Strings(append(append([]string(nil), proposal.MechanismRefs...), resolution.MechanismRefs...)),
			AvailableOptions:  append([]string(nil), proposal.AvailableOptions...),
			Decision:          proposal.Decision,
			DecisionReason:    proposal.DecisionReason,
			Action:            proposal.IntendedAction,
			ActionDuration:    proposal.ActionDuration,
			CompletionState:   resolution.CompletionState,
			ImmediateResult:   resolution.ImmediateResult,
			StateAfter:        resolution.StateAfter,
			PostState:         postState,
			VisibleToPOV:      resolution.VisibleToPOV,
			ButterflyEffects:  append([]DecisionButterflyEffect(nil), resolution.ButterflyEffects...),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Character < out[j].Character })
	return out, nil
}

type CharacterAgentMemoryFact struct {
	ID            string   `json:"id"`
	Chapter       int      `json:"chapter"`
	Kind          string   `json:"kind"`
	Text          string   `json:"text"`
	SourceDigest  string   `json:"source_digest"`
	KnowledgeRefs []string `json:"knowledge_refs,omitempty"`
	Accepted      bool     `json:"accepted"`
}

type CharacterAgentMemory struct {
	Version             string                     `json:"version"`
	AgentID             string                     `json:"agent_id"`
	Character           string                     `json:"character"`
	State               string                     `json:"state"` // canonical / projected
	GenerationID        string                     `json:"generation_id,omitempty"`
	LastAcceptedChapter int                        `json:"last_accepted_chapter,omitempty"`
	Facts               []CharacterAgentMemoryFact `json:"facts,omitempty"`
	UpdatedAt           string                     `json:"updated_at,omitempty"`
	MemoryRoot          string                     `json:"memory_root"`
}

func ComputeCharacterAgentMemoryRoot(m CharacterAgentMemory) (string, error) {
	m.MemoryRoot = ""
	return characterAgentDigest(m)
}

func FinalizeCharacterAgentMemory(m CharacterAgentMemory) (CharacterAgentMemory, error) {
	if m.Version == "" {
		m.Version = CharacterAgentMemoryVersion
	}
	if m.Version != CharacterAgentMemoryVersion || m.AgentID == "" || m.Character == "" {
		return m, fmt.Errorf("character memory identity is incomplete")
	}
	if m.State != "canonical" && m.State != "projected" {
		return m, fmt.Errorf("character memory has invalid state %q", m.State)
	}
	if m.State == "projected" && m.GenerationID == "" {
		return m, fmt.Errorf("projected character memory requires generation_id")
	}
	seen := map[string]struct{}{}
	for i := range m.Facts {
		fact := &m.Facts[i]
		fact.KnowledgeRefs = normalizeV2Strings(fact.KnowledgeRefs)
		if fact.ID == "" || fact.Chapter <= 0 || fact.Text == "" || fact.SourceDigest == "" {
			return m, fmt.Errorf("character memory fact is incomplete")
		}
		if _, duplicate := seen[fact.ID]; duplicate {
			return m, fmt.Errorf("duplicate character memory fact %q", fact.ID)
		}
		seen[fact.ID] = struct{}{}
		if m.State == "canonical" && !fact.Accepted {
			return m, fmt.Errorf("canonical memory cannot contain projected fact %q", fact.ID)
		}
	}
	sort.Slice(m.Facts, func(i, j int) bool {
		if m.Facts[i].Chapter == m.Facts[j].Chapter {
			return m.Facts[i].ID < m.Facts[j].ID
		}
		return m.Facts[i].Chapter < m.Facts[j].Chapter
	})
	root, err := ComputeCharacterAgentMemoryRoot(m)
	if err != nil {
		return m, err
	}
	m.MemoryRoot = root
	return m, nil
}

type CharacterAgentUsage struct {
	UsageID       string   `json:"usage_id,omitempty"`
	GenerationID  string   `json:"generation_id"`
	Role          string   `json:"role"` // character / world_arbiter
	AgentID       string   `json:"agent_id"`
	Character     string   `json:"character"`
	Chapter       int      `json:"chapter"`
	Cycle         int      `json:"cycle,omitempty"` // zero preserves historical single-round usage
	Round         int      `json:"round"`
	Input         int      `json:"input"`
	Output        int      `json:"output"`
	CacheRead     int      `json:"cache_read,omitempty"`
	CacheWrite    int      `json:"cache_write,omitempty"`
	CostUSD       float64  `json:"cost_usd,omitempty"`
	CostSource    string   `json:"cost_source,omitempty"` // reported / estimated / unknown
	Model         string   `json:"model,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Models        []string `json:"models,omitempty"` // provider/model identities when a run uses more than one
	Status        string   `json:"status,omitempty"` // success / failed / canceled
	ErrorCategory string   `json:"error_category,omitempty"`
	Attempts      int      `json:"attempts,omitempty"`
	UnpricedCalls int      `json:"unpriced_calls,omitempty"`
}

// CharacterAgentEvidenceBundle is the sealed, server-only proof for a
// simulation v2. It contains every observation/proposal/arbitration round used
// to reach the final result, so the projected bundle can be verified without
// trusting mutable workspace files. It is deliberately removed from prose
// render contexts.
type CharacterAgentEvidenceBundle struct {
	Version        string                       `json:"version"`
	GenerationID   string                       `json:"generation_id"`
	Chapter        int                          `json:"chapter"`
	Registry       CharacterAgentRegistry       `json:"registry"`
	Stimulus       WorldStimulusPacket          `json:"stimulus"`
	Activation     CharacterAgentActivation     `json:"activation"`
	Observations   []CharacterObservationPacket `json:"observations"`
	Proposals      []CharacterDecisionProposal  `json:"proposals"`
	Arbitrations   []WorldArbitrationReceipt    `json:"arbitrations"`
	MemoryRoots    []string                     `json:"memory_roots,omitempty"`
	Usage          []CharacterAgentUsage        `json:"usage,omitempty"`
	ProtocolDigest string                       `json:"protocol_digest"`
	EvidenceRoot   string                       `json:"evidence_root"`
}

func ComputeCharacterAgentEvidenceRoot(e CharacterAgentEvidenceBundle) (string, error) {
	e.EvidenceRoot = ""
	return characterAgentDigest(e)
}

func FinalizeCharacterAgentEvidenceBundle(e CharacterAgentEvidenceBundle) (CharacterAgentEvidenceBundle, error) {
	return finalizeCharacterEvidenceBundle(e, false)
}

// A hard-conflict proof is auditable but cannot be used as a completed world
// simulation. The distinct version prevents an open result entering a sealed
// legacy bundle through the ordinary evidence validator.
func FinalizeCharacterHardConflictEvidenceBundle(e CharacterAgentEvidenceBundle) (CharacterAgentEvidenceBundle, error) {
	return finalizeCharacterEvidenceBundle(e, true)
}

func finalizeCharacterEvidenceBundle(e CharacterAgentEvidenceBundle, hardConflictOnly bool) (CharacterAgentEvidenceBundle, error) {
	// Validation/finalization may sort and normalize nested slices. A value
	// parameter does not detach those slices from an immutable bundle shared by
	// concurrent store handles. Clone the serializable proof before touching it;
	// the JSON representation (and historical digest semantics) stays unchanged.
	raw, cloneErr := json.Marshal(e)
	if cloneErr != nil {
		return e, fmt.Errorf("clone character-agent evidence: %w", cloneErr)
	}
	var owned CharacterAgentEvidenceBundle
	if cloneErr := json.Unmarshal(raw, &owned); cloneErr != nil {
		return e, fmt.Errorf("clone character-agent evidence: %w", cloneErr)
	}
	e = owned
	expectedVersion := CharacterAgentEvidenceVersion
	if hardConflictOnly {
		expectedVersion = CharacterHardConflictEvidenceVersion
	}
	if e.Version == "" {
		e.Version = expectedVersion
	}
	if e.Version != expectedVersion || e.GenerationID == "" || e.Chapter <= 0 || strings.TrimSpace(e.ProtocolDigest) == "" {
		return e, fmt.Errorf("character-agent evidence identity is incomplete")
	}

	registryRoot := e.Registry.RegistryRoot
	registry, err := FinalizeCharacterAgentRegistry(e.Registry)
	if err != nil || registry.RegistryRoot != registryRoot {
		return e, fmt.Errorf("character-agent evidence registry: root mismatch: %w", err)
	}
	e.Registry = registry

	stimulusDigest := e.Stimulus.Digest
	stimulus, err := FinalizeWorldStimulusPacket(e.Stimulus)
	if err != nil || stimulus.Digest != stimulusDigest || stimulus.GenerationID != e.GenerationID || stimulus.Chapter != e.Chapter {
		return e, fmt.Errorf("character-agent evidence stimulus: identity/digest mismatch: %w", err)
	}
	e.Stimulus = stimulus

	activationDigest := e.Activation.Digest
	activation, err := FinalizeCharacterAgentActivation(e.Activation)
	if err != nil || activation.Digest != activationDigest || activation.GenerationID != e.GenerationID || activation.Chapter != e.Chapter || activation.RegistryRoot != registry.RegistryRoot {
		return e, fmt.Errorf("character-agent evidence activation: identity/digest mismatch: %w", err)
	}
	e.Activation = activation

	type observationKey struct {
		agentID string
		round   int
	}
	observations := make(map[observationKey]CharacterObservationPacket, len(e.Observations))
	for i := range e.Observations {
		storedDigest := e.Observations[i].Digest
		observation, finalizeErr := FinalizeCharacterObservationPacket(e.Observations[i])
		if finalizeErr != nil || observation.Digest != storedDigest || observation.GenerationID != e.GenerationID || observation.Chapter != e.Chapter || observation.StimulusDigest != stimulus.Digest {
			return e, fmt.Errorf("character-agent evidence observation[%d]: identity/digest mismatch: %w", i, finalizeErr)
		}
		if (stimulus.Version == WorldStimulusPacketV2Version) != (observation.Version == CharacterObservationV2Version) {
			return e, fmt.Errorf("character-agent evidence mixes observation/stimulus protocol versions")
		}
		if stimulus.Version == WorldStimulusPacketV2Version {
			if err := validateCharacterResourceViewsAgainstStimulusV2(stimulus, observation); err != nil {
				return e, err
			}
			for _, actor := range stimulus.PhysicalState.Actors {
				if actor.AgentID == observation.AgentID && (actor.Character != observation.Character || actor.Location != observation.Location) {
					return e, fmt.Errorf("v2 observation origin differs from actual physical actor")
				}
			}
		}
		key := observationKey{agentID: observation.AgentID, round: observation.Round}
		if _, duplicate := observations[key]; duplicate {
			return e, fmt.Errorf("character-agent evidence has duplicate observation for %s round %d", observation.AgentID, observation.Round)
		}
		observations[key] = observation
		e.Observations[i] = observation
	}
	sort.Slice(e.Observations, func(i, j int) bool {
		if e.Observations[i].Round == e.Observations[j].Round {
			return e.Observations[i].AgentID < e.Observations[j].AgentID
		}
		return e.Observations[i].Round < e.Observations[j].Round
	})

	activeIDs := make(map[string]CharacterAgentActivationEntry)
	for _, entry := range activation.Entries {
		if entry.State != CharacterAgentActive {
			continue
		}
		activeIDs[entry.AgentID] = entry
		observation, ok := observations[observationKey{agentID: entry.AgentID, round: 1}]
		if !ok || observation.Digest != entry.ObservationDigest {
			return e, fmt.Errorf("character-agent evidence is missing bound round-1 observation for %s", entry.AgentID)
		}
	}
	if len(activeIDs) == 0 {
		return e, fmt.Errorf("character-agent evidence has no active characters")
	}

	proposalsByRound := make(map[int][]CharacterDecisionProposal)
	seenProposal := make(map[observationKey]struct{}, len(e.Proposals))
	for i := range e.Proposals {
		key := observationKey{agentID: e.Proposals[i].AgentID, round: e.Proposals[i].Round}
		observation, ok := observations[key]
		if !ok {
			return e, fmt.Errorf("character-agent evidence proposal[%d] has no observation", i)
		}
		storedDigest := e.Proposals[i].Digest
		proposal, finalizeErr := FinalizeCharacterDecisionProposal(e.Proposals[i], observation)
		if finalizeErr != nil || proposal.Digest != storedDigest || proposal.GenerationID != e.GenerationID || proposal.Chapter != e.Chapter {
			return e, fmt.Errorf("character-agent evidence proposal[%d]: identity/digest mismatch: %w", i, finalizeErr)
		}
		if _, active := activeIDs[proposal.AgentID]; !active {
			return e, fmt.Errorf("character-agent evidence proposal belongs to inactive agent %s", proposal.AgentID)
		}
		if _, duplicate := seenProposal[key]; duplicate {
			return e, fmt.Errorf("character-agent evidence has duplicate proposal for %s round %d", proposal.AgentID, proposal.Round)
		}
		seenProposal[key] = struct{}{}
		proposalsByRound[proposal.Round] = append(proposalsByRound[proposal.Round], proposal)
		e.Proposals[i] = proposal
	}
	sort.Slice(e.Proposals, func(i, j int) bool {
		if e.Proposals[i].Round == e.Proposals[j].Round {
			return e.Proposals[i].AgentID < e.Proposals[j].AgentID
		}
		return e.Proposals[i].Round < e.Proposals[j].Round
	})

	if len(e.Arbitrations) == 0 {
		return e, fmt.Errorf("character-agent evidence has no arbitration")
	}
	sort.Slice(e.Arbitrations, func(i, j int) bool { return e.Arbitrations[i].Round < e.Arbitrations[j].Round })
	latest := make(map[string]CharacterDecisionProposal, len(activeIDs))
	maxRevisionRounds := 1
	for i := range e.Arbitrations {
		arbitration := e.Arbitrations[i]
		if arbitration.Round != i+1 {
			return e, fmt.Errorf("character-agent evidence arbitration rounds are not contiguous")
		}
		for _, proposal := range proposalsByRound[arbitration.Round] {
			latest[proposal.AgentID] = proposal
		}
		if len(latest) != len(activeIDs) {
			return e, fmt.Errorf("character-agent evidence arbitration round %d has incomplete proposals", arbitration.Round)
		}
		current := make([]CharacterDecisionProposal, 0, len(latest))
		for _, proposal := range latest {
			current = append(current, proposal)
		}
		storedDigest := arbitration.Digest
		finalized, finalizeErr := FinalizeWorldArbitrationReceipt(arbitration, stimulus, activation, current, maxRevisionRounds)
		if finalizeErr != nil || finalized.Digest != storedDigest {
			return e, fmt.Errorf("character-agent evidence arbitration round %d: digest mismatch: %w", arbitration.Round, finalizeErr)
		}
		if i < len(e.Arbitrations)-1 && finalized.Finalized {
			return e, fmt.Errorf("character-agent evidence finalized before its last arbitration")
		}
		if i == len(e.Arbitrations)-1 {
			if hardConflictOnly {
				if finalized.Finalized || finalized.HardContractStatus != "infeasible" {
					return e, fmt.Errorf("hard-conflict evidence requires an uncommitted hard-infeasible final receipt")
				}
			} else if !finalized.Finalized {
				return e, fmt.Errorf("character-agent evidence final arbitration is not closed")
			}
		}
		e.Arbitrations[i] = finalized
	}

	e.MemoryRoots = normalizeV2Strings(e.MemoryRoots)
	if len(e.MemoryRoots) != len(activeIDs) {
		return e, fmt.Errorf("character-agent evidence memory root set is incomplete")
	}
	for i, usage := range e.Usage {
		if usage.AgentID == "" || usage.GenerationID != e.GenerationID || usage.Chapter != e.Chapter || usage.Round <= 0 || usage.Round > e.Arbitrations[len(e.Arbitrations)-1].Round || usage.Input < 0 || usage.Output < 0 || usage.CostUSD < 0 || usage.Attempts < 0 || usage.UnpricedCalls < 0 {
			return e, fmt.Errorf("character-agent evidence usage[%d] is invalid", i)
		}
		if usage.CostSource != "" && usage.CostSource != "reported" && usage.CostSource != "estimated" && usage.CostSource != "unknown" {
			return e, fmt.Errorf("character-agent evidence usage[%d] has invalid cost source", i)
		}
		if usage.Status != "" && usage.Status != "success" && usage.Status != "failed" && usage.Status != "canceled" {
			return e, fmt.Errorf("character-agent evidence usage[%d] has invalid status", i)
		}
		if usage.Role != "character" && usage.Role != "world_arbiter" {
			return e, fmt.Errorf("character-agent evidence usage[%d] has invalid role %q", i, usage.Role)
		}
		if _, active := activeIDs[usage.AgentID]; usage.Role == "character" && !active {
			return e, fmt.Errorf("character-agent evidence usage belongs to inactive agent %s", usage.AgentID)
		}
	}
	sort.Slice(e.Usage, func(i, j int) bool {
		if e.Usage[i].Round == e.Usage[j].Round {
			return e.Usage[i].AgentID < e.Usage[j].AgentID
		}
		return e.Usage[i].Round < e.Usage[j].Round
	})
	root, err := ComputeCharacterAgentEvidenceRoot(e)
	if err != nil {
		return e, err
	}
	e.EvidenceRoot = root
	return e, nil
}

func ValidateCharacterAgentEvidenceBundle(e CharacterAgentEvidenceBundle) error {
	storedRoot := e.EvidenceRoot
	finalized, err := FinalizeCharacterAgentEvidenceBundle(e)
	if err != nil {
		return err
	}
	if storedRoot == "" || finalized.EvidenceRoot != storedRoot {
		return fmt.Errorf("character-agent evidence root mismatch")
	}
	return nil
}

func characterAgentDigest(value any) (string, error) {
	sum, err := DeterministicPlanningHash(value)
	if err != nil {
		return "", err
	}
	return "sha256:" + sum, nil
}

func ValidateCharacterHardConflictEvidenceBundle(e CharacterAgentEvidenceBundle) error {
	finalized, err := FinalizeCharacterHardConflictEvidenceBundle(e)
	if err != nil {
		return err
	}
	if e.EvidenceRoot == "" || finalized.EvidenceRoot != e.EvidenceRoot {
		return fmt.Errorf("character hard-conflict evidence root mismatch")
	}
	return nil
}
