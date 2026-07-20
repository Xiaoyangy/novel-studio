package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const (
	pipelineOutlineAllRunDirName           = ".outline-all"
	pipelineOutlineAllPublishDirName       = ".outline-all-publish"
	pipelineOutlineAllRequirementPath      = "meta/planning/outline_all_requirement.json"
	pipelineOutlineAllControlDirName       = ".outline-all-control"
	pipelineOutlineAllRunRequirementName   = "current.json"
	pipelineOutlineAllCandidatePreparePath = "meta/planning/outline_all_candidate_prepare.json"
)

type pipelineOutlineAllCandidatePrepareReceipt struct {
	Version       string `json:"version"`
	AttemptID     string `json:"attempt_id"`
	LiveDir       string `json:"live_dir"`
	CandidateDir  string `json:"candidate_dir"`
	PreparedAt    string `json:"prepared_at"`
	ReceiptDigest string `json:"receipt_digest"`
}

type pipelineOutlineAllRequirement struct {
	Version           string `json:"version"`
	Required          bool   `json:"required"`
	RequirementDigest string `json:"requirement_digest"`
}

func pipelineOutlineAllControlRoot(outputDir string) string {
	return filepath.Join(pipelineOutlineAllRunRoot(outputDir), pipelineOutlineAllControlDirName)
}

type pipelineOutlineAllHeldControl struct {
	file      *os.File
	exclusive bool
	refs      int
}

var (
	pipelineOutlineAllControlMu    sync.Mutex
	pipelineOutlineAllHeldControls = map[string]*pipelineOutlineAllHeldControl{}
)

// acquirePipelineOutlineAllControl returns a process-held flock release
// callback. Production mutation/planning/render paths use EX for their entire
// invocation. The shared option remains only for contention tests and must not
// be used as a live-directory publication capability on Darwin.
func acquirePipelineOutlineAllControl(outputDir string, exclusive bool) (func() error, error) {
	root := pipelineOutlineAllControlRoot(outputDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, "control.lock")
	pipelineOutlineAllControlMu.Lock()
	if held := pipelineOutlineAllHeldControls[lockPath]; held != nil {
		if exclusive && !held.exclusive {
			pipelineOutlineAllControlMu.Unlock()
			return nil, fmt.Errorf("outline-all control is already held shared by this process")
		}
		held.refs++
		pipelineOutlineAllControlMu.Unlock()
		var once sync.Once
		return func() error {
			var releaseErr error
			once.Do(func() { releaseErr = releasePipelineOutlineAllHeldControl(lockPath) })
			return releaseErr
		}, nil
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		pipelineOutlineAllControlMu.Unlock()
		return nil, err
	}
	operation := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		operation = syscall.LOCK_EX | syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		_ = file.Close()
		pipelineOutlineAllControlMu.Unlock()
		return nil, fmt.Errorf("outline-all control is busy: %w", err)
	}
	pipelineOutlineAllHeldControls[lockPath] = &pipelineOutlineAllHeldControl{file: file, exclusive: exclusive, refs: 1}
	pipelineOutlineAllControlMu.Unlock()
	var once sync.Once
	return func() error {
		var releaseErr error
		once.Do(func() {
			releaseErr = releasePipelineOutlineAllHeldControl(lockPath)
		})
		return releaseErr
	}, nil
}

func releasePipelineOutlineAllHeldControl(lockPath string) error {
	pipelineOutlineAllControlMu.Lock()
	defer pipelineOutlineAllControlMu.Unlock()
	held := pipelineOutlineAllHeldControls[lockPath]
	if held == nil {
		return nil
	}
	held.refs--
	if held.refs > 0 {
		return nil
	}
	delete(pipelineOutlineAllHeldControls, lockPath)
	var result error
	if err := syscall.Flock(int(held.file.Fd()), syscall.LOCK_UN); err != nil {
		result = err
	}
	if err := held.file.Close(); err != nil && result == nil {
		result = err
	}
	return result
}

func signPipelineOutlineAllRequirement(requirement pipelineOutlineAllRequirement) (pipelineOutlineAllRequirement, error) {
	requirement.RequirementDigest = ""
	digest, err := pipelineProjectAllDigestE(requirement)
	if err != nil {
		return requirement, err
	}
	requirement.RequirementDigest = digest
	return requirement, nil
}

func loadPipelineOutlineAllRequirement(outputDir string) (bool, error) {
	paths := []string{
		filepath.Join(pipelineOutlineAllControlRoot(outputDir), pipelineOutlineAllRunRequirementName),
		filepath.Join(outputDir, filepath.FromSlash(pipelineOutlineAllRequirementPath)),
	}
	found := false
	for _, path := range paths {
		exists, err := validatePipelineOutlineAllRequirementAt(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		found = found || exists
	}
	return found, nil
}

func ensurePipelineOutlineAllRequirement(outputDir string) error {
	requirement, err := signPipelineOutlineAllRequirement(pipelineOutlineAllRequirement{
		Version: "outline-all-requirement.v1", Required: true,
	})
	if err != nil {
		return err
	}
	for _, path := range []string{
		filepath.Join(pipelineOutlineAllControlRoot(outputDir), pipelineOutlineAllRunRequirementName),
		filepath.Join(outputDir, filepath.FromSlash(pipelineOutlineAllRequirementPath)),
	} {
		if exists, validateErr := validatePipelineOutlineAllRequirementAt(path); validateErr == nil && exists {
			continue
		} else if validateErr != nil && !os.IsNotExist(validateErr) {
			return validateErr
		}
		if _, err := writePipelinePlanningJSON(path, requirement); err != nil {
			return err
		}
	}
	return nil
}

func validatePipelineOutlineAllRequirementAt(path string) (bool, error) {
	var requirement pipelineOutlineAllRequirement
	if err := readPipelinePlanningJSON(path, &requirement); err != nil {
		return false, err
	}
	stored := requirement.RequirementDigest
	validated, err := signPipelineOutlineAllRequirement(requirement)
	if err != nil || requirement.Version != "outline-all-requirement.v1" || !requirement.Required || validated.RequirementDigest != stored {
		return false, fmt.Errorf("outline-all requirement marker is invalid at %s", path)
	}
	return true, nil
}

func pipelineOutlineAllRunRoot(outputDir string) string {
	return filepath.Dir(filepath.Dir(filepath.Clean(outputDir)))
}

func pipelineOutlineAllCandidatePath(outputDir, attemptID string) string {
	return filepath.Join(
		pipelineOutlineAllRunRoot(outputDir),
		pipelineOutlineAllRunDirName,
		attemptID,
		"output",
		"novel",
	)
}

func pipelineOutlineAllPublishRoot(outputDir string) string {
	return filepath.Join(pipelineOutlineAllRunRoot(outputDir), pipelineOutlineAllPublishDirName)
}

func preparePipelineOutlineAllCandidate(liveOutputDir, candidateDir, attemptID string) error {
	if err := validatePipelineOutlineAllCandidateNamespace(liveOutputDir, candidateDir, attemptID, false); err != nil {
		return err
	}
	if _, err := os.Stat(candidateDir); err == nil {
		if err := validatePipelineOutlineAllCandidateNamespace(liveOutputDir, candidateDir, attemptID, true); err != nil {
			return err
		}
		if err := validatePipelineOutlineAllCandidatePrepareReceipt(liveOutputDir, candidateDir, attemptID); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		// A directory without the final prepare receipt is an interrupted copy,
		// never a resumable candidate. Namespace validation above proves this is
		// the deterministic isolated path before the bounded destructive rebuild.
		if err := os.RemoveAll(candidateDir); err != nil {
			return fmt.Errorf("remove incomplete outline-all candidate: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copyProjectAllWorkspace(liveOutputDir, candidateDir); err != nil {
		return fmt.Errorf("copy outline-all candidate: %w", err)
	}
	// brainstorm.md belongs to the run root rather than output/novel. Materialize
	// the exact bytes inside the candidate so every resumed operation sees the
	// same authority even though its synthetic run root is different.
	brainstormSource := filepath.Join(pipelineOutlineAllRunRoot(liveOutputDir), "brainstorm.md")
	if info, err := os.Stat(brainstormSource); err == nil {
		brainstormTarget := filepath.Join(candidateDir, "meta", "brainstorm.md")
		if err := os.MkdirAll(filepath.Dir(brainstormTarget), 0o755); err != nil {
			return err
		}
		if existing, readErr := os.ReadFile(brainstormTarget); readErr == nil {
			source, sourceErr := os.ReadFile(brainstormSource)
			if sourceErr != nil {
				return sourceErr
			}
			if pipelineBytesSHA(existing) != pipelineBytesSHA(source) {
				return fmt.Errorf("outline-all candidate meta/brainstorm.md differs from run-root authority")
			}
		} else if os.IsNotExist(readErr) {
			if err := copyProjectAllFile(brainstormSource, brainstormTarget, info.Mode().Perm()); err != nil {
				return fmt.Errorf("copy outline-all run-root brainstorm: %w", err)
			}
		} else {
			return readErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	// The sealed mode receipt lives below meta/planning, which the generic
	// isolated copier excludes together with formal chapter plans. Copy only
	// this capability prerequisite back into the clean candidate.
	for _, rel := range []string{"meta/planning/writing_mode.json", pipelineOutlineAllRequirementPath} {
		modeSource := filepath.Join(liveOutputDir, filepath.FromSlash(rel))
		if info, err := os.Stat(modeSource); err == nil {
			modeTarget := filepath.Join(candidateDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(modeTarget), 0o755); err != nil {
				return err
			}
			if err := copyProjectAllFile(modeSource, modeTarget, info.Mode().Perm()); err != nil {
				return err
			}
		} else if err != nil {
			return fmt.Errorf("copy outline-all planning prerequisite %s: %w", rel, err)
		}
	}
	st := store.NewStore(candidateDir)
	if err := st.Init(); err != nil {
		return err
	}
	// A pre-outline calendar/contract may still encode the old partial chapter
	// count. Never expose that stale target to Architect; zero-init recreates the
	// structured time contract from the completed outline-all receipt.
	for _, rel := range []string{
		"meta/story_time_contract.json",
		"meta/story_time_contract.md",
		"meta/story_calendar.json",
	} {
		if err := os.Remove(filepath.Join(candidateDir, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := st.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return fmt.Errorf("copied outline-all candidate is not chapter-zero clean: %w", err)
	}
	if err := writePipelineOutlineAllCandidatePrepareReceipt(liveOutputDir, candidateDir, attemptID); err != nil {
		return err
	}
	return validatePipelineOutlineAllCandidateNamespace(liveOutputDir, candidateDir, attemptID, true)
}

func writePipelineOutlineAllCandidatePrepareReceipt(liveOutputDir, candidateDir, attemptID string) error {
	receipt := pipelineOutlineAllCandidatePrepareReceipt{
		Version: "outline-all-candidate-prepare.v1", AttemptID: attemptID,
		LiveDir: filepath.Clean(liveOutputDir), CandidateDir: filepath.Clean(candidateDir),
		PreparedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	receipt.ReceiptDigest = pipelineProjectAllDigest(struct {
		Version      string `json:"version"`
		AttemptID    string `json:"attempt_id"`
		LiveDir      string `json:"live_dir"`
		CandidateDir string `json:"candidate_dir"`
		PreparedAt   string `json:"prepared_at"`
	}{receipt.Version, receipt.AttemptID, receipt.LiveDir, receipt.CandidateDir, receipt.PreparedAt})
	_, err := writePipelinePlanningJSON(
		filepath.Join(candidateDir, filepath.FromSlash(pipelineOutlineAllCandidatePreparePath)),
		receipt,
	)
	return err
}

func validatePipelineOutlineAllCandidatePrepareReceipt(liveOutputDir, candidateDir, attemptID string) error {
	path := filepath.Join(candidateDir, filepath.FromSlash(pipelineOutlineAllCandidatePreparePath))
	var receipt pipelineOutlineAllCandidatePrepareReceipt
	if err := readPipelinePlanningJSON(path, &receipt); err != nil {
		return err
	}
	want := pipelineProjectAllDigest(struct {
		Version      string `json:"version"`
		AttemptID    string `json:"attempt_id"`
		LiveDir      string `json:"live_dir"`
		CandidateDir string `json:"candidate_dir"`
		PreparedAt   string `json:"prepared_at"`
	}{receipt.Version, receipt.AttemptID, receipt.LiveDir, receipt.CandidateDir, receipt.PreparedAt})
	if receipt.Version != "outline-all-candidate-prepare.v1" ||
		receipt.AttemptID != attemptID ||
		filepath.Clean(receipt.LiveDir) != filepath.Clean(liveOutputDir) ||
		filepath.Clean(receipt.CandidateDir) != filepath.Clean(candidateDir) ||
		strings.TrimSpace(receipt.PreparedAt) == "" || receipt.ReceiptDigest != want {
		return fmt.Errorf("outline-all candidate prepare receipt is invalid or drifted")
	}
	return nil
}

// validatePipelineOutlineAllCandidateNamespace prevents a pre-created symlink
// or non-directory at any candidate namespace component from turning the
// isolated Architect store into an alias of live canon.
func validatePipelineOutlineAllCandidateNamespace(
	liveOutputDir, candidateDir, attemptID string,
	requireCandidate bool,
) error {
	liveAbs, err := filepath.Abs(liveOutputDir)
	if err != nil {
		return err
	}
	candidateAbs, err := filepath.Abs(candidateDir)
	if err != nil {
		return err
	}
	expectedAbs, err := filepath.Abs(pipelineOutlineAllCandidatePath(liveAbs, attemptID))
	if err != nil {
		return err
	}
	if filepath.Clean(candidateAbs) != filepath.Clean(expectedAbs) {
		return fmt.Errorf("outline-all candidate path is outside its deterministic attempt namespace")
	}
	namespaceAbs, err := filepath.Abs(filepath.Join(pipelineOutlineAllRunRoot(liveAbs), pipelineOutlineAllRunDirName))
	if err != nil {
		return err
	}
	if !pathContainsPipelineRenderCandidate(namespaceAbs, candidateAbs) {
		return fmt.Errorf("outline-all candidate is not contained by %s", namespaceAbs)
	}

	rel, err := filepath.Rel(namespaceAbs, candidateAbs)
	if err != nil {
		return err
	}
	components := append([]string{namespaceAbs}, strings.Split(rel, string(filepath.Separator))...)
	current := components[0]
	for i, component := range components {
		if i > 0 {
			current = filepath.Join(current, component)
		}
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if requireCandidate {
				return fmt.Errorf("outline-all candidate namespace component is missing: %s", current)
			}
			break
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("outline-all candidate namespace refuses symlink %s", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("outline-all candidate namespace component is not a directory: %s", current)
		}
	}
	if !requireCandidate {
		if _, err := os.Lstat(candidateAbs); os.IsNotExist(err) {
			return nil
		}
	}
	resolvedLive, err := filepath.EvalSymlinks(liveAbs)
	if err != nil {
		return fmt.Errorf("resolve outline-all live directory: %w", err)
	}
	resolvedNamespace, err := filepath.EvalSymlinks(namespaceAbs)
	if err != nil {
		return fmt.Errorf("resolve outline-all namespace: %w", err)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		return fmt.Errorf("resolve outline-all candidate: %w", err)
	}
	if filepath.Clean(resolvedCandidate) == filepath.Clean(resolvedLive) ||
		!pathContainsPipelineRenderCandidate(resolvedNamespace, resolvedCandidate) {
		return fmt.Errorf("outline-all resolved candidate escapes its namespace or aliases live canon")
	}
	return validatePipelineOutlineAllCandidateTree(candidateAbs)
}

func validatePipelineOutlineAllCandidateTree(candidateDir string) error {
	return filepath.WalkDir(candidateDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("outline-all candidate tree refuses symlink %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("outline-all candidate tree refuses non-regular file %s", path)
		}
		return nil
	})
}

func validatePipelineOutlineAllEntry(st *store.Store) error {
	if st == nil {
		return fmt.Errorf("outline-all requires a store")
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return fmt.Errorf("outline-all requires initialized progress: %w", err)
	}
	if progress.CurrentChapter != 0 || progress.InProgressChapter != 0 ||
		progress.LatestCompleted() != 0 || len(progress.CompletedChapters) != 0 ||
		len(progress.CompletedScenes) != 0 || progress.TotalWordCount != 0 ||
		len(progress.PendingRewrites) != 0 || progress.ReopenedFromComplete ||
		progress.Phase == domain.PhaseComplete {
		return fmt.Errorf("outline-all only runs at a clean chapter-zero canon boundary")
	}
	for chapter, words := range progress.ChapterWordCounts {
		if words != 0 {
			return fmt.Errorf("outline-all found chapter %d word count=%d", chapter, words)
		}
	}
	if err := st.ValidateOutlineAllChapterZeroWorkspace(); err != nil {
		return err
	}
	projected := st.ProjectedV2()
	if active, err := projected.LoadActiveGeneration(); err != nil {
		return fmt.Errorf("outline-all inspect active projection: %w", err)
	} else if active != nil {
		return fmt.Errorf("outline-all refuses active projected generation %s", active.GenerationID)
	}
	if cursor, err := projected.LoadProjectionCursor(); err != nil {
		return fmt.Errorf("outline-all inspect projection cursor: %w", err)
	} else if cursor != nil {
		return fmt.Errorf("outline-all refuses an existing projection cursor")
	}
	if cursor, err := projected.LoadRealizationCursor(); err != nil {
		return fmt.Errorf("outline-all inspect realization cursor: %w", err)
	} else if cursor != nil {
		return fmt.Errorf("outline-all refuses an existing realization cursor")
	}
	for _, rel := range []string{
		"meta/planning/v2/.building",
		"meta/planning/v2/generations",
		"meta/rewrite_recovery",
	} {
		if has, err := directoryHasRegularFile(filepath.Join(st.Dir(), filepath.FromSlash(rel))); err != nil {
			return err
		} else if has {
			return fmt.Errorf("outline-all refuses building/projection/recovery state under %s", rel)
		}
	}
	if nonEmptyFile(filepath.Join(st.Dir(), "meta", "pending_commit.json")) {
		return fmt.Errorf("outline-all refuses pending commit recovery")
	}
	return nil
}

func directoryHasRegularFile(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if os.IsNotExist(err) {
		return false, nil
	}
	return found, err
}

// pipelineOutlineAllProtectedCanonRoot binds every pre-existing source that
// outline-all is not allowed to mutate. Outline/progress/receipt/session logs
// are intentionally outside this root; chapter and draft trees are included
// even though the entry gate requires them to be empty.
func pipelineOutlineAllProtectedCanonRoot(outputDir string) (string, error) {
	components := make(map[string]string)
	err := filepath.WalkDir(outputDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(outputDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if outlineAllMutableOrVolatilePath(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("outline-all protected canon refuses symlink %s", rel)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("outline-all protected canon refuses non-regular file %s", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		components[rel] = "sha256:" + hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return "", err
	}
	stableProgress, err := pipelineOutlineAllStableProgressRoot(outputDir)
	if err != nil {
		return "", err
	}
	components["meta/progress.json#without_total_chapters"] = stableProgress
	return pipelineProjectAllDigest(struct {
		Version    string            `json:"version"`
		Components map[string]string `json:"components"`
	}{Version: "outline-all-protected-canon.v2", Components: components}), nil
}

// pipelineOutlineAllStableProgressRoot binds every JSON field, including
// forward-compatible fields unknown to this binary, except the single derived
// total_chapters value that outline-all is authorized to synchronize.
func pipelineOutlineAllStableProgressRoot(outputDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(outputDir, "meta", "progress.json"))
	if err != nil {
		return "", fmt.Errorf("outline-all stable progress requires meta/progress.json: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", fmt.Errorf("parse outline-all stable progress: %w", err)
	}
	if fields == nil {
		return "", fmt.Errorf("outline-all progress must be a JSON object")
	}
	if _, exists := fields["total_chapters"]; !exists {
		return "", fmt.Errorf("outline-all progress is missing total_chapters")
	}
	delete(fields, "total_chapters")
	return pipelineProjectAllDigest(struct {
		Version string                     `json:"version"`
		Fields  map[string]json.RawMessage `json:"fields"`
	}{Version: "outline-all-stable-progress.v1", Fields: fields}), nil
}

func outlineAllMutableOrVolatilePath(rel string) bool {
	rel = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "./")
	for _, prefix := range []string{
		"logs",
		"meta/planning",
		"meta/runtime",
		"meta/sessions",
	} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	switch rel {
	case "outline.json", "outline.md", "layered_outline.json", "layered_outline.md",
		"meta/progress.json", "meta/pipeline.json", "meta/usage.json", "meta/diag-export.md",
		"meta/run.json",
		"meta/architect_readiness.json", "meta/architect_readiness.md",
		"meta/brainstorm.md",
		"meta/checkpoints.jsonl", "meta/prompt_manifest.json",
		"meta/story_time_contract.json", "meta/story_time_contract.md", "meta/story_calendar.json",
		"meta/rag/craft_recall_log.jsonl", "meta/rag/retrieval_trace.jsonl", "meta/rag/index_state.md",
		"headless.log", ".DS_Store":
		return true
	default:
		return false
	}
}

func pipelineOutlineAllSourceSnapshotRoot(outputDir string) (string, error) {
	protected, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		return "", err
	}
	artifacts := map[string]string{"protected_canon_root": protected}
	foundation, err := loadPipelineOutlineAllFrozenFoundation(outputDir)
	if err != nil {
		return "", err
	}
	artifacts["foundation_context_root"] = foundation.Root
	for _, rel := range []string{
		"layered_outline.json",
		"outline.json",
		"meta/progress.json",
		"meta/compass.json",
		"meta/planning/writing_mode.json",
	} {
		digest, err := pipelineRequiredFileSHA(outputDir, rel)
		if err != nil {
			return "", err
		}
		artifacts[rel] = digest
	}
	return pipelineProjectAllDigest(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{Version: "outline-all-source-snapshot.v1", Artifacts: artifacts}), nil
}

type pipelineOutlineAllFrozenFoundation struct {
	Root        string            `json:"root"`
	Premise     string            `json:"premise"`
	Characters  json.RawMessage   `json:"characters"`
	WorldRules  json.RawMessage   `json:"world_rules"`
	BookWorld   json.RawMessage   `json:"book_world"`
	Compass     json.RawMessage   `json:"compass"`
	Authorities map[string]string `json:"authorities,omitempty"`
}

func loadPipelineOutlineAllFrozenFoundation(outputDir string) (pipelineOutlineAllFrozenFoundation, error) {
	read := func(rel string) ([]byte, error) {
		raw, err := os.ReadFile(filepath.Join(outputDir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("outline-all frozen context requires %s: %w", rel, err)
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil, fmt.Errorf("outline-all frozen context %s is empty", rel)
		}
		return raw, nil
	}
	premise, err := read("premise.md")
	if err != nil {
		return pipelineOutlineAllFrozenFoundation{}, err
	}
	jsonFiles := []string{"characters.json", "world_rules.json", "book_world.json", "meta/compass.json"}
	rawJSON := make(map[string][]byte, len(jsonFiles))
	digests := map[string]string{"premise.md": pipelineBytesSHA(premise)}
	for _, rel := range jsonFiles {
		raw, err := read(rel)
		if err != nil {
			return pipelineOutlineAllFrozenFoundation{}, err
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return pipelineOutlineAllFrozenFoundation{}, fmt.Errorf("outline-all frozen context %s is invalid JSON: %w", rel, err)
		}
		rawJSON[rel] = raw
		digests[rel] = pipelineBytesSHA(raw)
	}
	authorities := make(map[string]string)
	brainstormPath := filepath.Join(outputDir, "meta", "brainstorm.md")
	if _, err := os.Stat(brainstormPath); os.IsNotExist(err) {
		brainstormPath = filepath.Join(pipelineOutlineAllRunRoot(outputDir), "brainstorm.md")
	}
	optional := map[string]string{
		"brainstorm.md":                      brainstormPath,
		"meta/user_rules.json":               filepath.Join(outputDir, "meta", "user_rules.json"),
		"meta/web_reference_brief.json":      filepath.Join(outputDir, "meta", "web_reference_brief.json"),
		"meta/web_reference_brief.md":        filepath.Join(outputDir, "meta", "web_reference_brief.md"),
		"meta/prewrite_storycraft_plan.json": filepath.Join(outputDir, "meta", "prewrite_storycraft_plan.json"),
		"meta/prewrite_storycraft_plan.md":   filepath.Join(outputDir, "meta", "prewrite_storycraft_plan.md"),
	}
	for rel, path := range optional {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			authorities[rel] = string(raw)
			digests[rel] = pipelineBytesSHA(raw)
		case os.IsNotExist(err):
			digests[rel] = "missing"
		default:
			return pipelineOutlineAllFrozenFoundation{}, err
		}
	}
	root := pipelineProjectAllDigest(struct {
		Version   string            `json:"version"`
		Artifacts map[string]string `json:"artifacts"`
	}{Version: "outline-all-foundation-context.v1", Artifacts: digests})
	return pipelineOutlineAllFrozenFoundation{
		Root: root, Premise: string(premise),
		Characters:  append(json.RawMessage(nil), rawJSON["characters.json"]...),
		WorldRules:  append(json.RawMessage(nil), rawJSON["world_rules.json"]...),
		BookWorld:   append(json.RawMessage(nil), rawJSON["book_world.json"]...),
		Compass:     append(json.RawMessage(nil), rawJSON["meta/compass.json"]...),
		Authorities: authorities,
	}, nil
}

func pipelineOutlineAllOperationContextRoot(foundationRoot, beforeLayeredDigest string) string {
	return pipelineProjectAllDigest(struct {
		Version               string `json:"version"`
		FoundationContextRoot string `json:"foundation_context_root"`
		BeforeLayeredDigest   string `json:"before_layered_digest"`
	}{
		Version:               "outline-all-operation-context.v1",
		FoundationContextRoot: foundationRoot,
		BeforeLayeredDigest:   beforeLayeredDigest,
	})
}

func validatePipelineOutlineAllStableInputs(outputDir, stableProgressRoot, foundationContextRoot string) error {
	currentProgress, err := pipelineOutlineAllStableProgressRoot(outputDir)
	if err != nil {
		return err
	}
	if currentProgress != stableProgressRoot {
		return fmt.Errorf("outline-all progress changed outside the authorized total_chapters field")
	}
	currentFoundation, err := loadPipelineOutlineAllFrozenFoundation(outputDir)
	if err != nil {
		return err
	}
	if currentFoundation.Root != foundationContextRoot {
		return fmt.Errorf("outline-all frozen foundation context changed during execution")
	}
	return nil
}

func outlineAllAttemptID(sourceRoot, executionIdentity string) string {
	return "oa-" + shortPipelineHash(sourceRoot+"\n"+executionIdentity)
}

func copyPipelineMetadataForOutlineAllPublish(liveOutputDir, candidateDir string) error {
	// pipeline.json is owned by the outer runner and was intentionally excluded
	// from the long-running candidate. Preserve its current pre-stage snapshot;
	// the runner writes stage completion after DirectoryPublish returns.
	for _, rel := range []string{"meta/pipeline.json"} {
		source := filepath.Join(liveOutputDir, filepath.FromSlash(rel))
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		target := filepath.Join(candidateDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		_ = os.Remove(target)
		if err := copyProjectAllFile(source, target, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// recoverAllDirectoryPublishesWithControlHeld repairs every protocol that can
// have moved the common live directory. Canon rebase runs first because its
// live_archived phase may leave no live path at all; outline-all recovery can
// then validate/bind against the restored chapter-zero tree. Callers must own
// the stable run-root EX lease and must invoke this before creating any marker
// or letting loadCfgBundle write prompt_manifest below the live directory.
func recoverAllDirectoryPublishesWithControlHeld(outputDir string) error {
	if err := recoverPipelineRebasePublishesWithControlHeld(outputDir); err != nil {
		return err
	}
	if err := recoverPipelineOutlineAllPublishesWithControlHeld(outputDir); err != nil {
		return err
	}
	return recoverPipelineRenderPublishesWithControlHeld(outputDir)
}

// recoverPipelineOutlineAllPublishesWithControlHeld is the transaction repair
// core for callers that already own the stable run-root exclusive control.
// Keeping this separate prevents a pre-load recovery path from depending on
// process-local re-entrant flock bookkeeping for correctness.
func recoverPipelineOutlineAllPublishesWithControlHeld(outputDir string) error {
	root := pipelineOutlineAllPublishRoot(outputDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	publisher := store.NewDirectoryPublishStore(root)
	live := filepath.Clean(outputDir)
	for _, id := range ids {
		if !strings.HasPrefix(id, "oa-") {
			return fmt.Errorf("outline-all publish root contains unexpected transaction %q", id)
		}
		state, err := publisher.LoadDirectoryPublishState(id)
		if err != nil {
			return fmt.Errorf("outline-all recovery inspect %s: %w", id, err)
		}
		if state == nil || state.Phase == store.DirectoryPublishAborted {
			continue
		}
		var receipt *store.DirectoryPublishReceipt
		if state.Phase == store.DirectoryPublishFinalized {
			receipt = state.Receipt
			if receipt == nil || filepath.Clean(receipt.LiveDir) != live || receipt.TransactionID != id ||
				receipt.CandidateRoot != receipt.CommittedLiveRoot {
				return fmt.Errorf("outline-all finalized transaction %s targets another live directory", id)
			}
			liveExecution, loadErr := store.NewStore(live).LoadOutlineAllExecutionReceipt()
			if loadErr != nil {
				return loadErr
			}
			// Finalized journals are durable history. A rebase/new generation may
			// legitimately have no receipt for an older attempt; never bind that
			// historical transaction into the current live project.
			if liveExecution == nil || liveExecution.AttemptID != id {
				continue
			}
			expectedCandidate := filepath.Clean(pipelineOutlineAllCandidatePath(live, id))
			if filepath.Clean(receipt.CandidateDir) != expectedCandidate ||
				filepath.Clean(liveExecution.CandidateDir) != expectedCandidate ||
				liveExecution.ExpectedLiveDirectoryRoot != receipt.BeforeLiveRoot {
				return fmt.Errorf("outline-all finalized transaction %s candidate directory lost its deterministic live-root binding", id)
			}
		} else {
			expectedCandidate := filepath.Clean(pipelineOutlineAllCandidatePath(live, id))
			if state.Intent == nil || state.Intent.TransactionID != id ||
				filepath.Clean(state.Intent.LiveDir) != live ||
				filepath.Clean(state.Intent.CandidateDir) != expectedCandidate ||
				!pathContainsPipelineRenderCandidate(
					filepath.Join(pipelineOutlineAllRunRoot(live), pipelineOutlineAllRunDirName),
					state.Intent.CandidateDir,
				) || state.Intent.BeforeLiveRoot == "" || state.Intent.CandidateRoot == "" {
				return fmt.Errorf("outline-all recovery transaction %s is not bound to this live/attempt namespace", id)
			}
			err = withPipelineWatchdogPaused(func() error {
				var recoverErr error
				receipt, recoverErr = publisher.RecoverDirectoryPublish(id)
				return recoverErr
			})
			if err != nil {
				return fmt.Errorf("outline-all recover transaction %s: %w", id, err)
			}
			if receipt == nil || receipt.TransactionID != id ||
				filepath.Clean(receipt.LiveDir) != live ||
				filepath.Clean(receipt.CandidateDir) != expectedCandidate ||
				receipt.CandidateRoot != receipt.CommittedLiveRoot ||
				receipt.BeforeLiveRoot != state.Intent.BeforeLiveRoot {
				return fmt.Errorf("outline-all recovery transaction %s returned mismatched roots", id)
			}
			if err := publisher.FinalizeDirectoryPublish(id); err != nil {
				return fmt.Errorf("outline-all finalize recovered transaction %s: %w", id, err)
			}
		}
		if receipt != nil {
			if err := bindRecoveredPipelineOutlineAllPublish(live, id, *receipt); err != nil {
				return err
			}
		}
	}
	return nil
}

func bindRecoveredPipelineOutlineAllPublish(
	live, attemptID string,
	publishReceipt store.DirectoryPublishReceipt,
) error {
	st := store.NewStore(live)
	receipt, err := st.LoadOutlineAllExecutionReceipt()
	if err != nil {
		return fmt.Errorf("outline-all directory transaction %s load execution receipt: %w", attemptID, err)
	}
	if receipt == nil {
		return fmt.Errorf("outline-all directory transaction %s lacks execution receipt", attemptID)
	}
	if receipt.Status != domain.OutlineAllExecutionComplete || receipt.AttemptID != attemptID {
		return fmt.Errorf("outline-all recovered publish %s execution identity mismatch", attemptID)
	}
	expectedAttemptID, err := pipelineOutlineAllAttemptIDFromReceipt(st, receipt)
	if err != nil || expectedAttemptID != attemptID {
		return fmt.Errorf("outline-all recovered publish %s deterministic attempt identity mismatch: %v", attemptID, err)
	}
	expectedCandidate := filepath.Clean(pipelineOutlineAllCandidatePath(live, attemptID))
	if filepath.Clean(receipt.CandidateDir) != expectedCandidate ||
		filepath.Clean(publishReceipt.CandidateDir) != expectedCandidate ||
		publishReceipt.CandidateRoot != publishReceipt.CommittedLiveRoot ||
		receipt.ExpectedLiveDirectoryRoot != publishReceipt.BeforeLiveRoot {
		return fmt.Errorf("outline-all recovered publish %s candidate directory deterministic namespace/root binding mismatch", attemptID)
	}
	// A finalized transaction that was already bound is durable publication
	// history. Downstream canon/progress may legitimately have advanced since
	// then; recovery must not re-impose the chapter-zero protected root.
	if receipt.PublishedCandidateRoot != "" || receipt.DirectoryPublishReceiptDigest != "" {
		if receipt.PublishedCandidateRoot != publishReceipt.CandidateRoot ||
			receipt.DirectoryPublishReceiptDigest != publishReceipt.ReceiptDigest {
			return fmt.Errorf("outline-all recovered publish %s roots or receipt digest binding drift", attemptID)
		}
		return nil
	}
	if currentProtected, rootErr := pipelineOutlineAllProtectedCanonRoot(live); rootErr != nil {
		return rootErr
	} else if currentProtected != receipt.ProtectedCanonRoot {
		return fmt.Errorf("outline-all recovered publish %s protected canon drift", attemptID)
	}
	if err := validatePipelineOutlineAllStableInputs(live, receipt.StableProgressRoot, receipt.FoundationContextRoot); err != nil {
		return err
	}
	_, err = st.UpdateOutlineAllExecutionReceipt(receipt.ReceiptDigest, func(current *domain.OutlineAllExecutionReceipt) error {
		current.PublishedCandidateRoot = publishReceipt.CandidateRoot
		current.DirectoryPublishReceiptDigest = publishReceipt.ReceiptDigest
		current.UpdatedAt = time.Now().UTC()
		return nil
	})
	return err
}
