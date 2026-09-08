package agents

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestActivationSelectionHonorsFrozenGenerationInsteadOfNewDefaults(t *testing.T) {
	cfg := bootstrap.Config{}
	cfg.FillDefaults()
	st := store.NewStore(t.TempDir())
	for _, tc := range []struct {
		boundary ProjectedArcBoundary
		want     int
	}{
		{ProjectedArcBoundary{CharacterProtocolPinned: true}, 1},
		{ProjectedArcBoundary{CharacterProtocolPinned: true, CharacterActivationPolicy: domain.CharacterActivationCyclePolicy, MaxCharacterActivationCycles: 4}, 4},
	} {
		got, err := characterActivationExecutionLimit(st, cfg, "pg2_frozen", tc.boundary)
		if err != nil || got != tc.want {
			t.Fatalf("frozen policy changed: got=%d want=%d err=%v", got, tc.want, err)
		}
	}
	if _, err := characterActivationExecutionLimit(st, cfg, "pg2_frozen", ProjectedArcBoundary{CharacterProtocolPinned: true, CharacterActivationPolicy: "unknown"}); err == nil {
		t.Fatal("unknown generation activation protocol was accepted")
	}
}

func TestActivationPlanningIdentitySeparatesPolicyAndBounds(t *testing.T) {
	base := ProjectAllPlanningProtocolDigest("planner", domain.CharacterAgentDecisionProtocolV2Version)
	if got := ProjectAllPlanningProtocolWithActivation("planner", domain.CharacterAgentDecisionProtocolV2Version, 1); got != base {
		t.Fatal("old single-cycle planning identity changed")
	}
	first := ProjectAllPlanningProtocolWithActivation("planner", domain.CharacterAgentDecisionProtocolV2Version, 4)
	second := ProjectAllPlanningProtocolWithActivation("planner", domain.CharacterAgentDecisionProtocolV2Version, 8)
	if first == base || first == second || first == "" {
		t.Fatal("activation protocol/bounds did not change new-generation identity")
	}
}
