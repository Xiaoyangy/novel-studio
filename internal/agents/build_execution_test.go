package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestFreshConvergenceWriterSessionIdentityDoesNotReuseWriterTranscript(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(st.Dir(), "meta", "sessions", "agents", "writer-ch05.jsonl")
	if err := os.MkdirAll(filepath.Dir(oldPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const poisoned = "poisoned historical writer transcript\n"
	if err := os.WriteFile(oldPath, []byte(poisoned), 0o644); err != nil {
		t.Fatal(err)
	}

	identities := []string{
		"convergence_planner_fresh_ch05_a01_1111111111111111",
		"convergence_planner_fresh_ch05_a02_2222222222222222",
	}
	for _, identity := range identities {
		if err := ValidateSubAgentSessionIdentity(identity); err != nil {
			t.Fatal(err)
		}
		logicalUsageRole := ""
		onMessage := newSubAgentMessageHandler(st, nil, func(agentName string, _ agentcore.AgentMessage) {
			logicalUsageRole = agentName
		}, identity)
		onMessage("writer", "规划第 5 章", agentcore.UserMsg("fresh paid attempt"))
		path := filepath.Join(st.Dir(), "meta", "sessions", "agents", identity+"-ch05.jsonl")
		raw, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(raw), "fresh paid attempt") {
			t.Fatalf("fresh identity was not independently persisted: path=%s raw=%q err=%v", path, raw, err)
		}
		if logicalUsageRole != "writer" || agentToRole(identity) != "writer" {
			t.Fatalf("session-only identity changed logical/model role: usage=%q model=%q", logicalUsageRole, agentToRole(identity))
		}
	}
	if raw, err := os.ReadFile(oldPath); err != nil || string(raw) != poisoned {
		t.Fatalf("fresh attempts loaded/appended historical writer transcript: raw=%q err=%v", raw, err)
	}
	if identities[0] == identities[1] {
		t.Fatal("distinct paid dispatches reused one session identity")
	}

	// Empty override is the ordinary pipeline behavior and must stay unchanged.
	newSubAgentMessageHandler(st, nil, nil, "")(
		"writer", "规划第 6 章", agentcore.UserMsg("ordinary writer"),
	)
	if _, err := os.Stat(filepath.Join(st.Dir(), "meta", "sessions", "agents", "writer-ch06.jsonl")); err != nil {
		t.Fatalf("normal writer session routing changed: %v", err)
	}
}

func TestSingleSubagentModeGateRejectsSharedStateParallelism(t *testing.T) {
	gate := singleSubagentModeGate()
	for _, args := range []string{
		`{"tasks":[{"agent":"writer","task":"a"}]}`,
		`{"chain":[{"agent":"writer","task":"a"}]}`,
		`{"agent":"writer","task":"a","background":true}`,
		`{"agent":"writer","task":"a","team_name":"writers"}`,
	} {
		decision, err := gate(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{Name: "subagent", Args: json.RawMessage(args)}})
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || decision.Allowed {
			t.Fatalf("expected mode to be rejected: %s", args)
		}
	}
}

func TestSingleSubagentModeGateAllowsSynchronousSingleTask(t *testing.T) {
	decision, err := singleSubagentModeGate()(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent",
		Args: json.RawMessage(`{"agent":"writer","task":"写第 1 章"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if decision != nil {
		t.Fatalf("single task should pass through, got %+v", decision)
	}
}

func TestFoundationRefreshCoordinatorGateAllowsOnlyArchitectLong(t *testing.T) {
	gate := foundationRefreshCoordinatorGate("premise")
	for _, tc := range []struct {
		name    string
		args    string
		allowed bool
	}{
		{name: "subagent", args: `{"agent":"architect_long","task":"refresh premise"}`, allowed: true},
		{name: "subagent", args: `{"agent":"architect_short","task":"refresh premise"}`, allowed: false},
		{name: "subagent", args: `{"agent":"writer","task":"write"}`, allowed: false},
		{name: "novel_context", args: `{}`, allowed: false},
		{name: "save_user_rules", args: `{}`, allowed: false},
		{name: "reopen_book", args: `{}`, allowed: false},
	} {
		decision, err := gate(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{
			Name: tc.name, Args: json.RawMessage(tc.args),
		}})
		if err != nil {
			t.Fatal(err)
		}
		gotAllowed := decision == nil || decision.Allowed
		if gotAllowed != tc.allowed {
			t.Fatalf("tool=%s args=%s allowed=%v decision=%+v", tc.name, tc.args, gotAllowed, decision)
		}
	}
}

func TestFoundationRefreshStopsAfterFirstSuccessfulExactSave(t *testing.T) {
	result := json.RawMessage(`{"saved":true,"type":"premise","foundation_ready":true}`)
	if !foundationRefreshShouldStopAfterToolResult("premise", "save_foundation", result) {
		t.Fatal("exact successful foundation save did not stop the refresh subagent")
	}
	if foundationRefreshShouldStopAfterToolResult("characters", "save_foundation", result) ||
		foundationRefreshShouldStopAfterToolResult("premise", "novel_context", result) {
		t.Fatal("refresh stop accepted a different target or tool")
	}
}

func TestPipelineInitialWorldTickAgentGateAllowsOnlyCurrentContractArchitectLong(t *testing.T) {
	st, contract := initialWorldTickAgentGateFixture(t, "林照微只提交独立风险告知，许珩单方保留四十八小时窗口", "章末贺今棠的名字出现在公开委托候选名单")
	gate := pipelineInitialWorldTickAgentGate(st)
	partialTask := strings.Join([]string{
		contract.Marker,
		contract.CoreEvent,
		contract.Hook,
		tools.InitialWorldTickNoPreemptToken,
	}, "\n")
	for _, tc := range []struct {
		name    string
		tool    string
		agent   string
		task    string
		allowed bool
	}{
		{name: "exact contract", tool: "subagent", agent: "architect_long", task: contract.Block, allowed: true},
		{name: "contract inside instruction", tool: "subagent", agent: "architect_long", task: "只执行初始 world_tick：\n" + contract.Block, allowed: true},
		{name: "wrong agent", tool: "subagent", agent: "architect_short", task: contract.Block},
		{name: "writer", tool: "subagent", agent: "writer", task: contract.Block},
		{name: "missing task", tool: "subagent", agent: "architect_long"},
		{name: "token without full no-preempt block", tool: "subagent", agent: "architect_long", task: partialTask},
		{name: "non subagent", tool: "novel_context"},
		{name: "coordinator mutation", tool: "save_user_rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]string{"agent": tc.agent, "task": tc.task})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := gate(context.Background(), agentcore.GateRequest{
				Call: agentcore.ToolCall{Name: tc.tool, Args: args},
			})
			if err != nil {
				t.Fatal(err)
			}
			gotAllowed := decision == nil || decision.Allowed
			if gotAllowed != tc.allowed {
				t.Fatalf("allowed=%v, decision=%+v", gotAllowed, decision)
			}
			if !tc.allowed && (decision == nil || !strings.Contains(decision.Reason, contract.Block)) {
				t.Fatalf("rejection did not return the complete current retry contract: %+v", decision)
			}
		})
	}
}

func TestPipelineInitialWorldTickAgentGateRejectsTaskAfterAuthoritativeOutlineDrift(t *testing.T) {
	st, stale := initialWorldTickAgentGateFixture(t, "旧核心：林照微只发出风险告知", "旧钩子：名单尚未公开")
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	volumes[0].Arcs[0].Chapters[0].CoreEvent = "新核心：林照微完成独立取样后选择公开风险告知"
	volumes[0].Arcs[0].Chapters[0].Hook = "新钩子：贺今棠进入公开委托候选名单"
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	current, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	if current.Marker == stale.Marker {
		t.Fatal("outline drift did not change the signed dispatch marker")
	}

	gate := pipelineInitialWorldTickAgentGate(st)
	staleArgs, _ := json.Marshal(map[string]string{"agent": "architect_long", "task": stale.Block})
	decision, err := gate(context.Background(), agentcore.GateRequest{
		Call: agentcore.ToolCall{Name: "subagent", Args: staleArgs},
	})
	if err != nil || decision == nil || decision.Allowed {
		t.Fatalf("stale contract crossed the world_tick gate: decision=%+v err=%v", decision, err)
	}
	if !strings.Contains(decision.Reason, current.Block) {
		t.Fatalf("stale rejection did not return current contract: %s", decision.Reason)
	}

	currentArgs, _ := json.Marshal(map[string]string{"agent": "architect_long", "task": current.Block})
	decision, err = gate(context.Background(), agentcore.GateRequest{
		Call: agentcore.ToolCall{Name: "subagent", Args: currentArgs},
	})
	if err != nil || decision != nil && !decision.Allowed {
		t.Fatalf("current authoritative contract was rejected: decision=%+v err=%v", decision, err)
	}
}

func TestPipelineInitialWorldTickAgentGateDoesNotAffectOtherExecutionModes(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "non-world-tick-agent-gate-test",
	}); err != nil {
		t.Fatal(err)
	}
	gate := pipelineInitialWorldTickAgentGate(st)
	for _, request := range []agentcore.GateRequest{
		{Call: agentcore.ToolCall{Name: "reopen_book", Args: json.RawMessage(`{}`)}},
		{Call: agentcore.ToolCall{Name: "subagent", Args: json.RawMessage(`{"agent":"writer","task":"写第1章"}`)}},
	} {
		decision, err := gate(context.Background(), request)
		if err != nil || decision != nil {
			t.Fatalf("non-world_tick mode was affected: decision=%+v err=%v", decision, err)
		}
	}
}

func initialWorldTickAgentGateFixture(t *testing.T, coreEvent, hook string) (*store.Store, tools.InitialWorldTickDispatchContract) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Chapters: []domain.OutlineEntry{{
				Title: "公开窗口", CoreEvent: coreEvent, Hook: hook,
			}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionWorldTick, TargetChapter: 1, Owner: "initial-world-tick-agent-gate-test",
	}); err != nil {
		t.Fatal(err)
	}
	contract, err := tools.BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		t.Fatal(err)
	}
	return st, contract
}

func TestFrozenRenderToolInventoryRejectsDynamicResearch(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	filtered := frozenRenderTools([]agentcore.Tool{
		tools.NewReadChapterTool(st),
		tools.NewCraftRecallTool(st),
		tools.NewWebResearchTool(st),
		tools.NewDraftChapterTool(st),
	})
	var names []string
	for _, tool := range filtered {
		names = append(names, tool.Name())
	}
	if slices.Contains(names, "craft_recall") || slices.Contains(names, "web_research") {
		t.Fatalf("frozen prose retained live research capabilities: %v", names)
	}
	for _, required := range []string{"read_chapter", "draft_chapter"} {
		if !slices.Contains(names, required) {
			t.Fatalf("frozen prose lost required tool %q: %v", required, names)
		}
	}
}

func TestPipelineRenderAgentGateAllowsOnlyTargetDrafter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 6,
		PlanDigest:    "sha256:frozen",
		Owner:         "render-agent-gate-test",
	}); err != nil {
		t.Fatal(err)
	}
	gate := pipelineRenderAgentGate(st)
	for _, tc := range []struct {
		args    string
		allowed bool
	}{
		{`{"agent":"drafter","task":"渲染第6章"}`, true},
		{`{"agent":"draft_finalizer","task":"验收第 6 章"}`, true},
		{`{"agent":"writer","task":"写第6章"}`, false},
		{`{"agent":"world_simulator","task":"推演第6章"}`, false},
		{`{"agent":"architect_long","task":"重做第6章"}`, false},
		{`{"agent":"drafter","task":"渲染第7章"}`, false},
		{`{"agent":"drafter","task":"渲染当前章"}`, false},
	} {
		decision, err := gate(context.Background(), agentcore.GateRequest{
			Call: agentcore.ToolCall{Name: "subagent", Args: json.RawMessage(tc.args)},
		})
		if err != nil {
			t.Fatal(err)
		}
		gotAllowed := decision == nil || decision.Allowed
		if gotAllowed != tc.allowed {
			t.Fatalf("args=%s allowed=%v decision=%+v", tc.args, gotAllowed, decision)
		}
	}
}

func TestPipelineRenderProsePermitGateBlocksMisdirectedDrafterButNotFinalizer(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 6,
		PlanDigest: "sha256:frozen", Owner: "render-permit-routing-test",
	}); err != nil {
		t.Fatal(err)
	}
	gate := pipelineRenderProsePermitGate(st)
	drafter, err := gate(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent", Args: json.RawMessage(`{"agent":"drafter","task":"渲染第6章"}`),
	}})
	if err != nil || drafter == nil || drafter.Allowed {
		t.Fatalf("zero-reservation Drafter crossed provider boundary: decision=%+v err=%v", drafter, err)
	}
	finalizer, err := gate(context.Background(), agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent", Args: json.RawMessage(`{"agent":"draft_finalizer","task":"验收第6章"}`),
	}})
	if err != nil || finalizer != nil && !finalizer.Allowed {
		t.Fatalf("draft_finalizer incorrectly required prose permit: decision=%+v err=%v", finalizer, err)
	}
}

func TestPipelineRenderProsePermitConsumesOnlyAfterAllOtherGatesApprove(t *testing.T) {
	st, ledgerPath := renderAgentPermitFixture(t)
	request := agentcore.GateRequest{Call: agentcore.ToolCall{
		Name: "subagent", Args: json.RawMessage(`{"agent":"drafter","task":"渲染第1章"}`),
	}}
	reject := func(context.Context, agentcore.GateRequest) (*agentcore.GateDecision, error) {
		return &agentcore.GateDecision{Allowed: false, Reason: "later business gate rejected"}, nil
	}
	decision, err := combineToolGates(
		pipelineRenderAgentGate(st),
		reject,
		pipelineRenderProsePermitGate(st),
	)(context.Background(), request)
	if err != nil || decision == nil || decision.Allowed {
		t.Fatalf("later gate did not reject: decision=%+v err=%v", decision, err)
	}
	ledger := loadRenderAgentPermitLedger(t, ledgerPath)
	if got := ledger.Reservations[0].Status; got != "permit_armed" {
		t.Fatalf("rejected downstream gate consumed provider dispatch: status=%q", got)
	}
	decision, err = pipelineRenderProsePermitGate(st)(context.Background(), request)
	if err != nil || decision != nil && !decision.Allowed {
		t.Fatalf("approved Drafter did not validate permit: decision=%+v err=%v", decision, err)
	}
	ledger = loadRenderAgentPermitLedger(t, ledgerPath)
	if got := ledger.Reservations[0].Status; got != "permit_armed" {
		t.Fatalf("Coordinator gate consumed provider evidence before priming: status=%q", got)
	}
	if err := st.Runtime.ConsumePipelineRenderProsePermit(1); err != nil {
		t.Fatalf("provider-side consume failed: %v", err)
	}
	ledger = loadRenderAgentPermitLedger(t, ledgerPath)
	if got := ledger.Reservations[0].Status; got != "provider_dispatched" {
		t.Fatalf("provider boundary did not mark dispatch: status=%q", got)
	}
	decision, err = pipelineRenderProsePermitGate(st)(context.Background(), request)
	if err != nil || decision == nil || decision.Allowed {
		t.Fatalf("single permit authorized more than one Drafter: decision=%+v err=%v", decision, err)
	}
}

func renderAgentPermitFixture(t *testing.T) (*store.Store, string) {
	t.Helper()
	base := t.TempDir()
	live := filepath.Join(base, "output")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateID := "render-ch0001-agent-permit"
	candidate := filepath.Join(base, ".render-candidates", candidateID, "output")
	st := store.NewStore(candidate)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	digest := func(ch string) string { return "sha256:" + strings.Repeat(ch, 64) }
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 1,
		PlanDigest: digest("a"), Owner: "render-agent-permit-owner",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	manifestRaw, _ := json.Marshal(map[string]any{
		"version": "pipeline-render-candidate.v2", "candidate_id": candidateID,
		"generation_id": "pg2_agent_permit", "chapter": 1,
		"plan_digest": digest("a"), "plan_checkpoint_seq": 1,
		"projected_bundle_digest": digest("c"), "promotion_receipt_digest": digest("d"),
		"source_output_dir": live,
	})
	manifestPath := filepath.Join(candidate, "meta", "planning", "render_candidate.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	authorization := digest("b")
	ledger := domain.PipelineRenderDispatchLedger{
		Version: domain.PipelineRenderDispatchLedgerVersion, CandidateID: candidateID,
		SourceOutputDir: live,
		GenerationID:    "pg2_agent_permit", Chapter: 1, PlanDigest: digest("a"),
		PlanCheckpointSeq: 1, ProjectedBundleDigest: digest("c"), PromotionReceiptDigest: digest("d"),
		Limit: domain.PipelineRenderWholeBodyDispatchLimit, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Reservations: []domain.PipelineRenderDispatchReservation{{
			AuthorizationDigest: authorization, Attempt: 1, Status: "reserved",
			ReservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}},
	}
	ledgerPath := filepath.Join(base, ".render-candidates", "convergence", candidateID, "dispatch_budget.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(ledger, "", "  ")
	if err := os.WriteFile(ledgerPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorization, 1); err != nil {
		t.Fatal(err)
	}
	return st, ledgerPath
}

func loadRenderAgentPermitLedger(t *testing.T, path string) domain.PipelineRenderDispatchLedger {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ledger domain.PipelineRenderDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func TestPipelineRenderDrafterStopsOnlyAfterExactSuccessfulWholeDraft(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode: domain.PipelineExecutionRender, TargetChapter: 5,
		PlanDigest: "sha256:frozen", Owner: "render-drafter-stop-test",
	}); err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"written":true,"chapter":5,"mode":"write"}`)
	if !pipelineRenderDrafterShouldStopAfterToolResult(st, "draft_chapter", valid) {
		t.Fatal("exact successful whole draft did not stop frozen Drafter")
	}
	for _, tc := range []struct {
		name   string
		tool   string
		result string
	}{
		{name: "wrong tool", tool: "check_consistency", result: string(valid)},
		{name: "wrong chapter", tool: "draft_chapter", result: `{"written":true,"chapter":4,"mode":"write"}`},
		{name: "append", tool: "draft_chapter", result: `{"written":true,"chapter":5,"mode":"append"}`},
		{name: "not written", tool: "draft_chapter", result: `{"written":false,"chapter":5,"mode":"write"}`},
		{name: "malformed", tool: "draft_chapter", result: `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if pipelineRenderDrafterShouldStopAfterToolResult(st, tc.tool, json.RawMessage(tc.result)) {
				t.Fatal("non-exact/non-success result stopped Drafter")
			}
		})
	}

	ordinary := store.NewStore(t.TempDir())
	if err := ordinary.Init(); err != nil {
		t.Fatal(err)
	}
	if pipelineRenderDrafterShouldStopAfterToolResult(ordinary, "draft_chapter", valid) {
		t.Fatal("ordinary non-pipeline Drafter inherited frozen-render stop semantics")
	}
}
