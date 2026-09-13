package llmcodex

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

const (
	codexExactAbsoluteInputTokens = 262_144
	codexExactAbsoluteInputBytes  = 2 * 1024 * 1024
	codexExactOutputReserve       = 32_768
	codexExactCLIOverheadReserve  = 8_192
	codexExactOldHistoryTokens    = 8_192
)

type codexExactAgentBudget struct {
	contextWindow   int
	maxOutputTokens int
	responseSchema  []byte
}

type codexExactHistoryEntry struct {
	index int
	text  string
}

// These values are estimates/operating limits, never provider-reported usage.
// Exact packets use a script-local heuristic, not a model tokenizer: a single
// extra CJK rune must not reprice every ASCII JSON field in the whole packet.
// We retain the 25% margin, independent output/CLI room and absolute size caps.
type codexExactBudgetEstimate struct {
	inputTokens, guardedTokens, inputLimit int
	outputReserve, bytes, runes            int
}

// estimateCodexExactTextTokens keeps the prior rates for ordinary ASCII runs
// and common three-byte Han/Japanese/Korean runes without a whole-text language
// switch. Integer half-token units preserve the 1.5 CJK rate until final ceil.
// Uncommon Unicode and non-whitespace controls use one token per UTF-8 byte;
// this is deliberately conservative, including supplementary Han and emoji.
// This estimate is not provider usage. The SDK/global context estimator and
// the independent old-history allowance remain untouched.
func estimateCodexExactTextTokens(text string) int {
	asciiBytes, halfTokens := 0, 0
	flushASCII := func() {
		halfTokens += 2 * ((asciiBytes + 3) / 4)
		asciiBytes = 0
	}
	for _, r := range text {
		if r >= 0x20 && r <= 0x7e || r == '\n' || r == '\r' || r == '\t' {
			asciiBytes++
			continue
		}
		flushASCII()
		width := utf8.RuneLen(r)
		if width == 3 && unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			halfTokens += 3
		} else {
			halfTokens += 2 * width
		}
	}
	flushASCII()
	return max((halfTokens+1)/2, 1)
}

func (budget codexExactAgentBudget) check(prompt string) (codexExactBudgetEstimate, error) {
	estimate := codexExactBudgetEstimate{bytes: len(prompt) + len(budget.responseSchema), runes: utf8.RuneCountInString(prompt) + utf8.RuneCount(budget.responseSchema)}
	if !utf8.ValidString(prompt) || !utf8.Valid(budget.responseSchema) {
		return estimate, fmt.Errorf("exact agent packet prompt/schema is not valid UTF-8; no provider call")
	}
	if budget.contextWindow <= 0 {
		return estimate, fmt.Errorf("exact agent packet has invalid configured context budget; no provider call")
	}
	windowEighth := budget.contextWindow / 8
	if budget.contextWindow%8 != 0 {
		windowEighth++
	}
	estimate.outputReserve = max(codexExactOutputReserve, windowEighth, budget.maxOutputTokens)
	if estimate.outputReserve < budget.contextWindow && budget.contextWindow-estimate.outputReserve > codexExactCLIOverheadReserve {
		estimate.inputLimit = min(codexExactAbsoluteInputTokens, budget.contextWindow-estimate.outputReserve-codexExactCLIOverheadReserve)
	}
	if estimate.bytes > codexExactAbsoluteInputBytes {
		return estimate, fmt.Errorf("exact agent packet exceeds absolute input byte limit: input_bytes=%d input_runes=%d max_input_bytes=%d; no input truncated or provider call", estimate.bytes, estimate.runes, codexExactAbsoluteInputBytes)
	}
	// Include the actual --output-schema content, which is not part of the
	// serialized user prompt. Tool parameter schemas already occur in prompt.
	estimate.inputTokens = estimateCodexExactTextTokens(prompt) + estimateCodexExactTextTokens(string(budget.responseSchema))
	estimate.guardedTokens = estimate.inputTokens + (estimate.inputTokens+3)/4
	if estimate.guardedTokens > estimate.inputLimit {
		return estimate, fmt.Errorf("exact agent packet exceeds configured operating budget: estimated_input_tokens=%d guarded_estimated_input_tokens=%d input_budget_tokens=%d configured_context_tokens=%d reserved_output_tokens=%d cli_overhead_reserve_tokens=%d input_runes=%d input_bytes=%d estimator=script-local.v1; estimates are not provider usage, no input truncated or provider call", estimate.inputTokens, estimate.guardedTokens, estimate.inputLimit, budget.contextWindow, estimate.outputReserve, codexExactCLIOverheadReserve, estimate.runes, estimate.bytes)
	}
	return estimate, nil
}

func assembleCodexWindowedExactPrompt(prefix, suffix string, messages []agentcore.Message, history []codexExactHistoryEntry, latest, latestError int, budget codexExactAgentBudget) (string, error) {
	selected := map[int]bool{}
	// Pin actual latest feedback/error and its corresponding original call,
	// not a guessed call chosen by tool name or a truncated argument excerpt.
	for _, index := range []int{latest, latestError} {
		if index < 0 {
			continue
		}
		selected[index] = true
		message := messages[index]
		if message.Role != agentcore.RoleTool {
			continue
		}
		callID, _ := message.Metadata["tool_call_id"].(string)
		if callID == "" {
			continue
		}
		for previous := index - 1; previous >= 0; previous-- {
			found := false
			for _, call := range messages[previous].ToolCalls() {
				found = found || call.ID == callID
			}
			if found {
				selected[previous] = true
				break
			}
		}
	}
	assemble := func() string {
		var prompt strings.Builder
		prompt.WriteString(prefix)
		for _, entry := range history {
			if selected[entry.index] {
				prompt.WriteString(entry.text)
			}
		}
		prompt.WriteString(suffix)
		return prompt.String()
	}
	prompt := assemble()
	if _, err := budget.check(prompt); err != nil {
		return "", err
	}
	// A large configured window is not permission to resend an arbitrarily
	// large old dialogue. This independent limit excludes mandatory feedback.
	historyRemaining := codexExactOldHistoryTokens
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if selected[entry.index] {
			continue
		}
		tokens := corecontext.EstimateTokens(agentcore.UserMsg(entry.text))
		if tokens > historyRemaining {
			continue
		}
		selected[entry.index] = true
		candidate := assemble()
		if _, err := budget.check(candidate); err != nil {
			delete(selected, entry.index)
			continue
		}
		prompt, historyRemaining = candidate, historyRemaining-tokens
	}
	return prompt, nil
}
