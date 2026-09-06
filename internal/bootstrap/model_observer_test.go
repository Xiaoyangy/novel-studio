package bootstrap

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/voocel/agentcore"
)

type attemptTestModel struct {
	agentcore.ChatModel
	failure          error
	entered, release chan struct{}
}

func (m *attemptTestModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.entered != nil {
		close(m.entered)
		<-m.release
	}
	if m.failure != nil {
		return nil, m.failure
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{Role: agentcore.RoleAssistant}}, nil
}

func TestModelAttemptDecoratorObservesEachFallbackAndPreservesRequestedRole(t *testing.T) {
	primary := NewSwappableModel("first", "primary", &attemptTestModel{failure: errors.New("insufficient balance")})
	ms := &ModelSet{Default: primary, models: map[string]*SwappableModel{"writer": primary}, fallbacks: map[string][]modelTarget{"writer": {{provider: "second", name: "fallback", model: &attemptTestModel{}}}}}
	var seen []string
	ms.SetAttemptDecorator(func(_ context.Context, role, provider, name string, model agentcore.ChatModel) agentcore.ChatModel {
		seen = append(seen, role+":"+provider+":"+name)
		return model
	})
	if _, err := ms.ForRoleWithFailover("drafter", nil).Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "drafter:first:primary" || seen[1] != "drafter:second:fallback" {
		t.Fatalf("fallback consumption or role inheritance was hidden: %v", seen)
	}
}

func TestModelAttemptDecoratorFreezesHotSwapTargetAndDefaultPurpose(t *testing.T) {
	old := &attemptTestModel{entered: make(chan struct{}), release: make(chan struct{})}
	primary := NewSwappableModel("old-provider", "old-model", old)
	ms := &ModelSet{Default: primary, models: map[string]*SwappableModel{"coordinator": NewSwappableModel("override", "override-model", &attemptTestModel{})}}
	var mu sync.Mutex
	var seen []string
	ms.SetAttemptDecorator(func(_ context.Context, role, provider, name string, model agentcore.ChatModel) agentcore.ChatModel {
		mu.Lock()
		seen = append(seen, role+":"+provider+":"+name)
		mu.Unlock()
		return model
	})
	model := ms.ForDefaultPurpose("coordinator")
	done := make(chan error, 1)
	go func() { _, err := model.Generate(context.Background(), nil, nil); done <- err }()
	<-old.entered
	primary.Swap("new-provider", "new-model", &attemptTestModel{})
	close(old.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := model.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "coordinator:old-provider:old-model" || seen[1] != "coordinator:new-provider:new-model" {
		t.Fatalf("hot swap changed an in-flight target or purpose changed routing: %v", seen)
	}
}
