package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// draftRenderPacket is the deliberately small prose-facing view of a chapter
// plan. Planning data remains authoritative on disk, but the Drafter should not
// see turn-by-turn action choreography or hidden world state and mistake it for
// a checklist to transcribe.
type draftRenderPacket struct {
	Version                 int                              `json:"version"`
	Chapter                 int                              `json:"chapter"`
	Heading                 string                           `json:"heading"`
	WordBudget              *draftWordBudget                 `json:"word_budget,omitempty"`
	Title                   string                           `json:"title,omitempty"`
	Goal                    string                           `json:"goal,omitempty"`
	Conflict                string                           `json:"conflict,omitempty"`
	Hook                    string                           `json:"hook,omitempty"`
	EmotionArc              string                           `json:"emotion_arc,omitempty"`
	MandatoryBeats          []string                         `json:"mandatory_beats,omitempty"`
	PreserveFacts           []string                         `json:"preserve_facts,omitempty"`
	OptionalStyleBeats      []string                         `json:"soft_style_candidates,omitempty"`
	ForbiddenMoves          []string                         `json:"forbidden_moves,omitempty"`
	ContinuityChecks        []string                         `json:"continuity_checks,omitempty"`
	PayoffPoints            []string                         `json:"soft_payoff_directions,omitempty"`
	SceneAnchors            []string                         `json:"soft_scene_anchors,omitempty"`
	CandidateBeats          []draftCandidateBeat             `json:"candidate_beats,omitempty"`
	RevealBudget            []string                         `json:"reveal_budget,omitempty"`
	CutOrCompress           []string                         `json:"cut_or_compress,omitempty"`
	PageTurnQuestions       []string                         `json:"page_turn_questions,omitempty"`
	ProtagonistProjection   draftProtagonistProjection       `json:"protagonist_projection"`
	EntertainmentPlan       draftEntertainmentPlan           `json:"reader_entertainment_plan,omitempty"`
	TrendLanguagePlan       []draftTrendLanguagePlan         `json:"trend_language_plan,omitempty"`
	LongformOpening         draftLongformOpening             `json:"longform_opening,omitempty"`
	AntiAIExecutionPlan     draftAntiAIPlan                  `json:"anti_ai_execution_plan,omitempty"`
	VoiceCards              []draftVoiceCard                 `json:"voice_cards,omitempty"`
	VisualCards             []draftVisualCard                `json:"visual_cards,omitempty"`
	DialogueScenes          []draftDialogueScene             `json:"dialogue_scenes,omitempty"`
	EmotionalLenses         []draftEmotionalLens             `json:"emotional_lenses,omitempty"`
	RelationshipLenses      []draftRelationshipLens          `json:"relationship_lenses,omitempty"`
	LiteraryRenderContract  draftLiteraryRenderContract      `json:"literary_render_contract"`
	StyleContract           *draftStyleContract              `json:"style_contract,omitempty"`
	VisibleCharacters       []string                         `json:"visible_characters,omitempty"`
	ExcludedNamedCharacters []string                         `json:"excluded_named_characters,omitempty"`
	GroundingDetails        []domain.GroundingDetailPlan     `json:"grounding_details,omitempty"`
	CraftMethods            []draftCraftMethod               `json:"craft_methods,omitempty"`
	EnvironmentSignals      []draftEnvironmentSignal         `json:"environment_signals,omitempty"`
	EndingContract          domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
	EndingAnchorCandidate   string                           `json:"ending_anchor_candidate,omitempty"`
	HardContractPolicy      string                           `json:"hard_contract_policy"`
	SoftMaterialPolicy      string                           `json:"soft_material_policy"`
	SelectionPolicy         string                           `json:"selection_policy"`
	SceneBridgePolicy       string                           `json:"scene_bridge_policy"`
	DialogueTopologyPolicy  string                           `json:"dialogue_topology_policy"`
	SystemVoicePolicy       string                           `json:"system_voice_policy"`
	JargonPolicy            string                           `json:"jargon_policy"`
	PlanTranslationPolicy   string                           `json:"plan_translation_policy"`
	ReaderRegisterPolicy    string                           `json:"reader_register_policy"`
	InterfaceCompression    string                           `json:"interface_compression_policy"`
	ScenePurposePolicy      string                           `json:"scene_purpose_policy"`
	SpokenLanguagePolicy    string                           `json:"spoken_language_policy"`
	EmotionalRenderPolicy   string                           `json:"emotional_render_policy"`
	GroupCompressionPolicy  string                           `json:"group_compression_policy"`
	ChronologyPolicy        string                           `json:"chronology_policy"`
	ProofFocusPolicy        string                           `json:"proof_focus_policy"`
	NamedRolePolicy         string                           `json:"named_role_policy"`
	RelationshipPriority    string                           `json:"relationship_priority_policy"`
	CharacterEntrancePolicy string                           `json:"character_entrance_policy"`
}

// draftWordBudget turns the delivery boundary into a prose-facing writing
// target. The hard range is still enforced by draft_chapter/commit_chapter;
// the inner target leaves enough headroom for title and final connective text
// so a provider does not repeatedly submit a candidate one or two sentences
// over the exact upper bound.
type draftWordBudget struct {
	Unit          string `json:"unit"`
	HardMin       int    `json:"hard_min,omitempty"`
	HardMax       int    `json:"hard_max,omitempty"`
	TargetMin     int    `json:"submission_target_min,omitempty"`
	TargetMax     int    `json:"submission_target_max,omitempty"`
	ExactBoundary bool   `json:"exact_boundary"`
}

type draftEntertainmentPlan struct {
	OpeningBeat          string   `json:"opening_beat,omitempty"`
	HumorBeats           []string `json:"humor_beats,omitempty"`
	ImmediatePayoffs     []string `json:"immediate_payoffs,omitempty"`
	ProcedureCompression string   `json:"procedure_compression,omitempty"`
	CompanionVoiceBeat   string   `json:"companion_voice_beat,omitempty"`
}

type draftTrendLanguagePlan struct {
	Item             string `json:"item,omitempty"`
	CharacterCarrier string `json:"character_carrier,omitempty"`
	SceneFunction    string `json:"scene_function,omitempty"`
	UsageBudget      string `json:"usage_budget,omitempty"`
}

type draftLongformOpening struct {
	TargetReader      string   `json:"target_reader,omitempty"`
	OpeningHook       string   `json:"opening_hook,omitempty"`
	FirstChapterProof []string `json:"first_chapter_proof,omitempty"`
	RetentionRisks    []string `json:"retention_risks,omitempty"`
}

type draftAntiAIPlan struct {
	RiskSignals          []string `json:"risk_signals,omitempty"`
	CounterMoves         []string `json:"counter_moves,omitempty"`
	SentenceRhythmPolicy string   `json:"sentence_rhythm_policy,omitempty"`
	ObjectResponseBudget string   `json:"object_response_budget,omitempty"`
	DialogueFunctionPlan string   `json:"dialogue_function_plan,omitempty"`
	ReviewChecks         []string `json:"review_checks,omitempty"`
}

// draftCraftMethod is the prose-safe projection of a receipt-backed external
// reference. It contains the planner's transformed move and provenance, never
// the retrieved benchmark/craft text or summary.
type draftCraftMethod struct {
	ReceiptID          string   `json:"receipt_id"`
	Need               string   `json:"need"`
	Moves              []string `json:"candidate_moves"`
	TransformationRule string   `json:"transformation_rule"`
	Avoid              []string `json:"hard_avoid"`
	SourceRefs         []string `json:"source_refs"`
	UsagePolicy        string   `json:"usage_policy"`
}

type draftProtagonistProjection struct {
	Protagonist       string   `json:"protagonist,omitempty"`
	ObservableEffects []string `json:"observable_effects,omitempty"`
	AvailableOptions  []string `json:"available_options,omitempty"`
	ChosenDecision    string   `json:"chosen_decision,omitempty"`
	DecisionReason    string   `json:"decision_reason,omitempty"`
	PlanConstraints   []string `json:"plan_constraints,omitempty"`
	CausalChain       []string `json:"causal_chain,omitempty"`
}

type draftVoiceCard struct {
	Character          string   `json:"character,omitempty"`
	SpeechPrinciple    string   `json:"speech_principle,omitempty"`
	HiddenSubtext      string   `json:"hidden_subtext,omitempty"`
	KnowledgeBoundary  string   `json:"knowledge_boundary,omitempty"`
	RelationshipStance string   `json:"relationship_stance,omitempty"`
	DictionAndRhythm   string   `json:"diction_and_rhythm,omitempty"`
	TypicalMoves       []string `json:"typical_moves,omitempty"`
	ForbiddenMoves     []string `json:"forbidden_moves,omitempty"`
}

type draftCandidateBeat struct {
	Event         string `json:"event,omitempty"`
	ReaderPayoff  string `json:"reader_payoff,omitempty"`
	SceneVehicle  string `json:"scene_vehicle,omitempty"`
	FunctionShift string `json:"function_shift,omitempty"`
}

type draftVisualCard struct {
	Character       string `json:"character,omitempty"`
	FirstAppearance bool   `json:"first_appearance,omitempty"`
	Silhouette      string `json:"silhouette,omitempty"`
	FaceAndHair     string `json:"face_and_hair,omitempty"`
	ClothingStyle   string `json:"clothing_style,omitempty"`
	BodyLanguage    string `json:"body_language,omitempty"`
	SignatureObject string `json:"signature_object,omitempty"`
	FirstImpression string `json:"first_impression,omitempty"`
	StatusWear      string `json:"status_wear,omitempty"`
	SceneUse        string `json:"scene_use,omitempty"`
}

type draftDialogueScene struct {
	SceneID           string   `json:"scene_id,omitempty"`
	ScenePressure     string   `json:"scene_pressure,omitempty"`
	RelationshipFrame string   `json:"relationship_frame,omitempty"`
	Participants      []string `json:"participants,omitempty"`
	LocationAnchor    string   `json:"location_anchor,omitempty"`
	DialogueObjective string   `json:"dialogue_objective,omitempty"`
	ExitBeat          string   `json:"exit_beat,omitempty"`
	DoNotUse          []string `json:"do_not_use,omitempty"`
}

type draftEmotionalLens struct {
	Kind                        string   `json:"kind,omitempty"`
	Character                   string   `json:"character,omitempty"`
	ImmediateState              string   `json:"immediate_state,omitempty"`
	PrimaryEmotion              string   `json:"primary_emotion,omitempty"`
	Trigger                     string   `json:"trigger,omitempty"`
	GoalAppraisal               string   `json:"goal_appraisal,omitempty"`
	Regulation                  string   `json:"regulation,omitempty"`
	EmotionLedAction            string   `json:"emotion_led_action,omitempty"`
	SceneEvidence               []string `json:"scene_evidence,omitempty"`
	SubjectiveCausalTarget      string   `json:"subjective_causal_target,omitempty"`
	SubjectiveCausalRequirement string   `json:"subjective_causal_requirement,omitempty"`
	SubjectiveCausalReason      string   `json:"subjective_causal_reason,omitempty"`
	SubjectiveCausalHardAvoid   string   `json:"subjective_causal_hard_avoid,omitempty"`
}

type draftRelationshipLens struct {
	Pair              []string `json:"pair,omitempty"`
	CurrentBond       string   `json:"current_bond,omitempty"`
	EmotionalWant     string   `json:"emotional_want,omitempty"`
	Fear              string   `json:"fear,omitempty"`
	Boundary          string   `json:"boundary,omitempty"`
	NextEmotionalBeat string   `json:"next_emotional_beat,omitempty"`
}

// draftLiteraryRenderContract is the compact, prose-facing projection of the
// chapter's literary choices. It carries decisions and provenance, not the
// full research reference or planning dossier.
type draftLiteraryRenderContract struct {
	Source                string                    `json:"source"`
	Focalizer             string                    `json:"focalizer"`
	NarrativeAccess       string                    `json:"narrative_access"`
	KnowledgeBoundary     string                    `json:"knowledge_boundary"`
	PerceptualBias        string                    `json:"soft_perceptual_bias,omitempty"`
	SceneModes            []draftLiterarySceneMode  `json:"soft_scene_choices,omitempty"`
	ActiveLenses          []draftLiteraryActiveLens `json:"soft_lens_choices,omitempty"`
	SummaryOmissionPolicy string                    `json:"summary_omission_policy,omitempty"`
	Afterimage            string                    `json:"soft_afterimage_candidate,omitempty"`
	SourceRefs            []string                  `json:"source_refs"`
	UsagePolicy           string                    `json:"usage_policy"`
}

type draftLiterarySceneMode struct {
	Target      string `json:"target"`
	Mode        string `json:"mode"`
	Distance    string `json:"distance"`
	StateChange string `json:"state_change"`
	RenderMove  string `json:"render_move"`
}

type draftLiteraryActiveLens struct {
	Kind       string   `json:"kind"`
	Target     string   `json:"target"`
	Move       string   `json:"move"`
	Why        string   `json:"why"`
	Avoid      string   `json:"avoid"`
	SourceRefs []string `json:"source_refs"`
}

type draftEnvironmentSignal struct {
	Place              string `json:"place,omitempty"`
	VisibleState       string `json:"visible_state,omitempty"`
	InformationCarried string `json:"information_carried,omitempty"`
	PressureApplied    string `json:"pressure_applied,omitempty"`
}

func applyChapterContextProfile(result map[string]any, profile string) {
	switch profile {
	case "world_simulation":
		applyWorldSimulationContextProfile(result)
	case "planning":
		applyPlanningContextProfile(result)
	case "draft":
		applyDraftContextProfile(result)
	}
}

func applyWorldSimulationContextProfile(result map[string]any) {
	status := chapterWorldSimulationContextStatus(result)
	working, _ := result["working_memory"].(map[string]any)
	hasRewriteSource := hasContextKey(result, "rewrite_source")
	if status == "ready" || status == "ready_to_finalize" {
		// A finalized simulation (or a gap-free partial that only needs the
		// atomic finalize call) has already passed the character-authority
		// validator. Replaying every dossier and every off-screen decision here
		// is both redundant and dangerous: it can overflow the focused context
		// and invite a model to resubmit facts that are now locked.
		sanitizePlanningWorldSimulation(result)
		compactSimulationAuthorityForProfile(result, status)
	} else {
		compactWorldSimulationAuthority(result)
	}
	// The exact old-body facts needed by rewrite_source_only actors are already
	// embedded in their guarded contracts. Preserve facts, brief requirements and
	// source hashes remain visible below, so the duplicate full chapter/markdown
	// blobs add anchoring and budget pressure without adding authority.
	if hasRewriteSource {
		compactWorldRewriteEvidence(result)
		compactWorldRewriteBrief(result)
		if working != nil {
			compactWorldRewriteEvidence(working)
			compactWorldRewriteBrief(working)
		}
		sanitizeWorldExternalReview(result)
	}
	// The simulator needs every character's current state and the shared world,
	// but not prose craft, prior render packets or project-level progress reports.
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "side_character_journeys", "future_outline_window",
		"recent_summaries", "previous_tail", "references", "writing_engine",
		"literary_rendering_cards", "genre_style_profile", "style_rules", "style_stats", "voice_samples", "rag_recall", "retrieval_trace",
		"chapter_plan", "chapter_contract", "causal_simulation", "next_plan", "character_dossiers",
	} {
		deleteContextKey(result, key)
	}
	if status == "ready" || status == "ready_to_finalize" {
		result["world_simulation_context_policy"] = "当前 simulation 已正式 ready，或 gaps 已清零只待 finalize；全量 authority/character_decisions 已折叠为校验收据，禁止重发或重建。按 chapter_world_simulation.planning_policy 执行唯一下一步。"
	} else {
		result["world_simulation_context_policy"] = "simulation_character_authority.entries 与 simulation_characters 一一对应，是全角色身份、状态和未知边界的权威入口；公共规则在 mode_policies，blocking 角色的 exact contract 仍在各 entry。完整 dossiers、写法资料、正文渲染历史与重复项目报告已隐藏，禁止补猜。"
	}
}

func applyPlanningContextProfile(result map[string]any) {
	working, _ := result["working_memory"].(map[string]any)
	// Capture the typed, source-bound formal plan before any profile compaction.
	// Staged repair contexts deliberately hide chapter_plan, so this remains nil
	// there and the staged plan_structure/plan_details protocol is unchanged.
	formalPlan := chapterPlanFromContext(result, working)
	// Once the world simulation is finalized, the POV planner consumes its
	// protagonist projection. Full off-screen decisions stay durable on disk and
	// must not be copied into the POV plan or paid for again in every planning turn.
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "character_stage_records", "side_character_journeys",
		"premise", "world_rules", "recent_summaries",
	} {
		deleteContextKey(result, key)
	}
	status := chapterWorldSimulationContextStatus(result)
	sanitizePlanningWorldSimulation(result)
	// Planning never consumes the full dossier-authority packet. Before the
	// simulation is ready, plan_structure is mechanically blocked and the full
	// packet remains available through profile=world_simulation. Once ready, the
	// compact receipt proves that the authority check already succeeded.
	compactSimulationAuthorityForProfile(result, status)
	if status != "" {
		compactPlanningRewriteEvidence(result)
		if working != nil {
			compactPlanningRewriteEvidence(working)
		}
	}
	if status == "ready" && formalPlan != nil {
		compactFinalizedPlanningContext(result, working, formalPlan)
	}
	result["planning_context_policy"] = "全角色决定和权威档案保留在正式 world simulation 中；本阶段只依据精确 protagonist_projection、逐条 preserve_facts/coverage receipt、结构化 rewrite_brief 与带来源的 RAG craft receipt 生成主视角 plan。simulation 未 ready 时 plan_structure 会机械拒绝。"
}

func applyDraftContextProfile(result map[string]any) {
	working, _ := result["working_memory"].(map[string]any)
	// Capture the rewrite authority before turning the source into a receipt.
	// The formal plan may contain typography-only copies added by later repair
	// rounds; the committed rewrite source is the preferred spelling of each
	// protected fact.
	sourcePreserveFacts := draftRewriteSourcePreserveFacts(result, working)
	// Drafter consumes only the finalized protagonist projection. The full
	// all-character authority roster was validated before plan finalization and
	// remains on disk; retain its source-bound receipt instead of replaying it.
	compactSimulationAuthorityForProfile(result, chapterWorldSimulationContextStatus(result))
	compactDraftRewriteSource(result)
	compactDraftRewriteBrief(result)
	if working != nil {
		compactDraftRewriteSource(working)
		compactDraftRewriteBrief(working)
	}
	sanitizeDraftExternalReview(result)
	plan := chapterPlanFromContext(result, working)
	if plan != nil {
		packet := newDraftRenderPacket(*plan)
		packet.PreserveFacts = canonicalDraftPreserveFacts(sourcePreserveFacts, packet.PreserveFacts)
		packet.WordBudget = draftWordBudgetFromContext(result)
		packet.StyleContract = newDraftStyleContract(result)
		if simulation, ok := result["chapter_world_simulation"].(map[string]any); ok {
			if projection, ok := draftProjectionFromAny(simulation["protagonist_projection"]); ok {
				packet.ProtagonistProjection = leanDraftProjection(projection)
			}
			packet.VisibleCharacters, packet.ExcludedNamedCharacters = draftVisibilityFromSimulation(simulation)
		}
		// Keep exactly one prose contract. working_memory is the canonical
		// chapter-stage container; mirroring the full packet at the root made
		// non-Codex providers pay for every hard fact and policy twice whenever
		// the overall context was still below the generic trimming threshold.
		if working != nil {
			working["render_packet"] = packet
			delete(result, "render_packet")
		} else {
			result["render_packet"] = packet
		}
		hasPlanBinding := strings.TrimSpace(plan.CausalSimulation.WorldSimulationID) != "" || len(compactStrings(plan.CausalSimulation.ContextSources)) > 0
		if hasPlanBinding {
			receipt := finalizedPlanReceipt(*plan)
			enrichDraftAuthorityReceipt(receipt, result, working, packet)
			result["formal_plan_receipt"] = receipt
			compactDraftRewriteAuthority(result, "formal_plan_receipt.rewrite_source", draftRenderPacketAuthorityPath(working))
			if working != nil {
				compactDraftRewriteAuthority(working, "formal_plan_receipt.rewrite_source", draftRenderPacketAuthorityPath(working))
			}
		}

		leanPlan := map[string]any{
			"chapter":       plan.Chapter,
			"title":         plan.Title,
			"goal":          plan.Goal,
			"conflict":      plan.Conflict,
			"hook":          plan.Hook,
			"status":        "finalized",
			"render_policy": "本对象只用于确认章号与目标；完整正式 plan 由 receipt 绑定，章节合同已投影到 render_packet，正文不得展开旧 contract 或 causal_simulation。",
		}
		if hasPlanBinding {
			leanPlan["formal_plan_receipt"] = "top-level formal_plan_receipt"
		}
		result["chapter_plan"] = leanPlan
		if working != nil {
			working["chapter_plan"] = leanPlan
		}
		// render_packet is the draft-stage canonical contract. Keeping the original
		// chapter_contract beside it pays for the same mandatory beats, exclusions
		// and continuity clauses a second time and tempts agents to serialize the
		// planning checklist into prose.
		deleteContextKey(result, "chapter_contract")
		if len(packet.CraftMethods) > 0 {
			deleteContextKey(result, "rewrite_craft_pack")
			deleteContextKey(result, "rewrite_craft_status")
		}
	}

	for _, key := range []string{"causal_simulation", "causal_simulation_policy"} {
		delete(result, key)
		if working != nil {
			delete(working, key)
		}
	}
	// The prose-facing repair/style contract above is the only draft-stage view
	// of writing assets and AI-voice diagnostics. Keeping raw reports here makes
	// non-Codex providers see advisory flags and full metric dossiers that Codex's
	// final-prose whitelist removes, creating provider-dependent rewrite loops.
	for _, key := range []string{
		"ai_voice_redflags", "ai_voice_redflags_policy", "chapter_ai_voice_metrics",
		"genre_style_profile", "literary_rendering_cards", "writing_engine", "style_rules",
	} {
		deleteContextKey(result, key)
	}
	hasDraftAuthorityReceipt := plan != nil && (strings.TrimSpace(plan.CausalSimulation.WorldSimulationID) != "" || len(compactStrings(plan.CausalSimulation.ContextSources)) > 0)
	sanitizeDraftWorldSimulation(result, plan != nil, hasDraftAuthorityReceipt, draftRenderPacketAuthorityPath(working))
	if plan != nil {
		compactFinalizedContextBackground(result)
	}
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "character_stage_records", "side_character_journeys",
		"premise", "world_rules", "characters", "character_dossiers",
		"future_outline_window", "next_chapter_outline",
	} {
		deleteContextKey(result, key)
	}
}

// compactFinalizedPlanningContext turns an already-finalized formal plan into
// the same prose-safe projection consumed by Drafter. The complete plan remains
// authoritative at drafts/NN.plan.json and is bound here by a canonical digest;
// replaying its 50-100 KiB causal dossier cannot improve it and can accidentally
// turn plan_structure into an unrequested second planning pass.
func compactFinalizedPlanningContext(result map[string]any, working map[string]any, plan *domain.ChapterPlan) {
	if result == nil || plan == nil {
		return
	}
	sourcePreserveFacts := draftRewriteSourcePreserveFacts(result, working)
	packet := newDraftRenderPacket(*plan)
	packet.PreserveFacts = canonicalDraftPreserveFacts(sourcePreserveFacts, packet.PreserveFacts)
	packet.StyleContract = newDraftStyleContract(result)
	if simulation, ok := result["chapter_world_simulation"].(map[string]any); ok {
		if projection, ok := draftProjectionFromAny(simulation["protagonist_projection"]); ok {
			packet.ProtagonistProjection = leanDraftProjection(projection)
		}
		packet.VisibleCharacters, packet.ExcludedNamedCharacters = draftVisibilityFromSimulation(simulation)
	}
	// Keep one canonical copy. working_memory is the existing chapter-stage
	// authority container and survives the generic mirror de-duplicator.
	if working != nil {
		working["render_packet"] = packet
	} else {
		result["render_packet"] = packet
	}

	receipt := finalizedPlanReceipt(*plan)
	enrichDraftAuthorityReceipt(receipt, result, working, packet)
	result["formal_plan_receipt"] = receipt
	leanPlan := map[string]any{
		"chapter":             plan.Chapter,
		"title":               plan.Title,
		"goal":                plan.Goal,
		"conflict":            plan.Conflict,
		"hook":                plan.Hook,
		"status":              "finalized",
		"formal_plan_receipt": "top-level formal_plan_receipt",
		"render_policy":       "完整正式 plan 已由 receipt 绑定；hard contract、场景化 RAG craft moves 与 POV 投影在 render_packet，禁止重跑或扩写 causal_simulation。",
	}
	result["chapter_plan"] = leanPlan
	if working != nil {
		working["chapter_plan"] = leanPlan
	}
	deleteContextKey(result, "chapter_contract")
	deleteContextKey(result, "causal_simulation")
	deleteContextKey(result, "causal_simulation_policy")

	compactDraftRewriteSource(result)
	compactDraftRewriteBrief(result)
	if working != nil {
		compactDraftRewriteSource(working)
		compactDraftRewriteBrief(working)
	}
	compactDraftRewriteAuthority(result, "formal_plan_receipt.rewrite_source", draftRenderPacketAuthorityPath(working))
	if working != nil {
		compactDraftRewriteAuthority(working, "formal_plan_receipt.rewrite_source", draftRenderPacketAuthorityPath(working))
	}
	sanitizeDraftExternalReview(result)
	sanitizeDraftWorldSimulation(result, true, true, draftRenderPacketAuthorityPath(working))
	// The transformed craft methods and their receipt/source refs now live in
	// render_packet. Keep the raw recall pack only if no formal method was
	// actually consumed, so a malformed legacy plan fails visibly downstream.
	if len(packet.CraftMethods) > 0 {
		deleteContextKey(result, "rewrite_craft_pack")
		deleteContextKey(result, "rewrite_craft_status")
	}
	for _, key := range []string{
		"ai_voice_redflags", "ai_voice_redflags_policy", "chapter_ai_voice_metrics",
		"genre_style_profile", "literary_rendering_cards", "writing_engine", "style_rules",
	} {
		deleteContextKey(result, key)
	}
	compactFinalizedContextBackground(result)
}

func finalizedPlanReceipt(plan domain.ChapterPlan) map[string]any {
	encoded, _ := json.Marshal(plan)
	digest := sha256.Sum256(encoded)
	receipt := map[string]any{
		"status":                   "finalized_source_bound",
		"chapter":                  plan.Chapter,
		"artifact":                 fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
		"canonical_content_sha256": fmt.Sprintf("%x", digest[:]),
		"world_simulation_id":      strings.TrimSpace(plan.CausalSimulation.WorldSimulationID),
		"protagonist_decision":     strings.TrimSpace(plan.CausalSimulation.ProtagonistDecision),
		"context_sources":          compactStrings(plan.CausalSimulation.ContextSources),
		"policy":                   "artifact+digest 绑定完整 plan；context_sources 绑定 rewrite/instruction/world/craft 消费链。",
	}
	return receipt
}

func draftRenderPacketAuthorityPath(working map[string]any) string {
	if working != nil {
		return "working_memory.render_packet"
	}
	return "render_packet"
}

func canonicalDraftPreserveFacts(authoritative, planned []string) []string {
	return canonicalPreserveFacts(authoritative, planned)
}

func draftRewriteSourcePreserveFacts(result, working map[string]any) []string {
	for _, container := range []map[string]any{working, result} {
		if container == nil {
			continue
		}
		source, ok := container["rewrite_source"].(map[string]any)
		if !ok || source == nil {
			continue
		}
		if chapter, ok := chapterRewriteSourceFromAny(source["chapter"]); ok && len(chapter.PreserveFacts) > 0 {
			return canonicalPreserveFacts(chapter.PreserveFacts, nil)
		}
	}
	return nil
}

func chapterRewriteSourceFromAny(raw any) (domain.ChapterRewriteSource, bool) {
	switch value := raw.(type) {
	case *domain.ChapterRewriteSource:
		if value == nil {
			return domain.ChapterRewriteSource{}, false
		}
		return *value, true
	case domain.ChapterRewriteSource:
		return value, true
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return domain.ChapterRewriteSource{}, false
		}
		var source domain.ChapterRewriteSource
		if err := json.Unmarshal(encoded, &source); err != nil {
			return domain.ChapterRewriteSource{}, false
		}
		return source, true
	}
}

func draftFactListSHA256(facts []string) string {
	encoded, _ := json.Marshal(facts)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

// enrichDraftAuthorityReceipt binds the single prose-facing hard contract to
// every durable upstream artifact. Full preserve facts live only in
// render_packet; rewrite_source and world-simulation coverage become
// path+digest receipts so the same 4-8 KiB list is not replayed three times.
func enrichDraftAuthorityReceipt(receipt map[string]any, result, working map[string]any, packet draftRenderPacket) {
	if receipt == nil {
		return
	}
	packetRaw, _ := json.Marshal(packet)
	packetDigest := sha256.Sum256(packetRaw)
	receipt["render_contract"] = map[string]any{
		"authority_path":           draftRenderPacketAuthorityPath(working),
		"canonical_content_sha256": fmt.Sprintf("%x", packetDigest[:]),
		"version":                  packet.Version,
		"preserve_fact_count":      len(packet.PreserveFacts),
		"preserve_facts_sha256":    draftFactListSHA256(packet.PreserveFacts),
		"policy":                   "此路径是 Drafter 唯一完整硬事实投影；receipt 只证明来源和完整性，不是第二份事实文本。",
	}

	for _, container := range []map[string]any{working, result} {
		if container == nil {
			continue
		}
		source, ok := container["rewrite_source"].(map[string]any)
		if !ok || source == nil {
			continue
		}
		chapter, ok := chapterRewriteSourceFromAny(source["chapter"])
		if !ok {
			continue
		}
		binding := compactRewriteSourceBinding(chapter)
		facts := canonicalPreserveFacts(chapter.PreserveFacts, nil)
		binding["preserve_fact_count"] = len(facts)
		binding["preserve_facts_sha256"] = draftFactListSHA256(facts)
		binding["fact_authority"] = draftRenderPacketAuthorityPath(working) + ".preserve_facts"
		receipt["rewrite_source"] = binding
		break
	}

	sim, _ := result["chapter_world_simulation"].(map[string]any)
	if sim == nil {
		return
	}
	coverage, exists := sim["rewrite_fact_coverage"]
	if !exists {
		return
	}
	coverageRaw, _ := json.Marshal(coverage)
	coverageDigest := sha256.Sum256(coverageRaw)
	coverageFacts := canonicalPreserveFacts(draftRewriteCoverageFacts(coverage), nil)
	receipt["rewrite_fact_coverage"] = map[string]any{
		"artifact":                 fmt.Sprintf("meta/chapter_simulations/%03d.json", packet.Chapter),
		"simulation_id":            sim["simulation_id"],
		"validation":               "finalized_and_source_bound",
		"canonical_content_sha256": fmt.Sprintf("%x", coverageDigest[:]),
		"fact_count":               len(coverageFacts),
		"facts_sha256":             draftFactListSHA256(coverageFacts),
		"fact_authority":           draftRenderPacketAuthorityPath(working) + ".preserve_facts",
	}
}

func draftRewriteCoverageFacts(raw any) []string {
	var coverage []domain.ChapterRewriteFactCoverage
	encoded, err := json.Marshal(raw)
	if err == nil && json.Unmarshal(encoded, &coverage) == nil {
		facts := make([]string, 0, len(coverage))
		for _, item := range coverage {
			facts = append(facts, item.Fact)
		}
		return facts
	}
	var receipts []map[string]any
	if err == nil && json.Unmarshal(encoded, &receipts) == nil {
		facts := make([]string, 0, len(receipts))
		for _, item := range receipts {
			facts = append(facts, strings.TrimSpace(fmt.Sprint(item["fact"])))
		}
		return facts
	}
	return nil
}

func compactDraftRewriteAuthority(container map[string]any, receiptPath, packetPath string) {
	if container == nil {
		return
	}
	source, ok := container["rewrite_source"].(map[string]any)
	if !ok || source == nil {
		return
	}
	policy, _ := source["source_body_policy"].(string)
	container["rewrite_source"] = map[string]any{
		"authority_receipt":        receiptPath,
		"preserve_facts_authority": packetPath + ".preserve_facts",
		"source_body_policy":       policy,
		"policy":                   "正文源、brief、canonical state、字数及 preserve fact count/SHA 均在 authority_receipt；此处不重复事实正文。",
	}
}

// These artifacts are planning inputs. Once a formal source-bound plan exists,
// replaying them beside the render projection duplicates world facts and method
// instructions. Current-chapter fact text lives once in render_packet; source,
// coverage and plan identity remain auditable through formal_plan_receipt, while
// the full chapter_pipeline_instruction stays visible as the user contract.
func compactFinalizedContextBackground(result map[string]any) {
	for _, key := range []string{
		"simulation_restart_policy", "simulation_restart_policy_note", "legacy_state_policy",
		"premise_sections", "premise_structure", "book_world", "book_world_context",
		"character_continuity", "character_dossiers", "characters",
		"world_foundation", "world_foundation_policy", "world_codex", "world_codex_policy",
		"volume_codex", "volume_codex_policy", "timeline",
		"moral_ceiling", "moral_ceiling_usage", "physics_axioms", "physics_axioms_usage",
		"story_calendar", "story_calendar_usage", "info_graph", "info_graph_usage",
		"social_mood", "social_mood_usage", "ritual_calendar", "ritual_calendar_usage",
		"cosmology", "cosmology_usage", "crowd_life", "crowd_life_usage",
		"ecological_map", "cultural_footnotes", "cultural_footnotes_usage",
		"horizon_events", "horizon_events_usage", "character_dossier_policy",
		"references", "retrieval_trace", "rag_recall",
	} {
		deleteContextKey(result, key)
	}
	delete(result, "reference_pack")
}

// compactDraftRewriteSource keeps the immutable source identity and preservation
// contract while hiding the old chapter body and the full review markdown. Those
// two prose blobs are required while planning a rewrite, but the draft stage must
// re-render from the approved packet instead of paraphrasing the previous surface.
func compactDraftRewriteSource(container map[string]any) {
	if container == nil {
		return
	}
	source, ok := container["rewrite_source"].(map[string]any)
	if !ok || source == nil {
		return
	}
	briefMarkdown, _ := source["brief_markdown"].(string)
	mergePlanningRewriteBriefSections(container, briefMarkdown)
	compact := map[string]any{
		"source_body_policy": "旧正文与完整评审原文只用于上游定事实，draft profile 已隐藏；按 render_packet 重新讲述，不得追忆或同义改写旧稿表面。",
	}
	for _, key := range []string{"chapter", "required_sources", "preservation_policy"} {
		if value, exists := source[key]; exists {
			compact[key] = value
		}
	}
	container["rewrite_source"] = compact
}

// compactWorldRewriteBrief keeps the current, actionable rewrite contract for
// a restarted simulator while dropping detector dossiers, quoted old-prose
// evidence and suggested replacement scenes. Blocking actors already carry
// their exact rewrite_source_only contracts in simulation authority entries.
func compactWorldRewriteBrief(container map[string]any) {
	if container == nil {
		return
	}
	brief, ok := container["rewrite_brief"].(map[string]any)
	if !ok || brief == nil {
		return
	}
	compact := map[string]any{
		"source_policy": "保留当前顶层硬栏目与全部问题本体；旧稿引文、示例修法、检测器指标 dossier 和 AI voice prose 已隐藏。完整 brief 由 rewrite_source.chapter.brief_path+brief_sha256 绑定。",
	}
	for _, key := range []string{
		"reason", "review_summary", "contract_misses", "human_acceptance_supplements", "human_acceptance_policy",
	} {
		if value, exists := brief[key]; exists {
			compact[key] = value
		}
	}
	for _, key := range []string{
		"current_conclusion", "user_requirements", "contract_miss_contract", "required_corrections",
		"whole_text_single_segment_gates", "acceptance_conditions",
	} {
		if values := canonicalPreserveFacts(nil, stringSliceFromAny(brief[key])); len(values) > 0 {
			compact[key] = values
		}
	}
	if issues := compactWorldRewriteIssues(brief["issues"]); len(issues) > 0 {
		compact["current_problems"] = issues
	}
	if gate := compactWorldMechanicalGateReceipt(brief["mechanical_gate"]); len(gate) > 0 {
		compact["mechanical_gate_receipt"] = gate
	}
	container["rewrite_brief"] = compact
}

func compactWorldRewriteIssues(raw any) []map[string]string {
	var issues []domain.ConsistencyIssue
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, &issues) != nil {
		return nil
	}
	out := make([]map[string]string, 0, len(issues))
	for _, issue := range issues {
		problem := strings.TrimSpace(issue.Description)
		if problem == "" {
			continue
		}
		out = append(out, map[string]string{
			"type":     strings.TrimSpace(issue.Type),
			"severity": strings.TrimSpace(issue.Severity),
			"problem":  problem,
		})
	}
	return out
}

func compactWorldMechanicalGateReceipt(raw any) map[string]any {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) == 0 || string(encoded) == "null" {
		return nil
	}
	digest := sha256.Sum256(encoded)
	receipt := map[string]any{
		"canonical_content_sha256": fmt.Sprintf("%x", digest[:]),
		"policy":                   "完整检测器 dossier 留在 source artifacts；world simulation 只执行 current required corrections，不按概率指标设计正文。",
	}
	var gate map[string]any
	if json.Unmarshal(encoded, &gate) != nil {
		return receipt
	}
	for _, key := range []string{"json_path", "markdown_path", "engine"} {
		if value, exists := gate[key]; exists {
			receipt[key] = value
		}
	}
	if rawViolations, exists := gate["rule_violations"]; exists {
		var violations []map[string]any
		violationJSON, _ := json.Marshal(rawViolations)
		if json.Unmarshal(violationJSON, &violations) == nil {
			compact := make([]map[string]any, 0, len(violations))
			for _, violation := range violations {
				item := map[string]any{}
				for _, key := range []string{"rule", "severity", "target", "limit"} {
					if value, ok := violation[key]; ok {
						item[key] = value
					}
				}
				if len(item) > 0 {
					compact = append(compact, item)
				}
			}
			if len(compact) > 0 {
				receipt["rule_contracts"] = compact
			}
		}
	}
	return receipt
}

func sanitizeWorldExternalReview(result map[string]any) {
	containers := []map[string]any{result}
	for _, sectionName := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		if section, ok := result[sectionName].(map[string]any); ok {
			containers = append(containers, section)
		}
	}
	for _, container := range containers {
		raw, exists := container["draft_external_ai_review"]
		if !exists {
			continue
		}
		encoded, err := json.Marshal(raw)
		var review map[string]any
		if err != nil || json.Unmarshal(encoded, &review) != nil {
			delete(container, "draft_external_ai_review")
			delete(container, "draft_external_ai_review_policy")
			continue
		}
		if blocking, _ := review["blocking"].(bool); !blocking {
			delete(container, "draft_external_ai_review")
			delete(container, "draft_external_ai_review_policy")
			continue
		}
		lean := map[string]any{
			"blocking": true,
			"policy":   "旧稿示例、证据引文和 revision plan 已隐藏；world simulation 执行 rewrite_brief.current required corrections 与逐角色 exact authority contracts。",
		}
		for _, key := range []string{"summary", "reasons"} {
			if value, ok := review[key]; ok {
				lean[key] = value
			}
		}
		container["draft_external_ai_review"] = lean
		container["draft_external_ai_review_policy"] = "blocking diagnosis receipt only"
	}
}

func sanitizeDraftExternalReview(result map[string]any) {
	containers := []map[string]any{result}
	for _, sectionName := range []string{"working_memory", "episodic_memory", "reference_pack", "selected_memory"} {
		if section, ok := result[sectionName].(map[string]any); ok {
			containers = append(containers, section)
		}
	}
	for _, container := range containers {
		raw, exists := container["draft_external_ai_review"]
		if !exists {
			delete(container, "draft_external_ai_review_policy")
			continue
		}
		var review map[string]any
		encoded, err := json.Marshal(raw)
		if err != nil || json.Unmarshal(encoded, &review) != nil {
			delete(container, "draft_external_ai_review")
			delete(container, "draft_external_ai_review_policy")
			continue
		}
		blocking, _ := review["blocking"].(bool)
		if !blocking {
			delete(container, "draft_external_ai_review")
			delete(container, "draft_external_ai_review_policy")
			continue
		}
		lean := map[string]any{
			"blocking":   true,
			"use_policy": "只吸收旧稿失败的可读性证据；示例场景、示例动作、示例台词与完整修改计划不是正文指令，不得照搬或换皮。",
		}
		for _, key := range []string{"summary", "reasons", "evidence"} {
			if value, ok := review[key]; ok {
				lean[key] = value
			}
		}
		container["draft_external_ai_review"] = lean
		container["draft_external_ai_review_policy"] = "仅保留 blocking 诊断的摘要、原因和证据；修改示例与逐项方案已跨 provider 删除。"
	}
}

func compactDraftRewriteBrief(container map[string]any) {
	if container == nil {
		return
	}
	brief, ok := container["rewrite_brief"].(map[string]any)
	if !ok || brief == nil {
		return
	}
	compact := map[string]any{
		"render_policy": "只吸收问题、人工硬约束和必须保留的结果；评审证据里的示例动作、示例台词与指标补丁不是剧情指令，禁止照搬或换皮。",
	}
	for _, key := range []string{
		"reason", "review_summary", "contract_misses", "human_acceptance_supplements", "human_acceptance_policy",
	} {
		if value, exists := brief[key]; exists {
			compact[key] = value
		}
	}
	if issues := compactDraftReviewIssues(brief["issues"], 3); len(issues) > 0 {
		compact["issues"] = issues
	}
	if rules := compactDraftAIVoiceRules(brief["ai_voice_redflags"], 4); len(rules) > 0 {
		compact["ai_voice_rules"] = rules
	}
	// Planning extracts these exact top-level bullets from the latest markdown.
	// They are the actionable brief contract, not verbose review evidence, so a
	// finalized-plan/draft projection keeps them in bounded form alongside the
	// brief path+sha256 stored in rewrite_source.
	for _, section := range []struct {
		key   string
		limit int
	}{
		{key: "current_conclusion", limit: 2},
		{key: "user_requirements", limit: 4},
		{key: "contract_miss_contract", limit: 4},
		{key: "required_corrections", limit: 8},
		{key: "whole_text_single_segment_gates", limit: 4},
		{key: "acceptance_conditions", limit: 6},
	} {
		if values := exactRewriteBriefItems(brief[section.key], section.limit); len(values) > 0 {
			compact[section.key] = values
		}
	}
	if _, ok := compact["source_policy"]; !ok {
		compact["source_policy"] = "actionable bullets are exact extracts from the latest rewrite brief; full markdown is bound by rewrite_source.chapter.brief_path+brief_sha256."
	}
	container["rewrite_brief"] = compact
}

func exactRewriteBriefItems(raw any, limit int) []string {
	if limit <= 0 {
		return nil
	}
	values := compactStrings(stringSliceFromAny(raw))
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func compactDraftReviewIssues(raw any, limit int) []map[string]string {
	if limit <= 0 || raw == nil {
		return nil
	}
	var issues []domain.ConsistencyIssue
	switch values := raw.(type) {
	case []domain.ConsistencyIssue:
		issues = values
	default:
		encoded, err := json.Marshal(values)
		if err != nil || json.Unmarshal(encoded, &issues) != nil {
			return nil
		}
	}
	out := make([]map[string]string, 0, min(limit, len(issues)))
	for _, issue := range issues {
		if len(out) >= limit {
			break
		}
		problem := firstRenderClause(issue.Description)
		if problem == "" {
			continue
		}
		out = append(out, map[string]string{
			"type":     strings.TrimSpace(issue.Type),
			"severity": strings.TrimSpace(issue.Severity),
			"problem":  problem,
		})
	}
	return out
}

func compactDraftAIVoiceRules(raw any, limit int) []string {
	if limit <= 0 || raw == nil {
		return nil
	}
	var analysis domain.AIVoiceAnalysis
	switch value := raw.(type) {
	case *domain.AIVoiceAnalysis:
		if value == nil {
			return nil
		}
		analysis = *value
	case domain.AIVoiceAnalysis:
		analysis = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil || json.Unmarshal(encoded, &analysis) != nil {
			return nil
		}
	}
	rules := make([]string, 0, min(limit, len(analysis.RedFlags)))
	for _, flag := range analysis.RedFlags {
		if len(rules) >= limit {
			break
		}
		rule := strings.TrimSpace(flag.Rule)
		if rule != "" && !domain.IsAdvisoryAIVoiceFlag(flag) {
			rules = append(rules, rule)
		}
	}
	return compactStrings(rules)
}

func sanitizePlanningWorldSimulation(result map[string]any) {
	sim, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok {
		return
	}
	status, _ := sim["status"].(string)
	if status != "ready" && status != "ready_to_finalize" {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{
		"status", "simulation_id", "base_tick_id", "time_window", "character_count",
		"characters_present", "protagonist_projection", "projection_seed",
		"rewrite_source", "rewrite_fact_coverage", "gaps", "policy", "projection_policy",
	} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	if coverage, exists := lean["rewrite_fact_coverage"]; exists {
		lean["rewrite_fact_coverage"] = compactPlanningRewriteFactCoverage(coverage)
		lean["rewrite_fact_coverage_policy"] = "fact 原文逐字保留；simulation_evidence 已由 finalize 校验并压缩为 count+sha256 收据，完整证据仍在 meta/chapter_simulations。不得把收据当作可改写事实。"
	}
	if status == "ready" {
		lean["planning_policy"] = "完整 character_decisions 已在 meta/chapter_simulations 落盘；POV plan 只能投影 protagonist_projection，不得重写隐藏角色决定。"
	} else {
		lean["planning_policy"] = "当前 partial 的 gaps 已清零；只调用 simulate_chapter_world(chapter=N, finalize=true)，不得重发决定、投影、coverage 或 sources，finalize 前禁止 plan_structure。"
	}
	result["chapter_world_simulation"] = lean
}

func chapterWorldSimulationContextStatus(result map[string]any) string {
	if result == nil {
		return ""
	}
	sim, _ := result["chapter_world_simulation"].(map[string]any)
	status, _ := sim["status"].(string)
	return strings.TrimSpace(status)
}

// compactSimulationAuthorityForProfile keeps an auditable receipt but removes
// the 50-100 KiB dossier packet from planning, or from a terminal simulation
// view where resubmission is forbidden. The validator remains fail-closed in
// simulate_chapter_world/plan_structure; this is a delivery compaction only.
func compactSimulationAuthorityForProfile(result map[string]any, simulationStatus string) {
	if result == nil {
		return
	}
	_, hasAuthority := result["simulation_character_authority"]
	_, hasAuthorityPolicy := result["simulation_character_authority_policy"]
	_, hasSimulation := result["chapter_world_simulation"]
	if !hasAuthority && !hasAuthorityPolicy && !hasSimulation {
		return
	}
	receipt := map[string]any{
		"simulation_status": simulationStatus,
		"policy":            "完整 simulation_character_authority 未删除，仍保存在项目角色档案与正式 world simulation 校验链中；planning 不得重建或覆盖角色决定。simulation 未 ready 时仅 profile=world_simulation 可读取全量 authority。",
	}
	if policy, ok := result["simulation_character_authority_policy"].(map[string]any); ok {
		for _, key := range []string{"required_count", "blocking_count"} {
			if value, exists := policy[key]; exists {
				receipt[key] = value
			}
		}
	}
	if sim, ok := result["chapter_world_simulation"].(map[string]any); ok {
		for _, key := range []string{"simulation_id", "character_count"} {
			if value, exists := sim[key]; exists {
				receipt[key] = value
			}
		}
		if _, exists := receipt["character_count"]; !exists {
			if names := stringSliceFromAny(sim["characters_present"]); len(names) > 0 {
				receipt["character_count"] = len(names)
			}
		}
		if simulationStatus == "ready_to_finalize" {
			receipt["validation"] = "gaps_clear_finalize_only"
		} else if simulationStatus == "ready" {
			receipt["validation"] = "finalized_and_source_bound"
		} else {
			receipt["validation"] = "not_ready_plan_blocked"
		}
	}
	delete(result, "simulation_characters")
	delete(result, "simulation_character_authority")
	delete(result, "simulation_character_authority_policy")
	result["simulation_authority_receipt"] = receipt
}

// compactWorldSimulationAuthority is a lossless-for-validation transport view
// of the authority roster. Repeated prose policy and unused future arc material
// are lifted out of each actor. Blocking actors keep the exact per-character
// contract accepted by the mechanical validator; authoritative actors keep the
// complete current-state/knowledge/decision inputs needed for simulation.
func compactWorldSimulationAuthority(result map[string]any) {
	if result == nil {
		return
	}
	authority, ok := result["simulation_character_authority"].([]simulationCharacterAuthority)
	if !ok || len(authority) == 0 {
		return
	}
	entries := make([]map[string]any, 0, len(authority))
	for _, item := range authority {
		entry := map[string]any{
			"character":                  item.Character,
			"authority_mode":             item.AuthorityMode,
			"simulation_status":          item.SimulationStatus,
			"blocking":                   item.Blocking,
			"visible_in_current_chapter": item.VisibleInCurrentChapter,
		}
		add := func(key string, value any, present bool) {
			if present {
				entry[key] = value
			}
		}
		add("role", item.Role, strings.TrimSpace(item.Role) != "")
		add("tier", item.Tier, strings.TrimSpace(item.Tier) != "")
		add("aliases", item.Aliases, len(item.Aliases) > 0)
		add("authority_sources", item.AuthoritySources, len(item.AuthoritySources) > 0)
		add("missing_authority", item.MissingAuthority, len(item.MissingAuthority) > 0)
		add("required_knowledge_boundaries", item.RequiredKnowledgeBoundary, len(item.RequiredKnowledgeBoundary) > 0)
		switch item.AuthorityMode {
		case simulationAuthorityHoldBaseline:
			// This is the exact validator payload. No dossier prose is useful once
			// the actor has been frozen to an unknown baseline.
			entry["hold_baseline_contract"] = item.HoldBaselineContract
		case "rewrite_source_only":
			entry["rewrite_source_evidence"] = item.RewriteSourceEvidence
			entry["rewrite_source_only_contract"] = item.RewriteSourceOnlyContract
		case "reuse_saved_decision":
			entry["locked_policy"] = "决定已在 partial 落盘；不得重发、改写或从旧正文重建。"
		case "authoritative":
			// Only current causal inputs belong here. Arc is deliberately omitted:
			// it is a future trajectory, not an authorized present fact.
			add("description", item.Description, strings.TrimSpace(item.Description) != "")
			add("traits", item.Traits, len(item.Traits) > 0)
			add("desires", item.Desires, len(item.Desires) > 0)
			add("boundaries", item.Boundaries, len(item.Boundaries) > 0)
			entry["current_location"] = item.CurrentLocation
			entry["current_status"] = item.CurrentStatus
			entry["current_action"] = item.CurrentAction
			entry["current_pressure"] = item.CurrentPressure
			entry["next_independent_move"] = item.NextIndependentMove
			add("resources", item.Resources, len(item.Resources) > 0)
			add("relationships", item.Relationships, len(item.Relationships) > 0)
			add("knowledge_boundary", item.KnowledgeBoundary, strings.TrimSpace(item.KnowledgeBoundary) != "")
			add("decision_model", item.DecisionModel, strings.TrimSpace(item.DecisionModel) != "")
			entry["communication_boundary"] = item.CommunicationBoundary
		default:
			// Unknown future modes fail safe by keeping their full original shape.
			encoded, err := json.Marshal(item)
			var full map[string]any
			if err != nil || json.Unmarshal(encoded, &full) != nil {
				return
			}
			entry = full
		}
		entries = append(entries, entry)
	}
	result["simulation_character_authority"] = map[string]any{
		"format": "layered_v1",
		"mode_policies": map[string]string{
			"authoritative":        "仅用 entry 中 current_*、desires/boundaries、resources、relationships、knowledge_boundary、required_knowledge_boundaries、decision_model 和通信边界推演；required_knowledge_boundaries 必须逐条原样进入提交的 knowledge_boundary，不得把未下发 arc 当当前事实。",
			"reuse_saved_decision": "该角色已落盘，禁止重发。",
			"hold_baseline":        "把角色实名放入 simulate_chapter_world.authority_contract_characters，由服务端物化 hold_baseline_contract；不得手抄或补职业、地点、关系、资源、通信、动机或未来行动。",
			"rewrite_source_only":  "把角色实名放入 simulate_chapter_world.authority_contract_characters，由服务端物化 rewrite_source_only_contract；不得手抄或改写 preserve_facts/rewrite_source_evidence。",
		},
		"entries": entries,
		"policy":  "entries 与 simulation_characters 一一对应；blocking 角色只提交 authority_contract_characters 名单，服务端生成 exact contract 并执行逐字段权威校验。",
	}
	if policy, ok := result["simulation_character_authority_policy"].(map[string]any); ok {
		policy["transport_format"] = "layered_v1"
		policy["transport_policy"] = "公共 mode policy 已提升到 simulation_character_authority.mode_policies；blocking 角色通过 authority_contract_characters 服务端物化，entry 保留 exact contract 仅供审计。"
	}
}

// compactWorldRewriteEvidence removes the old prose surface before a restarted
// world simulation. Exact preserve_facts remain in rewrite_source.chapter and
// in the simulator's fact gaps; blocking actors also retain their exact guarded
// contracts. The old body remains addressable by path+sha256.
func compactWorldRewriteEvidence(container map[string]any) {
	compactRewriteEvidence(container, "world_source_policy", "current_body 与 brief_markdown 已折叠；rewrite_source.chapter 的 path/sha256/preserve_facts、结构化 rewrite_brief、逐角色 exact authority contract 与逐条 fact gaps 是本轮世界推演权威。不得从旧稿表面补猜。")
}

// compactPlanningRewriteEvidence removes the same prose blobs after world
// simulation. Planning consumes the validated projection and coverage receipt
// instead of re-anchoring on an obsolete chapter surface.
func compactPlanningRewriteEvidence(container map[string]any) {
	compactRewriteEvidence(container, "planning_source_policy", "current_body 与 brief_markdown 已在 world simulation 通过后折叠；chapter 内的 path/sha256/preserve_facts、结构化 rewrite_brief、protagonist_projection 与 coverage receipt 是本轮规划事实。需要核对旧表面时按 body_path 调 read_chapter，不得凭记忆补写。")
}

func compactRewriteEvidence(container map[string]any, policyKey, policy string) {
	if container == nil {
		return
	}
	source, ok := container["rewrite_source"].(map[string]any)
	if !ok || source == nil {
		return
	}
	briefMarkdown, _ := source["brief_markdown"].(string)
	mergePlanningRewriteBriefSections(container, briefMarkdown)
	compact := map[string]any{
		policyKey: policy,
	}
	for _, key := range []string{"chapter", "required_sources", "preservation_policy"} {
		if value, exists := source[key]; exists {
			compact[key] = value
		}
	}
	container["rewrite_source"] = compact
}

func mergePlanningRewriteBriefSections(container map[string]any, markdown string) {
	if container == nil || strings.TrimSpace(markdown) == "" {
		return
	}
	brief, _ := container["rewrite_brief"].(map[string]any)
	hadBrief := brief != nil
	if brief == nil {
		brief = map[string]any{}
	}
	extracted := false
	sections := []struct {
		key      string
		headings []string
	}{
		{key: "current_conclusion", headings: []string{"当前结论"}},
		{key: "user_requirements", headings: []string{"用户本轮要求"}},
		{key: "contract_miss_contract", headings: []string{"合同漏项"}},
		{key: "required_corrections", headings: []string{"必须修正"}},
		{key: "whole_text_single_segment_gates", headings: []string{"最新整篇单段门禁"}},
		{key: "acceptance_conditions", headings: []string{"验收条件"}},
	}
	for _, section := range sections {
		items := rewriteBriefTopLevelBullets(markdown, section.headings...)
		if len(items) == 0 {
			continue
		}
		items = append(stringSliceFromAny(brief[section.key]), items...)
		brief[section.key] = compactStrings(items)
		extracted = true
	}
	if !hadBrief && !extracted {
		return
	}
	brief["source_policy"] = "本对象由当前 brief_markdown 的顶层合同确定性抽取；保留原句，不生成新剧情。完整 brief 由 rewrite_source.chapter.brief_path+brief_sha256 绑定。"
	container["rewrite_brief"] = brief
}

func compactPlanningRewriteFactCoverage(raw any) any {
	// The planning profile may already have folded evidence before a finalized
	// plan is projected for drafting. Preserve those receipts verbatim instead of
	// hashing an empty decoded simulation_evidence slice a second time.
	var receipts []map[string]any
	if encoded, err := json.Marshal(raw); err == nil && json.Unmarshal(encoded, &receipts) == nil && len(receipts) > 0 {
		alreadyCompact := true
		for _, receipt := range receipts {
			if strings.TrimSpace(fmt.Sprint(receipt["fact"])) == "" || receipt["evidence_count"] == nil || strings.TrimSpace(fmt.Sprint(receipt["evidence_sha256"])) == "" {
				alreadyCompact = false
				break
			}
			if _, verbose := receipt["simulation_evidence"]; verbose {
				alreadyCompact = false
				break
			}
		}
		if alreadyCompact {
			return receipts
		}
	}
	var coverage []domain.ChapterRewriteFactCoverage
	switch values := raw.(type) {
	case []domain.ChapterRewriteFactCoverage:
		coverage = values
	default:
		encoded, err := json.Marshal(values)
		if err != nil || json.Unmarshal(encoded, &coverage) != nil {
			// Fail safe: retain an unknown representation rather than silently
			// dropping a hard fact because an older store used another shape.
			return raw
		}
	}
	out := make([]map[string]any, 0, len(coverage))
	for _, item := range coverage {
		fact := strings.TrimSpace(item.Fact)
		if fact == "" {
			continue
		}
		evidenceJSON, _ := json.Marshal(item.SimulationEvidence)
		digest := sha256.Sum256(evidenceJSON)
		out = append(out, map[string]any{
			"fact":            fact,
			"evidence_count":  len(item.SimulationEvidence),
			"evidence_sha256": fmt.Sprintf("%x", digest[:]),
		})
	}
	return out
}

func chapterPlanFromContext(result map[string]any, working map[string]any) *domain.ChapterPlan {
	values := []any{result["chapter_plan"]}
	if working != nil {
		values = append(values, working["chapter_plan"])
	}
	for _, value := range values {
		switch plan := value.(type) {
		case *domain.ChapterPlan:
			return plan
		case domain.ChapterPlan:
			copy := plan
			return &copy
		}
	}
	return nil
}

func newDraftRenderPacket(plan domain.ChapterPlan) draftRenderPacket {
	sim := plan.CausalSimulation
	mandatoryBeats := RenderRequiredOutcomes(plan)
	softPayoffDirections, promotedPayoffFacts := splitHardRenderMaterials(plan.Contract.PayoffPoints)
	softSceneAnchors, promotedAnchorFacts := splitHardRenderMaterials(renderSceneAnchors(plan.Contract.SceneAnchors))
	continuityChecks := RenderContinuityChecks(plan)
	continuityChecks = compactStrings(append(continuityChecks, promotedPayoffFacts...))
	continuityChecks = compactStrings(append(continuityChecks, promotedAnchorFacts...))
	voices := make([]draftVoiceCard, 0, len(sim.VoiceLogic))
	for _, voice := range sim.VoiceLogic {
		voices = append(voices, draftVoiceCard{
			Character:         voice.Character,
			KnowledgeBoundary: firstRenderClause(voice.KnowledgeBoundary),
			DictionAndRhythm:  firstRenderClause(voice.DictionAndRhythm),
			ForbiddenMoves:    limitRenderStrings(voice.ForbiddenMoves, 1),
		})
	}
	firstAppearances := make(map[string]bool, len(sim.CharacterKit))
	for _, kit := range sim.CharacterKit {
		if kit.FirstAppearance {
			firstAppearances[strings.TrimSpace(kit.Character)] = true
		}
	}
	visuals := make([]draftVisualCard, 0, len(sim.VisualDesign))
	for _, visual := range sim.VisualDesign {
		name := strings.TrimSpace(visual.Character)
		visuals = append(visuals, draftVisualCard{
			Character:       name,
			FirstAppearance: firstAppearances[name],
			Silhouette:      firstRenderClause(visual.Silhouette),
			FaceAndHair:     firstRenderClause(visual.FaceAndHair),
			ClothingStyle:   firstRenderClause(visual.ClothingStyle),
			BodyLanguage:    firstRenderClause(visual.BodyLanguage),
			SignatureObject: firstRenderClause(visual.SignatureObject),
			FirstImpression: firstRenderClause(visual.FirstImpression),
			StatusWear:      firstRenderClause(visual.StatusWear),
			SceneUse:        firstRenderClause(visual.SceneUse),
		})
	}
	relationshipLenses := make([]draftRelationshipLens, 0, min(2, len(sim.RelationshipArcs)))
	for _, arc := range sim.RelationshipArcs {
		if len(relationshipLenses) >= 2 {
			break
		}
		relationshipLenses = append(relationshipLenses, draftRelationshipLens{
			Pair:              limitRenderStrings(arc.Pair, 2),
			CurrentBond:       firstRenderClause(arc.CurrentBond),
			EmotionalWant:     firstRenderClause(arc.EmotionalWant),
			Fear:              firstRenderClause(arc.Fear),
			Boundary:          firstRenderClause(arc.Boundary),
			NextEmotionalBeat: firstRenderClause(arc.NextEmotionalBeat),
		})
	}
	if len(sim.RelationshipArcs) == 0 {
		if lens, ok := strongestDraftDialogueRelationshipLens(sim.DialogueBlueprints); ok {
			relationshipLenses = append(relationshipLenses, lens)
		}
	}
	protagonist := draftPlanFocalizer(sim)
	projection := leanDraftProjection(draftProtagonistProjection{
		Protagonist:     protagonist,
		ChosenDecision:  sim.ProtagonistDecision,
		DecisionReason:  plan.Goal,
		PlanConstraints: sim.SceneConstraints,
		// Reuse the sanitized hard projection. Reading raw required_beats here
		// would reintroduce a trend-language candidate that was deliberately
		// excluded from mandatory_beats.
		ObservableEffects: append([]string(nil), mandatoryBeats...),
	})
	literaryContract := newDraftLiteraryRenderContract(plan, protagonist)
	return draftRenderPacket{
		Version:        9,
		Chapter:        plan.Chapter,
		Heading:        strings.TrimSpace(fmt.Sprintf("第%d章 %s", plan.Chapter, strings.TrimSpace(plan.Title))),
		Title:          plan.Title,
		Goal:           firstRenderClause(plan.Goal),
		Conflict:       firstRenderClause(plan.Conflict),
		Hook:           plan.Hook,
		EmotionArc:     firstRenderClause(plan.EmotionArc),
		MandatoryBeats: mandatoryBeats,
		// Every rewrite preserve constraint is a result-level invariant, not an
		// optional scene candidate. Keep the complete list while the packet policy
		// prevents the renderer from replaying it as a checklist.
		PreserveFacts:         canonicalPreserveFacts(nil, sim.ReviewRefinement.PreserveConstraints),
		ForbiddenMoves:        compactStrings(plan.Contract.ForbiddenMoves),
		ContinuityChecks:      continuityChecks,
		PayoffPoints:          limitRenderStrings(softPayoffDirections, 2),
		SceneAnchors:          sampleRenderStrings(softSceneAnchors, 2),
		ProtagonistProjection: projection,
		AntiAIExecutionPlan:   leanAntiAIPlan(sim.AntiAIPlan),
		VoiceCards:            selectEssentialVoiceCards(voices),
		// A first chapter can introduce the protagonist, love interest, parents,
		// pressure character and two or three local anchors in one continuous
		// opening. Keep enough cards for every meaningful entrance instead of
		// silently dropping the later (often more important) arrivals.
		VisualCards:            selectEntranceVisualCards(visuals, 8),
		DialogueScenes:         leanDraftDialogueScenes(sim.DialogueBlueprints),
		EmotionalLenses:        leanDraftEmotionalLenses(sim, protagonist),
		RelationshipLenses:     sampleRenderValues(relationshipLenses, 1),
		LiteraryRenderContract: literaryContract,
		CraftMethods:           draftCraftMethods(sim.ExternalRefs),
		// The ending is an on-page consequence contract, not hidden world state or
		// an optional shot suggestion. Preserve it byte-for-byte at the field level
		// so exact cut timing, audible anchors and forbidden answer semantics cannot
		// disappear during the finalized-plan -> prose-context projection.
		EndingContract:          sim.EndingContract,
		HardContractPolicy:      "mandatory_beats 的结果、preserve_facts、forbidden_moves、事实连续性、准确金额与数量、人物知识边界、授权边界、安全后果与 ending_consequence_contract 的截断时机/禁止结局是硬合同；preserve_facts 只锁结果，不是场景或句序清单，不得因删减写法素材而删改、合并成不同数量或模糊。",
		SoftMaterialPolicy:      "所有 soft_* 字段、文学镜头候选、笑点候选、示例措辞与 craft candidate_moves 都只是可替换素材。全章通常合计择取0—2项，允许重排、合并、换成现场自然细节或全部省略；未出现不算漏项，禁止为逐项验收另造场景。",
		SelectionPolicy:         "mandatory_beats 只是终局事实，不是镜头清单；只挑最有情绪、笑点或关系变化的场面写，其余允许离屏成立。",
		SceneBridgePolicy:       "转场服从人物注意力；能自然跳过就跳过，不为解释时间和因果另写汇报段。",
		DialogueTopologyPolicy:  "先朗读再保留台词；人物只说此刻会说的话，不替作者补全背景、规则和步骤，也不强制轮流发言。普通平静口述按完整气口说完，同一意思不得切成连续2—4汉字句号短句。",
		SystemVoicePolicy:       "系统只回应具体刺激；章级消息条数与时机优先，不得拆分加条。若只准一条短提示，其余边界靠人物试错和后果显出，不在该条打包规则。",
		JargonPolicy:            "计划、审核、项目管理和运营复盘语言不得进入叙述或日常对白；事实用生活经验表达。",
		PlanTranslationPolicy:   "从计划中取事实和人物欲望，重新组织成小说；不得照着 required_beats 的句序逐项证明。",
		ReaderRegisterPolicy:    "面向本项目约定的读者与题材语域；旁白贴着当前焦点人物当时的好恶、误判和词汇习惯，不替作者总结方法。",
		InterfaceCompression:    "界面只在惊讶、限制或奖励真正改变当下感受时出现，禁止连续用页面反馈替代人物反应。",
		ScenePurposePolicy:      "场景可以同时有正事、闲话、尴尬和关系余波；不要求每段都推进指标，也不写成办事过程复盘。",
		SpokenLanguagePolicy:    "对白服从身份、熟悉程度和现场情绪；普通人口述通常用一个完整气口承载对象、原因或条件，不能把同一判断切成连续2—4汉字句号碎片。短答可以短；连续碎断只用于抢险、打断、惊吓或刻意拒绝且现场有证据。不顺口的整轮推倒重说，不机械补口头禅。",
		EmotionalRenderPolicy:   "允许情绪在人物身上多停一会儿，也允许暂时没有结论；只写主角真的会注意到的细节，不在动作后逐句解释动机。",
		GroupCompressionPolicy:  "群体用嘈杂、等待、插话或背景动作形成现场感；只有真正与主角发生关系的人才展开，不能给每个人分配一条功能。",
		ChronologyPolicy:        "现实耗时要可信，但不按钟点报站；用饭点、天光、库存、疲劳和朋友手头的活自然表现时间过去。",
		ProofFocusPolicy:        "不强制跟拍看价、付款、取货、登记、测试等完整证明链；结果已由可见后果成立时可以一笔带过，把篇幅留给人物选择与关系余波。",
		NamedRolePolicy:         "无名功能角色保持无名；不得从角色册借用离屏人物姓名给路人补位，更不得改变已命名人物的职业、关系或当日行踪。",
		RelationshipPriority:    "核心关系人物同场时，先写他们怎样一起做事、怎样误读或修正对方；关系可以藏在偏心、边界、拒绝和没说完的话里，不靠手续问答或作者判词宣布推进。",
		CharacterEntrancePolicy: "实名角色首次进入读者视野时，在首次动作或对白附近落一个能画出人的视觉锚点：优先轮廓/脸发、穿着/标志物、身体语言三类中最贴 POV 与现场的一项，并让它同时传达身份、状态或关系印象。主配角至少一项，核心角色宜用两项分散落地；禁止证件照式罗列、镜前自检、只写帅美高冷，也禁止人物出场数段后才补长相。非首次出场只在状态变化或识别需要时提醒旧锚点。",
	}
}

func draftWordBudgetFromContext(result map[string]any) *draftWordBudget {
	working, _ := result["working_memory"].(map[string]any)
	userRules, _ := working["user_rules"].(map[string]any)
	structured, exists := userRules["structured"]
	if !exists {
		return nil
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		return nil
	}
	var payload struct {
		ChapterWords *struct {
			Min int `json:"min"`
			Max int `json:"max"`
		} `json:"chapter_words"`
	}
	if json.Unmarshal(encoded, &payload) != nil || payload.ChapterWords == nil {
		return nil
	}
	minWords, maxWords := payload.ChapterWords.Min, payload.ChapterWords.Max
	if minWords <= 0 && maxWords <= 0 {
		return nil
	}
	targetMin, targetMax := minWords, maxWords
	if minWords > 0 && maxWords > minWords {
		span := maxWords - minWords
		targetMin = minWords + span/3
		targetMax = maxWords - span/3
		if targetMax < targetMin {
			targetMin, targetMax = minWords, maxWords
		}
	}
	return &draftWordBudget{
		Unit:          "unicode_characters_including_title",
		HardMin:       minWords,
		HardMax:       maxWords,
		TargetMin:     targetMin,
		TargetMax:     targetMax,
		ExactBoundary: true,
	}
}

func draftCraftMethods(refs []domain.ExternalReferencePlan) []draftCraftMethod {
	var methods []draftCraftMethod
	seenNeeds := map[string]bool{}
	for _, ref := range refs {
		sourceType := strings.ToLower(strings.TrimSpace(ref.SourceType))
		if sourceType != craftSourceType && sourceType != benchmarkCraftSourceType {
			continue
		}
		var receiptID string
		var sourceRefs []string
		for _, sourceRef := range ref.SourceRefs {
			sourceRef = strings.TrimSpace(sourceRef)
			if !strings.HasPrefix(sourceRef, craftReceiptTokenPrefix) {
				continue
			}
			if receiptID == "" {
				receiptID = strings.TrimPrefix(sourceRef, craftReceiptTokenPrefix)
				if idx := strings.Index(receiptID, "#"); idx >= 0 {
					receiptID = receiptID[:idx]
				}
			}
			sourceRefs = append(sourceRefs, sourceRef)
		}
		if receiptID == "" || len(sourceRefs) == 0 {
			continue
		}
		needID := rewriteCraftNeedID(ref.QueryOrNeed)
		if seenNeeds[needID] {
			continue
		}
		seenNeeds[needID] = true
		methods = append(methods, draftCraftMethod{
			ReceiptID:          receiptID,
			Need:               needID,
			Moves:              compactCraftMethodStrings(ref.UsableDetails, 1),
			TransformationRule: firstRenderClause(ref.TransformationRule),
			Avoid:              compactCraftMethodStrings(ref.DoNotUse, 3),
			SourceRefs:         limitRenderStrings(sourceRefs, 3),
			UsagePolicy:        "这是解决诊断的候选方法，不是剧情或句段职责。若适合现场，最多采用一个 candidate_move 并可重排改写；不适合可省略具体 move，但仍须用人物因果解决原诊断。hard_avoid 始终遵守。",
		})
		if len(methods) >= 2 {
			break
		}
	}
	return methods
}

func rewriteCraftNeedID(query string) string {
	for _, id := range []string{"rewrite-methodology", "rewrite-dialogue", "rewrite-scene"} {
		if strings.Contains(query, id) {
			return id
		}
	}
	return "rewrite-craft"
}

func newDraftLiteraryRenderContract(plan domain.ChapterPlan, protagonist string) draftLiteraryRenderContract {
	const usagePolicy = "focalizer、narrative_access、knowledge_boundary 是信息硬边界。soft_perceptual_bias、soft_scene_choices、soft_lens_choices 与 soft_afterimage_candidate 只是备选渲染方法：全章最多择取1—2项，可重排、替换或全部省略，不逐项验收，也不得为消费候选物件另造镜头。"
	sim := plan.CausalSimulation
	if literary := sim.LiteraryRendering; literary != nil {
		contract := draftLiteraryRenderContract{
			Source:                "explicit_plan",
			Focalizer:             firstRenderClause(literary.Focalizer),
			NarrativeAccess:       string(literary.NarrativeAccess),
			KnowledgeBoundary:     firstRenderClause(literary.KnowledgeBoundary),
			PerceptualBias:        firstRenderClause(literary.PerceptualBias),
			SummaryOmissionPolicy: firstRenderClause(literary.SummaryOmissionPolicy),
			Afterimage:            firstRenderClause(literary.Afterimage),
			SourceRefs:            compactLiterarySourceRefs(literary.SourceRefs),
			UsagePolicy:           usagePolicy,
		}
		for _, scene := range literary.SceneModes {
			contract.SceneModes = append(contract.SceneModes, draftLiterarySceneMode{
				Target:      firstRenderClause(scene.Target),
				Mode:        string(scene.Mode),
				Distance:    string(scene.Distance),
				StateChange: firstRenderClause(scene.StateChange),
				RenderMove:  firstRenderClause(scene.RenderMove),
			})
		}
		for _, lens := range literary.ActiveLenses {
			contract.ActiveLenses = append(contract.ActiveLenses, draftLiteraryActiveLens{
				Kind:       firstRenderClause(lens.Kind),
				Target:     firstRenderClause(lens.Target),
				Move:       firstRenderClause(lens.Move),
				Why:        firstRenderClause(lens.Why),
				Avoid:      firstRenderClause(lens.Avoid),
				SourceRefs: compactLiterarySourceRefs(lens.SourceRefs),
			})
		}
		return limitDraftLiterarySoftChoices(contract)
	}

	// Legacy plans remain usable. Project a small contract from durable POV,
	// appraisal, retention and voice fields instead of making a rewrite wait for
	// a new 90KB planning pass. This is deliberately a projection: no scene
	// evidence, turn choreography or full anti-AI checklist crosses the boundary.
	focalizer := strings.TrimSpace(protagonist)
	if focalizer == "" && len(sim.VoiceLogic) > 0 {
		focalizer = strings.TrimSpace(sim.VoiceLogic[0].Character)
	}
	if focalizer == "" {
		focalizer = "当前章主视角人物"
	}
	contract := draftLiteraryRenderContract{
		Source:            "legacy_projection",
		Focalizer:         focalizer,
		NarrativeAccess:   string(domain.LiteraryNarrativeAccessInternal),
		KnowledgeBoundary: "只使用焦点位置可感知、记忆和有依据推断的信息；不得读取未表达的他人内心或幕后事实。",
		Afterimage:        firstRenderClause(plan.Hook),
		SourceRefs:        []string{"literary-rendering#focalization-boundary"},
		UsagePolicy:       usagePolicy,
	}

	if voice, ok := draftVoiceForCharacter(sim.VoiceLogic, focalizer); ok {
		if boundary := firstRenderClause(voice.KnowledgeBoundary); boundary != "" {
			contract.KnowledgeBoundary = boundary
		}
		move := firstNonemptyRenderClause(voice.SubtextStrategy, voice.HiddenSubtext, voice.SpeechPrinciple)
		why := firstNonemptyRenderClause(voice.HiddenSubtext, voice.RelationshipStance, voice.SceneObjective)
		if move != "" && why != "" {
			contract.ActiveLenses = append(contract.ActiveLenses, draftLiteraryActiveLens{
				Kind:       "dialogue-subtext",
				Target:     focalizer + "参与的关键对白",
				Move:       move,
				Why:        why,
				Avoid:      "不让角色替作者解释潜台词，也不把所有直率信息强行写成闪避。",
				SourceRefs: []string{"literary-rendering#dialogue-subtext"},
			})
			contract.SourceRefs = append(contract.SourceRefs, "literary-rendering#dialogue-subtext")
		}
	}

	if emotion, ok := draftEmotionForCharacter(sim.EmotionalLogic, focalizer); ok {
		contract.PerceptualBias = firstNonemptyRenderClause(emotion.CognitiveBias, emotion.GoalAppraisal, emotion.MeaningNeed)
		move := firstNonemptyRenderClause(emotion.EmotionLedAction, emotion.RegulationStrategy)
		why := firstNonemptyRenderClause(emotion.GoalAppraisal, emotion.EmotionalTrigger)
		if move != "" && why != "" {
			contract.ActiveLenses = append(contract.ActiveLenses, draftLiteraryActiveLens{
				Kind:       "emotion-appraisal",
				Target:     focalizer + "重新评价局面并作出选择的时刻",
				Move:       move,
				Why:        why,
				Avoid:      "不把情绪分析词或解释性结论写进正文；让注意、选择和代价留下证据。",
				SourceRefs: []string{"literary-rendering#emotion-appraisal"},
			})
			contract.SourceRefs = append(contract.SourceRefs, "literary-rendering#emotion-appraisal")
		}
	}
	if contract.PerceptualBias == "" {
		if state, ok := draftStateForCharacter(sim.InitialState, focalizer); ok {
			contract.PerceptualBias = firstNonemptyRenderClause(strings.Join(state.Misbeliefs, "；"), state.ActionTendency, state.CurrentGoal)
		}
	}

	if len(sim.OutcomeShift) > 0 {
		contract.SceneModes = append(contract.SceneModes, draftLiterarySceneMode{
			Target:      focalizer + "作出本章不可逆选择的关键场面",
			Mode:        string(domain.LiterarySceneModeScene),
			Distance:    string(domain.LiteraryNarrativeDistanceClose),
			StateChange: firstRenderClause(sim.OutcomeShift[0]),
			RenderMove:  "把触发、误判或重新评价、被放弃的动作与选择后果留在实时现场。",
		})
		contract.SourceRefs = append(contract.SourceRefs, "literary-rendering#psychic-distance", "literary-rendering#scene-summary")
	}
	if len(sim.ReaderRetentionPlan.CutOrCompress) > 0 {
		contract.SummaryOmissionPolicy = joinRenderClauses(sim.ReaderRetentionPlan.CutOrCompress, 2)
		contract.SceneModes = append(contract.SceneModes, draftLiterarySceneMode{
			Target:      firstRenderClause(sim.ReaderRetentionPlan.CutOrCompress[0]),
			Mode:        string(domain.LiterarySceneModeSummary),
			Distance:    string(domain.LiteraryNarrativeDistanceFar),
			StateChange: "只保留对人物选择、关系或现场后果有影响的结果",
			RenderMove:  "概述或直接跳过重复过程，不把手续和验证步骤还原成镜头清单。",
		})
		contract.SourceRefs = append(contract.SourceRefs, "literary-rendering#scene-summary")
	}
	if rhythm := firstRenderClause(sim.AntiAIPlan.SentenceRhythmPolicy); rhythm != "" {
		contract.ActiveLenses = append(contract.ActiveLenses, draftLiteraryActiveLens{
			Kind:       "syntax-rhythm",
			Target:     "本章压力、犹疑、决定与余波之间的句法换挡",
			Move:       rhythm,
			Why:        "让句法结构跟随认知负荷和信息重量，而不是全章维持同一概率分布。",
			Avoid:      "不随机轮换长短句，不用错字、碎句或冷僻词扰动检测。",
			SourceRefs: []string{"literary-rendering#syntax-rhythm"},
		})
		contract.SourceRefs = append(contract.SourceRefs, "literary-rendering#syntax-rhythm")
	}
	contract.SourceRefs = compactLiterarySourceRefs(contract.SourceRefs)
	return limitDraftLiterarySoftChoices(contract)
}

// limitDraftLiterarySoftChoices prevents a rich planning dossier from becoming
// a prose checklist. POV and knowledge boundaries remain intact; only optional
// scene/lens suggestions are sampled for the Drafter-facing packet.
func limitDraftLiterarySoftChoices(contract draftLiteraryRenderContract) draftLiteraryRenderContract {
	contract.SceneModes = sampleRenderValues(contract.SceneModes, 1)
	contract.ActiveLenses = sampleRenderValues(contract.ActiveLenses, 1)
	return contract
}

func draftVoiceForCharacter(values []domain.CharacterVoiceLogic, character string) (domain.CharacterVoiceLogic, bool) {
	for _, value := range values {
		if strings.TrimSpace(value.Character) == strings.TrimSpace(character) {
			return value, true
		}
	}
	return domain.CharacterVoiceLogic{}, false
}

func draftEmotionForCharacter(values []domain.CharacterEmotionalLogic, character string) (domain.CharacterEmotionalLogic, bool) {
	for _, value := range values {
		if strings.TrimSpace(value.Character) == strings.TrimSpace(character) {
			return value, true
		}
	}
	return domain.CharacterEmotionalLogic{}, false
}

func draftStateForCharacter(values []domain.CharacterSimulationState, character string) (domain.CharacterSimulationState, bool) {
	for _, value := range values {
		if strings.TrimSpace(value.Character) == strings.TrimSpace(character) {
			return value, true
		}
	}
	return domain.CharacterSimulationState{}, false
}

func firstNonemptyRenderClause(values ...string) string {
	for _, value := range values {
		if value = firstRenderClause(value); value != "" {
			return value
		}
	}
	return ""
}

func joinRenderClauses(values []string, limit int) string {
	clauses := make([]string, 0, min(limit, len(values)))
	for _, value := range values {
		if len(clauses) >= limit {
			break
		}
		if value = firstRenderClause(value); value != "" {
			clauses = append(clauses, value)
		}
	}
	return strings.Join(clauses, "；")
}

func compactLiterarySourceRefs(values []string) []string {
	refs := compactStrings(values)
	for i := range refs {
		refs[i] = strings.TrimSpace(refs[i])
		if utf8.RuneCountInString(refs[i]) > 180 {
			refs[i] = string([]rune(refs[i])[:180])
		}
	}
	return refs
}

func draftVisibilityFromSimulation(simulation map[string]any) (visible, excluded []string) {
	raw, ok := simulation["character_decisions"]
	if !ok {
		return nil, nil
	}
	var decisions []domain.CharacterWorldDecision
	switch values := raw.(type) {
	case []domain.CharacterWorldDecision:
		decisions = values
	default:
		encoded, err := json.Marshal(values)
		if err != nil || json.Unmarshal(encoded, &decisions) != nil {
			return nil, nil
		}
	}
	for _, decision := range decisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		if decision.VisibleToPOV {
			visible = append(visible, name)
		} else {
			excluded = append(excluded, name)
		}
	}
	return compactStrings(visible), compactStrings(excluded)
}

func selectEssentialVoiceCards(values []draftVoiceCard) []draftVoiceCard {
	if len(values) == 0 {
		return nil
	}
	out := make([]draftVoiceCard, 0, 3)
	add := func(value draftVoiceCard) {
		for _, existing := range out {
			if existing.Character == value.Character {
				return
			}
		}
		out = append(out, value)
	}
	add(values[0])
	if len(values) > 1 {
		add(values[1])
	}
	for _, value := range values {
		if strings.Contains(value.Character, "系统") {
			add(value)
			break
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func firstRenderClause(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, separator := range []string{"；", "。", "\n"} {
		if index := strings.Index(text, separator); index >= 0 {
			text = strings.TrimSpace(text[:index])
		}
	}
	runes := []rune(text)
	if len(runes) > 120 {
		text = strings.TrimSpace(string(runes[:120]))
	}
	return text
}

func keepDraftDialogueScene(scene domain.DialogueSceneBlueprint) bool {
	mode := strings.ToLower(strings.TrimSpace(scene.DialogueMode))
	context := scene.SceneID + " " + scene.RelationshipFrame + " " + scene.ScenePressure + " " + scene.DialogueObjective
	relationshipBearing := containsAnyRenderPhrase(context, []string{
		"旧识", "亲友", "父母", "朋友", "同盟", "恋", "暧昧", "信任", "关系", "体面", "羞耻", "控制权",
	})
	if strings.Contains(mode, "mediated") && containsAnyRenderPhrase(context, []string{"系统", "结算", "任务卡", "额度"}) {
		return false
	}
	if !strings.Contains(mode, "logistics") && !strings.Contains(mode, "settlement") &&
		!strings.Contains(mode, "status") && !strings.Contains(mode, "report") && !strings.Contains(mode, "procedure") {
		return true
	}
	return relationshipBearing
}

func strongestDraftDialogueRelationshipLens(scenes []domain.DialogueSceneBlueprint) (draftRelationshipLens, bool) {
	bestIndex := -1
	bestStrength := 0
	for i, scene := range scenes {
		if !keepDraftDialogueScene(scene) {
			continue
		}
		context := scene.RelationshipFrame + " " + scene.ScenePressure + " " + scene.DialogueObjective + " " + scene.ExitBeat
		signalStrength := draftDialogueRelationshipSignalStrength(context)
		if signalStrength == 0 {
			continue
		}
		strength := signalStrength * 10
		if strings.TrimSpace(scene.RelationshipFrame) != "" {
			strength += 3
		}
		if strings.TrimSpace(scene.DialogueObjective) != "" {
			strength += 2
		}
		if strings.TrimSpace(scene.ExitBeat) != "" {
			strength++
		}
		if strength > bestStrength {
			bestIndex = i
			bestStrength = strength
		}
	}
	if bestIndex < 0 {
		return draftRelationshipLens{}, false
	}

	scene := scenes[bestIndex]
	return draftRelationshipLens{
		Pair:              limitRenderStrings(scene.Participants, 2),
		CurrentBond:       firstRenderClause(scene.RelationshipFrame),
		EmotionalWant:     firstRenderClause(scene.DialogueObjective),
		NextEmotionalBeat: firstRenderClause(scene.ExitBeat),
	}, true
}

func draftDialogueRelationshipSignalStrength(context string) int {
	strength := 0
	for _, phrase := range []string{
		"旧识", "亲友", "父母", "父子", "父女", "母子", "母女", "兄弟", "姐妹", "朋友", "同盟",
		"恋", "暧昧", "信任", "关系", "亲密", "疏远", "背叛", "关心", "护短", "试探", "体面", "羞耻", "控制权",
		"秘密", "怀疑", "信息差", "没有追问", "共同", "搭档",
	} {
		if strings.Contains(context, phrase) {
			strength++
		}
	}
	return strength
}

func renderSceneAnchors(anchors []string) []string {
	out := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		if containsAnyRenderPhrase(anchor, []string{"材料清单", "采购凭证", "测试记录", "漏保测试", "电子票据"}) {
			continue
		}
		out = append(out, anchor)
	}
	return compactStrings(out)
}

// splitHardRenderMaterials keeps accidental hard facts out of soft candidate
// fields. Plans should normally place these in required_beats / continuity, but
// legacy and model-authored plans sometimes put an exact amount, knowledge line,
// authorization boundary or safety consequence in payoff_points/scene_anchors.
// Promote those clauses into factual continuity rather than silently demoting
// them when the prose packet labels the remaining material soft.
func splitHardRenderMaterials(values []string) (soft, hard []string) {
	for _, value := range compactStrings(values) {
		if hardRenderMaterial(value) {
			hard = append(hard, value)
			continue
		}
		soft = append(soft, value)
	}
	return soft, hard
}

func hardRenderMaterial(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if renderAmountMaterialPattern.MatchString(text) || renderPercentMaterialPattern.MatchString(text) || renderPermissionMaterialPattern.MatchString(text) {
		return true
	}
	if containsAnyRenderPhrase(text, []string{
		"不知情", "不知道真相", "尚不知道", "不得知道", "无权知道",
		"必须保密", "不得泄露", "不能泄露", "禁止泄露", "不能公开", "不得公开",
		"秘密边界", "保密边界", "信息边界", "知情范围",
		"未经授权", "未获授权", "没有授权", "授权边界", "授权范围", "越权",
		"未经同意", "尚未同意", "没有同意", "必须征得", "获得许可", "未经许可",
		"尚未审批", "未经审批", "审批结果", "必须签字", "未经签字",
		"安全边界", "安全责任", "安全后果", "存在隐患", "消除隐患", "责任后果",
		"漏电", "触电", "伤亡", "发生事故", "造成事故", "导致受伤", "防护要求",
	}) {
		return true
	}
	return false
}

var (
	renderAmountMaterialPattern     = regexp.MustCompile(`(?:人民币[[:space:]]*)?(?:[0-9０-９]+(?:[.,，][0-9０-９]+)?[[:space:]]*(?:亿|万|千|百)?|[零〇一二两三四五六七八九十百千万亿]+)[[:space:]]*(?:元|块钱)`)
	renderPercentMaterialPattern    = regexp.MustCompile(`(?:百分之[[:space:]]*[0-9０-９零〇一二两三四五六七八九十百点.]+|[0-9０-９]+(?:[.][0-9０-９]+)?[[:space:]]*%)`)
	renderPermissionMaterialPattern = regexp.MustCompile(`(?:未经|未获|没有|必须征得|必须获得)[^，。！？；;\n]{0,12}(?:同意|授权|许可|审批|签字)`)
)

func leanEntertainmentPlan(plan domain.ReaderEntertainmentPlan) draftEntertainmentPlan {
	return draftEntertainmentPlan{
		OpeningBeat:          firstRenderClause(plan.OpeningBeat),
		HumorBeats:           sampleRenderStrings(plan.HumorBeats, 1),
		ImmediatePayoffs:     limitRenderStrings(plan.ImmediatePayoffs, 1),
		ProcedureCompression: firstRenderClause(plan.ProcedureCompression),
		CompanionVoiceBeat:   firstRenderClause(plan.CompanionVoiceBeat),
	}
}

func leanTrendLanguagePlan(plans []domain.TrendLanguagePlan) []draftTrendLanguagePlan {
	out := make([]draftTrendLanguagePlan, 0, min(1, len(plans)))
	for _, plan := range plans {
		if len(out) >= 1 {
			break
		}
		out = append(out, draftTrendLanguagePlan{
			Item:             plan.Item,
			CharacterCarrier: firstRenderClause(plan.CharacterCarrier),
			SceneFunction:    firstRenderClause(plan.SceneFunction),
			UsageBudget:      firstRenderClause(plan.UsageBudget),
		})
	}
	return out
}

func leanLongformOpening(opening domain.LongformOpeningDesign) draftLongformOpening {
	return draftLongformOpening{
		TargetReader:      firstRenderClause(opening.TargetReader),
		OpeningHook:       firstRenderClause(opening.OpeningHook),
		FirstChapterProof: limitRenderStrings(opening.FirstChapterProof, 2),
		RetentionRisks:    limitRenderStrings(opening.RetentionRisks, 2),
	}
}

func leanAntiAIPlan(plan domain.AntiAIExecutionPlan) draftAntiAIPlan {
	return draftAntiAIPlan{
		RiskSignals:          qualitativeAntiAIStrings(plan.RiskSignals, 2),
		CounterMoves:         qualitativeAntiAIStrings(plan.CounterMoves, 2),
		SentenceRhythmPolicy: qualitativeAntiAIText(plan.SentenceRhythmPolicy),
		ObjectResponseBudget: strings.TrimSpace(plan.ObjectResponseBudget),
		DialogueFunctionPlan: strings.TrimSpace(plan.DialogueFunctionPlan),
		ReviewChecks:         qualitativeAntiAIStrings(plan.ReviewChecks, 2),
	}
}

var antiAIMetricRecipePattern = regexp.MustCompile(`(?i)(?:\b(?:cv|ttr)\b|百分(?:比|之)|[%％]|检测(?:器|分|值|概率)|概率分|阈值|均值|中位数|标准差|方差|分布曲线|句长曲线|段长曲线|节奏曲线|词汇丰富度|每(?:[零〇一二两三四五六七八九十百0-9０-９]+|几)(?:句|段|字|行)|(?:不少于|不低于|不超过|至少|至多|低于|高于|达到|控制在|维持在)[^，。！？；;\n]{0,18}(?:[0-9０-９]+|百分之)|[零〇一二两三四五六七八九十百0-9０-９]+次(?:反馈|回应|显字|换挡|转折|打断))`)

// qualitativeAntiAIText keeps failure-specific scene advice while preventing
// detector statistics and numeric cadence recipes from becoming prose-facing
// writing instructions. Exact event timing belongs to ObjectResponseBudget and
// DialogueFunctionPlan, which leanAntiAIPlan deliberately preserves verbatim.
func qualitativeAntiAIText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !antiAIMetricRecipePattern.MatchString(value) {
		return value
	}
	clauses := strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case '，', ',', '。', '；', ';', '！', '!', '？', '?', '\n', '\r', '—':
			return true
		default:
			return false
		}
	})
	kept := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" || antiAIMetricRecipePattern.MatchString(clause) {
			continue
		}
		kept = append(kept, clause)
	}
	return strings.Join(kept, "；")
}

func qualitativeAntiAIStrings(values []string, maxItems int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, min(maxItems, len(values)))
	seen := make(map[string]struct{}, maxItems)
	for _, value := range values {
		clean := qualitativeAntiAIText(value)
		if clean == "" {
			continue
		}
		key := clean
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

// draftPlanFocalizer resolves the one character whose private appraisal may
// cross into the prose packet. Explicit literary POV wins; legacy plans fall
// back to the first simulation state, then the first emotional/voice card.
// Other characters' private goals and decisions remain in the formal plan.
func draftPlanFocalizer(sim domain.ChapterCausalSimulation) string {
	if sim.LiteraryRendering != nil {
		if focalizer := strings.TrimSpace(sim.LiteraryRendering.Focalizer); focalizer != "" {
			return focalizer
		}
	}
	if len(sim.InitialState) > 0 {
		if focalizer := strings.TrimSpace(sim.InitialState[0].Character); focalizer != "" {
			return focalizer
		}
	}
	if len(sim.EmotionalLogic) > 0 {
		if focalizer := strings.TrimSpace(sim.EmotionalLogic[0].Character); focalizer != "" {
			return focalizer
		}
	}
	if len(sim.VoiceLogic) > 0 {
		return strings.TrimSpace(sim.VoiceLogic[0].Character)
	}
	return ""
}

// leanDraftDialogueScenes keeps the scene-level pressure, objective and exit
// that make dialogue behave like human conflict, while dropping entry lines,
// per-turn tactics, action beats and information ledgers that would turn prose
// into a transcript of the planner's choreography.
func leanDraftDialogueScenes(scenes []domain.DialogueSceneBlueprint) []draftDialogueScene {
	projected := make([]draftDialogueScene, 0, len(scenes))
	for _, scene := range scenes {
		if !keepDraftDialogueScene(scene) {
			continue
		}
		projected = append(projected, draftDialogueScene{
			SceneID:           strings.TrimSpace(scene.SceneID),
			ScenePressure:     firstRenderClause(scene.ScenePressure),
			RelationshipFrame: firstRenderClause(scene.RelationshipFrame),
			Participants:      limitRenderStrings(scene.Participants, 4),
			LocationAnchor:    firstRenderClause(scene.LocationAnchor),
			DialogueObjective: firstRenderClause(scene.DialogueObjective),
			ExitBeat:          firstRenderClause(scene.ExitBeat),
			DoNotUse:          limitRenderStrings(scene.DoNotUse, 3),
		})
	}
	return sampleRenderValues(projected, 2)
}

// leanDraftEmotionalLenses exposes only focalizer-owned subjective causality.
// Up to two explicit emotional-logic entries survive, plus the finalized
// emotion-appraisal lens when present. This lets a plan require two distinct
// stimulus -> appraisal -> regulation -> choice -> consequence chains without
// exposing another character's unspoken reasoning or all-character decisions.
func leanDraftEmotionalLenses(sim domain.ChapterCausalSimulation, focalizer string) []draftEmotionalLens {
	focalizer = strings.TrimSpace(focalizer)
	out := make([]draftEmotionalLens, 0, 3)
	for _, emotion := range sim.EmotionalLogic {
		character := strings.TrimSpace(emotion.Character)
		if focalizer != "" && character != focalizer {
			continue
		}
		out = append(out, draftEmotionalLens{
			Kind:             "character_emotional_logic",
			Character:        character,
			ImmediateState:   strings.TrimSpace(emotion.ImmediateState),
			PrimaryEmotion:   strings.TrimSpace(emotion.PrimaryEmotion),
			Trigger:          strings.TrimSpace(emotion.EmotionalTrigger),
			GoalAppraisal:    strings.TrimSpace(emotion.GoalAppraisal),
			Regulation:       strings.TrimSpace(emotion.RegulationStrategy),
			EmotionLedAction: strings.TrimSpace(emotion.EmotionLedAction),
			SceneEvidence:    sampleRenderStrings(emotion.EvidenceInScene, 3),
		})
		if len(out) >= 2 {
			break
		}
	}
	if sim.LiteraryRendering == nil {
		return out
	}
	for _, lens := range sim.LiteraryRendering.ActiveLenses {
		kind := strings.ToLower(strings.TrimSpace(lens.Kind))
		if !strings.Contains(kind, "emotion-appraisal") && !strings.Contains(kind, "subjective-causal") {
			continue
		}
		out = append(out, draftEmotionalLens{
			Kind:                        "subjective_causal_requirement",
			Character:                   focalizer,
			SubjectiveCausalTarget:      strings.TrimSpace(lens.Target),
			SubjectiveCausalRequirement: strings.TrimSpace(lens.Move),
			SubjectiveCausalReason:      strings.TrimSpace(lens.Why),
			SubjectiveCausalHardAvoid:   strings.TrimSpace(lens.Avoid),
		})
		break
	}
	return out
}

func limitRenderStrings(values []string, limit int) []string {
	values = compactStrings(values)
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]string(nil), values...)
}

func sampleRenderStrings(values []string, limit int) []string {
	return sampleRenderValues(compactStrings(values), limit)
}

// sampleRenderValues preserves the opening and payoff while spreading any
// remaining packet budget across the ordered material between them.
func sampleRenderValues[T any](values []T, limit int) []T {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= limit {
		return append([]T(nil), values...)
	}
	if limit == 1 {
		return append([]T(nil), values[0])
	}

	out := make([]T, 0, limit)
	span := len(values) - 1
	steps := limit - 1
	for i := 0; i < limit; i++ {
		index := (i*span + steps/2) / steps
		out = append(out, values[index])
	}
	return out
}

func containsAnyRenderPhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func limitCandidateBeats(values []draftCandidateBeat, limit int) []draftCandidateBeat {
	return sampleRenderValues(values, limit)
}

func limitVoiceCards(values []draftVoiceCard, limit int) []draftVoiceCard {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftVoiceCard(nil), values...)
}

func limitVisualCards(values []draftVisualCard, limit int) []draftVisualCard {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftVisualCard(nil), values...)
}

func selectEntranceVisualCards(values []draftVisualCard, limit int) []draftVisualCard {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	selected := make([]draftVisualCard, 0, min(limit, len(values)))
	for _, value := range values {
		if value.FirstAppearance {
			selected = append(selected, value)
			if len(selected) == limit {
				return selected
			}
		}
	}
	for _, value := range values {
		if value.FirstAppearance {
			continue
		}
		selected = append(selected, value)
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func limitGroundingDetails(values []domain.GroundingDetailPlan, limit int) []domain.GroundingDetailPlan {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]domain.GroundingDetailPlan(nil), values...)
}

func limitEnvironmentSignals(values []draftEnvironmentSignal, limit int) []draftEnvironmentSignal {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]draftEnvironmentSignal(nil), values...)
}

// RenderRequiredOutcomes projects every hard result-level requirement into the
// Drafter packet. It removes duplicate outline restatements and optional style
// literals, but it must not sample or shorten hard outcomes: amounts, counts,
// order constraints and limitations often live after the first punctuation.
func RenderRequiredOutcomes(plan domain.ChapterPlan) []string {
	beats, _ := splitOptionalStyleBeats(plan.Contract.RequiredBeats, plan.CausalSimulation.TrendLanguage)
	out := make([]string, 0, len(beats))
	for _, raw := range beats {
		beat := unwrapRenderOutcome(raw)
		beat = stripEmbeddedOptionalStyleMaterial(beat, plan.CausalSimulation.TrendLanguage)
		beat = strings.TrimSpace(beat)
		if beat == "" {
			continue
		}
		merged := false
		for i, existing := range out {
			if !renderOutcomesEquivalent(existing, beat) {
				continue
			}
			if preferRenderOutcome(beat, existing) {
				out[i] = beat
			}
			merged = true
			break
		}
		if !merged {
			out = append(out, beat)
		}
	}
	return compactStrings(out)
}

// RenderContinuityChecks keeps factual continuity in the prose packet while
// removing presentation instructions that belong to style or review policy.
func RenderContinuityChecks(plan domain.ChapterPlan) []string {
	out := make([]string, 0, len(plan.Contract.ContinuityChecks))
	for _, check := range plan.Contract.ContinuityChecks {
		if optionalStyleText(check, plan.CausalSimulation.TrendLanguage) || renderOnlyContinuityCheck(check) {
			continue
		}
		out = append(out, check)
	}
	return compactStrings(out)
}

func renderOnlyContinuityCheck(text string) bool {
	if strings.Contains(text, "sha256") || strings.Contains(text, "局部返工源正文") ||
		(strings.Contains(text, "chapters/") && strings.Contains(text, "事实来源")) {
		return true
	}
	for _, marker := range []string{
		"章末具体锚点", "短消息分开发送", "颜文字", "拟声", "吐槽的起头",
		"每次只承担拒绝", "不能连续用界面", "必须位于报价确认后", "台词原句",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// RenderEndingContract preserves the consequence and forward pull but moves a
// prescribed final prop/shot out of the hard contract. Exact framing belongs
// in EndingAnchorCandidate so the Drafter can replace it with a stronger beat.
func RenderEndingContract(plan domain.ChapterPlan) domain.EndingConsequenceContract {
	contract := plan.CausalSimulation.EndingContract
	contract.ConcreteAnchor = ""
	contract.WhyNotUI = ""
	return contract
}

func unwrapRenderOutcome(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{
		"必须完整兑现大纲核心事件：",
		"必须兑现大纲钩子；若现有章节契约已将其前移，则作为中段转折而非强行改写章末：",
	} {
		if strings.HasPrefix(text, prefix) {
			// goal / hook already carry these outline anchors in render_packet.
			return ""
		}
	}
	return text
}

func renderOutcomesEquivalent(a, b string) bool {
	a = normalizeRenderOutcome(a)
	b = normalizeRenderOutcome(b)
	if a == "" || b == "" {
		return false
	}
	shorter, longer := a, b
	if utf8.RuneCountInString(shorter) > utf8.RuneCountInString(longer) {
		shorter, longer = longer, shorter
	}
	if utf8.RuneCountInString(shorter) >= 8 && strings.Contains(longer, shorter) {
		return true
	}
	aPairs := renderOutcomeBigrams(a)
	bPairs := renderOutcomeBigrams(b)
	if len(aPairs) < 6 || len(bPairs) < 6 {
		return false
	}
	intersection := 0
	for pair := range aPairs {
		if _, ok := bPairs[pair]; ok {
			intersection++
		}
	}
	denominator := min(len(aPairs), len(bPairs))
	return intersection >= 6 && float64(intersection)/float64(denominator) >= 0.52
}

func normalizeRenderOutcome(text string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(text) {
		if unicode.Is(unicode.Han, r) || unicode.IsDigit(r) || unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderOutcomeBigrams(text string) map[string]struct{} {
	runes := []rune(text)
	out := make(map[string]struct{}, max(0, len(runes)-1))
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

func preferRenderOutcome(candidate, current string) bool {
	candidateLen := utf8.RuneCountInString(candidate)
	currentLen := utf8.RuneCountInString(current)
	if candidateLen < 8 {
		return false
	}
	if currentLen < 8 {
		return true
	}
	// Equivalent restatements are deduplicated in favor of the more complete
	// contract. Choosing the shorter wording can silently erase a later amount,
	// sequence condition or prohibition.
	return candidateLen > currentLen
}

func splitOptionalStyleBeats(beats []string, trends []domain.TrendLanguagePlan) ([]string, []string) {
	var mandatory []string
	var optional []string
	for _, beat := range beats {
		if optionalStyleText(beat, trends) {
			optional = append(optional, beat)
			continue
		}
		mandatory = append(mandatory, beat)
	}
	return mandatory, optional
}

func optionalStyleText(text string, trends []domain.TrendLanguagePlan) bool {
	trimmed := strings.TrimSpace(text)
	// A style literal embedded in a compound event must not demote the event's
	// real outcome (for example, "赵航用梗打断；林澈离席"). Only pure style
	// requirements belong in optional_style_beats.
	compound := strings.TrimRight(trimmed, "。！？!?；;")
	if strings.ContainsAny(compound, "；;。") {
		return false
	}
	for _, marker := range []string{"热梗", "颜文字", "台词原句", "原样使用", "必须说成", "句式槽位"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	trendTokens := renderTrendTokens(trends)
	if strings.Contains(text, "^_^") && utf8.RuneCountInString(trimmed) <= 60 {
		return true
	}
	for _, token := range trendTokens {
		if token != "" && strings.Contains(text, token) && utf8.RuneCountInString(trimmed) <= 60 &&
			containsRenderStyleMarker(text) {
			return true
		}
	}
	return false
}

func renderTrendTokens(trends []domain.TrendLanguagePlan) []string {
	tokens := make([]string, 0, len(trends)*2)
	for _, trend := range trends {
		item := strings.Trim(strings.TrimSpace(trend.Item), "`'\"“”‘’")
		if item == "" {
			continue
		}
		tokens = append(tokens, item)
		if strings.HasPrefix(item, "呱") {
			tokens = append(tokens, "呱")
		}
	}
	return compactStrings(tokens)
}

// stripEmbeddedOptionalStyleMaterial handles the legacy case where a planner
// put a prescribed trend-language move and real events in one required beat.
// Only the clause carrying the style literal is removed; later hard aftermath
// is retained instead of losing everything after the first semicolon.
func stripEmbeddedOptionalStyleMaterial(text string, trends []domain.TrendLanguagePlan) string {
	text = strings.TrimSpace(text)
	tokens := renderTrendTokens(trends)
	if text == "" || len(tokens) == 0 || !containsRenderStyleMarker(text) {
		return text
	}
	containsTrend := func(value string) bool {
		for _, token := range tokens {
			if token != "" && strings.Contains(value, token) {
				return true
			}
		}
		return false
	}
	if !containsTrend(text) {
		return text
	}

	clauses := strings.FieldsFunc(text, func(r rune) bool {
		return r == '；' || r == ';' || r == '。' || r == '\n'
	})
	hard := make([]string, 0, len(clauses))
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if !containsTrend(clause) || !containsRenderStyleMarker(clause) {
			hard = append(hard, clause)
			continue
		}
		// A mixed clause often has the form "角色按指定热梗起头，随后真实
		// 后果". Preserve the hard suffix after the final comma when it no
		// longer carries the style instruction.
		if comma := strings.LastIndexAny(clause, "，,"); comma >= 0 {
			suffixStart := comma + 1
			if strings.HasPrefix(clause[comma:], "，") {
				suffixStart = comma + len("，")
			}
			suffix := strings.TrimSpace(clause[suffixStart:])
			if suffix != "" && !containsTrend(suffix) && !containsRenderStyleMarker(suffix) {
				hard = append(hard, suffix)
			}
		}
	}
	return strings.Join(hard, "；")
}

func containsRenderStyleMarker(text string) bool {
	for _, marker := range []string{"必须", "原句", "原样", "起头", "说出", "使用"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func sanitizeDraftWorldSimulation(result map[string]any, packetReady, authorityReceiptReady bool, packetPath string) {
	sim, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{"status", "simulation_id", "base_tick_id", "time_window", "character_count", "source_version_policy"} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	if coverage, exists := sim["rewrite_fact_coverage"]; exists {
		if authorityReceiptReady {
			lean["rewrite_fact_coverage_receipt"] = "formal_plan_receipt.rewrite_fact_coverage"
		} else {
			lean["rewrite_fact_coverage"] = compactPlanningRewriteFactCoverage(coverage)
			lean["rewrite_fact_coverage_policy"] = "preserve fact 原文保留；逐条 simulation evidence 已在 finalize 校验并折叠为 count+sha256。完整证据留在 meta/chapter_simulations，正文不得改写事实。"
		}
	}
	if source, exists := sim["rewrite_source"]; exists {
		if authorityReceiptReady {
			lean["rewrite_source_receipt"] = "formal_plan_receipt.rewrite_source"
		} else if binding := compactRewriteSourceBinding(source); len(binding) > 0 {
			lean["rewrite_source_binding"] = binding
		}
	}
	if rawProjection, exists := sim["protagonist_projection"]; exists && !packetReady {
		lean["protagonist_projection"] = sanitizeProtagonistProjection(rawProjection)
	}
	if packetReady {
		lean["render_policy"] = "protagonist_projection 已完整吸收到 " + packetPath + "；全角色决定和 hidden_pressures 已隐藏。所有 preserve facts、硬结果、知识边界、金额数量与先后顺序只读取该 packet，不从 receipt 重建。"
	} else {
		lean["render_policy"] = "仅渲染 protagonist_projection.observable_effects 与 knowledge-bound plan_constraints 允许主角获得的信息；全角色决定和 hidden_pressures 已隐藏。"
	}
	result["chapter_world_simulation"] = lean
}

func compactRewriteSourceBinding(raw any) map[string]any {
	source, ok := chapterRewriteSourceFromAny(raw)
	if !ok {
		return nil
	}
	binding := map[string]any{}
	if value := strings.TrimSpace(source.BodyPath); value != "" {
		binding["body_path"] = value
	}
	if value := strings.TrimSpace(source.BodySHA256); value != "" {
		binding["body_sha256"] = value
	}
	if source.WordCount > 0 {
		binding["word_count"] = source.WordCount
	}
	if value := strings.TrimSpace(source.BriefPath); value != "" {
		binding["brief_path"] = value
	}
	if value := strings.TrimSpace(source.BriefSHA256); value != "" {
		binding["brief_sha256"] = value
	}
	if value := strings.TrimSpace(source.CanonicalStatePath); value != "" {
		binding["canonical_state_path"] = value
	}
	if value := strings.TrimSpace(source.CanonicalStateSHA256); value != "" {
		binding["canonical_state_sha256"] = value
	}
	return binding
}

func sanitizeProtagonistProjection(value any) any {
	if projection, ok := draftProjectionFromAny(value); ok {
		return leanDraftProjection(projection)
	}
	return value
}

// leanDraftProjection keeps the protagonist's reason for acting and a few
// visible consequences. Options and full causal chains remain on disk: when
// prose sees them, it tends to serialize each item as a scene or line of
// dialogue. A bounded subset of exact knowledge/authorization constraints stays
// visible because it is the fail-closed POV boundary, not scene choreography.
func leanDraftProjection(projection draftProtagonistProjection) draftProtagonistProjection {
	effects := make([]string, 0, len(projection.ObservableEffects))
	for _, effect := range projection.ObservableEffects {
		if clause := firstRenderClause(effect); clause != "" {
			effects = append(effects, clause)
		}
	}
	return draftProtagonistProjection{
		Protagonist:       strings.TrimSpace(projection.Protagonist),
		ObservableEffects: sampleRenderStrings(effects, 3),
		ChosenDecision:    firstRenderClause(projection.ChosenDecision),
		DecisionReason:    firstRenderClause(projection.DecisionReason),
		PlanConstraints:   selectDraftKnowledgeConstraints(projection.PlanConstraints, 8),
	}
}

func selectDraftKnowledgeConstraints(constraints []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	markers := []string{
		"视角", "POV", "知识", "知道", "不知道", "不得推", "不得注意", "秘密",
		"未知", "待确认", "尚未", "授权", "同意", "位置", "地点", "支付渠道",
		"资金", "运力", "皮卡", "到场", "时间",
	}
	selected := make([]string, 0, min(limit, len(constraints)))
	for _, raw := range constraints {
		constraint := strings.TrimSpace(raw)
		if constraint == "" {
			continue
		}
		keep := false
		for _, marker := range markers {
			if strings.Contains(constraint, marker) {
				keep = true
				break
			}
		}
		if !keep {
			continue
		}
		selected = append(selected, constraint)
		if len(selected) >= limit {
			break
		}
	}
	return compactStrings(selected)
}

func draftProjectionFromAny(value any) (draftProtagonistProjection, bool) {
	switch projection := value.(type) {
	case domain.ProtagonistDecisionProjection:
		return draftProtagonistProjection{
			Protagonist:       projection.Protagonist,
			ObservableEffects: projection.ObservableEffects,
			AvailableOptions:  projection.AvailableOptions,
			ChosenDecision:    projection.ChosenDecision,
			DecisionReason:    projection.DecisionReason,
			PlanConstraints:   projection.PlanConstraints,
			CausalChain:       projection.CausalChain,
		}, true
	case *domain.ProtagonistDecisionProjection:
		if projection == nil {
			return draftProtagonistProjection{}, false
		}
		return draftProjectionFromAny(*projection)
	case map[string]any:
		raw, err := json.Marshal(projection)
		if err != nil {
			return draftProtagonistProjection{}, false
		}
		var lean draftProtagonistProjection
		if err := json.Unmarshal(raw, &lean); err != nil {
			return draftProtagonistProjection{}, false
		}
		return lean, true
	default:
		return draftProtagonistProjection{}, false
	}
}
