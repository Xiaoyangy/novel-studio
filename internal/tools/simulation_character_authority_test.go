package tools

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSimulationCharacterAuthorityCoversRequiredRosterWithoutCap(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	var characters []domain.Character
	for i := 1; i <= 20; i++ {
		name := fmt.Sprintf("角色%02d", i)
		characters = append(characters, domain.Character{
			Name: name, Role: "配角", Tier: "important", Description: name + "的权威角色卡",
		})
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: "配角", Tier: "important",
			Profile: domain.CharacterDossierProfile{Description: name + "的档案描述"},
			CurrentAtStoryStart: domain.CharacterStartState{
				Location: "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
				Status:   "存活/状态待正文确认", CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Characters.Save(characters); err != nil {
		t.Fatal(err)
	}
	if err := st.Cast.Save([]domain.CastEntry{{Name: "临时邻居", BriefRole: "饭桌邻居", LastSeenChapter: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{{Character: "角色01"}},
	}); err != nil {
		t.Fatal(err)
	}

	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 21 {
		t.Fatalf("required roster was capped or lost: got=%d authority=%+v", len(authority), authority)
	}
	if authority[0].Character != "角色01" || authority[0].SimulationStatus != "already_present" || authority[0].AuthorityMode != "reuse_saved_decision" {
		t.Fatalf("saved partial decision must be protected from resubmission: %+v", authority[0])
	}
	second := authority[1]
	if second.Description != "角色02的档案描述" || second.CurrentLocation != "unknown" || second.CurrentAction != "unknown" || !second.Blocking || second.AuthorityMode != "hold_baseline" {
		t.Fatalf("placeholder dossier state must remain an explicit blocked unknown: %+v", second)
	}
	last := authority[len(authority)-1]
	if last.Character != "临时邻居" || last.Role != "饭桌邻居" || !last.Blocking || last.AuthorityMode != "hold_baseline" || !slices.Contains(last.MissingAuthority, "dossier") {
		t.Fatalf("cast-only actor must block invention instead of acquiring a guessed dossier: %+v", last)
	}
}

func TestSimulationCharacterAuthorityKeepsOnlyConcreteCurrentFacts(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile:               domain.CharacterDossierProfile{Description: "返乡青年", Traits: []string{"谨慎", "行动派"}, Desires: []string{"先保住饭桌上的体面"}},
		Resources:             []domain.CharacterResource{{Name: "手机", Status: "available"}},
		KnowledgeBoundary:     "不知道场外角色未传回的信息",
		DecisionModel:         "先确认现场证据，再选择可逆行动",
		CommunicationBoundary: domain.CommunicationBoundary{CanContactProtagonist: true, Channels: []string{"自我行动"}},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location: "林家饭桌", Status: "清醒", CurrentAction: "应付亲戚追问",
			Pressure: "亲戚追问工作去向", NextIndependentMove: "把话题转回饭桌账单",
		},
	}); err != nil {
		t.Fatal(err)
	}

	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 1 {
		t.Fatalf("unexpected authority: %+v", authority)
	}
	got := authority[0]
	if got.Blocking || got.AuthorityMode != "authoritative" || got.CurrentLocation != "林家饭桌" || got.CurrentAction != "应付亲戚追问" || !slices.Contains(got.Resources, "手机（available）") {
		t.Fatalf("concrete dossier facts were not preserved: %+v", got)
	}
	if !strings.Contains(got.DecisionPolicy, "不得把 arc") {
		t.Fatalf("future arc boundary missing: %+v", got)
	}
}

func TestSimulationAuthorityPinsPreserveFactKnowledgeBoundaryIndependentlyOfModel(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("knowledge lock", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{{Name: "林澈", Role: "主角", Tier: "core"}, {Name: "贺骁", Role: "配角", Tier: "core"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "章末电话", CoreEvent: "林澈拨通贺骁"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"林澈", "贺骁"} {
		if err := st.SaveCharacterDossier(domain.CharacterDossier{
			Version: 1, Character: name, Role: "配角", Tier: "core",
			Profile: domain.CharacterDossierProfile{Desires: []string{"守住当前边界"}},
			CurrentAtStoryStart: domain.CharacterStartState{
				Location: "青山县", Status: "清醒", CurrentAction: "处理手头事务", Pressure: "时间有限", NextIndependentMove: "只按已知信息行动",
			},
			Resources:         []domain.CharacterResource{{Name: "手机", Status: "available"}},
			KnowledgeBoundary: "只知道亲历与明确通信", DecisionModel: "先核验证据",
		}); err != nil {
			t.Fatal(err)
		}
	}
	const locked = "贺骁不知道林澈已回青山县、位于夜市附近或正在做夜市项目，也不得凭单向背景音推断这些位置与行动信息"
	prepareRewriteSourceTest(t, st, "第一章\n\n电话只传来贺骁一侧的扳手声。",
		"# 返工\n\n## 保留事实\n\n- "+locked+"。\n")
	var he, lin simulationCharacterAuthority
	for _, entry := range buildSimulationCharacterAuthority(st, 1) {
		if entry.Character == "贺骁" {
			he = entry
		}
		if entry.Character == "林澈" {
			lin = entry
		}
	}
	if len(he.RequiredKnowledgeBoundary) != 1 || he.RequiredKnowledgeBoundary[0] != locked {
		t.Fatalf("preserve knowledge lock missing from authority packet: %+v", he)
	}
	if len(lin.RequiredKnowledgeBoundary) != 0 {
		t.Fatalf("knowledge object was mistaken for the epistemic subject: %+v", lin)
	}
	decision := simulatedDecision("贺骁", "接起电话", true)
	decision.KnowledgeBoundary = "只知道亲历与明确通信"
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision}); err == nil || !strings.Contains(err.Error(), "required_preserve_clause[0]") {
		t.Fatalf("model-deleted preserve knowledge boundary passed: %v", err)
	}
	decision.KnowledgeBoundary += "；" + locked
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{decision}); err != nil {
		t.Fatalf("exact preserve knowledge boundary was rejected: %v", err)
	}
}

func TestBlockingAuthorityContractAndValidatorShareMergedKnowledgeLock(t *testing.T) {
	const locked = "贺骁不知道林澈已回青山县，也不得凭背景音推断位置"
	evidence := []string{"电话那头响了一声。"}
	decision := rewriteSourceOnlySentinelDecision("贺骁", 1, evidence)
	decision.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(decision.KnowledgeBoundary, []string{locked})
	if err := validateRequiredKnowledgeBoundaries(decision, []string{locked}); err != nil {
		t.Fatalf("merged contract lost the independent knowledge lock: %v", err)
	}
	if err := validateRewriteSourceOnlyDecision(1, decision, evidence, []string{locked}); err != nil {
		t.Fatalf("exact merged rewrite contract was rejected: %v", err)
	}
	payload := rewriteSourceOnlyContractPayload("贺骁", 1, evidence, []string{locked})
	if got, _ := payload["knowledge_boundary"].(string); got != decision.KnowledgeBoundary {
		t.Fatalf("published contract and validator disagree: payload=%q decision=%q", got, decision.KnowledgeBoundary)
	}

	hold := holdBaselineSentinelDecision("贺骁", 1)
	hold.KnowledgeBoundary = mergedAuthorityKnowledgeBoundary(hold.KnowledgeBoundary, []string{locked})
	if err := validateHoldBaselineDecision(1, hold, []string{locked}); err != nil {
		t.Fatalf("exact merged hold contract was rejected: %v", err)
	}
}

func TestKnowledgeBoundarySubjectIndexDoesNotBindNamedObject(t *testing.T) {
	clause := "贺骁不知道林澈已回青山县、位于夜市附近，也不得凭单向背景音推断这些信息"
	if got := knowledgeBoundarySubjectIndex(clause, "贺骁"); got != 0 {
		t.Fatalf("explicit epistemic subject was not recognized: %d", got)
	}
	if got := knowledgeBoundarySubjectIndex(clause, "林澈"); got != -1 {
		t.Fatalf("named knowledge object was bound as subject: %d", got)
	}
	if got := knowledgeBoundarySubjectIndex("沈知遥此时仍不知道系统存在", "沈知遥"); got != 0 {
		t.Fatalf("subject with epistemic modifiers was not recognized: %d", got)
	}
}

func TestSimulationCharacterAuthorityPolicyMatchesRoster(t *testing.T) {
	authority := []simulationCharacterAuthority{
		{Character: "甲", Blocking: false},
		{Character: "乙", Blocking: true},
	}
	policy := simulationCharacterAuthorityPolicy(authority)
	if policy["required_count"] != 2 || policy["blocking_count"] != 1 || !strings.Contains(policy["policy"].(string), "unknown") {
		t.Fatalf("unexpected policy: %+v", policy)
	}
}

func TestStagedPlanRepairKeepsSimulationCharacterAuthority(t *testing.T) {
	st := newChapterSimulationTestStore(t)
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		Profile: domain.CharacterDossierProfile{Description: "返乡青年"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlanPartial(1, map[string]any{
		"structure":         map[string]any{"chapter": 1, "title": "待修骨架"},
		"causal_simulation": map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChapterWorldSimulationPartial(domain.ChapterWorldSimulation{
		Version: 1, Chapter: 1,
		CharacterDecisions: []domain.CharacterWorldDecision{{Character: "林澈"}},
	}); err != nil {
		t.Fatal(err)
	}

	result, ok, err := NewContextTool(st, References{}, "default").stagedPlanRepairContext(1, 1, true)
	if err != nil || !ok {
		t.Fatalf("stagedPlanRepairContext: ok=%v err=%v", ok, err)
	}
	authority, ok := result["simulation_character_authority"].([]simulationCharacterAuthority)
	if !ok || len(authority) != 2 {
		t.Fatalf("staged repair lost the authoritative roster: %#v", result["simulation_character_authority"])
	}
	if authority[0].Character != "林澈" || authority[0].SimulationStatus != "already_present" || authority[1].Character != "沈知遥" {
		t.Fatalf("staged authority did not preserve present/missing identities: %+v", authority)
	}
	policy, ok := result["simulation_character_authority_policy"].(map[string]any)
	if !ok || policy["required_count"] != 2 {
		t.Fatalf("staged authority policy missing: %#v", result["simulation_character_authority_policy"])
	}
}

func TestHoldBaselineAuthorityGuardRejectsNarrativeGuessing(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "许牧", Role: "主角团配角", Tier: "core", Description: "主角旧友"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "许牧", Role: "主角团配角", Tier: "core",
		Profile: domain.CharacterDossierProfile{Description: "主角旧友"},
		CurrentAtStoryStart: domain.CharacterStartState{
			Location:      "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
			CurrentAction: "按自身开局目标行动或被自身场景压力困住。",
		},
	}); err != nil {
		t.Fatal(err)
	}
	guessed := holdBaselineDecisionForTest("许牧", 1)
	guessed.DecisionReason = "人在外地，尚未收到主角消息"
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{guessed}); err == nil || !strings.Contains(err.Error(), "decision_reason") || !strings.Contains(err.Error(), "hold_baseline_contract") {
		t.Fatalf("narrative guess passed hold-baseline guard: %v", err)
	}
	exact := holdBaselineDecisionForTest("许牧", 1)
	if err := validateIncomingSimulationCharacterAuthority(st, 1, []domain.CharacterWorldDecision{exact}); err != nil {
		t.Fatalf("exact sentinel contract rejected: %v", err)
	}
}

func TestHoldBaselineAuthorityPacketPublishesEveryValidatedSentinelField(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.Save([]domain.Character{
		{Name: "林澈", Role: "主角", Tier: "core"},
		{Name: "许牧", Role: "配角", Tier: "important"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "林澈", Role: "主角", Tier: "core",
		CurrentAtStoryStart: domain.CharacterStartState{Location: "林家饭桌", CurrentAction: "应付亲戚追问"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCharacterDossier(domain.CharacterDossier{
		Version: 1, Character: "许牧", Role: "配角", Tier: "important",
		CurrentAtStoryStart: domain.CharacterStartState{
			Location: "故事开始前未进入主角视角；正式出场前按角色卡和交通规则补足位置",
		},
	}); err != nil {
		t.Fatal(err)
	}
	authority := buildSimulationCharacterAuthority(st, 1)
	if len(authority) != 2 || authority[1].Character != "许牧" || authority[1].AuthorityMode != simulationAuthorityHoldBaseline {
		t.Fatalf("unexpected authority packet: %+v", authority)
	}
	want := holdBaselineContractPayload("许牧", 1)
	if !reflect.DeepEqual(authority[1].HoldBaselineContract, want) {
		t.Fatalf("authority packet contract drifted from validator canonical value:\n got=%#v\nwant=%#v", authority[1].HoldBaselineContract, want)
	}
	if !strings.Contains(authority[1].DecisionPolicy, "authority_contract_characters") || !strings.Contains(authority[1].DecisionPolicy, "服务端") || !strings.Contains(authority[1].DecisionPolicy, "hold_baseline_contract") {
		t.Fatalf("authority policy does not route the model through server-side contract materialization: %s", authority[1].DecisionPolicy)
	}
}

func holdBaselineDecisionForTest(name string, chapter int) domain.CharacterWorldDecision {
	return domain.CharacterWorldDecision{
		Character: name, Time: simulationAuthorityUnknown, Location: simulationAuthorityUnknown,
		CurrentGoal: simulationAuthorityHoldBaseline, Pressure: simulationAuthorityMissing,
		KnowledgeBoundary: simulationAuthorityMissing,
		AvailableOptions:  []string{simulationAuthorityHoldBaseline, simulationAuthorityWait},
		Decision:          simulationAuthorityHoldBaseline,
		DecisionReason:    simulationAuthorityMissing,
		Action:            simulationAuthorityHoldBaseline,
		ActionDuration:    simulationAuthorityNotApplicable,
		CompletionState:   "blocked",
		ImmediateResult:   simulationAuthorityNoEffect,
		StateAfter:        simulationAuthorityUnchanged,
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect: simulationAuthorityBlockedEffect, TransmissionPath: simulationAuthorityMissing,
			ArrivalChapter: chapter, Visibility: "hidden", ProtagonistImpact: simulationAuthorityNoImpact,
		}},
	}
}
