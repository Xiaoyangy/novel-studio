package domain

import (
	"strings"
	"testing"
)

func TestPlanningV2ArcRegistryUsesBookHorizonWithoutWeakeningLegacyScope(t *testing.T) {
	generation, registry := planningV2ArcGenerationFixture(
		t,
		"v001-a001",
		4,
		5,
		10,
		PlanningGenerationSealedV2,
		"",
	)
	obligation := planningV2ArcObligation(
		t,
		generation.GenerationID,
		4,
		8,
		ObligationPlannedV2,
		"跨弧抵达的审计回执必须在第八章改变主角选择",
	)
	generation, registry = planningV2ArcBindObligations(t, generation, registry, obligation)
	if err := ValidateObligationRegistryV2(registry); err != nil {
		t.Fatalf("arc registry rejected future obligation inside book horizon: %v", err)
	}
	if err := ValidateArcObligationCarryBoundaryV2(generation, registry); err != nil {
		t.Fatalf("valid cross-arc carry rejected: %v", err)
	}

	legacy := registry
	legacy.ProjectionScope = ""
	legacy.ScopeID = ""
	legacy.BookHorizonChapter = 0
	legacy.RegistryRoot, _ = ComputeObligationRegistryV2Root(legacy)
	if err := ValidateObligationRegistryV2(legacy); err == nil ||
		!strings.Contains(err.Error(), "due_window") {
		t.Fatalf("legacy registry silently gained an out-of-range horizon: %v", err)
	}
}

func TestPlanningV2ArcCarryRejectsDueWorkAndImportsOnlyFutureOpenWork(t *testing.T) {
	previous, previousRegistry := planningV2ArcGenerationFixture(
		t,
		"v001-a001",
		4,
		5,
		10,
		PlanningGenerationSealedV2,
		"",
	)
	future := planningV2ArcObligation(
		t,
		previous.GenerationID,
		4,
		8,
		ObligationPlannedV2,
		"罗成海在第八章带回盖章清单并迫使窗口公开排队规则",
	)
	superseded := planningV2ArcObligation(
		t,
		previous.GenerationID,
		4,
		8,
		ObligationSupersededV2,
		"旧版清单在第八章抵达后直接公开窗口规则",
	)
	future.Supersedes = []string{superseded.ID}
	satisfied := planningV2ArcObligation(
		t,
		previous.GenerationID,
		4,
		5,
		ObligationSatisfiedV2,
		"第五章必须完成桥面分流并留下可核对的计时记录",
	)
	previous, previousRegistry = planningV2ArcBindObligations(
		t,
		previous,
		previousRegistry,
		future,
		satisfied,
		superseded,
	)

	next, _ := planningV2ArcGenerationFixture(
		t,
		"v001-a002",
		6,
		7,
		10,
		PlanningGenerationBuildingV2,
		previous.GenerationID,
	)
	rebound, carried, err := CarryForwardArcObligationsV2(previous, previousRegistry, next)
	if err != nil {
		t.Fatalf("carry future obligation: %v", err)
	}
	if len(carried.Obligations) != 1 || carried.Obligations[0].ID != future.ID {
		t.Fatalf("carry registry did not contain exactly the future obligation: %+v", carried.Obligations)
	}
	if len(carried.Obligations[0].Supersedes) != 0 {
		t.Fatalf("carry retained a dangling supersession reference: %+v", carried.Obligations[0])
	}
	if carried.GenerationID != next.GenerationID ||
		carried.ScopeID != next.ScopeID ||
		carried.FirstChapter != 6 || carried.LastChapter != 7 ||
		carried.BookHorizonChapter != 10 ||
		rebound.ObligationRegistryRoot != carried.RegistryRoot {
		t.Fatalf("next arc identity was not rebound to carry registry: generation=%+v registry=%+v", rebound, carried)
	}
	if err := ValidatePlanningGenerationV2(rebound); err != nil {
		t.Fatalf("rebound next generation invalid: %v", err)
	}

	bad := future
	bad.DueWindow = ObligationDueWindowV2{FromChapter: 5, ToChapter: 5}
	bad.ConsumerChapters = []int{5}
	badPrevious, badRegistry := planningV2ArcBindObligations(
		t,
		previous,
		previousRegistry,
		bad,
		satisfied,
		superseded,
	)
	if _, _, err := CarryForwardArcObligationsV2(badPrevious, badRegistry, next); err == nil ||
		!strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("in-arc due obligation crossed arc boundary: %v", err)
	}
}

func TestPlanningV2SealedArcChainAcceptsArcScopeAndLegacyChainStillValid(t *testing.T) {
	legacyGeneration, legacyRegistry, legacyBundles := planningV2TestChain(t, 2)
	if err := ValidateProjectedChapterBundleChain(legacyGeneration, legacyBundles, legacyRegistry); err != nil {
		t.Fatalf("legacy unscoped chain regressed: %v", err)
	}

	generation := legacyGeneration
	registry := legacyRegistry
	generation.ProjectionScope = PlanningProjectionScopeArcV2
	generation.ScopeID = "v001-a001"
	generation.BookHorizonChapter = 10
	registry.ProjectionScope = generation.ProjectionScope
	registry.ScopeID = generation.ScopeID
	registry.BookHorizonChapter = generation.BookHorizonChapter
	registry.RegistryRoot, _ = ComputeObligationRegistryV2Root(registry)
	generation.ObligationRegistryRoot = registry.RegistryRoot

	genesis, err := DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundles := make([]ProjectedChapterBundle, 0, generation.ExpectedChapterCount)
	previous := genesis
	preState := generation.BaseStateRoot
	for chapter := generation.FirstProjectedChapter; chapter <= generation.LastProjectedChapter; chapter++ {
		planningContext, err := DeriveProjectedPlanningContextV2(generation, bundles, registry, chapter)
		if err != nil {
			t.Fatalf("derive arc planning context for chapter %d: %v", chapter, err)
		}
		bundle := planningV2TestBundle(
			t,
			generation.GenerationID,
			chapter,
			previous,
			preState,
			planningContext.ContextDigest,
			planningContext.PredecessorContract,
		)
		bundles = append(bundles, bundle)
		previous = bundle.BundleDigest
		preState = bundle.ProjectedPostStateRoot
	}
	generation.ChainHeadRoot = bundles[0].BundleDigest
	generation.ChainTailRoot = bundles[1].BundleDigest
	generation.GenerationDigest, _ = ComputePlanningGenerationV2Digest(generation)
	if err := ValidateProjectedChapterBundleChain(generation, bundles, registry); err != nil {
		t.Fatalf("sealed arc-scoped bundle chain rejected: %v", err)
	}

	context, err := DeriveProjectedPlanningContextV2(
		generation,
		bundles[:1],
		registry,
		bundles[1].Chapter,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantOutgoing := bundles[0].ChapterPlan.CausalSimulation.ArcTransition
	if context.PredecessorContract == nil ||
		context.PredecessorContract.OutgoingConsequenceID != wantOutgoing.OutgoingConsequenceID ||
		context.PredecessorContract.OutgoingConsequenceText != wantOutgoing.OutgoingConsequenceText ||
		context.PredecessorContract.ProjectedPostStateRoot != bundles[0].ProjectedPostStateRoot {
		t.Fatalf("planning context did not expose exact predecessor contract: %+v", context.PredecessorContract)
	}

	broken := append([]ProjectedChapterBundle(nil), bundles...)
	broken[1].ChapterPlan.CausalSimulation.ArcTransition.IncomingConsequenceText = "an unrelated adjacent chapter premise"
	broken[1].RenderContext, err = BindProjectedRenderContextV2(broken[1].RenderContext, broken[1])
	if err != nil {
		t.Fatal(err)
	}
	broken[1].RenderContextSHA256, err = ComputePlanningV2JSONDigest(broken[1].RenderContext)
	if err != nil {
		t.Fatal(err)
	}
	broken[1].BundleDigest, err = ComputeProjectedChapterBundleDigest(broken[1])
	if err != nil {
		t.Fatal(err)
	}
	brokenGeneration := generation
	brokenGeneration.ChainTailRoot = broken[1].BundleDigest
	brokenGeneration.GenerationDigest, err = ComputePlanningGenerationV2Digest(brokenGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProjectedChapterBundleChain(brokenGeneration, broken, registry); err == nil ||
		!strings.Contains(err.Error(), "incoming consequence does not exactly match") {
		t.Fatalf("unrelated adjacent plans were accepted as an arc transition: %v", err)
	}

	wrongCause := bundles[1].ChapterPlan
	wrongCause.CausalSimulation.ArcTransition.ConsumedByCause = "cause absent from causal beats"
	if err := ValidateArcChapterTransitionContract(wrongCause, context.PredecessorContract); err == nil ||
		!strings.Contains(err.Error(), "causal_beats") {
		t.Fatalf("unconsumed predecessor consequence was accepted: %v", err)
	}
}

func planningV2ArcGenerationFixture(
	t *testing.T,
	scopeID string,
	start int,
	end int,
	horizon int,
	status PlanningGenerationStatusV2,
	parent string,
) (PlanningGenerationV2, ObligationRegistryV2) {
	t.Helper()
	baseCanonRoot := planningV2TestDigest(t, "canon-"+scopeID)
	baseStateRoot := planningV2TestDigest(t, "state-"+scopeID)
	outlineRoot := planningV2TestDigest(t, "outline-arc")
	dependencyRoot := planningV2TestDigest(t, "dependency-"+scopeID)
	seedRoot, err := ComputePlanningSeedContractRootV2("arc carry seed")
	if err != nil {
		t.Fatal(err)
	}
	generationID, err := DerivePlanningGenerationAttemptV2ID(
		baseCanonRoot,
		outlineRoot,
		dependencyRoot,
		seedRoot,
		scopeID,
	)
	if err != nil {
		t.Fatal(err)
	}
	registry := ObligationRegistryV2{
		Version:            ObligationRegistryV2Version,
		GenerationID:       generationID,
		ProjectionScope:    PlanningProjectionScopeArcV2,
		ScopeID:            scopeID,
		BookHorizonChapter: horizon,
		FirstChapter:       start,
		LastChapter:        end,
		Obligations:        []ObligationV2{},
	}
	registry.RegistryRoot, err = ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation := PlanningGenerationV2{
		Version:                PlanningGenerationV2Version,
		GenerationID:           generationID,
		ParentGenerationID:     parent,
		ProjectionScope:        PlanningProjectionScopeArcV2,
		ScopeID:                scopeID,
		BookHorizonChapter:     horizon,
		Status:                 status,
		BaseCanonChapter:       start - 1,
		BaseCanonRoot:          baseCanonRoot,
		BaseStateRoot:          baseStateRoot,
		StableOutlineRoot:      outlineRoot,
		PlanningDependencyRoot: dependencyRoot,
		RandomSeedContractRoot: seedRoot,
		AttemptID:              scopeID,
		FirstProjectedChapter:  start,
		LastProjectedChapter:   end,
		ExpectedChapterCount:   end - start + 1,
		ObligationRegistryRoot: registry.RegistryRoot,
		CreatedAt:              planningV2TestTime(),
	}
	if status == PlanningGenerationSealedV2 {
		generation.ProjectedChapterCount = generation.ExpectedChapterCount
		generation.ChainHeadRoot = planningV2TestDigest(t, "head-"+scopeID)
		generation.ChainTailRoot = planningV2TestDigest(t, "tail-"+scopeID)
		generation.SealedAt = planningV2TestTime()
	}
	generation.GenerationDigest, err = ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanningGenerationV2(generation); err != nil {
		t.Fatalf("arc generation fixture invalid: %v", err)
	}
	if err := ValidateObligationRegistryV2(registry); err != nil {
		t.Fatalf("arc registry fixture invalid: %v", err)
	}
	return generation, registry
}

func planningV2ArcObligation(
	t *testing.T,
	generationID string,
	origin int,
	consumer int,
	state ObligationStateV2,
	contract string,
) ObligationV2 {
	t.Helper()
	id, err := DeriveObligationIDV2(ObligationRuleV2, origin, contract)
	if err != nil {
		t.Fatal(err)
	}
	obligation := ObligationV2{
		ID:       id,
		Kind:     ObligationRuleV2,
		Contract: contract,
		Origin: ObligationOriginV2{
			GenerationID: generationID,
			Chapter:      origin,
			SourceDigest: planningV2TestDigest(t, "source-"+id),
		},
		DueWindow: ObligationDueWindowV2{
			FromChapter: consumer,
			ToChapter:   consumer,
		},
		Hardness:         ObligationHardV2,
		State:            state,
		ConsumerChapters: []int{consumer},
		Evidence: []ObligationEvidenceV2{{
			Chapter:      origin,
			SourceDigest: planningV2TestDigest(t, "evidence-"+id),
			Detail:       "弧级规划记录了义务来源和兑现章",
		}},
		Supersedes: []string{},
	}
	if state == ObligationSatisfiedV2 {
		obligation.Evidence = append(obligation.Evidence, ObligationEvidenceV2{
			Chapter:      consumer,
			SourceDigest: planningV2TestDigest(t, "satisfied-"+id),
			Detail:       "本弧正文验收已证明义务兑现",
		})
	}
	return obligation
}

func planningV2ArcBindObligations(
	t *testing.T,
	generation PlanningGenerationV2,
	registry ObligationRegistryV2,
	obligations ...ObligationV2,
) (PlanningGenerationV2, ObligationRegistryV2) {
	t.Helper()
	registry.Obligations = append([]ObligationV2(nil), obligations...)
	var err error
	registry.RegistryRoot, err = ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest = ""
	generation.GenerationDigest, err = ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	return generation, registry
}
