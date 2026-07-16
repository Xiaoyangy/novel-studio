package store

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPlanningStoreRoundTrip(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"meta/planning", "meta/planning/volumes", "meta/planning/chapters"} {
		if info, err := os.Stat(filepath.Join(st.Dir(), rel)); err != nil || !info.IsDir() {
			t.Fatalf("planning directory %s missing: %v", rel, err)
		}
	}

	fingerprint := storePlanningTestFingerprint(t)
	book := domain.BookCausalSkeleton{
		Version:               domain.PlanningStoreVersion,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonChapter:      0,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		Nodes: []domain.CausalSkeletonNode{{
			ID: "book-need", Cause: "青山县缺少稳定供给", Effect: "主角建立本地供应链",
		}},
	}
	if err := st.Planning.SaveBookCausalSkeleton(book); err != nil {
		t.Fatal(err)
	}
	gotBook, err := st.Planning.LoadBookCausalSkeleton()
	if err != nil || !reflect.DeepEqual(gotBook, &book) {
		t.Fatalf("book roundtrip mismatch: got=%+v err=%v", gotBook, err)
	}

	volume := domain.VolumeCausalSkeleton{
		Version:               domain.PlanningStoreVersion,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonChapter:      0,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		Volume:                1,
		ChapterFrom:           1,
		ChapterTo:             2,
		Nodes: []domain.CausalSkeletonNode{{
			ID: "volume-market", Cause: "供应链尚未验证", Effect: "两章内完成真实交易",
		}},
	}
	if err := st.Planning.SaveVolumeCausalSkeleton(volume); err != nil {
		t.Fatal(err)
	}
	gotVolume, err := st.Planning.LoadVolumeCausalSkeleton(1)
	if err != nil || !reflect.DeepEqual(gotVolume, &volume) {
		t.Fatalf("volume roundtrip mismatch: got=%+v err=%v", gotVolume, err)
	}

	first := storePlanningTestManifest(t, fingerprint, 1, fingerprint.BaseCanonRoot)
	second := storePlanningTestManifest(t, fingerprint, 2, first.ProjectedState.PostStateRoot)
	if err := st.Planning.SaveStagedChapterPlanManifest(first); err != nil {
		t.Fatal(err)
	}
	if err := st.Planning.SaveStagedChapterPlanManifest(second); err != nil {
		t.Fatal(err)
	}
	manifests, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil || !reflect.DeepEqual(manifests, []domain.StagedChapterPlanManifest{first, second}) {
		t.Fatalf("chapter manifests roundtrip mismatch: got=%+v err=%v", manifests, err)
	}

	firstInvalidation := storePlanningInvalidation(fingerprint, "invalidate-1", "chapter", "1", first.PlanSHA256)
	if err := st.Planning.AppendInvalidation(firstInvalidation); err != nil {
		t.Fatal(err)
	}
	beforeAppend, err := os.ReadFile(filepath.Join(st.Dir(), planningInvalidationsPath))
	if err != nil {
		t.Fatal(err)
	}
	secondInvalidation := storePlanningInvalidation(fingerprint, "invalidate-2", "chapter", "2", second.PlanSHA256)
	if err := st.Planning.AppendInvalidation(secondInvalidation); err != nil {
		t.Fatal(err)
	}
	afterAppend, err := os.ReadFile(filepath.Join(st.Dir(), planningInvalidationsPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(afterAppend, beforeAppend) || bytes.Count(afterAppend, []byte("\n")) != 2 {
		t.Fatal("invalidation append rewrote or truncated prior history")
	}
	invalidations, err := st.Planning.LoadInvalidations()
	if err != nil {
		t.Fatal(err)
	}
	if len(invalidations) != 2 || invalidations[0].RecordRoot == "" ||
		invalidations[1].PreviousRecordRoot != invalidations[0].RecordRoot {
		t.Fatalf("invalidation hash chain mismatch: %+v", invalidations)
	}
	beforeDuplicate := append([]byte(nil), afterAppend...)
	if err := st.Planning.AppendInvalidation(firstInvalidation); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate invalidation id was not rejected: %v", err)
	}
	afterDuplicate, err := os.ReadFile(filepath.Join(st.Dir(), planningInvalidationsPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDuplicate, afterDuplicate) {
		t.Fatal("rejected duplicate invalidation changed append-only history")
	}
}

func TestPlanningStoreRejectsBrokenChapterChainWithoutWriting(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	fingerprint := storePlanningTestFingerprint(t)
	first := storePlanningTestManifest(t, fingerprint, 1, fingerprint.BaseCanonRoot)
	if err := st.Planning.SaveStagedChapterPlanManifest(first); err != nil {
		t.Fatal(err)
	}
	broken := storePlanningTestManifest(t, fingerprint, 2, "not-the-predecessor-root")
	if err := st.Planning.SaveStagedChapterPlanManifest(broken); err == nil || !strings.Contains(err.Error(), "does not match chapter 1 post_state_root") {
		t.Fatalf("broken chain was not rejected: %v", err)
	}
	if got, err := st.Planning.LoadStagedChapterPlanManifest(2); err != nil || got != nil {
		t.Fatalf("rejected manifest leaked to disk: got=%+v err=%v", got, err)
	}
}

func TestPlanningStoreReplacesWholeStagedGeneration(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	oldFingerprint := storePlanningTestFingerprint(t)
	oldFirst := storePlanningTestManifest(t, oldFingerprint, 1, oldFingerprint.BaseCanonRoot)
	oldSecond := storePlanningTestManifest(t, oldFingerprint, 2, oldFirst.ProjectedState.PostStateRoot)
	if err := st.Planning.ReplaceStagedChapterPlanManifests([]domain.StagedChapterPlanManifest{oldFirst, oldSecond}); err != nil {
		t.Fatal(err)
	}

	newFingerprint, err := domain.NewDependencyFingerprint("generation-2", "new-canon-root", []domain.PlanningDependency{
		{Kind: "outline", ID: "book", SHA256: "new-outline-sha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	newFirst := storePlanningTestManifest(t, newFingerprint, 1, newFingerprint.BaseCanonRoot)
	if err := st.Planning.ReplaceStagedChapterPlanManifests([]domain.StagedChapterPlanManifest{newFirst}); err != nil {
		t.Fatal(err)
	}
	got, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []domain.StagedChapterPlanManifest{newFirst}) {
		t.Fatalf("whole generation was not replaced: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), planningChapterManifestPath(2))); !os.IsNotExist(err) {
		t.Fatalf("obsolete chapter manifest survived generation replacement: %v", err)
	}

	broken := storePlanningTestManifest(t, newFingerprint, 2, "broken-root")
	if err := st.Planning.ReplaceStagedChapterPlanManifests([]domain.StagedChapterPlanManifest{newFirst, broken}); err == nil {
		t.Fatal("broken replacement chain was accepted")
	}
	unchanged, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil || !reflect.DeepEqual(unchanged, []domain.StagedChapterPlanManifest{newFirst}) {
		t.Fatalf("rejected replacement changed current generation: got=%+v err=%v", unchanged, err)
	}

	if err := st.Planning.ReplaceStagedChapterPlanManifests(nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := st.Planning.LoadStagedChapterPlanManifests()
	if err != nil || len(cleared) != 0 {
		t.Fatalf("empty replacement did not clear manifests: got=%+v err=%v", cleared, err)
	}
}

func TestPlanningWritesDoNotMutateCanonOrDraftStores(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("隔离测试", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{{
		Category: "society", Rule: "交易必须留收据", Boundary: "不能口头补账",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveFinalChapter(1, "已经提交的正文"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(2, "尚未提交的草稿"); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 2, Title: "既有计划"}); err != nil {
		t.Fatal(err)
	}

	protected := []string{
		"meta/progress.json",
		"world_rules.json",
		"world_rules.md",
		"chapters",
		"drafts",
	}
	before := snapshotPlanningProtectedPaths(t, st.Dir(), protected)

	fingerprint := storePlanningTestFingerprint(t)
	book := domain.BookCausalSkeleton{
		Version:               domain.PlanningStoreVersion,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonChapter:      0,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		Nodes: []domain.CausalSkeletonNode{{
			ID: "isolation", Cause: "只做推演", Effect: "不触碰 canon",
		}},
	}
	if err := st.Planning.SaveBookCausalSkeleton(book); err != nil {
		t.Fatal(err)
	}
	first := storePlanningTestManifest(t, fingerprint, 1, fingerprint.BaseCanonRoot)
	if err := st.Planning.SaveStagedChapterPlanManifest(first); err != nil {
		t.Fatal(err)
	}
	if err := st.Planning.AppendInvalidation(storePlanningInvalidation(fingerprint, "isolation-1", "chapter", "1", first.PlanSHA256)); err != nil {
		t.Fatal(err)
	}

	after := snapshotPlanningProtectedPaths(t, st.Dir(), protected)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("planning writes mutated progress/world/chapters/drafts:\nbefore=%v\nafter=%v", before, after)
	}
}

func storePlanningTestFingerprint(t *testing.T) domain.DependencyFingerprint {
	t.Helper()
	fingerprint, err := domain.NewDependencyFingerprint("generation-1", "canon-root", []domain.PlanningDependency{
		{Kind: "outline", ID: "book", SHA256: "outline-sha"},
		{Kind: "world", ID: "rules", SHA256: "world-sha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func storePlanningTestManifest(t *testing.T, fingerprint domain.DependencyFingerprint, chapter int, preStateRoot string) domain.StagedChapterPlanManifest {
	t.Helper()
	projectionRoot, err := domain.DeterministicPlanningHash(map[string]any{
		"chapter": chapter,
		"outcome": fmt.Sprintf("projected outcome %d", chapter),
	})
	if err != nil {
		t.Fatal(err)
	}
	postStateRoot, err := domain.DeriveProjectedStateRoot(
		chapter,
		fingerprint.GenerationID,
		fingerprint.BaseCanonRoot,
		fingerprint.RootSHA256,
		preStateRoot,
		projectionRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	return domain.StagedChapterPlanManifest{
		Version:               domain.PlanningStoreVersion,
		Chapter:               chapter,
		Volume:                1,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonChapter:      0,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		Authority:             domain.PlanningAuthoritySpeculative,
		Realization:           domain.PlanningRealizationStaged,
		PlanPath:              fmt.Sprintf("meta/planning/chapter_payloads/%06d.json", chapter),
		PlanSHA256:            fmt.Sprintf("plan-sha-%d", chapter),
		ProjectedState: domain.ProjectedStateReceipt{
			Version:        domain.PlanningStoreVersion,
			Chapter:        chapter,
			GenerationID:   fingerprint.GenerationID,
			BaseCanonRoot:  fingerprint.BaseCanonRoot,
			DependencyRoot: fingerprint.RootSHA256,
			Authority:      domain.PlanningAuthorityProjected,
			Realization:    domain.PlanningRealizationStaged,
			PreStateRoot:   preStateRoot,
			ProjectionRoot: projectionRoot,
			PostStateRoot:  postStateRoot,
		},
	}
}

func storePlanningInvalidation(fingerprint domain.DependencyFingerprint, id, kind, target, root string) domain.PlanningInvalidationRecord {
	return domain.PlanningInvalidationRecord{
		Version:               domain.PlanningStoreVersion,
		ID:                    id,
		GenerationID:          fingerprint.GenerationID,
		BaseCanonRoot:         fingerprint.BaseCanonRoot,
		DependencyFingerprint: fingerprint,
		TargetKind:            kind,
		TargetID:              target,
		InvalidatedRoot:       root,
		Reason:                "dependency changed",
		CreatedAt:             "2026-07-16T00:00:00Z",
	}
}

func snapshotPlanningProtectedPaths(t *testing.T, root string, rels []string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, rel := range rels {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat protected path %s: %v", rel, err)
		}
		if !info.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			out[rel] = string(raw)
			continue
		}
		if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			key, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out[key] = string(raw)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}
