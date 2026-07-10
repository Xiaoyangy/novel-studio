package main

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

const (
	rewriteRecoveryCandidateStaged = "candidate_staged"
	rewriteRecoveryReviewPending   = "review_pending"
)

// rewriteRecoveryState closes the crash window between swapping a rewritten
// body and producing its new review. The candidate hash is journaled before the
// chapter file changes, so a restart can recognize the swapped body without
// requiring the now-invalid old review.
type rewriteRecoveryState struct {
	Chapter       int    `json:"chapter"`
	State         string `json:"state"`
	BeforeSHA256  string `json:"before_sha256"`
	CandidateSHA  string `json:"candidate_sha256"`
	CandidatePath string `json:"candidate_path"`
	UpdatedAt     string `json:"updated_at"`
}

func rewriteRecoveryStatePath(root string, chapter int) string {
	return filepath.Join(root, "meta", "rewrite_recovery", fmt.Sprintf("%02d.json", chapter))
}

func rewriteRecoveryCandidatePath(root string, chapter int) string {
	return filepath.Join(root, "drafts", fmt.Sprintf("%02d.rewrite.candidate.md", chapter))
}

func stageRewriteRecovery(root string, chapter int, before, candidate string) error {
	candidatePath := rewriteRecoveryCandidatePath(root, chapter)
	if err := atomicWriteRewriteFile(candidatePath, []byte(candidate), 0o644); err != nil {
		return fmt.Errorf("stage rewrite candidate: %w", err)
	}
	state := rewriteRecoveryState{
		Chapter:       chapter,
		State:         rewriteRecoveryCandidateStaged,
		BeforeSHA256:  reviewreport.BodySHA256(before),
		CandidateSHA:  reviewreport.BodySHA256(candidate),
		CandidatePath: filepath.ToSlash(strings.TrimPrefix(candidatePath, root+string(filepath.Separator))),
		UpdatedAt:     time.Now().Format(time.RFC3339),
	}
	return saveRewriteRecoveryState(root, state)
}

func markRewriteReviewPending(root string, chapter int) error {
	state, err := loadRewriteRecoveryState(root, chapter)
	if err != nil || state == nil {
		return err
	}
	state.State = rewriteRecoveryReviewPending
	state.UpdatedAt = time.Now().Format(time.RFC3339)
	return saveRewriteRecoveryState(root, *state)
}

func rewriteAwaitingReview(root string, st *store.Store, chapter int, body string) (bool, error) {
	bodySHA := reviewreport.BodySHA256(body)
	state, err := loadRewriteRecoveryState(root, chapter)
	if err != nil {
		return false, err
	}
	if state != nil && state.CandidateSHA == bodySHA {
		if err := currentChapterReviewError(root, chapter); err == nil {
			return false, clearRewriteRecovery(root, chapter)
		}
		return true, nil
	}

	// Backward-compatible recovery for bodies swapped before the journal was
	// introduced. A matching rewrite checkpoint newer than the last review is
	// sufficient proof that the current body should proceed directly to review.
	if st == nil {
		return false, nil
	}
	rewriteCP := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "rewrite-body-swapped")
	if rewriteCP == nil {
		rewriteCP = st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "rewrite-existing")
	}
	if rewriteCP == nil || rewriteCP.Digest != "sha256:"+bodySHA {
		return false, nil
	}
	if reviewCP := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "review"); reviewCP != nil && reviewCP.Seq > rewriteCP.Seq {
		return false, nil
	}
	return currentChapterReviewError(root, chapter) != nil, nil
}

func loadRewriteRecoveryState(root string, chapter int) (*rewriteRecoveryState, error) {
	raw, err := os.ReadFile(rewriteRecoveryStatePath(root, chapter))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state rewriteRecoveryState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode rewrite recovery state: %w", err)
	}
	return &state, nil
}

func saveRewriteRecoveryState(root string, state rewriteRecoveryState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteRewriteFile(rewriteRecoveryStatePath(root, state.Chapter), raw, 0o644)
}

func clearRewriteRecovery(root string, chapter int) error {
	for _, path := range []string{rewriteRecoveryStatePath(root, chapter), rewriteRecoveryCandidatePath(root, chapter)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func atomicWriteRewriteFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rewrite-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
