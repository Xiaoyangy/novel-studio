package agents

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

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
