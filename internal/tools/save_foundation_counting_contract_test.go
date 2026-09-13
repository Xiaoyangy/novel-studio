package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSaveFoundationCountContractMatchesSchemaAndRepairHint(t *testing.T) {
	raw, err := json.Marshal(NewSaveFoundationTool(store.NewStore(t.TempDir())).Schema())
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range []string{string(raw), foundationShapeHint("characters")} {
		for _, required := range []string{
			"文书或材料不一律定性", "同一物理计数对象", "离散件数为非负整数",
			"对象份数不等于容器全部内容件数", "资源ID数量也不证明实物件数", "不新增假资源副本",
			"世界真值仍未知则保留null", "不从清单、传言或裁决自由文字补数",
			"resource_measurements", "绑定本人work、真实观测时点", "仅动作completed不能生成件数",
			"不回填封存代次或旧记忆",
		} {
			if !strings.Contains(hint, required) {
				t.Errorf("counting schema/repair hint missing %q", required)
			}
		}
		if strings.Contains(hint, "定性权限/材料用null") {
			t.Fatal("blanket qualitative-material rule contradicts counting")
		}
	}
}

func TestSaveFoundationCountTruthIsExplicitAndDoesNotBecomeOwnerKnowledge(t *testing.T) {
	two := 2.0
	for _, actual := range []*float64{&two, nil} {
		t.Run(map[bool]string{true: "defined", false: "unknown"}[actual != nil], func(t *testing.T) {
			st := store.NewStore(t.TempDir())
			if err := st.Init(); err != nil {
				t.Fatal(err)
			}
			var characters []domain.Character
			var registry domain.CharacterAgentRegistry
			for _, name := range []string{"甲", "乙"} {
				characters = append(characters, domain.Character{Name: name, Tier: "core", InitialState: &domain.CharacterInitialState{
					Location: "库房", CurrentGoal: "核对这批物料", Pressure: "交接临近", KnownFacts: []string{"尚未逐件点数"},
					Resources: []string{"旧外清单声称有一件，不能直接当实物件数"},
					ResourceBalances: []domain.InitialCharacterResourceV2{{ResourceID: "res_bbf0000000000001", Name: "同一物理原件批次",
						PerceivedLabel: "待点数的原件批次", Unit: "件", PerceivedUnit: "件", ActualAmount: actual,
						Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}, EvidenceRefs: []string{"explicit-world-source"}}},
				}})
				var err error
				registry, _, err = registry.UpsertCharacter(name, nil, "core", 1, "now")
				if err != nil {
					t.Fatal(err)
				}
			}
			args, err := json.Marshal(map[string]any{"type": "characters", "content": characters})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			stored, err := st.Characters.Load()
			if err != nil || !reflect.DeepEqual(stored, characters) {
				t.Fatalf("initial counting definition changed: %v", err)
			}
			world, err := domain.BuildWorldPhysicalStateFromInitialV2(stored, registry)
			if err != nil || len(world.Resources) != 1 || !reflect.DeepEqual(world.Resources[0].ActualAmount, actual) {
				t.Fatalf("shared physical source was cloned or inferred from the old manifest: %v", err)
			}
			for _, actor := range world.Actors {
				views, err := domain.BuildCharacterResourceViewsV2(world, actor.AgentID)
				if err != nil || len(views) != 1 || views[0].Perception.Kind != "unknown" || views[0].Perception.Amount != nil {
					t.Fatalf("unmeasured world count became owner knowledge: %v", err)
				}
			}
		})
	}
}
