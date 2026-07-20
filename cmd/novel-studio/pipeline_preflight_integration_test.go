package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineSealAdmitsLegacyV11WithoutRewritingProjectedBytes(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjectionWithMutator(
		t,
		func(chapter int, artifacts *agents.ProjectedChapterArtifacts) {
			if chapter != 1 {
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(artifacts.RenderContext, &payload); err != nil {
				t.Fatal(err)
			}
			packet := payload["working_memory"].(map[string]any)["render_packet"].(map[string]any)
			delete(packet, "anti_ai_render_contract")
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			artifacts.RenderContext = raw
		},
	)
	projected := st.ProjectedV2()
	before, err := projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 {
		t.Fatalf("legacy fixture bundle count=%d, want 3", len(before))
	}
	legacyBytes := append(json.RawMessage(nil), before[0].RenderContext...)
	legacySHA := before[0].RenderContextSHA256
	legacyBundleDigest := before[0].BundleDigest

	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatalf("seal rejected exact legacy v11 compatibility path: %v", err)
	}
	after, err := projected.LoadProjectedChapterBundles(identity.Generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || !bytes.Equal(after[0].RenderContext, legacyBytes) ||
		after[0].RenderContextSHA256 != legacySHA || after[0].BundleDigest != legacyBundleDigest {
		t.Fatalf(
			"seal rewrote immutable legacy context: before_sha=%s after_sha=%s before_bundle=%s after_bundle=%s",
			legacySHA,
			after[0].RenderContextSHA256,
			legacyBundleDigest,
			after[0].BundleDigest,
		)
	}
	var report pipelinePreflightReport
	if err := readPipelinePlanningJSON(
		pipelinePreflightReportPath(st.Dir(), pipelinePreflightReport{Stage: pipelinePreflightStageSeal}),
		&report,
	); err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !pipelinePreflightTestCheckPassed(report, "aigc.compatibility_injection") {
		t.Fatalf("seal did not persist compatibility admission evidence: %+v", report)
	}
}

func TestPipelineSealPreflightAggregatesBeforeSealing(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjectionWithMutator(
		t,
		func(chapter int, artifacts *agents.ProjectedChapterArtifacts) {
			pipelinePreflightTestMutateAIGCContract(t, chapter, artifacts)
		},
	)
	err := pipelineSeal(opts, pipelineFlags{})
	var typed *PipelinePreflightError
	if !errors.As(err, &typed) {
		t.Fatalf("seal did not return typed aggregate preflight: %T %v", err, err)
	}
	if !pipelinePreflightTestHasBlocker(typed.Report, 1, "aigc.render_packet_version") ||
		!pipelinePreflightTestHasBlocker(typed.Report, 2, "aigc.usage_policy_before_draft") {
		t.Fatalf("seal lost cross-chapter blockers: %+v", typed.Report.Blockers)
	}
	projected := st.ProjectedV2()
	if sealed, loadErr := projected.LoadSealedGeneration(identity.Generation.GenerationID); loadErr != nil || sealed != nil {
		t.Fatalf("blocked seal published generation: sealed=%+v err=%v", sealed, loadErr)
	}
	if active, loadErr := projected.LoadActiveGeneration(); loadErr != nil || active != nil {
		t.Fatalf("blocked seal activated generation: active=%+v err=%v", active, loadErr)
	}
	var persisted pipelinePreflightReport
	if readErr := readPipelinePlanningJSON(
		pipelinePreflightReportPath(st.Dir(), typed.Report),
		&persisted,
	); readErr != nil {
		t.Fatalf("seal did not persist non-canon preflight report: %v", readErr)
	}
	if persisted.Ready || len(persisted.Blockers) != len(typed.Report.Blockers) {
		t.Fatalf("persisted seal report lost blockers: %+v", persisted)
	}
}

func TestPipelinePromotePreflightRunsBeforeRetirementAndLiveMutation(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjectionWithMutator(
		t,
		func(chapter int, artifacts *agents.ProjectedChapterArtifacts) {
			if chapter == 1 {
				pipelinePreflightTestMutateAIGCContract(t, chapter, artifacts)
			}
		},
	)
	projected := st.ProjectedV2()
	building, err := projected.LoadBuildingGeneration(identity.Generation.GenerationID)
	if err != nil || building == nil {
		t.Fatalf("load building fixture: generation=%+v err=%v", building, err)
	}
	if _, err := savePipelineArcPlanningManifest(st, identity, *building); err != nil {
		t.Fatal(err)
	}
	source, err := projected.LoadPlanningSourceSnapshot(identity.Generation.GenerationID)
	if err != nil || source == nil {
		t.Fatalf("load source fixture: source=%+v err=%v", source, err)
	}
	if _, err := projected.SealGenerationExpected(
		building.GenerationID,
		building.GenerationDigest,
		source.SnapshotDigest,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := projected.ActivateSealedGeneration(building.GenerationID, nil); err != nil {
		t.Fatal(err)
	}
	legacyDraft := filepath.Join(st.Dir(), "drafts", "01.draft.md")
	projectAllCmdTestWriteFile(t, legacyDraft, "必须在 preflight 失败后原样保留的旧草稿\n")

	err = pipelinePromote(opts, pipelineFlags{Start: 1, End: 1})
	var typed *PipelinePreflightError
	if !errors.As(err, &typed) ||
		!pipelinePreflightTestHasBlocker(typed.Report, 1, "aigc.render_packet_version") {
		t.Fatalf("promote did not stop on typed preflight: report=%+v err=%v", typed, err)
	}
	if _, statErr := os.Stat(legacyDraft); statErr != nil {
		t.Fatalf("promote retired prose before preflight: %v", statErr)
	}
	cursor, err := projected.LoadRealizationCursor()
	if err != nil || cursor == nil || cursor.ActivePromotedChapter != 0 || cursor.ActivePromotionReceiptDigest != "" {
		t.Fatalf("promote mutated realization cursor before preflight: cursor=%+v err=%v", cursor, err)
	}
	if _, statErr := os.Stat(filepath.Join(st.Dir(), pipelineFrozenPlanPath)); !os.IsNotExist(statErr) {
		t.Fatalf("promote wrote live frozen plan before preflight: %v", statErr)
	}
}

func TestPipelineRenderPreflightUsesFrozenBundleUnderRenderLease(t *testing.T) {
	opts, st, _ := projectAllCmdTestInstallThreeChapterCLIProjection(t)
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	st = store.NewStore(st.Dir())
	owner := pipelineExecutionOwner("render-preflight-test", frozen.Chapter)
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: frozen.Chapter,
		PlanDigest:    frozen.PlanDigest,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Runtime.ReleasePipelineExecution(owner) }()
	binding, err := requirePipelineSealedRenderPreflight(st, frozen, false)
	if err != nil {
		t.Fatalf("locked frozen render preflight failed: %v", err)
	}
	if binding.Bundle.BundleDigest != frozen.ProjectedBundleDigest {
		t.Fatalf("render preflight did not retain exact sealed bundle: %+v", binding.Bundle)
	}
	var report pipelinePreflightReport
	if err := readPipelinePlanningJSON(
		pipelinePreflightReportPath(st.Dir(), pipelinePreflightReport{
			Stage:   pipelinePreflightStageRender,
			Chapter: frozen.Chapter,
		}),
		&report,
	); err != nil {
		t.Fatal(err)
	}
	if !report.Ready || !pipelinePreflightTestCheckPassed(report, "context.frozen_digest") {
		t.Fatalf("render preflight report did not bind frozen context: %+v", report)
	}
}

func pipelinePreflightTestMutateAIGCContract(
	t *testing.T,
	chapter int,
	artifacts *agents.ProjectedChapterArtifacts,
) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(artifacts.RenderContext, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	packet := working["render_packet"].(map[string]any)
	switch chapter {
	case 1:
		packet["version"] = 10
	case 2:
		packet["anti_ai_render_contract"] = map[string]any{
			"usage_policy": "正文生成后再补做检查",
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.RenderContext = raw
}

func pipelinePreflightTestHasBlocker(
	report pipelinePreflightReport,
	chapter int,
	code string,
) bool {
	return slices.ContainsFunc(report.Blockers, func(blocker pipelinePreflightBlocker) bool {
		return blocker.Chapter == chapter && blocker.Code == code
	})
}
