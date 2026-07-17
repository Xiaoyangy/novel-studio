package domain

import (
	"strings"
	"testing"
)

func TestParseBookScaleRangeChineseAndEnglish(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  BookScaleRange
	}{
		{
			value: "\u9884\u8ba1100-130\u4e07\u5b57\uff0c\u7ea68-10\u5377\uff0c360-480\u7ae0\uff1b\u4e3b\u7ebf\u65f6\u95f4\u8de8\u5ea64\u5e74",
			want:  BookScaleRange{MinVolumes: 8, MaxVolumes: 10, MinChapters: 360, MaxChapters: 480},
		},
		{
			value: "about 6\u20138 volumes and 180\u2013240 chapters",
			want:  BookScaleRange{MinVolumes: 6, MaxVolumes: 8, MinChapters: 180, MaxChapters: 240},
		},
	} {
		got, err := ParseBookScaleRange(tc.value)
		if err != nil {
			t.Fatalf("ParseBookScaleRange(%q): %v", tc.value, err)
		}
		if got != tc.want {
			t.Fatalf("ParseBookScaleRange(%q) = %+v, want %+v", tc.value, got, tc.want)
		}
	}
}

func TestResolveBookScaleTargetUsesFrozenMidpointAndWordBudget(t *testing.T) {
	target, err := ResolveBookScaleTarget(
		"\u9884\u8ba1100-130\u4e07\u5b57\uff0c\u7ea68-10\u5377\uff0c360-480\u7ae0\uff1b\u4e3b\u7ebf\u65f6\u95f4\u8de8\u5ea6\u7ea63.5-4\u5e74",
		2,
		128,
	)
	if err != nil {
		t.Fatal(err)
	}
	if target.TargetVolumes != 9 || target.TargetChapters != 420 ||
		target.TargetWords != 1150000 || target.TargetWordsPerChapter != 2738 ||
		target.StoryTimeHint != "3.5-4\u5e74" {
		t.Fatalf("target=%+v", target)
	}
}

func TestOutlineChapterContractRejectsThinAndJSONShell(t *testing.T) {
	volumes := []VolumeOutline{{Index: 1, Title: "\u5377", Theme: "\u4e3b\u9898", Arcs: []ArcOutline{{
		Index: 1, Title: "\u5f27", Goal: "\u76ee\u6807", Chapters: []OutlineEntry{
			{Title: "\u91cd\u590d", CoreEvent: "\u7ee7\u7eed\u63a8\u8fdb", Hook: "\u5236\u9020\u60ac\u5ff5", Scenes: []string{`["\u573a\u666f\u4e00","\u573a\u666f\u4e8c"]`}},
			{Title: "\u91cd\u590d", CoreEvent: "\u7ee7\u7eed\u63a8\u8fdb", Hook: "\u5236\u9020\u60ac\u5ff5", Scenes: nil},
		},
	}}}}
	issues := OutlineChapterContractIssues(volumes)
	joined := make([]string, 0, len(issues))
	for _, issue := range issues {
		joined = append(joined, issue.Code)
	}
	all := strings.Join(joined, ",")
	for _, code := range []string{"core_event_not_concrete", "hook_not_actionable", "scene_json_shell", "scenes_too_thin", "duplicate_title", "duplicate_core_event", "duplicate_hook"} {
		if !strings.Contains(all, code) {
			t.Fatalf("issues=%s, want %s", all, code)
		}
	}
}

func TestOutlineChapterContractAcceptsConcreteDistinctChapters(t *testing.T) {
	chapters := []OutlineEntry{
		{
			Title:     "\u96e8\u591c\u7684\u9519\u8d26\u5355",
			CoreEvent: "\u6797\u7b19\u5728\u4f9b\u8d27\u5546\u4e34\u65f6\u6da8\u4ef7\u540e\u91cd\u6392\u644a\u4f4d\uff0c\u62ff\u51fa\u7b2c\u4e09\u5957\u6838\u9500\u89c4\u5219\u4fdd\u4f4f\u4e86\u5f00\u5e02\u65f6\u95f4",
			Hook:      "\u65b0\u89c4\u5219\u521a\u5f20\u8d34\uff0c\u5e02\u76d1\u6240\u5374\u9001\u6765\u4e00\u5f20\u76f8\u53cd\u7684\u6574\u6539\u5355",
			Scenes:    []string{"\u5e93\u623f\u91cc\u4e09\u65b9\u5bf9\u7740\u9519\u8d26\u5355\u8ffd\u67e5\u8d27\u6b3e\u53bb\u5411", "\u96e8\u68da\u4e0b\u6797\u7b19\u73b0\u573a\u6362\u8868\u5e76\u8bf4\u670d\u644a\u4e3b", "\u5f00\u5e02\u540e\u6c88\u77e5\u9065\u6838\u5bf9\u6d41\u6c34\u53d1\u73b0\u65b0\u77db\u76fe"},
		},
		{
			Title:     "\u8c01\u52a8\u4e86\u5907\u7528\u7535\u6e90",
			CoreEvent: "\u6c88\u77e5\u9065\u8ffd\u5230\u914d\u7535\u623f\u627e\u51fa\u65ad\u7535\u4eba\uff0c\u4f46\u4e3a\u4e86\u4fdd\u4f4f\u4e3e\u62a5\u8005\u53ea\u516c\u5e03\u4e86\u53ef\u590d\u6838\u7684\u65f6\u95f4\u7ebf",
			Hook:      "\u65ad\u7535\u4eba\u7559\u4e0b\u7684\u95e8\u7981\u5361\u5c5e\u4e8e\u4e00\u4f4d\u4ece\u672a\u6765\u8fc7\u591c\u5e02\u7684\u7406\u4e8b",
			Scenes:    []string{"\u914d\u7535\u623f\u5185\u62c6\u5f00\u5c01\u6761\u590d\u539f\u65ad\u7535\u987a\u5e8f", "\u76d1\u63a7\u5ba4\u91cc\u9010\u5e27\u6838\u5bf9\u95e8\u7981\u548c\u811a\u5370", "\u529e\u516c\u5ba4\u4e2d\u7528\u533f\u540d\u5907\u5fd8\u5f55\u4fdd\u62a4\u4e3e\u62a5\u8005"},
		},
	}
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{Index: 1, Chapters: chapters}}}}
	if issues := OutlineChapterContractIssues(volumes); len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestMissingCompassCoverageRequiresTerminalContractAndThreads(t *testing.T) {
	compass := StoryCompass{
		EndingDirection: "\u4e24\u4eba\u5b8c\u6210\u5c0f\u57ce\u6837\u677f\u5e76\u56de\u5f52\u5bb6\u5ead",
		OpenThreads:     []string{"\u65e7\u5382\u5b8c\u6210\u81ea\u8f6c"},
		NonNegotiables:  []string{"\u4e0d\u5f97\u727a\u7272\u666e\u901a\u5546\u6237"},
	}
	registry := BuildStoryContractRegistry(compass)
	refs := make([]StoryContractRef, len(registry))
	copy(refs, registry)
	for i := range refs {
		refs[i].PlannedPayoffChapter = 1
		refs[i].PlannedResolution = []string{
			"林笙组织全体商户表决通过小城样板并与家人共同留下运营",
			"沈知遥公开补偿账本并确保普通商户获得足额返还与长期席位",
			"旧厂工人接管订单系统并完成不依赖外部输血的首月自转",
		}[i]
	}
	payoffEvidence := refs[0].PlannedResolution + "；" + refs[1].PlannedResolution + "；" + refs[2].PlannedResolution
	volumes := []VolumeOutline{{Index: 1, Theme: "\u6536\u675f", Arcs: []ArcOutline{{
		Index: 1, Goal: "\u8ba9\u6240\u6709\u957f\u7ebf\u5728\u4eba\u7269\u9009\u62e9\u4e2d\u5f97\u5230\u56de\u5e94", ContractRefs: refs,
		Chapters: []OutlineEntry{{CoreEvent: payoffEvidence, ContractRefs: refs}},
	}}}}
	if missing := MissingCompassCoverage(volumes, compass); len(missing) != 0 {
		t.Fatalf("missing=%v", missing)
	}
	volumes[0].Arcs[0].Chapters[0].ContractRefs[0].SourceDigest = "sha256:wrong"
	if missing := MissingCompassCoverage(volumes, compass); len(missing) == 0 {
		t.Fatal("source digest drift unexpectedly passed")
	}
}

func TestMissingCompassCoverageRejectsBareResolutionReceiptWithoutChapterEvidence(t *testing.T) {
	compass := StoryCompass{EndingDirection: "主角完成旧城自治并让居民掌握最终决策权"}
	ref := BuildStoryContractRegistry(compass)[0]
	ref.PlannedPayoffChapter = 1
	ref.PlannedResolution = "林笙召集居民投票通过自治章程并把最终决策权交给居民议会"
	volumes := []VolumeOutline{{Index: 1, Arcs: []ArcOutline{{
		Index: 1, ContractRefs: []StoryContractRef{ref},
		Chapters: []OutlineEntry{{
			CoreEvent:    "林笙在广场宣布事情终于解决，众人鼓掌庆祝",
			Scenes:       []string{"居民到场见证会议结束", "旧城重新开门", "主角离开会场"},
			ContractRefs: []StoryContractRef{ref},
		}},
	}}}}
	missing := strings.Join(MissingCompassCoverage(volumes, compass), ",")
	if !strings.Contains(missing, "planned_resolution_not_realized_in_core_event_or_scenes") {
		t.Fatalf("missing=%s", missing)
	}
	volumes[0].Arcs[0].Chapters[0].Scenes = append(
		volumes[0].Arcs[0].Chapters[0].Scenes,
		ref.PlannedResolution,
	)
	if missing := MissingCompassCoverage(volumes, compass); len(missing) != 0 {
		t.Fatalf("resolution evidence should satisfy binding: %v", missing)
	}
}
