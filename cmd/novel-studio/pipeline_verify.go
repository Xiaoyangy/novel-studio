package main

// --pipeline：把各功能串成一条可恢复的流水线，按阶段顺序执行。
// 阶段：cocreate → write → review → rewrite → deliver（默认不含 cocreate）。
// 状态持久化到 meta/pipeline.json：已完成的阶段在重跑时自动跳过，从断点继续。
//
// 设计：流水线只做"阶段编排 + 断点续跑"，每个阶段复用已有子命令逻辑（headless.Run /
// reviewExistingPipeline / ...）。阶段内部各自还有更细的恢复（write 走 checkpoint、
// review/rewrite 按章号），两层恢复叠加。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func verifyPipelineStage(stage, outputDir string, flags pipelineFlags, state *domain.PipelineState) (domain.PipelineStageEvidence, error) {
	evidence := domain.PipelineStageEvidence{
		Stage:     stage,
		Status:    "verified",
		CheckedAt: time.Now(),
	}
	switch stage {
	case "cocreate":
		if strings.TrimSpace(state.Prompt) == "" {
			evidence.Missing = []string{"prompt"}
			return evidence, fmt.Errorf("cocreate 未产出创作指令")
		}
		evidence.Message = "prompt captured"
	case "write":
		return verifyPipelineWriteStage(outputDir, flags, evidence)
	case "review":
		return verifyPipelineReviewStage(outputDir, flags, evidence)
	case "rewrite":
		return verifyPipelineRewriteStage(outputDir, flags, evidence)
	case "deliver":
		return verifyPipelineDeliverStage(outputDir, flags, evidence)
	default:
		return evidence, fmt.Errorf("未知阶段：%s", stage)
	}
	return evidence, nil
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
	for _, ch := range chapters {
		rel := fmt.Sprintf("reviews/%02d.md", ch)
		if !nonEmptyFile(filepath.Join(outputDir, filepath.FromSlash(rel))) {
			evidence.Missing = append(evidence.Missing, rel)
			continue
		}
		evidence.Artifacts = append(evidence.Artifacts, rel)
	}
	if !nonEmptyFile(filepath.Join(outputDir, "meta", "review-summary.md")) {
		evidence.Missing = append(evidence.Missing, "meta/review-summary.md")
	} else {
		evidence.Artifacts = append(evidence.Artifacts, "meta/review-summary.md")
	}
	sort.Strings(evidence.Missing)
	if len(evidence.Missing) > 0 {
		return evidence, fmt.Errorf("review 阶段缺少评审产物: %s", strings.Join(evidence.Missing, ", "))
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
	for _, ch := range chapters {
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
		plan := buildRevisionPlan(outputDir, ch, string(body), "")
		if !plan.HasRed && !(flags.PolishWarnings && plan.HasYellow) {
			evidence.Checkpoints = append(evidence.Checkpoints, fmt.Sprintf("chapter:%d:rewrite-not-needed", ch))
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
		return evidence, fmt.Errorf("rewrite 阶段缺少重写证据: %s", strings.Join(evidence.Missing, ", "))
	}
	evidence.CompletedChapters = len(chapters)
	return evidence, nil
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
