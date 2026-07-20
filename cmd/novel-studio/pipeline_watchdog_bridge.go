package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	pipelineWatchdogEventStageDispatched          = "stage_dispatched"
	pipelineWatchdogEventStageExecutionCompleted  = "stage_execution_completed"
	pipelineWatchdogEventStageVerified            = "stage_verified"
	pipelineWatchdogEventStageCheckpointed        = "stage_checkpointed"
	pipelineWatchdogEventRenderPreflightPassed    = "render_preflight_passed"
	pipelineWatchdogEventRenderCandidatePrepared  = "render_candidate_prepared"
	pipelineWatchdogEventRenderBodyCommitted      = "render_body_committed"
	pipelineWatchdogEventRenderFormalReviewed     = "render_formal_reviewed"
	pipelineWatchdogEventRenderActualMatched      = "render_actual_matched"
	pipelineWatchdogEventRenderCandidatePublished = "render_candidate_published"
	pipelineWatchdogEventRenderChapterAccepted    = "render_chapter_accepted"
)

type pipelineWatchdogHandle interface {
	Start() error
	Stop() error
	Progress(kind string) error
	ProgressBody(kind, bodySHA256 string) error
}

type pipelineWatchdogPauseHandle interface {
	Pause() error
	Resume() error
}

type pipelineWatchdogFactory func(pipelineWatchdogConfig) (pipelineWatchdogHandle, error)

var currentPipelineWatchdogBridge struct {
	sync.RWMutex
	handle pipelineWatchdogHandle
}

func bindCurrentPipelineWatchdog(handle pipelineWatchdogHandle) (func(), error) {
	if handle == nil {
		return nil, fmt.Errorf("pipeline watchdog bridge handle is nil")
	}
	currentPipelineWatchdogBridge.Lock()
	defer currentPipelineWatchdogBridge.Unlock()
	if currentPipelineWatchdogBridge.handle != nil {
		return nil, fmt.Errorf("pipeline watchdog bridge already has an active stage")
	}
	currentPipelineWatchdogBridge.handle = handle
	return func() {
		currentPipelineWatchdogBridge.Lock()
		if currentPipelineWatchdogBridge.handle == handle {
			currentPipelineWatchdogBridge.handle = nil
		}
		currentPipelineWatchdogBridge.Unlock()
	}, nil
}

// pipelineWatchdogProgress is a process-local bridge from deep durable
// boundaries to the one stage watchdog owned by the CLI loop. With no active
// pipeline stage it is intentionally a no-op, keeping helpers independently
// testable and preserving non-pipeline commands.
func pipelineWatchdogProgress(eventCode string) error {
	currentPipelineWatchdogBridge.RLock()
	defer currentPipelineWatchdogBridge.RUnlock()
	if currentPipelineWatchdogBridge.handle == nil {
		return nil
	}
	return currentPipelineWatchdogBridge.handle.Progress(eventCode)
}

func pipelineWatchdogProgressBody(eventCode, bodySHA256 string) error {
	currentPipelineWatchdogBridge.RLock()
	defer currentPipelineWatchdogBridge.RUnlock()
	if currentPipelineWatchdogBridge.handle == nil {
		return nil
	}
	return currentPipelineWatchdogBridge.handle.ProgressBody(eventCode, bodySHA256)
}

// withPipelineWatchdogPaused closes the live-root swap race between the
// independent heartbeat ticker and DirectoryPublishStore. Test doubles and
// legacy callers that do not expose Pause/Resume remain compatible.
func withPipelineWatchdogPaused(work func() error) (returnErr error) {
	if work == nil {
		return fmt.Errorf("pipeline watchdog paused work is nil")
	}
	currentPipelineWatchdogBridge.RLock()
	handle := currentPipelineWatchdogBridge.handle
	pauser, canPause := handle.(pipelineWatchdogPauseHandle)
	if canPause {
		if err := pauser.Pause(); err != nil {
			currentPipelineWatchdogBridge.RUnlock()
			return fmt.Errorf("pause pipeline watchdog: %w", err)
		}
	}
	currentPipelineWatchdogBridge.RUnlock()
	if !canPause {
		return work()
	}
	defer func() {
		if err := pauser.Resume(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("resume pipeline watchdog: %w", err))
		}
	}()
	return work()
}

func withPipelineStageWatchdog(
	cfg pipelineWatchdogConfig,
	work func() error,
) error {
	return withPipelineStageWatchdogUsing(
		cfg,
		func(cfg pipelineWatchdogConfig) (pipelineWatchdogHandle, error) {
			return newPipelineWatchdog(cfg)
		},
		work,
	)
}

func withPipelineStageWatchdogUsing(
	cfg pipelineWatchdogConfig,
	factory pipelineWatchdogFactory,
	work func() error,
) (returnErr error) {
	if factory == nil {
		return fmt.Errorf("pipeline watchdog factory is nil")
	}
	handle, err := factory(cfg)
	if err != nil {
		return fmt.Errorf("create stage %s watchdog: %w", cfg.Stage, err)
	}
	if handle == nil {
		return fmt.Errorf("create stage %s watchdog returned nil", cfg.Stage)
	}
	if startErr := handle.Start(); startErr != nil {
		stopErr := handle.Stop()
		return joinPipelineWatchdogStopError(
			fmt.Errorf("start stage %s watchdog: %w", cfg.Stage, startErr),
			cfg.Stage,
			stopErr,
		)
	}
	release, bindErr := bindCurrentPipelineWatchdog(handle)
	if bindErr != nil {
		return joinPipelineWatchdogStopError(
			fmt.Errorf("bind stage %s watchdog: %w", cfg.Stage, bindErr),
			cfg.Stage,
			handle.Stop(),
		)
	}
	defer func() {
		// Unbind first and wait for any in-flight bridge update under the RWMutex;
		// no late deep event can race Stop and revive a completed stage.
		release()
		returnErr = joinPipelineWatchdogStopError(returnErr, cfg.Stage, handle.Stop())
	}()
	if work == nil {
		return fmt.Errorf("stage %s watchdog work is nil", cfg.Stage)
	}
	return work()
}

func joinPipelineWatchdogStopError(primary error, stage string, stopErr error) error {
	if stopErr == nil {
		return primary
	}
	wrapped := fmt.Errorf("stop stage %s watchdog: %w", stage, stopErr)
	if primary == nil {
		return wrapped
	}
	return errors.Join(primary, wrapped)
}

// pipelineWatchdogStageIdentity is deliberately read-only and best-effort.
// A watchdog may start before a stage has produced its chapter identity; the
// render bridge fills the body hash later at the durable commit boundary.
func pipelineWatchdogStageIdentity(outputDir, stage string, fallbackChapter int) (int, string) {
	chapter := fallbackChapter
	planDigest := ""
	switch strings.TrimSpace(stage) {
	case "seal", "promote", "plan", "render", "write", "review", "rewrite":
		path := filepath.Join(outputDir, filepath.FromSlash(pipelineFrozenPlanPath))
		raw, err := os.ReadFile(path)
		if err == nil {
			var identity struct {
				Chapter    int    `json:"chapter"`
				PlanDigest string `json:"plan_digest"`
			}
			if json.Unmarshal(raw, &identity) == nil {
				if identity.Chapter > 0 {
					chapter = identity.Chapter
				}
				planDigest = identity.PlanDigest
			}
		}
	}
	if chapter < 0 {
		chapter = 0
	}
	return chapter, planDigest
}

func pipelineWatchdogStageInvocationID(runIdentity, fallback, stage string) string {
	base := strings.TrimSpace(runIdentity)
	if base == "" {
		base = strings.TrimSpace(fallback)
	}
	return base + ":" + strings.TrimSpace(stage)
}
