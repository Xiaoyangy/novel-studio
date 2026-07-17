package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
)

func buildPipelineProjectedChapterBundle(
	generation domain.PlanningGenerationV2,
	outline domain.OutlineEntry,
	previousBundleDigest string,
	preStateRoot string,
	artifacts *agents.ProjectedChapterArtifacts,
	registry domain.ObligationRegistryV2,
) (domain.ProjectedChapterBundle, domain.ObligationRegistryV2, error) {
	if artifacts == nil || artifacts.WorldSimulation == nil || artifacts.Plan == nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all bundle requires full simulation and plan")
	}
	if strings.TrimSpace(artifacts.PlanningContextDigest) == "" {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all bundle requires exact projected planning context digest")
	}
	if artifacts.RAGFactReceipt == nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all bundle requires an explicit RAG fact receipt, including no_material")
	}
	if artifacts.CraftRecallReceipt == nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all bundle requires an explicit craft receipt, including no_material")
	}
	factDigest, err := domain.RAGFactReceiptDigestV2(*artifacts.RAGFactReceipt)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all RAG fact receipt: %w", err)
	}
	contextToken, err := domain.ProjectedPlanningContextSourceTokenV2(artifacts.PlanningContextDigest)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	if !pipelineProjectAllSourcesContainExact(artifacts.WorldSimulation.Sources, contextToken) ||
		!pipelineProjectAllSourcesContainExact(
			artifacts.Plan.CausalSimulation.ContextSources,
			contextToken,
		) {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf(
			"project-all simulation and plan must both attest the exact authoritative context binding",
		)
	}
	chapter := artifacts.Plan.Chapter
	if artifacts.WorldSimulation.Chapter != chapter || outline.Chapter != chapter {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf(
			"project-all chapter identity mismatch: outline=%d simulation=%d plan=%d",
			outline.Chapter,
			artifacts.WorldSimulation.Chapter,
			chapter,
		)
	}
	if strings.TrimSpace(artifacts.WorldSimulation.GenerationID) != generation.GenerationID {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf(
			"project-all simulation generation identity mismatch: simulation=%q generation=%q; simulator must bind the validated project_all_state before computing simulation_id",
			artifacts.WorldSimulation.GenerationID,
			generation.GenerationID,
		)
	}
	simulation := *artifacts.WorldSimulation
	plan := *artifacts.Plan
	if domain.IsArcPlanningGenerationV2(generation) {
		var predecessor *domain.ProjectedPlanningPredecessorContractV2
		if chapter > generation.FirstProjectedChapter {
			incoming := plan.CausalSimulation.ArcTransition
			predecessor = &domain.ProjectedPlanningPredecessorContractV2{
				Chapter:                 chapter - 1,
				OutgoingConsequenceID:   incoming.IncomingConsequenceID,
				OutgoingConsequenceText: incoming.IncomingConsequenceText,
			}
		}
		if err := domain.ValidateArcChapterTransitionContract(plan, predecessor); err != nil {
			return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all explicit arc transition: %w", err)
		}
	}
	if err := domain.ValidateProjectAllCraftRecallReceipt(*artifacts.CraftRecallReceipt); err != nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all craft receipt: %w", err)
	}
	if artifacts.CraftRecallReceipt.Chapter != chapter ||
		artifacts.CraftRecallReceipt.GenerationID != generation.GenerationID ||
		artifacts.CraftRecallReceipt.PlanningContextDigest != artifacts.PlanningContextDigest {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all craft receipt identity does not match chapter/generation/planning context")
	}
	if err := domain.ValidateProjectAllCraftPlanConsumptionV2(plan, *artifacts.CraftRecallReceipt); err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	craftDigest, err := domain.CraftRecallReceiptDigestV2(*artifacts.CraftRecallReceipt)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	consumed, carried, registry := pipelineProjectAllExistingObligations(registry, chapter)
	created, registry, err := pipelineProjectAllCreateObligations(generation, simulation, plan, registry)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	delta := pipelineProjectAllDelta(chapter, simulation, plan, consumed, carried, created)
	if err := validatePipelineProjectAllRevealBudget(plan); err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	postStateRoot, err := domain.DeriveProjectedPostStateRootV2(preStateRoot, delta)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	bundle := domain.ProjectedChapterBundle{
		Version:                  domain.ProjectedChapterBundleV2Version,
		GenerationID:             generation.GenerationID,
		Chapter:                  chapter,
		Authority:                domain.ProjectedAuthorityV2,
		State:                    domain.ProjectedStateV2,
		ProjectionLevel:          domain.FormalProjectionLevelV2,
		PreviousBundleDigest:     previousBundleDigest,
		ProjectedPreStateRoot:    preStateRoot,
		ChapterWorldSimulation:   simulation,
		ChapterPlan:              plan,
		FormalWorldSimulation:    pipelineFormalWorldSimulationV2(simulation),
		POVPlan:                  pipelinePOVPlanV2(simulation, plan),
		HardRenderContract:       pipelineHardRenderContractV2(plan, simulation, delta),
		SourceBindings:           pipelineSourceBindingsV2(outline, plan, artifacts.RAGFactReceipt, artifacts.CraftRecallReceipt),
		RAGFactReceipt:           artifacts.RAGFactReceipt,
		RAGFactReceiptDigest:     factDigest,
		CraftRecallReceipt:       artifacts.CraftRecallReceipt,
		CraftRecallReceiptDigest: craftDigest,
		PlanningContextDigest:    artifacts.PlanningContextDigest,
		ObligationsConsumed:      consumed,
		ObligationsCreated:       created,
		ObligationsCarried:       carried,
		ProjectedDelta:           delta,
		ProjectedPostStateRoot:   postStateRoot,
		RenderContext:            append(json.RawMessage(nil), artifacts.RenderContext...),
	}
	bundle.RenderContext, err = augmentPipelineProjectAllRenderContext(bundle.RenderContext, bundle)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all augment render context: %w", err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, fmt.Errorf("project-all render context digest: %w", err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return domain.ProjectedChapterBundle{}, registry, err
	}
	return bundle, registry, nil
}

func pipelineProjectAllSourcesContainExact(sources []string, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	for _, source := range sources {
		if strings.TrimSpace(source) == token {
			return true
		}
	}
	return false
}

func pipelineFormalWorldSimulationV2(sim domain.ChapterWorldSimulation) domain.FormalWorldSimulationV2 {
	formal := domain.FormalWorldSimulationV2{
		SimulationID:     sim.SimulationID,
		AvailableChoices: compactProjectAllStrings(sim.ProtagonistProjection.AvailableOptions),
		ChosenDecision:   fallbackProjectAllText(sim.ProtagonistProjection.ChosenDecision, firstProjectAllOption(sim.ProtagonistProjection.AvailableOptions)),
		TimeAdvance:      strings.TrimSpace(sim.TimeWindow),
	}
	for i, decision := range sim.CharacterDecisions {
		name := strings.TrimSpace(decision.Character)
		if name == "" {
			continue
		}
		formal.InitialConditions = append(formal.InitialConditions,
			domain.SimulationStateFactV2{
				ID:      pipelineProjectAllStableID("initial", sim.Chapter, name, "location"),
				Subject: name,
				Field:   "location",
				Value:   fallbackProjectAllText(decision.Location, "location_unknown"),
			},
			domain.SimulationStateFactV2{
				ID:      pipelineProjectAllStableID("initial", sim.Chapter, name, "goal"),
				Subject: name,
				Field:   "goal",
				Value:   fallbackProjectAllText(decision.CurrentGoal, decision.Pressure),
			},
		)
		known := []string{}
		if text := strings.TrimSpace(decision.KnowledgeBoundary); text != "" {
			known = append(known, text)
		}
		formal.Actors = append(formal.Actors, domain.SimulationActorV2{
			CharacterID:      name,
			Motivation:       fallbackProjectAllText(decision.CurrentGoal, decision.DecisionReason),
			KnownFacts:       known,
			UnknownFacts:     compactProjectAllStrings(sim.ProtagonistProjection.HiddenPressures),
			OffscreenState:   fallbackProjectAllText(decision.StateAfter, decision.CompletionState),
			AvailableActions: fallbackProjectAllStrings(decision.AvailableOptions, fallbackProjectAllText(decision.Decision, decision.Action)),
		})
		causes := []string{formal.InitialConditions[max(0, len(formal.InitialConditions)-2)].ID}
		downstream := strings.TrimSpace(decision.StateAfter)
		if len(decision.ButterflyEffects) > 0 {
			downstream = strings.TrimSpace(decision.ButterflyEffects[0].Effect)
		}
		formal.CausalSteps = append(formal.CausalSteps, domain.SimulationCausalStepV2{
			ID:               pipelineProjectAllStableID("causal", sim.Chapter, name, fmt.Sprint(i)),
			CauseIDs:         causes,
			ActorID:          name,
			Decision:         fallbackProjectAllText(decision.Decision, decision.Action),
			ImmediateEffect:  fallbackProjectAllText(decision.ImmediateResult, decision.Action),
			DownstreamEffect: fallbackProjectAllText(downstream, decision.ImmediateResult),
		})
		formal.TerminalConditions = append(formal.TerminalConditions, domain.SimulationStateFactV2{
			ID:      pipelineProjectAllStableID("terminal", sim.Chapter, name, "state"),
			Subject: name,
			Field:   "state",
			Value:   fallbackProjectAllText(decision.StateAfter, decision.CompletionState),
		})
		if location := strings.TrimSpace(decision.Location); location != "" {
			formal.LocationFlow = appendUniqueProjectAllString(formal.LocationFlow, location)
		}
	}
	if len(formal.AvailableChoices) < 2 {
		for _, decision := range sim.CharacterDecisions {
			for _, option := range decision.AvailableOptions {
				formal.AvailableChoices = appendUniqueProjectAllString(formal.AvailableChoices, option)
			}
		}
	}
	if len(formal.AvailableChoices) < 2 {
		formal.AvailableChoices = appendUniqueProjectAllString(formal.AvailableChoices, "保持前态并暂不扩大行动")
	}
	for _, choice := range formal.AvailableChoices {
		if choice == formal.ChosenDecision {
			continue
		}
		formal.Counterfactuals = append(formal.Counterfactuals, domain.SimulationCounterfactualV2{
			Choice:      choice,
			RejectedBy:  fallbackProjectAllText(sim.ProtagonistProjection.DecisionReason, "不符合当前证据与边界"),
			Consequence: "该选择不会形成正式计划锁定的本章状态变化",
		})
	}
	if len(formal.LocationFlow) == 0 {
		formal.LocationFlow = []string{"本章正式计划限定的连续空间"}
	}
	return formal
}

func pipelinePOVPlanV2(sim domain.ChapterWorldSimulation, plan domain.ChapterPlan) domain.POVPlanV2 {
	protagonist := strings.TrimSpace(sim.ProtagonistProjection.Protagonist)
	out := domain.POVPlanV2{
		POVCharacterID:    protagonist,
		KnowledgeBoundary: compactProjectAllStrings(append([]string{projectAllLiteraryKnowledgeBoundary(plan)}, plan.CausalSimulation.SceneConstraints...)),
		Unknowns:          compactProjectAllStrings(append(sim.ProtagonistProjection.HiddenPressures, plan.CausalSimulation.InformationGaps...)),
		TimeAdvance:       strings.TrimSpace(sim.TimeWindow),
	}
	for _, initial := range plan.CausalSimulation.InitialState {
		out.Motivations = append(out.Motivations, domain.POVCharacterMotivationV2{
			CharacterID: strings.TrimSpace(initial.Character),
			Goal:        fallbackProjectAllText(initial.CurrentGoal, initial.ActionTendency),
			Pressure:    fallbackProjectAllText(initial.Pressure, initial.PrivateBoundary),
			Choice:      fallbackProjectAllText(initial.LikelyAction, plan.CausalSimulation.ProtagonistDecision),
		})
	}
	for _, decision := range sim.CharacterDecisions {
		if decision.Character == protagonist && len(sim.CharacterDecisions) > 1 {
			continue
		}
		out.OffscreenStates = append(out.OffscreenStates, domain.POVOffscreenStateV2{
			CharacterID: strings.TrimSpace(decision.Character),
			State:       fallbackProjectAllText(decision.StateAfter, decision.CompletionState),
			CausalImpact: fallbackProjectAllText(
				firstProjectAllButterflyEffect(decision.ButterflyEffects),
				decision.ImmediateResult,
			),
		})
	}
	for _, scene := range plan.CausalSimulation.DialogueBlueprints {
		out.Scenes = append(out.Scenes, domain.POVSceneV2{
			SceneID:        fallbackProjectAllText(scene.SceneID, pipelineProjectAllStableID("scene", plan.Chapter, scene.LocationAnchor)),
			Location:       fallbackProjectAllText(scene.LocationAnchor, firstProjectAllLocation(sim)),
			Time:           fallbackProjectAllText(sim.TimeWindow, "本章连续时段"),
			PresentActors:  fallbackProjectAllActors(scene.Participants, protagonist),
			POVKnows:       compactProjectAllStrings([]string{scene.InfoAsymmetry.POVKnows}),
			POVDoesNotKnow: compactProjectAllStrings([]string{scene.InfoAsymmetry.POVLacks, scene.InfoAsymmetry.OtherHolds}),
			CausalPurpose:  fallbackProjectAllText(scene.DialogueObjective, scene.ValueShift.Value),
		})
	}
	if len(out.Scenes) == 0 {
		for i, beat := range plan.CausalSimulation.ReaderRetentionPlan.SurfaceBeats {
			out.Scenes = append(out.Scenes, domain.POVSceneV2{
				SceneID:        pipelineProjectAllStableID("scene", plan.Chapter, fmt.Sprint(i)),
				Location:       firstProjectAllLocation(sim),
				Time:           fallbackProjectAllText(sim.TimeWindow, "本章连续时段"),
				PresentActors:  fallbackProjectAllActors(nil, protagonist),
				POVKnows:       compactProjectAllStrings(sim.ProtagonistProjection.ObservableEffects),
				POVDoesNotKnow: compactProjectAllStrings(sim.ProtagonistProjection.HiddenPressures),
				CausalPurpose:  fallbackProjectAllText(beat.MustShow, beat.FunctionShift),
			})
		}
	}
	if len(out.Motivations) == 0 {
		out.Motivations = []domain.POVCharacterMotivationV2{{
			CharacterID: protagonist,
			Goal:        plan.Goal,
			Pressure:    plan.Conflict,
			Choice:      plan.CausalSimulation.ProtagonistDecision,
		}}
	}
	if len(out.OffscreenStates) == 0 && len(sim.CharacterDecisions) > 0 {
		decision := sim.CharacterDecisions[0]
		out.OffscreenStates = []domain.POVOffscreenStateV2{{
			CharacterID:  decision.Character,
			State:        fallbackProjectAllText(decision.StateAfter, decision.CompletionState),
			CausalImpact: fallbackProjectAllText(decision.ImmediateResult, "该状态限制本章可见行动"),
		}}
	}
	if len(out.Scenes) == 0 {
		out.Scenes = []domain.POVSceneV2{{
			SceneID:        pipelineProjectAllStableID("scene", plan.Chapter, "contract"),
			Location:       firstProjectAllLocation(sim),
			Time:           fallbackProjectAllText(sim.TimeWindow, "本章连续时段"),
			PresentActors:  fallbackProjectAllActors(nil, protagonist),
			POVKnows:       compactProjectAllStrings(sim.ProtagonistProjection.ObservableEffects),
			POVDoesNotKnow: compactProjectAllStrings(sim.ProtagonistProjection.HiddenPressures),
			CausalPurpose:  plan.Goal,
		}}
	}
	if len(out.KnowledgeBoundary) == 0 {
		out.KnowledgeBoundary = []string{"正文只写主角已经获得证据支持的信息"}
	}
	if len(out.Unknowns) == 0 {
		out.Unknowns = []string{"场外角色尚未通过传播路径抵达主角的信息"}
	}
	return out
}

func pipelineHardRenderContractV2(
	plan domain.ChapterPlan,
	simulation domain.ChapterWorldSimulation,
	delta domain.ProjectedDelta,
) domain.HardRenderContractV2 {
	contract := domain.HardRenderContractV2{
		MustOccur:    compactProjectAllStrings(plan.Contract.RequiredBeats),
		MustNotOccur: compactProjectAllStrings(plan.Contract.ForbiddenMoves),
		MustPreserve: compactProjectAllStrings(plan.Contract.ContinuityChecks),
	}
	visibleActors := make(map[string]bool)
	protagonist := strings.TrimSpace(simulation.ProtagonistProjection.Protagonist)
	if protagonist != "" {
		visibleActors[protagonist] = true
	}
	for _, decision := range simulation.CharacterDecisions {
		if decision.VisibleToPOV {
			visibleActors[strings.TrimSpace(decision.Character)] = true
		}
	}
	contract.ForeshadowChanges = pipelineProjectAllMutationContracts(
		delta.Foreshadows,
		func(domain.StateMutationV2) bool { return true },
	)
	contract.ResourceChanges = pipelineProjectAllMutationContracts(
		delta.Resources,
		func(mutation domain.StateMutationV2) bool {
			return visibleActors[strings.TrimSpace(mutation.Subject)]
		},
	)
	contract.RelationshipChanges = pipelineProjectAllMutationContracts(
		delta.Relationships,
		func(mutation domain.StateMutationV2) bool {
			return visibleActors[strings.TrimSpace(mutation.Subject)] ||
				visibleActors[strings.TrimSpace(mutation.Object)]
		},
	)
	contract.KnowledgeChanges = pipelineProjectAllMutationContracts(
		delta.Knowledge,
		func(mutation domain.StateMutationV2) bool {
			return visibleActors[strings.TrimSpace(mutation.Subject)]
		},
	)
	for i, item := range plan.CausalSimulation.ReaderRetentionPlan.RevealBudget {
		contract.RevealBudget = append(contract.RevealBudget, domain.RevealBudgetItemV2{
			FactID: pipelineProjectAllStableID("reveal", plan.Chapter, fmt.Sprint(i), item),
			Action: "limit",
			Limit:  item,
		})
	}
	if len(contract.MustPreserve) == 0 {
		contract.MustPreserve = []string{"保持 projected_pre_state_root 已确认的前态事实与知识边界"}
	}
	return contract
}

func validatePipelineProjectAllRevealBudget(plan domain.ChapterPlan) error {
	for i, item := range plan.CausalSimulation.ReaderRetentionPlan.RevealBudget {
		clauses := splitPipelineSealedContractClauses(item)
		if len(clauses) == 0 {
			clauses = []string{item}
		}
		for _, clause := range clauses {
			probe, mechanicallyForbidden := pipelineSealedNegativeContractProbe(clause)
			if !mechanicallyForbidden ||
				utf8.RuneCountInString(normalizePipelineSealedText(probe)) < 4 {
				return fmt.Errorf(
					"project-all chapter %d reveal_budget[%d] must make every clause an explicit, mechanically locatable forbidden fact (at least 4 content characters after 不得/不揭示/不解释/不提前); got %q",
					plan.Chapter,
					i,
					item,
				)
			}
		}
	}
	return nil
}

func pipelineProjectAllMutationContracts(
	mutations []domain.StateMutationV2,
	include func(domain.StateMutationV2) bool,
) []string {
	out := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		if include != nil && !include(mutation) {
			continue
		}
		out = append(out, domain.RenderContractForStateMutationV2(mutation))
	}
	return compactProjectAllStrings(out)
}

func pipelineSourceBindingsV2(
	outline domain.OutlineEntry,
	plan domain.ChapterPlan,
	receipt *domain.RAGFactReceipt,
	craftReceipt *domain.CraftRecallReceipt,
) []domain.SourceBindingV2 {
	outlineDigest := pipelineProjectAllDigest(outline)
	bindings := []domain.SourceBindingV2{{
		Kind:            "stable_outline",
		SourceID:        fmt.Sprintf("outline:chapter:%d", outline.Chapter),
		SourceDigest:    outlineDigest,
		ExactReferences: []string{fmt.Sprintf("outline.json#chapter=%d", outline.Chapter)},
		UsableFacts: compactProjectAllStrings([]string{
			outline.Title,
			outline.CoreEvent,
			outline.Hook,
		}),
		Transformation: "只把稳定章位、核心事件和章节边界转化为完整角色选择与状态变化，不复制为正文句序。",
		DoNotUse:       []string{"不得把粗章位当作完整推演", "不得提前兑现未来章节结果"},
	}}
	for i, external := range plan.CausalSimulation.ExternalRefs {
		refs := compactProjectAllStrings(external.SourceRefs)
		if len(refs) == 0 {
			continue
		}
		usable := compactProjectAllStrings(external.UsableDetails)
		if len(usable) == 0 {
			continue
		}
		bindings = append(bindings, domain.SourceBindingV2{
			Kind:            fallbackProjectAllText(external.SourceType, "external_reference"),
			SourceID:        fallbackProjectAllText(external.QueryOrNeed, fmt.Sprintf("external:%d", i)),
			SourceDigest:    pipelineProjectAllDigest(refs),
			ExactReferences: refs,
			UsableFacts:     usable,
			Transformation:  fallbackProjectAllText(external.TransformationRule, "只使用本章化后的事实，不复制来源表达。"),
			DoNotUse:        fallbackProjectAllStrings(external.DoNotUse, "不得复制来源表达或引入未授权事实"),
		})
	}
	if receipt != nil {
		token := receipt.SourceToken()
		// Receipt identity is always a separate content-addressed binding.
		// Transformation bindings above may cite individual hits, but their
		// digest commits the transformed reference list rather than the
		// immutable retrieval receipt itself.
		bindings = append(bindings, domain.SourceBindingV2{
			Kind:            "rag_fact_receipt",
			SourceID:        receipt.ID,
			SourceDigest:    "sha256:" + receipt.PayloadSHA256,
			ExactReferences: []string{token},
			UsableFacts: []string{
				"本条只证明本章计划消费的不可变召回回执；具体可用事实必须另由 transformation binding 明示。",
			},
			Transformation: "只把已在本章 external reference 中显式转化的命中用于计划；无命中时保持 no_material 边界。",
			DoNotUse: []string{
				"不得把 receipt 身份本身扩写成事实",
				"不得复制原始召回表达或补造未命中的制度、金额、物件细节",
			},
		})
	}
	if craftReceipt != nil {
		craftDigest, err := domain.CraftRecallReceiptDigestV2(*craftReceipt)
		if err == nil {
			bindings = append(bindings, domain.SourceBindingV2{
				Kind:            "craft_recall_receipt",
				SourceID:        craftReceipt.ID,
				SourceDigest:    craftDigest,
				ExactReferences: []string{domain.CraftRecallReceiptSourceTokenV2(*craftReceipt)},
				UsableFacts: []string{
					"本条证明本章 methodology 与 dialogue/scene 两类 planning craft recall 已按固定 need 生成并消费；具体写法只使用 external_reference_plan 的本章化转换。",
				},
				Transformation: "仅将 receipt 命中的写法摘要转化为本章人物因果、对白摩擦或场景调度方法；no_material 保持显式边界。",
				DoNotUse: []string{
					"不得复制 craft 或 benchmark 摘要原句",
					"不得把 receipt id、source refs、检索分数或规划术语写入正文",
				},
			})
		}
	}
	return bindings
}

func pipelineProjectAllDelta(
	chapter int,
	sim domain.ChapterWorldSimulation,
	plan domain.ChapterPlan,
	consumed, carried, created []string,
) domain.ProjectedDelta {
	delta := domain.ProjectedDelta{Version: domain.ProjectedDeltaV2Version}
	for i, outcome := range plan.CausalSimulation.OutcomeShift {
		delta.Timeline = append(delta.Timeline, pipelineProjectAllMutation(
			"timeline", chapter, fmt.Sprint(i), "chapter", "outcome", "advance", outcome, plan.Goal,
		))
	}
	if len(delta.Timeline) == 0 {
		delta.Timeline = append(delta.Timeline, pipelineProjectAllMutation(
			"timeline", chapter, "contract", "chapter", "outcome", "advance", plan.Hook, plan.Goal,
		))
	}
	for _, decision := range sim.CharacterDecisions {
		// hold_baseline is an authority/control record proving that an
		// unresolved off-screen actor was considered and deliberately frozen.
		// It is not a story event or a state transition. Projecting its sentinel
		// fields would turn "no authorized change" into durable
		// unknown/hold_baseline facts and poison every later chapter context.
		if pipelineProjectAllAuthorityNoOp(decision) {
			continue
		}
		name := strings.TrimSpace(decision.Character)
		if state := strings.TrimSpace(decision.StateAfter); state != "" {
			delta.CharacterState = append(delta.CharacterState, pipelineProjectAllMutation(
				"character", chapter, name, name, "state", "update", state, decision.Decision,
			))
		}
		if location := strings.TrimSpace(decision.Location); location != "" {
			delta.Locations = append(delta.Locations, pipelineProjectAllMutation(
				"location", chapter, name, name, "location", "set", location, decision.Action,
			))
		}
		if knowledge := strings.TrimSpace(decision.KnowledgeBoundary); knowledge != "" {
			delta.Knowledge = append(delta.Knowledge, pipelineProjectAllMutation(
				"knowledge", chapter, name, name, "knowledge_boundary", "set", knowledge, decision.DecisionReason,
			))
		}
	}
	for _, arc := range plan.CausalSimulation.RelationshipArcs {
		if len(arc.Pair) != 2 {
			continue
		}
		pair := arc.Pair[0] + "|" + arc.Pair[1]
		value := fallbackProjectAllText(arc.NextEmotionalBeat, arc.CurrentBond)
		mutation := pipelineProjectAllMutation(
			"relationship", chapter, pair, arc.Pair[0], "relationship", "update", value, arc.ConflictTrigger,
		)
		mutation.Object = arc.Pair[1]
		delta.Relationships = append(delta.Relationships, mutation)
	}
	for _, resource := range plan.CausalSimulation.StructuralResources {
		if strings.TrimSpace(resource.Resource) == "" {
			continue
		}
		mutation := pipelineProjectAllMutation(
			"resource", chapter, resource.Resource, resource.Controller, "resource", "update",
			fallbackProjectAllText(resource.ChapterPressure, resource.PriceOrCost), resource.AccessRule,
		)
		mutation.Object = resource.Resource
		delta.Resources = append(delta.Resources, mutation)
	}
	for _, chain := range plan.CausalSimulation.EvidenceChains {
		if strings.TrimSpace(chain.Event) == "" {
			continue
		}
		mutation := pipelineProjectAllMutation(
			"foreshadow", chapter, chain.Event, chain.OffscreenCharacter, "evidence_return", "advance",
			fallbackProjectAllText(chain.Evidence, chain.ReturnTiming), chain.Event,
		)
		mutation.Object = chain.Event
		delta.Foreshadows = append(delta.Foreshadows, mutation)
	}
	for _, id := range created {
		delta.Obligations = append(delta.Obligations, pipelineProjectAllMutation(
			"obligation", chapter, id, id, "state", "create", "planned", "projected chapter created obligation",
		))
	}
	for _, id := range consumed {
		delta.Obligations = append(delta.Obligations, pipelineProjectAllMutation(
			"obligation", chapter, id, id, "state", "consume", "satisfied", "projected consumer chapter",
		))
	}
	for _, id := range carried {
		delta.Obligations = append(delta.Obligations, pipelineProjectAllMutation(
			"obligation", chapter, id, id, "state", "carry", "open", "not due in this chapter",
		))
	}
	return domain.NormalizeProjectedDeltaV2(delta)
}

func pipelineProjectAllAuthorityNoOp(decision domain.CharacterWorldDecision) bool {
	return strings.TrimSpace(decision.Decision) == "hold_baseline" &&
		strings.TrimSpace(decision.Action) == "hold_baseline" &&
		strings.TrimSpace(decision.ImmediateResult) == "no_chapter_effect" &&
		strings.TrimSpace(decision.StateAfter) == "unchanged_authoritative_baseline" &&
		strings.TrimSpace(decision.CompletionState) == "blocked" &&
		!decision.VisibleToPOV
}

// augmentPipelineProjectAllRenderContext binds the prose session to the exact
// structured transition that its commit metadata must independently evidence.
// It deliberately excludes bundle/receipt digests (which would be circular)
// and raw retrieval payloads. The contract is state evidence, not a prose
// checklist: the drafter may render it naturally, but cannot silently invent a
// different timeline/resource/relationship outcome.
func augmentPipelineProjectAllRenderContext(
	raw json.RawMessage,
	bundle domain.ProjectedChapterBundle,
) (json.RawMessage, error) {
	return domain.BindProjectedRenderContextV2(raw, bundle)
}

func pipelineProjectAllExistingObligations(
	registry domain.ObligationRegistryV2,
	chapter int,
) (consumed []string, carried []string, updated domain.ObligationRegistryV2) {
	updated = registry
	for i := range updated.Obligations {
		obligation := &updated.Obligations[i]
		if obligation.State == domain.ObligationSupersededV2 {
			continue
		}
		if obligation.State == domain.ObligationSatisfiedV2 {
			for _, evidence := range obligation.Evidence {
				if evidence.Chapter == chapter &&
					evidence.Detail == "sealed bundle designates this chapter as the consumer" {
					consumed = append(consumed, obligation.ID)
					break
				}
			}
			continue
		}
		if containsProjectAllChapter(obligation.ConsumerChapters, chapter) {
			consumed = append(consumed, obligation.ID)
			obligation.State = domain.ObligationSatisfiedV2
			obligation.Evidence = append(obligation.Evidence, domain.ObligationEvidenceV2{
				Chapter: chapter,
				SourceDigest: pipelineProjectAllDigest(struct {
					ObligationID string `json:"obligation_id"`
					Chapter      int    `json:"chapter"`
				}{obligation.ID, chapter}),
				Detail: "sealed bundle designates this chapter as the consumer",
			})
		} else {
			carried = append(carried, obligation.ID)
		}
	}
	sort.Strings(consumed)
	sort.Strings(carried)
	return consumed, carried, updated
}

func pipelineProjectAllCreateObligations(
	generation domain.PlanningGenerationV2,
	sim domain.ChapterWorldSimulation,
	plan domain.ChapterPlan,
	registry domain.ObligationRegistryV2,
) ([]string, domain.ObligationRegistryV2, error) {
	created := []string{}
	add := func(kind domain.ObligationKindV2, contract string, consumer int, hard bool) error {
		contract = strings.TrimSpace(contract)
		if contract == "" || consumer <= plan.Chapter {
			return nil
		}
		horizon := domain.PlanningGenerationBookHorizonV2(generation)
		if consumer > horizon {
			return fmt.Errorf(
				"project-all chapter %d creates future obligation outside terminal chapter %d (book horizon): %s",
				plan.Chapter,
				horizon,
				contract,
			)
		}
		id, err := domain.DeriveObligationIDV2(kind, plan.Chapter, contract)
		if err != nil {
			return err
		}
		for _, existing := range registry.Obligations {
			if existing.ID == id {
				// Exact replay after a registry-before-bundle crash must rebuild
				// the same bundle, including its created-obligation list.
				if existing.Origin.GenerationID == generation.GenerationID &&
					existing.Origin.Chapter == plan.Chapter {
					created = append(created, id)
				}
				return nil
			}
		}
		hardness := domain.ObligationSoftV2
		if hard {
			hardness = domain.ObligationHardV2
		}
		registry.Obligations = append(registry.Obligations, domain.ObligationV2{
			ID:       id,
			Kind:     kind,
			Contract: contract,
			Origin: domain.ObligationOriginV2{
				GenerationID: generation.GenerationID,
				Chapter:      plan.Chapter,
				SourceDigest: pipelineProjectAllDigest(contract),
			},
			DueWindow: domain.ObligationDueWindowV2{
				FromChapter: consumer,
				ToChapter:   consumer,
			},
			Hardness:         hardness,
			State:            domain.ObligationPlannedV2,
			ConsumerChapters: []int{consumer},
			Evidence: []domain.ObligationEvidenceV2{{
				Chapter:      plan.Chapter,
				SourceDigest: pipelineProjectAllDigest(plan.Hook),
				Detail:       contract,
			}},
		})
		created = append(created, id)
		return nil
	}
	for _, decision := range sim.CharacterDecisions {
		for _, effect := range decision.ButterflyEffects {
			if effect.ArrivalChapter > plan.Chapter {
				if err := add(domain.ObligationCharacterV2, effect.Effect, effect.ArrivalChapter, effect.Visibility != "hidden"); err != nil {
					return nil, registry, err
				}
			}
		}
	}
	for _, chain := range plan.CausalSimulation.EvidenceChains {
		if chain.ChapterToResolve > plan.Chapter {
			if err := add(domain.ObligationRevealV2, chain.Event+" → "+chain.Evidence, chain.ChapterToResolve, true); err != nil {
				return nil, registry, err
			}
		}
	}
	for _, step := range plan.CausalSimulation.ReaderRewardPlan.RewardLadder {
		if step.Chapter > plan.Chapter {
			if err := add(domain.ObligationResourceV2, step.Reward+"；代价："+step.Cost, step.Chapter, true); err != nil {
				return nil, registry, err
			}
		}
	}
	sort.Strings(created)
	return created, registry, nil
}

func pipelineProjectAllMutation(
	category string,
	chapter int,
	key, subject, field, operation, after, cause string,
) domain.StateMutationV2 {
	return domain.StateMutationV2{
		StableID:  pipelineProjectAllStableID(category, chapter, key, field),
		Subject:   fallbackProjectAllText(subject, category),
		Field:     fallbackProjectAllText(field, category),
		Operation: operation,
		After:     fallbackProjectAllText(after, "state unchanged within explicit boundary"),
		Cause:     fallbackProjectAllText(cause, "formal chapter plan"),
	}
}

func pipelineProjectAllStableID(parts ...any) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		values = append(values, fmt.Sprint(part))
	}
	return "v2:" + strings.TrimPrefix(pipelineProjectAllDigest(values), "sha256:")[:20]
}

func pipelineProjectAllDigest(value any) string {
	hash, err := domain.DeterministicPlanningHash(value)
	if err != nil {
		return "sha256:" + strings.Repeat("0", 64)
	}
	return "sha256:" + strings.TrimPrefix(hash, "sha256:")
}

func compactProjectAllStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueProjectAllString(out, value)
	}
	return out
}

func fallbackProjectAllStrings(values []string, fallback string) []string {
	values = compactProjectAllStrings(values)
	if len(values) == 0 {
		return []string{fallback}
	}
	return values
}

func fallbackProjectAllActors(values []string, protagonist string) []string {
	values = compactProjectAllStrings(values)
	if len(values) == 0 {
		return []string{fallbackProjectAllText(protagonist, "POV角色")}
	}
	return values
}

func fallbackProjectAllText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "未在当前投影中改变"
}

func firstProjectAllButterflyEffect(values []domain.DecisionButterflyEffect) string {
	for _, value := range values {
		if strings.TrimSpace(value.Effect) != "" {
			return value.Effect
		}
	}
	return ""
}

func firstProjectAllOption(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func projectAllLiteraryKnowledgeBoundary(plan domain.ChapterPlan) string {
	if plan.CausalSimulation.LiteraryRendering == nil {
		return ""
	}
	return plan.CausalSimulation.LiteraryRendering.KnowledgeBoundary
}

func firstProjectAllLocation(sim domain.ChapterWorldSimulation) string {
	for _, decision := range sim.CharacterDecisions {
		if strings.TrimSpace(decision.Location) != "" {
			return decision.Location
		}
	}
	return "本章正式计划限定的连续空间"
}

func pipelineProjectAllRelationshipChanges(plan domain.ChapterPlan) []string {
	var out []string
	for _, arc := range plan.CausalSimulation.RelationshipArcs {
		out = appendUniqueProjectAllString(out, strings.Join(append(append([]string{}, arc.Pair...), arc.NextEmotionalBeat), "："))
	}
	return out
}

func containsProjectAllChapter(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
