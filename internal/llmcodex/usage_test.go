package llmcodex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/voocel/agentcore"
)

func usageFakeCLI(t *testing.T, body string) *CodexModel {
	return usageFakeCLIWithMCP(t, "printf '[]'\nexit 0\n", body)
}

func usageFakeCLIWithMCP(t *testing.T, mcpBody, body string) *CodexModel {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nif [ \"$1\" = \"mcp\" ]; then\n" + mcpBody + "\nfi\n" + `
out=""
all_args="$*"
if [ -n "$CODEX_USAGE_ARGS_LOG" ]; then printf '%s\n' "$@" > "$CODEX_USAGE_ARGS_LOG"; pwd > "$CODEX_USAGE_ARGS_LOG.cwd"; fi
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift 2; continue; fi
  shift
done
prompt=$(cat)
` + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(path, "gpt-6-astra", "high")
}

func usageMessages(text string) []agentcore.Message {
	return []agentcore.Message{{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(text)}}}
}

func TestGenerateUsesReportedCLIUsageAndOnlyLastMessage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args")
	t.Setenv("CODEX_USAGE_ARGS_LOG", logPath)
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"item.completed","item":{"type":"reasoning","text":"DO_NOT_PERSIST_PRIVATE_REASONING"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":900,"output_tokens":50,"reasoning_output_tokens":40}}'
printf '%s' '最终可见正文。' > "$out"
`)
	response, err := model.Generate(context.Background(), usageMessages("输出结果"), nil)
	if err != nil {
		t.Fatal(err)
	}
	u := response.Message.Usage
	if u == nil || u.Input != 1200 || u.CacheRead != 900 || u.Output != 50 || u.TotalTokens != 1250 {
		t.Fatalf("wrong reported usage: %+v", u)
	}
	if response.Message.TextContent() != "最终可见正文。" {
		t.Fatalf("JSON stdout became prose: %q", response.Message.TextContent())
	}
	if response.Message.Metadata["codex_usage_source"] != "reported" || response.Message.Metadata["codex_exec_calls"] != 1 {
		t.Fatalf("missing usage attribution: %+v", response.Message.Metadata)
	}
	raw, err := json.Marshal(response.Message)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "DO_NOT_PERSIST") {
		t.Fatal("raw reasoning entered returned message")
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--json", "--ephemeral", "--ignore-rules", "multi_agent", "plugins", "apps", "browser_use", "computer_use", "shell_tool", `web_search="disabled"`, "memories.use_memories=false", "project_doc_max_bytes=0"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("control call missing %q", want)
		}
	}
	for _, forbidden := range []string{"\n-C\n", "--ignore-user-config", "model_provider="} {
		if strings.Contains(string(args), forbidden) {
			t.Fatalf("control call changed provider/cwd config: %s", args)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rawCWD, err := os.ReadFile(logPath + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(rawCWD)) != cwd {
		t.Fatalf("control cwd changed: %q want %q", rawCWD, cwd)
	}
}

func TestGenerateAggregatesControlProseAndRepairUsage(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("CODEX_USAGE_COUNT", filepath.Join(t.TempDir(), "count"))
	model := usageFakeCLI(t, `
n=0
if [ -f "$CODEX_USAGE_COUNT" ]; then n=$(cat "$CODEX_USAGE_COUNT"); fi
n=$((n+1))
printf '%s' "$n" > "$CODEX_USAGE_COUNT"
printf '{"type":"turn.completed","usage":{"input_tokens":%s,"cached_input_tokens":%s,"output_tokens":%s}}\n' "$((n*100))" "$((n*50))" "$((n*10))"
if [ "$n" -eq 1 ]; then
  printf '%s' '{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"mode\":\"write\",\"content\":\"占位\"}","text":null}' > "$out"
elif [ "$n" -eq 2 ]; then
  printf '%s' '{"prose":"短"}' > "$out"
else
  printf '%s' '{"prose":"第一章 修复后的正文。她推门进去，看见桌面上散落的账单。"}' > "$out"
fi
`)
	response, err := model.Generate(context.Background(), usageMessages(`调用 draft_chapter。{"word_budget":{"hard_min":10,"hard_max":100}}`), directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	u := response.Message.Usage
	if u == nil || u.Input != 600 || u.Output != 60 || u.CacheRead != 300 || u.TotalTokens != 660 {
		t.Fatalf("child usage was omitted or double-counted: %+v", u)
	}
	if response.Message.Metadata["codex_exec_calls"] != 3 || response.Message.Metadata["codex_usage_source"] != "reported" {
		t.Fatalf("wrong child-call accounting: %+v", response.Message.Metadata)
	}
	breakdown, ok := response.Message.Metadata["codex_usage_breakdown"].([]*agentcore.Usage)
	if !ok || len(breakdown) != 3 || breakdown[0].Input != 100 || breakdown[1].Input != 200 || breakdown[2].Input != 300 || response.Message.Metadata["codex_usage_breakdown_complete"] != true {
		t.Fatalf("per-CLI pricing breakdown lost: %+v", response.Message.Metadata)
	}
	if calls := response.Message.ToolCalls(); len(calls) != 1 || !strings.Contains(string(calls[0].Args), "修复后的正文") {
		t.Fatalf("repair output not used: %+v", calls)
	}
}

func TestGenerateLabelsMixedLegacyFallback(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("CODEX_USAGE_COUNT", filepath.Join(t.TempDir(), "count"))
	model := usageFakeCLI(t, `
if [ ! -f "$CODEX_USAGE_COUNT" ]; then
  printf done > "$CODEX_USAGE_COUNT"
  printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":70,"output_tokens":10}}'
  printf '%s' '{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"mode\":\"write\",\"content\":\"占位\"}","text":null}' > "$out"
else
  printf '%s' '{"prose":"这是旧CLI成功返回、没有usage事件的正文。"}' > "$out"
fi
`)
	messages := usageMessages("写第一章")
	response, err := model.Generate(context.Background(), messages, directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	estimate := model.estimateUsage(buildProsePrompt(messages), `{"prose":"这是旧CLI成功返回、没有usage事件的正文。"}`)
	if response.Message.Usage.Input != 100+estimate.Input || response.Message.Usage.Output != 10+estimate.Output || response.Message.Usage.CacheRead != 70 {
		t.Fatalf("mixed fallback did not account each call: %+v", response.Message.Usage)
	}
	if response.Message.Metadata["codex_usage_source"] != "mixed" || response.Message.Metadata["codex_estimated_usage_calls"] != 1 {
		t.Fatalf("estimate was not labeled: %+v", response.Message.Metadata)
	}
}

func TestLocalProseCacheHitDoesNotCreateEstimatedUsage(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	model := New(filepath.Join(t.TempDir(), "never-execute"), "gpt-6-astra", "high")
	messages := usageMessages("写第一章")
	if err := saveCachedProse(buildProsePrompt(messages), model.model, "high", "第一章 缓存正文。她走进院子，看见窗边的人。"); err != nil {
		t.Fatal(err)
	}
	msg, err := parseCodexResponse(`{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"mode\":\"write\",\"content\":\"占位\"}"}`, directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	collector := &codexUsageAccumulator{}
	ctx := context.WithValue(context.Background(), codexUsageContextKey{}, collector)
	if err := model.regenerateProseArgs(ctx, messages, &msg, "high"); err != nil {
		t.Fatal(err)
	}
	collector.apply(&msg)
	if msg.Usage.TotalTokens != 0 || msg.Metadata["codex_exec_calls"] != 0 || msg.Metadata["codex_usage_source"] != "none" {
		t.Fatalf("local cache was billed: %+v %+v", msg.Usage, msg.Metadata)
	}
}

func TestGenerateWithCachedProseAccountsOnlyControlCall(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":10}}'
printf '%s' '{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"mode\":\"write\",\"content\":\"占位\"}","text":null}' > "$out"
`)
	messages := usageMessages("用缓存写第一章")
	if err := saveCachedProse(buildProsePrompt(messages), model.model, "high", "第一章 缓存正文。她走进院子，看见窗边的人。"); err != nil {
		t.Fatal(err)
	}
	response, err := model.Generate(context.Background(), messages, directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Usage.Input != 100 || response.Message.Usage.Output != 10 || response.Message.Usage.CacheRead != 60 || response.Message.Metadata["codex_exec_calls"] != 1 || response.Message.Metadata["codex_estimated_usage_calls"] != 0 {
		t.Fatalf("cached prose inflated control usage: %+v %+v", response.Message.Usage, response.Message.Metadata)
	}
}

func TestAuthenticatedDirectProseUsesReportedUsage(t *testing.T) {
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":444,"cached_input_tokens":333,"output_tokens":22}}'
printf '%s' '{"prose":"第二章 独立正文。她推门进去。"}' > "$out"
`)
	response, err := model.Generate(context.Background(), directRenderPrimingMessages(t, 2, 1, 100), directRenderToolSpecs())
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Usage.Input != 444 || response.Message.Usage.CacheRead != 333 || response.Message.Usage.Output != 22 || response.Message.Metadata["codex_exec_calls"] != 1 {
		t.Fatalf("direct render lost CLI usage: %+v", response.Message.Usage)
	}
}

func TestGenerateUsageIsolatedAcrossConcurrentCalls(t *testing.T) {
	model := usageFakeCLI(t, `
case "$prompt" in
  *request-one*) n=101 ;;
  *) n=202 ;;
esac
printf '{"type":"turn.completed","usage":{"input_tokens":%s,"cached_input_tokens":1,"output_tokens":2}}\n' "$n"
printf '%s' '独立结果' > "$out"
`)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		i := i
		wg.Go(func() {
			text, want := "request-one", 101
			if i%2 != 0 {
				text, want = "request-two", 202
			}
			response, err := model.Generate(context.Background(), usageMessages(text), nil)
			if err != nil {
				t.Error(err)
				return
			}
			if response.Message.Usage.Input != want || response.Message.Metadata["codex_exec_calls"] != 1 {
				t.Errorf("usage crossed concurrent requests: %+v", response.Message.Usage)
			}
		})
	}
	wg.Wait()
}

func TestFailedExecUsageRemainsInRequestAccounting(t *testing.T) {
	t.Setenv("CODEX_USAGE_COUNT", filepath.Join(t.TempDir(), "failed-once"))
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":150,"output_tokens":30}}'
if [ ! -f "$CODEX_USAGE_COUNT" ]; then
  printf done > "$CODEX_USAGE_COUNT"
  exit 9
fi
printf '%s' '恢复成功' > "$out"
`)
	collector := &codexUsageAccumulator{}
	ctx := context.WithValue(context.Background(), codexUsageContextKey{}, collector)
	if _, err := model.runCodex(ctx, "已消耗token后进程失败", nil, "high"); err == nil {
		t.Fatal("fake CLI did not fail")
	}
	if _, err := model.runCodex(ctx, "后续恢复成功", nil, "high"); err != nil {
		t.Fatal(err)
	}
	var message agentcore.Message
	collector.apply(&message)
	if message.Usage.Input != 400 || message.Usage.Output != 60 || message.Metadata["codex_exec_calls"] != 2 {
		t.Fatalf("failed child usage lost: %+v %+v", message.Usage, message.Metadata)
	}
}

func TestUsageEventWriterRecoversAfterOversizedNonUsageLine(t *testing.T) {
	writer := &codexUsageEventWriter{}
	stream := `{"type":"item.completed","item":{"type":"reasoning","text":"` + strings.Repeat("x", maxCodexUsageEventBytes*3) + "\"}}\n" +
		`{"type":"turn.completed","usage":{"input_tokens":321,"cached_input_tokens":123,"output_tokens":45}}`
	for start := 0; start < len(stream); start += 777 {
		end := min(start+777, len(stream))
		if n, err := writer.Write([]byte(stream[start:end])); err != nil || n != end-start {
			t.Fatalf("stream write: %d %v", n, err)
		}
		if len(writer.line) > maxCodexUsageEventBytes {
			t.Fatal("event buffer exceeded bound")
		}
	}
	writer.finish()
	if writer.completed != 1 || writer.dropped != 1 || writer.usage.Input != 321 || writer.usage.CacheRead != 123 || len(writer.line) != 0 {
		t.Fatalf("large event hid trailing usage: %+v", writer)
	}
}

func TestUsageEventWriterRejectsInvalidCounters(t *testing.T) {
	for _, usage := range []string{`{"input_tokens":-1,"output_tokens":1}`, `{"input_tokens":1,"cached_input_tokens":2,"output_tokens":1}`, `{"input_tokens":"bad","output_tokens":1}`, `{"input_tokens":1}`, `null`} {
		writer := &codexUsageEventWriter{}
		_, _ = writer.Write([]byte(fmt.Sprintf("{\"type\":\"turn.completed\",\"usage\":%s}\n", usage)))
		if !writer.invalid || writer.completed != 0 {
			t.Fatalf("invalid counters trusted: %s %+v", usage, writer)
		}
	}
}

func TestCodexDiagnosticTailIsBounded(t *testing.T) {
	var tail codexDiagnosticTail
	for range 1000 {
		_, _ = tail.Write([]byte(strings.Repeat("diagnostic", 200)))
		if len(tail.data) > maxCodexDiagnosticBytes {
			t.Fatal("stderr buffering is unbounded")
		}
	}
	_, _ = tail.Write([]byte("final diagnostic"))
	if !strings.HasSuffix(tail.String(), "final diagnostic") {
		t.Fatal("stderr tail lost last error")
	}
}

func TestGenerateUsageErrorPreservesFailedProseAndControlUsage(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_PROSE_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("CODEX_USAGE_COUNT", filepath.Join(t.TempDir(), "count"))
	model := usageFakeCLI(t, `
if [ ! -f "$CODEX_USAGE_COUNT" ]; then
  printf done > "$CODEX_USAGE_COUNT"
  printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":150000,"cached_input_tokens":100000,"output_tokens":10}}'
  printf '%s' '{"action":"tool_call","tool_name":"draft_chapter","arguments_json":"{\"chapter\":1,\"mode\":\"write\",\"content\":\"占位\"}","text":null}' > "$out"
else
  printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":150000,"cached_input_tokens":100000,"output_tokens":20}}'
  exit 9
fi
`)
	response, err := model.Generate(context.Background(), usageMessages("写第一章"), directRenderToolSpecs())
	if err == nil || response != nil {
		t.Fatalf("expected Generate failure, got %+v %v", response, err)
	}
	var meter interface {
		LLMUsage() (*agentcore.Usage, string)
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}
	if !errors.As(err, &meter) {
		t.Fatalf("missing typed usage error: %v", err)
	}
	usage, source := meter.LLMUsage()
	if usage == nil || usage.Input != 300000 || usage.Output != 30 || usage.CacheRead != 200000 || source != "reported" {
		t.Fatalf("failed Generate lost billed usage: %+v %s", usage, source)
	}
	breakdown, complete := meter.LLMUsageBreakdown()
	if !complete || len(breakdown) != 2 || breakdown[0].Input != 150000 || breakdown[1].Input != 150000 {
		t.Fatalf("aggregated total lost per-call pricing boundary: %+v %v", breakdown, complete)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 9 {
		t.Fatalf("original CLI error was lost: %v", err)
	}
	usage.Input = 1
	breakdown[0].Input = 1
	again, _ := meter.LLMUsage()
	calls, _ := meter.LLMUsageBreakdown()
	if again.Input != 300000 || calls[0].Input != 150000 {
		t.Fatal("observer mutation changed durable metering")
	}
}

func TestGenerateUnknownUsageErrorDoesNotFabricateZeroUsage(t *testing.T) {
	model := usageFakeCLI(t, "exit 9\n")
	_, err := model.Generate(context.Background(), usageMessages("没有usage的失败"), nil)
	var meter interface {
		LLMUsage() (*agentcore.Usage, string)
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}
	if !errors.As(err, &meter) {
		t.Fatalf("executed CLI failure missing metering state: %v", err)
	}
	usage, source := meter.LLMUsage()
	if usage != nil || source != "unknown" {
		t.Fatalf("unknown usage presented as known: %+v %s", usage, source)
	}
	calls, complete := meter.LLMUsageBreakdown()
	if !complete || len(calls) != 1 || calls[0] != nil {
		t.Fatalf("unknown execution missing from breakdown: %+v %v", calls, complete)
	}
}

func TestUsageErrorPreservesCauseAndPartialCompleteness(t *testing.T) {
	collector := &codexUsageAccumulator{}
	collector.add(&agentcore.Usage{Input: 100, Output: 20, TotalTokens: 120}, "reported")
	collector.add(nil, "unavailable")
	err := collector.wrapError(fmt.Errorf("nested timeout: %w", context.DeadlineExceeded))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("typed usage wrapper broke errors.Is: %v", err)
	}
	var meter interface {
		LLMUsage() (*agentcore.Usage, string)
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}
	if !errors.As(err, &meter) {
		t.Fatal("errors.As metering interface unavailable")
	}
	usage, source := meter.LLMUsage()
	if usage == nil || usage.Input != 100 || source != "partial" {
		t.Fatalf("known subtotal confused with complete usage: %+v %s", usage, source)
	}
	calls, complete := meter.LLMUsageBreakdown()
	if !complete || len(calls) != 2 || calls[0] == nil || calls[1] != nil {
		t.Fatalf("partial breakdown changed: %+v %v", calls, complete)
	}
}

func TestGenerateDoesNotAttachUsageBeforeCLIStarts(t *testing.T) {
	model := New(filepath.Join(t.TempDir(), "missing-cli"), "gpt-6-astra", "high")
	_, err := model.Generate(context.Background(), usageMessages("未启动CLI"), nil)
	var meter interface {
		LLMUsage() (*agentcore.Usage, string)
	}
	if err == nil || errors.As(err, &meter) {
		t.Fatalf("pre-exec failure invented an executed call: %v", err)
	}
}

func TestUsageBreakdownIsBoundedAndMarksTruncation(t *testing.T) {
	collector := &codexUsageAccumulator{}
	for range maxCodexUsageBreakdownCalls + 1 {
		collector.add(&agentcore.Usage{Input: 10, Output: 1, TotalTokens: 11}, "reported")
	}
	err := collector.wrapError(context.Canceled)
	var meter interface {
		LLMUsageBreakdown() ([]*agentcore.Usage, bool)
	}
	if !errors.As(err, &meter) {
		t.Fatal("missing metering error")
	}
	calls, complete := meter.LLMUsageBreakdown()
	if len(calls) != maxCodexUsageBreakdownCalls || complete {
		t.Fatalf("unbounded or silently truncated breakdown: %d %v", len(calls), complete)
	}
	var message agentcore.Message
	collector.apply(&message)
	if message.Metadata["codex_usage_breakdown_complete"] != false || message.Usage.Input != (maxCodexUsageBreakdownCalls+1)*10 {
		t.Fatal("breakdown truncation corrupted aggregate counters")
	}
}
