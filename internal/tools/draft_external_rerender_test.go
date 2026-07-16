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
		inspection.Requirement == nil || !RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("registered high score was diluted by model pass: inspection=%+v err=%v", inspection, err)
	}
}

func TestRegisteredExternalHighRequiresSameDetectorRetestAfterRerender(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest {
		t.Fatalf("new hash escaped named detector retest: inspection=%+v err=%v", inspection, err)
	}
	appendRegisteredExternalDetection(t, dir, 2, newBody, "zhuque", "whole-single-segment", 3)
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved {
		t.Fatalf("same-detector current-hash pass did not approve both gates: inspection=%+v err=%v", inspection, err)
	}
}

func TestLegacyLooseNamedThresholdCannotApproveOrCommitAtFivePercent(t *testing.T) {
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
	if inspection.Status == DraftExternalGateApproved || inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("legacy 10%% marker approved a fixed-gate 5%% named result: %+v", inspection)
	}
	if err := RequireDraftExternalApproval(dir, 1); err == nil {
		t.Fatal("legacy 10% marker allowed commit with a 5% named result")
	}

	appendRegisteredExternalDetection(t, dir, 1, newBody, "zhuque", "whole-single-segment", 2)
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateApproved || !inspection.CurrentHashNamedRetestsPassed {
		t.Fatalf("strict 2%% named result did not approve current hash: inspection=%+v err=%v", inspection, err)
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

func TestRegisteredRetestStaysDeferredUntilCurrentHashPassesDeepSeek(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || !inspection.RegisteredRetestDeferred ||
		inspection.RequiresRegisteredRetest || !RequiresRegisteredExternalRetest(inspection.Requirement) ||
		inspection.Requirement == nil || inspection.Requirement.Source != "local_mechanical_gate" ||
		inspection.Requirement.EvaluatedBodySHA256 != reviewreport.BodySHA256(intermediate) {
		t.Fatalf("locally blocked intermediate hash should defer, not erase, named retest: inspection=%+v err=%v", inspection, err)
	}

	// Replacing the locally blocked bytes first creates a current-hash DeepSeek
	// obligation. The named platform remains deferred until that stage passes.
	candidate := "第一章\n\n林澈把桌边的鱼刺拨开，起身去了夜市。"
	if err := st.Drafts.SaveDraft(1, candidate); err != nil {
		t.Fatal(err)
	}
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.RequiresRegisteredRetest || !inspection.RegisteredRetestDeferred {
		t.Fatalf("replacement hash did not wait for DeepSeek before named retest: inspection=%+v err=%v", inspection, err)
	}
	writeDraftExternalJudgeStatus(t, dir, 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(candidate), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 1)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("DeepSeek-passing hash did not advance to named-platform retest: inspection=%+v err=%v", inspection, err)
	}
}

func TestDeepSeekBlockingHashAuthorizesRerenderWithoutErasingNamedRetest(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || !inspection.RegisteredRetestDeferred ||
		inspection.RequiresRegisteredRetest || inspection.Requirement == nil ||
		inspection.Requirement.Source != "deepseek_ai_judge" || inspection.Requirement.Evaluator != "deepseek" ||
		inspection.Requirement.EvaluatedBodySHA256 != reviewreport.BodySHA256(candidate) ||
		!RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("DeepSeek blocker did not authorize one rerender while retaining named obligation: inspection=%+v err=%v", inspection, err)
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
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || !RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("registered high did not upgrade independent marker: inspection=%+v err=%v", inspection, err)
	}
}

func TestRegisteredExternalRetestRequiresEveryBlockingIdentity(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || len(registeredExternalRetestIdentities(inspection.Requirement)) != 2 {
		t.Fatalf("multiple platform blockers were not retained: inspection=%+v err=%v", inspection, err)
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
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest ||
		inspection.CurrentHashNamedRetestsPassed || draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("one platform pass incorrectly cleared every identity: inspection=%+v err=%v", inspection, err)
	}
	appendRegisteredExternalDetection(t, dir, 2, newBody, "zhuque", "whole", 2)
	inspection, err = InspectDraftExternalGate(dir, 2)
	if err != nil || inspection.Status != DraftExternalGateApproved ||
		!inspection.CurrentHashNamedRetestsPassed || !draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("all platform passes plus model pass should approve: inspection=%+v err=%v", inspection, err)
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
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest ||
		inspection.CurrentHashNamedRetestsPassed || draftCurrentHashNamedPassFrozen(inspection) {
		t.Fatalf("old-hash named passes froze a different current draft: inspection=%+v err=%v", inspection, err)
	}
}

func TestLocalBlockCannotEraseRegisteredRetestObligation(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRerenderAuthorized || !RequiresRegisteredExternalRetest(inspection.Requirement) {
		t.Fatalf("local blocker erased registered contract: inspection=%+v err=%v", inspection, err)
	}
	if err := os.WriteFile(draftPath, []byte(secondRewrite), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, dir, 3, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(secondRewrite), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 3)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest {
		t.Fatalf("second rewrite escaped platform retest: inspection=%+v err=%v", inspection, err)
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

func TestRegisteredFinalHighWithStaleDraftAllowsOneRewriteThenRequiresRetest(t *testing.T) {
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
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || inspection.RequiresRegisteredRetest || !inspection.RegisteredRetestDeferred {
		t.Fatalf("replacement draft did not wait for DeepSeek before named-platform retest: inspection=%+v err=%v", inspection, err)
	}
	writeDraftExternalJudgeStatus(t, dir, 5, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(newDraft), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	inspection, err = InspectDraftExternalGate(dir, 5)
	if err != nil || inspection.Status != DraftExternalGateRejudgePending || !inspection.RequiresRegisteredRetest || inspection.RegisteredRetestDeferred {
		t.Fatalf("DeepSeek-passing replacement did not advance to named-platform retest: inspection=%+v err=%v", inspection, err)
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
