package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

// Reads real frozen inputs but executes ONLY a temporary fake CLI. Never run
// the business loop against a real Store with a fake provider: its accounting
// hooks would otherwise contaminate the production ledger.
func TestCharacterArbiterLiveExactBudgetReadOnly(t *testing.T) {
	dir := os.Getenv("NOVEL_ARBITER_BUDGET_OUTPUT")
	generation := os.Getenv("NOVEL_ARBITER_BUDGET_GENERATION")
	if dir == "" || generation == "" {
		t.Skip("set NOVEL_ARBITER_BUDGET_OUTPUT and GENERATION for read-only frozen arbitration replay")
	}
	before, err := store.DirectoryContentRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(dir)
	prefix, err := st.LoadVerifiedCharacterActivationPrefix(generation, 1)
	if err != nil || prefix == nil {
		t.Fatalf("read verified current prefix: %v", err)
	}
	session := prefix.Session()
	if session.Phase != "collecting" {
		t.Fatal("fixture must be stopped before a missing arbitration")
	}
	proofs, err := st.CharacterAgents.ForActivationCycle(session)
	if err != nil {
		t.Fatal(err)
	}
	input, err := proofs.LoadActivationInputs()
	if err != nil || input == nil {
		t.Fatalf("read frozen input: %v", err)
	}
	ids := activeCharacterAgentIDs(input.Activation)
	proposals, err := proofs.LoadLatestProposals(generation, 1, 1, ids)
	if err != nil || len(proposals) != len(ids) {
		t.Fatalf("this fixture expects a complete fresh proposal set, got=%d want=%d err=%v", len(proposals), len(ids), err)
	}
	inputs := activationInputsForExecution(*input, session)
	tool := tools.NewResolveChapterWorldTool(nil, input.Stimulus, input.Activation, proposals, characterActivationProtocolForPolicy(characterActivationPolicyForStimulus(input.Stimulus)), input.Stimulus.Sources, 1)
	system, user, execution, err := prepareCharacterArbitrationRequest(inputs, proposals, tool)
	if err != nil {
		t.Fatal(err)
	}
	message, err := modelinput.NewExactAgentPacketMessage(modelinput.KindWorldArbitration, user)
	if err != nil {
		t.Fatal(err)
	}
	messages := []agentcore.Message{agentcore.SystemMsg(system), message}
	specs := []agentcore.ToolSpec{{Name: execution.Name(), Description: execution.Description(), Parameters: execution.Schema()}}
	tmp := t.TempDir()
	cli, captured := filepath.Join(tmp, "fake-codex"), filepath.Join(tmp, "stdin.txt")
	script := `#!/bin/sh
set -eu
if [ "$1" = 'mcp' ]; then
  printf '%s' '[]'
  exit 0
fi
out=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output-last-message) shift; out="$1" ;;
  esac
  shift
done
cat > '` + captured + `'
printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"local budget replay"}' > "$out"
`
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := llmcodex.New(cli, "gpt-6-astra", "high")
	if _, err := legacy.Generate(context.Background(), messages, specs); err == nil || !strings.Contains(err.Error(), "90000") {
		t.Fatalf("expected the actual legacy rune-limit failure, got %v", err)
	}
	if _, err := os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("legacy rejection launched the fake CLI")
	}
	model := llmcodex.New(cli, "gpt-6-astra", "high", llmcodex.WithContextWindow(272000))
	response, err := model.Generate(context.Background(), messages, specs)
	if err != nil || response == nil || response.Message.TextContent() != "local budget replay" {
		t.Fatalf("configured window failed the complete local replay: %v", err)
	}
	stdin, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := json.Marshal(execution.Schema())
	if err != nil || !strings.Contains(string(stdin), user) || !strings.Contains(string(stdin), system) || !strings.Contains(string(stdin), string(parameters)) || strings.Contains(string(stdin), "Codex 入参压缩") {
		t.Fatal("configured replay clipped source/system/schema or omitted exact data")
	}
	after, err := store.DirectoryContentRoot(dir)
	if err != nil || before != after {
		t.Fatal("read-only budget replay modified real evidence/accounting")
	}
	t.Logf("cycle=%d actors=%d complete_prompt_runes=%d complete_prompt_bytes=%d configured_window=272000 fake_cli_only=true real_store_unchanged=true; these are not reported provider tokens", len(session.CycleDigests)+1, len(proposals), utf8.RuneCount(stdin), len(stdin))
}
