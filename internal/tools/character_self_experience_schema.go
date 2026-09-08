package tools

import (
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore/schema"
)

func characterSelfTasksSchema(operational ...bool) map[string]any {
	result := schema.Array("每轮至少声明一项本人任务；未执行也须让裁决记录not_started/blocked。不能从角色终点推定工具已携带；续做同一作业复用task_id及定义，不重新报总进度", schema.Object(
		schema.Property("task_id", schema.String("本人任务稳定ID，跨章继续同一任务时原样复用")).Required(),
		schema.Property("kind", schema.Enum("work=可累计有效工时；carry=从本章起点明确携至实际终点；place=在本章起点明确归放", "work", "carry", "place")).Required(),
		schema.Property("action", schema.String("简明、稳定的本人任务定义；不要重复整段intended_action，不写他人行动或已完成结果；续作同task_id保持原定义")).Required(),
		schema.Property("resource_ids", schema.Array("仅本人已知且有相应权限的resource_views稳定ID；搬运/归放必须明确资源，shared查阅不授予搬运", schema.String("resource_id"))),
		schema.Property("progress_target", physicalNullableNumberSchema("work可选：本人已知的总目标有效分钟数；续作保持原总目标，不是已完成量或剩余量")),
		schema.Property("progress_unit", schema.Enum("work使用minute；carry/place省略", "minute")),
		schema.Property("knowledge_refs", schema.Array("本人观察中可引用的fact/memory/已知资源证据id", schema.String(""))).Required(),
	))
	result["minItems"] = 1
	result["maxItems"] = domain.CharacterSelfTaskLimitV2
	if len(operational) > 0 && operational[0] {
		result["items"].(map[string]any)["properties"].(map[string]any)["observation_requests"] = characterOperationalRequestsSchema()
	}
	return result
}

func characterSelfExecutionsSchema(operational ...bool) map[string]any {
	result := schema.Array("本角色原提案self_tasks的实际执行结果；累计进度/经历/携带位置由宿主按这些区间确定性生成，不在post_state自由填写", schema.Object(
		schema.Property("task_id", schema.String("该角色本轮已提交的self_tasks.task_id，不能替他人新建行动")).Required(),
		schema.Property("status", schema.Enum("只记已发生程度；未执行不能伪造有效工时或搬运", "not_started", "in_progress", "completed", "blocked")).Required(),
		schema.Property("start_day", physicalNullableNumberSchema("实际执行区间开始故事日，必须在story_time内；not_started/blocked省略")),
		schema.Property("end_day", physicalNullableNumberSchema("实际执行区间结束故事日；work工时按(end-start)*1440累计，同人work区间不可重叠。carry/place完成才改变known_placement")),
	))
	if len(operational) > 0 && operational[0] {
		result["items"].(map[string]any)["properties"].(map[string]any)["observation_results"] = characterOperationalResultsSchema()
	}
	return result
}

func characterOperationalRequestsSchema() map[string]any {
	result := schema.Array("可选：本人决定检查已知非计量资源对指定用途的局部可操作性；仅用于work，不代替数量测量、文档读取或完整验收", schema.Object(
		schema.Property("request_id", schema.String("本轮唯一检查请求ID")).Required(),
		schema.Property("resource_id", schema.String("本人已知且有访问权的非计量resource_id；禁止数字余额及文档内容")).Required(),
		schema.Property("purpose", schema.String("本人希望检查的具体局部用途；只是意图，不能夹带已发生事实或整个任务完成结论")).Required(),
		schema.Property("mechanism_ref", schema.String("本人已知、并明确列入mechanism_refs的实际检查机制ID")).Required(),
		schema.Property("knowledge_refs", schema.Array("本人观察包可引用证据ID", schema.String(""))).Required(),
	))
	result["maxItems"] = domain.CharacterOperationalObservationRequestLimitV1
	return result
}

func characterOperationalResultsSchema() map[string]any {
	result := schema.Array("只对本任务原有请求填写实际观察；执行过的请求必须返回三态之一，未执行不得产出结果。宿主派生本人记忆，不在post_state抄写", schema.Object(
		schema.Property("request_id", schema.String("同一任务原 observation_requests 的 request_id")).Required(),
		schema.Property("result", schema.Enum("只表示当时该局部用途的可操作性；available不是容量、文档内容或整体检查合格，不能确定时inconclusive", "available", "unavailable", "inconclusive")).Required(),
		schema.Property("observed_at_day", schema.Number("角色实际观察时刻，必须在该任务实际 start_day/end_day 区间内")).Required(),
	))
	result["maxItems"] = domain.CharacterOperationalObservationRequestLimitV1
	return result
}
