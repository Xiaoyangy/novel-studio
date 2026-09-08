package bootstrap

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCharacterActivationConfigDefaultsAndLegacyLimits(t *testing.T) {
	for _, tc := range []struct {
		protocol    string
		limit, want int
	}{{"", 0, 8}, {"v2", 1, 1}, {"v2", 12, 12}, {"v1", 0, 1}, {"legacy", 0, 1}} {
		cfg := Config{CharacterAgents: CharacterAgentsConfig{Protocol: tc.protocol, MaxActivationCycles: tc.limit}}
		cfg.FillDefaults()
		if cfg.CharacterActivationLimit() != tc.want {
			t.Fatalf("protocol=%s limit=%d got=%d want=%d", tc.protocol, tc.limit, cfg.CharacterActivationLimit(), tc.want)
		}
		if tc.protocol == "v1" && cfg.CharacterAgentsProtocolVersion() != domain.CharacterAgentDecisionProtocolVersion {
			t.Fatal("v1 was upgraded by cycle defaults")
		}
	}
}
