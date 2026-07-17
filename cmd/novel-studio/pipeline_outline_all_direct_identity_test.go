package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
)

func TestPipelineOutlineAllExecutionIdentityIgnoresCoordinatorModelAndPrompt(t *testing.T) {
	cfg := bootstrap.Config{
		Provider:  "default-provider",
		ModelName: "default-model",
		Roles: map[string]bootstrap.RoleConfig{
			"coordinator": {Provider: "coordinator-a", Model: "coordinator-model-a", ReasoningEffort: "low"},
			"architect":   {Provider: "architect-provider", Model: "architect-main", ReasoningEffort: "high"},
		},
	}
	bundle := assets.Bundle{Prompts: assets.Prompts{
		Coordinator:   "coordinator prompt a",
		ArchitectLong: "architect long prompt",
	}}
	identityA, modelA, promptA, executionA, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if identityA.CoordinatorProvider != pipelineOutlineAllDispatchProvider ||
		identityA.CoordinatorModel != pipelineOutlineAllDispatchModel ||
		identityA.CoordinatorReasoning != pipelineOutlineAllDispatchReasoning {
		t.Fatalf("legacy dispatch identity = %+v", identityA)
	}

	cfg.Roles["coordinator"] = bootstrap.RoleConfig{
		Provider: "coordinator-b", Model: "coordinator-model-b", ReasoningEffort: "max",
	}
	bundle.Prompts.Coordinator = "completely different coordinator prompt"
	identityB, modelB, promptB, executionB, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if identityA != identityB || modelA != modelB || promptA != promptB || executionA != executionB {
		t.Fatalf("Coordinator drift changed direct Architect identity:\nA=%+v %s %s\nB=%+v %s %s", identityA, modelA, promptA, identityB, modelB, promptB)
	}

	cfg.Roles["architect"] = bootstrap.RoleConfig{
		Provider: "architect-provider", Model: "architect-main-v2", ReasoningEffort: "high",
	}
	_, modelC, _, executionC, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if modelC == modelA || executionC == executionA {
		t.Fatal("Architect primary model drift must change the execution identity")
	}

	cfg.Roles["architect"] = bootstrap.RoleConfig{
		Provider: "architect-provider", Model: "architect-main", ReasoningEffort: "high",
	}
	bundle.Prompts.ArchitectLong = "architect long prompt v2"
	_, _, promptD, executionD, err := pipelineOutlineAllExecutionIdentity(cfg, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if promptD == promptA || executionD == executionA {
		t.Fatal("Architect direct protocol drift must change the execution identity")
	}
}
