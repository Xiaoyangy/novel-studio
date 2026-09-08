package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPipelineArcRehearsalCapabilitiesUseActualConfigAcrossBoundaries(t *testing.T) {
	opts, st, _ := v3RestartEntryFixture(t)
	// The older CLI identity fixture predates explicit character initial state.
	publicationArtifactMust(t, st.Characters.Save([]domain.Character{{Name: "甲", Role: "值班员", Tier: "core", InitialState: &domain.CharacterInitialState{Location: "值班室", CurrentGoal: "按本人所见核对", Pressure: "有限时间", KnownFacts: []string{"知道本人核对职责"}}}}))
	cfg, _, err := loadPipelineDefaultInputsReadOnly(opts)
	publicationArtifactMust(t, err)
	input, err := buildPipelineArcRehearsalInput(st, cfg)
	publicationArtifactMust(t, err)
	profile, err := agents.ArcRehearsalExecutionCapabilities(cfg)
	publicationArtifactMust(t, err)
	if input.ExecutionCapabilities == nil || !reflect.DeepEqual(*input.ExecutionCapabilities, profile) || profile.ActivationPolicy != domain.CharacterActivationCyclePolicyV3 {
		t.Fatal("CLI omitted the actual V3 configuration")
	}
	defaultInput, err := buildPipelineArcRehearsalInput(st)
	publicationArtifactMust(t, err)
	if input.InputDigest == defaultInput.InputDigest || input.SourceRoot != defaultInput.SourceRoot || !reflect.DeepEqual(input.CharacterObservations, defaultInput.CharacterObservations) {
		t.Fatal("actual profile did not alter only rehearsal identity (not foundation or character knowledge)")
	}
	body := domain.ArcRehearsalBody{Summary: "测试仅确认条件性本人行动，无新外部材料结论", MaterialChecks: []domain.ArcRehearsalMaterialCheck{{Operation: "本测试无外部资料读取", Status: "not_required", Explanation: "只核配置传递，不认证真实故事可达"}}}
	for _, c := range input.Outline {
		body.Chapters = append(body.Chapters, domain.ArcRehearsalChapter{Chapter: c.Chapter, ConditionalForecast: "若条件成立则按本人选择行动", Assumptions: []string{"不是已执行"}, CausalLinks: []string{"依实际条件"}, TimeResourceChecks: []string{"实际工时待裁决"}})
	}
	for _, o := range input.CharacterObservations {
		body.CharacterConflicts = append(body.CharacterConflicts, domain.ArcRehearsalCharacterConflict{Character: o.Character, CurrentGoal: o.CurrentGoal, Conflicts: []string{"条件待核"}, ConditionalChoices: []string{"本人选择尚未发生"}})
	}
	for _, contract := range input.HardContracts {
		body.ContractChecks = append(body.ContractChecks, domain.ArcRehearsalContractCheck{Contract: contract, Assessment: "conditional", Conditions: []string{"实际依约执行"}})
	}
	call := func(role, id string) domain.ArcRehearsalCall {
		return domain.ArcRehearsalCall{Role: role, Provider: "test", Model: "no-provider", UsageIDs: []string{id}, ToolCallID: id, ResponseDigest: projectAllCmdTestDigest(id)}
	}
	draft, err := domain.FinalizeArcRehearsalDraft(input, domain.ArcRehearsalDraft{Body: body, Call: call("architect", "draft")})
	publicationArtifactMust(t, err)
	report, err := domain.FinalizeArcRehearsalReport(input, draft, domain.ArcRehearsalReport{Body: body, Call: call("world_arbiter", "review")})
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.SaveArcRehearsalReport(input, draft, report))
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	arc, err := locatePipelineArcScope(st, 1)
	publicationArtifactMust(t, err)
	window, err := loadPipelineDetailWindow(st, arc, 0, cfg)
	publicationArtifactMust(t, err)
	if window == nil || window.RehearsalDigest != report.ReportDigest {
		t.Fatal("detail boundary did not use the actual-profile report")
	}
	_, err = verifyPipelineArcRehearsalStage(st.Dir(), domain.PipelineStageEvidence{}, cfg)
	publicationArtifactMust(t, err)
	if _, err := loadPipelineDetailWindow(st, arc, 0); err == nil {
		t.Fatal("default profile authorized a V3 report")
	}
	if _, err := verifyPipelineArcRehearsalStage(st.Dir(), domain.PipelineStageEvidence{}); err == nil {
		t.Fatal("stage verifier silently omitted actual configuration")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("read-only profile checks changed source, generation or report")
	}
}

func TestPipelineArcRehearsalLegacyBuildingWindowRecoversWithoutForecast(t *testing.T) {
	opts, st, old := v3RestartEntryFixture(t)
	cfg, bundle, err := loadPipelineDefaultInputsReadOnly(opts)
	publicationArtifactMust(t, err)
	// A real pre-capability report and a real building envelope, no sealed
	// active generation. The new default profile must not replace this report.
	input, report := pipelineWindowCompletionTestRehearsal(t, st, old.Source, 1, 3, 1, nil)
	window := &domain.PlanningDetailWindowV1{Version: domain.PlanningDetailWindowVersionV1, ArcID: input.ArcID, ArcFirstChapter: 1, ArcLastChapter: 3, RehearsalDigest: report.ReportDigest, RehearsalInputDigest: input.InputDigest}
	nonce := "legacy-rehearsal-building-window"
	_, err = writePipelinePlanningJSON(filepath.Join(st.Dir(), pipelineProjectAllAttemptPath), pipelineProjectAllAttempt{Version: "project-all-attempt.v2", Nonce: nonce})
	publicationArtifactMust(t, err)
	root, err := pipelineProjectAllDependencyRootWithSourceRoots(cfg, bundle, old.Preplan, old.Source.FoundationSnapshotRoot, old.Source.RAGSnapshotRoot)
	publicationArtifactMust(t, err)
	root, err = domain.PlanningDetailWindowDependencyRootV1(root, *window)
	publicationArtifactMust(t, err)
	g, source, registry := old.Generation, old.Source, old.Registry
	g.GenerationID, err = domain.DerivePlanningGenerationAttemptV2ID(g.BaseCanonRoot, g.StableOutlineRoot, root, g.RandomSeedContractRoot, nonce)
	publicationArtifactMust(t, err)
	g.AttemptID, g.PlanningDependencyRoot, g.DetailWindow = nonce, root, window
	g.CharacterActivationPolicy, g.MaxCharacterActivationCycles = cfg.CharacterActivationPolicy(), cfg.CharacterActivationLimit()
	registry.GenerationID = g.GenerationID
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	publicationArtifactMust(t, err)
	g.ObligationRegistryRoot = registry.RegistryRoot
	g.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(g)
	publicationArtifactMust(t, err)
	source.GenerationID, source.PlanningDependencyRoot, source.DetailWindow = g.GenerationID, root, window
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	publicationArtifactMust(t, err)
	publicationArtifactMust(t, st.ProjectedV2().CreateBuildingGeneration(g, source, registry))
	publicationArtifactMust(t, st.ProjectedV2().ResetProjectionCursorForRestart(g.GenerationID))
	_, err = st.ProjectedV2().InitializeProjectionCursor(g.GenerationID)
	publicationArtifactMust(t, err)
	previous := saveDefaultMigrationState(t, opts, st, defaultPipelineStages)
	previous.MarkDone("rehearse-arc", domain.PipelineStageEvidence{Stage: "rehearse-arc", Status: "verified", Message: report.ReportDigest})
	publicationArtifactMust(t, savePipelineState(filepath.Join(st.Dir(), "meta/pipeline.json"), previous))
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	resume, err := preparePipelineDefaultResume(opts, pipelineFlags{}, defaultPipelineStages, "")
	publicationArtifactMust(t, err)
	if resume == nil || !resume.Preserve || resume.Generation.GenerationID != g.GenerationID || !resume.Previous.Done("rehearse-arc") {
		t.Fatal("default preflight lost the old building window")
	}
	evidence, err := verifyPipelineStage("rehearse-arc", st.Dir(), pipelineFlags{}, previous, cfg)
	publicationArtifactMust(t, err)
	if !strings.Contains(evidence.Message, report.ReportDigest) {
		t.Fatal("stage verifier reinterpreted the old report under the new capability profile")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if before != after {
		t.Fatal("legacy report verification moved generation, source, cursor or state")
	}
	t.Run("lost-projection-cursor", func(t *testing.T) {
		path := filepath.Join(st.Dir(), "meta/planning/v2/projection_cursor.json")
		raw, err := os.ReadFile(path)
		publicationArtifactMust(t, err)
		publicationArtifactMust(t, os.Remove(path))
		defer func() { publicationArtifactMust(t, os.WriteFile(path, raw, 0600)) }()
		before, err := store.DirectoryContentRoot(st.Dir())
		publicationArtifactMust(t, err)
		evidence, err := verifyPipelineStage("rehearse-arc", st.Dir(), pipelineFlags{}, previous, cfg)
		publicationArtifactMust(t, err)
		if !strings.Contains(evidence.Message, report.ReportDigest) {
			t.Fatal("lost cursor lost the frozen report")
		}
		after, err := store.DirectoryContentRoot(st.Dir())
		publicationArtifactMust(t, err)
		if before != after {
			t.Fatal("read-only recovery recreated a cursor")
		}
	})
	for _, kind := range []string{"model", "source", "outline"} {
		t.Run(kind, func(t *testing.T) {
			changed := cfg
			restore := func() {}
			switch kind {
			case "model":
				changed.ModelName += "-drift"
			case "source", "outline":
				path := "world_rules.json"
				if kind == "outline" {
					path = "outline.json"
				}
				path = filepath.Join(st.Dir(), path)
				original, err := os.ReadFile(path)
				publicationArtifactMust(t, err)
				// Whitespace changes the captured source byte hash without
				// invalidating JSON or depending on semantic interpretation.
				publicationArtifactMust(t, os.WriteFile(path, append(original, '\n'), 0600))
				restore = func() { publicationArtifactMust(t, os.WriteFile(path, original, 0600)) }
			}
			defer restore()
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if _, err := verifyPipelineStage("rehearse-arc", st.Dir(), pipelineFlags{}, previous, changed); err == nil {
				t.Fatal("historical report swallowed actual input drift")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("failed legacy recovery wrote state")
			}
		})
	}
}
