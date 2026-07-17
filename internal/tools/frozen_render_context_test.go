package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

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
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(frozen.Payload, &want); err != nil {
		t.Fatal(err)
	}
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
