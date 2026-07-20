package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestFinalizeSealedDraftMechanicallyCommitsExactApprovedBody(t *testing.T) {
	st, bundle, body := sealedMechanicalFinalizeFixture(t, "", true)

	result, err := FinalizeSealedDraftMechanically(context.Background(), st, 1)
	if err != nil {
		t.Fatalf("mechanical finalize failed: %v", err)
	}
	if result.Disposition != SealedMechanicalFinalizeCommitted ||
		result.BodySHA256 != reviewreport.BodySHA256(body) {
		t.Fatalf("unexpected mechanical finalize result: %+v", result)
	}
	finalBody, err := st.Drafts.LoadChapterText(1)
	if err != nil || finalBody != body {
		t.Fatalf("final body does not preserve exact approved bytes: body=%q err=%v", finalBody, err)
	}
	if !st.Progress.IsChapterCompleted(1) {
		t.Fatal("mechanical finalize did not mark chapter complete")
	}
	receipt, err := loadDraftHardConsistencyReceipt(st, 1)
	if err != nil || !receipt.Passed || receipt.BodySHA256 != result.BodySHA256 {
		t.Fatalf("exact-body consistency receipt missing: receipt=%+v err=%v", receipt, err)
	}
	delta, err := st.LoadChapterWorldDelta(1)
	if err != nil || delta == nil ||
		!sealedCommitControlTestContains(delta.Sources, "server-sealed-control:"+bundle.BundleDigest) {
		t.Fatalf("mechanical commit lost sealed control-plane provenance: delta=%+v err=%v", delta, err)
	}
}

func TestFinalizeSealedDraftMechanicallyFallsBackWithoutExactDeepSeekApproval(t *testing.T) {
	st, _, _ := sealedMechanicalFinalizeFixture(t, "", false)

	result, err := FinalizeSealedDraftMechanically(context.Background(), st, 1)
	if err != nil {
		t.Fatalf("missing DeepSeek artifact should fall back, not fail: %v", err)
	}
	if result.Disposition != SealedMechanicalFinalizeNotApplicable {
		t.Fatalf("missing exact-body approval was mechanically consumed: %+v", result)
	}
	if body, err := st.Drafts.LoadChapterText(1); err != nil || body != "" {
		t.Fatalf("not-applicable path wrote a final chapter: body=%q err=%v", body, err)
	}
}

func TestFinalizeSealedDraftMechanicallyReturnsNeedsAgentOnHardConsistencyFailure(t *testing.T) {
	body := "# 第一章\n\n林澈把欠费单推回桌面，问：\"先确认欠费单？\"随后才接过钥匙。"
	st, _, _ := sealedMechanicalFinalizeFixture(t, body, true)

	result, err := FinalizeSealedDraftMechanically(context.Background(), st, 1)
	if err != nil {
		t.Fatalf("hard consistency failure should route to Agent: %v", err)
	}
	if result.Disposition != SealedMechanicalFinalizeNeedsAgent ||
		!strings.Contains(result.Reason, "violations=") {
		t.Fatalf("hard consistency failure did not route to Agent: %+v", result)
	}
	receipt, err := loadDraftHardConsistencyReceipt(st, 1)
	if err != nil || receipt.Passed || len(receipt.HardGateViolations) == 0 {
		t.Fatalf("failed consistency receipt not persisted: receipt=%+v err=%v", receipt, err)
	}
	if finalBody, err := st.Drafts.LoadChapterText(1); err != nil || finalBody != "" {
		t.Fatalf("needs-Agent path committed a final body: body=%q err=%v", finalBody, err)
	}
}

func TestSealedMechanicalFinalizeExternalGateEligibleRejectsMutableWork(t *testing.T) {
	const bodySHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := DraftExternalGateInspection{
		Status:              DraftExternalGateApproved,
		CurrentBodySHA256:   bodySHA,
		EvaluatedBodySHA256: bodySHA,
		ArtifactExists:      true,
	}
	if !sealedMechanicalFinalizeExternalGateEligible(base, bodySHA) {
		t.Fatal("exact approved immutable gate should be eligible")
	}

	tests := []struct {
		name   string
		mutate func(*DraftExternalGateInspection)
	}{
		{"stale evaluated hash", func(g *DraftExternalGateInspection) { g.EvaluatedBodySHA256 = strings.Repeat("b", 64) }},
		{"local soft edit pending", func(g *DraftExternalGateInspection) { g.LocalSoftEditPending = true }},
		{"local soft edit consumed", func(g *DraftExternalGateInspection) { g.LocalSoftEditConsumed = true }},
		{"local soft edit failed closed", func(g *DraftExternalGateInspection) { g.LocalSoftEditFailedClosed = true }},
		{"registered retest pending", func(g *DraftExternalGateInspection) { g.RequiresRegisteredRetest = true }},
		{"automated hard requirement", func(g *DraftExternalGateInspection) {
			g.Requirement = &DraftExternalRerenderRequirement{
				ExternalRetestPolicy: DraftExternalRetestPolicyAutomatedHard,
				RequiredExternalRetests: []DraftExternalRetestIdentity{{
					Detector: "named-detector",
					Mode:     "full",
				}},
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inspection := base
			tt.mutate(&inspection)
			if sealedMechanicalFinalizeExternalGateEligible(inspection, bodySHA) {
				t.Fatalf("mutable/retest state was mechanically eligible: %+v", inspection)
			}
		})
	}
}

func sealedMechanicalFinalizeFixture(
	t *testing.T,
	body string,
	approved bool,
) (*store.Store, domain.ProjectedChapterBundle, string) {
	t.Helper()
	st, bundle, _, _ := sealedCommitControlTestFixture(t)
	activateSealedWritingModeForTest(t, st)
	markPipelineManaged(t, st)
	if strings.TrimSpace(body) != "" {
		if err := st.Drafts.SaveDraft(1, body); err != nil {
			t.Fatal(err)
		}
	}
	body, err := st.Drafts.LoadDraft(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1),
		"draft",
		"drafts/01.draft.md",
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if approved {
		writeDraftExternalJudgeStatus(t, st.Dir(), 1, draftExternalJudgeStatus{
			BodySHA256:           reviewreport.BodySHA256(body),
			AdviceComplete:       true,
			AIProbabilityPercent: 2,
			PassExclusivePercent: 4,
		})
	}
	return st, bundle, body
}
