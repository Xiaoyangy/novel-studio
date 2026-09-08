package bootstrap

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"testing"
)

func TestCharacterProtocolDefaultAndExplicitV1RemainDistinct(t *testing.T) {
	for _, test := range []struct{ configured, want string }{
		{"", domain.CharacterAgentDecisionProtocolV2Version}, {"v2", domain.CharacterAgentDecisionProtocolV2Version},
		{"v1", domain.CharacterAgentDecisionProtocolVersion}, {"legacy", "legacy"},
	} {
		cfg := Config{CharacterAgents: CharacterAgentsConfig{Protocol: test.configured}}
		cfg.FillDefaults()
		if got := cfg.CharacterAgentsProtocolVersion(); got != test.want {
			t.Fatalf("protocol %q resolved to %q, want %q", test.configured, got, test.want)
		}
	}
}
