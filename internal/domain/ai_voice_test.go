package domain

import "testing"

func TestChapterFunctionPlanningAdviceStaysAdvisoryAcrossLegacySeverity(t *testing.T) {
	for _, severity := range []string{"info", "warning"} {
		flag := AIVoiceRedFlag{Rule: AIVoiceChapterFunctionRepetitionRule, Severity: severity}
		if !IsAIVoicePlanningAdvice(flag) || !IsAdvisoryAIVoiceFlag(flag) {
			t.Fatalf("chapter-function advice severity=%q must remain advisory", severity)
		}
	}
	if IsAdvisoryAIVoiceFlag(AIVoiceRedFlag{Rule: "dialogue_conveyor_overuse", Severity: "warning"}) {
		t.Fatal("current-chapter warning was incorrectly downgraded to advisory")
	}
}
