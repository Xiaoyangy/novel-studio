package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestSealedConvergenceContinuationRoutesPastMissingFormalPlanArtifact(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	_, err := tools.CurrentChapterPlanCausalCheckpoint(st, 5)
	if err == nil {
		t.Fatal("missing formal plan unexpectedly produced a current checkpoint")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapped missing formal plan is not routable as absent: %v", err)
	}
}

func TestFreshConvergencePlannerSessionIdentityIsAttemptAndIntentBound(t *testing.T) {
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          pipelineFrozenPlan{Chapter: 5},
		PlannerRepairAttempts: 1,
		PlannerRepairLimit:    2,
		StartedAt:             "2026-07-19T00:00:00Z",
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	first, err := pipelineSealedConvergenceFreshPlannerSessionIdentity(5, intent)
	if err != nil {
		t.Fatal(err)
	}
	again, err := pipelineSealedConvergenceFreshPlannerSessionIdentity(5, intent)
	if err != nil || again != first {
		t.Fatalf("same durable reservation was not deterministic: first=%q again=%q err=%v", first, again, err)
	}

	intent.PlannerRepairAttempts = 2
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	second, err := pipelineSealedConvergenceFreshPlannerSessionIdentity(5, intent)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.Contains(first, "_ch05_a01_") || !strings.Contains(second, "_ch05_a02_") {
		t.Fatalf("paid dispatches did not receive distinct chapter/attempt identities: first=%q second=%q", first, second)
	}
	if _, err := pipelineSealedConvergenceFreshPlannerSessionIdentity(4, intent); err == nil {
		t.Fatal("chapter-drifted session identity was accepted")
	}
}

func TestSealedConvergenceReplanGateRejectsOrdinaryPlanInvocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  pipelineFlags
		stages []string
	}{
		{name: "no restart", flags: pipelineFlags{}, stages: []string{"plan"}},
		{name: "mixed stages", flags: pipelineFlags{Restart: true}, stages: []string{"plan", "render"}},
		{name: "wrong stage", flags: pipelineFlags{Restart: true}, stages: []string{"render"}},
		{name: "no exhausted proof", flags: pipelineFlags{Restart: true}, stages: []string{"plan"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := pipelineSealedConvergenceReplanAllowed(dir, tc.flags, tc.stages); err == nil {
				t.Fatal("ordinary sealed plan invocation was allowed")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("read-only eligibility check mutated prose/control files: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestCollectSealedConvergenceDiagnosticsIsSpecificAndProseFree(t *testing.T) {
	live, frozen, candidate := pipelineRenderConvergenceFixture(t)
	manifest, err := loadPipelineRenderCandidateManifest(candidate.OutputDir)
	if err != nil || manifest == nil {
		t.Fatalf("load candidate manifest: %v", err)
	}
	hashA, hashB, hashC := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	ledger := &pipelineRenderConvergenceLedger{
		Version:     pipelineRenderConvergenceLedgerVersion,
		CandidateID: manifest.CandidateID, GenerationID: manifest.GenerationID,
		Chapter: manifest.Chapter, PlanDigest: manifest.PlanDigest,
		PlanCheckpointSeq:      manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:  manifest.ProjectedBundleDigest,
		PromotionReceiptDigest: manifest.PromotionReceiptDigest,
		FailureLimit:           3,
		Records: []pipelineRenderConvergenceRecord{
			{BodySHA256: hashA, SemanticReject: true},
			{BodySHA256: hashB, StructuralBlock: true},
			{BodySHA256: hashC, ExternalBlocking: true},
		},
	}
	const failedProse = "绝不能进入规划上下文的失败正文密语"
	evidenceDir, err := pipelineRenderConvergenceEvidenceDir(live, *manifest, hashA)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(filepath.Join(evidenceDir, "reviews", "01.json"), domain.ReviewEntry{
		Chapter: 1, BodySHA256: hashA, Scope: "chapter", Verdict: "rewrite",
		Summary:    failedProse,
		Dimensions: []domain.DimensionScore{{Dimension: "pacing", Score: 41, Verdict: "fail", Comment: failedProse}},
		Issues: []domain.ConsistencyIssue{{
			Type: "pacing", Severity: "error", Description: failedProse, Evidence: failedProse,
			Suggestion: "把三个同功能场景压成一次不可撤回的选择，并让代价改变下一拍行动",
		}},
	})
	writeJSON(filepath.Join(evidenceDir, "reviews", "01_ai_gate.json"), map[string]any{
		"chapter": 1, "body_sha256": hashA,
		"rule_violations": []map[string]any{{"rule": "dialogue_conveyor_overuse", "severity": "error"}},
		"rewrite_focus":   []string{"拉开对白功能，让沉默、误判和后果承担不同的信息推进"},
	})
	briefPath := filepath.Join(evidenceDir, "reviews", "01_rewrite_brief.md")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := "## 必须修正\n\n- [pacing/error] " + failedProse + "\n  - 证据：" + failedProse + "\n  - 修法：把流程说明改成带关系代价的选择\n\n## 汇总改写建议\n\n- 让章末后果由主角的误判直接触发\n"
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(candidate.OutputDir, "reviews", "drafts", "01_full_rerender_required.json"), map[string]any{
		"chapter": 1, "evaluated_body_sha256": hashB, "source": "local_structural_gate",
		"revision_plan": []string{"重排段落功能并减少同序确认对白"}, "advice_complete": true,
	})

	diagnostics, err := collectPipelineSealedConvergenceDiagnostics(live, *manifest, ledger)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"pacing:fail:41", "pacing:error", "dialogue_conveyor_overuse:error", "local_structure:blocking", "把三个同功能场景压成一次不可撤回的选择", "重排段落功能"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnostics missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, failedProse) {
		t.Fatalf("failed prose leaked into diagnostics: %s", text)
	}
	eligibility := &pipelineSealedConvergenceReplanEligibility{
		Intent: pipelineSealedConvergenceReplanIntent{
			FailureCount: 3, FailureLimit: 3, FailedBodySHA256: []string{hashA, hashB, hashC},
			Diagnostics: diagnostics, DiagnosticsDigest: pipelineProjectAllDigest(diagnostics),
			PlannerRepairAttempts: 1, PlannerRepairLimit: 2,
		},
		Plan: domain.ChapterPlan{Chapter: frozen.Chapter, Title: "冻结计划"},
	}
	if task := pipelineSealedConvergenceReplanTask(eligibility); strings.Contains(task, failedProse) || !strings.Contains(task, "pacing:fail:41") {
		t.Fatalf("Planner task diagnostics visibility/prose isolation failed: %s", task)
	}
}

func TestSealedConvergencePlannerJournalIsBoundedAndPersistent(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	badPlan := domain.ChapterPlan{Chapter: 1, Title: "bad successor"}
	if err := st.Drafts.SaveChapterPlan(badPlan); err != nil {
		t.Fatal(err)
	}
	badCP, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := pipelineSealedConvergenceDiagnostics{IssueClasses: []string{"local_structure:blocking"}}
	intent := pipelineSealedConvergenceReplanIntent{
		Version:      pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen: pipelineFrozenPlan{PlanCheckpointSeq: 0},
		Diagnostics:  diagnostics, DiagnosticsDigest: pipelineProjectAllDigest(diagnostics),
		PlannerRepairAttempts: 1, PlannerRepairLimit: 2,
		PlannerFailures: []pipelineSealedConvergencePlannerFailure{{
			PlanDigest: badCP.Digest, PlanSeq: badCP.Seq, Class: "state_contract_drift",
		}},
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	eligibility := &pipelineSealedConvergenceReplanEligibility{Intent: intent}
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(dir, &intent, eligibility); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPipelineSealedConvergenceReplanIntent(dir)
	if err != nil || loaded == nil || loaded.PlannerRepairAttempts != 1 || len(loaded.PlannerFailures) != 1 {
		t.Fatalf("planner repair journal did not persist: loaded=%+v err=%v", loaded, err)
	}
	if !pipelineSealedConvergencePlannerAttemptAvailable(intent) {
		t.Fatal("bad first successor did not leave the one bounded correction slot")
	}
	if err := retirePipelineFormalPlan(st, 1, badCP.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "drafts", "01.plan.json")); !os.IsNotExist(err) {
		t.Fatalf("bad successor was not retired: %v", err)
	}
	archives, err := filepath.Glob(filepath.Join(dir, "meta", "planning", "retired_formal_plans", "ch000001", "*-plan-seq-*.json"))
	if err != nil || len(archives) == 0 {
		t.Fatalf("bad successor retirement is not auditable: paths=%v err=%v", archives, err)
	}
	correctedPlan := domain.ChapterPlan{Chapter: 1, Title: "corrected successor"}
	if err := st.Drafts.SaveChapterPlan(correctedPlan); err != nil {
		t.Fatal(err)
	}
	correctedCP, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil || correctedCP.Seq <= badCP.Seq {
		t.Fatalf("corrected successor did not get a higher epoch: cp=%+v err=%v", correctedCP, err)
	}
	intent.PlannerRepairAttempts++
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(dir, &intent, eligibility); err != nil {
		t.Fatal(err)
	}
	if intent.PlannerRepairAttempts != intent.PlannerRepairLimit || pipelineSealedConvergencePlannerAttemptAvailable(intent) {
		t.Fatalf("second corrected attempt did not consume exact bounded slot: %+v", intent)
	}
}

func TestSealedConvergenceCrashRecoveryRecognizesOnlyExactPublishedSuccessor(t *testing.T) {
	source := pipelineFrozenPlan{
		Chapter: 1, PlanningGenerationID: "pg2_generation",
		ProjectedBundleDigest:          "sha256:" + strings.Repeat("a", 64),
		PromotionReceiptDigest:         "sha256:" + strings.Repeat("b", 64),
		ConvergenceReplanReceiptDigest: "sha256:" + strings.Repeat("c", 64),
	}
	intent := pipelineSealedConvergenceReplanIntent{SourceFrozen: source}
	published := source
	published.ConvergenceReplanReceiptDigest = "sha256:" + strings.Repeat("d", 64)
	if !pipelineSealedConvergenceSuccessorAlreadyPublished(&published, intent) {
		t.Fatal("verified successor frozen marker was not recognized for crash cleanup")
	}
	if pipelineSealedConvergenceSuccessorAlreadyPublished(&source, intent) {
		t.Fatal("unchanged source frozen marker was mistaken for a published successor")
	}
	drifted := published
	drifted.ProjectedBundleDigest = "sha256:" + strings.Repeat("e", 64)
	if pipelineSealedConvergenceSuccessorAlreadyPublished(&drifted, intent) {
		t.Fatal("different bundle was mistaken for crash-safe successor publication")
	}
}

func TestSealedConvergenceContinuationJournalIsBackwardCompatibleAndOneShot(t *testing.T) {
	diagnostics := pipelineSealedConvergenceDiagnostics{IssueClasses: []string{"local_structure:blocking"}}
	legacyV2 := pipelineSealedConvergenceReplanIntent{
		Version:      pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen: pipelineFrozenPlan{Chapter: 5},
		Diagnostics:  diagnostics, DiagnosticsDigest: pipelineProjectAllDigest(diagnostics),
		PlannerRepairAttempts: 2, PlannerRepairLimit: 2,
	}
	legacyV2.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(legacyV2)
	raw, err := json.Marshal(legacyV2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "planner_continuation") {
		t.Fatalf("old v2 zero value changed its content-addressed JSON shape: %s", raw)
	}
	var roundTrip pipelineSealedConvergenceReplanIntent
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if got := pipelineSealedConvergenceReplanIntentDigest(roundTrip); got != legacyV2.IntentDigest {
		t.Fatalf("old v2 digest changed after continuation field was added: got=%s want=%s", got, legacyV2.IntentDigest)
	}

	reserved := legacyV2
	reserved.PlannerContinuation = &pipelineSealedConvergencePlannerContinuation{
		Version:              pipelineSealedConvergencePlannerContinuationVersion,
		PlannerAttempt:       2,
		InitialPartialSHA256: "sha256:" + strings.Repeat("a", 64),
		SessionIdentity:      "convergence_planner_continuation-ch05",
		ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	if !validPipelineSealedConvergencePlannerJournal(reserved) {
		t.Fatal("reserved-before-dispatch continuation was not crash-resumable")
	}
	reserved.PlannerContinuation.Dispatches = 1
	reserved.PlannerContinuation.DispatchedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if !validPipelineSealedConvergencePlannerJournal(reserved) {
		t.Fatal("exactly one dispatched continuation was rejected")
	}
	beforeAttempts := reserved.PlannerRepairAttempts
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(st.Dir(), "drafts", "05.plan.partial.json")
	partialBytes := []byte(`{"structure":{"chapter":5},"sentinel":"must-retain"}`)
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath, partialBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	err = pipelineSealedConvergenceContinuationConsumedStoreError(
		st,
		reserved,
		"sha256:"+strings.Repeat("b", 64),
	)
	for _, want := range []string{
		"already dispatched 1/1",
		"paid attempt 2/2",
		"attempts are not reset",
		"staged partial is retained",
		strings.Repeat("b", 64),
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("dispatched crash window missing %q: %v", want, err)
		}
	}
	if reserved.PlannerRepairAttempts != beforeAttempts || pipelineSealedConvergencePlannerAttemptAvailable(reserved) {
		t.Fatalf("continuation error reopened paid attempt budget: %+v", reserved)
	}
	if after, readErr := os.ReadFile(partialPath); readErr != nil || string(after) != string(partialBytes) {
		t.Fatalf("dispatched continuation failure retired/mutated staged partial: after=%q err=%v", after, readErr)
	}
	reserved.PlannerContinuation.Dispatches = 2
	if validPipelineSealedConvergencePlannerJournal(reserved) {
		t.Fatal("a second continuation dispatch passed the durable journal")
	}
}

func TestSealedConvergenceFirstPaidAttemptPartialReservesContinuationWithoutRetirement(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(st.Dir(), "drafts", "05.plan.partial.json")
	partialBytes := []byte(`{"structure":{"chapter":5,"title":"第五章","goal":"推进","conflict":"阻断","hook":"反转"},"sentinel":"paid-attempt-one"}`)
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath, partialBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	sourceFrozen := pipelineFrozenPlan{
		Chapter: 5, PlanCheckpointSeq: 138,
		PlanDigest: "sha256:" + strings.Repeat("a", 64),
		BaselineChapterSHA256: map[string]string{
			"chapters/01.md": "sha256:" + strings.Repeat("b", 64),
		},
	}
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          sourceFrozen,
		PlannerRepairAttempts: 1,
		PlannerRepairLimit:    2,
		StartedAt:             startedAt,
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	eligibility := &pipelineSealedConvergenceReplanEligibility{Intent: intent}
	if !pipelineSealedConvergencePaidAttemptContinuationEligible(intent) {
		t.Fatal("paid attempt 1/2 with no Planner failure was not continuation-eligible")
	}

	intent.PlannerContinuation = &pipelineSealedConvergencePlannerContinuation{
		Version:              pipelineSealedConvergencePlannerContinuationVersion,
		PlannerAttempt:       1,
		InitialPartialSHA256: pipelineBytesSHA(partialBytes),
		SessionIdentity:      "convergence_planner_continuation-ch05",
		ReservedAt:           time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := saveUpdatedPipelineSealedConvergenceReplanIntent(st.Dir(), &intent, eligibility); err != nil {
		t.Fatal(err)
	}
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("attempt 1/2 continuation reservation was rejected by the durable journal")
	}

	loaded, err := loadPipelineSealedConvergenceReplanIntent(st.Dir())
	if err != nil || loaded == nil {
		t.Fatalf("load persisted continuation: loaded=%+v err=%v", loaded, err)
	}
	if loaded.StartedAt != startedAt || loaded.PlannerRepairAttempts != 1 ||
		!reflect.DeepEqual(loaded.SourceFrozen, sourceFrozen) {
		t.Fatalf("continuation changed original intent identity: loaded=%+v", loaded)
	}
	if after, readErr := os.ReadFile(partialPath); readErr != nil || !reflect.DeepEqual(after, partialBytes) {
		t.Fatalf("continuation reservation changed paid partial: after=%q err=%v", after, readErr)
	}
	retired, err := filepath.Glob(filepath.Join(
		st.Dir(), "meta", "planning", "retired_formal_plans", "ch000005", "*",
	))
	if err != nil || len(retired) != 0 {
		t.Fatalf("continuation reservation retired an artifact: paths=%v err=%v", retired, err)
	}

	intent.PlannerContinuation.Dispatches = 1
	intent.PlannerContinuation.DispatchedAt = time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano)
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("attempt 1/2 continuation dispatch 1/1 was rejected")
	}
	intent.PlannerContinuation.Dispatches = 2
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("attempt 1/2 continuation exceeded its one-dispatch ceiling")
	}
}

func TestSealedConvergenceHostFinalizeJournalIsOneShotAndBackwardCompatible(t *testing.T) {
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          pipelineFrozenPlan{Chapter: 5, PlanCheckpointSeq: 138},
		PlannerRepairAttempts: 1,
		PlannerRepairLimit:    2,
	}
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("legacy intent without Host finalize journal was rejected")
	}
	now := time.Now().UTC()
	intent.HostFinalize = &pipelineSealedConvergenceHostFinalize{
		Version:              pipelineSealedConvergenceHostFinalizeVersion,
		PlannerAttempt:       1,
		InitialPartialSHA256: "sha256:" + strings.Repeat("a", 64),
		ReservedAt:           now.Format(time.RFC3339Nano),
	}
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("reserved Host finalize journal was not crash-safe")
	}
	intent.HostFinalize.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid123-1"
	intent.HostFinalize.PrefetchLockProcessID = 123
	intent.HostFinalize.PrefetchStartedAt = now.Add(time.Millisecond).Format(time.RFC3339Nano)
	intent.HostFinalize.PrefetchBaselineReceiptDigest = "sha256:" + strings.Repeat("b", 64)
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("prefetch write-ahead Host finalize journal was rejected")
	}
	intent.HostFinalize.AccessReceiptDigest = "sha256:" + strings.Repeat("c", 64)
	intent.HostFinalize.ArgsSHA256 = "sha256:" + strings.Repeat("d", 64)
	intent.HostFinalize.ToolDispatches = 1
	intent.HostFinalize.InvokedAt = now.Add(2 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("one Host-only tool invocation was rejected")
	}
	intent.HostFinalize.FailureClass = "semantic_preconsume_validation"
	intent.HostFinalize.FailurePartialSHA256 = "sha256:" + strings.Repeat("e", 64)
	intent.HostFinalize.FailedAt = now.Add(3 * time.Millisecond).Format(time.RFC3339Nano)
	intent.PlannerRepairAttempts = 2
	if !validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("terminal Host failure did not remain auditable after advancing to the remaining Planner attempt")
	}
	intent.PlannerRepairAttempts = 1
	intent.HostFinalize.ToolDispatches = 2
	if validPipelineSealedConvergenceHostFinalizeJournal(intent) {
		t.Fatal("a second Host-only invocation crossed the durable ceiling")
	}
}

func TestSealedConvergencePlannerJournalKeepsHistoricalContinuationAfterHostSemanticFailure(t *testing.T) {
	now := time.Now().UTC()
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          pipelineFrozenPlan{Chapter: 5, PlanCheckpointSeq: 138},
		PlannerRepairAttempts: 2,
		PlannerRepairLimit:    2,
		PlannerContinuation: &pipelineSealedConvergencePlannerContinuation{
			Version:              pipelineSealedConvergencePlannerContinuationVersion,
			PlannerAttempt:       1,
			InitialPartialSHA256: "sha256:" + strings.Repeat("1", 64),
			SessionIdentity:      "convergence_planner_continuation-ch05",
			Dispatches:           1,
			ReservedAt:           now.Format(time.RFC3339Nano),
			DispatchedAt:         now.Add(time.Millisecond).Format(time.RFC3339Nano),
		},
		HostFinalize: &pipelineSealedConvergenceHostFinalize{
			Version:                       pipelineSealedConvergenceHostFinalizeVersion,
			PlannerAttempt:                1,
			InitialPartialSHA256:          "sha256:" + strings.Repeat("2", 64),
			ReservedAt:                    now.Add(2 * time.Millisecond).Format(time.RFC3339Nano),
			PrefetchLockOwner:             "pipeline-convergence-replan-ch000005-pid123-1",
			PrefetchLockProcessID:         123,
			PrefetchStartedAt:             now.Add(3 * time.Millisecond).Format(time.RFC3339Nano),
			PrefetchBaselineReceiptDigest: "sha256:" + strings.Repeat("3", 64),
			AccessReceiptDigest:           "sha256:" + strings.Repeat("4", 64),
			ArgsSHA256:                    "sha256:" + strings.Repeat("5", 64),
			ToolDispatches:                1,
			InvokedAt:                     now.Add(4 * time.Millisecond).Format(time.RFC3339Nano),
			FailureClass:                  "semantic_preconsume_validation",
			FailurePartialSHA256:          "sha256:" + strings.Repeat("6", 64),
			FailedAt:                      now.Add(5 * time.Millisecond).Format(time.RFC3339Nano),
		},
	}
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("historical attempt-1 continuation was rejected after Host failure advanced to attempt 2/2")
	}
	intent.HostFinalize.FailureClass = ""
	if validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("attempt mismatch passed without the exact terminal Host failure proof")
	}
}

func TestSealedConvergenceExhaustedSeedJournalRequiresCompactedSessionAndOneTool(t *testing.T) {
	now := time.Now().UTC()
	intent := pipelineSealedConvergenceReplanIntent{
		Version:               pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen:          pipelineFrozenPlan{Chapter: 5, PlanCheckpointSeq: 138},
		PlannerRepairAttempts: 2,
		PlannerRepairLimit:    2,
		ExhaustedSeedFinalize: &pipelineSealedConvergenceExhaustedSeedFinalize{
			Version:                pipelineSealedConvergenceExhaustedSeedVersion,
			PlannerAttempt:         2,
			SourcePartialSHA256:    "sha256:" + strings.Repeat("a", 64),
			AllowedSeedKeys:        append([]string(nil), pipelineSealedConvergenceImmutableSeedKeys...),
			AllowedMutableKeys:     append([]string(nil), pipelineSealedConvergenceSeededMutableKeys...),
			WriterSessionSHA256:    "sha256:" + strings.Repeat("b", 64),
			WriterSessionBytes:     pipelineSealedConvergencePollutedSessionMinBytes,
			SessionCompactionCount: 1,
			SessionIdentity:        "convergence_planner_exhausted_compact_finalize-ch05",
			BinaryPath:             "/opt/novel-studio/codex",
			BinaryFileSHA256:       "sha256:" + strings.Repeat("f", 64),
			BinaryVersionOutput:    "codex-cli test",
			ReservedAt:             now.Format(time.RFC3339Nano),
		},
	}
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("reserved exhausted seed journal was rejected")
	}
	j := intent.ExhaustedSeedFinalize
	j.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid123-1"
	j.PrefetchLockProcessID = 123
	j.PrefetchStartedAt = now.Add(time.Millisecond).Format(time.RFC3339Nano)
	j.PrefetchBaselineReceiptDigest = "sha256:" + strings.Repeat("9", 64)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("exhausted seed prefetch write-ahead was rejected")
	}
	j.AccessReceiptDigest = "sha256:" + strings.Repeat("c", 64)
	j.SeedDigest = "sha256:" + strings.Repeat("d", 64)
	j.SeedArgsSHA256 = "sha256:" + strings.Repeat("e", 64)
	j.SeedToolDispatches = 1
	j.SeedInvokedAt = now.Add(2 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("one exhausted seed invocation was rejected")
	}
	j.SeedPartialSHA256 = "sha256:" + strings.Repeat("8", 64)
	j.SeedFailureClass = pipelineSealedConvergenceExhaustedSeedFailureClass
	j.SeededAt = now.Add(3 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("seeded exhausted compact journal was rejected")
	}
	j.PromptSHA256 = "sha256:" + strings.Repeat("7", 64)
	j.PromptRunes = 1024
	j.ModelDispatches = 1
	j.DispatchedAt = now.Add(4 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("one exhausted compact model dispatch was rejected")
	}
	j.DisabledToolRecovery = &pipelineSealedConvergenceDisabledToolRecovery{
		Version:              pipelineSealedConvergenceDisabledToolRecoveryVersion,
		SessionSHA256:        "sha256:" + strings.Repeat("6", 64),
		ToolCallID:           "second-repair",
		ArgsSHA256:           "sha256:" + strings.Repeat("5", 64),
		InitialPartialSHA256: "sha256:" + strings.Repeat("4", 64),
		ReservedAt:           now.Add(5 * time.Millisecond).Format(time.RFC3339Nano),
	}
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("reserved disabled-tool recovery was rejected")
	}
	r := j.DisabledToolRecovery
	r.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid124-2"
	r.PrefetchLockProcessID = 124
	r.PrefetchStartedAt = now.Add(6 * time.Millisecond).Format(time.RFC3339Nano)
	r.PrefetchBaselineReceiptDigest = "sha256:" + strings.Repeat("3", 64)
	r.AccessReceiptDigest = "sha256:" + strings.Repeat("2", 64)
	r.SourceArgsSHA256 = "sha256:" + strings.Repeat("1", 64)
	r.SourceToolDispatches = 1
	r.SourceInvokedAt = now.Add(7 * time.Millisecond).Format(time.RFC3339Nano)
	r.SourcePartialSHA256 = "sha256:" + strings.Repeat("0", 64)
	r.SourceSeededAt = now.Add(8 * time.Millisecond).Format(time.RFC3339Nano)
	r.PatchToolDispatches = 1
	r.PatchInvokedAt = now.Add(9 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("one exact disabled-tool recovery dispatch was rejected")
	}
	r.HostFinalize = &pipelineSealedConvergenceRecoveredHostFinalize{
		SourcePartialSHA256: "sha256:" + strings.Repeat("a", 64),
		ReservedAt:          now.Add(10 * time.Millisecond).Format(time.RFC3339Nano),
	}
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("reserved recovered Host finalize was rejected")
	}
	r.HostFinalize.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid125-3"
	r.HostFinalize.PrefetchLockProcessID = 125
	r.HostFinalize.PrefetchStartedAt = now.Add(11 * time.Millisecond).Format(time.RFC3339Nano)
	r.HostFinalize.PrefetchBaselineReceiptDigest = "sha256:" + strings.Repeat("b", 64)
	r.HostFinalize.AccessReceiptDigest = "sha256:" + strings.Repeat("c", 64)
	r.HostFinalize.ArgsSHA256 = "sha256:" + strings.Repeat("d", 64)
	r.HostFinalize.ToolDispatches = 1
	r.HostFinalize.InvokedAt = now.Add(12 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("one recovered Host finalize dispatch was rejected")
	}
	r.HostFinalize.UpgradeRetry = &pipelineSealedConvergenceRecoveredHostFinalizeRetry{
		SourcePartialSHA256: "sha256:" + strings.Repeat("f", 64),
		ReservedAt:          now.Add(13 * time.Millisecond).Format(time.RFC3339Nano),
	}
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("reserved recovered Host finalize upgrade was rejected")
	}
	r.HostFinalize.UpgradeRetry.PrefetchLockOwner = "pipeline-convergence-replan-ch000005-pid126-4"
	r.HostFinalize.UpgradeRetry.PrefetchLockProcessID = 126
	r.HostFinalize.UpgradeRetry.PrefetchStartedAt = now.Add(14 * time.Millisecond).Format(time.RFC3339Nano)
	r.HostFinalize.UpgradeRetry.PrefetchBaselineReceiptDigest = "sha256:" + strings.Repeat("1", 64)
	r.HostFinalize.UpgradeRetry.AccessReceiptDigest = "sha256:" + strings.Repeat("2", 64)
	r.HostFinalize.UpgradeRetry.ArgsSHA256 = "sha256:" + strings.Repeat("3", 64)
	r.HostFinalize.UpgradeRetry.ToolDispatches = 1
	r.HostFinalize.UpgradeRetry.InvokedAt = now.Add(15 * time.Millisecond).Format(time.RFC3339Nano)
	if !validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("one recovered Host finalize upgrade dispatch was rejected")
	}
	j.SeedToolDispatches = 2
	if validPipelineSealedConvergenceExhaustedSeedJournal(intent) {
		t.Fatal("a second exhausted seed invocation crossed its ceiling")
	}
}

func TestSealedConvergenceSessionAuditUsesRealAgentcoreToolCallSchema(t *testing.T) {
	dir := t.TempDir()
	identity := "convergence_planner_continuation-ch05"
	path := filepath.Join(dir, "meta", "sessions", "agents", identity+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	messages := []agentcore.Message{
		agentcore.UserMsg("host prompt"),
		{
			Role: agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
				ID: "details-1", Name: "plan_details", Args: json.RawMessage(`{"chapter":5}`),
			})},
		},
	}
	var raw []byte
	for _, message := range messages {
		row, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, row...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	audit, err := pipelineSealedConvergenceAuditSession(dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	if audit.Rows != 2 || audit.UserRows != 1 || audit.AssistantRows != 1 || audit.ToolCallBlocks != 1 {
		t.Fatalf("real agentcore toolCall row was not classified: %+v", audit)
	}
}

func TestSealedConvergenceExtractsExactSecondPatchBlockedBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	identity := "convergence_planner_exhausted_compact_finalize-ch05"
	path := filepath.Join(dir, "meta", "sessions", "agents", identity+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	rows := []string{
		`{"role":"user","content":[{"type":"text","text":"seeded"}]}`,
		`{"role":"assistant","content":[{"type":"toolCall","tool_call":{"id":"first","name":"plan_details","args":{"chapter":5,"causal_simulation":{"causal_beats":["first"]},"finalize":true}}}]}`,
		`{"role":"tool","content":[{"type":"text","text":"semantic rejection"}],"metadata":{"is_error":true,"tool_call_id":"first","tool_name":"plan_details"}}`,
		`{"role":"assistant","content":[{"type":"toolCall","tool_call":{"id":"second","name":"plan_details","args":{"chapter":5,"causal_simulation":{"render_capacity":{"total_target_runes":2500}},"finalize":false}}}]}`,
		`{"role":"tool","content":[{"type":"text","text":"\"tool \\\"plan_details\\\" disabled after 1 consecutive errors\""}],"metadata":{"is_error":true,"tool_call_id":"second"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	call, sessionSHA, err := pipelineSealedConvergenceExtractDisabledToolCall(
		dir, 5, identity, pipelineSealedConvergenceSeededMutableKeys,
	)
	if err != nil {
		t.Fatal(err)
	}
	if call.ID != "second" || !validPipelineSealedConvergenceDigest(sessionSHA) ||
		!strings.Contains(string(call.Args), `"render_capacity"`) {
		t.Fatalf("disabled exact patch was not recovered: call=%+v session=%s", call, sessionSHA)
	}
	var request struct {
		Finalize bool `json:"finalize"`
	}
	if err := json.Unmarshal(call.Args, &request); err != nil || request.Finalize {
		t.Fatalf("recovered args changed model finalize=false: args=%s err=%v", call.Args, err)
	}
}

func TestSealedConvergenceLegacyZeroSideEffectProofRequiresEveryExactInvariant(t *testing.T) {
	t.Run("exact live-compatible state", func(t *testing.T) {
		st, intent, partialSHA, _ := sealedConvergenceZeroSideEffectFixture(t)
		outcome, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
			st, intent, partialSHA, "", pipelineSealedConvergenceOutcomeLegacyZero, true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !outcome.LegacyV1Inference || outcome.TypedModelCallTimeout || outcome.SessionRows != 1 ||
			outcome.UserRows != 1 || outcome.AssistantRows != 0 || outcome.ToolRows != 0 ||
			outcome.ToolCallBlocks != 0 || outcome.FormalPlanPresent || outcome.AccessReceiptConsumed {
			t.Fatalf("legacy proof did not record exact zero-side-effect evidence: %+v", outcome)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *store.Store, pipelineSealedConvergenceReplanIntent, string, string)
		want   string
	}{
		{
			name: "assistant message",
			mutate: func(t *testing.T, st *store.Store, intent pipelineSealedConvergenceReplanIntent, _, _ string) {
				appendSealedConvergenceSessionMessage(t, st.Dir(), intent.PlannerContinuation.SessionIdentity, agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("partial output")}})
			},
			want: "not zero-side-effect",
		},
		{
			name: "tool call",
			mutate: func(t *testing.T, st *store.Store, intent pipelineSealedConvergenceReplanIntent, _, _ string) {
				appendSealedConvergenceSessionMessage(t, st.Dir(), intent.PlannerContinuation.SessionIdentity, agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "x", Name: "plan_details", Args: json.RawMessage(`{"chapter":5}`)})}})
			},
			want: "not zero-side-effect",
		},
		{
			name: "partial drift",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, _, _ string) {
				if err := os.WriteFile(filepath.Join(st.Dir(), "drafts", "05.plan.partial.json"), []byte(`{"drift":true}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "partial drift",
		},
		{
			name: "formal plan present",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, _, _ string) {
				if err := os.WriteFile(filepath.Join(st.Dir(), "drafts", "05.plan.json"), []byte(`{}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "formal plan is present",
		},
		{
			name: "access receipt consumed",
			mutate: func(t *testing.T, st *store.Store, _ pipelineSealedConvergenceReplanIntent, _, token string) {
				receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
				if err != nil || receipt == nil {
					t.Fatalf("load receipt: %+v %v", receipt, err)
				}
				if err := st.Runtime.ConsumePlanningContextAccessReceipt(*receipt, token, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			},
			want: "identity/consumption drift",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, intent, partialSHA, token := sealedConvergenceZeroSideEffectFixture(t)
			tc.mutate(t, st, intent, partialSHA, token)
			_, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
				st, intent, partialSHA, "", pipelineSealedConvergenceOutcomeLegacyZero, true,
			)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("mutation did not fail closed with %q: %v", tc.want, err)
			}
		})
	}
}

func TestSealedConvergenceReplacementJournalCrashWindowsAreBounded(t *testing.T) {
	st, intent, partialSHA, _ := sealedConvergenceZeroSideEffectFixture(t)
	outcome, err := pipelineSealedConvergenceCaptureZeroSideEffectOutcome(
		st, intent, partialSHA, "", pipelineSealedConvergenceOutcomeLegacyZero, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	continuation := intent.PlannerContinuation
	continuation.InitialAccessReceiptDigest = outcome.AccessReceiptDigest
	continuation.InitialPromptSHA256 = outcome.UserPromptSHA256
	continuation.ZeroSideEffectOutcome = outcome
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("atomic zero-side-effect outcome was not a valid backward-compatible journal extension")
	}
	if err := pipelineSealedConvergenceValidateZeroSideEffectOutcomeState(st, intent); err != nil {
		t.Fatalf("proof state did not survive the fresh-lock boundary: %v", err)
	}

	reservedAt := time.Now().UTC().Add(time.Millisecond)
	continuation.Replacement = &pipelineSealedConvergenceReplacementDispatch{
		Version:         pipelineSealedConvergenceReplacementDispatchVersion,
		SessionIdentity: "convergence_planner_continuation_replacement-ch05",
		ReservedAt:      reservedAt.Format(time.RFC3339Nano),
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("crash after replacement reservation was not resumable")
	}

	lock, err := pipelineSealedConvergenceCurrentReplacementLock(st, 5)
	if err != nil {
		t.Fatal(err)
	}
	continuation.Replacement.PrefetchLockOwner = lock.Owner
	continuation.Replacement.PrefetchLockProcessID = lock.ProcessID
	continuation.Replacement.PrefetchStartedAt = time.Now().UTC().Add(2 * time.Millisecond).Format(time.RFC3339Nano)
	continuation.Replacement.PrefetchBaselineReceiptDigest = outcome.AccessReceiptDigest
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("crash after prefetch write-ahead was not resumable")
	}

	replacementReceipt := saveSealedConvergenceTestReceipt(t, st, intent, lock.Owner, lock.ProcessID)
	if err := pipelineSealedConvergenceValidateZeroSideEffectOutcomeState(st, intent); err != nil {
		t.Fatalf("unconsumed receipt issued inside persisted prefetch crash window was rejected: %v", err)
	}
	// A later process may persist its new prefetch owner and then fail before
	// issuing another receipt. The write-ahead baseline digest keeps that exact
	// still-unconsumed prior receipt recoverable without widening dispatches.
	continuation.Replacement.PrefetchBaselineReceiptDigest = replacementReceipt.ReceiptDigest
	if err := pipelineSealedConvergenceValidateZeroSideEffectOutcomeState(st, intent); err != nil {
		t.Fatalf("failure before receipt re-issue lost the prefetch baseline crash proof: %v", err)
	}
	continuation.Replacement.AccessReceiptDigest = replacementReceipt.ReceiptDigest
	continuation.Replacement.PromptSHA256 = "sha256:" + strings.Repeat("9", 64)
	continuation.Replacement.Dispatches = 1
	continuation.Replacement.DispatchedAt = time.Now().UTC().Add(3 * time.Millisecond).Format(time.RFC3339Nano)
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if !validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("persisted replacement dispatch 1/1 was not valid")
	}
	continuation.Replacement.Dispatches = 2
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	if validPipelineSealedConvergencePlannerJournal(intent) {
		t.Fatal("a second replacement dispatch crossed the durable ceiling")
	}
}

func sealedConvergenceZeroSideEffectFixture(
	t *testing.T,
) (*store.Store, pipelineSealedConvergenceReplanIntent, string, string) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	chapter := 5
	partial := []byte(`{"structure":{"chapter":5,"title":"第五章","goal":"推进","conflict":"阻断","hook":"反转"},"sentinel":"unchanged"}`)
	partialPath := filepath.Join(st.Dir(), "drafts", "05.plan.partial.json")
	if err := os.MkdirAll(filepath.Dir(partialPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partialPath, partial, 0o644); err != nil {
		t.Fatal(err)
	}
	partialSHA := pipelineBytesSHA(partial)
	owner := fmt.Sprintf("pipeline-convergence-replan-ch%06d-pid%d-%d", chapter, os.Getpid(), time.Now().UnixNano())
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionProjectAll, TargetChapter: chapter, Owner: owner,
	}); err != nil {
		t.Fatal(err)
	}
	intent := pipelineSealedConvergenceReplanIntent{
		Version: pipelineSealedConvergenceReplanIntentVersion,
		SourceFrozen: pipelineFrozenPlan{
			Chapter: chapter, PlanningGenerationID: "pg2_zero_side_effect_fixture", PlanCheckpointSeq: 4,
		},
		PlannerRepairAttempts: 2,
		PlannerRepairLimit:    2,
		PlannerContinuation: &pipelineSealedConvergencePlannerContinuation{
			Version:              pipelineSealedConvergencePlannerContinuationVersion,
			PlannerAttempt:       2,
			InitialPartialSHA256: partialSHA,
			SessionIdentity:      "convergence_planner_continuation-ch05",
			Dispatches:           1,
			ReservedAt:           time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano),
			DispatchedAt:         time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
		},
	}
	intent.IntentDigest = pipelineSealedConvergenceReplanIntentDigest(intent)
	appendSealedConvergenceSessionMessage(t, st.Dir(), intent.PlannerContinuation.SessionIdentity, agentcore.UserMsg("legacy host-prefetched continuation prompt"))
	token := domain.PlanningContextAccessTokenPrefix + strings.Repeat("a", 64)
	_ = saveSealedConvergenceTestReceiptWithToken(t, st, intent, "released-original-lock", 77777, token)
	return st, intent, partialSHA, token
}

func appendSealedConvergenceSessionMessage(
	t *testing.T,
	outputDir string,
	identity string,
	message agentcore.Message,
) {
	t.Helper()
	path := filepath.Join(outputDir, "meta", "sessions", "agents", identity+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func saveSealedConvergenceTestReceipt(
	t *testing.T,
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	lockOwner string,
	lockPID int,
) domain.PlanningContextAccessReceipt {
	t.Helper()
	token := domain.PlanningContextAccessTokenPrefix + strings.Repeat("b", 64)
	return saveSealedConvergenceTestReceiptWithToken(t, st, intent, lockOwner, lockPID, token)
}

func saveSealedConvergenceTestReceiptWithToken(
	t *testing.T,
	st *store.Store,
	intent pipelineSealedConvergenceReplanIntent,
	lockOwner string,
	lockPID int,
	token string,
) domain.PlanningContextAccessReceipt {
	t.Helper()
	tokenSHA, err := domain.PlanningContextAccessTokenSHA256(token)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	receipt := domain.PlanningContextAccessReceipt{
		Version:               domain.PlanningContextAccessReceiptVersion,
		GenerationID:          intent.SourceFrozen.PlanningGenerationID,
		Chapter:               intent.SourceFrozen.Chapter,
		Profile:               "planning",
		PlanningContextDigest: "sha256:" + strings.Repeat("c", 64),
		Phase:                 domain.PlanningContextAccessPlan,
		LockMode:              domain.PipelineExecutionProjectAll,
		LockOwner:             lockOwner,
		LockProcessID:         lockPID,
		LockAcquiredAt:        now.Add(-time.Minute),
		IssuedAt:              now.Add(-time.Second),
		ExpiresAt:             now.Add(10 * time.Minute),
		TokenSHA256:           tokenSHA,
	}
	receipt.ReceiptDigest, err = domain.ComputePlanningContextAccessReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.SavePlanningContextAccessReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}
