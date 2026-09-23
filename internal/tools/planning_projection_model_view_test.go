package tools

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func rejectedProjectionFixture() map[string]any {
	effects := make([]string, 40)
	for i := range effects {
		effects[i] = strings.Repeat("完整已知结果；", 500)
	}
	return map[string]any{"chapter_world_simulation": map[string]any{"status": "ready", "simulation_id": "test-source", "protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "甲", ChosenDecision: "原决定", ObservableEffects: effects}}, "working_memory": map[string]any{"current_chapter_outline": domain.OutlineEntry{Chapter: 2, Title: "第二章"}}}
}

func TestPlanningProjectionFallbackPreservesEveryPreviouslySuccessfulPacket(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	for _, profile := range []string{"planning", "full", "world_simulation", "draft"} {
		t.Run(profile, func(t *testing.T) {
			makeResult := func() map[string]any {
				return map[string]any{"chapter_world_simulation": map[string]any{"status": "ready", "simulation_id": "old", "protagonist_projection": domain.ProtagonistDecisionProjection{Protagonist: "甲", ObservableEffects: []string{"完整事实；完整事实；"}, ChosenDecision: "决定"}}, "working_memory": map[string]any{"current_chapter_outline": domain.OutlineEntry{Chapter: 2, Title: "旧章"}}}
			}
			old, err := finalizeContextResult(makeResult(), 2, profile)
			if err != nil {
				t.Fatal(err)
			}
			current, err := tool.finalizeContextWithAccessReceipt(makeResult(), 2, profile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(old, current) {
				t.Fatal("successful preexisting view changed")
			}
		})
	}
}

func TestPlanningProjectionFallbackExcludedModesAndLegacySources(t *testing.T) {
	for _, name := range []string{"world", "full", "not_ready", "rewrite", "sealed", "v1", "single_cycle", "invalid_evidence"} {
		t.Run(name, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			tool := NewContextTool(st, References{}, "default")
			result := rejectedProjectionFixture()
			profile := "planning"
			sim := domain.ChapterWorldSimulation{Version: 2, Chapter: 2, SimulationID: "test-source", CharacterActivation: &domain.CharacterActivationSimulationBinding{CycleDigests: []string{"fake-cycle-1", "fake-cycle-2"}}}
			switch name {
			case "world":
				profile = "world_simulation"
			case "full":
				profile = "full"
			case "not_ready":
				result["chapter_world_simulation"].(map[string]any)["status"] = "partial"
			case "rewrite":
				result["rewrite_source"] = map[string]any{"current_body": "old"}
			case "sealed":
				result["sealed_convergence_replan_context"] = map[string]any{}
			case "v1":
				sim.Version = 1
				sim.CharacterActivation = nil
			case "single_cycle":
				sim.CharacterActivation.CycleDigests = sim.CharacterActivation.CycleDigests[:1]
			}
			dir := filepath.Join(st.Dir(), "meta/chapter_simulations")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(sim)
			if err := os.WriteFile(filepath.Join(dir, "002.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(result)
			changed, err := tool.compactRejectedPlanningProjection(result, 2, profile)
			after, _ := json.Marshal(result)
			if changed || !bytes.Equal(before, after) {
				t.Fatal("excluded/invalid source changed payload")
			}
			if name == "invalid_evidence" {
				if err == nil {
					t.Fatal("invalid independent source passed")
				}
			} else if err != nil {
				t.Fatalf("excluded mode tried validating source: %v", err)
			}
		})
	}
}

func TestPlanningProjectionFallbackDoesNotRelaxOldBudget(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	if raw, err := tool.finalizeContextWithAccessReceipt(rejectedProjectionFixture(), 2, "planning"); err == nil || raw != nil || !strings.Contains(err.Error(), "hard=163840") {
		t.Fatalf("unverified overflowing source did not remain fail closed: %v", err)
	}
	if contextBudget(2, "planning") != 163840 {
		t.Fatal("planning budget changed")
	}
}
