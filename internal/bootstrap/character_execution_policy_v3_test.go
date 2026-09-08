package bootstrap

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"testing"
)

func TestCharacterExecutionPolicyV3RequiresExplicitOptIn(t *testing.T) {
	base := Config{Provider: "local", ModelName: "test", Providers: map[string]ProviderConfig{"local": {Type: "openai", APIKey: "test"}}}
	base.FillDefaults()
	if base.CharacterAgents.ExecutionPolicy != "v1" || base.CharacterActivationPolicy() != domain.CharacterActivationCyclePolicy {
		t.Fatal("v3 changed default execution")
	}
	cfg := mergeConfig(base, Config{CharacterAgents: CharacterAgentsConfig{ExecutionPolicy: "v3"}})
	if err := cfg.ValidateBase(); err != nil {
		t.Fatal(err)
	}
	if cfg.CharacterActivationPolicy() != domain.CharacterActivationCyclePolicyV3 {
		t.Fatal("v3 opt-in was ignored")
	}
	for _, protocol := range []string{"v1", "legacy"} {
		bad := cfg
		bad.CharacterAgents.Protocol = protocol
		if bad.ValidateBase() == nil {
			t.Fatal("v3 selected a legacy actor protocol")
		}
	}
	bad := cfg
	bad.CharacterAgents.MaxActivationCycles = 1
	if bad.ValidateBase() == nil {
		t.Fatal("v3 silently became one-shot")
	}
}
