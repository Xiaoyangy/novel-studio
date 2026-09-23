package tools

import "github.com/voocel/agentcore/schema"

func characterAddressedCommunicationsSchemaV1() map[string]any {
	result := characterCommunicationsSchema()
	result["description"] = "本人本次选择的定向通信；to_character/recipient_hint/reply_to_received_fact_id严格三选一，不把私人意图改成广播。text保留本人原意，不是正文对白；是否送达仍需真实裁决。"
	item := result["items"].(map[string]any)
	fields := item["properties"].(map[string]any)
	fields["recipient_hint"] = schema.String("本人已感知的特定对象称呼/描述，如眼前照料我的医护；以knowledge_refs中的本人事实为依据，不猜实名、不代替广播；目标不唯一或未被识别不代表已送达")
	fields["reply_to_received_fact_id"] = schema.String("本人known_facts中已实际收到的通信事件ID；Host按该事件的严格来源绑定原发送者，不能引用别人的事实、旧记忆摘要或尚未收到的未来事件；仍须在knowledge_refs引用该事件")
	fields["condition_from_character"] = schema.String("仅named模式conditional_response可用的已知实名条件；非实名模式禁止填写，不从Host身份匹配触发匿名回复")
	fields["condition_kind"] = schema.String("conditional_response必填触发消息kind；非实名条件回复只允许reply_to_received_fact_id指定本人前态已收事件，且kind须与该事件一致。recipient_hint不能预先条件回复未知未来消息")
	var required []string
	for _, name := range item["required"].([]string) {
		if name != "to_character" {
			required = append(required, name)
		}
	}
	item["required"] = required
	return result
}

func characterCommunicationReceptionsSchemaV1() map[string]any {
	result := characterPassiveReceptionsSchema()
	result["description"] = "仅列本轮真正送达的非实名定向通信，活跃/休眠接收者均可；原recipient_hint必须依据本人实际感知且唯一可识别，reply事件须精确绑定其原发送者。未识别、不唯一、未送达则省略并保留失败事实，不能任选一人或广播。Host从原提案恢复text/kind，不在post_state另写收到文本。"
	item := result["items"].(map[string]any)
	fields := item["properties"].(map[string]any)
	fields["to_agent_id"] = schema.String("本轮确已收到原通信的唯一实际主体agent_id，必须存在于physical_state；不得给别人替作决定/回答")
	fields["communication_id"] = schema.String("原提案recipient_hint或reply_to_received_fact_id模式的通信ID；named to_character仍走原协议，不改原提案或文本")
	fields["recipient_resolution"] = schema.Enum("既有Arbiter基于原感知依据确认唯一对象；未识别/歧义不得写unique并投递", "unique")
	fields["evidence_refs"] = schema.Array("仅原communication.knowledge_refs中已有的角色感知依据；reply须包括其已收到事件ID，不用作者未投放真相作寻址依据", schema.String("fact id"))
	item["required"] = append(item["required"].([]string), "recipient_resolution", "evidence_refs")
	return result
}
