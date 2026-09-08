package agents

import (
	"context"
	"crypto/rand"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore"
)

// ProjectedPlanningAccounting contains host-owned observational and budget
// hooks. It does not affect prompts, proposal schemas or protocol digests.
type ProjectedPlanningAccounting struct {
	RecordUsage          UsageRecorder
	CoverCharacterUsage  func(string) error
	ImportCharacterUsage func(domain.CharacterAgentUsage) error
	BeforeAgent          func() error
	AfterAgent           func() error
	StartCall            func(id, agentName string) error
	SkipCall             func(id string) error
	DurableProgress      func(DurablePlanningProgress) error
}

type projectedPlanningAccountingKey struct{}
type characterAccountingGroupKey struct{}
type projectedPlanningChapterKey struct{}

func projectedAccounting(ctx context.Context) ProjectedPlanningAccounting {
	accounting, _ := ctx.Value(projectedPlanningAccountingKey{}).(ProjectedPlanningAccounting)
	return accounting
}

func projectedAccountingBefore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if hook := projectedAccounting(ctx).BeforeAgent; hook != nil {
		return hook()
	}
	return nil
}

func projectedAccountingAfter(ctx context.Context) error {
	if hook := projectedAccounting(ctx).AfterAgent; hook != nil {
		return hook()
	}
	return nil
}

func projectedAccountingModel(ctx context.Context, model agentcore.ChatModel, agentName, groupID string) (agentcore.ChatModel, func(agentcore.AgentMessage)) {
	if bound, ok := model.(interface{ UsageAccountingBound() bool }); ok && bound.UsageAccountingBound() {
		return WithDirectUsageAgentModel(model, agentName), nil
	}
	record := projectedAccounting(ctx).RecordUsage
	if record == nil {
		return model, nil
	}
	identity := directAgentModelIdentity{ChatModel: model, Name: characterUsageModelName(model)}
	if provider, ok := model.(agentcore.ProviderNamer); ok {
		identity.Provider = provider.ProviderName()
	}
	observer := &directAgentUsage{resolved: identity, record: func(msg agentcore.AgentMessage) {
		message, ok := msg.(agentcore.Message)
		if !ok || (message.Usage == nil && message.Role != agentcore.RoleAssistant) {
			return
		}
		if groupID != "" {
			metadata := make(map[string]any, len(message.Metadata)+1)
			for key, value := range message.Metadata {
				metadata[key] = value
			}
			metadata["usage_group_id"] = groupID
			message.Metadata = metadata
		}
		if chapter, _ := ctx.Value(projectedPlanningChapterKey{}).(int); chapter > 0 {
			if message.Metadata == nil {
				message.Metadata = make(map[string]any)
			}
			message.Metadata["chapter"] = chapter
		}
		record(agentName, message)
	}}
	wrapped := &directUsageModel{ChatModel: model, usage: observer, skipCall: projectedAccounting(ctx).SkipCall}
	if hook := projectedAccounting(ctx).StartCall; hook != nil {
		wrapped.beforeCall = func(id string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return hook(id, agentName)
		}
	}
	return wrapped, observer.message
}

func prepareCharacterAccounting(ctx context.Context, record domain.CharacterAgentUsage) (context.Context, domain.CharacterAgentUsage, error) {
	if err := projectedAccountingBefore(ctx); err != nil {
		return ctx, record, err
	}
	accounting := projectedAccounting(ctx)
	if accounting.RecordUsage == nil {
		return ctx, record, nil
	}
	record.UsageID = "ca_usage_" + rand.Text()
	if accounting.CoverCharacterUsage != nil {
		if err := accounting.CoverCharacterUsage(record.UsageID); err != nil {
			return ctx, record, err
		}
	}
	return context.WithValue(ctx, characterAccountingGroupKey{}, record), record, nil
}
