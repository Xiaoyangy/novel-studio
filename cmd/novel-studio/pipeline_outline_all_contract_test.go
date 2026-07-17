package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestOutlineAllPlannerExpandsReservationsBeforeRevisingThinExpandedArcs(t *testing.T) {
	makeChapters := func(prefix string, count int, thinFrom int) []domain.OutlineEntry {
		chapters := make([]domain.OutlineEntry, count)
		for i := range chapters {
			chapter := i + 1
			scenes := []string{
				fmt.Sprintf("%s第%02d章清晨在仓库门前核对第一份异常订单。", prefix, chapter),
				fmt.Sprintf("%s第%02d章的对手拿着旧规则阻止货车进入黄线。", prefix, chapter),
				fmt.Sprintf("%s第%02d章公开新证据并让后排农户先行入仓。", prefix, chapter),
			}
			if thinFrom > 0 && chapter >= thinFrom {
				scenes = scenes[:2]
			}
			chapters[i] = domain.OutlineEntry{
				Title:     fmt.Sprintf("%s第%02d章", prefix, chapter),
				CoreEvent: fmt.Sprintf("%s第%02d章的负责人核对异常订单，顶住旧规阻拦并公开证据，最终改写当天排期规则。", prefix, chapter),
				Hook:      fmt.Sprintf("次日%s第%02d章必须追查新回执暴露的关联账户。", prefix, chapter),
				Scenes:    scenes,
			}
		}
		return chapters
	}

	volumes := []domain.VolumeOutline{
		{
			Index: 1,
			Title: "第一卷",
			Arcs: []domain.ArcOutline{
				{
					Index:    1,
					Title:    "旧弧待修",
					Goal:     "完成第一轮公开核验",
					Chapters: makeChapters("V1A1", 12, 4),
				},
				{
					Index:             2,
					Title:             "后弧待展开",
					Goal:              "建立跨村联营规则",
					EstimatedChapters: 8,
				},
			},
		},
		{
			Index: 2,
			Title: "第二卷",
			Arcs: []domain.ArcOutline{
				{
					Index:    1,
					Title:    "第二处旧弧待修",
					Goal:     "完成第二轮公开核验",
					Chapters: makeChapters("V2A1", 8, 1),
				},
			},
		},
	}
	compass := domain.StoryCompass{}
	target := domain.BookScaleTarget{TargetVolumes: 2, TargetChapters: 28}

	action, ok, err := outlineAllNextStructuralAction(volumes, compass, target)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || action.Type != domain.OutlineAllActionExpandArc || action.Volume != 1 || action.Arc != 2 || action.ExpectedChapterSpan != 8 {
		t.Fatalf("reservation must win before repairs: action=%+v ok=%v", action, ok)
	}

	volumes[0].Arcs[1].Chapters = makeChapters("V1A2", 8, 0)
	volumes[0].Arcs[1].EstimatedChapters = 0
	if action, ok, err = outlineAllNextStructuralAction(volumes, compass, target); err != nil || ok {
		t.Fatalf("all reservations expanded: action=%+v ok=%v err=%v", action, ok, err)
	}

	action, ok = outlineAllNextRevisionAction(volumes, compass)
	if !ok || action.Type != domain.OutlineAllActionReviseArc || action.Volume != 1 || action.Arc != 1 || action.ExpectedChapterSpan != 12 {
		t.Fatalf("first thin expanded arc must be revised in place: action=%+v ok=%v", action, ok)
	}

	volumes[0].Arcs[0].Chapters = makeChapters("V1A1", 12, 0)
	action, ok = outlineAllNextRevisionAction(volumes, compass)
	if !ok || action.Type != domain.OutlineAllActionReviseArc || action.Volume != 2 || action.Arc != 1 || action.ExpectedChapterSpan != 8 {
		t.Fatalf("next thin expanded arc must remain repairable: action=%+v ok=%v", action, ok)
	}

	volumes[1].Arcs[0].Chapters = makeChapters("V2A1", 8, 0)
	if action, ok = outlineAllNextRevisionAction(volumes, compass); ok {
		t.Fatalf("fully repaired outline still requested revision: %+v", action)
	}
}

func TestOutlineAllPlannerSelectsArcWithMissingResolutionEvidence(t *testing.T) {
	compass := domain.StoryCompass{OpenThreads: []string{"旧厂工人最终独立接管排产"}}
	ref := domain.BuildStoryContractRegistry(compass)[0]
	ref.PlannedPayoffChapter = 3
	ref.PlannedResolution = "林澈召集旧厂工人表决通过轮班规则并由工人接管排产系统"
	readyChapter := func(title string) domain.OutlineEntry {
		return domain.OutlineEntry{
			Title:     title,
			CoreEvent: title + "负责人核对当天订单，顶住旧规则阻拦并公开证据，最后改写排产安排。",
			Hook:      title + "次日必须追查新回执暴露的关联账户和仓位缺口。",
			Scenes: []string{
				title + "清晨在仓库门前核对异常订单。",
				title + "旧班组拿着旧规阻止货车进线。",
				title + "负责人公开新证据并重排当日班次。",
			},
		}
	}
	chapters := []domain.OutlineEntry{
		readyChapter("第一章"),
		readyChapter("第二章"),
		readyChapter("第三章"),
		readyChapter("第四章"),
		readyChapter("第五章"),
		readyChapter("第六章"),
		readyChapter("第七章"),
		readyChapter("第八章"),
	}
	chapters[2].ContractRefs = []domain.StoryContractRef{ref}
	volumes := []domain.VolumeOutline{{Index: 1, Arcs: []domain.ArcOutline{{
		Index: 1, ContractRefs: []domain.StoryContractRef{ref}, Chapters: chapters,
	}}}}

	action, ok := outlineAllNextRevisionAction(volumes, compass)
	if !ok || action.Type != domain.OutlineAllActionReviseArc || action.Volume != 1 || action.Arc != 1 {
		t.Fatalf("missing resolution evidence did not select its arc: action=%+v ok=%v", action, ok)
	}
	volumes[0].Arcs[0].Chapters[2].CoreEvent = "林澈召集旧厂工人到旧工作台前，各班组表决通过轮班边界；新表当场生效，工人随后独立接管排产工具和次日订单。"
	if action, ok = outlineAllNextRevisionAction(volumes, compass); ok {
		t.Fatalf("realized payoff still requested revision: %+v", action)
	}
	drifted := ref
	drifted.PlannedResolution = "林澈独自宣布保留旧排产制度并继续掌握全部班次决定权"
	volumes[0].Arcs[0].Chapters[2].ContractRefs = []domain.StoryContractRef{drifted}
	volumes[0].Arcs[0].Chapters[2].CoreEvent = drifted.PlannedResolution
	if action, ok = outlineAllNextRevisionAction(volumes, compass); !ok || action.Volume != 1 || action.Arc != 1 {
		t.Fatalf("chapter ref drift escaped unconditional arc scan: action=%+v ok=%v", action, ok)
	}
}

func TestValidatePipelineOutlineAllFlatIdentityAllowsReservedGapsUntilExpanded(t *testing.T) {
	outputDir := t.TempDir()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("reserved-gap", 6); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{
			{
				Index: 1,
				Title: "已展开前弧",
				Chapters: []domain.OutlineEntry{
					{Title: "第一章"},
					{Title: "第二章"},
				},
			},
			{
				Index:             2,
				Title:             "待展开中弧",
				EstimatedChapters: 3,
			},
			{
				Index: 3,
				Title: "已展开后弧",
				Chapters: []domain.OutlineEntry{
					{Title: "第六章"},
				},
			},
		},
	}}
	if _, err := repairPipelineOutlineAllDerivedArtifacts(st, volumes); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, volumes); err != nil {
		t.Fatalf("partially expanded outline rejected its reserved chapter gap: %v", err)
	}
	if got, want := domain.FlattenOutline(volumes), []domain.OutlineEntry{
		{Chapter: 1, Title: "第一章"},
		{Chapter: 2, Title: "第二章"},
		{Chapter: 6, Title: "第六章"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reserved gap changed: got=%+v want=%+v", got, want)
	}

	volumes[0].Arcs[1].EstimatedChapters = 0
	volumes[0].Arcs[1].Chapters = []domain.OutlineEntry{
		{Title: "第三章"},
		{Title: "第四章"},
		{Title: "第五章"},
	}
	if _, err := repairPipelineOutlineAllDerivedArtifacts(st, volumes); err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineOutlineAllFlatIdentity(st, volumes); err != nil {
		t.Fatalf("fully expanded continuous outline rejected: %v", err)
	}
}

func TestPipelineOutlineAllProtectedCanonIgnoresHeadlessRuntimeFiles(t *testing.T) {
	outputDir := t.TempDir()
	st := store.NewStore(outputDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("protected-runtime", 1); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(outputDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("world_rules.json", `[{"rule":"不可变正史"}]`)
	write("logs/headless.log", "first runtime log\n")
	write("meta/run.json", `{"started_at":"first"}`)
	before, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}

	write("logs/headless.log", "second runtime log\n")
	write("logs/agent-debug.log", "new runtime log\n")
	write("meta/run.json", `{"started_at":"second"}`)
	afterRuntime, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterRuntime != before {
		t.Fatalf("headless runtime files changed protected canon: before=%s after=%s", before, afterRuntime)
	}

	write("world_rules.json", `[{"rule":"被篡改正史"}]`)
	afterCanon, err := pipelineOutlineAllProtectedCanonRoot(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if afterCanon == before {
		t.Fatal("protected canon did not detect a real foundation mutation")
	}
}
