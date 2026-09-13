package assets

import (
	"strings"
	"testing"
)

// This checks the shipped authoring guidance, not semantic completeness of
// arbitrary natural-language world data or an automatic secret classifier.
func TestArchitectPublicConditionsGuidanceIsLoadedAndProjectNeutral(t *testing.T) {
	bundle := Load("default")
	for name, prompt := range map[string]string{
		"short": bundle.Prompts.ArchitectShort,
		"long":  bundle.Prompts.ArchitectLong,
	} {
		t.Run(name, func(t *testing.T) {
			for _, required := range []string{
				"已定义且应公开的操作必要工时、数值门槛和前置必须完整保留",
				"不能只留在作者态 `rule/boundary`",
				"`costs/timing/preconditions`",
				"既有值、单位、适用对象、每次或累计口径",
				"“最低/预计/通常”的原有约束强度",
				"已有“至少 N 分钟”不能改写成“占实际工时”",
				"不能把预计预算升级为绝对最低条件",
				"公开操作数值不等于未观测实例数量",
				"未测余额、当前状态、他人秘密及未来结果仍不得成为角色已知",
				"不得为补齐视图而新造固定工时、数值门槛或额外程序",
				"不得扩大已有规则的适用范围",
				"复用已有 `counterfactual_tests` 做角色视图自检",
				"模拟只读角色视图",
				"无需猜测 Arbiter 私有条款",
				"确属应隐藏的条件仍保留未知及合法获知途径",
				"`given/action/expected_outcome/forbidden_outcome/mechanism_refs`",
				"在本次创建内完成，不新增 schema 字段、额外模型调用或常态评审阶段",
			} {
				if !strings.Contains(prompt, required) {
					t.Errorf("loaded authoring guidance missing %q", required)
				}
			}
			for _, projectSpecific := range []string{"F07", "R02", "M_EVIDENCE", "5分钟", "8分钟", "五分钟", "八分钟"} {
				if strings.Contains(prompt, projectSpecific) {
					t.Errorf("authoring guidance imports one project's value or object %q", projectSpecific)
				}
			}
		})
	}
}
