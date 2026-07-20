package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func appendRegisteredExternalDetection(t *testing.T, dir string, chapter int, body, detector, mode string, percent float64) reviewreport.RegisteredExternalDetection {
	t.Helper()
	row := reviewreport.RegisteredExternalDetection{
		Chapter: chapter, Detector: detector, Mode: mode, Score: percent, ScoreScale: "percent",
		ScorePercent: &percent, Verdict: "ai_like", BodySHA256: reviewreport.BodySHA256(body),
		CheckedAt: "2026-07-15T20:00:00+08:00",
	}
	if percent < 4 {
		row.Verdict = "human_like"
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "meta")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(meta, "external_detection_log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(file, "%s\n", raw); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	row.NormalizedScorePercent = percent
	return row
}

func TestDraftExternalGateStateFollowsCurrentAndEvaluatedHashes(t *testing.T) {
	dir := t.TempDir()
	draftDir := filepath.Join(dir, "drafts")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldBody := "第一章\n\n旧版本。"
	newBody := "第一章\n\n新版本把人物体验写在选择里。"
	draftPath := filepath.Join(draftDir, "01.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(oldBody),
		AIProbabilityPercent: 12, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"把流程压成结果"},
	}); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized {
		t.Fatalf("authorized inspection = %+v, err=%v", inspection, err)
	}
	if err := os.WriteFile(draftPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending {
		t.Fatalf("pending inspection = %+v, err=%v", inspection, err)
	}
}

func TestDraftExternalGateRequiresCurrentPassingArtifact(t *testing.T) {
	dir := t.TempDir()
	body := "第二章\n\n当前草稿。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "02.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewDir := filepath.Join(dir, "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	}
	raw, _ := json.Marshal(artifact)
	if err := os.WriteFile(filepath.Join(reviewDir, "02_deepseek_ai_judge.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved {
		t.Fatalf("approved inspection = %+v, err=%v", inspection, err)
	}
	if err := RequireDraftExternalApproval(dir, 2); err != nil {
		t.Fatalf("current passing artifact rejected: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "drafts", "02.draft.md"), []byte(body+"\n又改了一句。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RequireDraftExternalApproval(dir, 2); err == nil {
		t.Fatal("stale passing artifact unexpectedly allowed commit")
	}
}

func TestRegisteredExternalHighOverridesPassingIndependentJudge(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n当前整篇正文。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "01.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 1, body, "zhuque", "whole-single-segment", 86)
	reviewDir := filepath.Join(dir, "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	judge, _ := json.Marshal(draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	if err := os.WriteFile(filepath.Join(reviewDir, "01_deepseek_ai_judge.json"), judge, 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized ||
		inspection.Requirement == nil || !isRegisteredExternalSamplingTrigger(inspection.Requirement) ||
		RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("registered high score was diluted by model pass: inspection=%+v err=%v", inspection, err)
	}
}

func TestRegisteredExternalHighDoesNotRequireRetestAfterReplacementPassesAutomatedGates(t *testing.T) {
	dir := t.TempDir()
	oldBody := "第二章\n\n旧正文。"
	newBody := "第二章\n\n新正文把人物选择写进现场后果。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "02.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, dir, 2, oldBody, "zhuque", "whole-single-segment", 83)
	if err := SetRegisteredExternalRerenderRequirement(dir, high); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	reviewDir := filepath.Join(dir, "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	judge, _ := json.Marshal(draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newBody), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	if err := os.WriteFile(filepath.Join(reviewDir, "02_deepseek_ai_judge.json"), judge, 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest ||
		inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("replacement hash still waited for a sampling retest: inspection=%+v err=%v", inspection, err)
	}
	appendRegisteredExternalDetection(t, dir, 2, newBody, "zhuque", "whole-single-segment", 3)
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("optional low sample unexpectedly changed production state: inspection=%+v err=%v", inspection, err)
	}
}

func TestOptionalSamplingMarkerResetsCrossBodyContractAndLaterLowClearsLegacyHistory(t *testing.T) {
	dir := t.TempDir()
	body := "第二章\n\n新正文把人物的选择和现场后果写在一起。"
	oldSHA := reviewreport.BodySHA256("第二章\n\n旧正文。")
	bodySHA := reviewreport.BodySHA256(body)
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "02.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: bodySHA, AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})

	legacyPrior := &DraftExternalRerenderRequirement{
		Chapter: 2, EvaluatedBodySHA256: oldSHA,
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredDetector: "other", RequiredMode: "whole",
		RequiredExternalRetests: []DraftExternalRetestIdentity{
			{Detector: "zhuque", Mode: "whole", TriggerBodySHA256: oldSHA},
			{Detector: "other", Mode: "whole", TriggerBodySHA256: oldSHA},
		},
		RevisionPlan:   []string{"旧策略要求所有平台对新哈希复测。"},
		AdviceComplete: true,
	}
	high := appendRegisteredExternalDetection(t, dir, 2, body, "zhuque", "whole", 83)
	requirement := registeredExternalRerenderRequirement(high, legacyPrior)
	identities := registeredExternalRetestIdentities(requirement)
	if requirement.ExternalRetestPolicy != DraftExternalRetestPolicySamplingOptional ||
		requirement.BlockUntilExternalRetest || RequiresRegisteredExternalRetest(requirement) ||
		len(identities) != 1 || identities[0].Detector != "zhuque" ||
		identities[0].TriggerBodySHA256 != bodySHA {
		t.Fatalf("optional sampling inherited an old-body identity contract: %+v", requirement)
	}
	if strings.Contains(strings.Join(requirement.RevisionPlan, "\n"), "所有平台") {
		t.Fatalf("optional sampling inherited an old-body revision plan: %+v", requirement.RevisionPlan)
	}

	// Emulate an already-persisted legacy marker that accumulated another
	// detector on an older body. A later low result for the current zhuque sample
	// must clear the current-body rewrite trigger without requiring "other".
	requirement.RequiredExternalRetests = append(requirement.RequiredExternalRetests, DraftExternalRetestIdentity{
		Detector: "other", Mode: "whole", TriggerBodySHA256: oldSHA,
	})
	if err := SetDraftExternalRerenderRequirement(dir, *requirement); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(draftExternalRerenderRequirementPath(dir, 2))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"external_retest_policy": "sampling_optional"`) {
		t.Fatalf("persisted marker omitted explicit optional policy: %s", raw)
	}
	appendRegisteredExternalDetection(t, dir, 2, body, "zhuque", "whole", 2)
	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved ||
		inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("same-SHA low did not clear marker because an old identity was missing: inspection=%+v err=%v", inspection, err)
	}
	bodyGate, err := InspectRegisteredExternalRetestsForBody(dir, 2, bodySHA)
	if err != nil || !bodyGate.Approved || bodyGate.Required ||
		len(bodyGate.Missing) != 0 || len(bodyGate.Blocking) != 0 {
		t.Fatalf("same-SHA low did not clear the delivery gate: inspection=%+v err=%v", bodyGate, err)
	}
}

func TestCurrentHashSamplingHighBlocksEvenWhenLegacyMarkerThresholdWasLoose(t *testing.T) {
	dir := t.TempDir()
	oldBody := "第一章\n\n旧正文触发命名平台返工。"
	newBody := "第一章\n\n新正文把人物选择放回现场。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "01.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, dir, 1, oldBody, "zhuque", "whole-single-segment", 86)
	if err := SetRegisteredExternalRerenderRequirement(dir, high); err != nil {
		t.Fatal(err)
	}
	requirement, err := loadDraftExternalRerenderRequirement(dir, 1)
	if err != nil || requirement == nil {
		t.Fatalf("load named requirement: requirement=%+v err=%v", requirement, err)
	}
	// Simulate a persisted marker written by an older build with a looser gate.
	requirement.PassExclusivePercent = 10
	if err := SetDraftExternalRerenderRequirement(dir, *requirement); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newBody), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 10,
	})
	appendRegisteredExternalDetection(t, dir, 1, newBody, "zhuque", "whole-single-segment", 5)

	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status != DraftExternalGateRerenderAuthorized || inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("legacy 10%% marker approved a current 5%% sampling result: %+v", inspection)
	}
	if err := RequireDraftExternalApproval(dir, 1); err == nil {
		t.Fatal("legacy 10% marker allowed commit with a 5% named result")
	}

	appendRegisteredExternalDetection(t, dir, 1, newBody, "zhuque", "whole-single-segment", 2)
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("latest 2%% sample did not supersede the same-hash high event: inspection=%+v err=%v", inspection, err)
	}
	if err := RequireDraftExternalApproval(dir, 1); err != nil {
		t.Fatalf("strict 2%% named result did not allow commit approval: %v", err)
	}
}

func TestLegacyLooseDeepSeekThresholdCannotApproveFivePercent(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "pipeline.json"), []byte(`{"managed":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "第二章\n\n当前草稿把人物反应写在选择之后。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "02.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 5, PassExclusivePercent: 10,
	})

	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Status == DraftExternalGateApproved {
		t.Fatalf("legacy DeepSeek threshold approved 5%%: %+v", inspection)
	}
	if err := RequireDraftExternalApproval(dir, 2); err == nil {
		t.Fatal("legacy DeepSeek threshold allowed commit at 5%")
	}
	if pending, err := pipelineManagedCurrentDraftNeedsDeepSeekJudge(st, 2, reviewreport.BodySHA256(body)); err != nil || !pending {
		t.Fatalf("managed current hash treated legacy-threshold 5%% as passing: pending=%t err=%v", pending, err)
	}

	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(body), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 10,
	})
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved {
		t.Fatalf("fixed-gate 2%% DeepSeek result did not approve: inspection=%+v err=%v", inspection, err)
	}
	if err := RequireDraftExternalApproval(dir, 2); err != nil {
		t.Fatalf("fixed-gate 2%% DeepSeek result did not allow commit approval: %v", err)
	}
	if pending, err := pipelineManagedCurrentDraftNeedsDeepSeekJudge(st, 2, reviewreport.BodySHA256(body)); err != nil || pending {
		t.Fatalf("managed current hash did not accept fixed-gate 2%%: pending=%t err=%v", pending, err)
	}
}

func TestSampleTriggeredRewriteWaitsForDeepSeekButNotAnotherPlatformResult(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	oldBody := "第一章\n\n旧正文。"
	if err := st.Drafts.SaveDraft(1, oldBody); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, dir, 1, oldBody, "zhuque", "novel-whole-text-single-segment", 86)
	if err := SetRegisteredExternalRerenderRequirement(dir, high); err != nil {
		t.Fatal(err)
	}

	intermediate := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	if err := st.Drafts.SaveDraft(1, intermediate); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || inspection.RegisteredRetestDeferred ||
		inspection.RequiresRegisteredRetest || RequiresRegisteredExternalRetest(inspection.Requirement) ||
		inspection.Requirement == nil || inspection.Requirement.Source != "local_mechanical_gate" ||
		inspection.Requirement.EvaluatedBodySHA256 != reviewreport.BodySHA256(intermediate) {
		t.Fatalf("locally blocked intermediate hash should retain automated gates without a platform wait: inspection=%+v err=%v", inspection, err)
	}

	// Replacing the locally blocked bytes first creates a current-hash DeepSeek
	// obligation. The named platform remains deferred until that stage passes.
	candidate := "第一章\n\n林澈把桌边的鱼刺拨开，起身去了夜市。"
	if err := st.Drafts.SaveDraft(1, candidate); err != nil {
		t.Fatal(err)
	}
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("replacement hash did not wait for DeepSeek: inspection=%+v err=%v", inspection, err)
	}
	writeDraftExternalJudgeStatus(t, dir, 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(candidate), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("DeepSeek-passing hash still waited for a platform sample: inspection=%+v err=%v", inspection, err)
	}
}

func TestDeepSeekBlockingReplacementAuthorizesAnotherRerenderWithoutPlatformWait(t *testing.T) {
	dir := t.TempDir()
	oldBody := "第二章\n\n旧平台阻断正文。"
	candidate := "第二章\n\n林澈抬手拦住送货车，先让摊主把账本翻到昨晚。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "02.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, dir, 2, oldBody, "zhuque", "novel-whole-text-single-segment", 83)
	if err := SetRegisteredExternalRerenderRequirement(dir, high); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, []byte(candidate), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(candidate), Blocking: true, AdviceComplete: true,
		AIProbabilityPercent: 31, PassExclusivePercent: 4,
		Summary: "人物选择链仍呈模板化", Evidence: []string{"scene_sequence_uniform"},
		RevisionPlan: []string{"保留事实结果，重组现场压力与人物选择。"},
	})

	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || inspection.RegisteredRetestDeferred ||
		inspection.RequiresRegisteredRetest || inspection.Requirement == nil ||
		inspection.Requirement.Source != "deepseek_ai_judge" || inspection.Requirement.Evaluator != "deepseek" ||
		inspection.Requirement.EvaluatedBodySHA256 != reviewreport.BodySHA256(candidate) ||
		RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("DeepSeek blocker did not authorize one rerender under sampling policy: inspection=%+v err=%v", inspection, err)
	}
}

func TestDraftExternalLocalGateDispositionKeepsSoftFailureEditableAfterDeepSeek(t *testing.T) {
	content := "第一章\n\n当前哈希已经通过 DeepSeek；本测试只验证非 whole-text 的本地硬门禁分类。"
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, Passed: true, ExternalCorroborated: true,
		RawLocalGatePercent: 10.30, EffectiveGatePercent: 3, PassExclusivePercent: 4,
	}
	structural, soft := draftExternalLocalGateDisposition(content, report, gate)
	if structural || !soft {
		t.Fatalf("corroborated pass must not erase raw non-whole local failure: structural=%v soft=%v", structural, soft)
	}

	report.WholeTextSegmentGate = 18
	structural, soft = draftExternalLocalGateDisposition(content, report, gate)
	if !structural || soft {
		t.Fatalf("corroborated pass must not erase raw whole-text failure: structural=%v soft=%v", structural, soft)
	}

	report.WholeTextSegmentGate = 0
	gate.RawLocalGatePercent = 3.99
	structural, soft = draftExternalLocalGateDisposition(content, report, gate)
	if structural || soft {
		t.Fatalf("raw local score below threshold should clear both local actions: structural=%v soft=%v", structural, soft)
	}
}

func TestDraftExternalLocalGateDispositionClearsExactBodyProbabilityOnlyDisagreement(t *testing.T) {
	external := 2.0
	content := "第一章\n\n林砚把登记册推回窗口，先让值班员核对监控时间。"
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, RawLocalGatePercent: 20.99, EffectiveGatePercent: 20.99,
		PassExclusivePercent: 4, ExternalAIProbabilityPercent: &external,
	}
	structural, soft := draftExternalLocalGateDisposition(content, report, gate)
	if structural || soft {
		t.Fatalf("exact-body DeepSeek pass should clear a probability-only local disagreement: structural=%v soft=%v", structural, soft)
	}

	// Whole-text/segment is another probability proxy for an exact-body provider
	// pass; the raw score remains available as diagnostic evidence.
	report.WholeTextSegmentGate = 18
	structural, soft = draftExternalLocalGateDisposition(content, report, gate)
	if structural || soft {
		t.Fatalf("exact-body provider pass did not resolve whole-text probability proxy: structural=%v soft=%v", structural, soft)
	}
}

func TestDraftExternalProbabilityPassCannotClearMechanicalStructuralWarning(t *testing.T) {
	external := 2.0
	content := `“先把桌子挪开。”林澈说。

“价牌放左边。”沈知遥接话。

“车只能跑一趟。”贺骁补充。

“那就先装三家。”老丁回答。

“剩下两家怎么办？”摊主追问。

“下午再送。”林澈解释。

“票据别忘了。”沈知遥提醒。

“我现在就开。”老丁点头。`
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, RawLocalGatePercent: 20.99, EffectiveGatePercent: 20.99,
		PassExclusivePercent: 4, ExternalAIProbabilityPercent: &external,
	}
	if draftAIGCExternalProbabilityOnlySatisfied(content, report, gate) {
		t.Fatal("exact-body external probability pass cleared dialogue_conveyor_overuse")
	}
	blockers := draftAIGCExternalCurrentBodyBlockers(content)
	if len(blockers) == 0 || blockers[0].Rule != "dialogue_conveyor_overuse" {
		t.Fatalf("current-body structural blocker was not made explicit: %+v", blockers)
	}
	structural, soft := draftExternalLocalGateDisposition(content, report, gate)
	if structural || !soft {
		t.Fatalf("mechanical structural warning must remain on the repair path: structural=%v soft=%v", structural, soft)
	}
}

func TestDraftLocalSoftGateClosesAfterOneEditedHashPassesDeepSeek(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "一次软修"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	content := "第一章\n\n林砚把登记册推回窗口，要求值班员先核对监控时间。"
	if err := st.Drafts.SaveDraft(1, content); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, Passed: true, ExternalCorroborated: true,
		RawLocalGatePercent: 10.3, EffectiveGatePercent: 2, PassExclusivePercent: 4,
	}
	if draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, 1, content, report, gate) {
		t.Fatal("an unedited draft incorrectly consumed the bounded local soft edit")
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err != nil {
		t.Fatal(err)
	}
	edited := content + "她没有收回手，只把监控编号念给对方听。"
	if err := st.Drafts.SaveDraft(1, edited); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(edited), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	if !draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, 1, edited, report, gate) {
		t.Fatal("one edited exact hash with a DeepSeek pass did not close the local soft loop")
	}
	report.WholeTextSegmentGate = 18
	if draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, 1, edited, report, gate) {
		t.Fatal("whole-text structural failure was waived by the bounded local soft rule")
	}
}

func TestDraftLocalSoftQuotaIgnoresFormalReviewWrittenAfterCurrentEditedBody(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "事后复审不重键"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	initial := "第一章\n\n林砚把登记册推回窗口，要求值班员先核对监控时间。"
	if err := st.Drafts.SaveDraft(1, initial); err != nil {
		t.Fatal(err)
	}
	initialCheckpoint, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantQuota, wantSeed, err := draftLocalSoftEditQuotaIdentity(st, 1)
	if err != nil || wantSeed != initialCheckpoint.Seq {
		t.Fatalf("initial quota=%s seed=%d checkpoint=%+v err=%v", wantQuota, wantSeed, initialCheckpoint, err)
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err != nil {
		t.Fatal(err)
	}
	edited := initial + "她没有收回手，只把监控编号念给对方听。"
	if err := st.Drafts.SaveDraft(1, edited); err != nil {
		t.Fatal(err)
	}
	editCheckpoint, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(edited), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	review := domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: reviewreport.BodySHA256(edited),
		Verdict: "rewrite", Summary: "复审发生在当前 edit 之后。",
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	reviewCheckpoint, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "review", "reviews/01.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reviewCheckpoint.Seq <= editCheckpoint.Seq {
		t.Fatalf("fixture review did not follow edit: edit=%+v review=%+v", editCheckpoint, reviewCheckpoint)
	}

	gotQuota, gotSeed, err := draftLocalSoftEditQuotaIdentity(st, 1)
	if err != nil || gotQuota != wantQuota || gotSeed != wantSeed {
		t.Fatalf("post-body review re-keyed consumed quota: got=%s#%d want=%s#%d err=%v", gotQuota, gotSeed, wantQuota, wantSeed, err)
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(st, 1)
	if err != nil || !consumed {
		t.Fatalf("post-body review lost consumed token: consumed=%v err=%v", consumed, err)
	}
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, Passed: true, ExternalCorroborated: true,
		RawLocalGatePercent: 10.3, EffectiveGatePercent: 2, PassExclusivePercent: 4,
	}
	if !draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, 1, edited, report, gate) {
		t.Fatal("post-body formal review reopened a locally repaired exact-body gate")
	}
}

func TestDraftLocalSoftQuotaUsesLatestReviewBeforeSuccessorBodyAsNewSeed(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "复审后新渲染"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	initial := "第一章\n\n林砚先把旧登记册留在窗口外。"
	if err := st.Drafts.SaveDraft(1, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	oldQuota, _, err := draftLocalSoftEditQuotaIdentity(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err != nil {
		t.Fatal(err)
	}
	review := domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: reviewreport.BodySHA256(initial),
		Verdict: "rewrite", Summary: "该复审要求产生 successor body。",
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	reviewCheckpoint, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "review", "reviews/01.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	successor := "第一章\n\n门外脚步停下后，林砚才把新的监控编号写进登记册。"
	if err := st.Drafts.SaveDraft(1, successor); err != nil {
		t.Fatal(err)
	}
	successorCheckpoint, err := st.Checkpoints.AppendArtifact(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
	)
	if err != nil {
		t.Fatal(err)
	}
	if successorCheckpoint.Seq <= reviewCheckpoint.Seq {
		t.Fatalf("fixture successor did not follow review: review=%+v successor=%+v", reviewCheckpoint, successorCheckpoint)
	}

	newQuota, newSeed, err := draftLocalSoftEditQuotaIdentity(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if newSeed != reviewCheckpoint.Seq || newQuota == oldQuota {
		t.Fatalf("successor body did not open review-seeded quota: old=%s new=%s#%d review=%d", oldQuota, newQuota, newSeed, reviewCheckpoint.Seq)
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(st, 1)
	if err != nil || consumed {
		t.Fatalf("old-cycle token consumed the review-seeded successor quota: consumed=%v err=%v", consumed, err)
	}
}

func TestDraftLocalSoftTokenNeverWaivesOriginalHashAfterEditWriteFailure(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "写入失败闭锁"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	original := "第一章\n\n这不是提醒，而是命令。值班员把登记册推了回来。"
	if err := st.Drafts.SaveDraft(1, original); err != nil {
		t.Fatal(err)
	}
	draftCheckpoint, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err != nil {
		t.Fatal(err)
	}
	// Model a crash/save failure after token consumption but before the atomic
	// draft replacement. The original bytes and their draft checkpoint remain.
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(original), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	gate := draftAIGCGateResult{
		Enforced: true, Passed: true, ExternalCorroborated: true,
		RawLocalGatePercent: 10.3, EffectiveGatePercent: 2, PassExclusivePercent: 4,
	}
	if draftAIGCLocalSoftSatisfiedAfterBoundedEdit(st, 1, original, report, gate) {
		t.Fatal("consumed token waived the unchanged pre-edit body after write failure")
	}
	current, err := CurrentChapterBodyCheckpoint(st, 1)
	if err != nil || current.Seq != draftCheckpoint.Seq || current.Step != "draft" {
		t.Fatalf("write-failure fixture unexpectedly acquired an edit checkpoint: current=%+v err=%v", current, err)
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err == nil {
		t.Fatal("write failure minted a second local-soft edit quota")
	}
	quotaDigest, _, err := draftLocalSoftEditQuotaIdentity(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	consumption, err := loadDraftLocalSoftEditConsumption(st, 1, quotaDigest)
	if err != nil || consumption == nil || consumption.Token == nil ||
		consumption.Token.PreEditBodySHA256 != reviewreport.BodySHA256(original) ||
		consumption.Checkpoint == nil || consumption.Checkpoint.Seq <= draftCheckpoint.Seq {
		t.Fatalf("token did not preserve pre-edit identity/order: consumption=%+v err=%v", consumption, err)
	}
}

func TestDraftLocalSoftEditQuotaPersistsAcrossNewDraftHashAndProcess(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "同一渲染种子"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	); err != nil {
		t.Fatal(err)
	}
	initial := "第一章\n\n这不是提醒，而是命令。\n这不是商量，而是最后期限。\n这不是偶然，而是有人提前安排。"
	if err := st.Drafts.SaveDraft(1, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	report := aigc.Report{Stats: aigc.Stats{Hanzi: draftAIGCMinHanzi}}
	if !draftPreJudgeLocalSoftEditEligible(st, 1, initial, report, false, true) {
		t.Fatal("fresh plan/initial-render seed did not receive its one local-soft edit")
	}
	if err := consumeDraftLocalSoftEditQuota(st, 1); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(initial, "这不是提醒，而是命令。", "门外脚步一停，林砚把命令压低了半句。", 1)
	if err := st.Drafts.SaveDraft(1, edited); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "edit", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}

	// A later whole render changes the body hash and changes the latest body
	// checkpoint back to draft, but it must not mint another quota. Reloading the
	// Store proves the token is journal-backed rather than process-local.
	newDraft := initial + "\n她把回执折好，等对方先移开视线。"
	if err := st.Drafts.SaveDraft(1, newDraft); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	reloaded := store.NewStore(dir)
	if draftPreJudgeLocalSoftEditEligible(reloaded, 1, newDraft, report, false, true) {
		t.Fatal("draft -> edit -> new draft reopened the plan/seed local-soft quota")
	}
	consumed, err := draftLocalSoftEditQuotaConsumed(reloaded, 1)
	if err != nil || !consumed {
		t.Fatalf("persistent local-soft token was lost after reload: consumed=%v err=%v", consumed, err)
	}

	// Content-integrity evidence is never eligible for the latency shortcut,
	// even before the one-shot token is consumed.
	contentRisk := report
	contentRisk.ContentIntegrityFloor = 25
	if draftPreJudgeLocalSoftEditEligible(reloaded, 1, newDraft, contentRisk, false, true) {
		t.Fatal("content-integrity risk entered the pre-judge local edit path")
	}
}

func TestRegisteredExternalHighUpgradesExistingIndependentMarker(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n已有 DeepSeek 阻断的正文。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "01.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		AIProbabilityPercent: 30, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"重建场景"},
	}); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 1, body, "zhuque", "whole", 86)
	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized ||
		!isRegisteredExternalSamplingTrigger(inspection.Requirement) || RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("registered high did not upgrade independent marker: inspection=%+v err=%v", inspection, err)
	}
}

func TestMultipleSamplingIdentitiesDoNotBecomeReplacementObligations(t *testing.T) {
	dir := t.TempDir()
	oldBody := "第二章\n\n旧正文。"
	newBody := "第二章\n\n新正文保留事实并重建人物选择。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "02.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 2, oldBody, "zhuque", "whole", 83)
	appendRegisteredExternalDetection(t, dir, 2, oldBody, "other", "whole", 70)
	inspection, err := InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized ||
		len(registeredExternalRetestIdentities(inspection.Requirement)) != 1 {
		t.Fatalf("optional sampling marker accumulated chapter-lifetime identities: inspection=%+v err=%v", inspection, err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, *inspection.Requirement); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 2, newBody, "other", "whole", 3)
	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newBody), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest ||
		inspection.CurrentHashNamedRetestsPassed || draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("sample identities became replacement obligations: inspection=%+v err=%v", inspection, err)
	}
	appendRegisteredExternalDetection(t, dir, 2, newBody, "zhuque", "whole", 2)
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved ||
		inspection.CurrentHashNamedRetestsPassed || draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("optional platform passes should remain observations: inspection=%+v err=%v", inspection, err)
	}

	// Both passing rows remain valid evidence for newBody only. If the prose is
	// replaced out of band, even a same-hash DeepSeek pass for the new bytes must
	// not inherit the named freeze from the old hash.
	thirdBody := "第二章\n\n第三份正文换了精确载荷，仍保留人物选择。"
	if err := os.WriteFile(draftPath, []byte(thirdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 2, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(thirdBody), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest ||
		inspection.CurrentHashNamedRetestsPassed || draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("old-hash sampling rows affected a different current draft: inspection=%+v err=%v", inspection, err)
	}
}

func TestLocalBlockSupersedesSamplingTriggerWithoutCreatingPlatformObligation(t *testing.T) {
	dir := t.TempDir()
	oldBody := "第三章\n\n旧正文。"
	firstRewrite := "第三章\n\n第一轮重写正文。"
	secondRewrite := "第三章\n\n第二轮重写正文。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "03.draft.md")
	if err := os.WriteFile(draftPath, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, dir, 3, oldBody, "zhuque", "whole", 86)
	if err := SetRegisteredExternalRerenderRequirement(dir, high); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftPath, []byte(firstRewrite), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 3, firstRewrite, "zhuque", "whole", 3)
	writeDraftExternalJudgeStatus(t, dir, 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(firstRewrite), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 3, EvaluatedBodySHA256: reviewreport.BodySHA256(firstRewrite), Source: "local_mechanical_gate",
		AIProbabilityPercent: 12, PassExclusivePercent: 4, AdviceComplete: true,
		RevisionPlan: []string{"按本地结构证据重建"},
	}); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 3)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("local blocker did not remain the active automated gate: inspection=%+v err=%v", inspection, err)
	}
	if err := os.WriteFile(draftPath, []byte(secondRewrite), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(secondRewrite), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 3)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest {
		t.Fatalf("second rewrite still waited for a platform sample: inspection=%+v err=%v", inspection, err)
	}
}

func TestRegisteredExternalHighUsesCommittedChapterWhenDraftMissing(t *testing.T) {
	dir := t.TempDir()
	body := "第四章\n\n只有正式章节，没有历史草稿。"
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chapters", "04.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 4, body, "zhuque", "whole", 80)
	inspection, err := InspectDraftExternalGate(dir, 4)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || inspection.CurrentBodySHA256 != reviewreport.BodySHA256(body) {
		t.Fatalf("chapters-only project cannot start its first replacement draft: inspection=%+v err=%v", inspection, err)
	}
}

func TestRegisteredFinalHighWithStaleDraftAllowsOneRewriteThenAutomatedApproval(t *testing.T) {
	dir := t.TempDir()
	finalBody := "第五章\n\n已经提交检测的正式章节。"
	staleDraft := "第五章\n\n正式章节之前遗留的旧草稿。"
	newDraft := "第五章\n\n新稿把人物选择和现场后果重新写开。"
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chapters", "05.md"), []byte(finalBody), 0o644); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "drafts", "05.draft.md")
	if err := os.WriteFile(draftPath, []byte(staleDraft), 0o644); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, dir, 5, finalBody, "zhuque", "whole-single-segment", 81)

	inspection, err := InspectDraftExternalGate(dir, 5)
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized {
		t.Fatalf("stale retained draft should allow the first final-body replacement: inspection=%+v err=%v", inspection, err)
	}
	if inspection.Requirement == nil || inspection.Requirement.InitialDraftBodySHA256 != reviewreport.BodySHA256(staleDraft) {
		t.Fatalf("initial stale draft hash was not captured: inspection=%+v", inspection)
	}
	if inspection.FinalBodySHA256 != reviewreport.BodySHA256(finalBody) || inspection.EvaluatedBodySHA256 != reviewreport.BodySHA256(finalBody) {
		t.Fatalf("registered subject was not the committed final body: inspection=%+v", inspection)
	}
	// DraftChapter persists the synthesized bridge before replacing staleDraft.
	// Do the same here so the post-write inspection exercises crash-safe state.
	if err := SetDraftExternalRerenderRequirement(dir, *inspection.Requirement); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(draftPath, []byte(newDraft), 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err = InspectDraftExternalGate(dir, 5)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("replacement draft did not wait for DeepSeek: inspection=%+v err=%v", inspection, err)
	}
	writeDraftExternalJudgeStatus(t, dir, 5, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newDraft), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 5)
	if err != nil || inspection.Status != DraftExternalGateApproved || inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("DeepSeek-passing replacement still waited for a platform sample: inspection=%+v err=%v", inspection, err)
	}
}

func TestDraftChapterRejectsUnchangedStaleDraftBridge(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}
	finalBody := "第一章\n\n送检后被判高分的正式稿。"
	staleDraft := "第一章\n\n正式稿之前残留的草稿。"
	if err := st.Drafts.SaveFinalChapter(1, finalBody); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, staleDraft); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(st.Dir(), DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(finalBody),
		InitialDraftBodySHA256: reviewreport.BodySHA256(staleDraft),
		Evaluator:              draftExternalEvaluatorRegistered,
		RequiredDetector:       "zhuque",
		RequiredMode:           "novel-whole-text-single-segment",
		AIProbabilityPercent:   86, PassExclusivePercent: 4,
		AdviceComplete: true, RevisionPlan: []string{"整章重排"},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"chapter": 1, "content": staleDraft, "mode": "write"})
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "当前草稿哈希相同") {
		t.Fatalf("unchanged stale draft did not consume a real replacement: %v", err)
	}
}

func TestSetRegisteredExternalRequirementRejectsBlankIdentity(t *testing.T) {
	err := SetRegisteredExternalRerenderRequirement(t.TempDir(), reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: " ", Mode: "whole", BodySHA256: strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("blank detector created a named external contract")
	}
}

func TestAutomatedHardPolicyRejectsMissingOrMalformedIdentity(t *testing.T) {
	for name, requirement := range map[string]DraftExternalRerenderRequirement{
		"missing identity": {
			Chapter: 1, EvaluatedBodySHA256: strings.Repeat("a", 64),
			ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
		},
		"incomplete explicit pair": {
			Chapter: 1, EvaluatedBodySHA256: strings.Repeat("a", 64),
			ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
			RequiredDetector:     "automated-detector",
		},
		"invalid trigger sha": {
			Chapter: 1, EvaluatedBodySHA256: strings.Repeat("a", 64),
			ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
			RequiredExternalRetests: []DraftExternalRetestIdentity{{
				Detector: "automated-detector", Mode: "whole", TriggerBodySHA256: "not-a-sha",
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := SetDraftExternalRerenderRequirement(t.TempDir(), requirement); err == nil {
				t.Fatalf("malformed automated_hard marker was accepted: %+v", requirement)
			}
		})
	}
}

func TestUserSamplingDoesNotJoinExistingAutomatedHardIdentitySet(t *testing.T) {
	prior := &DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: strings.Repeat("a", 64),
		ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
		RequiredExternalRetests: []DraftExternalRetestIdentity{{
			Detector: "automated-detector", Mode: "whole", TriggerBodySHA256: strings.Repeat("a", 64),
		}},
		RevisionPlan:   []string{"自动 detector 必须对替换稿复判。"},
		AdviceComplete: true,
	}
	sampled := reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "user-whole",
		BodySHA256: strings.Repeat("b", 64), NormalizedScorePercent: 86,
	}
	requirement := registeredExternalRerenderRequirement(sampled, prior)
	labels := strings.Join(RegisteredExternalRetestLabels(requirement), ",")
	if !RequiresRegisteredExternalRetest(requirement) || !strings.Contains(labels, "automated-detector/whole") {
		t.Fatalf("existing automated_hard contract was lost: %+v", requirement)
	}
	if strings.Contains(labels, "zhuque/user-whole") {
		t.Fatalf("user sampling identity was promoted into automated_hard: %+v", requirement)
	}
}

func TestLocalStructuralMarkerDoesNotCarryOptionalSamplingIdentity(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第一章 县城试点\n\n" + strings.Repeat("林澈把价牌放好，然后核对票据，然后走到下一家。", 100)
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	base := &DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredExternalRetests: []DraftExternalRetestIdentity{{
			Detector: "zhuque", Mode: "whole", TriggerBodySHA256: reviewreport.BodySHA256(body),
		}},
		ExternalRetestPolicy: DraftExternalRetestPolicySamplingOptional,
	}
	requirement, blocked := currentDraftLocalStructuralRerenderRequirement(st, 1, base)
	if !blocked || requirement == nil {
		t.Fatal("fixture did not produce a local structural marker")
	}
	if labels := RegisteredExternalRetestLabels(requirement); len(labels) != 0 || RequiresRegisteredExternalRetest(requirement) {
		t.Fatalf("local marker carried optional sampling identity: requirement=%+v labels=%v", requirement, labels)
	}
}

func TestLegacyBlockFlagLoadsAsOptionalSamplingPolicy(t *testing.T) {
	dir := t.TempDir()
	path := draftExternalRerenderRequirementPath(dir, 1)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf(`{
  "chapter": 1,
  "evaluated_body_sha256": %q,
  "source": "registered_external_detection",
  "evaluator": "registered_external_detector",
  "required_external_retests": [{"detector":"automated-detector","mode":"whole"}],
  "block_until_external_retest": true,
  "revision_plan": ["整章重渲染"],
  "advice_complete": true
}`, strings.Repeat("a", 64))
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadDraftExternalRerenderRequirement(dir, 1)
	if err != nil || got == nil ||
		got.ExternalRetestPolicy != DraftExternalRetestPolicySamplingOptional ||
		got.BlockUntilExternalRetest || RequiresRegisteredExternalRetest(got) {
		t.Fatalf("legacy block alias became an implicit production dependency: requirement=%+v err=%v", got, err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, *got); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"external_retest_policy": "sampling_optional"`) ||
		strings.Contains(string(raw), `"block_until_external_retest": true`) {
		t.Fatalf("rewritten legacy marker did not retire implicit hard policy: %s", raw)
	}
}

func TestOptionalSamplingCorruptLogDoesNotBlockDraftOrBodyGate(t *testing.T) {
	dir := t.TempDir()
	currentBody := "第一章\n\n替换稿已经通过自动门禁。"
	currentSHA := reviewreport.BodySHA256(currentBody)
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "01.draft.md"), []byte(currentBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: strings.Repeat("b", 64),
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredDetector: "zhuque", RequiredMode: "whole",
		RequiredExternalRetests: []DraftExternalRetestIdentity{{
			Detector: "zhuque", Mode: "whole", TriggerBodySHA256: strings.Repeat("b", 64),
		}},
		RevisionPlan:   []string{"整章重渲染"},
		AdviceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 1, draftExternalJudgeStatus{
		BodySHA256: currentSHA, AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte("{not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved ||
		inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("optional sampling journal error blocked draft: inspection=%+v err=%v", inspection, err)
	}
	bodyGate, err := InspectRegisteredExternalRetestsForBody(dir, 1, currentSHA)
	if err != nil || !bodyGate.Approved || bodyGate.Required ||
		len(bodyGate.Missing) != 0 || len(bodyGate.Blocking) != 0 {
		t.Fatalf("optional sampling journal error blocked body gate: inspection=%+v err=%v", bodyGate, err)
	}
}

func TestCurrentSamplingMarkerBlocksExactBodyWhenJournalIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		corruptLog bool
	}{
		{name: "missing journal"},
		{name: "corrupt journal", corruptLog: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body := "第一章\n\n用户已经抽查并明确报告当前正文偏高。"
			bodySHA := reviewreport.BodySHA256(body)
			if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
				Chapter: 1, EvaluatedBodySHA256: bodySHA,
				Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
				RequiredDetector: "zhuque", RequiredMode: "whole",
				RequiredExternalRetests: []DraftExternalRetestIdentity{{
					Detector: "zhuque", Mode: "whole", TriggerBodySHA256: bodySHA,
				}},
				AIProbabilityPercent: 86, PassExclusivePercent: 4,
				RevisionPlan: []string{"整章重渲染"}, AdviceComplete: true,
			}); err != nil {
				t.Fatal(err)
			}
			if tc.corruptLog {
				if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte("{not-json\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			bodyGate, err := InspectRegisteredExternalRetestsForBody(dir, 1, bodySHA)
			if err != nil || bodyGate.Approved || !bodyGate.Required || len(bodyGate.Blocking) == 0 {
				t.Fatalf("persisted current-body high marker was lost with an unavailable journal: inspection=%+v err=%v", bodyGate, err)
			}
			if len(bodyGate.Missing) != 0 {
				t.Fatalf("optional sampling became a mandatory follow-up retest: inspection=%+v", bodyGate)
			}
		})
	}
}

func TestAutomatedHardCorruptLogRemainsFailClosed(t *testing.T) {
	dir := t.TempDir()
	body := "第一章\n\n自动外部门禁正文。"
	bodySHA := reviewreport.BodySHA256(body)
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "01.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: bodySHA,
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredExternalRetests: []DraftExternalRetestIdentity{{
			Detector: "automated-detector", Mode: "whole", TriggerBodySHA256: bodySHA,
		}},
		ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
		RevisionPlan:         []string{"整章重渲染"},
		AdviceComplete:       true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "external_detection_log.jsonl"), []byte("{not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InspectDraftExternalGate(dir, 1); err == nil {
		t.Fatal("automated-hard draft gate ignored a corrupt external journal")
	}
	if _, err := InspectRegisteredExternalRetestsForBody(dir, 1, bodySHA); err == nil {
		t.Fatal("automated-hard body gate ignored a corrupt external journal")
	}
}

func TestExplicitAutomatedExternalRetestOptInRemainsDurableAcrossNewGateReason(t *testing.T) {
	dir := t.TempDir()
	initial := DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: strings.Repeat("a", 64),
		Source: "registered_external_detection", Evaluator: draftExternalEvaluatorRegistered,
		RequiredExternalRetests: []DraftExternalRetestIdentity{{
			Detector: "automated-detector", Mode: "whole", TriggerBodySHA256: strings.Repeat("a", 64),
		}},
		ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
		AdviceComplete:       true,
		RevisionPlan:         []string{"整章重渲染"},
	}
	if err := SetDraftExternalRerenderRequirement(dir, initial); err != nil {
		t.Fatal(err)
	}
	local := DraftExternalRerenderRequirement{
		Chapter: 1, EvaluatedBodySHA256: strings.Repeat("b", 64),
		Source: "local_mechanical_gate", AdviceComplete: true,
		RevisionPlan: []string{"按本地结构证据重渲染"},
	}
	if err := SetDraftExternalRerenderRequirement(dir, local); err != nil {
		t.Fatal(err)
	}
	got, err := loadDraftExternalRerenderRequirement(dir, 1)
	if err != nil || !RequiresRegisteredExternalRetest(got) ||
		got.ExternalRetestPolicy != DraftExternalRetestPolicyAutomatedHard ||
		!strings.Contains(strings.Join(RegisteredExternalRetestLabels(got), ","), "automated-detector/whole") {
		t.Fatalf("explicit automated opt-in was not durable: requirement=%+v err=%v", got, err)
	}
}

func TestExplicitRerenderReplacementApprovedRequiresNewerPassingDraft(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "第二章\n\n旧草稿。"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(s.Dir(), "drafts", "02.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "rerender-request", "drafts/02.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	if ExplicitRerenderReplacementApproved(s, 2) {
		t.Fatal("old draft must not satisfy a newer rerender request")
	}

	newBody := "第二章\n\n新草稿把人物选择写清楚了。"
	if err := s.Drafts.SaveDraft(2, newBody); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoints.AppendArtifact(domain.ChapterScope(2), "draft", "drafts/02.draft.md"); err != nil {
		t.Fatal(err)
	}
	if ExplicitRerenderReplacementApproved(s, 2) {
		t.Fatal("replacement must not finalize before its exact hash is judged")
	}
	reviewDir := filepath.Join(s.Dir(), "reviews", "drafts")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newBody), AdviceComplete: true,
		AIProbabilityPercent: 3, PassExclusivePercent: 4,
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reviewDir, "02_deepseek_ai_judge.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if !ExplicitRerenderReplacementApproved(s, 2) {
		t.Fatal("newer rerender draft with same-hash passing judgment should finalize")
	}
}

func TestReviewRequiresFreshDraftStopsIdenticalCommitLoop(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	body := "第二章\n\n林澈借车把五块牌送到桥头。"
	if err := s.Drafts.SaveDraft(2, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(2, body); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2, BodySHA256: reviewreport.BodySHA256(body), Verdict: "rewrite", ContractStatus: "met",
	}); err != nil {
		t.Fatal(err)
	}
	if !ReviewRequiresFreshDraft(s, 2) {
		t.Fatal("same-hash blocking review must require a fresh draft")
	}
	if !BlockingReviewRejectsBody(s, 2, body) {
		t.Fatal("renderer must reject the exact body named by blocking formal review")
	}
	if err := s.Drafts.SaveDraft(2, body+"\n新稿已经改动。"); err != nil {
		t.Fatal(err)
	}
	if ReviewRequiresFreshDraft(s, 2) {
		t.Fatal("new draft hash should leave the identical-commit-loop state")
	}
}

func TestDraftExternalGateBlocksIncompleteAdvice(t *testing.T) {
	dir := t.TempDir()
	body := "第三章\n\n待修改草稿。"
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drafts", "03.draft.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDraftExternalRerenderRequirement(dir, DraftExternalRerenderRequirement{
		Chapter: 3, EvaluatedBodySHA256: reviewreport.BodySHA256(body),
		AIProbabilityPercent: 8, PassExclusivePercent: 4,
	}); err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectDraftExternalGate(dir, 3)
	if err != nil || inspection.Status != DraftExternalGateAdviceIncomplete {
		t.Fatalf("incomplete inspection = %+v, err=%v", inspection, err)
	}
}
