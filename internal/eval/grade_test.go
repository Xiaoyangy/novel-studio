package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/diag"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/stylestat"
)

// writerSmokeCase 是一个典型的 writer 第一章 smoke case，用于门禁测试。
func writerSmokeCase() Case {
	c := Case{
		ID:          "writer_first_chapter",
		Category:    "smoke",
		Role:        "writer",
		Prompt:      "写一本修仙小说",
		MaxChapters: 1,
		Expect: Expect{
			Phase:                "writing",
			MinCompletedChapters: 1,
			RequiredCheckpoints:  []string{"chapter:1:plan", "chapter:1:draft", "chapter:1:commit"},
			NoPending:            []string{"pending_commit", "pending_steer"},
		},
	}
	_ = c.Validate() // 填默认 max_severity
	return c
}

// cleanCollected 构造一个"一章正常完成"的采集结果（无 findings、无残留、契约齐备）。
func cleanCollected() Collected {
	return Collected{
		Dir:      "/fake",
		Report:   diag.Report{Stats: diag.Stats{CompletedChapters: 1, TotalChapters: 1, Phase: "writing", Flow: "writing"}},
		Progress: &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}},
		Checkpoints: []domain.Checkpoint{
			{Scope: domain.ChapterScope(1), Step: "plan"},
			{Scope: domain.ChapterScope(1), Step: "draft"},
			{Scope: domain.ChapterScope(1), Step: "commit"},
		},
		Pending: map[string]bool{},
	}
}

func TestGradePassesCleanRun(t *testing.T) {
	r := Grade(writerSmokeCase(), cleanCollected())
	if r.Outcome != Pass {
		t.Fatalf("期望 PASS，得到 %s；hard fails=%v", r.Outcome, r.HardFails)
	}
	if len(r.Passed) == 0 {
		t.Fatal("期望有通过的契约记录")
	}
}

// 核心假设：writer 跳过 commit 必须被拦下。
func TestGradeCatchesMissingCommit(t *testing.T) {
	col := cleanCollected()
	col.Checkpoints = col.Checkpoints[:2] // 去掉 commit
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("缺 commit 应 FAIL，得到 %s", r.Outcome)
	}
	if !hasIssue(r.HardFails, "contract:checkpoint", "chapter:1:commit") {
		t.Fatalf("应报告缺少 chapter:1:commit，实际 %+v", r.HardFails)
	}
}

// 核心假设：pending 残留必须被拦下。
func TestGradeCatchesPendingResidual(t *testing.T) {
	col := cleanCollected()
	col.Pending["pending_commit"] = true
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("pending 残留应 FAIL，得到 %s", r.Outcome)
	}
	if !hasIssue(r.HardFails, "contract:no_pending", "pending_commit") {
		t.Fatalf("应报告 pending_commit 残留，实际 %+v", r.HardFails)
	}
}

// 核心假设：phase 不符必须被拦下。
func TestGradeCatchesPhaseMismatch(t *testing.T) {
	col := cleanCollected()
	col.Progress.Phase = domain.PhaseOutline // 还没进入 writing
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("phase 不符应 FAIL，得到 %s", r.Outcome)
	}
	if !hasIssue(r.HardFails, "contract:phase", "outline") {
		t.Fatalf("应报告 phase 不符，实际 %+v", r.HardFails)
	}
}

func TestGradeMinChaptersNotMet(t *testing.T) {
	col := cleanCollected()
	col.Report.Stats.CompletedChapters = 0
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("未达 min_completed_chapters 应 FAIL，得到 %s", r.Outcome)
	}
}

// critical finding 触发 hard fail；warning finding 仅 WARN（默认 max_severity=warning）。
func TestGradeFindingSeverity(t *testing.T) {
	crit := cleanCollected()
	crit.Report.Findings = []diag.Finding{{Rule: "PhaseFlowMismatch", Severity: diag.SevCritical, Title: "状态机异常"}}
	if r := Grade(writerSmokeCase(), crit); r.Outcome != Fail {
		t.Fatalf("critical finding 应 FAIL，得到 %s", r.Outcome)
	}

	warn := cleanCollected()
	warn.Report.Findings = []diag.Finding{{Rule: "RewritePendingPressure", Severity: diag.SevWarning, Title: "重写积压"}}
	r := Grade(writerSmokeCase(), warn)
	if r.Outcome != Warn {
		t.Fatalf("warning finding 应 WARN，得到 %s", r.Outcome)
	}

	// info finding 是信息性 Note，不应把干净的 case 推成 WARN。
	info := cleanCollected()
	info.Report.Findings = []diag.Finding{{Rule: "GhostCharacter", Severity: diag.SevInfo, Title: "角色长期未出场"}}
	ri := Grade(writerSmokeCase(), info)
	if ri.Outcome != Pass {
		t.Fatalf("info finding 不应改变门禁，期望 PASS，得到 %s", ri.Outcome)
	}
	if len(ri.Notes) != 1 {
		t.Fatalf("info finding 应进 Notes，得到 %d 条", len(ri.Notes))
	}
}

func TestGradeRuntimeErrorFails(t *testing.T) {
	col := cleanCollected()
	col.RuntimeErr = "stream EOF"
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("运行时错误应 FAIL，得到 %s", r.Outcome)
	}
}

// 契约依赖工件读坏不能 false pass，必须 hard fail（fail-loud）。
func TestGradeLoadErrorFails(t *testing.T) {
	col := cleanCollected()
	col.LoadErrors = []string{"pending_commit: unexpected end of JSON input"}
	r := Grade(writerSmokeCase(), col)
	if r.Outcome != Fail {
		t.Fatalf("工件读取失败应 FAIL，得到 %s", r.Outcome)
	}
	if !hasIssue(r.HardFails, "load", "pending_commit") {
		t.Fatalf("应报告 load 失败，实际 %+v", r.HardFails)
	}
}

func TestGradeDeltaStylestatWarnAndBlock(t *testing.T) {
	base := cleanResult()
	base.Metrics.Stylestat = &stylestat.Stats{
		Patterns: []stylestat.PatternStat{{Name: "p", PerChapter: 1}},
		Ending:   stylestat.EndingStat{ShortRatio: 0.2},
	}
	variant := cleanResult()
	variant.Metrics.Stylestat = &stylestat.Stats{
		Patterns:          []stylestat.PatternStat{{Name: "p", PerChapter: 2}},
		RepeatedSentences: []stylestat.SentenceStat{{Text: "重复句", Chapters: 3, Count: 3}},
		Ending:            stylestat.EndingStat{ShortRatio: 0.5},
	}

	c := writerSmokeCase()
	c.Gate.StylestatRegression = "warn"
	d := GradeDelta(c, base, variant)
	if d.Outcome != Warn {
		t.Fatalf("stylestat 回归默认应 WARN，得到 %s", d.Outcome)
	}
	if !hasIssue(d.Warnings, "delta:stylestat", "文体指标回归") {
		t.Fatalf("应报告 stylestat warning，实际 %+v", d.Warnings)
	}

	c.Gate.StylestatRegression = "block"
	d = GradeDelta(c, base, variant)
	if d.Outcome != Fail {
		t.Fatalf("stylestat block 应 FAIL，得到 %s", d.Outcome)
	}
	if !hasIssue(d.HardFails, "delta:stylestat", "文体指标回归") {
		t.Fatalf("应报告 stylestat hard fail，实际 %+v", d.HardFails)
	}
}

func TestGradeDeltaTitleMixedUsesMinorityCount(t *testing.T) {
	base := cleanResult()
	base.Metrics.Stylestat = &stylestat.Stats{
		TitleFormats: &stylestat.TitleStat{WithPrefix: 2, WithoutPrefix: 3},
	}
	variant := cleanResult()
	variant.Metrics.Stylestat = &stylestat.Stats{
		TitleFormats: &stylestat.TitleStat{WithPrefix: 2, WithoutPrefix: 5},
	}

	d := GradeDelta(writerSmokeCase(), base, variant)
	if d.Metrics.Stylestat == nil {
		t.Fatal("应产生 stylestat delta")
	}
	if d.Metrics.Stylestat.TitleMixedDelta != 0 {
		t.Fatalf("少数派格式未增加时不应误报标题混用回归，得到 %+d", d.Metrics.Stylestat.TitleMixedDelta)
	}
	if d.Outcome != Pass {
		t.Fatalf("仅增加多数派标题数量不应改变门禁，得到 %s issues=%+v", d.Outcome, d.Warnings)
	}
}

func TestGradeDeltaCostAndToolCallThresholds(t *testing.T) {
	base := cleanResult()
	base.Metrics.ToolCalls = 10
	base.Metrics.Usage = UsageMetrics{UsageRecorded: true, CostUSD: 1, Input: 100, Output: 100}
	variant := cleanResult()
	variant.Metrics.ToolCalls = 14
	variant.Metrics.Usage = UsageMetrics{UsageRecorded: true, CostUSD: 1.4, Input: 150, Output: 140}

	c := writerSmokeCase()
	c.Gate.MaxToolCallDeltaRatio = float64Ptr(0.3)
	c.Gate.MaxCostDeltaRatio = float64Ptr(0.3)
	d := GradeDelta(c, base, variant)
	if d.Outcome != Warn {
		t.Fatalf("成本/tool_calls 超阈值应 WARN，得到 %s", d.Outcome)
	}
	if !hasIssue(d.Warnings, "delta:tool_calls", "超过阈值") {
		t.Fatalf("应报告 tool_calls 回归，实际 %+v", d.Warnings)
	}
	if !hasIssue(d.Warnings, "delta:cost", "超过阈值") {
		t.Fatalf("应报告 cost 回归，实际 %+v", d.Warnings)
	}
}

func TestGradeDeltaInsufficientStylestatIsNote(t *testing.T) {
	d := GradeDelta(writerSmokeCase(), cleanResult(), cleanResult())
	if d.Outcome != Pass {
		t.Fatalf("样本不足不应改变门禁，得到 %s", d.Outcome)
	}
	if !hasIssue(d.Notes, "stylestat", "样本不足") {
		t.Fatalf("应记录 stylestat 样本不足 note，实际 %+v", d.Notes)
	}
}

func TestGradeRAGContractsPassAndFail(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "chapter_progress.md"), []byte("章节推进"), 0o644); err != nil {
		t.Fatal(err)
	}
	col := cleanCollected()
	col.Dir = dir
	col.RAG = RAGCollection{
		IndexState: &domain.RAGIndexState{
			Config: domain.RAGIndexConfig{
				Collection:        "novel_studio_test",
				VectorStore:       "qdrant",
				EmbeddingProvider: "local",
				EmbeddingModel:    "local-hash-384",
				QdrantURL:         "http://127.0.0.1:6333",
			},
			Chunks: []domain.RAGChunk{
				{ID: "a", SourcePath: "meta/chapter_progress.md", SourceKind: "note", Facet: "project", Hash: "h1"},
				{ID: "b", SourcePath: "summaries/01.json", SourceKind: "chapter_summary_facts", Facet: "character", Hash: "h2"},
			},
		},
		VectorStore: &domain.RAGVectorStore{
			Config: domain.RAGIndexConfig{VectorStore: "qdrant", EmbeddingProvider: "local", EmbeddingModel: "local-hash-384"},
			Points: []domain.RAGVectorPoint{{ID: "a"}, {ID: "b"}},
		},
		QdrantHealthy: true,
		QdrantPoints:  2,
	}
	c := writerSmokeCase()
	c.Expect.RequiredFiles = []string{"meta/chapter_progress.md"}
	c.Expect.MinRAGChunks = 2
	c.Expect.RequiredRAGFacets = []string{"project", "character"}
	c.Expect.RequiredRAGSourceKinds = []string{"note", "chapter_summary_facts"}
	c.Expect.RequiredRAGSources = []string{"meta/chapter_progress.md"}
	c.Expect.ForbiddenRAGSources = []string{"拆文库", "data/runs/另一本书"}
	c.Expect.VectorStore = "qdrant"
	c.Expect.EmbeddingProvider = "local"
	c.Expect.EmbeddingModel = "local-hash-384"
	c.Expect.MinVectorPoints = 2
	c.Expect.RequireQdrant = true
	c.Expect.MinQdrantPoints = 2

	r := Grade(c, col)
	if r.Outcome != Pass {
		t.Fatalf("RAG contract should pass, got %s hard=%+v warn=%+v", r.Outcome, r.HardFails, r.Warnings)
	}

	col.RAG.IndexState.Chunks[0].SourcePath = "拆文库/参考/样本.md"
	r = Grade(c, col)
	if r.Outcome != Fail {
		t.Fatalf("forbidden RAG source should fail, got %s", r.Outcome)
	}
	if !hasIssue(r.HardFails, "contract:rag_forbidden_source", "拆文库") {
		t.Fatalf("expected forbidden source issue, got %+v", r.HardFails)
	}
}

func TestParseCheckpointSpec(t *testing.T) {
	cases := []struct {
		spec  string
		kind  domain.ScopeKind
		step  string
		valid bool
	}{
		{"chapter:1:commit", domain.ScopeChapter, "commit", true},
		{"arc:1:2:arc_summary", domain.ScopeArc, "arc_summary", true},
		{"volume:3:volume_summary", domain.ScopeVolume, "volume_summary", true},
		{"global:layered_outline", domain.ScopeGlobal, "layered_outline", true},
		{"chapter:commit", "", "", false},
		{"bogus:1:x", "", "", false},
	}
	for _, tc := range cases {
		scope, step, err := parseCheckpointSpec(tc.spec)
		if tc.valid && err != nil {
			t.Errorf("%s: 期望解析成功，得到 %v", tc.spec, err)
		}
		if !tc.valid {
			if err == nil {
				t.Errorf("%s: 期望解析失败", tc.spec)
			}
			continue
		}
		if scope.Kind != tc.kind || step != tc.step {
			t.Errorf("%s: 解析为 kind=%s step=%s", tc.spec, scope.Kind, step)
		}
	}
}

func cleanResult() Result {
	r := Grade(writerSmokeCase(), cleanCollected())
	r.Metrics.TotalWords = 1000
	return r
}

// TestCollectReadsCheckpoints 验证真实 store 读取路径：写入 checkpoint 后 Collect 能命中契约。
func TestCollectReadsCheckpoints(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "commit", "chapters/01.md", "d1"); err != nil {
		t.Fatalf("append checkpoint: %v", err)
	}

	col := Collect(dir, nil)
	ok, err := col.HasCheckpoint("chapter:1:commit")
	if err != nil || !ok {
		t.Fatalf("应命中 chapter:1:commit，ok=%v err=%v", ok, err)
	}
	if miss, _ := col.HasCheckpoint("chapter:2:commit"); miss {
		t.Fatal("不应命中不存在的 chapter:2:commit")
	}
	if col.Pending["pending_commit"] {
		t.Fatal("干净目录不应有 pending_commit 残留")
	}
}

func hasIssue(issues []Issue, source, detailContains string) bool {
	for _, it := range issues {
		if it.Source == source && strings.Contains(it.Detail, detailContains) {
			return true
		}
	}
	return false
}
