package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
)

func TestCharacterPhysicalObservationDoesNotInheritAuthorNameUnitOrLegacyResourceText(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	amount := 11.8
	character := domain.Character{Name: "角色", Role: "主角", Tier: "core", InitialState: &domain.CharacterInitialState{
		Location: "A", CurrentGoal: "核对自己知道的信息", Pressure: "等待实际交接", KnownFacts: []string{"尚未读到余量"},
		Resources: []string{"SECRET作者态旧资源11.8"}, ResourceBalances: []domain.InitialCharacterResourceV2{
			{ResourceID: "res_0000000000000001", Name: "SECRET作者名称11.8", Unit: "SECRET世界单位11.8", ActualAmount: &amount, PerceivedName: "未识别资源", Access: "shared", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
			{ResourceID: "res_0000000000000002", Name: "SECRET内部许可", Unit: "", ActualAmount: nil, PerceivedName: "合法操作资格", PerceivedLabel: "合法操作资格", Access: "exclusive", Perception: domain.ResourcePerceptionV2{Kind: "unknown"}},
		},
	}}
	if err := st.Characters.Save([]domain.Character{character}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "角色等待", CoreEvent: "角色核对已知信息"}}); err != nil {
		t.Fatal(err)
	}
	observations, err := BuildCharacterObservationsForProjectedState(st, "pg2_physical_privacy", 1, domain.ProjectedPlanningContextV2{})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations=%d", len(observations))
	}
	for _, observation := range observations {
		if len(observation.Resources) != 0 || len(observation.ResourceViews) != 2 {
			t.Fatalf("v2 resource view duplicated or omitted: %+v", observation)
		}
		raw, _ := json.Marshal(observation)
		raw = testutil.CharacterObservationPrivacyJSON(t, raw)
		for _, forbidden := range []string{"SECRET", "11.8", "actual_amount", "readable_facts"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("author state leaked via %s: %s", forbidden, raw)
			}
		}
		if !strings.Contains(string(raw), "合法操作资格") {
			t.Fatal("qualitative capability was lost")
		}
		if observation.Location != "A" || observation.Version != domain.CharacterObservationV2Version {
			t.Fatal("initial physical observation has wrong origin/version")
		}
	}
}

func TestCharacterProtocolVersionsDoNotMixOrFallBackSilently(t *testing.T) {
	if old, current := CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolVersion), CharacterAgentProtocolDigestForVersion(domain.CharacterAgentDecisionProtocolV2Version); old == "" || current == "" || old == current {
		t.Fatalf("v1/v2 protocol identities not separated: %q/%q", old, current)
	}
}
