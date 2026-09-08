package bootstrap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestCharacterExecutionPolicyDefaultsStayV1AndV2IsExplicit(t *testing.T) {
	for _, tc := range []struct {
		protocol, execution string
		limit               int
		want                string
	}{
		{"", "", 0, domain.CharacterActivationCyclePolicy},
		{"v2", "v1", 8, domain.CharacterActivationCyclePolicy},
		{"v2", "v2", 8, domain.CharacterActivationCyclePolicyV2},
		{"v2", "v2", 1, ""},
		{"v1", "v2", 8, ""},
		{"legacy", "v2", 8, ""},
	} {
		cfg := Config{CharacterAgents: CharacterAgentsConfig{Protocol: tc.protocol, ExecutionPolicy: tc.execution, MaxActivationCycles: tc.limit}}
		cfg.FillDefaults()
		if tc.execution == "" && cfg.CharacterAgents.ExecutionPolicy != "v1" {
			t.Fatal("unfinished v2 runtime became a default")
		}
		if got := cfg.CharacterActivationPolicy(); got != tc.want {
			t.Fatalf("protocol=%q execution=%q limit=%d: %q want %q", tc.protocol, tc.execution, tc.limit, got, tc.want)
		}
	}
}

func TestCharacterExecutionPolicyConfigValidationAndOverlay(t *testing.T) {
	for _, policy := range []string{"", "v1", "v2", "unknown"} {
		cfg := Config{Provider: "local", ModelName: "test", Providers: map[string]ProviderConfig{"local": {Type: "openai", APIKey: "test"}}, CharacterAgents: CharacterAgentsConfig{ExecutionPolicy: policy}}
		cfg.FillDefaults()
		err := cfg.ValidateBase()
		if (policy == "unknown") != (err != nil) || (err != nil && !strings.Contains(err.Error(), "execution_policy")) {
			t.Fatalf("policy=%q validation=%v", policy, err)
		}
	}
	base := Config{CharacterAgents: CharacterAgentsConfig{Protocol: "v2", ExecutionPolicy: "v2", MaxActivationCycles: 4, MaxConcurrency: 2}}
	var overlay Config
	if err := json.Unmarshal([]byte(`{"character_agents":{"execution_policy":"v1"}}`), &overlay); err != nil {
		t.Fatal(err)
	}
	merged := mergeConfig(base, overlay)
	if merged.CharacterAgents.ExecutionPolicy != "v1" || merged.CharacterAgents.MaxActivationCycles != 4 || merged.CharacterAgents.MaxConcurrency != 2 {
		t.Fatal("explicit execution overlay was ignored or changed other limits")
	}
	merged = mergeConfig(merged, Config{CharacterAgents: CharacterAgentsConfig{ExecutionPolicy: "v2"}})
	if merged.CharacterAgents.ExecutionPolicy != "v2" {
		t.Fatal("v2 opt-in overlay was ignored")
	}
	merged = mergeConfig(merged, Config{CharacterAgents: CharacterAgentsConfig{MaxConcurrency: 1}})
	if merged.CharacterAgents.ExecutionPolicy != "v2" || merged.CharacterAgents.MaxActivationCycles != 4 {
		t.Fatal("an unrelated overlay reset the selected execution policy")
	}
}
