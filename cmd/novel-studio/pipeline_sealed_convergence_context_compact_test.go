package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestSealedConvergenceContinuationContextProductionShapedWhitelist(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	stateToken, err := domain.ProjectedPlanningContextSourceTokenV2(digest)
	if err != nil {
		t.Fatal(err)
	}
	productionNoise := strings.Repeat("不得进入 continuation 的宽背景与重复检索轨迹", 1600)
	payload := map[string]any{
		"_context_profile": "planning",
		"planning_context_access_receipt": map[string]any{
			"phase": "plan", "source_token": "context-access:" + strings.Repeat("b", 64),
			"submit_in": "causal_simulation.context_sources",
		},
		"project_all_state": map[string]any{
			"version": "projected-planning-context.v2", "generation_id": "pg2_fixture",
			"next_chapter": 5, "through_chapter": 4, "state_root": digest,
			"context_digest": digest,
			"predecessor_contract": map[string]any{
				"chapter": 4, "outgoing_consequence_id": "v1a1-c04",
				"outgoing_consequence_text": "四单完成、三单在途，第六单即将抵达。",
			},
			"open_obligations": []any{map[string]any{"id": "c5-duty", "contract": "兑现第六单并触发反向钓取"}},
		},
		"project_all_state_source_token": stateToken,
		"project_all_state_policy":       "逐字消费 predecessor contract",
		"sealed_convergence_replan_context": map[string]any{
			"version": "sealed-convergence-authority-overlay.v1", "generation_id": "pg2_fixture",
			"chapter": 5, "planning_context_digest": digest, "projected_pre_state_root": digest,
			"bundle_digest": digest, "promotion_receipt_digest": digest,
			"simulation_id": "sim-5", "simulation_digest": digest,
			"authority_receipt_digest": digest, "immutable_state_contract_sha256": digest,
			"diagnostics_digest": digest,
			"diagnostics":        map[string]any{"revision_focus": []string{"重排场景阻力"}},
		},
		"chapter_world_simulation": map[string]any{
			"status": "ready", "simulation_id": "sim-5",
			"protagonist_projection": map[string]any{
				"protagonist": "程野", "chosen_decision": "留在安全区继续协作",
				"decision_reason": "证据链仍可推进但不得让骑手涉险",
			},
		},
		"simulation_authority_receipt": map[string]any{"receipt_digest": digest, "simulation_id": "sim-5"},
		"sealed_short_accepted_prose_word_budget": map[string]any{
			"chapter": 5, "required_runes_min": 2444, "required_runes_max": 2600,
			"prior_accepted_chapters": 4, "prior_accepted_runes": 9398,
		},
		"current_chapter_outline": map[string]any{
			"chapter": 5, "title": "两下，停一停，再两下", "core_event": "第六单命中后反派叫名改策",
			"contract_refs": []string{"outline-contract:ch5"},
		},
		"structure": map[string]any{
			"chapter": 5, "title": "两下，停一停，再两下", "goal": "完成七门定位闭环",
			"conflict": "排除假阳性且守住安全边界", "hook": "反派为何必须拖到00:40",
			"required_beats": []string{"第六单命中", "反派叫名改策"}, "_world_simulation_id": "sim-5",
		},
		"gap_summary":    []string{"missing causal_beats", "missing render_capacity", "missing voice_logic"},
		"fields_present": []string{},
		"recommended_batches": []string{
			"batch1_pov_projection", "batch2_render_capacity", "batch3_voice_and_rendering", "batch4_project_contracts_if_required",
		},
		"working_memory": map[string]any{
			"chapter_plan_stage":        map[string]any{"status": "partial", "chapter": 5, "causal_fields_present": []string{}},
			"user_rules":                map[string]any{"chapter_words": map[string]any{"min": 2200, "max": 2600}},
			"world_codex":               productionNoise,
			"simulation_restart_policy": productionNoise,
		},
		"reference_pack": map[string]any{
			"literary_rendering_cards": map[string]any{
				"version": 1, "cards": []any{map[string]any{"id": "goal-causality", "move": "让转折从选择与反馈生出"}},
			},
			"rag_fact_receipt": map[string]any{
				"chapter": 5, "receipt_id": "fact-5", "source_token": "rag_fact_receipt:fact-5",
				"selected_facts_sha256": strings.Repeat("c", 64), "retrieval_policy": "local_bm25_keyword_hybrid_v1",
				"no_material":  false,
				"hits":         []any{map[string]any{"ref": "rag_fact_receipt:fact-5#chunk=one", "source_path": "outline.md"}},
				"usage_policy": "采用事实必须绑定 hit.ref", "query": productionNoise,
			},
			"retrieval_trace": productionNoise,
		},
		"rewrite_craft_pack": map[string]any{
			"receipt_id": "craft-5", "needs": []any{map[string]any{"id": "scene-resistance"}},
			"hits": []any{map[string]any{"ref": "craft_recall_receipt:craft-5#hit=1"}},
		},
		"selected_memory":     map[string]any{"rag_recall": productionNoise},
		"premise_sections":    map[string]any{"重复宽背景": productionNoise},
		"failed_body_surface": productionNoise,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := pipelineSealedConvergenceCompactContinuationContext(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production-shaped planning context: %d -> %d runes", utf8.RuneCount(raw), utf8.RuneCount(compact))
	if utf8.RuneCount(compact) >= 12_000 {
		t.Fatalf("compact production-shaped payload=%d runes, want <12000", utf8.RuneCount(compact))
	}
	text := string(compact)
	for _, want := range []string{
		"context-access:", stateToken, "predecessor_contract", "protagonist_projection",
		"current_chapter_outline", "required_runes_min", "2444",
		"chapter_plan_stage", "missing render_capacity", "fact-5#chunk=one",
		"craft_recall_receipt:craft-5#hit=1", "simulation_authority_receipt",
		"immutable_state_contract_sha256", pipelineSealedConvergenceContinuationContextVersion,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("compact payload lost %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{"不得进入 continuation", "retrieval_trace", "selected_memory", "failed_body_surface", `"query"`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("compact payload retained %q", forbidden)
		}
	}
	again, err := pipelineSealedConvergenceCompactContinuationContext(raw)
	if err != nil || string(again) != string(compact) {
		t.Fatalf("compact projection is not deterministic: err=%v", err)
	}
}

func TestSealedConvergenceContinuationContextRejectsAuthorityDrift(t *testing.T) {
	raw := json.RawMessage(`{
		"planning_context_access_receipt":{"phase":"plan","source_token":"context-access:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"project_all_state":{"generation_id":"pg2_a","next_chapter":5,"state_root":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","context_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","predecessor_contract":{}},
		"project_all_state_source_token":"project-all-state:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sealed_convergence_replan_context":{"generation_id":"pg2_drift","chapter":5,"planning_context_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","projected_pre_state_root":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","simulation_id":"sim-5"},
		"chapter_world_simulation":{"status":"ready","simulation_id":"sim-5"},
		"sealed_short_accepted_prose_word_budget":{"chapter":5,"required_runes_min":2444,"required_runes_max":2600},
		"structure":{"chapter":5},"gap_summary":[]
	}`)
	if _, err := pipelineSealedConvergenceCompactContinuationContext(raw); err == nil || !strings.Contains(err.Error(), "authority binding drift") {
		t.Fatalf("authority drift was not rejected: %v", err)
	}
}

// This opt-in test runs only against a disposable byte-for-byte copy of a
// production run. It is intentionally environment-gated so CI never depends
// on a user's novel or mutates a live workspace.
func TestSealedConvergenceContinuationContextCopiedProductionFixture(t *testing.T) {
	dir := strings.TrimSpace(os.Getenv("NOVEL_STUDIO_COPIED_PRODUCTION_FIXTURE"))
	if dir == "" {
		t.Skip("set NOVEL_STUDIO_COPIED_PRODUCTION_FIXTURE to a disposable output/novel copy")
	}
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const chapter = 5
	owner := fmt.Sprintf("pipeline-convergence-replan-ch%06d-pid%d-%d", chapter, os.Getpid(), time.Now().UnixNano())
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionProjectAll, TargetChapter: chapter,
		Owner: owner, ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Runtime.ReleasePipelineExecution(owner) })
	bounds, err := tools.InspectShortChapterWordBoundsFromAcceptedProse(st, chapter)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := tools.NewContextTool(st, assets.Load("").References, "").Execute(
		context.Background(), json.RawMessage(`{"chapter":5,"profile":"planning"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = pipelineSealedConvergencePlanningContextWithWordBounds(raw, bounds)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := pipelineSealedConvergenceCompactContinuationContext(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("copied production planning context: bytes=%d runes=%d -> bytes=%d runes=%d",
		len(raw), utf8.RuneCount(raw), len(compact), utf8.RuneCount(compact))
	var measured map[string]json.RawMessage
	if err := json.Unmarshal(compact, &measured); err == nil {
		for key, value := range measured {
			if utf8.RuneCount(value) > 100 {
				t.Logf("compact field %s=%d runes", key, utf8.RuneCount(value))
			}
		}
	}
	if got := utf8.RuneCount(compact); got > 9_000 {
		t.Fatalf("copied production compact prompt payload=%d runes, want <=9000", got)
	}
	for _, want := range []string{
		"planning_context_access_receipt", "project_all_state", "predecessor_contract",
		"protagonist_projection", "structure", "gap_summary", "required_runes_min",
		"immutable_state_contract_sha256",
	} {
		if !strings.Contains(string(compact), want) {
			t.Errorf("copied production compact payload lost %q", want)
		}
	}
}
