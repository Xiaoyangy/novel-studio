package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestLoadPipelineEvidenceDetectsMissingArtifactsAndCheckpoints(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	state := domain.PipelineState{
		Stages:    []string{"write"},
		Completed: []string{"write"},
		Evidence: map[string]domain.PipelineStageEvidence{
			"write": {
				Stage:       "write",
				Status:      "verified",
				Artifacts:   []string{"chapters/01.md"},
				Checkpoints: []string{"chapter:1:commit_chapter#99"},
			},
		},
	}
	writePipelineState(t, dir, state)

	snap := Load(st)
	if snap.Pipeline == nil {
		t.Fatal("expected pipeline state to load")
	}
	if got := strings.Join(snap.PipelineMissingArtifacts["write"], ","); got != "chapters/01.md" {
		t.Fatalf("missing artifacts = %q, want chapters/01.md", got)
	}
	if got := strings.Join(snap.PipelineMissingCheckpoints["write"], ","); got != "chapter:1:commit_chapter#99" {
		t.Fatalf("missing checkpoints = %q, want chapter:1:commit_chapter#99", got)
	}
}

func TestLoadPipelineEvidenceAcceptsExistingArtifactAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	mustWriteDiagFile(t, filepath.Join(dir, "chapters", "01.md"), "第一章正文")
	checkpoint, err := st.Checkpoints.AppendArtifact(domain.ChapterScope(1), "commit_chapter", "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}

	state := domain.PipelineState{
		Stages:    []string{"write"},
		Completed: []string{"write"},
		Evidence: map[string]domain.PipelineStageEvidence{
			"write": {
				Stage:       "write",
				Status:      "verified",
				Artifacts:   []string{"chapters/01.md"},
				Checkpoints: []string{formatTestCheckpointRef(checkpoint)},
			},
		},
	}
	writePipelineState(t, dir, state)

	snap := Load(store.NewStore(dir))
	if len(snap.PipelineMissingArtifacts["write"]) != 0 {
		t.Fatalf("unexpected missing artifacts: %+v", snap.PipelineMissingArtifacts["write"])
	}
	if len(snap.PipelineMissingCheckpoints["write"]) != 0 {
		t.Fatalf("unexpected missing checkpoints: %+v", snap.PipelineMissingCheckpoints["write"])
	}
}

func TestPipelineEvidenceDriftReportsCompletedStageDrift(t *testing.T) {
	snap := &Snapshot{
		Pipeline: &domain.PipelineState{
			Completed: []string{"review"},
			Evidence: map[string]domain.PipelineStageEvidence{
				"review": {Stage: "review", Status: "verified"},
			},
		},
		PipelineMissingArtifacts: map[string][]string{"review": []string{"reviews/01.md"}},
	}

	findings := PipelineEvidenceDrift(snap)
	finding := requireFinding(t, findings, "PipelineEvidenceDrift")
	if finding.Severity != SevCritical {
		t.Fatalf("severity = %s, want critical", finding.Severity)
	}
	if !strings.Contains(finding.Evidence, "reviews/01.md") {
		t.Fatalf("evidence does not include missing artifact: %s", finding.Evidence)
	}
}

func TestPipelineEvidenceDriftReportsMissingEvidenceAndPendingRerun(t *testing.T) {
	snap := &Snapshot{
		Pipeline: &domain.PipelineState{
			Completed: []string{"write"},
			Evidence: map[string]domain.PipelineStageEvidence{
				"review": {Stage: "review", Status: "invalid", Message: "missing reviews/01.md"},
			},
		},
	}

	findings := PipelineEvidenceDrift(snap)
	requireFinding(t, findings, "PipelineEvidenceMissing")
	requireFinding(t, findings, "PipelineStagePendingRerun")
}

func writePipelineState(t *testing.T, dir string, state domain.PipelineState) {
	t.Helper()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "meta", "pipeline.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustWriteDiagFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func formatTestCheckpointRef(checkpoint *domain.Checkpoint) string {
	return "chapter:" + strconv.Itoa(checkpoint.Scope.Chapter) + ":" + checkpoint.Step + "#" + strconv.FormatInt(checkpoint.Seq, 10)
}

func requireFinding(t *testing.T, findings []Finding, rule string) Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.Rule == rule {
			return finding
		}
	}
	t.Fatalf("expected finding %s, got %+v", rule, findings)
	return Finding{}
}
