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
		schema.Property("resource_refs", texts("仅world_state.resources已有resource_id。requires_readable=true时每个引用都必须有非空readable_facts，只列实际读取的文书；钥匙、油料、工具等非文书依赖另列requires_readable=false的操作，不混入本数组；无实体则空")).Required(),
		schema.Property("status", schema.Enum("资料可用性，不默认创建", "available", "missing", "unclear", "not_required")).Required(),
		schema.Property("explanation", schema.String("来源、可读事实与真实缺口")).Required(),
	)
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
	if t.body != nil {
		return nil, fmt.Errorf("rehearsal stage already submitted")
	}
	t.body = &body
	return json.Marshal(map[string]any{"submitted": true, "authority": "speculative", "input_digest": t.input.InputDigest})
}
