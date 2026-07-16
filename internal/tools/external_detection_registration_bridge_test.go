package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRegisteredExternalDraftBridgeFreezesApprovedNamedHashUntilNewRerenderRequest(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}

	const detector = "zhuque"
	const mode = "novel-whole-text-single-segment"
	initial := "林澈把价牌放稳，沈知遥站在一旁看着。"
	if err := st.Drafts.SaveDraft(1, initial); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, st.Dir(), 1, initial, detector, mode, 86)
	if err := SetRegisteredExternalRerenderRequirement(st.Dir(), high); err != nil {
		t.Fatal(err)
	}
	appendRegisteredExternalDetection(t, st.Dir(), 1, initial, detector, mode, 2)
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(initial), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	before, err := InspectDraftExternalGate(st.Dir(), 1)
	if err != nil || before.Status != DraftExternalGateApproved || !before.CurrentHashNamedRetestsPassed {
		t.Fatalf("initial named hash must be approved before polish: inspection=%+v err=%v", before, err)
	}
	if err := RequireDraftExternalApprovalWithStore(st, 1); err != nil {
		t.Fatalf("named-pass freeze must still allow the commit approval path: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "放稳", "new_string": "扶稳",
	})
	if _, err := NewEditChapterTool(st).Execute(context.Background(), args); err == nil ||
		!errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "正文已冻结") {
		t.Fatalf("edit changed a named-approved exact payload: %v", err)
	}
	if got, _ := st.Drafts.LoadDraft(1); got != initial {
		t.Fatalf("rejected edit changed named-approved bytes: %q", got)
	}

	replacement := "第一章\n\n林澈重新摆放价牌，沈知遥把旧票据压在桌角。"
	draftArgs, _ := json.Marshal(map[string]any{
		"chapter": 1, "content": replacement, "mode": "write",
	})
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), draftArgs); err == nil ||
		!errors.Is(err, errs.ErrToolPrecondition) || !strings.Contains(err.Error(), "正文已冻结") {
		t.Fatalf("ordinary draft_chapter changed a named-approved exact payload: %v", err)
	}
	if got, _ := st.Drafts.LoadDraft(1); got != initial {
		t.Fatalf("rejected draft write changed named-approved bytes: %q", got)
	}

	// A later explicit full-rerender authorization is the only ordinary way to
	// replace a frozen pass without first receiving a new blocking judgment.
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "draft", "drafts/01.draft.md"); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(st.Dir(), "drafts", "01.rerender_request.json")
	if err := os.WriteFile(requestPath, []byte(`{"chapter":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "rerender-request", "drafts/01.rerender_request.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDraftChapterTool(st).Execute(context.Background(), draftArgs); err != nil {
		t.Fatalf("new explicit rerender request did not unlock one full replacement: %v", err)
	}
	if got, _ := st.Drafts.LoadDraft(1); got != replacement {
		t.Fatalf("authorized full replacement not written: %q", got)
	}
}
