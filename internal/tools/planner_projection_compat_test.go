package tools

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Golden bytes were captured before the overflow representation was introduced.
// These successful inputs must never acquire the new model-only encoding.
func TestPlannerProjectionOverflowKeepsLegacySuccessfulPackets(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	for _, mode := range []string{"plain", "rewrite", "sealed"} {
		t.Run(mode, func(t *testing.T) {
			projection := domain.ProtagonistDecisionProjection{
				Protagonist: "林澈", ObservableEffects: []string{"同一句可见结果。", "同一句可见结果。"},
				HiddenPressures: []string{"他不知道对方尚未到场"}, AvailableOptions: []string{"等待", "离开"},
				ChosenDecision: "等待", DecisionReason: "尚未收到确认", PlanConstraints: []string{"不能写成已见面"}, CausalChain: []string{"消息未到", "决定等待"},
			}
			result := map[string]any{
				"chapter_world_simulation": map[string]any{"status": "ready", "simulation_id": "sim-legacy", "protagonist_projection": projection},
				"active_chapter_task":      map[string]any{"chapter": 1, "goal": "保持原来的任务"},
			}
			if mode == "rewrite" {
				delete(result, "chapter_world_simulation")
				result["working_memory"] = map[string]any{
					"rewrite_source":          map[string]any{"current_body": strings.Repeat("b", 120*1024)},
					"rewrite_brief":           map[string]any{"brief_markdown": strings.Repeat("r", 70*1024)},
					"current_chapter_outline": map[string]any{"chapter": 1, "goal": "保留当前章任务"},
				}
			}
			if mode == "sealed" {
				result["sealed_convergence_replan_context"] = map[string]any{"version": sealedConvergenceAuthorityOverlayVersion, "diagnostics": map[string]any{"issue_classes": []string{"local_structure:blocking"}}}
				result["working_memory"] = map[string]any{"current_chapter_outline": domain.OutlineEntry{Chapter: 1, CoreEvent: strings.Repeat("当前章不可变事件边界", 6500)}}
			}
			replay := plannerProjectionLegacyPacketCopy(result)
			raw, err := finalizeContextResult(result, 1, "planning")
			if err != nil {
				t.Fatal(err)
			}
			if mode != "plain" && (len(raw) <= 160*1024 || len(raw) > 224*1024) {
				t.Fatalf("fixture must use legacy bounded overflow: %d", len(raw))
			}
			if strings.Contains(string(raw), "shared_text") {
				t.Fatal("legacy successful packet acquired compact encoding")
			}
			current, err := tool.finalizeContextWithAccessReceipt(replay, 1, "planning")
			if err != nil || !bytes.Equal(raw, current) {
				t.Fatalf("old successful packet changed in fallback entry point: %v", err)
			}
			got := fmt.Sprintf("%x", sha256.Sum256(raw))
			want := map[string]string{"plain": "c23c63b0733c54a108771ec65f649e936b9a37272233054def3fc04df7bb6798", "rewrite": "190da610d7d924d32df19607ca006bab361d44055ca39a18eaf72dbc264790df", "sealed": "6c02580a542a71d11813eb04f2496bddff542983c1ec72cc3f74eadba9f4d9a7"}[mode]
			if got != want {
				t.Fatalf("legacy %s bytes changed: got=%s want=%s", mode, got, want)
			}
		})
	}
}

// Keep Go field representations identical on both calls: a JSON roundtrip
// changes typed projections to maps and is itself a separate context input.
func plannerProjectionLegacyPacketCopy(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		if nested, ok := value.(map[string]any); ok {
			copy[key] = plannerProjectionLegacyPacketCopy(nested)
		} else {
			copy[key] = value
		}
	}
	return copy
}
