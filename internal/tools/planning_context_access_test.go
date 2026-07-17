package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestPlanningContextAccessReceiptCannotBeForgedOrCopied(t *testing.T) {
	t.Run("finalize without novel_context", func(t *testing.T) {
		st := newPhaseTestStore(t)
		_, _ = installPlanningContextAccessProjectAll(t, st, 1, "missing-context-owner")
		err := consumePlanningContextAccessReceipt(
			st,
			1,
			domain.PlanningContextAccessPlan,
			nil,
		)
		if err == nil || !strings.Contains(err.Error(), "必须先成功调用") {
			t.Fatalf("missing context access was accepted: %v", err)
		}
	})

	t.Run("valid token copied from another workspace", func(t *testing.T) {
		source := newPhaseTestStore(t)
		_, _ = installPlanningContextAccessProjectAll(t, source, 1, "copied-token-owner")
		token := readPlanningContextAccessToken(t, source, 1, "world_simulation")

		target := newPhaseTestStore(t)
		_, _ = installPlanningContextAccessProjectAll(t, target, 1, "copied-token-owner")
		err := consumePlanningContextAccessReceipt(
			target,
			1,
			domain.PlanningContextAccessSimulate,
			[]string{token},
		)
		if err == nil || !strings.Contains(err.Error(), "必须先成功调用") {
			t.Fatalf("copied token without server receipt was accepted: %v", err)
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("opaque access token leaked through error: %v", err)
		}
	})
}

func TestPlanningContextAccessReceiptIsPhaseBoundAndSingleUse(t *testing.T) {
	st := newPhaseTestStore(t)
	planningContext, _ := installPlanningContextAccessProjectAll(t, st, 1, "phase-bound-owner")
	staleSimulateToken := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	simulateToken := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	simulateReceipt, err := st.Runtime.LoadPlanningContextAccessReceipt(
		domain.PlanningContextAccessSimulate,
	)
	if err != nil || simulateReceipt == nil {
		t.Fatalf("load simulate receipt: receipt=%+v err=%v", simulateReceipt, err)
	}
	lock, err := st.Runtime.LoadPipelineExecution()
	if err != nil || lock == nil {
		t.Fatalf("load project-all lock: lock=%+v err=%v", lock, err)
	}
	if simulateReceipt.GenerationID != planningContext.GenerationID ||
		simulateReceipt.Chapter != 1 ||
		simulateReceipt.Profile != "world_simulation" ||
		simulateReceipt.PlanningContextDigest != planningContext.ContextDigest ||
		simulateReceipt.Phase != domain.PlanningContextAccessSimulate ||
		simulateReceipt.LockOwner != lock.Owner ||
		!simulateReceipt.LockAcquiredAt.Equal(lock.AcquiredAt) {
		t.Fatalf("server receipt did not bind exact context/execution identity: %+v", simulateReceipt)
	}

	err = consumePlanningContextAccessReceipt(
		st,
		1,
		domain.PlanningContextAccessPlan,
		[]string{simulateToken},
	)
	if err == nil {
		t.Fatal("world_simulation access token was reused by plan finalize")
	}
	if strings.Contains(err.Error(), simulateToken) {
		t.Fatalf("cross-stage error leaked token: %v", err)
	}
	if err := consumePlanningContextAccessReceipt(
		st,
		1,
		domain.PlanningContextAccessSimulate,
		[]string{staleSimulateToken, simulateToken},
	); err != nil {
		t.Fatalf("latest simulate receipt was rejected when stale batch token remained: %v", err)
	}
	if err := consumePlanningContextAccessReceipt(
		st,
		1,
		domain.PlanningContextAccessSimulate,
		[]string{simulateToken},
	); err == nil || !strings.Contains(err.Error(), "已被对应 finalize 消费") {
		t.Fatalf("consumed receipt was reused: %v", err)
	}

	planToken := readPlanningContextAccessToken(t, st, 1, "planning")
	if err := consumePlanningContextAccessReceipt(
		st,
		1,
		domain.PlanningContextAccessPlan,
		[]string{planToken},
	); err != nil {
		t.Fatalf("exact plan receipt was rejected: %v", err)
	}
}

func TestPlanningContextAccessReceiptRejectsExpiredAndWrongChapter(t *testing.T) {
	st := newPhaseTestStore(t)
	_, _ = installPlanningContextAccessProjectAll(t, st, 1, "expiry-owner")
	token := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	receipt, err := st.Runtime.LoadPlanningContextAccessReceipt(
		domain.PlanningContextAccessSimulate,
	)
	if err != nil || receipt == nil {
		t.Fatalf("load issued receipt: receipt=%+v err=%v", receipt, err)
	}

	expired := *receipt
	expired.IssuedAt = time.Now().UTC().Add(-2 * time.Hour)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Hour)
	expired.ReceiptDigest, err = domain.ComputePlanningContextAccessReceiptDigest(expired)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.SavePlanningContextAccessReceipt(expired); err != nil {
		t.Fatal(err)
	}
	err = st.Runtime.ConsumePlanningContextAccessReceipt(
		expired,
		token,
		time.Now().UTC(),
	)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired receipt was accepted: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("expiry error leaked token: %v", err)
	}

	// Re-issue a live receipt, then ask a different chapter to consume it.
	token = readPlanningContextAccessToken(t, st, 1, "world_simulation")
	err = consumePlanningContextAccessReceipt(
		st,
		2,
		domain.PlanningContextAccessSimulate,
		[]string{token},
	)
	if err == nil || !strings.Contains(err.Error(), "章号不一致") {
		t.Fatalf("wrong chapter consumed receipt: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("wrong-chapter error leaked token: %v", err)
	}
}

func TestSimulateFinalizeConsumesSuccessfulWorldSimulationContextReceipt(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	_, projectAllToken := installPlanningContextAccessProjectAll(
		t,
		st,
		1,
		"simulate-finalize-owner",
	)
	tool := NewSimulateChapterWorldTool(st)
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"亲戚追问把失业事实推到桌面"},
		HiddenPressures:   []string{"沈知遥的工作安排尚未传到林澈"},
		AvailableOptions:  []string{"继续隐瞒", "承认失业"},
		ChosenDecision:    "在饭桌承认失业",
		DecisionReason:    "继续隐瞒会让家人按错误信息安排明天",
		PlanConstraints:   []string{"不能提前知道沈知遥的离屏行动"},
		CausalChain:       []string{"亲戚追问", "父母护短", "物证压缩退路", "林澈承认失业"},
	}
	first, _ := json.Marshal(map[string]any{
		"chapter":     1,
		"time_window": "同一天晚饭前后两小时",
		"character_decisions": []domain.CharacterWorldDecision{
			simulatedDecision("林澈", "在饭桌承认失业", true),
			simulatedDecision("沈知遥", "继续完成夜市检查准备", false),
		},
		"protagonist_projection": projection,
		"sources": []string{
			projectAllToken,
			"character_dossiers",
			"current_chapter_outline",
		},
		"finalize": true,
	})
	if _, err := tool.Execute(context.Background(), first); err == nil ||
		!strings.Contains(err.Error(), "novel_context(world_simulation)") {
		t.Fatalf("simulation finalized without successful context access: %v", err)
	}

	accessToken := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	patch, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"sources": []string{accessToken},
	})
	if _, err := tool.Execute(context.Background(), patch); err != nil {
		t.Fatalf("persist access token into simulation partial: %v", err)
	}
	if _, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"finalize":true}`),
	); err != nil {
		t.Fatalf("simulation rejected exact server access receipt: %v", err)
	}
	consumed, err := st.Runtime.LoadPlanningContextAccessReceipt(
		domain.PlanningContextAccessSimulate,
	)
	if err != nil || consumed == nil || consumed.ConsumedAt.IsZero() {
		t.Fatalf("simulate finalize did not consume receipt: receipt=%+v err=%v", consumed, err)
	}
	if _, err := tool.Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1}`),
	); err == nil || !strings.Contains(err.Error(), "已被对应 finalize 消费") {
		t.Fatalf("finalized simulation was reused without a fresh context read: %v", err)
	}
	reuseToken := readPlanningContextAccessToken(t, st, 1, "world_simulation")
	reuseArgs, _ := json.Marshal(map[string]any{
		"chapter": 1,
		"sources": []string{reuseToken},
	})
	reusedRaw, err := tool.Execute(context.Background(), reuseArgs)
	if err != nil {
		t.Fatalf("finalized simulation rejected fresh reuse receipt: %v", err)
	}
	var reused map[string]any
	if err := json.Unmarshal(reusedRaw, &reused); err != nil || reused["reused"] != true {
		t.Fatalf("expected receipt-bound simulation reuse, got %s err=%v", reusedRaw, err)
	}
}

func TestPlanFinalizeConsumesSuccessfulPlanningContextReceipt(t *testing.T) {
	st := newPhaseTestStore(t)
	_, projectAllToken := installPlanningContextAccessProjectAll(
		t,
		st,
		1,
		"plan-finalize-owner",
	)
	tool := NewPlanChapterTool(st)
	var args map[string]any
	if err := json.Unmarshal(planArgs(1), &args); err != nil {
		t.Fatal(err)
	}
	causal, _ := args["causal_simulation"].(map[string]any)
	causal["render_capacity"] = toolsTestRenderCapacity(700, 700, 700)
	causal["arc_transition_contract"] = map[string]any{
		"outgoing_consequence_id":   "context-access-chapter-1",
		"outgoing_consequence_text": "本章选择留下下一章必须处理的具体后果",
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), raw); err == nil ||
		!strings.Contains(err.Error(), "novel_context(planning)") {
		t.Fatalf("plan finalized without successful planning context access: %v", err)
	}

	accessToken := readPlanningContextAccessToken(t, st, 1, "planning")
	sources := stringSliceFromAny(causal["context_sources"])
	causal["context_sources"] = append(sources, projectAllToken, accessToken)
	raw, err = json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatalf("plan rejected exact server access receipt: %v", err)
	}
	consumed, err := st.Runtime.LoadPlanningContextAccessReceipt(
		domain.PlanningContextAccessPlan,
	)
	if err != nil || consumed == nil || consumed.ConsumedAt.IsZero() {
		t.Fatalf("plan finalize did not consume receipt: receipt=%+v err=%v", consumed, err)
	}
}

func installPlanningContextAccessProjectAll(
	t *testing.T,
	st *store.Store,
	chapter int,
	owner string,
) (domain.ProjectedPlanningContextV2, string) {
	t.Helper()
	if err := st.UserRules.Save(&rules.Snapshot{
		Version: rules.SnapshotVersion,
		Status:  rules.StatusReady,
		Structured: rules.Structured{
			ChapterWords: &rules.WordRange{Min: 2000, Max: 3300},
		},
	}); err != nil {
		t.Fatal(err)
	}
	stateRoot, err := domain.DeterministicPlanningHash("context-access-state")
	if err != nil {
		t.Fatal(err)
	}
	stateRoot = domain.PlanningV2DigestPrefix + stateRoot
	planningContext := domain.ProjectedPlanningContextV2{
		Version:        domain.ProjectedPlanningContextV2Version,
		GenerationID:   "pg2_context_access_test",
		NextChapter:    chapter,
		ThroughChapter: chapter - 1,
		StateRoot:      stateRoot,
	}
	planningContext.ContextDigest, err =
		domain.ComputeProjectedPlanningContextV2Digest(planningContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateProjectedPlanningContextV2(planningContext); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(planningContext)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(projectAllStateContextPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(projectAllAuthorityWorkspaceManifest{
		Version:                "project-all-workspace.v3",
		GenerationID:           planningContext.GenerationID,
		SourceOutput:           st.Dir(),
		BaseChapter:            chapter - 1,
		Workspace:              st.Dir(),
		IsolatedWrites:         true,
		FoundationSnapshotRoot: stateRoot,
		RAGSnapshotRoot:        stateRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "project_all_workspace_manifest.json"),
		manifestRaw,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionProjectAll,
		TargetChapter: chapter,
		Owner:         owner,
		ExpiresAt:     time.Now().UTC().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	token, err := domain.ProjectedPlanningContextSourceTokenV2(
		planningContext.ContextDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	return planningContext, token
}

func readPlanningContextAccessToken(
	t *testing.T,
	st *store.Store,
	chapter int,
	profile string,
) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"profile": profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "")
	if tool.ReadOnly(args) || tool.ConcurrencySafe(args) {
		t.Fatal("phase context that issues a server receipt must be serialized as a write")
	}
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("novel_context(%s): %v", profile, err)
	}
	var payload struct {
		Receipt struct {
			SourceToken string `json:"source_token"`
		} `json:"planning_context_access_receipt"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, err := domain.PlanningContextAccessTokenSHA256(payload.Receipt.SourceToken); err != nil {
		t.Fatalf("novel_context returned invalid access token: %v", err)
	}
	return payload.Receipt.SourceToken
}
