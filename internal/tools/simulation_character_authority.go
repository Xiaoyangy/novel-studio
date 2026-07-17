package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	simulationAuthorityUnknown       = "unknown"
	simulationAuthorityHoldBaseline  = "hold_baseline"
	simulationAuthorityWait          = "wait_for_authoritative_state"
	simulationAuthorityMissing       = "authority_missing"
	simulationAuthorityNoEffect      = "no_chapter_effect"
	simulationAuthorityUnchanged     = "unchanged_authoritative_baseline"
	simulationAuthorityNotApplicable = "not_applicable"
	simulationAuthorityBlockedEffect = "transmission_blocked"
	simulationAuthorityNoImpact      = "none"
	simulationAuthorityUnstated      = "unstated_in_rewrite_source"
	simulationAuthorityEvidenceOnly  = "explicit_rewrite_source_evidence_only"
	simulationAuthorityPreserve      = "preserve_explicit_source_action"
	simulationAuthorityNoAlternative = "no_unstated_alternative"
	simulationAuthoritySourceResult  = "explicit_source_action_preserved"
	simulationAuthoritySourceEffect  = "source_visibility_preserved"
	simulationAuthorityDirectSource  = "direct_rewrite_source_observation"
	simulationAuthorityNoInference   = "no_new_inference"
	simulationAuthorityNotSpecified  = "not_specified_in_rewrite_source"
)

const (
	projectAllReuseDecisionPolicy            = "该角色决定已在当前 partial 落盘；只读校验，不得重发、改写或用本摘要覆盖。"
	projectAllGroundedDecisionPolicy         = "本角色由服务端 project_all_grounded authority receipt 授权。必须使用 current_goal、current_pressure、resources、current_action、decision_model、本章大纲和当前 project_all_state 推演；project_all_state/连续态覆盖零章种子。current_goal 与 resources 逐项原样复制；knowledge_boundary 必须严格等于本对象的 knowledge_boundary 加 required_knowledge_boundaries，不得追加任何知识或秘密；current_pressure_policy=exact_continuity 时原样复制，=outline_authorized_concise 时从 current_pressure 或本章大纲中截取一条完整短句，不得概括。location 必须是逐字来自上一连续态、本章大纲或 book_world 的紧凑空间锚点，不超过 32 个 Unicode 字符，不得包含，。；！？或换行，禁止复制整段场景概述。decision 与 action 是当前具体行动合同，不得整句等于 current_goal；二者必须逐字来自具体的 current_action，或本章大纲中的一条具体行动句，不得用目标、主题或概述冒充动作。decision_reason 与 available_options 的其他项、immediate_result、state_after、butterfly_effect.effect、protagonist_impact 等 projected output 必须保留至少两个输入因果锚点，不得引入输入中不存在的人名、地点、资源、数量、秘密、后台规则或世界机制，也不得使用事后信息或 arc 中尚未发生的未来结果。所有 projected output 会进入服务端 decision root、authority receipt、SimulationID 与后续 delta gate，禁止用一段合法前缀夹带新秘密。"
	projectAllRewriteOnlyDecisionPolicy      = "把角色实名放入 simulate_chapter_world.authority_contract_characters；服务端将逐字段物化 rewrite_source_only_contract。action 已优先取自 rewrite_source.preserve_facts 的角色明确动作、其次取正文原句；不得手抄、概括或扩写。"
	projectAllHoldBaselineDecisionPolicy     = "把角色实名放入 simulate_chapter_world.authority_contract_characters；服务端将逐字段物化 hold_baseline_contract。不得手抄或补职业、地点、关系、资源、通信、动机与未来行动。"
	projectAllAuthoritativeDecisionPolicy    = "只使用本摘要列出的权威事实和本章可见证据推进；不得把 arc 中的未来结果提前当成当前事实。"
	projectAllRequiredKnowledgePolicy        = " knowledge_boundary 必须逐条原样包含 required_knowledge_boundaries；这是 preserve_facts 的独立知识锁，不能删除、弱化或改成可能知道。"
	projectAllAuthoritySummaryPolicyTemplate = "名单与摘要一一对应。blocking=%d 的角色不得补猜或手抄合同；只把实名放入 simulate_chapter_world.authority_contract_characters，由服务端按 authority_mode 物化 rewrite_source_only 或 hold_baseline。unknown 是有效边界，不是等待模型填空。"
)

// ProjectAllSimulationAuthorityProtocolDigest returns a stable dependency root
// for every model-visible authority policy. Project-all generation identity can
// include this root so policy changes cannot resume an older generation under
// a different simulation contract.
func ProjectAllSimulationAuthorityProtocolDigest() string {
	digest, err := domain.DeterministicPlanningHash(struct {
		Version           string `json:"version"`
		Reuse             string `json:"reuse"`
		Grounded          string `json:"grounded"`
		RewriteSourceOnly string `json:"rewrite_source_only"`
		HoldBaseline      string `json:"hold_baseline"`
		Authoritative     string `json:"authoritative"`
		RequiredKnowledge string `json:"required_knowledge"`
		AuthoritySummary  string `json:"authority_summary"`
	}{
		Version:           "project-all-simulation-authority-protocol.v2",
		Reuse:             projectAllReuseDecisionPolicy,
		Grounded:          projectAllGroundedDecisionPolicy,
		RewriteSourceOnly: projectAllRewriteOnlyDecisionPolicy,
		HoldBaseline:      projectAllHoldBaselineDecisionPolicy,
		Authoritative:     projectAllAuthoritativeDecisionPolicy,
		RequiredKnowledge: projectAllRequiredKnowledgePolicy,
		AuthoritySummary:  projectAllAuthoritySummaryPolicyTemplate,
	})
	if err != nil {
		return ""
	}
	return digest
}

// effectiveProtagonistDecision removes authority-store sentinels from the POV
// contract. A blocking protagonist may keep an exact no-inference sentinel in
// character_decisions, but protagonist_projection must still contain an
// independently authored, source-bound human-readable choice. Returning empty
// when none exists makes simulation finalization fail closed instead of
// laundering decision_reason or another internal sentinel into a formal plan.
func effectiveProtagonistDecision(projection domain.ProtagonistDecisionProjection) string {
	decision := strings.TrimSpace(projection.ChosenDecision)
	if decision != "" && !simulationAuthorityDecisionPlaceholder(decision) {
		return decision
	}
	return ""
}

func planningProtagonistProjection(projection domain.ProtagonistDecisionProjection) domain.ProtagonistDecisionProjection {
	projection.ChosenDecision = effectiveProtagonistDecision(projection)
	return projection
}

func simulationAuthorityDecisionPlaceholder(value string) bool {
	switch strings.TrimSpace(value) {
	case simulationAuthorityUnknown,
		simulationAuthorityHoldBaseline,
		simulationAuthorityWait,
		simulationAuthorityMissing,
		simulationAuthorityNoEffect,
		simulationAuthorityUnchanged,
		simulationAuthorityNotApplicable,
		simulationAuthorityBlockedEffect,
		simulationAuthorityNoImpact,
		simulationAuthorityUnstated,
		simulationAuthorityEvidenceOnly,
		simulationAuthorityPreserve,
		simulationAuthorityNoAlternative,
		simulationAuthoritySourceResult,
		simulationAuthoritySourceEffect,
		simulationAuthorityDirectSource,
		simulationAuthorityNoInference,
		simulationAuthorityNotSpecified:
		return true
	default:
		return false
	}
}

// simulationCharacterAuthority is the compact, authoritative identity packet
// paired one-for-one with simulation_characters. Full dossiers are too large
// and easy to lose in the middle of a focused context; omitting them makes the
// simulator guess jobs, locations and relationships for off-screen actors.
// Unknown remains a first-class value here and must never be completed by
// improvisation.
type simulationCharacterAuthority struct {
	Character                 string                       `json:"character"`
	Role                      string                       `json:"role,omitempty"`
	Tier                      string                       `json:"tier,omitempty"`
	Aliases                   []string                     `json:"aliases,omitempty"`
	Description               string                       `json:"description,omitempty"`
	Traits                    []string                     `json:"traits,omitempty"`
	Desires                   []string                     `json:"desires,omitempty"`
	Boundaries                []string                     `json:"boundaries,omitempty"`
	Arc                       string                       `json:"arc,omitempty"`
	CurrentLocation           string                       `json:"current_location"`
	CurrentStatus             string                       `json:"current_status"`
	CurrentGoal               string                       `json:"current_goal,omitempty"`
	CurrentAction             string                       `json:"current_action"`
	CurrentPressure           string                       `json:"current_pressure"`
	CurrentPressurePolicy     string                       `json:"current_pressure_policy,omitempty"`
	NextIndependentMove       string                       `json:"next_independent_move"`
	Resources                 []string                     `json:"resources,omitempty"`
	Relationships             []string                     `json:"relationships,omitempty"`
	KnowledgeBoundary         string                       `json:"knowledge_boundary,omitempty"`
	RequiredKnowledgeBoundary []string                     `json:"required_knowledge_boundaries,omitempty"`
	DecisionModel             string                       `json:"decision_model,omitempty"`
	CommunicationBoundary     domain.CommunicationBoundary `json:"communication_boundary,omitempty"`
	VisibleInCurrentChapter   bool                         `json:"visible_in_current_chapter,omitempty"`
	SimulationStatus          string                       `json:"simulation_status"`
	AuthorityMode             string                       `json:"authority_mode"`
	AuthoritySources          []string                     `json:"authority_sources"`
	MissingAuthority          []string                     `json:"missing_authority,omitempty"`
	Blocking                  bool                         `json:"blocking"`
	DecisionPolicy            string                       `json:"decision_policy"`
	HoldBaselineContract      map[string]any               `json:"hold_baseline_contract,omitempty"`
	RewriteSourceEvidence     []string                     `json:"rewrite_source_evidence,omitempty"`
	RewriteSourceOnlyContract map[string]any               `json:"rewrite_source_only_contract,omitempty"`
	projectAllGrounded        bool                         `json:"-"`
	baseBlocking              bool                         `json:"-"`
}

func buildSimulationCharacterAuthority(st *store.Store, chapter int) []simulationCharacterAuthority {
	if st == nil || chapter <= 0 {
		return nil
	}
	required := requiredDossierCharacterNames(st, chapter)
	visible := chapterOutlineCharacterNames(st, chapter)
	visibleSet := make(map[string]bool, len(visible))
	for _, name := range visible {
		visibleSet[strings.TrimSpace(name)] = true
	}
	present := map[string]bool{}
	if partial, err := st.LoadChapterWorldSimulationPartial(chapter); err == nil && partial != nil {
		for _, name := range characterDecisionNames(partial.CharacterDecisions) {
			present[name] = true
		}
	}

	characters := map[string]domain.Character{}
	if all, err := st.Characters.Load(); err == nil {
		for _, character := range all {
			characters[strings.TrimSpace(character.Name)] = character
		}
	}
	dossiers := map[string]domain.CharacterDossier{}
	if all, err := st.LoadAllCharacterDossiers(); err == nil || len(all) > 0 {
		for _, dossier := range all {
			dossiers[strings.TrimSpace(dossier.Character)] = dossier
		}
	}
	cast := map[string]domain.CastEntry{}
	if all, err := st.Cast.Load(); err == nil {
		canonical := canonicalCharacterIdentityMap(st)
		for _, entry := range all {
			name := strings.TrimSpace(entry.Name)
			if resolved := canonical[name]; resolved != "" {
				name = resolved
			}
			cast[name] = entry
		}
	}
	projectAllInputs, projectAllErr := loadProjectAllAuthorityInputs(st, chapter)
	projectAllFresh := projectAllErr == nil && projectAllInputs != nil

	result := make([]simulationCharacterAuthority, 0, len(required))
	for _, name := range required {
		entry := simulationCharacterAuthority{
			Character:               name,
			CurrentLocation:         simulationAuthorityUnknown,
			CurrentStatus:           simulationAuthorityUnknown,
			CurrentAction:           simulationAuthorityUnknown,
			CurrentPressure:         simulationAuthorityUnknown,
			NextIndependentMove:     simulationAuthorityUnknown,
			VisibleInCurrentChapter: visibleSet[name],
			SimulationStatus:        "required_missing",
		}
		if present[name] {
			entry.SimulationStatus = "already_present"
		}
		if character, ok := characters[name]; ok {
			entry.Role = strings.TrimSpace(character.Role)
			entry.Tier = strings.TrimSpace(character.Tier)
			entry.Aliases = compactStrings(character.Aliases)
			entry.Description = strings.TrimSpace(character.Description)
			entry.Traits = limitAuthorityStrings(character.Traits, 6)
			entry.Arc = strings.TrimSpace(character.Arc)
			entry.AuthoritySources = append(entry.AuthoritySources, "characters.json:"+name)
		}
		projectAllGroundingComplete := false
		projectAllResourceBoundary := false
		if dossier, ok := dossiers[name]; ok {
			entry.AuthoritySources = append(entry.AuthoritySources, "meta/characters/"+name+"/dossier.json")
			if entry.Role == "" {
				entry.Role = strings.TrimSpace(dossier.Role)
			}
			if entry.Tier == "" {
				entry.Tier = strings.TrimSpace(dossier.Tier)
			}
			entry.Aliases = appendAuthorityUnique(entry.Aliases, dossier.Aliases...)
			if description := authoritativeSimulationText(dossier.Profile.Description); description != "" {
				entry.Description = description
			}
			if len(dossier.Profile.Traits) > 0 {
				entry.Traits = limitAuthorityStrings(dossier.Profile.Traits, 6)
			}
			entry.Desires = limitAuthorityStrings(authoritativeSimulationStrings(dossier.Profile.Desires), 5)
			entry.Boundaries = limitAuthorityStrings(authoritativeSimulationStrings(dossier.Profile.Boundaries), 5)
			if arc := authoritativeSimulationText(dossier.Profile.Arc); arc != "" {
				entry.Arc = arc
			}
			if location := authoritativeSimulationText(dossier.CurrentAtStoryStart.Location); location != "" {
				entry.CurrentLocation = location
			}
			if status := authoritativeSimulationText(dossier.CurrentAtStoryStart.Status); status != "" {
				entry.CurrentStatus = status
			}
			if action := authoritativeSimulationText(dossier.CurrentAtStoryStart.CurrentAction); action != "" {
				entry.CurrentAction = action
			}
			if pressure := authoritativeSimulationText(dossier.CurrentAtStoryStart.Pressure); pressure != "" {
				entry.CurrentPressure = pressure
			}
			if move := authoritativeSimulationText(dossier.CurrentAtStoryStart.NextIndependentMove); move != "" {
				entry.NextIndependentMove = move
			}
			for _, resource := range dossier.Resources {
				resourceName := authoritativeSimulationText(resource.Name)
				if resourceName == "" || resourceName == "故事开始前经验/身份资源" {
					continue
				}
				if status := authoritativeSimulationText(resource.Status); status != "" {
					resourceName += "（" + status + "）"
				}
				entry.Resources = append(entry.Resources, resourceName)
			}
			entry.Resources = limitAuthorityStrings(compactStrings(entry.Resources), 5)
			entry.Relationships = compactSimulationRelationships(dossier.Relationships, 5)
			entry.KnowledgeBoundary = authoritativeSimulationText(dossier.KnowledgeBoundary)
			entry.DecisionModel = authoritativeSimulationText(dossier.DecisionModel)
			entry.CommunicationBoundary = dossier.CommunicationBoundary
		} else {
			entry.MissingAuthority = append(entry.MissingAuthority, "dossier")
			if castEntry, ok := cast[name]; ok {
				entry.AuthoritySources = append(entry.AuthoritySources, "meta/cast_ledger.json:"+name)
				if entry.Role == "" {
					entry.Role = strings.TrimSpace(castEntry.BriefRole)
				}
				if entry.Description == "" {
					entry.Description = strings.TrimSpace(castEntry.BriefRole)
				}
			}
		}
		if projectAllFresh && entry.VisibleInCurrentChapter {
			dynamics, hasDynamics := projectAllInputs.InitialDynamics[name]
			switch {
			case projectAllInputs.InitialDynamicsSeedActive:
				if hasDynamics && completeInitialCharacterDynamics(dynamics) {
					projectAllGroundingComplete = true
					projectAllResourceBoundary = true
					entry.CurrentGoal = strings.TrimSpace(dynamics.CurrentGoal)
					entry.CurrentPressure = strings.TrimSpace(dynamics.Pressure)
					entry.CurrentPressurePolicy = "outline_authorized_concise"
					// LikelyAction is a shared zero-init template in many
					// projects. ActionTendency is tied to the exact character
					// card and is therefore the stronger grounding source.
					entry.CurrentAction = firstAuthorityText(dynamics.ActionTendency, dynamics.LikelyAction)
					entry.Resources = concreteInitialDynamicsResources(dynamics.Resources)
					entry.DecisionModel = strings.TrimSpace(dynamics.DecisionFrame.DecisionRule)
					entry.AuthoritySources = append(entry.AuthoritySources,
						"meta/initial_character_dynamics.json:"+name,
						projectAllStateContextPath+":"+projectAllInputs.Context.ContextDigest,
					)
				}
			default:
				continuity, hasContinuity := projectAllInputs.Continuity[name]
				if hasContinuity && continuity.DecisionModel == "" &&
					hasDynamics && completeInitialCharacterDynamics(dynamics) {
					continuity.DecisionModel = strings.TrimSpace(dynamics.DecisionFrame.DecisionRule)
					continuity.Sources = append(continuity.Sources,
						"meta/initial_character_dynamics.json:"+name+":decision_frame",
					)
				}
				if hasContinuity && completeProjectAllCharacterContinuity(continuity) {
					projectAllGroundingComplete = true
					projectAllResourceBoundary = continuity.ResourceBoundary
					entry.CurrentLocation = firstAuthorityText(continuity.CurrentLocation, entry.CurrentLocation)
					entry.CurrentStatus = firstAuthorityText(continuity.CurrentStatus, entry.CurrentStatus)
					entry.CurrentGoal = strings.TrimSpace(continuity.CurrentGoal)
					entry.CurrentPressure = strings.TrimSpace(continuity.CurrentPressure)
					entry.CurrentPressurePolicy = "exact_continuity"
					entry.CurrentAction = strings.TrimSpace(continuity.CurrentAction)
					entry.Resources = compactStrings(continuity.Resources)
					entry.KnowledgeBoundary = strings.TrimSpace(continuity.KnowledgeBoundary)
					entry.DecisionModel = strings.TrimSpace(continuity.DecisionModel)
					entry.AuthoritySources = append(entry.AuthoritySources, continuity.Sources...)
				} else if !hasContinuity && hasDynamics &&
					completeInitialCharacterDynamics(dynamics) {
					// A character held offscreen by earlier chapters has no
					// non-sentinel continuity yet. Its actor-specific zero seed
					// remains valid for first entry; any real prior delta, even
					// if incomplete, disables this fallback.
					projectAllGroundingComplete = true
					projectAllResourceBoundary = true
					entry.CurrentGoal = strings.TrimSpace(dynamics.CurrentGoal)
					entry.CurrentPressure = strings.TrimSpace(dynamics.Pressure)
					entry.CurrentPressurePolicy = "outline_authorized_concise"
					entry.CurrentAction = firstAuthorityText(dynamics.ActionTendency, dynamics.LikelyAction)
					entry.Resources = concreteInitialDynamicsResources(dynamics.Resources)
					entry.DecisionModel = strings.TrimSpace(dynamics.DecisionFrame.DecisionRule)
					entry.AuthoritySources = append(entry.AuthoritySources,
						"meta/initial_character_dynamics.json:"+name+":first_entry_fallback",
						projectAllStateContextPath+":"+projectAllInputs.Context.ContextDigest,
					)
				}
			}
		}

		if entry.CurrentLocation == simulationAuthorityUnknown {
			entry.MissingAuthority = append(entry.MissingAuthority, "current_location")
		}
		if entry.CurrentStatus == simulationAuthorityUnknown {
			entry.MissingAuthority = append(entry.MissingAuthority, "current_status")
		}
		if entry.CurrentAction == simulationAuthorityUnknown {
			entry.MissingAuthority = append(entry.MissingAuthority, "current_action")
		}
		if entry.CurrentPressure == simulationAuthorityUnknown {
			entry.MissingAuthority = append(entry.MissingAuthority, "pressure")
		}
		if entry.CurrentGoal == "" && len(entry.Desires) == 0 && entry.NextIndependentMove == simulationAuthorityUnknown {
			entry.MissingAuthority = append(entry.MissingAuthority, "current_goal")
		}
		if len(entry.Resources) == 0 && !projectAllResourceBoundary {
			entry.MissingAuthority = append(entry.MissingAuthority, "resources")
		}
		if entry.KnowledgeBoundary == "" {
			entry.MissingAuthority = append(entry.MissingAuthority, "knowledge_boundary")
		}
		if entry.DecisionModel == "" {
			entry.MissingAuthority = append(entry.MissingAuthority, "decision_model")
		}
		entry.MissingAuthority = compactStrings(entry.MissingAuthority)
		entry.RequiredKnowledgeBoundary = preserveKnowledgeBoundaryClauses(st, chapter, name)
		entry.baseBlocking = len(entry.MissingAuthority) > 0
		entry.Blocking = entry.baseBlocking && entry.SimulationStatus != "already_present"
		projectAllGrounded := projectAllGroundingComplete &&
			onlyChapterLocalPlacementMissing(entry.MissingAuthority)
		if projectAllGrounded {
			entry.Blocking = false
		}
		entry.projectAllGrounded = projectAllGrounded
		switch {
		case entry.SimulationStatus == "already_present":
			entry.AuthorityMode = "reuse_saved_decision"
			entry.DecisionPolicy = projectAllReuseDecisionPolicy
		case projectAllGrounded:
			entry.AuthorityMode = "project_all_grounded"
			entry.DecisionPolicy = projectAllGroundedDecisionPolicy
		case entry.Blocking && entry.VisibleInCurrentChapter && !projectAllFresh:
			entry.AuthorityMode = "rewrite_source_only"
			entry.RewriteSourceEvidence = rewriteSourceEvidenceForCharacter(st, chapter, name)
			entry.RewriteSourceOnlyContract = rewriteSourceOnlyContractPayload(name, chapter, entry.RewriteSourceEvidence, entry.RequiredKnowledgeBoundary)
			entry.DecisionPolicy = projectAllRewriteOnlyDecisionPolicy
		case entry.Blocking:
			entry.AuthorityMode = "hold_baseline"
			entry.DecisionPolicy = projectAllHoldBaselineDecisionPolicy
			entry.HoldBaselineContract = holdBaselineContractPayload(name, chapter, entry.RequiredKnowledgeBoundary)
		default:
			entry.AuthorityMode = "authoritative"
			entry.DecisionPolicy = projectAllAuthoritativeDecisionPolicy
		}
		if len(entry.RequiredKnowledgeBoundary) > 0 && entry.SimulationStatus != "already_present" {
			entry.DecisionPolicy += projectAllRequiredKnowledgePolicy
		}
		entry.AuthoritySources = compactStrings(entry.AuthoritySources)
		result = append(result, entry)
	}
	return result
}

func loadInitialCharacterDynamics(st *store.Store) map[string]domain.CharacterSimulationState {
	result := map[string]domain.CharacterSimulationState{}
	if st == nil {
		return result
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "initial_character_dynamics.json"))
	if err != nil {
		return result
	}
	var document struct {
		Characters []domain.CharacterSimulationState `json:"characters"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return result
	}
	for _, state := range document.Characters {
		name := strings.TrimSpace(state.Character)
		if name != "" {
			result[name] = state
		}
	}
	return result
}

func completeInitialCharacterDynamics(state domain.CharacterSimulationState) bool {
	return strings.TrimSpace(state.Character) != "" &&
		strings.TrimSpace(state.CurrentGoal) != "" &&
		strings.TrimSpace(state.Pressure) != "" &&
		strings.TrimSpace(state.ActionTendency) != "" &&
		strings.TrimSpace(state.DecisionFrame.DecisionRule) != "" &&
		len(compactStrings(state.KnowledgeLedger.KnownFacts)) > 0 &&
		len(compactStrings(state.KnowledgeLedger.ForbiddenKnowledge)) > 0
}

func concreteInitialDynamicsResources(values []string) []string {
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch value {
		case "", "角色卡既有经验", "第一章可见事实", "可复核的关系/资源台账",
			"离屏日程与职业资源", "首次入场前已授权的关系台账":
			continue
		default:
			result = appendAuthorityUnique(result, value)
		}
	}
	return limitAuthorityStrings(result, 5)
}

func firstAuthorityText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func onlyChapterLocalPlacementMissing(missing []string) bool {
	for _, field := range missing {
		switch strings.TrimSpace(field) {
		case "current_location", "current_status":
		default:
			return false
		}
	}
	return true
}

func validateIncomingSimulationCharacterAuthority(st *store.Store, chapter int, decisions []domain.CharacterWorldDecision) error {
	if st == nil || chapter <= 0 || len(decisions) == 0 {
		return nil
	}
	// Legacy/imported projects without any dossier corpus cannot opt into the
	// authority sentinel yet: their only source may be the current tool call.
	// Once at least one dossier exists, the project has declared the
	// authoritative workflow and every unresolved off-screen actor must fail
	// closed rather than mixing strict and guessed decisions.
	dossiers, dossierErr := st.LoadAllCharacterDossiers()
	if len(dossiers) == 0 {
		if dossierErr == nil || errors.Is(dossierErr, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("simulation authority guard 无法读取已存在的角色档案，拒绝降级为自由补写: %w", dossierErr)
	}
	authority := buildSimulationCharacterAuthority(st, chapter)
	byName := make(map[string]simulationCharacterAuthority, len(authority))
	for _, entry := range authority {
		byName[entry.Character] = entry
	}
	var resubmissions []string
	var violations []string
	for _, decision := range decisions {
		entry, ok := byName[strings.TrimSpace(decision.Character)]
		if !ok {
			continue
		}
		if entry.SimulationStatus == "already_present" {
			resubmissions = append(resubmissions, entry.Character)
			continue
		}
		if err := validateRequiredKnowledgeBoundaries(decision, entry.RequiredKnowledgeBoundary); err != nil {
			violations = append(violations, fmt.Sprintf("%s: %v", decision.Character, err))
			continue
		}
		switch entry.AuthorityMode {
		case "project_all_grounded":
			if err := validateProjectAllGroundedDecision(st, chapter, entry, decision); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", decision.Character, err))
			}
		case "hold_baseline":
			if err := validateHoldBaselineDecision(chapter, decision, entry.RequiredKnowledgeBoundary); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", decision.Character, err))
			}
		case "rewrite_source_only":
			if err := validateRewriteSourceOnlyDecision(chapter, decision, entry.RewriteSourceEvidence, entry.RequiredKnowledgeBoundary); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", decision.Character, err))
			}
		}
	}
	if len(resubmissions) > 0 {
		return fmt.Errorf("simulation authority guard: already_present 角色禁止重发或覆盖：%s", strings.Join(compactStrings(resubmissions), "、"))
	}
	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("blocking 角色禁止补猜，必须原样使用对应 authority contract：%s", strings.Join(violations, "；"))
}

func preserveKnowledgeBoundaryClauses(st *store.Store, chapter int, character string) []string {
	if st == nil || chapter <= 0 || strings.TrimSpace(character) == "" {
		return nil
	}
	source, _, _, err := loadChapterRewriteSource(st, chapter)
	if err != nil || source == nil {
		return nil
	}
	character = strings.TrimSpace(character)
	var clauses []string
	for _, fact := range source.PreserveFacts {
		for _, clause := range splitSimulationClauses(fact) {
			at := knowledgeBoundarySubjectIndex(clause, character)
			if at < 0 {
				continue
			}
			clause = strings.TrimSpace(clause[at:])
			clauses = appendUniqueString(clauses, clause)
		}
	}
	return clauses
}

// knowledgeBoundarySubjectIndex binds an epistemic restriction to its
// grammatical subject, not to every character name appearing later as the
// object of that restriction. For example, in "贺骁不知道林澈已回城" only 贺骁
// receives the lock; 林澈 must not be forced to claim ignorance of himself.
func knowledgeBoundarySubjectIndex(clause, character string) int {
	character = strings.TrimSpace(character)
	if character == "" {
		return -1
	}
	predicates := []string{"不知道", "不得知道", "不能知道", "不得推断", "不能推断", "不得凭", "不能凭"}
	modifiers := []string{"此时", "当前", "目前", "当下", "仍然", "仍", "还", "尚", "并", "也", "暂时", "一直", "明确", "本人", "自己"}
	for offset := 0; offset < len(clause); {
		rel := strings.Index(clause[offset:], character)
		if rel < 0 {
			break
		}
		at := offset + rel
		rest := strings.TrimLeftFunc(clause[at+len(character):], unicode.IsSpace)
		for {
			matched := false
			for _, modifier := range modifiers {
				if strings.HasPrefix(rest, modifier) {
					rest = strings.TrimLeftFunc(rest[len(modifier):], unicode.IsSpace)
					matched = true
					break
				}
			}
			if !matched {
				break
			}
		}
		for _, predicate := range predicates {
			if strings.HasPrefix(rest, predicate) {
				return at
			}
		}
		offset = at + len(character)
	}
	return -1
}

func validateRequiredKnowledgeBoundaries(decision domain.CharacterWorldDecision, required []string) error {
	if len(required) == 0 {
		return nil
	}
	actual := rewriteFactIdentity(decision.KnowledgeBoundary)
	var missing []string
	for i, clause := range required {
		if !strings.Contains(actual, rewriteFactIdentity(clause)) {
			missing = append(missing, fmt.Sprintf("knowledge_boundary.required_preserve_clause[%d]", i))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("preserve_facts 知识边界缺失：%s；请从 required_knowledge_boundaries 逐条原样复制到 knowledge_boundary", strings.Join(missing, ", "))
}

func validateHoldBaselineDecision(chapter int, decision domain.CharacterWorldDecision, requiredKnowledge ...[]string) error {
	expected := holdBaselineSentinelDecision(decision.Character, chapter)
	expected.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(expected.KnowledgeBoundary, firstKnowledgeBoundarySet(requiredKnowledge))
	var mismatches []string
	add := func(field string, equal bool) {
		if !equal {
			mismatches = append(mismatches, field)
		}
	}
	add("time", strings.TrimSpace(decision.Time) == "" || decision.Time == expected.Time)
	add("location", decision.Location == expected.Location)
	add("current_goal", decision.CurrentGoal == expected.CurrentGoal)
	add("pressure", decision.Pressure == expected.Pressure)
	add("resources", len(decision.Resources) == 0)
	add("knowledge_boundary", decision.KnowledgeBoundary == expected.KnowledgeBoundary)
	add("available_options", slices.Equal(decision.AvailableOptions, expected.AvailableOptions))
	add("decision", decision.Decision == expected.Decision)
	add("decision_reason", decision.DecisionReason == expected.DecisionReason)
	add("action", decision.Action == expected.Action)
	add("action_duration", decision.ActionDuration == expected.ActionDuration)
	add("completion_state", decision.CompletionState == expected.CompletionState)
	add("immediate_result", decision.ImmediateResult == expected.ImmediateResult)
	add("state_after", decision.StateAfter == expected.StateAfter)
	add("visible_to_pov", decision.VisibleToPOV == expected.VisibleToPOV)
	if len(decision.ButterflyEffects) != 1 {
		add("butterfly_effects", false)
	} else {
		effect := decision.ButterflyEffects[0]
		expectedEffect := expected.ButterflyEffects[0]
		add("butterfly_effects[0].effect", effect.Effect == expectedEffect.Effect)
		add("butterfly_effects[0].targets", len(effect.Targets) == 0)
		add("butterfly_effects[0].transmission_path", effect.TransmissionPath == expectedEffect.TransmissionPath)
		add("butterfly_effects[0].arrival_chapter", effect.ArrivalChapter == expectedEffect.ArrivalChapter)
		add("butterfly_effects[0].visibility", effect.Visibility == expectedEffect.Visibility)
		add("butterfly_effects[0].protagonist_impact", effect.ProtagonistImpact == expectedEffect.ProtagonistImpact)
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("hold_baseline_contract 字段不匹配：%s；请从 simulation_character_authority 逐字段原样复制，禁止写叙事解释或具体事实", strings.Join(mismatches, ", "))
	}
	return nil
}

func holdBaselineSentinelDecision(character string, chapter int) domain.CharacterWorldDecision {
	return domain.CharacterWorldDecision{
		Character:         strings.TrimSpace(character),
		Time:              simulationAuthorityUnknown,
		Location:          simulationAuthorityUnknown,
		CurrentGoal:       simulationAuthorityHoldBaseline,
		Pressure:          simulationAuthorityMissing,
		Resources:         []string{},
		KnowledgeBoundary: simulationAuthorityMissing,
		AvailableOptions:  []string{simulationAuthorityHoldBaseline, simulationAuthorityWait},
		Decision:          simulationAuthorityHoldBaseline,
		DecisionReason:    simulationAuthorityMissing,
		Action:            simulationAuthorityHoldBaseline,
		ActionDuration:    simulationAuthorityNotApplicable,
		CompletionState:   "blocked",
		ImmediateResult:   simulationAuthorityNoEffect,
		StateAfter:        simulationAuthorityUnchanged,
		VisibleToPOV:      false,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:            simulationAuthorityBlockedEffect,
			Targets:           []string{},
			TransmissionPath:  simulationAuthorityMissing,
			ArrivalChapter:    chapter,
			Visibility:        "hidden",
			ProtagonistImpact: simulationAuthorityNoImpact,
		}},
	}
}

func holdBaselineContractPayload(character string, chapter int, requiredKnowledge ...[]string) map[string]any {
	expected := holdBaselineSentinelDecision(character, chapter)
	expected.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(expected.KnowledgeBoundary, firstKnowledgeBoundarySet(requiredKnowledge))
	effect := expected.ButterflyEffects[0]
	return map[string]any{
		"character": expected.Character, "time": expected.Time, "location": expected.Location,
		"current_goal": expected.CurrentGoal, "pressure": expected.Pressure, "resources": []string{},
		"knowledge_boundary": expected.KnowledgeBoundary, "available_options": expected.AvailableOptions,
		"decision": expected.Decision, "decision_reason": expected.DecisionReason, "action": expected.Action,
		"action_duration": expected.ActionDuration, "completion_state": expected.CompletionState,
		"immediate_result": expected.ImmediateResult, "state_after": expected.StateAfter, "visible_to_pov": false,
		"butterfly_effects": []map[string]any{{
			"effect": effect.Effect, "targets": []string{}, "transmission_path": effect.TransmissionPath,
			"arrival_chapter": effect.ArrivalChapter, "visibility": effect.Visibility,
			"protagonist_impact": effect.ProtagonistImpact,
		}},
	}
}

func rewriteSourceOnlySentinelDecision(character string, chapter int, evidence []string) domain.CharacterWorldDecision {
	action := simulationAuthorityUnstated
	if len(evidence) > 0 && strings.TrimSpace(evidence[0]) != "" {
		action = strings.TrimSpace(evidence[0])
	}
	return domain.CharacterWorldDecision{
		Character:         strings.TrimSpace(character),
		Time:              simulationAuthorityUnknown,
		Location:          simulationAuthorityUnknown,
		CurrentGoal:       simulationAuthorityUnstated,
		Pressure:          simulationAuthorityUnstated,
		Resources:         []string{},
		KnowledgeBoundary: simulationAuthorityEvidenceOnly,
		AvailableOptions:  []string{simulationAuthorityPreserve, simulationAuthorityNoAlternative},
		Decision:          simulationAuthorityPreserve,
		DecisionReason:    simulationAuthorityEvidenceOnly,
		Action:            action,
		ActionDuration:    simulationAuthorityNotSpecified,
		CompletionState:   "completed",
		ImmediateResult:   simulationAuthoritySourceResult,
		StateAfter:        simulationAuthorityUnstated,
		VisibleToPOV:      true,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:            simulationAuthoritySourceEffect,
			Targets:           []string{},
			TransmissionPath:  simulationAuthorityDirectSource,
			ArrivalChapter:    chapter,
			Visibility:        "visible",
			ProtagonistImpact: simulationAuthorityNoInference,
		}},
	}
}

func rewriteSourceOnlyContractPayload(character string, chapter int, evidence []string, requiredKnowledge ...[]string) map[string]any {
	expected := rewriteSourceOnlySentinelDecision(character, chapter, evidence)
	expected.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(expected.KnowledgeBoundary, firstKnowledgeBoundarySet(requiredKnowledge))
	effect := expected.ButterflyEffects[0]
	return map[string]any{
		"character": expected.Character, "time": expected.Time, "location": expected.Location,
		"current_goal": expected.CurrentGoal, "pressure": expected.Pressure, "resources": []string{},
		"knowledge_boundary": expected.KnowledgeBoundary, "available_options": expected.AvailableOptions,
		"decision": expected.Decision, "decision_reason": expected.DecisionReason, "action": expected.Action,
		"action_duration": expected.ActionDuration, "completion_state": expected.CompletionState,
		"immediate_result": expected.ImmediateResult, "state_after": expected.StateAfter, "visible_to_pov": true,
		"butterfly_effects": []map[string]any{{
			"effect": effect.Effect, "targets": []string{}, "transmission_path": effect.TransmissionPath,
			"arrival_chapter": effect.ArrivalChapter, "visibility": effect.Visibility,
			"protagonist_impact": effect.ProtagonistImpact,
		}},
	}
}

func validateRewriteSourceOnlyDecision(chapter int, decision domain.CharacterWorldDecision, evidence []string, requiredKnowledge ...[]string) error {
	expected := rewriteSourceOnlySentinelDecision(decision.Character, chapter, evidence)
	expected.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(expected.KnowledgeBoundary, firstKnowledgeBoundarySet(requiredKnowledge))
	var mismatches []string
	add := func(field string, equal bool) {
		if !equal {
			mismatches = append(mismatches, field)
		}
	}
	add("time", strings.TrimSpace(decision.Time) == "" || decision.Time == expected.Time)
	add("location", decision.Location == expected.Location)
	add("current_goal", decision.CurrentGoal == expected.CurrentGoal)
	add("pressure", decision.Pressure == expected.Pressure)
	add("resources", len(decision.Resources) == 0)
	add("knowledge_boundary", decision.KnowledgeBoundary == expected.KnowledgeBoundary)
	add("available_options", slices.Equal(decision.AvailableOptions, expected.AvailableOptions))
	add("decision", decision.Decision == expected.Decision)
	add("decision_reason", decision.DecisionReason == expected.DecisionReason)
	add("action", decision.Action == expected.Action)
	add("action_duration", decision.ActionDuration == expected.ActionDuration)
	add("completion_state", decision.CompletionState == expected.CompletionState)
	add("immediate_result", decision.ImmediateResult == expected.ImmediateResult)
	add("state_after", decision.StateAfter == expected.StateAfter)
	add("visible_to_pov", decision.VisibleToPOV == expected.VisibleToPOV)
	if len(decision.ButterflyEffects) != 1 {
		add("butterfly_effects", false)
	} else {
		effect := decision.ButterflyEffects[0]
		expectedEffect := expected.ButterflyEffects[0]
		add("butterfly_effects[0].effect", effect.Effect == expectedEffect.Effect)
		add("butterfly_effects[0].targets", len(effect.Targets) == 0)
		add("butterfly_effects[0].transmission_path", effect.TransmissionPath == expectedEffect.TransmissionPath)
		add("butterfly_effects[0].arrival_chapter", effect.ArrivalChapter == expectedEffect.ArrivalChapter)
		add("butterfly_effects[0].visibility", effect.Visibility == expectedEffect.Visibility)
		add("butterfly_effects[0].protagonist_impact", effect.ProtagonistImpact == expectedEffect.ProtagonistImpact)
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("rewrite_source_only_contract 字段不匹配：%s；请逐字段原样复制，action 也不得改写原文证据", strings.Join(mismatches, ", "))
	}
	return nil
}

func firstKnowledgeBoundarySet(sets [][]string) []string {
	if len(sets) == 0 {
		return nil
	}
	return sets[0]
}

func mergedAuthorityKnowledgeBoundary(base string, required []string) string {
	parts := []string{strings.TrimSpace(base)}
	for _, clause := range required {
		if clause = strings.TrimSpace(clause); clause != "" && !slices.Contains(parts, clause) {
			parts = append(parts, clause)
		}
	}
	return strings.Join(compactStrings(parts), "；")
}

func rewriteSourceEvidenceForCharacter(st *store.Store, chapter int, character string) []string {
	if st == nil || chapter <= 0 || strings.TrimSpace(character) == "" {
		return nil
	}
	source, body, _, err := loadChapterRewriteSource(st, chapter)
	if err != nil || strings.TrimSpace(body) == "" {
		return nil
	}
	character = strings.TrimSpace(character)
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(body)
	units := rewriteSourceEvidenceUnits(normalized)
	var evidence []string
	// A rewrite brief can intentionally reverse a bad action in the committed
	// body.  In that case the protected fact is the newer authority: taking the
	// first body mention (often appearance description) as the sole contract
	// action makes the simulator look grounded while hiding the very correction
	// it must carry into planning. Prefer one exact preserve-fact sentence when
	// it gives this character an explicit action or timing boundary, then retain
	// body sentences as supporting evidence.
	if source != nil {
		bestScore := 0
		bestFact := ""
		for _, fact := range source.PreserveFacts {
			if score := preserveFactActionScore(fact, character); score > bestScore {
				bestScore = score
				bestFact = strings.TrimSpace(fact)
			}
		}
		if bestFact != "" {
			evidence = append(evidence, bestFact)
		}
	}
	for _, unit := range units {
		unit = strings.TrimSpace(strings.TrimLeft(unit, "#*-+> "))
		if unit == "" || !strings.Contains(unit, character) {
			continue
		}
		// rewrite_source_only promises an exact source sentence. Truncating a
		// long unit at an arbitrary rune silently turns that contract into a
		// paraphrase and can detach its closing quote, so retain the complete unit.
		evidence = appendUniqueString(evidence, unit)
		if len(evidence) >= 3 {
			break
		}
	}
	return evidence
}

func preserveFactActionScore(fact, character string) int {
	fact = strings.TrimSpace(fact)
	character = strings.TrimSpace(character)
	if fact == "" || character == "" {
		return 0
	}
	patterns := []struct {
		text  string
		score int
	}{
		{"必须由" + character, 100},
		{character + "独立", 96},
		{character + "先", 92},
		{character + "主动", 90},
		{character + "到场", 88},
		{character + "只能", 86},
		{character + "至多", 84},
		{character + "尚未", 78},
		{character + "已经", 74},
		{character + "已", 70},
		{character + "在", 64},
	}
	best := 0
	for _, pattern := range patterns {
		if strings.Contains(fact, pattern.text) && pattern.score > best {
			best = pattern.score
		}
	}
	return best
}

func rewriteSourceEvidenceUnits(text string) []string {
	var units []string
	var current strings.Builder
	terminalPending := false
	flush := func() {
		if unit := strings.TrimSpace(current.String()); unit != "" {
			units = append(units, unit)
		}
		current.Reset()
		terminalPending = false
	}
	for _, r := range text {
		if r == '\n' {
			flush()
			continue
		}
		// Sentence punctuation inside Chinese dialogue is followed by the closing
		// quote (for example `。”`).  Flushing at `。` used to drop that quote from
		// the exact rewrite-source contract and left a stray one-rune unit behind.
		// Delay the boundary through quote/bracket closers and whitespace, then
		// flush immediately before the first rune of the next sentence.
		if terminalPending && !unicode.IsSpace(r) && !rewriteSourceSentenceCloser(r) && !rewriteSourceSentenceTerminal(r) {
			flush()
		}
		current.WriteRune(r)
		if rewriteSourceSentenceTerminal(r) {
			terminalPending = true
		}
	}
	flush()
	return units
}

func rewriteSourceSentenceTerminal(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?':
		return true
	default:
		return false
	}
}

func rewriteSourceSentenceCloser(r rune) bool {
	switch r {
	case '”', '’', '"', '\'', '」', '』', '）', ')', '】', ']', '》', '〉':
		return true
	default:
		return false
	}
}

func authoritativeSimulationStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value := authoritativeSimulationText(value); value != "" {
			result = append(result, value)
		}
	}
	return compactStrings(result)
}

func compactSimulationRelationships(values []domain.CharacterRelationNote, limit int) []string {
	result := make([]string, 0, len(values))
	for _, relation := range values {
		other := strings.TrimSpace(relation.Other)
		if other == "" {
			continue
		}
		parts := []string{other}
		for _, detail := range []string{relation.CurrentTie, relation.DebtOrTrust, relation.HowMet} {
			if detail := authoritativeSimulationText(detail); detail != "" {
				parts = append(parts, detail)
			}
		}
		result = append(result, strings.Join(parts, "｜"))
	}
	return limitAuthorityStrings(compactStrings(result), limit)
}

func authoritativeSimulationText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") {
		return ""
	}
	for _, marker := range []string{
		"故事开始前未进入主角视角", "正式出场前按角色卡", "按自身开局目标行动",
		"待正文确认", "信息缺口", "未知处标记", "后续补档",
		"在开局压力中保住自己的目标、资源或关系边界",
		"按自身目标、恐惧、资源、关系和现场证据选择，不为主角工具化",
		"进入第一章现场选择，并在章末产生可回填状态变化",
		"保持背景职责和资源压力；正式引入前补位置、资源、关系和通信边界",
	} {
		if strings.Contains(value, marker) {
			return ""
		}
	}
	return value
}

func limitAuthorityStrings(values []string, limit int) []string {
	values = compactStrings(values)
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return slices.Clone(values[:limit])
}

func appendAuthorityUnique(values []string, incoming ...string) []string {
	for _, value := range incoming {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(values, value) {
			continue
		}
		values = append(values, value)
	}
	return values
}

func simulationCharacterAuthorityPolicy(authority []simulationCharacterAuthority) map[string]any {
	missing := 0
	for _, entry := range authority {
		if entry.Blocking {
			missing++
		}
	}
	return map[string]any{
		"required_count": len(authority),
		"blocking_count": missing,
		"policy":         fmt.Sprintf(projectAllAuthoritySummaryPolicyTemplate, missing),
	}
}
