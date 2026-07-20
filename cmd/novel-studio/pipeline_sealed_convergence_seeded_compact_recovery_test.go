package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rag"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSealedConvergenceImmutableSeedMapHasExactAuthorizedRoot(t *testing.T) {
	seed := pipelineSealedConvergenceImmutableSeedMap(
		domain.ChapterCausalSimulation{
			ProjectPromise:  "双女主共同查明身份",
			ChapterFunction: "让证据命中变成当面叫名",
			InitialState: []domain.CharacterSimulationState{{
				Character: "程野",
			}},
			ReaderRetentionPlan: domain.ReaderRetentionPlan{RevealBudget: []string{"只揭示身份命中"}},
			ArcTransition:       domain.ArcChapterTransitionContract{ConsumedByCause: "上一章证据到手"},
			EndingContract:      domain.EndingConsequenceContract{EndingMode: "策略后果"},
			OutcomeShift:        []string{"程野失去继续装作不知的退路"},
		},
		domain.PlanningContextAccessTokenPrefix+strings.Repeat("a", 64),
		[]domain.ExternalReferencePlan{{
			QueryOrNeed: "身份核验", SourceType: "RAG",
			SourceRefs: []string{"rag_fact_receipt:fixture#chunk=one#hash=" + strings.Repeat("b", 64)},
		}},
	)
	if err := pipelineSealedConvergenceValidateImmutableSeedRoot(seed); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	var roots map[string]json.RawMessage
	if err := json.Unmarshal(raw, &roots); err != nil {
		t.Fatal(err)
	}
	if len(roots) != len(pipelineSealedConvergenceImmutableSeedKeys) {
		t.Fatalf("serialized seed root widened: %s", raw)
	}
	for _, forbidden := range []string{
		"anti_ai_execution_plan", "causal_beats", "decision_points", "emotional_logic",
		"literary_rendering_plan", "render_capacity", "voice_logic",
		"reader_reward_plan", "scene_constraints", "writing_norms_applied",
	} {
		if _, present := roots[forbidden]; present {
			t.Fatalf("explicit immutable seed serialized forbidden zero/mutable field %q: %s", forbidden, raw)
		}
	}
	seed["anti_ai_execution_plan"] = map[string]any{}
	if err := pipelineSealedConvergenceValidateImmutableSeedRoot(seed); err == nil {
		t.Fatal("mutable field widened the Host seed root")
	}
}

func TestSealedConvergenceSeededPendingAdoptionDoesNotOpenAReplayBudget(t *testing.T) {
	initial := "sha256:" + strings.Repeat("a", 64)
	changed := "sha256:" + strings.Repeat("b", 64)
	recovery := &pipelineSealedConvergenceSeededCompactFinalize{SeedToolDispatches: 1}
	continuation := &pipelineSealedConvergencePlannerContinuation{
		InitialPartialSHA256: initial,
		Replacement: &pipelineSealedConvergenceReplacementDispatch{
			BinaryFailover: &pipelineSealedConvergenceBinaryFailoverDispatch{
				SeededCompactFinalize: recovery,
			},
		},
	}
	if !pipelineSealedConvergenceRecoveryPartialMatches(continuation, changed) {
		t.Fatal("post-Execute/pre-SHA crash could not enter its strict adoption audit")
	}
	recovery.ModelDispatches = 1
	if pipelineSealedConvergenceRecoveryPartialMatches(continuation, changed) {
		t.Fatal("unknown seed side effect was admitted after compact dispatch 1/1")
	}
	recovery.ModelDispatches = 0
	recovery.SeedPartialSHA256 = changed
	if !pipelineSealedConvergenceRecoveryPartialMatches(continuation, changed) ||
		pipelineSealedConvergenceRecoveryPartialMatches(continuation, "sha256:"+strings.Repeat("c", 64)) {
		t.Fatal("adopted seed SHA did not become the only accepted successor partial")
	}
}

func TestSealedConvergenceMutableWrapperHasTwoCallRepairCeiling(t *testing.T) {
	inner := &seededCompactRecordingPlanDetailsTool{
		schema: map[string]any{"type": "object"},
	}
	wrapped := newPipelineSealedConvergenceMutablePlanDetailsTool(inner, 5, []string{"decision_points"})
	args := json.RawMessage(`{"chapter":5,"causal_simulation":{"decision_points":["当面叫名"]},"finalize":true}`)
	for i := 0; i < 2; i++ {
		if _, err := wrapped.Execute(context.Background(), args); err != nil {
			t.Fatalf("allowed repair call %d failed: %v", i+1, err)
		}
	}
	if _, err := wrapped.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "two-turn") {
		t.Fatalf("third plan_details call crossed the compact repair ceiling: %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("inner tool executed %d times, want exact ceiling 2", inner.calls)
	}
}

func TestSealedConvergenceRebindsExactSealedRAGReceiptAfterAdditiveRetrievalDrift(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)

	sealedChunk := rag.RehashChunk(domain.RAGChunk{
		ID: "chunk-sealed-rag", SourcePath: "meta/writing_assets.json",
		SourceKind: "project_fact", Text: "身份核验必须由当事人逐项比对编号和时间。",
	})
	sealedReceipt, err := domain.NewRAGFactReceipt(
		1, "身份核验", []string{"身份", "核验"}, "project_facts_exact_v1", "",
		[]domain.RAGFactReceiptHit{{
			Rank: 1, ChunkID: sealedChunk.ID, ContentSHA256: sealedChunk.Hash,
			SourcePath: sealedChunk.SourcePath, SourceKind: sealedChunk.SourceKind, Facet: sealedChunk.Facet,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	filtered := artifacts.Plan.CausalSimulation.ContextSources[:0]
	for _, source := range artifacts.Plan.CausalSimulation.ContextSources {
		if !strings.HasPrefix(source, domain.RAGFactReceiptTokenPrefix) {
			filtered = append(filtered, source)
		}
	}
	artifacts.Plan.CausalSimulation.ContextSources = append(filtered, sealedReceipt.SourceToken())
	artifacts.Plan.CausalSimulation.ExternalRefs = append(
		artifacts.Plan.CausalSimulation.ExternalRefs,
		domain.ExternalReferencePlan{
			QueryOrNeed: "身份核验动作", SourceType: "RAG",
			SourceRefs:         []string{sealedReceipt.Hits[0].Ref},
			UsableDetails:      []string{"逐项比对编号和时间"},
			TransformationRule: "转成角色现场核验动作",
			DoNotUse:           []string{"不复制召回原句"},
		},
	)
	artifacts.RAGFactReceipt = &sealedReceipt
	bundle, _, err := buildPipelineProjectedChapterBundle(
		generation, outline, genesis, generation.BaseStateRoot, artifacts, registry,
	)
	if err != nil {
		t.Fatal(err)
	}

	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	newerChunk := rag.RehashChunk(domain.RAGChunk{
		ID: "chunk-newer-rag", SourcePath: "meta/other_facts.json",
		SourceKind: "project_fact", Text: "另一轮检索新增但不替代冻结事实。",
	})
	if err := st.RAG.SaveIndexState(domain.RAGIndexState{
		SchemaVersion: domain.CurrentRAGIndexSchemaVersion,
		Chunks:        []domain.RAGChunk{sealedChunk, newerChunk},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveRAGFactReceipt(sealedReceipt); err != nil {
		t.Fatal(err)
	}
	newerReceipt, err := domain.NewRAGFactReceipt(
		1, "另一轮检索", []string{"新增"}, "project_facts_exact_v1", "",
		[]domain.RAGFactReceiptHit{{
			Rank: 1, ChunkID: newerChunk.ID, ContentSHA256: newerChunk.Hash,
			SourcePath: newerChunk.SourcePath, SourceKind: newerChunk.SourceKind, Facet: newerChunk.Facet,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RAG.SaveRAGFactReceipt(newerReceipt); err != nil {
		t.Fatal(err)
	}
	eligibility := &pipelineSealedConvergenceReplanEligibility{
		Intent: pipelineSealedConvergenceReplanIntent{SourceFrozen: pipelineFrozenPlan{
			Chapter: 1, ProjectedBundleDigest: bundle.BundleDigest,
		}},
		Binding:    pipelineSealedRenderBinding{Bundle: bundle},
		Plan:       bundle.ChapterPlan,
		Simulation: bundle.ChapterWorldSimulation,
	}
	rebound, digest, err := pipelineSealedConvergenceRebindSealedRAGReceipt(st, eligibility)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := domain.RAGFactReceiptDigestV2(sealedReceipt)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || latest == nil {
		t.Fatalf("load rebound latest: %+v %v", latest, err)
	}
	if rebound.ID != sealedReceipt.ID || latest.ID != sealedReceipt.ID || digest != wantDigest || latest.ID == newerReceipt.ID {
		t.Fatalf("latest was not rebound to exact sealed receipt: rebound=%s latest=%s newer=%s", rebound.ID, latest.ID, newerReceipt.ID)
	}
}
