package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host/flow"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const directRefreshWorldRules = `[{"category":"交易","rule":"登记后才可交接","boundary":"双方分别确认","visibility":"formal","character_view":"登记并经双方分别确认后才可交接。"}]`

// This fake replaces only provider I/O. The real agentcore child, source reader,
// SaveFoundation, observer and durable accounting remain in the execution path.
type directRefreshModel struct {
	role    string
	calls   atomic.Int32
	source  string
	mode    string
	entered chan struct{}
	release <-chan struct{}
}

func (*directRefreshModel) SupportsTools() bool { return true }

func (m *directRefreshModel) Generate(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	call := m.calls.Add(1)
	if call == 1 && m.entered != nil {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.role != "architect" {
		return nil, fmt.Errorf("400 Bad Request: forbidden %s provider dispatch", m.role)
	}
	if len(specs) != 2 || specs[0].Name != "novel_context" || specs[1].Name != "save_foundation" {
		return nil, fmt.Errorf("400 Bad Request: unexpected Architect capabilities: %+v", specs)
	}
	if m.mode == "failure" {
		return nil, fmt.Errorf("400 Bad Request: injected Architect provider failure")
	}
	if m.mode == "no_save" {
		return &agentcore.LLMResponse{Message: agentcore.Message{
			Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonStop,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("修订完成。")},
			Usage:   &agentcore.Usage{Provider: "offline", Model: "direct-refresh", Input: 10, Output: 2, Cost: &agentcore.Cost{Total: .01}},
		}}, nil
	}
	name, args := "novel_context", json.RawMessage(`{}`)
	if call > 1 {
		found := false
		for _, message := range messages {
			if message.Role != agentcore.RoleTool {
				continue
			}
			if message.Metadata["is_error"] == true {
				return nil, fmt.Errorf("400 Bad Request: real foundation tool rejected the request: %s", message.TextContent())
			}
			var packet struct {
				Version   string `json:"version"`
				Source    string `json:"source"`
				Content   string `json:"content"`
				Truncated bool   `json:"truncated"`
			}
			if json.Unmarshal([]byte(message.TextContent()), &packet) == nil && packet.Version == tools.FoundationSourceContextVersion {
				if packet.Source != "world_rules.json" || packet.Content != m.source || packet.Truncated {
					return nil, fmt.Errorf("400 Bad Request: real source tool returned the wrong source: %+v", packet)
				}
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("400 Bad Request: Architect did not receive exact source context")
		}
		if call != 2 {
			return nil, fmt.Errorf("400 Bad Request: Architect continued after its one successful save")
		}
		name = "save_foundation"
		args = json.RawMessage(`{"type":"world_rules","content":` + directRefreshWorldRules + `}`)
		if m.mode == "wrong_type" {
			args = json.RawMessage(`{"type":"premise","content":"不得覆盖的错误目标"}`)
		}
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: fmt.Sprintf("direct-refresh-%d", call), Name: name, Args: args})},
		Usage:   &agentcore.Usage{Provider: "offline", Model: "direct-refresh", Input: 10, Output: 2, Cost: &agentcore.Cost{Total: .01}},
	}}, nil
}

func (m *directRefreshModel) GenerateStream(ctx context.Context, messages []agentcore.Message, specs []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, specs, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message}
	close(events)
	return events, nil
}

func newDirectRefreshHost(t *testing.T, target string) (*Host, *directRefreshModel, *directRefreshModel, map[string]string) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"premise.md":           "# 交接\n林澄核验手续后完成交接。\n",
		"characters.json":      `[{"name":"林澄","role":"主角"}]`,
		"world_rules.json":     `[{"category":"交易","rule":"登记后才可交接","boundary":"双方分别确认","visibility":"formal","character_view":"登记后交接。"}]`,
		"book_world.json":      `{"name":"交接所"}`,
		"world_codex.json":     `{"version":1}`,
		"meta/compass.json":    `{"ending_direction":"完成交接","estimated_scale":"1-1卷，1-1章"}`,
		"layered_outline.json": `[{"index":1,"title":"交接","theme":"核验","arcs":[{"index":1,"title":"登记","goal":"完成交接","chapters":[{"chapter":1,"title":"核验","core_event":"登记交接","hook":"手续尚缺一项"}]}]}]`,
		"outline.json":         `[{"chapter":1,"title":"核验","core_event":"登记交接","hook":"手续尚缺一项"}]`,
	}
	for rel, content := range sources {
		if err := os.WriteFile(filepath.Join(st.Dir(), rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Progress.Save(&domain.Progress{NovelName: "交接", Phase: domain.PhaseWriting, CurrentChapter: 1, InProgressChapter: 1, TotalChapters: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierShort); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "direct-refresh-offline-test"}); err != nil {
		t.Fatal(err)
	}
	digest, err := tools.FoundationRefreshArtifactsDigest(st.Dir(), "world_rules")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendAlways(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("world_rules"), "world_rules.json", digest); err != nil {
		t.Fatal(err)
	}
	accounting, err := newHostProviderAccounting(st)
	if err != nil {
		t.Fatal(err)
	}
	parent := &directRefreshModel{role: "coordinator"}
	child := &directRefreshModel{role: "architect", source: sources["world_rules.json"]}
	models := &bootstrap.ModelSet{Default: bootstrap.NewSwappableModel("offline", "direct-refresh", parent)}
	models.SetAttemptDecorator(func(ctx context.Context, role, provider, name string, _ agentcore.ChatModel) agentcore.ChatModel {
		model := parent
		if role == "architect" {
			model = child
		}
		return accounting.decorate(ctx, role, provider, name, model)
	})
	cfg := bootstrap.Config{OutputDir: st.Dir(), Provider: "offline", ModelName: "direct-refresh", DisableLiveRAG: true,
		Roles: map[string]bootstrap.RoleConfig{"architect": {MaxTurns: 3}}}
	cfg.FillDefaults()
	bundle := assets.Load("default")
	var directDispatch agents.FoundationRefreshDispatch
	coordinator, askUser, restore, contextManager, thinking := agents.BuildCoordinatorWithOptions(cfg, st, models, bundle, accounting.record, nil, agents.CoordinatorBuildOptions{
		FoundationRefreshTarget: target, RecordFoundationRefreshEpoch: true, OneShotFoundationRefresh: true, DeferFoundationFinalization: true,
		OnFoundationRefreshReady: func(dispatch agents.FoundationRefreshDispatch) { directDispatch = dispatch },
	})
	h := &Host{
		cfg: cfg, bundle: bundle, store: st, models: models, coordinator: coordinator, coordinatorCtxMgr: contextManager,
		thinkingApplier: thinking, askUser: askUser, writerRestore: restore, usage: accounting.meter.Tracker(), usageAccounting: accounting,
		preserveCheckpointsOnStart: true, disableFlowRouter: true,
		foundationRefreshTarget: target, foundationRefreshDispatch: directDispatch,
		events: make(chan Event, 100), streamCh: make(chan string, 256), done: make(chan struct{}, 4), lifecycle: lifecycleIdle,
	}
	h.observer = newObserver(coordinator, st, h.emitEvent, h.emitDelta, h.emitClear)
	h.router = flow.NewDispatcher(coordinator, st)
	t.Cleanup(h.Close)
	return h, parent, child, sources
}

func waitDirectRefresh(t *testing.T, h *Host) error {
	t.Helper()
	finished := make(chan error, 1)
	go func() { finished <- h.WaitFoundationRefresh() }()
	select {
	case err := <-finished:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("offline direct foundation refresh did not finish")
		return nil
	}
}

func TestFoundationRefreshDirectArchitectBypassesCoordinator(t *testing.T) {
	h, coordinator, architect, sources := newDirectRefreshHost(t, "world_rules")
	before := h.store.Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("world_rules"))
	if err := h.StartFoundationRefresh("完整读取 world_rules 并仅补齐公开操作条件，原样保留其他规则。"); err != nil {
		t.Fatal(err)
	}
	if err := waitDirectRefresh(t, h); err != nil {
		t.Fatal(err)
	}
	if got := coordinator.calls.Load(); got != 0 {
		t.Fatalf("exact-target refresh invoked forbidden Coordinator provider %d time(s); want direct Architect dispatch", got)
	}
	if got := architect.calls.Load(); got != 2 {
		t.Fatalf("Architect calls = %d, want one exact-source read and one save", got)
	}
	for rel, original := range sources {
		raw, err := os.ReadFile(filepath.Join(h.Dir(), rel))
		if err != nil {
			t.Fatal(err)
		}
		if rel == "world_rules.json" {
			if bytes.Equal(raw, []byte(original)) || !bytes.Contains(raw, []byte("登记并经双方分别确认后才可交接。")) {
				t.Fatalf("target source was not refreshed: %s", raw)
			}
		} else if string(raw) != original {
			t.Errorf("refresh changed unrelated foundation source %s", rel)
		}
	}
	after := h.store.Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("world_rules"))
	digest, err := tools.FoundationRefreshArtifactsDigest(h.Dir(), "world_rules")
	if err != nil || after == nil || after.Seq <= before.Seq || after.Digest != digest || after.Digest == before.Digest {
		t.Fatalf("fresh epoch does not bind refreshed target: before=%+v after=%+v digest=%s err=%v", before, after, digest, err)
	}
	raw, err := os.ReadFile(filepath.Join(h.Dir(), store.UsageAuditPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"agent":"architect"`) || strings.Contains(string(raw), `"agent":"coordinator"`) {
		t.Fatalf("durable provider audit did not preserve Architect-only usage: %s", raw)
	}
	var epochs int
	for _, checkpoint := range h.store.Checkpoints.All() {
		if checkpoint.Step == tools.FoundationRefreshCheckpointStep("world_rules") {
			epochs++
		}
	}
	if epochs != 2 {
		t.Fatalf("refresh produced %d epochs, want old epoch plus exactly one new save", epochs)
	}
}

func TestFoundationRefreshDirectRejectsUnsuccessfulChildWithoutMutation(t *testing.T) {
	for _, mode := range []string{"failure", "no_save", "wrong_type"} {
		t.Run(mode, func(t *testing.T) {
			h, coordinator, architect, sources := newDirectRefreshHost(t, "world_rules")
			architect.mode = mode
			before := h.store.Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("world_rules"))
			if err := h.StartFoundationRefresh("只修订 world_rules 的公开操作条件。"); err != nil {
				t.Fatal(err)
			}
			err := waitDirectRefresh(t, h)
			if err == nil {
				t.Fatal("unsaved/failed child was reported as a successful refresh")
			}
			if mode == "failure" && (!strings.Contains(err.Error(), "injected Architect provider failure") || architect.calls.Load() != 1) {
				t.Fatalf("child failure was hidden or caused an extra provider dispatch: calls=%d err=%v", architect.calls.Load(), err)
			}
			if mode == "wrong_type" && !strings.Contains(err.Error(), "only allows") {
				t.Fatalf("wrong-target rejection was lost: %v", err)
			}
			if coordinator.calls.Load() != 0 || architect.calls.Load() == 0 {
				t.Fatalf("failure path rerouted to Coordinator: coordinator=%d architect=%d", coordinator.calls.Load(), architect.calls.Load())
			}
			for rel, original := range sources {
				raw, err := os.ReadFile(filepath.Join(h.Dir(), rel))
				if err != nil || string(raw) != original {
					t.Errorf("failed refresh mutated %s: %v", rel, err)
				}
			}
			after := h.store.Checkpoints.LatestByStep(domain.GlobalScope(), tools.FoundationRefreshCheckpointStep("world_rules"))
			if after == nil || after.Seq != before.Seq || after.Digest != before.Digest {
				t.Fatalf("unsaved child created a refresh epoch: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestFoundationRefreshDirectLeavesOrdinaryStartPreparedOnCoordinator(t *testing.T) {
	h, coordinator, architect, _ := newDirectRefreshHost(t, "")
	if err := h.StartPrepared("普通创作仍由 Coordinator 处理。"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary offline Coordinator did not finish")
	}
	if coordinator.calls.Load() != 1 || architect.calls.Load() != 0 {
		t.Fatalf("ordinary StartPrepared changed its routing: coordinator=%d architect=%d", coordinator.calls.Load(), architect.calls.Load())
	}
}

func TestFoundationRefreshDirectRejectsBeforeProviderDispatch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		target    string
		complete  bool
		wantError string
	}{
		{name: "complete_phase", target: "world_rules", complete: true, wantError: "phase=complete"},
		{name: "unknown_target", target: "unknown", wantError: "unsupported direct foundation refresh target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, coordinator, architect, protected := newDirectRefreshHost(t, tc.target)
			if tc.complete {
				progress, err := h.store.Progress.Load()
				if err != nil {
					t.Fatal(err)
				}
				progress.Phase = domain.PhaseComplete
				if err := h.store.Progress.Save(progress); err != nil {
					t.Fatal(err)
				}
			}
			for _, rel := range []string{"meta/checkpoints.jsonl", "meta/progress.json", store.UsageAuditPath} {
				raw, err := os.ReadFile(filepath.Join(h.Dir(), rel))
				if err != nil {
					t.Fatal(err)
				}
				protected[rel] = string(raw)
			}
			if err := h.StartFoundationRefresh("只修订指定来源的公开操作条件。"); err != nil {
				t.Fatalf("Host rejected before exercising the real dispatch gate: %v", err)
			}
			if err := waitDirectRefresh(t, h); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("direct dispatch lost its pre-provider refusal: want=%q got=%v", tc.wantError, err)
			}
			if coordinator.calls.Load() != 0 || architect.calls.Load() != 0 {
				t.Fatalf("rejected direct dispatch reached a provider: coordinator=%d architect=%d", coordinator.calls.Load(), architect.calls.Load())
			}
			for rel, original := range protected {
				raw, err := os.ReadFile(filepath.Join(h.Dir(), rel))
				if err != nil || string(raw) != original {
					t.Errorf("pre-provider refusal mutated %s: %v", rel, err)
				}
			}
		})
	}
}

func TestFoundationRefreshDirectPreservesOuterLeaseUntilOwnerReleases(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "abort"} {
		t.Run(outcome, func(t *testing.T) {
			h, coordinator, architect, _ := newDirectRefreshHost(t, "world_rules")
			leasePath := filepath.Join(h.Dir(), "meta/runtime/pipeline_execution.json")
			before, err := os.ReadFile(leasePath)
			if err != nil {
				t.Fatal(err)
			}
			assertLease := func(stage string) {
				t.Helper()
				after, err := os.ReadFile(leasePath)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("%s changed or released the outer execution lease: before=%s after=%s err=%v", stage, before, after, err)
				}
			}
			entered, release := make(chan struct{}), make(chan struct{})
			architect.entered, architect.release = entered, release
			if outcome == "failure" {
				architect.mode = "failure"
			}
			if err := h.StartFoundationRefresh("只修订 world_rules 的公开操作条件。"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("Architect did not enter its offline provider")
			}
			assertLease("active Architect dispatch")
			if outcome == "abort" {
				if !h.Abort() {
					t.Fatal("running direct Architect was not aborted")
				}
				assertLease("Abort")
			} else {
				close(release)
			}
			runErr := waitDirectRefresh(t, h)
			if (runErr == nil) != (outcome == "success") {
				t.Fatalf("unexpected %s completion: %v", outcome, runErr)
			}
			if coordinator.calls.Load() != 0 {
				t.Fatal("lease lifecycle dispatched a Coordinator provider")
			}
			assertLease("completed Architect dispatch")
			h.Close()
			assertLease("Host.Close")
			if err := h.store.Runtime.ReleasePipelineExecution("direct-refresh-offline-test"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
				t.Fatalf("outer owner did not release its lease: %v", err)
			}
		})
	}
}
