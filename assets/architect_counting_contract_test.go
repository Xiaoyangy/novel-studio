package assets

import (
	"strings"
	"testing"
)

func TestArchitectCountingContractKeepsPhysicalTruthSeparateFromOwnerKnowledge(t *testing.T) {
	bundle := Load("default")
	for name, prompt := range map[string]string{"short": bundle.Prompts.ArchitectShort, "long": bundle.Prompts.ArchitectLong} {
		t.Run(name, func(t *testing.T) {
			for _, required := range []string{
				"不能因为是文书或材料就一律定性", "同一物理计数对象", "离散件数用非负整数",
				"对象本身的份数不等于容器全部内容件数", "一个资源ID也不证明只有一件实物",
				"不新增假资源副本", "世界源仍未确定真值，就保留null", "清单记载、传言或裁决自由文字",
				"角色初始仍可不知道真实数量", "绑定本人work任务与真实时点的resource_measurements",
				"不能靠“清点动作completed”自动获得件数", "不从旧自由文字补写封存代次和既有角色记忆",
			} {
				if !strings.Contains(prompt, required) {
					t.Errorf("counting contract missing %q", required)
				}
			}
			for _, forbidden := range []string{"纯权限/材料用null", "F07", "R02", "M_EVIDENCE"} {
				if strings.Contains(prompt, forbidden) {
					t.Errorf("counting contract retained a blanket rule or imported one book: %q", forbidden)
				}
			}
		})
	}
}
