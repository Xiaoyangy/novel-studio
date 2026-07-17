package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestChapterRenderRAGValidationUsesSealedReceiptOnlyForExactBinding(t *testing.T) {
	t.Run("ordinary plan rejects deleted live chunk", func(t *testing.T) {
		st, plan, _ := sealedRAGGuardFixture(t, false)
		deleteSealedRAGGuardLiveChunk(t, st)
		err := validateRAGFactPlanForChapterRender(st, plan.Chapter, plan)
		if err == nil || !strings.Contains(err.Error(), "已删除") {
			t.Fatalf("ordinary plan bypassed live chunk freshness: %v", err)
		}
	})

	t.Run("exact sealed marker and render lock permit immutable receipt replay", func(t *testing.T) {
		st, plan, marker := sealedRAGGuardFixture(t, true)
		deleteSealedRAGGuardLiveChunk(t, st)
		if err := validateRAGFactPlanForChapterRender(st, plan.Chapter, plan); err != nil {
			t.Fatalf("exact sealed receipt replay was rejected after live chunk deletion: %v", err)
		}
		if marker.ProjectionBinding != sealedV2ProjectionBinding {
			t.Fatalf("fixture did not install sealed marker: %+v", marker)
		}
	})

	t.Run("sealed marker mismatch falls back to live rejection", func(t *testing.T) {
		st, plan, marker := sealedRAGGuardFixture(t, true)
		marker.ProjectedBundleDigest = sealedRAGGuardDigest(t, "wrong-bundle")
		writeSealedRAGGuardMarker(t, st, marker)
		deleteSealedRAGGuardLiveChunk(t, st)
		err := validateRAGFactPlanForChapterRender(st, plan.Chapter, plan)
		if err == nil || !strings.Contains(err.Error(), "已删除") {
			t.Fatalf("mismatched sealed marker bypassed live freshness: %v", err)
		}
	})
}

func sealedRAGGuardFixture(
	t *testing.T,
	installSealedBinding bool,
) (*store.Store, domain.ChapterPlan, sealedV2FrozenPlanMarker) {
	t.Helper()
	chunk := domain.RAGChunk{
		ID:         "fact:sealed-rent",
		SourcePath: "summaries/00.json",
		SourceKind: "chapter_summary_facts",
		Facet:      "plot",
		Context:    "商铺 欠费单 钥匙",
		Summary:    "欠费单先确认，随后交钥匙。",
		Text:       "项目事实：承租人确认欠费单之后才交接钥匙。",
	}
	st := newRAGFactReceiptTestStore(t, []domain.RAGChunk{chunk})
	fresh := rag.RehashChunk(rag.NormalizeChunk(chunk))
	receipt, err := domain.NewRAGFactReceipt(
		1,
		"商铺 欠费单 钥匙",
		[]string{"商铺", "欠费单", "钥匙"},
		"project_facts_exact_v1",
		strings.Repeat("b", 64),
		[]domain.RAGFactReceiptHit{{
			Rank:          1,
			ChunkID:       fresh.ID,
			ContentSHA256: fresh.Hash,
			SourcePath:    fresh.SourcePath,
			SourceKind:    fresh.SourceKind,
			Facet:         fresh.Facet,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveRAGFactReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	simulationID := "sealed-rag-sim-1"
	plan := domain.ChapterPlan{
		Chapter:  1,
		Title:    "第一章",
		Goal:     "完成可撤回的小范围试验",
		Conflict: "资源和信任同时受压",
		Hook:     "钥匙交接后出现下一项选择",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"先确认欠费单，再交接钥匙"},
			ForbiddenMoves:   []string{"不得跳过确认手续"},
			ContinuityChecks: []string{"钥匙交接顺序"},
		},
		CausalSimulation: domain.ChapterCausalSimulation{
			WorldSimulationID:   simulationID,
			ProtagonistDecision: "先确认再接钥匙",
			ContextSources:      []string{receipt.SourceToken()},
			ExternalRefs: []domain.ExternalReferencePlan{{
				QueryOrNeed:        "商铺交接顺序",
				SourceType:         "RAG",
				SourceRefs:         []string{receipt.Hits[0].Ref},
				UsableDetails:      []string{"欠费单须先确认"},
				TransformationRule: "转成角色把欠费单推回桌面确认后才接钥匙的动作",
				DoNotUse:           []string{"不复制召回摘要"},
			}},
			GroundingDetails: []domain.GroundingDetailPlan{{
				Detail:        "欠费单先由承租人确认",
				SourceRef:     receipt.Hits[0].Ref,
				TransformedAs: "林澈把欠费单推回去逐项确认",
				SceneAnchor:   "钥匙交接",
			}},
			CausalBeats: []domain.CausalSimulationBeat{{
				Cause:           "房东拿出欠费单",
				CharacterChoice: "先核对再签收",
				WorldResponse:   "钥匙暂留桌面",
				StoryResult:     "交接顺序得到验证",
			}},
			DecisionPoints: []string{"是否先接钥匙"},
			OutcomeShift:   []string{"从口头承诺转为票据确认"},
		},
	}
	if !installSealedBinding {
		return st, plan, sealedV2FrozenPlanMarker{}
	}

	fullSimulation := sealedRAGGuardSimulation(simulationID)
	generation, source, registry, bundle := sealedRAGGuardProjectedFixture(
		t,
		plan,
		fullSimulation,
		receipt,
	)
	plan = bundle.ChapterPlan
	fullSimulation = bundle.ChapterWorldSimulation
	projected := st.ProjectedV2()
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projected.ProjectBundleAndAdvance(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		*cursor,
		bundle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	_, realization, err := projected.ActivateSealedGeneration(generation.GenerationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	promotion := sealedRAGGuardPromotion(t, bundle)
	if _, err := projected.Promote(*realization, promotion); err != nil {
		t.Fatal(err)
	}

	if err := st.SaveChapterWorldSimulation(fullSimulation); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"chapter_world_simulation",
		"meta/chapter_simulations/001.json",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    checkpoint.Digest,
		Owner:         "sealed-rag-test",
	}); err != nil {
		t.Fatal(err)
	}
	planDigest, err := domain.ComputeChapterPlanV2Digest(plan)
	if err != nil {
		t.Fatal(err)
	}
	marker := sealedV2FrozenPlanMarker{
		Version:                "pipeline-planning.v1",
		Chapter:                1,
		PlanDigest:             checkpoint.Digest,
		PlanningGenerationID:   generation.GenerationID,
		ProjectionBinding:      sealedV2ProjectionBinding,
		ProjectedPlanSHA256:    planDigest,
		ProjectedBundleDigest:  bundle.BundleDigest,
		PromotionReceiptDigest: promotion.ReceiptDigest,
	}
	writeSealedRAGGuardMarker(t, st, marker)
	return st, plan, marker
}

func sealedRAGGuardProjectedFixture(
	t *testing.T,
	plan domain.ChapterPlan,
	simulation domain.ChapterWorldSimulation,
	receipt domain.RAGFactReceipt,
) (
	domain.PlanningGenerationV2,
	domain.PlanningSourceSnapshotV2,
	domain.ObligationRegistryV2,
	domain.ProjectedChapterBundle,
) {
	t.Helper()
	baseCanonRoot := sealedRAGGuardDigest(t, "canon")
	baseStateRoot := sealedRAGGuardDigest(t, "state")
	outlineRoot := sealedRAGGuardDigest(t, "outline")
	dependencyRoot := sealedRAGGuardDigest(t, "dependencies")
	seedRoot, err := domain.ComputePlanningSeedContractRootV2("sealed rag test seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := domain.DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		outlineRoot,
		dependencyRoot,
		seedRoot,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	generation := domain.PlanningGenerationV2{
		Version:                domain.PlanningGenerationV2Version,
		GenerationID:           generationID,
		Status:                 domain.PlanningGenerationBuildingV2,
		BaseCanonChapter:       0,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FirstProjectedChapter:  1,
		LastProjectedChapter:   1,
		ExpectedChapterCount:   1,
		CreatedAt:              sealedRAGGuardTime(),
	}
	registry := domain.ObligationRegistryV2{
		Version:      domain.ObligationRegistryV2Version,
		GenerationID: generationID,
		FirstChapter: 1,
		LastChapter:  1,
		Obligations:  []domain.ObligationV2{},
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.PlanningSourceSnapshotV2{
		Version:                domain.PlanningSourceSnapshotV2Version,
		GenerationID:           generationID,
		BaseCanonChapter:       0,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FoundationSnapshotRoot: sealedRAGGuardDigest(t, "foundation-snapshot"),
		RAGSnapshotRoot:        sealedRAGGuardDigest(t, "rag-snapshot"),
		CapturedAt:             sealedRAGGuardTime(),
	}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	context, err := domain.DeriveProjectedPlanningContextV2(generation, nil, registry, 1)
	if err != nil {
		t.Fatal(err)
	}
	contextToken, err := domain.ProjectedPlanningContextSourceTokenV2(context.ContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	simulation.GenerationID = generationID
	simulation.Sources = append(simulation.Sources, contextToken)
	craftReceipt := sealedRAGGuardNoMaterialCraftReceipt(
		t,
		generationID,
		plan.Chapter,
		context.ContextDigest,
	)
	craftDigest, err := domain.CraftRecallReceiptDigestV2(craftReceipt)
	if err != nil {
		t.Fatal(err)
	}
	plan.CausalSimulation.ContextSources = append(
		plan.CausalSimulation.ContextSources,
		contextToken,
		domain.CraftRecallReceiptSourceTokenV2(craftReceipt),
	)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	renderContext, err := json.Marshal(map[string]any{
		"_context_profile": "draft",
		"draft_packet": map[string]any{
			"chapter": plan.Chapter,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta := domain.ProjectedDelta{
		Version: domain.ProjectedDeltaV2Version,
		Timeline: []domain.StateMutationV2{{
			StableID:  "timeline:1",
			Subject:   "story_clock",
			Field:     "chapter",
			Operation: "advance",
			Before:    "0",
			After:     "1",
			Cause:     "本章行动完成",
		}},
		CharacterState: []domain.StateMutationV2{},
		Relationships:  []domain.StateMutationV2{},
		Resources:      []domain.StateMutationV2{},
		Knowledge:      []domain.StateMutationV2{},
		Locations:      []domain.StateMutationV2{},
		Foreshadows:    []domain.StateMutationV2{},
		Obligations:    []domain.StateMutationV2{},
	}
	post, err := domain.DeriveProjectedPostStateRootV2(baseStateRoot, delta)
	if err != nil {
		t.Fatal(err)
	}
	bundle := domain.ProjectedChapterBundle{
		Version:                domain.ProjectedChapterBundleV2Version,
		GenerationID:           generationID,
		Chapter:                1,
		Authority:              domain.ProjectedAuthorityV2,
		State:                  domain.ProjectedStateV2,
		ProjectionLevel:        domain.FormalProjectionLevelV2,
		PreviousBundleDigest:   genesis,
		ProjectedPreStateRoot:  baseStateRoot,
		ChapterWorldSimulation: simulation,
		ChapterPlan:            plan,
		FormalWorldSimulation: domain.FormalWorldSimulationV2{
			SimulationID: simulation.SimulationID,
			InitialConditions: []domain.SimulationStateFactV2{{
				ID: "initial", Subject: "protagonist", Field: "certainty", Value: "low",
			}},
			Actors: []domain.SimulationActorV2{{
				CharacterID:      "protagonist",
				Motivation:       "验证交接顺序",
				KnownFacts:       []string{"欠费单在桌面"},
				UnknownFacts:     []string{"房东是否让步"},
				OffscreenState:   "等待确认",
				AvailableActions: []string{"确认", "跳过"},
			}},
			AvailableChoices: []string{"确认", "跳过"},
			ChosenDecision:   "确认",
			CausalSteps: []domain.SimulationCausalStepV2{{
				ID:               "step-1",
				CauseIDs:         []string{"initial"},
				ActorID:          "protagonist",
				Decision:         "先确认",
				ImmediateEffect:  "欠费单被核对",
				DownstreamEffect: "钥匙随后交接",
			}},
			Counterfactuals: []domain.SimulationCounterfactualV2{{
				Choice: "跳过确认", RejectedBy: "责任不清", Consequence: "后续争议",
			}},
			TerminalConditions: []domain.SimulationStateFactV2{{
				ID: "terminal", Subject: "protagonist", Field: "certainty", Value: "higher",
			}},
			TimeAdvance:  "推进一小时",
			LocationFlow: []string{"商铺"},
		},
		POVPlan: domain.POVPlanV2{
			POVCharacterID:    "protagonist",
			KnowledgeBoundary: []string{"只知道桌面材料"},
			Unknowns:          []string{"房东场外安排"},
			Motivations: []domain.POVCharacterMotivationV2{{
				CharacterID: "protagonist", Goal: "确认责任", Pressure: "时间", Choice: "先核对",
			}},
			OffscreenStates: []domain.POVOffscreenStateV2{{
				CharacterID: "landlord", State: "等待签收", CausalImpact: "钥匙暂不交出",
			}},
			Scenes: []domain.POVSceneV2{{
				SceneID:        "scene-1",
				Location:       "商铺",
				Time:           "上午",
				PresentActors:  []string{"protagonist", "landlord"},
				POVKnows:       []string{"欠费金额"},
				POVDoesNotKnow: []string{"房东底线"},
				CausalPurpose:  "验证交接顺序",
			}},
			TimeAdvance: "推进一小时",
		},
		HardRenderContract: domain.HardRenderContractV2{
			MustOccur:    []string{"先确认欠费单"},
			MustNotOccur: []string{"不得跳过手续"},
			MustPreserve: []string{"钥匙仍在桌面"},
		},
		SourceBindings: []domain.SourceBindingV2{
			{
				Kind:            "source_snapshot",
				SourceID:        generationID,
				SourceDigest:    source.SnapshotDigest,
				ExactReferences: []string{"outline:chapter:1"},
				UsableFacts:     []string{"稳定章位"},
				Transformation:  "转换为行动边界",
				DoNotUse:        []string{"不复制原始措辞"},
			},
			{
				Kind:            "rag_fact_receipt",
				SourceID:        receipt.ID,
				SourceDigest:    domain.PlanningV2DigestPrefix + receipt.PayloadSHA256,
				ExactReferences: []string{receipt.SourceToken()},
				UsableFacts:     []string{"欠费单先确认"},
				Transformation:  "转换为桌面确认动作",
				DoNotUse:        []string{"不复制召回摘要"},
			},
			{
				Kind:            "craft_recall_receipt",
				SourceID:        craftReceipt.ID,
				SourceDigest:    craftDigest,
				ExactReferences: []string{domain.CraftRecallReceiptSourceTokenV2(craftReceipt)},
				UsableFacts:     []string{"两类 craft need 均为 no_material"},
				Transformation:  "保持 no_material 边界",
				DoNotUse:        []string{"不得伪造 craft 引用"},
			},
		},
		RAGFactReceipt:           &receipt,
		RAGFactReceiptDigest:     domain.PlanningV2DigestPrefix + receipt.PayloadSHA256,
		CraftRecallReceipt:       &craftReceipt,
		CraftRecallReceiptDigest: craftDigest,
		PlanningContextDigest:    context.ContextDigest,
		RenderContext:            renderContext,
		ObligationsConsumed:      []string{},
		ObligationsCreated:       []string{},
		ObligationsCarried:       []string{},
		ProjectedDelta:           delta,
		ProjectedPostStateRoot:   post,
	}
	bundle.RenderContext, err = domain.BindProjectedRenderContextV2(
		bundle.RenderContext,
		bundle,
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = domain.ComputePlanningV2JSONDigest(
		bundle.RenderContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("sealed RAG bundle fixture invalid: %v", err)
	}
	return generation, source, registry, bundle
}

func sealedRAGGuardNoMaterialCraftReceipt(
	t *testing.T,
	generationID string,
	chapter int,
	planningContextDigest string,
) domain.CraftRecallReceipt {
	t.Helper()
	receipt := domain.CraftRecallReceipt{
		Version:               1,
		ID:                    fmt.Sprintf("%024x", chapter),
		Chapter:               chapter,
		Stage:                 domain.ProjectAllCraftReceiptStage,
		GenerationID:          generationID,
		PlanningContextDigest: planningContextDigest,
		IndexIdentity:         "fixture-index",
		Enforcement:           true,
		CreatedAt:             sealedRAGGuardTime(),
		Attempts: []domain.CraftRecallReceiptAttempt{
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-methodology", Field: "methodology", Topic: "fixture methodology"},
				NoMaterial: true,
			},
			{
				Need:       domain.CraftRecallNeed{ID: "project-all-scene", Field: "scene_situation", Topic: "fixture scene"},
				NoMaterial: true,
			},
		},
	}
	receipt.PayloadSHA256 = domain.ComputeCraftRecallReceiptPayloadSHA256(receipt)
	if err := domain.ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func sealedRAGGuardSimulation(id string) domain.ChapterWorldSimulation {
	return domain.ChapterWorldSimulation{
		Version:      1,
		SimulationID: id,
		Chapter:      1,
		TimeWindow:   "上午",
		CharacterDecisions: []domain.CharacterWorldDecision{{
			Character:         "protagonist",
			Location:          "商铺",
			CurrentGoal:       "确认责任",
			Pressure:          "时间",
			KnowledgeBoundary: "只知道桌面材料",
			AvailableOptions:  []string{"先确认再接钥匙", "立即接钥匙"},
			Decision:          "先确认再接钥匙",
			DecisionReason:    "避免责任不清",
			Action:            "核对欠费单",
			ActionDuration:    "一小时",
			CompletionState:   "completed",
			ImmediateResult:   "欠费责任确认",
			StateAfter:        "接过钥匙",
		}},
		ProtagonistProjection: domain.ProtagonistDecisionProjection{
			Protagonist:      "protagonist",
			AvailableOptions: []string{"先确认再接钥匙", "立即接钥匙"},
			ChosenDecision:   "先确认再接钥匙",
			DecisionReason:   "避免责任不清",
			PlanConstraints:  []string{"不得跳过确认"},
			CausalChain:      []string{"欠费单→核对→钥匙交接"},
		},
	}
}

func sealedRAGGuardPromotion(
	t *testing.T,
	bundle domain.ProjectedChapterBundle,
) domain.PromotionReceiptV2 {
	t.Helper()
	planDigest, err := domain.ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	receipt := domain.PromotionReceiptV2{
		Version:               domain.PromotionReceiptV2Version,
		GenerationID:          bundle.GenerationID,
		Chapter:               bundle.Chapter,
		BundleDigest:          bundle.BundleDigest,
		ActualPreStateRoot:    bundle.ProjectedPreStateRoot,
		ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
		RenderDependencyRoot:  sealedRAGGuardDigest(t, "render-dependencies"),
		FrozenPlanDigest:      planDigest,
		Mode:                  domain.ExactPromotionModeV2,
		PromotedAt:            sealedRAGGuardTime(),
	}
	receipt.ReceiptDigest, err = domain.ComputePromotionReceiptV2Digest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func deleteSealedRAGGuardLiveChunk(t *testing.T, st *store.Store) {
	t.Helper()
	state, err := st.RAG.LoadIndexState()
	if err != nil || state == nil {
		t.Fatalf("load live RAG index: state=%+v err=%v", state, err)
	}
	state.Chunks = nil
	if err := st.RAG.SaveIndexState(*state); err != nil {
		t.Fatal(err)
	}
}

func writeSealedRAGGuardMarker(
	t *testing.T,
	st *store.Store,
	marker sealedV2FrozenPlanMarker,
) {
	t.Helper()
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(currentFrozenPlanMarkerPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sealedRAGGuardDigest(t *testing.T, value string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"value": value})
	digest, err := domain.ComputePlanningV2JSONDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func sealedRAGGuardTime() string {
	return time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC).Format(time.RFC3339)
}

func TestValidateRAGFactPlanSealedStillRejectsReceiptAndTransformForgery(t *testing.T) {
	st, plan, _ := sealedRAGGuardFixture(t, false)
	deleteSealedRAGGuardLiveChunk(t, st)
	if err := ValidateRAGFactPlanSealed(st, plan); err != nil {
		t.Fatalf("sealed verifier unexpectedly required live chunk membership: %v", err)
	}
	forged := plan
	forged.CausalSimulation.ExternalRefs = append(
		[]domain.ExternalReferencePlan(nil),
		plan.CausalSimulation.ExternalRefs...,
	)
	forged.CausalSimulation.ExternalRefs[0].SourceRefs = []string{
		fmt.Sprintf(
			"%s%s#chunk=fact:sealed-rent#hash=%s",
			domain.RAGFactReceiptTokenPrefix,
			strings.TrimPrefix(plan.CausalSimulation.ContextSources[0], domain.RAGFactReceiptTokenPrefix)[:24],
			strings.Repeat("f", 64),
		),
	}
	if err := ValidateRAGFactPlanSealed(st, forged); err == nil {
		t.Fatal("sealed verifier accepted a forged receipt hit reference")
	}
}
