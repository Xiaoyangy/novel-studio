package llmcodex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

func TestCodexTerminalFailurePrefersWhitelistedJSONAndKeepsUsageAndExitError(t *testing.T) {
	for _, event := range []string{
		`{"type":"error","code":"context_length_exceeded","message":"Input exceeds the context limit","reasoning":"PRIVATE_REASONING"}`,
		`{"type":"turn.failed","error":{"code":"context_length_exceeded","message":"Input exceeds the context limit","details":{"tool_args":"PRIVATE_TOOL_ARGS"}}}`,
	} {
		model := usageFakeCLI(t, `
printf '%s\n' '{"type":"item.completed","item":{"type":"reasoning","text":"PRIVATE_REASONING"}}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"PRIVATE_AGENT_MESSAGE"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":900,"output_tokens":50}}'
printf '%s' '`+event+`'
printf '%s\n' 'rollout DB warning: not the provider cause' >&2
exit 1
`)
		_, err := model.Generate(context.Background(), usageMessages("do not echo this prompt"), nil)
		if err == nil || !strings.Contains(err.Error(), "context_length_exceeded") || !strings.Contains(err.Error(), "Input exceeds the context limit") || !strings.Contains(err.Error(), "structured:") {
			t.Fatalf("structured cause lost: %v", err)
		}
		for _, forbidden := range []string{"PRIVATE_", "rollout DB", "do not echo this prompt", `"type":"item.completed"`} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatalf("unrelated stdout/stderr was exposed: %s", forbidden)
			}
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatal("structured summary replaced the original wrapped exit error")
		}
		var metered interface {
			LLMUsage() (*agentcore.Usage, string)
		}
		if !errors.As(err, &metered) {
			t.Fatal("structured failure lost typed usage")
		}
		usage, source := metered.LLMUsage()
		if source != "reported" || usage == nil || usage.Input != 1200 || usage.Output != 50 || usage.CacheRead != 900 {
			t.Fatalf("failure changed reported counters: %+v %s", usage, source)
		}
	}
}

func TestCodexTerminalFailureDoesNotTurnRecoveredErrorsIntoFailure(t *testing.T) {
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"error","message":"EARLY_RETRY_ERROR"}'
printf '%s\n' '{"type":"turn.failed","error":{"message":"EARLY_FAILED_TURN"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":20}}'
printf '%s' '恢复后的有效结果' > "$out"
`)
	response, err := model.Generate(context.Background(), usageMessages("test recovery"), nil)
	if err != nil || response.Message.TextContent() != "恢复后的有效结果" || response.Message.Usage.Input != 100 {
		t.Fatalf("recovered turn was treated as failure: %+v %v", response, err)
	}
	raw, _ := json.Marshal(response.Message)
	if strings.Contains(string(raw), "EARLY_") {
		t.Fatal("early errors leaked into successful output or metadata")
	}
}

func TestCodexTerminalFailureFallbackExcludesNonterminalStdout(t *testing.T) {
	model := usageFakeCLI(t, `
printf '%s\n' '{"type":"error","message":"RECOVERED_PROVIDER_ERROR"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"error","code":"PRIVATE_CODE","message":"PRIVATE_MESSAGE"}}'
printf '%s\n' '{"type":"agent_message","message":"PRIVATE_AGENT_MESSAGE"}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":9,"output_tokens":1}}'
printf '%s\n' 'legacy stderr cause' >&2
exit 1
`)
	_, err := model.Generate(context.Background(), usageMessages("fallback"), nil)
	if err == nil || !strings.Contains(err.Error(), "legacy stderr cause") || strings.Contains(err.Error(), "PRIVATE_") || strings.Contains(err.Error(), "RECOVERED_PROVIDER_ERROR") {
		t.Fatalf("incorrect fallback diagnostic: %v", err)
	}
}

func TestCodexTerminalFailureWithoutUsageRemainsExplicitlyUnknown(t *testing.T) {
	model := usageFakeCLI(t, `
printf '%s' '{"type":"turn.failed","error":{"code":"unavailable","message":"No provider usage was reported"}}'
exit 1
`)
	_, err := model.Generate(context.Background(), usageMessages("not a token receipt"), nil)
	var metered *UsageError
	if !errors.As(err, &metered) || !strings.Contains(err.Error(), "No provider usage was reported") {
		t.Fatalf("missing structured unknown-usage failure: %v", err)
	}
	usage, source := metered.LLMUsage()
	breakdown, complete := metered.LLMUsageBreakdown()
	if usage != nil || source != "unknown" || !complete || len(breakdown) != 1 || breakdown[0] != nil {
		t.Fatalf("failure fabricated usage from its error text: %+v %s %#v", usage, source, breakdown)
	}
}

func TestTerminalEventWriterFragmentedLongAndUnterminatedEventsRemainBounded(t *testing.T) {
	writer := &codexUsageEventWriter{}
	message := "实际失败原因\n\t\u202e" + strings.Repeat("很长的错误说明", 3000)
	errorEvent, _ := json.Marshal(map[string]any{"type": "turn.failed", "error": map[string]any{
		"code": strings.Repeat("capacity_", 40), "message": message, "reasoning": "PRIVATE_REASONING", "tool_args": "PRIVATE_ARGS",
	}})
	stream := `{"type":"item.completed","item":{"type":"reasoning","text":"` + strings.Repeat("x", maxCodexUsageEventBytes*3) + "\"}}\n" +
		`{"type":"turn.completed","usage":{"input_tokens":321,"cached_input_tokens":123,"output_tokens":45}}` + "\n" + string(errorEvent)
	for start := 0; start < len(stream); start += 13 {
		end := min(start+13, len(stream))
		if n, err := writer.Write([]byte(stream[start:end])); err != nil || n != end-start {
			t.Fatalf("fragment was not consumed: %d %v", n, err)
		}
		if len(writer.line) > maxCodexUsageEventBytes {
			t.Fatal("stream retained unbounded stdout")
		}
	}
	writer.finish()
	summary := writer.failureSummary()
	if writer.dropped != 1 || writer.completed != 1 || writer.usage.Input != 321 || writer.usage.CacheRead != 123 || len(writer.line) != 0 {
		t.Fatal("long/fragmented diagnostics changed usage or stream bounds")
	}
	if !strings.Contains(summary, "实际失败原因") || !strings.Contains(summary, "[truncated]") || strings.Contains(summary, "PRIVATE_") || !utf8.ValidString(summary) {
		t.Fatalf("incorrect bounded terminal summary: %q", summary)
	}
	if len(writer.lastFailedTurn.message) > maxCodexFailureMessageBytes || len(writer.lastFailedTurn.code) > maxCodexFailureCodeBytes || strings.ContainsAny(writer.lastFailedTurn.message, "\n\t\u202e") {
		t.Fatal("terminal fields exceeded bounds or retained control characters")
	}
	writer.finish()
	if writer.completed != 1 || writer.failureSummary() != summary {
		t.Fatal("finish replay counted a line twice")
	}
}

func TestTerminalEventWriterIgnoresNestedPayloadsAndClearsRecoveredFailure(t *testing.T) {
	writer := &codexUsageEventWriter{}
	for _, line := range []string{
		`{"type":"item.completed","item":{"type":"error","message":"PRIVATE_ITEM"}}`,
		`{"type":"error","code":{"private":"PRIVATE_CODE"},"message":{"private":"PRIVATE_MESSAGE"}}`,
		`{"type":"turn.failed","error":{"details":{"message":"PRIVATE_DETAILS"}}}`,
		`{"type":"error","reasoning":"PRIVATE_REASONING","arguments":"PRIVATE_ARGS"}`,
	} {
		writer.consume([]byte(line))
	}
	if writer.failureSummary() != "" {
		t.Fatal("unwhitelisted nested payload became a diagnostic")
	}
	writer.consume([]byte(`{"type":"error","code":429,"message":"first retry"}`))
	if !strings.Contains(writer.failureSummary(), `code="429"`) {
		t.Fatal("scalar numeric error code was lost")
	}
	writer.consume([]byte(`{"type":"turn.failed","error":{"message":"terminal cause"}}`))
	writer.consume([]byte(`{"type":"error","message":"later generic diagnostic"}`))
	if !strings.Contains(writer.failureSummary(), "terminal cause") || strings.Contains(writer.failureSummary(), "later generic") {
		t.Fatal("terminal failed-turn cause was replaced by a generic error")
	}
	writer.consume([]byte(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":1}}`))
	if writer.failureSummary() != "" || writer.usage.Input != 10 {
		t.Fatal("a completed turn retained stale failure or lost usage")
	}
}

func TestCodexTerminalFailurePreservesCancellationTimeoutAndReportedUsage(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "timeout"}[timeout], func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			t.Setenv("CODEX_FAILURE_STARTED", marker)
			model := usageFakeCLI(t, `
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":50,"cached_input_tokens":20,"output_tokens":5}}'
printf '%s\n' '{"type":"error","message":"provider still pending"}'
printf started > "$CODEX_FAILURE_STARTED"
exec sleep 30
`)
			ctx, cancel := context.WithCancel(context.Background())
			wanted := context.Canceled
			if timeout {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				wanted = context.DeadlineExceeded
			}
			defer cancel()
			finished := make(chan error, 1)
			go func() { _, err := model.Generate(ctx, usageMessages("wait for cancellation"), nil); finished <- err }()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, err := os.Stat(marker); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("fake provider never began")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-finished:
				if !errors.Is(err, wanted) || !strings.Contains(err.Error(), "provider still pending") {
					t.Fatalf("context identity or structured detail lost: %v", err)
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal("context handling lost the underlying process error")
				}
				var metered *UsageError
				if !errors.As(err, &metered) {
					t.Fatal("canceled attempt lost its usage error")
				}
				u, source := metered.LLMUsage()
				if u == nil || u.Input != 50 || u.Output != 5 || source != "reported" {
					t.Fatalf("context failure changed usage: %+v %s", u, source)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("fake CLI did not stop at its context boundary")
			}
		})
	}
}
