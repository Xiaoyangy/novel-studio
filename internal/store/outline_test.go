package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func stableLayeredFixture() []domain.VolumeOutline {
	return []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "稳定章号",
		Arcs: []domain.ArcOutline{
			{
				Index: 1, Title: "前弧", Goal: "铺垫",
				Chapters: []domain.OutlineEntry{
					{Title: "前一", CoreEvent: "前一事件"},
					{Title: "前二", CoreEvent: "前二事件"},
				},
			},
			{Index: 2, Title: "中弧", Goal: "待展开", EstimatedChapters: 3},
			{
				Index: 3, Title: "后弧", Goal: "承接",
				Chapters: []domain.OutlineEntry{
					{Title: "后一", CoreEvent: "后一事件"},
					{Title: "后二", CoreEvent: "后二事件"},
				},
			},
		},
	}}
}

func setupLayered(t *testing.T, volumes []domain.VolumeOutline) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}
	return s
}

func TestCheckArcBoundaryNeedsNewVolume(t *testing.T) {
	// 只有 1 卷 1 弧 1 章，且非 Final → 应触发 NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "起步",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "首弧", Goal: "目标",
			Chapters: []domain.OutlineEntry{{Title: "第一章", CoreEvent: "开局", Hook: "继续"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1) // 第 1 章 = 弧/卷最后一章
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if b == nil {
		t.Fatal("expected boundary, got nil")
	}
	if !b.IsArcEnd || !b.IsVolumeEnd {
		t.Fatalf("expected arc+volume end, got arc=%v vol=%v", b.IsArcEnd, b.IsVolumeEnd)
	}
	if !b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=true")
	}
	if b.NextVolume != 0 || b.NextArc != 0 {
		t.Fatalf("expected no next, got vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
}

func TestCheckArcBoundaryLastVolumeRequiresDecision(t *testing.T) {
	// 单卷最后一章 → 触发 NeedsNewVolume，让 Router 让架构师二选一：
	// append_volume 续写 / complete_book 收尾。
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "唯一卷", Theme: "主题",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "唯一弧", Goal: "收束",
			Chapters: []domain.OutlineEntry{{Title: "终章", CoreEvent: "结局", Hook: "无"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=true at last expanded chapter")
	}
	if b.HasNextArc() {
		t.Fatal("expected no next arc")
	}
}

func TestCheckArcBoundaryNextArcInSameVolume(t *testing.T) {
	// 2 弧：第 1 弧结束应指向第 2 弧，不触发 NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "起步",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "首弧", Goal: "目标", Chapters: []domain.OutlineEntry{{Title: "章一", CoreEvent: "事件", Hook: "钩子"}}},
			{Index: 2, Title: "次弧", Goal: "目标2", EstimatedChapters: 10},
		},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.IsArcEnd {
		t.Fatal("expected arc end")
	}
	if b.IsVolumeEnd {
		t.Fatal("expected not volume end (second arc exists)")
	}
	if b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=false")
	}
	if b.NextVolume != 1 || b.NextArc != 2 {
		t.Fatalf("expected next vol=1 arc=2, got vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
	if !b.NeedsExpansion {
		t.Fatal("expected NeedsExpansion=true for skeleton arc")
	}
}

func TestLayeredChapterMappingReservesSkeletonSpanAndSurvivesExpansion(t *testing.T) {
	s := setupLayered(t, stableLayeredFixture())

	if _, err := s.Outline.GetChapterFromLayered(3); err == nil {
		t.Fatal("reserved skeleton chapter 3 should not be writable before expansion")
	}
	entry, err := s.Outline.GetChapterFromLayered(6)
	if err != nil {
		t.Fatalf("GetChapterFromLayered(6): %v", err)
	}
	if entry.Title != "后一" || entry.Chapter != 6 {
		t.Fatalf("chapter 6 = %+v, want later expanded arc first chapter", entry)
	}
	volume, arc, err := s.Outline.LocateChapter(7)
	if err != nil {
		t.Fatalf("LocateChapter(7): %v", err)
	}
	if volume != 1 || arc != 3 {
		t.Fatalf("LocateChapter(7) = volume %d arc %d, want volume 1 arc 3", volume, arc)
	}

	beforeMiddle, err := s.Outline.CheckArcBoundary(2)
	if err != nil {
		t.Fatalf("CheckArcBoundary(2): %v", err)
	}
	if beforeMiddle == nil || !beforeMiddle.IsArcEnd ||
		beforeMiddle.NextVolume != 1 || beforeMiddle.NextArc != 2 ||
		!beforeMiddle.NeedsExpansion {
		t.Fatalf("chapter 2 boundary = %+v, want skeleton arc 2 expansion", beforeMiddle)
	}
	laterBoundary, err := s.Outline.CheckArcBoundary(7)
	if err != nil {
		t.Fatalf("CheckArcBoundary(7): %v", err)
	}
	if laterBoundary == nil || !laterBoundary.IsArcEnd || !laterBoundary.IsVolumeEnd || !laterBoundary.NeedsNewVolume {
		t.Fatalf("chapter 7 boundary = %+v, want final expanded arc boundary", laterBoundary)
	}

	middle := []domain.OutlineEntry{
		{Title: "中一", CoreEvent: "中一事件"},
		{Title: "中二", CoreEvent: "中二事件"},
		{Title: "中三", CoreEvent: "中三事件"},
	}
	if err := s.ExpandArc(1, 2, middle); err != nil {
		t.Fatalf("ExpandArc exact reservation: %v", err)
	}

	flat, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	var chapters []int
	for _, item := range flat {
		chapters = append(chapters, item.Chapter)
	}
	if want := []int{1, 2, 3, 4, 5, 6, 7}; !reflect.DeepEqual(chapters, want) {
		t.Fatalf("expanded chapter numbers = %v, want %v", chapters, want)
	}
	entry, err = s.Outline.GetChapterFromLayered(6)
	if err != nil {
		t.Fatalf("GetChapterFromLayered(6) after expansion: %v", err)
	}
	if entry.Title != "后一" {
		t.Fatalf("later arc shifted after middle expansion: chapter 6 = %q", entry.Title)
	}
}

func TestExpandArcRejectsCountMismatchWithoutRenumberingLaterArc(t *testing.T) {
	volumes := stableLayeredFixture()
	s := setupLayered(t, volumes)
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	err := s.ExpandArc(1, 2, []domain.OutlineEntry{
		{Title: "中一"},
		{Title: "中二"},
	})
	if err == nil || !strings.Contains(err.Error(), "must equal reserved estimated_chapters 3") {
		t.Fatalf("expected explicit reservation mismatch error, got %v", err)
	}

	layered, loadErr := s.Outline.LoadLayeredOutline()
	if loadErr != nil {
		t.Fatalf("LoadLayeredOutline: %v", loadErr)
	}
	if layered[0].Arcs[1].IsExpanded() || layered[0].Arcs[1].EstimatedChapters != 3 {
		t.Fatalf("failed expansion mutated skeleton arc: %+v", layered[0].Arcs[1])
	}
	flat, loadErr := s.Outline.LoadOutline()
	if loadErr != nil {
		t.Fatalf("LoadOutline: %v", loadErr)
	}
	if got := []int{flat[0].Chapter, flat[1].Chapter, flat[2].Chapter, flat[3].Chapter}; !reflect.DeepEqual(got, []int{1, 2, 6, 7}) {
		t.Fatalf("failed expansion renumbered outline: %v", got)
	}
}

func TestAppendVolumeRegenerationPreservesExistingStableChapterNumbers(t *testing.T) {
	volumes := stableLayeredFixture()
	s := setupLayered(t, volumes)
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 2, Title: "第二卷", Theme: "追加",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "新弧", Goal: "继续",
			Chapters: []domain.OutlineEntry{{Title: "新一", CoreEvent: "新事件"}},
		}},
	}); err != nil {
		t.Fatalf("AppendVolume: %v", err)
	}

	flat, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	var chapters []int
	for _, item := range flat {
		chapters = append(chapters, item.Chapter)
	}
	if want := []int{1, 2, 6, 7, 8}; !reflect.DeepEqual(chapters, want) {
		t.Fatalf("chapter numbers after append = %v, want %v", chapters, want)
	}
	if flat[2].Title != "后一" || flat[4].Title != "新一" {
		t.Fatalf("append changed existing mapping: %+v", flat)
	}
}

func TestAppendVolumeValidation(t *testing.T) {
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "第一卷", Theme: "起步",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "首弧", Goal: "目标",
			Chapters: []domain.OutlineEntry{{Title: "章", CoreEvent: "事件", Hook: "钩子"}},
		}},
	}})

	validVol := domain.VolumeOutline{
		Index: 2, Title: "第二卷", Theme: "升级",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "弧一", Goal: "目标",
			Chapters: []domain.OutlineEntry{{Title: "新章", CoreEvent: "推进", Hook: "钩子"}},
		}},
	}

	// 正常追加应成功
	if err := s.AppendVolume(validVol); err != nil {
		t.Fatalf("AppendVolume valid: %v", err)
	}

	// Index 不递增 → 失败
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 1, Title: "重复", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "弧", Goal: "g", Chapters: []domain.OutlineEntry{{Title: "ch", CoreEvent: "e", Hook: "h"}}}},
	}); err == nil {
		t.Fatal("expected error for non-increasing index")
	}

	// 无弧 → 失败
	if err := s.AppendVolume(domain.VolumeOutline{Index: 3, Title: "空", Theme: "x"}); err == nil {
		t.Fatal("expected error for volume with no arcs")
	}

	// 首弧无章节 → 失败
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 3, Title: "骨架", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "弧", Goal: "g", EstimatedChapters: 10}},
	}); err == nil {
		t.Fatal("expected error for first arc without chapters")
	}
}

func TestAppendVolumeSkeletonAndReviseArcPreserveStableChapterSpace(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	base := []domain.VolumeOutline{{Index: 1, Title: "base", Arcs: []domain.ArcOutline{{
		Index: 1, Title: "expanded", Chapters: []domain.OutlineEntry{
			{Title: "one", CoreEvent: "old-one"}, {Title: "two", CoreEvent: "old-two"},
		},
	}}}}
	if err := s.Outline.SaveLayeredOutline(base); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(base)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendVolumeSkeleton(domain.VolumeOutline{Index: 2, Title: "skeleton", Arcs: []domain.ArcOutline{
		{Index: 1, Title: "reserved-a", EstimatedChapters: 12},
		{Index: 2, Title: "reserved-b", EstimatedChapters: 12},
	}}); err != nil {
		t.Fatalf("AppendVolumeSkeleton: %v", err)
	}
	if err := s.ReviseArc(1, 1, []domain.OutlineEntry{
		{Title: "new-one", CoreEvent: "new-one-event"},
		{Title: "new-two", CoreEvent: "new-two-event"},
	}); err != nil {
		t.Fatalf("ReviseArc: %v", err)
	}
	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if got := domain.TotalChapters(volumes); got != 26 {
		t.Fatalf("total=%d want 26", got)
	}
	if volumes[1].Arcs[0].IsExpanded() || volumes[1].Arcs[0].EstimatedChapters != 12 {
		t.Fatalf("skeleton reservation changed: %+v", volumes[1].Arcs[0])
	}
	flat := domain.FlattenOutline(volumes)
	if len(flat) != 2 || flat[0].Chapter != 1 || flat[1].Chapter != 2 || flat[0].Title != "new-one" {
		t.Fatalf("flat=%+v", flat)
	}
	if err := s.ReviseArc(1, 1, []domain.OutlineEntry{{Title: "would-shrink"}}); err == nil {
		t.Fatal("ReviseArc accepted span change")
	}
}

// 注：原先用 Final 卷拒绝 append 的语义已下沉到 save_foundation 层（Phase=Complete 拒绝），
// 见 save_foundation_test.go::TestSaveFoundationAppendVolumeRejectsAfterComplete。
// store 层只保留结构性校验（Index 递增 / 首弧含章节等）。

func TestSaveAndLoadCompass(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 空 direction 应失败
	if err := s.Outline.SaveCompass(domain.StoryCompass{EstimatedScale: "3 卷"}); err == nil {
		t.Fatal("expected error for empty ending_direction")
	}

	// 正常保存
	compass := domain.StoryCompass{
		EndingDirection: "主角面对最终抉择",
		OpenThreads:     []string{"线索A", "关系B"},
		EstimatedScale:  "预计 4-6 卷",
		LastUpdated:     12,
	}
	if err := s.Outline.SaveCompass(compass); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}

	loaded, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected compass, got nil")
	}
	if loaded.EndingDirection != "主角面对最终抉择" {
		t.Fatalf("expected direction %q, got %q", "主角面对最终抉择", loaded.EndingDirection)
	}
	if len(loaded.OpenThreads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(loaded.OpenThreads))
	}
}
