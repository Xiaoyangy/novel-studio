package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

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
