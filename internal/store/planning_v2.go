package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	projectedPlanningV2Root         = "meta/planning/v2"
	projectedBuildingDir            = "meta/planning/v2/.building"
	projectedGenerationsDir         = "meta/planning/v2/generations"
	projectedActiveGenerationPath   = "meta/planning/v2/active_generation.json"
	projectedProjectionCursorPath   = "meta/planning/v2/projection_cursor.json"
	projectedRealizationCursorPath  = "meta/planning/v2/realization_cursor.json"
	projectedLifecycleDir           = "meta/planning/v2/lifecycle"
	projectedPromotionReceiptsDir   = "meta/planning/v2/promotion_receipts"
	projectedActualOutcomesDir      = "meta/planning/v2/actual_outcomes"
	projectedGenerationManifestFile = "generation.json"
	projectedSourceSnapshotFile     = "source_snapshot.json"
	projectedObligationRegistryFile = "obligation_registry.json"
	projectedChainManifestFile      = "manifests/chain.json"
	projectedSealReceiptFile        = "seal_receipt.json"
	projectedWriteLockFile          = "meta/planning/v2/.write.lock"
	projectedInvalidationsDir       = "meta/planning/v2/lifecycle/invalidations"
	projectedArchivesDir            = "meta/planning/v2/lifecycle/archives"
	projectedChapterIntentsDir      = "meta/planning/v2/intents/project_chapter"
	projectedIntentCompletionsDir   = "meta/planning/v2/intent_completions/project_chapter"
)

var projectedPathComponentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// ProjectedStoreV2 owns only files below meta/planning/v2. In particular, it
// has no reference to ProgressStore, DraftStore, WorldStore, or RAGStore and
// therefore cannot advance canon or persist prose.
type ProjectedStoreV2 struct {
	io        *IO
	now       func() time.Time
	testFault func(stage string) error
}

type projectedChapterIntentV2 struct {
	Version                  string                        `json:"version"`
	GenerationID             string                        `json:"generation_id"`
	Chapter                  int                           `json:"chapter"`
	ExpectedGenerationDigest string                        `json:"expected_generation_digest"`
	ExpectedTailDigest       string                        `json:"expected_tail_digest"`
	ExpectedRegistryRoot     string                        `json:"expected_registry_root"`
	ExpectedCursor           domain.ProjectionCursorV2     `json:"expected_cursor"`
	Bundle                   domain.ProjectedChapterBundle `json:"bundle"`
	NextRegistry             domain.ObligationRegistryV2   `json:"next_registry"`
	NextCursor               domain.ProjectionCursorV2     `json:"next_cursor"`
	IntentDigest             string                        `json:"intent_digest"`
}

type projectedChapterIntentCompletionV2 struct {
	Version              string `json:"version"`
	GenerationID         string `json:"generation_id"`
	Chapter              int    `json:"chapter"`
	IntentDigest         string `json:"intent_digest"`
	ProjectionCursorRoot string `json:"projection_cursor_root"`
	CompletedAt          string `json:"completed_at"`
	ReceiptDigest        string `json:"receipt_digest"`
}

func NewProjectedStoreV2(io *IO) *ProjectedStoreV2 {
	return &ProjectedStoreV2{
		io:  io,
		now: time.Now,
	}
}

func (s *ProjectedStoreV2) failProjectedStage(stage string) error {
	if s.testFault == nil {
		return nil
	}
	return s.testFault(stage)
}

// ProjectedV2 exposes the isolated v2 projected store without adding canon
// write capabilities to it.
func (s *Store) ProjectedV2() *ProjectedStoreV2 {
	return NewProjectedStoreV2(s.Planning.io)
}

func projectedBuildingGenerationPath(generationID string) string {
	return filepath.Join(projectedBuildingDir, generationID)
}

func projectedSealedGenerationPath(generationID string) string {
	return filepath.Join(projectedGenerationsDir, generationID)
}

func projectedBundlePath(base string, chapter int) string {
	return filepath.Join(base, "chapters", fmt.Sprintf("%04d.bundle.json", chapter))
}

func projectedPromotionReceiptPath(generationID string, chapter int, digest string) string {
	return filepath.Join(projectedPromotionReceiptsDir, generationID, fmt.Sprintf("%04d", chapter), digest+".json")
}

func projectedActualOutcomePath(generationID string, chapter int, digest string) string {
	return filepath.Join(projectedActualOutcomesDir, generationID, fmt.Sprintf("%04d", chapter), digest+".json")
}

func projectedInvalidationReceiptPath(generationID, digest string) string {
	return filepath.Join(projectedInvalidationsDir, generationID, digest+".json")
}

func projectedArchiveReceiptPath(generationID, digest string) string {
	return filepath.Join(projectedArchivesDir, generationID, digest+".json")
}

func projectedChapterIntentPath(generationID string, chapter int, digest string) string {
	return filepath.Join(projectedChapterIntentsDir, generationID, fmt.Sprintf("%04d", chapter), digest+".json")
}

func projectedChapterIntentCompletionPath(generationID string, chapter int, digest string) string {
	return filepath.Join(projectedIntentCompletionsDir, generationID, fmt.Sprintf("%04d", chapter), digest+".json")
}

func validateProjectedPathComponent(name, value string) error {
	value = strings.TrimSpace(value)
	if !projectedPathComponentPattern.MatchString(value) || value == "." || value == ".." {
		return fmt.Errorf("%s %q is not a safe path component", name, value)
	}
	return nil
}

func validateProjectedChapter(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	return nil
}

func canonicalIndentedJSON(v any) ([]byte, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func sameJSON(left, right []byte) bool {
	var lv any
	var rv any
	if json.Unmarshal(left, &lv) != nil || json.Unmarshal(right, &rv) != nil {
		return bytes.Equal(left, right)
	}
	lraw, lerr := json.Marshal(lv)
	rraw, rerr := json.Marshal(rv)
	return lerr == nil && rerr == nil && bytes.Equal(lraw, rraw)
}

func computeProjectedChapterIntentDigest(intent projectedChapterIntentV2) (string, error) {
	intent.IntentDigest = ""
	raw, err := json.Marshal(intent)
	if err != nil {
		return "", err
	}
	return domain.ComputePlanningV2JSONDigest(raw)
}

func validateProjectedChapterIntent(intent projectedChapterIntentV2) error {
	if intent.Version != "project-chapter-intent.v2" {
		return fmt.Errorf("unsupported projected chapter intent version %q", intent.Version)
	}
	if intent.GenerationID != intent.Bundle.GenerationID ||
		intent.GenerationID != intent.NextRegistry.GenerationID ||
		intent.GenerationID != intent.ExpectedCursor.GenerationID ||
		intent.GenerationID != intent.NextCursor.GenerationID ||
		intent.Chapter != intent.Bundle.Chapter {
		return fmt.Errorf("projected chapter intent identity mismatch")
	}
	if err := domain.ValidateProjectedChapterBundle(intent.Bundle); err != nil {
		return err
	}
	if err := domain.ValidateObligationRegistryV2(intent.NextRegistry); err != nil {
		return err
	}
	if err := domain.ValidateProjectionCursorV2(intent.ExpectedCursor); err != nil {
		return err
	}
	if err := domain.ValidateProjectionCursorV2(intent.NextCursor); err != nil {
		return err
	}
	if intent.ExpectedCursor.NextProjectChapter != intent.Chapter ||
		intent.NextCursor.LastProjectedChapter != intent.Chapter ||
		intent.NextCursor.NextProjectChapter != intent.Chapter+1 ||
		intent.NextCursor.LastBundleDigest != intent.Bundle.BundleDigest {
		return fmt.Errorf("projected chapter intent cursor transition mismatch")
	}
	nextRegistryRoot, err := domain.ComputeObligationRegistryV2Root(intent.NextRegistry)
	if err != nil {
		return err
	}
	if intent.NextRegistry.RegistryRoot != nextRegistryRoot {
		return fmt.Errorf("projected chapter intent next registry root mismatch")
	}
	for name, digest := range map[string]string{
		"expected_generation_digest": intent.ExpectedGenerationDigest,
		"expected_registry_root":     intent.ExpectedRegistryRoot,
		"intent_digest":              intent.IntentDigest,
	} {
		if err := validateProjectedPathComponent(name, digest); err != nil {
			return err
		}
	}
	if intent.ExpectedTailDigest != "" {
		if err := validateProjectedPathComponent("expected_tail_digest", intent.ExpectedTailDigest); err != nil {
			return err
		}
	}
	want, err := computeProjectedChapterIntentDigest(intent)
	if err != nil {
		return err
	}
	if intent.IntentDigest != want {
		return fmt.Errorf("projected chapter intent digest mismatch")
	}
	return nil
}

func computeProjectedIntentCompletionDigest(completion projectedChapterIntentCompletionV2) (string, error) {
	completion.ReceiptDigest = ""
	raw, err := json.Marshal(completion)
	if err != nil {
		return "", err
	}
	return domain.ComputePlanningV2JSONDigest(raw)
}

func (s *ProjectedStoreV2) ensureBaseDirsUnlocked() error {
	for _, rel := range []string{
		projectedBuildingDir,
		projectedGenerationsDir,
		projectedLifecycleDir,
		projectedPromotionReceiptsDir,
		projectedActualOutcomesDir,
		projectedInvalidationsDir,
		projectedArchivesDir,
		projectedChapterIntentsDir,
		projectedIntentCompletionsDir,
	} {
		if err := os.MkdirAll(s.io.path(rel), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// withProjectedWriteLock combines the in-process IO mutex with an advisory
// filesystem lock, so independent Store instances/processes serialize the
// complete read-check-write transaction.
func (s *ProjectedStoreV2) withProjectedWriteLock(fn func() error) error {
	return s.io.WithWriteLock(func() error {
		if err := os.MkdirAll(s.io.path(projectedPlanningV2Root), 0o755); err != nil {
			return err
		}
		lock, err := os.OpenFile(s.io.path(projectedWriteLockFile), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
		defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
		return fn()
	})
}

func (s *ProjectedStoreV2) withProjectedReadLock(fn func() error) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	if _, err := os.Lstat(s.io.path(projectedPlanningV2Root)); os.IsNotExist(err) {
		return fn()
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(s.io.path(projectedWriteLockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func withProjectedReadResult[T any](s *ProjectedStoreV2, fn func() (T, error)) (T, error) {
	var result T
	err := s.withProjectedReadLock(func() error {
		var err error
		result, err = fn()
		return err
	})
	return result, err
}

func (s *ProjectedStoreV2) generationExistsUnlocked(base, generationID string) (bool, error) {
	info, err := os.Lstat(s.io.path(filepath.Join(base, generationID)))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("generation path for %s is not a directory", generationID)
	}
	return true, nil
}

func (s *ProjectedStoreV2) requireBuildingUnlocked(generationID string) (string, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return "", err
	}
	if sealed, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID); err != nil {
		return "", err
	} else if sealed {
		return "", fmt.Errorf("generation %s is sealed and immutable", generationID)
	}
	rel := projectedBuildingGenerationPath(generationID)
	info, err := os.Lstat(s.io.path(rel))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("building generation %s does not exist", generationID)
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("building generation path for %s is not a directory", generationID)
	}
	return rel, nil
}

func (s *ProjectedStoreV2) writeContentAddressedJSONUnlocked(rel string, value any) (bool, error) {
	raw, err := canonicalIndentedJSON(value)
	if err != nil {
		return false, err
	}
	info, statErr := os.Lstat(s.io.path(rel))
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("content-addressed path %s is not a regular file", rel)
		}
		existing, err := s.io.ReadFileUnlocked(rel)
		if err != nil {
			return false, err
		}
		if sameJSON(existing, raw) {
			return false, nil
		}
		return false, fmt.Errorf("content-addressed artifact %s already exists with different content", rel)
	}
	if !os.IsNotExist(statErr) {
		return false, statErr
	}
	if err := s.writeFileNoReplaceUnlocked(rel, raw); err != nil {
		if os.IsExist(err) {
			existing, readErr := s.io.ReadFileUnlocked(rel)
			if readErr == nil && sameJSON(existing, raw) {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

func (s *ProjectedStoreV2) contentAddressedJSONExistsUnlocked(rel string, value any) (bool, error) {
	info, err := os.Lstat(s.io.path(rel))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("content-addressed path %s is not a regular file", rel)
	}
	existing, err := s.io.ReadFileUnlocked(rel)
	if err != nil {
		return false, err
	}
	incoming, err := canonicalIndentedJSON(value)
	if err != nil {
		return false, err
	}
	if !sameJSON(existing, incoming) {
		return false, fmt.Errorf("content-addressed artifact %s already exists with different content", rel)
	}
	return true, nil
}

func (s *ProjectedStoreV2) writeFileNoReplaceUnlocked(rel string, data []byte) error {
	final := s.io.path(rel)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), "."+filepath.Base(final)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
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
	if err := os.Link(tmpPath, final); err != nil {
		return err
	}
	return syncProjectedDirs(filepath.Dir(final))
}

func syncProjectedDirs(paths ...string) error {
	for _, path := range paths {
		dir, err := os.Open(path)
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil && err != syscall.EINVAL && err != syscall.ENOTSUP {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *ProjectedStoreV2) readJSONUnlocked(rel string, value any) error {
	info, err := os.Lstat(s.io.path(rel))
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("read %s: projected artifact is not a regular file", rel)
	}
	if err := s.io.ReadJSONUnlocked(rel, value); err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}
	return nil
}

func rejectUnexpectedBuildingFiles(root string) error {
	allowedExact := map[string]struct{}{
		projectedGenerationManifestFile: {},
		projectedSourceSnapshotFile:     {},
		projectedObligationRegistryFile: {},
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("building generation contains symlink %s", rel)
		}
		if entry.IsDir() {
			if rel == "chapters" {
				return nil
			}
			return fmt.Errorf("building generation contains unexpected directory %s", rel)
		}
		if _, ok := allowedExact[rel]; ok {
			return nil
		}
		if filepath.Dir(rel) == "chapters" && strings.HasSuffix(filepath.Base(rel), ".bundle.json") {
			return nil
		}
		return fmt.Errorf("building generation contains forbidden or unexpected file %s", rel)
	})
}

// CreateBuildingGeneration creates the complete writable generation envelope
// in a sibling temporary directory, then publishes it under .building with one
// rename. Exact retries are idempotent; a different payload for the same
// generation id is rejected.
func (s *ProjectedStoreV2) CreateBuildingGeneration(
	generation domain.PlanningGenerationV2,
	source domain.PlanningSourceSnapshotV2,
	registry domain.ObligationRegistryV2,
) error {
	if err := validateProjectedPathComponent("generation_id", generation.GenerationID); err != nil {
		return err
	}
	if string(generation.Status) != "building" {
		return fmt.Errorf("new generation %s must have building status", generation.GenerationID)
	}
	if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
		return err
	}
	if err := domain.ValidatePlanningSourceSnapshotV2(source); err != nil {
		return err
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return err
	}
	if err := domain.ValidateObligationRegistryAgainstGenerationV2(generation, registry); err != nil {
		return err
	}
	if err := domain.ValidatePlanningSourceSnapshotAgainstGenerationV2(source, generation); err != nil {
		return err
	}
	registryRoot, err := domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		return err
	}
	if registryRoot != generation.ObligationRegistryRoot {
		return fmt.Errorf("obligation registry root mismatch: got %s want %s", registryRoot, generation.ObligationRegistryRoot)
	}

	return s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		if sealed, err := s.generationExistsUnlocked(projectedGenerationsDir, generation.GenerationID); err != nil {
			return err
		} else if sealed {
			return fmt.Errorf("generation %s is already sealed and immutable", generation.GenerationID)
		}
		finalRel := projectedBuildingGenerationPath(generation.GenerationID)
		if building, err := s.generationExistsUnlocked(projectedBuildingDir, generation.GenerationID); err != nil {
			return err
		} else if building {
			return s.verifyExistingBuildingGenerationUnlocked(finalRel, generation, source, registry)
		}

		tmpAbs, err := os.MkdirTemp(s.io.path(projectedBuildingDir), "."+generation.GenerationID+".create-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmpAbs) }()
		tmpRel, err := filepath.Rel(s.io.dir, tmpAbs)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(tmpAbs, "chapters"), 0o755); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedGenerationManifestFile), generation); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedSourceSnapshotFile), source); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedObligationRegistryFile), registry); err != nil {
			return err
		}
		if err := syncProjectedDirs(filepath.Join(tmpAbs, "chapters"), tmpAbs); err != nil {
			return err
		}
		if err := os.Rename(tmpAbs, s.io.path(finalRel)); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedBuildingDir))
	})
}

func (s *ProjectedStoreV2) verifyExistingBuildingGenerationUnlocked(
	base string,
	generation domain.PlanningGenerationV2,
	source domain.PlanningSourceSnapshotV2,
	registry domain.ObligationRegistryV2,
) error {
	var existingGeneration domain.PlanningGenerationV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), &existingGeneration); err != nil {
		return err
	}
	var existingSource domain.PlanningSourceSnapshotV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedSourceSnapshotFile), &existingSource); err != nil {
		return err
	}
	var existingRegistry domain.ObligationRegistryV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedObligationRegistryFile), &existingRegistry); err != nil {
		return err
	}
	wantGeneration, _ := canonicalIndentedJSON(generation)
	gotGeneration, _ := canonicalIndentedJSON(existingGeneration)
	wantSource, _ := canonicalIndentedJSON(source)
	gotSource, _ := canonicalIndentedJSON(existingSource)
	wantRegistry, _ := canonicalIndentedJSON(registry)
	gotRegistry, _ := canonicalIndentedJSON(existingRegistry)
	if !bytes.Equal(wantGeneration, gotGeneration) ||
		!bytes.Equal(wantSource, gotSource) ||
		!bytes.Equal(wantRegistry, gotRegistry) {
		return fmt.Errorf("building generation %s already exists with different content", generation.GenerationID)
	}
	return nil
}

func (s *ProjectedStoreV2) loadGenerationAtUnlocked(base string) (*domain.PlanningGenerationV2, error) {
	var generation domain.PlanningGenerationV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), &generation); err != nil {
		if os.IsNotExist(rootCause(err)) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidatePlanningGenerationV2(generation); err != nil {
		return nil, err
	}
	return &generation, nil
}

func (s *ProjectedStoreV2) loadSourceAtUnlocked(base string) (*domain.PlanningSourceSnapshotV2, error) {
	var source domain.PlanningSourceSnapshotV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedSourceSnapshotFile), &source); err != nil {
		return nil, err
	}
	if err := domain.ValidatePlanningSourceSnapshotV2(source); err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *ProjectedStoreV2) loadRegistryAtUnlocked(base string) (*domain.ObligationRegistryV2, error) {
	var registry domain.ObligationRegistryV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedObligationRegistryFile), &registry); err != nil {
		return nil, err
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return nil, err
	}
	return &registry, nil
}

func (s *ProjectedStoreV2) loadBundlesAtUnlocked(base string) ([]domain.ProjectedChapterBundle, error) {
	dirRel := filepath.Join(base, "chapters")
	entries, err := os.ReadDir(s.io.path(dirRel))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bundles := make([]domain.ProjectedChapterBundle, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !strings.HasSuffix(entry.Name(), ".bundle.json") {
			return nil, fmt.Errorf("unexpected entry in projected chapters directory: %s", entry.Name())
		}
		var bundle domain.ProjectedChapterBundle
		if err := s.readJSONUnlocked(filepath.Join(dirRel, entry.Name()), &bundle); err != nil {
			return nil, err
		}
		if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
			return nil, err
		}
		wantName := fmt.Sprintf("%04d.bundle.json", bundle.Chapter)
		if entry.Name() != wantName {
			return nil, fmt.Errorf("bundle path %s contains chapter %d", entry.Name(), bundle.Chapter)
		}
		bundles = append(bundles, bundle)
	}
	sortProjectedBundles(bundles)
	return bundles, nil
}

func sortProjectedBundles(bundles []domain.ProjectedChapterBundle) {
	for i := 1; i < len(bundles); i++ {
		for j := i; j > 0 && bundles[j].Chapter < bundles[j-1].Chapter; j-- {
			bundles[j], bundles[j-1] = bundles[j-1], bundles[j]
		}
	}
}

func rootCause(err error) error {
	for {
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok || unwrapped.Unwrap() == nil {
			return err
		}
		err = unwrapped.Unwrap()
	}
}

func (s *ProjectedStoreV2) LoadBuildingGeneration(generationID string) (*domain.PlanningGenerationV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.PlanningGenerationV2, error) {
		return s.loadGenerationAtUnlocked(projectedBuildingGenerationPath(generationID))
	})
}

func (s *ProjectedStoreV2) LoadSealedGeneration(generationID string) (*domain.PlanningGenerationV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.PlanningGenerationV2, error) {
		exists, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, nil
		}
		if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
			return nil, err
		}
		return s.loadGenerationAtUnlocked(projectedSealedGenerationPath(generationID))
	})
}

func (s *ProjectedStoreV2) LoadPlanningSourceSnapshot(
	generationID string,
) (*domain.PlanningSourceSnapshotV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.PlanningSourceSnapshotV2, error) {
		base := projectedSealedGenerationPath(generationID)
		if exists, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID); err != nil {
			return nil, err
		} else if exists {
			if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
				return nil, err
			}
		} else {
			base = projectedBuildingGenerationPath(generationID)
		}
		return s.loadSourceAtUnlocked(base)
	})
}

func (s *ProjectedStoreV2) LoadObligationRegistry(
	generationID string,
) (*domain.ObligationRegistryV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.ObligationRegistryV2, error) {
		base := projectedSealedGenerationPath(generationID)
		if exists, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID); err != nil {
			return nil, err
		} else if exists {
			if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
				return nil, err
			}
		} else {
			base = projectedBuildingGenerationPath(generationID)
		}
		return s.loadRegistryAtUnlocked(base)
	})
}

// PrepareCarriedArcGeneration loads one immutable sealed predecessor and
// returns the next building arc generation rebound to a registry containing
// only unresolved future obligations. It performs no write; callers pass the
// returned pair to CreateBuildingGeneration, whose atomic create remains the
// single publication boundary.
func (s *ProjectedStoreV2) PrepareCarriedArcGeneration(
	predecessorGenerationID string,
	nextGeneration domain.PlanningGenerationV2,
) (domain.PlanningGenerationV2, domain.ObligationRegistryV2, error) {
	if err := validateProjectedPathComponent("predecessor_generation_id", predecessorGenerationID); err != nil {
		return nextGeneration, domain.ObligationRegistryV2{}, err
	}
	previous, err := s.LoadSealedGeneration(predecessorGenerationID)
	if err != nil {
		return nextGeneration, domain.ObligationRegistryV2{}, err
	}
	if previous == nil {
		return nextGeneration, domain.ObligationRegistryV2{}, fmt.Errorf(
			"sealed predecessor generation %s does not exist",
			predecessorGenerationID,
		)
	}
	registry, err := s.LoadObligationRegistry(predecessorGenerationID)
	if err != nil {
		return nextGeneration, domain.ObligationRegistryV2{}, err
	}
	if registry == nil {
		return nextGeneration, domain.ObligationRegistryV2{}, fmt.Errorf(
			"sealed predecessor generation %s has no obligation registry",
			predecessorGenerationID,
		)
	}
	return domain.CarryForwardArcObligationsV2(*previous, *registry, nextGeneration)
}

// LoadProjectedChapterBundles reads either the sealed immutable generation or,
// if it has not yet been published, its building counterpart.
func (s *ProjectedStoreV2) LoadProjectedChapterBundles(generationID string) ([]domain.ProjectedChapterBundle, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() ([]domain.ProjectedChapterBundle, error) {
		base := projectedSealedGenerationPath(generationID)
		if exists, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID); err != nil {
			return nil, err
		} else if exists {
			if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
				return nil, err
			}
		} else {
			base = projectedBuildingGenerationPath(generationID)
			generation, err := s.loadGenerationAtUnlocked(base)
			if err != nil {
				return nil, err
			}
			if generation == nil {
				return nil, nil
			}
			registry, err := s.loadRegistryAtUnlocked(base)
			if err != nil {
				return nil, err
			}
			bundles, err := s.loadBundlesAtUnlocked(base)
			if err != nil {
				return nil, err
			}
			if err := domain.ValidateProjectedChapterBundleChain(*generation, bundles, *registry); err != nil {
				return nil, err
			}
			return bundles, nil
		}
		return s.loadBundlesAtUnlocked(base)
	})
}

func (s *ProjectedStoreV2) LoadProjectedChainManifest(
	generationID string,
) (*domain.ProjectedChainManifestV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.ProjectedChainManifestV2, error) {
		if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
			if os.IsNotExist(rootCause(err)) {
				return nil, nil
			}
			return nil, err
		}
		var manifest domain.ProjectedChainManifestV2
		if err := s.readJSONUnlocked(
			filepath.Join(projectedSealedGenerationPath(generationID), projectedChainManifestFile),
			&manifest,
		); err != nil {
			return nil, err
		}
		return &manifest, nil
	})
}

func (s *ProjectedStoreV2) LoadSealReceipt(generationID string) (*domain.SealReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.SealReceiptV2, error) {
		receipt, err := s.validateSealedGenerationUnlocked(generationID)
		if err != nil {
			if os.IsNotExist(rootCause(err)) {
				return nil, nil
			}
			return nil, err
		}
		return receipt, nil
	})
}

// CompareAndSwapProjectedChapterBundle appends the next chapter to a building
// chain using the generation and tail roots observed by the caller. An exact
// replay repairs a possibly stale generation manifest. Replacing the current
// tail is allowed while building, but rewriting an interior chapter is
// rejected because doing so would silently invalidate successors.
func (s *ProjectedStoreV2) CompareAndSwapProjectedChapterBundle(
	expectedGenerationDigest string,
	expectedTailDigest string,
	bundle domain.ProjectedChapterBundle,
) error {
	if strings.TrimSpace(expectedGenerationDigest) == "" {
		return fmt.Errorf("expected generation digest is required")
	}
	if err := validateProjectedPathComponent("generation_id", bundle.GenerationID); err != nil {
		return err
	}
	if err := validateProjectedChapter(bundle.Chapter); err != nil {
		return err
	}
	digest, err := domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		return err
	}
	if bundle.BundleDigest == "" {
		bundle.BundleDigest = digest
	}
	if bundle.BundleDigest != digest {
		return fmt.Errorf("bundle digest mismatch: got %s want %s", bundle.BundleDigest, digest)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return err
	}

	return s.withProjectedWriteLock(func() error {
		_, err := s.saveProjectedChapterBundleUnlocked(
			expectedGenerationDigest,
			expectedTailDigest,
			bundle,
		)
		return err
	})
}

func (s *ProjectedStoreV2) saveProjectedChapterBundleUnlocked(
	expectedGenerationDigest string,
	expectedTailDigest string,
	bundle domain.ProjectedChapterBundle,
) (*domain.PlanningGenerationV2, error) {
	base, err := s.requireBuildingUnlocked(bundle.GenerationID)
	if err != nil {
		return nil, err
	}
	generation, err := s.loadGenerationAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if generation == nil {
		return nil, fmt.Errorf("building generation %s has no generation manifest", bundle.GenerationID)
	}
	if generation.GenerationDigest != expectedGenerationDigest {
		existing, readErr := s.loadBundleAtUnlocked(base, bundle.Chapter)
		if readErr != nil || existing.BundleDigest != bundle.BundleDigest || !jsonValuesEqual(*existing, bundle) {
			return nil, fmt.Errorf("bundle compare-and-swap failed: generation digest got %s want %s",
				generation.GenerationDigest, expectedGenerationDigest)
		}
	}
	if generation.ChainTailRoot != expectedTailDigest {
		existing, readErr := s.loadBundleAtUnlocked(base, bundle.Chapter)
		if readErr != nil || existing.BundleDigest != bundle.BundleDigest || !jsonValuesEqual(*existing, bundle) {
			return nil, fmt.Errorf("bundle compare-and-swap failed: chain tail got %s want %s",
				generation.ChainTailRoot, expectedTailDigest)
		}
	}
	if bundle.GenerationID != generation.GenerationID {
		return nil, fmt.Errorf("bundle generation %s does not match %s", bundle.GenerationID, generation.GenerationID)
	}
	if bundle.Chapter < generation.FirstProjectedChapter || bundle.Chapter > generation.LastProjectedChapter {
		return nil, fmt.Errorf("bundle chapter %d is outside generation range %d..%d",
			bundle.Chapter, generation.FirstProjectedChapter, generation.LastProjectedChapter)
	}
	registry, err := s.loadRegistryAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	current, err := s.loadBundlesAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	position := -1
	for i := range current {
		if current[i].Chapter == bundle.Chapter {
			position = i
			break
		}
	}
	exactReplay := false
	if position >= 0 && current[position].BundleDigest == bundle.BundleDigest {
		currentRaw, _ := canonicalIndentedJSON(current[position])
		incomingRaw, _ := canonicalIndentedJSON(bundle)
		if bytes.Equal(currentRaw, incomingRaw) {
			exactReplay = true
		} else {
			return nil, fmt.Errorf("bundle %d reuses digest %s with different content", bundle.Chapter, bundle.BundleDigest)
		}
	}
	if !exactReplay && position >= 0 {
		if position != len(current)-1 {
			return nil, fmt.Errorf("cannot rewrite interior bundle chapter %d while successor chapter %d exists",
				bundle.Chapter, current[position+1].Chapter)
		}
		current[position] = bundle
	} else if !exactReplay {
		wantChapter := generation.FirstProjectedChapter + len(current)
		if bundle.Chapter != wantChapter {
			return nil, fmt.Errorf("bundle chain must append chapter %d, got %d", wantChapter, bundle.Chapter)
		}
		current = append(current, bundle)
	}
	updated := *generation
	updated.ProjectedChapterCount = len(current)
	updated.ChainHeadRoot = current[0].BundleDigest
	updated.ChainTailRoot = current[len(current)-1].BundleDigest
	updated.GenerationDigest = ""
	updated.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(updated)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateProjectedChapterBundleChain(updated, current, *registry); err != nil {
		return nil, err
	}
	if err := domain.ValidatePlanningGenerationV2(updated); err != nil {
		return nil, err
	}
	if !exactReplay {
		if err := s.io.WriteJSONUnlocked(projectedBundlePath(base, bundle.Chapter), bundle); err != nil {
			return nil, err
		}
		if err := s.failProjectedStage("bundle_written"); err != nil {
			return nil, err
		}
	}
	if err := s.io.WriteJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), updated); err != nil {
		return nil, err
	}
	if err := s.failProjectedStage("bundle_generation_written"); err != nil {
		return nil, err
	}
	if err := syncProjectedDirs(s.io.path(filepath.Join(base, "chapters")), s.io.path(base)); err != nil {
		return nil, err
	}
	return &updated, nil
}

// SaveProjectedChapterBundle is a single-writer convenience wrapper. Parallel
// project-all workers should use CompareAndSwapProjectedChapterBundle.
func (s *ProjectedStoreV2) SaveProjectedChapterBundle(bundle domain.ProjectedChapterBundle) error {
	generation, err := s.LoadBuildingGeneration(bundle.GenerationID)
	if err != nil {
		return err
	}
	if generation == nil {
		sealed, sealedErr := s.LoadSealedGeneration(bundle.GenerationID)
		if sealedErr != nil {
			return sealedErr
		}
		if sealed != nil {
			return fmt.Errorf("generation %s is sealed and immutable", bundle.GenerationID)
		}
		return fmt.Errorf("building generation %s does not exist", bundle.GenerationID)
	}
	return s.CompareAndSwapProjectedChapterBundle(
		generation.GenerationDigest,
		generation.ChainTailRoot,
		bundle,
	)
}

// ProjectChapterAndAdvance CAS-publishes the chapter's evolved obligation
// registry, bundle, generation manifest, and projection cursor under one
// cross-process lock. Exact replay repairs every intermediate crash window.
func (s *ProjectedStoreV2) ProjectChapterAndAdvance(
	expectedGenerationDigest string,
	expectedTailDigest string,
	expectedRegistryRoot string,
	expectedCursor domain.ProjectionCursorV2,
	bundle domain.ProjectedChapterBundle,
	nextRegistry domain.ObligationRegistryV2,
) (*domain.ProjectionCursorV2, error) {
	if err := domain.ValidateProjectionCursorV2(expectedCursor); err != nil {
		return nil, fmt.Errorf("expected projection cursor: %w", err)
	}
	if expectedCursor.GenerationID != bundle.GenerationID ||
		expectedCursor.NextProjectChapter != bundle.Chapter ||
		expectedCursor.LastBundleDigest != expectedTailDigest {
		return nil, fmt.Errorf("bundle does not match expected projection cursor")
	}
	if strings.TrimSpace(expectedCursor.BlockedReason) != "" {
		return nil, fmt.Errorf("projection cursor is blocked: %s", expectedCursor.BlockedReason)
	}
	if strings.TrimSpace(expectedRegistryRoot) == "" {
		return nil, fmt.Errorf("expected obligation registry root is required")
	}
	if err := domain.ValidateObligationRegistryV2(nextRegistry); err != nil {
		return nil, err
	}
	if nextRegistry.GenerationID != bundle.GenerationID {
		return nil, fmt.Errorf("obligation registry generation differs from bundle")
	}
	nextRegistryRoot, err := domain.ComputeObligationRegistryV2Root(nextRegistry)
	if err != nil {
		return nil, err
	}
	if nextRegistry.RegistryRoot != nextRegistryRoot {
		return nil, fmt.Errorf("next obligation registry root mismatch")
	}
	digest, err := domain.ComputeProjectedChapterBundleDigest(bundle)
	if err != nil {
		return nil, err
	}
	if bundle.BundleDigest == "" {
		bundle.BundleDigest = digest
	}
	if bundle.BundleDigest != digest {
		return nil, fmt.Errorf("bundle digest mismatch: got %s want %s", bundle.BundleDigest, digest)
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return nil, err
	}
	nextCursor := expectedCursor
	nextCursor.LastProjectedChapter = bundle.Chapter
	nextCursor.NextProjectChapter = bundle.Chapter + 1
	nextCursor.LastBundleDigest = bundle.BundleDigest
	nextCursor.BlockedReason = ""
	nextCursor.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	nextCursor.CursorDigest = ""
	nextCursor.CursorDigest, err = domain.ComputeProjectionCursorV2Digest(nextCursor)
	if err != nil {
		return nil, err
	}
	intent := projectedChapterIntentV2{
		Version:                  "project-chapter-intent.v2",
		GenerationID:             bundle.GenerationID,
		Chapter:                  bundle.Chapter,
		ExpectedGenerationDigest: expectedGenerationDigest,
		ExpectedTailDigest:       expectedTailDigest,
		ExpectedRegistryRoot:     expectedRegistryRoot,
		ExpectedCursor:           expectedCursor,
		Bundle:                   bundle,
		NextRegistry:             nextRegistry,
		NextCursor:               nextCursor,
	}
	intent.IntentDigest, err = computeProjectedChapterIntentDigest(intent)
	if err != nil {
		return nil, err
	}
	if err := validateProjectedChapterIntent(intent); err != nil {
		return nil, err
	}

	var result domain.ProjectionCursorV2
	err = s.withProjectedWriteLock(func() error {
		completed, err := s.findCompletedProjectedChapterIntentUnlocked(intent)
		if err != nil {
			return err
		}
		if completed != nil {
			current, err := s.loadProjectionCursorUnlocked()
			if err != nil {
				return err
			}
			if current == nil || current.LastProjectedChapter < completed.Chapter {
				return fmt.Errorf("completed chapter intent exists but projection cursor is behind")
			}
			result = *current
			return nil
		}
		currentCursor, err := s.loadProjectionCursorUnlocked()
		if err != nil {
			return err
		}
		if currentCursor != nil && currentCursor.LastProjectedChapter >= bundle.Chapter {
			return fmt.Errorf("chapter %d is already projected by a different completed intent", bundle.Chapter)
		}
		pending, err := s.loadPendingProjectedChapterIntentsUnlocked(bundle.GenerationID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			if len(pending) != 1 || !sameProjectedChapterIntentInputs(pending[0], intent) {
				return fmt.Errorf("generation %s has a different pending chapter intent; recover it before replanning",
					bundle.GenerationID)
			}
			intent = pending[0]
		} else {
			rel := projectedChapterIntentPath(intent.GenerationID, intent.Chapter, intent.IntentDigest)
			if _, err := s.writeContentAddressedJSONUnlocked(rel, intent); err != nil {
				return err
			}
			if err := s.failProjectedStage("intent_written"); err != nil {
				return err
			}
		}
		applied, err := s.applyProjectedChapterIntentUnlocked(intent)
		if err != nil {
			return err
		}
		result = *applied
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func sameProjectedChapterIntentInputs(left, right projectedChapterIntentV2) bool {
	return left.GenerationID == right.GenerationID &&
		left.Chapter == right.Chapter &&
		left.ExpectedGenerationDigest == right.ExpectedGenerationDigest &&
		left.ExpectedTailDigest == right.ExpectedTailDigest &&
		left.ExpectedRegistryRoot == right.ExpectedRegistryRoot &&
		jsonValuesEqual(left.ExpectedCursor, right.ExpectedCursor) &&
		jsonValuesEqual(left.Bundle, right.Bundle) &&
		jsonValuesEqual(left.NextRegistry, right.NextRegistry)
}

func (s *ProjectedStoreV2) findCompletedProjectedChapterIntentUnlocked(
	candidate projectedChapterIntentV2,
) (*projectedChapterIntentV2, error) {
	dir := s.io.path(filepath.Join(
		projectedChapterIntentsDir,
		candidate.GenerationID,
		fmt.Sprintf("%04d", candidate.Chapter),
	))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("invalid projected chapter intent entry %s", entry.Name())
		}
		var intent projectedChapterIntentV2
		if err := s.readJSONUnlocked(
			filepath.Join(
				projectedChapterIntentsDir,
				candidate.GenerationID,
				fmt.Sprintf("%04d", candidate.Chapter),
				entry.Name(),
			),
			&intent,
		); err != nil {
			return nil, err
		}
		if err := validateProjectedChapterIntent(intent); err != nil {
			return nil, err
		}
		if !sameProjectedChapterIntentInputs(intent, candidate) {
			continue
		}
		completed, err := s.projectedChapterIntentCompletedUnlocked(intent)
		if err != nil {
			return nil, err
		}
		if completed {
			return &intent, nil
		}
	}
	return nil, nil
}

func (s *ProjectedStoreV2) loadPendingProjectedChapterIntentsUnlocked(
	generationID string,
) ([]projectedChapterIntentV2, error) {
	root := s.io.path(filepath.Join(projectedChapterIntentsDir, generationID))
	var intents []projectedChapterIntentV2
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return fmt.Errorf("invalid projected chapter intent entry %s", path)
		}
		rel, err := filepath.Rel(s.io.dir, path)
		if err != nil {
			return err
		}
		var intent projectedChapterIntentV2
		if err := s.readJSONUnlocked(rel, &intent); err != nil {
			return err
		}
		if err := validateProjectedChapterIntent(intent); err != nil {
			return err
		}
		if intent.GenerationID != generationID || entry.Name() != intent.IntentDigest+".json" {
			return fmt.Errorf("projected chapter intent path identity mismatch")
		}
		completed, err := s.projectedChapterIntentCompletedUnlocked(intent)
		if err != nil {
			return err
		}
		if !completed {
			intents = append(intents, intent)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for i := 1; i < len(intents); i++ {
		for j := i; j > 0 && intents[j].Chapter < intents[j-1].Chapter; j-- {
			intents[j], intents[j-1] = intents[j-1], intents[j]
		}
	}
	return intents, nil
}

func (s *ProjectedStoreV2) projectedChapterIntentCompletedUnlocked(
	intent projectedChapterIntentV2,
) (bool, error) {
	rel := projectedChapterIntentCompletionPath(intent.GenerationID, intent.Chapter, intent.IntentDigest)
	var completion projectedChapterIntentCompletionV2
	if err := s.readJSONUnlocked(rel, &completion); err != nil {
		if os.IsNotExist(rootCause(err)) {
			return false, nil
		}
		return false, err
	}
	if completion.Version != "project-chapter-intent-completion.v2" ||
		completion.GenerationID != intent.GenerationID ||
		completion.Chapter != intent.Chapter ||
		completion.IntentDigest != intent.IntentDigest ||
		completion.ProjectionCursorRoot != intent.NextCursor.CursorDigest {
		return false, fmt.Errorf("projected chapter intent completion identity mismatch")
	}
	want, err := computeProjectedIntentCompletionDigest(completion)
	if err != nil {
		return false, err
	}
	if completion.ReceiptDigest != want {
		return false, fmt.Errorf("projected chapter intent completion digest mismatch")
	}
	return true, nil
}

func (s *ProjectedStoreV2) completeProjectedChapterIntentUnlocked(
	intent projectedChapterIntentV2,
) error {
	completion := projectedChapterIntentCompletionV2{
		Version:              "project-chapter-intent-completion.v2",
		GenerationID:         intent.GenerationID,
		Chapter:              intent.Chapter,
		IntentDigest:         intent.IntentDigest,
		ProjectionCursorRoot: intent.NextCursor.CursorDigest,
		CompletedAt:          intent.NextCursor.UpdatedAt,
	}
	var err error
	completion.ReceiptDigest, err = computeProjectedIntentCompletionDigest(completion)
	if err != nil {
		return err
	}
	_, err = s.writeContentAddressedJSONUnlocked(
		projectedChapterIntentCompletionPath(intent.GenerationID, intent.Chapter, intent.IntentDigest),
		completion,
	)
	return err
}

func (s *ProjectedStoreV2) applyProjectedChapterIntentUnlocked(
	intent projectedChapterIntentV2,
) (*domain.ProjectionCursorV2, error) {
	if err := validateProjectedChapterIntent(intent); err != nil {
		return nil, err
	}
	current, err := s.loadProjectionCursorUnlocked()
	if err != nil {
		return nil, err
	}
	if current == nil ||
		(!jsonValuesEqual(*current, intent.ExpectedCursor) && !jsonValuesEqual(*current, intent.NextCursor)) {
		return nil, fmt.Errorf("projected chapter intent cursor compare-and-swap failed")
	}
	base := projectedBuildingGenerationPath(intent.GenerationID)
	before, err := s.loadGenerationAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, fmt.Errorf("building generation %s does not exist", intent.GenerationID)
	}
	nextRegistryRoot := intent.NextRegistry.RegistryRoot
	if before.GenerationDigest != intent.ExpectedGenerationDigest &&
		before.ObligationRegistryRoot != nextRegistryRoot {
		return nil, fmt.Errorf("projected chapter intent generation compare-and-swap failed")
	}
	if before.ObligationRegistryRoot != intent.ExpectedRegistryRoot &&
		before.ObligationRegistryRoot != nextRegistryRoot {
		return nil, fmt.Errorf("projected chapter intent registry compare-and-swap failed")
	}
	withRegistry, err := s.replaceBuildingObligationRegistryUnlocked(
		intent.GenerationID,
		intent.ExpectedRegistryRoot,
		nextRegistryRoot,
		intent.NextRegistry,
	)
	if err != nil {
		return nil, err
	}
	updatedGeneration, err := s.saveProjectedChapterBundleUnlocked(
		withRegistry.GenerationDigest,
		intent.ExpectedTailDigest,
		intent.Bundle,
	)
	if err != nil {
		return nil, err
	}
	if intent.NextCursor.LastBundleDigest != updatedGeneration.ChainTailRoot {
		return nil, fmt.Errorf("projected chapter intent cursor tail differs from generation")
	}
	if err := s.validateProjectionCursorAgainstStoreUnlocked(intent.NextCursor); err != nil {
		return nil, err
	}
	if !jsonValuesEqual(*current, intent.NextCursor) {
		if err := s.io.WriteJSONUnlocked(projectedProjectionCursorPath, intent.NextCursor); err != nil {
			return nil, err
		}
		if err := s.failProjectedStage("cursor_written"); err != nil {
			return nil, err
		}
	}
	if err := syncProjectedDirs(s.io.path(projectedPlanningV2Root)); err != nil {
		return nil, err
	}
	if err := s.completeProjectedChapterIntentUnlocked(intent); err != nil {
		return nil, err
	}
	next := intent.NextCursor
	return &next, nil
}

// ProjectBundleAndAdvance keeps the registry unchanged. New project-all code
// should call ProjectChapterAndAdvance so LLM-created/superseded obligations
// share the same crash-recovery transaction.
func (s *ProjectedStoreV2) ProjectBundleAndAdvance(
	expectedGenerationDigest string,
	expectedTailDigest string,
	expectedCursor domain.ProjectionCursorV2,
	bundle domain.ProjectedChapterBundle,
) (*domain.ProjectionCursorV2, error) {
	registry, err := s.LoadObligationRegistry(bundle.GenerationID)
	if err != nil {
		return nil, err
	}
	if registry == nil {
		return nil, fmt.Errorf("generation %s has no obligation registry", bundle.GenerationID)
	}
	return s.ProjectChapterAndAdvance(
		expectedGenerationDigest,
		expectedTailDigest,
		registry.RegistryRoot,
		expectedCursor,
		bundle,
		*registry,
	)
}

func (s *ProjectedStoreV2) InitializeProjectionCursor(generationID string) (*domain.ProjectionCursorV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	generation, err := s.LoadBuildingGeneration(generationID)
	if err != nil {
		return nil, err
	}
	if generation == nil {
		return nil, fmt.Errorf("building generation %s does not exist", generationID)
	}
	cursor := domain.ProjectionCursorV2{
		GenerationID:         generationID,
		NextProjectChapter:   generation.FirstProjectedChapter,
		LastProjectedChapter: generation.FirstProjectedChapter - 1,
		UpdatedAt:            s.now().UTC().Format(time.RFC3339Nano),
	}
	cursor.CursorDigest, err = domain.ComputeProjectionCursorV2Digest(cursor)
	if err != nil {
		return nil, err
	}
	if err := s.CompareAndSwapProjectionCursor(nil, cursor); err != nil {
		return nil, err
	}
	return &cursor, nil
}

// ResetProjectionCursorForRestart abandons only the global projection pointer
// when an explicit, durable project-all restart has selected a different
// successor generation. The old building/sealed generation and its bundle
// chain remain intact and auditable; removing this derived pointer lets
// RecoverBuildingProjection reconstruct the successor cursor from its own
// durable chain. Repeating the call for the same successor is a no-op so crash
// recovery cannot erase already resumed progress.
func (s *ProjectedStoreV2) ResetProjectionCursorForRestart(
	successorGenerationID string,
) error {
	if err := validateProjectedPathComponent("generation_id", successorGenerationID); err != nil {
		return err
	}
	return s.withProjectedWriteLock(func() error {
		current, err := s.loadProjectionCursorUnlocked()
		if err != nil {
			return err
		}
		if current == nil || current.GenerationID == successorGenerationID {
			return nil
		}
		if err := s.io.RemoveFileUnlocked(projectedProjectionCursorPath); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
}

// RecoverBuildingProjection repairs the two intentional multi-file crash
// windows by deriving generation.json and projection_cursor.json exclusively
// from the validated durable bundle chain.
func (s *ProjectedStoreV2) RecoverBuildingProjection(
	generationID string,
) (*domain.PlanningGenerationV2, *domain.ProjectionCursorV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, nil, err
	}
	var recoveredGeneration domain.PlanningGenerationV2
	var recoveredCursor domain.ProjectionCursorV2
	err := s.withProjectedWriteLock(func() error {
		base, err := s.requireBuildingUnlocked(generationID)
		if err != nil {
			return err
		}
		pending, err := s.loadPendingProjectedChapterIntentsUnlocked(generationID)
		if err != nil {
			return err
		}
		for _, intent := range pending {
			if _, err := s.applyProjectedChapterIntentUnlocked(intent); err != nil {
				return fmt.Errorf("recover projected chapter intent %s: %w", intent.IntentDigest, err)
			}
		}
		generation, err := s.loadGenerationAtUnlocked(base)
		if err != nil {
			return err
		}
		if generation == nil {
			return fmt.Errorf("building generation %s has no manifest", generationID)
		}
		registry, err := s.loadRegistryAtUnlocked(base)
		if err != nil {
			return err
		}
		bundles, err := s.loadBundlesAtUnlocked(base)
		if err != nil {
			return err
		}
		updated := *generation
		updated.ObligationRegistryRoot = registry.RegistryRoot
		updated.ProjectedChapterCount = len(bundles)
		updated.ChainHeadRoot = ""
		updated.ChainTailRoot = ""
		if len(bundles) > 0 {
			updated.ChainHeadRoot = bundles[0].BundleDigest
			updated.ChainTailRoot = bundles[len(bundles)-1].BundleDigest
		}
		updated.GenerationDigest = ""
		updated.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(updated)
		if err != nil {
			return err
		}
		if err := domain.ValidateProjectedChapterBundleChain(updated, bundles, *registry); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), updated); err != nil {
			return err
		}
		currentCursor, err := s.loadProjectionCursorUnlocked()
		if err != nil {
			return err
		}
		lastChapter := updated.FirstProjectedChapter - 1
		lastDigest := ""
		if len(bundles) > 0 {
			lastChapter = bundles[len(bundles)-1].Chapter
			lastDigest = bundles[len(bundles)-1].BundleDigest
		}
		if currentCursor != nil {
			if currentCursor.GenerationID != generationID ||
				currentCursor.LastProjectedChapter > lastChapter {
				return fmt.Errorf("projection cursor cannot be recovered from durable chain")
			}
			recoveredCursor = *currentCursor
		} else {
			recoveredCursor.GenerationID = generationID
		}
		cursorMatches := currentCursor != nil &&
			currentCursor.LastProjectedChapter == lastChapter &&
			currentCursor.NextProjectChapter == lastChapter+1 &&
			currentCursor.LastBundleDigest == lastDigest
		if !cursorMatches {
			recoveredCursor.LastProjectedChapter = lastChapter
			recoveredCursor.NextProjectChapter = lastChapter + 1
			recoveredCursor.LastBundleDigest = lastDigest
			recoveredCursor.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
			recoveredCursor.CursorDigest = ""
			recoveredCursor.CursorDigest, err = domain.ComputeProjectionCursorV2Digest(recoveredCursor)
			if err != nil {
				return err
			}
			if err := domain.ValidateProjectionCursorV2(recoveredCursor); err != nil {
				return err
			}
			if err := s.io.WriteJSONUnlocked(projectedProjectionCursorPath, recoveredCursor); err != nil {
				return err
			}
		}
		if err := syncProjectedDirs(s.io.path(base), s.io.path(projectedPlanningV2Root)); err != nil {
			return err
		}
		recoveredGeneration = updated
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &recoveredGeneration, &recoveredCursor, nil
}

// ReplaceBuildingObligationRegistry CAS-replaces the registry while the
// generation is building. Existing bundles are revalidated against the
// replacement before either file is changed.
func (s *ProjectedStoreV2) ReplaceBuildingObligationRegistry(
	generationID string,
	expectedRoot string,
	registry domain.ObligationRegistryV2,
) error {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return err
	}
	if strings.TrimSpace(expectedRoot) == "" {
		return fmt.Errorf("expected obligation registry root is required")
	}
	if err := domain.ValidateObligationRegistryV2(registry); err != nil {
		return err
	}
	root, err := domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		return err
	}
	return s.withProjectedWriteLock(func() error {
		_, err := s.replaceBuildingObligationRegistryUnlocked(generationID, expectedRoot, root, registry)
		return err
	})
}

func (s *ProjectedStoreV2) replaceBuildingObligationRegistryUnlocked(
	generationID string,
	expectedRoot string,
	nextRoot string,
	registry domain.ObligationRegistryV2,
) (*domain.PlanningGenerationV2, error) {
	base, err := s.requireBuildingUnlocked(generationID)
	if err != nil {
		return nil, err
	}
	generation, err := s.loadGenerationAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if generation == nil {
		return nil, fmt.Errorf("building generation %s has no generation manifest", generationID)
	}
	currentRegistry, err := s.loadRegistryAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if generation.ObligationRegistryRoot == nextRoot && jsonValuesEqual(*currentRegistry, registry) {
		return generation, nil
	}
	if generation.ObligationRegistryRoot != expectedRoot {
		return nil, fmt.Errorf("obligation registry compare-and-swap failed: got root %s want %s",
			generation.ObligationRegistryRoot, expectedRoot)
	}
	bundles, err := s.loadBundlesAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	updated := *generation
	updated.ObligationRegistryRoot = nextRoot
	updated.GenerationDigest = ""
	updated.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(updated)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidatePlanningGenerationV2(updated); err != nil {
		return nil, err
	}
	if err := domain.ValidateProjectedChapterBundleChain(updated, bundles, registry); err != nil {
		return nil, err
	}
	if !jsonValuesEqual(*currentRegistry, registry) {
		if err := s.io.WriteJSONUnlocked(filepath.Join(base, projectedObligationRegistryFile), registry); err != nil {
			return nil, err
		}
		if err := s.failProjectedStage("registry_written"); err != nil {
			return nil, err
		}
	}
	if err := s.io.WriteJSONUnlocked(filepath.Join(base, projectedGenerationManifestFile), updated); err != nil {
		return nil, err
	}
	if err := s.failProjectedStage("registry_generation_written"); err != nil {
		return nil, err
	}
	if err := syncProjectedDirs(s.io.path(base)); err != nil {
		return nil, err
	}
	return &updated, nil
}

// SaveObligationRegistry is a convenience wrapper for single-writer callers.
// Concurrent project-all code should use ReplaceBuildingObligationRegistry
// with the root it read.
func (s *ProjectedStoreV2) SaveObligationRegistry(
	generationID string,
	registry domain.ObligationRegistryV2,
) error {
	generation, err := s.LoadBuildingGeneration(generationID)
	if err != nil {
		return err
	}
	if generation == nil {
		return fmt.Errorf("building generation %s does not exist", generationID)
	}
	return s.ReplaceBuildingObligationRegistry(generationID, generation.ObligationRegistryRoot, registry)
}

// SealGeneration validates a complete building chain and atomically publishes
// a clean immutable generation directory. The building directory is never
// edited into a sealed state in place.
func (s *ProjectedStoreV2) SealGeneration(generationID string) (*domain.SealReceiptV2, error) {
	generation, err := s.LoadBuildingGeneration(generationID)
	if err != nil {
		return nil, err
	}
	if generation == nil {
		receipt, err := s.LoadSealReceipt(generationID)
		if err != nil {
			return nil, err
		}
		if receipt == nil {
			return nil, fmt.Errorf("generation %s does not exist", generationID)
		}
		return receipt, nil
	}
	source, err := s.LoadPlanningSourceSnapshot(generationID)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("building generation %s has no source snapshot", generationID)
	}
	return s.SealGenerationExpected(generationID, generation.GenerationDigest, source.SnapshotDigest)
}

// SealGenerationExpected adds compare-and-swap attestation for callers that
// verified live canon/planning roots under the global execution lock.
func (s *ProjectedStoreV2) SealGenerationExpected(
	generationID string,
	expectedGenerationDigest string,
	expectedSourceSnapshotDigest string,
) (*domain.SealReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	var result domain.SealReceiptV2
	err := s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		if exists, err := s.generationExistsUnlocked(projectedGenerationsDir, generationID); err != nil {
			return err
		} else if exists {
			receipt, err := s.validateSealedGenerationUnlocked(generationID)
			if err != nil {
				return err
			}
			result = *receipt
			_ = os.RemoveAll(s.io.path(projectedBuildingGenerationPath(generationID)))
			return nil
		}

		buildingBase, err := s.requireBuildingUnlocked(generationID)
		if err != nil {
			return err
		}
		pendingIntents, err := s.loadPendingProjectedChapterIntentsUnlocked(generationID)
		if err != nil {
			return err
		}
		if len(pendingIntents) > 0 {
			return fmt.Errorf("cannot seal generation %s with %d pending chapter intent(s)",
				generationID, len(pendingIntents))
		}
		if err := rejectUnexpectedBuildingFiles(s.io.path(buildingBase)); err != nil {
			return err
		}
		generation, err := s.loadGenerationAtUnlocked(buildingBase)
		if err != nil {
			return err
		}
		if generation == nil {
			return fmt.Errorf("building generation %s has no generation manifest", generationID)
		}
		if string(generation.Status) != "building" {
			return fmt.Errorf("generation %s is not building", generationID)
		}
		source, err := s.loadSourceAtUnlocked(buildingBase)
		if err != nil {
			return err
		}
		if generation.GenerationDigest != expectedGenerationDigest {
			return fmt.Errorf("seal generation compare-and-swap failed: got %s want %s",
				generation.GenerationDigest, expectedGenerationDigest)
		}
		if source.SnapshotDigest != expectedSourceSnapshotDigest {
			return fmt.Errorf("seal source snapshot compare-and-swap failed: got %s want %s",
				source.SnapshotDigest, expectedSourceSnapshotDigest)
		}
		registry, err := s.loadRegistryAtUnlocked(buildingBase)
		if err != nil {
			return err
		}
		bundles, err := s.loadBundlesAtUnlocked(buildingBase)
		if err != nil {
			return err
		}
		if len(bundles) != generation.ExpectedChapterCount {
			return fmt.Errorf("cannot seal generation %s: projected %d of %d chapters",
				generationID, len(bundles), generation.ExpectedChapterCount)
		}
		if len(bundles) == 0 {
			return fmt.Errorf("cannot seal generation %s without bundles", generationID)
		}
		if bundles[0].Chapter != generation.FirstProjectedChapter ||
			bundles[len(bundles)-1].Chapter != generation.LastProjectedChapter {
			return fmt.Errorf("cannot seal generation %s: bundle range is %d..%d, want %d..%d",
				generationID,
				bundles[0].Chapter,
				bundles[len(bundles)-1].Chapter,
				generation.FirstProjectedChapter,
				generation.LastProjectedChapter,
			)
		}
		registryRoot, err := domain.ComputeObligationRegistryV2Root(*registry)
		if err != nil {
			return err
		}

		completeBuilding := *generation
		completeBuilding.ProjectedChapterCount = len(bundles)
		completeBuilding.ChainHeadRoot = bundles[0].BundleDigest
		completeBuilding.ChainTailRoot = bundles[len(bundles)-1].BundleDigest
		completeBuilding.ObligationRegistryRoot = registryRoot
		completeBuilding.GenerationDigest = ""
		completeBuilding.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(completeBuilding)
		if err != nil {
			return err
		}
		if err := domain.ValidatePlanningGenerationV2(completeBuilding); err != nil {
			return err
		}
		if err := domain.ValidateProjectedChapterBundleChain(completeBuilding, bundles, *registry); err != nil {
			return err
		}
		if err := domain.ValidatePlanningSourceSnapshotAgainstGenerationV2(*source, completeBuilding); err != nil {
			return err
		}

		sealedAt := s.now().UTC().Format(time.RFC3339Nano)
		sealed := completeBuilding
		sealed.Status = domain.PlanningGenerationSealedV2
		sealed.SealedAt = sealedAt
		sealed.GenerationDigest = ""
		sealed.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(sealed)
		if err != nil {
			return err
		}
		if err := domain.ValidatePlanningGenerationV2(sealed); err != nil {
			return err
		}

		manifest, err := buildProjectedChainManifest(sealed, bundles, *registry, sealedAt)
		if err != nil {
			return err
		}
		receipt := domain.SealReceiptV2{
			Version:                domain.SealReceiptV2Version,
			GenerationID:           sealed.GenerationID,
			GenerationDigest:       sealed.GenerationDigest,
			ChainManifestDigest:    manifest.ManifestDigest,
			ChainHeadRoot:          sealed.ChainHeadRoot,
			ChainTailRoot:          sealed.ChainTailRoot,
			ObligationRegistryRoot: sealed.ObligationRegistryRoot,
			BaseCanonRoot:          sealed.BaseCanonRoot,
			BaseStateRoot:          sealed.BaseStateRoot,
			PlanningDependencyRoot: sealed.PlanningDependencyRoot,
			SealedAt:               sealedAt,
		}
		receipt.ReceiptDigest, err = domain.ComputeSealReceiptV2Digest(receipt)
		if err != nil {
			return err
		}
		if err := domain.ValidateProjectedChainManifestV2(manifest, sealed, bundles, *registry); err != nil {
			return err
		}
		if err := domain.ValidateSealReceiptV2(receipt, sealed, manifest); err != nil {
			return err
		}

		tmpAbs, err := os.MkdirTemp(s.io.path(projectedGenerationsDir), "."+generationID+".seal-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmpAbs) }()
		tmpRel, err := filepath.Rel(s.io.dir, tmpAbs)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(tmpAbs, "chapters"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(tmpAbs, "manifests"), 0o755); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedGenerationManifestFile), sealed); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedSourceSnapshotFile), *source); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedObligationRegistryFile), *registry); err != nil {
			return err
		}
		for i := range bundles {
			if err := s.io.WriteJSONUnlocked(projectedBundlePath(tmpRel, bundles[i].Chapter), bundles[i]); err != nil {
				return err
			}
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedChainManifestFile), manifest); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(filepath.Join(tmpRel, projectedSealReceiptFile), receipt); err != nil {
			return err
		}
		if err := syncProjectedDirs(
			filepath.Join(tmpAbs, "chapters"),
			filepath.Join(tmpAbs, "manifests"),
			tmpAbs,
		); err != nil {
			return err
		}
		if err := os.Rename(tmpAbs, s.io.path(projectedSealedGenerationPath(generationID))); err != nil {
			return err
		}
		if err := syncProjectedDirs(s.io.path(projectedGenerationsDir)); err != nil {
			return err
		}
		result = receipt
		_ = os.RemoveAll(s.io.path(buildingBase))
		return syncProjectedDirs(s.io.path(projectedBuildingDir))
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func buildProjectedChainManifest(
	generation domain.PlanningGenerationV2,
	bundles []domain.ProjectedChapterBundle,
	registry domain.ObligationRegistryV2,
	createdAt string,
) (domain.ProjectedChainManifestV2, error) {
	entries := make([]domain.ProjectedBundleDigestEntryV2, 0, len(bundles))
	factDigests := make(map[string]struct{})
	craftDigests := make(map[string]struct{})
	for _, bundle := range bundles {
		if bundle.RAGFactReceipt == nil || bundle.CraftRecallReceipt == nil {
			return domain.ProjectedChainManifestV2{}, fmt.Errorf("chapter %d does not have 100%% RAG/craft receipt coverage", bundle.Chapter)
		}
		if err := domain.ValidateRAGFactReceipt(*bundle.RAGFactReceipt); err != nil {
			return domain.ProjectedChainManifestV2{}, fmt.Errorf("chapter %d RAG fact receipt: %w", bundle.Chapter, err)
		}
		ragDigest, err := domain.RAGFactReceiptDigestV2(*bundle.RAGFactReceipt)
		if err != nil {
			return domain.ProjectedChainManifestV2{}, err
		}
		if bundle.RAGFactReceiptDigest != ragDigest {
			return domain.ProjectedChainManifestV2{}, fmt.Errorf("chapter %d RAG fact receipt digest mismatch", bundle.Chapter)
		}
		factDigests[ragDigest] = struct{}{}
		craftDigest, err := domain.CraftRecallReceiptDigestV2(*bundle.CraftRecallReceipt)
		if err != nil {
			return domain.ProjectedChainManifestV2{}, fmt.Errorf("chapter %d craft receipt: %w", bundle.Chapter, err)
		}
		if bundle.CraftRecallReceiptDigest != craftDigest {
			return domain.ProjectedChainManifestV2{}, fmt.Errorf("chapter %d craft receipt digest mismatch", bundle.Chapter)
		}
		craftDigests[craftDigest] = struct{}{}
		entries = append(entries, domain.ProjectedBundleDigestEntryV2{
			Chapter:                  bundle.Chapter,
			BundleDigest:             bundle.BundleDigest,
			PreviousBundleDigest:     bundle.PreviousBundleDigest,
			ProjectedPreStateRoot:    bundle.ProjectedPreStateRoot,
			ProjectedPostStateRoot:   bundle.ProjectedPostStateRoot,
			RAGFactReceiptDigest:     ragDigest,
			CraftRecallReceiptDigest: craftDigest,
		})
	}
	orderedFactDigests := make([]string, 0, len(factDigests))
	for digest := range factDigests {
		orderedFactDigests = append(orderedFactDigests, digest)
	}
	sortStrings(orderedFactDigests)
	orderedCraftDigests := make([]string, 0, len(craftDigests))
	for digest := range craftDigests {
		orderedCraftDigests = append(orderedCraftDigests, digest)
	}
	sortStrings(orderedCraftDigests)
	manifest := domain.ProjectedChainManifestV2{
		Version:                domain.ProjectedChainManifestV2Version,
		GenerationID:           generation.GenerationID,
		FirstChapter:           generation.FirstProjectedChapter,
		LastChapter:            generation.LastProjectedChapter,
		ChapterCount:           len(bundles),
		Entries:                entries,
		FactReceiptDigests:     orderedFactDigests,
		CraftReceiptDigests:    orderedCraftDigests,
		ChainHeadRoot:          generation.ChainHeadRoot,
		ChainTailRoot:          generation.ChainTailRoot,
		ObligationRegistryRoot: generation.ObligationRegistryRoot,
		CreatedAt:              createdAt,
	}
	var err error
	manifest.ManifestDigest, err = domain.ComputeProjectedChainManifestV2Digest(manifest)
	if err != nil {
		return domain.ProjectedChainManifestV2{}, err
	}
	if root, err := domain.ComputeObligationRegistryV2Root(registry); err != nil {
		return domain.ProjectedChainManifestV2{}, err
	} else if root != manifest.ObligationRegistryRoot {
		return domain.ProjectedChainManifestV2{}, fmt.Errorf("manifest obligation registry root mismatch")
	}
	return manifest, nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func (s *ProjectedStoreV2) validateSealedGenerationUnlocked(
	generationID string,
) (*domain.SealReceiptV2, error) {
	base := projectedSealedGenerationPath(generationID)
	generation, err := s.loadGenerationAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if generation == nil || string(generation.Status) != "sealed" {
		return nil, fmt.Errorf("generation %s is not a sealed generation", generationID)
	}
	source, err := s.loadSourceAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if source.GenerationID != generation.GenerationID {
		return nil, fmt.Errorf("sealed source snapshot generation mismatch")
	}
	registry, err := s.loadRegistryAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	bundles, err := s.loadBundlesAtUnlocked(base)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateProjectedChapterBundleChain(*generation, bundles, *registry); err != nil {
		return nil, err
	}
	var manifest domain.ProjectedChainManifestV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedChainManifestFile), &manifest); err != nil {
		return nil, err
	}
	if err := domain.ValidateProjectedChainManifestV2(manifest, *generation, bundles, *registry); err != nil {
		return nil, err
	}
	var receipt domain.SealReceiptV2
	if err := s.readJSONUnlocked(filepath.Join(base, projectedSealReceiptFile), &receipt); err != nil {
		return nil, err
	}
	if err := domain.ValidateSealReceiptV2(receipt, *generation, manifest); err != nil {
		return nil, err
	}
	return &receipt, nil
}

func (s *ProjectedStoreV2) SaveSuffixInvalidationReceipt(
	receipt domain.SuffixInvalidationReceiptV2,
) (string, error) {
	if err := validateProjectedPathComponent("generation_id", receipt.GenerationID); err != nil {
		return "", err
	}
	digest, err := domain.ComputeSuffixInvalidationReceiptV2Digest(receipt)
	if err != nil {
		return "", err
	}
	if receipt.ReceiptDigest == "" {
		receipt.ReceiptDigest = digest
	}
	if receipt.ReceiptDigest != digest {
		return "", fmt.Errorf("suffix invalidation digest mismatch: got %s want %s", receipt.ReceiptDigest, digest)
	}
	if err := domain.ValidateSuffixInvalidationReceiptV2(receipt); err != nil {
		return "", err
	}
	err = s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		rel := projectedInvalidationReceiptPath(receipt.GenerationID, receipt.ReceiptDigest)
		if exists, err := s.contentAddressedJSONExistsUnlocked(rel, receipt); err != nil {
			return err
		} else if exists {
			return nil
		}
		generation, err := s.loadGenerationAtUnlocked(projectedSealedGenerationPath(receipt.GenerationID))
		if err != nil {
			return err
		}
		if generation == nil {
			return fmt.Errorf("suffix invalidation requires sealed generation %s", receipt.GenerationID)
		}
		if _, err := s.validateSealedGenerationUnlocked(receipt.GenerationID); err != nil {
			return err
		}
		if receipt.FromChapter < generation.FirstProjectedChapter ||
			receipt.ThroughChapter > generation.LastProjectedChapter {
			return fmt.Errorf("suffix invalidation range %d..%d is outside generation %d..%d",
				receipt.FromChapter,
				receipt.ThroughChapter,
				generation.FirstProjectedChapter,
				generation.LastProjectedChapter,
			)
		}
		records, err := s.loadSuffixInvalidationsUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		tail, err := suffixInvalidationTail(records)
		if err != nil {
			return err
		}
		if receipt.PreviousReceiptDigest != tail {
			return fmt.Errorf("suffix invalidation previous digest got %s want %s", receipt.PreviousReceiptDigest, tail)
		}
		_, err = s.writeContentAddressedJSONUnlocked(rel, receipt)
		return err
	})
	return receipt.ReceiptDigest, err
}

func (s *ProjectedStoreV2) loadSuffixInvalidationsUnlocked(
	generationID string,
) ([]domain.SuffixInvalidationReceiptV2, error) {
	dir := s.io.path(filepath.Join(projectedInvalidationsDir, generationID))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.SuffixInvalidationReceiptV2, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("invalid suffix invalidation entry %s", entry.Name())
		}
		var receipt domain.SuffixInvalidationReceiptV2
		if err := s.readJSONUnlocked(filepath.Join(projectedInvalidationsDir, generationID, entry.Name()), &receipt); err != nil {
			return nil, err
		}
		if err := domain.ValidateSuffixInvalidationReceiptV2(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || entry.Name() != receipt.ReceiptDigest+".json" {
			return nil, fmt.Errorf("suffix invalidation path identity mismatch")
		}
		out = append(out, receipt)
	}
	if _, err := suffixInvalidationTail(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ProjectedStoreV2) LoadSuffixInvalidationReceipts(
	generationID string,
) ([]domain.SuffixInvalidationReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() ([]domain.SuffixInvalidationReceiptV2, error) {
		return s.loadSuffixInvalidationsUnlocked(generationID)
	})
}

func suffixInvalidationTail(records []domain.SuffixInvalidationReceiptV2) (string, error) {
	if len(records) == 0 {
		return "", nil
	}
	known := make(map[string]struct{}, len(records))
	referenced := make(map[string]struct{}, len(records))
	for _, record := range records {
		known[record.ReceiptDigest] = struct{}{}
		if record.PreviousReceiptDigest != "" {
			referenced[record.PreviousReceiptDigest] = struct{}{}
		}
	}
	tail := ""
	for digest := range known {
		if _, used := referenced[digest]; used {
			continue
		}
		if tail != "" {
			return "", fmt.Errorf("suffix invalidation history has multiple tails")
		}
		tail = digest
	}
	if tail == "" {
		return "", fmt.Errorf("suffix invalidation history contains a cycle")
	}
	return tail, nil
}

func (s *ProjectedStoreV2) SaveGenerationArchiveReceipt(
	receipt domain.GenerationArchiveReceiptV2,
) (string, error) {
	if err := validateProjectedPathComponent("generation_id", receipt.GenerationID); err != nil {
		return "", err
	}
	digest, err := domain.ComputeGenerationArchiveReceiptV2Digest(receipt)
	if err != nil {
		return "", err
	}
	if receipt.ReceiptDigest == "" {
		receipt.ReceiptDigest = digest
	}
	if receipt.ReceiptDigest != digest {
		return "", fmt.Errorf("generation archive digest mismatch: got %s want %s", receipt.ReceiptDigest, digest)
	}
	if err := domain.ValidateGenerationArchiveReceiptV2(receipt); err != nil {
		return "", err
	}
	err = s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		rel := projectedArchiveReceiptPath(receipt.GenerationID, receipt.ReceiptDigest)
		if exists, err := s.contentAddressedJSONExistsUnlocked(rel, receipt); err != nil {
			return err
		} else if exists {
			return nil
		}
		seal, err := s.validateSealedGenerationUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		if seal.ReceiptDigest != receipt.SealReceiptDigest {
			return fmt.Errorf("archive seal digest mismatch: got %s want %s", receipt.SealReceiptDigest, seal.ReceiptDigest)
		}
		records, err := s.loadGenerationArchivesUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		tail, err := generationArchiveTail(records)
		if err != nil {
			return err
		}
		if receipt.PreviousReceiptDigest != tail {
			return fmt.Errorf("generation archive previous digest got %s want %s", receipt.PreviousReceiptDigest, tail)
		}
		_, err = s.writeContentAddressedJSONUnlocked(rel, receipt)
		return err
	})
	return receipt.ReceiptDigest, err
}

func (s *ProjectedStoreV2) loadGenerationArchivesUnlocked(
	generationID string,
) ([]domain.GenerationArchiveReceiptV2, error) {
	dir := s.io.path(filepath.Join(projectedArchivesDir, generationID))
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.GenerationArchiveReceiptV2, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("invalid generation archive entry %s", entry.Name())
		}
		var receipt domain.GenerationArchiveReceiptV2
		if err := s.readJSONUnlocked(filepath.Join(projectedArchivesDir, generationID, entry.Name()), &receipt); err != nil {
			return nil, err
		}
		if err := domain.ValidateGenerationArchiveReceiptV2(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || entry.Name() != receipt.ReceiptDigest+".json" {
			return nil, fmt.Errorf("generation archive path identity mismatch")
		}
		out = append(out, receipt)
	}
	if _, err := generationArchiveTail(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ProjectedStoreV2) LoadGenerationArchiveReceipts(
	generationID string,
) ([]domain.GenerationArchiveReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() ([]domain.GenerationArchiveReceiptV2, error) {
		return s.loadGenerationArchivesUnlocked(generationID)
	})
}

func generationArchiveTail(records []domain.GenerationArchiveReceiptV2) (string, error) {
	if len(records) == 0 {
		return "", nil
	}
	known := make(map[string]struct{}, len(records))
	referenced := make(map[string]struct{}, len(records))
	for _, record := range records {
		known[record.ReceiptDigest] = struct{}{}
		if record.PreviousReceiptDigest != "" {
			referenced[record.PreviousReceiptDigest] = struct{}{}
		}
	}
	tail := ""
	for digest := range known {
		if _, used := referenced[digest]; used {
			continue
		}
		if tail != "" {
			return "", fmt.Errorf("generation archive history has multiple tails")
		}
		tail = digest
	}
	if tail == "" {
		return "", fmt.Errorf("generation archive history contains a cycle")
	}
	return tail, nil
}

func (s *ProjectedStoreV2) generationInvalidatedForChapterUnlocked(generationID string, chapter int) (bool, error) {
	records, err := s.loadSuffixInvalidationsUnlocked(generationID)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if chapter >= record.FromChapter && chapter <= record.ThroughChapter {
			return true, nil
		}
	}
	return false, nil
}

func (s *ProjectedStoreV2) generationArchivedUnlocked(generationID string) (bool, error) {
	records, err := s.loadGenerationArchivesUnlocked(generationID)
	if err != nil {
		return false, err
	}
	return len(records) > 0, nil
}

func jsonValuesEqual(left, right any) bool {
	lraw, lerr := json.Marshal(left)
	rraw, rerr := json.Marshal(right)
	return lerr == nil && rerr == nil && bytes.Equal(lraw, rraw)
}

func (s *ProjectedStoreV2) LoadProjectionCursor() (*domain.ProjectionCursorV2, error) {
	return withProjectedReadResult(s, func() (*domain.ProjectionCursorV2, error) {
		cursor, err := s.loadProjectionCursorUnlocked()
		if err != nil || cursor == nil {
			return cursor, err
		}
		if err := s.validateProjectionCursorAgainstStoreUnlocked(*cursor); err != nil {
			return nil, err
		}
		return cursor, nil
	})
}

func (s *ProjectedStoreV2) loadProjectionCursorUnlocked() (*domain.ProjectionCursorV2, error) {
	var cursor domain.ProjectionCursorV2
	if err := s.io.ReadJSONUnlocked(projectedProjectionCursorPath, &cursor); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateProjectionCursorV2(cursor); err != nil {
		return nil, err
	}
	return &cursor, nil
}

// CompareAndSwapProjectionCursor changes only projection_cursor.json. It does
// not read or write realization_cursor.json.
func (s *ProjectedStoreV2) CompareAndSwapProjectionCursor(
	expected *domain.ProjectionCursorV2,
	next domain.ProjectionCursorV2,
) error {
	if err := validateProjectedPathComponent("generation_id", next.GenerationID); err != nil {
		return err
	}
	if err := domain.ValidateProjectionCursorV2(next); err != nil {
		return err
	}
	if expected != nil {
		if err := domain.ValidateProjectionCursorV2(*expected); err != nil {
			return fmt.Errorf("expected projection cursor: %w", err)
		}
	}
	return s.withProjectedWriteLock(func() error {
		var current *domain.ProjectionCursorV2
		var decoded domain.ProjectionCursorV2
		err := s.io.ReadJSONUnlocked(projectedProjectionCursorPath, &decoded)
		switch {
		case err == nil:
			if err := domain.ValidateProjectionCursorV2(decoded); err != nil {
				return err
			}
			current = &decoded
		case os.IsNotExist(err):
		default:
			return err
		}
		if expected == nil {
			if current != nil {
				return fmt.Errorf("projection cursor compare-and-swap failed: expected no cursor")
			}
		} else if current == nil || !jsonValuesEqual(*current, *expected) {
			return fmt.Errorf("projection cursor compare-and-swap failed: current cursor differs from expected")
		}
		if current != nil && jsonValuesEqual(*current, next) {
			return nil
		}
		if err := s.validateProjectionCursorAgainstStoreUnlocked(next); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(projectedProjectionCursorPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
}

func (s *ProjectedStoreV2) validateProjectionCursorAgainstStoreUnlocked(cursor domain.ProjectionCursorV2) error {
	base := projectedBuildingGenerationPath(cursor.GenerationID)
	generation, err := s.loadGenerationAtUnlocked(base)
	if err != nil {
		return err
	}
	if generation == nil {
		base = projectedSealedGenerationPath(cursor.GenerationID)
		generation, err = s.loadGenerationAtUnlocked(base)
		if err != nil {
			return err
		}
	}
	if generation == nil {
		return fmt.Errorf("projection cursor references unknown generation %s", cursor.GenerationID)
	}
	bundles, err := s.loadBundlesAtUnlocked(base)
	if err != nil {
		return err
	}
	last := generation.FirstProjectedChapter - 1
	lastDigest := ""
	if len(bundles) > 0 {
		last = bundles[len(bundles)-1].Chapter
		lastDigest = bundles[len(bundles)-1].BundleDigest
	}
	if cursor.LastProjectedChapter != last ||
		cursor.NextProjectChapter != last+1 ||
		cursor.LastBundleDigest != lastDigest {
		return fmt.Errorf("projection cursor does not match durable chain: last/next/tail got %d/%d/%s want %d/%d/%s",
			cursor.LastProjectedChapter,
			cursor.NextProjectChapter,
			cursor.LastBundleDigest,
			last,
			last+1,
			lastDigest,
		)
	}
	return nil
}

func (s *ProjectedStoreV2) LoadRealizationCursor() (*domain.RealizationCursorV2, error) {
	return withProjectedReadResult(s, func() (*domain.RealizationCursorV2, error) {
		cursor, err := s.loadRealizationCursorUnlocked()
		if err != nil || cursor == nil {
			return cursor, err
		}
		if err := s.validateRealizationControlUnlocked(*cursor); err != nil {
			return nil, err
		}
		return cursor, nil
	})
}

func (s *ProjectedStoreV2) loadRealizationCursorUnlocked() (*domain.RealizationCursorV2, error) {
	var cursor domain.RealizationCursorV2
	if err := s.io.ReadJSONUnlocked(projectedRealizationCursorPath, &cursor); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateRealizationCursorV2(cursor); err != nil {
		return nil, err
	}
	return &cursor, nil
}

// CompareAndSwapRealizationCursor changes only realization_cursor.json and
// requires its generation to have been sealed.
func (s *ProjectedStoreV2) CompareAndSwapRealizationCursor(
	expected *domain.RealizationCursorV2,
	next domain.RealizationCursorV2,
) error {
	if err := validateProjectedPathComponent("active_generation_id", next.ActiveGenerationID); err != nil {
		return err
	}
	if err := domain.ValidateRealizationCursorV2(next); err != nil {
		return err
	}
	if expected != nil {
		if err := domain.ValidateRealizationCursorV2(*expected); err != nil {
			return fmt.Errorf("expected realization cursor: %w", err)
		}
	}
	return s.withProjectedWriteLock(func() error {
		var current *domain.RealizationCursorV2
		var decoded domain.RealizationCursorV2
		err := s.io.ReadJSONUnlocked(projectedRealizationCursorPath, &decoded)
		switch {
		case err == nil:
			if err := domain.ValidateRealizationCursorV2(decoded); err != nil {
				return err
			}
			current = &decoded
		case os.IsNotExist(err):
		default:
			return err
		}
		if expected == nil {
			if current != nil {
				return fmt.Errorf("realization cursor compare-and-swap failed: expected no cursor")
			}
		} else if current == nil || !jsonValuesEqual(*current, *expected) {
			return fmt.Errorf("realization cursor compare-and-swap failed: current cursor differs from expected")
		}
		if current != nil && jsonValuesEqual(*current, next) {
			return nil
		}
		if err := s.validateRealizationControlUnlocked(next); err != nil {
			return err
		}
		if err := s.validateRealizationTransitionUnlocked(current, next); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
}

// AbandonInvalidatedPromotion clears an unaccepted promotion only after a
// durable suffix-invalidation receipt names a different successor generation.
// This is the explicit escape hatch for a sealed plan proven impossible during
// render; ordinary cursor transitions still cannot move backward.
func (s *ProjectedStoreV2) AbandonInvalidatedPromotion(
	expected domain.RealizationCursorV2,
	invalidation domain.SuffixInvalidationReceiptV2,
) (*domain.RealizationCursorV2, error) {
	if err := domain.ValidateRealizationCursorV2(expected); err != nil {
		return nil, err
	}
	if err := domain.ValidateSuffixInvalidationReceiptV2(invalidation); err != nil {
		return nil, err
	}
	if expected.ActivePromotedChapter <= 0 ||
		invalidation.GenerationID != expected.ActiveGenerationID ||
		expected.ActivePromotedChapter < invalidation.FromChapter ||
		expected.ActivePromotedChapter > invalidation.ThroughChapter ||
		invalidation.CauseReceiptDigest != expected.ActivePromotionReceiptDigest ||
		invalidation.ReplacementGenerationID == "" {
		return nil, fmt.Errorf("abandon promotion requires exact active promotion invalidation and successor")
	}
	var next domain.RealizationCursorV2
	err := s.withProjectedWriteLock(func() error {
		current, err := s.loadRealizationCursorUnlocked()
		if err != nil || current == nil {
			return fmt.Errorf("abandon promotion load realization cursor: %w", err)
		}
		if !jsonValuesEqual(*current, expected) {
			return fmt.Errorf("abandon promotion compare-and-swap failed: current cursor differs")
		}
		var durable domain.SuffixInvalidationReceiptV2
		if err := s.readJSONUnlocked(
			projectedInvalidationReceiptPath(
				invalidation.GenerationID,
				invalidation.ReceiptDigest,
			),
			&durable,
		); err != nil {
			return fmt.Errorf("abandon promotion requires durable invalidation receipt: %w", err)
		}
		if !jsonValuesEqual(durable, invalidation) {
			return fmt.Errorf("abandon promotion invalidation receipt differs from durable record")
		}
		ok, err := s.hasExactPromotionReceiptUnlocked(
			current.ActiveGenerationID,
			current.ActivePromotedChapter,
			current.ActivePromotionReceiptDigest,
		)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("abandon promotion active receipt is not durable")
		}
		next = *current
		next.ActivePromotedChapter = 0
		next.ActivePromotionReceiptDigest = ""
		next.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		next.CursorDigest, err = domain.ComputeRealizationCursorV2Digest(next)
		if err != nil {
			return err
		}
		if err := domain.ValidateRealizationCursorV2(next); err != nil {
			return err
		}
		if err := s.validateRealizationControlUnlocked(next); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
	if err != nil {
		return nil, err
	}
	return &next, nil
}

func (s *ProjectedStoreV2) validateRealizationControlUnlocked(cursor domain.RealizationCursorV2) error {
	if _, err := s.validateSealedGenerationUnlocked(cursor.ActiveGenerationID); err != nil {
		return fmt.Errorf("realization cursor requires valid sealed generation %s: %w", cursor.ActiveGenerationID, err)
	}
	var active domain.ActivePlanningGenerationV2
	if err := s.readJSONUnlocked(projectedActiveGenerationPath, &active); err != nil {
		return fmt.Errorf("realization cursor requires active generation pointer: %w", err)
	}
	if err := domain.ValidateActivePlanningGenerationV2(active); err != nil {
		return err
	}
	return s.validateRealizationControlWithActiveUnlocked(cursor, active)
}

func (s *ProjectedStoreV2) validateRealizationControlWithActiveUnlocked(
	cursor domain.RealizationCursorV2,
	active domain.ActivePlanningGenerationV2,
) error {
	seal, err := s.validateSealedGenerationUnlocked(cursor.ActiveGenerationID)
	if err != nil {
		return err
	}
	if seal.ReceiptDigest != active.SealReceiptDigest {
		return fmt.Errorf("active generation seal receipt digest mismatch")
	}
	if active.GenerationID != cursor.ActiveGenerationID {
		return fmt.Errorf("realization cursor generation %s differs from active generation %s",
			cursor.ActiveGenerationID, active.GenerationID)
	}
	return nil
}

func (s *ProjectedStoreV2) validateRealizationTransitionUnlocked(
	current *domain.RealizationCursorV2,
	next domain.RealizationCursorV2,
) error {
	if current == nil {
		generation, err := s.loadGenerationAtUnlocked(projectedSealedGenerationPath(next.ActiveGenerationID))
		if err != nil {
			return err
		}
		if generation == nil {
			return fmt.Errorf("realization cursor references missing generation")
		}
		if next.ActivePromotedChapter != 0 ||
			next.ActivePromotionReceiptDigest != "" ||
			next.LastAcceptedChapter != generation.BaseCanonChapter ||
			next.NextPromoteChapter != generation.FirstProjectedChapter {
			return fmt.Errorf("initial realization cursor does not match generation base/range")
		}
		return nil
	}
	if current.ActiveGenerationID != next.ActiveGenerationID {
		if next.ActivePromotedChapter != 0 ||
			next.ActivePromotionReceiptDigest != "" ||
			next.LastAcceptedChapter != current.LastAcceptedChapter ||
			next.NextPromoteChapter != current.NextPromoteChapter ||
			next.LastOutcomeReceiptDigest != current.LastOutcomeReceiptDigest {
			return fmt.Errorf("generation switch may not advance realization counters")
		}
		return nil
	}
	sameCounters := current.NextPromoteChapter == next.NextPromoteChapter &&
		current.ActivePromotedChapter == next.ActivePromotedChapter &&
		current.ActivePromotionReceiptDigest == next.ActivePromotionReceiptDigest &&
		current.LastAcceptedChapter == next.LastAcceptedChapter &&
		current.LastOutcomeReceiptDigest == next.LastOutcomeReceiptDigest
	if sameCounters {
		return nil
	}
	if current.ActivePromotedChapter == 0 &&
		next.ActivePromotedChapter == current.NextPromoteChapter &&
		next.NextPromoteChapter == current.NextPromoteChapter &&
		next.LastAcceptedChapter == current.LastAcceptedChapter {
		if len(next.BlockedByRewrites) > 0 {
			return fmt.Errorf("cannot promote while blocked_by_rewrites is non-empty")
		}
		if next.ActivePromotionReceiptDigest == "" {
			return fmt.Errorf("promotion transition must bind promotion receipt digest")
		}
		ok, err := s.hasExactPromotionReceiptUnlocked(
			next.ActiveGenerationID,
			next.ActivePromotedChapter,
			next.ActivePromotionReceiptDigest,
		)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("realization promotion transition lacks durable promotion receipt")
		}
		return nil
	}
	if current.ActivePromotedChapter > 0 &&
		next.ActivePromotedChapter == 0 &&
		next.ActivePromotionReceiptDigest == "" &&
		next.LastAcceptedChapter == current.ActivePromotedChapter &&
		next.NextPromoteChapter == current.ActivePromotedChapter+1 {
		if next.LastOutcomeReceiptDigest == "" {
			return fmt.Errorf("acceptance transition must bind outcome receipt digest")
		}
		ok, err := s.hasExactActualOutcomeReceiptUnlocked(
			next.ActiveGenerationID,
			current.ActivePromotedChapter,
			next.LastOutcomeReceiptDigest,
			current.ActivePromotionReceiptDigest,
		)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("realization acceptance transition lacks durable actual outcome receipt")
		}
		var outcome domain.ActualOutcomeReceiptV2
		if err := s.readJSONUnlocked(
			projectedActualOutcomePath(
				next.ActiveGenerationID,
				current.ActivePromotedChapter,
				next.LastOutcomeReceiptDigest,
			),
			&outcome,
		); err != nil {
			return err
		}
		if !outcome.ProjectionMatch {
			generation, err := s.loadGenerationAtUnlocked(
				projectedSealedGenerationPath(next.ActiveGenerationID),
			)
			if err != nil {
				return err
			}
			if generation != nil && current.ActivePromotedChapter < generation.LastProjectedChapter {
				invalidated, err := s.generationInvalidatedForChapterUnlocked(
					next.ActiveGenerationID,
					current.ActivePromotedChapter+1,
				)
				if err != nil {
					return err
				}
				if !invalidated {
					return fmt.Errorf("mismatching actual outcome requires durable suffix invalidation before cursor advance")
				}
			}
		}
		return nil
	}
	return fmt.Errorf("illegal realization cursor transition")
}

func (s *ProjectedStoreV2) LoadActiveGeneration() (*domain.ActivePlanningGenerationV2, error) {
	return withProjectedReadResult(s, func() (*domain.ActivePlanningGenerationV2, error) {
		active, err := s.loadActiveGenerationUnlocked()
		if err != nil || active == nil {
			return active, err
		}
		seal, err := s.validateSealedGenerationUnlocked(active.GenerationID)
		if err != nil {
			return nil, err
		}
		if seal.ReceiptDigest != active.SealReceiptDigest {
			return nil, fmt.Errorf("active generation seal digest mismatch")
		}
		return active, nil
	})
}

func (s *ProjectedStoreV2) loadActiveGenerationUnlocked() (*domain.ActivePlanningGenerationV2, error) {
	var active domain.ActivePlanningGenerationV2
	if err := s.io.ReadJSONUnlocked(projectedActiveGenerationPath, &active); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := domain.ValidateActivePlanningGenerationV2(active); err != nil {
		return nil, err
	}
	return &active, nil
}

// CompareAndSwapActiveGeneration atomically switches the external active
// pointer. The sealed directory itself is never edited.
func (s *ProjectedStoreV2) CompareAndSwapActiveGeneration(
	expected *domain.ActivePlanningGenerationV2,
	next domain.ActivePlanningGenerationV2,
) error {
	if err := validateProjectedPathComponent("generation_id", next.GenerationID); err != nil {
		return err
	}
	if err := domain.ValidateActivePlanningGenerationV2(next); err != nil {
		return err
	}
	if expected != nil {
		if err := domain.ValidateActivePlanningGenerationV2(*expected); err != nil {
			return fmt.Errorf("expected active generation: %w", err)
		}
	}
	return s.withProjectedWriteLock(func() error {
		var current *domain.ActivePlanningGenerationV2
		var decoded domain.ActivePlanningGenerationV2
		err := s.io.ReadJSONUnlocked(projectedActiveGenerationPath, &decoded)
		switch {
		case err == nil:
			if err := domain.ValidateActivePlanningGenerationV2(decoded); err != nil {
				return err
			}
			current = &decoded
		case os.IsNotExist(err):
		default:
			return err
		}
		if expected == nil {
			if current != nil {
				return fmt.Errorf("active generation compare-and-swap failed: expected no active generation")
			}
		} else if current == nil || !jsonValuesEqual(*current, *expected) {
			return fmt.Errorf("active generation compare-and-swap failed: current pointer differs from expected")
		}
		if current != nil && jsonValuesEqual(*current, next) {
			return nil
		}
		if current == nil {
			if next.PreviousGenerationID != "" {
				return fmt.Errorf("first active generation cannot claim previous generation %s", next.PreviousGenerationID)
			}
		} else if next.PreviousGenerationID != current.GenerationID {
			return fmt.Errorf("active generation previous_generation_id got %s want %s",
				next.PreviousGenerationID, current.GenerationID)
		}
		seal, err := s.validateSealedGenerationUnlocked(next.GenerationID)
		if err != nil {
			return fmt.Errorf("active generation requires valid sealed generation %s: %w", next.GenerationID, err)
		}
		archived, err := s.generationArchivedUnlocked(next.GenerationID)
		if err != nil {
			return err
		}
		if archived {
			return fmt.Errorf("generation %s is archived and cannot become active", next.GenerationID)
		}
		if seal.ReceiptDigest != next.SealReceiptDigest {
			return fmt.Errorf("active generation seal digest mismatch: got %s want %s", next.SealReceiptDigest, seal.ReceiptDigest)
		}
		if err := s.io.WriteJSONUnlocked(projectedActiveGenerationPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
}

// ActivateGeneration jointly publishes active_generation.json and the
// realization cursor under one cross-process lock. The two file renames are
// replay-safe: if a crash lands between them, repeating the same expected/next
// values completes the second rename instead of accepting a mixed control
// state.
func (s *ProjectedStoreV2) ActivateGeneration(
	expectedActive *domain.ActivePlanningGenerationV2,
	nextActive domain.ActivePlanningGenerationV2,
	expectedCursor *domain.RealizationCursorV2,
	nextCursor domain.RealizationCursorV2,
) error {
	if err := domain.ValidateActivePlanningGenerationV2(nextActive); err != nil {
		return err
	}
	if err := domain.ValidateRealizationCursorV2(nextCursor); err != nil {
		return err
	}
	if nextActive.GenerationID != nextCursor.ActiveGenerationID {
		return fmt.Errorf("active generation and realization cursor must name the same generation")
	}
	if expectedActive != nil {
		if err := domain.ValidateActivePlanningGenerationV2(*expectedActive); err != nil {
			return fmt.Errorf("expected active generation: %w", err)
		}
	}
	if expectedCursor != nil {
		if err := domain.ValidateRealizationCursorV2(*expectedCursor); err != nil {
			return fmt.Errorf("expected realization cursor: %w", err)
		}
	}
	return s.withProjectedWriteLock(func() error {
		currentActive, err := s.loadActiveGenerationUnlocked()
		if err != nil {
			return err
		}
		currentCursor, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return err
		}
		activeAtExpected := optionalJSONEqual(currentActive, expectedActive)
		activeAtNext := currentActive != nil && jsonValuesEqual(*currentActive, nextActive)
		cursorAtExpected := optionalJSONEqual(currentCursor, expectedCursor)
		cursorAtNext := currentCursor != nil && jsonValuesEqual(*currentCursor, nextCursor)
		if activeAtNext && cursorAtNext {
			return nil
		}
		if !activeAtExpected && !activeAtNext {
			return fmt.Errorf("activate generation active pointer compare-and-swap failed")
		}
		if !cursorAtExpected && !cursorAtNext {
			return fmt.Errorf("activate generation realization cursor compare-and-swap failed")
		}
		if expectedActive == nil {
			if nextActive.PreviousGenerationID != "" {
				return fmt.Errorf("first active generation cannot claim a predecessor")
			}
		} else if nextActive.PreviousGenerationID != expectedActive.GenerationID {
			return fmt.Errorf("next active generation does not bind expected predecessor")
		}
		seal, err := s.validateSealedGenerationUnlocked(nextActive.GenerationID)
		if err != nil {
			return err
		}
		if seal.ReceiptDigest != nextActive.SealReceiptDigest {
			return fmt.Errorf("active generation seal digest mismatch")
		}
		archived, err := s.generationArchivedUnlocked(nextActive.GenerationID)
		if err != nil {
			return err
		}
		if archived {
			return fmt.Errorf("generation %s is archived", nextActive.GenerationID)
		}
		if err := s.validateRealizationControlWithActiveUnlocked(nextCursor, nextActive); err != nil {
			return err
		}
		if err := s.validateRealizationTransitionUnlocked(expectedCursor, nextCursor); err != nil {
			return err
		}
		if !activeAtNext {
			if err := s.io.WriteJSONUnlocked(projectedActiveGenerationPath, nextActive); err != nil {
				return err
			}
		}
		if !cursorAtNext {
			if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, nextCursor); err != nil {
				return err
			}
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
}

func optionalJSONEqual[T any](left, right *T) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return jsonValuesEqual(*left, *right)
}

// ActivateSealedGeneration is the recovery-oriented activation entry point for
// CLI use. It reads raw control files under the write lock and deterministically
// finishes a prior active-pointer-before-cursor crash without requiring callers
// to recover stale timestamps or internal file contents.
func (s *ProjectedStoreV2) ActivateSealedGeneration(
	generationID string,
	blockedRewrites []int,
) (*domain.ActivePlanningGenerationV2, *domain.RealizationCursorV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, nil, err
	}
	for i, chapter := range blockedRewrites {
		if chapter <= 0 {
			return nil, nil, fmt.Errorf("blocked_rewrites[%d] must be > 0", i)
		}
	}
	blockedRewrites = normalizedPositiveInts(blockedRewrites)
	var resultActive domain.ActivePlanningGenerationV2
	var resultCursor domain.RealizationCursorV2
	err := s.withProjectedWriteLock(func() error {
		seal, err := s.validateSealedGenerationUnlocked(generationID)
		if err != nil {
			return err
		}
		archived, err := s.generationArchivedUnlocked(generationID)
		if err != nil {
			return err
		}
		if archived {
			return fmt.Errorf("generation %s is archived", generationID)
		}
		generation, err := s.loadGenerationAtUnlocked(projectedSealedGenerationPath(generationID))
		if err != nil {
			return err
		}
		if generation == nil {
			return fmt.Errorf("sealed generation %s is missing generation manifest", generationID)
		}
		currentActive, err := s.loadActiveGenerationUnlocked()
		if err != nil {
			return err
		}
		currentCursor, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return err
		}
		if currentActive != nil && currentActive.GenerationID == generationID &&
			currentCursor != nil && currentCursor.ActiveGenerationID == generationID {
			if err := s.validateRealizationControlWithActiveUnlocked(*currentCursor, *currentActive); err != nil {
				return err
			}
			resultActive = *currentActive
			resultCursor = *currentCursor
			return nil
		}
		if currentCursor != nil {
			if currentCursor.ActivePromotedChapter != 0 {
				return fmt.Errorf("cannot switch generation while chapter %d is actively promoted",
					currentCursor.ActivePromotedChapter)
			}
			if currentCursor.LastAcceptedChapter != generation.BaseCanonChapter {
				return fmt.Errorf("generation base chapter %d differs from realization last accepted chapter %d",
					generation.BaseCanonChapter, currentCursor.LastAcceptedChapter)
			}
		}

		if currentActive != nil && currentActive.GenerationID == generationID {
			resultActive = *currentActive
		} else {
			resultActive = domain.ActivePlanningGenerationV2{
				Version:           domain.ActivePlanningGenerationV2Version,
				GenerationID:      generationID,
				SealReceiptDigest: seal.ReceiptDigest,
				ActivatedAt:       s.now().UTC().Format(time.RFC3339Nano),
			}
			if currentActive != nil {
				resultActive.PreviousGenerationID = currentActive.GenerationID
			}
			resultActive.RecordDigest, err = domain.ComputeActivePlanningGenerationV2Digest(resultActive)
			if err != nil {
				return err
			}
		}
		resultCursor = domain.RealizationCursorV2{
			ActiveGenerationID:       generationID,
			NextPromoteChapter:       generation.FirstProjectedChapter,
			LastAcceptedChapter:      generation.BaseCanonChapter,
			BlockedByRewrites:        blockedRewrites,
			UpdatedAt:                s.now().UTC().Format(time.RFC3339Nano),
			LastOutcomeReceiptDigest: "",
		}
		if currentCursor != nil && currentCursor.LastAcceptedChapter == generation.BaseCanonChapter {
			resultCursor.LastOutcomeReceiptDigest = currentCursor.LastOutcomeReceiptDigest
		}
		resultCursor.CursorDigest, err = domain.ComputeRealizationCursorV2Digest(resultCursor)
		if err != nil {
			return err
		}
		if err := domain.ValidateActivePlanningGenerationV2(resultActive); err != nil {
			return err
		}
		if err := domain.ValidateRealizationCursorV2(resultCursor); err != nil {
			return err
		}
		if err := s.validateRealizationControlWithActiveUnlocked(resultCursor, resultActive); err != nil {
			return err
		}
		if currentActive == nil || !jsonValuesEqual(*currentActive, resultActive) {
			if err := s.io.WriteJSONUnlocked(projectedActiveGenerationPath, resultActive); err != nil {
				return err
			}
		}
		if currentCursor == nil || !jsonValuesEqual(*currentCursor, resultCursor) {
			if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, resultCursor); err != nil {
				return err
			}
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
	if err != nil {
		return nil, nil, err
	}
	return &resultActive, &resultCursor, nil
}

func normalizedPositiveInts(values []int) []int {
	out := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if out == nil {
		return []int{}
	}
	return out
}

func (s *ProjectedStoreV2) loadBundleAtUnlocked(base string, chapter int) (*domain.ProjectedChapterBundle, error) {
	if err := validateProjectedChapter(chapter); err != nil {
		return nil, err
	}
	var bundle domain.ProjectedChapterBundle
	if err := s.readJSONUnlocked(projectedBundlePath(base, chapter), &bundle); err != nil {
		return nil, err
	}
	if err := domain.ValidateProjectedChapterBundle(bundle); err != nil {
		return nil, err
	}
	if bundle.Chapter != chapter {
		return nil, fmt.Errorf("bundle path chapter %d contains chapter %d", chapter, bundle.Chapter)
	}
	return &bundle, nil
}

func (s *ProjectedStoreV2) hasExactPromotionReceiptUnlocked(
	generationID string,
	chapter int,
	digest string,
) (bool, error) {
	if err := validateProjectedPathComponent("promotion receipt digest", digest); err != nil {
		return false, err
	}
	var receipt domain.PromotionReceiptV2
	if err := s.readJSONUnlocked(projectedPromotionReceiptPath(generationID, chapter, digest), &receipt); err != nil {
		if os.IsNotExist(rootCause(err)) {
			return false, nil
		}
		return false, err
	}
	if receipt.ReceiptDigest != digest {
		return false, fmt.Errorf("promotion receipt path digest mismatch")
	}
	bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(generationID), chapter)
	if err != nil {
		return false, err
	}
	if err := domain.ValidatePromotionReceiptAgainstBundleV2(receipt, *bundle); err != nil {
		return false, err
	}
	return true, nil
}

func (s *ProjectedStoreV2) hasExactActualOutcomeReceiptUnlocked(
	generationID string,
	chapter int,
	outcomeDigest string,
	promotionDigest string,
) (bool, error) {
	if err := validateProjectedPathComponent("outcome receipt digest", outcomeDigest); err != nil {
		return false, err
	}
	var outcome domain.ActualOutcomeReceiptV2
	if err := s.readJSONUnlocked(projectedActualOutcomePath(generationID, chapter, outcomeDigest), &outcome); err != nil {
		if os.IsNotExist(rootCause(err)) {
			return false, nil
		}
		return false, err
	}
	if outcome.ReceiptDigest != outcomeDigest || outcome.PromotionReceiptDigest != promotionDigest {
		return false, fmt.Errorf("actual outcome receipt binding mismatch")
	}
	var promotion domain.PromotionReceiptV2
	if err := s.readJSONUnlocked(
		projectedPromotionReceiptPath(generationID, chapter, promotionDigest),
		&promotion,
	); err != nil {
		return false, err
	}
	bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(generationID), chapter)
	if err != nil {
		return false, err
	}
	if err := domain.ValidateActualOutcomeAgainstPromotionV2(outcome, promotion, *bundle); err != nil {
		return false, err
	}
	return true, nil
}

// SavePromotionReceipt is the single-writer convenience form of Promote. It
// durably publishes the receipt and advances active_promoted_chapter under the
// same projected-store lock.
func (s *ProjectedStoreV2) SavePromotionReceipt(receipt domain.PromotionReceiptV2) (string, error) {
	cursor, err := s.LoadRealizationCursor()
	if err != nil {
		return "", err
	}
	if cursor == nil {
		return "", fmt.Errorf("promotion requires initialized realization cursor")
	}
	if cursor.ActiveGenerationID == receipt.GenerationID && cursor.ActivePromotedChapter == receipt.Chapter {
		digest, err := domain.ComputePromotionReceiptV2Digest(receipt)
		if err != nil {
			return "", err
		}
		if receipt.ReceiptDigest == "" {
			receipt.ReceiptDigest = digest
		}
		existing, err := s.LoadPromotionReceipt(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest)
		if err == nil && existing != nil && jsonValuesEqual(*existing, receipt) {
			return receipt.ReceiptDigest, nil
		}
	}
	return s.Promote(*cursor, receipt)
}

// Promote makes a promotion receipt authoritative using an expected
// realization cursor. A crash after receipt publication but before cursor
// publication is recovered by replaying this same operation.
func (s *ProjectedStoreV2) Promote(
	expected domain.RealizationCursorV2,
	receipt domain.PromotionReceiptV2,
) (string, error) {
	if err := validateProjectedPathComponent("generation_id", receipt.GenerationID); err != nil {
		return "", err
	}
	if err := validateProjectedChapter(receipt.Chapter); err != nil {
		return "", err
	}
	digest, err := domain.ComputePromotionReceiptV2Digest(receipt)
	if err != nil {
		return "", err
	}
	if receipt.ReceiptDigest == "" {
		receipt.ReceiptDigest = digest
	}
	if receipt.ReceiptDigest != digest {
		return "", fmt.Errorf("promotion receipt digest mismatch: got %s want %s", receipt.ReceiptDigest, digest)
	}
	if err := domain.ValidatePromotionReceiptV2(receipt); err != nil {
		return "", err
	}

	err = s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		current, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return err
		}
		if current == nil || !jsonValuesEqual(*current, expected) {
			return fmt.Errorf("promotion realization cursor compare-and-swap failed")
		}
		if current.ActiveGenerationID != receipt.GenerationID ||
			current.NextPromoteChapter != receipt.Chapter ||
			current.ActivePromotedChapter != 0 {
			return fmt.Errorf("chapter %d is not the next promotable chapter", receipt.Chapter)
		}
		if len(current.BlockedByRewrites) > 0 {
			return fmt.Errorf("promotion blocked by rewrites %v", current.BlockedByRewrites)
		}
		if err := s.validateRealizationControlUnlocked(*current); err != nil {
			return err
		}
		archived, err := s.generationArchivedUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		if archived {
			return fmt.Errorf("generation %s is archived", receipt.GenerationID)
		}
		invalidated, err := s.generationInvalidatedForChapterUnlocked(receipt.GenerationID, receipt.Chapter)
		if err != nil {
			return err
		}
		if invalidated {
			return fmt.Errorf("generation %s chapter %d is invalidated", receipt.GenerationID, receipt.Chapter)
		}
		base := projectedSealedGenerationPath(receipt.GenerationID)
		if _, err := s.validateSealedGenerationUnlocked(receipt.GenerationID); err != nil {
			return err
		}
		bundle, err := s.loadBundleAtUnlocked(base, receipt.Chapter)
		if err != nil {
			return err
		}
		if err := domain.ValidatePromotionReceiptAgainstBundleV2(receipt, *bundle); err != nil {
			return err
		}
		_, err = s.writeContentAddressedJSONUnlocked(
			projectedPromotionReceiptPath(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest),
			receipt,
		)
		if err != nil {
			return err
		}
		next := *current
		next.ActivePromotedChapter = receipt.Chapter
		next.ActivePromotionReceiptDigest = receipt.ReceiptDigest
		next.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		next.CursorDigest = ""
		next.CursorDigest, err = domain.ComputeRealizationCursorV2Digest(next)
		if err != nil {
			return err
		}
		if err := domain.ValidateRealizationCursorV2(next); err != nil {
			return err
		}
		if err := s.validateRealizationTransitionUnlocked(current, next); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
	return receipt.ReceiptDigest, err
}

func (s *ProjectedStoreV2) LoadPromotionReceipt(
	generationID string,
	chapter int,
	digest string,
) (*domain.PromotionReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	if err := validateProjectedChapter(chapter); err != nil {
		return nil, err
	}
	if err := validateProjectedPathComponent("receipt_digest", digest); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.PromotionReceiptV2, error) {
		var receipt domain.PromotionReceiptV2
		if err := s.io.ReadJSONUnlocked(projectedPromotionReceiptPath(generationID, chapter, digest), &receipt); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if err := domain.ValidatePromotionReceiptV2(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || receipt.Chapter != chapter || receipt.ReceiptDigest != digest {
			return nil, fmt.Errorf("promotion receipt path identity mismatch")
		}
		if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
			return nil, err
		}
		bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(generationID), chapter)
		if err != nil {
			return nil, err
		}
		if err := domain.ValidatePromotionReceiptAgainstBundleV2(receipt, *bundle); err != nil {
			return nil, err
		}
		return &receipt, nil
	})
}

// RecordActualOutcomeReceipt durably records an accepted body outcome without
// moving the realization cursor. It is the first step for projection-mismatch
// handling: append suffix invalidation, then CAS the cursor with this receipt
// digest.
func (s *ProjectedStoreV2) RecordActualOutcomeReceipt(
	receipt domain.ActualOutcomeReceiptV2,
) (string, error) {
	if err := validateProjectedPathComponent("generation_id", receipt.GenerationID); err != nil {
		return "", err
	}
	if err := validateProjectedChapter(receipt.Chapter); err != nil {
		return "", err
	}
	if err := validateProjectedPathComponent("promotion_receipt_digest", receipt.PromotionReceiptDigest); err != nil {
		return "", err
	}
	digest, err := domain.ComputeActualOutcomeReceiptV2Digest(receipt)
	if err != nil {
		return "", err
	}
	if receipt.ReceiptDigest == "" {
		receipt.ReceiptDigest = digest
	}
	if receipt.ReceiptDigest != digest {
		return "", fmt.Errorf("actual outcome receipt digest mismatch: got %s want %s", receipt.ReceiptDigest, digest)
	}
	if err := domain.ValidateActualOutcomeReceiptV2(receipt); err != nil {
		return "", err
	}
	err = s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		rel := projectedActualOutcomePath(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest)
		if exists, err := s.contentAddressedJSONExistsUnlocked(rel, receipt); err != nil {
			return err
		} else if exists {
			return nil
		}
		current, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return err
		}
		if current == nil ||
			current.ActiveGenerationID != receipt.GenerationID ||
			current.ActivePromotedChapter != receipt.Chapter ||
			current.ActivePromotionReceiptDigest != receipt.PromotionReceiptDigest {
			return fmt.Errorf("actual outcome does not match active promotion control state")
		}
		var promotion domain.PromotionReceiptV2
		if err := s.readJSONUnlocked(
			projectedPromotionReceiptPath(
				receipt.GenerationID,
				receipt.Chapter,
				receipt.PromotionReceiptDigest,
			),
			&promotion,
		); err != nil {
			return err
		}
		bundle, err := s.loadBundleAtUnlocked(
			projectedSealedGenerationPath(receipt.GenerationID),
			receipt.Chapter,
		)
		if err != nil {
			return err
		}
		if err := domain.ValidateActualOutcomeAgainstPromotionV2(receipt, promotion, *bundle); err != nil {
			return err
		}
		_, err = s.writeContentAddressedJSONUnlocked(rel, receipt)
		return err
	})
	return receipt.ReceiptDigest, err
}

// SaveActualOutcomeReceipt is the single-writer convenience form of
// AcceptOutcome. It publishes the accepted receipt and advances the
// realization cursor under one projected-store lock. A structural mismatch is
// recorded but deliberately leaves the cursor active until suffix invalidation
// is durable.
func (s *ProjectedStoreV2) SaveActualOutcomeReceipt(receipt domain.ActualOutcomeReceiptV2) (string, error) {
	if !receipt.ProjectionMatch {
		return s.RecordActualOutcomeReceipt(receipt)
	}
	cursor, err := s.LoadRealizationCursor()
	if err != nil {
		return "", err
	}
	if cursor == nil {
		return "", fmt.Errorf("actual outcome requires initialized realization cursor")
	}
	if cursor.ActiveGenerationID == receipt.GenerationID &&
		cursor.ActivePromotedChapter == 0 &&
		cursor.LastAcceptedChapter >= receipt.Chapter {
		digest, err := domain.ComputeActualOutcomeReceiptV2Digest(receipt)
		if err != nil {
			return "", err
		}
		if receipt.ReceiptDigest == "" {
			receipt.ReceiptDigest = digest
		}
		existing, err := s.LoadActualOutcomeReceipt(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest)
		if err == nil && existing != nil && jsonValuesEqual(*existing, receipt) {
			return receipt.ReceiptDigest, nil
		}
	}
	return s.AcceptOutcome(*cursor, receipt)
}

// AcceptOutcome makes an accepted actual outcome authoritative using an
// expected realization cursor.
func (s *ProjectedStoreV2) AcceptOutcome(
	expected domain.RealizationCursorV2,
	receipt domain.ActualOutcomeReceiptV2,
) (string, error) {
	if err := validateProjectedPathComponent("generation_id", receipt.GenerationID); err != nil {
		return "", err
	}
	if err := validateProjectedChapter(receipt.Chapter); err != nil {
		return "", err
	}
	if err := validateProjectedPathComponent("promotion_receipt_digest", receipt.PromotionReceiptDigest); err != nil {
		return "", err
	}
	digest, err := domain.ComputeActualOutcomeReceiptV2Digest(receipt)
	if err != nil {
		return "", err
	}
	if receipt.ReceiptDigest == "" {
		receipt.ReceiptDigest = digest
	}
	if receipt.ReceiptDigest != digest {
		return "", fmt.Errorf("actual outcome receipt digest mismatch: got %s want %s", receipt.ReceiptDigest, digest)
	}
	if err := domain.ValidateActualOutcomeReceiptV2(receipt); err != nil {
		return "", err
	}
	if !receipt.ProjectionMatch {
		return "", fmt.Errorf("projection mismatch requires suffix invalidation transaction before realization can advance")
	}

	err = s.withProjectedWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		current, err := s.loadRealizationCursorUnlocked()
		if err != nil {
			return err
		}
		if current == nil || !jsonValuesEqual(*current, expected) {
			return fmt.Errorf("actual outcome realization cursor compare-and-swap failed")
		}
		if current.ActiveGenerationID != receipt.GenerationID ||
			current.ActivePromotedChapter != receipt.Chapter {
			return fmt.Errorf("chapter %d is not the active promoted chapter", receipt.Chapter)
		}
		if err := s.validateRealizationControlUnlocked(*current); err != nil {
			return err
		}
		var promotion domain.PromotionReceiptV2
		promotionPath := projectedPromotionReceiptPath(
			receipt.GenerationID,
			receipt.Chapter,
			receipt.PromotionReceiptDigest,
		)
		if err := s.readJSONUnlocked(promotionPath, &promotion); err != nil {
			return fmt.Errorf("actual outcome requires promotion receipt %s: %w", receipt.PromotionReceiptDigest, err)
		}
		if err := domain.ValidatePromotionReceiptV2(promotion); err != nil {
			return err
		}
		if promotion.GenerationID != receipt.GenerationID ||
			promotion.Chapter != receipt.Chapter ||
			promotion.ReceiptDigest != receipt.PromotionReceiptDigest {
			return fmt.Errorf("actual outcome promotion identity mismatch")
		}
		base := projectedSealedGenerationPath(receipt.GenerationID)
		if _, err := s.validateSealedGenerationUnlocked(receipt.GenerationID); err != nil {
			return err
		}
		bundle, err := s.loadBundleAtUnlocked(base, receipt.Chapter)
		if err != nil {
			return err
		}
		if err := domain.ValidatePromotionReceiptAgainstBundleV2(promotion, *bundle); err != nil {
			return err
		}
		if receipt.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
			return fmt.Errorf("actual outcome projected_post_state_root does not match sealed bundle")
		}
		_, err = s.writeContentAddressedJSONUnlocked(
			projectedActualOutcomePath(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest),
			receipt,
		)
		if err != nil {
			return err
		}
		next := *current
		next.ActivePromotedChapter = 0
		next.ActivePromotionReceiptDigest = ""
		next.LastAcceptedChapter = receipt.Chapter
		next.LastOutcomeReceiptDigest = receipt.ReceiptDigest
		next.NextPromoteChapter = receipt.Chapter + 1
		next.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		next.CursorDigest = ""
		next.CursorDigest, err = domain.ComputeRealizationCursorV2Digest(next)
		if err != nil {
			return err
		}
		if err := domain.ValidateRealizationCursorV2(next); err != nil {
			return err
		}
		if err := s.validateRealizationTransitionUnlocked(current, next); err != nil {
			return err
		}
		if err := s.io.WriteJSONUnlocked(projectedRealizationCursorPath, next); err != nil {
			return err
		}
		return syncProjectedDirs(s.io.path(projectedPlanningV2Root))
	})
	return receipt.ReceiptDigest, err
}

func (s *ProjectedStoreV2) LoadActualOutcomeReceipt(
	generationID string,
	chapter int,
	digest string,
) (*domain.ActualOutcomeReceiptV2, error) {
	if err := validateProjectedPathComponent("generation_id", generationID); err != nil {
		return nil, err
	}
	if err := validateProjectedChapter(chapter); err != nil {
		return nil, err
	}
	if err := validateProjectedPathComponent("receipt_digest", digest); err != nil {
		return nil, err
	}
	return withProjectedReadResult(s, func() (*domain.ActualOutcomeReceiptV2, error) {
		var receipt domain.ActualOutcomeReceiptV2
		if err := s.io.ReadJSONUnlocked(projectedActualOutcomePath(generationID, chapter, digest), &receipt); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if err := domain.ValidateActualOutcomeReceiptV2(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || receipt.Chapter != chapter || receipt.ReceiptDigest != digest {
			return nil, fmt.Errorf("actual outcome receipt path identity mismatch")
		}
		if _, err := s.validateSealedGenerationUnlocked(generationID); err != nil {
			return nil, err
		}
		bundle, err := s.loadBundleAtUnlocked(projectedSealedGenerationPath(generationID), chapter)
		if err != nil {
			return nil, err
		}
		if receipt.ProjectedPostStateRoot != bundle.ProjectedPostStateRoot {
			return nil, fmt.Errorf("actual outcome projected root differs from sealed bundle")
		}
		var promotion domain.PromotionReceiptV2
		if err := s.readJSONUnlocked(
			projectedPromotionReceiptPath(generationID, chapter, receipt.PromotionReceiptDigest),
			&promotion,
		); err != nil {
			return nil, err
		}
		if promotion.ReceiptDigest != receipt.PromotionReceiptDigest {
			return nil, fmt.Errorf("actual outcome promotion receipt digest mismatch")
		}
		if err := domain.ValidatePromotionReceiptAgainstBundleV2(promotion, *bundle); err != nil {
			return nil, err
		}
		return &receipt, nil
	})
}
