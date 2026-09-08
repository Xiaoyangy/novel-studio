package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

func diagnosticTestResponse(id, args string) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleAssistant, StopReason: agentcore.StopReasonToolUse,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("MODEL_PRIVATE_TEXT_NOT_A_DIAGNOSTIC"), agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: "submit_character_decision", Args: json.RawMessage(args)})},
		Usage:   &agentcore.Usage{Input: 100, Output: 20}, Metadata: map[string]any{"reasoning": "PRIVATE_REASONING_NOT_A_DIAGNOSTIC"}}
}

func TestArbiterSingleTurnDiagnosticStopsWithoutChangingInputOrUsage(t *testing.T) {
	t.Setenv("NOVEL_STUDIO_ARBITER_SINGLE_TURN", "1")
	if characterArbiterDiagnosticTurnLimit("submit_character_decision", 6) != 6 || characterArbiterDiagnosticTurnLimit("resolve_chapter_world", 1) != 1 {
		t.Fatal("diagnostic cap changed another role or raised a bound")
	}
	st := store.NewStore(t.TempDir())
	response := diagnosticTestResponse("rejected", `{"decision":"retry"}`)
	response.Content[1].ToolCall.Name = "resolve_chapter_world"
	model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{response, response}}
	tool := agentcore.NewFuncTool("resolve_chapter_world", "diagnostic probe", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("EXACT_PRIVATE_REJECTION")
	})
	usage, err := runCharacterAgentTerminalLoop(context.Background(), model, "UNCHANGED_SYSTEM", "UNCHANGED_INPUT", tool, tool.Name(), 6, "", nil, "same-cache-key", st)
	if err == nil || model.calls != 1 || usage.Attempts != 1 || usage.Input != 100 || usage.Output != 20 {
		t.Fatalf("single-turn diagnostic repeated or lost a paid call: err=%v calls=%d usage=%+v", err, model.calls, usage)
	}
	rows, _ := readPrivateDiagnostics(t, st)
	if len(rows) != 1 || rows[0].ErrorText != "EXACT_PRIVATE_REJECTION" {
		t.Fatal("single-turn diagnostic lost the original rejection")
	}
}

func readPrivateDiagnostics(t *testing.T, st *store.Store) ([]store.CharacterToolDiagnostic, []byte) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(st.Dir(), store.CharacterToolDiagnosticDirectory, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.CharacterToolDiagnostic
	var all []byte
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, raw...)
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
			var row store.CharacterToolDiagnostic
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row)
		}
	}
	return rows, all
}

func TestCharacterTerminalDiagnosticsKeepRejectionWithoutChangingRetryOrUsage(t *testing.T) {
	for _, kind := range []string{"schema", "business", "disabled", "io-failure"} {
		t.Run(kind, func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if kind == "io-failure" {
				path := filepath.Join(st.Dir(), store.CharacterToolDiagnosticDirectory)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("PRIVATE_IO_BLOCKER"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			firstArgs := `{"decision":"retry","private_note":"PRIVATE_MODEL_ARGS"}`
			if kind == "schema" {
				firstArgs = `{"private_note":"PRIVATE_MODEL_ARGS"}`
			}
			model := &outlineAllOperationCaptureModel{responses: []agentcore.Message{diagnosticTestResponse("first", firstArgs), diagnosticTestResponse("second", `{"decision":"ok"}`)}}
			calls, saved := 0, false
			tool := agentcore.NewFuncTool("submit_character_decision", "diagnostic test", map[string]any{"type": "object", "properties": map[string]any{"decision": map[string]any{"type": "string"}, "private_note": map[string]any{"type": "string"}}, "required": []string{"decision"}}, func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
				calls++
				if strings.Contains(string(args), "retry") {
					return nil, errors.New("PRIVATE_VALIDATOR_VALUE: precise host precondition rejection")
				}
				saved = true
				return json.RawMessage(`{"submitted":true}`), nil
			})
			ctx := withCharacterToolDiagnosticScope(context.Background(), domain.CharacterAgentUsage{GenerationID: "pg2_diagnostic", Chapter: 1, Cycle: 3, Round: 1, AgentID: "ca_owner", Role: "character", UsageID: "usage-scope"})
			var sinks []*store.Store
			if kind != "disabled" {
				sinks = []*store.Store{st}
			}
			usage, err := runCharacterAgentTerminalLoop(ctx, model, "UNCHANGED SYSTEM", "PRIVATE_INPUT_PACKET", tool, tool.Name(), 4, "", nil, "diagnostic-cache-key", sinks...)
			if err != nil || model.calls != 2 || !saved || usage.Attempts != 2 || usage.Input != 200 || usage.Output != 40 {
				t.Fatalf("diagnostic changed paid outcome/retry/usage: %v calls=%d saved=%t usage=%+v", err, model.calls, saved, usage)
			}
			if kind == "schema" && calls != 1 {
				t.Fatal("schema rejection was not observed before Execute")
			}
			if kind == "io-failure" {
				if !strings.Contains(logs.String(), "private_diagnostic_write_failed") {
					t.Fatal("diagnostic loss was silent")
				}
				for _, secret := range []string{"PRIVATE_VALIDATOR_VALUE", "PRIVATE_IO_BLOCKER", "PRIVATE_MODEL_ARGS", st.Dir()} {
					if strings.Contains(logs.String(), secret) {
						t.Fatal("diagnostic IO warning leaked private data")
					}
				}
				return
			}
			rows, raw := readPrivateDiagnostics(t, st)
			if kind == "disabled" {
				if len(rows) != 0 {
					t.Fatal("legacy no-store call wrote diagnostics")
				}
				return
			}
			if len(rows) != 1 || rows[0].Sequence != 1 || rows[0].AgentID != "ca_owner" || rows[0].Cycle != 3 || rows[0].UsageID != "usage-scope" || rows[0].ToolCallID != "first" {
				t.Fatalf("rejection lost scope or duplicated successful tool result: %+v", rows)
			}
			if kind == "business" && rows[0].ErrorText != "PRIVATE_VALIDATOR_VALUE: precise host precondition rejection" {
				t.Fatal("specific Host rejection was not retained privately")
			}
			for _, secret := range []string{"PRIVATE_MODEL_ARGS", "MODEL_PRIVATE_TEXT_NOT_A_DIAGNOSTIC", "PRIVATE_REASONING_NOT_A_DIAGNOSTIC", "PRIVATE_INPUT_PACKET", `"args"`, `"reasoning"`} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatalf("transcript/arguments entered diagnostic: %s", secret)
				}
			}
			if _, err := os.Stat(filepath.Join(st.Dir(), "meta/usage.json")); !os.IsNotExist(err) {
				t.Fatal("diagnostic wrote the monetary ledger")
			}
			if _, err := os.Stat(filepath.Join(st.Dir(), "meta/character_agents")); !os.IsNotExist(err) {
				t.Fatal("diagnostic became character evidence/memory")
			}
		})
	}
}

func TestCharacterToolDiagnosticObserverFiltersAndSeparatesConcurrentLoops(t *testing.T) {
	st := store.NewStore(t.TempDir())
	var wg sync.WaitGroup
	for _, actor := range []string{"ca_one", "ca_two"} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			ctx := withCharacterToolDiagnosticScope(context.Background(), domain.CharacterAgentUsage{AgentID: actor, Chapter: 1, Cycle: 2})
			observer := newCharacterToolDiagnosticObserver(ctx, "submit_character_decision", []*store.Store{st})
			observer.observe(agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "other_tool", IsError: true, Result: json.RawMessage(`"ignored"`)})
			observer.observe(agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "submit_character_decision", IsError: false, Result: json.RawMessage(`"success"`)})
			observer.observe(agentcore.Event{Type: agentcore.EventMessageUpdate, Tool: "submit_character_decision", IsError: true, Delta: "PRIVATE_COT_DELTA"})
			for i := 0; i < 2; i++ {
				observer.observe(agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "submit_character_decision", ToolID: "only-terminal-errors", IsError: true, Result: json.RawMessage(`"precise error"`), Args: json.RawMessage(`{"secret":"DO_NOT_STORE_ARGS"}`)})
			}
		}(actor)
	}
	wg.Wait()
	rows, raw := readPrivateDiagnostics(t, st)
	byLoop := map[string][]store.CharacterToolDiagnostic{}
	for _, row := range rows {
		byLoop[row.LoopID] = append(byLoop[row.LoopID], row)
	}
	if len(rows) != 4 || len(byLoop) != 2 || bytes.Contains(raw, []byte("DO_NOT_STORE")) || bytes.Contains(raw, []byte("PRIVATE_COT")) {
		t.Fatalf("diagnostic filter/isolation failed: rows=%d loops=%d", len(rows), len(byLoop))
	}
	for _, records := range byLoop {
		if len(records) != 2 || records[0].Sequence != 1 || records[1].Sequence != 2 || records[0].AgentID != records[1].AgentID {
			t.Fatal("concurrent loops mixed identities or sequences")
		}
	}
}

func TestCharacterTerminalDiagnosticsRetainDeliveredRejectionAfterCancellation(t *testing.T) {
	st := store.NewStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = withCharacterToolDiagnosticScope(ctx, domain.CharacterAgentUsage{AgentID: "ca_canceled", Chapter: 1, Cycle: 2})
	model := &outlineAllOperationCaptureModel{response: diagnosticTestResponse("canceled-tool", `{"decision":"check"}`)}
	tool := agentcore.NewFuncTool("submit_character_decision", "cancel boundary", map[string]any{"type": "object"}, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		cancel()
		return nil, errors.New("specific delivered rejection at cancellation")
	})
	usage, err := runCharacterAgentTerminalLoop(ctx, model, "system", "private input", tool, tool.Name(), 4, "", nil, "cancel-diagnostic", st)
	if !errors.Is(err, context.Canceled) || model.calls != 1 || usage.Attempts != 1 || usage.Input != 100 {
		t.Fatalf("diagnostic changed cancellation or paid usage: %v calls=%d usage=%+v", err, model.calls, usage)
	}
	rows, _ := readPrivateDiagnostics(t, st)
	if len(rows) != 1 || rows[0].ErrorText != "specific delivered rejection at cancellation" || rows[0].ToolCallID != "canceled-tool" {
		t.Fatalf("delivered terminal rejection lost on cancellation: %+v", rows)
	}
}
