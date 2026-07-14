package tools

import (
	"encoding/json"
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
	Title                   string                           `json:"title,omitempty"`
	Goal                    string                           `json:"goal,omitempty"`
	Conflict                string                           `json:"conflict,omitempty"`
	Hook                    string                           `json:"hook,omitempty"`
	EmotionArc              string                           `json:"emotion_arc,omitempty"`
	MandatoryBeats          []string                         `json:"mandatory_beats,omitempty"`
	OptionalStyleBeats      []string                         `json:"optional_style_beats,omitempty"`
	ForbiddenMoves          []string                         `json:"forbidden_moves,omitempty"`
	ContinuityChecks        []string                         `json:"continuity_checks,omitempty"`
	PayoffPoints            []string                         `json:"payoff_points,omitempty"`
	SceneAnchors            []string                         `json:"scene_anchors,omitempty"`
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
	EnvironmentSignals      []draftEnvironmentSignal         `json:"environment_signals,omitempty"`
	EndingContract          domain.EndingConsequenceContract `json:"ending_consequence_contract,omitempty"`
	EndingAnchorCandidate   string                           `json:"ending_anchor_candidate,omitempty"`
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
	RiskSignals  []string `json:"risk_signals,omitempty"`
	CounterMoves []string `json:"counter_moves,omitempty"`
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
	Character        string   `json:"character,omitempty"`
	ImmediateState   string   `json:"immediate_state,omitempty"`
	PrimaryEmotion   string   `json:"primary_emotion,omitempty"`
	Trigger          string   `json:"trigger,omitempty"`
	GoalAppraisal    string   `json:"goal_appraisal,omitempty"`
	Regulation       string   `json:"regulation,omitempty"`
	EmotionLedAction string   `json:"emotion_led_action,omitempty"`
	SceneEvidence    []string `json:"scene_evidence,omitempty"`
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
	PerceptualBias        string                    `json:"perceptual_bias,omitempty"`
	SceneModes            []draftLiterarySceneMode  `json:"scene_modes,omitempty"`
	ActiveLenses          []draftLiteraryActiveLens `json:"active_lenses,omitempty"`
	SummaryOmissionPolicy string                    `json:"summary_omission_policy,omitempty"`
	Afterimage            string                    `json:"afterimage,omitempty"`
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
	// The simulator needs every character's current state and the shared world,
	// but not prose craft, prior render packets or project-level progress reports.
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "side_character_journeys", "future_outline_window",
		"recent_summaries", "previous_tail", "references", "writing_engine",
		"literary_rendering_cards", "genre_style_profile", "style_rules", "style_stats", "voice_samples", "rag_recall", "retrieval_trace",
		"chapter_plan", "chapter_contract", "causal_simulation", "next_plan",
	} {
		deleteContextKey(result, key)
	}
	result["world_simulation_context_policy"] = "保留全部实名角色的当前状态、知识边界、关系、资源和本章大纲；写法资料、正文渲染历史与重复项目报告已隐藏。"
}

func applyPlanningContextProfile(result map[string]any) {
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
	sanitizePlanningWorldSimulation(result)
	result["planning_context_policy"] = "全角色决定已落盘；本阶段只依据 protagonist_projection、当前章/未来窗口、角色声口与审核约束生成主视角 plan。"
}

func applyDraftContextProfile(result map[string]any) {
	working, _ := result["working_memory"].(map[string]any)
	compactDraftRewriteBrief(result)
	compactDraftRewriteSource(result)
	if working != nil {
		compactDraftRewriteBrief(working)
		compactDraftRewriteSource(working)
	}
	sanitizeDraftExternalReview(result)
	plan := chapterPlanFromContext(result, working)
	if plan != nil {
		packet := newDraftRenderPacket(*plan)
		packet.StyleContract = newDraftStyleContract(result)
		if simulation, ok := result["chapter_world_simulation"].(map[string]any); ok {
			if projection, ok := draftProjectionFromAny(simulation["protagonist_projection"]); ok {
				packet.ProtagonistProjection = leanDraftProjection(projection)
			}
			packet.VisibleCharacters, packet.ExcludedNamedCharacters = draftVisibilityFromSimulation(simulation)
		}
		result["render_packet"] = packet
		if working != nil {
			working["render_packet"] = packet
		}

		leanPlan := map[string]any{
			"chapter":       plan.Chapter,
			"title":         plan.Title,
			"goal":          plan.Goal,
			"conflict":      plan.Conflict,
			"hook":          plan.Hook,
			"render_policy": "本对象只用于确认章号与目标；章节合同已经完整投影到 render_packet，正文不得展开旧 contract 或 causal_simulation。",
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
	sanitizeDraftWorldSimulation(result)
	for _, key := range []string{
		"outline", "progression_snapshot", "project_progress", "evolution_report",
		"chapter_world_deltas", "character_stage_records", "side_character_journeys",
		"premise", "world_rules", "characters", "character_dossiers",
		"future_outline_window", "next_chapter_outline",
	} {
		deleteContextKey(result, key)
	}
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
	container["rewrite_brief"] = compact
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
	if !ok || sim["status"] != "ready" {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{
		"status", "simulation_id", "base_tick_id", "time_window", "character_count",
		"protagonist_projection", "rewrite_source", "rewrite_fact_coverage",
	} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	lean["planning_policy"] = "完整 character_decisions 已在 meta/chapter_simulations 落盘；POV plan 只能投影 protagonist_projection，不得重写隐藏角色决定。"
	result["chapter_world_simulation"] = lean
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
	protagonist := ""
	if len(sim.InitialState) > 0 {
		protagonist = sim.InitialState[0].Character
	}
	projection := leanDraftProjection(draftProtagonistProjection{
		Protagonist:       protagonist,
		ChosenDecision:    sim.ProtagonistDecision,
		DecisionReason:    plan.Goal,
		PlanConstraints:   sim.SceneConstraints,
		ObservableEffects: append([]string(nil), plan.Contract.RequiredBeats...),
	})
	literaryContract := newDraftLiteraryRenderContract(plan, protagonist)
	return draftRenderPacket{
		Version:               6,
		Chapter:               plan.Chapter,
		Title:                 plan.Title,
		Goal:                  firstRenderClause(plan.Goal),
		Conflict:              firstRenderClause(plan.Conflict),
		Hook:                  plan.Hook,
		EmotionArc:            firstRenderClause(plan.EmotionArc),
		MandatoryBeats:        mandatoryBeats,
		ForbiddenMoves:        compactStrings(plan.Contract.ForbiddenMoves),
		ContinuityChecks:      RenderContinuityChecks(plan),
		PayoffPoints:          limitRenderStrings(plan.Contract.PayoffPoints, 4),
		SceneAnchors:          renderSceneAnchors(plan.Contract.SceneAnchors),
		ProtagonistProjection: projection,
		VoiceCards:            selectEssentialVoiceCards(voices),
		// A first chapter can introduce the protagonist, love interest, parents,
		// pressure character and two or three local anchors in one continuous
		// opening. Keep enough cards for every meaningful entrance instead of
		// silently dropping the later (often more important) arrivals.
		VisualCards:             selectEntranceVisualCards(visuals, 8),
		RelationshipLenses:      sampleRenderValues(relationshipLenses, 1),
		LiteraryRenderContract:  literaryContract,
		SelectionPolicy:         "mandatory_beats 只是终局事实，不是镜头清单；只挑最有情绪、笑点或关系变化的场面写，其余允许离屏成立。",
		SceneBridgePolicy:       "转场服从人物注意力；能自然跳过就跳过，不为解释时间和因果另写汇报段。",
		DialogueTopologyPolicy:  "先朗读再保留台词；人物只说此刻会说的话，不替作者补全背景、规则和步骤，也不强制轮流发言。普通平静口述按完整气口说完，同一意思不得切成连续2—4汉字句号短句。",
		SystemVoicePolicy:       "若本章存在系统、界面或非人媒介，它只能按既定声口回应眼前具体刺激；不复述读者刚看见的结果，不用客服套话替人物作决定。",
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

func newDraftLiteraryRenderContract(plan domain.ChapterPlan, protagonist string) draftLiteraryRenderContract {
	const usagePolicy = "这是本章已选择的镜头决定，不是技法清单：信息权限为硬边界；距离、场景/概述、意象、句法、自由间接话语和潜台词只在其目标位置按功能使用，不补齐未选卡，不统计次数。"
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
		return contract
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
	return limitRenderStrings(out, 4)
}

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
		RiskSignals:  limitRenderStrings(plan.RiskSignals, 4),
		CounterMoves: limitRenderStrings(plan.CounterMoves, 4),
	}
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

// RenderRequiredOutcomes projects the full chapter contract into the smallest
// result-level contract needed by prose. The source plan remains untouched and
// keeps every simulation fact; only the Drafter-facing view removes duplicate
// outline anchors and prefers concise outcome statements over process recipes.
func RenderRequiredOutcomes(plan domain.ChapterPlan) []string {
	beats, _ := splitOptionalStyleBeats(plan.Contract.RequiredBeats, plan.CausalSimulation.TrendLanguage)
	out := make([]string, 0, len(beats))
	for _, raw := range beats {
		beat := unwrapRenderOutcome(raw)
		beat = firstRenderClause(beat)
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
	// Prose should see an opening pressure, one central human/system payoff and
	// the final consequence. The full contract remains on disk for consistency
	// review; feeding every process beat to the Drafter turns the chapter into a
	// shot-by-shot acceptance checklist.
	return selectEssentialRenderOutcomes(compactStrings(out), 3)
}

func selectEssentialRenderOutcomes(values []string, limit int) []string {
	values = compactStrings(values)
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) <= limit {
		return append([]string(nil), values...)
	}

	selected := map[int]bool{0: true, len(values) - 1: true}
	for len(selected) < limit {
		bestIndex, bestScore := -1, -1
		for i := 1; i < len(values)-1; i++ {
			if selected[i] {
				continue
			}
			score := renderOutcomeHumanPriority(values[i])
			if score > bestScore {
				bestIndex, bestScore = i, score
			}
		}
		if bestIndex < 0 {
			break
		}
		selected[bestIndex] = true
	}

	out := make([]string, 0, len(selected))
	for i, value := range values {
		if selected[i] {
			out = append(out, value)
		}
	}
	return out
}

func renderOutcomeHumanPriority(text string) int {
	score := 0
	for _, marker := range []string{
		"系统", "奖励", "个人账户", "秘密", "坦白", "信任", "怀疑", "关系", "感情",
		"父亲", "母亲", "朋友", "女主", "男主", "沈知遥", "贺骁",
	} {
		if strings.Contains(text, marker) {
			score += 10
		}
	}
	for _, marker := range []string{"票据", "逐笔", "安装", "运输", "登记", "测试", "核验", "名单"} {
		if strings.Contains(text, marker) {
			score--
		}
	}
	return score
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
	return candidateLen < currentLen
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
	trendTokens := make([]string, 0, len(trends)*2)
	for _, trend := range trends {
		item := strings.Trim(strings.TrimSpace(trend.Item), "`'\"“”‘’")
		if item == "" {
			continue
		}
		trendTokens = append(trendTokens, item)
		if strings.HasPrefix(item, "呱") {
			trendTokens = append(trendTokens, "呱")
		}
	}
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

func containsRenderStyleMarker(text string) bool {
	for _, marker := range []string{"必须", "原句", "原样", "起头", "说出", "使用"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func sanitizeDraftWorldSimulation(result map[string]any) {
	sim, ok := result["chapter_world_simulation"].(map[string]any)
	if !ok {
		return
	}
	lean := map[string]any{}
	for _, key := range []string{"status", "simulation_id", "base_tick_id", "time_window", "character_count", "rewrite_source", "rewrite_fact_coverage", "source_version_policy"} {
		if value, exists := sim[key]; exists {
			lean[key] = value
		}
	}
	if rawProjection, exists := sim["protagonist_projection"]; exists {
		lean["protagonist_projection"] = sanitizeProtagonistProjection(rawProjection)
	}
	lean["render_policy"] = "仅渲染 protagonist_projection.observable_effects 与主角合法获得的信息；全角色决定和 hidden_pressures 已从 draft profile 隐藏。"
	result["chapter_world_simulation"] = lean
}

func sanitizeProtagonistProjection(value any) any {
	if projection, ok := draftProjectionFromAny(value); ok {
		return leanDraftProjection(projection)
	}
	return value
}

// leanDraftProjection keeps the protagonist's reason for acting and a few
// visible consequences. Options, full causal chains and planning constraints
// remain on disk: when prose sees them, it tends to serialize each item as a
// scene or a line of dialogue even though mandatory_beats already define scope.
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
	}
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
