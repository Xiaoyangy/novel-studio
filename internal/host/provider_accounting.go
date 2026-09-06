package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type hostProviderAccounting struct {
	meter                           *DurableUsageMeter
	scope, generation, processStart string
	mu                              sync.Mutex
	err                             error
	onError                         func(error)
	beforeCall                      func() error
}

func newHostProviderAccounting(st *store.Store) (*hostProviderAccounting, error) {
	meter, err := NewDurableUsageMeter(st)
	if err != nil {
		return nil, err
	}
	if err := meter.RecoverInterruptedCalls(nil); err != nil {
		return nil, err
	}
	birth, alive, err := UsageProcessIdentity(os.Getpid())
	if err != nil {
		return nil, err
	}
	if !alive {
		return nil, fmt.Errorf("host accounting owner is unavailable")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return nil, err
	}
	generation := ""
	if progress != nil {
		generation = progress.GenerationID
	}
	sum := sha256.Sum256([]byte(st.Dir()))
	return &hostProviderAccounting{meter: meter, scope: "host_usage_" + hex.EncodeToString(sum[:]), generation: generation, processStart: birth}, nil
}

func (a *hostProviderAccounting) decorate(ctx context.Context, role, provider, name string, model agentcore.ChatModel) agentcore.ChatModel {
	return agents.NewAuditedUsageModel(ctx, model, role, provider, name, a.scope, a.record, agents.DirectUsageLifecycle{StartCall: a.start, SkipCall: a.meter.SkipCall, GenerationID: a.generation})
}

func (a *hostProviderAccounting) start(id, agent string) error {
	a.mu.Lock()
	err := a.err
	guard := a.beforeCall
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	return a.meter.StartCall(id, agent, a.generation, os.Getpid(), a.processStart)
}

// Provider callbacks and forwarded OnMessage observations share this entry.
// Synthetic/history messages without a provider-owned ID are never charged.
func (a *hostProviderAccounting) record(agent string, raw agentcore.AgentMessage) {
	message, ok := raw.(agentcore.Message)
	if !ok {
		return
	}
	id, _ := message.Metadata["usage_audit_id"].(string)
	if id == "" {
		return
	}
	metadata := make(map[string]any, len(message.Metadata)+2)
	for key, value := range message.Metadata {
		metadata[key] = value
	}
	if origin, ok := metadata["usage_audit_agent"].(string); ok && origin != "" {
		agent = origin
	}
	metadata["stage"] = agent
	if _, exists := metadata["generation_id"]; !exists {
		metadata["generation_id"] = a.generation
	}
	if message.Usage == nil {
		message.Usage = &agentcore.Usage{}
		metadata["codex_usage_source"] = "unknown"
		metadata["codex_usage_breakdown"] = []*agentcore.Usage{nil}
		metadata["codex_usage_breakdown_complete"] = true
	}
	message.Metadata = metadata
	if err := a.meter.Record(id, agent, message); err != nil {
		a.mu.Lock()
		a.err = errors.Join(a.err, err)
		handler := a.onError
		a.mu.Unlock()
		if handler != nil {
			handler(err)
		}
	}
}

func (a *hostProviderAccounting) flush() error {
	err := a.meter.Flush()
	a.mu.Lock()
	defer a.mu.Unlock()
	return errors.Join(a.err, err)
}
