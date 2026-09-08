package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestPipelineBundleCarriesCompleteActivationEvidence(t *testing.T) {
	evidence := testutil.CharacterActivationChapter(t)
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 1)
	generation.GenerationID, registry.GenerationID = evidence.Session.GenerationID, evidence.Session.GenerationID
	generation.CharacterAgentProtocol, generation.CharacterActivationPolicy = domain.CharacterAgentDecisionProtocolV2Version, domain.CharacterActivationCyclePolicy
	generation.MaxCharacterActivationCycles = evidence.Session.MaxCycles
	var err error
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	artifacts, outline := projectAllCmdTestArtifacts(t, generation.GenerationID, 1)
	simulation, err := domain.BuildCharacterActivationSimulation(evidence, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.WorldSimulation, artifacts.CharacterActivationEvidence = &simulation, &evidence
	// Reuse the existing structural transaction fixture, not its story facts.
	raw, _ := json.Marshal(artifacts.Plan)
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "主角", "甲")), artifacts.Plan); err != nil {
		t.Fatal(err)
	}
	plan := artifacts.Plan
	plan.Title, plan.Goal, plan.Conflict, plan.Hook = "船上检查", "呈现甲自主检查与实际消耗", "时间有限", "这轮结束后仍需权衡下一步"
	plan.Contract.RequiredBeats = []string{"甲完成本轮检查，世界燃油实际减少0.2升；未测量不自动刷新本人感知"}
	plan.Contract.ForbiddenMoves = []string{"不得把未测量余额写成甲亲眼知道"}
	plan.Contract.ContinuityChecks = []string{"资源实际量与本人感知分开"}
	plan.Contract.PayoffPoints = []string{"完成所选检查"}
	plan.CausalSimulation.InitialState[0].Character = "甲"
	plan.CausalSimulation.CausalBeats = []domain.CausalSimulationBeat{{Cause: "时间有限", CharacterChoice: "甲选择继续检查", WorldResponse: "检查执行并消耗燃油", StoryResult: "本轮完成"}}
	plan.CausalSimulation.OutcomeShift = []string{"甲完成本轮检查"}
	for i := range plan.CausalSimulation.RenderCapacity.SceneUnits {
		unit := &plan.CausalSimulation.RenderCapacity.SceneUnits[i]
		unit.POVObjective, unit.ActiveOpposition, unit.Turn, unit.ExitConsequence = "完成本人的检查", "时间有限", "选择落成实际行动", "本轮观察与消耗已发生"
		unit.ConcreteActionBeats = []string{"甲收回注意力", "甲按所选步骤检查", "时间继续经过"}
	}
	plan.CausalSimulation.WorldSimulationID, plan.CausalSimulation.ProtagonistDecision = simulation.SimulationID, simulation.ProtagonistProjection.ChosenDecision
	outline.Title, outline.CoreEvent, outline.Hook = plan.Title, plan.Goal, plan.Hook
	projectAllCmdTestBindPlanningContext(t, artifacts, generation, nil, registry, 1)
	simulation, err = domain.BuildCharacterActivationSimulation(evidence, "", artifacts.WorldSimulation.Sources)
	if err != nil {
		t.Fatal(err)
	}
	artifacts.WorldSimulation = &simulation
	plan.CausalSimulation.WorldSimulationID = simulation.SimulationID
	input, err := domain.NewActivationPlanGroundingInput(*plan, simulation, evidence, projectAllCmdTestDigest("explicit-test-classifier"))
	if err != nil {
		t.Fatal(err)
	}
	// This tests structural source binding. It does not claim a real model
	// judged prose quality or that a body has been generated/accepted.
	passed, err := domain.FinalizePlanGroundingReceipt(input, domain.PlanGroundingVerdict{Pass: true})
	if err != nil {
		t.Fatal(err)
	}
	plan.GroundingReview = &passed
	genesis, err := domain.DeriveProjectedChainGenesisV2(generation)
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := buildPipelineProjectedChapterBundle(generation, outline, genesis, generation.BaseStateRoot, artifacts, registry)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.CharacterAgentEvidence != nil || bundle.CharacterActivationEvidence == nil || bundle.CharacterActivationEvidence.Digest != evidence.Digest {
		t.Fatal("bundle omitted or downgraded whole-chapter evidence")
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateGenerationCharacterProtocolV2(generation, bundle); err != nil {
		t.Fatal(err)
	}
	oldGeneration := generation
	oldGeneration.CharacterActivationPolicy = ""
	if err := domain.ValidateGenerationCharacterProtocolV2(oldGeneration, bundle); err == nil {
		t.Fatal("old generation silently mixed in multi-cycle protocol")
	}
	for _, change := range []func(*domain.ProjectedChapterBundle){
		func(b *domain.ProjectedChapterBundle) { b.CharacterActivationEvidence = nil },
		func(b *domain.ProjectedChapterBundle) {
			b.CharacterActivationEvidence.Reviews[0].Receipt.Reason = "篡改审查"
		},
		func(b *domain.ProjectedChapterBundle) {
			b.CharacterActivationEvidence.Inputs[0].Observations[0].CurrentGoal = "篡改观察"
		},
	} {
		raw, _ := json.Marshal(bundle)
		var bad domain.ProjectedChapterBundle
		if err := json.Unmarshal(raw, &bad); err != nil {
			t.Fatal(err)
		}
		change(&bad)
		bad.BundleDigest, err = domain.ComputeProjectedChapterBundleDigest(bad)
		if err != nil {
			t.Fatal(err)
		}
		if err := domain.ValidateProjectedChapterBundle(bad); err == nil {
			t.Fatal("outer re-signing hid missing/tampered cycle evidence")
		}
	}
}
