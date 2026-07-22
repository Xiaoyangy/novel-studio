package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const (
	pipelineRenderStyleEpochIntentVersion = "pipeline-render-style-epoch-intent.v1"
	pipelineRenderStyleEpochIntentDir     = "style-epochs"
)

// pipelineRenderStyleEpochIntent is an immutable, candidate-adjacent protocol
// declaration. It lives outside the disposable candidate tree, so clearing a
// mutable frozen-plan field or relabeling render_candidate.json cannot turn a
// v3 pre-style candidate into a legacy v2 provider request.
type pipelineRenderStyleEpochIntent struct {
	Version                   string `json:"version"`
	CandidateProtocol         string `json:"candidate_protocol"`
	StyleContractProtocol     string `json:"style_contract_protocol"`
	CandidateID               string `json:"candidate_id"`
	GenerationID              string `json:"generation_id"`
	Chapter                   int    `json:"chapter"`
	PlanDigest                string `json:"plan_digest"`
	PlanCheckpointSeq         int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest     string `json:"projected_bundle_digest"`
	PromotionReceiptDigest    string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest string `json:"pipeline_render_input_digest"`
	RenderContextSHA256       string `json:"render_context_sha256"`
	IntentDigest              string `json:"intent_digest"`
}

func pipelineRenderStyleEpochIntentValue(
	frozen *pipelineFrozenPlan,
	candidateID string,
) (pipelineRenderStyleEpochIntent, error) {
	var intent pipelineRenderStyleEpochIntent
	if frozen == nil || frozen.ProjectionBinding != "sealed_v2" ||
		strings.TrimSpace(frozen.PlanningGenerationID) == "" || frozen.Chapter <= 0 ||
		frozen.PlanCheckpointSeq <= 0 || !pipelineRenderInputSHA256(frozen.PlanDigest) ||
		!pipelineRenderInputSHA256(frozen.ProjectedBundleDigest) ||
		!pipelineRenderInputSHA256(frozen.PromotionReceiptDigest) ||
		!pipelineRenderInputSHA256(frozen.PipelineRunInputDigest) ||
		!pipelineRenderInputSHA256(frozen.RenderContextSHA256) {
		return intent, fmt.Errorf("v3 render style epoch intent requires a complete sealed identity")
	}
	wantID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return intent, err
	}
	if candidateID != wantID {
		return intent, fmt.Errorf("v3 render style epoch intent candidate id mismatch")
	}
	intent = pipelineRenderStyleEpochIntent{
		Version:                   pipelineRenderStyleEpochIntentVersion,
		CandidateProtocol:         pipelineRenderCandidateManifestVersion,
		StyleContractProtocol:     tools.RenderStyleContractProtocolVersion,
		CandidateID:               candidateID,
		GenerationID:              frozen.PlanningGenerationID,
		Chapter:                   frozen.Chapter,
		PlanDigest:                frozen.PlanDigest,
		PlanCheckpointSeq:         frozen.PlanCheckpointSeq,
		ProjectedBundleDigest:     frozen.ProjectedBundleDigest,
		PromotionReceiptDigest:    frozen.PromotionReceiptDigest,
		PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
		RenderContextSHA256:       frozen.RenderContextSHA256,
	}
	intent.IntentDigest = pipelineRenderStyleEpochIntentDigest(intent)
	return intent, nil
}

func pipelineRenderStyleEpochIntentDigest(intent pipelineRenderStyleEpochIntent) string {
	intent.IntentDigest = ""
	return pipelineProjectAllDigest(intent)
}

func pipelineRenderStyleEpochIntentPath(liveOutputDir, candidateID string) (string, error) {
	if _, err := pipelineRenderConvergenceDir(liveOutputDir, candidateID); err != nil {
		return "", err
	}
	return filepath.Join(
		pipelineRenderCandidateRoot(liveOutputDir),
		pipelineRenderStyleEpochIntentDir,
		candidateID+".json",
	), nil
}

func pipelineRenderStyleEpochIntentBytes(intent pipelineRenderStyleEpochIntent) ([]byte, error) {
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// loadPipelineRenderV3StyleEpochIntents validates the entire immutable intent
// namespace before returning any declaration from it. Callers must still match
// a declaration against the complete sealed identity they actually possess;
// chapter number alone is not an identity boundary.
func loadPipelineRenderV3StyleEpochIntents(
	liveOutputDir string,
) ([]pipelineRenderStyleEpochIntent, error) {
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, false); err != nil {
		return nil, err
	}
	dir := filepath.Join(
		pipelineRenderCandidateRoot(liveOutputDir),
		pipelineRenderStyleEpochIntentDir,
	)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, true); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	intents := make([]pipelineRenderStyleEpochIntent, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() ||
			filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("render style epoch namespace contains unsafe entry %q", entry.Name())
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var decoded pipelineRenderStyleEpochIntent
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("decode v3 render style epoch intent %q: %w", entry.Name(), err)
		}
		canonical, canonicalErr := pipelineRenderStyleEpochIntentBytes(decoded)
		if canonicalErr != nil {
			return nil, canonicalErr
		}
		identityFrozen := &pipelineFrozenPlan{
			PlanningGenerationID:   decoded.GenerationID,
			Chapter:                decoded.Chapter,
			PlanDigest:             decoded.PlanDigest,
			PlanCheckpointSeq:      decoded.PlanCheckpointSeq,
			ProjectionBinding:      "sealed_v2",
			ProjectedBundleDigest:  decoded.ProjectedBundleDigest,
			PromotionReceiptDigest: decoded.PromotionReceiptDigest,
			PipelineRunInputDigest: decoded.PipelineRenderInputDigest,
		}
		expectedID, idErr := pipelineRenderTransactionID(identityFrozen)
		if idErr != nil || decoded.Version != pipelineRenderStyleEpochIntentVersion ||
			decoded.CandidateProtocol != pipelineRenderCandidateManifestVersion ||
			decoded.StyleContractProtocol != tools.RenderStyleContractProtocolVersion ||
			decoded.CandidateID != expectedID || entry.Name() != decoded.CandidateID+".json" ||
			strings.TrimSpace(decoded.GenerationID) == "" || decoded.Chapter <= 0 || decoded.PlanCheckpointSeq <= 0 ||
			!pipelineRenderInputSHA256(decoded.PlanDigest) ||
			!pipelineRenderInputSHA256(decoded.ProjectedBundleDigest) ||
			!pipelineRenderInputSHA256(decoded.PromotionReceiptDigest) ||
			!pipelineRenderInputSHA256(decoded.PipelineRenderInputDigest) ||
			!pipelineRenderInputSHA256(decoded.RenderContextSHA256) ||
			decoded.IntentDigest != pipelineRenderStyleEpochIntentDigest(decoded) ||
			!bytes.Equal(raw, canonical) {
			return nil, fmt.Errorf("v3 render style epoch intent %q is malformed or non-canonical", entry.Name())
		}
		intents = append(intents, decoded)
	}
	return intents, nil
}

func pipelineRenderStyleEpochIntentMatchesManifestIdentity(
	intent pipelineRenderStyleEpochIntent,
	manifest *pipelineRenderCandidateManifest,
) bool {
	if manifest == nil || intent.GenerationID != manifest.GenerationID ||
		intent.Chapter != manifest.Chapter || intent.PlanDigest != manifest.PlanDigest ||
		intent.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		intent.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		intent.PromotionReceiptDigest != manifest.PromotionReceiptDigest {
		return false
	}
	if value := strings.TrimSpace(manifest.PipelineRenderInputDigest); value != "" &&
		intent.PipelineRenderInputDigest != value {
		return false
	}
	if value := strings.TrimSpace(manifest.RenderContextSHA256); value != "" &&
		intent.RenderContextSHA256 != value {
		return false
	}
	return true
}

func pipelineRenderStyleEpochIntentMatchesPlanIdentity(
	intent pipelineRenderStyleEpochIntent,
	plan domain.ChapterRenderPlanIdentity,
) bool {
	return intent.GenerationID == plan.GenerationID && intent.Chapter == plan.Chapter &&
		intent.PlanDigest == plan.PlanDigest && intent.PlanCheckpointSeq == plan.PlanCheckpointSeq &&
		intent.ProjectedBundleDigest == plan.ProjectedBundleDigest &&
		intent.PromotionReceiptDigest == plan.PromotionReceiptDigest &&
		intent.PipelineRenderInputDigest == plan.PipelineRunInputDigest &&
		intent.RenderContextSHA256 == plan.RenderContextSHA256
}

type pipelineRenderV3BodyStyleEvidence struct {
	CandidateID     string
	PlanDigest      string
	ReceiptDigest   string
	ChapterAccepted bool
}

// inspectPipelineRenderV3BodyStyleEvidence recovers only enough immutable
// evidence to prevent a manifest-loss fallback from reaching a provider. An
// intent is authoritative here only when a fully validated transaction binds
// the exact current chapter bytes and the same complete sealed identity.
func inspectPipelineRenderV3BodyStyleEvidence(
	liveOutputDir string,
	chapter int,
	body []byte,
) (*pipelineRenderV3BodyStyleEvidence, error) {
	if chapter <= 0 {
		return nil, fmt.Errorf("review v3 body style evidence requires a chapter")
	}
	intents, err := loadPipelineRenderV3StyleEpochIntents(liveOutputDir)
	if err != nil {
		return nil, err
	}
	hasChapterIntent := false
	for _, intent := range intents {
		if intent.Chapter == chapter {
			hasChapterIntent = true
			break
		}
	}
	if !hasChapterIntent {
		return nil, nil
	}

	txnStore := store.NewChapterRenderTransactionStore(liveOutputDir)
	root := txnStore.Root()
	rootInfo, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("review chapter render transaction root is not a real directory")
	}
	plansRoot := filepath.Join(root, "plans")
	plansInfo, err := os.Lstat(plansRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if plansInfo.Mode()&os.ModeSymlink != 0 || !plansInfo.IsDir() {
		return nil, fmt.Errorf("review chapter render plans root is not a real directory")
	}
	planEntries, err := os.ReadDir(plansRoot)
	if err != nil {
		return nil, err
	}
	targetBodySHA := domain.ComputeChapterRenderBodySHA256(body)
	bodyReadyName := fmt.Sprintf(
		"%02d-%s.json",
		domain.ChapterRenderPhaseOrdinal(domain.ChapterRenderPhaseBodyReady),
		domain.ChapterRenderPhaseBodyReady,
	)
	var recovered *pipelineRenderV3BodyStyleEvidence
	for _, planEntry := range planEntries {
		planDir := filepath.Join(plansRoot, planEntry.Name())
		planInfo, statErr := os.Lstat(planDir)
		if statErr != nil {
			return nil, statErr
		}
		if planInfo.Mode()&os.ModeSymlink != 0 || !planInfo.IsDir() {
			return nil, fmt.Errorf("review chapter render plans contain unsafe entry %q", planEntry.Name())
		}
		bodiesDir := filepath.Join(planDir, "bodies")
		bodiesInfo, statErr := os.Lstat(bodiesDir)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if bodiesInfo.Mode()&os.ModeSymlink != 0 || !bodiesInfo.IsDir() {
			return nil, fmt.Errorf("review chapter render bodies root is not a real directory")
		}
		bodyEntries, readErr := os.ReadDir(bodiesDir)
		if readErr != nil {
			return nil, readErr
		}
		for _, bodyEntry := range bodyEntries {
			bodyDir := filepath.Join(bodiesDir, bodyEntry.Name())
			bodyDirInfo, statErr := os.Lstat(bodyDir)
			if statErr != nil {
				return nil, statErr
			}
			if bodyDirInfo.Mode()&os.ModeSymlink != 0 || !bodyDirInfo.IsDir() {
				return nil, fmt.Errorf("review chapter render bodies contain unsafe entry %q", bodyEntry.Name())
			}
			bodyPath := filepath.Join(bodyDir, "body.md")
			bodyInfo, statErr := os.Lstat(bodyPath)
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return nil, statErr
			}
			if bodyInfo.Mode()&os.ModeSymlink != 0 || !bodyInfo.Mode().IsRegular() {
				return nil, fmt.Errorf("review chapter render body is not a real file")
			}
			storedBody, readErr := os.ReadFile(bodyPath)
			if readErr != nil {
				return nil, readErr
			}
			if domain.ComputeChapterRenderBodySHA256(storedBody) != targetBodySHA {
				continue
			}

			bodyReadyPath := filepath.Join(bodyDir, bodyReadyName)
			bodyReadyInfo, statErr := os.Lstat(bodyReadyPath)
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil {
				return nil, statErr
			}
			if bodyReadyInfo.Mode()&os.ModeSymlink != 0 || !bodyReadyInfo.Mode().IsRegular() {
				return nil, fmt.Errorf("review body_ready receipt is not a real file")
			}
			raw, readErr := os.ReadFile(bodyReadyPath)
			if readErr != nil {
				return nil, readErr
			}
			var bodyReady domain.ChapterRenderPhaseReceipt
			if decodeErr := decodePipelineChapterRenderJSONStrict(raw, &bodyReady); decodeErr != nil {
				return nil, fmt.Errorf("decode review body_ready receipt: %w", decodeErr)
			}
			if validateErr := domain.ValidateChapterRenderPhaseReceipt(bodyReady); validateErr != nil {
				return nil, fmt.Errorf("validate review body_ready receipt: %w", validateErr)
			}
			if bodyReady.Phase != domain.ChapterRenderPhaseBodyReady ||
				bodyReady.Identity.PlanAttemptID != planEntry.Name() ||
				bodyReady.Identity.TransactionID != bodyEntry.Name() ||
				bodyReady.Identity.BodySHA256 != targetBodySHA ||
				bodyReady.Identity.Plan.Chapter != chapter {
				continue
			}
			var matchedIntent *pipelineRenderStyleEpochIntent
			for i := range intents {
				if pipelineRenderStyleEpochIntentMatchesPlanIdentity(intents[i], bodyReady.Identity.Plan) {
					if matchedIntent != nil {
						return nil, fmt.Errorf("review body_ready matches multiple immutable v3 style epoch intents")
					}
					matchedIntent = &intents[i]
				}
			}
			if matchedIntent == nil {
				continue
			}
			if strings.TrimSpace(bodyReady.Evidence.EffectiveStyleReceiptDigest) == "" {
				return nil, fmt.Errorf("v3 body_ready is missing its effective style receipt digest")
			}
			receipts, loadErr := txnStore.LoadReceipts(bodyReady.Identity)
			if loadErr != nil {
				return nil, fmt.Errorf("load exact review body transaction: %w", loadErr)
			}
			accepted := pipelineChapterRenderReceiptForPhase(
				receipts, domain.ChapterRenderPhaseChapterAccepted,
			) != nil || pipelineChapterRenderReceiptForPhase(
				receipts, domain.ChapterRenderPhaseCompleted,
			) != nil
			found := &pipelineRenderV3BodyStyleEvidence{
				CandidateID:     matchedIntent.CandidateID,
				PlanDigest:      bodyReady.Identity.Plan.PlanDigest,
				ReceiptDigest:   bodyReady.Evidence.EffectiveStyleReceiptDigest,
				ChapterAccepted: accepted,
			}
			if recovered == nil {
				recovered = found
				continue
			}
			if recovered.CandidateID != found.CandidateID ||
				recovered.PlanDigest != found.PlanDigest ||
				recovered.ReceiptDigest != found.ReceiptDigest {
				return nil, fmt.Errorf("current review body has ambiguous immutable v3 style evidence")
			}
			// A single still-open transaction for the same exact style/body
			// identity keeps the fallback closed, even if another convergence
			// attempt with identical prose has already been accepted.
			recovered.ChapterAccepted = recovered.ChapterAccepted && found.ChapterAccepted
		}
	}
	return recovered, nil
}

// pipelineRenderV3StyleEpochDeclared merges the frozen declaration with the
// non-disposable CandidateID intent. The intent remains authoritative when a
// damaged recovery snapshot has cleared mutable frozen/manifest fields.
func pipelineRenderV3StyleEpochDeclared(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
) (bool, error) {
	if frozen == nil {
		return false, fmt.Errorf("render style epoch declaration requires frozen identity")
	}
	declared := frozen.EffectiveStyleProtocol == pipelineRenderCandidateManifestVersion
	if frozen.ProjectionBinding != "sealed_v2" {
		return declared, nil
	}
	candidateID, err := pipelineRenderTransactionID(frozen)
	if err != nil {
		return false, err
	}
	intent, err := inspectPipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, candidateID)
	if err != nil {
		return false, err
	}
	return declared || intent != nil, nil
}

func pipelineRenderV3StyleEpochIntentExists(
	liveOutputDir string,
	candidateID string,
) (bool, error) {
	path, err := pipelineRenderStyleEpochIntentPath(liveOutputDir, candidateID)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if validateErr := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, false); validateErr != nil {
			return false, validateErr
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("v3 render style epoch intent must be a real file")
	}
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, true); err != nil {
		return false, err
	}
	return true, nil
}

func inspectPipelineRenderV3StyleEpochIntent(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidateID string,
) (*pipelineRenderStyleEpochIntent, error) {
	path, err := pipelineRenderStyleEpochIntentPath(liveOutputDir, candidateID)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if validateErr := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, false); validateErr != nil {
			return nil, validateErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect v3 render style epoch intent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("v3 render style epoch intent must be a real file")
	}
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, true); err != nil {
		return nil, err
	}
	expected, err := pipelineRenderStyleEpochIntentValue(frozen, candidateID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	wantRaw, err := pipelineRenderStyleEpochIntentBytes(expected)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, wantRaw) {
		return nil, fmt.Errorf("v3 render style epoch intent bytes or sealed identity drifted")
	}
	var decoded pipelineRenderStyleEpochIntent
	if err := json.Unmarshal(raw, &decoded); err != nil ||
		decoded.IntentDigest != pipelineRenderStyleEpochIntentDigest(decoded) {
		return nil, fmt.Errorf("v3 render style epoch intent digest is invalid")
	}
	return &decoded, nil
}

// inspectPipelineRenderV3StyleEpochIntentForManifest recovers the immutable v3
// declaration without trusting the mutable manifest CandidateID. This path is
// used for published review recovery when current_frozen_plan.json is missing:
// an intent for the same complete sealed identity makes a CandidateID spoof
// fail closed instead of silently selecting the legacy review protocol. An
// unrelated generation or plan for the same chapter remains valid legacy
// history and must not be treated as a downgrade attempt.
func inspectPipelineRenderV3StyleEpochIntentForManifest(
	liveOutputDir string,
	manifest *pipelineRenderCandidateManifest,
) (*pipelineRenderStyleEpochIntent, error) {
	if manifest == nil || manifest.Chapter <= 0 {
		return nil, fmt.Errorf("review style epoch scan requires a candidate manifest")
	}
	intents, err := loadPipelineRenderV3StyleEpochIntents(liveOutputDir)
	if err != nil {
		return nil, err
	}
	var identityIntent *pipelineRenderStyleEpochIntent
	identityIntentCount := 0
	for i := range intents {
		if !pipelineRenderStyleEpochIntentMatchesManifestIdentity(intents[i], manifest) {
			continue
		}
		identityIntentCount++
		if intents[i].CandidateID == manifest.CandidateID {
			copy := intents[i]
			identityIntent = &copy
		}
	}
	if identityIntentCount == 0 {
		return nil, nil
	}
	if identityIntent == nil {
		return nil, fmt.Errorf("review CandidateID conflicts with immutable v3 style epoch intent for the sealed identity")
	}
	return identityIntent, nil
}

func ensurePipelineRenderV3StyleEpochIntent(
	liveOutputDir string,
	frozen *pipelineFrozenPlan,
	candidateID string,
) (*pipelineRenderStyleEpochIntent, error) {
	if existing, err := inspectPipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, candidateID); err != nil || existing != nil {
		return existing, err
	}
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, false); err != nil {
		return nil, err
	}
	namespace := pipelineRenderCandidateRoot(liveOutputDir)
	if err := os.Mkdir(namespace, 0o755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("create render candidate namespace for style epoch: %w", err)
	}
	if err := syncPipelineRenderControlDirectory(filepath.Dir(namespace)); err != nil {
		return nil, fmt.Errorf("sync source parent after render candidate namespace creation: %w", err)
	}
	intentDir := filepath.Join(namespace, pipelineRenderStyleEpochIntentDir)
	if err := os.Mkdir(intentDir, 0o755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("create render style epoch directory: %w", err)
	}
	if err := syncPipelineRenderControlDirectory(namespace); err != nil {
		return nil, fmt.Errorf("sync render candidate namespace after style epoch directory creation: %w", err)
	}
	if err := validatePipelineRenderStyleEpochIntentDirs(liveOutputDir, true); err != nil {
		return nil, err
	}
	intent, err := pipelineRenderStyleEpochIntentValue(frozen, candidateID)
	if err != nil {
		return nil, err
	}
	raw, err := pipelineRenderStyleEpochIntentBytes(intent)
	if err != nil {
		return nil, err
	}
	path, err := pipelineRenderStyleEpochIntentPath(liveOutputDir, candidateID)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if os.IsExist(err) {
		return inspectPipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, candidateID)
	}
	if err != nil {
		return nil, fmt.Errorf("create v3 render style epoch intent: %w", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if n, writeErr := file.Write(raw); writeErr != nil || n != len(raw) {
		if writeErr == nil {
			writeErr = fmt.Errorf("short write %d/%d", n, len(raw))
		}
		return nil, fmt.Errorf("write v3 render style epoch intent: %w", writeErr)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync v3 render style epoch intent: %w", err)
	}
	if err := file.Chmod(0o644); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := syncPipelineRenderControlDirectory(intentDir); err != nil {
		return nil, fmt.Errorf("sync render style epoch directory after intent creation: %w", err)
	}
	complete = true
	return inspectPipelineRenderV3StyleEpochIntent(liveOutputDir, frozen, candidateID)
}

func validatePipelineRenderStyleEpochIntentDirs(liveOutputDir string, requireIntentDir bool) error {
	liveOutputDir = strings.TrimSpace(liveOutputDir)
	if liveOutputDir == "" || !filepath.IsAbs(liveOutputDir) || liveOutputDir != filepath.Clean(liveOutputDir) {
		return fmt.Errorf("render style epoch source output must be a clean absolute path")
	}
	namespace := pipelineRenderCandidateRoot(liveOutputDir)
	intentDir := filepath.Join(namespace, pipelineRenderStyleEpochIntentDir)
	items := []struct {
		name     string
		path     string
		required bool
	}{
		{name: "source parent", path: filepath.Dir(liveOutputDir), required: true},
		{name: "source output", path: liveOutputDir, required: true},
		{name: "candidate namespace", path: namespace, required: requireIntentDir},
		{name: "style epoch directory", path: intentDir, required: requireIntentDir},
	}
	for _, item := range items {
		info, err := os.Lstat(item.path)
		if os.IsNotExist(err) && !item.required {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect render style epoch %s: %w", item.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("render style epoch %s must be a real directory", item.name)
		}
	}
	resolvedLive, err := filepath.EvalSymlinks(liveOutputDir)
	if err != nil {
		return fmt.Errorf("resolve render style epoch source output: %w", err)
	}
	if _, err := os.Lstat(namespace); err == nil {
		resolvedNamespace, resolveErr := filepath.EvalSymlinks(namespace)
		if resolveErr != nil {
			return resolveErr
		}
		if filepath.Clean(resolvedNamespace) != filepath.Join(filepath.Dir(resolvedLive), ".render-candidates") {
			return fmt.Errorf("render style epoch namespace escapes source parent")
		}
	}
	if _, err := os.Lstat(intentDir); err == nil {
		resolvedDir, resolveErr := filepath.EvalSymlinks(intentDir)
		if resolveErr != nil {
			return resolveErr
		}
		if filepath.Clean(resolvedDir) != filepath.Join(filepath.Dir(resolvedLive), ".render-candidates", pipelineRenderStyleEpochIntentDir) {
			return fmt.Errorf("render style epoch directory escapes source namespace")
		}
	}
	return nil
}
