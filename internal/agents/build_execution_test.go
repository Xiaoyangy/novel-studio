package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
