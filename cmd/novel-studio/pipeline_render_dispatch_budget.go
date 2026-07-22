package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineRenderDispatchLedgerVersion = domain.PipelineRenderDispatchLedgerVersion
	// One initial whole-body realization plus at most two full rerenders. A
	// fourth Drafter dispatch would be the third rerender and is blocked before
	// any provider request is made.
	pipelineRenderWholeBodyDispatchLimit = domain.PipelineRenderWholeBodyDispatchLimit
)

type pipelineRenderDispatchReservation = domain.PipelineRenderDispatchReservation

type pipelineRenderDispatchLedger = domain.PipelineRenderDispatchLedger

// pipelineRenderDispatchBudgetExhaustedError is a pre-provider circuit breaker.
// It is intentionally separate from exact-body rejection convergence: provider
// crashes and lost partial responses still consume a dispatch reservation and
// therefore cannot create an unbounded retry loop across process restarts.
type pipelineRenderDispatchBudgetExhaustedError struct {
	Chapter  int
	Reserved int
	Limit    int
}

func (e *pipelineRenderDispatchBudgetExhaustedError) Error() string {
	if e == nil {
		return "sealed render whole-body dispatch budget exhausted"
	}
	return fmt.Sprintf(
		"第 %d 章同一 sealed plan 已预留 %d 次完整正文模型调用（初稿 + 最多两次整章重渲染，上限 %d）；第三次重渲染已在 Drafter 调用前熔断，正文、候选与诊断证据均保留",
		e.Chapter,
		e.Reserved,
		e.Limit,
	)
}

func pipelineRenderDispatchBudgetExhausted(err error) bool {
	var target *pipelineRenderDispatchBudgetExhaustedError
	return errors.As(err, &target)
}

var pipelineRenderDispatchProcessMu sync.Mutex

func pipelineRenderDispatchLedgerPath(liveOutputDir, candidateID string) (string, error) {
	dir, err := pipelineRenderConvergenceDir(liveOutputDir, candidateID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dispatch_budget.json"), nil
}

func pipelineRenderDispatchAuthorization(
	manifest pipelineRenderCandidateManifest,
	invocationID string,
	hostTurn int,
) (string, error) {
	if strings.TrimSpace(invocationID) == "" || hostTurn <= 0 {
		return "", fmt.Errorf("render dispatch authorization requires invocation id and host turn")
	}
	digest, err := domain.DeterministicPlanningHash(struct {
		Version                     string `json:"version"`
		CandidateID                 string `json:"candidate_id"`
		SourceOutputDir             string `json:"source_output_dir"`
		GenerationID                string `json:"generation_id"`
		Chapter                     int    `json:"chapter"`
		PipelineRenderInputDigest   string `json:"pipeline_render_input_digest,omitempty"`
		RenderContextSHA256         string `json:"render_context_sha256,omitempty"`
		EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest,omitempty"`
		InvocationID                string `json:"invocation_id"`
		HostTurn                    int    `json:"host_turn"`
	}{
		Version:                     "pipeline-render-dispatch-authorization.v3-source-output",
		CandidateID:                 manifest.CandidateID,
		SourceOutputDir:             manifest.SourceOutputDir,
		GenerationID:                manifest.GenerationID,
		Chapter:                     manifest.Chapter,
		PipelineRenderInputDigest:   manifest.PipelineRenderInputDigest,
		RenderContextSHA256:         manifest.RenderContextSHA256,
		EffectiveStyleReceiptDigest: manifest.EffectiveStyleReceiptDigest,
		InvocationID:                strings.TrimSpace(invocationID),
		HostTurn:                    hostTurn,
	})
	if err != nil {
		return "", err
	}
	return domain.PlanningV2DigestPrefix + digest, nil
}

func newPipelineRenderDispatchLedger(manifest pipelineRenderCandidateManifest) pipelineRenderDispatchLedger {
	return pipelineRenderDispatchLedger{
		Version:                     pipelineRenderDispatchLedgerVersion,
		CandidateID:                 manifest.CandidateID,
		SourceOutputDir:             manifest.SourceOutputDir,
		GenerationID:                manifest.GenerationID,
		Chapter:                     manifest.Chapter,
		PlanDigest:                  manifest.PlanDigest,
		PlanCheckpointSeq:           manifest.PlanCheckpointSeq,
		ProjectedBundleDigest:       manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:      manifest.PromotionReceiptDigest,
		PipelineRenderInputDigest:   manifest.PipelineRenderInputDigest,
		RenderContextSHA256:         manifest.RenderContextSHA256,
		EffectiveStyleReceiptDigest: manifest.EffectiveStyleReceiptDigest,
		Limit:                       pipelineRenderWholeBodyDispatchLimit,
		Reservations:                []pipelineRenderDispatchReservation{},
	}
}

func validatePipelineRenderDispatchLedger(
	ledger *pipelineRenderDispatchLedger,
	manifest pipelineRenderCandidateManifest,
) error {
	if err := domain.ValidatePipelineRenderDispatchLedger(ledger); err != nil {
		return err
	}
	if ledger == nil ||
		ledger.CandidateID != manifest.CandidateID ||
		ledger.SourceOutputDir != manifest.SourceOutputDir ||
		ledger.GenerationID != manifest.GenerationID ||
		ledger.Chapter != manifest.Chapter ||
		ledger.PlanDigest != manifest.PlanDigest ||
		ledger.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		ledger.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		ledger.PromotionReceiptDigest != manifest.PromotionReceiptDigest ||
		ledger.PipelineRenderInputDigest != manifest.PipelineRenderInputDigest ||
		ledger.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		ledger.EffectiveStyleReceiptDigest != manifest.EffectiveStyleReceiptDigest {
		return fmt.Errorf("render dispatch budget ledger identity mismatch")
	}
	return nil
}

func loadPipelineRenderDispatchLedger(
	liveOutputDir string,
	manifest pipelineRenderCandidateManifest,
) (*pipelineRenderDispatchLedger, error) {
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, manifest.CandidateID, true); err != nil {
		return nil, err
	}
	path, err := pipelineRenderDispatchLedgerPath(liveOutputDir, manifest.CandidateID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		ledger := newPipelineRenderDispatchLedger(manifest)
		return &ledger, nil
	}
	if err != nil {
		return nil, err
	}
	var ledger pipelineRenderDispatchLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return nil, fmt.Errorf("decode render dispatch budget ledger: %w", err)
	}
	if err := validatePipelineRenderDispatchLedger(&ledger, manifest); err != nil {
		return nil, err
	}
	return &ledger, nil
}

func savePipelineRenderDispatchLedger(
	liveOutputDir string,
	ledger *pipelineRenderDispatchLedger,
) error {
	if ledger == nil {
		return fmt.Errorf("render dispatch budget ledger is nil")
	}
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, ledger.CandidateID, true); err != nil {
		return err
	}
	ledger.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := domain.ValidatePipelineRenderDispatchLedger(ledger); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path, err := pipelineRenderDispatchLedgerPath(liveOutputDir, ledger.CandidateID)
	if err != nil {
		return err
	}
	return atomicWriteRewriteFile(path, raw, 0o644)
}

func withPipelineRenderDispatchLock(
	liveOutputDir string,
	candidateID string,
	fn func() error,
) error {
	dir, err := ensurePipelineRenderConvergenceControlDir(liveOutputDir, candidateID)
	if err != nil {
		return err
	}
	path, err := pipelineRenderDispatchLedgerPath(liveOutputDir, candidateID)
	if err != nil {
		return err
	}
	if filepath.Dir(path) != dir {
		return fmt.Errorf("render dispatch ledger path escaped authenticated convergence directory")
	}
	pipelineRenderDispatchProcessMu.Lock()
	defer pipelineRenderDispatchProcessMu.Unlock()
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, candidateID, true); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock render dispatch budget: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	if _, err := validatePipelineRenderConvergenceControlDir(liveOutputDir, candidateID, true); err != nil {
		return err
	}
	return fn()
}

// reservePipelineWholeBodyDispatch is the last barrier before a sealed Host
// turn that may call Drafter. A repeated authorization is idempotent; a fresh
// authorization consumes one of the three persistent slots.
func reservePipelineWholeBodyDispatch(
	candidateOutputDir string,
	invocationID string,
	hostTurn int,
) (*pipelineRenderDispatchReservation, bool, error) {
	manifest, err := loadPipelineRenderCandidateManifest(candidateOutputDir)
	if err != nil || manifest == nil {
		return nil, false, fmt.Errorf("reserve render dispatch requires active candidate manifest: %w", err)
	}
	if pipelineRenderConvergenceManifestIsPublishedLive(storeForPipelineRenderDispatch(candidateOutputDir), manifest) {
		return nil, false, fmt.Errorf("published live render manifest cannot reserve a new prose dispatch")
	}
	authorization, err := pipelineRenderDispatchAuthorization(*manifest, invocationID, hostTurn)
	if err != nil {
		return nil, false, err
	}
	liveOutputDir := filepath.Clean(manifest.SourceOutputDir)
	var reserved pipelineRenderDispatchReservation
	reused := false
	err = withPipelineRenderDispatchLock(liveOutputDir, manifest.CandidateID, func() error {
		ledger, loadErr := loadPipelineRenderDispatchLedger(liveOutputDir, *manifest)
		if loadErr != nil {
			return loadErr
		}
		for _, existing := range ledger.Reservations {
			if existing.AuthorizationDigest == authorization {
				reserved = existing
				reused = true
				return nil
			}
		}
		if len(ledger.Reservations) >= ledger.Limit {
			return &pipelineRenderDispatchBudgetExhaustedError{
				Chapter: ledger.Chapter, Reserved: len(ledger.Reservations), Limit: ledger.Limit,
			}
		}
		reserved = pipelineRenderDispatchReservation{
			AuthorizationDigest: authorization,
			Attempt:             len(ledger.Reservations) + 1,
			Status:              "reserved",
			ReservedAt:          time.Now().UTC().Format(time.RFC3339Nano),
		}
		ledger.Reservations = append(ledger.Reservations, reserved)
		return savePipelineRenderDispatchLedger(liveOutputDir, ledger)
	})
	if err != nil {
		return nil, false, err
	}
	return &reserved, reused, nil
}

// finishPipelineWholeBodyDispatch records only opaque outcome metadata. It is
// safe to call after a failed Host turn and never releases a consumed slot.
func finishPipelineWholeBodyDispatch(
	candidateOutputDir string,
	authorizationDigest string,
	status string,
	bodySHA256 string,
	bodyCheckpointSeq int64,
) error {
	manifest, err := loadPipelineRenderCandidateManifest(candidateOutputDir)
	if err != nil || manifest == nil {
		return fmt.Errorf("finish render dispatch requires active candidate manifest: %w", err)
	}
	if pipelineRenderConvergenceManifestIsPublishedLive(storeForPipelineRenderDispatch(candidateOutputDir), manifest) {
		return fmt.Errorf("published live render manifest cannot finish a prose dispatch")
	}
	status = strings.TrimSpace(status)
	if status == "" || strings.TrimSpace(authorizationDigest) == "" {
		return fmt.Errorf("finish render dispatch requires authorization and status")
	}
	liveOutputDir := filepath.Clean(manifest.SourceOutputDir)
	return withPipelineRenderDispatchLock(liveOutputDir, manifest.CandidateID, func() error {
		ledger, loadErr := loadPipelineRenderDispatchLedger(liveOutputDir, *manifest)
		if loadErr != nil {
			return loadErr
		}
		found := false
		for i := range ledger.Reservations {
			entry := &ledger.Reservations[i]
			if entry.AuthorizationDigest != authorizationDigest {
				continue
			}
			found = true
			if entry.FinishedAt != "" {
				if entry.Status == status && entry.BodySHA256 == bodySHA256 && entry.BodyCheckpointSeq == bodyCheckpointSeq {
					return nil
				}
				return fmt.Errorf("render dispatch completion already exists with different evidence")
			}
			entry.Status = status
			entry.BodySHA256 = strings.TrimSpace(bodySHA256)
			entry.BodyCheckpointSeq = bodyCheckpointSeq
			entry.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			break
		}
		if !found {
			return fmt.Errorf("render dispatch authorization is not reserved")
		}
		return savePipelineRenderDispatchLedger(liveOutputDir, ledger)
	})
}

// storeForPipelineRenderDispatch is a tiny seam so the published-live check
// stays identical to the convergence ledger's existing boundary.
func storeForPipelineRenderDispatch(outputDir string) *store.Store {
	return store.NewStore(outputDir)
}

func pipelineRenderDispatchReservations(ledger *pipelineRenderDispatchLedger) []pipelineRenderDispatchReservation {
	if ledger == nil {
		return nil
	}
	out := append([]pipelineRenderDispatchReservation(nil), ledger.Reservations...)
	sort.Slice(out, func(i, j int) bool { return out[i].Attempt < out[j].Attempt })
	return out
}

// pipelineWholeBodyDispatchNeeded distinguishes a Drafter realization from a
// cache/judge/finalizer recovery turn. Only the former consumes the persistent
// prose budget. An existing rejected exact body still requires a fresh slot;
// an approved body awaiting mechanical or agent finalization does not.
func pipelineWholeBodyDispatchNeeded(st *store.Store, chapter int) (bool, int64, error) {
	if st == nil || chapter <= 0 {
		return false, 0, fmt.Errorf("render dispatch classification requires store and chapter")
	}
	manifest, err := loadPipelineRenderCandidateManifest(st.Dir())
	if err != nil {
		return false, 0, err
	}
	// Legacy render-only runs do not own the sealed candidate transaction this
	// ledger is designed to police. A manifest left in an already-published live
	// tree is provenance, not permission to reserve another prose dispatch.
	if manifest == nil || pipelineRenderConvergenceManifestIsPublishedLive(st, manifest) {
		return false, 0, nil
	}
	if manifest.Chapter != chapter {
		return false, 0, fmt.Errorf(
			"render dispatch candidate chapter=%d, requested=%d",
			manifest.Chapter,
			chapter,
		)
	}
	body, err := st.Drafts.LoadDraft(chapter)
	if err != nil {
		return false, 0, err
	}
	if strings.TrimSpace(body) == "" {
		return true, 0, nil
	}
	bodyCheckpoint, err := tools.CurrentChapterBodyCheckpoint(st, chapter)
	if err != nil {
		return false, 0, fmt.Errorf("classify render dispatch current body: %w", err)
	}
	bodySHA256 := strings.TrimPrefix(domain.ComputeChapterRenderBodySHA256([]byte(body)), domain.PlanningV2DigestPrefix)
	ledger, err := syncPipelineRenderConvergence(st)
	if err != nil {
		return false, bodyCheckpoint.Seq, err
	}
	for _, record := range ledger.Records {
		if record.BodySHA256 != bodySHA256 {
			continue
		}
		if record.ExternalBlocking || record.StructuralBlock ||
			(record.SemanticReject && !record.FormalAccepted) {
			return true, bodyCheckpoint.Seq, nil
		}
		break
	}
	inspection, err := tools.InspectDraftExternalGateWithStore(st, chapter)
	if err != nil {
		return false, bodyCheckpoint.Seq, err
	}
	if inspection.Status == tools.DraftExternalGateRerenderAuthorized {
		return true, bodyCheckpoint.Seq, nil
	}
	return false, bodyCheckpoint.Seq, nil
}

func finishPipelineWholeBodyDispatchFromCandidate(
	candidateOutputDir string,
	chapter int,
	baselineBodyCheckpointSeq int64,
	reservation *pipelineRenderDispatchReservation,
	hostErr error,
) error {
	if reservation == nil {
		return nil
	}
	status := "no_durable_body"
	if hostErr != nil {
		status = "provider_or_host_error"
	}
	bodySHA256 := ""
	bodyCheckpointSeq := int64(0)
	st := store.NewStore(candidateOutputDir)
	if checkpoint, err := tools.CurrentChapterBodyCheckpoint(st, chapter); err == nil &&
		checkpoint != nil && checkpoint.Seq > baselineBodyCheckpointSeq {
		body, loadErr := st.Drafts.LoadDraft(chapter)
		if loadErr != nil {
			return loadErr
		}
		bodySHA256 = domain.ComputeChapterRenderBodySHA256([]byte(body))
		if checkpoint.Digest != bodySHA256 {
			return fmt.Errorf("render dispatch produced body/checkpoint digest mismatch")
		}
		bodyCheckpointSeq = checkpoint.Seq
		status = "body_ready"
	}
	return finishPipelineWholeBodyDispatch(
		candidateOutputDir,
		reservation.AuthorizationDigest,
		status,
		bodySHA256,
		bodyCheckpointSeq,
	)
}
