package main

import (
	"os"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/store"
)

// Opt-in operator check over a stopped real attempt. It calls only the exact
// production read-only preflight, never rebase, a model, or directory publish.
func TestFoundationRepairRealSourceReadOnlyPreflight(t *testing.T) {
	live := os.Getenv("NOVEL_STUDIO_REPAIR_PREFLIGHT_OUTPUT")
	if live == "" {
		t.Skip("set an explicit stopped book and manifest for a read-only admission check")
	}
	manifest := os.Getenv("NOVEL_STUDIO_REPAIR_PREFLIGHT_MANIFEST")
	promptPath := os.Getenv("NOVEL_STUDIO_REPAIR_PREFLIGHT_PROMPT")
	if manifest == "" || promptPath == "" {
		t.Fatal("both explicit manifest and original prompt are required")
	}
	before, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	m, digest, err := loadPipelineFoundationRepairManifest(manifest)
	publicationArtifactMust(t, err)
	prompt, err := os.ReadFile(promptPath)
	publicationArtifactMust(t, err)
	plan := &pipelineFoundationRepairPlan{Manifest: m, Digest: digest, ExplicitPrompt: strings.TrimSpace(string(prompt))}
	preflightErr := plan.preflight(live)
	after, err := store.DirectoryContentRoot(live)
	publicationArtifactMust(t, err)
	if after != before {
		t.Fatal("read-only repair preflight changed the live content root")
	}
	publicationArtifactMust(t, preflightErr)
	t.Logf("target=%s source=%s report=%s unchanged_live_root=%s model_calls=0", m.Target, m.ExpectedSourceDigest, m.ReportDigest, before)
}
