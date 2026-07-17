package host

// Harness 衰变定律：模型越强，所需 Harness 越薄。每个 harness 组件必须显式标注
// 生命周期类别，便于模型代际升级时评估哪些可以拆除、哪些永远保留。
// 注册表纯声明、零运行时行为：不改变任何组件的实际逻辑。

// HarnessDecayClass harness 组件的衰变类别。
type HarnessDecayClass string

const (
	// DecayBusinessLogic 业务必需：承载业务不变量，不随模型升级消失。
	DecayBusinessLogic HarnessDecayClass = "business_logic"
	// DecayModelGap 补模型能力缺口：模型代际升级后应重新评估、可能拆除。
	DecayModelGap HarnessDecayClass = "model_gap"
	// DecayInterim 临时方案：已知不是长期设计，需要重新设计。
	DecayInterim HarnessDecayClass = "interim"
)

// HarnessMetadata 一个 harness 组件的生命周期元数据。
type HarnessMetadata struct {
	Name       string            `json:"name"`
	Reason     string            `json:"reason"`
	DecayClass HarnessDecayClass `json:"decay_class"`
	DecayNote  string            `json:"decay_note,omitempty"` // 拆除/保留的判断依据
	ReviewAt   string            `json:"review_at,omitempty"`  // 下次评估时点，如 "2027-Q1"
}

// HarnessRegistry 全部 harness 组件的集中注册表。
// 新增 Guard / Gate / Sentinel 时必须同步登记（有测试兜底）。
var HarnessRegistry = map[string]HarnessMetadata{
	"coordinator_stop_guard": {
		Name:       "coordinator_stop_guard",
		Reason:     "Phase≠Complete 时物理阻止 Coordinator end_turn，连续阻拦超限才升级终止",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "承载\"全书未完不许收工\"的业务不变量，永远保留",
	},
	"writer_stop_guard": {
		Name:       "writer_stop_guard",
		Reason:     "Writer 未完成 plan→draft→check→commit 步骤链就想 end_turn 时阻拦",
		DecayClass: DecayModelGap,
		DecayNote:  "强模型极少漏步骤；下一代模型基线（2027 评估）上重测漏步率后决定是否简化",
		ReviewAt:   "2027-Q1",
	},
	"architect_stop_guard": {
		Name:       "architect_stop_guard",
		Reason:     "Architect 未产出必需 foundation 工件就 end_turn 时阻拦",
		DecayClass: DecayModelGap,
		DecayNote:  "同 writer_stop_guard，随模型代际重新评估",
		ReviewAt:   "2027-Q1",
	},
	"editor_stop_guard": {
		Name:       "editor_stop_guard",
		Reason:     "Editor 未落盘评审结论就 end_turn 时阻拦",
		DecayClass: DecayModelGap,
		DecayNote:  "同 writer_stop_guard，随模型代际重新评估",
		ReviewAt:   "2027-Q1",
	},
	"subagent_checkpoint_delta_guard": {
		Name:       "subagent_checkpoint_delta_guard",
		Reason:     "子代理空转（长时间无 checkpoint 增量）死循环兜底",
		DecayClass: DecayModelGap,
		DecayNote:  "弱模型/劣质代理下高频触发；主力模型稳定后可放宽阈值",
		ReviewAt:   "2027-Q1",
	},
	"tool_gate_complete_phase": {
		Name:       "tool_gate_complete_phase",
		Reason:     "Phase=Complete 时硬拦 subagent 派发（完结后不允许继续写作类调度）",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "业务不变量，永远保留",
	},
	"pipeline_outline_all_agent_gate": {
		Name:       "pipeline_outline_all_agent_gate",
		Reason:     "outline-all 独占窗口只允许 architect_long 消费与当前 receipt 完全一致的单次结构 mutation",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "结构发布能力边界与模型能力无关，永远保留",
	},
	"writer_expanded_chapter_gate": {
		Name:       "writer_expanded_chapter_gate",
		Reason:     "阻止 Writer 写没有已展开大纲的章节（防越界续写）",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "滚动规划的结构性约束，永远保留",
	},
	"budget_sentinel": {
		Name:       "budget_sentinel",
		Reason:     "book_usd 预算水位告警与熔断（warn_ratio / hard_stop）",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "成本控制与模型能力无关，永远保留",
	},
	"reminder_flow_generator": {
		Name:       "reminder_flow_generator",
		Reason:     "每轮从事实层重算当前该做什么/弧末刹车，注入 system-reminder",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "事实与指令解耦的核心机制；生成内容可随模型精简，机制保留",
	},
	"reminder_queue_guard": {
		Name:       "reminder_queue_guard",
		Reason:     "返工队列未清时禁止开新章的提醒",
		DecayClass: DecayBusinessLogic,
		DecayNote:  "业务不变量，永远保留",
	},
	"context_compression_pipeline": {
		Name:       "context_compression_pipeline",
		Reason:     "四级上下文压缩（microcompact→trim→store→full summary）+ 熔断器",
		DecayClass: DecayModelGap,
		DecayNote:  "上下文窗口持续增长 + 注意力衰退改善后，前两级可能先行退役",
		ReviewAt:   "2027-Q2",
	},
}
