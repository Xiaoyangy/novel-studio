package main

import (
	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"testing"
)

func TestPipelineV3UpgradeRequiresNewGenerationWithoutMutatingOld(t *testing.T) {
	st := store.NewStore(t.TempDir())
	g := createExecutionPolicyPinFixture(t, st, domain.CharacterActivationCyclePolicyV2, 4)
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v3", MaxActivationCycles: 8}}
	before, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := pinPipelineActivationForExistingAttempt(&cfg, st, g.BaseCanonChapter, g.FirstProjectedChapter, g.LastProjectedChapter, g.AttemptID); err == nil {
		t.Fatal("building generation silently upgraded or requested v3 silently downgraded")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || before != after {
		t.Fatal("upgrade check rewrote old generation")
	}
	if err := pinPipelineActivationForExistingAttempt(&cfg, st, g.BaseCanonChapter, g.FirstProjectedChapter, g.LastProjectedChapter, g.AttemptID+"new"); err != nil {
		t.Fatal(err)
	}
	if cfg.CharacterActivationPolicy() != domain.CharacterActivationCyclePolicyV3 {
		t.Fatal("new attempt did not keep explicit v3")
	}
	v3 := pipelineProjectAllInputDigest(cfg, assets.Bundle{})
	cfg.CharacterAgents.ExecutionPolicy = "v2"
	v2 := pipelineProjectAllInputDigest(cfg, assets.Bundle{})
	cfg.CharacterAgents.ExecutionPolicy = "v1"
	v1 := pipelineProjectAllInputDigest(cfg, assets.Bundle{})
	if v3 == "" || v3 == v2 || v3 == v1 {
		t.Fatal("v3 protocol failed to separate generation identity")
	}
}

func TestPipelineV3ResumePinsActualGenerationPolicy(t *testing.T) {
	st := store.NewStore(t.TempDir())
	g := createExecutionPolicyPinFixture(t, st, domain.CharacterActivationCyclePolicyV3, 6)
	cfg := bootstrap.Config{CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v1", MaxActivationCycles: 8}}
	if err := pinPipelineActivationForExistingAttempt(&cfg, st, g.BaseCanonChapter, g.FirstProjectedChapter, g.LastProjectedChapter, g.AttemptID); err != nil {
		t.Fatal(err)
	}
	if cfg.CharacterActivationPolicy() != domain.CharacterActivationCyclePolicyV3 || cfg.CharacterActivationLimit() != 6 {
		t.Fatal("resume reinterpreted existing v3")
	}
}
