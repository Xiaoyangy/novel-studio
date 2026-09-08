package tools

import "github.com/voocel/agentcore/schema"

func characterWorkOutputRequestsSchema() map[string]any {
	claim := schema.Object(
		schema.Property("claim_id", schema.String("本记录内稳定的声明ID；不是世界真相认证")).Required(),
		schema.Property("text", schema.String("本人实际打算写入的原文；裁判只能裁决哪些条目已写入，不能替换文字")).Required(),
		schema.Property("epistemic_kind", schema.Enum("保持原来源性质；派生记录不能升级成独立证据", "document_statement", "attributed_statement", "self_statement", "pending")).Required(),
		schema.Property("source_refs", schema.Array("仅本人已知的fact/memory/artifact claim来源，不能用未读作者资料", schema.String(""))).Required(),
		schema.Property("attributed_to", schema.String("attributed_statement必填原陈述者；不是转述者担保内容真实")),
	)
	return schema.Array("本人任务的明确文书产物意图；不声明产物时，工时完成不会自动生成可读文档", schema.Object(
		schema.Property("output_key", schema.String("同一任务内稳定产物键，续写不改键；Host据此分配稳定资源ID")).Required(),
		schema.Property("resource_id", schema.String("首次创建省略；续写必须使用本人已知的同一产物ID")),
		schema.Property("expected_version_digest", schema.String("首次创建省略；续写必须绑定实际已知版本，不能覆盖他人修改")),
		schema.Property("label", schema.String("本人为产物选择的短名称；不得把整批原料改名")).Required(),
		schema.Property("material_inputs", schema.Array("首次创建实际分配的已知材料及数量；已分配材料在续写时不得再次扣除", schema.Object(
			schema.Property("resource_id", schema.String("本人可访问的原料资源ID")).Required(),
			schema.Property("amount", schema.Number("明确正数投入量，按真实结算只扣一次")).Required(),
		))),
		schema.Property("claims", schema.Array("本次拟写的有限声明与来源；pending保持待核", claim)).Required(),
	))
}

func characterWorkOutputResultsSchema() map[string]any {
	return schema.Array("只落实对应任务原output_requests的实际产出；不能依据completed工时或自由描述自动补文书", schema.Object(
		schema.Property("output_key", schema.String("原任务声明的稳定output_key")).Required(),
		schema.Property("status", schema.Enum("首次真实产出created；既有版本续写updated；未产出blocked", "created", "updated", "blocked")).Required(),
		schema.Property("at_day", schema.Number("created/updated须等于对应本人self_execution的end_day，不取其他角色的终点；blocked须在本轮真实裁决窗口内")).Required(),
		schema.Property("claim_ids", schema.Array("本段实际写入的原声明ID子集；不传正文、位置、资源ID或版本摘要", schema.String(""))),
		schema.Property("complete", schema.Bool("仅表示这份有限声明文书已完成，不证明其陈述为真或硬合同已满足")).Required(),
	))
}

func characterArtifactReadsSchema(actual bool) map[string]any {
	item := schema.Object(
		schema.Property("resource_id", schema.String("本人可访问、已存在的文书产物ID")).Required(),
		schema.Property("version_digest", schema.String("本次拟核读/实际核读的精确正文版本；观察中未读版本的摘要仅用于绑定，不证明已知正文")).Required(),
		schema.Property("claim_ids", schema.Array("核读的声明ID范围；意图可用空数组请求整个授权版本，实际结果必须列出真正读到的声明ID", schema.String(""))).Required(),
	)
	if actual {
		item["properties"].(map[string]any)["at_day"] = schema.Number("实际完成这次授权阅读的时刻，须在真实裁决窗口内")
		item["required"] = append(item["required"].([]string), "at_day")
		return schema.Array("只记录本人已请求且确实发生的版本读取；收到文书或听说写成不等于读过", item)
	}
	item["properties"].(map[string]any)["task_id"] = schema.String("本人 self_tasks 中实际执行阅读的 work 任务ID；任务必须绑定该文书资源，不能用等待或无关工作代替")
	item["required"] = append(item["required"].([]string), "task_id")
	return schema.Array("本人明确选择读取已知文书的指定版本；新版本不因旧版读过而自动已知", item)
}

func characterArtifactSignsSchema(actual bool) map[string]any {
	item := schema.Object(
		schema.Property("resource_id", schema.String("本人实际读过或亲自写入的文书产物ID")).Required(),
		schema.Property("version_digest", schema.String("签名绑定的本人已知且status=complete的正文版本，不签草稿、未读或未来版本")).Required(),
		schema.Property("claim_ids", schema.Array("本人主动同意签认的有限声明ID", schema.String(""))).Required(),
		schema.Property("scope", schema.String("本人签认范围，保持来源/待核边界；不是代签或对全篇真相担保")).Required(),
	)
	if actual {
		item["properties"].(map[string]any)["at_day"] = schema.Number("本人实际签认时刻，须在真实裁决窗口内")
		item["required"] = append(item["required"].([]string), "at_day")
		return schema.Array("只落实该角色原artifact_signs；Host绑定签署者、版本和签名摘要，不得替角色改范围", item)
	}
	item["properties"].(map[string]any)["task_id"] = schema.String("本人 self_tasks 中实际执行签认的 work 任务ID；任务必须绑定该文书资源，不能用等待或无关工作代替")
	item["required"] = append(item["required"].([]string), "task_id")
	return schema.Array("本人独立选择签认已实际读过或亲自写入的已完成版本与范围；文字修改后旧签名不能替新版本背书", item)
}

func characterArtifactAccessSchema() map[string]any {
	return schema.Array("本人主动允许既有角色访问指定文书版本；口头告知名称不等于授权，世界裁判仍须确认实际同地出示/交付", schema.Object(
		schema.Property("resource_id", schema.String("本人实际持有/有权授权的产物ID")).Required(),
		schema.Property("version_digest", schema.String("明确授权的已知内容版本")).Required(),
		schema.Property("to_character", schema.String("接收者角色名，由Host匹配稳定身份；不要猜他人的agent_id")).Required(),
		schema.Property("access", schema.Enum("shared只授查阅，exclusive才转移独占持有", "shared", "exclusive")).Required(),
	))
}
