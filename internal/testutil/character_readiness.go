package testutil

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func CharacterReadiness(t *testing.T, final bool) (domain.CharacterReadinessContext, domain.CharacterActivationSession, domain.CharacterActivationCycle, domain.CharacterReadinessReviewInput) {
	t.Helper()
	bookLast := 3
	if final {
		bookLast = 1
	}
	context, err := domain.FinalizeCharacterReadinessContext(domain.CharacterReadinessContext{GenerationID: "pg2_cycle_fixture", Chapter: 1, POVCharacter: "甲", ArcLastChapter: 1, BookLastChapter: bookLast, TargetWords: 2250, SoftOutline: domain.OutlineEntry{Chapter: 1, Title: "检查设备", CoreEvent: "角色独立选择检查"}, HardContracts: []string{"保留全书结局"}, Obligations: []domain.ProjectedPlanningObligationV2{{ID: "due_check", Contract: "完成本轮检查", Hardness: domain.ObligationHardV2, DueNow: true}}})
	if err != nil {
		t.Fatal(err)
	}
	cycle := CharacterCycle(t, 1, "", nil, 0, context.Digest)
	baseline, err := domain.NewCharacterActivationSession(cycle.GenerationID, 1, context.Digest, *cycle.Evidence.Stimulus.PhysicalState, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.AppendCharacterActivationCycle(baseline, cycle)
	if err != nil {
		t.Fatal(err)
	}
	input, err := domain.NewCharacterReadinessReviewInput(context, session, []domain.CharacterActivationCycle{cycle}, "sha256:"+strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	return context, session, cycle, input
}

func ReadyVerdict(input domain.CharacterReadinessReviewInput) domain.CharacterReadinessVerdict {
	ref := input.Trace.Cycles[len(input.Trace.Cycles)-1].ArbitrationDigest
	verdict := domain.CharacterReadinessVerdict{Decision: "ready_for_plan", Reason: "实际检查与资源消耗已闭合，后续义务仍可兑现", EvidenceRefs: []string{ref}, ContractChecks: []domain.CharacterReadinessContractCheck{}}
	for _, requirement := range input.Requirements {
		status := "pending"
		if requirement.DueNow {
			status = "satisfied"
		}
		verdict.ContractChecks = append(verdict.ContractChecks, domain.CharacterReadinessContractCheck{ContractID: requirement.ID, Status: status, EvidenceRefs: []string{ref}})
	}
	return verdict
}
