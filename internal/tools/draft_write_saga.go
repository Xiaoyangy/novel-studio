package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// draftWriteIntent closes the crash window between atomically replacing the
// Markdown file and appending its causal/structural checkpoints. The intent is
// written first and removed only after every derived artifact is durable.
type draftWriteIntent struct {
	Chapter             int                    `json:"chapter"`
	Mode                string                 `json:"mode"`
	Artifact            string                 `json:"artifact"`
	PriorBodySHA256     string                 `json:"prior_body_sha256,omitempty"`
	CandidateBodySHA256 string                 `json:"candidate_body_sha256"`
	CausalEpochKey      string                 `json:"causal_epoch_key"`
	Sampling            *domain.SamplingRecord `json:"sampling,omitempty"`
	CreatedAt           string                 `json:"created_at"`
}

func draftWriteIntentPath(projectDir string, chapter int) string {
	return filepath.Join(projectDir, "drafts", fmt.Sprintf("%02d.draft_write_intent.json", chapter))
}

func beginDraftWriteIntent(st *store.Store, chapter int, prior, candidate, mode string, sampling *domain.SamplingRecord) error {
	if st == nil || chapter <= 0 || strings.TrimSpace(candidate) == "" {
		return fmt.Errorf("invalid draft write intent for chapter %d", chapter)
	}
	priorSHA := ""
	if prior != "" {
		priorSHA = reviewreport.BodySHA256(prior)
	}
	intent := draftWriteIntent{
		Chapter:             chapter,
		Mode:                strings.TrimSpace(mode),
		Artifact:            fmt.Sprintf("drafts/%02d.draft.md", chapter),
		PriorBodySHA256:     priorSHA,
		CandidateBodySHA256: reviewreport.BodySHA256(candidate),
		CausalEpochKey:      renderOnlyCausalEpochKey(st, chapter),
		Sampling:            sampling,
		CreatedAt:           time.Now().Format(time.RFC3339Nano),
	}
	raw, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicDraftIntent(draftWriteIntentPath(st.Dir(), chapter), raw)
}

func writeAtomicDraftIntent(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func clearDraftWriteIntent(projectDir string, chapter int) error {
	err := os.Remove(draftWriteIntentPath(projectDir, chapter))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// recoverDraftWriteIntent is called by every external-gate inspection and
// before every new whole-draft write. If candidate bytes reached disk, it
// reconstructs the matching draft/edit checkpoint plus derived local evidence
// idempotently. If the atomic replace never happened, the untouched prior hash
// safely cancels the intent. Epoch drift is fail-closed because old-plan prose
// must not be rebound to a newer plan.
func (t *DraftChapterTool) recoverDraftWriteIntent(chapter int) error {
	path := draftWriteIntentPath(t.store.Dir(), chapter)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var intent draftWriteIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	expectedArtifact := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	if intent.Chapter != chapter || intent.Artifact != expectedArtifact || !validExternalBodySHA256(intent.CandidateBodySHA256) {
		return fmt.Errorf("invalid draft write intent: %+v", intent)
	}
	current, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return err
	}
	currentSHA := ""
	if current != "" {
		currentSHA = reviewreport.BodySHA256(current)
	}
	if currentSHA == strings.TrimSpace(intent.PriorBodySHA256) {
		return clearDraftWriteIntent(t.store.Dir(), chapter)
	}
	if currentSHA != strings.TrimSpace(intent.CandidateBodySHA256) {
		return fmt.Errorf("draft write intent candidate=%s, current=%s, prior=%s", intent.CandidateBodySHA256, currentSHA, intent.PriorBodySHA256)
	}
	if currentEpoch := renderOnlyCausalEpochKey(t.store, chapter); currentEpoch != strings.TrimSpace(intent.CausalEpochKey) {
		return fmt.Errorf("draft write intent belongs to causal epoch %q, current epoch is %q", intent.CausalEpochKey, currentEpoch)
	}
	checkpointStep := "draft"
	switch strings.TrimSpace(intent.Mode) {
	case "write", "append", "merge":
	case "edit":
		checkpointStep = "edit"
	default:
		return fmt.Errorf("unsupported draft write intent mode %q", intent.Mode)
	}
	if _, err := t.store.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chapter), checkpointStep, expectedArtifact,
		"plan", "rerender-request", "draft", "edit",
	); err != nil {
		return fmt.Errorf("recover %s checkpoint: %w", checkpointStep, err)
	}
	if checkpointStep == "draft" {
		if _, err := t.saveDraftAIVoice(chapter, current, intent.Sampling); err != nil {
			return err
		}
	}
	report, gate := inspectDraftAIGCGate(t.store, chapter, current)
	rawGate := draftAIGCRawLocalGateResult(report, gate)
	if err := checkpointDraftStructuralBlock(t.store, chapter, current, report, gate); err != nil {
		return fmt.Errorf("recover draft structural checkpoint: %w", err)
	}
	if !rawGate.Passed {
		if err := persistDraftAIGCRerenderRequirement(t.store, chapter, current, report, gate); err != nil {
			return fmt.Errorf("recover draft AIGC rerender requirement: %w", err)
		}
	}
	return clearDraftWriteIntent(t.store.Dir(), chapter)
}
