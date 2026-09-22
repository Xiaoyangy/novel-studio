package agents

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

func TestFoundationCompleteSourceHostCapabilityBinding(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "complete-source-test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "characters.json"), []byte(`{"text":"`+strings.Repeat("a", 67053)+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target                  string
		oneShot, epoch, allowed bool
	}{{"characters", true, true, true}, {"characters", false, true, false}, {"characters", true, false, false}, {"", true, true, false}} {
		tool := foundationSourceContextForBuild(st, CoordinatorBuildOptions{FoundationRefreshTarget: tc.target, OneShotFoundationRefresh: tc.oneShot, RecordFoundationRefreshEpoch: tc.epoch})
		_, err := tool.Execute(t.Context(), json.RawMessage(`{}`))
		if (err == nil) != tc.allowed {
			t.Fatalf("host binding %+v: %v", tc, err)
		}
	}
}

func TestFoundationCompleteSourceRealSubagentCodexTransport(t *testing.T) {
	content := []byte(`{"begin":"SOURCE_BEGIN","data":"` + strings.Repeat("字", 21000) + "SOURCE_MIDDLE" + strings.Repeat(" ", 46000) + `","end":"SOURCE_END"}`)
	if path := os.Getenv("NOVEL_FOUNDATION_SOURCE_PROBE"); path != "" {
		var err error
		content, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	sources := map[string][]byte{"characters.json": content, "world_rules.json": []byte(`{"rules":["distinct exact rules"]}`), "book_world.json": []byte(`{"places":["distinct exact place"]}`)}
	if path := os.Getenv("NOVEL_FOUNDATION_SOURCE_PROBE"); path != "" {
		for _, name := range []string{"world_rules.json", "book_world.json"} {
			raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
			if err != nil {
				t.Fatal(err)
			}
			sources[name] = raw
		}
	}
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for name, raw := range sources {
		if err := os.WriteFile(filepath.Join(st.Dir(), name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Runtime.AcquirePipelineExecution(domain.PipelineExecutionLock{Mode: domain.PipelineExecutionFoundation, TargetChapter: 1, Owner: "complete-source-transport"}); err != nil {
		t.Fatal(err)
	}
	beforeRoot, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary, capture, count := filepath.Join(dir, "fake-codex"), filepath.Join(dir, "prompt"), filepath.Join(dir, "count")
	t.Setenv("FOUNDATION_CAPTURE", capture)
	t.Setenv("FOUNDATION_COUNT", count)
	script := `#!/bin/sh
set -eu
if [ "$1" = mcp ]; then printf '[]'; exit 0; fi
out=''
while [ "$#" -gt 0 ]; do
 case "$1" in -o|--output-last-message) shift; out="$1" ;; esac
 shift
done
cat > "$FOUNDATION_CAPTURE"
n=0
if [ -f "$FOUNDATION_COUNT" ]; then n=$(wc -l < "$FOUNDATION_COUNT" | tr -d ' '); fi
case "$n" in
 0) printf '%s' '{"action":"tool_call","tool_name":"novel_context","arguments_json":"{}","text":null}' > "$out" ;;
 1) printf '%s' '{"action":"tool_call","tool_name":"novel_context","arguments_json":"{\"source\":\"world_rules.json\"}","text":null}' > "$out" ;;
 2) printf '%s' '{"action":"tool_call","tool_name":"novel_context","arguments_json":"{\"source\":\"book_world.json\"}","text":null}' > "$out" ;;
 *) printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"offline source transport verified"}' > "$out" ;;
esac
printf 'call\n' >> "$FOUNDATION_COUNT"
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	read := foundationSourceContextForBuild(st, CoordinatorBuildOptions{FoundationRefreshTarget: "characters", OneShotFoundationRefresh: true, RecordFoundationRefreshEpoch: true})
	save := tools.NewSaveFoundationTool(st).WithFoundationTypeRestriction("characters")
	model := llmcodex.New(binary, "gpt-6-astra", "high", llmcodex.WithContextWindow(272000))
	child := subagent.New(subagent.Config{Name: "architect_long", Model: model, SystemPrompt: assets.Load("default").Prompts.ArchitectLong, Tools: []agentcore.Tool{read, save}, MaxTurns: 5})
	if _, err := child.Execute(t.Context(), json.RawMessage(`{"agent":"architect_long","task":"仅读取完整characters.json，确认原字节完整；本次测试不保存任何修订。"}`)); err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range sources {
		if bytes.Count(prompt, raw) != 1 {
			t.Fatalf("%s lost, truncated or duplicated in actual CLI input", name)
		}
	}
	if !bytes.Contains(prompt, []byte(assets.Load("default").Prompts.ArchitectLong)) || !bytes.Contains(prompt, []byte("仅读取完整characters.json，确认原字节完整；本次测试不保存任何修订。")) {
		t.Fatal("complete source transport clipped the actual Architect system or user task")
	}
	if calls, _ := os.ReadFile(count); string(calls) != "call\ncall\ncall\ncall\n" {
		t.Fatalf("unexpected offline provider calls: %q", calls)
	}
	afterRoot, err := store.DirectoryContentRoot(st.Dir())
	if err != nil || beforeRoot != afterRoot {
		t.Fatalf("source read changed source/lease: %v", err)
	}
	t.Logf("source bytes=%d runes=%d; actual CLI stdin bytes=%d runes=%d; exact occurrence=1; original root unchanged", len(content), utf8.RuneCount(content), len(prompt), utf8.RuneCount(prompt))
}
