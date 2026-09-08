package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

// A real V3 Store seal, using actual task submission/arbitration and a structural
// planning fixture. No provider is constructed and no seal metadata is forged.
func TestContinuationProducerCLIRecoversVerifiedSealedV3(t *testing.T) {
	f := newPublicationArtifactFixture(t)
	outlines := []domain.OutlineEntry{{Chapter: 1, Title: "有限声明", CoreEvent: "甲实际记录并独立签认", Hook: "保留声明边界"}}
	publicationArtifactMust(t, f.st.Outline.SaveOutline(outlines))
	publicationArtifactMust(t, f.st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Title: "一卷", Arcs: []domain.ArcOutline{{Index: 1, Title: "一弧", Goal: "保留真实声明", Chapters: outlines}}}}))
	publicationArtifactMust(t, f.st.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "本人承担有限声明", EstimatedScale: "一章"}))
	publicationArtifactMust(t, f.st.World.SaveWorldRules([]domain.WorldRule{{Category: "society", Rule: "本人声明不自动等于独立核验", Boundary: "只保留真实发生"}}))
	progress := &domain.Progress{NovelName: "旧V3封版恢复", Phase: domain.PhaseWriting, Flow: domain.FlowWriting, CurrentChapter: 1, TotalChapters: 1, GenerationID: "sealed-producer-canon"}
	publicationArtifactMust(t, f.st.Progress.Save(progress))
	_, err := writePipelinePlanningJSON(filepath.Join(f.st.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{Version: "project-all-attempt.v2", Nonce: "sealed-old-producer"})
	publicationArtifactMust(t, err)
	base, err := pipelineCanonRoot(f.st.Dir(), progress)
	publicationArtifactMust(t, err)
	dependencies, artifacts, err := pipelinePlanningDependencies(f.st.Dir())
	publicationArtifactMust(t, err)
	fingerprint, err := domain.NewDependencyFingerprint(progress.GenerationID, base, dependencies)
	publicationArtifactMust(t, err)
	projectAllCmdTestInstallPreplanManifests(t, f.st, outlines, progress.GenerationID, base, fingerprint)
	preplan := pipelinePreplanReceipt{Version: pipelinePlanningSchema, GenerationID: progress.GenerationID, BaseCanonRoot: base, CurrentCanonRoot: base, DependencyRoot: fingerprint.RootSHA256, TotalChapters: 1, VolumeIndices: []int{1}, StagedChapters: []int{1}, DetailedChapters: []int{1}, CreatedAt: "2026-09-08T00:00:00Z", SourceArtifacts: artifacts}
	_, err = writePipelinePlanningJSON(filepath.Join(f.st.Dir(), pipelinePlanningReceiptPath), preplan)
	publicationArtifactMust(t, err)
	producer := agents.CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3)[1]
	cfg := bootstrap.Config{OutputDir: f.st.Dir(), Provider: "ollama", ModelName: "no-provider", CharacterAgents: bootstrap.CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v3", MaxActivationCycles: 4, FrozenActivationProducer: producer}}
	prompts := assets.Bundle{}
	identity, err := buildPipelineProjectAllIdentity(cfg, prompts, f.st, progress)
	publicationArtifactMust(t, err)
	live := f.st
	publicationArtifactMust(t, live.ProjectedV2().CreateBuildingGeneration(identity.Generation, identity.Source, identity.Registry))
	shadow := t.TempDir()
	publicationArtifactMust(t, os.CopyFS(shadow, os.DirFS(live.Dir())))
	f.st = store.NewStore(shadow)
	// Rebind this fresh fixture before it has authored any task. The older,
	// unrelated fixture generation is retained and never modified.
	f.generation, f.registry, f.producer = identity.Generation, identity.Registry, producer
	f.readiness.GenerationID = identity.Generation.GenerationID
	f.readiness, err = domain.FinalizeCharacterReadinessContext(f.readiness)
	publicationArtifactMust(t, err)
	f.session, err = domain.NewCharacterActivationSession(f.generation.GenerationID, 1, f.readiness.Digest, *f.input.Stimulus.PhysicalState, 0, 4)
	publicationArtifactMust(t, err)
	f.input.Stimulus.GenerationID, f.input.Activation.GenerationID = f.generation.GenerationID, f.generation.GenerationID
	for i := range f.input.Observations {
		f.input.Observations[i].GenerationID = f.generation.GenerationID
	}
	for i := range f.input.Memories {
		f.input.Memories[i].GenerationID = f.generation.GenerationID
		f.input.Memories[i], err = domain.FinalizeCharacterAgentMemory(f.input.Memories[i])
		publicationArtifactMust(t, err)
	}
	f.rebind(t)
	publicationArtifactMust(t, f.st.SaveCharacterReadinessContext(f.readiness))
	publicationArtifactMust(t, f.st.CreateCharacterActivationSession(f.session))
	publicationArtifactMust(t, f.st.ProjectedV2().CreateBuildingGeneration(identity.Generation, identity.Source, identity.Registry))
	f.cycle(t, false)
	f.cycle(t, true)
	sealContinuationProducerFixture(t, f)
	// Mirror production: execute only in the isolated copy, then publish the
	// validated bundle/registry to live. Private work proofs are never canon.
	bundles, err := f.st.ProjectedV2().LoadProjectedChapterBundles(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	registry, err := f.st.ProjectedV2().LoadObligationRegistry(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, live.ProjectedV2().SaveObligationRegistry(f.generation.GenerationID, *registry))
	for _, bundle := range bundles {
		publicationArtifactMust(t, live.ProjectedV2().SaveProjectedChapterBundle(bundle))
	}
	_, err = live.ProjectedV2().SealGeneration(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	f.st = live
	sealed, err := f.st.ProjectedV2().LoadSealedGeneration(f.generation.GenerationID)
	publicationArtifactMust(t, err)
	if sealed == nil {
		t.Fatal("fixture was not actually sealed")
	}
	cfg.CharacterAgents.FrozenActivationProducer = ""
	before, err := store.DirectoryContentRoot(f.st.Dir())
	publicationArtifactMust(t, err)
	resumed, err := buildPipelineProjectAllIdentity(cfg, prompts, store.NewStore(f.st.Dir()), progress)
	publicationArtifactMust(t, err)
	if resumed.Generation.GenerationID != sealed.GenerationID || resumed.FrozenActivationProducer != producer {
		t.Fatal("sealed V3 was reidentified or silently upgraded")
	}
	publicationArtifactMust(t, validatePipelineSealedGenerationDependencies(cfg, prompts, *sealed))
	changed := cfg
	changed.ModelName += "-drift"
	if err := validatePipelineSealedGenerationDependencies(changed, prompts, *sealed); err == nil {
		t.Fatal("sealed promotion swallowed actual model drift while selecting old producer")
	}
	after, err := store.DirectoryContentRoot(f.st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("sealed identity recovery rewrote source/evidence/cursor")
	}
}

func sealContinuationProducerFixture(t *testing.T, f *publicationArtifactFixture) {
	t.Helper()
	evidence, err := f.st.CollectCharacterActivationChapterEvidence(f.session.GenerationID, 1)
	publicationArtifactMust(t, err)
	planning, err := domain.DeriveProjectedPlanningContextV2(f.generation, nil, f.registry, 1)
	publicationArtifactMust(t, err)
	token, err := domain.ProjectedPlanningContextSourceTokenV2(planning.ContextDigest)
	publicationArtifactMust(t, err)
	simulation, _, err := tools.PublishCharacterActivationSimulation(context.Background(), f.st, f.session.GenerationID, 1, []string{token})
	publicationArtifactMust(t, err)
	artifacts, outline := projectAllCmdTestArtifacts(t, f.session.GenerationID, 1)
	raw, _ := json.Marshal(artifacts.Plan)
	publicationArtifactMust(t, json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "主角", "甲")), artifacts.Plan))
	artifacts.WorldSimulation, artifacts.CharacterActivationEvidence = simulation, evidence
	plan := artifacts.Plan
	plan.Title, plan.Goal, plan.Conflict, plan.Hook = "有限声明", "甲记录并自主签认有限声明", "声明不能代替真相", "本人留下有边界的签认"
	plan.Contract.RequiredBeats = []string{"甲实际写下有限声明，之后独立签认这一版本"}
	plan.Contract.ForbiddenMoves = []string{"不得把签认升级为世界真相"}
	plan.Contract.ContinuityChecks = []string{"纸张只分配一次，签名只绑定确切版本"}
	plan.Contract.PayoffPoints = []string{"甲承担本人的声明范围"}
	plan.CausalSimulation.WorldSimulationID, plan.CausalSimulation.ProtagonistDecision = simulation.SimulationID, simulation.ProtagonistProjection.ChosenDecision
	plan.CausalSimulation.CausalBeats = []domain.CausalSimulationBeat{{Cause: "本人愿意留下有边界的记录", CharacterChoice: "甲记录并独立签认", WorldResponse: "材料分配形成实际文书与版本签名", StoryResult: "有限声明留存"}}
	plan.CausalSimulation.OutcomeShift = []string{"甲保留有版本绑定的本人签认"}
	for i := range plan.CausalSimulation.RenderCapacity.SceneUnits {
		unit := &plan.CausalSimulation.RenderCapacity.SceneUnits[i]
		unit.POVObjective = "完成本人工序"
		unit.ActiveOpposition = "避免超出所知范围"
		unit.Turn = "本人决定落为真实结果"
		unit.ExitConsequence = "本人保留明确结果"
		unit.ConcreteActionBeats = []string{"甲查看手边纸张", "甲完成本人声明工序", "甲确认有限范围"}
	}
	artifacts.PlanningContextDigest = planning.ContextDigest
	plan.CausalSimulation.ArcTransition = domain.ArcChapterTransitionContract{OutgoingConsequenceID: "artifact-end", OutgoingConsequenceText: "本人承担有限声明"}
	fact, err := domain.NewRAGFactReceipt(1, "artifact fixture", []string{"fixture"}, "no_material_v1", "", nil)
	publicationArtifactMust(t, err)
	artifacts.RAGFactReceipt = &fact
	craft := projectAllCmdTestNoMaterialCraftReceipt(t, f.session.GenerationID, 1, planning.ContextDigest)
	artifacts.CraftRecallReceipt = &craft
	plan.CausalSimulation.ContextSources = []string{token, fact.SourceToken(), domain.CraftRecallReceiptSourceTokenV2(craft)}
	grounding, err := domain.NewActivationPlanGroundingInput(*plan, *simulation, *evidence, projectAllCmdTestDigest("explicit-structural-fixture-review"))
	publicationArtifactMust(t, err)
	passed, err := domain.FinalizePlanGroundingReceipt(grounding, domain.PlanGroundingVerdict{Pass: true})
	publicationArtifactMust(t, err)
	plan.GroundingReview = &passed // Structural fixture; no claim of a model quality judgment.
	outline.Title, outline.CoreEvent, outline.Hook = plan.Title, plan.Goal, plan.Hook
	genesis, err := domain.DeriveProjectedChainGenesisV2(f.generation)
	publicationArtifactMust(t, err)
	bundle, nextRegistry, err := buildPipelineProjectedChapterBundle(f.generation, outline, genesis, f.generation.BaseStateRoot, artifacts, f.registry)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, domain.ValidateProjectedChapterBundle(bundle))
	publicationArtifactMust(t, domain.ValidateGenerationCharacterProtocolV2(f.generation, bundle))
	publicationArtifactMust(t, f.st.ProjectedV2().SaveObligationRegistry(f.generation.GenerationID, nextRegistry))
	publicationArtifactMust(t, f.st.ProjectedV2().SaveProjectedChapterBundle(bundle))
	_, err = f.st.ProjectedV2().SealGeneration(f.generation.GenerationID)
	publicationArtifactMust(t, err)
}
