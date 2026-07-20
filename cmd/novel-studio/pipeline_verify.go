package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → architect → zero-init → write → review → rewrite → deliver（默认不含 cocreate）。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/reviewreport"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func verifyPipelineStage(stage, outputDir string, flags pipelineFlags, state *domain.PipelineState) (evidence domain.PipelineStageEvidence, returnErr error) {
	evidence = domain.PipelineStageEvidence{
		Stage:     stage,
		Status:    "verified",
		CheckedAt: time.Now(),
	}
	switch stage {
	case "zero-init", "preplan", "project-all", "seal", "promote", "render":
		_, releaseControl, err := acquirePublishedOutlineAllStageAtOutput(outputDir)
		if err != nil {
			return evidence, fmt.Errorf("%s verifier requires published outline-all: %w", stage, err)
		}
		defer releasePublishedOutlineAllStage(releaseControl, stage+" verifier", &returnErr)
	}
	switch stage {
	case "cocreate":
		if strings.TrimSpace(state.Prompt) == "" {
			evidence.Missing = []string{"prompt"}
			return evidence, fmt.Errorf("cocreate 未产出创作指令")
		}
		evidence.Message = "prompt captured"
	case "architect":
		return verifyPipelineArchitectStage(outputDir, evidence)
	case "outline-all":
		return verifyPipelineOutlineAllStage(outputDir, evidence)
	case "zero-init":
		return verifyPipelineZeroInitStage(outputDir, evidence)
	case "preplan":
		return verifyPipelinePreplanStage(outputDir, evidence)
	case "project-all":
		return verifyPipelineProjectAllStage(outputDir, evidence)
	case "seal":
		return verifyPipelineSealStage(outputDir, evidence)
	case "promote":
		return verifyPipelinePromoteStage(outputDir, evidence)
	case "plan":
		return verifyPipelinePlanStage(outputDir, evidence)
	case "render":
		return verifyPipelineRenderStage(outputDir, evidence)
	case "write":
		return verifyPipelineWriteStage(outputDir, flags, evidence)
	case "review":
		return verifyPipelineReviewStage(outputDir, flags, evidence)
	case "rewrite":
		return verifyPipelineRewriteStage(outputDir, flags, evidence)
	case "finalize":
		return verifyPipelineFinalizeStage(outputDir, evidence)
	case "deliver":
		return verifyPipelineDeliverStage(outputDir, flags, evidence)
	default:
		return evidence, fmt.Errorf("未知阶段：%s", stage)
	}
	return evidence, nil
}

func verifyPipelineArchitectStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	for _, rel := range []string{
		"premise.md",
		"characters.json",
		"world_rules.json",
		"book_world.json",
		"world_codex.json",
		filepath.Join("meta", "compass.json"),
	} {
		if nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(rel))) {
			evidence.Artifacts = append(evidence.Artifacts, filepath.ToSlash(rel))
		}
	}
	if nonEmptyFile(filepath.Join(outputDir, "layered_outline.json")) {
		evidence.Artifacts = append(evidence.Artifacts, "layered_outline.json")
	} else if nonEmptyFile(filepath.Join(outputDir, "outline.json")) {
		evidence.Artifacts = append(evidence.Artifacts, "outline.json")
	}
	if missing := tools.FoundationCoreMissing(outputDir); len(missing) > 0 {
		evidence.Missing = append(evidence.Missing, missing...)
		sort.Strings(evidence.Missing)
		return evidence, fmt.Errorf("architect 阶段 foundation 未齐：%s", strings.Join(evidence.Missing, ", "))
	}
	for _, rel := range []string{
		filepath.Join("meta", "architect_readiness.json"),
		filepath.Join("meta", "architect_readiness.md"),
	} {
		if nonEmptyFile(filepath.Join(outputDir, rel)) {
			evidence.Artifacts = append(evidence.Artifacts, filepath.ToSlash(rel))
		} else {
			evidence.Missing = append(evidence.Missing, filepath.ToSlash(rel))
		}
	}
	if ok, reason := architectReadinessState(outputDir); !ok {
		sort.Strings(evidence.Missing)
		return evidence, fmt.Errorf("architect 阶段 readiness 未通过：%s", reason)
	}
	if len(evidence.Missing) > 0 {
		sort.Strings(evidence.Missing)
		return evidence, fmt.Errorf("architect 阶段缺少 readiness 产物：%s", strings.Join(evidence.Missing, ", "))
	}
	evidence.Message = "foundation ready"
	return evidence, nil
}

func verifyPipelineZeroInitStage(outputDir string, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	st := store.NewStore(outputDir)
	firstWritePending := tools.ChapterOnePendingFirstWrite(st)
	if !firstWritePending && !pipelineZeroInitEvidenceExists(outputDir) {
		evidence.Message = "zero-init skipped after chapter one (legacy project)"
		return evidence, nil
	}
	for _, rel := range []string{
		filepath.Join("meta", "first_chapter_generation_readiness.json"),
		filepath.Join("meta", "first_chapter_generation_readiness.md"),
		filepath.Join("meta", "ch01_zero_init_plan.md"),
	} {
		if nonEmptyFile(filepath.Join(outputDir, rel)) {
			evidence.Artifacts = append(evidence.Artifacts, filepath.ToSlash(rel))
		} else {
			evidence.Missing = append(evidence.Missing, filepath.ToSlash(rel))
		}
	}
	if !firstWritePending {
		// world_tick/world_events are live ledgers. Their advancement proves
		// writing happened; it must not stale the immutable fact that zero-init
		// was completed before chapter one. Keep only the static readiness files
		// in stage hash evidence and let later stages validate current ledgers.
		if len(evidence.Missing) > 0 {
			sort.Strings(evidence.Missing)
			return evidence, fmt.Errorf("zero-init historical readiness artifacts are missing: %s", strings.Join(evidence.Missing, ", "))
		}
		evidence.Message = "zero-init completed before chapter one; live world ledgers have advanced"
		return evidence, nil
	}
	if firstWritePending {
		if ok, reason := pipelineCurrentZeroInitReadinessState(outputDir); !ok {
			sort.Strings(evidence.Missing)
			return evidence, fmt.Errorf("zero-init 阶段 readiness 未就绪：%s", reason)
		}
	}
	if err := verifyPipelineZeroInitWorldTick(st); err != nil {
		evidence.Missing = append(evidence.Missing, filepath.ToSlash(filepath.Join("meta", "world_tick.json")), filepath.ToSlash(filepath.Join("meta", "world_events.jsonl")))
		sort.Strings(evidence.Missing)
		return evidence, fmt.Errorf("zero-init 阶段初始 world_tick 未就绪：%w", err)
	}
	if len(evidence.Missing) > 0 {
		sort.Strings(evidence.Missing)
		return evidence, fmt.Errorf("zero-init 阶段缺少产物：%s", strings.Join(evidence.Missing, ", "))
	}
	evidence.Message = "zero-init ready"
	return evidence, nil
}

func pipelineZeroInitEvidenceExists(outputDir string) bool {
	for _, rel := range []string{
		filepath.Join("meta", "first_chapter_generation_readiness.json"),
		filepath.Join("meta", "first_chapter_generation_readiness.md"),
		filepath.Join("meta", "ch01_zero_init_plan.md"),
		filepath.Join("drafts", "01.zero_init.plan.json"),
		filepath.Join("meta", "world_tick.json"),
		filepath.Join("meta", "world_events.jsonl"),
	} {
		if nonEmptyFile(filepath.Join(outputDir, rel)) {
			return true
		}
	}
	return false
}

func verifyPipelineZeroInitWorldTick(st *store.Store) error {
	if tools.ChapterOnePendingFirstWrite(st) {
		return tools.EnsureInitialWorldTickForChapterOne(st)
	}
	if !pipelineZeroInitEvidenceExists(st.Dir()) {
		return nil
	}
	if issues := tools.InitialWorldTickQualityIssues(st); len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "；"))
	}
	return nil
}

func verifyPipelineWriteStage(outputDir string, flags pipelineFlags, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	st := store.NewStore(outputDir)
	progress, err := st.Progress.Load()
	if err != nil {
		return evidence, fmt.Errorf("读取 progress 失败: %w", err)
	}
	if progress == nil {
		evidence.Missing = append(evidence.Missing, "meta/progress.json")
		return evidence, fmt.Errorf("缺少 meta/progress.json")
	}
	evidence.ProgressPhase = string(progress.Phase)
	evidence.ProgressFlow = string(progress.Flow)
	evidence.CompletedChapters = len(progress.CompletedChapters)
	completedSet := make(map[int]struct{}, len(progress.CompletedChapters))
	for _, ch := range progress.CompletedChapters {
		completedSet[ch] = struct{}{}
	}
	if flags.WriteTo > 0 {
		if hasPendingRewriteAtOrBefore(progress, flags.WriteTo) {
			return evidence, fmt.Errorf("write 阶段仍有待返工章节未完成：pending_rewrites=%v", progress.PendingRewrites)
		}
		if _, ok := completedSet[flags.WriteTo]; !ok {
			return evidence, fmt.Errorf("write 阶段尚未写到 --write-to=%d：completed=%d/%d", flags.WriteTo, len(progress.CompletedChapters), progress.TotalChapters)
		}
		evidence.Message = fmt.Sprintf("write reached chapter %d", flags.WriteTo)
	} else if progress.Phase != domain.PhaseComplete {
		return evidence, fmt.Errorf("write 阶段未完结：phase=%s completed=%d/%d", progress.Phase, len(progress.CompletedChapters), progress.TotalChapters)
	}
	if len(progress.CompletedChapters) == 0 {
		return evidence, fmt.Errorf("write 阶段没有任何 completed_chapters")
	}

	chaptersToVerify := append([]int(nil), progress.CompletedChapters...)
	if flags.WriteTo > 0 {
		chaptersToVerify = chaptersToVerify[:0]
		for ch := 1; ch <= flags.WriteTo; ch++ {
			if _, ok := completedSet[ch]; !ok {
				evidence.Missing = append(evidence.Missing, fmt.Sprintf("completed_chapter:%d", ch))
				continue
			}
			chaptersToVerify = append(chaptersToVerify, ch)
		}
	}

	for _, ch := range chaptersToVerify {
		rel := fmt.Sprintf("chapters/%02d.md", ch)
		if !nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(rel))) {
			evidence.Missing = append(evidence.Missing, rel)
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, rel)
		cp := latestCommitCheckpoint(st, ch)
		if cp == nil {
			if flags.WriteTo > 0 {
				evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:legacy-file", ch))
				continue
			}
			evidence.Missing = append(evidence.Missing, fmt.Sprintf("checkpoint:chapter:%d:commit_chapter", ch))
			continue
		}
		evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:%s#%d", ch, cp.Step, cp.Seq))
	}
	sort.Strings(evidence.Missing)
	if len(evidence.Missing) > 0 {
		return evidence, fmt.Errorf("write 阶段缺少完成证据: %s", strings.Join(evidence.Missing, ", "))
	}
	return evidence, nil
}

func hasPendingRewriteAtOrBefore(progress *domain.Progress, chapter int) bool {
	if progress == nil || chapter <= 0 {
		return false
	}
	for _, pending := range progress.PendingRewrites {
		if pending > 0 && pending <= chapter {
			return true
		}
	}
	return false
}

func latestCommitCheckpoint(st *store.Store, chapter int) *domain.Checkpoint {
	scope := domain.ChapterScope(chapter)
	if cp := st.Checkpoints.LatestByStep(scope, "commit_chapter"); cp != nil {
		return cp
	}
	return st.Checkpoints.LatestByStep(scope, "commit")
}

func verifyPipelineReviewStage(outputDir string, flags pipelineFlags, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return evidence, err
	}
	if len(chapters) == 0 {
		return evidence, fmt.Errorf("review 阶段找不到 chapters/*.md")
	}
	chapters = filterChaptersForPipelineRange(chapters, flags)
	if len(chapters) == 0 {
		return evidence, fmt.Errorf("review 阶段指定范围内没有章节")
	}
	hashes := make(map[int]string, len(chapters))
	progress, _ := store.NewStore(outputDir).Progress.Load()
	for _, ch := range chapters {
		current := inspectCurrentChapterReview(outputDir, ch)
		hashes[ch] = current.BodySHA256
		evidence.Artifacts = append(evidence.Artifacts, current.Artifacts...)
		evidence.Missing = append(evidence.Missing, current.Issues...)
		if current.Disposition == "是" && (progress == nil || !slices.Contains(progress.PendingRewrites, ch)) {
			evidence.Missing = append(evidence.Missing, fmt.Sprintf("meta/progress.json (ch%02d blocking review not queued)", ch))
		}
	}
	if summaryRel, issues := inspectReviewSummaryCurrent(outputDir, chapters, hashes); summaryRel != "" {
		evidence.Artifacts = append(evidence.Artifacts, summaryRel)
		evidence.Missing = append(evidence.Missing, issues...)
	} else {
		evidence.Missing = append(evidence.Missing, issues...)
	}
	sort.Strings(evidence.Missing)
	if len(evidence.Missing) > 0 {
		return evidence, fmt.Errorf("review 阶段缺少或存在过期评审产物: %s", strings.Join(evidence.Missing, ", "))
	}
	evidence.CompletedChapters = len(chapters)
	return evidence, nil
}

func verifyPipelineRewriteStage(outputDir string, flags pipelineFlags, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return evidence, err
	}
	if len(chapters) == 0 {
		return evidence, fmt.Errorf("rewrite 阶段找不到 chapters/*.md")
	}
	chapters = filterChaptersForPipelineRange(chapters, flags)
	if len(chapters) == 0 {
		return evidence, fmt.Errorf("rewrite 阶段指定范围内没有章节")
	}
	for _, ch := range chapters {
		currentReview := inspectCurrentChapterReview(outputDir, ch)
		if len(currentReview.Issues) > 0 {
			evidence.Missing = append(evidence.Missing, currentReview.Issues...)
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, currentReview.Artifacts...)
		chapterRel := fmt.Sprintf("chapters/%02d.md", ch)
		backupRel := fmt.Sprintf("chapters/%02d.md.pre-rewrite.md", ch)
		briefRel := fmt.Sprintf("reviews/%02d_rewrite_brief.md", ch)
		chapterPath := filepath.Join(outputDir, filepath.FromSlash(chapterRel))
		if !nonEmptyFile(chapterPath) {
			evidence.Missing = append(evidence.Missing, chapterRel)
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, chapterRel)
		if flags.RewriteBriefOnly {
			if !nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(briefRel))) {
				evidence.Missing = append(evidence.Missing, briefRel)
				continue
			}
			evidence.Artifacts = append(evidence.Artifacts, briefRel)
			evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:rewrite-brief-only", ch))
			continue
		}
		body, _ := os.ReadFile(chapterPath)
		if cp := latestRewriteResolutionCheckpoint(store.NewStore(outputDir), ch); cp != nil &&
			cp.Digest == "sha256:"+reviewreport.BodySHA256(string(body)) {
			evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:%s#%d", ch, cp.Step, cp.Seq))
			continue
		}
		plan := buildRevisionPlan(outputDir, ch, string(body), "")
		if !plan.HasRed && !(flags.PolishWarnings && plan.HasYellow) {
			if cp := latestRewriteResolutionCheckpoint(store.NewStore(outputDir), ch); cp != nil {
				evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:%s#%d", ch, cp.Step, cp.Seq))
			} else if nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(backupRel))) {
				evidence.Artifacts = append(evidence.Artifacts, backupRel)
			} else {
				evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:rewrite-not-needed", ch))
			}
			continue
		}
		if !nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(backupRel))) {
			evidence.Missing = append(evidence.Missing, backupRel)
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, backupRel)
	}
	sort.Strings(evidence.Missing)
	if len(evidence.Missing) > 0 {
		return evidence, fmt.Errorf("rewrite 阶段缺少重写证据或当前审核: %s", strings.Join(evidence.Missing, ", "))
	}
	evidence.CompletedChapters = len(chapters)
	return evidence, nil
}

func latestRewriteResolutionCheckpoint(st *store.Store, chapter int) *domain.Checkpoint {
	scope := domain.ChapterScope(chapter)
	for _, step := range []string{"causal-rewrite", "rewrite-existing", "rewrite-not-needed", "rewrite-brief-only"} {
		if cp := st.Checkpoints.LatestByStep(scope, step); cp != nil {
			return cp
		}
	}
	return nil
}

func filterChaptersForPipelineRange(chapters []int, flags pipelineFlags) []int {
	if flags.Start <= 0 && flags.End <= 0 {
		return chapters
	}
	start := flags.Start
	if start <= 0 {
		start = 1
	}
	end := flags.End
	if end <= 0 {
		end = chapters[len(chapters)-1]
	}
	filtered := chapters[:0]
	for _, ch := range chapters {
		if ch >= start && ch <= end {
			filtered = append(filtered, ch)
		}
	}
	return filtered
}

func verifyPipelineDeliverStage(outputDir string, flags pipelineFlags, evidence domain.PipelineStageEvidence) (domain.PipelineStageEvidence, error) {
	if err := requirePipelineFinalizedShortBook(outputDir); err != nil {
		return evidence, err
	}
	chapters, err := chapterNumbersFromFiles(filepath.Join(outputDir, "chapters"))
	if err != nil {
		return evidence, err
	}
	chapters = filterChaptersForPipelineRange(chapters, flags)
	if len(chapters) == 0 {
		return evidence, fmt.Errorf("deliver 阶段指定范围内没有章节")
	}
	for _, chapter := range chapters {
		current := inspectCurrentChapterReview(outputDir, chapter)
		evidence.Artifacts = append(evidence.Artifacts, current.Artifacts...)
		evidence.Missing = append(evidence.Missing, current.Issues...)
		evidence.Missing = append(evidence.Missing, currentRegisteredExternalDeliveryIssues(outputDir, chapter)...)
		if len(current.Issues) == 0 && current.Verdict != "accept" {
			evidence.Missing = append(evidence.Missing, fmt.Sprintf("reviews/%02d.json (verdict=%s, want accept)", chapter, current.Verdict))
		}
		if len(current.Issues) == 0 && (current.Disposition == "是" || current.Disposition == "待定") {
			evidence.Missing = append(evidence.Missing, fmt.Sprintf("reviews/%02d.md (rewrite disposition=%s)", chapter, current.Disposition))
		}
	}
	for _, rel := range []string{
		filepath.Join("meta", "delivery_log.jsonl"),
		filepath.Join("meta", "delivery_log.md"),
	} {
		if nonEmptyFile(filepath.Join(outputDir, rel)) {
			evidence.Artifacts = append(evidence.Artifacts, filepath.ToSlash(rel))
		} else {
			evidence.Missing = append(evidence.Missing, filepath.ToSlash(rel))
		}
	}
	if len(evidence.Missing) > 0 {
		return evidence, fmt.Errorf("deliver 阶段缺少交付沉淀产物: %s", strings.Join(evidence.Missing, ", "))
	}
	return evidence, nil
}

type pipelineDeliverySnapshot struct {
	Version                  int                        `json:"version"`
	DeliveredAt              string                     `json:"delivered_at"`
	Chapter                  int                        `json:"chapter"`
	ReviewVerdict            string                     `json:"review_verdict,omitempty"`
	ReviewSummary            string                     `json:"review_summary,omitempty"`
	ChapterProgressRefreshed bool                       `json:"chapter_progress_refreshed"`
	ProjectProgressRefreshed bool                       `json:"project_progress_refreshed"`
	EvolutionReportRefreshed bool                       `json:"evolution_report_refreshed"`
	RAGFactChunkPresent      bool                       `json:"rag_fact_chunk_present"`
	RAGFactChunkAdded        bool                       `json:"rag_fact_chunk_added"`
	Completion               *pipelineChapterCompletion `json:"completion,omitempty"`
	Artifacts                []string                   `json:"artifacts,omitempty"`
}

type pipelineChapterCompletion struct {
	Version                        int                         `json:"version"`
	GeneratedAt                    string                      `json:"generated_at"`
	Chapter                        int                         `json:"chapter"`
	SummarySource                  string                      `json:"summary_source,omitempty"`
	Summary                        *domain.ChapterSummary      `json:"summary,omitempty"`
	TimelineProgress               []domain.TimelineEvent      `json:"timeline_progress,omitempty"`
	StateChanges                   []domain.StateChange        `json:"state_changes,omitempty"`
	ProtagonistChanges             []domain.StateChange        `json:"protagonist_changes,omitempty"`
	CharacterStateRecommendations  []domain.CharacterHint      `json:"character_state_recommendations,omitempty"`
	ResourceLedgerUpdates          []domain.ResourceClaim      `json:"resource_ledger_updates,omitempty"`
	ResourceLedgerRecommendations  []domain.ResourceClaim      `json:"resource_ledger_recommendations,omitempty"`
	ResourceFocus                  []domain.ResourceClaim      `json:"resource_focus,omitempty"`
	WorldRuleProgress              []pipelineWorldRuleProgress `json:"world_rule_progress,omitempty"`
	DynamicOutlineRecommendation   *pipelineNextPlanDigest     `json:"dynamic_outline_recommendation,omitempty"`
	PlanningRecommendations        []string                    `json:"planning_recommendations,omitempty"`
	ProjectPlanningRecommendations []string                    `json:"project_planning_recommendations,omitempty"`
	EvolutionRecommendations       []string                    `json:"evolution_recommendations,omitempty"`
	RAG                            pipelineRAGCompletion       `json:"rag"`
	ArtifactRefs                   []string                    `json:"artifact_refs,omitempty"`
}

type pipelineWorldRuleProgress struct {
	Category string   `json:"category,omitempty"`
	Rule     string   `json:"rule"`
	Boundary string   `json:"boundary,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	Source   string   `json:"source"`
}

type pipelineNextPlanDigest struct {
	Chapter          int                    `json:"chapter"`
	Title            string                 `json:"title,omitempty"`
	Position         domain.ChapterPosition `json:"position,omitempty"`
	CoreEvent        string                 `json:"core_event,omitempty"`
	Hook             string                 `json:"hook,omitempty"`
	RequiredBeats    []string               `json:"required_beats,omitempty"`
	ContinuityInputs []string               `json:"continuity_inputs,omitempty"`
}

type pipelineRAGCompletion struct {
	Present     bool   `json:"present"`
	Added       bool   `json:"added"`
	SourcePath  string `json:"source_path,omitempty"`
	SourceKind  string `json:"source_kind,omitempty"`
	UsesBody    bool   `json:"uses_body"`
	GeneratedAt string `json:"generated_at,omitempty"`
}
