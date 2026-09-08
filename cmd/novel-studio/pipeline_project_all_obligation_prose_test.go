package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func projectAllObligationProseFixture(t *testing.T) (*store.Store, domain.ChapterWorldSimulation, domain.ObligationRegistryV2) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{Index: 1, Title: "一卷", Arcs: []domain.ArcOutline{{Index: 1, Title: "一弧", Chapters: []domain.OutlineEntry{{Chapter: 1, Title: "一"}, {Chapter: 2, Title: "二"}, {Chapter: 3, Title: "三"}}}}}}); err != nil {
		t.Fatal(err)
	}
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 3)
	registry.Obligations = nil
	hard := projectAllCharacterObligationTestDecision()
	hard.ButterflyEffects[0].Effect = "主角核对90升与60升记录，到第3章仍保留原始页"
	hard.ButterflyEffects[0].ArrivalChapter = 3
	hidden := projectAllCharacterObligationTestDecision()
	hidden.Character = "未向主角公开的保管人"
	hidden.ButterflyEffects[0].Effect = "封存资料仍保留2页，未向主角展示"
	hidden.ButterflyEffects[0].ArrivalChapter = 3
	hidden.ButterflyEffects[0].Visibility = "hidden"
	sim := domain.ChapterWorldSimulation{Chapter: 1, GenerationID: generation.GenerationID, CharacterAgentProtocol: &domain.CharacterAgentProtocolReceipt{Version: domain.CharacterAgentDecisionProtocolV2Version}, CharacterDecisions: []domain.CharacterWorldDecision{hard, hidden}}
	_, registry, err := pipelineProjectAllCreateObligations(generation, sim, domain.ChapterPlan{Chapter: 1, Hook: "下一步"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	return st, sim, registry
}

func TestProjectAllObligationProseRealOutlinePlanRenderPreservesFactsNotMetadata(t *testing.T) {
	for _, retainOrigin := range []bool{true, false} {
		name := "carried_registry_without_origin_sidecar"
		if retainOrigin {
			name = "current_arc_with_origin_simulation"
		}
		t.Run(name, func(t *testing.T) {
			st, sim, registry := projectAllObligationProseFixture(t)
			if retainOrigin {
				if err := st.SaveChapterWorldSimulation(sim); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := json.Marshal(registry)
			entry, err := applyPipelineProjectAllObligationsToOutline(st, registry, nil, 3)
			if err != nil || entry == nil {
				t.Fatal(err)
			}
			if _, err := applyPipelineProjectAllObligationsToOutline(st, registry, nil, 3); err != nil {
				t.Fatal(err)
			}
			stored, err := st.Outline.GetChapterOutline(3)
			if err != nil || stored == nil || len(stored.Scenes) != 2 {
				t.Fatalf("outline lost or retry duplicated audit views: %+v %v", stored, err)
			}
			plan := domain.ChapterPlan{Chapter: 3}
			tools.ApplyProjectAllOutlineObligations(&plan, stored.Scenes)
			outcomes := tools.RenderRequiredOutcomes(plan)
			continuity := tools.RenderContinuityChecks(plan)
			hard := pipelineHardRenderContractV2(plan, domain.ChapterWorldSimulation{}, domain.ProjectedDelta{})
			if len(outcomes) != 1 || outcomes[0] != sim.CharacterDecisions[0].ButterflyEffects[0].Effect || len(hard.MustOccur) != 1 || hard.MustOccur[0] != outcomes[0] {
				t.Fatalf("hard effect/real amounts changed: plan=%+v outcomes=%v hard=%+v", plan, outcomes, hard)
			}
			if len(continuity) != 1 || !strings.Contains(continuity[0], sim.CharacterDecisions[1].ButterflyEffects[0].Effect) || !strings.Contains(continuity[0], "不得越过 POV 知识边界") {
				t.Fatalf("hidden obligation lost its world-only classification: %v", continuity)
			}
			render, _ := json.Marshal([]any{outcomes, continuity, hard})
			for _, obligation := range registry.Obligations {
				idParts := strings.Split(obligation.ID, ":")
				for _, forbidden := range []string{obligation.ID, idParts[len(idParts)-1], strings.TrimPrefix(obligation.Origin.SourceDigest, "sha256:")[:20], "因果来源", sim.CharacterDecisions[1].Character} {
					if strings.Contains(string(render), forbidden) {
						t.Fatalf("provenance became prose fact: %q in %s", forbidden, render)
					}
				}
				if !strings.Contains(strings.Join(stored.Scenes, "\n"), obligation.ID) {
					t.Fatal("host outline lost audit identity")
				}
			}
			after, _ := json.Marshal(registry)
			if string(before) != string(after) {
				t.Fatal("prose projection modified registry identity")
			}
		})
	}
}

func TestProjectAllObligationProseRejectsTamperedProofAndKeepsV1UserText(t *testing.T) {
	t.Run("registry_root", func(t *testing.T) {
		st, _, registry := projectAllObligationProseFixture(t)
		registry.RegistryRoot = "sha256:" + strings.Repeat("0", 64)
		if _, err := applyPipelineProjectAllObligationsToOutline(st, registry, nil, 3); err == nil {
			t.Fatal("unverified registry was used to erase source metadata")
		}
		volumes, err := st.Outline.LoadLayeredOutline()
		if err != nil {
			t.Fatal(err)
		}
		if len(volumes[0].Arcs[0].Chapters[2].Scenes) != 0 {
			t.Fatal("failed proof wrote the outline")
		}
	})
	t.Run("source_simulation", func(t *testing.T) {
		st, sim, registry := projectAllObligationProseFixture(t)
		sim.CharacterDecisions[0].Action = "另一项行动"
		if err := st.SaveChapterWorldSimulation(sim); err != nil {
			t.Fatal(err)
		}
		if _, err := applyPipelineProjectAllObligationsToOutline(st, registry, nil, 3); err == nil {
			t.Fatal("different source simulation was accepted")
		}
	})
	t.Run("v1_literal_source_looking_text", func(t *testing.T) {
		st, sim, registry := projectAllObligationProseFixture(t)
		generation, _ := projectAllCmdTestGenerationAndRegistry(t, 3)
		registry.Obligations = nil
		sim.CharacterAgentProtocol.Version = domain.CharacterAgentDecisionProtocolVersion
		literal := "保留90与60记录〔角色：用户文字；因果来源：12345678901234567890〕"
		sim.CharacterDecisions = sim.CharacterDecisions[:1]
		sim.CharacterDecisions[0].ButterflyEffects[0].Effect = literal
		_, registry, err := pipelineProjectAllCreateObligations(generation, sim, domain.ChapterPlan{Chapter: 1}, registry)
		if err != nil {
			t.Fatal(err)
		}
		registry.RegistryRoot, _ = domain.ComputeObligationRegistryV2Root(registry)
		entry, err := applyPipelineProjectAllObligationsToOutline(st, registry, nil, 3)
		if err != nil {
			t.Fatal(err)
		}
		plan := domain.ChapterPlan{Chapter: 3}
		tools.ApplyProjectAllOutlineObligations(&plan, entry.Scenes)
		if got := strings.Join(tools.RenderRequiredOutcomes(plan), "\n"); !strings.Contains(got, literal) || !strings.Contains(got, registry.Obligations[0].ID) {
			t.Fatalf("v1 contract/tag behavior changed: %s", got)
		}
	})
}
