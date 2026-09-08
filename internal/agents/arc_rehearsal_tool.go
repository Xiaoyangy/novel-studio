package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/voocel/agentcore/schema"
)

type submitArcRehearsalTool struct {
	input domain.ArcRehearsalInput
	draft *domain.ArcRehearsalDraft // Host-only; never decoded from submission.
	body  *domain.ArcRehearsalBody
}

func (*submitArcRehearsalTool) Name() string { return "submit_arc_rehearsal" }
func (*submitArcRehearsalTool) Description() string {
	return "提交整弧条件性宏观预演；不执行角色行动、生成正文或更新正史。身份、阶段和模型调用来源由Host绑定。"
}
func (*submitArcRehearsalTool) Schema() map[string]any {
	texts := func(description string) map[string]any { return schema.Array(description, schema.String("")) }
	chapter := schema.Object(
		schema.Property("chapter", schema.Int("原章位")).Required(),
		schema.Property("conditional_forecast", schema.String("未来章的条件预测；已接受章逐字复制原摘要")).Required(),
		schema.Property("assumptions", texts("条件假设；不得伪称已执行")).Required(),
		schema.Property("causal_links", texts("假设下的因果关系")).Required(),
		schema.Property("time_resource_checks", texts("时间资源限制与未决条件")).Required(),
		schema.Property("accepted_source_digest", schema.String("仅已接受章填写其accepted_evidence摘要")),
	)
	character := schema.Object(
		schema.Property("character", schema.String("当前观察中的角色名")).Required(),
		schema.Property("current_goal", schema.String("逐字复制当前目标")).Required(),
		schema.Property("conflicts", texts("目标冲突")).Required(),
		schema.Property("conditional_choices", texts("条件性可能行为，不是正式决定")).Required(),
	)
	contract := schema.Object(
		schema.Property("contract", schema.String("输入硬要求原文")).Required(),
		schema.Property("assessment", schema.Enum("预测可达性，不是实际硬冲突裁决", "plausible", "conditional", "unresolved", "infeasible_prediction")).Required(),
		schema.Property("conditions", texts("判断所需条件与依据")).Required(),
	)
	material := schema.Object(
		schema.Property("operation", schema.String("关键操作；复核保留草案的同名操作")).Required(),
		schema.Property("requires_readable", schema.Bool("是否依赖读取既有文书/规程")).Required(),
		schema.Property("resource_refs", texts("仅world_state.resources已有resource_id。读取原始文书须有readable_facts，已有产物须有artifact并使用artifact_read；未来产物只填依赖的artifact_ref，不造resource_id。钥匙、油料、工具等非文书依赖另列requires_readable=false的操作；无实体则空")).Required(),
		schema.Property("status", schema.Enum("资料可用性，不默认创建", "available", "missing", "unclear", "not_required")).Required(),
		schema.Property("explanation", schema.String("来源、可读事实与真实缺口")).Required(),
	)
	dependency := schema.Object(
		schema.Property("key", schema.String("全报告唯一声明键；不是resource_id，后续depends_on按此引用")).Required(),
		schema.Property("kind", schema.Enum("执行API类型；self_work/resource_use不产生新观察或许可", "resource_read", "resource_measurement", "operational_observation", "communication", "resource_delivery", "resource_use", "self_work", "artifact_write", "artifact_read", "artifact_sign", "unsupported")).Required(),
		schema.Property("actor_ref", schema.String("world_state.actors中的agent_id；未知/不支持岗位只能missing/unclear")).Required(),
		schema.Property("recipient_ref", schema.String("通信/交付必须指定不同的现有actor；不接受背景职务名")),
		schema.Property("resource_refs", texts("本动作的实际现有resource_id；读取/测量/局部设备检查各一个，未来产物不填假ID")),
		schema.Property("mechanism_refs", texts("实际公开机制ID；测量/局部设备检查必填，机制不能代替对象")),
		schema.Property("artifact_ref", schema.String("较早artifact_write的key，表示未来预期版本；同时列入depends_on，与resource_refs互斥")),
		schema.Property("depends_on", texts("较早依赖key；不同actor的未来产物使用还需实际交付路径，签认需本人读过该预期版本")),
		schema.Property("material_inputs", schema.Array("仅首次写成未来产物分配现有量化材料，不造新资源；修改不重复分配", schema.Object(
			schema.Property("resource_id", schema.String("现有材料resource_id")).Required(),
			schema.Property("amount", schema.Number("有限正数量")).Required(),
		))),
	)
	material["properties"].(map[string]any)["capability_requirements"] = schema.Array("按执行依赖顺序声明；available必须非空并通过Host能力门禁。未来产物使用报告内声明键，不是现有资源", dependency)
	material["required"] = append(material["required"].([]string), "capability_requirements")
	return schema.Object(
		schema.Property("summary", schema.String("条件性预演概述，不是实际结果")).Required(),
		schema.Property("chapters", schema.Array("按顺序覆盖整弧全部章位", chapter)).Required(),
		schema.Property("character_conflicts", schema.Array("覆盖当前主要角色", character)).Required(),
		schema.Property("contract_checks", schema.Array("覆盖全部hard_contracts", contract)).Required(),
		schema.Property("material_checks", schema.Array("关键操作实际资料检查，未定义文书不能当可读", material)).Required(),
		schema.Property("unresolved_items", texts("保留尚未确定的条件与资料缺口")).Required(),
	)
}
func (t *submitArcRehearsalTool) Execute(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("arc rehearsal output exceeds bounded size")
	}
	var body domain.ArcRehearsalBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("rehearsal output must contain one JSON object")
	}
	if err := domain.ValidateArcRehearsalBody(t.input, body); err != nil {
		return nil, err
	}
	if t.draft != nil {
		verified, err := domain.FinalizeArcRehearsalDraft(t.input, *t.draft)
		if err != nil || !sameCharacterCycleValue(verified, *t.draft) {
			return nil, fmt.Errorf("rehearsal review lacks its exact verified draft")
		}
		if err := domain.ValidateArcRehearsalReviewBody(t.input, t.draft.Body, body); err != nil {
			return nil, err
		}
	}
	if t.body != nil {
		return nil, fmt.Errorf("rehearsal stage already submitted")
	}
	t.body = &body
	return json.Marshal(map[string]any{"submitted": true, "authority": "speculative", "input_digest": t.input.InputDigest})
}
