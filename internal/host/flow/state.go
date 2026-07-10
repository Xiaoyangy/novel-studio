package flow

import (
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
)

// chapterPlanReadyForDraft 判断某章的写前计划是否已就绪、可直接交 drafter 渲染。
//   - 新章：drafts/NN.plan.json 存在即就绪。
//   - 返工章：计划还必须已纳入 rewrite_brief（context_sources 含 rewrite_brief），
//     否则说明尚未按审阅结论重推演，应先派 planner 重做计划。
func chapterPlanReadyForDraft(store *storepkg.Store, chapter int, isRewrite bool) bool {
	plan, err := store.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return false
	}
	var preferenceText string
	if snapshot, loadErr := store.UserRules.Load(); loadErr == nil && snapshot != nil {
		preferenceText = snapshot.Structured.Genre + "\n" + snapshot.Preferences
	}
	requireLongform := false
	if chapter == 1 {
		if progress, loadErr := store.Progress.Load(); loadErr == nil && progress != nil && progress.TotalChapters >= 50 {
			requireLongform = true
		}
		if strings.Contains(preferenceText, "长篇") || strings.Contains(preferenceText, "万字") {
			requireLongform = true
		}
	}
	if !domain.ChapterAttractionPlanReady(
		*plan,
		domain.TrendLanguageRequested(preferenceText),
		domain.ReaderEntertainmentRequested(preferenceText),
		requireLongform,
		domain.SystemCompanionVoiceRequested(preferenceText),
	) {
		return false
	}
	if !isRewrite {
		return true
	}
	for _, src := range plan.CausalSimulation.ContextSources {
		if strings.Contains(src, "rewrite_brief") {
			return true
		}
	}
	return false
}

// LoadState 从 Store 读取 Route 所需的全部事实。
// 这是路由的"IO 边界"：所有读取集中在这里，Route 保持纯。
// 读取失败按保守默认填充（has*=false, boundary=nil），让 Router 倾向重派而非跳过。
func LoadState(store *storepkg.Store) State {
	s := State{
		FoundationMissing: store.FoundationMissing(),
	}
	progress, err := store.Progress.Load()
	if err != nil || progress == nil {
		return s
	}
	s.Progress = progress

	if n := len(progress.CompletedChapters); n > 0 {
		s.LastCompleted = progress.CompletedChapters[n-1]
		s.HasChapterReview = store.World.HasAcceptedChapterReview(s.LastCompleted)
		s.UnreviewedChapter = store.World.FirstUnacceptedChapterReview(progress.CompletedChapters)
	}

	// 阶段拆分：判断下一个要处理章节的计划是否已就绪可渲染。
	if len(progress.PendingRewrites) > 0 {
		target := progress.PendingRewrites[0]
		s.NextActionPlanReady = chapterPlanReadyForDraft(store, target, true)
		loadNextActionPlanStage(store, target, &s)
		loadNextActionOutline(store, target, &s)
	} else if next := progress.NextChapter(); next > 0 {
		s.NextActionPlanReady = chapterPlanReadyForDraft(store, next, false)
		loadNextActionPlanStage(store, next, &s)
		loadNextActionOutline(store, next, &s)
	}

	s.BookCompleteByChapters = domain.StructurallyComplete(progress)
	if s.BookCompleteByChapters && !progress.Layered {
		meta, _ := store.RunMeta.Load()
		s.NeedsFinalGlobalReview = domain.RequiresFinalGlobalReview(progress, meta)
		s.HasFinalGlobalReview = store.World.HasAcceptedGlobalReview(progress.LatestCompleted())
	}

	// 弧边界仅在分层模式且有已完成章节时才计算
	if progress.Layered && s.LastCompleted > 0 {
		if boundary, berr := store.Outline.CheckArcBoundary(s.LastCompleted); berr == nil && boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview = store.World.HasArcReview(s.LastCompleted)
				s.HasArcSummary = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary = store.Summaries.HasVolumeSummary(boundary.Volume)
				}
			}
		}
	}

	return s
}

func loadNextActionPlanStage(store *storepkg.Store, chapter int, state *State) {
	if store == nil || state == nil || chapter <= 0 {
		return
	}
	if partial, err := store.Drafts.LoadChapterPlanPartial(chapter); err == nil && partial != nil {
		state.NextActionPlanPartial = true
	}
	if meta, err := store.RunMeta.Load(); err == nil && meta != nil && strings.HasPrefix(strings.TrimSpace(meta.PendingSteer), "Pipeline staged-plan repair") {
		state.NextActionPlanRepairTask = strings.TrimSpace(meta.PendingSteer)
	}
}

func loadNextActionOutline(store *storepkg.Store, chapter int, state *State) {
	if store == nil || state == nil || chapter <= 0 {
		return
	}
	entry, err := store.Outline.GetChapterOutline(chapter)
	if err != nil || entry == nil {
		return
	}
	state.NextActionTitle = entry.Title
	state.NextActionCoreEvent = entry.CoreEvent
	state.NextActionHook = entry.Hook
}
