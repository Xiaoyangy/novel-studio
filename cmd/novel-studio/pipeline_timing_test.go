package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendPipelineTimingPreservesAttempts(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	for attempt, status := range []string{"error", "ok"} {
		record := pipelineTimingRecord{
			InvocationID: "invocation-1",
			RunIdentity:  "sha256:test",
			Scope:        "stage",
			Stage:        "render",
			Attempt:      attempt + 1,
			Status:       status,
			StartedAt:    started.Add(time.Duration(attempt) * time.Second).Format(time.RFC3339Nano),
			FinishedAt:   started.Add(time.Duration(attempt+1) * time.Second).Format(time.RFC3339Nano),
			ElapsedMS:    1000,
		}
		if err := appendPipelineTiming(dir, record); err != nil {
			t.Fatalf("append attempt %d: %v", attempt+1, err)
		}
	}

	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(pipelineTimingLogPath)))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var got []pipelineTimingRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record pipelineTimingRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode timing line: %v", err)
		}
		got = append(got, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Status != "error" || got[1].Status != "ok" {
		t.Fatalf("timing attempts=%+v", got)
	}
	if got[0].Schema != "pipeline-timing.v1" || got[1].Attempt != 2 {
		t.Fatalf("timing identity=%+v", got)
	}
}

func TestAppendPipelineTimingRejectsIncompleteRecord(t *testing.T) {
	err := appendPipelineTiming(t.TempDir(), pipelineTimingRecord{Stage: "render"})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordPipelineStageTimingTruncatesError(t *testing.T) {
	dir := t.TempDir()
	recordPipelineStageTiming(
		dir,
		"invocation-2",
		"sha256:test",
		"render",
		time.Now().Add(-time.Second),
		"error",
		errors.New(strings.Repeat("x", 2500)),
	)
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(pipelineTimingLogPath)))
	if err != nil {
		t.Fatal(err)
	}
	var record pipelineTimingRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &record); err != nil {
		t.Fatal(err)
	}
	if len(record.Error) != 2000 {
		t.Fatalf("error length=%d", len(record.Error))
	}
}

func TestRecordPipelineChapterTimingIncludesBudgetAndAttempt(t *testing.T) {
	dir := t.TempDir()
	recordPipelineChapterTiming(
		dir,
		"invocation-3",
		"formal_review",
		7,
		2,
		time.Now().Add(-2*time.Second),
		3*time.Minute,
		"ok",
		nil,
	)
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(pipelineTimingLogPath)))
	if err != nil {
		t.Fatal(err)
	}
	var record pipelineTimingRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &record); err != nil {
		t.Fatal(err)
	}
	if record.Scope != "chapter" || record.Stage != "formal_review" || record.Chapter != 7 ||
		record.Attempt != 2 || record.BudgetMS != (3*time.Minute).Milliseconds() {
		t.Fatalf("chapter timing=%+v", record)
	}
}
