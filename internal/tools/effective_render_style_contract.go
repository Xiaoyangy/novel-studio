package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/aigc"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

const (
	EffectiveRenderStyleContractPath       = "meta/planning/effective_render_style_contract.json"
	EffectiveRenderStyleContractArchiveDir = "meta/planning/effective_render_style_contracts"
	effectiveRenderStyleContractVersion    = "effective-render-style-contract.v1"
	effectiveStyleCandidateVersion         = "pipeline-render-candidate.v3-effective-style"
	effectiveStyleCandidatePreStyle        = "pipeline-render-candidate.v3-pre-style"
	effectiveStyleCandidatePrevious        = "pipeline-render-candidate.v2"
	effectiveStyleCandidateLegacy          = "pipeline-render-candidate.v1"
)

var ErrRenderStyleContractMissing = errors.New("render packet style_contract is missing")

// EffectiveRenderStyleContractIdentity binds the provider-facing style
// contract to one sealed candidate. It intentionally excludes world/causal
// fields: those remain byte-for-byte owned by the frozen render context.
type EffectiveRenderStyleContractIdentity struct {
	GenerationID              string `json:"generation_id"`
	Chapter                   int    `json:"chapter"`
	PlanDigest                string `json:"plan_digest"`
	PlanCheckpointSeq         int64  `json:"plan_checkpoint_seq"`
	BaseRenderContextSHA256   string `json:"base_render_context_sha256"`
	PipelineRenderInputDigest string `json:"pipeline_render_input_digest"`
	ProjectedBundleDigest     string `json:"projected_bundle_digest"`
	PromotionReceiptDigest    string `json:"promotion_receipt_digest"`
	CandidateID               string `json:"candidate_id"`
}

type EffectiveRenderStyleSourceBody = stylestat.SerialMemorySourceBody

// EffectiveRenderStyleContractReceipt is the immutable bridge shared by the
// Drafter and formal Editor. It captures the exact style object after the
// selected surface-only asset and accepted-prose serial memory are compiled.
// Recovery reads this receipt instead of rebuilding from current config.
type EffectiveRenderStyleContractReceipt struct {
	Version                   string                           `json:"version"`
	GenerationID              string                           `json:"generation_id"`
	Chapter                   int                              `json:"chapter"`
	PlanDigest                string                           `json:"plan_digest"`
	PlanCheckpointSeq         int64                            `json:"plan_checkpoint_seq"`
	BaseRenderContextSHA256   string                           `json:"base_render_context_sha256"`
	PipelineRenderInputDigest string                           `json:"pipeline_render_input_digest"`
	ProjectedBundleDigest     string                           `json:"projected_bundle_digest"`
	PromotionReceiptDigest    string                           `json:"promotion_receipt_digest"`
	CandidateID               string                           `json:"candidate_id"`
	StyleID                   string                           `json:"style_id"`
	StyleAssetSHA256          string                           `json:"style_asset_sha256"`
	StyleContractProtocol     string                           `json:"style_contract_protocol"`
	StyleContract             json.RawMessage                  `json:"style_contract"`
	StyleContractSHA256       string                           `json:"style_contract_sha256"`
	SerialMemoryCompletedSet  []int                            `json:"serial_memory_completed_chapters"`
	SourceChapterBodies       []EffectiveRenderStyleSourceBody `json:"source_chapter_bodies"`
	SerialMemoryStopwords     []string                         `json:"serial_memory_stopwords"`
	SerialMemoryCompiler      string                           `json:"serial_memory_compiler_protocol"`
	SerialMemoryCompilerRoot  string                           `json:"serial_memory_compiler_root_sha256"`
	CreatedAt                 string                           `json:"created_at"`
	ReceiptDigest             string                           `json:"receipt_digest"`
}

type effectiveRenderStyleSerialMemoryInputs struct {
	CompletedChapters []int
	SourceBodies      []EffectiveRenderStyleSourceBody
	Stopwords         []string
	CompilerProtocol  string
	CompilerRoot      string
}

type effectiveRenderStyleCandidateManifest struct {
	Version                     string `json:"version"`
	CandidateID                 string `json:"candidate_id"`
	GenerationID                string `json:"generation_id"`
	Chapter                     int    `json:"chapter"`
	PlanDigest                  string `json:"plan_digest"`
	PlanCheckpointSeq           int64  `json:"plan_checkpoint_seq"`
	ProjectedBundleDigest       string `json:"projected_bundle_digest"`
	PromotionReceiptDigest      string `json:"promotion_receipt_digest"`
	PipelineRenderInputDigest   string `json:"pipeline_render_input_digest"`
	RenderContextSHA256         string `json:"render_context_sha256"`
	EffectiveStyleReceiptDigest string `json:"effective_style_receipt_digest"`
}

// PublishEffectiveRenderStyleContract materializes the only mutable
// provider-facing addition allowed around an immutable sealed render context.
// Calling it again with the same identity and inputs is idempotent; a copied
// prior-chapter receipt is replaced inside the isolated candidate tree.
func PublishEffectiveRenderStyleContract(
	st *store.Store,
	identity EffectiveRenderStyleContractIdentity,
	style string,
	styleBody string,
) (*EffectiveRenderStyleContractReceipt, error) {
	if st == nil || strings.TrimSpace(identity.GenerationID) == "" || identity.Chapter <= 0 ||
		identity.PlanCheckpointSeq <= 0 || strings.TrimSpace(identity.PlanDigest) == "" ||
		strings.TrimSpace(identity.BaseRenderContextSHA256) == "" ||
		strings.TrimSpace(identity.PipelineRenderInputDigest) == "" ||
		strings.TrimSpace(identity.ProjectedBundleDigest) == "" ||
		strings.TrimSpace(identity.PromotionReceiptDigest) == "" ||
		strings.TrimSpace(identity.CandidateID) == "" {
		return nil, fmt.Errorf("effective render style contract requires complete sealed identity")
	}
	base, envelope, err := LoadFrozenDraftRenderContext(st, identity.Chapter, identity.PlanDigest)
	if err != nil {
		return nil, fmt.Errorf("load base context for effective style contract: %w", err)
	}
	if envelope.PayloadSHA256 != identity.BaseRenderContextSHA256 {
		return nil, fmt.Errorf("effective style base context identity drift")
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))
	if existing, loadErr := loadEffectiveRenderStyleContractFile(path); loadErr == nil && existing.CandidateID == identity.CandidateID {
		// Recovery of an already-materialized candidate is strictly load-only.
		// Current assets are irrelevant once this candidate has a valid receipt.
		// Mutable progress/cast state may already contain the target chapter on a
		// post-commit replay; provider dispatch independently applies the strict
		// compiler-input check through its one-shot prose permit.
		if err := validateEffectiveRenderStyleSealedIdentity(st, &existing, &identity); err != nil {
			return nil, err
		}
		manifest, manifestErr := loadEffectiveRenderStyleCandidateManifest(st)
		if manifestErr != nil {
			return nil, manifestErr
		}
		if manifest != nil {
			if err := validateEffectiveRenderStyleCandidateForPublish(manifest, identity); err != nil {
				return nil, err
			}
			if manifest.Version == effectiveStyleCandidateVersion &&
				strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" &&
				manifest.EffectiveStyleReceiptDigest != existing.ReceiptDigest {
				return nil, fmt.Errorf("effective render style receipt digest conflicts with bound candidate manifest")
			}
		}
		if _, err := ensureEffectiveRenderStyleContractArchive(st, existing); err != nil {
			return nil, err
		}
		return &existing, nil
	} else if loadErr != nil && !os.IsNotExist(loadErr) {
		return nil, loadErr
	}
	manifest, manifestErr := loadEffectiveRenderStyleCandidateManifest(st)
	if manifestErr != nil {
		return nil, manifestErr
	}
	if manifest != nil {
		if err := validateEffectiveRenderStyleCandidateForPublish(manifest, identity); err != nil {
			return nil, err
		}
		if manifest.Version == effectiveStyleCandidateVersion &&
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			archivePath, err := EffectiveRenderStyleContractArchivePath(
				identity.Chapter,
				identity.CandidateID,
				manifest.EffectiveStyleReceiptDigest,
			)
			if err != nil {
				return nil, err
			}
			archived, err := LoadArchivedEffectiveRenderStyleContract(
				st,
				archivePath,
				identity.Chapter,
				manifest.EffectiveStyleReceiptDigest,
			)
			if err != nil {
				return nil, fmt.Errorf("effective render style receipt is missing after candidate binding and its archive is unavailable: %w", err)
			}
			if err := validateEffectiveRenderStyleSealedIdentity(st, archived, &identity); err != nil {
				return nil, fmt.Errorf("validate archived effective style receipt for recovery: %w", err)
			}
			raw, err := json.Marshal(archived)
			if err != nil {
				return nil, err
			}
			if err := atomicWriteFrozenRenderContext(path, append(raw, '\n')); err != nil {
				return nil, fmt.Errorf("restore current effective style receipt from immutable archive: %w", err)
			}
			return archived, nil
		}
	}
	effective, err := applyConfiguredStyleOverlay(base, style, styleBody)
	if err != nil {
		return nil, fmt.Errorf("compile configured style overlay: %w", err)
	}
	serialInputs, memory, err := acceptedProseSerialStyleMemory(st, identity.Chapter)
	if err != nil {
		return nil, err
	}
	effective, err = applySerialStyleMemoryOverlay(effective, memory)
	if err != nil {
		return nil, err
	}
	contract, err := ExtractRenderStyleContract(effective)
	if err != nil {
		return nil, err
	}
	contractRaw, err := json.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("encode effective style contract: %w", err)
	}
	style = strings.TrimSpace(style)
	if style == "" {
		style = "default"
	}
	receipt := EffectiveRenderStyleContractReceipt{
		Version:                   effectiveRenderStyleContractVersion,
		GenerationID:              identity.GenerationID,
		Chapter:                   identity.Chapter,
		PlanDigest:                strings.TrimSpace(identity.PlanDigest),
		PlanCheckpointSeq:         identity.PlanCheckpointSeq,
		BaseRenderContextSHA256:   identity.BaseRenderContextSHA256,
		PipelineRenderInputDigest: identity.PipelineRenderInputDigest,
		ProjectedBundleDigest:     identity.ProjectedBundleDigest,
		PromotionReceiptDigest:    identity.PromotionReceiptDigest,
		CandidateID:               identity.CandidateID,
		StyleID:                   style,
		StyleAssetSHA256:          effectiveRenderStyleSHA256([]byte(styleBody)),
		StyleContractProtocol:     RenderStyleContractProtocolVersion,
		StyleContract:             contractRaw,
		StyleContractSHA256:       effectiveRenderStyleSHA256(contractRaw),
		SerialMemoryCompletedSet:  serialInputs.CompletedChapters,
		SourceChapterBodies:       serialInputs.SourceBodies,
		SerialMemoryStopwords:     serialInputs.Stopwords,
		SerialMemoryCompiler:      serialInputs.CompilerProtocol,
		SerialMemoryCompilerRoot:  serialInputs.CompilerRoot,
		CreatedAt:                 envelope.FrozenAt,
	}
	if existing, loadErr := loadEffectiveRenderStyleContractFile(path); loadErr == nil {
		// Reuse only the exact same compiled contract and sealed identity. The
		// timestamp and receipt digest therefore remain stable across recovery.
		if sameEffectiveRenderStyleReceiptInputs(existing, receipt) {
			if err := validateEffectiveRenderStyleContractReceipt(st, &existing, &identity); err != nil {
				return nil, err
			}
			return &existing, nil
		}
	} else if !os.IsNotExist(loadErr) {
		return nil, loadErr
	}
	receipt.ReceiptDigest = effectiveRenderStyleReceiptDigest(receipt)
	// Keep the embedded RawMessage byte-for-byte identical to the canonical
	// style object hashed above. json.MarshalIndent rewrites whitespace inside a
	// RawMessage and would make a freshly reloaded receipt fail its own digest.
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := ensureEffectiveRenderStyleContractArchiveBytes(st, receipt, raw); err != nil {
		return nil, err
	}
	if err := atomicWriteFrozenRenderContext(path, raw); err != nil {
		return nil, fmt.Errorf("publish effective render style contract: %w", err)
	}
	if err := validateEffectiveRenderStyleContractReceipt(st, &receipt, &identity); err != nil {
		return nil, fmt.Errorf("verify effective render style contract: %w", err)
	}
	return &receipt, nil
}

// EffectiveRenderStyleContractArchivePath returns the immutable, content-
// addressed path used by review/acceptance evidence after the mutable current
// pointer advances to a later chapter.
func EffectiveRenderStyleContractArchivePath(
	chapter int,
	candidateID string,
	receiptDigest string,
) (string, error) {
	candidateID = strings.TrimSpace(candidateID)
	receiptDigest = strings.TrimSpace(receiptDigest)
	if chapter <= 0 || candidateID == "" || candidateID == "." || candidateID == ".." ||
		filepath.Base(candidateID) != candidateID || strings.ContainsAny(candidateID, `/\\`) ||
		!validEffectiveRenderStyleSHA256(receiptDigest) {
		return "", fmt.Errorf("effective render style archive identity is malformed")
	}
	return filepath.ToSlash(filepath.Join(
		EffectiveRenderStyleContractArchiveDir,
		fmt.Sprintf("ch%04d", chapter),
		candidateID,
		strings.TrimPrefix(receiptDigest, "sha256:")+".json",
	)), nil
}

func ensureEffectiveRenderStyleContractArchive(
	st *store.Store,
	receipt EffectiveRenderStyleContractReceipt,
) (string, error) {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return ensureEffectiveRenderStyleContractArchiveBytes(st, receipt, append(raw, '\n'))
}

func ensureEffectiveRenderStyleContractArchiveBytes(
	st *store.Store,
	receipt EffectiveRenderStyleContractReceipt,
	raw []byte,
) (string, error) {
	if st == nil {
		return "", fmt.Errorf("effective render style archive requires a store")
	}
	if err := validateEffectiveRenderStyleReceiptSelf(&receipt); err != nil {
		return "", err
	}
	rel, err := EffectiveRenderStyleContractArchivePath(
		receipt.Chapter,
		receipt.CandidateID,
		receipt.ReceiptDigest,
	)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(st.Dir(), filepath.FromSlash(rel))
	if err := ensureEffectiveRenderStyleArchiveParent(st.Dir(), filepath.Dir(filepath.FromSlash(rel))); err != nil {
		return "", err
	}
	if info, statErr := os.Lstat(abs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("effective render style archive is not a real file")
		}
		if _, err := validateEffectiveRenderStyleArchiveFilesystemPath(st.Dir(), rel); err != nil {
			return "", err
		}
		existing, readErr := os.ReadFile(abs)
		if readErr != nil {
			return "", readErr
		}
		if !bytes.Equal(existing, raw) {
			return "", fmt.Errorf("effective render style archive content conflicts with immutable digest path")
		}
		if err := syncFrozenRenderDirectory(filepath.Dir(abs)); err != nil {
			return "", fmt.Errorf("sync existing effective render style archive directory: %w", err)
		}
		return rel, nil
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	file, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return ensureEffectiveRenderStyleContractArchiveBytes(st, receipt, raw)
	}
	if err != nil {
		return "", fmt.Errorf("create immutable effective render style archive: %w", err)
	}
	writeErr := error(nil)
	if _, err := file.Write(raw); err != nil {
		writeErr = err
	} else if err := file.Sync(); err != nil {
		writeErr = err
	}
	if err := file.Close(); writeErr == nil && err != nil {
		writeErr = err
	}
	if writeErr != nil {
		_ = os.Remove(abs)
		return "", fmt.Errorf("write immutable effective render style archive: %w", writeErr)
	}
	if err := syncFrozenRenderDirectory(filepath.Dir(abs)); err != nil {
		return "", fmt.Errorf("sync immutable effective render style archive directory: %w", err)
	}
	return rel, nil
}

func ensureEffectiveRenderStyleArchiveParent(root string, parentRel string) error {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return fmt.Errorf("effective render style archive root must be absolute")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("effective render style archive root must be a real directory")
	}
	cleanParent := filepath.Clean(parentRel)
	if cleanParent == "." || filepath.IsAbs(cleanParent) || cleanParent == ".." ||
		strings.HasPrefix(cleanParent, ".."+string(filepath.Separator)) {
		return fmt.Errorf("effective render style archive parent is unsafe")
	}
	cursor := root
	for _, component := range strings.Split(filepath.ToSlash(cleanParent), "/") {
		if component == "" || component == "." {
			continue
		}
		cursor = filepath.Join(cursor, component)
		info, statErr := os.Lstat(cursor)
		created := false
		if os.IsNotExist(statErr) {
			if err := os.Mkdir(cursor, 0o755); err != nil && !os.IsExist(err) {
				return fmt.Errorf("create effective render style archive directory: %w", err)
			}
			created = true
			info, statErr = os.Lstat(cursor)
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("effective render style archive parent must be a real directory: %s", cursor)
		}
		if created {
			if err := syncFrozenRenderDirectory(filepath.Dir(cursor)); err != nil {
				return fmt.Errorf("sync effective render style archive parent creation: %w", err)
			}
		}
	}
	return nil
}

func validateEffectiveRenderStyleArchiveFilesystemPath(root string, rel string) (string, error) {
	root = filepath.Clean(root)
	cleanRel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if !filepath.IsAbs(root) || cleanRel == "." || filepath.IsAbs(cleanRel) || cleanRel == ".." ||
		strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("effective render style archive filesystem path is unsafe")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", fmt.Errorf("effective render style archive root must be a real directory")
	}
	cursor := root
	components := strings.Split(filepath.ToSlash(cleanRel), "/")
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		cursor = filepath.Join(cursor, component)
		info, err := os.Lstat(cursor)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("effective render style archive path contains a symlink")
		}
		if index == len(components)-1 {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("effective render style archive must be a real file")
			}
		} else if !info.IsDir() {
			return "", fmt.Errorf("effective render style archive parent must be a real directory")
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("effective render style archive resolves outside the project root")
	}
	return cursor, nil
}

// LoadArchivedEffectiveRenderStyleContract verifies an immutable historical
// receipt without requiring the project's mutable CompletedChapters set to
// still equal the set visible at compilation time.
func LoadArchivedEffectiveRenderStyleContract(
	st *store.Store,
	rel string,
	chapter int,
	expectedReceiptDigest string,
) (*EffectiveRenderStyleContractReceipt, error) {
	if st == nil || chapter <= 0 || !validEffectiveRenderStyleSHA256(expectedReceiptDigest) {
		return nil, fmt.Errorf("archived effective render style receipt identity is malformed")
	}
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if clean == "." || clean != strings.TrimSpace(rel) || filepath.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return nil, fmt.Errorf("archived effective render style receipt path is unsafe")
	}
	abs, err := validateEffectiveRenderStyleArchiveFilesystemPath(st.Dir(), clean)
	if err != nil {
		return nil, err
	}
	receipt, err := loadEffectiveRenderStyleContractFile(abs)
	if err != nil {
		return nil, err
	}
	wantPath, err := EffectiveRenderStyleContractArchivePath(
		receipt.Chapter,
		receipt.CandidateID,
		receipt.ReceiptDigest,
	)
	if err != nil || clean != wantPath || receipt.Chapter != chapter ||
		receipt.ReceiptDigest != expectedReceiptDigest {
		return nil, fmt.Errorf("archived effective render style receipt path/identity mismatch")
	}
	if err := validateEffectiveRenderStyleReceiptSelf(&receipt); err != nil {
		return nil, err
	}
	if err := validateEffectiveRenderStyleSourceBodies(st, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// RecoverEffectiveRenderStyleContractArchiveFromCurrent closes the defensive
// current-only recovery edge. Normal publication writes archive -> current,
// so this is not a natural crash state; it exists for an accidentally removed
// archive whose current receipt still proves the exact sealed input. Invalid
// or non-canonical current bytes fail closed instead of triggering a second
// compilation under the same CandidateID.
func RecoverEffectiveRenderStyleContractArchiveFromCurrent(
	st *store.Store,
	expected EffectiveRenderStyleContractIdentity,
) (*EffectiveRenderStyleContractReceipt, string, error) {
	if st == nil {
		return nil, "", fmt.Errorf("recover effective style archive requires store")
	}
	currentPath := filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))
	info, err := os.Lstat(currentPath)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("current effective render style receipt must be a real file")
	}
	raw, err := os.ReadFile(currentPath)
	if err != nil {
		return nil, "", err
	}
	var receipt EffectiveRenderStyleContractReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, "", fmt.Errorf("decode current effective render style receipt: %w", err)
	}
	if err := validateEffectiveRenderStyleSealedIdentity(st, &receipt, &expected); err != nil {
		return nil, "", err
	}
	canonical, err := json.Marshal(receipt)
	if err != nil {
		return nil, "", err
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return nil, "", fmt.Errorf("current effective render style receipt bytes are non-canonical")
	}
	archiveRel, err := EffectiveRenderStyleContractArchivePath(
		receipt.Chapter, receipt.CandidateID, receipt.ReceiptDigest,
	)
	if err != nil {
		return nil, "", err
	}
	writtenRel, err := ensureEffectiveRenderStyleContractArchiveBytes(st, receipt, canonical)
	if err != nil {
		return nil, "", err
	}
	if writtenRel != archiveRel {
		return nil, "", fmt.Errorf("recovered effective render style archive path drifted")
	}
	verified, err := LoadArchivedEffectiveRenderStyleContract(
		st, archiveRel, receipt.Chapter, receipt.ReceiptDigest,
	)
	if err != nil {
		return nil, "", err
	}
	return verified, archiveRel, nil
}

// LoadBoundArchivedEffectiveRenderStyleContract loads the immutable receipt
// named by the active v3 candidate and independently rechecks its frozen
// sealed identity. Unlike LoadEffectiveRenderStyleContract, it deliberately
// does not compare mutable post-render progress/cast inputs with the compiler
// snapshot: committing the target chapter is expected to change both. Use the
// strict mutable-input loader at provider-permit time and this loader only at
// post-body/review/acceptance boundaries.
func LoadBoundArchivedEffectiveRenderStyleContract(
	st *store.Store,
	chapter int,
	planDigest string,
) (map[string]any, *EffectiveRenderStyleContractReceipt, string, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, nil, "", fmt.Errorf("load bound archived effective style contract requires store, chapter and plan digest")
	}
	manifest, err := loadEffectiveRenderStyleCandidateManifest(st)
	if err != nil {
		return nil, nil, "", err
	}
	if manifest == nil {
		return nil, nil, "", fmt.Errorf("bound effective style candidate manifest is missing")
	}
	if manifest.Version != effectiveStyleCandidateVersion || manifest.Chapter != chapter ||
		manifest.PlanDigest != strings.TrimSpace(planDigest) ||
		!validEffectiveRenderStyleSHA256(manifest.PipelineRenderInputDigest) ||
		!validEffectiveRenderStyleSHA256(manifest.RenderContextSHA256) ||
		!validEffectiveRenderStyleSHA256(manifest.EffectiveStyleReceiptDigest) {
		return nil, nil, "", fmt.Errorf("active candidate does not contain a complete v3 effective-style archive binding")
	}
	expected, err := effectiveRenderStyleIdentityFromStore(st, chapter, planDigest)
	if err != nil {
		return nil, nil, "", err
	}
	if expected == nil {
		return nil, nil, "", fmt.Errorf("bound effective style sealed identity is unavailable")
	}
	rel, err := EffectiveRenderStyleContractArchivePath(
		chapter,
		manifest.CandidateID,
		manifest.EffectiveStyleReceiptDigest,
	)
	if err != nil {
		return nil, nil, "", err
	}
	receipt, err := LoadArchivedEffectiveRenderStyleContract(
		st,
		rel,
		chapter,
		manifest.EffectiveStyleReceiptDigest,
	)
	if err != nil {
		return nil, nil, "", err
	}
	if err := validateEffectiveRenderStyleSealedIdentity(st, receipt, expected); err != nil {
		return nil, nil, "", err
	}
	var contract map[string]any
	if err := json.Unmarshal(receipt.StyleContract, &contract); err != nil || len(contract) == 0 {
		return nil, nil, "", fmt.Errorf("archived effective render style contract payload is invalid: %w", err)
	}
	return contract, receipt, rel, nil
}

// LoadEffectiveRenderStyleContract verifies the receipt and returns the exact
// JSON object shared by prose generation and formal review. os.ErrNotExist is
// preserved so legacy frozen contexts can explicitly fall back to their own
// embedded style_contract without consulting current config.
func LoadEffectiveRenderStyleContract(
	st *store.Store,
	chapter int,
	planDigest string,
) (map[string]any, *EffectiveRenderStyleContractReceipt, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return nil, nil, fmt.Errorf("load effective render style contract requires store, chapter and plan digest")
	}
	path := filepath.Join(st.Dir(), filepath.FromSlash(EffectiveRenderStyleContractPath))
	receipt, err := loadEffectiveRenderStyleContractFile(path)
	if err != nil {
		return nil, nil, err
	}
	if receipt.Chapter != chapter || receipt.PlanDigest != strings.TrimSpace(planDigest) {
		return nil, nil, fmt.Errorf("effective render style contract chapter/plan identity mismatch")
	}
	expected, err := effectiveRenderStyleIdentityFromStore(st, chapter, planDigest)
	if err != nil {
		return nil, nil, err
	}
	if err := validateEffectiveRenderStyleContractReceipt(st, &receipt, expected); err != nil {
		return nil, nil, err
	}
	required, expectedReceiptDigest, err := EffectiveRenderStyleContractRequired(st, chapter, planDigest)
	if err != nil {
		return nil, nil, err
	}
	if !required || receipt.ReceiptDigest != expectedReceiptDigest {
		return nil, nil, fmt.Errorf("effective render style receipt is not bound by the active candidate manifest")
	}
	var contract map[string]any
	if err := json.Unmarshal(receipt.StyleContract, &contract); err != nil || len(contract) == 0 {
		return nil, nil, fmt.Errorf("effective render style contract payload is invalid: %w", err)
	}
	return contract, &receipt, nil
}

// EffectiveRenderStyleContractRequired distinguishes new candidates from
// genuine v1/v2 recovery. A v3 manifest is a fail-closed declaration that both
// Drafter and Editor must consume the exact receipt named by the manifest.
func EffectiveRenderStyleContractRequired(
	st *store.Store,
	chapter int,
	planDigest string,
) (bool, string, error) {
	if st == nil || chapter <= 0 || strings.TrimSpace(planDigest) == "" {
		return false, "", fmt.Errorf("effective render style requirement needs store, chapter and plan digest")
	}
	manifest, err := loadEffectiveRenderStyleCandidateManifest(st)
	if err != nil {
		return false, "", err
	}
	if manifest == nil {
		return false, "", nil
	}
	if manifest.Chapter != chapter || manifest.PlanDigest != strings.TrimSpace(planDigest) {
		return false, "", fmt.Errorf("render candidate identity does not match style-contract requirement")
	}
	switch manifest.Version {
	case effectiveStyleCandidateVersion:
		if !validEffectiveRenderStyleSHA256(manifest.PipelineRenderInputDigest) ||
			!validEffectiveRenderStyleSHA256(manifest.RenderContextSHA256) ||
			!validEffectiveRenderStyleSHA256(manifest.EffectiveStyleReceiptDigest) {
			return false, "", fmt.Errorf("v3 render candidate has incomplete effective-style binding")
		}
		return true, manifest.EffectiveStyleReceiptDigest, nil
	case effectiveStyleCandidatePreStyle:
		return false, "", fmt.Errorf("pre-style render candidate has not bound an effective style receipt")
	case effectiveStyleCandidatePrevious, effectiveStyleCandidateLegacy:
		if strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
			return false, "", fmt.Errorf("legacy render candidate contains v3 effective-style fields")
		}
		return false, "", nil
	default:
		return false, "", fmt.Errorf("unsupported render candidate manifest version %q", manifest.Version)
	}
}

// ApplyEffectiveRenderStyleContract replaces only render_packet.style_contract
// on a private provider-facing copy. Every causal/beat/fact field remains from
// the verified frozen context bytes.
func ApplyEffectiveRenderStyleContract(
	raw json.RawMessage,
	st *store.Store,
	chapter int,
	planDigest string,
) (json.RawMessage, error) {
	_, receipt, err := LoadEffectiveRenderStyleContract(st, chapter, planDigest)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode render context for effective style contract: %w", err)
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
	if err != nil {
		return nil, err
	}
	packet["style_contract"] = json.RawMessage(append([]byte(nil), receipt.StyleContract...))
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode effective render style context: %w", err)
	}
	return out, nil
}

// ExtractRenderStyleContract returns an isolated style-contract object from a
// unique prose packet and fails closed on missing/ambiguous packet structure.
func ExtractRenderStyleContract(raw json.RawMessage) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode render context style contract: %w", err)
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
	if err != nil {
		return nil, err
	}
	contract, ok := packet["style_contract"].(map[string]any)
	if !ok || len(contract) == 0 {
		return nil, ErrRenderStyleContractMissing
	}
	encoded, err := json.Marshal(contract)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func applySerialStyleMemoryOverlay(raw json.RawMessage, memory *draftSerialStyleMemory) (json.RawMessage, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	packet, _, err := aigc.FindUniqueProseRenderPacket(payload)
	if err != nil {
		return nil, err
	}
	contract, _ := packet["style_contract"].(map[string]any)
	if contract == nil {
		contract = map[string]any{}
		packet["style_contract"] = contract
	}
	contract["version"] = 3
	if memory == nil {
		delete(contract, "serial_style_memory")
	} else {
		contract["serial_style_memory"] = memory
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func acceptedProseSerialStyleMemory(
	st *store.Store,
	targetChapter int,
) (effectiveRenderStyleSerialMemoryInputs, *draftSerialStyleMemory, error) {
	var inputs effectiveRenderStyleSerialMemoryInputs
	if st == nil || targetChapter <= 0 {
		return inputs, nil, fmt.Errorf("serial style memory requires store and target chapter")
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return inputs, nil, fmt.Errorf("load accepted prose for serial style memory: %w", err)
	}
	var authoritative []int
	if progress != nil {
		authoritative = progress.CompletedChapters
	}
	completed, err := stylestat.CanonicalCompletedChapters(authoritative)
	if err != nil {
		return inputs, nil, fmt.Errorf("canonicalize accepted prose for serial style memory: %w", err)
	}
	stopwords, err := st.CanonicalSerialStyleMemoryStopwords()
	if err != nil {
		return inputs, nil, fmt.Errorf("load serial style memory stopwords: %w", err)
	}
	stopwords = stylestat.CanonicalStopwords(stopwords)
	texts := make([]string, 0, len(completed))
	sources := make([]EffectiveRenderStyleSourceBody, 0, len(completed))
	for _, chapter := range completed {
		if chapter >= targetChapter {
			continue
		}
		body, loadErr := st.Drafts.LoadChapterText(chapter)
		if loadErr != nil {
			return inputs, nil, fmt.Errorf("load accepted chapter %d for serial style memory: %w", chapter, loadErr)
		}
		if strings.TrimSpace(body) == "" {
			return inputs, nil, fmt.Errorf("accepted chapter %d is empty while building serial style memory", chapter)
		}
		texts = append(texts, body)
		sources = append(sources, EffectiveRenderStyleSourceBody{
			Chapter:    chapter,
			BodySHA256: effectiveRenderStyleSHA256([]byte(body)),
		})
	}
	stats := stylestat.Compute(stylestat.Input{
		Chapters:  texts,
		Stopwords: stopwords,
	})
	inputs = effectiveRenderStyleSerialMemoryInputs{
		CompletedChapters: completed,
		SourceBodies:      sources,
		Stopwords:         stopwords,
		CompilerProtocol:  stylestat.SerialMemoryCompilerProtocolVersion,
		CompilerRoot:      stylestat.SerialMemoryCompilerRoot(completed, sources, stopwords),
	}
	return inputs, newDraftSerialStyleMemory(stats), nil
}

func loadEffectiveRenderStyleContractFile(path string) (EffectiveRenderStyleContractReceipt, error) {
	var receipt EffectiveRenderStyleContractReceipt
	raw, err := os.ReadFile(path)
	if err != nil {
		return receipt, err
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("decode %s: %w", EffectiveRenderStyleContractPath, err)
	}
	return receipt, nil
}

func loadEffectiveRenderStyleCandidateManifest(
	st *store.Store,
) (*effectiveRenderStyleCandidateManifest, error) {
	if st == nil {
		return nil, fmt.Errorf("effective render style candidate store is nil")
	}
	path := filepath.Join(st.Dir(), "meta", "planning", "render_candidate.json")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest effectiveRenderStyleCandidateManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode render candidate identity for style receipt: %w", err)
	}
	if strings.TrimSpace(manifest.CandidateID) == "" || manifest.CandidateID == "." ||
		manifest.CandidateID == ".." || filepath.Base(manifest.CandidateID) != manifest.CandidateID ||
		strings.ContainsAny(manifest.CandidateID, `/\\`) || strings.TrimSpace(manifest.GenerationID) == "" ||
		manifest.Chapter <= 0 || !validEffectiveRenderStyleSHA256(manifest.PlanDigest) ||
		!validEffectiveRenderStyleSHA256(manifest.ProjectedBundleDigest) ||
		!validEffectiveRenderStyleSHA256(manifest.PromotionReceiptDigest) {
		return nil, fmt.Errorf("render candidate identity for style receipt is malformed")
	}
	return &manifest, nil
}

func validateEffectiveRenderStyleCandidateForPublish(
	manifest *effectiveRenderStyleCandidateManifest,
	identity EffectiveRenderStyleContractIdentity,
) error {
	if manifest == nil {
		return nil
	}
	if manifest.Version != effectiveStyleCandidateVersion &&
		manifest.Version != effectiveStyleCandidatePreStyle &&
		manifest.Version != effectiveStyleCandidatePrevious &&
		manifest.Version != effectiveStyleCandidateLegacy {
		return fmt.Errorf("unsupported render candidate manifest version %q", manifest.Version)
	}
	if manifest.CandidateID != identity.CandidateID || manifest.GenerationID != identity.GenerationID ||
		manifest.Chapter != identity.Chapter || manifest.PlanDigest != strings.TrimSpace(identity.PlanDigest) ||
		manifest.ProjectedBundleDigest != identity.ProjectedBundleDigest ||
		manifest.PromotionReceiptDigest != identity.PromotionReceiptDigest {
		return fmt.Errorf("render candidate identity does not match effective style publish request")
	}
	if (manifest.Version == effectiveStyleCandidateVersion ||
		manifest.Version == effectiveStyleCandidatePreStyle) &&
		(manifest.PlanCheckpointSeq != identity.PlanCheckpointSeq ||
			manifest.PipelineRenderInputDigest != identity.PipelineRenderInputDigest ||
			manifest.RenderContextSHA256 != identity.BaseRenderContextSHA256) {
		return fmt.Errorf("v3 render candidate input does not match effective style publish request")
	}
	if manifest.Version == effectiveStyleCandidatePreStyle &&
		strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "" {
		return fmt.Errorf("pre-style render candidate already contains a receipt digest")
	}
	if manifest.Version == effectiveStyleCandidatePrevious &&
		(manifest.PlanCheckpointSeq != identity.PlanCheckpointSeq ||
			strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return fmt.Errorf("v2 render candidate identity contains v3 fields or plan drift")
	}
	if manifest.Version == effectiveStyleCandidateLegacy &&
		(strings.TrimSpace(manifest.PipelineRenderInputDigest) != "" ||
			strings.TrimSpace(manifest.RenderContextSHA256) != "" ||
			strings.TrimSpace(manifest.EffectiveStyleReceiptDigest) != "") {
		return fmt.Errorf("v1 render candidate identity contains v3 fields")
	}
	return nil
}

func validateEffectiveRenderStyleContractReceipt(
	st *store.Store,
	receipt *EffectiveRenderStyleContractReceipt,
	expected *EffectiveRenderStyleContractIdentity,
) error {
	if err := validateEffectiveRenderStyleSealedIdentity(st, receipt, expected); err != nil {
		return err
	}
	current, _, err := acceptedProseSerialStyleMemory(st, receipt.Chapter)
	if err != nil {
		return err
	}
	if !slices.Equal(current.CompletedChapters, receipt.SerialMemoryCompletedSet) {
		return fmt.Errorf("effective render style completed chapter set drift")
	}
	if !slices.Equal(current.SourceBodies, receipt.SourceChapterBodies) {
		return fmt.Errorf("effective render style source chapter set drift")
	}
	if !slices.Equal(current.Stopwords, receipt.SerialMemoryStopwords) {
		return fmt.Errorf("effective render style serial-memory stopword drift")
	}
	if current.CompilerProtocol != receipt.SerialMemoryCompiler ||
		current.CompilerRoot != receipt.SerialMemoryCompilerRoot {
		return fmt.Errorf("effective render style serial-memory compiler drift")
	}
	return nil
}

func validateEffectiveRenderStyleSealedIdentity(
	st *store.Store,
	receipt *EffectiveRenderStyleContractReceipt,
	expected *EffectiveRenderStyleContractIdentity,
) error {
	if st == nil || receipt == nil {
		return fmt.Errorf("effective render style receipt is missing")
	}
	if err := validateEffectiveRenderStyleReceiptSelf(receipt); err != nil {
		return err
	}
	if expected != nil && (receipt.GenerationID != expected.GenerationID || receipt.Chapter != expected.Chapter ||
		receipt.PlanDigest != strings.TrimSpace(expected.PlanDigest) ||
		receipt.PlanCheckpointSeq != expected.PlanCheckpointSeq ||
		receipt.BaseRenderContextSHA256 != expected.BaseRenderContextSHA256 ||
		receipt.PipelineRenderInputDigest != expected.PipelineRenderInputDigest ||
		receipt.ProjectedBundleDigest != expected.ProjectedBundleDigest ||
		receipt.PromotionReceiptDigest != expected.PromotionReceiptDigest ||
		receipt.CandidateID != expected.CandidateID) {
		return fmt.Errorf("effective render style receipt does not match sealed candidate identity")
	}
	_, envelope, err := LoadFrozenDraftRenderContext(st, receipt.Chapter, receipt.PlanDigest)
	if err != nil {
		return err
	}
	if envelope.PayloadSHA256 != receipt.BaseRenderContextSHA256 {
		return fmt.Errorf("effective render style receipt base context drift")
	}
	if err := validateEffectiveRenderStyleSourceBodies(st, receipt); err != nil {
		return err
	}
	return nil
}

func validateEffectiveRenderStyleReceiptSelf(receipt *EffectiveRenderStyleContractReceipt) error {
	if receipt == nil {
		return fmt.Errorf("effective render style receipt is missing")
	}
	if receipt.Version != effectiveRenderStyleContractVersion || strings.TrimSpace(receipt.GenerationID) == "" ||
		receipt.Chapter <= 0 || receipt.PlanCheckpointSeq <= 0 ||
		strings.TrimSpace(receipt.PlanDigest) == "" || strings.TrimSpace(receipt.CandidateID) == "" ||
		filepath.Base(receipt.CandidateID) != receipt.CandidateID || strings.ContainsAny(receipt.CandidateID, `/\\`) ||
		strings.TrimSpace(receipt.StyleID) == "" ||
		!validEffectiveRenderStyleSHA256(receipt.PlanDigest) ||
		!validEffectiveRenderStyleSHA256(receipt.BaseRenderContextSHA256) ||
		!validEffectiveRenderStyleSHA256(receipt.PipelineRenderInputDigest) ||
		!validEffectiveRenderStyleSHA256(receipt.ProjectedBundleDigest) ||
		!validEffectiveRenderStyleSHA256(receipt.PromotionReceiptDigest) ||
		!validEffectiveRenderStyleSHA256(receipt.StyleAssetSHA256) ||
		!validEffectiveRenderStyleSHA256(receipt.StyleContractSHA256) ||
		!validEffectiveRenderStyleSHA256(receipt.SerialMemoryCompilerRoot) ||
		!validEffectiveRenderStyleSHA256(receipt.ReceiptDigest) ||
		receipt.StyleContractProtocol != RenderStyleContractProtocolVersion || len(receipt.StyleContract) == 0 ||
		receipt.SerialMemoryCompiler != stylestat.SerialMemoryCompilerProtocolVersion ||
		receipt.SerialMemoryCompletedSet == nil || receipt.SourceChapterBodies == nil ||
		receipt.SerialMemoryStopwords == nil ||
		receipt.StyleContractSHA256 != effectiveRenderStyleSHA256(receipt.StyleContract) ||
		receipt.ReceiptDigest != effectiveRenderStyleReceiptDigest(*receipt) {
		return fmt.Errorf("effective render style receipt identity or digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.CreatedAt); err != nil {
		return fmt.Errorf("effective render style receipt timestamp is invalid: %w", err)
	}
	canonicalCompleted, err := stylestat.CanonicalCompletedChapters(receipt.SerialMemoryCompletedSet)
	if err != nil || !slices.Equal(canonicalCompleted, receipt.SerialMemoryCompletedSet) {
		return fmt.Errorf("effective render style completed chapter set is invalid")
	}
	if !slices.Equal(stylestat.CanonicalStopwords(receipt.SerialMemoryStopwords), receipt.SerialMemoryStopwords) {
		return fmt.Errorf("effective render style serial-memory stopwords are not canonical")
	}
	expectedSourceCount := 0
	for _, chapter := range receipt.SerialMemoryCompletedSet {
		if chapter < receipt.Chapter {
			expectedSourceCount++
		}
	}
	if len(receipt.SourceChapterBodies) != expectedSourceCount {
		return fmt.Errorf("effective render style source bodies do not cover the completed chapter set")
	}
	previous := 0
	for index, source := range receipt.SourceChapterBodies {
		if source.Chapter <= previous || source.Chapter >= receipt.Chapter ||
			!validEffectiveRenderStyleSHA256(source.BodySHA256) ||
			index >= len(canonicalCompleted) || source.Chapter != canonicalCompleted[index] {
			return fmt.Errorf("effective render style source-body identity is invalid")
		}
		previous = source.Chapter
	}
	if receipt.SerialMemoryCompilerRoot != stylestat.SerialMemoryCompilerRoot(
		receipt.SerialMemoryCompletedSet,
		receipt.SourceChapterBodies,
		receipt.SerialMemoryStopwords,
	) {
		return fmt.Errorf("effective render style serial-memory compiler root is invalid")
	}
	return nil
}

func validateEffectiveRenderStyleSourceBodies(
	st *store.Store,
	receipt *EffectiveRenderStyleContractReceipt,
) error {
	if st == nil || receipt == nil {
		return fmt.Errorf("effective render style source-body validation requires store and receipt")
	}
	for _, source := range receipt.SourceChapterBodies {
		body, err := st.Drafts.LoadChapterText(source.Chapter)
		if err != nil || effectiveRenderStyleSHA256([]byte(body)) != source.BodySHA256 {
			return fmt.Errorf("effective render style source chapter %d drift", source.Chapter)
		}
	}
	return nil
}

// effectiveRenderStyleIdentityFromStore independently resolves the candidate
// and frozen input identities. This prevents either Drafter or Editor from
// trusting identity fields found only inside the style receipt itself.
func effectiveRenderStyleIdentityFromStore(
	st *store.Store,
	chapter int,
	planDigest string,
) (*EffectiveRenderStyleContractIdentity, error) {
	manifest, err := loadEffectiveRenderStyleCandidateManifest(st)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return nil, nil
	}
	if manifest.Version != effectiveStyleCandidateVersion || manifest.Chapter != chapter ||
		manifest.PlanDigest != strings.TrimSpace(planDigest) || manifest.PlanCheckpointSeq <= 0 ||
		!validEffectiveRenderStyleSHA256(manifest.PipelineRenderInputDigest) ||
		!validEffectiveRenderStyleSHA256(manifest.RenderContextSHA256) ||
		!validEffectiveRenderStyleSHA256(manifest.EffectiveStyleReceiptDigest) {
		return nil, fmt.Errorf("render candidate identity does not match style receipt request")
	}
	frozenRaw, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "planning", "current_frozen_plan.json"))
	if err != nil {
		return nil, fmt.Errorf("read frozen plan identity for style receipt: %w", err)
	}
	var frozen struct {
		Chapter                int    `json:"chapter"`
		PlanDigest             string `json:"plan_digest"`
		PlanCheckpointSeq      int64  `json:"plan_checkpoint_seq"`
		PlanningGenerationID   string `json:"planning_generation_id"`
		RenderContextSHA256    string `json:"render_context_sha256"`
		PipelineRunInputDigest string `json:"pipeline_run_input_digest"`
		ProjectedBundleDigest  string `json:"projected_bundle_digest"`
		PromotionReceiptDigest string `json:"promotion_receipt_digest"`
	}
	if err := json.Unmarshal(frozenRaw, &frozen); err != nil {
		return nil, fmt.Errorf("decode frozen plan identity for style receipt: %w", err)
	}
	if frozen.Chapter != chapter || frozen.PlanDigest != strings.TrimSpace(planDigest) ||
		frozen.PlanCheckpointSeq != manifest.PlanCheckpointSeq ||
		frozen.PlanningGenerationID != manifest.GenerationID ||
		!validEffectiveRenderStyleSHA256(frozen.RenderContextSHA256) ||
		!validEffectiveRenderStyleSHA256(frozen.PipelineRunInputDigest) ||
		frozen.RenderContextSHA256 != manifest.RenderContextSHA256 ||
		frozen.PipelineRunInputDigest != manifest.PipelineRenderInputDigest ||
		frozen.ProjectedBundleDigest != manifest.ProjectedBundleDigest ||
		frozen.PromotionReceiptDigest != manifest.PromotionReceiptDigest {
		return nil, fmt.Errorf("frozen plan identity does not match style receipt request")
	}
	return &EffectiveRenderStyleContractIdentity{
		GenerationID:              manifest.GenerationID,
		Chapter:                   chapter,
		PlanDigest:                strings.TrimSpace(planDigest),
		PlanCheckpointSeq:         manifest.PlanCheckpointSeq,
		BaseRenderContextSHA256:   frozen.RenderContextSHA256,
		PipelineRenderInputDigest: frozen.PipelineRunInputDigest,
		ProjectedBundleDigest:     manifest.ProjectedBundleDigest,
		PromotionReceiptDigest:    manifest.PromotionReceiptDigest,
		CandidateID:               manifest.CandidateID,
	}, nil
}

func sameEffectiveRenderStyleReceiptInputs(a, b EffectiveRenderStyleContractReceipt) bool {
	return a.Version == b.Version && a.GenerationID == b.GenerationID &&
		a.Chapter == b.Chapter && a.PlanDigest == b.PlanDigest && a.PlanCheckpointSeq == b.PlanCheckpointSeq &&
		a.BaseRenderContextSHA256 == b.BaseRenderContextSHA256 &&
		a.PipelineRenderInputDigest == b.PipelineRenderInputDigest && a.CandidateID == b.CandidateID &&
		a.ProjectedBundleDigest == b.ProjectedBundleDigest && a.PromotionReceiptDigest == b.PromotionReceiptDigest &&
		a.StyleID == b.StyleID && a.StyleAssetSHA256 == b.StyleAssetSHA256 &&
		a.StyleContractProtocol == b.StyleContractProtocol &&
		a.StyleContractSHA256 == b.StyleContractSHA256 &&
		string(a.StyleContract) == string(b.StyleContract) &&
		slices.Equal(a.SerialMemoryCompletedSet, b.SerialMemoryCompletedSet) &&
		slices.Equal(a.SourceChapterBodies, b.SourceChapterBodies) &&
		slices.Equal(a.SerialMemoryStopwords, b.SerialMemoryStopwords) &&
		a.SerialMemoryCompiler == b.SerialMemoryCompiler &&
		a.SerialMemoryCompilerRoot == b.SerialMemoryCompilerRoot
}

func effectiveRenderStyleReceiptDigest(receipt EffectiveRenderStyleContractReceipt) string {
	receipt.ReceiptDigest = ""
	raw, _ := json.Marshal(receipt)
	return effectiveRenderStyleSHA256(raw)
}

func effectiveRenderStyleSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validEffectiveRenderStyleSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
