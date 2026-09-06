package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/chenhongyang/novel-studio/assets"
	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/host"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
)

type pipelineOutlineAllModelOperation func(context.Context, bootstrap.Config, assets.Bundle, string, string, ...agents.UsageRecorder) error

func runPipelineOutlineAllArchitectWithUsage(
	ctx context.Context, cfg bootstrap.Config, bundle assets.Bundle, prompt string, run pipelineOutlineAllModelOperation,
	liveOutputDirs ...string,
) error {
	liveOutputDir := cfg.OutputDir
	if len(liveOutputDirs) > 0 && liveOutputDirs[0] != "" {
		liveOutputDir = liveOutputDirs[0]
	}
	st := store.NewStore(liveOutputDir)
	if err := st.Init(); err != nil {
		return fmt.Errorf("outline-all usage store init: %w", err)
	}
	usage, err := host.NewDurableUsageMeter(st)
	if err != nil {
		return fmt.Errorf("outline-all load usage: %w", err)
	}
	if err := recoverPipelineInterruptedUsageCalls(usage, pipelineUsageProcessIdentity); err != nil {
		return err
	}
	ownerStart, alive, err := pipelineUsageProcessIdentity(os.Getpid())
	if err != nil {
		return fmt.Errorf("outline-all cannot verify usage owner identity: %w", err)
	}
	if !alive {
		return fmt.Errorf("outline-all usage owner process is unavailable")
	}
	generationID := "outline-all"
	if receipt, err := store.NewStore(cfg.OutputDir).LoadOutlineAllExecutionReceipt(); err != nil {
		return err
	} else if receipt != nil && receipt.GenerationID != "" {
		generationID = receipt.GenerationID
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var saveErr error
	record := func(agentName string, msg agentcore.AgentMessage) {
		message, ok := msg.(agentcore.Message)
		if !ok || (message.Role != agentcore.RoleAssistant && message.Usage == nil) {
			return
		}
		metadata := make(map[string]any, len(message.Metadata)+3)
		for key, value := range message.Metadata {
			metadata[key] = value
		}
		id, _ := metadata["usage_audit_id"].(string)
		if id == "" {
			id = "direct_usage_" + rand.Text()
		}
		metadata["usage_audit_id"] = id
		metadata["generation_id"] = generationID
		metadata["stage"] = "architect_outline_all"
		message.Metadata = metadata
		mu.Lock()
		defer mu.Unlock()
		if err := usage.Record(id, agentName, message); err != nil {
			saveErr = errors.Join(saveErr, fmt.Errorf("outline-all persist usage: %w", err))
			cancel()
		}
	}
	runCtx = agents.WithDirectUsageLifecycle(runCtx, agents.DirectUsageLifecycle{
		StartCall: func(id, role string) error { return usage.StartCall(id, role, generationID, os.Getpid(), ownerStart) }, SkipCall: usage.SkipCall,
	})
	runErr := run(runCtx, cfg, bundle, cfg.OutputDir, prompt, record)
	mu.Lock()
	defer mu.Unlock()
	if err := usage.Flush(); err != nil {
		saveErr = errors.Join(saveErr, fmt.Errorf("outline-all flush usage: %w", err))
	}
	return errors.Join(runErr, saveErr)
}
