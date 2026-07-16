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

// CurrentChapterPlanCheckpoint proves that the latest plan checkpoint names
// the canonical formal-plan artifact and digests the exact bytes currently on
// disk. Merely having an older plan checkpoint is not enough: finalize writes
// the plan before appending its checkpoint, so a failed append must leave the
// new file unroutable instead of letting it inherit the old plan epoch.
func CurrentChapterPlanCheckpoint(st *store.Store, chapter int) (*domain.Checkpoint, error) {
	if st == nil || chapter <= 0 {
		return nil, fmt.Errorf("invalid chapter %d: %w", chapter, errs.ErrToolArgs)
	}
	artifact := fmt.Sprintf("drafts/%02d.plan.json", chapter)
	raw, err := os.ReadFile(filepath.Join(st.Dir(), filepath.FromSlash(artifact)))
	if err != nil {
		return nil, fmt.Errorf("读取第 %d 章正式 plan artifact %s 失败: %w: %w", chapter, artifact, errs.ErrStoreRead, err)
	}
	sum := sha256.Sum256(raw)
	wantDigest := fmt.Sprintf("sha256:%x", sum)
	cp := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "plan")
	if cp == nil {
		return nil, fmt.Errorf("第 %d 章正式 plan 没有 plan checkpoint，无法证明 finalize 已完成: %w", chapter, errs.ErrToolPrecondition)
	}
	if cp.Artifact != artifact {
		return nil, fmt.Errorf("第 %d 章最新 plan checkpoint 指向 %q，不是当前正式 plan %q: %w", chapter, cp.Artifact, artifact, errs.ErrToolPrecondition)
	}
	if cp.Digest != wantDigest {
		return nil, fmt.Errorf("第 %d 章正式 plan 与最新 plan checkpoint 摘要不匹配（checkpoint=%s, current=%s）；可能是新 plan 已写入但 checkpoint 追加失败: %w",
			chapter, cp.Digest, wantDigest, errs.ErrToolPrecondition)
	}
	return cp, nil
}

// CurrentChapterPlanCausalCheckpoint additionally proves that no finalized
// chapter-world simulation was checkpointed after the current formal plan.
// SimulationID equality is not sufficient here: a forced structural
// resimulation may preserve the same projected facts while still opening a new
// causal epoch that the POV plan must explicitly consume before prose writes.
func CurrentChapterPlanCausalCheckpoint(st *store.Store, chapter int) (*domain.Checkpoint, error) {
	plan, err := CurrentChapterPlanCheckpoint(st, chapter)
	if err != nil {
		return nil, err
	}
	simulation := st.Checkpoints.LatestByStep(domain.ChapterScope(chapter), "chapter_world_simulation")
	if simulation != nil && simulation.Seq > plan.Seq {
		return nil, fmt.Errorf(
			"第 %d 章 chapter_world_simulation checkpoint(seq=%d) 晚于当前正式 plan checkpoint(seq=%d)；必须基于该 simulation 重新 finalize POV plan 后才能写入正文: %w",
			chapter, simulation.Seq, plan.Seq, errs.ErrToolPrecondition,
		)
	}
	return plan, nil
}
