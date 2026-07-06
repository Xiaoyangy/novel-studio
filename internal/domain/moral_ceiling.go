package domain

// MoralCeiling 道德天花板：显式控制"主角能坏到什么程度"。
// 网文 / 出版 / 影视、不同题材差异巨大，必须项目级显式建模。
// 落盘 meta/moral_ceiling.json（可选工件），经 novel_context 注入 Writer / Editor。
type MoralCeiling struct {
	KillsAllowedPerArc    int      `json:"kills_allowed_per_arc,omitempty"`     // 每弧允许的击杀上限
	BetrayalLimit         string   `json:"betrayal_limit,omitempty"`            // 背叛行为的边界描述
	NecessaryEvilBudget   int      `json:"necessary_evil_budget,omitempty"`     // "必要之恶"预算次数
	TabooZones            []string `json:"taboo_zones,omitempty"`               // 绝对禁区
	MoralLevelResetByPlot bool     `json:"moral_level_reset_by_plot,omitempty"` // 剧情是否可重置道德水位
}

// IsEmpty 全部零值时为真，消费方据此跳过注入。
func (m MoralCeiling) IsEmpty() bool {
	return m.KillsAllowedPerArc == 0 && m.BetrayalLimit == "" &&
		m.NecessaryEvilBudget == 0 && len(m.TabooZones) == 0 && !m.MoralLevelResetByPlot
}
