package tools

import "github.com/voocel/agentcore/schema"

func physicalNullableNumberSchema(description string) map[string]any {
	return map[string]any{"type": []string{"number", "null"}, "description": description}
}

func characterResourcePerceptionSchema(observationTimePolicy ...bool) map[string]any {
	result := schema.Object(
		schema.Property("kind", schema.Enum("角色感知类型，与世界真实余额分开；新估计/观测/报告须按原提案来源绑定", "unaware", "unknown", "last_observed", "estimated", "reported")).Required(),
		schema.Property("amount", physicalNullableNumberSchema("上次观测/他人报告值；estimated/unknown/unaware不填数量")),
		schema.Property("estimate_min", physicalNullableNumberSchema("estimated时逐字复制本人提案估计下界")),
		schema.Property("estimate_max", physicalNullableNumberSchema("estimated时逐字复制本人提案估计上界")),
		schema.Property("as_of_chapter", schema.Int("感知来源所属章，未重新测量时保留旧章号")).Required(),
		schema.Property("evidence_refs", schema.Array("角色本人已知依据、原提案估计/测量请求或明确发送者报告提案digest；禁止世界余额自证", schema.String(""))).Required(),
	)
	if len(observationTimePolicy) > 0 && observationTimePolicy[0] {
		result["properties"].(map[string]any)["observed_at_day"] = schema.Number("新实测last_observed必填；必须等于原measurement.task_id对应work任务实际执行end_day，amount是该时点可确认的真实数值。不变感知保留旧时点；未测量不得刷新，周期末余额不代表较早读数")
	}
	return result
}

func characterPhysicalPostStateSchema(selfPolicies ...bool) map[string]any {
	observationTimePolicy := len(selfPolicies) > 1 && selfPolicies[1]
	holding := schema.Object(
		schema.Property("resource_id", schema.String("全世界catalog已有的opaque resource_id")).Required(),
		schema.Property("perceived_name", schema.String("仅变化时给出；不能复制作者态名称")),
		schema.Property("perceived_unit", schema.String("仅变化时给出；不能从世界单位自动补")),
		schema.Property("access", schema.Enum("仅变化时给出", "exclusive", "shared", "none")),
		schema.Property("perception", characterResourcePerceptionSchema(observationTimePolicy)),
		schema.Property("evidence_refs", schema.Array("仅变化时给出实际访问/交接来源", schema.String(""))),
	)
	result := schema.Object(
		schema.Property("agent_id", schema.String("可省略由宿主绑定；给出时必须与resolution相同")),
		schema.Property("character", schema.String("可省略由宿主绑定；给出时必须与resolution相同")),
		schema.Property("location", schema.String("行动实际结束位置，不能再复制proposal起点冒充移动后态")).Required(),
		schema.Property("resource_updates", schema.Array("推荐增量提交：按id只给变化，缺省/[]继承全部原资源与感知；不得与完整resources同时给出", holding)),
		schema.Property("resources", schema.Array("完整后态资源引用表，显式[]表示不持有；可获得世界catalog中的既有资源，不克隆真实余额", schema.Object(
			schema.Property("resource_id", schema.String("全世界catalog已有的resource_id")).Required(),
			schema.Property("perceived_name", schema.String("角色独立可知名称；保持本人已有名称或有来源交接名称，新持有人未获命名信息时用未识别资源，禁止复制作者态catalog.Name")),
			schema.Property("perceived_unit", schema.String("角色已知量纲；未知可为空，有数值感知必须明确；只能沿用本人旧单位或有来源的交接单位，禁止回退world.Unit")),
			schema.Property("access", schema.Enum("实际访问权；exclusive不能同时归属多人，共享余额只结算一次", "exclusive", "shared", "none")).Required(),
			schema.Property("perception", characterResourcePerceptionSchema(observationTimePolicy)).Required(),
			schema.Property("evidence_refs", schema.Array("取得/失去/移交/共享引用的实际来源", schema.String(""))).Required(),
		))),
	)
	result["properties"].(map[string]any)["received_facts"] = characterReceivedFactsSchema()
	if len(selfPolicies) > 0 && selfPolicies[0] {
		holding["properties"].(map[string]any)["perceived_label"] = schema.String("角色已有/实际收到的静态安全短标签，不能含位置/进行中动作/余额等动态断言")
		full := result["properties"].(map[string]any)["resources"].(map[string]any)["items"].(map[string]any)
		full["properties"].(map[string]any)["perceived_label"] = schema.String("可省略沿用本人已有静态标签；新标签必须有来源，不从作者catalog或动态名称猜")
	}
	return result
}
