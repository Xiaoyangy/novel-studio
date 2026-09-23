package userrules

import "testing"

func TestExplicitChapterWordsSkipsSupersededHistory(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		min, max   int
	}{
		{"actual production wording", "种子中的旧30万字、100章及每章2700–3300字属于已被替代的篇幅方案，不是当前预算。\n\n当前篇幅方案：3-3卷，110-110章，全书正文500000-520000字，中心目标510000字；约10个故事弧，章数按因果承载量重新分配。每章通常4500–5000字，按全书实际累计调整。", 4500, 5000},
		{"historical prefix", "历史方案：每章2700-3300字。当前每章4500-5000字。", 4500, 5000},
		{"historical suffix", "每章2700-3300字是旧方案；现在每章4500-5000字。", 4500, 5000},
		{"continue original plan", "继续沿用原方案每章4500–5000字。", 4500, 5000},
		{"keep original chapter range", "保持原每章4500–5000字。", 4500, 5000},
		{"retain old plan", "保留旧方案每章4500–5000字。", 4500, 5000},
		{"explicit replacement overrides retention", "保留旧方案每章2700–3300字的记录但已被替代。当前每章4500–5000字。", 4500, 5000},
		{"do not continue old plan", "不再沿用旧方案每章2700–3300字。当前每章4500–5000字。", 4500, 5000},
		{"same sentence new clause", "旧方案每章2700-3300字，当前每章4500-5000字。", 4500, 5000},
		{"direct old new markers", "旧每章2700-3300，现每章4500-5000", 4500, 5000},
		{"direct old new smaller", "旧每章4500-5000，现每章2700-3300", 2700, 3300},
		{"new is smaller", "旧方案每章4500-5000字；当前每章2700-3300字。", 2700, 3300},
		{"history only", "历史方案每章2700-3300字，已被替代，不是当前预算。", 0, 0},
		{"ordinary first still wins", "每章2700-3300字。附录每章4500-5000字。", 2700, 3300},
		{"ordinary range with prose exclusion", "每章4500–5000字且不要保留重复描写。", 4500, 5000},
		{"old story is not obsolete instruction", "故事发生在旧港，每章2700-3300字。", 2700, 3300},
		{"history genre is not obsolete instruction", "写历史小说，每章2700-3300字。", 2700, 3300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplicitChapterWords(tc.text)
			if tc.min == 0 {
				if got != nil {
					t.Fatalf("obsolete range became current: %+v", got)
				}
				return
			}
			if got == nil || got.Min != tc.min || got.Max != tc.max {
				t.Fatalf("got %+v, want %d-%d", got, tc.min, tc.max)
			}
		})
	}
}
