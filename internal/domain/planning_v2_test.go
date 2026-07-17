package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPlanningV2GenerationIdentityDigestAndSealAreImmutable(t *testing.T) {
	generation, registry, bundles := planningV2TestChain(t, 2)
	if err := ValidateProjectedChapterBundleChain(generation, bundles, registry); err != nil {
		t.Fatalf("valid sealed chain rejected: %v", err)
	}
	manifest := planningV2TestManifest(t, generation, registry, bundles)
	if err := ValidateProjectedChainManifestV2(manifest, generation, bundles, registry); err != nil {
		t.Fatalf("valid chain manifest rejected: %v", err)
	}
	seal := planningV2TestSeal(t, generation, manifest)
	if err := ValidateSealReceiptV2(seal, generation, manifest); err != nil {
		t.Fatalf("valid seal rejected: %v", err)
	}

	tampered := generation
	tampered.StableOutlineRoot = planningV2TestDigest(t, "other-outline")
	if err := ValidateSealReceiptV2(seal, tampered, manifest); err == nil {
		t.Fatal("mutated sealed generation was accepted")
	}

	tamperedBundle := bundles[1]
	tamperedBundle.ChapterPlan.Goal = "silently changed after sealing"
	if err := ValidateProjectedChapterBundle(tamperedBundle); err == nil {
		t.Fatal("mutated sealed bundle was accepted")
	}
}

func TestPlanningV2RejectsCoarseProjectionAsFormalBundle(t *testing.T) {
	generation, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	bundle.ProjectionLevel = "coarse"
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	err := ValidateProjectedChapterBundle(bundle)
	if err == nil || !strings.Contains(err.Error(), "coarse") {
		t.Fatalf("coarse projection was not rejected clearly: %v", err)
	}

	bundle = bundles[0]
	bundle.FormalWorldSimulation.CausalSteps = nil
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	err = ValidateProjectedChapterBundle(bundle)
	if err == nil || !strings.Contains(err.Error(), "coarse") {
		t.Fatalf("four-field-like incomplete projection was not rejected: %v", err)
	}

	if generation.ProjectedChapterCount != 1 {
		t.Fatalf("fixture generation count changed unexpectedly: %d", generation.ProjectedChapterCount)
	}
}

func TestPlanningV2BundleCarriesExactPlanRAGAndRenderContext(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	receipt := planningV2TestRAGReceipt(t, bundle.Chapter)
	bundle.RAGFactReceipt = &receipt
	bundle.RAGFactReceiptDigest, _ = RAGFactReceiptDigestV2(receipt)
	bundle.ChapterPlan.CausalSimulation.ContextSources = append(
		bundle.ChapterPlan.CausalSimulation.ContextSources,
		receipt.SourceToken(),
	)
	bundle.RenderContext, _ = BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.SourceBindings = append(bundle.SourceBindings, SourceBindingV2{
		Kind:            "rag_fact_receipt",
		SourceID:        receipt.ID,
		SourceDigest:    PlanningV2DigestPrefix + receipt.PayloadSHA256,
		ExactReferences: []string{receipt.SourceToken()},
		UsableFacts:     []string{"只使用已转化后的摊位灯具规格事实"},
		Transformation:  "把规格转化为角色可见的价格与安装选择",
		DoNotUse:        []string{"不得把原始召回文本交给正文"},
	})
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("receipt-backed bundle rejected: %v", err)
	}

	wrongChapter := receipt
	wrongChapter.Chapter++
	bundle.RAGFactReceipt = &wrongChapter
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil {
		t.Fatal("RAG receipt from another chapter was accepted")
	}

	bundle = bundles[0]
	var context map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &context); err != nil {
		t.Fatal(err)
	}
	context["_context_profile"] = "planning"
	bundle.RenderContext, _ = json.Marshal(context)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil {
		t.Fatal("non-draft prose context was accepted")
	}

	bundle = bundles[0]
	if err := json.Unmarshal(bundle.RenderContext, &context); err != nil {
		t.Fatal(err)
	}
	context["working"] = map[string]any{
		"nested": map[string]any{
			"projected_delta": bundle.ProjectedDelta,
		},
	}
	bundle.RenderContext, _ = json.Marshal(context)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil ||
		!strings.Contains(err.Error(), "server-only key") {
		t.Fatalf("nested hidden server state was accepted in render context: %v", err)
	}

	bundle = bundles[0]
	if err := json.Unmarshal(bundle.RenderContext, &context); err != nil {
		t.Fatal(err)
	}
	context["working"] = map[string]any{
		"nested": map[string]any{
			"chapter_world_simulation": map[string]any{
				"character_decisions": []map[string]any{{
					"character":      "offscreen-helper",
					"visible_to_pov": false,
					"hidden_pressures": []string{
						"主角尚未知的场外决定",
					},
				}},
			},
		},
	}
	bundle.RenderContext, _ = json.Marshal(context)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil ||
		!strings.Contains(err.Error(), `server-only key "chapter_world_simulation"`) {
		t.Fatalf("nested chapter-world hidden state was accepted in render context: %v", err)
	}
}

func TestProjectedBundleRequiresHitBearingCraftInFrozenRenderPacket(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	receipt := *bundle.CraftRecallReceipt
	hit := CraftRecallReceiptHit{
		Ref:         CraftRecallReceiptSourceTokenV2(receipt) + "#chunk=method-1#hash=method-hash",
		ChunkID:     "method-1",
		ChunkHash:   "method-hash",
		SourcePath:  "deconstruction-library/writing-techniques/method.md",
		SourceKind:  "craft_method",
		Facet:       "craft",
		Summary:     "人物选择改变信息释放顺序。",
		Score:       0.9,
		UsageStages: []string{"plan", "writing"},
	}
	receipt.Attempts[0].Hits = []CraftRecallReceiptHit{hit}
	receipt.Attempts[0].NoMaterial = false
	receipt.PayloadSHA256 = ComputeCraftRecallReceiptPayloadSHA256(receipt)
	bundle.CraftRecallReceipt = &receipt
	bundle.CraftRecallReceiptDigest, _ = CraftRecallReceiptDigestV2(receipt)
	bundle.ChapterPlan.CausalSimulation.ExternalRefs = append(
		bundle.ChapterPlan.CausalSimulation.ExternalRefs,
		ExternalReferencePlan{
			QueryOrNeed:        receipt.Attempts[0].Need.ID,
			SourceType:         "craft_recall",
			SourceRefs:         []string{hit.Ref},
			UsableDetails:      []string{"让主角的核对动作先于解释。"},
			TransformationRule: "只迁移信息释放方法，不复制素材情节或措辞。",
			DoNotUse:           []string{"不复制原句"},
		},
	)
	bundle.SourceBindings = append(bundle.SourceBindings, SourceBindingV2{
		Kind:            "craft_recall_receipt",
		SourceID:        receipt.ID,
		SourceDigest:    bundle.CraftRecallReceiptDigest,
		ExactReferences: []string{CraftRecallReceiptSourceTokenV2(receipt)},
		UsableFacts:     []string{"只使用正式 plan 中转化后的 craft 方法"},
		Transformation:  "投影为 prose-safe craft method",
		DoNotUse:        []string{"不得暴露原始召回摘要"},
	})
	bundle.RenderContext, _ = BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil ||
		!strings.Contains(err.Error(), "requires render_packet.craft_methods") {
		t.Fatalf("hit-bearing craft receipt was accepted without frozen craft method: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(bundle.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	packet := payload["draft_packet"].(map[string]any)
	packet["craft_methods"] = []any{map[string]any{
		"receipt_id": receipt.ID,
		"need":       receipt.Attempts[0].Need.ID,
		"source_refs": []string{
			hit.Ref,
		},
	}}
	bundle.RenderContext, _ = json.Marshal(payload)
	bundle.RenderContext, _ = BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("exact receipt-backed frozen craft method was rejected: %v", err)
	}

	packet["craft_methods"] = []any{map[string]any{
		"receipt_id": receipt.ID,
		"need":       receipt.Attempts[0].Need.ID,
		"source_refs": []string{
			CraftRecallReceiptSourceTokenV2(receipt) + "#chunk=other#hash=other",
		},
	}}
	bundle.RenderContext, _ = json.Marshal(payload)
	bundle.RenderContext, _ = BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	bundle.RenderContextSHA256, _ = ComputePlanningV2JSONDigest(bundle.RenderContext)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err == nil ||
		!strings.Contains(err.Error(), "lacks an exact receipt hit ref") {
		t.Fatalf("frozen craft method accepted a ref outside exact receipt hit: %v", err)
	}
}

func TestPlanningV2DeltaRootIsOrderIndependentAndSharedWithOutcome(t *testing.T) {
	left := planningV2TestDelta(4)
	left.CharacterState = []StateMutationV2{
		{StableID: "character:b", Subject: "b", Field: "stance", Operation: "set", After: "wait", Cause: "pressure"},
		{StableID: "character:a", Subject: "a", Field: "stance", Operation: "set", After: "act", Cause: "deadline"},
	}
	right := left
	right.CharacterState = []StateMutationV2{left.CharacterState[1], left.CharacterState[0]}
	pre := planningV2TestDigest(t, "pre-state")
	leftRoot, err := DeriveProjectedPostStateRootV2(pre, left)
	if err != nil {
		t.Fatal(err)
	}
	rightRoot, err := DeriveStructuredStateRootV2(pre, right)
	if err != nil {
		t.Fatal(err)
	}
	if leftRoot != rightRoot {
		t.Fatalf("semantic mutation ordering changed state root: %s != %s", leftRoot, rightRoot)
	}

	outcome := ActualOutcomeReceiptV2{
		Version:                     ActualOutcomeReceiptV2Version,
		GenerationID:                planningV2TestGenerationID(t),
		Chapter:                     4,
		PromotionReceiptDigest:      planningV2TestDigest(t, "promotion"),
		ChapterBodySHA256:           planningV2TestDigest(t, "body"),
		CommitCheckpointSeq:         12,
		ActualDelta:                 right,
		ActualPreStateRoot:          pre,
		ActualPostStateRoot:         leftRoot,
		ActualCanonRoot:             planningV2TestDigest(t, "actual-canon"),
		ProjectedPostStateRoot:      leftRoot,
		ObligationsSatisfied:        []string{},
		ObligationsCreatedUnplanned: []string{},
		ProjectionMatch:             true,
		AcceptedAt:                  planningV2TestTime(),
	}
	outcome.ReceiptDigest, err = ComputeActualOutcomeReceiptV2Digest(outcome)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateActualOutcomeReceiptV2(outcome); err != nil {
		t.Fatalf("matching outcome rejected: %v", err)
	}
	missingCanon := outcome
	missingCanon.ActualCanonRoot = ""
	missingCanon.ReceiptDigest, _ = ComputeActualOutcomeReceiptV2Digest(missingCanon)
	if err := ValidateActualOutcomeReceiptV2(missingCanon); err == nil ||
		!strings.Contains(err.Error(), "actual_canon_root") {
		t.Fatalf("outcome without a durable actual canon root was accepted: %v", err)
	}
	outcome.ProjectionMatch = false
	outcome.ReceiptDigest, _ = ComputeActualOutcomeReceiptV2Digest(outcome)
	if err := ValidateActualOutcomeReceiptV2(outcome); err == nil {
		t.Fatal("false projection_match with equal structural roots was accepted")
	}
}

func TestPlanningV2RejectsUnboundHardChangeContract(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	bundle.HardRenderContract.KnowledgeChanges = []string{"主角知道一个 projected_delta 中不存在的秘密"}
	planningV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	err := ValidateProjectedChapterBundle(bundle)
	if err == nil || !strings.Contains(err.Error(), "must exactly equal visible projected changes") {
		t.Fatalf("free-floating hard knowledge change was accepted: %v", err)
	}
}

func TestPlanningV2RejectsMissingVisibleHardChangeContract(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	mutation := StateMutationV2{
		StableID:  "knowledge:protagonist:receipt",
		Subject:   "protagonist",
		Field:     "knowledge_boundary",
		Operation: "set",
		After:     "知道票据已经生效",
		Cause:     "亲眼看见盖章结果",
	}
	bundle.ProjectedDelta.Knowledge = []StateMutationV2{mutation}
	var err error
	bundle.ProjectedPostStateRoot, err = DeriveProjectedPostStateRootV2(
		bundle.ProjectedPreStateRoot,
		bundle.ProjectedDelta,
	)
	if err != nil {
		t.Fatal(err)
	}
	planningV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	err = ValidateProjectedChapterBundle(bundle)
	if err == nil || !strings.Contains(err.Error(), "must exactly equal visible projected changes") {
		t.Fatalf("missing visible hard knowledge change was accepted: %v", err)
	}

	bundle.HardRenderContract.KnowledgeChanges =
		[]string{RenderContractForStateMutationV2(mutation)}
	planningV2RebindRenderContext(t, &bundle)
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("exact visible hard knowledge change was rejected: %v", err)
	}
}

func TestPlanningV2ChainRejectsDigestAndStateDiscontinuity(t *testing.T) {
	generation, registry, bundles := planningV2TestChain(t, 2)

	broken := append([]ProjectedChapterBundle(nil), bundles...)
	broken[1].PreviousBundleDigest = planningV2TestDigest(t, "wrong-previous")
	broken[1].BundleDigest = planningV2MustBundleDigest(t, broken[1])
	err := ValidateProjectedChapterBundleChain(generation, broken, registry)
	if err == nil || !strings.Contains(err.Error(), "previous_bundle_digest") {
		t.Fatalf("broken digest chain was not rejected: %v", err)
	}

	broken = append([]ProjectedChapterBundle(nil), bundles...)
	broken[1].ProjectedPreStateRoot = planningV2TestDigest(t, "wrong-state")
	broken[1].ProjectedPostStateRoot, _ = DeriveProjectedPostStateRootV2(broken[1].ProjectedPreStateRoot, broken[1].ProjectedDelta)
	planningV2RebindRenderContext(t, &broken[1])
	broken[1].BundleDigest = planningV2MustBundleDigest(t, broken[1])
	err = ValidateProjectedChapterBundleChain(generation, broken, registry)
	if err == nil || !strings.Contains(err.Error(), "pre-state") {
		t.Fatalf("broken state chain was not rejected: %v", err)
	}
}

func TestProjectedPlanningContextCarriesCumulativeLatestStateBeyondRecentWindow(t *testing.T) {
	generation, registry, bundles := planningV2TestChain(t, 8)
	nextChapter := generation.LastProjectedChapter
	context, err := DeriveProjectedPlanningContextV2(
		generation,
		bundles[:len(bundles)-1],
		registry,
		nextChapter,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(context.RecentTransitions) != 6 {
		t.Fatalf("recent transition window = %d, want 6", len(context.RecentTransitions))
	}
	if len(context.CumulativeState) == 0 {
		t.Fatal("authoritative context lost cumulative state")
	}
	var timelineFacts []ProjectedPlanningStateFactV2
	for _, fact := range context.CumulativeState {
		if fact.Category == "timeline" &&
			fact.Subject == "story_clock" &&
			fact.Field == "chapter" {
			timelineFacts = append(timelineFacts, fact)
		}
	}
	if len(timelineFacts) != 1 ||
		timelineFacts[0].ThroughChapter != nextChapter-1 {
		t.Fatalf("cumulative latest timeline state = %+v", timelineFacts)
	}
	if context.CumulativeState[0].ThroughChapter >= context.NextChapter {
		t.Fatalf("cumulative state crossed next-chapter boundary: %+v", context.CumulativeState[0])
	}
}

func planningV2RebindRenderContext(t *testing.T, bundle *ProjectedChapterBundle) {
	t.Helper()
	var err error
	bundle.RenderContext, err = BindProjectedRenderContextV2(bundle.RenderContext, *bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlanningV2ObligationRegistryRejectsOrphanHardCommitment(t *testing.T) {
	generationID := planningV2TestGenerationID(t)
	obligation := planningV2TestObligation(t, generationID, 4, nil)
	registry := ObligationRegistryV2{
		Version:      ObligationRegistryV2Version,
		GenerationID: generationID,
		FirstChapter: 4,
		LastChapter:  6,
		Obligations:  []ObligationV2{obligation},
	}
	registry.RegistryRoot, _ = ComputeObligationRegistryV2Root(registry)
	err := ValidateObligationRegistryV2(registry)
	if err == nil || !strings.Contains(err.Error(), "no consumer") {
		t.Fatalf("orphan hard obligation was not rejected: %v", err)
	}

	registry.Obligations[0].DueWindow.TerminalResolution = true
	registry.RegistryRoot, _ = ComputeObligationRegistryV2Root(registry)
	if err := ValidateObligationRegistryV2(registry); err != nil {
		t.Fatalf("explicit terminal resolution was rejected: %v", err)
	}
}

func TestPlanningV2CursorsHaveIndependentContracts(t *testing.T) {
	generationID := planningV2TestGenerationID(t)
	projection := ProjectionCursorV2{
		GenerationID:         generationID,
		NextProjectChapter:   6,
		LastProjectedChapter: 5,
		LastBundleDigest:     planningV2TestDigest(t, "bundle-5"),
		BlockedReason:        "",
		UpdatedAt:            planningV2TestTime(),
	}
	projection.CursorDigest, _ = ComputeProjectionCursorV2Digest(projection)
	if err := ValidateProjectionCursorV2(projection); err != nil {
		t.Fatalf("projection cursor rejected: %v", err)
	}
	realization := RealizationCursorV2{
		ActiveGenerationID:       generationID,
		NextPromoteChapter:       4,
		ActivePromotedChapter:    0,
		LastAcceptedChapter:      3,
		BlockedByRewrites:        []int{},
		UpdatedAt:                planningV2TestTime(),
		LastOutcomeReceiptDigest: "",
	}
	realization.CursorDigest, _ = ComputeRealizationCursorV2Digest(realization)
	if err := ValidateRealizationCursorV2(realization); err != nil {
		t.Fatalf("realization cursor rejected: %v", err)
	}
	originalRealization := realization.CursorDigest
	projection.NextProjectChapter++
	projection.LastProjectedChapter++
	projection.CursorDigest, _ = ComputeProjectionCursorV2Digest(projection)
	if realization.CursorDigest != originalRealization {
		t.Fatal("projection cursor update changed realization cursor")
	}
}

func TestPlanningV2PromotionIsExactMechanicalMapping(t *testing.T) {
	_, _, bundles := planningV2TestChain(t, 1)
	bundle := bundles[0]
	planDigest, err := ComputeChapterPlanV2Digest(bundle.ChapterPlan)
	if err != nil {
		t.Fatal(err)
	}
	promotion := PromotionReceiptV2{
		Version:               PromotionReceiptV2Version,
		GenerationID:          bundle.GenerationID,
		Chapter:               bundle.Chapter,
		BundleDigest:          bundle.BundleDigest,
		ActualPreStateRoot:    bundle.ProjectedPreStateRoot,
		ProjectedPreStateRoot: bundle.ProjectedPreStateRoot,
		RenderDependencyRoot:  planningV2TestDigest(t, "render-dependencies"),
		FrozenPlanDigest:      planDigest,
		Mode:                  ExactPromotionModeV2,
		PromotedAt:            planningV2TestTime(),
	}
	promotion.ReceiptDigest, err = ComputePromotionReceiptV2Digest(promotion)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePromotionReceiptAgainstBundleV2(promotion, bundle); err != nil {
		t.Fatalf("exact promotion rejected: %v", err)
	}
	promotion.FrozenPlanDigest = planningV2TestDigest(t, "replanned")
	promotion.ReceiptDigest, _ = ComputePromotionReceiptV2Digest(promotion)
	if err := ValidatePromotionReceiptAgainstBundleV2(promotion, bundle); err == nil {
		t.Fatal("creative/replanned frozen plan was accepted as exact promotion")
	}
}

func TestPlanningV2DigestExcludesSelfDigestAndNothingElse(t *testing.T) {
	generation, _, bundles := planningV2TestChain(t, 1)
	first, err := ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	generation.GenerationDigest = planningV2TestDigest(t, "arbitrary-self-field")
	second, err := ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("generation digest included its own digest field")
	}
	generation.BaseStateRoot = planningV2TestDigest(t, "changed-state")
	third, err := ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("generation digest ignored a semantic root field")
	}

	bundle := bundles[0]
	original, err := ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest = planningV2TestDigest(t, "arbitrary-self-field")
	recomputed, err := ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if original != recomputed {
		t.Fatal("bundle digest included its own digest field")
	}
	bundle.HardRenderContract.MustOccur = append(bundle.HardRenderContract.MustOccur, "新增硬约束")
	changed, err := ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if changed == original {
		t.Fatal("bundle digest ignored a hard render contract change")
	}
}

func planningV2TestChain(t *testing.T, count int) (PlanningGenerationV2, ObligationRegistryV2, []ProjectedChapterBundle) {
	t.Helper()
	now := planningV2TestTime()
	baseCanonRoot := planningV2TestDigest(t, "canon")
	baseStateRoot := planningV2TestDigest(t, "state")
	outlineRoot := planningV2TestDigest(t, "outline")
	dependencyRoot := planningV2TestDigest(t, "dependencies")
	seedRoot, err := ComputePlanningSeedContractRootV2("seed contract")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := DerivePlanningGenerationAttemptV2ID(baseCanonRoot, outlineRoot, dependencyRoot, seedRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	registry := ObligationRegistryV2{
		Version:      ObligationRegistryV2Version,
		GenerationID: generationID,
		FirstChapter: 4,
		LastChapter:  3 + count,
		Obligations:  []ObligationV2{},
	}
	registry.RegistryRoot, err = ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := PlanningGenerationV2{
		Version:                PlanningGenerationV2Version,
		GenerationID:           generationID,
		Status:                 PlanningGenerationSealedV2,
		BaseCanonChapter:       3,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		FirstProjectedChapter:  4,
		LastProjectedChapter:   3 + count,
		ExpectedChapterCount:   count,
		ProjectedChapterCount:  count,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              now,
		SealedAt:               now,
	}
	genesis, err := DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]ProjectedChapterBundle, 0, count)
	pre := baseStateRoot
	previous := genesis
	for chapter := 4; chapter < 4+count; chapter++ {
		planningContext, err := DeriveProjectedPlanningContextV2(
			generation,
			bundles,
			registry,
			chapter,
		)
		if err != nil {
			t.Fatalf("derive planning context for chapter %d: %v", chapter, err)
		}
		bundle := planningV2TestBundle(
			t,
			generationID,
			chapter,
			previous,
			pre,
			planningContext.ContextDigest,
			planningContext.PredecessorContract,
		)
		bundles = append(bundles, bundle)
		previous = bundle.BundleDigest
		pre = bundle.ProjectedPostStateRoot
	}
	generation.ChainHeadRoot = bundles[0].BundleDigest
	generation.ChainTailRoot = bundles[len(bundles)-1].BundleDigest
	generation.GenerationDigest, err = ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	return generation, registry, bundles
}

func planningV2TestBundle(
	t *testing.T,
	generationID string,
	chapter int,
	previous string,
	pre string,
	planningContextDigest string,
	predecessor *ProjectedPlanningPredecessorContractV2,
) ProjectedChapterBundle {
	t.Helper()
	simulationID := "sim-" + generationID + "-" + string(rune('a'+chapter))
	fullSimulation := ChapterWorldSimulation{
		Version:      1,
		SimulationID: simulationID,
		Chapter:      chapter,
		GenerationID: generationID,
		TimeWindow:   "morning to noon",
		CharacterDecisions: []CharacterWorldDecision{
			{
				Character:         "protagonist",
				Location:          "street",
				CurrentGoal:       "验证规则",
				Pressure:          "时间",
				KnowledgeBoundary: "只知道亲历结果",
				AvailableOptions:  []string{"先验证再扩张", "立即扩张"},
				Decision:          "先验证再扩张",
				DecisionReason:    "保留撤回空间",
				Action:            "执行小额试验",
				ActionDuration:    "三小时",
				CompletionState:   "completed",
				ImmediateResult:   "获得反馈",
				StateAfter:        "证据增加",
			},
		},
		ProtagonistProjection: ProtagonistDecisionProjection{
			Protagonist:      "protagonist",
			AvailableOptions: []string{"先验证再扩张", "立即扩张"},
			ChosenDecision:   "先验证再扩张",
			DecisionReason:   "保留撤回空间",
			PlanConstraints:  []string{"不得全知泄漏"},
			CausalChain:      []string{"额度出现→验证→反馈"},
		},
	}
	plan := ChapterPlan{
		Chapter:  chapter,
		Title:    "第" + string(rune('0'+chapter)) + "章",
		Goal:     "完成可撤回的小范围试验",
		Conflict: "资源和信任同时受压",
		Hook:     "下一项真实选择到来",
		Contract: ChapterContract{
			RequiredBeats:    []string{"角色主动选择"},
			ForbiddenMoves:   []string{"不得替角色总结道理"},
			ContinuityChecks: []string{"资源数量连续"},
		},
		CausalSimulation: ChapterCausalSimulation{
			WorldSimulationID:   simulationID,
			ProtagonistDecision: "先验证再扩张",
			CausalBeats: []CausalSimulationBeat{
				{Cause: "额度出现", CharacterChoice: "小额验证", WorldResponse: "规则结算", StoryResult: "获得反馈"},
			},
			DecisionPoints: []string{"是否扩大试验"},
			OutcomeShift:   []string{"从猜测转为有证据"},
		},
	}
	plan.CausalSimulation.ArcTransition = ArcChapterTransitionContract{
		OutgoingConsequenceID:   fmt.Sprintf("fixture-consequence-%06d", chapter),
		OutgoingConsequenceText: fmt.Sprintf("chapter %d leaves a concrete consequence for chapter %d", chapter, chapter+1),
	}
	if predecessor != nil {
		plan.CausalSimulation.ArcTransition.IncomingConsequenceID = predecessor.OutgoingConsequenceID
		plan.CausalSimulation.ArcTransition.IncomingConsequenceText = predecessor.OutgoingConsequenceText
		plan.CausalSimulation.ArcTransition.ConsumedByCause = plan.CausalSimulation.CausalBeats[0].Cause
	}
	contextToken, err := ProjectedPlanningContextSourceTokenV2(planningContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	fullSimulation.Sources = append(fullSimulation.Sources, contextToken)
	factReceipt := planningV2TestNoMaterialRAGReceipt(t, chapter)
	craftReceipt := planningV2TestNoMaterialCraftReceipt(t, generationID, chapter, planningContextDigest)
	plan.CausalSimulation.ContextSources = append(
		plan.CausalSimulation.ContextSources,
		contextToken,
		factReceipt.SourceToken(),
		CraftRecallReceiptSourceTokenV2(craftReceipt),
	)
	craftDigest, err := CraftRecallReceiptDigestV2(craftReceipt)
	if err != nil {
		t.Fatal(err)
	}
	factDigest, err := RAGFactReceiptDigestV2(factReceipt)
	if err != nil {
		t.Fatal(err)
	}
	renderContext, err := json.Marshal(map[string]any{
		"_context_profile": "draft",
		"draft_packet": map[string]any{
			"chapter": chapter,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta := planningV2TestDelta(chapter)
	post, err := DeriveProjectedPostStateRootV2(pre, delta)
	if err != nil {
		t.Fatal(err)
	}
	bundle := ProjectedChapterBundle{
		Version:                ProjectedChapterBundleV2Version,
		GenerationID:           generationID,
		Chapter:                chapter,
		Authority:              ProjectedAuthorityV2,
		State:                  ProjectedStateV2,
		ProjectionLevel:        FormalProjectionLevelV2,
		PreviousBundleDigest:   previous,
		ProjectedPreStateRoot:  pre,
		ChapterWorldSimulation: fullSimulation,
		ChapterPlan:            plan,
		FormalWorldSimulation: FormalWorldSimulationV2{
			SimulationID: simulationID,
			InitialConditions: []SimulationStateFactV2{
				{ID: "initial", Subject: "protagonist", Field: "certainty", Value: "low"},
			},
			Actors: []SimulationActorV2{
				{
					CharacterID:      "protagonist",
					Motivation:       "验证规则",
					KnownFacts:       []string{"额度可见"},
					UnknownFacts:     []string{"结算结果"},
					OffscreenState:   "等待下一次观察",
					AvailableActions: []string{"验证", "放弃"},
				},
			},
			AvailableChoices: []string{"验证", "放弃"},
			ChosenDecision:   "验证",
			CausalSteps: []SimulationCausalStepV2{
				{
					ID:               "step-1",
					CauseIDs:         []string{"initial"},
					ActorID:          "protagonist",
					Decision:         "验证",
					ImmediateEffect:  "执行小额动作",
					DownstreamEffect: "获得可复核反馈",
				},
			},
			Counterfactuals: []SimulationCounterfactualV2{
				{Choice: "立即扩张", RejectedBy: "信息不足", Consequence: "失去撤回空间"},
			},
			TerminalConditions: []SimulationStateFactV2{
				{ID: "terminal", Subject: "protagonist", Field: "certainty", Value: "higher"},
			},
			TimeAdvance:  "推进三小时",
			LocationFlow: []string{"家中", "街面"},
		},
		POVPlan: POVPlanV2{
			POVCharacterID:    "protagonist",
			KnowledgeBoundary: []string{"只知道亲历结果"},
			Unknowns:          []string{"场外角色完整计划"},
			Motivations: []POVCharacterMotivationV2{
				{CharacterID: "protagonist", Goal: "验证", Pressure: "时间", Choice: "小额行动"},
			},
			OffscreenStates: []POVOffscreenStateV2{
				{CharacterID: "helper", State: "等待联络", CausalImpact: "限制资源"},
			},
			Scenes: []POVSceneV2{
				{
					SceneID:        "scene-1",
					Location:       "街面",
					Time:           "上午",
					PresentActors:  []string{"protagonist"},
					POVKnows:       []string{"当前价格"},
					POVDoesNotKnow: []string{"后台处理"},
					CausalPurpose:  "用行动验证规则",
				},
			},
			TimeAdvance: "推进三小时",
		},
		HardRenderContract: HardRenderContractV2{
			MustOccur:    []string{"完成小额验证"},
			MustNotOccur: []string{"不得全知泄漏"},
			MustPreserve: []string{"资源数量"},
			RevealBudget: []RevealBudgetItemV2{
				{FactID: "fact-1", Action: "partial", Limit: "只呈现结果"},
			},
		},
		SourceBindings: []SourceBindingV2{
			{
				Kind:            "source_snapshot",
				SourceID:        generationID,
				SourceDigest:    planningV2TestDigest(t, "source-"+generationID),
				ExactReferences: []string{"outline:chapter"},
				UsableFacts:     []string{"稳定章位"},
				Transformation:  "转换为本章行动边界",
				DoNotUse:        []string{"不复制原始措辞"},
			},
			{
				Kind:            "rag_fact_receipt",
				SourceID:        factReceipt.ID,
				SourceDigest:    factDigest,
				ExactReferences: []string{factReceipt.SourceToken()},
				UsableFacts:     []string{"本章无额外事实材料"},
				Transformation:  "保持 no_material 边界",
				DoNotUse:        []string{"不得补造事实"},
			},
			{
				Kind:            "craft_recall_receipt",
				SourceID:        craftReceipt.ID,
				SourceDigest:    craftDigest,
				ExactReferences: []string{CraftRecallReceiptSourceTokenV2(craftReceipt)},
				UsableFacts:     []string{"两类 craft need 均为 no_material"},
				Transformation:  "保持 no_material 边界",
				DoNotUse:        []string{"不得伪造 craft 引用"},
			},
		},
		RAGFactReceipt:           &factReceipt,
		RAGFactReceiptDigest:     factDigest,
		CraftRecallReceipt:       &craftReceipt,
		CraftRecallReceiptDigest: craftDigest,
		PlanningContextDigest:    planningContextDigest,
		RenderContext:            renderContext,
		ObligationsConsumed:      []string{},
		ObligationsCreated:       []string{},
		ObligationsCarried:       []string{},
		ProjectedDelta:           delta,
		ProjectedPostStateRoot:   post,
	}
	bundle.RenderContext, err = BindProjectedRenderContextV2(bundle.RenderContext, bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.RenderContextSHA256, err = ComputePlanningV2JSONDigest(bundle.RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	bundle.BundleDigest = planningV2MustBundleDigest(t, bundle)
	if err := ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatalf("fixture bundle invalid: %v", err)
	}
	return bundle
}

func planningV2TestDelta(chapter int) ProjectedDelta {
	return ProjectedDelta{
		Version: ProjectedDeltaV2Version,
		Timeline: []StateMutationV2{
			{
				StableID:  "timeline:" + string(rune('0'+chapter)),
				Subject:   "story_clock",
				Field:     "chapter",
				Operation: "advance",
				Before:    string(rune('0' + chapter - 1)),
				After:     string(rune('0' + chapter)),
				Cause:     "本章行动完成",
			},
		},
		CharacterState: []StateMutationV2{},
		Relationships:  []StateMutationV2{},
		Resources:      []StateMutationV2{},
		Knowledge:      []StateMutationV2{},
		Locations:      []StateMutationV2{},
		Foreshadows:    []StateMutationV2{},
		Obligations:    []StateMutationV2{},
	}
}

func planningV2TestManifest(t *testing.T, generation PlanningGenerationV2, registry ObligationRegistryV2, bundles []ProjectedChapterBundle) ProjectedChainManifestV2 {
	t.Helper()
	entries := make([]ProjectedBundleDigestEntryV2, 0, len(bundles))
	facts := make([]string, 0)
	craft := make([]string, 0)
	for _, bundle := range bundles {
		entry := ProjectedBundleDigestEntryV2{
			Chapter:                bundle.Chapter,
			BundleDigest:           bundle.BundleDigest,
			PreviousBundleDigest:   bundle.PreviousBundleDigest,
			ProjectedPreStateRoot:  bundle.ProjectedPreStateRoot,
			ProjectedPostStateRoot: bundle.ProjectedPostStateRoot,
		}
		if bundle.RAGFactReceipt != nil {
			entry.RAGFactReceiptDigest, _ = RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
			facts = append(facts, entry.RAGFactReceiptDigest)
		}
		if bundle.CraftRecallReceipt != nil {
			entry.CraftRecallReceiptDigest, _ = CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
			craft = append(craft, entry.CraftRecallReceiptDigest)
		}
		entries = append(entries, entry)
	}
	manifest := ProjectedChainManifestV2{
		Version:                ProjectedChainManifestV2Version,
		GenerationID:           generation.GenerationID,
		FirstChapter:           generation.FirstProjectedChapter,
		LastChapter:            generation.LastProjectedChapter,
		ChapterCount:           generation.ExpectedChapterCount,
		Entries:                entries,
		FactReceiptDigests:     facts,
		CraftReceiptDigests:    craft,
		ChainHeadRoot:          generation.ChainHeadRoot,
		ChainTailRoot:          generation.ChainTailRoot,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              planningV2TestTime(),
	}
	manifest.ManifestDigest, _ = ComputeProjectedChainManifestV2Digest(manifest)
	return manifest
}

func planningV2TestSeal(t *testing.T, generation PlanningGenerationV2, manifest ProjectedChainManifestV2) SealReceiptV2 {
	t.Helper()
	receipt := SealReceiptV2{
		Version:                SealReceiptV2Version,
		GenerationID:           generation.GenerationID,
		GenerationDigest:       generation.GenerationDigest,
		ChainManifestDigest:    manifest.ManifestDigest,
		ChainHeadRoot:          generation.ChainHeadRoot,
		ChainTailRoot:          generation.ChainTailRoot,
		ObligationRegistryRoot: generation.ObligationRegistryRoot,
		BaseCanonRoot:          generation.BaseCanonRoot,
		BaseStateRoot:          generation.BaseStateRoot,
		PlanningDependencyRoot: generation.PlanningDependencyRoot,
		SealedAt:               generation.SealedAt,
	}
	receipt.ReceiptDigest, _ = ComputeSealReceiptV2Digest(receipt)
	return receipt
}

func planningV2TestObligation(t *testing.T, generationID string, origin int, consumers []int) ObligationV2 {
	t.Helper()
	contract := "必须在终局前兑现资源代价"
	id, err := DeriveObligationIDV2(ObligationResourceV2, origin, contract)
	if err != nil {
		t.Fatal(err)
	}
	return ObligationV2{
		ID:       id,
		Kind:     ObligationResourceV2,
		Contract: contract,
		Origin: ObligationOriginV2{
			GenerationID: generationID,
			Chapter:      origin,
			SourceDigest: planningV2TestDigest(t, "obligation-source"),
		},
		DueWindow: ObligationDueWindowV2{
			FromChapter: origin,
			ToChapter:   origin + 2,
		},
		Hardness:         ObligationHardV2,
		State:            ObligationPlannedV2,
		ConsumerChapters: consumers,
		Evidence:         []ObligationEvidenceV2{},
		Supersedes:       []string{},
	}
}

func planningV2TestRAGReceipt(t *testing.T, chapter int) RAGFactReceipt {
	t.Helper()
	hit := RAGFactReceiptHit{
		Rank:          1,
		ChunkID:       "chunk-1",
		ContentSHA256: strings.Repeat("a", 64),
		SourcePath:    "refs/fact.md",
		SourceKind:    "project",
	}
	receipt, err := NewRAGFactReceipt(
		chapter,
		"摊位 灯具",
		[]string{"摊位", "灯具"},
		"project_facts_exact_v1",
		strings.Repeat("b", 64),
		[]RAGFactReceiptHit{hit},
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func planningV2TestNoMaterialRAGReceipt(t *testing.T, chapter int) RAGFactReceipt {
	t.Helper()
	receipt, err := NewRAGFactReceipt(
		chapter,
		"chapter fixture",
		[]string{"fixture"},
		"no_material_v1",
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func planningV2TestNoMaterialCraftReceipt(
	t *testing.T,
	generationID string,
	chapter int,
	planningContextDigest string,
) CraftRecallReceipt {
	t.Helper()
	receipt := CraftRecallReceipt{
		Version:               1,
		ID:                    fmt.Sprintf("%024x", chapter),
		Chapter:               chapter,
		Stage:                 ProjectAllCraftReceiptStage,
		GenerationID:          generationID,
		PlanningContextDigest: planningContextDigest,
		IndexIdentity:         "fixture-index",
		Enforcement:           true,
		CreatedAt:             planningV2TestTime(),
		Attempts: []CraftRecallReceiptAttempt{
			{
				Need:       CraftRecallNeed{ID: "project-all-methodology", Field: "methodology", Topic: "fixture methodology"},
				NoMaterial: true,
			},
			{
				Need:       CraftRecallNeed{ID: "project-all-scene", Field: "scene_situation", Topic: "fixture scene"},
				NoMaterial: true,
			},
		},
	}
	receipt.PayloadSHA256 = ComputeCraftRecallReceiptPayloadSHA256(receipt)
	if err := ValidateProjectAllCraftRecallReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func planningV2TestGenerationID(t *testing.T) string {
	t.Helper()
	base := planningV2TestDigest(t, "canon")
	outline := planningV2TestDigest(t, "outline")
	deps := planningV2TestDigest(t, "deps")
	seed, err := ComputePlanningSeedContractRootV2("seed")
	if err != nil {
		t.Fatal(err)
	}
	id, err := DerivePlanningGenerationAttemptV2ID(base, outline, deps, seed, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func planningV2TestDigest(t *testing.T, value string) string {
	t.Helper()
	digest, err := planningV2Digest(struct {
		Value string `json:"value"`
	}{value})
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func planningV2MustBundleDigest(t *testing.T, bundle ProjectedChapterBundle) string {
	t.Helper()
	digest, err := ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func planningV2TestTime() string {
	return time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC).Format(time.RFC3339)
}
