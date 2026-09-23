package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestPlannerProjectionOverflowExactTransportOffline(t *testing.T) {
	want := domain.ProtagonistDecisionProjection{
		Protagonist: "甲", ChosenDecision: "核验原件", DecisionReason: "尚未获知核验结果",
		AvailableOptions: []string{"核验原件", "等待"}, HiddenPressures: []string{"不能泄露未收到的答复"},
		PlanConstraints: []string{"保留不同周期的不同状态"}, CausalChain: []string{"收到原件", "决定核验"},
	}
	for i := range 40 {
		want.ObservableEffects = append(want.ObservableEffects, fmt.Sprintf("事件%02d：%s末尾%02d。", i, strings.Repeat("相同已知材料，保留原文。\n", 160), i))
	}
	plain, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) <= 160*1024 {
		t.Fatal("fixture did not exercise original overflow size")
	}
	binding := modelinput.PlanningProjectionSourceBinding{SimulationID: "offline-sim", SimulationDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("offline-fixture")))}
	encoded, changed, err := modelinput.EncodePlanningProjectionModelViewV1(plain, binding)
	if err != nil || !changed {
		t.Fatalf("fixture not encoded: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"chapter_world_simulation": map[string]any{"simulation_id": binding.SimulationID, "protagonist_projection": json.RawMessage(encoded)}})
	if err != nil || len(raw) > 160*1024 {
		t.Fatalf("encoding/budget error: %v bytes=%d", err, len(raw))
	}
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	assertPlannerProjectionPacketAtCLI(t, st, assets.Load("default"), raw, binding, want)
}

// The real source is read only. All leases and access receipts are created in
// a fresh disposable copy; the final CLI and final tool response are fake.
// This test proves transport and source binding, not prose/plan acceptance.
func TestActualPlannerProjectionOverflowReachesDirectPlannerCLI(t *testing.T) {
	source := os.Getenv("NOVEL_ACTUAL_SHADOW_READONLY")
	if source == "" {
		t.Skip("requires explicit read-only real shadow source")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	sim, err := st.LoadChapterWorldSimulation(2)
	if err != nil || sim == nil {
		t.Fatalf("load real C2 simulation: %v", err)
	}
	if len(sim.ProtagonistProjection.ObservableEffects) != 40 {
		t.Fatalf("expected real 40-effect regression, got %d", len(sim.ProtagonistProjection.ObservableEffects))
	}
	before, err := json.Marshal(sim)
	if err != nil {
		t.Fatal(err)
	}
	simHash, err := domain.DeterministicPlanningHash(*sim)
	if err != nil {
		t.Fatal(err)
	}
	binding := modelinput.PlanningProjectionSourceBinding{SimulationID: sim.SimulationID, SimulationDigest: "sha256:" + simHash}
	const owner = "planner-projection-offline-transport"
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionProjectAll, TargetChapter: 2, Owner: owner, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Runtime.ReleasePipelineExecution(owner); err != nil {
			t.Error(err)
		}
	})
	state, _, err := tools.LoadProjectAllStateForExecution(st, 2)
	if err != nil || state == nil {
		t.Fatalf("current planning state: %v", err)
	}
	bundle := assets.Load("default")
	contextTool := tools.NewContextTool(st, bundle.References, "default")
	raw, err := contextTool.Execute(t.Context(), json.RawMessage(`{"chapter":2,"profile":"planning"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 160*1024 {
		t.Fatalf("planning limit raised: %d", len(raw))
	}
	var payload struct {
		World struct {
			SimulationID string          `json:"simulation_id"`
			Projection   json.RawMessage `json:"protagonist_projection"`
		} `json:"chapter_world_simulation"`
		Access struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.World.SimulationID != sim.SimulationID {
		t.Fatal("simulation identity lost")
	}
	if !bytes.Contains(payload.World.Projection, []byte(modelinput.PlanningProjectionModelViewPolicyV1)) {
		t.Fatal("real oversized fixture did not use fallback")
	}
	decoded, err := modelinput.DecodePlanningProjectionModelViewV1(payload.World.Projection, binding)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.ProtagonistDecisionProjection
	if err := json.Unmarshal(decoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, sim.ProtagonistProjection) {
		t.Fatal("projection fields, 40 effects, ordering or exact text changed")
	}
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(domain.PlanningContextAccessPlan)
	if err != nil || receipt == nil || receipt.PlanningContextDigest != state.ContextDigest || receipt.GenerationID != sim.GenerationID || receipt.Chapter != 2 || receipt.LockOwner != owner {
		t.Fatalf("access binding invalid: %v", err)
	}
	if err := st.Runtime.ConsumePlanningContextAccessReceipt(*receipt, payload.Access.SourceToken, time.Now().UTC()); err != nil {
		t.Fatalf("real access token cannot be consumed: %v", err)
	}
	assertPlannerProjectionPacketAtCLI(t, st, bundle, raw, binding, sim.ProtagonistProjection)
	afterSim, err := st.LoadChapterWorldSimulation(2)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(afterSim)
	if !bytes.Equal(before, after) {
		t.Fatal("transport rewrote source simulation")
	}
	t.Logf("real_context_bytes=%d complete_projection_bytes=%d effects=%d generation=%s", len(raw), len(decoded), len(restored.ObservableEffects), sim.GenerationID)
}

func assertPlannerProjectionPacketAtCLI(t *testing.T, st *store.Store, bundle assets.Bundle, raw json.RawMessage, binding modelinput.PlanningProjectionSourceBinding, want domain.ProtagonistDecisionProjection) {
	t.Helper()
	binary, captures := outlineAllExactFakeCLI(t)
	t.Setenv("OUTLINE_EXACT_FIXTURE_RESPONSE", `{"action":"tool_call","tool_name":"plan_details","arguments_json":"{}","text":null}`)
	realTool := tools.NewPlanDetailsTool(st)
	calls := 0
	tool := agentcore.NewFuncTool(realTool.Name(), realTool.Description(), realTool.Schema(), func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"planned":true}`), nil
	})
	prompt := "Host 已预取完整规划上下文：\n<host_prefetched_novel_context>\n" + string(raw) + "\n</host_prefetched_novel_context>\n只提交当前章节计划，不生成正文。"
	system := bundle.Prompts.Planner + projectAllPlannerBoundary + projectAllActivationPlannerBoundary
	model := llmcodex.New(binary, "planner-projection-offline", "", llmcodex.WithContextWindow(272000))
	err := runProjectAllPlannerLoop(t.Context(), 2, prompt, agentcore.AgentContext{SystemPrompt: system, Tools: []agentcore.Tool{tool}}, agentcore.LoopConfig{Model: model, MaxTurns: 2, CacheLastMessage: promptCacheControl})
	if err != nil || calls != 1 {
		t.Fatalf("direct Planner loop failed or needed extra call: calls=%d err=%v", calls, err)
	}
	stdin, err := os.ReadFile(filepath.Join(captures, "prompt-1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(stdin), string(raw)) != 1 || !bytes.Contains(stdin, []byte(system)) || bytes.Contains(stdin, []byte("Codex 入参压缩")) {
		t.Fatal("actual CLI stdin dropped/duplicated context or cut instructions")
	}
	if _, err := os.Stat(filepath.Join(captures, "prompt-2")); !os.IsNotExist(err) {
		t.Fatal("unneeded second provider call")
	}
	start := bytes.Index(stdin, []byte("<host_prefetched_novel_context>\n")) + len("<host_prefetched_novel_context>\n")
	end := bytes.Index(stdin[start:], []byte("\n</host_prefetched_novel_context>"))
	if end < 0 {
		t.Fatal("missing complete context closing boundary")
	}
	var payload struct {
		World struct {
			Projection json.RawMessage `json:"protagonist_projection"`
		} `json:"chapter_world_simulation"`
	}
	if err := json.Unmarshal(stdin[start:start+end], &payload); err != nil {
		t.Fatal(err)
	}
	decoded, err := modelinput.DecodePlanningProjectionModelViewV1(payload.World.Projection, binding)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.ProtagonistDecisionProjection
	if err := json.Unmarshal(decoded, &restored); err != nil || !reflect.DeepEqual(restored, want) {
		t.Fatal("CLI received incomplete dictionary/instructions/source or changed projection")
	}
	schema, _ := json.Marshal(realTool.Schema())
	if !bytes.Contains(stdin, schema) {
		t.Fatal("real plan_details schema changed/truncated")
	}
	t.Logf("direct_planner_fake_cli_stdin=%d source=%s", len(stdin), fmt.Sprintf("%s/%s", binding.SimulationID, binding.SimulationDigest))
}
