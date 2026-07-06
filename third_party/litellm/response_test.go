package litellm

import "testing"

func TestNormalizeFinishReasonPreservesProviderContracts(t *testing.T) {
	tests := []struct {
		raw  string
		want FinishReason
	}{
		{raw: "stop", want: FinishReasonStop},
		{raw: "end_turn", want: FinishReasonStop},
		{raw: "pause_turn", want: FinishReasonStop},
		{raw: "STOP", want: FinishReasonStop},
		{raw: "length", want: FinishReasonLength},
		{raw: "max_tokens", want: FinishReasonLength},
		{raw: "tool_use", want: FinishReasonToolCall},
		{raw: "FUNCTION_CALLING", want: FinishReasonToolCall},
		{raw: "content_filter", want: FinishReasonSafety},
		{raw: "refusal", want: FinishReasonSafety},
		{raw: "RECITATION", want: FinishReasonSafety},
		{raw: "PROHIBITED_CONTENT", want: FinishReasonSafety},
		{raw: "IMAGE_SAFETY", want: FinishReasonSafety},
		{raw: "MALFORMED_FUNCTION_CALL", want: FinishReasonError},
		{raw: "UNEXPECTED_TOOL_CALL", want: FinishReasonError},
		{raw: "MISSING_THOUGHT_SIGNATURE", want: FinishReasonError},
		{raw: "insufficient_system_resource", want: FinishReasonError},
		{raw: "provider_specific", want: FinishReason("provider_specific")},
	}

	for _, tt := range tests {
		if got := NormalizeFinishReason(tt.raw); got != tt.want {
			t.Fatalf("NormalizeFinishReason(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
