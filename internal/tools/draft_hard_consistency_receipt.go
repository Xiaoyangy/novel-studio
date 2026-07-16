package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const draftHardConsistencyReceiptVersion = 1

// DraftHardConsistencyReceipt is the durable, exact-body proof emitted by a
// successful check_consistency run. A plain checkpoint is intentionally not a
// pass signal: commit/recovery must validate Passed plus every identity below.
type DraftHardConsistencyReceipt struct {
	Version              int      `json:"version"`
	Chapter              int      `json:"chapter"`
	BodySHA256           string   `json:"body_sha256"`
	Passed               bool     `json:"passed"`
	HardGateViolations   []string `json:"hard_gate_violations,omitempty"`
	PlanCheckpointSeq    int64    `json:"plan_checkpoint_seq,omitempty"`
	PlanCheckpointDigest string   `json:"plan_checkpoint_digest,omitempty"`
	BodyCheckpointSeq    int64    `json:"body_checkpoint_seq,omitempty"`
	BodyCheckpointDigest string   `json:"body_checkpoint_digest,omitempty"`
}

func draftHardConsistencyReceiptArtifact(chapter int) string {
	return fmt.Sprintf("drafts/%02d.hard_consistency.json", chapter)
}

func persistDraftHardConsistencyReceipt(st *store.Store, chapter int, content string, violations []string) (*domain.Checkpoint, DraftHardConsistencyReceipt, error) {
	receipt := DraftHardConsistencyReceipt{
		Version:            draftHardConsistencyReceiptVersion,
		Chapter:            chapter,
		BodySHA256:         reviewreport.BodySHA256(content),
		Passed:             len(violations) == 0,
		HardGateViolations: append([]string(nil), violations...),
	}
	scope := domain.ChapterScope(chapter)
	if st.Checkpoints.LatestByStep(scope, "plan") != nil {
		cp, err := CurrentChapterPlanCheckpoint(st, chapter)
		if err != nil {
			return nil, receipt, fmt.Errorf("capture hard consistency plan identity: %w", err)
		}
		receipt.PlanCheckpointSeq = cp.Seq
		receipt.PlanCheckpointDigest = cp.Digest
	}
	if st.Checkpoints.LatestByStep(scope, "draft") != nil || st.Checkpoints.LatestByStep(scope, "edit") != nil {
		cp, err := CurrentChapterBodyCheckpoint(st, chapter)
		if err != nil {
			return nil, receipt, fmt.Errorf("capture hard consistency body identity: %w", err)
		}
		receipt.BodyCheckpointSeq = cp.Seq
		receipt.BodyCheckpointDigest = cp.Digest
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, receipt, fmt.Errorf("encode hard consistency receipt: %w", err)
	}
	artifact := draftHardConsistencyReceiptArtifact(chapter)
	if err := writeAtomicDraftIntent(filepath.Join(st.Dir(), artifact), raw); err != nil {
		return nil, receipt, fmt.Errorf("write hard consistency receipt: %w", err)
	}
	step := "consistency_check"
	if !receipt.Passed {
		step = "consistency_check_failed"
	}
	cp, err := st.Checkpoints.AppendArtifactLatestAcross(
		scope, step, artifact,
		"plan", "rerender-request", "draft", "edit", "consistency_check", "consistency_check_failed",
	)
	if err != nil {
		return nil, receipt, fmt.Errorf("checkpoint hard consistency receipt: %w", err)
	}
	return cp, receipt, nil
}

func loadDraftHardConsistencyReceipt(st *store.Store, chapter int) (DraftHardConsistencyReceipt, error) {
	var receipt DraftHardConsistencyReceipt
	raw, err := os.ReadFile(filepath.Join(st.Dir(), draftHardConsistencyReceiptArtifact(chapter)))
	if err != nil {
		return receipt, err
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func requirePassedDraftHardConsistencyReceipt(st *store.Store, chapter int, content string) (*domain.Checkpoint, error) {
	scope := domain.ChapterScope(chapter)
	bodyCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return nil, fmt.Errorf("第 %d 章当前草稿没有精确正文 checkpoint：%w", chapter, err)
	}
	receipt, err := loadDraftHardConsistencyReceipt(st, chapter)
	if err != nil {
		return nil, fmt.Errorf("第 %d 章缺少 exact-body hard consistency receipt: %v: %w", chapter, err, errs.ErrToolPrecondition)
	}
	wantBodySHA := reviewreport.BodySHA256(content)
	if receipt.Version != draftHardConsistencyReceiptVersion || receipt.Chapter != chapter ||
		!receipt.Passed || len(receipt.HardGateViolations) != 0 || receipt.BodySHA256 != wantBodySHA ||
		receipt.BodyCheckpointSeq != bodyCheckpoint.Seq || receipt.BodyCheckpointDigest != bodyCheckpoint.Digest {
		return nil, fmt.Errorf("第 %d 章 hard consistency receipt 未通过或未绑定当前正文 epoch（passed=%t, receipt_sha=%q, current_sha=%q）: %w",
			chapter, receipt.Passed, receipt.BodySHA256, wantBodySHA, errs.ErrToolPrecondition)
	}
	if st.Checkpoints.LatestByStep(scope, "plan") != nil {
		planCheckpoint, planErr := CurrentChapterPlanCheckpoint(st, chapter)
		if planErr != nil || receipt.PlanCheckpointSeq != planCheckpoint.Seq || receipt.PlanCheckpointDigest != planCheckpoint.Digest {
			return nil, fmt.Errorf("第 %d 章 hard consistency receipt 未绑定当前 plan epoch: %v: %w", chapter, planErr, errs.ErrToolPrecondition)
		}
	} else if receipt.PlanCheckpointSeq != 0 || strings.TrimSpace(receipt.PlanCheckpointDigest) != "" {
		return nil, fmt.Errorf("第 %d 章 hard consistency receipt 引用了不存在的 plan epoch: %w", chapter, errs.ErrToolPrecondition)
	}
	passCheckpoint := st.Checkpoints.LatestByStep(scope, "consistency_check")
	failedCheckpoint := st.Checkpoints.LatestByStep(scope, "consistency_check_failed")
	if passCheckpoint == nil || passCheckpoint.Artifact != draftHardConsistencyReceiptArtifact(chapter) ||
		passCheckpoint.Seq <= bodyCheckpoint.Seq || (failedCheckpoint != nil && failedCheckpoint.Seq > passCheckpoint.Seq) {
		return nil, fmt.Errorf("第 %d 章没有当前正文 epoch 后的成功 hard consistency checkpoint: %w", chapter, errs.ErrToolPrecondition)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), passCheckpoint.Artifact))
	if err != nil || passCheckpoint.Digest != "sha256:"+reviewreport.BodySHA256(string(raw)) {
		return nil, fmt.Errorf("第 %d 章 hard consistency receipt 与成功 checkpoint 摘要不匹配: %v: %w", chapter, err, errs.ErrToolPrecondition)
	}
	return passCheckpoint, nil
}
