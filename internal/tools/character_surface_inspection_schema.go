package tools

import "github.com/voocel/agentcore/schema"

// Only the explicitly opted-in producer calls these extensions. The original
// operational schemas remain byte-identical for all frozen older producers.
func characterSurfaceInspectionRequestsSchemaV1() map[string]any {
	result := characterOperationalRequestsSchema()
	result["description"] = "本人work的实际检查请求；surface为空沿原非文书局部可操作性，非空只检查同一resource_id明确inspectable_surfaces允许的外表面，不读取文字或内部内容"
	props := result["items"].(map[string]any)["properties"].(map[string]any)
	props["resource_id"] = schema.String("本人已知可访问的既有resource_id；表面检查必须选择该resource_views.inspectable_surfaces中的面，不新建封条或物体ID")
	props["surface"] = schema.Enum("可选：该资源明确提供的外表面；省略沿原局部可操作性，不作为已经完好的事实", "container_exterior", "seal_exterior")
	return result
}

func characterSurfaceInspectionResultsSchemaV1() map[string]any {
	result := characterOperationalResultsSchema()
	props := result["items"].(map[string]any)["properties"].(map[string]any)
	props["surface"] = schema.Enum("原请求的准确surface；原请求为空则省略，不能换面或从结果猜面", "container_exterior", "seal_exterior")
	props["result"] = schema.Enum("surface非空仅用no_visible_damage/visible_damage/indeterminate，表示当时指定外表面所见，不证明历史未拆封、文字内容、许可或整体安全；surface为空仅用原available/unavailable/inconclusive", "available", "unavailable", "inconclusive", "no_visible_damage", "visible_damage", "indeterminate")
	return result
}
