package diag

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

// Snapshot 是对 output 目录全部工件的只读快照。
// 所有规则函数只接收 Snapshot，不直接访问文件系统。
type Snapshot struct {
	Progress      *domain.Progress
	RunMeta       *domain.RunMeta
	Compass       *domain.StoryCompass
	Outline       []domain.OutlineEntry
	Volumes       []domain.VolumeOutline
	Characters    []domain.Character
	CastLedger    []domain.CastEntry
	WorldRules    []domain.WorldRule
	Timeline      []domain.TimelineEvent
	Foreshadow    []domain.ForeshadowEntry
	Relationships []domain.RelationshipEntry
	StateChanges  []domain.StateChange
	StyleRules    *domain.WritingStyleRules
	Reviews       map[int]*domain.ReviewEntry
	Plans         map[int]*domain.ChapterPlan
	Summaries     map[int]*domain.ChapterSummary

	Pipeline                   *domain.PipelineState
	PipelineMissingArtifacts   map[string][]string
	PipelineMissingCheckpoints map[string][]string

	// 世界模拟（离屏推演）工件；未启用的项目保持零值。
	WorldTick       *domain.WorldTick
	OffscreenAgenda domain.OffscreenAgendaLedger
	// 书级 AI 味统计（Task 082）；未生成时 nil。
	BookStylestat map[string]any
	// 读者数据登记（Task 077）；未登记为空。
	ReaderMetrics []ReaderMetricRow
	// 用量累计（Task 080 成本剖面）；未生成为 nil。
	Usage *domain.UsageState

	LoadErrors []string // 非 NotExist 的加载失败，区分"无数据"和"读取出错"
}

// Load 从 store 中读取全部工件，构建只读快照。
// 文件不存在视为"无数据"（字段保持零值）；其他错误记录到 LoadErrors。
func Load(s *store.Store) Snapshot {
	snap := Snapshot{
		Reviews:   make(map[int]*domain.ReviewEntry),
		Plans:     make(map[int]*domain.ChapterPlan),
		Summaries: make(map[int]*domain.ChapterSummary),
	}

	check := func(name string, err error) {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			snap.LoadErrors = append(snap.LoadErrors, fmt.Sprintf("%s: %v", name, err))
		}
	}

	var err error
	snap.Progress, err = s.Progress.Load()
	check("progress", err)
	snap.RunMeta, err = s.RunMeta.Load()
	check("run_meta", err)
	snap.Compass, err = s.Outline.LoadCompass()
	check("compass", err)
	snap.Outline, err = s.Outline.LoadOutline()
	check("outline", err)
	snap.Volumes, err = s.Outline.LoadLayeredOutline()
	check("volumes", err)
	snap.Characters, err = s.Characters.Load()
	check("characters", err)
	snap.CastLedger, err = s.Cast.Load()
	check("cast_ledger", err)
	snap.WorldRules, err = s.World.LoadWorldRules()
	check("world_rules", err)
	snap.Timeline, err = s.World.LoadTimeline()
	check("timeline", err)
	snap.Foreshadow, err = s.World.LoadForeshadowLedger()
	check("foreshadow", err)
	snap.WorldTick, err = s.WorldSim.LoadTick()
	check("world_tick", err)
	snap.OffscreenAgenda, err = s.WorldSim.LoadAgendaLedger()
	check("offscreen_agenda", err)
	snap.BookStylestat, err = s.Methodology.LoadBookStylestatRaw()
	check("book_stylestat", err)
	snap.ReaderMetrics = loadReaderMetrics(s.Dir())
	snap.Usage, err = s.Usage.Load()
	check("usage", err)
	snap.Relationships, err = s.World.LoadRelationships()
	check("relationships", err)
	snap.StateChanges, err = s.World.LoadStateChanges()
	check("state_changes", err)
	snap.StyleRules, err = s.World.LoadStyleRules()
	check("style_rules", err)

	snap.Pipeline, snap.PipelineMissingArtifacts, snap.PipelineMissingCheckpoints, err = loadPipelineEvidence(s)
	check("pipeline", err)

	if snap.Progress != nil {
		for _, ch := range snap.Progress.CompletedChapters {
			if plan, err := s.Drafts.LoadChapterPlan(ch); err == nil && plan != nil {
				snap.Plans[ch] = plan
			} else {
				check(fmt.Sprintf("plan_ch%d", ch), err)
			}
			if summary, err := s.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.Summaries[ch] = summary
			} else {
				check(fmt.Sprintf("summary_ch%d", ch), err)
			}
			if review, err := s.World.LoadReview(ch); err == nil && review != nil {
				snap.Reviews[ch] = review
			} else {
				check(fmt.Sprintf("review_ch%d", ch), err)
			}
		}
	}

	return snap
}

// CompletedCount 返回已完成章节数（安全访问）。
func (s *Snapshot) CompletedCount() int {
	if s.Progress == nil {
		return 0
	}
	return len(s.Progress.CompletedChapters)
}

// LatestCompleted 返回最大已完成章节号；无则返回 0。
func (s *Snapshot) LatestCompleted() int {
	if s.Progress == nil {
		return 0
	}
	max := 0
	for _, ch := range s.Progress.CompletedChapters {
		if ch > max {
			max = ch
		}
	}
	return max
}

// ReaderMetricRow 读者指标登记行（与 reader-metrics CLI 输出对齐）。
type ReaderMetricRow struct {
	Chapter     int     `json:"chapter"`
	Platform    string  `json:"platform,omitempty"`
	ReadThrough float64 `json:"read_through,omitempty"`
	Comments    int     `json:"comments,omitempty"`
	Note        string  `json:"note,omitempty"`
}

func loadReaderMetrics(dir string) []ReaderMetricRow {
	data, err := os.ReadFile(dir + "/meta/reader_metrics.jsonl")
	if err != nil {
		return nil
	}
	var out []ReaderMetricRow
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r ReaderMetricRow
		if json.Unmarshal([]byte(line), &r) == nil && r.Chapter > 0 {
			out = append(out, r)
		}
	}
	return out
}
