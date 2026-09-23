package agents

import (
	"encoding/json"
	"fmt"
)

// Runtime recovery guidance is outside the frozen system/tool protocol. The
// staged context and exact tool error remain the only authority for a repair.
func projectAllPlannerPrompt(chapter int, packet json.RawMessage, boundary ProjectedArcBoundary, bookBudget string, cause error) string {
	opening := fmt.Sprintf("Host 已代你完成本章唯一一次 novel_context(chapter=%d, profile=planning) 调用并签发当前访问收据；不要再次调用 novel_context，也不要解释或结束，直接消费下列权威 JSON 并调用 plan_structure，然后用 plan_details 分批 finalize：", chapter)
	operation := "用 plan_structure + plan_details 分批生成并 finalize 完整 POV plan。"
	if cause != nil {
		opening = fmt.Sprintf("Host 已加载第%d章已保存的 staged plan 并刷新本轮 planning context；只修这个 partial。已尝试一次正常 plan_details(finalize=true)，未通过的实际错误如下：\n%s\n\n不要重做 plan_structure，不要重发整份计划，不要重新推演角色，不要再次检索或调用 novel_context。确定性缺项只补点名字段；正式 grounding findings 只修该矛盾及其关联表述。保留所有无关字段，最后用 plan_details(finalize=true) 经过原门禁收口；禁止改角色意图和裁决。", chapter, cause)
		operation = "只用 plan_details 修复已保存 partial 并 finalize 完整 POV plan，不重新 plan_structure。"
	}
	// Never rewrite packet bytes to change host dispatch instructions.
	return opening + "\n<host_prefetched_novel_context>\n" + string(packet) + "\n</host_prefetched_novel_context>\n\n" + fmt.Sprintf(
		"Project-Arc 已完成 V%dA%d《%s》中第 %d 章的全角色世界推演。本弧范围第%d-%d章，整体目标：%s。只规划第 %d 章：必须消费当前 content-addressed craft receipt；有 hits 的每个 need 都要按 receipt pack 精确转化进 external_reference_plan，fact receipt 有 hits 时同理；no_material 只绑定来源，禁止伪造材料。%s若 project_all_state 有 predecessor_contract，arc_transition_contract 的 incoming id/text 必须逐字复制，consumed_by_cause 必须逐字等于本章一个 causal_beats[].cause；弧首章 incoming 留空。每章都必须另写弧内唯一的 outgoing consequence id/text，禁止用 goal/hook 冒充。render_capacity 必须给出3-6个有主动阻力、转折、退出后果和具体行动证据的场景单元，总量自然支撑 user_rules.chapter_words，不得靠手续、复述或总结注水。%s不得读取或生成正文，不得转去其他章节。跨弧 payoff/reveal/reward 必须保留为 carried-forward，不能挤到本弧末章提前兑现；只有第%d章才是全书末章。",
		boundary.Volume, boundary.Arc, boundary.Title, chapter, boundary.FirstChapter, boundary.LastChapter, boundary.Goal, chapter, operation, bookBudget, boundary.BookLastChapter)
}
