package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

const (
	arcCycleRoot          = "meta/planning/v3/arc_cycle"
	arcCycleManifestDir   = "meta/planning/v3/arc_cycle/manifests"
	arcCycleAcceptanceDir = "meta/planning/v3/arc_cycle/acceptances"
	arcCycleCompletionDir = "meta/planning/v3/arc_cycle/completions"
	arcCycleWriteLockFile = "meta/planning/v3/arc_cycle/.write.lock"
)

var (
	arcCycleGenerationPattern = regexp.MustCompile(`^pg2_[A-Za-z0-9._:-]+$`)
	arcCycleDigestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ArcCycleStore owns only immutable arc planning/acceptance/completion
// evidence. It never writes chapter prose or projected generation state.
type ArcCycleStore struct {
	io *IO
}

func NewArcCycleStore(io *IO) *ArcCycleStore {
	return &ArcCycleStore{io: io}
}

// ArcCycle exposes the isolated arc-cycle receipt store without changing the
// Store composition root while this protocol is introduced incrementally.
func (s *Store) ArcCycle() *ArcCycleStore {
	return NewArcCycleStore(newIO(s.dir))
}

// ArcCycles is a plural alias for call sites that treat the store as a receipt
// collection.
func (s *Store) ArcCycles() *ArcCycleStore {
	return s.ArcCycle()
}

func arcCycleManifestPath(generationID, digest string) string {
	return filepath.Join(arcCycleManifestDir, generationID, digest+".json")
}

func arcCycleAcceptancePath(generationID string, chapter int, digest string) string {
	return filepath.Join(arcCycleAcceptanceDir, generationID, fmt.Sprintf("%06d", chapter), digest+".json")
}

func arcCycleCompletionPath(generationID, digest string) string {
	return filepath.Join(arcCycleCompletionDir, generationID, digest+".json")
}

func validateArcCycleGenerationID(generationID string) error {
	if !arcCycleGenerationPattern.MatchString(generationID) || strings.TrimSpace(generationID) != generationID {
		return fmt.Errorf("generation_id %q is not a safe planning generation id", generationID)
	}
	return nil
}

func validateArcCycleDigest(name, digest string) error {
	if !arcCycleDigestPattern.MatchString(digest) {
		return fmt.Errorf("%s must use sha256:<64 lowercase hex> form", name)
	}
	return nil
}

func (s *ArcCycleStore) ensureBaseDirsUnlocked() error {
	for _, rel := range []string{arcCycleManifestDir, arcCycleAcceptanceDir, arcCycleCompletionDir} {
		if err := os.MkdirAll(s.io.path(rel), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *ArcCycleStore) withWriteLock(fn func() error) error {
	return s.io.WithWriteLock(func() error {
		if err := os.MkdirAll(s.io.path(arcCycleRoot), 0o755); err != nil {
			return err
		}
		lock, err := os.OpenFile(s.io.path(arcCycleWriteLockFile), os.O_CREATE|os.O_RDWR, 0o644)
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

func (s *ArcCycleStore) withReadLock(fn func() error) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	if _, err := os.Lstat(s.io.path(arcCycleRoot)); os.IsNotExist(err) {
		return fn()
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(s.io.path(arcCycleWriteLockFile), os.O_CREATE|os.O_RDWR, 0o644)
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

func withArcCycleReadResult[T any](s *ArcCycleStore, fn func() (T, error)) (T, error) {
	var result T
	err := s.withReadLock(func() error {
		var err error
		result, err = fn()
		return err
	})
	return result, err
}

func arcCycleJSON(value any) ([]byte, error) {
	return json.MarshalIndent(value, "", "  ")
}

func arcCycleSameJSON(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return bytes.Equal(left, right)
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func (s *ArcCycleStore) readJSONStrictUnlocked(rel string, value any) error {
	raw, err := s.io.ReadFileUnlocked(rel)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s contains multiple JSON values", rel)
		}
		return err
	}
	return nil
}

func (s *ArcCycleStore) writeFileNoReplaceUnlocked(rel string, raw []byte) error {
	finalPath := s.io.path(rel)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(finalPath), "."+filepath.Base(finalPath)+".tmp-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Hard-link publication is atomic and, unlike rename, cannot replace an
	// existing immutable content address.
	if err := os.Link(tempPath, finalPath); err != nil {
		return err
	}
	return syncArcCycleDirs(filepath.Dir(finalPath))
}

func syncArcCycleDirs(paths ...string) error {
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

func (s *ArcCycleStore) writeContentAddressedJSONUnlocked(rel string, value any) error {
	raw, err := arcCycleJSON(value)
	if err != nil {
		return err
	}
	info, statErr := os.Lstat(s.io.path(rel))
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("content-addressed path %s is not a regular file", rel)
		}
		existing, err := s.io.ReadFileUnlocked(rel)
		if err != nil {
			return err
		}
		if arcCycleSameJSON(existing, raw) {
			return nil
		}
		return fmt.Errorf("content-addressed artifact %s already exists with different content", rel)
	}
	if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := s.writeFileNoReplaceUnlocked(rel, raw); err != nil {
		if os.IsExist(err) {
			existing, readErr := s.io.ReadFileUnlocked(rel)
			if readErr == nil && arcCycleSameJSON(existing, raw) {
				return nil
			}
		}
		return err
	}
	return nil
}

func validateArcCycleRegularJSONEntry(entry os.DirEntry, dir string) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
		return fmt.Errorf("invalid arc-cycle entry %s", filepath.Join(dir, entry.Name()))
	}
	return nil
}

func (s *ArcCycleStore) listManifestsUnlocked(generationID string) ([]domain.ArcPlanningManifest, error) {
	dir := filepath.Join(arcCycleManifestDir, generationID)
	entries, err := os.ReadDir(s.io.path(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.ArcPlanningManifest, 0, len(entries))
	for _, entry := range entries {
		if err := validateArcCycleRegularJSONEntry(entry, dir); err != nil {
			return nil, err
		}
		var manifest domain.ArcPlanningManifest
		rel := filepath.Join(dir, entry.Name())
		if err := s.readJSONStrictUnlocked(rel, &manifest); err != nil {
			return nil, fmt.Errorf("read arc planning manifest %s: %w", rel, err)
		}
		if err := domain.ValidateArcPlanningManifest(manifest); err != nil {
			return nil, err
		}
		if manifest.GenerationID != generationID || entry.Name() != manifest.ManifestDigest+".json" {
			return nil, fmt.Errorf("arc planning manifest path identity mismatch at %s", rel)
		}
		out = append(out, manifest)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ManifestDigest < out[j].ManifestDigest })
	return out, nil
}

func (s *ArcCycleStore) listAcceptancesUnlocked(generationID string) ([]domain.ChapterAcceptanceReceipt, error) {
	generationDir := filepath.Join(arcCycleAcceptanceDir, generationID)
	chapterDirs, err := os.ReadDir(s.io.path(generationDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.ChapterAcceptanceReceipt, 0, len(chapterDirs))
	seenChapter := make(map[int]struct{}, len(chapterDirs))
	for _, chapterDir := range chapterDirs {
		info, err := chapterDir.Info()
		if err != nil {
			return nil, err
		}
		if chapterDir.Type()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("invalid arc acceptance chapter entry %s", chapterDir.Name())
		}
		chapter, err := strconv.Atoi(chapterDir.Name())
		if err != nil || chapter <= 0 || chapterDir.Name() != fmt.Sprintf("%06d", chapter) {
			return nil, fmt.Errorf("invalid arc acceptance chapter directory %s", chapterDir.Name())
		}
		dir := filepath.Join(generationDir, chapterDir.Name())
		entries, err := os.ReadDir(s.io.path(dir))
		if err != nil {
			return nil, err
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("chapter %d must have exactly one immutable acceptance receipt, got %d", chapter, len(entries))
		}
		entry := entries[0]
		if err := validateArcCycleRegularJSONEntry(entry, dir); err != nil {
			return nil, err
		}
		var receipt domain.ChapterAcceptanceReceipt
		rel := filepath.Join(dir, entry.Name())
		if err := s.readJSONStrictUnlocked(rel, &receipt); err != nil {
			return nil, fmt.Errorf("read chapter acceptance receipt %s: %w", rel, err)
		}
		if err := domain.ValidateChapterAcceptanceReceipt(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || receipt.Chapter != chapter || entry.Name() != receipt.ReceiptDigest+".json" {
			return nil, fmt.Errorf("chapter acceptance receipt path identity mismatch at %s", rel)
		}
		if _, duplicate := seenChapter[chapter]; duplicate {
			return nil, fmt.Errorf("duplicate chapter acceptance identity for chapter %d", chapter)
		}
		seenChapter[chapter] = struct{}{}
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Chapter < out[j].Chapter })
	return out, nil
}

func (s *ArcCycleStore) listCompletionsUnlocked(generationID string) ([]domain.ArcCompletionReceipt, error) {
	dir := filepath.Join(arcCycleCompletionDir, generationID)
	entries, err := os.ReadDir(s.io.path(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.ArcCompletionReceipt, 0, len(entries))
	for _, entry := range entries {
		if err := validateArcCycleRegularJSONEntry(entry, dir); err != nil {
			return nil, err
		}
		var receipt domain.ArcCompletionReceipt
		rel := filepath.Join(dir, entry.Name())
		if err := s.readJSONStrictUnlocked(rel, &receipt); err != nil {
			return nil, fmt.Errorf("read arc completion receipt %s: %w", rel, err)
		}
		if err := domain.ValidateArcCompletionReceipt(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || entry.Name() != receipt.ReceiptDigest+".json" {
			return nil, fmt.Errorf("arc completion receipt path identity mismatch at %s", rel)
		}
		out = append(out, receipt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceiptDigest < out[j].ReceiptDigest })
	return out, nil
}

func exactlyOneArcManifest(manifests []domain.ArcPlanningManifest, generationID string) (*domain.ArcPlanningManifest, error) {
	if len(manifests) == 0 {
		return nil, fmt.Errorf("generation %s has no arc planning manifest", generationID)
	}
	if len(manifests) != 1 {
		return nil, fmt.Errorf("generation %s has %d arc planning manifests; immutable identity collision", generationID, len(manifests))
	}
	manifest := manifests[0]
	return &manifest, nil
}

func (s *ArcCycleStore) validateAcceptedBodyUnlocked(
	receipt domain.ChapterAcceptanceReceipt,
	manifest domain.ArcPlanningManifest,
) error {
	rel := filepath.Join("chapters", fmt.Sprintf("%02d.md", receipt.Chapter))
	body, err := readArcCycleSealedEvidenceFile(s.io.dir, rel)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("chapter %d accepted body is missing", receipt.Chapter)
		}
		return err
	}
	want := domain.ComputeArcChapterBodySHA256(body)
	if receipt.ChapterBodySHA256 != want {
		return fmt.Errorf("chapter %d body hash drift: accepted=%s current=%s", receipt.Chapter, receipt.ChapterBodySHA256, want)
	}
	if !utf8.Valid(body) {
		return fmt.Errorf("chapter %d accepted body is not valid UTF-8", receipt.Chapter)
	}
	actualRunes := utf8.RuneCount(body)
	if receipt.ChapterBodyRunes != actualRunes {
		return fmt.Errorf(
			"chapter %d body rune count drift: accepted=%d current=%d",
			receipt.Chapter,
			receipt.ChapterBodyRunes,
			actualRunes,
		)
	}
	if err := domain.ValidateAcceptedChapterBodyRunes(receipt.Chapter, actualRunes, manifest.ChapterBodyRunes); err != nil {
		return err
	}
	return nil
}

func (s *ArcCycleStore) validateReviewArtifactsUnlocked(
	receipt domain.ChapterAcceptanceReceipt,
	manifest domain.ArcPlanningManifest,
) error {
	for _, artifact := range receipt.ReviewArtifacts {
		if err := domain.ValidateChapterReviewArtifactPath(artifact.Path, receipt.Chapter); err != nil {
			return err
		}
		raw, err := readArcCycleSealedEvidenceFile(s.io.dir, artifact.Path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("chapter %d review artifact %s is missing", receipt.Chapter, artifact.Path)
			}
			return err
		}
		want := domain.ComputeArcArtifactSHA256(raw)
		if artifact.Digest != want {
			return fmt.Errorf(
				"chapter %d review artifact hash drift at %s: accepted=%s current=%s",
				receipt.Chapter,
				artifact.Path,
				artifact.Digest,
				want,
			)
		}
	}
	if receipt.EffectiveStyleReceiptPath != "" {
		raw, err := readArcCycleSealedEvidenceFile(s.io.dir, receipt.EffectiveStyleReceiptPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("chapter %d effective style receipt archive is missing: %w", receipt.Chapter, err)
			}
			return fmt.Errorf("chapter %d effective style receipt archive path is invalid: %w", receipt.Chapter, err)
		}
		if got := domain.ComputeArcArtifactSHA256(raw); got != receipt.EffectiveStyleArtifactSHA256 {
			return fmt.Errorf(
				"chapter %d effective style receipt archive hash drift: accepted=%s current=%s",
				receipt.Chapter,
				receipt.EffectiveStyleArtifactSHA256,
				got,
			)
		}
		styleReceipt, err := validateArchivedRenderPermitEffectiveStyleReceipt(
			raw,
			receipt.Chapter,
			receipt.EffectiveStyleReceiptDigest,
			receipt.EffectiveStyleReceiptPath,
		)
		if err != nil {
			return err
		}
		if styleReceipt.GenerationID != receipt.GenerationID {
			return fmt.Errorf(
				"chapter %d effective style receipt belongs to generation %s, want %s",
				receipt.Chapter,
				styleReceipt.GenerationID,
				receipt.GenerationID,
			)
		}
		bundleDigest := ""
		for _, binding := range manifest.Chapters {
			if binding.Chapter == receipt.Chapter {
				bundleDigest = binding.BundleDigest
				break
			}
		}
		if bundleDigest == "" || styleReceipt.ProjectedBundleDigest != bundleDigest {
			return fmt.Errorf(
				"chapter %d effective style receipt does not bind the arc manifest bundle",
				receipt.Chapter,
			)
		}
		for _, source := range styleReceipt.SourceChapterBodies {
			bodyPath := fmt.Sprintf("chapters/%02d.md", source.Chapter)
			body, err := readArcCycleSealedEvidenceFile(s.io.dir, bodyPath)
			if err != nil || renderPermitEffectiveStyleSHA256(body) != source.BodySHA256 {
				return fmt.Errorf("chapter %d effective style source chapter %d drift", receipt.Chapter, source.Chapter)
			}
		}
	}
	return nil
}

// readArcCycleSealedEvidenceFile reads path-bound immutable evidence without
// following symlinks and rejects alternate hard-link names. Arc-cycle receipt
// publication deliberately uses hard links and therefore does not use this
// helper; it is reserved for chapter, review, and style evidence.
func readArcCycleSealedEvidenceFile(root string, rel string) ([]byte, error) {
	path, before, err := validateArcCycleSealedEvidenceFilesystemPath(root, rel)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateArcCycleSealedEvidenceFileInfo(opened); err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("sealed evidence path changed before open")
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	afterPath, after, err := validateArcCycleSealedEvidenceFilesystemPath(root, rel)
	if err != nil {
		return nil, err
	}
	if afterPath != path || !os.SameFile(opened, after) {
		return nil, fmt.Errorf("sealed evidence path changed while reading")
	}
	openedAfter, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := validateArcCycleSealedEvidenceFileInfo(openedAfter); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateArcCycleSealedEvidenceFilesystemPath(root string, rel string) (string, os.FileInfo, error) {
	root = filepath.Clean(root)
	cleanRel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if !filepath.IsAbs(root) || cleanRel == "." || filepath.IsAbs(cleanRel) || cleanRel == ".." ||
		strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("sealed evidence path is unsafe")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", nil, fmt.Errorf("sealed evidence project root must be a real directory")
	}
	cursor := root
	components := strings.Split(filepath.ToSlash(cleanRel), "/")
	var leafInfo os.FileInfo
	for index, component := range components {
		if component == "" || component == "." {
			continue
		}
		cursor = filepath.Join(cursor, component)
		info, err := os.Lstat(cursor)
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("sealed evidence path contains a symlink")
		}
		if index == len(components)-1 {
			if err := validateArcCycleSealedEvidenceFileInfo(info); err != nil {
				return "", nil, err
			}
			leafInfo = info
		} else if !info.IsDir() {
			return "", nil, fmt.Errorf("sealed evidence parent is not a directory")
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, err
	}
	resolvedPath, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", nil, err
	}
	within, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("sealed evidence resolves outside the project root")
	}
	return cursor, leafInfo, nil
}

func validateArcCycleSealedEvidenceFileInfo(info os.FileInfo) error {
	if info == nil || !info.Mode().IsRegular() {
		return fmt.Errorf("sealed evidence is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("sealed evidence link count is unavailable")
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("sealed evidence must have exactly one hard link")
	}
	return nil
}

func (s *ArcCycleStore) SaveArcPlanningManifest(manifest domain.ArcPlanningManifest) (string, error) {
	if manifest.ManifestDigest == "" {
		var err error
		manifest, err = domain.SignArcPlanningManifest(manifest)
		if err != nil {
			return "", err
		}
	}
	if err := domain.ValidateArcPlanningManifest(manifest); err != nil {
		return "", err
	}
	if err := validateArcCycleGenerationID(manifest.GenerationID); err != nil {
		return "", err
	}
	if err := validateArcCycleDigest("manifest_digest", manifest.ManifestDigest); err != nil {
		return "", err
	}
	err := s.withWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		existing, err := s.listManifestsUnlocked(manifest.GenerationID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			if len(existing) == 1 && existing[0].ManifestDigest == manifest.ManifestDigest {
				return s.writeContentAddressedJSONUnlocked(arcCycleManifestPath(manifest.GenerationID, manifest.ManifestDigest), manifest)
			}
			return fmt.Errorf("arc planning manifest for generation %s already exists with different content", manifest.GenerationID)
		}
		return s.writeContentAddressedJSONUnlocked(arcCycleManifestPath(manifest.GenerationID, manifest.ManifestDigest), manifest)
	})
	return manifest.ManifestDigest, err
}

func (s *ArcCycleStore) SaveChapterAcceptanceReceipt(receipt domain.ChapterAcceptanceReceipt) (string, error) {
	if receipt.ReceiptDigest == "" {
		var err error
		receipt, err = domain.SignChapterAcceptanceReceipt(receipt)
		if err != nil {
			return "", err
		}
	}
	if err := domain.ValidateChapterAcceptanceReceipt(receipt); err != nil {
		return "", err
	}
	if err := validateArcCycleGenerationID(receipt.GenerationID); err != nil {
		return "", err
	}
	if err := validateArcCycleDigest("receipt_digest", receipt.ReceiptDigest); err != nil {
		return "", err
	}
	err := s.withWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		manifests, err := s.listManifestsUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		manifest, err := exactlyOneArcManifest(manifests, receipt.GenerationID)
		if err != nil {
			return err
		}
		if err := domain.ValidateChapterAcceptanceReceiptAgainstManifest(receipt, *manifest); err != nil {
			return err
		}
		existing, err := s.listAcceptancesUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		if err := s.validateAcceptedBodyUnlocked(receipt, *manifest); err != nil {
			return err
		}
		if err := s.validateReviewArtifactsUnlocked(receipt, *manifest); err != nil {
			return err
		}
		for _, saved := range existing {
			if saved.Chapter != receipt.Chapter {
				continue
			}
			if saved.ReceiptDigest != receipt.ReceiptDigest {
				return fmt.Errorf("chapter %d acceptance already exists with different content; body hash drift or review replacement is forbidden", receipt.Chapter)
			}
			return s.writeContentAddressedJSONUnlocked(arcCycleAcceptancePath(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest), receipt)
		}
		expectedChapter := manifest.FirstChapter + len(existing)
		if receipt.Chapter != expectedChapter {
			return fmt.Errorf("chapter acceptance is out of order: got chapter %d want %d", receipt.Chapter, expectedChapter)
		}
		return s.writeContentAddressedJSONUnlocked(arcCycleAcceptancePath(receipt.GenerationID, receipt.Chapter, receipt.ReceiptDigest), receipt)
	})
	return receipt.ReceiptDigest, err
}

func (s *ArcCycleStore) SaveArcCompletionReceipt(receipt domain.ArcCompletionReceipt) (string, error) {
	if receipt.ReceiptDigest == "" {
		var err error
		receipt, err = domain.SignArcCompletionReceipt(receipt)
		if err != nil {
			return "", err
		}
	}
	if err := domain.ValidateArcCompletionReceipt(receipt); err != nil {
		return "", err
	}
	if err := validateArcCycleGenerationID(receipt.GenerationID); err != nil {
		return "", err
	}
	if err := validateArcCycleDigest("receipt_digest", receipt.ReceiptDigest); err != nil {
		return "", err
	}
	err := s.withWriteLock(func() error {
		if err := s.ensureBaseDirsUnlocked(); err != nil {
			return err
		}
		manifests, err := s.listManifestsUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		manifest, err := exactlyOneArcManifest(manifests, receipt.GenerationID)
		if err != nil {
			return err
		}
		acceptances, err := s.listAcceptancesUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		if err := domain.ValidateArcCompletionReceiptAgainstManifest(receipt, *manifest, acceptances); err != nil {
			return err
		}
		for _, acceptance := range acceptances {
			if err := s.validateAcceptedBodyUnlocked(acceptance, *manifest); err != nil {
				return err
			}
			if err := s.validateReviewArtifactsUnlocked(acceptance, *manifest); err != nil {
				return err
			}
		}
		existing, err := s.listCompletionsUnlocked(receipt.GenerationID)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			if len(existing) == 1 && existing[0].ReceiptDigest == receipt.ReceiptDigest {
				return s.writeContentAddressedJSONUnlocked(arcCycleCompletionPath(receipt.GenerationID, receipt.ReceiptDigest), receipt)
			}
			return fmt.Errorf("arc completion receipt for generation %s already exists with different content", receipt.GenerationID)
		}
		return s.writeContentAddressedJSONUnlocked(arcCycleCompletionPath(receipt.GenerationID, receipt.ReceiptDigest), receipt)
	})
	return receipt.ReceiptDigest, err
}

func (s *ArcCycleStore) LoadArcPlanningManifest(generationID, digest string) (*domain.ArcPlanningManifest, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	if err := validateArcCycleDigest("manifest_digest", digest); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() (*domain.ArcPlanningManifest, error) {
		var manifest domain.ArcPlanningManifest
		rel := arcCycleManifestPath(generationID, digest)
		if err := s.readJSONStrictUnlocked(rel, &manifest); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if err := domain.ValidateArcPlanningManifest(manifest); err != nil {
			return nil, err
		}
		if manifest.GenerationID != generationID || manifest.ManifestDigest != digest {
			return nil, fmt.Errorf("arc planning manifest path identity mismatch")
		}
		return &manifest, nil
	})
}

func (s *ArcCycleStore) ListArcPlanningManifests(generationID string) ([]domain.ArcPlanningManifest, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() ([]domain.ArcPlanningManifest, error) {
		return s.listManifestsUnlocked(generationID)
	})
}

func (s *ArcCycleStore) LoadChapterAcceptanceReceipt(generationID string, chapter int, digest string) (*domain.ChapterAcceptanceReceipt, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	if chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	if err := validateArcCycleDigest("receipt_digest", digest); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() (*domain.ChapterAcceptanceReceipt, error) {
		var receipt domain.ChapterAcceptanceReceipt
		rel := arcCycleAcceptancePath(generationID, chapter, digest)
		if err := s.readJSONStrictUnlocked(rel, &receipt); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if err := domain.ValidateChapterAcceptanceReceipt(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || receipt.Chapter != chapter || receipt.ReceiptDigest != digest {
			return nil, fmt.Errorf("chapter acceptance receipt path identity mismatch")
		}
		return &receipt, nil
	})
}

func (s *ArcCycleStore) ListChapterAcceptanceReceipts(generationID string) ([]domain.ChapterAcceptanceReceipt, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() ([]domain.ChapterAcceptanceReceipt, error) {
		return s.listAcceptancesUnlocked(generationID)
	})
}

func (s *ArcCycleStore) LoadArcCompletionReceipt(generationID, digest string) (*domain.ArcCompletionReceipt, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	if err := validateArcCycleDigest("receipt_digest", digest); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() (*domain.ArcCompletionReceipt, error) {
		var receipt domain.ArcCompletionReceipt
		rel := arcCycleCompletionPath(generationID, digest)
		if err := s.readJSONStrictUnlocked(rel, &receipt); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, err
		}
		if err := domain.ValidateArcCompletionReceipt(receipt); err != nil {
			return nil, err
		}
		if receipt.GenerationID != generationID || receipt.ReceiptDigest != digest {
			return nil, fmt.Errorf("arc completion receipt path identity mismatch")
		}
		return &receipt, nil
	})
}

func (s *ArcCycleStore) ListArcCompletionReceipts(generationID string) ([]domain.ArcCompletionReceipt, error) {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return nil, err
	}
	return withArcCycleReadResult(s, func() ([]domain.ArcCompletionReceipt, error) {
		return s.listCompletionsUnlocked(generationID)
	})
}

// RequireChapterReviewArtifactsMutable fails closed when any immutable arc
// acceptance receipt already binds this chapter's formal review files. It is
// intended for standalone review commands, which must never overwrite
// content-addressed evidence after chapter acceptance.
func (s *ArcCycleStore) RequireChapterReviewArtifactsMutable(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	return s.withReadLock(func() error {
		root := arcCycleAcceptanceDir
		generationEntries, err := os.ReadDir(s.io.path(root))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, generationEntry := range generationEntries {
			info, err := generationEntry.Info()
			if err != nil {
				return err
			}
			generationID := generationEntry.Name()
			if generationEntry.Type()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("invalid arc acceptance generation entry %s", generationID)
			}
			if err := validateArcCycleGenerationID(generationID); err != nil {
				return err
			}
			acceptances, err := s.listAcceptancesUnlocked(generationID)
			if err != nil {
				return err
			}
			for _, acceptance := range acceptances {
				if acceptance.Chapter == chapter {
					return fmt.Errorf(
						"chapter %d review artifacts are sealed by immutable acceptance %s in generation %s; standalone review overwrite is forbidden",
						chapter,
						acceptance.ReceiptDigest,
						generationID,
					)
				}
			}
		}
		return nil
	})
}

// ValidateArcCycle replays all immutable relationships and re-hashes every
// currently published chapter body. Partial acceptance prefixes are valid
// while an arc is in progress; the presence of a completion receipt requires
// exact full-range coverage.
func (s *ArcCycleStore) ValidateArcCycle(generationID string) error {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return err
	}
	return s.withReadLock(func() error {
		manifests, err := s.listManifestsUnlocked(generationID)
		if err != nil {
			return err
		}
		manifest, err := exactlyOneArcManifest(manifests, generationID)
		if err != nil {
			return err
		}
		acceptances, err := s.listAcceptancesUnlocked(generationID)
		if err != nil {
			return err
		}
		for i, acceptance := range acceptances {
			wantChapter := manifest.FirstChapter + i
			if acceptance.Chapter != wantChapter {
				return fmt.Errorf("chapter acceptances are out of order: got %d want %d", acceptance.Chapter, wantChapter)
			}
			if err := domain.ValidateChapterAcceptanceReceiptAgainstManifest(acceptance, *manifest); err != nil {
				return err
			}
			if err := s.validateAcceptedBodyUnlocked(acceptance, *manifest); err != nil {
				return err
			}
			if err := s.validateReviewArtifactsUnlocked(acceptance, *manifest); err != nil {
				return err
			}
		}
		completions, err := s.listCompletionsUnlocked(generationID)
		if err != nil {
			return err
		}
		if len(completions) > 1 {
			return fmt.Errorf("generation %s has multiple arc completion receipts", generationID)
		}
		if len(completions) == 1 {
			return domain.ValidateArcCompletionReceiptAgainstManifest(completions[0], *manifest, acceptances)
		}
		return nil
	})
}

// ValidateArcCompletion validates one requested content address and its full
// stored proof chain. It is useful for a next-arc unlock guard.
func (s *ArcCycleStore) ValidateArcCompletion(generationID, digest string) error {
	if err := validateArcCycleGenerationID(generationID); err != nil {
		return err
	}
	if err := validateArcCycleDigest("receipt_digest", digest); err != nil {
		return err
	}
	return s.withReadLock(func() error {
		manifests, err := s.listManifestsUnlocked(generationID)
		if err != nil {
			return err
		}
		manifest, err := exactlyOneArcManifest(manifests, generationID)
		if err != nil {
			return err
		}
		acceptances, err := s.listAcceptancesUnlocked(generationID)
		if err != nil {
			return err
		}
		var receipt domain.ArcCompletionReceipt
		rel := arcCycleCompletionPath(generationID, digest)
		if err := s.readJSONStrictUnlocked(rel, &receipt); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("arc completion receipt %s is missing", digest)
			}
			return err
		}
		if receipt.ReceiptDigest != digest || receipt.GenerationID != generationID {
			return fmt.Errorf("arc completion receipt path identity mismatch")
		}
		for _, acceptance := range acceptances {
			if err := s.validateAcceptedBodyUnlocked(acceptance, *manifest); err != nil {
				return err
			}
			if err := s.validateReviewArtifactsUnlocked(acceptance, *manifest); err != nil {
				return err
			}
		}
		return domain.ValidateArcCompletionReceiptAgainstManifest(receipt, *manifest, acceptances)
	})
}
