package tools

import "github.com/voocel/agentcore/schema"

func characterPassiveReceptionsSchema() map[string]any {
	return schema.Array("只列本轮实际送达给休眠角色的通信；无送达时省略或[]。不能替接收者作选择/回应/行动，不填原文；宿主从原通信恢复内容。", schema.Object(
		schema.Property("to_agent_id", schema.String("physical_state 中本轮没有提案/resolution的休眠接收者agent_id")).Required(),
		schema.Property("from_agent_id", schema.String("原通信提案的发送者agent_id")).Required(),
		schema.Property("source_proposal_digest", schema.String("发送者原提案digest")).Required(),
		schema.Property("communication_id", schema.String("原提案communications中的id；to_character必须等于接收者实名")).Required(),
		schema.Property("delivered_at_day", schema.Number("实际送达故事日，必须在本轮story_time起止之间；不是预计到达时间")).Required(),
		schema.Property("channel", schema.Enum("实际传递方式。当面传递须同地；远程传递须引用既有且实际使用的世界机制", "in_person", "mechanism")).Required(),
		schema.Property("mechanism_ref", schema.String("仅mechanism通道：同时存在于world机制、发送者mechanism_refs及其实际resolution.mechanism_refs的id")),
	))
}
