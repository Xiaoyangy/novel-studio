package agents

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/tools"
	"github.com/voocel/agentcore"
)

func TestInitialWorldTickRetryFeedbackReachesArchitectProvider(t *testing.T) {
	st, messages, _ := initialTickTransportFixture(t)
	const rejection = "[initial world_tick 上次质量拒绝]\ninitial world_tick 留有未解决 warning: existing warning"
	binary, capture, _ := initialTickFakeCodex(t)
	// The Coordinator omitted every feedback byte from the delegated task.
	// The host transport must still deliver the real rejection exactly once.
	model := withInitialWorldTickTransport(llmcodex.New(binary, "fixture", "", llmcodex.WithContextWindow(272000)), st, rejection)
	save := tools.NewSaveWorldTickTool(st)
	specs := []agentcore.ToolSpec{{Name: save.Name(), Description: save.Description(), Parameters: save.Schema()}}
	if _, err := model.Generate(context.Background(), messages, specs); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), rejection); got != 1 {
		t.Fatalf("actual provider lost/duplicated rejection: count=%d", got)
	}
	if !strings.Contains(string(raw), "FIRST-CHAPTER-EXACT-CORE") || !strings.Contains(string(raw), "FIRST-CHAPTER-EXACT-HOOK") {
		t.Fatal("retry feedback changed chapter contract")
	}
	// A compliant delegation already carries the exact feedback, so the host
	// must not add a second token-identical block.
	messages[1] = agentcore.UserMsg(messages[1].TextContent() + "\n" + rejection)
	if _, err := model.Generate(context.Background(), messages, specs); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), rejection); got != 1 {
		t.Fatalf("compliant delegation duplicated feedback: count=%d", got)
	}
}
