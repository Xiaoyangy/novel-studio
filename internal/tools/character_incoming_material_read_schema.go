package tools

import "github.com/voocel/agentcore/schema"

// These additions are selected only by the frozen incoming-material policy;
// the shared historical schemas remain byte-for-byte unchanged.
func characterIncomingMaterialReadsSchemaV1() map[string]any {
	result := characterResourceReadsSchema()
	result["description"] = "读取已知普通文书，或明确授权若指定角色本轮实际交来唯一材料则阅读。条件来件可能是普通文书或前态已存在的版本化文书；阅读不等于接收保管、签名或相信其陈述。"
	properties := result["items"].(map[string]any)["properties"].(map[string]any)
	properties["task_id"] = schema.String("可选：条件来件阅读绑定的本人self_tasks中work任务；阅读版本化来件时必填。无需猜测未见的resource_id或version_digest，任务声明实际阅读及所需工时，只有唯一实际送达且获授权的来件可绑定。")
	return result
}

func characterIncomingMaterialDeliveriesSchemaV1() map[string]any {
	result := characterResourceDeliveriesSchema()
	properties := result["items"].(map[string]any)["properties"].(map[string]any)
	properties["delivered_at_day"] = schema.Number("仅为本轮带task_id的条件来件阅读提供真实交付/授权生效时刻，此路径必填，其他交付省略。须有发送者原授权、真实位置与交付执行依据，且不晚于接收者实际阅读开始；不能因为同轮发生便推定先交后读。")
	properties["received_fields"].(map[string]any)["description"] = "只能列匹配原resource_reports实际发送的name/unit/amount；没有对应报告就填[]，出示及访问权不自动告知名称或数量。"
	properties["evidence_refs"].(map[string]any)["description"] = "原提案或已绑定来源的实际交付依据，不把仅用于结算的机制ID或通信ID填在这里；实际使用的机制另列resolution.mechanism_refs。"
	return result
}
