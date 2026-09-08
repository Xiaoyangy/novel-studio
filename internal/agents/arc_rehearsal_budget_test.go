package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

func TestArcRehearsalExactTransportUsesConfiguredBudgetWithoutClipping(t *testing.T) {
	dir := t.TempDir()
	binary, captured := filepath.Join(dir, "fake-codex"), filepath.Join(dir, "input")
	t.Setenv("ARC_REHEARSAL_TEST_CAPTURE", captured)
	script := `#!/bin/sh
set -eu
if [ "$1" = mcp ]; then printf '[]'; exit 0; fi
out=''
while [ "$#" -gt 0 ]; do
  case "$1" in -o|--output-last-message) shift; out="$1";; esac
  shift
done
cat > "$ARC_REHEARSAL_TEST_CAPTURE"
printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"transport only"}' > "$out"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	text := "EXACT-START" + strings.Repeat("source-data-", 10000) + "EXACT-END"
	packet, err := modelinput.NewExactAgentPacketMessage(modelinput.KindArcRehearsal, text)
	if err != nil {
		t.Fatal(err)
	}
	tool := &submitArcRehearsalTool{}
	specs := []agentcore.ToolSpec{{Name: tool.Name(), Description: tool.Description(), Parameters: tool.Schema()}}
	messages := []agentcore.Message{agentcore.SystemMsg(arcRehearsalPrompt), packet}
	withoutWindow := llmcodex.New(binary, "test-rehearsal", "")
	if _, err := withoutWindow.Generate(context.Background(), messages, specs); err == nil || !strings.Contains(err.Error(), "90000") {
		t.Fatalf("unknown window must retain its conservative guard: %v", err)
	}
	if _, err := os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("budget rejection launched a provider subprocess")
	}
	model := llmcodex.New(binary, "test-rehearsal", "", llmcodex.WithContextWindow(272000))
	if _, err := model.Generate(context.Background(), messages, specs); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	parameters, _ := json.Marshal(tool.Schema())
	if strings.Count(string(got), text) != 1 || !strings.Contains(string(got), arcRehearsalPrompt) || !strings.Contains(string(got), string(parameters)) || strings.Contains(string(got), "Codex 入参压缩") {
		t.Fatal("exact rehearsal lost input, system instructions or tool schema")
	}
}
