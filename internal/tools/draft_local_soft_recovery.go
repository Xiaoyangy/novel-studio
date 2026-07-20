package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const draftLocalSoftEditRecoveryReceiptVersion = "draft-local-soft-edit-recovery.v1"

// DraftLocalSoftEditRecoveryChain is an opaque, validated proof captured from
// an isolated render candidate. It may be carried across candidate selection,
// but ReplayDraftLocalSoftEditRecoveryChain revalidates every identity before
// writing anything into the fresh candidate.
type DraftLocalSoftEditRecoveryChain struct {
	Chapter                 int
	PlanCheckpointSeq       int64
	PlanDigest              string
	SourceOutputDir         string
	SourceDraftSeedSeq      int64
	SourceDraftSeedDigest   string
	SourceDraftSeedArtifact string
	SourceConsumedSeq       int64
	SourceConsumedDigest    string
	TokenArtifact           string
	QuotaDigest             string
	TokenSeedCheckpointSeq  int64
	PreEditBodySHA256       string
	SourceFinalEditSeq      int64
	SourceFinalEditDigest   string
	tokenArtifactBytes      []byte
}

type DraftLocalSoftEditRecoveryReceipt struct {
	Version                 string `json:"version"`
	Chapter                 int    `json:"chapter"`
	SourceOutputDir         string `json:"source_output_dir"`
	PlanCheckpointSeq       int64  `json:"plan_checkpoint_seq"`
	PlanDigest              string `json:"plan_digest"`
	QuotaDigest             string `json:"quota_digest"`
	PreEditBodySHA256       string `json:"pre_edit_body_sha256"`
	TokenArtifact           string `json:"token_artifact"`
	TokenArtifactDigest     string `json:"token_artifact_digest"`
	SourceDraftSeedSeq      int64  `json:"source_draft_seed_seq"`
	SourceDraftSeedDigest   string `json:"source_draft_seed_digest"`
	SourceConsumedSeq       int64  `json:"source_consumed_seq"`
	SourceConsumedDigest    string `json:"source_consumed_digest"`
	SourceFinalEditSeq      int64  `json:"source_final_edit_seq"`
	SourceFinalEditDigest   string `json:"source_final_edit_digest"`
	ReplayedDraftSeedSeq    int64  `json:"replayed_draft_seed_seq"`
	ReplayedDraftSeedDigest string `json:"replayed_draft_seed_digest"`
	ReplayedConsumedSeq     int64  `json:"replayed_consumed_seq"`
	ReplayedConsumedDigest  string `json:"replayed_consumed_digest"`
	ReplayedFinalEditSeq    int64  `json:"replayed_final_edit_seq"`
	ReplayedFinalEditDigest string `json:"replayed_final_edit_digest"`
}

// InspectDraftLocalSoftEditRecoveryChain returns only a complete, exact
// initial-draft local-soft chain. Invalid, partial, legacy or review-seeded
// histories are deliberately treated as absent so generic recovery can retain
// its historical single-body-checkpoint fallback without minting authority.
func InspectDraftLocalSoftEditRecoveryChain(
	st *store.Store,
	chapter int,
) *DraftLocalSoftEditRecoveryChain {
	if st == nil || chapter <= 0 {
		return nil
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil || plan == nil || plan.Seq <= 0 || strings.TrimSpace(plan.Digest) == "" {
		return nil
	}
	finalBody, err := st.Drafts.LoadDraft(chapter)
	if err != nil || strings.TrimSpace(finalBody) == "" {
		return nil
	}
	finalCheckpoint, err := CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil || finalCheckpoint.Step != "edit" ||
		finalCheckpoint.Digest != "sha256:"+reviewreport.BodySHA256(finalBody) {
		return nil
	}
	quotaDigest, seedSeq, err := draftLocalSoftEditQuotaIdentity(st, chapter)
	if err != nil {
		return nil
	}
	var seed *domain.Checkpoint
	for _, checkpoint := range st.Checkpoints.All() {
		if checkpoint.Seq == seedSeq {
			copy := checkpoint
			seed = &copy
			break
		}
	}
	draftArtifact := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	if seed == nil || seed.Step != "draft" || !seed.Scope.Matches(domain.ChapterScope(chapter)) ||
		seed.Seq <= plan.Seq || filepath.ToSlash(strings.TrimSpace(seed.Artifact)) != draftArtifact ||
		!canonicalDraftLocalSoftBodyDigest(seed.Digest) {
		return nil
	}
	consumption, err := loadDraftLocalSoftEditConsumption(st, chapter, quotaDigest)
	if err != nil || consumption == nil || consumption.Legacy || consumption.Token == nil ||
		consumption.Checkpoint == nil {
		return nil
	}
	token := consumption.Token
	consumed := consumption.Checkpoint
	if token.Version != draftLocalSoftEditTokenVersion || token.Chapter != chapter ||
		token.QuotaDigest != quotaDigest || token.SeedCheckpointSeq != seed.Seq ||
		!canonicalDraftLocalSoftBodySHA256(token.PreEditBodySHA256) ||
		seed.Digest != "sha256:"+token.PreEditBodySHA256 ||
		consumed.Seq <= seed.Seq || finalCheckpoint.Seq <= consumed.Seq ||
		filepath.ToSlash(strings.TrimSpace(consumed.Artifact)) != draftLocalSoftEditTokenArtifact(chapter, quotaDigest) ||
		filepath.ToSlash(strings.TrimSpace(finalCheckpoint.Artifact)) != draftArtifact ||
		finalCheckpoint.Digest == seed.Digest {
		return nil
	}
	// The bounded path has exactly one pre-consumption body seed and exactly one
	// post-consumption body edit. Extra body epochs cannot be safely inferred.
	for _, checkpoint := range st.Checkpoints.All() {
		if !checkpoint.Scope.Matches(domain.ChapterScope(chapter)) ||
			(checkpoint.Step != "draft" && checkpoint.Step != "edit") ||
			checkpoint.Seq <= plan.Seq {
			continue
		}
		if checkpoint.Seq < consumed.Seq && checkpoint.Seq != seed.Seq {
			return nil
		}
		if checkpoint.Seq > consumed.Seq && checkpoint.Seq != finalCheckpoint.Seq {
			return nil
		}
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(consumed.Artifact)))
	if err != nil || consumed.Digest != "sha256:"+reviewreport.BodySHA256(string(raw)) {
		return nil
	}
	var persisted draftLocalSoftEditToken
	if json.Unmarshal(raw, &persisted) != nil || persisted != *token {
		return nil
	}
	return &DraftLocalSoftEditRecoveryChain{
		Chapter:                 chapter,
		PlanCheckpointSeq:       plan.Seq,
		PlanDigest:              plan.Digest,
		SourceOutputDir:         filepath.Clean(st.Dir()),
		SourceDraftSeedSeq:      seed.Seq,
		SourceDraftSeedDigest:   seed.Digest,
		SourceDraftSeedArtifact: seed.Artifact,
		SourceConsumedSeq:       consumed.Seq,
		SourceConsumedDigest:    consumed.Digest,
		TokenArtifact:           consumed.Artifact,
		QuotaDigest:             quotaDigest,
		TokenSeedCheckpointSeq:  token.SeedCheckpointSeq,
		PreEditBodySHA256:       token.PreEditBodySHA256,
		SourceFinalEditSeq:      finalCheckpoint.Seq,
		SourceFinalEditDigest:   finalCheckpoint.Digest,
		tokenArtifactBytes:      append([]byte(nil), raw...),
	}
}

// ReplayDraftLocalSoftEditRecoveryChain restores the validated causal proof
// before the final edit checkpoint. replayed=false means the fresh journal
// cannot preserve the source seed sequence, so callers must use their existing
// single-checkpoint fallback; no mutation has occurred in that case.
func ReplayDraftLocalSoftEditRecoveryChain(
	st *store.Store,
	chain *DraftLocalSoftEditRecoveryChain,
) (_ *DraftLocalSoftEditRecoveryReceipt, replayed bool, returnErr error) {
	if st == nil || validateDraftLocalSoftEditRecoveryChain(chain) != nil {
		return nil, false, nil
	}
	plan, err := CurrentChapterPlanCheckpoint(st, chain.Chapter)
	if err != nil || plan.Seq != chain.PlanCheckpointSeq || plan.Digest != chain.PlanDigest {
		return nil, false, nil
	}
	latest := st.Checkpoints.LatestGlobal()
	if latest == nil || latest.Seq+1 != chain.SourceDraftSeedSeq {
		return nil, false, nil
	}
	for _, checkpoint := range st.Checkpoints.All() {
		if checkpoint.Scope.Matches(domain.ChapterScope(chain.Chapter)) && checkpoint.Seq > plan.Seq &&
			(checkpoint.Step == "draft" || checkpoint.Step == "edit" ||
				checkpoint.Step == draftLocalSoftEditConsumedStep) {
			return nil, false, nil
		}
	}
	finalBody, err := st.Drafts.LoadDraft(chain.Chapter)
	if err != nil || "sha256:"+reviewreport.BodySHA256(finalBody) != chain.SourceFinalEditDigest {
		return nil, false, nil
	}
	tokenPath := filepath.Join(st.Dir(), filepath.FromSlash(chain.TokenArtifact))
	if _, err := os.Stat(tokenPath); err == nil || !os.IsNotExist(err) {
		return nil, false, nil
	}

	seed, err := st.Checkpoints.AppendLatestAcross(
		domain.ChapterScope(chain.Chapter),
		"draft",
		chain.SourceDraftSeedArtifact,
		chain.SourceDraftSeedDigest,
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		return nil, true, fmt.Errorf("replay local-soft draft seed: %w", err)
	}
	if seed.Seq != chain.SourceDraftSeedSeq || seed.Digest != chain.SourceDraftSeedDigest || seed.Step != "draft" {
		return nil, true, fmt.Errorf("replayed local-soft draft seed identity mismatch")
	}
	if err := writeAtomicDraftIntent(tokenPath, chain.tokenArtifactBytes); err != nil {
		return nil, true, fmt.Errorf("replay local-soft token artifact: %w", err)
	}
	consumed, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chain.Chapter),
		draftLocalSoftEditConsumedStep,
		chain.TokenArtifact,
		"plan", "draft", "edit", draftLocalSoftEditConsumedStep,
	)
	if err != nil {
		return nil, true, fmt.Errorf("replay local-soft consumption checkpoint: %w", err)
	}
	if consumed.Seq <= seed.Seq || consumed.Digest != chain.SourceConsumedDigest ||
		filepath.ToSlash(consumed.Artifact) != filepath.ToSlash(chain.TokenArtifact) {
		return nil, true, fmt.Errorf("replayed local-soft consumption identity mismatch")
	}
	finalEdit, err := st.Checkpoints.AppendArtifactLatestAcross(
		domain.ChapterScope(chain.Chapter),
		"edit",
		fmt.Sprintf("drafts/%02d.draft.md", chain.Chapter),
		"plan", "rerender-request", "draft", "edit",
	)
	if err != nil {
		return nil, true, fmt.Errorf("replay local-soft final edit: %w", err)
	}
	if finalEdit.Seq <= consumed.Seq || finalEdit.Digest != chain.SourceFinalEditDigest || finalEdit.Step != "edit" {
		return nil, true, fmt.Errorf("replayed local-soft final edit identity mismatch")
	}
	quotaDigest, seedSeq, err := draftLocalSoftEditQuotaIdentity(st, chain.Chapter)
	if err != nil || quotaDigest != chain.QuotaDigest || seedSeq != seed.Seq {
		return nil, true, fmt.Errorf("replayed local-soft quota identity mismatch: digest=%s seed=%d err=%v", quotaDigest, seedSeq, err)
	}
	verified, err := loadDraftLocalSoftEditConsumption(st, chain.Chapter, quotaDigest)
	if err != nil || verified == nil || verified.Legacy || verified.Token == nil || verified.Checkpoint == nil ||
		verified.Token.SeedCheckpointSeq != seed.Seq ||
		verified.Token.PreEditBodySHA256 != chain.PreEditBodySHA256 ||
		verified.Checkpoint.Seq != consumed.Seq || verified.Checkpoint.Digest != consumed.Digest {
		return nil, true, fmt.Errorf("replayed local-soft token validation failed: consumption=%+v err=%v", verified, err)
	}
	return &DraftLocalSoftEditRecoveryReceipt{
		Version:                 draftLocalSoftEditRecoveryReceiptVersion,
		Chapter:                 chain.Chapter,
		SourceOutputDir:         chain.SourceOutputDir,
		PlanCheckpointSeq:       chain.PlanCheckpointSeq,
		PlanDigest:              chain.PlanDigest,
		QuotaDigest:             chain.QuotaDigest,
		PreEditBodySHA256:       chain.PreEditBodySHA256,
		TokenArtifact:           chain.TokenArtifact,
		TokenArtifactDigest:     chain.SourceConsumedDigest,
		SourceDraftSeedSeq:      chain.SourceDraftSeedSeq,
		SourceDraftSeedDigest:   chain.SourceDraftSeedDigest,
		SourceConsumedSeq:       chain.SourceConsumedSeq,
		SourceConsumedDigest:    chain.SourceConsumedDigest,
		SourceFinalEditSeq:      chain.SourceFinalEditSeq,
		SourceFinalEditDigest:   chain.SourceFinalEditDigest,
		ReplayedDraftSeedSeq:    seed.Seq,
		ReplayedDraftSeedDigest: seed.Digest,
		ReplayedConsumedSeq:     consumed.Seq,
		ReplayedConsumedDigest:  consumed.Digest,
		ReplayedFinalEditSeq:    finalEdit.Seq,
		ReplayedFinalEditDigest: finalEdit.Digest,
	}, true, nil
}

func validateDraftLocalSoftEditRecoveryChain(chain *DraftLocalSoftEditRecoveryChain) error {
	if chain == nil || chain.Chapter <= 0 || chain.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(chain.PlanDigest) == "" ||
		chain.SourceDraftSeedSeq <= chain.PlanCheckpointSeq ||
		chain.SourceConsumedSeq <= chain.SourceDraftSeedSeq ||
		chain.SourceFinalEditSeq <= chain.SourceConsumedSeq ||
		chain.TokenSeedCheckpointSeq != chain.SourceDraftSeedSeq ||
		!canonicalDraftLocalSoftBodyDigest(chain.SourceDraftSeedDigest) ||
		!canonicalDraftLocalSoftBodyDigest(chain.SourceConsumedDigest) ||
		!canonicalDraftLocalSoftBodyDigest(chain.SourceFinalEditDigest) ||
		!canonicalDraftLocalSoftBodySHA256(chain.PreEditBodySHA256) ||
		chain.SourceDraftSeedDigest != "sha256:"+chain.PreEditBodySHA256 ||
		chain.SourceFinalEditDigest == chain.SourceDraftSeedDigest {
		return fmt.Errorf("local-soft recovery chain identity is incomplete")
	}
	draftArtifact := fmt.Sprintf("drafts/%02d.draft.md", chain.Chapter)
	if filepath.ToSlash(strings.TrimSpace(chain.SourceDraftSeedArtifact)) != draftArtifact ||
		filepath.ToSlash(strings.TrimSpace(chain.TokenArtifact)) != draftLocalSoftEditTokenArtifact(chain.Chapter, chain.QuotaDigest) {
		return fmt.Errorf("local-soft recovery chain artifact identity mismatch")
	}
	seed := &domain.Checkpoint{Seq: chain.SourceDraftSeedSeq, Step: "draft", Digest: chain.SourceDraftSeedDigest}
	plan := &domain.Checkpoint{Seq: chain.PlanCheckpointSeq, Step: "plan", Digest: chain.PlanDigest}
	quotaDigest, err := draftLocalSoftEditQuotaDigest(chain.Chapter, plan, seed)
	if err != nil || quotaDigest != chain.QuotaDigest {
		return fmt.Errorf("local-soft recovery chain quota mismatch")
	}
	if chain.SourceConsumedDigest != "sha256:"+reviewreport.BodySHA256(string(chain.tokenArtifactBytes)) {
		return fmt.Errorf("local-soft recovery token artifact hash mismatch")
	}
	var token draftLocalSoftEditToken
	if json.Unmarshal(chain.tokenArtifactBytes, &token) != nil ||
		token.Version != draftLocalSoftEditTokenVersion || token.Chapter != chain.Chapter ||
		token.QuotaDigest != chain.QuotaDigest || token.SeedCheckpointSeq != chain.SourceDraftSeedSeq ||
		token.PreEditBodySHA256 != chain.PreEditBodySHA256 {
		return fmt.Errorf("local-soft recovery token identity mismatch")
	}
	return nil
}

func canonicalDraftLocalSoftBodySHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func canonicalDraftLocalSoftBodyDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") &&
		canonicalDraftLocalSoftBodySHA256(strings.TrimPrefix(value, "sha256:"))
}
