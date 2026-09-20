package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

const outlineAllExactFixturePolicy = "OUTLINE_ALL_INPUT_POLICY outline-all-input.complete-foundation.v1\n"

// As in the existing grounding budget fixture, only the CLI executable is
// fake. Actual agent streaming, Codex prompt/schema assembly, stdin transport,
// response parsing and usage observation remain in the production path.
func outlineAllExactFakeCLI(t *testing.T) (binary, captureDir string) {
	t.Helper()
	captureDir = t.TempDir()
	binary = filepath.Join(captureDir, "fake-codex")
	t.Setenv("OUTLINE_EXACT_FIXTURE_DIR", captureDir)
	t.Setenv("OUTLINE_EXACT_FIXTURE_RESPONSE", `{"action":"tool_call","tool_name":"save_foundation","arguments_json":"{\"type\":\"expand_arc\",\"volume\":3,\"arc\":3,\"content\":[]}","text":null}`)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$1" >> "$OUTLINE_EXACT_FIXTURE_DIR/invocations"
if [ "$1" = mcp ]; then printf '[]'; exit 0; fi
out=''; schema=''; n=1
while [ -f "$OUTLINE_EXACT_FIXTURE_DIR/prompt-$n" ]; do n=$((n+1)); done
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output-last-message) shift; out="$1";;
    --output-schema) shift; schema="$1";;
  esac
  shift
done
cat > "$OUTLINE_EXACT_FIXTURE_DIR/prompt-$n"
cp "$schema" "$OUTLINE_EXACT_FIXTURE_DIR/schema-$n"
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1234,"cached_input_tokens":0,"output_tokens":56}}'
printf '%s' "$OUTLINE_EXACT_FIXTURE_RESPONSE" > "$out"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, captureDir
}

func outlineAllExactFixtureStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestOutlineAllExactPacketReachesCodexCLIWithCompleteInputAndFeedback(t *testing.T) {
	st := outlineAllExactFixtureStore(t)
	binary, captures := outlineAllExactFakeCLI(t)
	payload := outlineAllExactFixturePolicy + outlineAllOperationTask(t, 29, 3, 3, 14) +
		"\nSOURCE-BEGIN\n" + strings.Repeat("foundation-left-", 4500) + "\nFOUNDATION-MIDDLE-MUST-SURVIVE\n" +
		strings.Repeat("foundation-right-", 4500) + "\nFOUNDATION-TAIL-MUST-SURVIVE\n"
	system := "SYSTEM-BEGIN\n" + strings.Repeat("system-boundary-", 2500) + "\nSYSTEM-END"
	feedback := "LATEST-REAL-SAVE-REJECTION-BEGIN\n" + strings.Repeat("具体原始拒因", 1600) + "\nLATEST-REAL-SAVE-REJECTION-END"
	realTool := tools.NewSaveFoundationTool(st)
	executions, successes, metered := 0, 0, 0
	save := agentcore.NewFuncTool(realTool.Name(), realTool.Description(), realTool.Schema(), func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		executions++
		if string(args) != `{"type":"expand_arc","volume":3,"arc":3,"content":[]}` {
			t.Errorf("Codex response changed the authorized tool arguments: %s", args)
		}
		if executions == 1 {
			return nil, errors.New(feedback)
		}
		successes++
		return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
	})
	model := llmcodex.New(binary, "outline-exact-fixture", "", llmcodex.WithContextWindow(272000))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := runOutlineAllOperationWithModel(ctx, bootstrap.Config{}, assets.Bundle{Prompts: assets.Prompts{ArchitectLong: system}}, st, payload,
		outlineAllOperationModel{ChatModel: model, Provider: "codex-cli", Name: "outline-exact-fixture"}, save,
		func(role string, raw agentcore.AgentMessage) {
			if message, ok := raw.(agentcore.Message); ok && message.Role == agentcore.RoleAssistant && message.Usage != nil {
				if role != "architect_outline_all" || message.Usage.Input != 1234 || message.Usage.Output != 56 {
					t.Errorf("actual CLI usage or role changed: role=%s usage=%+v", role, message.Usage)
				}
				metered++
			}
		})
	if err != nil || executions != 2 || successes != 1 || metered != 2 {
		t.Fatalf("actual adapter retry/save/usage failed: err=%v attempts=%d successes=%d metered=%d", err, executions, successes, metered)
	}
	parameters, err := json.Marshal(realTool.Schema())
	if err != nil {
		t.Fatal(err)
	}
	for call := 1; call <= 2; call++ {
		raw, err := os.ReadFile(filepath.Join(captures, fmt.Sprintf("prompt-%d", call)))
		if err != nil {
			t.Fatal(err)
		}
		input := string(raw)
		if !strings.Contains(input, "FOUNDATION-MIDDLE-MUST-SURVIVE") || !strings.Contains(input, "FOUNDATION-TAIL-MUST-SURVIVE") || strings.Count(input, payload) != 1 {
			t.Fatalf("Codex CLI call %d lost full frozen foundation input: payload_occurrences=%d middle=%t tail=%t", call, strings.Count(input, payload), strings.Contains(input, "FOUNDATION-MIDDLE-MUST-SURVIVE"), strings.Contains(input, "FOUNDATION-TAIL-MUST-SURVIVE"))
		}
		for _, want := range []string{system, outlineAllOperationSystemBoundary, realTool.Description(), string(parameters), `exact_agent_packet kind="outline_all"`} {
			if !strings.Contains(input, want) {
				t.Fatalf("Codex CLI call %d lost a complete system/tool/input boundary", call)
			}
		}
		if call == 2 && !strings.Contains(input, feedback) {
			t.Fatal("Codex CLI retry lost the complete latest actual save rejection")
		}
		if strings.Contains(input, "Codex 入参压缩") {
			t.Fatal("exact operation was routed through legacy prompt clipping")
		}
		outputSchema, err := os.ReadFile(filepath.Join(captures, fmt.Sprintf("schema-%d", call)))
		if err != nil || !json.Valid(outputSchema) || !strings.Contains(string(outputSchema), "arguments_json") {
			t.Fatalf("actual CLI output schema missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(captures, "prompt-3")); !os.IsNotExist(err) {
		t.Fatal("successful exact save triggered an extra CLI call")
	}
}

func TestOutlineAllExactPacketOversizeRejectsBeforeAnyCLIInvocation(t *testing.T) {
	st := outlineAllExactFixtureStore(t)
	binary, captures := outlineAllExactFakeCLI(t)
	// The packet is below the configured window before output/CLI reserves
	// and safety margin. Those existing reserves must still make it fail.
	payload := outlineAllExactFixturePolicy + outlineAllOperationTask(t, 29, 3, 3, 14) + "\n" + strings.Repeat("界", 125000)
	realTool := tools.NewSaveFoundationTool(st)
	executions := 0
	save := agentcore.NewFuncTool(realTool.Name(), realTool.Description(), realTool.Schema(), func(context.Context, json.RawMessage) (json.RawMessage, error) {
		executions++
		return nil, errors.New("oversized input reached the save tool")
	})
	model := llmcodex.New(binary, "outline-exact-fixture", "", llmcodex.WithContextWindow(272000))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runOutlineAllOperationWithModel(ctx, bootstrap.Config{}, assets.Bundle{}, st, payload,
		outlineAllOperationModel{ChatModel: model, Provider: "codex-cli", Name: "outline-exact-fixture"}, save)
	if err == nil {
		t.Fatal("oversized complete foundation packet was accepted")
	}
	for _, want := range []string{"exact agent packet", "configured_context_tokens=272000", "reserved_output_tokens=34000", "input_budget_tokens=229808", "no input truncated or provider call"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("oversize failure did not retain the existing complete-input budget/reserve: missing %q in %v", want, err)
		}
	}
	if executions != 0 {
		t.Fatalf("oversized input executed %d tool calls", executions)
	}
	if raw, readErr := os.ReadFile(filepath.Join(captures, "invocations")); !os.IsNotExist(readErr) {
		t.Fatalf("oversized complete input started a CLI process: invocations=%q err=%v", raw, readErr)
	}
}

func TestOutlineAllExactPacketRequiresTheExactLeadingPolicyLine(t *testing.T) {
	for _, mode := range []string{"legacy_unmarked", "marker_inside_source"} {
		t.Run(mode, func(t *testing.T) {
			st := outlineAllExactFixtureStore(t)
			binary, captures := outlineAllExactFakeCLI(t)
			payload := outlineAllOperationTask(t, 29, 3, 3, 14) + "\n"
			if mode == "marker_inside_source" {
				payload += "只读来源中的协议示例，不是宿主首行：\n" + outlineAllExactFixturePolicy
			}
			payload += strings.Repeat("legacy-left-", 3500) + "LEGACY-MIDDLE-NOT-PROTECTED" + strings.Repeat("legacy-right-", 3500)
			realTool := tools.NewSaveFoundationTool(st)
			saves := 0
			save := agentcore.NewFuncTool(realTool.Name(), realTool.Description(), realTool.Schema(), func(context.Context, json.RawMessage) (json.RawMessage, error) {
				saves++
				return json.RawMessage(`{"saved":true,"outline_all":true,"type":"expand_arc"}`), nil
			})
			model := llmcodex.New(binary, "outline-exact-fixture", "", llmcodex.WithContextWindow(272000))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := runOutlineAllOperationWithModel(ctx, bootstrap.Config{}, assets.Bundle{}, st, payload,
				outlineAllOperationModel{ChatModel: model, Provider: "codex-cli", Name: "outline-exact-fixture"}, save); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(captures, "prompt-1"))
			if err != nil {
				t.Fatal(err)
			}
			if saves != 1 || strings.Contains(string(raw), "LEGACY-MIDDLE-NOT-PROTECTED") || strings.Contains(string(raw), "exact_agent_packet") {
				t.Fatalf("unmarked/ordinary source text changed legacy transport: saves=%d full_middle=%t exact=%t", saves,
					strings.Contains(string(raw), "LEGACY-MIDDLE-NOT-PROTECTED"), strings.Contains(string(raw), "exact_agent_packet"))
			}
			if _, err := os.Stat(filepath.Join(captures, "prompt-2")); !os.IsNotExist(err) {
				t.Fatal("legacy exact save started another CLI call")
			}
		})
	}
}

// The input is the separately generated operation prompt, never a book path.
// The opt-in probe that produced it verifies the real sources against the
// complete context builder; this checks the final provider serialization.
func TestOutlineAllExactPacketCurrentFoundationPrompt(t *testing.T) {
	inputPath := os.Getenv("OUTLINE_FOUNDATION_PROMPT_INPUT")
	if inputPath == "" {
		t.Skip("set OUTLINE_FOUNDATION_PROMPT_INPUT to the read-only complete operation prompt artifact")
	}
	original, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(original)
	if !strings.HasPrefix(prompt, outlineAllExactFixturePolicy) {
		t.Fatal("input artifact lacks the complete-foundation policy first line")
	}
	intent, err := domain.ParseOutlineAllIntent(prompt)
	if err != nil || intent.Type != domain.OutlineAllActionPlanStructure {
		t.Fatalf("input must be the actual plan_structure operation prompt: %v", err)
	}
	_, contextJSON, ok := strings.Cut(prompt, "MODEL_VISIBLE_CONTEXT:\n")
	if !ok {
		t.Fatal("input artifact lacks actual model-visible context")
	}
	type completeSource struct {
		Text          string `json:"text"`
		OriginalBytes int    `json:"original_bytes"`
		Truncated     bool   `json:"truncated"`
	}
	var visible struct {
		Foundation struct {
			Characters  completeSource            `json:"characters"`
			WorldRules  completeSource            `json:"world_rules"`
			BookWorld   completeSource            `json:"book_world"`
			Authorities map[string]completeSource `json:"authorities"`
		} `json:"foundation"`
	}
	if err := json.NewDecoder(strings.NewReader(contextJSON)).Decode(&visible); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		source completeSource
		marker string
	}{
		{visible.Foundation.Characters, "唐若宁"},
		{visible.Foundation.WorldRules, "秘密来源同步与现实处置边界"},
		{visible.Foundation.BookWorld, "澜汀市第二医院"},
	} {
		if item.source.Truncated || len(item.source.Text) != item.source.OriginalBytes || !strings.Contains(item.source.Text, item.marker) {
			t.Fatalf("input artifact lacks complete factual source %s", item.marker)
		}
	}
	for _, name := range []string{store.AuthorSourcesPath, "world_codex.json"} {
		source, present := visible.Foundation.Authorities[name]
		if !present || source.Text == "" || source.Truncated || len(source.Text) != source.OriginalBytes {
			t.Fatalf("input artifact lacks complete authority %s", name)
		}
	}

	st := outlineAllExactFixtureStore(t)
	binary, captures := outlineAllExactFakeCLI(t)
	t.Setenv("OUTLINE_EXACT_FIXTURE_RESPONSE", `{"action":"tool_call","tool_name":"save_foundation","arguments_json":"{\"type\":\"plan_structure\",\"content\":[]}","text":null}`)
	realTool := tools.NewSaveFoundationTool(st)
	saves := 0
	save := agentcore.NewFuncTool(realTool.Name(), realTool.Description(), realTool.Schema(), func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		saves++
		if string(args) != `{"type":"plan_structure","content":[]}` {
			t.Errorf("fixture emitted a different operation: %s", args)
		}
		return json.RawMessage(`{"saved":true,"outline_all":true,"type":"plan_structure"}`), nil
	})
	bundle := assets.Load("default")
	model := llmcodex.New(binary, "gpt-6-astra", "", llmcodex.WithContextWindow(272000))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := runOutlineAllOperationWithModel(ctx, bootstrap.Config{}, bundle, st, prompt,
		outlineAllOperationModel{ChatModel: model, Provider: "codex-cli", Name: "gpt-6-astra"}, save); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(filepath.Join(captures, "prompt-1"))
	if err != nil {
		t.Fatal(err)
	}
	input := string(captured)
	if saves != 1 || strings.Count(input, prompt) != 1 || strings.Contains(input, "Codex 入参压缩") {
		t.Fatalf("final CLI input lost or duplicated the real full-source operation: saves=%d prompt_occurrences=%d", saves, strings.Count(input, prompt))
	}
	parameters, err := json.Marshal(realTool.Schema())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{bundle.Prompts.ArchitectLong, outlineAllOperationSystemBoundary, string(parameters), "唐若宁", "秘密来源同步与现实处置边界", "澜汀市第二医院", store.AuthorSourcesPath, "world_codex.json"} {
		if !strings.Contains(input, required) {
			t.Fatal("final CLI input lost a source fact, authority, actual system or schema")
		}
	}
	if _, err := os.Stat(filepath.Join(captures, "prompt-2")); !os.IsNotExist(err) {
		t.Fatal("transport fixture dispatched after the acknowledged save")
	}
	after, err := os.ReadFile(inputPath)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("read-only prompt artifact changed: %v", err)
	}
	t.Logf("real_operation_prompt_bytes=%d real_operation_prompt_runes=%d final_cli_input_bytes=%d final_cli_input_runes=%d complete_prompt_occurrences=1 author_sources_bytes=%d world_codex_bytes=%d configured_context=272000 real_provider_calls=0",
		len(original), utf8.RuneCount(original), len(captured), utf8.RuneCount(captured),
		len(visible.Foundation.Authorities[store.AuthorSourcesPath].Text), len(visible.Foundation.Authorities["world_codex.json"].Text))
}
