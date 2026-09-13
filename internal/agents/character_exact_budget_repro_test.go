package agents

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

// Opt-in, read-only replay of one immutable failed packet. The subprocess is a
// local fake; this test never creates a model or writes to the novel workspace.
func TestExactBudgetFrozenCycle20ReadOnlyReplay(t *testing.T) {
	path := os.Getenv("NOVEL_EXACT_BUDGET_OBSERVATION")
	if path == "" {
		t.Skip("set the explicit frozen C20 Zhou observation path for read-only replay")
	}
	raw, err := os.ReadFile(path)
	selectionMust(t, err)
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != "320d4b962980b040c4853042d71d4ac2e4ddb12b289796f81cbaba8c12dfc81d" {
		t.Fatal("replay input is not the exact failed observation")
	}
	var observation domain.CharacterObservationPacket
	selectionMust(t, json.Unmarshal(raw, &observation))
	if observation.CycleContext == nil || observation.CycleContext.Index != 20 || !domain.HasCharacterSurfaceInspectionPolicyV1(observation.Sources) || domain.HasCharacterIncomingMaterialReadPolicyV1(observation.Sources) {
		t.Fatal("replay requires the original frozen surface producer")
	}
	codec, err := modelinput.NewScopedArtifactReferenceCodecV1(modelinput.KindCharacterObservation, observation)
	selectionMust(t, err)
	body, err := json.Marshal(codec.ModelView())
	selectionMust(t, err)
	tool := tools.NewSubmitCharacterDecisionTool(nil, observation)
	// This is the exact old producer's actor prompt composition. The measured
	// bytes/runes and old estimate below pin it to the original failure log.
	system := characterAgentSystemPromptV2 + characterSelfExperiencePromptV2 + characterOperationalAvailabilityPromptV1 + characterWorkArtifactPromptV1 + characterResourceObservationTimePromptV1 + characterCarryAPIHelpV1 + characterSelfCompletionViewPromptV1 + characterSurfaceInspectionPromptV1 + scopedReferencePrompt + characterWorkContinuationPrompt
	message, err := modelinput.NewExactAgentPacketMessage(modelinput.KindCharacterObservation, "你是 "+observation.Character+"。这是你唯一可见的观察包：\n<character_observation_packet>\n"+string(body)+"\n</character_observation_packet>\n现在只调用 submit_character_decision。")
	selectionMust(t, err)
	dir := t.TempDir()
	capture, schemaCapture := filepath.Join(dir, "prompt"), filepath.Join(dir, "schema")
	t.Setenv("NOVEL_BUDGET_CAPTURE", capture)
	t.Setenv("NOVEL_BUDGET_SCHEMA_CAPTURE", schemaCapture)
	binary := filepath.Join(dir, "fake-codex")
	script := `#!/bin/sh
set -eu
if [ "$1" = mcp ]; then printf '[]'; exit 0; fi
out=''; schema=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output-last-message) shift; out="$1";;
    --output-schema) shift; schema="$1";;
  esac
  shift
done
cat > "$NOVEL_BUDGET_CAPTURE"
cp "$schema" "$NOVEL_BUDGET_SCHEMA_CAPTURE"
printf '%s' '{"action":"final","tool_name":null,"arguments_json":null,"text":"local budget replay only"}' > "$out"
`
	selectionMust(t, os.WriteFile(binary, []byte(script), 0700))
	model := llmcodex.New(binary, "gpt-6-astra", "medium", llmcodex.WithContextWindow(272000))
	_, err = model.Generate(context.Background(), []agentcore.Message{agentcore.SystemMsg(system), message}, []agentcore.ToolSpec{{Name: tool.Name(), Description: tool.Description(), Parameters: tool.Schema()}})
	selectionMust(t, err)
	got, err := os.ReadFile(capture)
	selectionMust(t, err)
	schema, err := os.ReadFile(schemaCapture)
	selectionMust(t, err)
	bytes, runes := len(got)+len(schema), utf8.RuneCount(got)+utf8.RuneCount(schema)
	oldEstimate := corecontext.EstimateTokens(agentcore.UserMsg(string(got))) + corecontext.EstimateTokens(agentcore.UserMsg(string(schema)))
	oldGuard := oldEstimate + (oldEstimate+3)/4
	if bytes != 264250 || runes != 123110 || oldEstimate != 184194 || oldGuard != 230243 {
		t.Fatalf("reconstruction differs from actual failed boundary: bytes=%d runes=%d old=%d guarded=%d", bytes, runes, oldEstimate, oldGuard)
	}
	if strings.Count(string(got), string(body)) != 1 || !strings.Contains(string(got), system) || strings.Contains(string(got), "Codex 入参压缩") {
		t.Fatal("passing the budget lost or duplicated the original full packet/system")
	}
	after, err := os.ReadFile(path)
	selectionMust(t, err)
	if string(after) != string(raw) {
		t.Fatal("read-only replay changed the frozen input")
	}
	t.Logf("exact original failed packet preserved: bytes=%d runes=%d old_estimate=%d old_guard=%d same_context=272000 fake_cli_reached=true real_provider_calls=0", bytes, runes, oldEstimate, oldGuard)
}
