package rag

import "testing"

func TestCraftContentFacet(t *testing.T) {
	cases := []struct {
		path string
		want CraftFacet
	}{
		{"novel_all/05-素材与描写词汇/人物外貌_神态_心理_动作描写总汇.md", FacetAppearance},
		{"novel_all/05-素材与描写词汇/形容女人的词语.md", FacetAppearance},
		{"novel_all/05-素材与描写词汇/冥界地名.md", FacetLexicon},
		{"novel_all/05-素材与描写词汇/描写自然环境的成语.md", FacetScene},
		{"novel_all/05-素材与描写词汇/戏剧结构.md", FacetPlot},
		{"novel_all/05-素材与描写词汇/描写人物心理的词语.md", FacetEmotion},
		{"novel_all/07-拆文分析/拆文-茧爱.md", FacetBenchmark},
		{"novel_all/07-拆文分析/短篇-煤矿少女.md", FacetBenchmark},
		{"novel_all/03-题材与套路/网游小说八大职业设定.md", FacetSkillAbility},
		{"novel_all/03-题材与套路/悬疑小说诡计大全.md", FacetPlot},
		{"novel_all/02-大纲模板与示例/剧情大纲模板.md", FacetOutline},
		{"novel_all/06-爽点与剧情钩子/爽点清单.md", FacetPlot},
		{"review-calibration/人工样本共通点与审核校准报告.md", FacetCalibration},
		{"novel_all/08-运营与平台/签约模板.md", FacetMarket},
		{"deconstruction-library/writing-techniques/appearance/eyes/眼睛.md", FacetAppearance},
	}
	for _, c := range cases {
		if got := CraftContentFacet(c.path, ""); got != c.want {
			t.Errorf("%s: got %s want %s", c.path, got, c.want)
		}
	}
}

func TestUsageStagesForFacet(t *testing.T) {
	// 外貌 → plan+writing；大纲 → architect；校准 → review。
	if s := UsageStagesForFacet(FacetAppearance); len(s) != 2 || s[0] != StagePlan {
		t.Fatalf("appearance stages: %v", s)
	}
	if s := UsageStagesForFacet(FacetOutline); len(s) != 1 || s[0] != StageArchitect {
		t.Fatalf("outline stages: %v", s)
	}
	if s := UsageStagesForFacet(FacetCalibration); len(s) != 1 || s[0] != StageReview {
		t.Fatalf("calibration stages: %v", s)
	}
}
