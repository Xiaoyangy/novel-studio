package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/voocel/agentcore"
)

const scopedReferencePrompt = `
这是Host绑定的只读模型视图。body保留实际事实与意图；binding只验证来源与视图身份，不是可引用事实。typed引用字段中的@refN是当前包内局部句柄，Host会在工具执行前恢复原引用再严格验证。只在对应引用字段使用这些句柄；decision/action/feedback等自然语言字段使用本人已知的自然名称或中性描述，不要把运输句柄当作人物、事件或叙事文字。旧记忆文字内原有的ID原文没有替换，不代表获得额外权限；task_id不是已执行证据，机制来源也不等于角色获得了知识。`

// Each wrapper belongs to exactly one immutable input/codec. It cannot choose
// a new observation, proposal set or source mapping from model arguments.
type scopedReferenceTool struct {
	agentcore.Tool
	codec   *modelinput.ScopedReferenceCodec
	binding modelinput.ScopedReferenceBinding
}

func newScopedReferenceTool(tool agentcore.Tool, codec *modelinput.ScopedReferenceCodec) (agentcore.Tool, error) {
	if tool == nil || codec == nil {
		return nil, fmt.Errorf("scoped reference tool requires its original bound tool and input")
	}
	return &scopedReferenceTool{Tool: tool, codec: codec, binding: codec.Binding()}, nil
}

func (t *scopedReferenceTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if err := t.codec.ValidateModelArguments(args, t.binding); err != nil {
		return nil, err
	}
	expanded, err := t.codec.ExpandArguments(args, t.binding)
	if err != nil {
		return nil, err
	}
	return t.Tool.Execute(ctx, expanded)
}

func (t *scopedReferenceTool) ReadOnly(args json.RawMessage) bool {
	expanded, err := t.codec.ExpandArguments(args, t.binding)
	if err != nil {
		return false
	}
	if original, ok := t.Tool.(interface{ ReadOnly(json.RawMessage) bool }); ok {
		return original.ReadOnly(expanded)
	}
	return false
}

func (t *scopedReferenceTool) ConcurrencySafe(args json.RawMessage) bool {
	expanded, err := t.codec.ExpandArguments(args, t.binding)
	if err != nil {
		return false
	}
	if original, ok := t.Tool.(interface{ ConcurrencySafe(json.RawMessage) bool }); ok {
		return original.ConcurrencySafe(expanded)
	}
	return false
}
