package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const InitialWorldTickContextVersion = "initial-world-tick-author-context.v1"
const initialWorldTickContextMaxBytes = 2 * 1024 * 1024

type initialWorldTickSource struct {
	SHA256  string `json:"source_sha256"`
	Content string `json:"content"`
}

// InitialWorldTickExactContext is the chapter-zero author's source view. It
// activates only for the current process's existing world-tick-only lease;
// ordinary chapter/world-simulation context semantics are unchanged.
func InitialWorldTickExactContext(st *store.Store) (json.RawMessage, bool, error) {
	if st == nil {
		return nil, false, nil
	}
	// Read without LoadPipelineExecution's expired-lease cleanup: a stale
	// initial tick must fail closed, never fall back to ordinary context.
	leaseRaw, err := os.ReadFile(filepath.Join(st.Dir(), "meta/runtime/pipeline_execution.json"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var lock domain.PipelineExecutionLock
	if err := json.Unmarshal(leaseRaw, &lock); err != nil {
		return nil, false, err
	}
	if lock.Mode != domain.PipelineExecutionWorldTick {
		return nil, false, nil
	}
	if lock.Version != 1 || lock.TargetChapter != 1 || lock.ProcessID != os.Getpid() || strings.TrimSpace(lock.Owner) == "" || lock.AcquiredAt.IsZero() || !lock.ActiveAt(time.Now().UTC()) {
		return nil, true, fmt.Errorf("initial world_tick context requires the current process's active chapter-one lease: %w", errs.ErrToolPrecondition)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil || progress.CurrentChapter != 0 {
		return nil, true, fmt.Errorf("initial world_tick context requires chapter-zero progress: %w", errs.ErrToolPrecondition)
	}
	base, err := os.Lstat(st.Dir())
	if err != nil || !base.IsDir() || base.Mode()&os.ModeSymlink != 0 {
		return nil, true, fmt.Errorf("initial world_tick source root is not a regular directory")
	}
	root, err := os.OpenRoot(st.Dir())
	if err != nil {
		return nil, true, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(base, opened) {
		return nil, true, fmt.Errorf("initial world_tick source root changed")
	}
	leaseBefore, err := readFoundationSourceFile(root, "meta/runtime/pipeline_execution.json", 16384)
	if err != nil {
		return nil, true, err
	}
	if string(leaseBefore) != string(leaseRaw) {
		return nil, true, fmt.Errorf("initial world_tick source lease changed while opening")
	}
	sources := map[string]initialWorldTickSource{}
	for _, path := range []string{"premise.md", "characters.json", "world_rules.json", "book_world.json", "world_codex.json", "meta/user_rules.json"} {
		raw, err := readFoundationSourceFile(root, path, initialWorldTickContextMaxBytes)
		if err != nil {
			return nil, true, err
		}
		if len(raw) == 0 || !utf8.Valid(raw) || (strings.HasSuffix(path, ".json") && !json.Valid(raw)) {
			return nil, true, fmt.Errorf("initial world_tick source %s is incomplete or invalid; no partial source returned", path)
		}
		sources[path] = initialWorldTickSource{SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(raw)), Content: string(raw)}
	}
	contract, err := BuildInitialWorldTickDispatchContract(st)
	if err != nil {
		return nil, true, err
	}
	chapterOne, _, err := initialWorldTickChapterOne(st)
	if err != nil {
		return nil, true, err
	}
	if chapterOne.CoreEvent != contract.CoreEvent || chapterOne.Hook != contract.Hook {
		return nil, true, fmt.Errorf("initial world_tick chapter-one source changed during read")
	}
	leaseAfter, err := readFoundationSourceFile(root, "meta/runtime/pipeline_execution.json", 16384)
	if err != nil || string(leaseBefore) != string(leaseAfter) || !lock.ActiveAt(time.Now().UTC()) {
		return nil, true, fmt.Errorf("initial world_tick source lease changed during read")
	}
	currentRoot, err := os.Lstat(st.Dir())
	if err != nil || !os.SameFile(base, currentRoot) || currentRoot.Mode()&os.ModeSymlink != 0 {
		return nil, true, fmt.Errorf("initial world_tick source root changed during read")
	}
	leaseBinding, err := domain.DeterministicPlanningHash(struct {
		Owner    string
		Process  int
		Acquired time.Time
	}{lock.Owner, lock.ProcessID, lock.AcquiredAt})
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(struct {
		Version          string                            `json:"version"`
		LeaseBinding     string                            `json:"lease_binding"`
		Generation       string                            `json:"generation_id"`
		Policy           string                            `json:"policy"`
		Sources          map[string]initialWorldTickSource `json:"sources"`
		DispatchContract string                            `json:"dispatch_contract"`
		ChapterOne       domain.OutlineEntry               `json:"chapter_one"`
	}{InitialWorldTickContextVersion, leaseBinding, progress.GenerationID, "完整作者态资料仅用于章零 world_tick 条件设置，不是角色已知信息，也不授予其他工具权限；不得提前执行第1章。", sources, contract.Block, chapterOne})
	if err != nil {
		return nil, true, err
	}
	if len(raw) > initialWorldTickContextMaxBytes {
		return nil, true, fmt.Errorf("initial world_tick exact context exceeds %d bytes; no sources truncated or returned: %w", initialWorldTickContextMaxBytes, errs.ErrToolPrecondition)
	}
	return raw, true, nil
}
