package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/llmcodex"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

func TestActualChapterTwoGroundingFitsUnchangedBudget(t *testing.T) {
	path := os.Getenv("NOVEL_GROUNDING_INPUT_CAPTURE")
	if path == "" {
		t.Skip("actual captured input required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var input domain.PlanGroundingInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	binary, calls, capture, schemaCapture := groundingBudgetFakeCLI(t)
	model := llmcodex.New(binary, "grounding-actual-offline", "high", llmcodex.WithContextWindow(272000))
	_, err = runPlanGroundingReview(t.Context(), model, agentcore.ThinkingHigh, input)
	if err != nil {
		var budget *PlanGroundingInputBudgetError
		if !errors.As(err, &budget) {
			t.Fatalf("wrong failure: %v", err)
		}
		if _, statErr := os.Stat(calls); !os.IsNotExist(statErr) {
			t.Fatalf("pre-dispatch failure reached CLI: %v", statErr)
		}
		t.Fatalf("actual full grounding input cannot be delivered (zero CLI/provider calls): %v", err)
	}
	digest, err := domain.PlanGroundingInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, used, err := modelinput.EncodePlanGroundingModelViewV1(raw, digest)
	if err != nil || !used {
		t.Fatalf("no compact actual transport: %v", err)
	}
	decoded, err := modelinput.DecodePlanGroundingModelViewV1(encoded, digest)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.PlanGroundingInput
	if err := json.Unmarshal(decoded, &restored); err != nil {
		t.Fatal(err)
	}
	restoredRaw, _ := json.Marshal(restored)
	if !bytes.Equal(restoredRaw, raw) {
		t.Fatal("full original input changed")
	}
	prompt, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(prompt, encoded) != 1 || !bytes.Contains(prompt, []byte(planGroundingPrompt+activationGroundingPrompt)) {
		t.Fatal("full compact body/system not retained exactly once")
	}
	parameters, _ := json.Marshal(planGroundingToolSpec().Parameters)
	if !bytes.Contains(prompt, parameters) {
		t.Fatal("schema changed")
	}
	responseSchema, err := os.ReadFile(schemaCapture)
	if err != nil || !json.Valid(responseSchema) {
		t.Fatal("missing original output schema")
	}
	log, err := os.ReadFile(calls)
	if err != nil || string(log) != "inventory\nexec\n" {
		t.Fatalf("unexpected provider dispatches: %q %v", log, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatal("canonical source input changed")
	}
	t.Logf("canonical_bytes=%d encoded_bytes=%d encoded_runes=%d complete_CLI_bytes=%d complete_CLI_runes=%d", len(raw), len(encoded), utf8.RuneCount(encoded), len(prompt)+len(responseSchema), utf8.RuneCount(prompt)+utf8.RuneCount(responseSchema))
}
