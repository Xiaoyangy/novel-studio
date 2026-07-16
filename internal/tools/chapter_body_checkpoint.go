package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/errs"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// CurrentChapterBodyCheckpoint proves that the newest prose-writing event
// (draft or edit) names and digests the exact draft bytes currently on disk.
// Sequence freshness relative to plan/rerender is checked by callers because
// legacy/import projects may not have a causal plan journal.
func CurrentChapterBodyCheckpoint(st *store.Store, chapter int) (*domain.Checkpoint, error) {
	if st == nil || chapter <= 0 {
		return nil, fmt.Errorf("invalid chapter %d: %w", chapter, errs.ErrToolArgs)
	}
	artifact := fmt.Sprintf("drafts/%02d.draft.md", chapter)
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(artifact)))
	if err != nil {
		return nil, fmt.Errorf("读取第 %d 章当前 draft artifact %s 失败: %w: %w", chapter, artifact, errs.ErrStoreRead, err)
	}
	sum := sha256.Sum256(raw)
	wantDigest := fmt.Sprintf("sha256:%x", sum)
	var latest *domain.Checkpoint
	for _, cp := range st.Checkpoints.All() {
		if !cp.Scope.Matches(domain.ChapterScope(chapter)) || (cp.Step != "draft" && cp.Step != "edit") {
			continue
		}
		if latest == nil || cp.Seq > latest.Seq {
			copy := cp
			latest = &copy
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("第 %d 章当前 draft 没有 draft/edit checkpoint，无法证明正文写入完成: %w", chapter, errs.ErrToolPrecondition)
	}
	if latest.Artifact != artifact {
		return nil, fmt.Errorf("第 %d 章最新正文 checkpoint 指向 %q，不是当前 draft %q: %w", chapter, latest.Artifact, artifact, errs.ErrToolPrecondition)
	}
	if latest.Digest != wantDigest {
		return nil, fmt.Errorf("第 %d 章当前 draft 与最新正文 checkpoint 摘要不匹配（checkpoint=%s, current=%s）；可能是正文写入后 checkpoint 追加失败: %w",
			chapter, latest.Digest, wantDigest, errs.ErrToolPrecondition)
	}
	return latest, nil
}
