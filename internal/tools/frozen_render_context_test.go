package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestRenderLockedContextInjectsV11CompatibilityBeforeModelWithoutChangingEnvelope(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "旧版冻结上下文"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	legacy := json.RawMessage(`{
		"_context_profile":"draft",
		"working_memory":{"render_packet":{"version":11,"chapter":1,"title":"旧版冻结上下文"}}
	}`)
	frozen, err := PublishFrozenDraftRenderContext(st, 1, cp.Digest, legacy)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath := filepath.Join(st.Dir(), filepath.FromSlash(FrozenDraftRenderContextPath))
	before, err := os.ReadFile(envelopePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    cp.Digest,
		Owner:         "v11-compatibility-test",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := NewContextTool(st, References{}, "default").Execute(
		context.Background(),
		json.RawMessage(`{"chapter":1,"profile":"draft"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var returned map[string]any
	if err := json.Unmarshal(raw, &returned); err != nil {
		t.Fatal(err)
	}
	packet := returned["working_memory"].(map[string]any)["render_packet"].(map[string]any)
	contract, ok := packet["anti_ai_render_contract"].(map[string]any)
	if !ok || contract["compatibility_protocol"] != aigc.ProseRenderCompatibilityProtocolVersion ||
		contract["usage_policy"] != string(aigc.ProseRenderUsagePolicyV1) {
		t.Fatalf("render tool response lacks prospective compatibility contract: %#v", packet)
	}
	if _, ok := packet["event_timing_safeguards"].(map[string]any); !ok {
		t.Fatalf("render tool response lacks compatibility timing safeguards: %#v", packet)
	}

	after, err := os.ReadFile(envelopePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("dynamic compatibility overlay rewrote the frozen envelope")
	}
	_, reloaded, err := LoadFrozenDraftRenderContext(st, 1, cp.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.PayloadSHA256 != frozen.PayloadSHA256 {
		t.Fatalf("dynamic compatibility overlay changed signed payload digest: before=%s after=%s", frozen.PayloadSHA256, reloaded.PayloadSHA256)
	}
}

func TestSealedRenderContextAddsOnlyExactRejectedBodyFeedback(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结上下文"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(domain.ChapterScope(1), "plan", "drafts/01.plan.json")
	if err != nil {
		t.Fatal(err)
	}
	const body = "第一章 冻结上下文\n\n七个地点被逐项列成清单。"
	if err := st.Drafts.SaveDraft(1, body); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md", "plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, body); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("semantic overlay", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(body)), "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	bodySHA := reviewreport.BodySHA256(body)
	review := domain.ReviewEntry{
		Chapter: 1, Scope: "chapter", BodySHA256: bodySHA, Verdict: "rewrite", ContractStatus: "met",
		Summary: "删掉清单感，补一处主角真实误判。",
		Issues:  []domain.ConsistencyIssue{{Type: "aesthetic", Severity: "warning", Description: "catalog stuffing"}},
	}
	if err := st.World.SaveReview(review); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewritesAndFlow([]int{1}, review.Summary, domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	brief := "# rewrite brief\n\n- 待返工正文 SHA-256：`" + bodySHA + "`。\n- 删掉清单感。\n"
	if err := st.Drafts.SaveRewriteBrief(1, brief); err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	raw, err := tool.attachSealedRerenderFeedback(json.RawMessage(`{"render_packet":{"chapter":1}}`), 1, cp.Digest)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	feedback, ok := payload["sealed_rerender_feedback"].(map[string]any)
	if !ok || feedback["body_sha256"] != bodySHA || feedback["summary"] != review.Summary ||
		!strings.Contains(feedback["rewrite_brief"].(string), "删掉清单感") {
		t.Fatalf("exact semantic feedback missing: %#v", payload["sealed_rerender_feedback"])
	}

	if err := st.Drafts.SaveDraft(1, body+"\n新哈希"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(1), "draft", "drafts/01.draft.md", "plan", "rerender-request", "draft", "edit",
	); err != nil {
		t.Fatal(err)
	}
	raw, err = tool.attachSealedRerenderFeedback(json.RawMessage(`{"render_packet":{"chapter":1}}`), 1, cp.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sealed_rerender_feedback") {
		t.Fatalf("old review leaked onto replacement hash: %s", raw)
	}
}

func TestRenderContextReturnsExactPlanFrozenPayloadAndRejectsLiveProfiles(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 1,
		Title:   "冻结上下文",
		Goal:    "只按冻结事实渲染",
	}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewContextTool(st, References{}, "default")
	frozen, err := FreezeDraftRenderContext(context.Background(), tool, 1, cp.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{
		Mode:          domain.PipelineExecutionRender,
		TargetChapter: 1,
		PlanDigest:    cp.Digest,
		Owner:         "frozen-context-test",
	}); err != nil {
		t.Fatal(err)
	}

	// A live change after plan freeze must not change the prose payload.
	if err := os.MkdirAll(filepath.Join(st.Dir(), "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(st.Dir(), "meta", "user_rules.json"),
		[]byte(`{"preferences":"渲染时偷偷新增的规则"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"profile":"draft"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(frozen.Payload, &want); err != nil {
		t.Fatal(err)
	}
	// The sole permitted difference from frozen bytes is the deterministic,
	// in-memory compatibility overlay also used by every prose provider.
	aigc.ApplyProseRenderCompatibilityContracts(want)
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("render rebuilt live context instead of returning frozen payload")
	}
	if strings.Contains(string(raw), "渲染时偷偷新增的规则") {
		t.Fatal("post-freeze user rule leaked into render context")
	}

	for _, args := range []string{
		`{}`,
		`{"profile":"full"}`,
		`{"chapter":1,"profile":"planning"}`,
		`{"chapter":1,"profile":"full"}`,
		`{"chapter":2,"profile":"draft"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err == nil ||
			!strings.Contains(err.Error(), "render execution lock") {
			t.Fatalf("render lock accepted live context args=%s err=%v", args, err)
		}
	}
}

func TestFrozenRenderContextRejectsPayloadTampering(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "冻结"}); err != nil {
		t.Fatal(err)
	}
	cp, err := st.Checkpoints.AppendArtifactLatest(
		domain.ChapterScope(1),
		"plan",
		"drafts/01.plan.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FreezeDraftRenderContext(
		context.Background(),
		NewContextTool(st, References{}, "default"),
		1,
		cp.Digest,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(FrozenDraftRenderContextPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var envelope FrozenDraftRenderContext
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	working, _ := payload["working_memory"].(map[string]any)
	packet, _ := working["render_packet"].(map[string]any)
	packet["heading"] = "第1章 被篡改"
	envelope.Payload, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFrozenDraftRenderContext(st, 1, cp.Digest); err == nil ||
		!strings.Contains(err.Error(), "payload drift") {
		t.Fatalf("tampered frozen context was accepted: %v", err)
	}
}
