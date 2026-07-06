package domain

import "fmt"

// PacingContract 体裁级节奏契约。来源 meta/pacing_contract.json（可选工件：
// Architect 设计期落盘或用户手工放置），缺失时不 lint。
// 与 rules.Structured.ChapterWords 语义不同且叠加：一个是用户偏好，一个是体裁契约。
type PacingContract struct {
	Name                string             `json:"name"`
	ChapterHookRequired bool               `json:"chapter_hook_required"`
	MajorHookIntervalCh int                `json:"major_hook_interval_chs,omitempty"` // 大爽点最大间隔章数；0 = 不约束
	ChapterWordMin      int                `json:"chapter_word_min,omitempty"`
	ChapterWordMax      int                `json:"chapter_word_max,omitempty"`
	ConflictEngineMix   map[string]float64 `json:"conflict_engine_mix,omitempty"` // 期望冲突引擎占比
}

// Validate 校验契约自洽。
func (p PacingContract) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("pacing_contract.name 不能为空")
	}
	if p.ChapterWordMin < 0 || p.ChapterWordMax < 0 {
		return fmt.Errorf("pacing_contract 字数带不能为负")
	}
	if p.ChapterWordMax > 0 && p.ChapterWordMin > p.ChapterWordMax {
		return fmt.Errorf("pacing_contract.chapter_word_min(%d) > chapter_word_max(%d)", p.ChapterWordMin, p.ChapterWordMax)
	}
	if p.MajorHookIntervalCh < 0 {
		return fmt.Errorf("pacing_contract.major_hook_interval_chs 不能为负")
	}
	return nil
}

// pacingPresets 六个内置体裁预设（硬数字来自主流平台观察值）。
var pacingPresets = map[string]PacingContract{
	"qidian_xuanhuan": {Name: "qidian_xuanhuan", ChapterHookRequired: true, MajorHookIntervalCh: 3, ChapterWordMin: 2000, ChapterWordMax: 3000,
		ConflictEngineMix: map[string]float64{"interest": 0.4, "value": 0.3, "emotion": 0.2, "survival": 0.1}},
	"qidian_dushi": {Name: "qidian_dushi", ChapterHookRequired: true, MajorHookIntervalCh: 5, ChapterWordMin: 3000, ChapterWordMax: 5000},
	"jinjiang":     {Name: "jinjiang", ChapterHookRequired: true, MajorHookIntervalCh: 10, ChapterWordMin: 3000, ChapterWordMax: 4000},
	"douban":       {Name: "douban", ChapterHookRequired: false, MajorHookIntervalCh: 25, ChapterWordMin: 4000, ChapterWordMax: 8000},
	"publish":      {Name: "publish", ChapterHookRequired: false, MajorHookIntervalCh: 50, ChapterWordMin: 5000, ChapterWordMax: 10000},
	"short":        {Name: "short", ChapterHookRequired: true},
}

// PacingPreset 按名字取内置预设。
func PacingPreset(name string) (PacingContract, bool) {
	p, ok := pacingPresets[name]
	return p, ok
}

// PacingPresetNames 全部内置预设名（测试与文档用）。
func PacingPresetNames() []string {
	return []string{"qidian_xuanhuan", "qidian_dushi", "jinjiang", "douban", "publish", "short"}
}

// LintPacing 对一章做确定性节奏检查，返回违规描述（warning 语义，不阻塞）。
// hookType 为空表示本章无章末钩子；hookHistory 是含本章在内的按章钩子历史。
func (p PacingContract) LintPacing(chapter, wordCount int, hookType string, hookHistory []string) []string {
	var issues []string
	if p.ChapterWordMin > 0 && wordCount < p.ChapterWordMin {
		issues = append(issues, fmt.Sprintf("第%d章 %d 字低于体裁契约 %s 下限 %d", chapter, wordCount, p.Name, p.ChapterWordMin))
	}
	if p.ChapterWordMax > 0 && wordCount > p.ChapterWordMax {
		issues = append(issues, fmt.Sprintf("第%d章 %d 字超过体裁契约 %s 上限 %d", chapter, wordCount, p.Name, p.ChapterWordMax))
	}
	if p.ChapterHookRequired && hookType == "" {
		issues = append(issues, fmt.Sprintf("体裁契约 %s 要求章末必有钩子，第%d章未报 hook_type", p.Name, chapter))
	}
	if p.MajorHookIntervalCh > 0 && len(hookHistory) >= p.MajorHookIntervalCh {
		// crisis / choice 视为强钩子（大爽点/大转折的代理信号）
		tail := hookHistory[len(hookHistory)-p.MajorHookIntervalCh:]
		strong := false
		for _, h := range tail {
			if h == "crisis" || h == "choice" {
				strong = true
				break
			}
		}
		if !strong {
			issues = append(issues, fmt.Sprintf("最近 %d 章无强钩子（crisis/choice），超过体裁契约 %s 的大爽点间隔", p.MajorHookIntervalCh, p.Name))
		}
	}
	return issues
}
