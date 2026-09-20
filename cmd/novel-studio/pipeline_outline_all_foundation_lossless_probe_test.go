package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

// Opt-in read-only probe: no Store initialization, writes, model construction,
// provider dispatch or live process access. Only the actual context builder runs.
func TestOutlineAllCurrentFoundationCompleteProbe(t *testing.T) {
	dir := os.Getenv("OUTLINE_FOUNDATION_SOURCE_DIR")
	if dir == "" {
		t.Skip("set OUTLINE_FOUNDATION_SOURCE_DIR to a read-only foundation source")
	}
	foundation, err := loadPipelineOutlineAllFrozenFoundation(dir, domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	compass, err := store.NewStore(dir).Outline.LoadCompass()
	if err != nil || compass == nil {
		t.Fatalf("load source compass: %v", err)
	}
	var volumes []domain.VolumeOutline
	outline, err := os.ReadFile(filepath.Join(dir, "layered_outline.json"))
	if err != nil || json.Unmarshal(outline, &volumes) != nil {
		t.Fatalf("read source outline: %v", err)
	}
	digest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	target, err := domain.ResolveBookScaleTarget(compass.EstimatedScale, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest}
	view, raw, _, err := buildPipelineOutlineAllModelVisibleContext(volumes, *compass, target, action, foundation, assets.Load("default").References, "", domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("context_bytes=%d foundation_root=%s", len(raw), foundation.Root)
	for _, item := range []struct {
		name    string
		raw     json.RawMessage
		visible pipelineOutlineAllBoundedText
		marker  string
	}{
		{"characters", foundation.Characters, view.Foundation.Characters, `"name":"唐若宁"`},
		{"world_rules", foundation.WorldRules, view.Foundation.WorldRules, "秘密来源同步与现实处置边界"},
		{"book_world", foundation.BookWorld, view.Foundation.BookWorld, `"name":"澜汀市第二医院"`},
	} {
		var compact bytes.Buffer
		if err := json.Compact(&compact, item.raw); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s compact=%d visible=%d truncated=%t source_marker=%t visible_marker=%t", item.name, compact.Len(), len(item.visible.Text), item.visible.Truncated, strings.Contains(compact.String(), item.marker), strings.Contains(item.visible.Text, item.marker))
		if !strings.Contains(compact.String(), item.marker) {
			t.Fatalf("source changed; expected factual marker absent: %s", item.name)
		}
		if item.visible.Truncated || item.visible.Text != compact.String() {
			t.Errorf("%s source facts were cut before the model; tail_marker_visible=%t", item.name, strings.Contains(item.visible.Text, item.marker))
		}
	}
	author := foundation.Authorities[store.AuthorSourcesPath]
	visibleAuthor, ok := view.Foundation.Authorities[store.AuthorSourcesPath]
	t.Logf("author_sources original=%d visible=%d present=%t truncated=%t", len(author), len(visibleAuthor.Text), ok, visibleAuthor.Truncated)
	if author == "" || !ok || visibleAuthor.Truncated || visibleAuthor.Text != author {
		t.Error("original author source authority is not visible in full")
	}
	codex, err := os.ReadFile(filepath.Join(dir, "world_codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	visibleCodex, ok := view.Foundation.Authorities["world_codex.json"]
	t.Logf("world_codex original=%d visible=%d present=%t", len(codex), len(visibleCodex.Text), ok)
	if !ok || visibleCodex.Truncated || visibleCodex.Text != string(codex) {
		t.Error("existing world codex is absent from complete foundation context")
	}
	if output := os.Getenv("OUTLINE_FOUNDATION_PROMPT_OUTPUT"); output != "" {
		prompt, err := pipelineOutlineAllOperationPrompt(volumes, *compass, target, action, view, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, []byte(prompt), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("complete_operation_prompt_bytes=%d", len(prompt))
	}
}

func TestOutlineAllFoundationTailFactsSurviveMinimalContext(t *testing.T) {
	characters := json.RawMessage(`[{"name":"first","description":"` + strings.Repeat("x", 30*1024) + `"},{"name":"TAIL_ACTOR","private_fact":"TAIL_FACT"}]`)
	foundation := pipelineOutlineAllFrozenFoundation{Root: "sha256:minimal-frozen", Premise: "premise", Characters: characters, WorldRules: json.RawMessage(`[]`), BookWorld: json.RawMessage(`{}`), Compass: json.RawMessage(`{}`)}
	digest, err := domain.ComputeLayeredOutlineDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{Type: domain.OutlineAllActionPlanStructure, Operation: 1, BeforeLayeredDigest: digest}
	view, _, _, err := buildPipelineOutlineAllModelVisibleContext(nil, domain.StoryCompass{}, domain.BookScaleTarget{}, action, foundation, tools.References{}, "", domain.OutlineAllInputPolicyCompleteFoundationV1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view.Foundation.Characters.Text, "TAIL_FACT") || !json.Valid([]byte(view.Foundation.Characters.Text)) {
		t.Fatalf("model lost the final actor's factual source: original=%d visible=%d truncated=%t", len(characters), len(view.Foundation.Characters.Text), view.Foundation.Characters.Truncated)
	}
}
