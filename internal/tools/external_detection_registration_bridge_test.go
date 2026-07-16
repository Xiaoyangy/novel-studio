package tools

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRegisteredExternalSamplingHighTriggersRewriteWithoutCreatingRetestObligation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 3); err != nil {
		t.Fatal(err)
	}

	const detector = "zhuque"
	const mode = "novel-whole-text-single-segment"
	initial := "第一章\n\n旧正文被用户抽查为高分。"
	if err := st.Drafts.SaveDraft(1, initial); err != nil {
		t.Fatal(err)
	}
	high := appendRegisteredExternalDetection(t, st.Dir(), 1, initial, detector, mode, 86)
	if err := SetRegisteredExternalRerenderRequirement(st.Dir(), high); err != nil {
		t.Fatal(err)
	}
	before, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || before.Status != DraftExternalGateRerenderAuthorized {
		t.Fatalf("current sampled high did not trigger rewrite: inspection=%+v err=%v", before, err)
	}
	if before.Requirement == nil || RequiresRegisteredExternalRetest(before.Requirement) {
		t.Fatalf("sampling trigger became a durable retest obligation: %+v", before)
	}

	replacement := "第一章\n\n林澈重新摆放价牌，沈知遥把旧票据压在桌角。"
	if err := st.Drafts.SaveDraft(1, replacement); err != nil {
		t.Fatal(err)
	}
	writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
		BodySHA256: reviewreport.BodySHA256(replacement), AdviceComplete: true,
		AIProbabilityPercent: 2, PassExclusivePercent: 4,
	})
	after, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || after.Status != DraftExternalGateApproved || after.RequiresRegisteredRetest ||
		after.CurrentHashNamedRetestsPassed {
		t.Fatalf("replacement still waited for user sampling: inspection=%+v err=%v", after, err)
	}
	if err := RequireDraftExternalApprovalWithStore(st, 1); err != nil {
		t.Fatalf("automated gates did not release replacement: %v", err)
	}

	appendRegisteredExternalDetection(t, st.Dir(), 1, replacement, detector, mode, 9)
	rechecked, err := InspectDraftExternalGateWithStore(st, 1)
	if err != nil || rechecked.Status != DraftExternalGateRerenderAuthorized {
		t.Fatalf("a later high sample on the exact replacement did not trigger a new rewrite: inspection=%+v err=%v", rechecked, err)
	}
}
