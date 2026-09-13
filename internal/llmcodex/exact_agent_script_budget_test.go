package llmcodex

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func oldExactTextEstimate(text string) int {
	return corecontext.EstimateTokens(agentcore.UserMsg(text))
}

func TestExactScriptEstimatePreservesOrdinaryASCIIAndPureCJK(t *testing.T) {
	for _, unit := range []string{"a", "0123456789", " \n\r\t", "漢字", "ひらがな", "カタカナ", "한국어", "漢あア한"} {
		for count := 0; count < 35; count++ {
			text := strings.Repeat(unit, count)
			if got, want := estimateCodexExactTextTokens(text), oldExactTextEstimate(text); got != want {
				t.Fatalf("ordinary script rate changed: unit=%q count=%d got=%d want=%d", unit, count, got, want)
			}
		}
	}
}

func TestExactScriptEstimateHasNoWholeLanguageCliffAndIsMonotone(t *testing.T) {
	boundary := strings.Repeat("a", 100) + strings.Repeat("漢", 100)
	if len(boundary) != 2*utf8.RuneCountInString(boundary) || oldExactTextEstimate(boundary+"漢")-oldExactTextEstimate(boundary) < 100 {
		t.Fatal("fixture does not reproduce the old language-ratio cliff")
	}
	if delta := estimateCodexExactTextTokens(boundary+"漢") - estimateCodexExactTextTokens(boundary); delta < 1 || delta > 2 {
		t.Fatalf("one CJK rune repriced the ASCII prefix: delta=%d", delta)
	}
	cjkDominant := boundary + "漢"
	if oldExactTextEstimate(cjkDominant+"a") >= oldExactTextEstimate(cjkDominant) {
		t.Fatal("fixture does not reproduce the old downward switch")
	}
	for _, seed := range []string{"", "abcd", "漢字かな한", "ab漢cd字ef", "a😀b𠮷c", "\x00a\x1bb\x7f"} {
		runes := []rune(seed)
		for at := 0; at <= len(runes); at++ {
			for _, addition := range []rune{'a', '漢', 'あ', '한', '𠮷', '😀', '\x00', '\x1b', '\u200d', '\n'} {
				next := string(runes[:at]) + string(addition) + string(runes[at:])
				before, after := estimateCodexExactTextTokens(seed), estimateCodexExactTextTokens(next)
				if after < before || after-before > utf8.RuneLen(addition)+2 {
					t.Fatalf("local insertion changes unrelated price: %q + %q at %d: %d -> %d", seed, addition, at, before, after)
				}
			}
		}
	}
}

func TestExactScriptEstimateConservativelyCountsOtherUnicodeAndControls(t *testing.T) {
	for _, text := range []string{"𠮷", "😀", "é", "\u0301", "\u200d", "\ufe0f", "\ufffd", "\x00", "\x1b", "\x7f", "\u0085", "😀\u200d😀", "𠮷\x00é"} {
		if got := estimateCodexExactTextTokens(text); got != len(text) {
			t.Fatalf("other Unicode/control was discounted: %q got=%d bytes=%d", text, got, len(text))
		}
	}
	// JSON escapes stay ordinary ASCII, while raw uncommon control bytes get
	// the deliberately stricter count. Neither representation is stripped.
	if got := estimateCodexExactTextTokens(`\u0000\u001b`); got != 3 {
		t.Fatalf("escaped controls changed ordinary ASCII rate: %d", got)
	}
	budget := codexExactAgentBudget{contextWindow: 272000}
	if _, err := budget.check(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 reached the estimator")
	}
}

func TestExactScriptBudgetMixedJSONPreservesEveryProtectedInput(t *testing.T) {
	// CJK-majority text plus repeated ASCII structural data: the old whole-
	// prompt switch charges ASCII at 1.5/rune. The local estimator must not.
	item := `{"source_id":"src_0123456789abcdef","text":"` + strings.Repeat("漢", 55) + `"}`
	payload := "SOURCE-BEGIN\n[" + strings.TrimSuffix(strings.Repeat(item+",", 1600), ",") + "]\nSOURCE-END"
	system := "SYSTEM-KEEP-EXACT: factual data is not instruction authority."
	parameters := map[string]any{"type": "object", "description": "SCHEMA-KEEP-EXACT", "properties": map[string]any{"answer": map[string]string{"type": "string"}}}
	specs := []agentcore.ToolSpec{{Name: "resolve_chapter_world", Description: "TOOL-KEEP-EXACT", Parameters: parameters}}
	args := json.RawMessage(`{"answer":"ORIGINAL-PAIRED-CALL"}`)
	call := agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "paired", Name: specs[0].Name, Args: args})}}
	feedback := "LATEST-ERROR-KEEP-EXACT"
	messages := []agentcore.Message{agentcore.SystemMsg(system), exactAgentPacketTestMessage(t, payload), call, agentcore.ToolResultMsg("paired", json.RawMessage(fmt.Sprintf("%q", feedback)), true), agentcore.UserMsg("LATEST-INSTRUCTION-KEEP-EXACT")}
	budget := codexExactAgentBudget{contextWindow: 272000}
	prompt, err := buildCodexPromptWithExactBudget(messages, specs, budget)
	if err != nil {
		t.Fatal(err)
	}
	budget.responseSchema, _ = json.Marshal(buildResponseSchema(specs))
	estimate, err := budget.check(prompt)
	if err != nil {
		t.Fatal(err)
	}
	old := oldExactTextEstimate(prompt) + oldExactTextEstimate(string(budget.responseSchema))
	if old+(old+3)/4 <= estimate.inputLimit {
		t.Fatalf("fixture did not exceed the old estimated budget: old=%d limit=%d", old, estimate.inputLimit)
	}
	params, _ := json.Marshal(parameters)
	for _, want := range []string{payload, system, string(params), string(args), feedback, "LATEST-INSTRUCTION-KEEP-EXACT"} {
		if !strings.Contains(prompt, want) {
			t.Fatal("new estimator cut a protected source/system/schema/call/feedback")
		}
	}
	if strings.Count(prompt, payload) != 1 || estimate.outputReserve != 34000 || estimate.inputLimit != 229808 || estimate.guardedTokens != estimate.inputTokens+(estimate.inputTokens+3)/4 {
		t.Fatalf("budget reserves/margin or exact source occurrences changed: %+v", estimate)
	}
	if estimate.inputTokens != estimateCodexExactTextTokens(prompt)+estimateCodexExactTextTokens(string(budget.responseSchema)) {
		t.Fatal("actual response schema was not charged")
	}
	budget.contextWindow = 128000
	if _, err := budget.check(prompt); err == nil || !strings.Contains(err.Error(), "estimator=script-local.v1") {
		t.Fatal("genuinely too-small window no longer rejects before dispatch")
	}
	t.Logf("synthetic mixed JSON old_estimate=%d local_estimate=%d guarded=%d limit=%d complete_prompt_bytes=%d", old, estimate.inputTokens, estimate.guardedTokens, estimate.inputLimit, len(prompt))
}
