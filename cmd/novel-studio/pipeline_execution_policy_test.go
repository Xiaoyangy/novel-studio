package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func createExecutionPolicyPinFixture(t *testing.T, st *store.Store, policy string, limit int) domain.PlanningGenerationV2 {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	generation.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	generation.CharacterActivationPolicy, generation.MaxCharacterActivationCycles = policy, limit
	var err error
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, GenerationID: generation.GenerationID, BaseCanonChapter: generation.BaseCanonChapter, BaseCanonRoot: generation.BaseCanonRoot, BaseStateRoot: generation.BaseStateRoot, StableOutlineRoot: generation.StableOutlineRoot, PlanningDependencyRoot: generation.PlanningDependencyRoot, RandomSeedContractRoot: generation.RandomSeedContractRoot, FoundationSnapshotRoot: projectAllCmdTestDigest("policy-foundation"), RAGSnapshotRoot: projectAllCmdTestDigest("policy-rag"), CapturedAt: generation.CreatedAt}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ProjectedV2().CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProjectedV2().InitializeProjectionCursor(generation.GenerationID); err != nil {
		t.Fatal(err)
	}
	return generation
}

func TestPipelineExecutionPolicyResumePinsOriginalPolicyAndLimit(t *testing.T) {
	for _, stored := range []struct {
		policy string
		limit  int
	}{
		{"", 0}, {domain.CharacterActivationCyclePolicy, 4}, {domain.CharacterActivationCyclePolicyV2, 6},
	} {
		t.Run(stored.policy, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			generation := createExecutionPolicyPinFixture(t, st, stored.policy, stored.limit)
			before, err := store.DirectoryContentRoot(st.Dir())
			if err != nil {
				t.Fatal(err)
			}
			for _, selected := range []string{"v1", "v2"} {
				cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: selected, MaxActivationCycles: 12}}
				if err := pinPipelineActivationForExistingAttempt(&cfg, st, generation.BaseCanonChapter, generation.FirstProjectedChapter, generation.LastProjectedChapter, generation.AttemptID); err != nil {
					t.Fatal(err)
				}
				wantLimit := stored.limit
				if stored.policy == "" {
					wantLimit = 1
				}
				if cfg.CharacterActivationPolicy() != stored.policy || cfg.CharacterActivationLimit() != wantLimit {
					t.Fatalf("resume selected current config over frozen policy: %+v", cfg.CharacterAgents)
				}
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			if err != nil || before != after {
				t.Fatal("pinning rewrote existing generation or cursor")
			}
		})
	}
}

func TestPipelineExecutionPolicyNewAttemptChoosesNewConfigAndIdentity(t *testing.T) {
	st := store.NewStore(t.TempDir())
	generation := createExecutionPolicyPinFixture(t, st, domain.CharacterActivationCyclePolicy, 4)
	for _, attempt := range []string{generation.AttemptID + "new-attempt", generation.AttemptID + "|successor-digest"} {
		cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v2", MaxActivationCycles: 8}}
		if err := pinPipelineActivationForExistingAttempt(&cfg, st, generation.BaseCanonChapter, generation.FirstProjectedChapter, generation.LastProjectedChapter, attempt); err != nil {
			t.Fatal(err)
		}
		if cfg.CharacterActivationPolicy() != domain.CharacterActivationCyclePolicyV2 || cfg.CharacterActivationLimit() != 8 {
			t.Fatal("new explicit attempt inherited the abandoned execution selection")
		}
	}
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", MaxActivationCycles: 8}}
	legacyDefault := pipelineProjectAllInputDigest(cfg, assets.Bundle{})
	cfg.CharacterAgents.ExecutionPolicy = "v1"
	if got := pipelineProjectAllInputDigest(cfg, assets.Bundle{}); got != legacyDefault {
		t.Fatal("explicit/default v1 changed the original planning identity")
	}
	cfg.CharacterAgents.ExecutionPolicy = "v2"
	if got := pipelineProjectAllInputDigest(cfg, assets.Bundle{}); got == legacyDefault || got == "" {
		t.Fatal("new execution policy failed to separate generation input identity")
	}
}
