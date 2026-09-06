package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/voocel/agentcore"
)

type snapshotTestModel struct {
	agentcore.ChatModel
	name string
}

func (m *snapshotTestModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return &agentcore.LLMResponse{Message: agentcore.Message{Usage: &agentcore.Usage{Model: m.name}}}, nil
}

func TestModelSnapshotPinsInheritedTargetAndNewOverrideAcrossConcurrentSwaps(t *testing.T) {
	ms, err := NewModelSet(Config{Provider: "local", ModelName: "default", Providers: map[string]ProviderConfig{"local": {Type: "openai"}}, Roles: map[string]RoleConfig{"writer": {Provider: "local", Model: "writer-inherited"}}})
	if err != nil {
		t.Fatal(err)
	}
	ms.SetAttemptDecorator(func(_ context.Context, _, _, name string, _ agentcore.ChatModel) agentcore.ChatModel {
		return &snapshotTestModel{name: name}
	})
	initial, err := ms.SnapshotForRole("world_arbiter")
	if err != nil {
		t.Fatal(err)
	}
	if err := ms.Swap("world_arbiter", "local", "judge-0"); err != nil {
		t.Fatal(err)
	}
	r, err := initial.Model.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Name != "writer-inherited" || r.Message.Usage.Model != initial.Name {
		t.Fatal("initial snapshot followed newly explicit override")
	}
	var wg sync.WaitGroup
	errors := make(chan error, 100)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 30 {
			if err := ms.Swap("world_arbiter", "local", fmt.Sprintf("judge-%d", i)); err != nil {
				errors <- err
			}
			if err := ms.Swap("character", "local", fmt.Sprintf("actor-%d", i)); err != nil {
				errors <- err
			}
		}
	}()
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				snapshot, err := ms.SnapshotForRole("world_arbiter")
				if err != nil {
					errors <- err
					return
				}
				r, err := snapshot.Model.Generate(context.Background(), nil, nil)
				if err != nil {
					errors <- err
					return
				}
				if r.Message.Usage.Model != snapshot.Name {
					errors <- fmt.Errorf("model=%s snapshot=%s", r.Message.Usage.Model, snapshot.Name)
				}
				_, _, _ = ms.CurrentSelection("world_arbiter")
				_ = ms.ForRole("world_arbiter")
				_ = ms.ForRoleWithFailover("world_arbiter", nil)
				_, _, _ = ms.CurrentSelection("character")
				if _, err := ms.SnapshotForRole("character"); err != nil {
					errors <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}
