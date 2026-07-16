package tools

import (
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestPreserveFactAnyFieldGuardRejectsForbiddenAvailableOption(t *testing.T) {
	fact := "夜市风险由林澈先发现，老丁在沈知遥到场前完成断电退线；任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	decision := domain.CharacterWorldDecision{
		Character: "沈知遥",
		AvailableOptions: []string{
			"到场后发现线缆问题立即要求断电退线",
			"检查已经完成的自纠并补充边界",
		},
	}
	gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "available_options[0]") {
		t.Fatalf("forbidden counterfactual must identify its exact field: %#v", gaps)
	}
}

func TestPreserveFactAnyFieldGuardAllowsNegatedWarningsAndCompletedCorrection(t *testing.T) {
	fact := "夜市风险由林澈先发现；任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	decision := domain.CharacterWorldDecision{
		Character: "沈知遥",
		AvailableOptions: []string{
			"到场后发现自纠已完成，检查合规性",
			"先与摊主沟通再补充边界",
		},
		Decision:       "确认已经完成的断电退线；不能写‘要求断电’或‘要求退线’",
		DecisionReason: "她不需要再要求什么；老丁已经完成断电退线",
		Action:         "检查已断电的灯和已退回摊内的线",
	}
	gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact})
	if len(gaps) != 0 {
		t.Fatalf("negated warning or post-correction inspection must remain valid: %#v", gaps)
	}
}

func TestPreserveFactAnyFieldGuardRejectsImplicitModalCorrectionOrder(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	decision := domain.CharacterWorldDecision{
		Character:        "沈知遥",
		AvailableOptions: []string{"检查已完成的自纠", "先问摊主"},
		Action:           "她到场检查后说，灯能留，但线必须退回摊内、今后不得横穿通道。",
	}
	gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact})
	if len(gaps) != 1 || !strings.Contains(gaps[0], ".action") {
		t.Fatalf("implicit modal correction order must be rejected: %#v", gaps)
	}
}

func TestPreserveFactAnyFieldGuardDoesNotLetEarlierCompletedActionHideLaterOrder(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	decision := domain.CharacterWorldDecision{
		Character: "沈知遥",
		Action:    "她检查已断电的灯，但线必须退回摊内。",
	}
	if gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact}); len(gaps) != 1 {
		t.Fatalf("earlier completed action hid a later modal order: %#v", gaps)
	}
}

func TestPreserveFactAnyFieldGuardRejectsDelayedCorrectionWithoutOrderVerb(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	for _, action := range []string{
		"沈知遥到场后，老丁才断电退线。",
		"她指出问题后，老丁这才收线。",
		"沈知遥到场后检查线路，老丁随后断电退线。",
	} {
		decision := domain.CharacterWorldDecision{Character: "沈知遥", Action: action}
		gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact})
		if len(gaps) != 1 || !strings.Contains(gaps[0], ".action") {
			t.Fatalf("delayed correction order was not rejected for %q: %#v", action, gaps)
		}
	}

	for _, action := range []string{
		"沈知遥到场后检查已经完成的断电退线。",
		"沈知遥到场后检查已断电的灯和已退回的线。",
		"她随后检查断电线路，确认线已收回摊内。",
	} {
		allowed := domain.CharacterWorldDecision{Character: "沈知遥", Action: action}
		if gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": allowed}, []string{fact}); len(gaps) != 0 {
			t.Fatalf("inspection of a pre-completed correction must pass for %q: %#v", action, gaps)
		}
	}
}

func TestPreserveFactAnyFieldGuardRejectsControlVerbSynonyms(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	for _, action := range []string{"她让老丁断电退线", "她叫老丁收线", "她示意老丁马上断电", "她催促老丁返工"} {
		decision := domain.CharacterWorldDecision{Character: "沈知遥", Action: action}
		if gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact}); len(gaps) != 1 {
			t.Fatalf("control synonym bypassed invariant for %q: %#v", action, gaps)
		}
	}
}

func TestPreserveFactAnyFieldGuardRespectsControlDirection(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	for _, action := range []string{
		"她检查已断电的灯，要求保留价牌。",
		"已断电的灯让她放心。",
		"她要求复核已经完成的断电退线。",
	} {
		decision := domain.CharacterWorldDecision{Character: "沈知遥", Action: action}
		if gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact}); len(gaps) != 0 {
			t.Fatalf("unrelated control direction was rejected for %q: %#v", action, gaps)
		}
	}
}

func TestPreserveFactAnyFieldGuardScansLaterControlOccurrence(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	decision := domain.CharacterWorldDecision{
		Character: "沈知遥",
		Action:    "她不需要要求断电，但后来要求老丁退线。",
	}
	if gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{fact}); len(gaps) != 1 {
		t.Fatalf("later affirmative control occurrence bypassed invariant: %#v", gaps)
	}
}

func TestPreserveFactAnyFieldGuardDoesNotInventRuleWithoutExplicitAnyFieldClause(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:        "沈知遥",
		AvailableOptions: []string{"要求断电退线", "继续观察"},
	}
	gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"沈知遥": decision}, []string{
		"沈知遥最终选择检查已经完成的自纠。",
	})
	if len(gaps) != 0 {
		t.Fatalf("guard must be activated only by an explicit any-field rejection: %#v", gaps)
	}
}

func TestProjectionPreserveGuardRejectsForbiddenCorrectionAndPendingResourceClaims(t *testing.T) {
	facts := []string{
		"任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。",
		"所有角色与 protagonist_projection 均不得写皮卡已借出、运力即将到位、次日按约到场或任何已确认执行时间。",
		"protagonist_projection 中不得埋“异常渠道”“开始留意资金”“后续推理系统”的线索。",
	}
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"沈知遥到场后要求老丁返工", "皮卡已借出"},
		HiddenPressures:   []string{"沈知遥开始留意资金"},
		AvailableOptions:  []string{"依靠运力即将到位扩大试点", "暂停"},
		ChosenDecision:    "暂停",
		DecisionReason:    "等待确认",
		PlanConstraints:   []string{"次日按约到场"},
		CausalChain:       []string{"票据 -> 后续推理系统"},
	}
	joined := strings.Join(chapterWorldSimulationProjectionInvariantGaps(projection, facts), "\n")
	for _, path := range []string{"observable_effects[0]", "observable_effects[1]", "hidden_pressures[0]", "available_options[0]", "plan_constraints[0]", "causal_chain[0]"} {
		if !strings.Contains(joined, path) {
			t.Fatalf("projection guard missed %s: %s", path, joined)
		}
	}
}

func TestProjectionPreserveGuardAllowsNegatedHardConstraints(t *testing.T) {
	facts := []string{
		"任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。",
		"均不得写皮卡已借出、运力即将到位、次日按约到场或任何已确认执行时间。",
		"不得埋“异常渠道”“开始留意资金”“后续推理系统”的线索。",
	}
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"老丁已完成断电退线，沈知遥随后只检查"},
		HiddenPressures:   []string{"沈知遥不知道系统，不得开始留意资金"},
		AvailableOptions:  []string{"继续争取皮卡，运力尚未到位", "暂停"},
		ChosenDecision:    "继续争取",
		DecisionReason:    "皮卡未借出",
		PlanConstraints:   []string{"不得写次日按约到场或已确认执行时间"},
		CausalChain:       []string{"电话截断，禁止后续推理系统"},
	}
	if gaps := chapterWorldSimulationProjectionInvariantGaps(projection, facts); len(gaps) != 0 {
		t.Fatalf("negated projection constraints must pass: %#v", gaps)
	}
}

func TestProjectionCorrectionGuardOnlyAppliesToNamedForbiddenActor(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"林澈让老丁断电退线；沈知遥只检查已完成的自纠"},
		AvailableOptions:  []string{"继续", "暂停"}, ChosenDecision: "暂停", DecisionReason: "安全优先",
		PlanConstraints: []string{"任何字段写成‘沈知遥要求老丁断电退线’均不通过"},
	}
	if gaps := chapterWorldSimulationProjectionInvariantGaps(projection, []string{fact}); len(gaps) != 0 {
		t.Fatalf("required protagonist correction was mistaken for forbidden actor action: %#v", gaps)
	}
}

func TestProjectionCorrectionGuardAllowsForbiddenActorBeforeCorrectActorClause(t *testing.T) {
	fact := "任何字段写成“沈知遥要求/命令/纠偏后才断电、退线或返工”均不通过。"
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"沈知遥到场前，林澈已要求老丁断电退线"},
		AvailableOptions:  []string{"继续", "暂停"}, ChosenDecision: "暂停", DecisionReason: "安全优先",
	}
	if gaps := chapterWorldSimulationProjectionInvariantGaps(projection, []string{fact}); len(gaps) != 0 {
		t.Fatalf("named actor in an earlier comma clause was misattributed: %#v", gaps)
	}
}

func TestProjectionForbiddenPhraseGuardScansLaterAffirmativeOccurrence(t *testing.T) {
	fact := "均不得写皮卡已借出。"
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"不得写皮卡已借出，但实际皮卡已借出"},
		AvailableOptions:  []string{"继续", "暂停"}, ChosenDecision: "暂停", DecisionReason: "安全优先",
	}
	gaps := chapterWorldSimulationProjectionInvariantGaps(projection, []string{fact})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "observable_effects[0]") {
		t.Fatalf("later affirmative forbidden claim bypassed projection guard: %#v", gaps)
	}
}

func TestPhoneKnowledgeGuardTreatsCallbackSynonymsAsForbiddenFutureAction(t *testing.T) {
	fact := "贺骁不知道林澈已回青山县；任何角色决定、蝴蝶效应和 protagonist_projection 都不得据此生成主动回拨、到场或后续行动。"
	projection := domain.ProtagonistDecisionProjection{
		Protagonist:       "林澈",
		ObservableEffects: []string{"电话截在扳手声"},
		AvailableOptions:  []string{"等待贺骁回电确认皮卡是否可用", "另找运力"},
		ChosenDecision:    "另找运力", DecisionReason: "答复未知",
	}
	gaps := chapterWorldSimulationProjectionInvariantGaps(projection, []string{fact})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "available_options[0]") || !strings.Contains(gaps[0], "回拨") {
		t.Fatalf("callback synonym bypassed projection guard: %#v", gaps)
	}
	projection.AvailableOptions[0] = "贺骁尚未回电，不得把皮卡视为可用"
	if gaps := chapterWorldSimulationProjectionInvariantGaps(projection, []string{fact}); len(gaps) != 0 {
		t.Fatalf("negated callback boundary was rejected: %#v", gaps)
	}
}

func TestPhoneKnowledgeGuardRejectsCallbackHiddenInCharacterDecision(t *testing.T) {
	fact := "贺骁不知道林澈已回青山县；任何角色决定、蝴蝶效应和 protagonist_projection 都不得据此生成主动回拨、到场或后续行动。"
	decision := domain.CharacterWorldDecision{Character: "贺骁", ImmediateResult: "贺骁稍后回电确认皮卡"}
	gaps := chapterWorldSimulationPreserveInvariantGaps(nil, 1, map[string]domain.CharacterWorldDecision{"贺骁": decision}, []string{fact})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "immediate_result") {
		t.Fatalf("callback hidden in decision bypassed guard: %#v", gaps)
	}
}

func TestSimulationKnowledgeBoundaryRejectsUnevidencedPositiveClaim(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:         "贺骁",
		KnowledgeBoundary: "不知道林澈今天回青山县；不知道林澈在夜市做的事；不知道系统存在",
		ImmediateResult:   "贺骁听到林澈声音，知道林澈回青山县了",
		ButterflyEffects: []domain.DecisionButterflyEffect{{
			Effect:           "贺骁知道林澈在夜市一带活动",
			TransmissionPath: "背景可能有夜市环境音，推断林澈在夜市附近",
		}},
	}
	gaps := simulationDecisionKnowledgeGaps(decision)
	joined := strings.Join(gaps, "\n")
	if !strings.Contains(joined, "immediate_result") || !strings.Contains(joined, "butterfly_effects[0].effect") || !strings.Contains(joined, "transmission_path") {
		t.Fatalf("knowledge contradiction paths were not reported: %#v", gaps)
	}
}

func TestSimulationKnowledgeBoundaryAllowsExplicitLaterDiscoveryAndNegatedReminder(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:         "沈知遥",
		KnowledgeBoundary: "不知道老丁已完成断电退线（到场时才发现）；不知道系统存在",
		Action:            "到场后看见老丁已经完成断电退线；不得推断系统存在",
		StateAfter:        "仍然不知道系统存在",
	}
	if gaps := simulationDecisionKnowledgeGaps(decision); len(gaps) != 0 {
		t.Fatalf("explicit discovery transition and negated reminder should pass: %#v", gaps)
	}
}

func TestSimulationKnowledgeBoundaryStillRejectsKnowledgeBeforePinnedDiscovery(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:         "沈知遥",
		KnowledgeBoundary: "不知道老丁已完成断电退线（到场时才发现）",
		DecisionReason:    "她出发前已知道老丁完成断电退线，所以只去走过场",
	}
	gaps := simulationDecisionKnowledgeGaps(decision)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "decision_reason") {
		t.Fatalf("knowledge before the pinned discovery must be rejected: gaps=%#v unknown=%q candidate=%q match=%d", gaps,
			canonicalUnknownClaim("老丁已完成断电退线（到场时才发现）"),
			canonicalUnknownClaim(decision.DecisionReason),
			simulationUnknownClaimMatchAt(canonicalUnknownClaim(decision.DecisionReason), canonicalUnknownClaim("老丁已完成断电退线（到场时才发现）")),
		)
	}
}

func TestSimulationKnowledgeBoundaryAllowsExplicitCommunicationOfFormerUnknown(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:         "贺骁",
		KnowledgeBoundary: "接通前不知道林澈今天回青山县",
		ImmediateResult:   "林澈在电话里明确说‘我今天回青山县了’，贺骁这才知道林澈回青山县",
	}
	if gaps := simulationDecisionKnowledgeGaps(decision); len(gaps) != 0 {
		t.Fatalf("explicit communication should ground a knowledge transition: %#v", gaps)
	}
}

func TestSimulationKnowledgeBoundaryParsesMultipleUnknownClaimsInOneClause(t *testing.T) {
	decision := domain.CharacterWorldDecision{
		Character:         "沈知遥",
		KnowledgeBoundary: "不知道系统存在，也不知道资金来源",
		ImmediateResult:   "她知道系统存在，随后又知道资金来源",
	}
	joined := strings.Join(simulationDecisionKnowledgeGaps(decision), "\n")
	for _, claim := range []string{"系统存在", "资金来源"} {
		if !strings.Contains(joined, claim) {
			t.Fatalf("multiple unknown parser missed %q: %s", claim, joined)
		}
	}
}

func TestSimulationKnowledgeBoundaryDoesNotLetUnrelatedEvidenceLaunderInference(t *testing.T) {
	for _, result := range []string{
		"票据显示交易真实，因此她知道系统存在",
		"票据显示交易真实，她知道系统存在",
		"她亲眼看见老丁退线，她知道系统存在",
	} {
		decision := domain.CharacterWorldDecision{
			Character:         "沈知遥",
			KnowledgeBoundary: "不知道系统存在",
			ImmediateResult:   result,
		}
		gaps := simulationDecisionKnowledgeGaps(decision)
		if len(gaps) != 1 || !strings.Contains(gaps[0], "immediate_result") {
			t.Fatalf("unrelated evidence must not prove a secret for %q: %#v", result, gaps)
		}
	}
}
