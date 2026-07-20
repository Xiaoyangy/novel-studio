package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const pipelineTimingLogPath = "meta/pipeline_timings.jsonl"

// pipelineTimingRecord is an append-only production trace. pipeline.json is a
// resumability cursor and is intentionally rewritten; keeping timings in a
// separate ledger preserves failed attempts so a slow run can be diagnosed
// without reconstructing terminal output.
type pipelineTimingRecord struct {
	Schema       string `json:"schema"`
	InvocationID string `json:"invocation_id"`
	RunIdentity  string `json:"run_identity,omitempty"`
	Scope        string `json:"scope"`
	Stage        string `json:"stage"`
	Chapter      int    `json:"chapter,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	ElapsedMS    int64  `json:"elapsed_ms"`
	BudgetMS     int64  `json:"budget_ms,omitempty"`
	Error        string `json:"error,omitempty"`
}

func newPipelineTimingInvocationID(now time.Time) string {
	return fmt.Sprintf("%d-%d", os.Getpid(), now.UTC().UnixNano())
}

func appendPipelineTiming(outputDir string, record pipelineTimingRecord) error {
	if strings.TrimSpace(outputDir) == "" {
		return fmt.Errorf("pipeline timing output directory is empty")
	}
	record.Schema = "pipeline-timing.v1"
	if record.Scope == "" {
		record.Scope = "stage"
	}
	if record.Status == "" || record.Stage == "" || record.StartedAt == "" || record.FinishedAt == "" {
		return fmt.Errorf("pipeline timing record is incomplete")
	}
	if len(record.Error) > 2000 {
		record.Error = record.Error[:2000]
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal pipeline timing: %w", err)
	}
	path := filepath.Join(outputDir, filepath.FromSlash(pipelineTimingLogPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create pipeline timing directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open pipeline timing ledger: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("append pipeline timing ledger: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync pipeline timing ledger: %w", err)
	}
	return nil
}

func recordPipelineStageTiming(
	outputDir string,
	invocationID string,
	runIdentity string,
	stage string,
	started time.Time,
	status string,
	err error,
) {
	finished := time.Now().UTC()
	record := pipelineTimingRecord{
		InvocationID: invocationID,
		RunIdentity:  runIdentity,
		Scope:        "stage",
		Stage:        stage,
		Status:       status,
		StartedAt:    started.UTC().Format(time.RFC3339Nano),
		FinishedAt:   finished.Format(time.RFC3339Nano),
		ElapsedMS:    finished.Sub(started).Milliseconds(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	if writeErr := appendPipelineTiming(outputDir, record); writeErr != nil {
		fmt.Fprintf(os.Stderr, "[pipeline:timing] 持久化阶段耗时失败（stage=%s）：%v\n", stage, writeErr)
	}
}

func recordPipelineChapterTiming(
	outputDir string,
	invocationID string,
	stage string,
	chapter int,
	attempt int,
	started time.Time,
	budget time.Duration,
	status string,
	err error,
) {
	finished := time.Now().UTC()
	record := pipelineTimingRecord{
		InvocationID: invocationID,
		RunIdentity:  fmt.Sprintf("sealed-ch%02d", chapter),
		Scope:        "chapter",
		Stage:        stage,
		Chapter:      chapter,
		Attempt:      attempt,
		Status:       status,
		StartedAt:    started.UTC().Format(time.RFC3339Nano),
		FinishedAt:   finished.Format(time.RFC3339Nano),
		ElapsedMS:    finished.Sub(started).Milliseconds(),
		BudgetMS:     budget.Milliseconds(),
	}
	if err != nil {
		record.Error = err.Error()
	}
	if writeErr := appendPipelineTiming(outputDir, record); writeErr != nil {
		fmt.Fprintf(os.Stderr, "[pipeline:timing] 持久化章节耗时失败（chapter=%d stage=%s）：%v\n", chapter, stage, writeErr)
	}
}
