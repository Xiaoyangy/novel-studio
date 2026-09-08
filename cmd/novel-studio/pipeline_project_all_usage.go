package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type pipelineProjectAllAccounting struct {
	ctx          context.Context
	cancel       context.CancelCauseFunc
	meter        *host.DurableUsageMeter
	budget       *host.BudgetSentinel
	generationID string
	mu           sync.Mutex
	err          error
	ownerPID     int
	ownerStart   string
}

func newPipelineProjectAllAccounting(ctx context.Context, cfg bootstrap.Config, live, shadow *store.Store, generationID string) (*pipelineProjectAllAccounting, error) {
	meter, err := host.NewDurableUsageMeter(live)
	if err != nil {
		return nil, fmt.Errorf("project-all initialize live accounting: %w", err)
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	ownerStart, alive, err := pipelineUsageProcessIdentity(os.Getpid())
	if err != nil || !alive {
		cancel(err)
		return nil, fmt.Errorf("project-all cannot establish accounting owner identity: %w", err)
	}
	a := &pipelineProjectAllAccounting{ctx: runCtx, cancel: cancel, meter: meter, generationID: generationID, ownerPID: os.Getpid(), ownerStart: ownerStart}
	a.budget = host.NewBudgetSentinel(cfg.Budget, func() float64 { cost, _, _, _, _ := meter.Tracker().Totals(); return cost },
		func(reason string) { cancel(errors.New(reason)) },
		func(level, summary string) {
			fmt.Fprintf(os.Stderr, "[pipeline:project-all:budget:%s] %s\n", level, summary)
		})
	meter.Tracker().SetOnCost(a.budget.OnCost)
	meter.Tracker().SetOnMissingUsage(func() {
		fmt.Fprintln(os.Stderr, "[pipeline:project-all:usage] 存在未计价或历史缺失消费；账本及预算仅包含已知小计，不代表完整账单。")
	})
	if meter.Tracker().MissingAssistantUsage() > 0 {
		fmt.Fprintln(os.Stderr, "[pipeline:project-all:usage] 既有账本仍含计量缺口；恢复后的成本及预算继续使用已知小计。")
	}
	existing, err := shadow.CharacterAgents.LoadUsage()
	if err != nil {
		cancel(err)
		return nil, fmt.Errorf("project-all load independent usage: %w", err)
	}
	startedID := "project_all_accounting_started:" + generationID
	if !meter.Has(startedID) {
		partials, globErr := filepath.Glob(filepath.Join(shadow.Dir(), "drafts", "*.plan.partial.json"))
		if globErr != nil {
			cancel(globErr)
			return nil, globErr
		}
		if len(existing) > 0 || len(partials) > 0 {
			if err := a.historyGap("project_all_history_gap:"+generationID, "此前 project-all Planner/旧 Simulator 未写入调用审计，历史消费存在未知缺口；禁止由调试日志静默补账。"); err != nil {
				cancel(err)
				return nil, err
			}
		}
		if err := meter.Cover(startedID); err != nil {
			cancel(err)
			return nil, err
		}
	}
	for _, record := range existing {
		if err := a.importCharacterUsage(record); err != nil {
			cancel(err)
			return nil, err
		}
	}
	if err := a.recoverInterruptedCalls(pipelineUsageProcessIdentity); err != nil {
		cancel(err)
		return nil, err
	}
	if err := a.beforeAgent(); err != nil {
		cancel(err)
		return nil, err
	}
	return a, nil
}

func (a *pipelineProjectAllAccounting) historyGap(id, detail string) error {
	if a.meter.Has(id) {
		return nil
	}
	return a.meter.Record(id, "writer", agentcore.Message{Role: agentcore.RoleAssistant, Usage: &agentcore.Usage{}, Metadata: map[string]any{
		"usage_audit_id": id, "generation_id": a.generationID, "usage_accounting_gap": detail,
		"codex_usage_breakdown": []*agentcore.Usage{nil}, "codex_usage_breakdown_complete": true,
	}})
}

func (a *pipelineProjectAllAccounting) record(agentName string, raw agentcore.AgentMessage) {
	message, ok := raw.(agentcore.Message)
	if !ok || (message.Usage == nil && message.Role != agentcore.RoleAssistant) {
		return
	}
	metadata := make(map[string]any, len(message.Metadata)+2)
	for key, value := range message.Metadata {
		metadata[key] = value
	}
	id, _ := metadata["usage_audit_id"].(string)
	if id == "" {
		id = "project_usage_" + rand.Text()
		metadata["usage_audit_id"] = id
	}
	metadata["generation_id"] = a.generationID
	metadata["stage"] = agentName
	if message.Usage == nil {
		message.Usage = &agentcore.Usage{}
		metadata["codex_usage_breakdown"] = []*agentcore.Usage{nil}
		metadata["codex_usage_breakdown_complete"] = true
	}
	message.Metadata = metadata
	if agentName == "project_all_planner" || agentName == "project_all_world_simulator" {
		agentName = "writer"
	}
	if agentName == "plan_grounding" {
		agentName = "world_arbiter"
	}
	if err := a.meter.Record(id, agentName, message); err != nil {
		a.mu.Lock()
		a.err = errors.Join(a.err, err)
		a.mu.Unlock()
		a.cancel(fmt.Errorf("project-all persist usage: %w", err))
	}
}

func (a *pipelineProjectAllAccounting) importCharacterUsage(record domain.CharacterAgentUsage) error {
	if strings.TrimSpace(record.UsageID) == "" {
		return a.historyGap("legacy_character_usage_gap:"+a.generationID, "旧独立角色用量没有 usage_id，无法证明是否已入账；保留历史计量缺口，不猜测重复消费。")
	}
	if a.meter.Covered(record.UsageID) {
		return nil
	}
	if math.IsNaN(record.CostUSD) || math.IsInf(record.CostUSD, 0) || record.CostUSD < 0 || record.Input < 0 || record.Output < 0 || record.CacheRead < 0 || record.CacheWrite < 0 {
		return fmt.Errorf("project-all independent usage %s has invalid accounting values", record.UsageID)
	}
	// The independent ledger already priced each provider request. Import its
	// known subtotal without repricing aggregate tokens as a long-context call,
	// and do not manufacture cache savings without its request boundaries.
	parts := []*agentcore.Usage{{Provider: record.Provider, Model: record.Model, Cost: &agentcore.Cost{Total: record.CostUSD}}}
	if record.CostSource != "reported" && record.CostSource != "estimated" || record.UnpricedCalls > 0 {
		parts = append(parts, nil)
	}
	agentName := "character_" + record.AgentID
	if record.Role == "world_arbiter" {
		agentName = "world_arbiter"
	}
	return a.meter.Record(record.UsageID, agentName, agentcore.Message{Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{Provider: record.Provider, Model: record.Model, Input: record.Input, Output: record.Output, CacheRead: record.CacheRead, CacheWrite: record.CacheWrite},
		Metadata: map[string]any{"usage_audit_id": record.UsageID, "generation_id": record.GenerationID, "chapter": record.Chapter,
			"codex_usage_source": record.CostSource, "codex_usage_breakdown": parts, "codex_usage_breakdown_complete": true},
	})
}

func (a *pipelineProjectAllAccounting) beforeAgent() error {
	if err := context.Cause(a.ctx); err != nil {
		return err
	}
	return a.budget.Refuse()
}

func (a *pipelineProjectAllAccounting) afterAgent() error {
	a.budget.HandleBoundary()
	return context.Cause(a.ctx)
}

func (a *pipelineProjectAllAccounting) hooks() agents.ProjectedPlanningAccounting {
	return agents.ProjectedPlanningAccounting{RecordUsage: a.record, CoverCharacterUsage: a.meter.Cover,
		ImportCharacterUsage: a.importCharacterUsage, BeforeAgent: a.beforeAgent, AfterAgent: a.afterAgent, StartCall: a.startCall, SkipCall: a.meter.SkipCall}
}

func (a *pipelineProjectAllAccounting) close() error {
	flushErr := a.meter.Flush()
	a.mu.Lock()
	persistedErr := a.err
	a.mu.Unlock()
	cause := context.Cause(a.ctx)
	a.cancel(nil)
	return errors.Join(persistedErr, flushErr, cause)
}
