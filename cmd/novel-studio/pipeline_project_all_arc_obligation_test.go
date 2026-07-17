package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPipelineProjectAllArcKeepsFutureConsumerBeyondCurrentArc(t *testing.T) {
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	generation.ProjectionScope = domain.PlanningProjectionScopeArcV2
	generation.ScopeID = "v001-a001"
	generation.BookHorizonChapter = 4
	registry.ProjectionScope = generation.ProjectionScope
	registry.ScopeID = generation.ScopeID
	registry.BookHorizonChapter = generation.BookHorizonChapter
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}

	artifacts, _ := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	const contract = "第三章送达的跨弧回执必须改变主角选择"
	artifacts.WorldSimulation.CharacterDecisions[0].ButterflyEffects = []domain.DecisionButterflyEffect{{
		Effect:            contract,
		Targets:           []string{"主角"},
		TransmissionPath:  "盖章回执",
		ArrivalChapter:    3,
		Visibility:        "visible",
		ProtagonistImpact: "迫使主角公开旧规则",
	}}
	created, updated, err := pipelineProjectAllCreateObligations(
		generation,
		*artifacts.WorldSimulation,
		*artifacts.Plan,
		registry,
	)
	if err != nil {
		t.Fatalf("arc-scoped future obligation was rejected: %v", err)
	}
	var matched *domain.ObligationV2
	for i := range updated.Obligations {
		if updated.Obligations[i].Contract == contract {
			matched = &updated.Obligations[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("future obligation was not created: ids=%v registry=%+v", created, updated)
	}
	if matched.DueWindow.FromChapter != 3 ||
		matched.DueWindow.ToChapter != 3 ||
		len(matched.ConsumerChapters) != 1 || matched.ConsumerChapters[0] != 3 {
		t.Fatalf("future consumer was clamped to current arc: %+v", *matched)
	}
	updated.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(updated)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateObligationRegistryV2(updated); err != nil {
		t.Fatalf("book-horizon registry rejected exact cross-arc consumer: %v", err)
	}
}
