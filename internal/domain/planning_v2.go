package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	PlanningGenerationV2Version             = "planning-generation.v2"
	PlanningSourceSnapshotV2Version         = "planning-source-snapshot.v2"
	ProjectedChapterBundleV2Version         = "projected-chapter-bundle.v2"
	ProjectedPlanningContextV2Version       = "projected-planning-context.v2"
	ProjectedDeltaV2Version                 = "projected-delta.v2"
	ObligationRegistryV2Version             = "obligation-registry.v2"
	ProjectedChainManifestV2Version         = "projected-chain-manifest.v2"
	SealReceiptV2Version                    = "seal-receipt.v2"
	ActivePlanningGenerationV2Version       = "active-planning-generation.v2"
	PromotionReceiptV2Version               = "promotion-receipt.v2"
	ActualOutcomeReceiptV2Version           = "actual-outcome-receipt.v2"
	SuffixInvalidationV2Version             = "suffix-invalidation-receipt.v2"
	GenerationArchiveV2Version              = "generation-archive-receipt.v2"
	SealedProjectionRenderContractV2Version = "sealed-projection-render-contract.v2"

	ProjectedAuthorityV2                 = "projected_non_canon"
	ProjectedStateV2                     = "projected_non_canon"
	FormalProjectionLevelV2              = "formal"
	ExactPromotionModeV2                 = "exact"
	PlanningV2DigestPrefix               = "sha256:"
	PlanningGenerationIDPrefix           = "pg2_"
	ProjectedPlanningContextSourcePrefix = "project-all-state:"
	CraftRecallReceiptTokenPrefix        = "craft_recall_receipt:"
	ProjectAllCraftReceiptStage          = "project_all"
)

type PlanningProjectionScopeV2 string

const (
	// PlanningProjectionScopeArcV2 marks a generation whose immutable bundle
	// range is one story arc while its obligation horizon may extend through the
	// rest of the book. The empty value remains the legacy whole-generation
	// contract so existing persisted v2 fixtures keep their exact semantics.
	PlanningProjectionScopeArcV2 PlanningProjectionScopeV2 = "arc"
)

func ProjectedPlanningContextSourceTokenV2(contextDigest string) (string, error) {
	contextDigest = strings.TrimSpace(contextDigest)
	if err := validatePlanningV2Digest("context_digest", contextDigest); err != nil {
		return "", err
	}
	return ProjectedPlanningContextSourcePrefix + contextDigest, nil
}

type PlanningGenerationStatusV2 string

const (
	PlanningGenerationBuildingV2 PlanningGenerationStatusV2 = "building"
	PlanningGenerationSealedV2   PlanningGenerationStatusV2 = "sealed"

	PlanningGenerationV2StatusBuilding = PlanningGenerationBuildingV2
	PlanningGenerationV2StatusSealed   = PlanningGenerationSealedV2
)

type PlanningGenerationV2 struct {
	Version                string                     `json:"version"`
	GenerationID           string                     `json:"generation_id"`
	ParentGenerationID     string                     `json:"parent_generation_id"`
	ProjectionScope        PlanningProjectionScopeV2  `json:"projection_scope,omitempty"`
	ScopeID                string                     `json:"scope_id,omitempty"`
	BookHorizonChapter     int                        `json:"book_horizon_chapter,omitempty"`
	Status                 PlanningGenerationStatusV2 `json:"status"`
	BaseCanonChapter       int                        `json:"base_canon_chapter"`
	BaseCanonRoot          string                     `json:"base_canon_root"`
	BaseStateRoot          string                     `json:"base_state_root"`
	StableOutlineRoot      string                     `json:"stable_outline_root"`
	PlanningDependencyRoot string                     `json:"planning_dependency_root"`
	RandomSeedContractRoot string                     `json:"random_seed_contract_root"`
	AttemptID              string                     `json:"attempt_id"`
	FirstProjectedChapter  int                        `json:"first_projected_chapter"`
	LastProjectedChapter   int                        `json:"last_projected_chapter"`
	ExpectedChapterCount   int                        `json:"expected_chapter_count"`
	ProjectedChapterCount  int                        `json:"projected_chapter_count"`
	ChainHeadRoot          string                     `json:"chain_head_root"`
	ChainTailRoot          string                     `json:"chain_tail_root"`
	ObligationRegistryRoot string                     `json:"obligation_registry_root"`
	CreatedAt              string                     `json:"created_at"`
	SealedAt               string                     `json:"sealed_at"`
	GenerationDigest       string                     `json:"generation_digest"`
}

// PlanningSourceSnapshotV2 is a read-only fingerprint of canon and planning
// inputs. Projected stores may retain it, but must never mutate the canon store.
type PlanningSourceSnapshotV2 struct {
	Version                string `json:"version"`
	GenerationID           string `json:"generation_id"`
	BaseCanonChapter       int    `json:"base_canon_chapter"`
	BaseCanonRoot          string `json:"base_canon_root"`
	BaseStateRoot          string `json:"base_state_root"`
	StableOutlineRoot      string `json:"stable_outline_root"`
	PlanningDependencyRoot string `json:"planning_dependency_root"`
	RandomSeedContractRoot string `json:"random_seed_contract_root"`
	FoundationSnapshotRoot string `json:"foundation_snapshot_root"`
	RAGSnapshotRoot        string `json:"rag_snapshot_root"`
	CapturedAt             string `json:"captured_at"`
	SnapshotDigest         string `json:"snapshot_digest"`
}

type StateMutationV2 struct {
	StableID  string `json:"stable_id"`
	Subject   string `json:"subject"`
	Object    string `json:"object,omitempty"`
	Field     string `json:"field"`
	Operation string `json:"operation"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Cause     string `json:"cause"`
	Evidence  string `json:"evidence,omitempty"`
}

// RenderContractForStateMutationV2 is the canonical prose-facing statement
// for one structured transition. HardRenderContract change lists use this
// exact representation, allowing bundle validation and actual realization to
// prove that no free-floating "hard" change exists outside ProjectedDelta.
func RenderContractForStateMutationV2(mutation StateMutationV2) string {
	subject := strings.TrimSpace(mutation.Subject)
	if object := strings.TrimSpace(mutation.Object); object != "" {
		subject += "与" + object
	}
	return strings.TrimSpace(fmt.Sprintf(
		"%s的%s必须变为%s",
		subject,
		strings.TrimSpace(mutation.Field),
		strings.TrimSpace(mutation.After),
	))
}

// ProjectedDelta is deliberately category-structured. It cannot represent a
// coarse title/goal/conflict/hook summary as a completed formal transition.
type ProjectedDelta struct {
	Version        string            `json:"version"`
	Timeline       []StateMutationV2 `json:"timeline"`
	CharacterState []StateMutationV2 `json:"character_state"`
	Relationships  []StateMutationV2 `json:"relationship"`
	Resources      []StateMutationV2 `json:"resource"`
	Knowledge      []StateMutationV2 `json:"knowledge"`
	Locations      []StateMutationV2 `json:"location"`
	Foreshadows    []StateMutationV2 `json:"foreshadow"`
	Obligations    []StateMutationV2 `json:"obligation"`
}

type StructuredStateDeltaV2 = ProjectedDelta
type ProjectedDeltaV2 = ProjectedDelta

type SimulationStateFactV2 struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Field   string `json:"field"`
	Value   string `json:"value"`
}

type SimulationActorV2 struct {
	CharacterID      string   `json:"character_id"`
	Motivation       string   `json:"motivation"`
	KnownFacts       []string `json:"known_facts"`
	UnknownFacts     []string `json:"unknown_facts"`
	OffscreenState   string   `json:"offscreen_state"`
	AvailableActions []string `json:"available_actions"`
}

type SimulationCausalStepV2 struct {
	ID               string   `json:"id"`
	CauseIDs         []string `json:"cause_ids"`
	ActorID          string   `json:"actor_id"`
	Decision         string   `json:"decision"`
	ImmediateEffect  string   `json:"immediate_effect"`
	DownstreamEffect string   `json:"downstream_effect"`
}

type SimulationCounterfactualV2 struct {
	Choice      string `json:"choice"`
	RejectedBy  string `json:"rejected_by"`
	Consequence string `json:"consequence"`
}

type FormalWorldSimulationV2 struct {
	SimulationID       string                       `json:"simulation_id"`
	InitialConditions  []SimulationStateFactV2      `json:"initial_conditions"`
	Actors             []SimulationActorV2          `json:"actors"`
	AvailableChoices   []string                     `json:"available_choices"`
	ChosenDecision     string                       `json:"chosen_decision"`
	CausalSteps        []SimulationCausalStepV2     `json:"causal_steps"`
	Counterfactuals    []SimulationCounterfactualV2 `json:"counterfactuals"`
	TerminalConditions []SimulationStateFactV2      `json:"terminal_conditions"`
	TimeAdvance        string                       `json:"time_advance"`
	LocationFlow       []string                     `json:"location_flow"`
}

type POVCharacterMotivationV2 struct {
	CharacterID string `json:"character_id"`
	Goal        string `json:"goal"`
	Pressure    string `json:"pressure"`
	Choice      string `json:"choice"`
}

type POVOffscreenStateV2 struct {
	CharacterID  string `json:"character_id"`
	State        string `json:"state"`
	CausalImpact string `json:"causal_impact"`
}

type POVSceneV2 struct {
	SceneID        string   `json:"scene_id"`
	Location       string   `json:"location"`
	Time           string   `json:"time"`
	PresentActors  []string `json:"present_actors"`
	POVKnows       []string `json:"pov_knows"`
	POVDoesNotKnow []string `json:"pov_does_not_know"`
	CausalPurpose  string   `json:"causal_purpose"`
}

type POVPlanV2 struct {
	POVCharacterID    string                     `json:"pov_character_id"`
	KnowledgeBoundary []string                   `json:"knowledge_boundary"`
	Unknowns          []string                   `json:"unknowns"`
	Motivations       []POVCharacterMotivationV2 `json:"motivations"`
	OffscreenStates   []POVOffscreenStateV2      `json:"offscreen_states"`
	Scenes            []POVSceneV2               `json:"scenes"`
	TimeAdvance       string                     `json:"time_advance"`
}

type RevealBudgetItemV2 struct {
	FactID string `json:"fact_id"`
	Action string `json:"action"`
	Limit  string `json:"limit"`
}

type HardRenderContractV2 struct {
	MustOccur           []string             `json:"must_occur"`
	MustNotOccur        []string             `json:"must_not_occur"`
	MustPreserve        []string             `json:"must_preserve"`
	RevealBudget        []RevealBudgetItemV2 `json:"reveal_budget"`
	ForeshadowChanges   []string             `json:"foreshadow_changes"`
	ResourceChanges     []string             `json:"resource_changes"`
	RelationshipChanges []string             `json:"relationship_changes"`
	KnowledgeChanges    []string             `json:"knowledge_changes"`
}

type SourceBindingV2 struct {
	Kind            string   `json:"kind"`
	SourceID        string   `json:"source_id"`
	SourceDigest    string   `json:"source_digest"`
	ExactReferences []string `json:"exact_references"`
	UsableFacts     []string `json:"usable_facts"`
	Transformation  string   `json:"transformation"`
	DoNotUse        []string `json:"do_not_use"`
}

type ProjectedPlanningTransitionV2 struct {
	Chapter                int            `json:"chapter"`
	BundleDigest           string         `json:"bundle_digest"`
	ProjectedPostStateRoot string         `json:"projected_post_state_root"`
	Delta                  ProjectedDelta `json:"delta"`
}

type ProjectedPlanningObligationV2 struct {
	ID               string                `json:"id"`
	Kind             ObligationKindV2      `json:"kind"`
	Contract         string                `json:"contract"`
	Hardness         ObligationHardnessV2  `json:"hardness"`
	DueWindow        ObligationDueWindowV2 `json:"due_window"`
	ConsumerChapters []int                 `json:"consumer_chapters"`
	DueNow           bool                  `json:"due_now"`
}

type ProjectedPlanningStateFactV2 struct {
	Category       string `json:"category"`
	StableID       string `json:"stable_id"`
	Subject        string `json:"subject"`
	Object         string `json:"object,omitempty"`
	Field          string `json:"field"`
	Value          string `json:"value"`
	ThroughChapter int    `json:"through_chapter"`
}

// ProjectedPlanningPredecessorContractV2 is the exact model-authored outgoing
// consequence of the immediately preceding chapter. It is intentionally
// small: the next Planner must copy its identity/text into the incoming side
// of ArcChapterTransitionContract and name the causal beat that consumes it.
type ProjectedPlanningPredecessorContractV2 struct {
	Chapter                 int    `json:"chapter"`
	OutgoingConsequenceID   string `json:"outgoing_consequence_id"`
	OutgoingConsequenceText string `json:"outgoing_consequence_text"`
	BundleDigest            string `json:"bundle_digest"`
	ProjectedPostStateRoot  string `json:"projected_post_state_root"`
}

// ProjectedPlanningContextV2 is the exact projected state packet consumed by
// the next World Simulator and Planner. It is derived only from the durable
// bundle chain and obligation registry; shadow convenience ledgers are not a
// second authority.
type ProjectedPlanningContextV2 struct {
	Version             string                                  `json:"version"`
	GenerationID        string                                  `json:"generation_id"`
	NextChapter         int                                     `json:"next_chapter"`
	ThroughChapter      int                                     `json:"through_chapter"`
	StateRoot           string                                  `json:"state_root"`
	PredecessorContract *ProjectedPlanningPredecessorContractV2 `json:"predecessor_contract,omitempty"`
	CumulativeState     []ProjectedPlanningStateFactV2          `json:"cumulative_state"`
	RecentTransitions   []ProjectedPlanningTransitionV2         `json:"recent_transitions"`
	OpenObligations     []ProjectedPlanningObligationV2         `json:"open_obligations"`
	ContextDigest       string                                  `json:"context_digest"`
}

type ProjectedChapterBundle struct {
	Version                  string                  `json:"version"`
	GenerationID             string                  `json:"generation_id"`
	Chapter                  int                     `json:"chapter"`
	Authority                string                  `json:"authority"`
	State                    string                  `json:"state"`
	ProjectionLevel          string                  `json:"projection_level"`
	PreviousBundleDigest     string                  `json:"previous_bundle_digest"`
	ProjectedPreStateRoot    string                  `json:"projected_pre_state_root"`
	ChapterWorldSimulation   ChapterWorldSimulation  `json:"chapter_world_simulation"`
	ChapterPlan              ChapterPlan             `json:"chapter_plan"`
	FormalWorldSimulation    FormalWorldSimulationV2 `json:"formal_world_simulation"`
	POVPlan                  POVPlanV2               `json:"pov_plan"`
	HardRenderContract       HardRenderContractV2    `json:"hard_render_contract"`
	SourceBindings           []SourceBindingV2       `json:"source_bindings"`
	RAGFactReceipt           *RAGFactReceipt         `json:"rag_fact_receipt,omitempty"`
	RAGFactReceiptDigest     string                  `json:"rag_fact_receipt_digest,omitempty"`
	CraftRecallReceipt       *CraftRecallReceipt     `json:"craft_recall_receipt,omitempty"`
	CraftRecallReceiptDigest string                  `json:"craft_recall_receipt_digest,omitempty"`
	PlanningContextDigest    string                  `json:"planning_context_digest"`
	RenderContext            json.RawMessage         `json:"render_context"`
	RenderContextSHA256      string                  `json:"render_context_sha256"`
	ObligationsConsumed      []string                `json:"obligations_consumed"`
	ObligationsCreated       []string                `json:"obligations_created"`
	ObligationsCarried       []string                `json:"obligations_carried"`
	ProjectedDelta           ProjectedDelta          `json:"projected_delta"`
	ProjectedPostStateRoot   string                  `json:"projected_post_state_root"`
	BundleDigest             string                  `json:"bundle_digest"`
}

type ObligationKindV2 string

const (
	ObligationRevealV2       ObligationKindV2 = "reveal"
	ObligationForeshadowV2   ObligationKindV2 = "foreshadow"
	ObligationResourceV2     ObligationKindV2 = "resource"
	ObligationRelationshipV2 ObligationKindV2 = "relationship"
	ObligationRuleV2         ObligationKindV2 = "rule"
	ObligationCharacterV2    ObligationKindV2 = "character"
)

type ObligationHardnessV2 string

const (
	ObligationHardV2 ObligationHardnessV2 = "hard"
	ObligationSoftV2 ObligationHardnessV2 = "soft"
)

type ObligationStateV2 string

const (
	ObligationOpenV2       ObligationStateV2 = "open"
	ObligationPlannedV2    ObligationStateV2 = "planned"
	ObligationSatisfiedV2  ObligationStateV2 = "satisfied"
	ObligationSupersededV2 ObligationStateV2 = "superseded"
)

type ObligationOriginV2 struct {
	GenerationID string `json:"generation_id"`
	Chapter      int    `json:"chapter"`
	SourceDigest string `json:"source_digest"`
}

type ObligationDueWindowV2 struct {
	FromChapter        int  `json:"from_chapter"`
	ToChapter          int  `json:"to_chapter"`
	TerminalResolution bool `json:"terminal_resolution"`
}

type ObligationEvidenceV2 struct {
	Chapter      int    `json:"chapter"`
	SourceDigest string `json:"source_digest"`
	Detail       string `json:"detail"`
}

type ObligationV2 struct {
	ID               string                 `json:"id"`
	Kind             ObligationKindV2       `json:"kind"`
	Contract         string                 `json:"contract"`
	Origin           ObligationOriginV2     `json:"origin"`
	DueWindow        ObligationDueWindowV2  `json:"due_window"`
	Hardness         ObligationHardnessV2   `json:"hardness"`
	State            ObligationStateV2      `json:"state"`
	ConsumerChapters []int                  `json:"consumer_chapters"`
	Evidence         []ObligationEvidenceV2 `json:"evidence"`
	Supersedes       []string               `json:"supersedes"`
}

type ObligationRegistryV2 struct {
	Version            string                    `json:"version"`
	GenerationID       string                    `json:"generation_id"`
	ProjectionScope    PlanningProjectionScopeV2 `json:"projection_scope,omitempty"`
	ScopeID            string                    `json:"scope_id,omitempty"`
	BookHorizonChapter int                       `json:"book_horizon_chapter,omitempty"`
	FirstChapter       int                       `json:"first_chapter"`
	LastChapter        int                       `json:"last_chapter"`
	Obligations        []ObligationV2            `json:"obligations"`
	RegistryRoot       string                    `json:"registry_root"`
}

type ProjectedBundleDigestEntryV2 struct {
	Chapter                  int    `json:"chapter"`
	BundleDigest             string `json:"bundle_digest"`
	PreviousBundleDigest     string `json:"previous_bundle_digest"`
	ProjectedPreStateRoot    string `json:"projected_pre_state_root"`
	ProjectedPostStateRoot   string `json:"projected_post_state_root"`
	RAGFactReceiptDigest     string `json:"rag_fact_receipt_digest,omitempty"`
	CraftRecallReceiptDigest string `json:"craft_recall_receipt_digest,omitempty"`
}

type ProjectedChainManifestV2 struct {
	Version                string                         `json:"version"`
	GenerationID           string                         `json:"generation_id"`
	FirstChapter           int                            `json:"first_chapter"`
	LastChapter            int                            `json:"last_chapter"`
	ChapterCount           int                            `json:"chapter_count"`
	Entries                []ProjectedBundleDigestEntryV2 `json:"entries"`
	FactReceiptDigests     []string                       `json:"fact_receipt_digests"`
	CraftReceiptDigests    []string                       `json:"craft_receipt_digests"`
	ChainHeadRoot          string                         `json:"chain_head_root"`
	ChainTailRoot          string                         `json:"chain_tail_root"`
	ObligationRegistryRoot string                         `json:"obligation_registry_root"`
	CreatedAt              string                         `json:"created_at"`
	ManifestDigest         string                         `json:"manifest_digest"`
}

type SealReceiptV2 struct {
	Version                string `json:"version"`
	GenerationID           string `json:"generation_id"`
	GenerationDigest       string `json:"generation_digest"`
	ChainManifestDigest    string `json:"chain_manifest_digest"`
	ChainHeadRoot          string `json:"chain_head_root"`
	ChainTailRoot          string `json:"chain_tail_root"`
	ObligationRegistryRoot string `json:"obligation_registry_root"`
	BaseCanonRoot          string `json:"base_canon_root"`
	BaseStateRoot          string `json:"base_state_root"`
	PlanningDependencyRoot string `json:"planning_dependency_root"`
	SealedAt               string `json:"sealed_at"`
	ReceiptDigest          string `json:"receipt_digest"`
}

type ProjectionCursorV2 struct {
	GenerationID         string `json:"generation_id"`
	NextProjectChapter   int    `json:"next_project_chapter"`
	LastProjectedChapter int    `json:"last_projected_chapter"`
	LastBundleDigest     string `json:"last_bundle_digest"`
	BlockedReason        string `json:"blocked_reason"`
	UpdatedAt            string `json:"updated_at"`
	CursorDigest         string `json:"cursor_digest"`
}

type RealizationCursorV2 struct {
	ActiveGenerationID           string `json:"active_generation_id"`
	NextPromoteChapter           int    `json:"next_promote_chapter"`
	ActivePromotedChapter        int    `json:"active_promoted_chapter"`
	ActivePromotionReceiptDigest string `json:"active_promotion_receipt_digest"`
	LastAcceptedChapter          int    `json:"last_accepted_chapter"`
	LastOutcomeReceiptDigest     string `json:"last_outcome_receipt_digest"`
	BlockedByRewrites            []int  `json:"blocked_by_rewrites"`
	UpdatedAt                    string `json:"updated_at"`
	CursorDigest                 string `json:"cursor_digest"`
}

type ActivePlanningGenerationV2 struct {
	Version              string `json:"version"`
	GenerationID         string `json:"generation_id"`
	SealReceiptDigest    string `json:"seal_receipt_digest"`
	ActivatedAt          string `json:"activated_at"`
	PreviousGenerationID string `json:"previous_generation_id"`
	RecordDigest         string `json:"record_digest"`
}

type PromotionReceiptV2 struct {
	Version               string `json:"version"`
	GenerationID          string `json:"generation_id"`
	Chapter               int    `json:"chapter"`
	BundleDigest          string `json:"bundle_digest"`
	ActualPreStateRoot    string `json:"actual_pre_state_root"`
	ProjectedPreStateRoot string `json:"projected_pre_state_root"`
	RenderDependencyRoot  string `json:"render_dependency_root"`
	FrozenPlanDigest      string `json:"frozen_plan_digest"`
	Mode                  string `json:"mode"`
	PromotedAt            string `json:"promoted_at"`
	ReceiptDigest         string `json:"receipt_digest"`
}

type ActualOutcomeReceiptV2 struct {
	Version                     string         `json:"version"`
	GenerationID                string         `json:"generation_id"`
	Chapter                     int            `json:"chapter"`
	PromotionReceiptDigest      string         `json:"promotion_receipt_digest"`
	ChapterBodySHA256           string         `json:"chapter_body_sha256"`
	CommitCheckpointSeq         int64          `json:"commit_checkpoint_seq"`
	ActualDelta                 ProjectedDelta `json:"actual_delta"`
	ActualPreStateRoot          string         `json:"actual_pre_state_root"`
	ActualPostStateRoot         string         `json:"actual_post_state_root"`
	ActualCanonRoot             string         `json:"actual_canon_root"`
	ProjectedPostStateRoot      string         `json:"projected_post_state_root"`
	ObligationsSatisfied        []string       `json:"obligations_satisfied"`
	ObligationsCreatedUnplanned []string       `json:"obligations_created_unplanned"`
	ProjectionMatch             bool           `json:"projection_match"`
	AcceptedAt                  string         `json:"accepted_at"`
	ReceiptDigest               string         `json:"receipt_digest"`
}

type SuffixInvalidationReceiptV2 struct {
	Version                 string `json:"version"`
	GenerationID            string `json:"generation_id"`
	FromChapter             int    `json:"from_chapter"`
	ThroughChapter          int    `json:"through_chapter"`
	CauseReceiptDigest      string `json:"cause_receipt_digest"`
	Reason                  string `json:"reason"`
	ReplacementGenerationID string `json:"replacement_generation_id"`
	InvalidatedAt           string `json:"invalidated_at"`
	PreviousReceiptDigest   string `json:"previous_receipt_digest"`
	ReceiptDigest           string `json:"receipt_digest"`
}

type GenerationArchiveReceiptV2 struct {
	Version               string `json:"version"`
	GenerationID          string `json:"generation_id"`
	SealReceiptDigest     string `json:"seal_receipt_digest"`
	SuccessorGenerationID string `json:"successor_generation_id"`
	Reason                string `json:"reason"`
	ArchivedAt            string `json:"archived_at"`
	PreviousReceiptDigest string `json:"previous_receipt_digest"`
	ReceiptDigest         string `json:"receipt_digest"`
}

type ProjectedChapterBundleV2 = ProjectedChapterBundle
type ObligationRegistry = ObligationRegistryV2
type Obligation = ObligationV2
type ProjectedChainManifest = ProjectedChainManifestV2
type SealReceipt = SealReceiptV2
type ProjectionCursor = ProjectionCursorV2
type RealizationCursor = RealizationCursorV2
type PromotionReceipt = PromotionReceiptV2
type ActualOutcomeReceipt = ActualOutcomeReceiptV2

// Public digest/validation implementations follow below. They intentionally
// live in the domain package so stores cannot weaken the schema.

func planningV2Digest(value any) (string, error) {
	sum, err := DeterministicPlanningHash(value)
	if err != nil {
		return "", err
	}
	return PlanningV2DigestPrefix + sum, nil
}

func validatePlanningV2Digest(name, value string) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, PlanningV2DigestPrefix) {
		return fmt.Errorf("%s must use sha256:<lowercase-hex> form", name)
	}
	raw := strings.TrimPrefix(value, PlanningV2DigestPrefix)
	if len(raw) != 64 || strings.ToLower(raw) != raw {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex> form", name)
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex> form", name)
	}
	return nil
}

func validatePlanningV2Time(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s must be RFC3339: %w", name, err)
	}
	return nil
}

func normalizeV2Strings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

func normalizeV2Ints(values []int) []int {
	out := append([]int(nil), values...)
	sort.Ints(out)
	result := make([]int, 0, len(out))
	for _, value := range out {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	if result == nil {
		return []int{}
	}
	return result
}

func sameV2Strings(left, right []string) bool {
	left = normalizeV2Strings(left)
	right = normalizeV2Strings(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func DerivePlanningGenerationV2ID(baseCanonRoot, stableOutlineRoot, planningDependencyRoot, randomSeedContract string) (string, error) {
	for name, value := range map[string]string{
		"base_canon_root":          baseCanonRoot,
		"stable_outline_root":      stableOutlineRoot,
		"planning_dependency_root": planningDependencyRoot,
		"random_seed_contract":     randomSeedContract,
	} {
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("%s is required", name)
		}
	}
	seedRoot, err := ComputePlanningSeedContractRootV2(randomSeedContract)
	if err != nil {
		return "", err
	}
	return DerivePlanningGenerationAttemptV2ID(baseCanonRoot, stableOutlineRoot, planningDependencyRoot, seedRoot, "")
}

func ComputePlanningSeedContractRootV2(randomSeedContract string) (string, error) {
	randomSeedContract = strings.Join(strings.Fields(randomSeedContract), " ")
	if randomSeedContract == "" {
		return "", fmt.Errorf("random_seed_contract is required")
	}
	return planningV2Digest(struct {
		Version  string `json:"version"`
		Contract string `json:"contract"`
	}{"planning-random-seed-contract.v2", randomSeedContract})
}

func DerivePlanningGenerationAttemptV2ID(baseCanonRoot, stableOutlineRoot, planningDependencyRoot, seedContractRoot, attemptID string) (string, error) {
	for name, value := range map[string]string{
		"base_canon_root":           baseCanonRoot,
		"stable_outline_root":       stableOutlineRoot,
		"planning_dependency_root":  planningDependencyRoot,
		"random_seed_contract_root": seedContractRoot,
	} {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return "", err
		}
	}
	digest, err := planningV2Digest(struct {
		Version                string `json:"version"`
		BaseCanonRoot          string `json:"base_canon_root"`
		StableOutlineRoot      string `json:"stable_outline_root"`
		PlanningDependencyRoot string `json:"planning_dependency_root"`
		RandomSeedContractRoot string `json:"random_seed_contract_root"`
		AttemptID              string `json:"attempt_id"`
	}{
		Version:                PlanningGenerationV2Version,
		BaseCanonRoot:          strings.TrimSpace(baseCanonRoot),
		StableOutlineRoot:      strings.TrimSpace(stableOutlineRoot),
		PlanningDependencyRoot: strings.TrimSpace(planningDependencyRoot),
		RandomSeedContractRoot: strings.TrimSpace(seedContractRoot),
		AttemptID:              strings.TrimSpace(attemptID),
	})
	if err != nil {
		return "", err
	}
	return PlanningGenerationIDPrefix + strings.TrimPrefix(digest, PlanningV2DigestPrefix)[:24], nil
}

func ComputePlanningGenerationV2Digest(g PlanningGenerationV2) (string, error) {
	g.GenerationDigest = ""
	return planningV2Digest(g)
}

func PlanningGenerationV2Digest(g PlanningGenerationV2) (string, error) {
	return ComputePlanningGenerationV2Digest(g)
}

func IsArcPlanningGenerationV2(g PlanningGenerationV2) bool {
	return g.ProjectionScope == PlanningProjectionScopeArcV2
}

// PlanningGenerationBookHorizonV2 returns the last chapter at which a
// generation may schedule an obligation. Legacy unscoped generations retain
// their historical behavior: the projection range is also the book horizon.
func PlanningGenerationBookHorizonV2(g PlanningGenerationV2) int {
	if IsArcPlanningGenerationV2(g) {
		return g.BookHorizonChapter
	}
	return g.LastProjectedChapter
}

func validatePlanningProjectionScopeV2(
	scope PlanningProjectionScopeV2,
	scopeID string,
	bookHorizonChapter int,
	lastProjectedChapter int,
) error {
	rawScopeID := scopeID
	scopeID = strings.TrimSpace(scopeID)
	if rawScopeID != scopeID {
		return fmt.Errorf("scope_id must not contain leading or trailing whitespace")
	}
	switch scope {
	case "":
		if scopeID != "" || bookHorizonChapter != 0 {
			return fmt.Errorf("legacy unscoped projection cannot declare scope_id or book_horizon_chapter")
		}
		return nil
	case PlanningProjectionScopeArcV2:
		if scopeID == "" || len(scopeID) > 128 || strings.ContainsAny(scopeID, "\r\n\t") {
			return fmt.Errorf("arc projection requires a stable printable scope_id up to 128 bytes")
		}
		if bookHorizonChapter < lastProjectedChapter {
			return fmt.Errorf(
				"arc projection book_horizon_chapter %d is before arc end %d",
				bookHorizonChapter,
				lastProjectedChapter,
			)
		}
		return nil
	default:
		return fmt.Errorf("unsupported projection_scope %q", scope)
	}
}

func ValidatePlanningGenerationV2(g PlanningGenerationV2) error {
	if g.Version != PlanningGenerationV2Version {
		return fmt.Errorf("planning generation v2: unsupported version %q", g.Version)
	}
	if !strings.HasPrefix(strings.TrimSpace(g.GenerationID), PlanningGenerationIDPrefix) {
		return fmt.Errorf("planning generation v2: generation_id must start with %q", PlanningGenerationIDPrefix)
	}
	if g.ParentGenerationID == g.GenerationID {
		return fmt.Errorf("planning generation v2: parent_generation_id cannot equal generation_id")
	}
	if g.ParentGenerationID != "" && !strings.HasPrefix(g.ParentGenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("planning generation v2: parent_generation_id must start with %q", PlanningGenerationIDPrefix)
	}
	if err := validatePlanningProjectionScopeV2(
		g.ProjectionScope,
		g.ScopeID,
		g.BookHorizonChapter,
		g.LastProjectedChapter,
	); err != nil {
		return fmt.Errorf("planning generation v2: %w", err)
	}
	if g.BaseCanonChapter < 0 || g.FirstProjectedChapter != g.BaseCanonChapter+1 {
		return fmt.Errorf("planning generation v2: first_projected_chapter must immediately follow base_canon_chapter")
	}
	if g.LastProjectedChapter < g.FirstProjectedChapter {
		return fmt.Errorf("planning generation v2: invalid projected chapter range")
	}
	wantCount := g.LastProjectedChapter - g.FirstProjectedChapter + 1
	if g.ExpectedChapterCount != wantCount || g.ExpectedChapterCount <= 0 {
		return fmt.Errorf("planning generation v2: expected_chapter_count must equal projected range length %d", wantCount)
	}
	if g.ProjectedChapterCount < 0 || g.ProjectedChapterCount > g.ExpectedChapterCount {
		return fmt.Errorf("planning generation v2: projected_chapter_count is outside expected range")
	}
	for name, value := range map[string]string{
		"base_canon_root":           g.BaseCanonRoot,
		"base_state_root":           g.BaseStateRoot,
		"stable_outline_root":       g.StableOutlineRoot,
		"planning_dependency_root":  g.PlanningDependencyRoot,
		"random_seed_contract_root": g.RandomSeedContractRoot,
		"obligation_registry_root":  g.ObligationRegistryRoot,
	} {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return fmt.Errorf("planning generation v2: %w", err)
		}
	}
	wantGenerationID, err := DerivePlanningGenerationAttemptV2ID(
		g.BaseCanonRoot,
		g.StableOutlineRoot,
		g.PlanningDependencyRoot,
		g.RandomSeedContractRoot,
		g.AttemptID,
	)
	if err != nil {
		return fmt.Errorf("planning generation v2: derive generation_id: %w", err)
	}
	if g.GenerationID != wantGenerationID {
		return fmt.Errorf("planning generation v2: generation_id does not match base/dependency/seed/attempt identity: got %s want %s", g.GenerationID, wantGenerationID)
	}
	if err := validatePlanningV2Time("created_at", g.CreatedAt); err != nil {
		return fmt.Errorf("planning generation v2: %w", err)
	}
	switch g.Status {
	case PlanningGenerationBuildingV2:
		if g.SealedAt != "" {
			return fmt.Errorf("planning generation v2: building generation cannot have sealed_at")
		}
		if g.ProjectedChapterCount == 0 {
			if g.ChainHeadRoot != "" || g.ChainTailRoot != "" {
				return fmt.Errorf("planning generation v2: empty building generation cannot claim chain roots")
			}
		} else {
			if err := validatePlanningV2Digest("chain_head_root", g.ChainHeadRoot); err != nil {
				return fmt.Errorf("planning generation v2: %w", err)
			}
			if err := validatePlanningV2Digest("chain_tail_root", g.ChainTailRoot); err != nil {
				return fmt.Errorf("planning generation v2: %w", err)
			}
		}
	case PlanningGenerationSealedV2:
		if g.ProjectedChapterCount != g.ExpectedChapterCount {
			return fmt.Errorf("planning generation v2: sealed generation must contain every expected formal bundle")
		}
		if err := validatePlanningV2Digest("chain_head_root", g.ChainHeadRoot); err != nil {
			return fmt.Errorf("planning generation v2: %w", err)
		}
		if err := validatePlanningV2Digest("chain_tail_root", g.ChainTailRoot); err != nil {
			return fmt.Errorf("planning generation v2: %w", err)
		}
		if err := validatePlanningV2Time("sealed_at", g.SealedAt); err != nil {
			return fmt.Errorf("planning generation v2: %w", err)
		}
	default:
		return fmt.Errorf("planning generation v2: unsupported status %q", g.Status)
	}
	if err := validatePlanningV2Digest("generation_digest", g.GenerationDigest); err != nil {
		return fmt.Errorf("planning generation v2: %w", err)
	}
	want, err := ComputePlanningGenerationV2Digest(g)
	if err != nil {
		return err
	}
	if g.GenerationDigest != want {
		return fmt.Errorf("planning generation v2: generation_digest mismatch: got %s want %s", g.GenerationDigest, want)
	}
	return nil
}

func ComputePlanningSourceSnapshotV2Digest(snapshot PlanningSourceSnapshotV2) (string, error) {
	snapshot.SnapshotDigest = ""
	return planningV2Digest(snapshot)
}

func ValidatePlanningSourceSnapshotV2(snapshot PlanningSourceSnapshotV2) error {
	if snapshot.Version != PlanningSourceSnapshotV2Version {
		return fmt.Errorf("planning source snapshot v2: unsupported version %q", snapshot.Version)
	}
	if !strings.HasPrefix(snapshot.GenerationID, PlanningGenerationIDPrefix) || snapshot.BaseCanonChapter < 0 {
		return fmt.Errorf("planning source snapshot v2: invalid generation_id or base_canon_chapter")
	}
	for name, value := range map[string]string{
		"base_canon_root":           snapshot.BaseCanonRoot,
		"base_state_root":           snapshot.BaseStateRoot,
		"stable_outline_root":       snapshot.StableOutlineRoot,
		"planning_dependency_root":  snapshot.PlanningDependencyRoot,
		"random_seed_contract_root": snapshot.RandomSeedContractRoot,
		"foundation_snapshot_root":  snapshot.FoundationSnapshotRoot,
		"rag_snapshot_root":         snapshot.RAGSnapshotRoot,
		"snapshot_digest":           snapshot.SnapshotDigest,
	} {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return fmt.Errorf("planning source snapshot v2: %w", err)
		}
	}
	if err := validatePlanningV2Time("captured_at", snapshot.CapturedAt); err != nil {
		return fmt.Errorf("planning source snapshot v2: %w", err)
	}
	want, err := ComputePlanningSourceSnapshotV2Digest(snapshot)
	if err != nil {
		return err
	}
	if snapshot.SnapshotDigest != want {
		return fmt.Errorf("planning source snapshot v2: snapshot_digest mismatch")
	}
	return nil
}

func ValidatePlanningSourceSnapshotAgainstGenerationV2(snapshot PlanningSourceSnapshotV2, generation PlanningGenerationV2) error {
	if err := ValidatePlanningSourceSnapshotV2(snapshot); err != nil {
		return err
	}
	if snapshot.GenerationID != generation.GenerationID ||
		snapshot.BaseCanonChapter != generation.BaseCanonChapter ||
		snapshot.BaseCanonRoot != generation.BaseCanonRoot ||
		snapshot.BaseStateRoot != generation.BaseStateRoot ||
		snapshot.StableOutlineRoot != generation.StableOutlineRoot ||
		snapshot.PlanningDependencyRoot != generation.PlanningDependencyRoot {
		return fmt.Errorf("planning source snapshot v2: identity/roots do not match generation")
	}
	if snapshot.RandomSeedContractRoot != generation.RandomSeedContractRoot {
		return fmt.Errorf("planning source snapshot v2: identity/roots do not match generation")
	}
	return nil
}

func normalizeStateMutationV2(m StateMutationV2) StateMutationV2 {
	m.StableID = strings.TrimSpace(m.StableID)
	m.Subject = strings.TrimSpace(m.Subject)
	m.Object = strings.TrimSpace(m.Object)
	m.Field = strings.TrimSpace(m.Field)
	m.Operation = strings.TrimSpace(m.Operation)
	m.Before = strings.TrimSpace(m.Before)
	m.After = strings.TrimSpace(m.After)
	m.Cause = strings.TrimSpace(m.Cause)
	m.Evidence = strings.TrimSpace(m.Evidence)
	return m
}

func normalizeStateMutationsV2(values []StateMutationV2) []StateMutationV2 {
	out := make([]StateMutationV2, len(values))
	for i := range values {
		out[i] = normalizeStateMutationV2(values[i])
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].StableID + "\x00" + out[i].Subject + "\x00" + out[i].Field + "\x00" + out[i].Operation
		right := out[j].StableID + "\x00" + out[j].Subject + "\x00" + out[j].Field + "\x00" + out[j].Operation
		return left < right
	})
	if out == nil {
		return []StateMutationV2{}
	}
	return out
}

func NormalizeProjectedDeltaV2(delta ProjectedDelta) ProjectedDelta {
	delta.Version = strings.TrimSpace(delta.Version)
	delta.Timeline = normalizeStateMutationsV2(delta.Timeline)
	delta.CharacterState = normalizeStateMutationsV2(delta.CharacterState)
	delta.Relationships = normalizeStateMutationsV2(delta.Relationships)
	delta.Resources = normalizeStateMutationsV2(delta.Resources)
	delta.Knowledge = normalizeStateMutationsV2(delta.Knowledge)
	delta.Locations = normalizeStateMutationsV2(delta.Locations)
	delta.Foreshadows = normalizeStateMutationsV2(delta.Foreshadows)
	delta.Obligations = normalizeStateMutationsV2(delta.Obligations)
	return delta
}

func ValidateProjectedDeltaV2(delta ProjectedDelta) error {
	if delta.Version != ProjectedDeltaV2Version {
		return fmt.Errorf("projected delta v2: unsupported version %q", delta.Version)
	}
	if len(delta.Timeline) == 0 {
		return fmt.Errorf("projected delta v2: timeline must contain a formal time/state transition")
	}
	categories := []struct {
		name   string
		values []StateMutationV2
	}{
		{"timeline", delta.Timeline},
		{"character_state", delta.CharacterState},
		{"relationship", delta.Relationships},
		{"resource", delta.Resources},
		{"knowledge", delta.Knowledge},
		{"location", delta.Locations},
		{"foreshadow", delta.Foreshadows},
		{"obligation", delta.Obligations},
	}
	seen := make(map[string]string)
	for _, category := range categories {
		for i, mutation := range category.values {
			mutation = normalizeStateMutationV2(mutation)
			if mutation.StableID == "" || mutation.Subject == "" || mutation.Field == "" || mutation.Operation == "" || mutation.Cause == "" {
				return fmt.Errorf("projected delta v2: %s[%d] requires stable_id, subject, field, operation, and cause", category.name, i)
			}
			switch mutation.Operation {
			case "create", "set", "update", "advance", "consume", "resolve", "carry", "supersede", "remove":
			default:
				return fmt.Errorf("projected delta v2: %s[%d] has unsupported operation %q", category.name, i, mutation.Operation)
			}
			if mutation.Operation != "remove" && mutation.After == "" {
				return fmt.Errorf("projected delta v2: %s[%d] requires after for operation %q", category.name, i, mutation.Operation)
			}
			if prior, duplicate := seen[mutation.StableID]; duplicate {
				return fmt.Errorf("projected delta v2: duplicate stable_id %q in %s and %s", mutation.StableID, prior, category.name)
			}
			seen[mutation.StableID] = category.name
		}
	}
	return nil
}

func ComputeProjectedDeltaV2Digest(delta ProjectedDelta) (string, error) {
	if err := ValidateProjectedDeltaV2(delta); err != nil {
		return "", err
	}
	return planningV2Digest(NormalizeProjectedDeltaV2(delta))
}

func DeriveStructuredStateRootV2(preStateRoot string, delta ProjectedDelta) (string, error) {
	if err := validatePlanningV2Digest("pre_state_root", preStateRoot); err != nil {
		return "", err
	}
	deltaDigest, err := ComputeProjectedDeltaV2Digest(delta)
	if err != nil {
		return "", err
	}
	return planningV2Digest(struct {
		Version      string `json:"version"`
		PreStateRoot string `json:"pre_state_root"`
		DeltaRoot    string `json:"delta_root"`
	}{
		Version:      "structured-state-transition.v2",
		PreStateRoot: preStateRoot,
		DeltaRoot:    deltaDigest,
	})
}

func DeriveProjectedPostStateRootV2(preStateRoot string, delta ProjectedDelta) (string, error) {
	return DeriveStructuredStateRootV2(preStateRoot, delta)
}

func validateObligationKindV2(kind ObligationKindV2) error {
	switch kind {
	case ObligationRevealV2, ObligationForeshadowV2, ObligationResourceV2,
		ObligationRelationshipV2, ObligationRuleV2, ObligationCharacterV2:
		return nil
	default:
		return fmt.Errorf("unsupported obligation kind %q", kind)
	}
}

func normalizeObligationContractV2(contract string) string {
	return strings.ToLower(strings.Join(strings.Fields(contract), " "))
}

func DeriveObligationIDV2(kind ObligationKindV2, originStableChapter int, contract string) (string, error) {
	if err := validateObligationKindV2(kind); err != nil {
		return "", err
	}
	if originStableChapter <= 0 {
		return "", fmt.Errorf("origin stable chapter must be > 0")
	}
	contract = normalizeObligationContractV2(contract)
	if contract == "" {
		return "", fmt.Errorf("obligation contract is required")
	}
	digest, err := planningV2Digest(struct {
		Kind     ObligationKindV2 `json:"kind"`
		Origin   int              `json:"origin_stable_chapter"`
		Contract string           `json:"normalized_contract"`
	}{kind, originStableChapter, contract})
	if err != nil {
		return "", err
	}
	return "obl:" + string(kind) + ":" + strconv.Itoa(originStableChapter) + ":" +
		strings.TrimPrefix(digest, PlanningV2DigestPrefix)[:12], nil
}

func normalizeObligationV2(obligation ObligationV2) ObligationV2 {
	obligation.ID = strings.TrimSpace(obligation.ID)
	obligation.Contract = strings.TrimSpace(obligation.Contract)
	obligation.Origin.GenerationID = strings.TrimSpace(obligation.Origin.GenerationID)
	obligation.Origin.SourceDigest = strings.TrimSpace(obligation.Origin.SourceDigest)
	obligation.ConsumerChapters = normalizeV2Ints(obligation.ConsumerChapters)
	obligation.Supersedes = normalizeV2Strings(obligation.Supersedes)
	evidence := append([]ObligationEvidenceV2(nil), obligation.Evidence...)
	for i := range evidence {
		evidence[i].SourceDigest = strings.TrimSpace(evidence[i].SourceDigest)
		evidence[i].Detail = strings.TrimSpace(evidence[i].Detail)
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].Chapter != evidence[j].Chapter {
			return evidence[i].Chapter < evidence[j].Chapter
		}
		if evidence[i].SourceDigest != evidence[j].SourceDigest {
			return evidence[i].SourceDigest < evidence[j].SourceDigest
		}
		return evidence[i].Detail < evidence[j].Detail
	})
	if evidence == nil {
		evidence = []ObligationEvidenceV2{}
	}
	obligation.Evidence = evidence
	return obligation
}

func normalizeObligationRegistryV2(registry ObligationRegistryV2) ObligationRegistryV2 {
	registry.GenerationID = strings.TrimSpace(registry.GenerationID)
	registry.ScopeID = strings.TrimSpace(registry.ScopeID)
	out := make([]ObligationV2, len(registry.Obligations))
	for i := range registry.Obligations {
		out[i] = normalizeObligationV2(registry.Obligations[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []ObligationV2{}
	}
	registry.Obligations = out
	return registry
}

func ComputeObligationRegistryV2Root(registry ObligationRegistryV2) (string, error) {
	registry.RegistryRoot = ""
	return planningV2Digest(normalizeObligationRegistryV2(registry))
}

func ValidateObligationRegistryV2(registry ObligationRegistryV2) error {
	if registry.Version != ObligationRegistryV2Version {
		return fmt.Errorf("obligation registry v2: unsupported version %q", registry.Version)
	}
	if !strings.HasPrefix(registry.GenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("obligation registry v2: invalid generation_id")
	}
	if registry.FirstChapter <= 0 || registry.LastChapter < registry.FirstChapter {
		return fmt.Errorf("obligation registry v2: invalid chapter range")
	}
	if err := validatePlanningProjectionScopeV2(
		registry.ProjectionScope,
		registry.ScopeID,
		registry.BookHorizonChapter,
		registry.LastChapter,
	); err != nil {
		return fmt.Errorf("obligation registry v2: %w", err)
	}
	obligationHorizon := registry.LastChapter
	if registry.ProjectionScope == PlanningProjectionScopeArcV2 {
		obligationHorizon = registry.BookHorizonChapter
	}
	known := make(map[string]ObligationV2, len(registry.Obligations))
	for i, raw := range registry.Obligations {
		obligation := normalizeObligationV2(raw)
		if err := validateObligationKindV2(obligation.Kind); err != nil {
			return fmt.Errorf("obligation registry v2: obligations[%d]: %w", i, err)
		}
		originGeneration := obligation.Origin.GenerationID
		if (originGeneration != "canon" && !strings.HasPrefix(originGeneration, PlanningGenerationIDPrefix)) ||
			obligation.Origin.Chapter <= 0 || obligation.Origin.Chapter > registry.LastChapter {
			return fmt.Errorf("obligation registry v2: obligations[%d] has invalid origin", i)
		}
		if err := validatePlanningV2Digest("origin.source_digest", obligation.Origin.SourceDigest); err != nil {
			return fmt.Errorf("obligation registry v2: obligations[%d]: %w", i, err)
		}
		wantID, err := DeriveObligationIDV2(obligation.Kind, obligation.Origin.Chapter, obligation.Contract)
		if err != nil {
			return fmt.Errorf("obligation registry v2: obligations[%d]: %w", i, err)
		}
		if obligation.ID != wantID {
			return fmt.Errorf("obligation registry v2: obligations[%d] id mismatch: got %s want %s", i, obligation.ID, wantID)
		}
		if _, duplicate := known[obligation.ID]; duplicate {
			return fmt.Errorf("obligation registry v2: duplicate obligation %s", obligation.ID)
		}
		if obligation.DueWindow.FromChapter < obligation.Origin.Chapter ||
			obligation.DueWindow.ToChapter < obligation.DueWindow.FromChapter ||
			obligation.DueWindow.ToChapter > obligationHorizon {
			return fmt.Errorf("obligation registry v2: obligation %s has invalid due_window", obligation.ID)
		}
		switch obligation.Hardness {
		case ObligationHardV2, ObligationSoftV2:
		default:
			return fmt.Errorf("obligation registry v2: obligation %s has unsupported hardness %q", obligation.ID, obligation.Hardness)
		}
		switch obligation.State {
		case ObligationOpenV2, ObligationPlannedV2, ObligationSatisfiedV2, ObligationSupersededV2:
		default:
			return fmt.Errorf("obligation registry v2: obligation %s has unsupported state %q", obligation.ID, obligation.State)
		}
		if obligation.Hardness == ObligationHardV2 && len(obligation.ConsumerChapters) == 0 && !obligation.DueWindow.TerminalResolution {
			return fmt.Errorf("obligation registry v2: hard obligation %s has no consumer or terminal resolution", obligation.ID)
		}
		if obligation.State == ObligationPlannedV2 && len(obligation.ConsumerChapters) == 0 && !obligation.DueWindow.TerminalResolution {
			return fmt.Errorf("obligation registry v2: planned obligation %s has no projected consumer or terminal resolution", obligation.ID)
		}
		if obligation.State == ObligationSatisfiedV2 && len(obligation.Evidence) == 0 {
			return fmt.Errorf("obligation registry v2: satisfied obligation %s requires evidence", obligation.ID)
		}
		for _, chapter := range obligation.ConsumerChapters {
			if chapter < obligation.DueWindow.FromChapter || chapter > obligation.DueWindow.ToChapter {
				return fmt.Errorf("obligation registry v2: obligation %s consumer chapter %d is outside due_window", obligation.ID, chapter)
			}
		}
		for j, evidence := range obligation.Evidence {
			if evidence.Chapter <= 0 || evidence.Chapter > registry.LastChapter || strings.TrimSpace(evidence.Detail) == "" {
				return fmt.Errorf("obligation registry v2: obligation %s evidence[%d] is incomplete", obligation.ID, j)
			}
			if err := validatePlanningV2Digest("evidence.source_digest", evidence.SourceDigest); err != nil {
				return fmt.Errorf("obligation registry v2: obligation %s evidence[%d]: %w", obligation.ID, j, err)
			}
		}
		known[obligation.ID] = obligation
	}
	supersededBy := make(map[string]string)
	for _, obligation := range known {
		for _, supersededID := range obligation.Supersedes {
			if supersededID == obligation.ID {
				return fmt.Errorf("obligation registry v2: obligation %s cannot supersede itself", obligation.ID)
			}
			if _, exists := known[supersededID]; !exists {
				return fmt.Errorf("obligation registry v2: obligation %s supersedes unknown id %s", obligation.ID, supersededID)
			}
			if prior, exists := supersededBy[supersededID]; exists {
				return fmt.Errorf("obligation registry v2: obligation %s is superseded by both %s and %s", supersededID, prior, obligation.ID)
			}
			supersededBy[supersededID] = obligation.ID
		}
	}
	for id, obligation := range known {
		_, hasSuccessor := supersededBy[id]
		if obligation.State == ObligationSupersededV2 && !hasSuccessor {
			return fmt.Errorf("obligation registry v2: superseded obligation %s has no successor reference", id)
		}
		if obligation.State != ObligationSupersededV2 && hasSuccessor {
			return fmt.Errorf("obligation registry v2: obligation %s is superseded by registry but state is %q", id, obligation.State)
		}
	}
	if err := validatePlanningV2Digest("registry_root", registry.RegistryRoot); err != nil {
		return fmt.Errorf("obligation registry v2: %w", err)
	}
	want, err := ComputeObligationRegistryV2Root(registry)
	if err != nil {
		return err
	}
	if registry.RegistryRoot != want {
		return fmt.Errorf("obligation registry v2: registry_root mismatch: got %s want %s", registry.RegistryRoot, want)
	}
	return nil
}

func validateObligationRegistryScopeAgainstGenerationV2(
	generation PlanningGenerationV2,
	registry ObligationRegistryV2,
) error {
	if registry.GenerationID != generation.GenerationID ||
		registry.ProjectionScope != generation.ProjectionScope ||
		registry.ScopeID != generation.ScopeID ||
		registry.BookHorizonChapter != generation.BookHorizonChapter ||
		registry.FirstChapter != generation.FirstProjectedChapter ||
		registry.LastChapter != generation.LastProjectedChapter ||
		registry.RegistryRoot != generation.ObligationRegistryRoot {
		return fmt.Errorf("obligation registry identity/scope/root differs from generation")
	}
	return nil
}

// ValidateObligationRegistryAgainstGenerationV2 validates both artifacts and
// then proves that the registry is the exact obligation scope committed by the
// generation. This is useful at store creation boundaries, before any chapter
// bundle exists to trigger full chain validation.
func ValidateObligationRegistryAgainstGenerationV2(
	generation PlanningGenerationV2,
	registry ObligationRegistryV2,
) error {
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		return err
	}
	if err := ValidateObligationRegistryV2(registry); err != nil {
		return err
	}
	return validateObligationRegistryScopeAgainstGenerationV2(generation, registry)
}

// ValidateArcObligationCarryBoundaryV2 proves that every unresolved
// obligation in a sealed arc is genuinely future work. An obligation that
// became due anywhere inside the arc cannot be smuggled into the next arc by
// leaving it open or planned.
func ValidateArcObligationCarryBoundaryV2(
	generation PlanningGenerationV2,
	registry ObligationRegistryV2,
) error {
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		return err
	}
	if !IsArcPlanningGenerationV2(generation) {
		return fmt.Errorf("arc obligation carry boundary requires projection_scope=arc")
	}
	if generation.Status != PlanningGenerationSealedV2 {
		return fmt.Errorf("arc obligation carry boundary requires a sealed generation")
	}
	if err := ValidateObligationRegistryV2(registry); err != nil {
		return err
	}
	if err := validateObligationRegistryScopeAgainstGenerationV2(generation, registry); err != nil {
		return err
	}
	arcEnd := generation.LastProjectedChapter
	for _, obligation := range registry.Obligations {
		switch obligation.State {
		case ObligationOpenV2, ObligationPlannedV2:
			if obligation.DueWindow.FromChapter <= arcEnd {
				return fmt.Errorf(
					"arc %s leaves due obligation %s unresolved at chapter %d",
					generation.ScopeID,
					obligation.ID,
					arcEnd,
				)
			}
			for _, consumer := range obligation.ConsumerChapters {
				if consumer <= arcEnd {
					return fmt.Errorf(
						"arc %s carries obligation %s despite in-arc consumer chapter %d",
						generation.ScopeID,
						obligation.ID,
						consumer,
					)
				}
			}
		}
	}
	return nil
}

// CarryForwardArcObligationsV2 prepares the next building arc generation from
// an immutable predecessor registry. Satisfied and superseded obligations are
// historical facts and are not copied; only future open/planned obligations
// cross the boundary. The returned generation is rebound to the new registry
// root and receives a fresh generation digest.
func CarryForwardArcObligationsV2(
	previousGeneration PlanningGenerationV2,
	previousRegistry ObligationRegistryV2,
	nextGeneration PlanningGenerationV2,
) (PlanningGenerationV2, ObligationRegistryV2, error) {
	empty := ObligationRegistryV2{}
	if err := ValidatePlanningGenerationV2(previousGeneration); err != nil {
		return nextGeneration, empty, fmt.Errorf("previous arc generation: %w", err)
	}
	if previousGeneration.Status != PlanningGenerationSealedV2 ||
		!IsArcPlanningGenerationV2(previousGeneration) {
		return nextGeneration, empty, fmt.Errorf("previous generation must be a sealed arc")
	}
	if err := ValidateArcObligationCarryBoundaryV2(previousGeneration, previousRegistry); err != nil {
		return nextGeneration, empty, fmt.Errorf("previous arc carry boundary: %w", err)
	}
	if err := ValidatePlanningGenerationV2(nextGeneration); err != nil {
		return nextGeneration, empty, fmt.Errorf("next arc generation: %w", err)
	}
	if nextGeneration.Status != PlanningGenerationBuildingV2 ||
		!IsArcPlanningGenerationV2(nextGeneration) ||
		nextGeneration.ProjectedChapterCount != 0 ||
		nextGeneration.ChainHeadRoot != "" || nextGeneration.ChainTailRoot != "" {
		return nextGeneration, empty, fmt.Errorf("next generation must be an empty building arc")
	}
	if nextGeneration.ParentGenerationID != previousGeneration.GenerationID {
		return nextGeneration, empty, fmt.Errorf("next arc parent_generation_id does not name predecessor")
	}
	if nextGeneration.BaseCanonChapter != previousGeneration.LastProjectedChapter ||
		nextGeneration.FirstProjectedChapter != previousGeneration.LastProjectedChapter+1 {
		return nextGeneration, empty, fmt.Errorf("next arc does not immediately follow predecessor chapter range")
	}
	if nextGeneration.BookHorizonChapter != previousGeneration.BookHorizonChapter {
		return nextGeneration, empty, fmt.Errorf("next arc book horizon differs from predecessor")
	}
	if nextGeneration.ScopeID == previousGeneration.ScopeID {
		return nextGeneration, empty, fmt.Errorf("next arc scope_id must differ from predecessor")
	}

	registry := ObligationRegistryV2{
		Version:            ObligationRegistryV2Version,
		GenerationID:       nextGeneration.GenerationID,
		ProjectionScope:    nextGeneration.ProjectionScope,
		ScopeID:            nextGeneration.ScopeID,
		BookHorizonChapter: nextGeneration.BookHorizonChapter,
		FirstChapter:       nextGeneration.FirstProjectedChapter,
		LastChapter:        nextGeneration.LastProjectedChapter,
		Obligations:        []ObligationV2{},
	}
	for _, obligation := range previousRegistry.Obligations {
		switch obligation.State {
		case ObligationOpenV2, ObligationPlannedV2:
			carried := normalizeObligationV2(obligation)
			// Supersession history remains provable in the immutable predecessor
			// registry. Its superseded targets are intentionally not copied into
			// the live next-arc registry, so remove those now-dangling local refs.
			carried.Supersedes = []string{}
			registry.Obligations = append(registry.Obligations, carried)
		}
	}
	var err error
	registry.RegistryRoot, err = ComputeObligationRegistryV2Root(registry)
	if err != nil {
		return nextGeneration, empty, err
	}
	if err := ValidateObligationRegistryV2(registry); err != nil {
		return nextGeneration, empty, fmt.Errorf("carried obligation registry: %w", err)
	}
	nextGeneration.ObligationRegistryRoot = registry.RegistryRoot
	nextGeneration.GenerationDigest = ""
	nextGeneration.GenerationDigest, err = ComputePlanningGenerationV2Digest(nextGeneration)
	if err != nil {
		return nextGeneration, empty, err
	}
	if err := ValidatePlanningGenerationV2(nextGeneration); err != nil {
		return nextGeneration, empty, fmt.Errorf("rebound next arc generation: %w", err)
	}
	if err := validateObligationRegistryScopeAgainstGenerationV2(nextGeneration, registry); err != nil {
		return nextGeneration, empty, err
	}
	return nextGeneration, registry, nil
}

func validateFormalWorldSimulationV2(sim FormalWorldSimulationV2) error {
	if strings.TrimSpace(sim.SimulationID) == "" || strings.TrimSpace(sim.ChosenDecision) == "" ||
		strings.TrimSpace(sim.TimeAdvance) == "" {
		return fmt.Errorf("formal world simulation requires simulation_id, chosen_decision, and time_advance")
	}
	if len(sim.InitialConditions) == 0 || len(sim.Actors) == 0 || len(sim.AvailableChoices) < 2 ||
		len(sim.CausalSteps) == 0 || len(sim.Counterfactuals) == 0 || len(sim.TerminalConditions) == 0 ||
		len(sim.LocationFlow) == 0 {
		return fmt.Errorf("formal world simulation is incomplete; coarse summary cannot be sealed")
	}
	for i, actor := range sim.Actors {
		if strings.TrimSpace(actor.CharacterID) == "" || strings.TrimSpace(actor.Motivation) == "" ||
			strings.TrimSpace(actor.OffscreenState) == "" || len(actor.AvailableActions) == 0 {
			return fmt.Errorf("formal world simulation actor[%d] is incomplete", i)
		}
	}
	for i, step := range sim.CausalSteps {
		if strings.TrimSpace(step.ID) == "" || len(step.CauseIDs) == 0 || strings.TrimSpace(step.ActorID) == "" ||
			strings.TrimSpace(step.Decision) == "" || strings.TrimSpace(step.ImmediateEffect) == "" ||
			strings.TrimSpace(step.DownstreamEffect) == "" {
			return fmt.Errorf("formal world simulation causal_steps[%d] is incomplete", i)
		}
	}
	return nil
}

func validatePOVPlanV2(plan POVPlanV2) error {
	if strings.TrimSpace(plan.POVCharacterID) == "" || strings.TrimSpace(plan.TimeAdvance) == "" ||
		len(plan.KnowledgeBoundary) == 0 || len(plan.Unknowns) == 0 || len(plan.Motivations) == 0 ||
		len(plan.OffscreenStates) == 0 || len(plan.Scenes) == 0 {
		return fmt.Errorf("POV plan is incomplete; knowledge, motivation, offscreen state, scenes, and time advance are required")
	}
	for i, scene := range plan.Scenes {
		if strings.TrimSpace(scene.SceneID) == "" || strings.TrimSpace(scene.Location) == "" ||
			strings.TrimSpace(scene.Time) == "" || len(scene.PresentActors) == 0 ||
			strings.TrimSpace(scene.CausalPurpose) == "" {
			return fmt.Errorf("POV plan scenes[%d] is incomplete", i)
		}
	}
	return nil
}

func validateHardRenderContractV2(contract HardRenderContractV2) error {
	if len(normalizeV2Strings(contract.MustOccur)) == 0 ||
		len(normalizeV2Strings(contract.MustNotOccur)) == 0 ||
		len(normalizeV2Strings(contract.MustPreserve)) == 0 {
		return fmt.Errorf("hard render contract requires must_occur, must_not_occur, and must_preserve")
	}
	for i, reveal := range contract.RevealBudget {
		if strings.TrimSpace(reveal.FactID) == "" || strings.TrimSpace(reveal.Action) == "" || strings.TrimSpace(reveal.Limit) == "" {
			return fmt.Errorf("hard render contract reveal_budget[%d] is incomplete", i)
		}
	}
	return nil
}

func validateHardRenderContractDeltaBindings(
	contract HardRenderContractV2,
	delta ProjectedDelta,
	simulation ChapterWorldSimulation,
) error {
	visibleActors := make(map[string]struct{})
	if protagonist := strings.TrimSpace(simulation.ProtagonistProjection.Protagonist); protagonist != "" {
		visibleActors[protagonist] = struct{}{}
	}
	for _, decision := range simulation.CharacterDecisions {
		if !decision.VisibleToPOV {
			continue
		}
		if character := strings.TrimSpace(decision.Character); character != "" {
			visibleActors[character] = struct{}{}
		}
	}
	isVisible := func(character string) bool {
		_, ok := visibleActors[strings.TrimSpace(character)]
		return ok
	}
	expectedContracts := func(
		mutations []StateMutationV2,
		include func(StateMutationV2) bool,
	) []string {
		expected := make([]string, 0, len(mutations))
		for _, mutation := range mutations {
			if include != nil && !include(mutation) {
				continue
			}
			expected = append(expected, RenderContractForStateMutationV2(mutation))
		}
		return normalizeV2Strings(expected)
	}
	checks := []struct {
		name      string
		contracts []string
		expected  []string
	}{
		{
			"foreshadow_changes",
			contract.ForeshadowChanges,
			expectedContracts(delta.Foreshadows, func(StateMutationV2) bool { return true }),
		},
		{
			"resource_changes",
			contract.ResourceChanges,
			expectedContracts(delta.Resources, func(mutation StateMutationV2) bool {
				return isVisible(mutation.Subject)
			}),
		},
		{
			"relationship_changes",
			contract.RelationshipChanges,
			expectedContracts(delta.Relationships, func(mutation StateMutationV2) bool {
				return isVisible(mutation.Subject) || isVisible(mutation.Object)
			}),
		},
		{
			"knowledge_changes",
			contract.KnowledgeChanges,
			expectedContracts(delta.Knowledge, func(mutation StateMutationV2) bool {
				return isVisible(mutation.Subject)
			}),
		},
	}
	for _, check := range checks {
		actual := normalizeV2Strings(check.contracts)
		if !sameV2Strings(actual, check.expected) {
			return fmt.Errorf(
				"hard render contract %s must exactly equal visible projected changes: got=%q want=%q",
				check.name,
				actual,
				check.expected,
			)
		}
	}
	return nil
}

func normalizeSourceBindingsV2(bindings []SourceBindingV2) []SourceBindingV2 {
	out := append([]SourceBindingV2(nil), bindings...)
	for i := range out {
		out[i].Kind = strings.TrimSpace(out[i].Kind)
		out[i].SourceID = strings.TrimSpace(out[i].SourceID)
		out[i].SourceDigest = strings.TrimSpace(out[i].SourceDigest)
		out[i].ExactReferences = normalizeV2Strings(out[i].ExactReferences)
		out[i].UsableFacts = normalizeV2Strings(out[i].UsableFacts)
		out[i].Transformation = strings.TrimSpace(out[i].Transformation)
		out[i].DoNotUse = normalizeV2Strings(out[i].DoNotUse)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].SourceID < out[j].SourceID
	})
	if out == nil {
		return []SourceBindingV2{}
	}
	return out
}

func normalizeProjectedChapterBundleV2(bundle ProjectedChapterBundle) ProjectedChapterBundle {
	bundle.GenerationID = strings.TrimSpace(bundle.GenerationID)
	bundle.Authority = strings.TrimSpace(bundle.Authority)
	bundle.State = strings.TrimSpace(bundle.State)
	bundle.ProjectionLevel = strings.TrimSpace(bundle.ProjectionLevel)
	bundle.PreviousBundleDigest = strings.TrimSpace(bundle.PreviousBundleDigest)
	bundle.ProjectedPreStateRoot = strings.TrimSpace(bundle.ProjectedPreStateRoot)
	bundle.PlanningContextDigest = strings.TrimSpace(bundle.PlanningContextDigest)
	bundle.RAGFactReceiptDigest = strings.TrimSpace(bundle.RAGFactReceiptDigest)
	bundle.CraftRecallReceiptDigest = strings.TrimSpace(bundle.CraftRecallReceiptDigest)
	bundle.SourceBindings = normalizeSourceBindingsV2(bundle.SourceBindings)
	bundle.ObligationsConsumed = normalizeV2Strings(bundle.ObligationsConsumed)
	bundle.ObligationsCreated = normalizeV2Strings(bundle.ObligationsCreated)
	bundle.ObligationsCarried = normalizeV2Strings(bundle.ObligationsCarried)
	bundle.ProjectedDelta = NormalizeProjectedDeltaV2(bundle.ProjectedDelta)
	bundle.ProjectedPostStateRoot = strings.TrimSpace(bundle.ProjectedPostStateRoot)
	if canonical, err := CanonicalPlanningV2JSON(bundle.RenderContext); err == nil {
		bundle.RenderContext = canonical
	}
	return bundle
}

func CanonicalPlanningV2JSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("JSON payload is required")
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("JSON payload must be an object")
	}
	return json.Marshal(value)
}

func ComputePlanningV2JSONDigest(raw json.RawMessage) (string, error) {
	canonical, err := CanonicalPlanningV2JSON(raw)
	if err != nil {
		return "", err
	}
	return planningV2Digest(json.RawMessage(canonical))
}

// BindProjectedRenderContextV2 adds only immutable identity and the
// prose-safe hard contract to a draft-profile context. Full simulation,
// offscreen state, projected deltas and the obligation registry deliberately
// remain outside the prose session.
func BindProjectedRenderContextV2(
	raw json.RawMessage,
	bundle ProjectedChapterBundle,
) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, fmt.Errorf("draft render context is not an object")
	}
	// Planning may retain compact receipts under planning-stage key names.
	// Remove the entire server-only namespace at the sealed prose boundary so a
	// nested context-builder regression cannot expose hidden simulation state.
	planningV2DeleteJSONKeys(payload, planningV2RenderContextProhibitedKeys())
	planDigest, err := ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return nil, err
	}
	simulationDigest, err := DeterministicPlanningHash(bundle.ChapterWorldSimulation)
	if err != nil {
		return nil, err
	}
	payload["sealed_projection_contract"] = map[string]any{
		"version":                 SealedProjectionRenderContractV2Version,
		"generation_id":           bundle.GenerationID,
		"chapter":                 bundle.Chapter,
		"planning_context_digest": bundle.PlanningContextDigest,
		"chapter_plan_digest":     planDigest,
		"world_simulation_digest": simulationDigest,
		"projected_pre_state":     bundle.ProjectedPreStateRoot,
		"projected_post_state":    bundle.ProjectedPostStateRoot,
		"hard_render_contract":    bundle.HardRenderContract,
		"policy": "正文必须自然实现 hard_render_contract。完整全角色推演、场外状态、projected_delta 和 obligation registry 不进入正文会话；" +
			"计划决定事实边界，不决定句式、段落或表面措辞。",
	}
	return json.Marshal(payload)
}

func ComputeProjectedChapterBundleDigest(bundle ProjectedChapterBundle) (string, error) {
	bundle.BundleDigest = ""
	return planningV2Digest(normalizeProjectedChapterBundleV2(bundle))
}

func ProjectedChapterBundleDigestV2(bundle ProjectedChapterBundle) (string, error) {
	return ComputeProjectedChapterBundleDigest(bundle)
}

func RAGFactReceiptDigestV2(receipt RAGFactReceipt) (string, error) {
	if err := ValidateRAGFactReceipt(receipt); err != nil {
		return "", err
	}
	return PlanningV2DigestPrefix + receipt.PayloadSHA256, nil
}

func ComputeCraftRecallReceiptPayloadSHA256(receipt CraftRecallReceipt) string {
	receipt.PayloadSHA256 = ""
	raw, _ := json.Marshal(receipt)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func CraftRecallReceiptDigestV2(receipt CraftRecallReceipt) (string, error) {
	if err := ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		return "", err
	}
	return PlanningV2DigestPrefix + receipt.PayloadSHA256, nil
}

func CraftRecallReceiptSourceTokenV2(receipt CraftRecallReceipt) string {
	return CraftRecallReceiptTokenPrefix + strings.TrimSpace(receipt.ID)
}

// ValidateProjectAllCraftRecallReceipt validates the immutable planning-time
// method receipt without consulting a mutable index. The creator separately
// verifies IndexIdentity against the isolated workspace before persisting it;
// sealed bundles and render only verify these content-addressed bytes.
func ValidateProjectAllCraftRecallReceipt(receipt CraftRecallReceipt) error {
	if receipt.Version != 1 || receipt.Stage != ProjectAllCraftReceiptStage ||
		!receipt.Enforcement || receipt.Chapter <= 0 {
		return fmt.Errorf("project-all craft receipt: invalid version/stage/enforcement/chapter")
	}
	if len(receipt.ID) != 24 {
		return fmt.Errorf("project-all craft receipt: id must be 24 lowercase hex characters")
	}
	for _, r := range receipt.ID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("project-all craft receipt: id must be 24 lowercase hex characters")
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(receipt.GenerationID), PlanningGenerationIDPrefix) {
		return fmt.Errorf("project-all craft receipt: generation_id is invalid")
	}
	if err := validatePlanningV2Digest("planning_context_digest", receipt.PlanningContextDigest); err != nil {
		return fmt.Errorf("project-all craft receipt: %w", err)
	}
	if strings.TrimSpace(receipt.IndexIdentity) == "" {
		return fmt.Errorf("project-all craft receipt: index_identity is required")
	}
	if receipt.RewriteBodyPath != "" || receipt.RewriteBodySHA256 != "" ||
		receipt.RewriteBriefPath != "" || receipt.RewriteBriefSHA256 != "" {
		return fmt.Errorf("project-all craft receipt: rewrite bindings are forbidden")
	}
	if err := validatePlanningV2Time("created_at", receipt.CreatedAt); err != nil {
		return fmt.Errorf("project-all craft receipt: %w", err)
	}
	if len(receipt.Attempts) < 2 {
		return fmt.Errorf("project-all craft receipt: methodology and dialogue/scene attempts are required")
	}
	seenNeeds := make(map[string]struct{}, len(receipt.Attempts))
	hasMethodology := false
	hasDialogueOrScene := false
	for i, attempt := range receipt.Attempts {
		need := attempt.Need
		if strings.TrimSpace(need.ID) == "" || strings.TrimSpace(need.Field) == "" ||
			strings.TrimSpace(need.Topic) == "" {
			return fmt.Errorf("project-all craft receipt: attempts[%d].need is incomplete", i)
		}
		if _, exists := seenNeeds[need.ID]; exists {
			return fmt.Errorf("project-all craft receipt: duplicate need %q", need.ID)
		}
		seenNeeds[need.ID] = struct{}{}
		switch strings.TrimSpace(need.Field) {
		case "methodology":
			hasMethodology = true
		case "dialogue", "scene_situation":
			hasDialogueOrScene = true
		}
		if attempt.NoMaterial != (len(attempt.Hits) == 0) {
			return fmt.Errorf("project-all craft receipt: attempts[%d] no_material/hits mismatch", i)
		}
		for j, hit := range attempt.Hits {
			if !strings.HasPrefix(strings.TrimSpace(hit.Ref), CraftRecallReceiptTokenPrefix+receipt.ID+"#") ||
				strings.TrimSpace(hit.ChunkID) == "" || strings.TrimSpace(hit.ChunkHash) == "" ||
				strings.TrimSpace(hit.SourcePath) == "" || strings.TrimSpace(hit.SourceKind) == "" {
				return fmt.Errorf("project-all craft receipt: attempts[%d].hits[%d] is incomplete", i, j)
			}
		}
	}
	if !hasMethodology || !hasDialogueOrScene {
		return fmt.Errorf("project-all craft receipt: methodology and dialogue/scene coverage is required")
	}
	if receipt.PayloadSHA256 == "" ||
		receipt.PayloadSHA256 != ComputeCraftRecallReceiptPayloadSHA256(receipt) {
		return fmt.Errorf("project-all craft receipt: payload_sha256 mismatch")
	}
	return nil
}

// ValidateArcChapterTransitionContract validates the explicit intra-arc edge
// authored in one formal chapter plan. predecessor is nil only for the first
// chapter of an arc-scoped generation. Semantic prose similarity is never used
// as authority: identity and text must match byte-for-byte after rejecting
// surrounding whitespace, and consumed_by_cause must be an exact causal beat.
func ValidateArcChapterTransitionContract(
	plan ChapterPlan,
	predecessor *ProjectedPlanningPredecessorContractV2,
) error {
	contract := plan.CausalSimulation.ArcTransition
	if err := validateArcChapterTransitionContractShapeV2(plan.Chapter, contract, true); err != nil {
		return err
	}

	if predecessor == nil {
		if contract.IncomingConsequenceID != "" ||
			contract.IncomingConsequenceText != "" ||
			contract.ConsumedByCause != "" {
			return fmt.Errorf("arc transition contract: first arc chapter %d cannot claim an incoming predecessor", plan.Chapter)
		}
		return nil
	}
	if predecessor.Chapter != plan.Chapter-1 {
		return fmt.Errorf(
			"arc transition contract: chapter %d predecessor chapter=%d is not adjacent",
			plan.Chapter,
			predecessor.Chapter,
		)
	}
	if contract.IncomingConsequenceID != predecessor.OutgoingConsequenceID ||
		contract.IncomingConsequenceText != predecessor.OutgoingConsequenceText {
		return fmt.Errorf(
			"arc transition contract: chapter %d incoming consequence does not exactly match chapter %d outgoing consequence",
			plan.Chapter,
			predecessor.Chapter,
		)
	}
	foundCause := false
	for _, beat := range plan.CausalSimulation.CausalBeats {
		if beat.Cause == contract.ConsumedByCause {
			foundCause = true
			break
		}
	}
	if !foundCause {
		return fmt.Errorf(
			"arc transition contract: chapter %d consumed_by_cause must exactly equal one causal_beats[].cause",
			plan.Chapter,
		)
	}
	if contract.OutgoingConsequenceID == predecessor.OutgoingConsequenceID {
		return fmt.Errorf("arc transition contract: chapter %d must publish a new outgoing consequence id", plan.Chapter)
	}
	return nil
}

func validateArcChapterTransitionContractShapeV2(
	chapter int,
	contract ArcChapterTransitionContract,
	required bool,
) error {
	values := map[string]string{
		"incoming_consequence_id":   contract.IncomingConsequenceID,
		"incoming_consequence_text": contract.IncomingConsequenceText,
		"consumed_by_cause":         contract.ConsumedByCause,
		"outgoing_consequence_id":   contract.OutgoingConsequenceID,
		"outgoing_consequence_text": contract.OutgoingConsequenceText,
	}
	for name, value := range values {
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("arc transition contract: chapter %d %s must be trimmed", chapter, name)
		}
	}
	any := contract.IncomingConsequenceID != "" ||
		contract.IncomingConsequenceText != "" ||
		contract.ConsumedByCause != "" ||
		contract.OutgoingConsequenceID != "" ||
		contract.OutgoingConsequenceText != ""
	if !any && !required {
		return nil
	}
	if contract.OutgoingConsequenceID == "" || contract.OutgoingConsequenceText == "" {
		return fmt.Errorf("arc transition contract: chapter %d requires outgoing consequence id/text", chapter)
	}
	incomingFields := 0
	for _, value := range []string{
		contract.IncomingConsequenceID,
		contract.IncomingConsequenceText,
		contract.ConsumedByCause,
	} {
		if value != "" {
			incomingFields++
		}
	}
	if incomingFields != 0 && incomingFields != 3 {
		return fmt.Errorf("arc transition contract: chapter %d incoming id/text/consumed_by_cause must be all present or all absent", chapter)
	}
	return nil
}

func projectedPlanningPredecessorContractV2(bundle ProjectedChapterBundle) ProjectedPlanningPredecessorContractV2 {
	contract := bundle.ChapterPlan.CausalSimulation.ArcTransition
	return ProjectedPlanningPredecessorContractV2{
		Chapter:                 bundle.Chapter,
		OutgoingConsequenceID:   contract.OutgoingConsequenceID,
		OutgoingConsequenceText: contract.OutgoingConsequenceText,
		BundleDigest:            bundle.BundleDigest,
		ProjectedPostStateRoot:  bundle.ProjectedPostStateRoot,
	}
}

func ValidateProjectedChapterBundle(bundle ProjectedChapterBundle) error {
	if bundle.Version != ProjectedChapterBundleV2Version {
		return fmt.Errorf("projected chapter bundle v2: unsupported version %q", bundle.Version)
	}
	if !strings.HasPrefix(bundle.GenerationID, PlanningGenerationIDPrefix) || bundle.Chapter <= 0 {
		return fmt.Errorf("projected chapter bundle v2: invalid generation_id or chapter")
	}
	if bundle.Authority != ProjectedAuthorityV2 || bundle.State != ProjectedStateV2 {
		return fmt.Errorf("projected chapter bundle v2: authority/state must remain projected_non_canon")
	}
	if bundle.ProjectionLevel != FormalProjectionLevelV2 {
		return fmt.Errorf("projected chapter bundle v2: projection_level must be formal; detailed/coarse projections cannot be sealed")
	}
	for name, value := range map[string]string{
		"previous_bundle_digest":    bundle.PreviousBundleDigest,
		"projected_pre_state_root":  bundle.ProjectedPreStateRoot,
		"projected_post_state_root": bundle.ProjectedPostStateRoot,
		"bundle_digest":             bundle.BundleDigest,
		"planning_context_digest":   bundle.PlanningContextDigest,
		"render_context_sha256":     bundle.RenderContextSHA256,
		"rag_fact_receipt_digest":   bundle.RAGFactReceiptDigest,
		"craft_receipt_digest":      bundle.CraftRecallReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return fmt.Errorf("projected chapter bundle v2: %w", err)
		}
	}
	contextToken, err := ProjectedPlanningContextSourceTokenV2(bundle.PlanningContextDigest)
	if err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if !planningV2ContainsExactString(bundle.ChapterWorldSimulation.Sources, contextToken) ||
		!planningV2ContainsExactString(
			bundle.ChapterPlan.CausalSimulation.ContextSources,
			contextToken,
		) {
		return fmt.Errorf(
			"projected chapter bundle v2: simulation and plan must attest exact planning context token",
		)
	}
	canonicalRenderContext, err := CanonicalPlanningV2JSON(bundle.RenderContext)
	if err != nil {
		return fmt.Errorf("projected chapter bundle v2: render_context: %w", err)
	}
	wantRenderContextDigest, err := ComputePlanningV2JSONDigest(canonicalRenderContext)
	if err != nil {
		return fmt.Errorf("projected chapter bundle v2: render_context: %w", err)
	}
	if bundle.RenderContextSHA256 != wantRenderContextDigest {
		return fmt.Errorf("projected chapter bundle v2: render_context_sha256 mismatch")
	}
	var renderPayload any
	if err := json.Unmarshal(canonicalRenderContext, &renderPayload); err != nil {
		return fmt.Errorf("projected chapter bundle v2: decode render_context payload: %w", err)
	}
	prohibitedKeys := planningV2RenderContextProhibitedKeys()
	if path, key, found := planningV2FindJSONKey(
		renderPayload,
		prohibitedKeys,
		"$",
	); found {
		return fmt.Errorf(
			"projected chapter bundle v2: render_context exposes server-only key %q at %s",
			key,
			path,
		)
	}
	var renderContext struct {
		ContextProfile string `json:"_context_profile"`
		SealedContract struct {
			Version                string               `json:"version"`
			GenerationID           string               `json:"generation_id"`
			Chapter                int                  `json:"chapter"`
			PlanningContextDigest  string               `json:"planning_context_digest"`
			ChapterPlanDigest      string               `json:"chapter_plan_digest"`
			WorldSimulationDigest  string               `json:"world_simulation_digest"`
			ProjectedPreStateRoot  string               `json:"projected_pre_state"`
			ProjectedPostStateRoot string               `json:"projected_post_state"`
			HardRenderContract     HardRenderContractV2 `json:"hard_render_contract"`
		} `json:"sealed_projection_contract"`
	}
	if err := json.Unmarshal(canonicalRenderContext, &renderContext); err != nil {
		return fmt.Errorf("projected chapter bundle v2: decode render_context identity: %w", err)
	}
	sealed := renderContext.SealedContract
	if renderContext.ContextProfile != "draft" ||
		sealed.Version != SealedProjectionRenderContractV2Version ||
		sealed.GenerationID != bundle.GenerationID ||
		sealed.Chapter != bundle.Chapter ||
		sealed.PlanningContextDigest != bundle.PlanningContextDigest ||
		sealed.ProjectedPreStateRoot != bundle.ProjectedPreStateRoot ||
		sealed.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
		return fmt.Errorf("projected chapter bundle v2: render_context sealed projection identity mismatch")
	}
	bundlePlanDigest, err := ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return err
	}
	bundleSimulationDigest, err := DeterministicPlanningHash(bundle.ChapterWorldSimulation)
	if err != nil {
		return err
	}
	if sealed.ChapterPlanDigest != bundlePlanDigest ||
		sealed.WorldSimulationDigest != bundleSimulationDigest {
		return fmt.Errorf("projected chapter bundle v2: render_context sealed plan/simulation digest mismatch")
	}
	contextContractDigest, err := planningV2Digest(sealed.HardRenderContract)
	if err != nil {
		return err
	}
	bundleContractDigest, err := planningV2Digest(bundle.HardRenderContract)
	if err != nil {
		return err
	}
	if contextContractDigest != bundleContractDigest {
		return fmt.Errorf("projected chapter bundle v2: render_context hard_render_contract mismatch")
	}
	sim := bundle.ChapterWorldSimulation
	plan := bundle.ChapterPlan
	// Old unscoped v2 bundles remain readable when the field is absent. Once a
	// transition contract is present, however, partial or whitespace-normalized
	// identities are never accepted. Arc-scoped chain validation below makes
	// the complete contract mandatory for every chapter.
	if err := validateArcChapterTransitionContractShapeV2(
		bundle.Chapter,
		plan.CausalSimulation.ArcTransition,
		false,
	); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if sim.Chapter != bundle.Chapter || plan.Chapter != bundle.Chapter ||
		strings.TrimSpace(sim.SimulationID) == "" || sim.Version <= 0 ||
		strings.TrimSpace(sim.TimeWindow) == "" || len(sim.CharacterDecisions) == 0 {
		return fmt.Errorf("projected chapter bundle v2: full chapter simulation/plan identity is incomplete")
	}
	projection := sim.ProtagonistProjection
	if strings.TrimSpace(projection.Protagonist) == "" ||
		len(projection.AvailableOptions) < 2 ||
		strings.TrimSpace(projection.ChosenDecision) == "" ||
		strings.TrimSpace(projection.DecisionReason) == "" ||
		len(projection.PlanConstraints) == 0 ||
		len(projection.CausalChain) == 0 {
		return fmt.Errorf("projected chapter bundle v2: full chapter simulation protagonist projection is incomplete")
	}
	if sim.GenerationID != "" && sim.GenerationID != bundle.GenerationID {
		return fmt.Errorf("projected chapter bundle v2: chapter simulation generation_id mismatch")
	}
	if plan.CausalSimulation.WorldSimulationID != sim.SimulationID {
		return fmt.Errorf("projected chapter bundle v2: chapter plan world_simulation_id does not match simulation")
	}
	if strings.TrimSpace(plan.CausalSimulation.ProtagonistDecision) != strings.TrimSpace(projection.ChosenDecision) {
		return fmt.Errorf("projected chapter bundle v2: chapter plan protagonist decision does not match simulation projection")
	}
	if strings.TrimSpace(plan.Title) == "" || strings.TrimSpace(plan.Goal) == "" ||
		strings.TrimSpace(plan.Conflict) == "" || strings.TrimSpace(plan.Hook) == "" ||
		len(plan.Contract.RequiredBeats) == 0 || len(plan.Contract.ForbiddenMoves) == 0 ||
		len(plan.Contract.ContinuityChecks) == 0 || len(plan.CausalSimulation.CausalBeats) == 0 ||
		len(plan.CausalSimulation.DecisionPoints) == 0 || len(plan.CausalSimulation.OutcomeShift) == 0 {
		return fmt.Errorf("projected chapter bundle v2: full chapter plan causal/render contract is incomplete")
	}
	if err := validateFormalWorldSimulationV2(bundle.FormalWorldSimulation); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if bundle.FormalWorldSimulation.SimulationID != sim.SimulationID {
		return fmt.Errorf("projected chapter bundle v2: normalized formal simulation id does not match full simulation")
	}
	if err := validatePOVPlanV2(bundle.POVPlan); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if err := validateHardRenderContractV2(bundle.HardRenderContract); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if len(bundle.SourceBindings) == 0 {
		return fmt.Errorf("projected chapter bundle v2: at least one exact source binding is required")
	}
	for i, binding := range bundle.SourceBindings {
		if strings.TrimSpace(binding.Kind) == "" || strings.TrimSpace(binding.SourceID) == "" ||
			len(binding.ExactReferences) == 0 || len(binding.UsableFacts) == 0 ||
			strings.TrimSpace(binding.Transformation) == "" || len(binding.DoNotUse) == 0 {
			return fmt.Errorf("projected chapter bundle v2: source_bindings[%d] is incomplete", i)
		}
		if err := validatePlanningV2Digest("source_binding.source_digest", binding.SourceDigest); err != nil {
			return fmt.Errorf("projected chapter bundle v2: source_bindings[%d]: %w", i, err)
		}
	}
	if bundle.RAGFactReceipt == nil {
		return fmt.Errorf("projected chapter bundle v2: every chapter requires an explicit RAG fact receipt, including no_material")
	}
	if bundle.RAGFactReceipt != nil {
		if err := ValidateRAGFactReceipt(*bundle.RAGFactReceipt); err != nil {
			return fmt.Errorf("projected chapter bundle v2: RAG fact receipt: %w", err)
		}
		if bundle.RAGFactReceipt.Chapter != bundle.Chapter {
			return fmt.Errorf("projected chapter bundle v2: RAG fact receipt chapter mismatch")
		}
		token := bundle.RAGFactReceipt.SourceToken()
		ragDigest, err := RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
		if err != nil {
			return fmt.Errorf("projected chapter bundle v2: RAG fact receipt digest: %w", err)
		}
		found := false
		for _, binding := range bundle.SourceBindings {
			for _, ref := range binding.ExactReferences {
				if binding.SourceDigest == ragDigest &&
					(ref == token || strings.HasPrefix(ref, RAGFactReceiptTokenPrefix+bundle.RAGFactReceipt.ID+"#")) {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("projected chapter bundle v2: RAG fact receipt is not materially bound by source_bindings")
		}
		if bundle.RAGFactReceiptDigest != ragDigest {
			return fmt.Errorf("projected chapter bundle v2: RAG fact receipt digest mismatch")
		}
		if !planningV2ValueContainsRAGRef(bundle.ChapterPlan, bundle.RAGFactReceipt.ID) {
			return fmt.Errorf("projected chapter bundle v2: full chapter plan does not bind the exact RAG fact receipt")
		}
	}
	if bundle.CraftRecallReceipt == nil {
		return fmt.Errorf("projected chapter bundle v2: every chapter requires an explicit project-all craft receipt, including no_material")
	}
	if err := ValidateProjectAllCraftRecallReceipt(*bundle.CraftRecallReceipt); err != nil {
		return fmt.Errorf("projected chapter bundle v2: craft receipt: %w", err)
	}
	if bundle.CraftRecallReceipt.Chapter != bundle.Chapter ||
		bundle.CraftRecallReceipt.GenerationID != bundle.GenerationID ||
		bundle.CraftRecallReceipt.PlanningContextDigest != bundle.PlanningContextDigest {
		return fmt.Errorf("projected chapter bundle v2: craft receipt chapter/generation/planning context mismatch")
	}
	craftDigest, err := CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
	if err != nil {
		return fmt.Errorf("projected chapter bundle v2: craft receipt digest: %w", err)
	}
	if bundle.CraftRecallReceiptDigest != craftDigest {
		return fmt.Errorf("projected chapter bundle v2: craft receipt digest mismatch")
	}
	craftToken := CraftRecallReceiptSourceTokenV2(*bundle.CraftRecallReceipt)
	craftBindingFound := false
	for _, binding := range bundle.SourceBindings {
		for _, ref := range binding.ExactReferences {
			if binding.Kind == "craft_recall_receipt" &&
				binding.SourceDigest == craftDigest && ref == craftToken {
				craftBindingFound = true
				break
			}
		}
	}
	if !craftBindingFound {
		return fmt.Errorf("projected chapter bundle v2: craft receipt is not materially bound by source_bindings")
	}
	if !planningV2ValueContainsCraftRef(bundle.ChapterPlan, bundle.CraftRecallReceipt.ID) {
		return fmt.Errorf("projected chapter bundle v2: full chapter plan does not bind the exact craft receipt")
	}
	if err := ValidateProjectAllCraftPlanConsumptionV2(bundle.ChapterPlan, *bundle.CraftRecallReceipt); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if err := ValidateProjectAllCraftRenderConsumptionV2(bundle.RenderContext, *bundle.CraftRecallReceipt); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if err := ValidateProjectedDeltaV2(bundle.ProjectedDelta); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if err := validateHardRenderContractDeltaBindings(
		bundle.HardRenderContract,
		bundle.ProjectedDelta,
		bundle.ChapterWorldSimulation,
	); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	wantPost, err := DeriveProjectedPostStateRootV2(bundle.ProjectedPreStateRoot, bundle.ProjectedDelta)
	if err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	if bundle.ProjectedPostStateRoot != wantPost {
		return fmt.Errorf("projected chapter bundle v2: projected_post_state_root mismatch")
	}
	for _, values := range [][]string{bundle.ObligationsConsumed, bundle.ObligationsCreated, bundle.ObligationsCarried} {
		for _, id := range values {
			if !strings.HasPrefix(id, "obl:") {
				return fmt.Errorf("projected chapter bundle v2: invalid obligation id %q", id)
			}
		}
	}
	if err := validateDisjointV2Sets(
		map[string][]string{
			"consumed": bundle.ObligationsConsumed,
			"created":  bundle.ObligationsCreated,
			"carried":  bundle.ObligationsCarried,
		},
	); err != nil {
		return fmt.Errorf("projected chapter bundle v2: %w", err)
	}
	obligationOps := make(map[string]map[string]struct{})
	for _, mutation := range bundle.ProjectedDelta.Obligations {
		id := strings.TrimSpace(mutation.Subject)
		if _, exists := obligationOps[id]; !exists {
			obligationOps[id] = make(map[string]struct{})
		}
		obligationOps[id][strings.TrimSpace(mutation.Operation)] = struct{}{}
	}
	for _, requirement := range []struct {
		name       string
		ids        []string
		operations []string
	}{
		{"created", bundle.ObligationsCreated, []string{"create"}},
		{"consumed", bundle.ObligationsConsumed, []string{"consume", "resolve"}},
		{"carried", bundle.ObligationsCarried, []string{"carry"}},
	} {
		for _, id := range requirement.ids {
			matched := false
			for _, operation := range requirement.operations {
				if _, exists := obligationOps[id][operation]; exists {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("projected chapter bundle v2: obligation %s is listed as %s but projected_delta has no matching structured operation", id, requirement.name)
			}
		}
	}
	wantDigest, err := ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		return err
	}
	if bundle.BundleDigest != wantDigest {
		return fmt.Errorf("projected chapter bundle v2: bundle_digest mismatch: got %s want %s", bundle.BundleDigest, wantDigest)
	}
	return nil
}

func planningV2FindJSONKey(
	value any,
	prohibited map[string]struct{},
	path string,
) (string, string, bool) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			childPath := path + "." + key
			if _, blocked := prohibited[key]; blocked {
				return childPath, key, true
			}
			if foundPath, foundKey, found := planningV2FindJSONKey(
				current[key],
				prohibited,
				childPath,
			); found {
				return foundPath, foundKey, true
			}
		}
	case []any:
		for i, item := range current {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if foundPath, foundKey, found := planningV2FindJSONKey(
				item,
				prohibited,
				childPath,
			); found {
				return foundPath, foundKey, true
			}
		}
	}
	return "", "", false
}

func planningV2RenderContextProhibitedKeys() map[string]struct{} {
	keys := []string{
		"formal_world_simulation",
		"chapter_world_simulation",
		"character_decisions",
		"locked_character_decisions",
		"hidden_pressures",
		"offscreen_states",
		"offscreen_stage",
		"offscreen_state",
		"projection_seed",
		"simulation_character_authority",
		"project_all_state",
		"pov_plan",
		"projected_delta",
		"obligation_registry",
		"obligations_consumed",
		"obligations_created",
		"obligations_carried",
		"source_bindings",
		"rag_fact_receipt",
		"craft_recall_receipt",
		"rewrite_craft_pack",
		"retrieval_trace",
		"rag_recall",
	}
	prohibited := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		prohibited[key] = struct{}{}
	}
	return prohibited
}

func planningV2DeleteJSONKeys(value any, prohibited map[string]struct{}) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if _, blocked := prohibited[key]; blocked {
				delete(current, key)
				continue
			}
			planningV2DeleteJSONKeys(child, prohibited)
		}
	case []any:
		for _, child := range current {
			planningV2DeleteJSONKeys(child, prohibited)
		}
	}
}

func planningV2ContainsExactString(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func planningV2ValueContainsRAGRef(value any, receiptID string) bool {
	return planningV2ValueContainsReceiptRef(value, RAGFactReceiptTokenPrefix+strings.TrimSpace(receiptID))
}

func planningV2ValueContainsCraftRef(value any, receiptID string) bool {
	return planningV2ValueContainsReceiptRef(value, CraftRecallReceiptTokenPrefix+strings.TrimSpace(receiptID))
}

func planningV2ValueContainsReceiptRef(value any, prefix string) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case string:
			return strings.HasPrefix(strings.TrimSpace(typed), prefix)
		case []any:
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		case map[string]any:
			for _, item := range typed {
				if visit(item) {
					return true
				}
			}
		}
		return false
	}
	return visit(decoded)
}

func ValidateProjectAllCraftPlanConsumptionV2(plan ChapterPlan, receipt CraftRecallReceipt) error {
	if !planningV2ContainsExactString(plan.CausalSimulation.ContextSources, CraftRecallReceiptSourceTokenV2(receipt)) {
		return fmt.Errorf("project-all craft receipt source token is absent from chapter plan")
	}
	hitsByNeed := make(map[string]map[string]struct{})
	for _, attempt := range receipt.Attempts {
		if len(attempt.Hits) == 0 {
			continue
		}
		allowed := make(map[string]struct{}, len(attempt.Hits))
		for _, hit := range attempt.Hits {
			allowed[strings.TrimSpace(hit.Ref)] = struct{}{}
		}
		hitsByNeed[attempt.Need.ID] = allowed
	}
	if len(hitsByNeed) == 0 {
		return nil
	}
	consumed := make(map[string]bool, len(hitsByNeed))
	for _, ref := range plan.CausalSimulation.ExternalRefs {
		needID := strings.TrimSpace(ref.QueryOrNeed)
		allowed, required := hitsByNeed[needID]
		if !required {
			continue
		}
		if consumed[needID] {
			return fmt.Errorf("project-all craft need %q is represented more than once", needID)
		}
		sourceType := strings.ToLower(strings.TrimSpace(ref.SourceType))
		if sourceType != "craft_recall" && sourceType != "benchmark_craft_recall" {
			return fmt.Errorf("project-all craft need %q has invalid source_type %q", needID, ref.SourceType)
		}
		if len(ref.UsableDetails) == 0 || strings.TrimSpace(ref.TransformationRule) == "" || len(ref.DoNotUse) == 0 {
			return fmt.Errorf("project-all craft need %q requires usable_details/transformation_rule/do_not_use", needID)
		}
		matched := false
		for _, sourceRef := range ref.SourceRefs {
			if _, ok := allowed[strings.TrimSpace(sourceRef)]; !ok {
				return fmt.Errorf("project-all craft need %q cites a hit outside its exact receipt attempt", needID)
			}
			matched = true
		}
		if !matched {
			return fmt.Errorf("project-all craft need %q requires at least one exact receipt hit", needID)
		}
		consumed[needID] = true
	}
	var missing []string
	for needID := range hitsByNeed {
		if !consumed[needID] {
			missing = append(missing, needID)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("project-all craft receipt has unconsumed hit-bearing needs: %s", strings.Join(missing, ","))
	}
	return nil
}

// ValidateProjectAllCraftRenderConsumptionV2 proves that every hit-bearing
// project-all craft need survived the plan-to-prose projection. The sealed
// render context carries only transformed methods and exact receipt refs; raw
// retrieval text remains server-side.
func ValidateProjectAllCraftRenderConsumptionV2(raw json.RawMessage, receipt CraftRecallReceipt) error {
	required := make(map[string]map[string]struct{})
	for _, attempt := range receipt.Attempts {
		if len(attempt.Hits) == 0 {
			continue
		}
		refs := make(map[string]struct{}, len(attempt.Hits))
		for _, hit := range attempt.Hits {
			if summary := strings.TrimSpace(hit.Summary); summary != "" && strings.Contains(string(raw), summary) {
				return fmt.Errorf("project-all craft render consumption: raw craft summary leaked into render context")
			}
			refs[strings.TrimSpace(hit.Ref)] = struct{}{}
		}
		required[strings.TrimSpace(attempt.Need.ID)] = refs
	}
	if len(required) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("project-all craft render consumption: decode render context: %w", err)
	}
	packet := planningV2RenderPacket(payload)
	if packet == nil {
		return fmt.Errorf("project-all craft render consumption: hit-bearing receipt requires render_packet.craft_methods")
	}
	type craftMethod struct {
		ReceiptID  string   `json:"receipt_id"`
		Need       string   `json:"need"`
		SourceRefs []string `json:"source_refs"`
	}
	rawMethods, ok := packet["craft_methods"]
	if !ok {
		return fmt.Errorf("project-all craft render consumption: hit-bearing receipt requires render_packet.craft_methods")
	}
	encoded, err := json.Marshal(rawMethods)
	if err != nil {
		return fmt.Errorf("project-all craft render consumption: encode craft_methods: %w", err)
	}
	var methods []craftMethod
	if err := json.Unmarshal(encoded, &methods); err != nil {
		return fmt.Errorf("project-all craft render consumption: decode craft_methods: %w", err)
	}
	consumed := make(map[string]bool, len(required))
	for _, method := range methods {
		needID := strings.TrimSpace(method.Need)
		allowed, needed := required[needID]
		if !needed {
			continue
		}
		if consumed[needID] {
			return fmt.Errorf("project-all craft render consumption: need %q is represented more than once", needID)
		}
		if strings.TrimSpace(method.ReceiptID) != strings.TrimSpace(receipt.ID) {
			return fmt.Errorf("project-all craft render consumption: need %q receipt id mismatch", needID)
		}
		matched := false
		for _, ref := range method.SourceRefs {
			if _, exact := allowed[strings.TrimSpace(ref)]; exact {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("project-all craft render consumption: need %q lacks an exact receipt hit ref", needID)
		}
		consumed[needID] = true
	}
	missing := make([]string, 0)
	for needID := range required {
		if !consumed[needID] {
			missing = append(missing, needID)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("project-all craft render consumption: missing hit-bearing needs: %s", strings.Join(missing, ","))
	}
	return nil
}

func planningV2RenderPacket(payload map[string]any) map[string]any {
	if working, ok := payload["working_memory"].(map[string]any); ok {
		for _, key := range []string{"render_packet", "draft_packet"} {
			if packet, ok := working[key].(map[string]any); ok {
				return packet
			}
		}
	}
	for _, key := range []string{"render_packet", "draft_packet"} {
		if packet, ok := payload[key].(map[string]any); ok {
			return packet
		}
	}
	return nil
}

func validateDisjointV2Sets(sets map[string][]string) error {
	seen := make(map[string]string)
	keys := make([]string, 0, len(sets))
	for key := range sets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range normalizeV2Strings(sets[key]) {
			if previous, exists := seen[value]; exists {
				return fmt.Errorf("%s appears in both %s and %s", value, previous, key)
			}
			seen[value] = key
		}
	}
	return nil
}

func ComputeProjectedPlanningContextV2Digest(context ProjectedPlanningContextV2) (string, error) {
	context.ContextDigest = ""
	return planningV2Digest(context)
}

func ValidateProjectedPlanningContextV2(context ProjectedPlanningContextV2) error {
	if context.Version != ProjectedPlanningContextV2Version ||
		!strings.HasPrefix(context.GenerationID, PlanningGenerationIDPrefix) ||
		context.NextChapter <= 0 ||
		context.ThroughChapter != context.NextChapter-1 {
		return fmt.Errorf("projected planning context v2: invalid identity or chapter boundary")
	}
	if err := validatePlanningV2Digest("state_root", context.StateRoot); err != nil {
		return fmt.Errorf("projected planning context v2: %w", err)
	}
	if predecessor := context.PredecessorContract; predecessor != nil {
		if predecessor.Chapter != context.ThroughChapter ||
			strings.TrimSpace(predecessor.OutgoingConsequenceID) == "" ||
			predecessor.OutgoingConsequenceID != strings.TrimSpace(predecessor.OutgoingConsequenceID) ||
			strings.TrimSpace(predecessor.OutgoingConsequenceText) == "" ||
			predecessor.OutgoingConsequenceText != strings.TrimSpace(predecessor.OutgoingConsequenceText) {
			return fmt.Errorf("projected planning context v2: predecessor_contract identity is incomplete")
		}
		if err := validatePlanningV2Digest("predecessor_contract.bundle_digest", predecessor.BundleDigest); err != nil {
			return fmt.Errorf("projected planning context v2: %w", err)
		}
		if err := validatePlanningV2Digest("predecessor_contract.projected_post_state_root", predecessor.ProjectedPostStateRoot); err != nil {
			return fmt.Errorf("projected planning context v2: %w", err)
		}
		if predecessor.ProjectedPostStateRoot != context.StateRoot {
			return fmt.Errorf("projected planning context v2: predecessor_contract post-state does not equal state_root")
		}
	}
	lastChapter := 0
	lastFactKey := ""
	for i, fact := range context.CumulativeState {
		key := strings.Join([]string{
			strings.TrimSpace(fact.Category),
			strings.TrimSpace(fact.Subject),
			strings.TrimSpace(fact.Object),
			strings.TrimSpace(fact.Field),
		}, "\x00")
		if strings.TrimSpace(fact.Category) == "" ||
			strings.TrimSpace(fact.StableID) == "" ||
			strings.TrimSpace(fact.Subject) == "" ||
			strings.TrimSpace(fact.Field) == "" ||
			strings.TrimSpace(fact.Value) == "" ||
			fact.ThroughChapter <= 0 ||
			fact.ThroughChapter >= context.NextChapter {
			return fmt.Errorf("projected planning context v2: cumulative_state[%d] is incomplete", i)
		}
		if lastFactKey != "" && key <= lastFactKey {
			return fmt.Errorf("projected planning context v2: cumulative_state is not strictly ordered")
		}
		lastFactKey = key
	}
	for i, transition := range context.RecentTransitions {
		if transition.Chapter <= lastChapter || transition.Chapter >= context.NextChapter {
			return fmt.Errorf("projected planning context v2: recent_transitions[%d] is out of order", i)
		}
		if err := validatePlanningV2Digest("bundle_digest", transition.BundleDigest); err != nil {
			return fmt.Errorf("projected planning context v2: recent_transitions[%d]: %w", i, err)
		}
		if err := validatePlanningV2Digest("projected_post_state_root", transition.ProjectedPostStateRoot); err != nil {
			return fmt.Errorf("projected planning context v2: recent_transitions[%d]: %w", i, err)
		}
		if err := ValidateProjectedDeltaV2(transition.Delta); err != nil {
			return fmt.Errorf("projected planning context v2: recent_transitions[%d]: %w", i, err)
		}
		lastChapter = transition.Chapter
	}
	if predecessor := context.PredecessorContract; predecessor != nil {
		if len(context.RecentTransitions) == 0 {
			return fmt.Errorf("projected planning context v2: predecessor_contract lacks recent transition evidence")
		}
		latest := context.RecentTransitions[len(context.RecentTransitions)-1]
		if latest.Chapter != predecessor.Chapter ||
			latest.BundleDigest != predecessor.BundleDigest ||
			latest.ProjectedPostStateRoot != predecessor.ProjectedPostStateRoot {
			return fmt.Errorf("projected planning context v2: predecessor_contract does not match latest transition")
		}
	}
	seen := make(map[string]struct{}, len(context.OpenObligations))
	for i, obligation := range context.OpenObligations {
		if strings.TrimSpace(obligation.ID) == "" ||
			strings.TrimSpace(obligation.Contract) == "" ||
			obligation.DueWindow.FromChapter <= 0 ||
			obligation.DueWindow.ToChapter < obligation.DueWindow.FromChapter {
			return fmt.Errorf("projected planning context v2: open_obligations[%d] is incomplete", i)
		}
		if _, exists := seen[obligation.ID]; exists {
			return fmt.Errorf("projected planning context v2: duplicate obligation %s", obligation.ID)
		}
		seen[obligation.ID] = struct{}{}
		dueNow := false
		for _, consumer := range obligation.ConsumerChapters {
			if consumer == context.NextChapter {
				dueNow = true
			}
		}
		if obligation.DueNow != dueNow {
			return fmt.Errorf("projected planning context v2: obligation %s due_now mismatch", obligation.ID)
		}
	}
	if err := validatePlanningV2Digest("context_digest", context.ContextDigest); err != nil {
		return fmt.Errorf("projected planning context v2: %w", err)
	}
	want, err := ComputeProjectedPlanningContextV2Digest(context)
	if err != nil {
		return err
	}
	if context.ContextDigest != want {
		return fmt.Errorf("projected planning context v2: context_digest mismatch")
	}
	return nil
}

// DeriveProjectedPlanningContextV2 reconstructs the exact authoritative
// packet for nextChapter from prior formal bundles. StateRoot commits the full
// history; a bounded transition window gives the model the latest changes.
func DeriveProjectedPlanningContextV2(
	generation PlanningGenerationV2,
	prior []ProjectedChapterBundle,
	registry ObligationRegistryV2,
	nextChapter int,
) (ProjectedPlanningContextV2, error) {
	context := ProjectedPlanningContextV2{
		Version:        ProjectedPlanningContextV2Version,
		GenerationID:   generation.GenerationID,
		NextChapter:    nextChapter,
		ThroughChapter: nextChapter - 1,
		StateRoot:      generation.BaseStateRoot,
	}
	if nextChapter < generation.FirstProjectedChapter || nextChapter > generation.LastProjectedChapter {
		return context, fmt.Errorf("projected planning context v2: chapter %d outside generation range", nextChapter)
	}
	ordered := append([]ProjectedChapterBundle(nil), prior...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Chapter < ordered[j].Chapter })
	wantCount := nextChapter - generation.FirstProjectedChapter
	if len(ordered) != wantCount {
		return context, fmt.Errorf(
			"projected planning context v2: got %d prior bundles, want %d before chapter %d",
			len(ordered),
			wantCount,
			nextChapter,
		)
	}
	for i := range ordered {
		wantChapter := generation.FirstProjectedChapter + i
		if ordered[i].Chapter != wantChapter {
			return context, fmt.Errorf("projected planning context v2: expected prior chapter %d, got %d", wantChapter, ordered[i].Chapter)
		}
		if err := ValidateProjectedChapterBundle(ordered[i]); err != nil {
			return context, err
		}
		context.StateRoot = ordered[i].ProjectedPostStateRoot
	}
	if len(ordered) > 0 {
		last := ordered[len(ordered)-1]
		contract := last.ChapterPlan.CausalSimulation.ArcTransition
		if contract.OutgoingConsequenceID != "" || contract.OutgoingConsequenceText != "" {
			predecessor := projectedPlanningPredecessorContractV2(last)
			context.PredecessorContract = &predecessor
		}
	}
	latestFacts := make(map[string]ProjectedPlanningStateFactV2)
	for _, bundle := range ordered {
		for _, category := range projectedPlanningDeltaCategoriesV2(bundle.ProjectedDelta) {
			for _, mutation := range category.Mutations {
				key := strings.Join([]string{
					category.Name,
					strings.TrimSpace(mutation.Subject),
					strings.TrimSpace(mutation.Object),
					strings.TrimSpace(mutation.Field),
				}, "\x00")
				latestFacts[key] = ProjectedPlanningStateFactV2{
					Category:       category.Name,
					StableID:       mutation.StableID,
					Subject:        mutation.Subject,
					Object:         mutation.Object,
					Field:          mutation.Field,
					Value:          mutation.After,
					ThroughChapter: bundle.Chapter,
				}
			}
		}
	}
	factKeys := make([]string, 0, len(latestFacts))
	for key := range latestFacts {
		factKeys = append(factKeys, key)
	}
	sort.Strings(factKeys)
	for _, key := range factKeys {
		context.CumulativeState = append(context.CumulativeState, latestFacts[key])
	}
	if context.CumulativeState == nil {
		context.CumulativeState = []ProjectedPlanningStateFactV2{}
	}
	start := len(ordered) - 6
	if start < 0 {
		start = 0
	}
	for _, bundle := range ordered[start:] {
		context.RecentTransitions = append(context.RecentTransitions, ProjectedPlanningTransitionV2{
			Chapter:                bundle.Chapter,
			BundleDigest:           bundle.BundleDigest,
			ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
			Delta:                  NormalizeProjectedDeltaV2(bundle.ProjectedDelta),
		})
	}
	for _, obligation := range registry.Obligations {
		if obligation.Origin.Chapter >= nextChapter {
			continue
		}
		consumed := false
		dueNow := false
		for _, consumer := range obligation.ConsumerChapters {
			if consumer < nextChapter {
				consumed = true
			}
			if consumer == nextChapter {
				dueNow = true
			}
		}
		// Registry.State is the final seal-time state. Historical planning
		// contexts must not be rewritten retroactively when a later chapter
		// satisfies or supersedes an obligation; origin/consumer chronology is
		// the only authority here.
		if consumed {
			continue
		}
		context.OpenObligations = append(context.OpenObligations, ProjectedPlanningObligationV2{
			ID:               obligation.ID,
			Kind:             obligation.Kind,
			Contract:         obligation.Contract,
			Hardness:         obligation.Hardness,
			DueWindow:        obligation.DueWindow,
			ConsumerChapters: normalizeV2Ints(obligation.ConsumerChapters),
			DueNow:           dueNow,
		})
	}
	sort.Slice(context.OpenObligations, func(i, j int) bool {
		return context.OpenObligations[i].ID < context.OpenObligations[j].ID
	})
	context.ContextDigest, _ = ComputeProjectedPlanningContextV2Digest(context)
	if err := ValidateProjectedPlanningContextV2(context); err != nil {
		return context, err
	}
	return context, nil
}

type projectedPlanningDeltaCategoryV2 struct {
	Name      string
	Mutations []StateMutationV2
}

func projectedPlanningDeltaCategoriesV2(delta ProjectedDelta) []projectedPlanningDeltaCategoryV2 {
	return []projectedPlanningDeltaCategoryV2{
		{Name: "timeline", Mutations: delta.Timeline},
		{Name: "character_state", Mutations: delta.CharacterState},
		{Name: "relationship", Mutations: delta.Relationships},
		{Name: "resource", Mutations: delta.Resources},
		{Name: "knowledge", Mutations: delta.Knowledge},
		{Name: "location", Mutations: delta.Locations},
		{Name: "foreshadow", Mutations: delta.Foreshadows},
		{Name: "obligation", Mutations: delta.Obligations},
	}
}

func DeriveProjectedChainGenesisV2(generation PlanningGenerationV2) (string, error) {
	if !strings.HasPrefix(generation.GenerationID, PlanningGenerationIDPrefix) ||
		generation.FirstProjectedChapter <= 0 {
		return "", fmt.Errorf("projected chain genesis v2: invalid generation identity/range")
	}
	for name, value := range map[string]string{
		"base_canon_root":           generation.BaseCanonRoot,
		"base_state_root":           generation.BaseStateRoot,
		"stable_outline_root":       generation.StableOutlineRoot,
		"planning_dependency_root":  generation.PlanningDependencyRoot,
		"random_seed_contract_root": generation.RandomSeedContractRoot,
	} {
		if err := validatePlanningV2Digest(name, value); err != nil {
			return "", fmt.Errorf("projected chain genesis v2: %w", err)
		}
	}
	if IsArcPlanningGenerationV2(generation) {
		return planningV2Digest(struct {
			Version                string                    `json:"version"`
			GenerationID           string                    `json:"generation_id"`
			ProjectionScope        PlanningProjectionScopeV2 `json:"projection_scope"`
			ScopeID                string                    `json:"scope_id"`
			BookHorizonChapter     int                       `json:"book_horizon_chapter"`
			BaseCanonRoot          string                    `json:"base_canon_root"`
			BaseStateRoot          string                    `json:"base_state_root"`
			StableOutlineRoot      string                    `json:"stable_outline_root"`
			PlanningDependencyRoot string                    `json:"planning_dependency_root"`
			RandomSeedContractRoot string                    `json:"random_seed_contract_root"`
			FirstProjectedChapter  int                       `json:"first_projected_chapter"`
		}{
			Version:                "projected-chain-genesis.arc.v2",
			GenerationID:           generation.GenerationID,
			ProjectionScope:        generation.ProjectionScope,
			ScopeID:                generation.ScopeID,
			BookHorizonChapter:     generation.BookHorizonChapter,
			BaseCanonRoot:          generation.BaseCanonRoot,
			BaseStateRoot:          generation.BaseStateRoot,
			StableOutlineRoot:      generation.StableOutlineRoot,
			PlanningDependencyRoot: generation.PlanningDependencyRoot,
			RandomSeedContractRoot: generation.RandomSeedContractRoot,
			FirstProjectedChapter:  generation.FirstProjectedChapter,
		})
	}
	return planningV2Digest(struct {
		Version                string `json:"version"`
		GenerationID           string `json:"generation_id"`
		BaseCanonRoot          string `json:"base_canon_root"`
		BaseStateRoot          string `json:"base_state_root"`
		StableOutlineRoot      string `json:"stable_outline_root"`
		PlanningDependencyRoot string `json:"planning_dependency_root"`
		RandomSeedContractRoot string `json:"random_seed_contract_root"`
		FirstProjectedChapter  int    `json:"first_projected_chapter"`
	}{
		Version:                "projected-chain-genesis.v2",
		GenerationID:           generation.GenerationID,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		StableOutlineRoot:      generation.StableOutlineRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		RandomSeedContractRoot: generation.RandomSeedContractRoot,
		FirstProjectedChapter:  generation.FirstProjectedChapter,
	})
}

func ValidateProjectedChapterBundleChain(generation PlanningGenerationV2, bundles []ProjectedChapterBundle, registry ObligationRegistryV2) error {
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		return fmt.Errorf("projected chapter bundle chain v2: %w", err)
	}
	if err := ValidateObligationRegistryV2(registry); err != nil {
		return fmt.Errorf("projected chapter bundle chain v2: %w", err)
	}
	if err := validateObligationRegistryScopeAgainstGenerationV2(generation, registry); err != nil {
		return fmt.Errorf("projected chapter bundle chain v2: %w", err)
	}
	if len(bundles) != generation.ProjectedChapterCount {
		return fmt.Errorf("projected chapter bundle chain v2: got %d bundles, generation claims %d", len(bundles), generation.ProjectedChapterCount)
	}
	if generation.Status == PlanningGenerationSealedV2 && len(bundles) != generation.ExpectedChapterCount {
		return fmt.Errorf("projected chapter bundle chain v2: sealed generation has incomplete formal bundle range")
	}
	if len(bundles) == 0 {
		if generation.Status == PlanningGenerationSealedV2 {
			return fmt.Errorf("projected chapter bundle chain v2: sealed generation cannot be empty")
		}
		return nil
	}
	ordered := append([]ProjectedChapterBundle(nil), bundles...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Chapter < ordered[j].Chapter })
	genesis, err := DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		return err
	}
	knownObligations := make(map[string]ObligationV2, len(registry.Obligations))
	if generation.Status == PlanningGenerationSealedV2 && IsArcPlanningGenerationV2(generation) {
		if err := ValidateArcObligationCarryBoundaryV2(generation, registry); err != nil {
			return fmt.Errorf("projected chapter bundle chain v2: %w", err)
		}
	}
	for _, obligation := range registry.Obligations {
		knownObligations[obligation.ID] = obligation
	}
	createdAt := make(map[string]int)
	seenArcConsequences := make(map[string]int)
	for i, bundle := range ordered {
		if err := ValidateProjectedChapterBundle(bundle); err != nil {
			return fmt.Errorf("projected chapter bundle chain v2: chapter %d: %w", bundle.Chapter, err)
		}
		if bundle.GenerationID != generation.GenerationID {
			return fmt.Errorf("projected chapter bundle chain v2: chapter %d generation_id mismatch", bundle.Chapter)
		}
		wantChapter := generation.FirstProjectedChapter + i
		if bundle.Chapter != wantChapter {
			return fmt.Errorf("projected chapter bundle chain v2: expected chapter %d, got %d", wantChapter, bundle.Chapter)
		}
		if i == 0 {
			if bundle.PreviousBundleDigest != genesis {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d previous_bundle_digest does not match genesis", bundle.Chapter)
			}
			if bundle.ProjectedPreStateRoot != generation.BaseStateRoot {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d pre-state does not match base_state_root", bundle.Chapter)
			}
		} else {
			previous := ordered[i-1]
			if bundle.PreviousBundleDigest != previous.BundleDigest {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d previous_bundle_digest does not match chapter %d", bundle.Chapter, previous.Chapter)
			}
			if bundle.ProjectedPreStateRoot != previous.ProjectedPostStateRoot {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d pre-state does not match chapter %d post-state", bundle.Chapter, previous.Chapter)
			}
		}
		planningContext, err := DeriveProjectedPlanningContextV2(
			generation,
			ordered[:i],
			registry,
			bundle.Chapter,
		)
		if err != nil {
			return fmt.Errorf("projected chapter bundle chain v2: chapter %d planning context: %w", bundle.Chapter, err)
		}
		if bundle.PlanningContextDigest != planningContext.ContextDigest {
			return fmt.Errorf("projected chapter bundle chain v2: chapter %d did not consume the exact prior projected context", bundle.Chapter)
		}
		if bundle.ProjectedPreStateRoot != planningContext.StateRoot {
			return fmt.Errorf("projected chapter bundle chain v2: chapter %d pre-state does not equal its authoritative planning context", bundle.Chapter)
		}
		if IsArcPlanningGenerationV2(generation) {
			if err := ValidateArcChapterTransitionContract(bundle.ChapterPlan, planningContext.PredecessorContract); err != nil {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d: %w", bundle.Chapter, err)
			}
			consequenceID := bundle.ChapterPlan.CausalSimulation.ArcTransition.OutgoingConsequenceID
			if previousChapter, duplicate := seenArcConsequences[consequenceID]; duplicate {
				return fmt.Errorf(
					"projected chapter bundle chain v2: outgoing consequence id %q is reused by chapters %d and %d",
					consequenceID,
					previousChapter,
					bundle.Chapter,
				)
			}
			seenArcConsequences[consequenceID] = bundle.Chapter
		}
		for _, id := range append(append(append([]string{}, bundle.ObligationsConsumed...), bundle.ObligationsCreated...), bundle.ObligationsCarried...) {
			if _, exists := knownObligations[id]; !exists {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d references orphan obligation %s", bundle.Chapter, id)
			}
		}
		for _, id := range bundle.ObligationsCreated {
			obligation := knownObligations[id]
			if obligation.Origin.GenerationID != generation.GenerationID || obligation.Origin.Chapter != bundle.Chapter {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d cannot create obligation %s with different origin", bundle.Chapter, id)
			}
			if prior, duplicate := createdAt[id]; duplicate {
				return fmt.Errorf("projected chapter bundle chain v2: obligation %s created in both chapters %d and %d", id, prior, bundle.Chapter)
			}
			createdAt[id] = bundle.Chapter
		}
		for _, id := range bundle.ObligationsConsumed {
			obligation := knownObligations[id]
			found := false
			for _, consumer := range obligation.ConsumerChapters {
				if consumer == bundle.Chapter {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("projected chapter bundle chain v2: chapter %d consumes obligation %s but registry does not name it as consumer", bundle.Chapter, id)
			}
		}
	}
	for _, obligation := range registry.Obligations {
		if obligation.Origin.GenerationID == generation.GenerationID &&
			obligation.Origin.Chapter >= generation.FirstProjectedChapter &&
			obligation.Origin.Chapter <= generation.FirstProjectedChapter+len(ordered)-1 {
			if _, exists := createdAt[obligation.ID]; !exists {
				return fmt.Errorf("projected chapter bundle chain v2: registry obligation %s has projected origin but no creating bundle", obligation.ID)
			}
		}
		if generation.Status == PlanningGenerationSealedV2 {
			arcScoped := IsArcPlanningGenerationV2(generation)
			if !arcScoped && (obligation.State == ObligationOpenV2 || obligation.State == ObligationPlannedV2) {
				return fmt.Errorf(
					"projected chapter bundle chain v2: sealed generation leaves obligation %s unresolved",
					obligation.ID,
				)
			}
			consumerHorizon := generation.LastProjectedChapter
			if arcScoped {
				consumerHorizon = generation.BookHorizonChapter
			}
			hasInArcConsumer := false
			for _, consumer := range obligation.ConsumerChapters {
				if consumer <= obligation.Origin.Chapter ||
					consumer < generation.FirstProjectedChapter ||
					consumer > consumerHorizon {
					return fmt.Errorf(
						"projected chapter bundle chain v2: sealed obligation %s has invalid future consumer chapter %d",
						obligation.ID,
						consumer,
					)
				}
				if consumer <= generation.LastProjectedChapter {
					hasInArcConsumer = true
				}
			}
			if arcScoped && obligation.State == ObligationSatisfiedV2 && !hasInArcConsumer {
				return fmt.Errorf(
					"projected chapter bundle chain v2: arc marks obligation %s satisfied without an in-arc consumer",
					obligation.ID,
				)
			}
		}
	}
	if generation.ChainHeadRoot != ordered[0].BundleDigest {
		return fmt.Errorf("projected chapter bundle chain v2: generation chain_head_root mismatch")
	}
	if generation.ChainTailRoot != ordered[len(ordered)-1].BundleDigest {
		return fmt.Errorf("projected chapter bundle chain v2: generation chain_tail_root mismatch")
	}
	return nil
}

func ValidateProjectedChapterChainV2(generation PlanningGenerationV2, bundles []ProjectedChapterBundle, registry ObligationRegistryV2) error {
	return ValidateProjectedChapterBundleChain(generation, bundles, registry)
}

func normalizeProjectedChainManifestV2(manifest ProjectedChainManifestV2) ProjectedChainManifestV2 {
	manifest.GenerationID = strings.TrimSpace(manifest.GenerationID)
	entries := append([]ProjectedBundleDigestEntryV2(nil), manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Chapter < entries[j].Chapter })
	if entries == nil {
		entries = []ProjectedBundleDigestEntryV2{}
	}
	manifest.Entries = entries
	manifest.FactReceiptDigests = normalizeV2Strings(manifest.FactReceiptDigests)
	manifest.CraftReceiptDigests = normalizeV2Strings(manifest.CraftReceiptDigests)
	return manifest
}

func ComputeProjectedChainManifestV2Digest(manifest ProjectedChainManifestV2) (string, error) {
	manifest.ManifestDigest = ""
	return planningV2Digest(normalizeProjectedChainManifestV2(manifest))
}

func ComputeProjectedChainManifestDigest(manifest ProjectedChainManifestV2) (string, error) {
	return ComputeProjectedChainManifestV2Digest(manifest)
}

func ValidateProjectedChainManifestV2(manifest ProjectedChainManifestV2, generation PlanningGenerationV2, bundles []ProjectedChapterBundle, registry ObligationRegistryV2) error {
	if manifest.Version != ProjectedChainManifestV2Version {
		return fmt.Errorf("projected chain manifest v2: unsupported version %q", manifest.Version)
	}
	if err := ValidateProjectedChapterBundleChain(generation, bundles, registry); err != nil {
		return fmt.Errorf("projected chain manifest v2: %w", err)
	}
	if generation.Status != PlanningGenerationSealedV2 {
		return fmt.Errorf("projected chain manifest v2: manifest can only describe a sealed generation")
	}
	if manifest.GenerationID != generation.GenerationID ||
		manifest.FirstChapter != generation.FirstProjectedChapter ||
		manifest.LastChapter != generation.LastProjectedChapter ||
		manifest.ChapterCount != generation.ExpectedChapterCount ||
		len(manifest.Entries) != len(bundles) ||
		manifest.ChainHeadRoot != generation.ChainHeadRoot ||
		manifest.ChainTailRoot != generation.ChainTailRoot ||
		manifest.ObligationRegistryRoot != generation.ObligationRegistryRoot {
		return fmt.Errorf("projected chain manifest v2: generation/range/chain identity mismatch")
	}
	if err := validatePlanningV2Time("created_at", manifest.CreatedAt); err != nil {
		return fmt.Errorf("projected chain manifest v2: %w", err)
	}
	ordered := append([]ProjectedChapterBundle(nil), bundles...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Chapter < ordered[j].Chapter })
	entries := append([]ProjectedBundleDigestEntryV2(nil), manifest.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Chapter < entries[j].Chapter })
	factDigests := make([]string, 0, len(ordered))
	craftDigests := make([]string, 0, len(ordered))
	for i, bundle := range ordered {
		entry := entries[i]
		if entry.Chapter != bundle.Chapter ||
			entry.BundleDigest != bundle.BundleDigest ||
			entry.PreviousBundleDigest != bundle.PreviousBundleDigest ||
			entry.ProjectedPreStateRoot != bundle.ProjectedPreStateRoot ||
			entry.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
			return fmt.Errorf("projected chain manifest v2: entry for chapter %d does not match exact bundle", bundle.Chapter)
		}
		if bundle.RAGFactReceipt == nil || bundle.CraftRecallReceipt == nil {
			return fmt.Errorf("projected chain manifest v2: chapter %d receipt coverage is incomplete", bundle.Chapter)
		}
		wantRAGDigest, err := RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
		if err != nil {
			return fmt.Errorf("projected chain manifest v2: chapter %d RAG receipt: %w", bundle.Chapter, err)
		}
		factDigests = append(factDigests, wantRAGDigest)
		if entry.RAGFactReceiptDigest != wantRAGDigest ||
			bundle.RAGFactReceiptDigest != wantRAGDigest {
			return fmt.Errorf("projected chain manifest v2: chapter %d RAG receipt digest mismatch", bundle.Chapter)
		}
		wantCraftDigest, err := CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
		if err != nil {
			return fmt.Errorf("projected chain manifest v2: chapter %d craft receipt: %w", bundle.Chapter, err)
		}
		craftDigests = append(craftDigests, wantCraftDigest)
		if entry.CraftRecallReceiptDigest != wantCraftDigest ||
			bundle.CraftRecallReceiptDigest != wantCraftDigest {
			return fmt.Errorf("projected chain manifest v2: chapter %d craft receipt digest mismatch", bundle.Chapter)
		}
	}
	if !sameV2Strings(manifest.FactReceiptDigests, factDigests) {
		return fmt.Errorf("projected chain manifest v2: fact_receipt_digests do not cover exact bundle receipts")
	}
	if !sameV2Strings(manifest.CraftReceiptDigests, craftDigests) {
		return fmt.Errorf("projected chain manifest v2: craft_receipt_digests do not cover exact bundle receipts")
	}
	for _, digest := range append(append(append([]string{}, manifest.FactReceiptDigests...), manifest.CraftReceiptDigests...), manifest.ChainHeadRoot, manifest.ChainTailRoot, manifest.ObligationRegistryRoot, manifest.ManifestDigest) {
		if err := validatePlanningV2Digest("manifest digest/root", digest); err != nil {
			return fmt.Errorf("projected chain manifest v2: %w", err)
		}
	}
	want, err := ComputeProjectedChainManifestV2Digest(manifest)
	if err != nil {
		return err
	}
	if manifest.ManifestDigest != want {
		return fmt.Errorf("projected chain manifest v2: manifest_digest mismatch")
	}
	return nil
}

func ComputeSealReceiptV2Digest(receipt SealReceiptV2) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func ValidateSealReceiptV2(receipt SealReceiptV2, generation PlanningGenerationV2, manifest ProjectedChainManifestV2) error {
	if receipt.Version != SealReceiptV2Version {
		return fmt.Errorf("seal receipt v2: unsupported version %q", receipt.Version)
	}
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		return fmt.Errorf("seal receipt v2: %w", err)
	}
	if generation.Status != PlanningGenerationSealedV2 {
		return fmt.Errorf("seal receipt v2: generation must be sealed")
	}
	if manifest.ManifestDigest == "" {
		return fmt.Errorf("seal receipt v2: chain manifest digest is required")
	}
	if manifest.Version != ProjectedChainManifestV2Version ||
		manifest.GenerationID != generation.GenerationID ||
		manifest.FirstChapter != generation.FirstProjectedChapter ||
		manifest.LastChapter != generation.LastProjectedChapter ||
		manifest.ChapterCount != generation.ExpectedChapterCount ||
		len(manifest.Entries) != generation.ExpectedChapterCount ||
		manifest.ChainHeadRoot != generation.ChainHeadRoot ||
		manifest.ChainTailRoot != generation.ChainTailRoot ||
		manifest.ObligationRegistryRoot != generation.ObligationRegistryRoot {
		return fmt.Errorf("seal receipt v2: chain manifest does not describe the exact sealed generation")
	}
	wantManifestDigest, err := ComputeProjectedChainManifestV2Digest(manifest)
	if err != nil {
		return fmt.Errorf("seal receipt v2: compute chain manifest digest: %w", err)
	}
	if manifest.ManifestDigest != wantManifestDigest {
		return fmt.Errorf("seal receipt v2: chain manifest digest is invalid")
	}
	if receipt.GenerationID != generation.GenerationID ||
		receipt.GenerationDigest != generation.GenerationDigest ||
		receipt.ChainManifestDigest != manifest.ManifestDigest ||
		receipt.ChainHeadRoot != generation.ChainHeadRoot ||
		receipt.ChainTailRoot != generation.ChainTailRoot ||
		receipt.ObligationRegistryRoot != generation.ObligationRegistryRoot ||
		receipt.BaseCanonRoot != generation.BaseCanonRoot ||
		receipt.BaseStateRoot != generation.BaseStateRoot ||
		receipt.PlanningDependencyRoot != generation.PlanningDependencyRoot ||
		receipt.SealedAt != generation.SealedAt {
		return fmt.Errorf("seal receipt v2: receipt does not bind exact immutable generation/manifest roots")
	}
	for name, digest := range map[string]string{
		"generation_digest":        receipt.GenerationDigest,
		"chain_manifest_digest":    receipt.ChainManifestDigest,
		"chain_head_root":          receipt.ChainHeadRoot,
		"chain_tail_root":          receipt.ChainTailRoot,
		"obligation_registry_root": receipt.ObligationRegistryRoot,
		"base_canon_root":          receipt.BaseCanonRoot,
		"base_state_root":          receipt.BaseStateRoot,
		"planning_dependency_root": receipt.PlanningDependencyRoot,
		"receipt_digest":           receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("seal receipt v2: %w", err)
		}
	}
	if err := validatePlanningV2Time("sealed_at", receipt.SealedAt); err != nil {
		return fmt.Errorf("seal receipt v2: %w", err)
	}
	want, err := ComputeSealReceiptV2Digest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("seal receipt v2: receipt_digest mismatch")
	}
	return nil
}

func ComputeProjectionCursorV2Digest(cursor ProjectionCursorV2) (string, error) {
	cursor.CursorDigest = ""
	return planningV2Digest(cursor)
}

func ValidateProjectionCursorV2(cursor ProjectionCursorV2) error {
	if !strings.HasPrefix(cursor.GenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("projection cursor v2: invalid generation_id")
	}
	if cursor.LastProjectedChapter < 0 || cursor.NextProjectChapter <= 0 ||
		cursor.NextProjectChapter != cursor.LastProjectedChapter+1 {
		return fmt.Errorf("projection cursor v2: next_project_chapter must immediately follow last_projected_chapter")
	}
	if cursor.LastBundleDigest != "" {
		if err := validatePlanningV2Digest("last_bundle_digest", cursor.LastBundleDigest); err != nil {
			return fmt.Errorf("projection cursor v2: %w", err)
		}
	}
	if (cursor.UpdatedAt == "") != (cursor.CursorDigest == "") {
		return fmt.Errorf("projection cursor v2: updated_at and cursor_digest must be supplied together")
	}
	if cursor.CursorDigest != "" {
		if err := validatePlanningV2Time("updated_at", cursor.UpdatedAt); err != nil {
			return fmt.Errorf("projection cursor v2: %w", err)
		}
		if err := validatePlanningV2Digest("cursor_digest", cursor.CursorDigest); err != nil {
			return fmt.Errorf("projection cursor v2: %w", err)
		}
		want, err := ComputeProjectionCursorV2Digest(cursor)
		if err != nil {
			return err
		}
		if cursor.CursorDigest != want {
			return fmt.Errorf("projection cursor v2: cursor_digest mismatch")
		}
	}
	return nil
}

func ComputeRealizationCursorV2Digest(cursor RealizationCursorV2) (string, error) {
	cursor.CursorDigest = ""
	cursor.BlockedByRewrites = normalizeV2Ints(cursor.BlockedByRewrites)
	return planningV2Digest(cursor)
}

func ValidateRealizationCursorV2(cursor RealizationCursorV2) error {
	if !strings.HasPrefix(cursor.ActiveGenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("realization cursor v2: invalid active_generation_id")
	}
	if cursor.LastAcceptedChapter < 0 || cursor.NextPromoteChapter <= 0 ||
		cursor.NextPromoteChapter != cursor.LastAcceptedChapter+1 {
		return fmt.Errorf("realization cursor v2: next_promote_chapter must immediately follow last_accepted_chapter")
	}
	if cursor.ActivePromotedChapter != 0 && cursor.ActivePromotedChapter != cursor.NextPromoteChapter {
		return fmt.Errorf("realization cursor v2: active_promoted_chapter must be zero or next_promote_chapter")
	}
	if cursor.ActivePromotedChapter == 0 && cursor.ActivePromotionReceiptDigest != "" {
		return fmt.Errorf("realization cursor v2: inactive cursor cannot bind an active promotion receipt")
	}
	if cursor.ActivePromotedChapter != 0 {
		if err := validatePlanningV2Digest("active_promotion_receipt_digest", cursor.ActivePromotionReceiptDigest); err != nil {
			return fmt.Errorf("realization cursor v2: %w", err)
		}
	}
	if cursor.LastOutcomeReceiptDigest != "" {
		if err := validatePlanningV2Digest("last_outcome_receipt_digest", cursor.LastOutcomeReceiptDigest); err != nil {
			return fmt.Errorf("realization cursor v2: %w", err)
		}
	}
	normalizedRewrites := normalizeV2Ints(cursor.BlockedByRewrites)
	if len(normalizedRewrites) != len(cursor.BlockedByRewrites) {
		return fmt.Errorf("realization cursor v2: blocked_by_rewrites must be unique")
	}
	for i, chapter := range normalizedRewrites {
		if chapter <= 0 {
			return fmt.Errorf("realization cursor v2: blocked_by_rewrites[%d] must be > 0", i)
		}
		if i < len(cursor.BlockedByRewrites) && chapter != cursor.BlockedByRewrites[i] {
			return fmt.Errorf("realization cursor v2: blocked_by_rewrites must be sorted")
		}
	}
	if (cursor.UpdatedAt == "") != (cursor.CursorDigest == "") {
		return fmt.Errorf("realization cursor v2: updated_at and cursor_digest must be supplied together")
	}
	if cursor.CursorDigest != "" {
		if err := validatePlanningV2Time("updated_at", cursor.UpdatedAt); err != nil {
			return fmt.Errorf("realization cursor v2: %w", err)
		}
		if err := validatePlanningV2Digest("cursor_digest", cursor.CursorDigest); err != nil {
			return fmt.Errorf("realization cursor v2: %w", err)
		}
		want, err := ComputeRealizationCursorV2Digest(cursor)
		if err != nil {
			return err
		}
		if cursor.CursorDigest != want {
			return fmt.Errorf("realization cursor v2: cursor_digest mismatch")
		}
	}
	return nil
}

func ComputeActivePlanningGenerationV2Digest(active ActivePlanningGenerationV2) (string, error) {
	active.RecordDigest = ""
	return planningV2Digest(active)
}

func ValidateActivePlanningGenerationV2(active ActivePlanningGenerationV2) error {
	if active.Version != ActivePlanningGenerationV2Version ||
		!strings.HasPrefix(active.GenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("active planning generation v2: invalid version or generation_id")
	}
	if active.PreviousGenerationID != "" && !strings.HasPrefix(active.PreviousGenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("active planning generation v2: invalid previous_generation_id")
	}
	if active.PreviousGenerationID == active.GenerationID {
		return fmt.Errorf("active planning generation v2: previous_generation_id cannot equal generation_id")
	}
	for name, digest := range map[string]string{
		"seal_receipt_digest": active.SealReceiptDigest,
		"record_digest":       active.RecordDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("active planning generation v2: %w", err)
		}
	}
	if err := validatePlanningV2Time("activated_at", active.ActivatedAt); err != nil {
		return fmt.Errorf("active planning generation v2: %w", err)
	}
	want, err := ComputeActivePlanningGenerationV2Digest(active)
	if err != nil {
		return err
	}
	if active.RecordDigest != want {
		return fmt.Errorf("active planning generation v2: record_digest mismatch")
	}
	return nil
}

func ComputeChapterPlanV2Digest(plan ChapterPlan) (string, error) {
	return planningV2Digest(plan)
}

func ComputePromotionReceiptV2Digest(receipt PromotionReceiptV2) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func ValidatePromotionReceiptV2(receipt PromotionReceiptV2) error {
	if receipt.Version != PromotionReceiptV2Version {
		return fmt.Errorf("promotion receipt v2: unsupported version %q", receipt.Version)
	}
	if !strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) || receipt.Chapter <= 0 {
		return fmt.Errorf("promotion receipt v2: invalid generation_id or chapter")
	}
	for name, digest := range map[string]string{
		"bundle_digest":            receipt.BundleDigest,
		"actual_pre_state_root":    receipt.ActualPreStateRoot,
		"projected_pre_state_root": receipt.ProjectedPreStateRoot,
		"render_dependency_root":   receipt.RenderDependencyRoot,
		"frozen_plan_digest":       receipt.FrozenPlanDigest,
		"receipt_digest":           receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("promotion receipt v2: %w", err)
		}
	}
	if receipt.ActualPreStateRoot != receipt.ProjectedPreStateRoot {
		return fmt.Errorf("promotion receipt v2: actual_pre_state_root must exactly match projected_pre_state_root")
	}
	if receipt.Mode != ExactPromotionModeV2 {
		return fmt.Errorf("promotion receipt v2: mode must be exact")
	}
	if err := validatePlanningV2Time("promoted_at", receipt.PromotedAt); err != nil {
		return fmt.Errorf("promotion receipt v2: %w", err)
	}
	want, err := ComputePromotionReceiptV2Digest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("promotion receipt v2: receipt_digest mismatch")
	}
	return nil
}

func ValidatePromotionReceiptAgainstBundleV2(receipt PromotionReceiptV2, bundle ProjectedChapterBundle) error {
	if err := ValidatePromotionReceiptV2(receipt); err != nil {
		return err
	}
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		return err
	}
	if receipt.GenerationID != bundle.GenerationID ||
		receipt.Chapter != bundle.Chapter ||
		receipt.BundleDigest != bundle.BundleDigest ||
		receipt.ProjectedPreStateRoot != bundle.ProjectedPreStateRoot {
		return fmt.Errorf("promotion receipt v2: receipt does not bind exact projected bundle")
	}
	wantPlanDigest, err := ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		return err
	}
	if receipt.FrozenPlanDigest != wantPlanDigest {
		return fmt.Errorf("promotion receipt v2: frozen_plan_digest is not the exact sealed chapter plan")
	}
	return nil
}

func ComputeActualOutcomeReceiptV2Digest(receipt ActualOutcomeReceiptV2) (string, error) {
	receipt.ReceiptDigest = ""
	receipt.ActualDelta = NormalizeProjectedDeltaV2(receipt.ActualDelta)
	receipt.ObligationsSatisfied = normalizeV2Strings(receipt.ObligationsSatisfied)
	receipt.ObligationsCreatedUnplanned = normalizeV2Strings(receipt.ObligationsCreatedUnplanned)
	return planningV2Digest(receipt)
}

func ValidateActualOutcomeReceiptV2(receipt ActualOutcomeReceiptV2) error {
	if receipt.Version != ActualOutcomeReceiptV2Version {
		return fmt.Errorf("actual outcome receipt v2: unsupported version %q", receipt.Version)
	}
	if !strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) ||
		receipt.Chapter <= 0 || receipt.CommitCheckpointSeq <= 0 {
		return fmt.Errorf("actual outcome receipt v2: invalid generation_id, chapter, or commit_checkpoint_seq")
	}
	for name, digest := range map[string]string{
		"promotion_receipt_digest":  receipt.PromotionReceiptDigest,
		"chapter_body_sha256":       receipt.ChapterBodySHA256,
		"actual_pre_state_root":     receipt.ActualPreStateRoot,
		"actual_post_state_root":    receipt.ActualPostStateRoot,
		"actual_canon_root":         receipt.ActualCanonRoot,
		"projected_post_state_root": receipt.ProjectedPostStateRoot,
		"receipt_digest":            receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("actual outcome receipt v2: %w", err)
		}
	}
	if err := ValidateProjectedDeltaV2(receipt.ActualDelta); err != nil {
		return fmt.Errorf("actual outcome receipt v2: actual_delta: %w", err)
	}
	wantPost, err := DeriveProjectedPostStateRootV2(receipt.ActualPreStateRoot, receipt.ActualDelta)
	if err != nil {
		return fmt.Errorf("actual outcome receipt v2: %w", err)
	}
	if receipt.ActualPostStateRoot != wantPost {
		return fmt.Errorf("actual outcome receipt v2: actual_post_state_root does not match structured actual_delta")
	}
	for _, values := range [][]string{receipt.ObligationsSatisfied, receipt.ObligationsCreatedUnplanned} {
		for _, id := range values {
			if !strings.HasPrefix(id, "obl:") {
				return fmt.Errorf("actual outcome receipt v2: invalid obligation id %q", id)
			}
		}
	}
	if err := validateDisjointV2Sets(map[string][]string{
		"satisfied": receipt.ObligationsSatisfied,
		"unplanned": receipt.ObligationsCreatedUnplanned,
	}); err != nil {
		return fmt.Errorf("actual outcome receipt v2: %w", err)
	}
	wantMatch := receipt.ActualPostStateRoot == receipt.ProjectedPostStateRoot &&
		len(normalizeV2Strings(receipt.ObligationsCreatedUnplanned)) == 0
	if receipt.ProjectionMatch != wantMatch {
		return fmt.Errorf("actual outcome receipt v2: projection_match does not match structural roots/unplanned obligations")
	}
	if err := validatePlanningV2Time("accepted_at", receipt.AcceptedAt); err != nil {
		return fmt.Errorf("actual outcome receipt v2: %w", err)
	}
	want, err := ComputeActualOutcomeReceiptV2Digest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("actual outcome receipt v2: receipt_digest mismatch")
	}
	return nil
}

func ValidateActualOutcomeAgainstPromotionV2(outcome ActualOutcomeReceiptV2, promotion PromotionReceiptV2, bundle ProjectedChapterBundle) error {
	if err := ValidateActualOutcomeReceiptV2(outcome); err != nil {
		return err
	}
	if err := ValidatePromotionReceiptAgainstBundleV2(promotion, bundle); err != nil {
		return err
	}
	if outcome.GenerationID != promotion.GenerationID ||
		outcome.Chapter != promotion.Chapter ||
		outcome.PromotionReceiptDigest != promotion.ReceiptDigest ||
		outcome.ActualPreStateRoot != promotion.ActualPreStateRoot ||
		outcome.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
		return fmt.Errorf("actual outcome receipt v2: outcome does not bind exact promotion/bundle")
	}
	return nil
}

func ComputeSuffixInvalidationReceiptV2Digest(receipt SuffixInvalidationReceiptV2) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func ValidateSuffixInvalidationReceiptV2(receipt SuffixInvalidationReceiptV2) error {
	if receipt.Version != SuffixInvalidationV2Version ||
		!strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("suffix invalidation receipt v2: invalid version or generation_id")
	}
	if receipt.FromChapter <= 0 || receipt.ThroughChapter < receipt.FromChapter {
		return fmt.Errorf("suffix invalidation receipt v2: invalid chapter range")
	}
	if strings.TrimSpace(receipt.Reason) == "" {
		return fmt.Errorf("suffix invalidation receipt v2: reason is required")
	}
	for name, digest := range map[string]string{
		"cause_receipt_digest": receipt.CauseReceiptDigest,
		"receipt_digest":       receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("suffix invalidation receipt v2: %w", err)
		}
	}
	if receipt.PreviousReceiptDigest != "" {
		if err := validatePlanningV2Digest("previous_receipt_digest", receipt.PreviousReceiptDigest); err != nil {
			return fmt.Errorf("suffix invalidation receipt v2: %w", err)
		}
	}
	if receipt.ReplacementGenerationID != "" &&
		!strings.HasPrefix(receipt.ReplacementGenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("suffix invalidation receipt v2: invalid replacement_generation_id")
	}
	if receipt.ReplacementGenerationID == receipt.GenerationID {
		return fmt.Errorf("suffix invalidation receipt v2: replacement generation must differ")
	}
	if err := validatePlanningV2Time("invalidated_at", receipt.InvalidatedAt); err != nil {
		return fmt.Errorf("suffix invalidation receipt v2: %w", err)
	}
	want, err := ComputeSuffixInvalidationReceiptV2Digest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("suffix invalidation receipt v2: receipt_digest mismatch")
	}
	return nil
}

func ComputeGenerationArchiveReceiptV2Digest(receipt GenerationArchiveReceiptV2) (string, error) {
	receipt.ReceiptDigest = ""
	return planningV2Digest(receipt)
}

func ValidateGenerationArchiveReceiptV2(receipt GenerationArchiveReceiptV2) error {
	if receipt.Version != GenerationArchiveV2Version ||
		!strings.HasPrefix(receipt.GenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("generation archive receipt v2: invalid version or generation_id")
	}
	if strings.TrimSpace(receipt.Reason) == "" {
		return fmt.Errorf("generation archive receipt v2: reason is required")
	}
	for name, digest := range map[string]string{
		"seal_receipt_digest": receipt.SealReceiptDigest,
		"receipt_digest":      receipt.ReceiptDigest,
	} {
		if err := validatePlanningV2Digest(name, digest); err != nil {
			return fmt.Errorf("generation archive receipt v2: %w", err)
		}
	}
	if receipt.PreviousReceiptDigest != "" {
		if err := validatePlanningV2Digest("previous_receipt_digest", receipt.PreviousReceiptDigest); err != nil {
			return fmt.Errorf("generation archive receipt v2: %w", err)
		}
	}
	if receipt.SuccessorGenerationID != "" &&
		!strings.HasPrefix(receipt.SuccessorGenerationID, PlanningGenerationIDPrefix) {
		return fmt.Errorf("generation archive receipt v2: invalid successor_generation_id")
	}
	if receipt.SuccessorGenerationID == receipt.GenerationID {
		return fmt.Errorf("generation archive receipt v2: successor generation must differ")
	}
	if err := validatePlanningV2Time("archived_at", receipt.ArchivedAt); err != nil {
		return fmt.Errorf("generation archive receipt v2: %w", err)
	}
	want, err := ComputeGenerationArchiveReceiptV2Digest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != want {
		return fmt.Errorf("generation archive receipt v2: receipt_digest mismatch")
	}
	return nil
}
