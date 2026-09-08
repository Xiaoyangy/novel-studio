package domain_test

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestReadinessChecksDueContractsWithoutForcingFutureEnding(t *testing.T) {
	_, _, _, input := testutil.CharacterReadiness(t, false)
	verdict := testutil.ReadyVerdict(input)
	r, err := domain.FinalizeCharacterReadinessReview(input, verdict)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != domain.CharacterReadinessReviewedVersion || r.InputDigest == "" || r.Decision != "ready_for_plan" {
		t.Fatal("missing reviewed readiness binding")
	}
	if err := domain.ValidateCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: input, Receipt: r}); err != nil {
		t.Fatal(err)
	}
	_, _, _, finalInput := testutil.CharacterReadiness(t, true)
	if _, err := domain.FinalizeCharacterReadinessReview(finalInput, verdict); err == nil {
		t.Fatal("final chapter accepted an ending deferred to the future")
	}
}

func TestReadinessRejectsMissingInventedOrOnlyIntendedEvidence(t *testing.T) {
	for _, mode := range []string{"missing-check", "pending-due", "unknown-ref", "proposal-only", "limit-is-conflict", "altered-requirements", "altered-context"} {
		t.Run(mode, func(t *testing.T) {
			_, _, _, input := testutil.CharacterReadiness(t, false)
			verdict := testutil.ReadyVerdict(input)
			switch mode {
			case "missing-check":
				verdict.ContractChecks = verdict.ContractChecks[:1]
			case "pending-due":
				verdict.ContractChecks[1].Status = "pending"
			case "unknown-ref":
				verdict.EvidenceRefs = []string{"invented-result"}
			case "proposal-only":
				verdict.EvidenceRefs = []string{input.Trace.Cycles[0].Actions[0].ProposalDigest}
			case "limit-is-conflict":
				input.RemainingCycles = 0
				verdict.Decision = "hard_conflict"
			case "altered-requirements":
				input.Requirements[1].DueNow = false
			case "altered-context":
				input.Context.HardContracts = nil
			}
			if _, err := domain.FinalizeCharacterReadinessReview(input, verdict); err == nil {
				t.Fatal("invalid readiness verdict accepted")
			}
		})
	}
}

func TestReadinessReceiptTamperingFailsVerification(t *testing.T) {
	_, _, _, input := testutil.CharacterReadiness(t, false)
	r, err := domain.FinalizeCharacterReadinessReview(input, testutil.ReadyVerdict(input))
	if err != nil {
		t.Fatal(err)
	}
	r.Reason = "重新解释但保留旧摘要"
	if err := domain.ValidateCharacterReadinessReviewAudit(domain.CharacterReadinessReviewAudit{Input: input, Receipt: r}); err == nil {
		t.Fatal("readiness receipt tampering passed")
	}
}
