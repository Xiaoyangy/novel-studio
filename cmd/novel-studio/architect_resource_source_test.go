package main

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"testing"
)

func TestArchitectInitialResourceReadinessRejectsConflictingSharedTruth(t *testing.T) {
	quantity := 12.0
	entry := domain.InitialCharacterResourceV2{ResourceID: "res_0000000000000001", Name: "共用资源", Unit: "L", ActualAmount: &quantity, PerceivedName: "共用器材", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}}
	characters := []domain.Character{
		{Name: "甲", Role: "主角", InitialState: &domain.CharacterInitialState{Location: "A", CurrentGoal: "核验", Pressure: "有限时间", KnownFacts: []string{"知道现场位置"}, ResourceBalances: []domain.InitialCharacterResourceV2{entry}}},
		{Name: "乙", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "A", CurrentGoal: "工作", Pressure: "有限资源", KnownFacts: []string{"知道自己职责"}, ResourceBalances: []domain.InitialCharacterResourceV2{entry}}},
	}
	codex := &domain.WorldCodex{CharacterViewVersion: domain.CurrentWorldCharacterViewVersion}
	world := &domain.BookWorld{Places: []domain.WorldPlace{{ID: "A", Name: "A"}}}
	if findings := architectCharacterInitialStateFindings(characters, codex, world); len(findings) != 0 {
		t.Fatalf("consistent shared source rejected: %+v", findings)
	}
	other := 13.0
	characters[1].InitialState.ResourceBalances[0].ActualAmount = &other
	findings := architectCharacterInitialStateFindings(characters, codex, world)
	if len(findings) == 0 {
		t.Fatal("different world balance replicas passed readiness")
	}
	if root := architectSourceFindingRepairRoot(findings[0]); root != "characters" {
		t.Fatalf("resource-source repair lost exact authority: %q", root)
	}
}
