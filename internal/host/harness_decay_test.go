package host

import "testing"

func TestHarnessRegistryRequiredFields(t *testing.T) {
	if len(HarnessRegistry) == 0 {
		t.Fatal("HarnessRegistry 不应为空")
	}
	for key, m := range HarnessRegistry {
		if m.Name != key {
			t.Errorf("注册键 %q 与 Name %q 不一致", key, m.Name)
		}
		if m.Reason == "" {
			t.Errorf("%s 缺少 Reason", key)
		}
		switch m.DecayClass {
		case DecayBusinessLogic, DecayModelGap, DecayInterim:
		default:
			t.Errorf("%s 的 DecayClass 非法: %s", key, m.DecayClass)
		}
		// model_gap / interim 必须写明重估依据，否则永远不会被重新审视。
		if m.DecayClass != DecayBusinessLogic && m.DecayNote == "" {
			t.Errorf("%s 是 %s 类，必须写 DecayNote", key, m.DecayClass)
		}
	}
}

// TestHarnessRegistryCoversKnownGuards 防漏登记：reminder 包导出的 Guard 构造器
// 与 agents 包的 ToolGate 必须在注册表中有对应条目。新增组件时同步登记。
func TestHarnessRegistryCoversKnownGuards(t *testing.T) {
	required := []string{
		"coordinator_stop_guard",
		"writer_stop_guard",
		"architect_stop_guard",
		"editor_stop_guard",
		"subagent_checkpoint_delta_guard",
		"tool_gate_complete_phase",
		"writer_expanded_chapter_gate",
		"budget_sentinel",
	}
	for _, name := range required {
		if _, ok := HarnessRegistry[name]; !ok {
			t.Errorf("HarnessRegistry 缺少已知组件 %s", name)
		}
	}
}
