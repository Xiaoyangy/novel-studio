package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

type renderPrimingCaptureModel struct {
	mu       sync.Mutex
	calls    int
	messages []agentcore.Message
}

func (m *renderPrimingCaptureModel) Generate(
	_ context.Context,
	messages []agentcore.Message,
	_ []agentcore.ToolSpec,
	_ ...agentcore.CallOption,
) (*agentcore.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	m.messages = append([]agentcore.Message(nil), messages...)
	m.mu.Unlock()
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID:   "direct-draft",
			Name: "draft_chapter",
			Args: json.RawMessage(`{"chapter":1,"content":"正文"}`),
		})},
	}}, nil
}

func (m *renderPrimingCaptureModel) GenerateStream(
	ctx context.Context,
	messages []agentcore.Message,
	tools []agentcore.ToolSpec,
	opts ...agentcore.CallOption,
) (<-chan agentcore.StreamEvent, error) {
	response, err := m.Generate(ctx, messages, tools, opts...)
	if err != nil {
		return nil, err
	}
	events := make(chan agentcore.StreamEvent, 1)
	events <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: response.Message}
	close(events)
	return events, nil
}

func (*renderPrimingCaptureModel) SupportsTools() bool { return true }

func (m *renderPrimingCaptureModel) snapshot() (int, []agentcore.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, append([]agentcore.Message(nil), m.messages...)
}

func TestRenderContextPrimingMakesContractVisibleBeforeDirectDraftCall(t *testing.T) {
	st, contextTool, ledgerPath := newRenderContextPrimingFixture(t, true)
	provider := &renderPrimingCaptureModel{}
	model := withRenderContextPriming(provider, st, contextTool)
	original := []agentcore.Message{{
		Role:    agentcore.RoleUser,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("不要先调用 novel_context，直接 draft_chapter")},
	}}
	if _, err := model.Generate(context.Background(), original, nil); err != nil {
		t.Fatal(err)
	}
	calls, first := provider.snapshot()
	if calls != 1 {
		t.Fatalf("provider calls=%d, want 1", calls)
	}
	if len(original) != 1 {
		t.Fatal("priming mutated persisted input messages")
	}
	payload, identity := requireSingleRenderPrimingEnvelope(t, first)
	if identity.Chapter != 1 || identity.ProtocolVersion != aigc.ProseRenderPrimingProtocolVersion {
		t.Fatalf("wrong priming identity: %+v", identity)
	}
	if err := validatePrimedRenderContext(payload, 1); err != nil {
		t.Fatalf("first provider call did not receive complete contract: %v\npayload=%s", err, payload)
	}
	if got := loadRenderAgentPermitLedger(t, ledgerPath).Reservations[0].Status; got != "provider_dispatched" {
		t.Fatalf("successful priming did not consume provider permit: status=%q", got)
	}

	// The same Drafter session cannot cross the provider boundary twice. The
	// previous envelope is safely replaceable, but the one-shot permit remains
	// the final authority.
	if _, err := model.Generate(context.Background(), first, nil); err == nil {
		t.Fatal("second provider call reused a consumed prose permit")
	}
	calls, second := provider.snapshot()
	if calls != 1 {
		t.Fatalf("provider calls=%d, want exactly 1", calls)
	}
	requireSingleRenderPrimingEnvelope(t, second)
}

func TestRenderContextPrimingLoadFailureCallsNoProvider(t *testing.T) {
	st, contextTool, ledgerPath := newRenderContextPrimingFixture(t, false)
	provider := &renderPrimingCaptureModel{}
	model := withRenderContextPriming(provider, st, contextTool)
	if _, err := model.Generate(context.Background(), []agentcore.Message{{
		Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("直接写正文")},
	}}, nil); err == nil {
		t.Fatal("missing frozen context did not fail closed")
	}
	if calls, messages := provider.snapshot(); calls != 0 || len(messages) != 0 {
		t.Fatalf("provider was called after priming failure: calls=%d messages=%d", calls, len(messages))
	}
	if got := loadRenderAgentPermitLedger(t, ledgerPath).Reservations[0].Status; got != "permit_armed" {
		t.Fatalf("priming failure consumed provider permit: status=%q", got)
	}
}

func TestRenderContextPrimingEvidenceSymlinkCallsNoProvider(t *testing.T) {
	st, contextTool, ledgerPath := newRenderContextPrimingFixture(t, true)
	external := filepath.Join(t.TempDir(), "characters.json")
	if err := os.WriteFile(external, []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(st.Dir(), "characters.json")); err != nil {
		t.Fatal(err)
	}
	provider := &renderPrimingCaptureModel{}
	model := withRenderContextPriming(provider, st, contextTool)
	if _, err := model.Generate(context.Background(), []agentcore.Message{{
		Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("直接写正文")},
	}}, nil); err == nil {
		t.Fatal("symlinked candidate evidence reached render provider")
	}
	if calls, messages := provider.snapshot(); calls != 0 || len(messages) != 0 {
		t.Fatalf("provider was called with symlinked evidence: calls=%d messages=%d", calls, len(messages))
	}
	if got := loadRenderAgentPermitLedger(t, ledgerPath).Reservations[0].Status; got != "permit_armed" {
		t.Fatalf("symlink rejection consumed provider permit: status=%q", got)
	}
}

func TestRenderContextPrimingStreamConsumesPermitAtProviderBoundary(t *testing.T) {
	st, contextTool, ledgerPath := newRenderContextPrimingFixture(t, true)
	provider := &renderPrimingCaptureModel{}
	model := withRenderContextPriming(provider, st, contextTool)
	events, err := model.GenerateStream(context.Background(), []agentcore.Message{{
		Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("直接写正文")},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range events {
	}
	if calls, _ := provider.snapshot(); calls != 1 {
		t.Fatalf("stream provider calls=%d, want 1", calls)
	}
	if got := loadRenderAgentPermitLedger(t, ledgerPath).Reservations[0].Status; got != "provider_dispatched" {
		t.Fatalf("stream provider boundary did not consume permit: status=%q", got)
	}
}

func TestServerPrimedRenderToolsExposeOnlyWholeBodyDraft(t *testing.T) {
	st := store.NewStore(t.TempDir())
	filtered := serverPrimedRenderTools([]agentcore.Tool{
		tools.NewContextTool(st, tools.References{}, "default"),
		tools.NewReadChapterTool(st),
		tools.NewDraftChapterTool(st),
	})
	seen := make([]string, 0, len(filtered))
	for _, tool := range filtered {
		seen = append(seen, tool.Name())
	}
	if len(seen) != 1 || seen[0] != "draft_chapter" {
		t.Fatalf("server-primed render tool inventory=%v", seen)
	}
}

func TestValidatePrimedRenderContextUsesSharedLocationsAndExactV11(t *testing.T) {
	payload := map[string]any{
		"_context_profile": "draft",
		"episodic_memory": map[string]any{
			"render_packet": map[string]any{"version": 11, "chapter": 2},
		},
	}
	aigc.ApplyProseRenderCompatibilityContracts(payload)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePrimedRenderContext(raw, 2); err != nil {
		t.Fatalf("supported shared packet location failed priming: %v", err)
	}

	packet := payload["episodic_memory"].(map[string]any)["render_packet"].(map[string]any)
	packet["version"] = 12
	future, _ := json.Marshal(payload)
	if err := validatePrimedRenderContext(future, 2); err == nil {
		t.Fatal("future packet inherited exact v11 priming protocol")
	}

	packet["version"] = 11
	payload["selected_memory"] = map[string]any{"render_packet": packet}
	duplicated, _ := json.Marshal(payload)
	if err := validatePrimedRenderContext(duplicated, 2); err == nil {
		t.Fatal("duplicate packet locations passed priming")
	}
}

func newRenderContextPrimingFixture(t *testing.T, publish bool) (*store.Store, *tools.ContextTool, string) {
	t.Helper()
	base := t.TempDir()
	live := filepath.Join(base, "output")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	candidateID := "render-ch0001-priming"
	candidate := filepath.Join(base, ".render-candidates", candidateID, "output")
	st := store.NewStore(candidate)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "首 token 合同"}); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1), "plan", "drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(ch string) string { return "sha256:" + strings.Repeat(ch, 64) }
	if publish {
		legacy := json.RawMessage(`{
			"_context_profile":"draft",
			"working_memory":{"render_packet":{"version":11,"chapter":1,"title":"首 token 合同"}}
		}`)
		if _, err := tools.PublishFrozenDraftRenderContext(st, 1, checkpoint.Digest, legacy); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    checkpoint.Digest,
		Owner:         "render-context-priming-test",
	}); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(map[string]any{
		"version": "pipeline-render-candidate.v2", "candidate_id": candidateID,
		"generation_id": "pg2_priming", "chapter": 1,
		"plan_digest": checkpoint.Digest, "plan_checkpoint_seq": 1,
		"projected_bundle_digest": digest("c"), "promotion_receipt_digest": digest("d"),
		"source_output_dir": live,
	})
	if err != nil {
		t.Fatal(err)
	}
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
		GenerationID:    "pg2_priming", Chapter: 1, PlanDigest: checkpoint.Digest,
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
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.ArmPipelineRenderProsePermit(authorization, 1); err != nil {
		t.Fatal(err)
	}
	return st, tools.NewContextTool(st, tools.References{}, "default"), ledgerPath
}

func requireSingleRenderPrimingEnvelope(
	t *testing.T,
	messages []agentcore.Message,
) (json.RawMessage, aigc.ProseRenderPrimingIdentity) {
	t.Helper()
	count := 0
	var payload json.RawMessage
	var identity aigc.ProseRenderPrimingIdentity
	for _, message := range messages {
		candidate, candidateIdentity, recognized, err := aigc.ParseProseRenderPrimingEnvelope(message.TextContent())
		if !recognized {
			continue
		}
		if err != nil {
			t.Fatalf("provider received malformed priming envelope: %v", err)
		}
		count++
		payload = candidate
		identity = candidateIdentity
		if message.Role != agentcore.RoleUser || message.Metadata["server_owned"] != true {
			t.Fatalf("priming envelope was not a server-owned user message: %+v", message)
		}
	}
	if count != 1 {
		t.Fatalf("provider received %d priming envelopes, want exactly 1", count)
	}
	return payload, identity
}
