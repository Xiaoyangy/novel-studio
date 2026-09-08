package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestCharacterExplicitViewPreservesDefaultFormalVisibility(t *testing.T) {
	st := store.NewStore(t.TempDir())
	rule := domain.WorldRule{Category: "记录", Rule: "作者侧固定结局不直接公开", CharacterView: "原件可以留下交接记录"}
	if domain.WorldRuleVisibility(rule) != "formal" {
		t.Fatal("fixture no longer exercises the domain's optional visibility contract")
	}
	if err := st.World.SaveWorldRules([]domain.WorldRule{rule}); err != nil {
		t.Fatal(err)
	}
	stimulus, err := buildWorldStimulus(st, "pg2_default_visibility", 1, ProjectedArcBoundary{}, domain.ProjectedPlanningContextV2{}, nil, "now")
	if err != nil {
		t.Fatal(err)
	}
	if len(stimulus.PublicFacts) != 1 || stimulus.PublicFacts[0].Visibility != "formal" || stimulus.PublicFacts[0].Text != rule.CharacterView {
		t.Fatalf("explicit view with omitted visibility was silently lost: %+v", stimulus.PublicFacts)
	}
	legacy := domain.CodexMechanism{ID: "M_LEGACY_VIEW", Name: "作者原文不回退", CharacterView: &domain.CharacterMechanismView{Name: "公开交接"}}
	if domain.CodexMechanismVisibility(legacy) != "formal" {
		t.Fatal("fixture no longer exercises the domain's legacy mechanism visibility default")
	}
	if projected, ok := characterFacingMechanism(legacy); !ok || projected.Visibility != "formal" || projected.Name != legacy.CharacterView.Name {
		t.Fatalf("legacy explicit view with omitted visibility was silently lost: %+v", projected)
	}
	legacy.Visibility = "unknown-label"
	if _, ok := characterFacingMechanism(legacy); ok {
		t.Fatal("unknown nonempty visibility inherited the default-formal path")
	}
}

func TestCharacterObservationConsumesOnlyExplicitWorldViews(t *testing.T) {
	st := store.NewStore(t.TempDir())
	const authorTruth = "第三章须证明父亲只领六十升，许岚另外转走三十升。"
	const privateView = "不得向不知情角色透露的秘密投影"
	rules := []domain.WorldRule{
		{Category: "终局合同", Rule: authorTruth, Boundary: "终局不得反转", Visibility: "formal", CharacterView: "有来源的原件可以交叉核对，记账更正必须保留原页。"},
		{Category: "作者约束", Rule: "第一章拆封第二章取证第三章结案", Visibility: "formal"},
		{Category: "隐秘事实", Rule: "许岚是改账者", Visibility: "secret", CharacterView: privateView},
		{Category: "非法标签", Rule: "不应公开", Visibility: "unrecognized", CharacterView: "非法可见性投影"},
		{Category: "礼俗", Rule: "先称呼再询问", Visibility: "informal", CharacterView: "向人借阅东西应先说明用途。"},
	}
	if err := st.World.SaveWorldRules(rules); err != nil {
		t.Fatal(err)
	}
	mechanism := domain.CodexMechanism{
		ID: "M_EVIDENCE", Name: "许岚改账的作者答案", Visibility: "formal",
		SectionRefs: []string{"secret-author-section"},
		ActorScope:  []string{"作者预定责任人"}, Trigger: "第三章固定取证",
		Preconditions: []string{authorTruth}, Inputs: []string{"隐藏票据的固定原数六十升"},
		Costs: []string{"第二章固定失去保管资格"}, Effects: []string{authorTruth},
		FailureModes: []string{"不得改变结局清白"}, Observability: []string{"作者全知"},
		Timing: "第三章完成", Cooldown: "结局后结束",
		CharacterView: &domain.CharacterMechanismView{
			Name: "纸质证据核验", ActorScope: []string{"实际取得原件的人"}, Trigger: "原件被出示后核对",
			Preconditions: []string{"编号、形成来源和经手链可核对"}, Inputs: []string{"实际取得的材料"},
			Costs: []string{"每处原件核对预留五分钟"}, Effects: []string{"足够证据可支持登记事实更正"},
			FailureModes:  []string{"涂改本身不能证明原始数量或经手人"},
			Observability: []string{"只知实际看过的材料和可验证推断"}, Timing: "完成核对后才能登记更正",
		},
	}
	codex := domain.WorldCodex{Mechanisms: []domain.CodexMechanism{
		mechanism,
		{ID: "M_SECRET", Name: "隐秘改账", Visibility: "secret", CharacterView: &domain.CharacterMechanismView{Name: privateView}},
		{ID: "M_NO_VIEW", Name: "原文含秘密但未单列投影", Visibility: "formal", Effects: []string{authorTruth}},
		{ID: "M_UNKNOWN", Name: "错误标签", Visibility: "unexpected", CharacterView: &domain.CharacterMechanismView{Name: "未知标签投影"}},
	}}
	if err := st.SaveWorldCodex(codex); err != nil {
		t.Fatal(err)
	}
	stimulus, err := buildWorldStimulus(st, "pg2_world_view", 1, ProjectedArcBoundary{Goal: "作者软方向"}, domain.ProjectedPlanningContextV2{}, nil, "now")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stimulus.Mechanisms, codex.Mechanisms) || !strings.Contains(strings.Join(stimulus.HardContracts, "\n"), authorTruth) {
		t.Fatal("character redaction removed the Arbiter's complete authored evidence")
	}
	if len(stimulus.PublicFacts) != 2 {
		t.Fatalf("non-explicit or secret rules entered public stimulus: %+v", stimulus.PublicFacts)
	}
	// Unmarked legacy/public payloads must not become character knowledge merely
	// because a caller inserted them into the stimulus's PublicFacts array.
	stimulus.PublicFacts = append(stimulus.PublicFacts,
		newCharacterAgentFact("world_rule", authorTruth, "world_rules.json", "formal"),
		newCharacterAgentFact(characterWorldRuleViewFactKind, privateView, "world_rules.json", "secret"),
	)
	build := func(name, agentID string, known []string) domain.CharacterObservationPacket {
		t.Helper()
		observation, err := buildCharacterObservation(st, stimulus.GenerationID, 1, characterAgentProfile{
			Character:  domain.Character{Name: name, Role: "当班人员", Traits: []string{"谨慎"}},
			Record:     domain.CharacterAgentRecord{AgentID: agentID, Character: name},
			Continuity: &domain.CharacterContinuityEntry{CurrentFacts: known},
		}, stimulus, domain.ProjectedPlanningContextV2{}, "now")
		if err != nil {
			t.Fatal(err)
		}
		return observation
	}
	observer := build("林澄", "ca_lin", []string{"眼前封条完整"})
	witness := build("周砚", "ca_zhou", []string{"我实际接收救援油六十升"})
	for _, observation := range []domain.CharacterObservationPacket{observer, witness} {
		raw, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{authorTruth, privateView, "第三章", "第二章", "许岚是改账者", "secret-author-section", "作者预定责任人", "三十升", "隐藏票据", "M_SECRET", "M_NO_VIEW", "M_UNKNOWN"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("%s received author-only or undisclosed world content %q: %s", observation.Character, forbidden, raw)
			}
		}
		if len(observation.PublicRules) != 2 || len(observation.PublicMechanisms) != 1 {
			t.Fatalf("explicit safe views were dropped: %+v", observation)
		}
		projected := observation.PublicMechanisms[0]
		if projected.ID != mechanism.ID || projected.Name != mechanism.CharacterView.Name ||
			!reflect.DeepEqual(projected.Effects, mechanism.CharacterView.Effects) ||
			!reflect.DeepEqual(projected.Preconditions, mechanism.CharacterView.Preconditions) ||
			projected.CharacterView != nil || len(projected.SectionRefs) != 0 || projected.Cooldown != "" {
			t.Fatalf("public mechanism was not rebuilt from its explicit view: %+v", projected)
		}
	}
	observerJSON, _ := json.Marshal(observer)
	witnessJSON, _ := json.Marshal(witness)
	if strings.Contains(string(observerJSON), "六十升") || !strings.Contains(string(witnessJSON), "我实际接收救援油六十升") {
		t.Fatal("private established knowledge was leaked or deleted instead of kept per character")
	}
	observer.PublicMechanisms[0].Effects[0] = "mutated observation"
	if stimulus.Mechanisms[0].CharacterView.Effects[0] == "mutated observation" || witness.PublicMechanisms[0].Effects[0] == "mutated observation" {
		t.Fatal("public mechanism views share mutable slices across character observations or Arbiter source")
	}
}
