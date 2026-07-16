package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestRegisteredExternalHighInvalidatesExistingReviewAndDelivery(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	hash := reviewreport.BodySHA256("第一章正文")
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 0.86, ScoreScale: "probability", Verdict: "ai_like", BodySHA256: hash,
		CheckedAt: "2026-07-15T20:00:00+08:00",
	})

	current := inspectCurrentChapterReview(dir, 1)
	if got := strings.Join(current.Issues, "\n"); !strings.Contains(got, "current registered external detection") {
		t.Fatalf("same-hash external high result did not invalidate old review: %+v", current.Issues)
	}
	if _, err := verifyPipelineStage("review", dir, pipelineFlags{}, &domain.PipelineState{}); err == nil ||
		!strings.Contains(err.Error(), "registered external detection") {
		t.Fatalf("review verification accepted a pre-registration review: %v", err)
	}
	if err := settlePipelineDelivery(dir, pipelineFlags{Start: 1, End: 1}); err == nil ||
		(!strings.Contains(err.Error(), "registered external detection") && !strings.Contains(err.Error(), "external sampling result requires rewrite")) {
		t.Fatalf("delivery accepted a pre-registration review: %v", err)
	}
}

func TestRegisteredExternalFreshnessKeepsDetectorIdentitiesIndependent(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	hash := reviewreport.BodySHA256("第一章正文")

	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "whole", Score: 0.83, ScoreScale: "probability",
		Verdict: "ai_like", BodySHA256: hash, CheckedAt: "2026-07-15T20:00:00+08:00",
	})
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "other", Mode: "whole", Score: 0.02, ScoreScale: "probability",
		Verdict: "human_like", BodySHA256: hash, CheckedAt: "2026-07-15T20:01:00+08:00",
	})
	if got := strings.Join(inspectCurrentChapterReview(dir, 1).Issues, "\n"); !strings.Contains(got, "zhuque/whole") {
		t.Fatalf("later low result from another identity hid zhuque high result: %s", got)
	}

	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "whole", Score: 0.03, ScoreScale: "probability",
		Verdict: "human_like", BodySHA256: hash, CheckedAt: "2026-07-15T20:02:00+08:00",
	})
	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("same-identity passing retest should supersede its high result: %+v", current.Issues)
	}
}

func TestRegisteredExternalHighAllowsFreshBlockingReview(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("外部检测复审", 3); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	hash := reviewreport.BodySHA256("第一章正文")
	detection := reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "whole", Score: 0.86, ScoreScale: "probability",
		Verdict: "ai_like", BodySHA256: hash, CheckedAt: "2026-07-15T20:00:00+08:00",
		NormalizedScorePercent: 86,
	}
	appendRegisteredExternalFreshnessRow(t, dir, detection)
	mustWriteCurrentReviewArtifactsWithVerdict(t, dir, 1, "rewrite")

	var mechanical reviewreport.MechanicalGatePayload
	readJSONFileForFreshness(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), &mechanical)
	mechanical.RuleViolations = append(mechanical.RuleViolations, rules.Violation{
		Rule: "external_aigc_ratio", Target: "zhuque/whole", Limit: "<4%", Actual: 86.0,
		Deviation: 0.86, Severity: rules.SeverityError,
	})
	mustWriteJSONFile(t, filepath.Join(dir, "reviews", "01_ai_gate.json"), mechanical)
	var voice domain.AIVoiceAnalysis
	readJSONFileForFreshness(t, filepath.Join(dir, "reviews", "01_ai_voice_redflags.json"), &voice)
	var editor domain.ReviewEntry
	readJSONFileForFreshness(t, filepath.Join(dir, "reviews", "01.json"), &editor)
	if err := reviewreport.WriteUnifiedMarkdown(dir, reviewreport.UnifiedMarkdownInput{
		Chapter: 1, Mechanical: &mechanical, AIVoice: &voice, Editor: &editor,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendLatest(
		domain.ChapterScope(1), "registered-external-detection", "meta/external_detection_log.jsonl",
		reviewreport.RegisteredExternalDetectionDigest(detection),
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune("第一章正文")), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "registered external high"); err != nil {
		t.Fatal(err)
	}
	if _, err := rebuildReviewSummary(dir); err != nil {
		t.Fatal(err)
	}

	current := inspectCurrentChapterReview(dir, 1)
	if len(current.Issues) != 0 || current.Disposition != "是" {
		t.Fatalf("fresh blocking review should remain valid for rewrite routing: %+v", current)
	}
	if _, err := verifyPipelineStage("review", dir, pipelineFlags{}, &domain.PipelineState{}); err != nil {
		t.Fatalf("review stage should accept a fresh blocking result queued for rewrite: %v", err)
	}
}

func TestOptionalSamplingLogReadFailureDoesNotInvalidateReview(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	// Opening a directory succeeds on Unix, but scanning it fails. This covers
	// the read-error path instead of relying on permissions under a root runner.
	if err := os.Mkdir(filepath.Join(dir, "meta", "external_detection_log.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("optional sampling journal read failure blocked automated review evidence: %+v", current.Issues)
	}
}

func TestMissingFollowupSampleDoesNotBlockDelivery(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "chapters", "01.md"), "已经通过审核的定稿正文")
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	if err := st.Drafts.SaveDraft(1, "外部平台判定为高风险的旧草稿"); err != nil {
		t.Fatal(err)
	}
	oldHash := reviewreport.BodySHA256("外部平台判定为高风险的旧草稿")
	if err := tools.SetRegisteredExternalRerenderRequirement(dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: oldHash, NormalizedScorePercent: 86,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "整章重渲染后等待同平台复测的新草稿"); err != nil {
		t.Fatal(err)
	}

	// The current final review remains structurally fresh, so rewrite/review
	// recovery is not poisoned by a draft-only pending retest.
	if current := inspectCurrentChapterReview(dir, 1); len(current.Issues) != 0 {
		t.Fatalf("draft retest pending should not invalidate final review artifacts: %+v", current.Issues)
	}
	if issues := currentRegisteredExternalDeliveryIssues(dir, 1); len(issues) != 0 {
		t.Fatalf("missing optional sample blocked delivery: %+v", issues)
	}
}

func TestOldBodySamplingRowsDoNotCreateFinalBodyDeliveryRequirement(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	const oldBody = "旧版正文同时也是残留草稿"
	if err := st.Drafts.SaveFinalChapter(1, oldBody); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, oldBody); err != nil {
		t.Fatal(err)
	}
	oldHash := reviewreport.BodySHA256(oldBody)
	if err := tools.SetRegisteredExternalRerenderRequirement(dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		BodySHA256: oldHash, NormalizedScorePercent: 86,
	}); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 0.03, ScoreScale: "probability", Verdict: "human_like", BodySHA256: oldHash,
		CheckedAt: "2026-07-15T20:01:00+08:00",
	})

	// 模拟绕过 draft 路径直接改正式终稿，并把本地/DeepSeek/review 全部刷新。
	// 旧草稿仍有平台低分；它既不能替新终稿背书，也不能制造新终稿的
	// 人工复测义务。新终稿仍由本地、DeepSeek、review 与一致性门禁负责。
	const finalBody = "直接修改后的正式终稿"
	if err := st.Drafts.SaveFinalChapter(1, finalBody); err != nil {
		t.Fatal(err)
	}
	mustWriteCurrentReviewArtifacts(t, dir, 1)
	if issues := currentRegisteredExternalDeliveryIssues(dir, 1); len(issues) != 0 {
		t.Fatalf("old-body sampling row created a new-body delivery requirement: %+v", issues)
	}

	finalHash := reviewreport.BodySHA256(finalBody)
	appendRegisteredExternalFreshnessRow(t, dir, reviewreport.RegisteredExternalDetection{
		Chapter: 1, Detector: "zhuque", Mode: "novel-whole-text-single-segment",
		Score: 0.03, ScoreScale: "probability", Verdict: "human_like", BodySHA256: finalHash,
		CheckedAt: "2026-07-15T20:02:00+08:00",
	})
	if issues := currentRegisteredExternalDeliveryIssues(dir, 1); len(issues) != 0 {
		t.Fatalf("optional low sample on the exact final body changed delivery state: %+v", issues)
	}
}

func appendRegisteredExternalFreshnessRow(t *testing.T, dir string, row reviewreport.RegisteredExternalDetection) {
	t.Helper()
	path := filepath.Join(dir, "meta", "external_detection_log.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readJSONFileForFreshness(t *testing.T, path string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatal(err)
	}
}
