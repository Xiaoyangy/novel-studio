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
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestConvergenceReplanExecutionLockRequiresExactPipelineOwnerIdentity(t *testing.T) {
	valid := &domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionProjectAll, TargetChapter: 5, ProcessID: 66663,
		Owner: "pipeline-convergence-replan-ch000005-pid66663-1721376000000000000",
	}
	if !convergenceReplanExecutionLock(valid) {
		t.Fatal("real pipeline convergence owner format was not recognized")
	}
	for _, mutate := range []func(*domain.PipelineExecutionLock){
		func(lock *domain.PipelineExecutionLock) { lock.Owner = "convergence-replan-ch000005" },
		func(lock *domain.PipelineExecutionLock) { lock.TargetChapter = 6 },
		func(lock *domain.PipelineExecutionLock) { lock.ProcessID = 7 },
		func(lock *domain.PipelineExecutionLock) { lock.Mode = domain.PipelineExecutionRender },
	} {
		copy := *valid
		mutate(&copy)
		if convergenceReplanExecutionLock(&copy) {
			t.Fatalf("invalid convergence execution identity passed: %+v", copy)
		}
	}
}

func TestSealedConvergenceAuthorityOverlayIsContentAddressedAndIdentityOnly(t *testing.T) {
	digest := func(seed string) string {
		hash, err := domain.DeterministicPlanningHash(seed)
		if err != nil {
			t.Fatal(err)
		}
		return domain.PlanningV2DigestPrefix + hash
	}
	simulation := domain.ChapterWorldSimulation{
		Version: 1, SimulationID: "ch005-signed", Chapter: 5, GenerationID: "pg2_test",
		TimeWindow: "00:00后五分钟",
		AuthorityReceipt: &domain.SimulationAuthorityReceipt{
			Version: domain.SimulationAuthorityReceiptVersion,
			Mode:    domain.SimulationAuthorityModeGrounded, GenerationID: "pg2_test", Chapter: 5,
			ReceiptDigest: digest("authority"),
		},
	}
	bundle := domain.ProjectedChapterBundle{
		GenerationID: "pg2_test", Chapter: 5,
		PlanningContextDigest: digest("context"), ProjectedPreStateRoot: digest("pre"),
		BundleDigest: digest("bundle"), ChapterWorldSimulation: simulation,
	}
	overlay, err := NewSealedConvergenceAuthorityOverlay(
		bundle, digest("promotion"), digest("state-contract"), "2026-07-19T00:00:00Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSealedConvergenceAuthorityOverlaySchema(overlay); err != nil {
		t.Fatalf("fresh overlay failed validation: %v", err)
	}
	raw, err := json.Marshal(overlay)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"character_decisions", "protagonist_projection", "chapter_plan", "projected_delta"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("identity overlay carried mutable/story payload %q: %s", forbidden, raw)
		}
	}
	tampered := overlay
	tampered.SimulationID = "ch005-tampered"
	if err := validateSealedConvergenceAuthorityOverlaySchema(tampered); err == nil {
		t.Fatal("tampered overlay retained a valid content digest")
	}
}

func TestPlanDetailsProjectAllStateSourceInjectionIsConvergenceOnly(t *testing.T) {
	t.Run("ordinary project-all still requires the planner token", func(t *testing.T) {
		st := newPhaseTestStore(t)
		_, stateToken := installPlanningContextAccessProjectAll(
			t,
			st,
			1,
			"ordinary-plan-details-state-token",
		)
		accessToken := domain.PlanningContextAccessTokenPrefix + strings.Repeat("a", 64)
		merged := map[string]any{"context_sources": []any{accessToken}}
		err := ensurePlanDetailsProjectAllStateSource(
			st,
			1,
			merged,
			nil,
			stateToken,
		)
		if err == nil || !strings.Contains(err.Error(), "exact project-all-state authoritative source token") {
			t.Fatalf("ordinary project-all omitted model token without a precise failure: %v", err)
		}
		if projectAllStateSourcesContain(stringSliceFromAny(merged["context_sources"]), stateToken) {
			t.Fatal("ordinary project-all received a server-injected state token")
		}
		if got := stringSliceFromAny(merged["context_sources"]); len(got) != 1 || got[0] != accessToken {
			t.Fatalf("ordinary rejection mutated model sources: %#v", got)
		}
	})

	t.Run("exact sealed convergence overlay injects only state authority", func(t *testing.T) {
		st, simulation, stateToken, _ := sealedConvergencePlanDetailsStateFixture(t)
		accessToken := domain.PlanningContextAccessTokenPrefix + strings.Repeat("b", 64)
		merged := map[string]any{"context_sources": []any{accessToken, "current_chapter_outline"}}
		if err := ensurePlanDetailsProjectAllStateSource(
			st,
			1,
			merged,
			&simulation,
			stateToken,
		); err != nil {
			t.Fatalf("exact convergence state injection failed: %v", err)
		}
		sources := stringSliceFromAny(merged["context_sources"])
		if !projectAllStateSourcesContain(sources, stateToken) {
			t.Fatalf("verified convergence did not receive exact state token: %#v", sources)
		}
		accessSources := 0
		for _, source := range sources {
			if strings.HasPrefix(source, domain.PlanningContextAccessTokenPrefix) {
				accessSources++
				if source != accessToken {
					t.Fatalf("server forged or replaced opaque planning access token: %q", source)
				}
			}
		}
		if accessSources != 1 {
			t.Fatalf("planning access token count=%d, want exact model-submitted token only: %#v", accessSources, sources)
		}
	})

	t.Run("content-addressed overlay drift fails closed", func(t *testing.T) {
		st, simulation, stateToken, overlay := sealedConvergencePlanDetailsStateFixture(t)
		overlay.BundleDigest = sealedRAGGuardDigest(t, "drifted-convergence-bundle")
		var err error
		overlay.OverlayDigest, err = computeSealedConvergenceAuthorityOverlayDigest(overlay)
		if err != nil {
			t.Fatal(err)
		}
		writeSealedConvergenceAuthorityOverlayFixture(t, st, overlay)

		merged := map[string]any{"context_sources": []any{"current_chapter_outline"}}
		err = ensurePlanDetailsProjectAllStateSource(
			st,
			1,
			merged,
			&simulation,
			stateToken,
		)
		if err == nil || !strings.Contains(err.Error(), "sealed convergence authority overlay") {
			t.Fatalf("drifted convergence overlay did not fail closed: %v", err)
		}
		if projectAllStateSourcesContain(stringSliceFromAny(merged["context_sources"]), stateToken) {
			t.Fatal("drifted convergence overlay injected authoritative state token")
		}
	})
}

func sealedConvergencePlanDetailsStateFixture(
	t *testing.T,
) (*store.Store, domain.ChapterWorldSimulation, string, SealedConvergenceAuthorityOverlay) {
	t.Helper()
	st, plan, _ := sealedRAGGuardFixture(t, false)
	ragReceipt, err := st.RAG.LoadLatestRAGFactReceipt(1)
	if err != nil || ragReceipt == nil {
		t.Fatalf("load convergence fixture RAG receipt: receipt=%+v err=%v", ragReceipt, err)
	}
	simulation := sealedRAGGuardSimulation(plan.CausalSimulation.WorldSimulationID)
	simulation.AuthorityReceipt = &domain.SimulationAuthorityReceipt{
		Version:       domain.SimulationAuthorityReceiptVersion,
		Mode:          domain.SimulationAuthorityModeGrounded,
		Chapter:       1,
		ReceiptDigest: sealedRAGGuardDigest(t, "sealed-convergence-authority-receipt"),
	}
	generation, source, registry, bundle := sealedRAGGuardProjectedFixture(
		t,
		plan,
		simulation,
		*ragReceipt,
	)
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

	planningContext, err := domain.DeriveProjectedPlanningContextV2(
		generation,
		nil,
		registry,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	planningContextRaw, err := json.Marshal(planningContext)
	if err != nil {
		t.Fatal(err)
	}
	planningContextPath := filepath.Join(st.Dir(), filepath.FromSlash(projectAllStateContextPath))
	if err := os.MkdirAll(filepath.Dir(planningContextPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planningContextPath, planningContextRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf(
		"pipeline-convergence-replan-ch000001-pid%d-%d",
		os.Getpid(),
		time.Now().UnixNano(),
	)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: 1,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	overlay, err := NewSealedConvergenceAuthorityOverlay(
		bundle,
		promotion.ReceiptDigest,
		sealedRAGGuardDigest(t, "sealed-convergence-state-contract"),
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		t.Fatal(err)
	}
	writeSealedConvergenceAuthorityOverlayFixture(t, st, overlay)
	stateToken, err := domain.ProjectedPlanningContextSourceTokenV2(planningContext.ContextDigest)
	if err != nil {
		t.Fatal(err)
	}
	return st, bundle.ChapterWorldSimulation, stateToken, overlay
}

func writeSealedConvergenceAuthorityOverlayFixture(
	t *testing.T,
	st *store.Store,
	overlay SealedConvergenceAuthorityOverlay,
) {
	t.Helper()
	raw, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(SealedConvergenceAuthorityOverlayPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSealedConvergencePlanningProfileCompactsFutureAndProjectedHistory(t *testing.T) {
	digest := func(seed string) string {
		hash, err := domain.DeterministicPlanningHash(seed)
		if err != nil {
			t.Fatal(err)
		}
		return domain.PlanningV2DigestPrefix + hash
	}
	futureSecret := strings.Repeat("FUTURE-OUTLINE-MUST-BE-PRUNED", 2500)
	history := strings.Repeat("PROJECTED-HISTORY-MUST-BE-FOLDED", 1800)
	result := map[string]any{
		"sealed_convergence_replan_context": map[string]any{
			"version":                         "sealed-convergence-authority-overlay.v1",
			"immutable_state_contract_sha256": digest("contract"),
			"diagnostics":                     map[string]any{"issue_classes": []string{"local_structure:blocking"}},
		},
		"chapter_world_simulation": map[string]any{
			"status": "ready", "simulation_id": "sim-5",
			"protagonist_projection": domain.ProtagonistDecisionProjection{
				Protagonist: "程野", ChosenDecision: "继续核验", DecisionReason: "证据仍不足",
			},
		},
		"project_all_state": domain.ProjectedPlanningContextV2{
			Version: "projected-planning-context.v2", GenerationID: "pg2_test", NextChapter: 5,
			ThroughChapter: 4, StateRoot: digest("state"), ContextDigest: digest("context"),
			PredecessorContract: &domain.ProjectedPlanningPredecessorContractV2{
				Chapter: 4, OutgoingConsequenceID: "c4", OutgoingConsequenceText: "第4章不可逆后果",
			},
			CumulativeState: []domain.ProjectedPlanningStateFactV2{{StableID: "history", Value: history}},
			OpenObligations: []domain.ProjectedPlanningObligationV2{{ID: "must-keep", Contract: "本章必须保留的义务"}},
		},
		"working_memory": map[string]any{
			"current_chapter_outline": domain.OutlineEntry{Chapter: 5, Title: "当前章", CoreEvent: "继续核验"},
			"future_outline_window":   []domain.OutlineEntry{{Chapter: 6, CoreEvent: futureSecret}},
			"user_rules":              map[string]any{"chapter_words": "2200-2600"},
		},
		"future_outline_window": []domain.OutlineEntry{{Chapter: 6, CoreEvent: futureSecret}},
	}
	applyPlanningContextProfile(result)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= contextBudget(5, "planning") {
		t.Fatalf("sealed convergence context still exceeds planning budget: %d", len(raw))
	}
	text := string(raw)
	for _, forbidden := range []string{"FUTURE-OUTLINE-MUST-BE-PRUNED", "PROJECTED-HISTORY-MUST-BE-FOLDED", "cumulative_state"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sealed convergence context retained %q", forbidden)
		}
	}
	for _, required := range []string{"current_chapter_outline", "must-keep", "protagonist_projection", "immutable_state_contract_sha256"} {
		if !strings.Contains(text, required) {
			t.Fatalf("sealed convergence context lost required authority %q: %s", required, text)
		}
	}
}

func TestSealedConvergenceCriticalPacketUsesBoundedNinetySixKiBOverflow(t *testing.T) {
	diagnostics := map[string]any{
		"issue_classes":  []string{"local_structure:blocking"},
		"revision_focus": []string{"重组场景阻力并让选择产生可见代价"},
	}
	// current_chapter_outline is task authority and therefore intentionally not
	// silently trimmed. This live-shaped packet proves the special path has a
	// bounded 96 KiB ceiling without reopening the broad full profile.
	currentCore := strings.Repeat("当前章不可变事件边界", 2500)
	result := map[string]any{
		"sealed_convergence_replan_context": map[string]any{
			"version":     sealedConvergenceAuthorityOverlayVersion,
			"diagnostics": diagnostics,
		},
		"chapter_world_simulation": map[string]any{
			"status": "ready", "simulation_id": "sim-5",
			"protagonist_projection": domain.ProtagonistDecisionProjection{
				Protagonist: "程野", ChosenDecision: "继续核验", DecisionReason: "证据仍不足",
			},
		},
		"project_all_state": map[string]any{
			"generation_id": "pg2_test", "next_chapter": 5,
			"predecessor_contract": map[string]any{"outgoing_consequence_id": "c4"},
			"open_obligations":     []map[string]any{{"id": "must-keep", "contract": "本章必须履行"}},
		},
		"planning_context_access_receipt": map[string]any{
			"source_token": "context-access:test", "phase": "plan",
		},
		"working_memory": map[string]any{
			"current_chapter_outline": domain.OutlineEntry{Chapter: 5, Title: "当前章", CoreEvent: currentCore},
			"user_rules":              map[string]any{"chapter_words": "2200-2600"},
		},
	}
	raw, err := finalizeContextResult(result, 5, "planning")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= contextBudget(5, "planning") || len(raw) > contextHardBudget(5, "planning") {
		t.Fatalf("sealed overflow bytes=%d, want (65536,98304]", len(raw))
	}
	text := string(raw)
	for _, required := range []string{
		"current_chapter_outline", "protagonist_projection", "open_obligations",
		"planning_context_access_receipt", "user_rules", "revision_focus",
		"sealed_convergence_critical_overflow",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("sealed 96 KiB packet lost %q", required)
		}
	}
}
