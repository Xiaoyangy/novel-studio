package domain

// ChapterWorldSimulation is the prewriting source of truth for one chapter.
// The world advances first; the POV plan is derived from ProtagonistProjection.
type ChapterWorldSimulation struct {
	Version               int                           `json:"version"`
	SimulationID          string                        `json:"simulation_id"`
	Chapter               int                           `json:"chapter"`
	GenerationID          string                        `json:"generation_id,omitempty"`
	BaseTickID            string                        `json:"base_tick_id,omitempty"`
	TimeWindow            string                        `json:"time_window"`
	CharacterDecisions    []CharacterWorldDecision      `json:"character_decisions"`
	ProtagonistProjection ProtagonistDecisionProjection `json:"protagonist_projection"`
	RewriteSource         *ChapterRewriteSource         `json:"rewrite_source,omitempty"`
	RewriteFactCoverage   []ChapterRewriteFactCoverage  `json:"rewrite_fact_coverage,omitempty"`
	GeneratedAt           string                        `json:"generated_at,omitempty"`
	Sources               []string                      `json:"sources,omitempty"`
}

// ChapterRewriteSource pins a rewrite simulation to the exact committed body
// and rewrite brief it is allowed to revise. PreserveFacts are copied from the
// brief and become hard planning anchors; prose may change, these facts may not.
type ChapterRewriteSource struct {
	BodyPath      string   `json:"body_path"`
	BodySHA256    string   `json:"body_sha256"`
	WordCount     int      `json:"word_count"`
	BriefPath     string   `json:"brief_path"`
	BriefSHA256   string   `json:"brief_sha256"`
	PreserveFacts []string `json:"preserve_facts,omitempty"`
}

// ChapterRewriteFactCoverage makes the simulation account for every fact the
// rewrite brief protects instead of merely claiming that it read the brief.
type ChapterRewriteFactCoverage struct {
	Fact               string   `json:"fact"`
	SimulationEvidence []string `json:"simulation_evidence"`
}

// CharacterWorldDecision records one named character's autonomous choice in
// the shared timeline. A decision may be to wait, refuse, observe, or continue
// an existing action, but it still needs a reason and a downstream effect.
type CharacterWorldDecision struct {
	Character         string                    `json:"character"`
	Time              string                    `json:"time,omitempty"`
	Location          string                    `json:"location"`
	CurrentGoal       string                    `json:"current_goal"`
	Pressure          string                    `json:"pressure"`
	Resources         []string                  `json:"resources,omitempty"`
	KnowledgeBoundary string                    `json:"knowledge_boundary"`
	AvailableOptions  []string                  `json:"available_options"`
	Decision          string                    `json:"decision"`
	DecisionReason    string                    `json:"decision_reason"`
	Action            string                    `json:"action"`
	ActionDuration    string                    `json:"action_duration"`
	CompletionState   string                    `json:"completion_state"` // instant / started / in_progress / completed / blocked
	ImmediateResult   string                    `json:"immediate_result"`
	StateAfter        string                    `json:"state_after"`
	VisibleToPOV      bool                      `json:"visible_to_pov,omitempty"`
	ButterflyEffects  []DecisionButterflyEffect `json:"butterfly_effects"`
}

// DecisionButterflyEffect connects one character choice to later world or POV
// pressure. Hidden and delayed effects are first-class and must not leak into
// prose before their transmission path reaches the protagonist.
type DecisionButterflyEffect struct {
	Effect            string   `json:"effect"`
	Targets           []string `json:"targets,omitempty"`
	TransmissionPath  string   `json:"transmission_path"`
	ArrivalChapter    int      `json:"arrival_chapter"`
	Visibility        string   `json:"visibility"` // visible / delayed / hidden
	ProtagonistImpact string   `json:"protagonist_impact"`
}

// ProtagonistDecisionProjection is the only simulation slice a POV chapter
// plan may render directly.
type ProtagonistDecisionProjection struct {
	Protagonist       string   `json:"protagonist"`
	ObservableEffects []string `json:"observable_effects"`
	HiddenPressures   []string `json:"hidden_pressures"`
	AvailableOptions  []string `json:"available_options"`
	ChosenDecision    string   `json:"chosen_decision"`
	DecisionReason    string   `json:"decision_reason"`
	PlanConstraints   []string `json:"plan_constraints"`
	CausalChain       []string `json:"causal_chain"`
}
