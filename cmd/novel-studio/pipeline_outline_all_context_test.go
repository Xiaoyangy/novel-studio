package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestPreparePipelineOutlineAllCandidateRebuildsInterruptedCopyWithoutReceipt(t *testing.T) {
	runRoot := t.TempDir()
	live := filepath.Join(runRoot, "output", "novel")
	st := store.NewStore(live)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := activatePipelineSealedTwoPassModeAtOutput(live); err != nil {
		t.Fatal(err)
	}
	if err := ensurePipelineOutlineAllRequirement(live); err != nil {
		t.Fatal(err)
	}
	const attemptID = "oa-interrupted-prepare"
	candidate := pipelineOutlineAllCandidatePath(live, attemptID)
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate, "partial-copy.txt"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preparePipelineOutlineAllCandidate(live, candidate, attemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(candidate, "partial-copy.txt")); !os.IsNotExist(err) {
		t.Fatalf("interrupted bytes survived deterministic rebuild: %v", err)
	}
	if err := validatePipelineOutlineAllCandidatePrepareReceipt(live, candidate, attemptID); err != nil {
		t.Fatalf("prepare receipt: %v", err)
	}
	if err := preparePipelineOutlineAllCandidate(live, candidate, attemptID); err != nil {
		t.Fatalf("prepared candidate should resume idempotently: %v", err)
	}
}

func TestPipelineOutlineAllVisibleContextBounds420ChaptersAndPreservesContracts(t *testing.T) {
	var volumes []domain.VolumeOutline
	contractCount := 0
	globalChapter := 1
	for volumeIndex := 1; volumeIndex <= 10; volumeIndex++ {
		volume := domain.VolumeOutline{Index: volumeIndex, Title: fmt.Sprintf("第%d卷", volumeIndex), Theme: "压力逐卷升级且人物选择留下后果"}
		for arcIndex := 1; arcIndex <= 3; arcIndex++ {
			ref := domain.StoryContractRef{
				ID: fmt.Sprintf("contract-%02d-%02d", volumeIndex, arcIndex), Kind: domain.StoryContractOpenThread,
				SourceDigest:         fmt.Sprintf("sha256:%064d", contractCount+1),
				PlannedPayoffChapter: globalChapter + 13,
				PlannedResolution:    fmt.Sprintf("角色%d完成不可逆行动并把第%d条长线推进到可验证终态", volumeIndex, contractCount+1),
			}
			contractCount++
			arc := domain.ArcOutline{Index: arcIndex, Title: fmt.Sprintf("弧%d", arcIndex), Goal: "角色在具体阻力下选择并承担跨弧后果", ContractRefs: []domain.StoryContractRef{ref}}
			for chapterIndex := 0; chapterIndex < 14; chapterIndex++ {
				arc.Chapters = append(arc.Chapters, domain.OutlineEntry{
					Title:     fmt.Sprintf("第%d章的独特事件", globalChapter),
					CoreEvent: fmt.Sprintf("主角在第%d章面对对手封锁后改变方案并造成可追踪的资源变化", globalChapter),
					Hook:      fmt.Sprintf("变化迫使下一章处理第%d号后果", globalChapter),
					Scenes:    []string{"现场核对资源与约束", "对手执行阻断并留下证据", "主角选择新方案并支付代价"},
				})
				globalChapter++
			}
			volume.Arcs = append(volume.Arcs, arc)
		}
		volumes = append(volumes, volume)
	}
	digest, err := domain.ComputeLayeredOutlineDigest(volumes)
	if err != nil {
		t.Fatal(err)
	}
	action := domain.OutlineAllPendingAction{
		Type: domain.OutlineAllActionReviseArc, Operation: 31,
		Volume: 5, Arc: 2, ExpectedChapterSpan: 14, BeforeLayeredDigest: digest,
	}
	foundation := pipelineOutlineAllFrozenFoundation{
		Root:       "sha256:foundation",
		Premise:    "一群居民在封闭旧城中争夺规则制定权，并让每次选择留下可追踪后果。",
		Characters: json.RawMessage(`[{"name":"林笙","goal":"保住居民决策权"}]`),
		WorldRules: json.RawMessage(`[{"rule":"资源转移必须留下账本"}]`),
		BookWorld:  json.RawMessage(`{"city":"旧城","pressure":"封锁"}`),
		Compass:    json.RawMessage(`{"ending_direction":"居民掌握规则"}`),
		Authorities: map[string]string{
			"meta/web_reference_brief.md":      "外部事实只作压力机制，不复制表达。",
			"meta/prewrite_storycraft_plan.md": "每弧必须有选择、代价和跨弧后果。",
		},
	}
	view, raw, visibleDigest, err := buildPipelineOutlineAllModelVisibleContext(
		volumes,
		domain.StoryCompass{EndingDirection: "居民掌握规则", NonNegotiables: []string{"账本不能被抹除"}},
		domain.BookScaleTarget{TargetVolumes: 10, TargetChapters: 420},
		action,
		foundation,
		tools.References{LongformPlanning: "长篇因果升级", ArcTemplates: "弧模板", CharacterBuilding: "人物压力反应"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > pipelineOutlineAllVisibleContextMaxBytes || visibleDigest != pipelineBytesSHA(raw) {
		t.Fatalf("visible bytes=%d digest=%s", len(raw), visibleDigest)
	}
	if len(view.CompleteLayeredArcMap) != 30 {
		t.Fatalf("arc map=%d", len(view.CompleteLayeredArcMap))
	}
	gotContracts := 0
	for _, arc := range view.CompleteLayeredArcMap {
		gotContracts += len(arc.ContractRefs)
	}
	if gotContracts != contractCount {
		t.Fatalf("contracts=%d want=%d", gotContracts, contractCount)
	}
	if view.TargetArc == nil || len(view.TargetArc.Chapters) != 14 ||
		view.PreviousBoundary == nil || len(view.PreviousBoundary.Chapters) != 2 ||
		view.NextBoundary == nil || len(view.NextBoundary.Chapters) != 2 {
		t.Fatalf("target/boundary context incomplete: target=%+v previous=%+v next=%+v", view.TargetArc, view.PreviousBoundary, view.NextBoundary)
	}
}
