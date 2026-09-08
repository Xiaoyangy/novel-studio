package main

import (
	"reflect"
	"testing"
)

func TestInitializeOnlyEndsAtZeroInitWithoutPlanningOrRendering(t *testing.T) {
	flags, extra, err := parsePipelineFlags([]string{"--init-only", "--prompt", "三章小说"})
	if err != nil || len(extra) != 0 {
		t.Fatalf("parse initialize-only: %v", err)
	}
	stages, err := resolveStages(flags.Stages)
	if err != nil || !reflect.DeepEqual(stages, []string{"architect", "outline-all", "zero-init"}) {
		t.Fatalf("initialization expanded into execution: %v %v", stages, err)
	}
	if flags.Restart || !flags.InitializeOnly {
		t.Fatal("initialization implied destructive restart")
	}
	ordinary, _, err := parsePipelineFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := resolveStages(ordinary.Stages)
	if err != nil || !reflect.DeepEqual(defaults, defaultPipelineStages) {
		t.Fatal("ordinary production stages changed")
	}
}

func TestInitializeOnlyRejectsConflictingProductionFlags(t *testing.T) {
	for _, other := range [][]string{{"--stages", "render"}, {"--from", "2"}, {"--to", "3"}, {"--write-to", "2"}, {"--force-rerender"}, {"--refresh-render-input"}, {"--rebase-all-chapters"}} {
		if _, _, err := parsePipelineFlags(append([]string{"--init-only"}, other...)); err == nil {
			t.Fatalf("ambiguous initialization flags accepted: %v", other)
		}
	}
}
