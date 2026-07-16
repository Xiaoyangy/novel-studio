package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

const rewriteAuthorityEvidenceForTest = "二姨把汤勺递给赵航，让他去盛汤。"

func TestRewriteSourceEvidenceKeepsDialogueClosingQuoteWithSentence(t *testing.T) {
	body := "赵航笑了一声，二姨说：“去盛汤，别只顾听热闹。”下一句没有人物名。\n林澈点头。"
	units := rewriteSourceEvidenceUnits(body)
	if len(units) != 3 {
		t.Fatalf("unexpected evidence units: %#v", units)
	}
	if units[0] != "赵航笑了一声，二姨说：“去盛汤，别只顾听热闹。”" {
		t.Fatalf("sentence-final closing quote was detached: %#v", units[0])
	}
}

func TestRewriteSourceEvidenceKeepsStackedTerminalPunctuationAndClosingQuote(t *testing.T) {
	units := rewriteSourceEvidenceUnits("二姨问：“真的？！”赵航点头。")
	if len(units) != 2 || units[0] != "二姨问：“真的？！”" || units[1] != "赵航点头。" {
		t.Fatalf("stacked terminal punctuation detached from its quote: %#v", units)
	}
}

func TestRewriteSourceOnlyEvidenceDoesNotTruncateLongExactSentence(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	long := "二姨说：“" + strings.Repeat("这句话必须完整保留", 60) + "。”"
	if len([]rune(long)) <= 360 {
		t.Fatal("test sentence must exceed the legacy truncation threshold")
	}
	prepareRewriteSourceTest(t, st, "第一章\n\n"+long,
		"# 返工\n\n## 保留事实\n\n- 保留二姨原句。\n")
	evidence := rewriteSourceEvidenceForCharacter(st, 1, "二姨")
	if len(evidence) != 1 || evidence[0] != long {
		t.Fatalf("long exact evidence was truncated: got_runes=%d want_runes=%d tail=%q", len([]rune(firstString(evidence))), len([]rune(long)), lastRunes(firstString(evidence), 8))
	}
}

func TestRewriteSourceAuthorityPrefersExplicitPreserveActionOverEarlyBodyMention(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	const preserve = "夜市线缆风险必须由林澈独立发现；林澈先叫停继续装灯，老丁在沈知遥到场前已经断电退线；沈知遥到场时只能检查已经完成的自纠。"
	prepareRewriteSourceTest(t, st,
		"第一章\n\n林澈低头挑鱼刺。沈知遥进门时，林澈认出了她。老丁提着工具箱赶来。",
		"# 返工\n\n## 保留事实\n\n- "+preserve+"\n")
	var decisions []domain.CharacterWorldDecision
	for _, character := range []string{"林澈", "老丁", "沈知遥"} {
		evidence := rewriteSourceEvidenceForCharacter(st, 1, character)
		if len(evidence) == 0 || evidence[0] != preserve {
			t.Fatalf("%s did not receive the newer preserve-fact action authority: %#v", character, evidence)
		}
		contract := rewriteSourceOnlyContractPayload(character, 1, evidence)
		if got, _ := contract["action"].(string); got != preserve {
			t.Fatalf("%s contract action ignored preserve authority: %q", character, got)
		}
		decisions = append(decisions, rewriteSourceOnlySentinelDecision(character, 1, evidence))
	}
	source, _, _, err := loadChapterRewriteSource(st, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateIncomingSimulationSemanticInvariants(st, 1, decisions, domain.ProtagonistDecisionProjection{}, source); err != nil {
		t.Fatalf("exact preserve-fact authority was mistaken for a semantic violation: %v", err)
	}
}

func TestRewriteSourceAuthorityDoesNotPromoteMetaMentionToAction(t *testing.T) {
	if score := preserveFactActionScore("保留二姨原句。", "二姨"); score != 0 {
		t.Fatalf("meta mention was mistaken for an actor action: %d", score)
	}
}

func TestRuleDeclarationCannotHideAppendedActorCorrection(t *testing.T) {
	const fact = "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	text := fact + "；沈知遥命令老丁断电退线。"
	if exactPreserveFactText(text, []string{fact}) {
		t.Fatal("appended action was collapsed into the exact rule declaration")
	}
	if !affirmedNamedActorControlOutcome(text, "沈知遥") {
		t.Fatal("rule declaration prefix hid an appended forbidden correction")
	}
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func TestRewriteSourceOnlyAuthorityPublishesStructuredNoInferenceContract(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	entry := rewriteSourceOnlyAuthorityEntryForTest(t, st, "二姨")
	contract := rewriteSourceOnlyContractMapForTest(t, entry)

	if len(entry.RewriteSourceEvidence) != 1 || entry.RewriteSourceEvidence[0] != rewriteAuthorityEvidenceForTest {
		t.Fatalf("authority packet must publish exact rewrite evidence, got %#v", entry.RewriteSourceEvidence)
	}
	if got, _ := contract["action"].(string); got != rewriteAuthorityEvidenceForTest {
		t.Fatalf("contract action must be copied from rewrite evidence, got %#v", contract["action"])
	}
	if visible, ok := contract["visible_to_pov"].(bool); !ok || !visible {
		t.Fatalf("rewrite-source-only action was visible in the committed body and must remain visible: %#v", contract["visible_to_pov"])
	}
	for _, field := range []string{
		"time", "location", "current_goal", "pressure", "resources", "knowledge_boundary",
		"available_options", "decision", "decision_reason", "action_duration", "completion_state",
		"immediate_result", "state_after", "butterfly_effects",
	} {
		if _, ok := contract[field]; !ok {
			t.Fatalf("rewrite_source_only_contract omitted no-inference field %q: %#v", field, contract)
		}
	}
	if resources, ok := contract["resources"].([]any); !ok || len(resources) != 0 {
		t.Fatalf("unknown resources must remain an explicit empty list: %#v", contract["resources"])
	}
	effects, ok := contract["butterfly_effects"].([]any)
	if !ok || len(effects) != 1 {
		t.Fatalf("contract must publish one fixed no-inference effect: %#v", contract["butterfly_effects"])
	}
	effect, ok := effects[0].(map[string]any)
	if !ok {
		t.Fatalf("contract effect must be structured: %#v", effects[0])
	}
	for _, field := range []string{"effect", "targets", "transmission_path", "arrival_chapter", "visibility", "protagonist_impact"} {
		if _, ok := effect[field]; !ok {
			t.Fatalf("rewrite_source_only_contract effect omitted %q: %#v", field, effect)
		}
	}
	if targets, ok := effect["targets"].([]any); !ok || len(targets) != 0 {
		t.Fatalf("unknown future targets must remain empty: %#v", effect["targets"])
	}
}

func TestRewriteSourceOnlyAuthorityGuardAcceptsExactPublishedContract(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	entry := rewriteSourceOnlyAuthorityEntryForTest(t, st, "二姨")
	decision := rewriteSourceOnlyDecisionFromContractForTest(t, entry)
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision}); err != nil {
		t.Fatalf("exact published rewrite_source_only_contract was rejected: %v", err)
	}
}

func TestAuthorityContractCharactersMaterializeExactContractServerSide(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	decisions, err := materializeSimulationAuthorityContracts(st, 1, []string{"二姨"})
	if err != nil || len(decisions) != 1 {
		t.Fatalf("server-side authority materialization failed: decisions=%+v err=%v", decisions, err)
	}
	entry := rewriteSourceOnlyAuthorityEntryForTest(t, st, "二姨")
	expected := rewriteSourceOnlyDecisionFromContractForTest(t, entry)
	if decisions[0].Action != expected.Action || decisions[0].KnowledgeBoundary != expected.KnowledgeBoundary || decisions[0].Decision != expected.Decision {
		t.Fatalf("materialized contract differs from published authority: got=%+v want=%+v", decisions[0], expected)
	}
	if err := validateIncomingSimulationCharacterAuthority(st, 1, decisions); err != nil {
		t.Fatalf("server-generated exact contract failed its own guard: %v", err)
	}
	if _, err := materializeSimulationAuthorityContracts(st, 1, []string{"不存在角色"}); err == nil || !strings.Contains(err.Error(), "不在 simulation_character_authority") {
		t.Fatalf("unknown authority reference was not rejected: %v", err)
	}
	if _, err := materializeSimulationAuthorityContracts(st, 1, []string{"二姨", "二姨"}); err == nil || !strings.Contains(err.Error(), "重复角色") {
		t.Fatalf("duplicate authority reference was not rejected: %v", err)
	}
}

func TestSimulateChapterWorldStagesAuthorityContractReferenceWithoutLongJSON(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	tool := NewSimulateChapterWorldTool(st)
	raw, err := json.Marshal(map[string]any{
		"chapter":                       1,
		"time_window":                   "当日晚饭",
		"authority_contract_characters": []string{"二姨"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), raw); err != nil {
		t.Fatalf("short authority reference failed to stage: %v", err)
	}
	partial, err := st.LoadChapterWorldSimulationPartial(1)
	if err != nil || partial == nil || len(partial.CharacterDecisions) != 1 || partial.CharacterDecisions[0].Character != "二姨" || partial.CharacterDecisions[0].Action != rewriteAuthorityEvidenceForTest {
		t.Fatalf("server-side contract was not staged exactly: partial=%+v err=%v", partial, err)
	}
}

func TestFinalizedSimulationRevalidatesRewriteSourceOnlyContractAfterGuardUpgrade(t *testing.T) {
	st := newRewriteSourceOnlyAuthorityTestStore(t)
	entry := rewriteSourceOnlyAuthorityEntryForTest(t, st, "二姨")
	decision := rewriteSourceOnlyDecisionFromContractForTest(t, entry)
	decision.Action = strings.TrimSuffix(decision.Action, "。")
	sim := domain.ChapterWorldSimulation{
		Chapter: 1, SimulationID: "legacy-finalized", CharacterDecisions: []domain.CharacterWorldDecision{decision},
	}
	gap := storedSimulationCharacterAuthorityGap(st, sim)
	if !strings.Contains(gap, "stored simulation authority contract invalid") || !strings.Contains(gap, "action") {
		t.Fatalf("finalized stale contract was not revalidated: %q", gap)
	}

	decision.Action = rewriteAuthorityEvidenceForTest
	sim.CharacterDecisions = []domain.CharacterWorldDecision{decision}
	if gap := storedSimulationCharacterAuthorityGap(st, sim); gap != "" {
		t.Fatalf("current finalized contract should remain reusable: %q", gap)
	}
}

func TestRewriteSourceOnlyAuthorityGuardRejectsNarrativeInferenceByField(t *testing.T) {
	tests := []struct {
		name      string
		fieldPath string
		mutate    func(*domain.CharacterWorldDecision)
	}{
		{
			name:      "invented relative marriage",
			fieldPath: "state_after",
			mutate: func(d *domain.CharacterWorldDecision) {
				d.StateAfter = "二姨回家后与丈夫商量赵航的婚事"
			},
		},
		{
			name:      "invented motive",
			fieldPath: "decision_reason",
			mutate: func(d *domain.CharacterWorldDecision) {
				d.DecisionReason = "她担心亲属关系恶化，所以主动替林澈解围"
			},
		},
		{
			name:      "invented phone behavior",
			fieldPath: "action",
			mutate: func(d *domain.CharacterWorldDecision) {
				d.Action = "二姨拿起手机，在亲友群里替林澈解释"
			},
		},
		{
			name:      "invented future impact",
			fieldPath: "butterfly_effects[0].protagonist_impact",
			mutate: func(d *domain.CharacterWorldDecision) {
				d.ButterflyEffects[0].ProtagonistImpact = "二姨明天会说服亲友给林澈提供资金"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newRewriteSourceOnlyAuthorityTestStore(t)
			entry := rewriteSourceOnlyAuthorityEntryForTest(t, st, "二姨")
			decision := rewriteSourceOnlyDecisionFromContractForTest(t, entry)
			tc.mutate(&decision)
			err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision})
			if err == nil {
				t.Fatalf("invented rewrite-source-only field %s unexpectedly passed", tc.fieldPath)
			}
			if !strings.Contains(err.Error(), tc.fieldPath) {
				t.Fatalf("error must identify field path %q, got: %v", tc.fieldPath, err)
			}
		})
	}
}

func newRewriteSourceOnlyAuthorityTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("rewrite authority", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "失业饭桌", CoreEvent: "林澈在饭桌承认失业"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile: domain.CharacterDossierProfile{Desires: []string{"守住体面"}},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location: "林家饭桌", Status: "清醒", CurrentAction: "应付亲戚追问", Pressure: "失业被追问",
			NextIndependentMove: "结束饭局后寻找县内经营试验",
		},
		Resources:         []domain.CharacterResource{{Name: "手机", Status: "available"}},
		KnowledgeBoundary: "只知道亲历和同场信息",
		DecisionModel:     "先保留可撤回选项",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Cast.Save([]domain.CastEntry{{Name: "二姨", BriefRole: "饭桌亲属", LastSeenChapter: 1}}); err != nil {
		t.Fatal(err)
	}
	body := "第一章 失业饭桌\n\n林澈低头看着饭碗。\n\n" + rewriteAuthorityEvidenceForTest
	prepareRewriteSourceTest(t, st, body,
		"# 返工\n\n## 保留事实\n\n- "+rewriteAuthorityEvidenceForTest+"\n\n## 必须修正\n\n- 不得替二姨补写原文之外的动机、婚姻或手机行为。\n")
	return st
}

func rewriteSourceOnlyAuthorityEntryForTest(t *testing.T, st *store.Store, name string) simulationCharacterAuthority {
	t.Helper()
	for _, entry := range buildSimulationCharacterAuthority(st, 1) {
		if entry.Character != name {
			continue
		}
		if entry.AuthorityMode != "rewrite_source_only" || !entry.VisibleInCurrentChapter || !entry.Blocking {
			t.Fatalf("expected visible blocking rewrite_source_only authority for %s, got %+v", name, entry)
		}
		return entry
	}
	t.Fatalf("missing authority entry for %s", name)
	return simulationCharacterAuthority{}
}

func rewriteSourceOnlyContractMapForTest(t *testing.T, entry simulationCharacterAuthority) map[string]any {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var packet map[string]any
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	contract, ok := packet["rewrite_source_only_contract"].(map[string]any)
	if !ok || len(contract) == 0 {
		t.Fatalf("authority packet must publish structured rewrite_source_only_contract: %s", raw)
	}
	return contract
}

func rewriteSourceOnlyDecisionFromContractForTest(t *testing.T, entry simulationCharacterAuthority) domain.CharacterWorldDecision {
	t.Helper()
	contract := rewriteSourceOnlyContractMapForTest(t, entry)
	raw, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	var decision domain.CharacterWorldDecision
	if err := json.Unmarshal(raw, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Character != entry.Character {
		t.Fatalf("contract character mismatch: got=%q want=%q", decision.Character, entry.Character)
	}
	return decision
}
