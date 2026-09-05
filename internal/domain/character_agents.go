package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const (
	CharacterAgentRegistryVersion         = "character-agent-registry.v1"
	CharacterAgentActivationVersion       = "character-agent-activation.v1"
	WorldStimulusPacketVersion            = "world-stimulus-packet.v1"
	CharacterObservationVersion           = "character-observation-packet.v1"
	CharacterDecisionProposalVersion      = "character-decision-proposal.v1"
	WorldArbitrationReceiptVersion        = "world-arbitration-receipt.v1"
	CharacterAgentMemoryVersion           = "character-agent-memory.v1"
	CharacterAgentEvidenceVersion         = "character-agent-evidence.v1"
	CharacterAgentSuccessorPlanVersion    = "character-agent-successor-plan.v1"
	CharacterAgentDecisionProtocolVersion = "character-agent-protocol.v1"

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

type WorldStimulusPacket struct {
	Version       string               `json:"version"`
	GenerationID  string               `json:"generation_id"`
	Chapter       int                  `json:"chapter"`
	TimeWindow    string               `json:"time_window"`
	PublicFacts   []CharacterAgentFact `json:"public_facts,omitempty"`
	CurrentEvents []CharacterAgentFact `json:"current_events,omitempty"`
	HardContracts []string             `json:"hard_contracts,omitempty"`
	SoftGuidance  []string             `json:"soft_guidance,omitempty"`
	Sources       []string             `json:"sources,omitempty"`
	GeneratedAt   string               `json:"generated_at,omitempty"`
	Digest        string               `json:"digest"`
}

func ComputeWorldStimulusPacketDigest(p WorldStimulusPacket) (string, error) {
	p.Digest = ""
	return characterAgentDigest(p)
}

func FinalizeWorldStimulusPacket(p WorldStimulusPacket) (WorldStimulusPacket, error) {
	if p.Version == "" {
		p.Version = WorldStimulusPacketVersion
	}
	if p.Version != WorldStimulusPacketVersion || p.Chapter <= 0 || strings.TrimSpace(p.GenerationID) == "" {
		return p, fmt.Errorf("world stimulus identity is incomplete")
	}
	p.HardContracts = normalizeV2Strings(p.HardContracts)
	p.SoftGuidance = normalizeV2Strings(p.SoftGuidance)
	p.Sources = normalizeV2Strings(p.Sources)
	digest, err := ComputeWorldStimulusPacketDigest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

type CharacterObservationPacket struct {
	Version          string                     `json:"version"`
	GenerationID     string                     `json:"generation_id"`
	Chapter          int                        `json:"chapter"`
	Round            int                        `json:"round"`
	AgentID          string                     `json:"agent_id"`
	Character        string                     `json:"character"`
	Tier             string                     `json:"tier"`
	TimeWindow       string                     `json:"time_window"`
	Location         string                     `json:"location,omitempty"`
	CurrentGoal      string                     `json:"current_goal"`
	Pressure         string                     `json:"pressure"`
	Resources        []string                   `json:"resources,omitempty"`
	Relationships    []string                   `json:"relationships,omitempty"`
	Commitments      []string                   `json:"commitments,omitempty"`
	KnownFacts       []CharacterAgentFact       `json:"known_facts,omitempty"`
	PerceivedEvents  []CharacterAgentFact       `json:"perceived_events,omitempty"`
	PublicRules      []CharacterAgentFact       `json:"public_rules,omitempty"`
	Memory           []CharacterAgentMemoryFact `json:"memory,omitempty"`
	ConflictFeedback []string                   `json:"conflict_feedback,omitempty"`
	StimulusDigest   string                     `json:"stimulus_digest"`
	MemoryRoot       string                     `json:"memory_root,omitempty"`
	Sources          []string                   `json:"sources,omitempty"`
	GeneratedAt      string                     `json:"generated_at,omitempty"`
	Digest           string                     `json:"digest"`
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
	return out
}

func ComputeCharacterObservationDigest(p CharacterObservationPacket) (string, error) {
	p.Digest = ""
	return characterAgentDigest(p)
}

func FinalizeCharacterObservationPacket(p CharacterObservationPacket) (CharacterObservationPacket, error) {
	if p.Version == "" {
		p.Version = CharacterObservationVersion
	}
	if p.Version != CharacterObservationVersion || p.GenerationID == "" || p.Chapter <= 0 || p.Round <= 0 || p.AgentID == "" || p.Character == "" {
		return p, fmt.Errorf("character observation identity is incomplete")
	}
	if p.Round > 1 && len(p.ConflictFeedback) == 0 {
		return p, fmt.Errorf("revision observation must contain conflict feedback")
	}
	p.Resources = normalizeV2Strings(p.Resources)
	p.Relationships = normalizeV2Strings(p.Relationships)
	p.Commitments = normalizeV2Strings(p.Commitments)
	p.ConflictFeedback = normalizeV2Strings(p.ConflictFeedback)
	p.Sources = normalizeV2Strings(p.Sources)
	digest, err := ComputeCharacterObservationDigest(p)
	if err != nil {
		return p, err
	}
	p.Digest = digest
	return p, nil
}

type CharacterDecisionProposal struct {
	Version              string   `json:"version"`
	GenerationID         string   `json:"generation_id"`
	Chapter              int      `json:"chapter"`
	Round                int      `json:"round"`
	AgentID              string   `json:"agent_id"`
	Character            string   `json:"character"`
	ObservationDigest    string   `json:"observation_digest"`
	Time                 string   `json:"time,omitempty"`
	Location             string   `json:"location"`
	CurrentGoal          string   `json:"current_goal"`
	Pressure             string   `json:"pressure"`
	Resources            []string `json:"resources,omitempty"`
	AvailableOptions     []string `json:"available_options"`
	RejectedOptions      []string `json:"rejected_options,omitempty"`
	Decision             string   `json:"decision"`
	DecisionReason       string   `json:"decision_reason"`
	IntendedAction       string   `json:"intended_action"`
	ActionDuration       string   `json:"action_duration"`
	KnowledgeRefs        []string `json:"knowledge_refs"`
	ResourceClaims       []string `json:"resource_claims,omitempty"`
	Constraints          []string `json:"constraints,omitempty"`
	Contingencies        []string `json:"contingencies,omitempty"`
	ExpectedConsequences []string `json:"expected_consequences,omitempty"`
	SubmittedAt          string   `json:"submitted_at,omitempty"`
	Digest               string   `json:"digest"`
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
	if strings.TrimSpace(p.Location) == "" || strings.TrimSpace(p.CurrentGoal) == "" || strings.TrimSpace(p.Pressure) == "" || len(p.AvailableOptions) < 2 || strings.TrimSpace(p.Decision) == "" || strings.TrimSpace(p.DecisionReason) == "" || strings.TrimSpace(p.IntendedAction) == "" || strings.TrimSpace(p.ActionDuration) == "" || len(p.KnowledgeRefs) == 0 {
		return p, fmt.Errorf("character proposal is incomplete")
	}
	p.Resources = normalizeV2Strings(p.Resources)
	p.AvailableOptions = normalizeV2Strings(p.AvailableOptions)
	p.RejectedOptions = normalizeV2Strings(p.RejectedOptions)
	p.KnowledgeRefs = normalizeV2Strings(p.KnowledgeRefs)
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
	AgentID          string                    `json:"agent_id"`
	Character        string                    `json:"character"`
	ProposalDigest   string                    `json:"proposal_digest"`
	Decision         string                    `json:"decision"`
	IntendedAction   string                    `json:"intended_action"`
	ActionOrder      int                       `json:"action_order"`
	Outcome          string                    `json:"outcome"` // success / partial / blocked
	CompletionState  string                    `json:"completion_state"`
	ImmediateResult  string                    `json:"immediate_result"`
	StateAfter       string                    `json:"state_after"`
	VisibleToPOV     bool                      `json:"visible_to_pov,omitempty"`
	ButterflyEffects []DecisionButterflyEffect `json:"butterfly_effects"`
	ConflictIDs      []string                  `json:"conflict_ids,omitempty"`
}

type WorldArbitrationReceipt struct {
	Version               string                        `json:"version"`
	GenerationID          string                        `json:"generation_id"`
	Chapter               int                           `json:"chapter"`
	Round                 int                           `json:"round"`
	StimulusDigest        string                        `json:"stimulus_digest"`
	ActivationDigest      string                        `json:"activation_digest"`
	ProposalDigests       []string                      `json:"proposal_digests"`
	Resolutions           []CharacterDecisionResolution `json:"resolutions"`
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

func FinalizeWorldArbitrationReceipt(r WorldArbitrationReceipt, stimulus WorldStimulusPacket, activation CharacterAgentActivation, proposals []CharacterDecisionProposal, maxRevisionRounds int) (WorldArbitrationReceipt, error) {
	if r.Version == "" {
		r.Version = WorldArbitrationReceiptVersion
	}
	if r.Version != WorldArbitrationReceiptVersion || r.GenerationID != stimulus.GenerationID || r.Chapter != stimulus.Chapter || r.StimulusDigest != stimulus.Digest || r.ActivationDigest != activation.Digest || r.Round <= 0 || r.Round > maxRevisionRounds+1 {
		return r, fmt.Errorf("world arbitration identity is incomplete")
	}
	byAgent := make(map[string]CharacterDecisionProposal, len(proposals))
	wantedDigests := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		if proposal.Round <= 0 || proposal.Round > r.Round {
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
	for i := range r.Resolutions {
		resolution := &r.Resolutions[i]
		proposal, ok := byAgent[resolution.AgentID]
		if !ok || resolution.Character != proposal.Character || resolution.ProposalDigest != proposal.Digest {
			return r, fmt.Errorf("resolution for %s is not bound to a proposal", resolution.AgentID)
		}
		if resolution.Decision != proposal.Decision || resolution.IntendedAction != proposal.IntendedAction {
			return r, fmt.Errorf("arbiter rewrote intent for %s", resolution.AgentID)
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
		return r, fmt.Errorf("world arbitration exhausted revision rounds")
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
	if protagonistProposal == nil || projection.ChosenDecision != protagonistProposal.Decision || len(projection.AvailableOptions) < 2 || strings.TrimSpace(projection.DecisionReason) == "" || len(projection.PlanConstraints) == 0 || len(projection.CausalChain) == 0 {
		return r, fmt.Errorf("world arbitration protagonist projection is not bound to the protagonist proposal")
	}
	digest, err := ComputeWorldArbitrationReceiptDigest(r)
	if err != nil {
		return r, err
	}
	r.Digest = digest
	return r, nil
}

func (r WorldArbitrationReceipt) CharacterDecisions(proposals []CharacterDecisionProposal) ([]CharacterWorldDecision, error) {
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
		out = append(out, CharacterWorldDecision{
			Character:         proposal.Character,
			Time:              proposal.Time,
			Location:          proposal.Location,
			CurrentGoal:       proposal.CurrentGoal,
			Pressure:          proposal.Pressure,
			Resources:         append([]string(nil), proposal.Resources...),
			KnowledgeBoundary: strings.Join(proposal.KnowledgeRefs, ","),
			AvailableOptions:  append([]string(nil), proposal.AvailableOptions...),
			Decision:          proposal.Decision,
			DecisionReason:    proposal.DecisionReason,
			Action:            proposal.IntendedAction,
			ActionDuration:    proposal.ActionDuration,
			CompletionState:   resolution.CompletionState,
			ImmediateResult:   resolution.ImmediateResult,
			StateAfter:        resolution.StateAfter,
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
	GenerationID string  `json:"generation_id"`
	Role         string  `json:"role"` // character / world_arbiter
	AgentID      string  `json:"agent_id"`
	Character    string  `json:"character"`
	Chapter      int     `json:"chapter"`
	Round        int     `json:"round"`
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	CacheRead    int     `json:"cache_read,omitempty"`
	CacheWrite   int     `json:"cache_write,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
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
	if e.Version == "" {
		e.Version = CharacterAgentEvidenceVersion
	}
	if e.Version != CharacterAgentEvidenceVersion || e.GenerationID == "" || e.Chapter <= 0 || strings.TrimSpace(e.ProtocolDigest) == "" {
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
		if i == len(e.Arbitrations)-1 && !finalized.Finalized {
			return e, fmt.Errorf("character-agent evidence final arbitration is not closed")
		}
		e.Arbitrations[i] = finalized
	}

	e.MemoryRoots = normalizeV2Strings(e.MemoryRoots)
	if len(e.MemoryRoots) != len(activeIDs) {
		return e, fmt.Errorf("character-agent evidence memory root set is incomplete")
	}
	for i, usage := range e.Usage {
		if usage.AgentID == "" || usage.GenerationID != e.GenerationID || usage.Chapter != e.Chapter || usage.Round <= 0 || usage.Round > e.Arbitrations[len(e.Arbitrations)-1].Round || usage.Input < 0 || usage.Output < 0 || usage.CostUSD < 0 {
			return e, fmt.Errorf("character-agent evidence usage[%d] is invalid", i)
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
