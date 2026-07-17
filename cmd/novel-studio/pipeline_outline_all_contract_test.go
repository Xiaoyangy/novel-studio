package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestValidatePipelineOutlineAllFlatIdentityAllowsReservedGapsUntilExpanded(t *testing.T) {
	outputDir := t.TempDir()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("reserved-gap", 6); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{
			{
				Index: 1,
				Title: "已展开前弧",
				Chapters: []domain.OutlineEntry{
					{Title: "第一章"},
					{Title: "第二章"},
				},
			},
			{
				Index:             2,
				Title:             "待展开中弧",
				EstimatedChapters: 3,
			},
			{
				Index: 3,
				Title: "已展开后弧",
				Chapters: []domain.OutlineEntry{
					{Title: "第六章"},
				},
			},
		},
	}}
	if _, err := repairPipelineOutlineAllDerivedArtifacts(st, volumes); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, volumes); err != nil {
		t.Fatalf("partially expanded outline rejected its reserved chapter gap: %v", err)
	}
	if got, want := domain.FlattenOutline(volumes), []domain.OutlineEntry{
		{Chapter: 1, Title: "第一章"},
		{Chapter: 2, Title: "第二章"},
		{Chapter: 6, Title: "第六章"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reserved gap changed: got=%+v want=%+v", got, want)
	}

	volumes[0].Arcs[1].EstimatedChapters = 0
	volumes[0].Arcs[1].Chapters = []domain.OutlineEntry{
		{Title: "第三章"},
		{Title: "第四章"},
		{Title: "第五章"},
	}
	if _, err := repairPipelineOutlineAllDerivedArtifacts(st, volumes); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, volumes); err != nil {
		t.Fatalf("fully expanded continuous outline rejected: %v", err)
	}
}

func TestPipelineOutlineAllProtectedCanonIgnoresHeadlessRuntimeFiles(t *testing.T) {
	outputDir := t.TempDir()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("protected-runtime", 1); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(outputDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("world_rules.json", `[{"rule":"不可变正史"}]`)
	write("logs/headless.log", "first runtime log\n")
	write("meta/run.json", `{"started_at":"first"}`)
	before, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}

	write("logs/headless.log", "second runtime log\n")
	write("logs/agent-debug.log", "new runtime log\n")
	write("meta/run.json", `{"started_at":"second"}`)
	afterRuntime, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterRuntime != before {
		t.Fatalf("headless runtime files changed protected canon: before=%s after=%s", before, afterRuntime)
	}

	write("world_rules.json", `[{"rule":"被篡改正史"}]`)
	afterCanon, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterCanon == before {
		t.Fatal("protected canon did not detect a real foundation mutation")
	}
}
