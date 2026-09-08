package tools

import "github.com/voocel/agentcore/schema"

func characterCommunicationsSchema() map[string]any {
	return schema.Array("本次选择中拟传递的信息/请求/承诺/条件响应；text是语义要点，不是正文对白", schema.Object(
		schema.Property("id", schema.String("本提案内唯一通信标识")).Required(),
		schema.Property("to_character", schema.String("明确接收者实名")).Required(),
		schema.Property("kind", schema.Enum("语义类型", "statement", "commitment", "information", "request", "conditional_response")).Required(),
		schema.Property("text", schema.String("基于本人已知事实/决定的语义信息，Drafter另写对白")).Required(),
		schema.Property("knowledge_refs", schema.Array("本人observation可引用的fact id", schema.String(""))).Required(),
		schema.Property("condition_from_character", schema.String("conditional_response时：需先实际收到谁的信息")),
		schema.Property("condition_kind", schema.Enum("触发条件响应需收到的类型", "statement", "commitment", "information", "request")),
	))
}

func characterResourceReadsSchema() map[string]any {
	return schema.Array("多步/条件读取意图：二选一给已知resource_id，或incoming_delivery_from表示若该人本轮实际送达材料就当场读；不会扫描其私有目录", schema.Object(
		schema.Property("resource_id", schema.String("本人resource_views可见的既有文档/资源ID")),
		schema.Property("incoming_delivery_from", schema.String("条件读取来源角色实名；与resource_id互斥，只解析本轮实际送达且已授予访问的材料")),
	))
}

func characterResourceDeliveriesSchema() map[string]any {
	return schema.Array("实际交付/送达裁决，提案和承诺不等于已收到；name/unit/amount与访问权分别声明。仅答应去取材料时access仍none", schema.Object(
		schema.Property("resource_id", schema.String("既有resource_id，封袋与袋内文件严格分开")).Required(),
		schema.Property("from_agent_id", schema.String("实际发送者，自行取得可与接收者相同")).Required(),
		schema.Property("to_agent_id", schema.String("实际接收者")).Required(),
		schema.Property("source_proposal_digest", schema.String("发送/取得行为的原提案digest")).Required(),
		schema.Property("received_fields", schema.Array("实际收到哪些信息，[]可表示仅实物访问交接", schema.Enum("字段", "name", "unit", "amount"))).Required(),
		schema.Property("access", schema.Enum("仅信息/承诺用none，不自动持有", "none", "shared", "exclusive")).Required(),
		schema.Property("evidence_refs", schema.Array("实际交付依据；受限资料需要实际解封/许可机制", schema.String(""))).Required(),
	))
}

func characterReceivedFactsSchema() map[string]any {
	return schema.Array("本轮新增已收到/读到的事实，宿主保留历史。通信取原communication语义，文档取readable_facts，不能引用StateAfter/当前余额造旧文书", schema.Object(
		schema.Property("id", schema.String("可省略由宿主按来源生成")),
		schema.Property("source_type", schema.Enum("认知来源", "communication", "resource_read")).Required(),
		schema.Property("source_id", schema.String("通信id或文档readable_fact id")).Required(),
		schema.Property("from_agent_id", schema.String("communication需发送者；resource_read省略或本人")),
		schema.Property("resource_id", schema.String("resource_read需实际可读文档id，外清单不会递归袋内文件")),
		schema.Property("source_proposal_digest", schema.String("可省略由宿主绑定，通信为sender提案、读取为本人的请求提案")),
		schema.Property("kind", schema.String("可省略从源补全，读取固定document_statement")),
		schema.Property("text", schema.String("可省略由宿主逐字取源语义/原文；不得写作者推断")),
		schema.Property("chapter", schema.Int("可省略，由宿主绑定当前章")),
	))
}
