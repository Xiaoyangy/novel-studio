package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// TestManagedCommitRejectsFailedHardConsistencyReceipt guards the final
// delivery boundary. A same-hash provider pass cannot turn a consistency run
// containing a deterministic local AIGC hard violation into pass evidence.
func TestManagedCommitRejectsFailedHardConsistencyReceipt(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("managed-commit-aigc", 3); err != nil {
		t.Fatal(err)
	}
	prepareManagedDraftJudgePlan(t, st, 1)

	body := "第一章 测试章\n\n" + strings.Repeat(
		"首先，主角感到前所未有的恐惧，这意味着局势已经发生了变化。其次，他终于明白自己必须面对命运的安排。最后，所有人都意识到问题的严重性。\n",
		70,
	)
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}

	report, gate := inspectDraftAIGCGate(st, 1, body)
	if !gate.Enforced || gate.Passed {
		t.Fatalf("regression fixture must fail the enforced local AIGC gate: report=%+v gate=%+v", report, gate)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256:           reviewreport.BodySHA256(body),
		AdviceComplete:       true,
		AIProbabilityPercent: 2,
		PassExclusivePercent: 4,
	})
	if err := RequireDraftExternalApprovalWithStore(st, 1); err != nil {
		t.Fatalf("same-hash provider pass should satisfy the external gate: %v", err)
	}

	consistencyRaw, err := NewCheckConsistencyTool(st).Execute(
		context.Background(), mustJSON(t, map[string]any{"chapter": 1}),
	)
	if err != nil {
		t.Fatalf("check_consistency: %v", err)
	}
	var consistency map[string]any
	if err := json.Unmarshal(consistencyRaw, &consistency); err != nil {
		t.Fatal(err)
	}
	if _, ok := consistency["hard_gate_violations"]; !ok {
		t.Fatalf("fixture unexpectedly lost its local AIGC failure: %#v", consistency)
	}
	if err := requireCurrentDraftConsistency(st, 1, body); err == nil || !strings.Contains(err.Error(), "passed=true") {
		t.Fatalf("failed exact-body hard receipt should not be valid: %v", err)
	}

	args := mustJSON(t, map[string]any{
		"chapter":                 1,
		"summary":                 "模板化叙述没有通过本地交付门禁。",
		"characters":              []string{"主角", "配角"},
		"key_events":              []string{"本地门禁复验"},
		"character_stage_records": testCharacterStageRecords("主角", "配角"),
	})
	if _, err := NewCommitChapterTool(st).Execute(context.Background(), args); err == nil ||
		!strings.Contains(err.Error(), "passed=true") {
		t.Fatalf("managed commit did not reject the still-failing exact body: %v", err)
	}
	if final, _ := st.Drafts.LoadChapterText(1); final != "" {
		t.Fatalf("rejected managed commit wrote final chapter: %q", final)
	}
	if pending, err := st.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("rejected managed commit crossed the pending-commit boundary: pending=%+v err=%v", pending, err)
	}
	if progress, err := st.Progress.Load(); err != nil || st.Progress.IsChapterCompleted(1) {
		t.Fatalf("rejected managed commit changed completion state: progress=%+v err=%v", progress, err)
	}
}
