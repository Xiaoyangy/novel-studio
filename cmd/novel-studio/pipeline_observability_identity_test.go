package main

import "testing"

func TestPipelineCanonSnapshotExcludesObservabilityLedgers(t *testing.T) {
	tests := []string{
		"meta/pipeline_timings.jsonl",
		"meta/runtime/watchdog.json",
		"meta/runtime/diagnostics/ch01.json",
	}
	for _, path := range tests {
		if !pipelineCanonSnapshotExcluded(path, false) {
			t.Fatalf("observability artifact %q must not mutate canon identity", path)
		}
	}
}
