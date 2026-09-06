package llmcodex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

// Replays captured pre-provider fixtures only; it never invokes a model or
// mutates a story artifact. Set the directory explicitly for private eval data.
func TestExactAgentPacketRealArbitrationFixtures(t *testing.T) {
	dir := os.Getenv("NOVEL_STUDIO_EXACT_PACKET_FIXTURE_DIR")
	if dir == "" {
		t.Skip("requires an explicit read-only captured fixture directory")
	}
	for _, chapter := range []int{1, 3} {
		t.Run(fmt.Sprint(chapter), func(t *testing.T) {
			path := filepath.Join(dir, fmt.Sprintf("c%d.json", chapter))
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var captured struct {
				Messages []agentcore.Message  `json:"messages"`
				Tools    []agentcore.ToolSpec `json:"tools"`
			}
			if err := json.Unmarshal(raw, &captured); err != nil {
				t.Fatal(err)
			}
			if len(captured.Messages) != 2 || captured.Messages[1].Role != agentcore.RoleUser {
				t.Fatal("unexpected source fixture shape")
			}
			user := captured.Messages[1].TextContent()
			legacy, err := buildCodexPromptChecked(captured.Messages, captured.Tools)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(legacy, user) {
				t.Fatal("fixture no longer exercises the old truncation defect")
			}
			captured.Messages[1], err = modelinput.NewExactAgentPacketMessage(modelinput.KindWorldArbitration, user)
			if err != nil {
				t.Fatal(err)
			}
			for _, retry := range []bool{false, true} {
				messages := append([]agentcore.Message(nil), captured.Messages...)
				feedback := `{"error":"保持所有角色真实前态，只修当前拒绝字段"}`
				if retry {
					messages = append(messages, agentcore.ToolResultMsg("rejected-resolution", json.RawMessage(feedback), true))
				}
				prompt, err := buildCodexPromptChecked(messages, captured.Tools)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(prompt, user) || strings.Count(prompt, user) != 1 || strings.Contains(prompt, "Codex 入参压缩") || utf8.RuneCountInString(prompt) > codexPromptRuneBudget {
					t.Fatal("real complete input was truncated, duplicated, or over budget")
				}
				if retry && !strings.Contains(prompt, feedback) {
					t.Fatal("repair feedback was lost")
				}
				start := strings.Index(prompt, "<world_arbitration_input>\n") + len("<world_arbitration_input>\n")
				end := strings.Index(prompt, "\n</world_arbitration_input>")
				if start < 0 || end < start || !json.Valid([]byte(prompt[start:end])) {
					t.Fatal("complete arbitration input is no longer valid JSON")
				}
				t.Logf("C%d retry=%t exact_user_runes=%d prompt_runes=%d fixture_sha256=%x", chapter, retry, utf8.RuneCountInString(user), utf8.RuneCountInString(prompt), sha256.Sum256(raw))
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("read-only fixture was changed")
			}
		})
	}
}
