package domain

// ChapterPosition records where a chapter sits in the layered story plan.
type ChapterPosition struct {
	Volume      int    `json:"volume,omitempty"`
	VolumeTitle string `json:"volume_title,omitempty"`
	Arc         int    `json:"arc,omitempty"`
	ArcTitle    string `json:"arc_title,omitempty"`
	ArcGoal     string `json:"arc_goal,omitempty"`
}

// ChapterProgressEntry is the durable per-chapter story-progress ledger entry.
// It is generated from committed summaries, timeline/state facts, reviews, and
// the current outline; writers should treat it as already-written fact.
type ChapterProgressEntry struct {
	Chapter             int                 `json:"chapter"`
	Title               string              `json:"title,omitempty"`
	Position            ChapterPosition     `json:"position,omitempty"`
	ReviewStatus        string              `json:"review_status"`
	ReviewSummary       string              `json:"review_summary,omitempty"`
	Summary             string              `json:"summary,omitempty"`
	KeyEvents           []string            `json:"key_events,omitempty"`
	TimelineEvents      []TimelineEvent     `json:"timeline_events,omitempty"`
	ProtagonistChanges  []StateChange       `json:"protagonist_changes,omitempty"`
	StateChanges        []StateChange       `json:"state_changes,omitempty"`
	RelationshipChanges []RelationshipEntry `json:"relationship_changes,omitempty"`
	ResourceChanges     []ResourceClaim     `json:"resource_changes,omitempty"`
	OutlineCoreEvent    string              `json:"outline_core_event,omitempty"`
	OutlineHook         string              `json:"outline_hook,omitempty"`
	UpdatedAt           string              `json:"updated_at,omitempty"`
}

// NextChapterProgressPlan is a deterministic handoff plan for the next chapter.
// It does not replace the chapter plan written by an agent; it constrains that
// plan with the current outline, protagonist state, and open facts.
type NextChapterProgressPlan struct {
	Chapter                  int               `json:"chapter"`
	Title                    string            `json:"title,omitempty"`
	Position                 ChapterPosition   `json:"position,omitempty"`
	CoreEvent                string            `json:"core_event,omitempty"`
	Hook                     string            `json:"hook,omitempty"`
	RequiredBeats            []string          `json:"required_beats,omitempty"`
	ContinuityInputs         []string          `json:"continuity_inputs,omitempty"`
	CharacterContinuity      []CharacterHint   `json:"character_continuity,omitempty"`
	RecentProtagonistChanges []StateChange     `json:"recent_protagonist_changes,omitempty"`
	RecentTimeline           []TimelineEvent   `json:"recent_timeline,omitempty"`
	ActiveForeshadow         []ForeshadowEntry `json:"active_foreshadow,omitempty"`
	ResourceFocus            []ResourceClaim   `json:"resource_focus,omitempty"`
	PlanningInstructions     []string          `json:"planning_instructions,omitempty"`
}

// ChapterProgressLedger is written to meta/chapter_progress.json and rendered
// to meta/chapter_progress.md after accepted chapter reviews.
type ChapterProgressLedger struct {
	Version           int                      `json:"version"`
	NovelName         string                   `json:"novel_name,omitempty"`
	GeneratedAt       string                   `json:"generated_at"`
	Protagonist       string                   `json:"protagonist,omitempty"`
	CompletedChapters []int                    `json:"completed_chapters,omitempty"`
	TotalChapters     int                      `json:"total_chapters,omitempty"`
	CurrentChapter    int                      `json:"current_chapter,omitempty"`
	CurrentVolume     int                      `json:"current_volume,omitempty"`
	CurrentArc        int                      `json:"current_arc,omitempty"`
	Entries           []ChapterProgressEntry   `json:"entries"`
	NextPlan          *NextChapterProgressPlan `json:"next_plan,omitempty"`
}

// CharacterFutureUse records an outline-backed or planning-backed way to reuse
// a character later. It is a writing aid, not a review gate.
type CharacterFutureUse struct {
	Chapter   int             `json:"chapter,omitempty"`
	Title     string          `json:"title,omitempty"`
	Position  ChapterPosition `json:"position,omitempty"`
	UsageType string          `json:"usage_type"` // outline_return / arc_plan / long_arc / optional_cameo / dormant
	Action    string          `json:"action"`
	Evidence  string          `json:"evidence,omitempty"`
}

// CharacterHint is the compact form injected into next_plan and novel_context.
type CharacterHint struct {
	Name       string `json:"name"`
	UsageType  string `json:"usage_type"`
	Suggestion string `json:"suggestion"`
	Evidence   string `json:"evidence,omitempty"`
	ReviewNote string `json:"review_note,omitempty"`
}

// CharacterKnowledgeLedger records what a character can legitimately know or
// suspect before a chapter starts.
type CharacterKnowledgeLedger struct {
	KnownFacts         []string `json:"known_facts,omitempty"`
	UnknownFacts       []string `json:"unknown_facts,omitempty"`
	Suspicions         []string `json:"suspicions,omitempty"`
	FalseBeliefs       []string `json:"false_beliefs,omitempty"`
	EvidenceSeen       []string `json:"evidence_seen,omitempty"`
	Confidence         string   `json:"confidence,omitempty"`
	SourceChapter      int      `json:"source_chapter,omitempty"`
	ForbiddenKnowledge []string `json:"forbidden_knowledge,omitempty"`
}

// CharacterDecisionFrame explains why one action is reasonable for a character
// and what alternatives were rejected.
type CharacterDecisionFrame struct {
	AvailableOptions        []string `json:"available_options,omitempty"`
	RejectedOptions         []string `json:"rejected_options,omitempty"`
	DecisionRule            string   `json:"decision_rule,omitempty"`
	Tradeoff                string   `json:"tradeoff,omitempty"`
	CostPaid                string   `json:"cost_paid,omitempty"`
	RiskAccepted            string   `json:"risk_accepted,omitempty"`
	ExpectedGain            string   `json:"expected_gain,omitempty"`
	MinimumEvidenceRequired string   `json:"minimum_evidence_required,omitempty"`
}

// CharacterRelationshipContract turns relationship pressure into auditable
// trust, debt, leverage, promise, and betrayal thresholds.
type CharacterRelationshipContract struct {
	Counterpart       string `json:"counterpart,omitempty"`
	Trust             string `json:"trust,omitempty"`
	Debt              string `json:"debt,omitempty"`
	Leverage          string `json:"leverage,omitempty"`
	Promise           string `json:"promise,omitempty"`
	SharedSecret      string `json:"shared_secret,omitempty"`
	BetrayalRecord    string `json:"betrayal_record,omitempty"`
	Dependency        string `json:"dependency,omitempty"`
	FearSource        string `json:"fear_source,omitempty"`
	AllianceStatus    string `json:"alliance_status,omitempty"`
	BetrayalThreshold string `json:"betrayal_threshold,omitempty"`
	HelpCondition     string `json:"help_condition,omitempty"`
	SourceChapter     int    `json:"source_chapter,omitempty"`
}

// CharacterEmotionAppraisal explains what caused an emotion and how it changes
// visible behavior, rather than storing emotion as a static tag.
type CharacterEmotionAppraisal struct {
	TriggerEvent         string `json:"trigger_event,omitempty"`
	GoalImpact           string `json:"goal_impact,omitempty"`
	ThreatToValue        string `json:"threat_to_value,omitempty"`
	VisibleExpression    string `json:"visible_expression,omitempty"`
	SuppressedExpression string `json:"suppressed_expression,omitempty"`
	CopingStrategy       string `json:"coping_strategy,omitempty"`
	ActionPressure       string `json:"action_pressure,omitempty"`
	RelationshipEffect   string `json:"relationship_effect,omitempty"`
}

// CharacterArcAxis records the longer-lived inner change track behind chapter
// actions and state changes.
type CharacterArcAxis struct {
	Want             string `json:"want,omitempty"`
	Need             string `json:"need,omitempty"`
	WoundOrGhost     string `json:"wound_or_ghost,omitempty"`
	CoreLie          string `json:"core_lie,omitempty"`
	ValueAxis        string `json:"value_axis,omitempty"`
	ArcStage         string `json:"arc_stage,omitempty"`
	PressureTest     string `json:"pressure_test,omitempty"`
	GrowthSignal     string `json:"growth_signal,omitempty"`
	RegressionSignal string `json:"regression_signal,omitempty"`
}

// CharacterDynamicsProfile treats a character as a changing decision system,
// not just a static persona tag. It is derived from character cards, summaries,
// state changes, relationship ledger, resources, and future outline uses.
type CharacterDynamicsProfile struct {
	CurrentGoal          string                          `json:"current_goal,omitempty"`
	PrimaryPressure      string                          `json:"primary_pressure,omitempty"`
	Resources            []string                        `json:"resources,omitempty"`
	RelationshipForces   []string                        `json:"relationship_forces,omitempty"`
	Secrets              []string                        `json:"secrets,omitempty"`
	Misbeliefs           []string                        `json:"misbeliefs,omitempty"`
	ActionBias           string                          `json:"action_bias,omitempty"`
	RiskPressure         string                          `json:"risk_pressure,omitempty"`
	EmotionalState       string                          `json:"emotional_state,omitempty"`
	PhysicalState        string                          `json:"physical_state,omitempty"`
	ExposureLevel        string                          `json:"exposure_level,omitempty"`
	NextLikelyAction     string                          `json:"next_likely_action,omitempty"`
	ConflictVector       string                          `json:"conflict_vector,omitempty"`
	KnowledgeLedger      CharacterKnowledgeLedger        `json:"knowledge_ledger"`
	DecisionFrame        CharacterDecisionFrame          `json:"decision_frame"`
	RelationshipContract []CharacterRelationshipContract `json:"relationship_contract"`
	EmotionAppraisal     CharacterEmotionAppraisal       `json:"emotion_appraisal"`
	ArcAxis              CharacterArcAxis                `json:"arc_axis"`

	// Psych 从 Character.Psych 透传的定量心理画像（大五/依恋/价值观/偏差/能力/DNA），
	// 派生时不二次加工；缺失时消费方跳过。
	Psych *CharacterPsychProfile `json:"psych,omitempty"`
}

// CharacterReturnPlan explains whether and how a character should be reused.
type CharacterReturnPlan struct {
	ReturnPriority     string `json:"return_priority,omitempty"` // required / near_future / optional / dormant
	SuggestedChapter   int    `json:"suggested_chapter,omitempty"`
	DueReason          string `json:"due_reason,omitempty"`
	WithNewInformation string `json:"with_new_information,omitempty"`
	UpgradePotential   string `json:"upgrade_potential,omitempty"`
	RetireReason       string `json:"retire_reason,omitempty"`
}

// CharacterContinuityEntry tracks whether a character is a core long-arc
// participant, an outline-backed return, an optional cameo candidate, or dormant.
type CharacterContinuityEntry struct {
	Name               string                   `json:"name"`
	Source             string                   `json:"source"` // characters / cast_ledger
	Role               string                   `json:"role,omitempty"`
	Tier               string                   `json:"tier,omitempty"`
	BriefRole          string                   `json:"brief_role,omitempty"`
	Aliases            []string                 `json:"aliases,omitempty"`
	FirstSeenChapter   int                      `json:"first_seen_chapter,omitempty"`
	LastSeenChapter    int                      `json:"last_seen_chapter,omitempty"`
	AppearanceCount    int                      `json:"appearance_count,omitempty"`
	AppearanceChapters []int                    `json:"appearance_chapters,omitempty"`
	CurrentFacts       []string                 `json:"current_facts,omitempty"`
	ArcDirection       string                   `json:"arc_direction,omitempty"`
	ReturnMode         string                   `json:"return_mode"`
	PlanningNote       string                   `json:"planning_note"`
	FutureUses         []CharacterFutureUse     `json:"future_uses,omitempty"`
	Dynamics           CharacterDynamicsProfile `json:"dynamics,omitempty"`
	ReturnPlan         CharacterReturnPlan      `json:"return_plan,omitempty"`
	ConsistencyChecks  []string                 `json:"consistency_checks,omitempty"`
}

// CharacterContinuityLedger is written to meta/character_continuity.json/md.
// It is regenerated after accepted chapter reviews and by --refresh-progress.
type CharacterContinuityLedger struct {
	Version           int                        `json:"version"`
	NovelName         string                     `json:"novel_name,omitempty"`
	GeneratedAt       string                     `json:"generated_at"`
	CurrentChapter    int                        `json:"current_chapter,omitempty"`
	CompletedChapters []int                      `json:"completed_chapters,omitempty"`
	ReviewPolicy      string                     `json:"review_policy"`
	Entries           []CharacterContinuityEntry `json:"entries"`
	NextChapterFocus  []CharacterHint            `json:"next_chapter_focus,omitempty"`
}

// ProjectProgressLedger is a higher-level planning dashboard rebuilt after
// accepted chapter reviews. ChapterProgressLedger answers "what changed in each
// chapter"; this ledger answers "is the whole project still moving coherently".
type ProjectProgressLedger struct {
	Version             int                        `json:"version"`
	NovelName           string                     `json:"novel_name,omitempty"`
	GeneratedAt         string                     `json:"generated_at"`
	CurrentChapter      int                        `json:"current_chapter,omitempty"`
	CompletedChapters   []int                      `json:"completed_chapters,omitempty"`
	TotalChapters       int                        `json:"total_chapters,omitempty"`
	DeliveryChapters    int                        `json:"delivery_chapters,omitempty"`
	CurrentVolume       int                        `json:"current_volume,omitempty"`
	CurrentArc          int                        `json:"current_arc,omitempty"`
	ScopeWarnings       []ProjectPlanningWarning   `json:"scope_warnings,omitempty"`
	OutlineStatus       []OutlineArcStatus         `json:"outline_status,omitempty"`
	PromiseEntries      []ChapterPromiseEntry      `json:"promise_entries,omitempty"`
	ProtagonistArc      []ProtagonistArcEntry      `json:"protagonist_arc,omitempty"`
	HookAnalysis        HookAnalysis               `json:"hook_analysis,omitempty"`
	ResourceHygiene     ResourceHygieneReport      `json:"resource_hygiene,omitempty"`
	ForeshadowPlan      []ForeshadowPlanningEntry  `json:"foreshadow_plan,omitempty"`
	RelationshipTension []RelationshipTensionEntry `json:"relationship_tension,omitempty"`
	AssetOperations     []AssetOperationEntry      `json:"asset_operations,omitempty"`
	NextChapterActions  []string                   `json:"next_chapter_actions,omitempty"`
}

type ProtagonistArcEntry struct {
	Chapter      int             `json:"chapter"`
	Title        string          `json:"title,omitempty"`
	Position     ChapterPosition `json:"position,omitempty"`
	Source       string          `json:"source"` // actual / planned / missing_outline
	Change       string          `json:"change,omitempty"`
	Driver       string          `json:"driver,omitempty"`
	Result       string          `json:"result,omitempty"`
	NextPressure string          `json:"next_pressure,omitempty"`
}

type ProjectPlanningWarning struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

type OutlineArcStatus struct {
	Volume            int    `json:"volume,omitempty"`
	VolumeTitle       string `json:"volume_title,omitempty"`
	Arc               int    `json:"arc,omitempty"`
	ArcTitle          string `json:"arc_title,omitempty"`
	Goal              string `json:"goal,omitempty"`
	Expanded          bool   `json:"expanded"`
	StartChapter      int    `json:"start_chapter,omitempty"`
	EndChapter        int    `json:"end_chapter,omitempty"`
	TotalChapters     int    `json:"total_chapters,omitempty"`
	CompletedChapters int    `json:"completed_chapters,omitempty"`
	EstimatedChapters int    `json:"estimated_chapters,omitempty"`
	Status            string `json:"status"`
}

type ChapterPromiseEntry struct {
	Chapter               int             `json:"chapter"`
	Title                 string          `json:"title,omitempty"`
	Position              ChapterPosition `json:"position,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	PromiseSignals        []string        `json:"promise_signals,omitempty"`
	MissingSignals        []string        `json:"missing_signals,omitempty"`
	HookType              string          `json:"hook_type,omitempty"`
	HookShape             string          `json:"hook_shape,omitempty"`
	HasAssetOrRiskChange  bool            `json:"has_asset_or_risk_change,omitempty"`
	HasRelationshipChange bool            `json:"has_relationship_change,omitempty"`
	HasTimelineEvent      bool            `json:"has_timeline_event,omitempty"`
	HasForeshadowSignal   bool            `json:"has_foreshadow_signal,omitempty"`
}

type HookAnalysis struct {
	RecentHookTypes []string       `json:"recent_hook_types,omitempty"`
	HookTypeCounts  map[string]int `json:"hook_type_counts,omitempty"`
	RecentShapes    []string       `json:"recent_shapes,omitempty"`
	Warnings        []string       `json:"warnings,omitempty"`
}

type ResourceHygieneReport struct {
	PendingCount    int             `json:"pending_count,omitempty"`
	StalePending    []ResourceClaim `json:"stale_pending,omitempty"`
	DuplicateLikely []string        `json:"duplicate_likely,omitempty"`
	Actions         []string        `json:"actions,omitempty"`
}

type ForeshadowPlanningEntry struct {
	ID                       string `json:"id"`
	Description              string `json:"description,omitempty"`
	Status                   string `json:"status,omitempty"`
	PlantedAt                int    `json:"planted_at,omitempty"`
	AgeChapters              int    `json:"age_chapters,omitempty"`
	Priority                 string `json:"priority,omitempty"`
	PayoffType               string `json:"payoff_type,omitempty"`
	SuggestedDeadlineChapter int    `json:"suggested_deadline_chapter,omitempty"`
	Action                   string `json:"action,omitempty"`
}

type RelationshipTensionEntry struct {
	Pair            string `json:"pair"`
	Chapter         int    `json:"chapter,omitempty"`
	CurrentRelation string `json:"current_relation,omitempty"`
	NextNeed        string `json:"next_need,omitempty"`
	AvoidRepeat     string `json:"avoid_repeat,omitempty"`
}

type AssetOperationEntry struct {
	Name          string `json:"name"`
	Owner         string `json:"owner,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
	LastChapter   int    `json:"last_chapter,omitempty"`
	CurrentRisk   string `json:"current_risk,omitempty"`
	OperationNeed string `json:"operation_need,omitempty"`
	NextTrigger   string `json:"next_trigger,omitempty"`
}

// EvolutionReport is an auditable self-improvement report. It never mutates
// prompts, rules, or code by itself; it records diagnosed patterns and proposed
// changes that can be reviewed or promoted by a separate controlled step.
type EvolutionReport struct {
	Version           int                  `json:"version"`
	NovelName         string               `json:"novel_name,omitempty"`
	GeneratedAt       string               `json:"generated_at"`
	CurrentChapter    int                  `json:"current_chapter,omitempty"`
	CompletedChapters []int                `json:"completed_chapters,omitempty"`
	WindowChapters    []int                `json:"window_chapters,omitempty"`
	Health            EvolutionHealth      `json:"health"`
	Patterns          []EvolutionPattern   `json:"patterns,omitempty"`
	Candidates        []EvolutionCandidate `json:"candidates,omitempty"`
	Guardrails        []string             `json:"guardrails,omitempty"`
	VerificationPlan  []string             `json:"verification_plan,omitempty"`
	SourceArtifacts   []string             `json:"source_artifacts,omitempty"`
}

type EvolutionHealth struct {
	Status              string  `json:"status"` // stable / watch / intervene
	Score               int     `json:"score"`
	AcceptedReviewed    int     `json:"accepted_reviewed"`
	Completed           int     `json:"completed"`
	RecentAIVoiceScore  float64 `json:"recent_ai_voice_score,omitempty"`
	RecentDialogueRatio float64 `json:"recent_dialogue_ratio,omitempty"`
	PendingRewriteCount int     `json:"pending_rewrite_count,omitempty"`
	WarningCount        int     `json:"warning_count,omitempty"`
}

type EvolutionPattern struct {
	ID             string   `json:"id"`
	Category       string   `json:"category"` // prompt / lint / outline / memory / review / project
	Severity       string   `json:"severity"` // info / watch / action
	Chapters       []int    `json:"chapters,omitempty"`
	Evidence       []string `json:"evidence,omitempty"`
	Diagnosis      string   `json:"diagnosis"`
	RecommendedFix string   `json:"recommended_fix"`
}

type EvolutionCandidate struct {
	ID               string   `json:"id"`
	Level            string   `json:"level"`  // L1 report / L2 book_rule / L3 prompt_lint / L4 code
	Target           string   `json:"target"` // artifact or subsystem
	Impact           string   `json:"impact,omitempty"`
	Status           string   `json:"status"` // proposed / adopted / rejected
	Change           string   `json:"change"`
	Rationale        string   `json:"rationale"`
	Risk             string   `json:"risk,omitempty"`
	Validation       []string `json:"validation,omitempty"`
	Verification     string   `json:"verification,omitempty"`
	PromotionTrigger string   `json:"promotion_trigger,omitempty"`
}
